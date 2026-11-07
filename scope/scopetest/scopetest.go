// Package scopetest checks that a scope confines an entity: that a tenancy
// mixin's Policy and Hooks are both bound, and do what package scope says.
// Run it in a test for each entity that lists the mixin:
//
//	func TestProjectIsolated(t *testing.T) {
//		scopetest.Isolates[gen.Project](t, ctx, db, schema.OrgKey, "org_id", 1001, 1002,
//			func() *gen.Project { return &gen.Project{Name: uniqueName()} })
//	}
//
// It writes rows, so run it on a test database.
package scopetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"
)

// Isolates fails t with Check's error, when it returns one.
func Isolates[E any, V comparable](t testing.TB, ctx context.Context, db *velox.DB, key *scope.Key[V], column string, a, b V, row func() *E) {
	t.Helper()
	if err := Check(ctx, db, key, column, a, b, row); err != nil {
		t.Fatal(err)
	}
}

// Check reports whether key's scope confines entity E by its column named
// column, of Go type V. It returns nil when every check holds, and else an
// error that names the first that fails:
//
//   - E has the column, of type V, Immutable, so that no update moves a row
//     to another scope, and NOT NULL, so that every row is in one;
//   - an insert under a, of a row whose column is the zero value, succeeds
//     and fills the column with a;
//   - a sees one more row, and b does not see it;
//   - a delete under b of a's rows affects none;
//   - without a value, a read and an insert fail with scope.ErrNoScope;
//   - an insert under a of a row whose column is b fails with
//     scope.ErrMismatch.
//
// ctx carries what else E's policies need, such as a user, and no value of
// key. a and b are two values no other test uses, so that no other rows
// change the counts. row returns a new row each time, valid for an insert:
// Check inserts two, and the first stays.
func Check[E any, V comparable](ctx context.Context, db *velox.DB, key *scope.Key[V], column string, a, b V, row func() *E) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("scopetest: %v", p)
		}
	}()
	t := velox.TableOf[E]()
	if err := t.Err(); err != nil {
		return fmt.Errorf("scopetest: %w", err)
	}
	name := fmt.Sprintf("%T", *new(E))
	ci, ok := t.Info().Column(column)
	if !ok {
		return fmt.Errorf("scopetest: %s has no column %q", name, column)
	}
	switch c := t.Info().Columns[ci]; {
	case !c.Immutable:
		return fmt.Errorf("scopetest: %s: column %q is mutable: make it Immutable, so that no update moves a row to another scope", name, column)
	case c.Nullable:
		return fmt.Errorf("scopetest: %s: column %q is nullable: a scope's column holds a value in every row", name, column)
	case velox.GoType(c) != reflect.TypeFor[V]():
		return fmt.Errorf("scopetest: %s: column %q is a %v, not a %v", name, column, velox.GoType(c), reflect.TypeFor[V]())
	}
	get := func(x *E) V { return t.Value(x, ci).(V) }
	// The column's type is V, checked above, so SetField cannot fail.
	set := func(x *E, v V) { _ = t.SetField(x, ci, v) }
	inA, inB := key.With(ctx, a), key.With(ctx, b)
	ofA := t.Pred(ci, velox.PredEQ, a)
	count := func(ctx context.Context, where ...velox.Predicate) (int64, error) {
		return db.QueryTable(t).Where(where...).Count(ctx)
	}
	before, err := count(inA)
	if err != nil {
		return fmt.Errorf("scopetest: %s: a count under %v: %w", name, a, err)
	}

	var zero V
	x := row()
	set(x, zero)
	if err := db.InsertTable(t, x).Exec(inA); err != nil {
		return fmt.Errorf("scopetest: %s: an insert under %v: %w", name, a, err)
	}
	if got := get(x); got != a {
		return fmt.Errorf("scopetest: %s: an insert under %v left %s %v: no hook fills it (list the mixin's Hooks)", name, a, column, got)
	}
	if n, err := count(inA); err != nil || n != before+1 {
		return fmt.Errorf("scopetest: %s: %v sees %d rows after its insert, want %d (%v)", name, a, n, before+1, err)
	}
	if n, err := count(inB, ofA); err != nil || n != 0 {
		return fmt.Errorf("scopetest: %s: %v sees %d of %v's rows, want 0 (%v): no policy confines reads (list the mixin's Policy)", name, b, n, a, err)
	}
	if n, err := db.DeleteTable(t).Where(ofA).Exec(inB); err != nil || n != 0 {
		return fmt.Errorf("scopetest: %s: a delete under %v of %v's rows affected %d, want 0 (%v)", name, b, a, n, err)
	}

	if _, err := count(ctx); !errors.Is(err, scope.ErrNoScope) {
		return fmt.Errorf("scopetest: %s: a read without a value gave %v, want scope.ErrNoScope", name, err)
	}
	if err := db.InsertTable(t, row()).Exec(ctx); !errors.Is(err, scope.ErrNoScope) {
		return fmt.Errorf("scopetest: %s: an insert without a value gave %v, want scope.ErrNoScope", name, err)
	}
	y := row()
	set(y, b)
	if err := db.InsertTable(t, y).Exec(inA); !errors.Is(err, scope.ErrMismatch) {
		return fmt.Errorf("scopetest: %s: an insert under %v of a row of %v gave %v, want scope.ErrMismatch", name, a, b, err)
	}
	return nil
}
