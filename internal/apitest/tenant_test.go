package apitest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

var Tk = testmodel.Tickets

const ticketCols = `"id", "created_by", "org_id", "owner", "title", "done", "invoice_id", "parent_id"`

// tenant returns a context scoped to tenant id, with the policy skipped: the
// tenant tests check the scope alone.
func tenant(id any) context.Context {
	return velox.WithTenant(velox.WithoutPolicy(context.Background(), "tenant tests"), id)
}

func ticketRow(id, org int64, parent any) []any {
	return []any{id, "ann", org, "ann", fmt.Sprintf("T-%d", id), false, int64(1), parent}
}

func wantSent(t *testing.T, e *fakeconn.Exec, sql string, args ...any) {
	t.Helper()
	if e.SQL != sql {
		t.Errorf("SQL\n got %s\nwant %s", e.SQL, sql)
	}
	if !reflect.DeepEqual(e.Args, args) {
		t.Errorf("args %#v, want %#v", e.Args, args)
	}
}

func TestTenantPredicateIsAdded(t *testing.T) {
	bg := context.Background()
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5).All(velox.WithTenant(bg, int64(7))); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+allCols+` FROM "invoices" WHERE "paid" = $1 AND "org_id" = $2 LIMIT $3`, true, int64(7), 5)
}

