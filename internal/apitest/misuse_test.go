package apitest

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
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
	}
	for name, c := range cases {
		e := &fakeconn.Exec{}
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { c.hook(st); return nil }))
		_, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") || e.Calls != 0 {
			t.Errorf("%s: %v, calls %d", name, err, e.Calls)
		}
	}
}

func TestZeroPredicateIsRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	for name, p := range map[string]velox.Pred[testmodel.Invoice]{
		"top level": {},
		"nested":    velox.And(I.ID.EQ(1), velox.Pred[testmodel.Invoice]{}),
	} {
		_, err := db.Query[testmodel.Invoice]().Where(p).Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), "zero Predicate") || e.Calls != 0 {
			t.Errorf("%s: %v, calls %d", name, err, e.Calls)
		}
	}
	hooked := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.Predicate{})
		return nil
	}))
	if _, err := hooked.Query[testmodel.Invoice]().Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "zero Predicate") || e.Calls != 0 {
		t.Errorf("from a hook: %v, calls %d", err, e.Calls)
	}
	// HasWith does not refuse a zero condition; a hook can add one inside
	// an EXISTS, which checkColumns must still find.
	exists := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.Predicate(I.Items.HasWith(velox.Pred[testmodel.Item]{})))
		return nil
	}))
	if _, err := exists.Query[testmodel.Invoice]().Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "zero Predicate") || e.Calls != 0 {
		t.Errorf("from a hook, inside HasWith: %v, calls %d", err, e.Calls)
	}
}

func TestQuerySelectingNoColumnsIsRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	all := []velox.Column[testmodel.Invoice]{I.CreatedAt, I.UpdatedAt, I.ID, I.OrgID, I.Number, I.Total, I.Paid, I.PaidAt, I.Note, I.Data}
	_, err := db.Query[testmodel.Invoice]().Omit(all...).Limit(1).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "selects no columns") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}

// TestForgedHandle: a handle made by hand with a column index its entity
// does not have fails the statement with an error, in every place a handle
// goes, rather than panicking in the renderer.
func TestForgedHandle(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	bad := velox.NumberCol[testmodel.Invoice, int64]{I: 42}
	neg := velox.NumberCol[testmodel.Invoice, int64]{I: -3}
	ctx := context.Background()
	for _, c := range []velox.NumberCol[testmodel.Invoice, int64]{bad, neg} {
		for name, run := range map[string]func() error{
			"Where": func() error {
				_, err := db.Query[testmodel.Invoice]().Where(c.EQ(1)).Limit(1).All(ctx)
				return err
			},
			"In": func() error {
				_, err := db.Query[testmodel.Invoice]().Where(c.In(1, 2)).Limit(1).All(ctx)
				return err
			},
			"IsNull": func() error {
				_, err := db.Query[testmodel.Invoice]().Where(c.EQ(1), velox.Not(c.GT(2))).Count(ctx)
				return err
			},
			"OrderBy": func() error {
				_, err := db.Query[testmodel.Invoice]().OrderBy(c.Asc()).Limit(1).All(ctx)
				return err
			},
			"Columns": func() error {
				_, err := db.Query[testmodel.Invoice]().Columns(c).Limit(1).All(ctx)
				return err
			},
			"Set": func() error {
				_, err := db.Update[testmodel.Invoice]().Where(testmodel.Invoices.ID.EQ(1)).Set(c.Set(1)).Exec(ctx)
				return err
			},
			"Add": func() error {
				_, err := db.Update[testmodel.Invoice]().Where(testmodel.Invoices.ID.EQ(1)).Set(c.Add(1)).Exec(ctx)
				return err
			},
			"Sum": func() error {
				_, err := velox.Sum(ctx, db.Query[testmodel.Invoice](), c)
				return err
			},
		} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("I %d: %s panicked: %v", c.I, name, r)
					}
				}()
				if err := run(); err == nil || !strings.Contains(err.Error(), "column") {
					t.Errorf("I %d: %s: %v, want an error naming the column", c.I, name, err)
				}
			}()
		}
	}
}

// TestZeroDBAndQueryPanic: the zero DB and the zero Query have no
// connection, as their godoc says: running a statement on either panics.
func TestZeroDBAndQueryPanic(t *testing.T) {
	ctx := context.Background()
	for name, run := range map[string]func(){
		"DB": func() {
			var db velox.DB
			_, _ = db.Query[testmodel.Invoice]().Limit(1).All(ctx)
		},
		"Query": func() {
			var q velox.Query[testmodel.Invoice]
			_, _ = q.Limit(1).All(ctx)
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("the zero %s did not panic", name)
				}
			}()
			run()
		}()
	}
}
