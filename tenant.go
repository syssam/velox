package velox

import (
	"context"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/veloxtype"
)

// WithTenant returns ctx scoped to tenant id: statements on a tenant-scoped
// entity read and change only that tenant's rows, and inserts fill or check
// the tenant column. id is converted to the tenant column's Go type when it
// is of a predeclared integer type (an untyped constant is an int) and the
// column's type, predeclared or named such as an OrgID int64, is an integer
// type whose range holds it; or when it is a string and the column's kind is
// string. An id of a named type is never converted: it must be the column's
// type, so a UserID never serves an OrgID column. Any other id fails the
// statement with ErrTenantMismatch. A nil id, or the type's zero value (what
// parsing a missing claim gives), scopes nothing: such statements fail with
// ErrNoTenant. It replaces an earlier WithTenant or WithAllTenants on ctx.
//
// A write under a tenant that sets a foreign key to a tenant-scoped row first
// asks whether that row is the tenant's, with one SELECT per column on the
// write's connection, and fails unsent with ErrTenantRef if not, whether the row
// is another tenant's or is not there. It covers Insert, InsertAll, Update, Save
// and upserts, with the values hooks left. That is one more round trip per column
// on every such write, in its transaction if it has one; Around hooks do not see it,
// and nothing turns it off under a tenant. It does not make the reference safe by
// itself: a target deleted after the check is the database foreign key's to
// refuse, and a composite foreign key, (org_id, project_id) to (org_id, id), is
// the stronger guarantee, as it also holds for writes that do not go through
// velox. WithAllTenants sends no check. A row may name a key its own insert sets,
// unless it is an upsert, which may not insert it: there it is refused.
//
// The join rows of a many-to-many edge have no tenant of their own, and are scoped
// through the TenantRefs velox-gen writes for each tenant-scoped side. Under a tenant,
// a read of one (Count, Exists, the aggregates and Iter too), its update and its delete
// hold, per side, fk IN (SELECT key FROM target WHERE tenant = ?), so a link that
// crosses tenants is hidden from both in With and Has; with neither a tenant nor WithAllTenants they
// fail with ErrNoTenant, as its writes do. With and Has pay nothing for it: what they
// read is scoped already. Of, LoadEach and CountEach pay on a target with no tenant, as
// the owners are the caller's. The tenant converts to the kind each TenantRef states,
// so "2" does not serve an integer column; a named id of that kind passes, as the
// column's own type is not known there: on a join row a UserID serves an OrgID column
// of the same kind. Of, LoadEach and CountEach on a scoped target take the caller's
// owners as given: an owner of another tenant that the caller holds still reads its
// links. Deleting a shared row, one with no tenant, deletes it for every tenant, and
// the ON DELETE CASCADE velox-gen writes on join rows removes every tenant's links to
// it: guard such a delete with a Policy.
//
// An entity with no tenant column that is no join entity is not scoped by any of this,
// though its rows may name, or be reachable only through, tenant rows: every tenant
// reads and changes all of them. Give it a tenant column, or a policy. A hand-written
// join entity without TenantRefs is not scoped either.
func WithTenant(ctx context.Context, id any) context.Context { return engine.WithTenant(ctx, id) }

// WithAllTenants returns ctx unscoped: statements on a tenant-scoped entity
// reach every tenant's rows. The reason is required and is shown to hooks as
// Statement.TenantBypass; with a reason that is empty or only white space
// WithAllTenants returns ctx unchanged, so a statement without a tenant still
// fails. It replaces an earlier WithTenant or WithAllTenants on ctx.
func WithAllTenants(ctx context.Context, reason string) context.Context {
	return engine.WithAllTenants(ctx, reason)
}

// WithoutPolicy returns ctx with every entity's policy switched off. The reason
// is required and is shown to hooks as Statement.PolicyBypass; with a reason
// that is empty or only white space WithoutPolicy returns ctx unchanged. It
// does not lift tenant scoping: see WithAllTenants.
func WithoutPolicy(ctx context.Context, reason string) context.Context {
	return engine.WithoutPolicy(ctx, reason)
}

// RowScope is what a context scopes statements by.
type RowScope = veloxtype.RowScope

// RowScopeFromContext returns ctx's row scope.
func RowScopeFromContext(ctx context.Context) RowScope { return engine.RowScopeFromContext(ctx) }

// IsDryRun reports whether ctx is that of a SQL(ctx) method: the statement
// being built will not be sent. The SQL(ctx) methods mark the context before
// anything runs, so everything handed that context sees it: the entity's
// hooks (Hooks), the Policy functions and the Rewrite hooks. A Rewrite hook
// can also read it from Statement.DryRun.
//
// A write made under such a context is refused: Exec of an Insert, InsertAll,
// Update or Delete, Save, an upsert, DB.Exec (raw SQL) and Tx fail with
// ErrDryRun before anything is sent, on any *DB, so a hook's audit write through
// Mutation.DB does not happen and needs no check. Reads run as normal.
func IsDryRun(ctx context.Context) bool { return engine.IsDryRun(ctx) }