func TestTenantPredicateOnEveryOperation(t *testing.T) {
	t7 := velox.WithTenant(context.Background(), int64(7))
	e := &fakeconn.Exec{Rows: [][]any{{int64(3)}}, Affected: 1}
	db := openFake(velox.Postgres, e)

	if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Count(t7); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT COUNT(*) FROM "invoices" WHERE "paid" = $1 AND "org_id" = $2`, true, int64(7))

	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(t7); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.SQL, ` WHERE "id" = $3 AND "org_id" = $4`) || e.Args[3] != int64(7) {
		t.Errorf("update: %s %v", e.SQL, e.Args)
	}

	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).All().Exec(t7); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.SQL, ` WHERE "org_id" = $3`) || e.Args[2] != int64(7) {
		t.Errorf("update of all rows: %s %v", e.SQL, e.Args)
	}

	if _, err := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(t7); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `DELETE FROM "invoices" WHERE "id" = $1 AND "org_id" = $2`, int64(1), int64(7))

	if _, err := db.Delete[testmodel.Invoice]().All().Exec(t7); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `DELETE FROM "invoices" WHERE "org_id" = $1`, int64(7))

	inv := testmodel.Invoice{ID: 1, OrgID: 7, Number: "INV-1"}
	if err := db.Save(&inv, I.Number).Exec(t7); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.SQL, ` WHERE "id" = $3 AND "org_id" = $4`) || e.Args[3] != int64(7) {
		t.Errorf("save: %s %v", e.SQL, e.Args)
	}
}

// TestTenantScopeOnBothPaths: the scope applies with hooks (run) as without
// (plain), after the Rewrite hooks, so they see the statement unscoped.
func TestTenantScopeOnBothPaths(t *testing.T) {
	var where int
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		where = len(st.Where)
		return nil
	}))
	if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5).All(tenant(int64(7))); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+allCols+` FROM "invoices" WHERE "paid" = $1 AND "org_id" = $2 LIMIT $3`, true, int64(7), 5)
	if where != 1 {
		t.Errorf("the Rewrite hook saw %d conditions, want the caller's 1", where)
	}
}

// TestNoTenantFailsEveryOperation: with neither WithTenant nor WithAllTenants,
// every operation on a scoped entity fails, on both paths, and sends nothing.
func TestNoTenantFailsEveryOperation(t *testing.T) {
	bg := context.Background()
	around := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		return next(ctx)
	})
	for _, opts := range [][]velox.Option{nil, {around}} {
		e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}, Affected: 1}
		db := openFake(velox.Postgres, e, opts...)
		inv := testmodel.Invoice{ID: 1, OrgID: 7}
		var iterErr error
		for _, err := range db.Query[testmodel.Invoice]().Limit(5).Iter(bg) {
			iterErr = err
		}
		_, allErr := db.Query[testmodel.Invoice]().Limit(5).All(bg)
		_, oneErr := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(bg)
		_, countErr := db.Query[testmodel.Invoice]().Count(bg)
		_, updErr := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(bg)
		_, delErr := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(bg)
		saveErr := db.Save(&inv, I.Number).Exec(bg)
		for name, err := range map[string]error{"All": allErr, "One": oneErr, "Iter": iterErr, "Count": countErr,
			"Update": updErr, "Delete": delErr, "Save": saveErr} {
			if !errors.Is(err, velox.ErrNoTenant) || err.Error() != "velox: testmodel.Invoice: no tenant in the context" {
				t.Errorf("%s (hooks %d): %v", name, len(opts), err)
			}
		}
		if e.Calls != 0 {
			t.Errorf("hooks %d: %d statements sent", len(opts), e.Calls)
		}
	}
}

func TestAllTenantsAddsNothing(t *testing.T) {
	var seen []velox.Statement
	var mu sync.Mutex
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		mu.Lock()
		seen = append(seen, *st)
		mu.Unlock()
		return next(ctx)
	}))
	all := velox.WithAllTenants(context.Background(), "reason")
	if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5).All(all); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+allCols+` FROM "invoices" WHERE "paid" = $1 LIMIT $2`, true, 5)
	if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5).All(tenant(int64(7))); err != nil {
		t.Fatal(err)
	}
	e.Rows = nil
	if _, err := db.Query[testmodel.Item]().Limit(5).All(tenant(int64(7))); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("the Around hook saw %d statements", len(seen))
	}
	if seen[0].TenantBypass != "reason" || seen[0].Tenant != nil {
		t.Errorf("WithAllTenants: Tenant %v, TenantBypass %q", seen[0].Tenant, seen[0].TenantBypass)
	}
	if seen[1].Tenant != int64(7) || seen[1].TenantBypass != "" {
		t.Errorf("WithTenant: Tenant %v, TenantBypass %q", seen[1].Tenant, seen[1].TenantBypass)
	}
	if seen[2].Tenant != nil || seen[2].TenantBypass != "" {
		t.Errorf("an unscoped entity: Tenant %v, TenantBypass %q", seen[2].Tenant, seen[2].TenantBypass)
	}
}

// TestTenantTypeMustMatch is Review Focus 2: an id of a named type is never
// converted, so an orgID is not the column's int64, though it is an int64.
func TestTenantTypeMustMatch(t *testing.T) {
	type orgID int64
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openFake(velox.Postgres, e)
	_, err := db.Query[testmodel.Invoice]().Limit(5).All(velox.WithTenant(context.Background(), orgID(7)))
	if !errors.Is(err, velox.ErrTenantMismatch) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: tenant does not match") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "int64") || !strings.Contains(err.Error(), "orgID") {
		t.Errorf("the error names neither type: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

func TestTenantColumnAssignments(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	t7 := tenant(int64(7))
	upd := func(s velox.Setter[testmodel.Invoice]) velox.UpdateQuery[testmodel.Invoice] {
		return db.Update[testmodel.Invoice]().Set(s).Where(I.ID.EQ(1))
	}
	if _, err := upd(I.OrgID.Set(7)).Exec(t7); err != nil {
		t.Errorf("Set to the tenant: %v", err)
	}
	calls := e.Calls
	for name, s := range map[string]velox.Setter[testmodel.Invoice]{"Set(8)": I.OrgID.Set(8), "Add(1)": I.OrgID.Add(1), "Sub(1)": I.OrgID.Sub(1)} {
		_, err := upd(s).Exec(t7)
		if !errors.Is(err, velox.ErrTenantImmutable) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: tenant column cannot change") {
			t.Errorf("%s: %v", name, err)
		}
	}
	inv := testmodel.Invoice{ID: 1, OrgID: 8}
	if err := db.Save(&inv, I.OrgID).Exec(t7); !errors.Is(err, velox.ErrTenantImmutable) {
		t.Errorf("Save of another tenant: %v", err)
	}
	if e.Calls != calls {
		t.Errorf("%d refused statements sent", e.Calls-calls)
	}
	inv.OrgID = 7
	if err := db.Save(&inv, I.OrgID).Exec(t7); err != nil {
		t.Errorf("Save of the tenant: %v", err)
	}
	all := velox.WithAllTenants(context.Background(), "move an invoice")
	if _, err := upd(I.OrgID.Set(8)).Exec(all); err != nil {
		t.Errorf("WithAllTenants, Set(8): %v", err)
	}
	if _, err := upd(I.OrgID.Add(1)).Exec(all); err != nil {
		t.Errorf("WithAllTenants, Add(1): %v", err)
	}
}

func TestHasOnAScopedTarget(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Ticket]().Where(Tk.Invoice.Has()).Limit(1).All(tenant(int64(7))); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE EXISTS (SELECT 1 FROM "invoices" AS "vx1" WHERE "vx1"."id" = "tickets"."invoice_id" AND "org_id" = $1) AND "org_id" = $2 LIMIT $3`,
		int64(7), int64(7), 1)
}

