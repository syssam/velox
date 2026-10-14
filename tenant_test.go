package velox_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

var Tk = testmodel.Tickets

const ticketCols = `"created_by", "id", "org_id", "owner", "title", "done", "invoice_id", "parent_id"`

// tenant returns a context scoped to tenant id, with the policy skipped: the
// tenant tests check the scope alone.
func tenant(id any) context.Context {
	return velox.WithTenant(velox.SkipPolicy(context.Background(), "tenant tests"), id)
}

func ticketRow(id, org int64, parent any) []any {
	return []any{"ann", id, org, "ann", fmt.Sprintf("T-%d", id), false, int64(1), parent}
}

func wantSent(t *testing.T, e *velox.FakeExec, sql string, args ...any) {
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
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5).All(velox.WithTenant(bg, int64(7))); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+allCols+` FROM "invoices" WHERE "paid" = $1 AND "org_id" = $2 LIMIT $3`, true, int64(7), 5)
}

func TestTenantPredicateOnEveryOperation(t *testing.T) {
	t7 := velox.WithTenant(context.Background(), int64(7))
	e := &velox.FakeExec{Rows: [][]any{{int64(3)}}, Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)

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
	if err := db.Save(t7, &inv, I.Number); err != nil {
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
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
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

// TestNoTenantFailsEveryOperation: with neither WithTenant nor AllTenants,
// every operation on a scoped entity fails, on both paths, and sends nothing.
func TestNoTenantFailsEveryOperation(t *testing.T) {
	bg := context.Background()
	around := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		return next(ctx)
	})
	for _, opts := range [][]velox.Option{nil, {around}} {
		e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}, Affected: 1}
		db := velox.OpenFake(velox.Postgres, e, opts...)
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
		saveErr := db.Save(bg, &inv, I.Number)
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
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		mu.Lock()
		seen = append(seen, *st)
		mu.Unlock()
		return next(ctx)
	}))
	all := velox.AllTenants(context.Background(), "reason")
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
		t.Errorf("AllTenants: Tenant %v, TenantBypass %q", seen[0].Tenant, seen[0].TenantBypass)
	}
	if seen[1].Tenant != int64(7) || seen[1].TenantBypass != "" {
		t.Errorf("WithTenant: Tenant %v, TenantBypass %q", seen[1].Tenant, seen[1].TenantBypass)
	}
	if seen[2].Tenant != nil || seen[2].TenantBypass != "" {
		t.Errorf("an unscoped entity: Tenant %v, TenantBypass %q", seen[2].Tenant, seen[2].TenantBypass)
	}
}

// TestTenantTypeMustMatch is Review Focus 2: an untyped constant is an int,
// not the column's int64.
func TestTenantTypeMustMatch(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e)
	_, err := db.Query[testmodel.Invoice]().Limit(5).All(velox.WithTenant(context.Background(), 7))
	if !errors.Is(err, velox.ErrTenantMismatch) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: tenant does not match") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "int64") || !strings.Contains(err.Error(), "int") {
		t.Errorf("the error names neither type: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

func TestTenantColumnAssignments(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
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
	if err := db.Save(t7, &inv, I.OrgID); !errors.Is(err, velox.ErrTenantImmutable) {
		t.Errorf("Save of another tenant: %v", err)
	}
	if e.Calls != calls {
		t.Errorf("%d refused statements sent", e.Calls-calls)
	}
	inv.OrgID = 7
	if err := db.Save(t7, &inv, I.OrgID); err != nil {
		t.Errorf("Save of the tenant: %v", err)
	}
	all := velox.AllTenants(context.Background(), "move an invoice")
	if _, err := upd(I.OrgID.Set(8)).Exec(all); err != nil {
		t.Errorf("AllTenants, Set(8): %v", err)
	}
	if _, err := upd(I.OrgID.Add(1)).Exec(all); err != nil {
		t.Errorf("AllTenants, Add(1): %v", err)
	}
}

func TestHasOnAScopedTarget(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
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
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
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
	if _, err := q.All(velox.AllTenants(context.Background(), "reason")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SQL, "org_id") || seen.TenantBypass != "reason" {
		t.Errorf("AllTenants: %s, TenantBypass %q", e.SQL, seen.TenantBypass)
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
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
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

	e0 := &velox.FakeExec{}
	db0 := velox.OpenFake(velox.Postgres, e0)
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

	var wg sync.WaitGroup
	for i := range 50 {
		id := int64(i + 100)
		wg.Go(func() {
			e := &velox.FakeExec{}
			db := velox.OpenFake(velox.Postgres, e)
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
	}
	wg.Wait()

	if sql, args := sqlOK(t)(base.SQL(tenant(int64(1)))); sql != sql0 || !reflect.DeepEqual(args, args0) {
		t.Errorf("the base query's SQL changed:\n%s %v\n%s %v", sql0, args0, sql, args)
	}
	if after, afterArgs := run0(); after != before || !reflect.DeepEqual(afterArgs, beforeArgs) {
		t.Errorf("the base query sends another statement:\n%s %v\n%s %v", before, beforeArgs, after, afterArgs)
	}
}

func TestWithLoadIsScoped(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{ticketRow(2, 7, int64(1))},
		{ticketRow(1, 7, nil)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
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
	e = &velox.FakeExec{Results: [][][]any{{{int64(1), int64(1), "SKU", int64(2)}}, {invoiceRow(1)}}}
	db = velox.OpenFake(velox.Postgres, e)
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
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
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
			db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, setDefaults,
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
	e := &velox.FakeExec{Rows: [][]any{{int64(5)}}, Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
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
