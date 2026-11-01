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
// and upserts, with the values hooks left, and the join rows of a many-to-many
// edge, which have no tenant of their own and so need WithTenant or
// WithAllTenants like any scoped write. It does not make the reference safe by
// itself: a target deleted after the check is the database foreign key's to
// refuse, and a composite foreign key, (org_id, project_id) to (org_id, id), is
// the stronger guarantee, as it also holds for writes that do not go through
// velox. WithAllTenants sends no check, and a delete of a join row checks nothing.
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