// TestHasOnAScopedTargetFromAnUnscopedEntity: the target's scope applies
// though the statement's own entity has none.
func TestHasOnAScopedTargetFromAnUnscopedEntity(t *testing.T) {
	var seen velox.Statement
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		seen = *st
		return next(ctx)
	}))
	q := db.Query[testmodel.Item]().Where(It.Qty.GT(0), velox.Not(It.Invoice.HasWhere(I.Paid.EQ(true)))).Limit(1)
	if _, err := q.All(tenant(int64(7))); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT "id", "invoice_id", "sku", "qty" FROM "items" WHERE "qty" > $1 AND NOT (EXISTS (SELECT 1 FROM "invoices" AS "vx1" WHERE "vx1"."id" = "items"."invoice_id" AND "paid" = $2 AND "org_id" = $3)) LIMIT $4`,
		int64(0), true, int64(7), 1)
	if seen.Tenant != int64(7) {
		t.Errorf("Statement.Tenant %v", seen.Tenant)
	}

	_, err := q.All(context.Background())
	want(t, err, velox.ErrNoTenant, "velox: testmodel.Item: in the EXISTS on testmodel.Invoice: no tenant in the context")
	_, err = q.All(velox.WithTenant(context.Background(), "acme"))
	if !errors.Is(err, velox.ErrTenantMismatch) || !strings.HasPrefix(err.Error(), "velox: testmodel.Item: in the EXISTS on testmodel.Invoice: tenant does not match: ") {
		t.Errorf("mismatch inside EXISTS: %v", err)
	}
	if _, err := q.All(velox.WithAllTenants(context.Background(), "reason")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SQL, "org_id") || seen.TenantBypass != "reason" {
		t.Errorf("WithAllTenants: %s, TenantBypass %q", e.SQL, seen.TenantBypass)
	}

	// An unscoped entity whose conditions reach no scoped target needs no
	// tenant.
	if _, err := db.Query[testmodel.Item]().Where(It.Qty.GT(0)).Limit(1).All(context.Background()); err != nil {
		t.Errorf("unscoped: %v", err)
	}
}

// TestNestedHasIsScopedAtEveryLevel: a HasWhere inside a HasWhere scopes
// each target.
func TestNestedHasIsScopedAtEveryLevel(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	q := db.Query[testmodel.Ticket]().Where(Tk.Parent.HasWhere(Tk.Invoice.Has())).Limit(1)
	if _, err := q.All(tenant(int64(7))); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE EXISTS (SELECT 1 FROM "tickets" AS "vx1" WHERE "vx1"."id" = "tickets"."parent_id" AND EXISTS (SELECT 1 FROM "invoices" AS "vx2" WHERE "vx2"."id" = "vx1"."invoice_id" AND "org_id" = $1) AND "org_id" = $2) AND "org_id" = $3 LIMIT $4`,
		int64(7), int64(7), int64(7), 1)
}

