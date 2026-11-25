// Package softdelete is the mixin that makes an entity's delete set
// deleted_at, and hides rows whose deleted_at is set from every read and
// write (by default; see Options). An entity lists it in its Mixin method:
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
// SQL is not filtered. velox.DB.WithoutPolicy does not lift it: an operator who
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
//   - A velox.WithHooksAll hook runs before the mixin's, so it sees a soft
//     delete twice: as the delete (OpDelete), then as the mixin's update
//     inside it (OpUpdate). That update's m.Value(Column) is a time; a
//     restore's is nil.
//   - A velox.WithFilter filter is given OpDelete for the soft delete's
//     update, so one that restricts deletes restricts it too. The entity's
//     policies see an update.
//   - SQL(ctx) of a soft delete fails with velox.ErrDryRun: there is no
//     DELETE to render.
//   - ON DELETE CASCADE does not fire. A child row stays; a hook that wants
//     to cascade soft-deletes the children itself.
//   - Through a recording handle (velox.DB.Record), each row is reported as
//     a delete with Soft set, and with the row before and after the update.
//
// # Options
//
// Assign sets more columns in the soft delete's UPDATE, read from the
// delete's context, such as who deleted the rows; its error fails the
// delete. By makes an Assign of a scope.Key's value, and Assigns joins
// several. Reads: Unfiltered installs no interceptor, so reads and updates
// see deleted rows, while a delete stays soft and marks live rows only:
//
//	softdelete.Mixin{
//		Assign: func(ctx context.Context) ([]softdelete.Set, error) {
//			return []softdelete.Set{{Column: "deleted_by_id", Value: auth.UserID(ctx)}}, nil
//		},
//		Reads: softdelete.Unfiltered,
//	}
//
// # Skip
//
// [Skip] returns a context under which the mixin does nothing: reads see
// deleted rows, and a delete removes rows for good, deleted ones too. It
// lifts nothing else: a policy, a tenant's included, still applies. A purge
// is a delete under Skip:
//
//	db.Delete[veloxgen.Invoice]().Where(veloxgen.Invoices.DeletedAt.NotNull()).Exec(softdelete.Skip(ctx))
//
// and a restore is an update under Skip, as without it the interceptor hides
// the row from the update:
//
//	db.Update[veloxgen.Invoice]().Where(veloxgen.Invoices.ID.EQ(id)).
//		Set(veloxgen.Invoices.DeletedAt.SetNull()).Exec(softdelete.Skip(ctx))
//
// # Uniqueness
//
// A unique key over live rows only is a partial index. On PostgreSQL and
// SQLite, declare the entity's unique keys partial:
//
//	index.Fields("org_id", "email").Unique().Annotations(sqlschema.IndexWhere("deleted_at IS NULL"))
//
// so that a deleted row does not block a new one with the same email. MySQL
// has no partial index, and velox writes no workaround. There, either keep
// the key over every row, so that a deleted row blocks the value until it is
// purged or restored, or check uniqueness in a hook.
package softdelete

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/scope"
	"github.com/syssam/velox/untyped"
)

// Column is the name of the column the mixin adds.
const Column = "deleted_at"

// Mixin adds deleted_at, and the interceptor and hook that use it. List it
// in a schema's Mixin method. Its zero value filters reads and sets only
// deleted_at; Assign and Reads change that.
type Mixin struct {
	mixin.Schema
	// Assign, when not nil, returns more columns for a soft delete to set,
	// read from the delete's context, such as who deleted the rows:
	//
	//	softdelete.Mixin{Assign: func(ctx context.Context) ([]softdelete.Set, error) {
	//		return []softdelete.Set{{Column: "deleted_by_id", Value: auth.UserID(ctx)}}, nil
	//	}}
	//
	// It runs once per delete, in the hook, and its columns go into the
	// same UPDATE as deleted_at. An error from it, an unknown column, a value
	// Mutation.Set would refuse, or deleted_at or a primary-key column among
	// them fails the delete, and nothing is sent. It does not run under Skip,
	// whose delete removes the rows.
	Assign func(ctx context.Context) ([]Set, error)
	// Reads says whether reads hide deleted rows: Filtered, the zero value,
	// or Unfiltered.
	Reads ReadMode
}

// Set is a column a soft delete sets beside deleted_at, by the column's
// name, as velox.FieldEQ names it, and its value, converted as
// Mutation.Set converts it; nil sets NULL.
type Set struct {
	Column string
	Value  any
}

