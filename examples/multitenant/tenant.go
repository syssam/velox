// Package multitenant shows how to isolate tenants in a velox application
// without narrowing the reads that guard invariants.
//
// The shape is two clients over one connection pool:
//
//	Scoped   every read is constrained to the caller's tenant
//	System   no read scope at all — for invariant checks and internal jobs
//
// Reads are scoped with an interceptor rather than a Policy because an
// interceptor is registered per client, so the System client is untouched by
// construction. A Policy is process-global; scoping reads through one would
// narrow every read in the process, and its only escape is a context flag
// that fails open when forgotten. Writes go the other way: the Policy on
// TenantMixin is global on purpose, because a write that silently crosses
// tenants is never wanted, and an internal job that must do so says so by
// carrying the system role.
//
// Wire it so that request handlers receive Scoped and services receive
// System. The distinction then holds by construction rather than by anyone
// remembering an opt-out at 200 call sites.
package multitenant

import (
	"context"
	"fmt"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/privacy"

	"example.com/multitenant/schema"
	"example.com/multitenant/velox"
	"example.com/multitenant/velox/intercept"
)

// Clients holds the two views of the same database.
type Clients struct {
	// Scoped serves user requests. Every read it issues is narrowed to the
	// tenant on the context viewer, and a read without a viewer fails.
	Scoped *velox.Client

	// System serves everything else: dependency checks before a delete,
	// uniqueness probes, lock acquisition, reconciliation jobs.
	//
	// Reads through it are NOT tenant-scoped, and that is the point. A
	// narrowed `Exist()` guard answers "no dependents" for rows outside the
	// caller's tenant and lets a delete through — that destroys data rather
	// than leaking it, and nothing detects it afterwards.
	System *velox.Client

	drv dialect.Driver
}

// Open builds both clients over a single driver.
func Open(driverName, dsn string) (*Clients, error) {
	drv, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("multitenant: open: %w", err)
	}

	system := velox.NewClient(velox.Driver(drv))

	scoped := velox.NewClient(velox.Driver(drv))
	scoped.Intercept(tenantReadScope())

	return &Clients{Scoped: scoped, System: system, drv: drv}, nil
}

// Close releases the shared driver.
func (c *Clients) Close() error { return c.drv.Close() }

// tenantReadScope narrows every read to the viewer's tenant.
//
// One generic traverser covers every entity, so adding an entity does not
// mean remembering to register anything. Measured coverage: All, Count, IDs,
// Exist, Only, First, Get, Select, GroupBy, eager-loaded child SELECTs, edge
// queries and reads inside a transaction all reach it.
//
// Two things it does not cover, both by design of the mechanism rather than
// oversight:
//
//   - mutations. Those are the Policy's job (see schema.TenantMixin).
//   - subqueries from edge predicates. HasOrdersWith(...) compiles straight
//     to a *sql.Selector and never becomes a Query, so no rule observes it.
//     Treat edge predicates as unscoped in tenant-sensitive paths.
func tenantReadScope() velox.Interceptor {
	return intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
		viewer, _ := privacy.ViewerFromContext(ctx).(privacy.TenantIDer)
		if viewer == nil || viewer.TenantID() == "" {
			// Fail closed: a scoped read with no tenant must not fall
			// through to every tenant's rows.
			return fmt.Errorf("multitenant: %s read requires a viewer carrying a tenant", q.Type())
		}
		tenant := viewer.TenantID()
		q.WhereP(func(s *sql.Selector) {
			s.Where(sql.EQ(s.C("tenant_id"), tenant))
		})
		return nil
	})
}

// Viewer identifies the caller. Implements privacy.Viewer and
// privacy.TenantIDer.
type Viewer struct {
	UserID   string
	Tenant   string
	IsSystem bool
}

// ID returns the viewer's user identifier.
func (v Viewer) ID() string { return v.UserID }

// Roles returns the viewer's roles.
func (v Viewer) Roles() []string {
	if v.IsSystem {
		return []string{schema.RoleSystem}
	}
	return nil
}

// TenantID returns the viewer's tenant.
func (v Viewer) TenantID() string { return v.Tenant }

// WithTenant returns a context carrying an ordinary tenant user.
func WithTenant(ctx context.Context, tenant, userID string) context.Context {
	return privacy.WithViewer(ctx, Viewer{UserID: userID, Tenant: tenant})
}

// WithSystem returns a context for internal work: exempt from the tenant
// write filter. It still carries a tenant, because rows it creates must
// still belong somewhere.
//
// Grep for callers of this function to audit every place that can write
// across the tenant boundary.
func WithSystem(ctx context.Context, tenant string) context.Context {
	return privacy.WithViewer(ctx, Viewer{UserID: "system", Tenant: tenant, IsSystem: true})
}