// TestScopeNeverChangesASharedQuery is Review Focus 1: the scope copies the
// conditions it changes, so a query and its predicates can be run under many
// tenants at once.
func TestScopeNeverChangesASharedQuery(t *testing.T) {
	has := Tk.Invoice.HasWhere(I.Paid.EQ(true))
	cond := velox.Or(Tk.Done.EQ(true), velox.And(Tk.Title.NEQ(""), has))

	e0 := &fakeconn.Exec{}
	db0 := openFake(velox.Postgres, e0)
	base := db0.Query[testmodel.Ticket]().Where(cond, has).Limit(1)
	sql0, args0 := sqlOK(t)(base.SQL(tenant(int64(1))))
	run0 := func() (string, []any) {
		t.Helper()
		e0.Log = nil
		if _, err := base.All(tenant(int64(1))); err != nil {
			t.Fatal(err)
		}
		return e0.Log[0], e0.Args
	}
	before, beforeArgs := run0()

	// dbH has an Around hook, so its statements take run's path, where
	// prepare clones Where; db0 stays plain to keep the plain path covered.
	// The hook checks, for each statement, that it holds the statement's
	// tenant three times and no other goroutine's.
	var hookBad atomic.Pointer[string]
	eH := &fakeconn.Exec{}
	dbH := openFake(velox.Postgres, eH, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		n := 0
		for _, a := range st.Args {
			if a == st.Tenant {
				n++
			}
		}
		if n != 3 || slices.ContainsFunc(st.Args, func(a any) bool { v, ok := a.(int64); return ok && a != st.Tenant && v >= 100 }) {
			s := fmt.Sprintf("tenant %v: args %v", st.Tenant, st.Args)
			hookBad.CompareAndSwap(nil, &s)
		}
		return next(ctx)
	}))
	baseH := dbH.Query[testmodel.Ticket]().Where(cond, has).Limit(1)
	sqlH0, argsH0 := sqlOK(t)(baseH.SQL(tenant(int64(1))))

	var wg sync.WaitGroup
	for i := range 50 {
		id := int64(i + 100)
		wg.Go(func() {
			e := &fakeconn.Exec{}
			db := openFake(velox.Postgres, e)
			if _, err := db.Query[testmodel.Ticket]().Where(cond, has).Limit(1).All(tenant(id)); err != nil {
				t.Error(err)
				return
			}
			n := 0
			for _, a := range e.Args {
				if a == id {
					n++
				}
			}
			// The ticket's own tenant, and the invoice's in each EXISTS.
			if n != 3 || slices.ContainsFunc(e.Args, func(a any) bool { v, ok := a.(int64); return ok && v != id && v >= 100 }) {
				t.Errorf("tenant %d: args %v", id, e.Args)
			}
		})
		wg.Go(func() {
			// The shared query itself, on the plain path: its Where must be
			// copied before an EXISTS in it is replaced.
			if _, err := base.All(tenant(id)); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			// The same on the hooked path, run's.
			if _, err := baseH.All(tenant(id)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if s := hookBad.Load(); s != nil {
		t.Errorf("hooked path, %s", *s)
	}
	if sql, args := sqlOK(t)(baseH.SQL(tenant(int64(1)))); sql != sqlH0 || !reflect.DeepEqual(args, argsH0) {
		t.Errorf("the hooked base query's SQL changed:\n%s %v\n%s %v", sqlH0, argsH0, sql, args)
	}

	if sql, args := sqlOK(t)(base.SQL(tenant(int64(1)))); sql != sql0 || !reflect.DeepEqual(args, args0) {
		t.Errorf("the base query's SQL changed:\n%s %v\n%s %v", sql0, args0, sql, args)
	}
	if after, afterArgs := run0(); after != before || !reflect.DeepEqual(afterArgs, beforeArgs) {
		t.Errorf("the base query sends another statement:\n%s %v\n%s %v", before, beforeArgs, after, afterArgs)
	}
}

func TestWithLoadIsScoped(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{ticketRow(2, 7, int64(1))},
		{ticketRow(1, 7, nil)},
	}}
	db := openFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Ticket]().With(Tk.Parent).Limit(5).All(tenant(int64(7)))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := got[0].Parent.Get(); !ok || p == nil || p.ID != 1 {
		t.Fatalf("parent %v, %v", p, ok)
	}
	if len(e.Log) != 2 {
		t.Fatalf("%d statements: %v", len(e.Log), e.Log)
	}
	if !strings.Contains(e.Log[1], `WHERE "id" IN ($1) AND "org_id" = $2 `) || e.Args[1] != int64(7) {
		t.Errorf("the parent load is unscoped: %s %v", e.Log[1], e.Args)
	}

	// An unscoped read loading a scoped relation needs a tenant for the load.
	e = &fakeconn.Exec{Results: [][][]any{{{int64(1), int64(1), "SKU", int64(2)}}, {invoiceRow(1)}}}
	db = openFake(velox.Postgres, e)
	_, err = db.Query[testmodel.Item]().With(It.Invoice).Limit(5).All(context.Background())
	want(t, err, velox.ErrNoTenant, "velox: testmodel.Item: loading Invoice: testmodel.Invoice: no tenant in the context")
	if len(e.Log) != 1 {
		t.Errorf("%d statements sent, want the item read alone", len(e.Log))
	}
}

// TestScopeNeverAppendsIntoAHooksArray: a Rewrite hook may point st.Where at
// an array it does not own, with room past its length; the scope's tenant
// condition goes into a copy, never into that array.
func TestScopeNeverAppendsIntoAHooksArray(t *testing.T) {
	shared := make([]velox.Predicate, 0, 4)
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = shared[:0]
		return nil
	}))
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(tenant(int64(7))); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+allCols+` FROM "invoices" WHERE "org_id" = $1 LIMIT $2`, int64(7), 1)
	if !reflect.ValueOf(shared[:1][0]).IsZero() {
		t.Errorf("the scope appended into the hook's array: %v", shared[:1])
	}
}

