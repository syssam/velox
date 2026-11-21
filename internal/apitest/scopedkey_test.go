package apitest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/untyped"
)

type hideIOwnerKey struct{}

// hideIOwner is a context under which hideIOwnerFilter hides owner 1.
func hideIOwner(ctx context.Context) context.Context {
	return context.WithValue(ctx, hideIOwnerKey{}, true)
}

// hideIOwnerFilter hides IOwner 1 from reads under hideIOwner's context, and
// restricts nothing else.
func hideIOwnerFilter(ctx context.Context, entity string, op velox.Op) (velox.Predicate, error) {
	if entity != "IOwner" || op != velox.OpSelect || ctx.Value(hideIOwnerKey{}) == nil {
		return velox.NoFilter(), nil
	}
	return velox.FieldNEQ("id", 1), nil
}

// TestScopedKey: Table.ScopedKey is its condition alone where the target
// gets no row condition on the statement, with no EXISTS, and with the
// edge's Has where it gets one, from a WithFilter filter, an interceptor or
// a Read policy: IS NULL is the Has negated, IS NOT NULL the Has, and any
// other condition it AND the Has. The choice is the statement's, so one
// condition, run on one DB under contexts that differ, gives each its own
// SQL, and the render cache, on the plain path and the hooked one, never
// hands one context's SQL to the other.
func TestScopedKey(t *testing.T) {
	notes, owners := untyped.TableOf[fixture.INote](), untyped.TableOf[fixture.IOwner]()
	noteOwner := int(fixture.INotes.Owner.I)
	ownerPinned := int(fixture.IOwners.Pinned.I)
	noteKey := func(op untyped.PredOp, v any) velox.Predicate {
		return notes.ScopedKey(notes.Pred(int(fixture.INotes.OwnerID.I), op, v), noteOwner, owners)
	}
	pinnedKey := func(op untyped.PredOp) velox.Predicate {
		return owners.ScopedKey(owners.Pred(int(fixture.IOwners.PinnedID.I), op, nil), ownerPinned, notes)
	}
	plain := context.Background()
	// INote's interceptor restricts nothing under FilterSkip, and with the
	// policy lifted INote gets no row condition at all.
	open := fixture.WithFilterMode(plain, fixture.FilterSkip)
	type run struct {
		ctx    context.Context
		bypass bool // run on db.WithoutPolicy
		want   []int64
		exists bool
	}
	cases := []struct {
		name  string
		table untyped.Table
		p     velox.Predicate
		runs  []run
	}{
		// Notes 1 and 4 are visible, of owners 1 and 2.
		{"owner EQ 1", notes, noteKey(untyped.PredEQ, int64(1)), []run{
			{plain, false, []int64{1}, false}, {hideIOwner(plain), false, []int64{}, true},
		}},
		{"owner NotIn 2", notes, noteKey(untyped.PredNotIn, []any{int64(2)}), []run{
			{plain, false, []int64{1}, false}, {hideIOwner(plain), false, []int64{}, true},
		}},
		{"owner In 1, 2", notes, noteKey(untyped.PredIn, []any{int64(1), int64(2)}), []run{
			{plain, false, []int64{1, 4}, false}, {hideIOwner(plain), false, []int64{4}, true},
		}},
		// Owner 1 pins note 2, which the interceptor hides; owner 2 pins
		// note 1.
		{"pinned IsNull", owners, pinnedKey(untyped.PredIsNull), []run{
			{open, true, []int64{}, false}, {plain, false, []int64{1}, true}, {open, false, []int64{}, true},
		}},
		{"pinned NotNull", owners, pinnedKey(untyped.PredNotNull), []run{
			{open, true, []int64{1, 2}, false}, {plain, false, []int64{2}, true}, {open, false, []int64{1, 2}, true},
		}},
	}
	var mu sync.Mutex
	var ran []string
	hooked := interceptorDB(t, velox.WithFilter(hideIOwnerFilter), velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		mu.Lock()
		ran = append(ran, st.SQL)
		mu.Unlock()
		return next(ctx)
	}))
	bare := interceptorDB(t, velox.WithFilter(hideIOwnerFilter))
	ids := func(db *velox.DB, tbl untyped.Table, ctx context.Context, p velox.Predicate) []int64 {
		t.Helper()
		rows, err := untyped.Query(db, tbl).Where(p).OrderBy(velox.OrderTerm{Column: 0}).AllowUnbounded().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for i := range rows.Len() {
			out = append(out, *tbl.Field(rows.At(i), 0).(*int64))
		}
		return out
	}
	for _, c := range cases {
		// Every run twice, in turn: the second finds its shape cached.
		for i, r := range append(slices.Clone(c.runs), c.runs...) {
			mu.Lock()
			ran = nil
			mu.Unlock()
			for _, db := range []*velox.DB{hooked, bare} {
				if r.bypass {
					db = db.WithoutPolicy("test")
				}
				if got := ids(db, c.table, r.ctx, c.p); !slices.Equal(got, r.want) {
					t.Errorf("%s, run %d: %v, want %v", c.name, i, got, r.want)
				}
			}
			mu.Lock()
			sql := slices.Clone(ran)
			mu.Unlock()
			if len(sql) != 1 {
				t.Fatalf("%s, run %d: %q", c.name, i, sql)
			}
			if got := strings.Contains(sql[0], "EXISTS"); got != r.exists {
				t.Errorf("%s, run %d: EXISTS %v in %s", c.name, i, got, sql[0])
			}
		}
	}
	// A condition that fails, or a relation that does not resolve, fails
	// the statement.
	if _, err := untyped.Query(bare, notes).Where(notes.ScopedKey(notes.Pred(99, untyped.PredEQ, 1), noteOwner, owners)).Limit(1).All(plain); err == nil {
		t.Error("ScopedKey of an invalid condition: no error")
	}
	if _, err := untyped.Query(bare, notes).Where(notes.ScopedKey(notes.Pred(int(fixture.INotes.OwnerID.I), untyped.PredEQ, int64(1)), 7, owners)).Limit(1).All(plain); err == nil || !strings.Contains(err.Error(), "relation 7") {
		t.Errorf("ScopedKey of no relation: %v", err)
	}
}
