package velox_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestNilHooksPanic(t *testing.T) {
	for name, f := range map[string]func(){
		"WithRewrite": func() { velox.WithRewrite(nil) },
		"WithAround":  func() { velox.WithAround(nil) },
	} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(r.(string), name+"(nil)") {
					t.Errorf("%s(nil): recovered %v", name, r)
				}
			}()
			f()
		}()
	}
}

func TestRewriteCannotNilOrForeignEntity(t *testing.T) {
	cases := map[string]struct {
		hook func(st *velox.Statement)
		want string
	}{
		"nil": {func(st *velox.Statement) { st.Entity = nil }, "set the statement's Entity to nil"},
		"foreign": {func(st *velox.Statement) {
			info := velox.EntityInfo{Name: "other.Thing", Table: "things", Columns: st.Entity.Columns}
			st.Entity = &info
		}, "not a copy of it"},
		"unscoped copy": {func(st *velox.Statement) {
			info := *st.Entity
			info.Tenant = -1
			st.Entity = &info
		}, "a copy whose tenant column or policy differs"},
	}
	for name, c := range cases {
		e := &velox.FakeExec{}
		db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { c.hook(st); return nil }))
		_, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") || e.Calls != 0 {
			t.Errorf("%s: %v, calls %d", name, err, e.Calls)
		}
	}
}

func TestZeroPredicateIsRefused(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	for name, p := range map[string]velox.Pred[testmodel.Invoice]{
		"top level": {},
		"nested":    velox.And(I.ID.EQ(1), velox.Pred[testmodel.Invoice]{}),
	} {
		_, err := db.Query[testmodel.Invoice]().Where(p).Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), "zero Predicate") || e.Calls != 0 {
			t.Errorf("%s: %v, calls %d", name, err, e.Calls)
		}
	}
	hooked := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.Predicate{})
		return nil
	}))
	if _, err := hooked.Query[testmodel.Invoice]().Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "zero Predicate") || e.Calls != 0 {
		t.Errorf("from a hook: %v, calls %d", err, e.Calls)
	}
	// HasWhere does not refuse a zero condition; a hook can add one inside
	// an EXISTS, which checkColumns must still find.
	exists := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.Predicate(I.Items.HasWhere(velox.Pred[testmodel.Item]{})))
		return nil
	}))
	if _, err := exists.Query[testmodel.Invoice]().Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "zero Predicate") || e.Calls != 0 {
		t.Errorf("from a hook, inside HasWhere: %v, calls %d", err, e.Calls)
	}
}

func TestQuerySelectingNoColumnsIsRefused(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	all := []velox.Column[testmodel.Invoice]{I.CreatedAt, I.UpdatedAt, I.ID, I.OrgID, I.Number, I.Total, I.Paid, I.PaidAt, I.Note, I.Data}
	_, err := db.Query[testmodel.Invoice]().Omit(all...).Limit(1).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "selects no columns") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}

// TestInCopiesItsValues: a predicate holds its own copy of In's values, so
// neither the caller nor a hook can change a built query's arguments
// through the slice (closes the Core follow-up on the one-level copy).
func TestInCopiesItsValues(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	ids := []int64{1, 2}
	q := db.Query[testmodel.Invoice]().Where(I.ID.In(ids...)).Limit(5)
	ids[0] = 9
	if _, args := sqlOK(t)(q.SQL(ctx)); args[0] != int64(1) {
		t.Errorf("args %v: In shares the caller's slice", args)
	}
}