// TestScopeWithSharedDefaults is C1 of policy 2's final review: a Rewrite
// hook that sets every statement's Where to one shared slice of defaults,
// with spare capacity, must not let concurrent statements overwrite each
// other's tenant condition. Run it with -race.
func TestScopeWithSharedDefaults(t *testing.T) {
	defaults := make([]velox.Predicate, 1, 8)
	defaults[0] = velox.Predicate(I.Paid.EQ(true))
	setDefaults := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = defaults
		return nil
	})
	var wg sync.WaitGroup
	for g := range 8 {
		id := int64(100 + g)
		wg.Go(func() {
			var wrong int
			var last []any
			db := openFake(velox.Postgres, &fakeconn.Exec{}, setDefaults,
				velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
					if len(st.Args) != 3 || st.Args[1] != id || st.Tenant != id {
						wrong, last = wrong+1, st.Args
					}
					return next(ctx)
				}))
			ctx := tenant(id)
			for range 200 {
				if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
					t.Error(err)
					return
				}
			}
			if wrong > 0 {
				t.Errorf("tenant %d: %d of 200 statements carried another tenant's condition, last with args %v", id, wrong, last)
			}
		})
	}
	wg.Wait()
	if len(defaults) != 1 || !reflect.ValueOf(defaults[:2][1]).IsZero() {
		t.Errorf("the defaults changed: %v", defaults[:2])
	}
}

// TestZeroTenantIsRefused: WithTenant of the type's zero value, which
// middleware gets by parsing a missing claim, scopes nothing: every
// statement fails closed, inserts included.
func TestZeroTenantIsRefused(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}, Affected: 1}
	db := openFake(velox.Postgres, e)
	zero := tenant(int64(0))
	const msg = "velox: testmodel.Invoice: no tenant in the context: the tenant is the zero value"
	_, err := db.Query[testmodel.Invoice]().Limit(5).All(zero)
	want(t, err, velox.ErrNoTenant, msg)
	_, err = db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(zero)
	want(t, err, velox.ErrNoTenant, msg)
	_, err = db.Query[testmodel.Item]().Where(It.Invoice.Has()).Limit(5).All(zero)
	want(t, err, velox.ErrNoTenant, "velox: testmodel.Item: in the EXISTS on testmodel.Invoice: no tenant in the context: the tenant is the zero value")
	inv := testmodel.Invoice{Number: "A"}
	err = db.Insert(&inv).Exec(zero)
	want(t, err, velox.ErrNoTenant, msg)
	if inv.OrgID != 0 {
		t.Errorf("OrgID %d", inv.OrgID)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// The tconv entities hold tenant columns of other Go types than
// testmodel.Invoice's int64, for the conversion of WithTenant's id.
type (
	torg  int64  // a named integer tenant
	tuorg uint16 // a named unsigned tenant
	tslug string // a named string tenant
)

// tconvA has an int64 tenant and edges to tconvB, whose tenant is an int32,
// and to tconvW, whose tenant is a uint16.
type tconvA struct {
	ID    int64
	OrgID int64
	Bs    velox.Many[tconvB]
	Ws    velox.Many[tconvW]
}

type tconvW struct {
	ID    int64
	OrgID uint16
	AID   int64
}

// tconvR is unscoped, with edges to the scoped tconvB and tconvW.
type tconvR struct {
	ID int64
	Bs velox.Many[tconvB]
	Ws velox.Many[tconvW]
}

type tconvB struct {
	ID    int64
	OrgID int32
	AID   int64
}

type tconvU struct {
	ID    int64
	OrgID uint32
}

type tconvS struct {
	ID    int64
	OrgID tslug
}

type tconvN struct {
	ID    int64
	OrgID torg
}

type tconvNU struct {
	ID    int64
	OrgID tuorg
}

var (
	tconvAB = velox.Rel[tconvA, tconvB]{I: 0}
	tconvAW = velox.Rel[tconvA, tconvW]{I: 1}
	tconvRB = velox.Rel[tconvR, tconvB]{I: 0}
	tconvRW = velox.Rel[tconvR, tconvW]{I: 1}
)

var (
	tconvAMeta  velox.MetaOnce[tconvA]
	tconvBMeta  velox.MetaOnce[tconvB]
	tconvUMeta  velox.MetaOnce[tconvU]
	tconvSMeta  velox.MetaOnce[tconvS]
	tconvNMeta  velox.MetaOnce[tconvN]
	tconvNUMeta velox.MetaOnce[tconvNU]
	tconvWMeta  velox.MetaOnce[tconvW]
	tconvRMeta  velox.MetaOnce[tconvR]
)

// tconvDesc is the Desc of a tconv entity: the columns id and org_id, the
// tenant, then more.
func tconvDesc[E any](table string, addr func(*E, int) any, more ...velox.ColumnDesc) func() velox.Desc[E] {
	return func() velox.Desc[E] {
		return velox.Desc[E]{
			Table:      table,
			Columns:    append([]velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}}, more...),
			PrimaryKey: []int{0},
			Tenant:     1,
			Addr:       addr,
		}
	}
}