// By returns an Assign that sets column to k's value in the delete's
// context, such as the user who deletes, and sets nothing when the context
// has none (scope.Key.From), so the column keeps its value, NULL for a live
// row:
//
//	var User = scope.NewKey[int64]("user")
//
//	softdelete.Mixin{Assign: softdelete.By("deleted_by_id", User)}
//
// Assigns joins several, as for a row that records one of several kinds of
// actor.
func By[V comparable](column string, k *scope.Key[V]) func(context.Context) ([]Set, error) {
	return func(ctx context.Context) ([]Set, error) {
		v, ok := k.From(ctx)
		if !ok {
			return nil, nil
		}
		return []Set{{Column: column, Value: v}}, nil
	}
}

// Assigns returns an Assign that runs each of fs in order and sets every
// column they return; the first error fails the delete:
//
//	softdelete.Mixin{Assign: softdelete.Assigns(
//		softdelete.By("deleted_by_user_id", User),
//		softdelete.By("deleted_by_member_id", Member),
//	)}
func Assigns(fs ...func(context.Context) ([]Set, error)) func(context.Context) ([]Set, error) {
	return func(ctx context.Context) ([]Set, error) {
		var all []Set
		for _, f := range fs {
			s, err := f(ctx)
			if err != nil {
				return nil, err
			}
			all = append(all, s...)
		}
		return all, nil
	}
}

// ReadMode says whether the mixin hides deleted rows.
type ReadMode uint8

// The read modes.
const (
	// Filtered hides deleted rows from every read and write: the mixin's
	// interceptor ANDs deleted_at IS NULL into each statement.
	Filtered ReadMode = iota
	// Unfiltered installs no interceptor: reads, updates and Saves see
	// deleted rows, which the application tells apart by deleted_at, and a
	// restore is a plain update. A delete stays soft, and sets deleted_at on
	// live rows only, so a second delete affects 0 rows and keeps the first
	// one's time.
	Unfiltered
)

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
// set, but under Skip; none when Reads is Unfiltered.
func (sd Mixin) Interceptors() []velox.Interceptor {
	if sd.Reads == Unfiltered {
		return nil
	}
	return []velox.Interceptor{{Filter: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
		if Skipped(ctx) {
			return velox.Allow(), nil
		}
		return velox.FieldIsNull(Column), nil
	}}}
}

// Hooks returns the hook that makes a delete an update that sets
// deleted_at, and Assign's columns, but under Skip.
func (sd Mixin) Hooks() []velox.Hook {
	return []velox.Hook{velox.On(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			if Skipped(ctx) {
				return next.Mutate(ctx, m)
			}
			n, err := sd.softDelete(ctx, m, time.Now())
			m.SetAffected(n)
			return err
		})
	}, velox.OpDelete)}
}

// softDelete updates the rows the delete would remove to set deleted_at to
// now, and Assign's columns, or every row when where is empty: an empty
// where is the delete's All, as velox refused a delete with neither a Where
// nor All before the hooks ran. Under Unfiltered, where no interceptor
// hides deleted rows, it ANDs deleted_at IS NULL itself.
func (sd Mixin) softDelete(ctx context.Context, m *velox.Mutation, now time.Time) (int, error) {
	t := untyped.MutationTable(m)
	in := t.Info()
	col, ok := in.Column(Column)
	if !ok {
		return 0, fmt.Errorf("velox/softdelete: %s has no column %s", in.Name, Column)
	}
	set, err := t.Assign(col, now)
	if err != nil {
		return 0, err
	}
	sets := []engine.Assignment{set}
	if sd.Assign != nil {
		extra, err := sd.Assign(ctx)
		if err != nil {
			return 0, fmt.Errorf("velox/softdelete: %s: Assign: %w", in.Name, err)
		}
		for _, s := range extra {
			a, err := engine.NamedAssignment(in, s.Column, s.Value)
			if err != nil {
				return 0, fmt.Errorf("velox/softdelete: Assign: %w", err)
			}
			if a.Column == col {
				return 0, fmt.Errorf("velox/softdelete: %s: Assign sets %s, which the mixin sets", in.Name, Column)
			}
			if slices.Contains(in.PrimaryKey, a.Column) {
				return 0, fmt.Errorf("velox/softdelete: %s: Assign sets %s, a primary-key column", in.Name, s.Column)
			}
			sets = append(sets, a)
		}
	}
	// Marked a soft delete, so that a recording handle reports its rows as
	// deletes, Soft; it sends the same statement.
	u := engine.MarkSoftDelete(untyped.Update(m.DB(), t)).Assign(sets...)
	cs := slices.Clip(m.Predicates())
	if sd.Reads == Unfiltered {
		cs = append(cs, velox.FieldIsNull(Column))
	}
	if len(cs) > 0 {
		u = u.Where(cs...)
	} else {
		u = u.All()
	}
	return u.Exec(ctx)
}
