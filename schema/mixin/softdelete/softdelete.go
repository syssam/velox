// Package softdelete is the mixin that makes an entity's delete set
// deleted_at, and hides rows whose deleted_at is set from every read and
// write. An entity lists it in its Mixin method:
//
//	func (Invoice) Mixin() []schema.Mixin {
//		return []schema.Mixin{mixin.Time{}, softdelete.Mixin{}}
//	}
//
// velox-gen binds its Interceptors and Hooks for each entity that lists it.
//
// # Reads and writes
//
// The interceptor ANDs deleted_at IS NULL into every statement on the
// entity: a query, Count, an aggregate, a With load, a Has, a join, an
// update, a delete, a Save and an upsert's update branch. So an update does
// not change a deleted row, and deleting a deleted row affects 0 rows. Raw
// SQL is not filtered. velox.WithoutPolicy does not lift it: an operator who
// lifts the policies still does not see deleted rows unless they say so.
//
// # Delete
//
// The hook turns a delete into an update that sets deleted_at to the time
// of the delete, and returns the rows it changed as the delete's count. The
// update runs the entity's whole update pipeline: its update hooks, its
// policies, the interceptors and its UpdateDefault columns, such as
// updated_at. So:
//
//   - The entity's own delete hooks see only hard deletes, as the mixin's
//     hook runs before them and does not call them. A soft delete reaches
//     its update hooks.
//   - SQL(ctx) of a soft delete fails with velox.ErrDryRun: there is no
//     DELETE to render.
//   - ON DELETE CASCADE does not fire. A child row stays; a hook that wants
//     to cascade soft-deletes the children itself.
//
// # Skip
//
// [Skip] returns a context under which the mixin does nothing: reads see
// deleted rows, and a delete removes rows for good, deleted ones too. It
// lifts nothing else: a policy, a tenant's included, still applies. A purge
// is a delete under Skip:
//
//	db.Delete[gen.Invoice]().Where(gen.Invoices.DeletedAt.NotNull()).Exec(softdelete.Skip(ctx))
//
// and a restore is an update under Skip, as without it the interceptor hides
// the row from the update:
//
//	db.Update[gen.Invoice]().Where(gen.Invoices.ID.EQ(id)).
//		Set(gen.Invoices.DeletedAt.SetNull()).Exec(softdelete.Skip(ctx))
//
// # Uniqueness
//
// A unique key over live rows only is a partial index. On PostgreSQL and
// SQLite, declare the entity's unique keys with a Where:
//
//	index.Fields("org_id", "email").Unique().Where("deleted_at IS NULL")
//
// so that a deleted row does not block a new one with the same email. MySQL
// has no partial index, and velox writes no workaround. There, either keep
// the key over every row, so that a deleted row blocks the value until it is
// purged or restored, or check uniqueness in a hook.
package softdelete

import (
	"context"
	"fmt"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Column is the name of the column the mixin adds.
const Column = "deleted_at"

// Mixin adds deleted_at, and the interceptor and hook that use it. List it
// in a schema's Mixin method.
type Mixin struct{ mixin.Schema }

// Fields returns deleted_at, NULL while the row is live.
func (Mixin) Fields() []schema.Field {
	return []schema.Field{field.Time(Column).Optional().Nillable()}
}

type skipKey struct{}

// Skip returns ctx under which the mixin does nothing: reads see deleted
// rows, and a delete removes rows for good. It lifts nothing else: a policy,
// a tenant's included, still applies. It records itself with
// velox.WithScopeValue, so that the GraphQL batcher keeps callers with and
// without it apart.
func Skip(ctx context.Context) context.Context {
	return velox.WithScopeValue(context.WithValue(ctx, skipKey{}, true), "softdelete", "skip")
}

// Skipped reports whether ctx is under Skip.
func Skipped(ctx context.Context) bool {
	v, _ := ctx.Value(skipKey{}).(bool)
	return v
}

// Interceptors returns the interceptor that hides rows whose deleted_at is
// set, but under Skip.
func (Mixin) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(ctx context.Context) (velox.Cond, error) {
		if Skipped(ctx) {
			return velox.Allow(), nil
		}
		return velox.C(Column).IsNull(), nil
	}}}
}

// Hooks returns the hook that makes a delete an update that sets
// deleted_at, but under Skip.
func (Mixin) Hooks() []velox.Hook {
	return []velox.Hook{velox.On(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			if Skipped(ctx) {
				return next.Mutate(ctx, m)
			}
			n, err := softDelete(ctx, m, time.Now())
			m.SetAffected(n)
			return err
		})
	}, velox.OpDelete)}
}

// softDelete updates the rows the delete would remove to set deleted_at to
// now, or every row when where is empty: an empty where is the delete's
// All, as velox refused a delete with neither a Where nor All before the
// hooks ran.
func softDelete(ctx context.Context, m *velox.Mutation, now time.Time) (int64, error) {
	t := m.Table()
	col, ok := t.Info().Column(Column)
	if !ok {
		return 0, fmt.Errorf("velox/softdelete: %s has no column %s", t.Info().Name, Column)
	}
	set, err := t.Assign(col, now)
	if err != nil {
		return 0, err
	}
	u := m.DB().UpdateTable(t).Assign(set)
	if cs := m.Conds(); len(cs) > 0 {
		u = u.Where(velox.Predicates(cs)...)
	} else {
		u = u.All()
	}
	return u.Exec(ctx)
}