func (*tconvA) VeloxMeta() *velox.Meta[tconvA] {
	return tconvAMeta.Load(func() velox.Desc[tconvA] {
		d := tconvDesc("tconv_a", func(x *tconvA, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.OrgID
		})()
		d.Edges = []velox.EdgeDesc{velox.EdgeTo[tconvB]("Bs", 0, 2), velox.EdgeTo[tconvW]("Ws", 0, 2)}
		d.EdgeAddr = func(x *tconvA, i int) any {
			if i == 0 {
				return &x.Bs
			}
			return &x.Ws
		}
		return d
	})
}

func (*tconvW) VeloxMeta() *velox.Meta[tconvW] {
	return tconvWMeta.Load(tconvDesc("tconv_w", func(x *tconvW, i int) any {
		switch i {
		case 0:
			return &x.ID
		case 1:
			return &x.OrgID
		}
		return &x.AID
	}, velox.ColumnDesc{Field: "AID", Name: "a_id"}))
}

func (*tconvR) VeloxMeta() *velox.Meta[tconvR] {
	return tconvRMeta.Load(func() velox.Desc[tconvR] {
		return velox.Desc[tconvR]{
			Table:      "tconv_r",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Edges:      []velox.EdgeDesc{velox.EdgeTo[tconvB]("Bs", 0, 2), velox.EdgeTo[tconvW]("Ws", 0, 2)},
			Addr:       func(x *tconvR, _ int) any { return &x.ID },
			EdgeAddr: func(x *tconvR, i int) any {
				if i == 0 {
					return &x.Bs
				}
				return &x.Ws
			},
		}
	})
}

func (*tconvB) VeloxMeta() *velox.Meta[tconvB] {
	return tconvBMeta.Load(tconvDesc("tconv_b", func(x *tconvB, i int) any {
		switch i {
		case 0:
			return &x.ID
		case 1:
			return &x.OrgID
		}
		return &x.AID
	}, velox.ColumnDesc{Field: "AID", Name: "a_id"}))
}

func (*tconvU) VeloxMeta() *velox.Meta[tconvU] {
	return tconvUMeta.Load(tconvDesc("tconv_u", func(x *tconvU, i int) any {
		if i == 0 {
			return &x.ID
		}
		return &x.OrgID
	}))
}

func (*tconvS) VeloxMeta() *velox.Meta[tconvS] {
	return tconvSMeta.Load(tconvDesc("tconv_s", func(x *tconvS, i int) any {
		if i == 0 {
			return &x.ID
		}
		return &x.OrgID
	}))
}

func (*tconvN) VeloxMeta() *velox.Meta[tconvN] {
	return tconvNMeta.Load(tconvDesc("tconv_n", func(x *tconvN, i int) any {
		if i == 0 {
			return &x.ID
		}
		return &x.OrgID
	}))
}

func (*tconvNU) VeloxMeta() *velox.Meta[tconvNU] {
	return tconvNUMeta.Load(tconvDesc("tconv_nu", func(x *tconvNU, i int) any {
		if i == 0 {
			return &x.ID
		}
		return &x.OrgID
	}))
}

// TestTenantConversion: a predeclared integer converts into a tenant column
// of any integer kind whose range holds it, a string into one of string kind,
// predeclared or named; a named id never converts.
func TestTenantConversion(t *testing.T) {
	type orgID int64
	type otherSlug string
	e := &fakeconn.Exec{Rows: [][]any{{int64(3)}}}
	db := openFake(velox.Postgres, e)
	count := map[string]func(context.Context) error{
		"Invoice": func(ctx context.Context) error { _, err := db.Query[testmodel.Invoice]().Count(ctx); return err },
		"tconvN":  func(ctx context.Context) error { _, err := db.Query[tconvN]().Count(ctx); return err },
		"tconvNU": func(ctx context.Context) error { _, err := db.Query[tconvNU]().Count(ctx); return err },
		"tconvU":  func(ctx context.Context) error { _, err := db.Query[tconvU]().Count(ctx); return err },
		"tconvS":  func(ctx context.Context) error { _, err := db.Query[tconvS]().Count(ctx); return err },
	}
	for _, c := range []struct {
		entity string
		id     any
		sent   any    // the tenant sent; nil when the statement fails
		err    error  // the failure
		text   string // in the failure's text
	}{
		{"Invoice", 7, int64(7), nil, ""},
		{"Invoice", int32(7), int64(7), nil, ""},
		{"Invoice", uint8(7), int64(7), nil, ""},
		{"Invoice", uint64(7), int64(7), nil, ""},
		{"Invoice", int64(7), int64(7), nil, ""},
		{"Invoice", uint64(1 << 63), nil, velox.ErrTenantMismatch, "does not fit"},
		{"Invoice", 0, nil, velox.ErrNoTenant, ""},
		{"Invoice", int8(0), nil, velox.ErrNoTenant, ""},
		{"Invoice", "7", nil, velox.ErrTenantMismatch, ""},
		{"Invoice", orgID(7), nil, velox.ErrTenantMismatch, ""},
		{"tconvN", 7, torg(7), nil, ""},
		{"tconvN", uint8(7), torg(7), nil, ""},
		{"tconvN", orgID(7), nil, velox.ErrTenantMismatch, ""},
		{"tconvN", torg(7), torg(7), nil, ""},
		{"tconvNU", -1, nil, velox.ErrTenantMismatch, "does not fit"},
		{"tconvNU", 70000, nil, velox.ErrTenantMismatch, "does not fit"},
		{"tconvNU", 7, tuorg(7), nil, ""},
		{"tconvU", -1, nil, velox.ErrTenantMismatch, "does not fit"},
		{"tconvU", int64(1 << 40), nil, velox.ErrTenantMismatch, "does not fit"},
		{"tconvU", 7, uint32(7), nil, ""},
		{"tconvS", "acme", tslug("acme"), nil, ""},
		{"tconvS", otherSlug("acme"), nil, velox.ErrTenantMismatch, ""},
	} {
		name := fmt.Sprintf("%s, %T(%v)", c.entity, c.id, c.id)
		calls := e.Calls
		err := count[c.entity](tenant(c.id))
		if c.err == nil {
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if got := e.Args[len(e.Args)-1]; !reflect.DeepEqual(got, c.sent) {
				t.Errorf("%s: sent %#v (a %T), want %#v (a %T)", name, got, got, c.sent, c.sent)
			}
			continue
		}
		if !errors.Is(err, c.err) || !strings.Contains(err.Error(), c.text) {
			t.Errorf("%s: got %v, want %v with %q", name, err, c.err, c.text)
		}
		if e.Calls != calls {
			t.Errorf("%s: the refused statement was sent", name)
		}
	}

	// The scope records the converted tenant; an Around hook sees it, a
	// Rewrite hook runs before the scope.
	var seen any
	db = openFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		seen = st.Tenant
		return next(ctx)
	}))
	if _, err := db.Query[testmodel.Invoice]().Count(tenant(7)); err != nil {
		t.Fatal(err)
	}
	if seen != int64(7) {
		t.Errorf("the Around hook saw the tenant %#v (a %T), want int64(7)", seen, seen)
	}
}

// TestTenantInsertConverts: an insert fills and checks the converted tenant.
func TestTenantInsertConverts(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{Number: "INV-1"}
	if err := db.Insert(&inv).Exec(tenant(7)); err != nil || inv.OrgID != 7 {
		t.Errorf("a zero tenant: %v, OrgID %d", err, inv.OrgID)
	}
	inv = testmodel.Invoice{Number: "INV-1", OrgID: 7}
	if err := db.Insert(&inv).Exec(tenant(7)); err != nil {
		t.Errorf("the tenant: %v", err)
	}
	inv = testmodel.Invoice{Number: "INV-1", OrgID: 8}
	if err := db.Insert(&inv).Exec(tenant(7)); !errors.Is(err, velox.ErrTenantMismatch) {
		t.Errorf("another tenant: %v", err)
	}
	xs := []testmodel.Invoice{{Number: "INV-1"}, {Number: "INV-2", OrgID: 7}}
	e.Rows = [][]any{{int64(5)}, {int64(6)}}
	if err := db.InsertAll(xs).Exec(tenant(7)); err != nil || xs[0].OrgID != 7 {
		t.Errorf("InsertAll: %v, OrgID %d", err, xs[0].OrgID)
	}
}

// TestTenantConversionPerTarget is Review Focus 1: each entity of a statement
// gets the tenant as its own column's type.
func TestTenantConversionPerTarget(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(3)}}}
	db := openFake(velox.Postgres, e)
	if _, err := db.Query[tconvA]().Where(tconvAB.Has()).Count(tenant(7)); err != nil {
		t.Fatal(err)
	}
	if want := []any{int32(7), int64(7)}; !reflect.DeepEqual(e.Args, want) {
		t.Errorf("sent %#v, want %#v; SQL %s", e.Args, want, e.SQL)
	}
}

// TestRowScopeKeepsTheRawTenant: RowScope is the context's, not any column's.
func TestRowScopeKeepsTheRawTenant(t *testing.T) {
	if got := velox.RowScopeFromContext(velox.WithTenant(context.Background(), 7)).Tenant; got != 7 {
		t.Errorf("Tenant %#v (a %T), want int(7)", got, got)
	}
}

// TestTenantConversionIsolation: a conversion that fails anywhere in a
// statement refuses it before it is sent, and a converted tenant guards
// assignments and upserts as the exact type does.
func TestTenantConversionIsolation(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(3)}}, Affected: 1}
	db := openFake(velox.Postgres, e)

	// 70000 fits tconvA's int64, not the uint16 of the EXISTS's target.
	_, err := db.Query[tconvA]().Where(tconvAW.Has()).Count(tenant(70000))
	if !errors.Is(err, velox.ErrTenantMismatch) || !strings.Contains(err.Error(), "in the EXISTS on apitest.tconvW") || !strings.Contains(err.Error(), "does not fit") {
		t.Errorf("an EXISTS whose tenant does not fit: %v", err)
	}
	for name, id := range map[string]any{"a typed nil": (*int64)(nil), "an *int64": new(int64(7))} {
		_, err := db.Query[testmodel.Invoice]().Count(tenant(id))
		if !errors.Is(err, velox.ErrTenantMismatch) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if e.Calls != 0 {
		t.Fatalf("%d refused statements sent", e.Calls)
	}

	upd := func(v int64) error {
		_, err := db.Update[testmodel.Invoice]().Set(I.OrgID.Set(v)).Where(I.ID.EQ(1)).Exec(tenant(7))
		return err
	}
	if err := upd(7); err != nil {
		t.Errorf("Set(7) under the int 7: %v", err)
	}
	sent := e.Calls
	if err := upd(8); !errors.Is(err, velox.ErrTenantImmutable) {
		t.Errorf("Set(8) under the int 7: %v", err)
	}
	if e.Calls != sent {
		t.Errorf("Set(8) under the int 7 was sent")
	}

	e.Rows = [][]any{{int64(5)}}
	inv := testmodel.Invoice{Number: "INV-1"}
	if err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).Update(I.Paid).Exec(tenant(7)); err != nil || inv.OrgID != 7 {
		t.Errorf("a converted upsert: %v, OrgID %d", err, inv.OrgID)
	} else if !slices.Contains(e.Args, any(int64(7))) {
		t.Errorf("the upsert sent %#v, without int64(7)", e.Args)
	}
	calls := e.Calls
	inv = testmodel.Invoice{Number: "INV-1"}
	if err := db.Insert(&inv).OnConflict(I.Number).Update(I.Paid).Exec(tenant(7)); !errors.Is(err, velox.ErrTenantUpsert) {
		t.Errorf("a converted upsert on (number): %v", err)
	}
	inv = testmodel.Invoice{Number: "INV-1", OrgID: 8}
	if err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).Update(I.Paid).Exec(tenant(7)); !errors.Is(err, velox.ErrTenantMismatch) {
		t.Errorf("a converted upsert of another tenant's row: %v", err)
	}
	if e.Calls != calls {
		t.Errorf("%d refused upserts sent", e.Calls-calls)
	}
}

// TestTenantUnscopedEntityRecordsTheGivenTenant: a statement on an unscoped
// entity that reads scoped targets of differently typed tenant columns shows
// hooks the tenant as WithTenant was given it, and each target gets its own
// column's type.
func TestTenantUnscopedEntityRecordsTheGivenTenant(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(3)}}}
	var seen any
	db := openFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		seen = st.Tenant
		return next(ctx)
	}))
	if _, err := db.Query[tconvR]().Where(tconvRB.Has(), tconvRW.Has()).Count(tenant(7)); err != nil {
		t.Fatal(err)
	}
	if seen != 7 {
		t.Errorf("the Around hook saw the tenant %#v (a %T), want int(7)", seen, seen)
	}
	if want := []any{int32(7), uint16(7)}; !reflect.DeepEqual(e.Args, want) {
		t.Errorf("sent %#v, want %#v", e.Args, want)
	}
}
