package apitest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/fakeconn"
	"github.com/syssam/velox2/internal/testmodel"
)

var (
	ctx = velox.WithAllTenants(context.Background(), "velox tests")
	t0  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	I   = testmodel.Invoices
	It  = testmodel.Items
)

const allCols = `"id", "created_at", "updated_at", "org_id", "number", "total", "paid", "paid_at", "note", "data"`

func invoiceRow(id int64) []any {
	return []any{id, t0, t0, int64(1), fmt.Sprintf("INV-%d", id), 12.5, true, t0, "", []byte("d")}
}

func TestAllScansEveryColumn(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1), invoiceRow(2)}}
	db := openFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).OrderBy(I.ID.Asc()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != 2 || got[1].Number != "INV-2" || got[1].PaidAt == nil ||
		!got[1].PaidAt.Equal(t0) || got[1].Note != "" || string(got[1].Data) != "d" || !got[1].CreatedAt.Equal(t0) {
		t.Fatalf("%+v", got)
	}
	want := `SELECT ` + allCols + ` FROM "invoices" WHERE "org_id" = $1 ORDER BY "id" LIMIT $2`
	if e.SQL != want {
		t.Errorf("SQL\n got %s\nwant %s", e.SQL, want)
	}
	if !reflect.DeepEqual(e.Args, []any{int64(1), 10}) {
		t.Errorf("args %#v", e.Args)
	}
}

func TestOne(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(3)}}
	db := openFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(3)).One(ctx)
	if err != nil || got.ID != 3 {
		t.Fatalf("%+v, %v", got, err)
	}
	if !strings.HasSuffix(e.SQL, `WHERE "id" = $1 LIMIT $2`) || e.Args[1] != 2 {
		t.Errorf("One must ask for 2 rows: %s %v", e.SQL, e.Args)
	}
	e.Rows = nil
	if _, err := db.Query[testmodel.Invoice]().One(ctx); !errors.Is(err, velox.ErrNotFound) || !strings.Contains(err.Error(), "testmodel.Invoice") {
		t.Errorf("no rows: %v", err)
	}
	e.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
	if _, err := db.Query[testmodel.Invoice]().One(ctx); !errors.Is(err, velox.ErrNotSingular) {
		t.Errorf("two rows: %v", err)
	}
}

func TestColumnsAndOmit(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(5), "INV-5"}}}
	db := openFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Columns(I.ID, I.Number).Unbounded().All(ctx)
	if err != nil || len(got) != 1 || got[0].ID != 5 || got[0].Number != "INV-5" || got[0].Total != 0 {
		t.Fatalf("%+v, %v", got, err)
	}
	if want := `SELECT "id", "number" FROM "invoices"`; e.SQL != want {
		t.Errorf("Columns SQL %s", e.SQL)
	}
	row := invoiceRow(6)
	e.Rows = [][]any{append(row[:8:8], row[9])}
	got, err = db.Query[testmodel.Invoice]().Omit(I.Note).Unbounded().All(ctx)
	if err != nil || got[0].ID != 6 || got[0].Note != "" {
		t.Fatalf("%+v, %v", got, err)
	}
	if want := `SELECT "id", "created_at", "updated_at", "org_id", "number", "total", "paid", "paid_at", "data" FROM "invoices"`; e.SQL != want {
		t.Errorf("Omit SQL %s", e.SQL)
	}
}

func TestCount(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(3)}}}
	db := openFake(velox.Postgres, e)
	n, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).Limit(5).Count(ctx)
	if err != nil || n != 3 {
		t.Fatalf("%d, %v", n, err)
	}
	if want := `SELECT COUNT(*) FROM "invoices" WHERE "paid" = $1`; e.SQL != want {
		t.Errorf("SQL %s", e.SQL)
	}
}

func TestIterStopsEarly(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3)}}
	db := openFake(velox.Postgres, e)
	var seen []int64
	for inv, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, inv.ID)
		if len(seen) == 2 {
			break
		}
	}
	if !reflect.DeepEqual(seen, []int64{1, 2}) {
		t.Errorf("%v", seen)
	}
}

// TestIterBreakClosesTheRows: leaving an Iter loop early must close the rows,
// or the statement's connection is never returned to the pool. It covers both
// execution paths (no hooks, and an Around hook that wraps the loop body).
func TestIterBreakClosesTheRows(t *testing.T) {
	for name, opts := range map[string][]velox.Option{
		"no hooks": nil,
		"with Around": {velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
			return next(ctx)
		})},
	} {
		e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3)}}
		db := openFake(velox.Postgres, e, opts...)
		seen := 0
		for _, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			seen++
			break // on the first row, with two more unread
		}
		if seen != 1 || e.Calls != 1 || e.Closed != 1 {
			t.Errorf("%s: saw %d rows, %d queries, %d closed; want 1, 1, 1", name, seen, e.Calls, e.Closed)
		}
	}
}

func TestSQLNeedsNoDatabase(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.MySQL, e)
	text, args := sqlOK(t)(db.Query[testmodel.Invoice]().Where(I.Number.HasPrefix("A")).Limit(3).SQL(ctx))
	want := "SELECT `id`, `created_at`, `updated_at`, `org_id`, `number`, `total`, `paid`, `paid_at`, `note`, `data` FROM `invoices` WHERE `number` LIKE ? ESCAPE '!' LIMIT ?"
	if text != want || !reflect.DeepEqual(args, []any{"A%", 3}) || e.Calls != 0 {
		t.Errorf("%s %v calls %d", text, args, e.Calls)
	}
}

// TestQueryCanRunAgain is Review Focus 4.
func TestQueryCanRunAgain(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	tenant := func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.ColumnEQ(st.Entity.Tenant, int64(7)))
		return nil
	}
	db := openFake(velox.Postgres, e, velox.WithRewrite(tenant))
	q := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5)
	want := `SELECT ` + allCols + ` FROM "invoices" WHERE "paid" = $1 AND "org_id" = $2 LIMIT $3`
	for run := range 3 {
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		if e.SQL != want || !reflect.DeepEqual(e.Args, []any{true, int64(7), 5}) {
			t.Fatalf("run %d:\n got %s %v\nwant %s", run, e.SQL, e.Args, want)
		}
	}
	// SQL(ctx) runs the hook too, and shows its condition once.
	if text, _ := sqlOK(t)(q.SQL(ctx)); text != want {
		t.Errorf("SQL(ctx):\n got %s\nwant %s", text, want)
	}
}

// TestChainingCopiesTheQuery: extending a query returns a new one and leaves
// the query extended as it was, so one base query can be extended many ways.
func TestChainingCopiesTheQuery(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	base := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).OrderBy(I.ID.Asc()).Unbounded()
	baseSQL, baseArgs := sqlOK(t)(base.SQL(ctx))

	paid := base.Where(I.Paid.EQ(true))
	big := base.Where(I.Total.GT(100))
	if text, args := sqlOK(t)(paid.SQL(ctx)); !strings.HasSuffix(text, `WHERE "org_id" = $1 AND "paid" = $2 ORDER BY "id"`) || !reflect.DeepEqual(args, []any{int64(1), true}) {
		t.Errorf("first branch: %s %v", text, args)
	}
	if text, args := sqlOK(t)(big.SQL(ctx)); !strings.HasSuffix(text, `WHERE "org_id" = $1 AND "total" > $2 ORDER BY "id"`) || !reflect.DeepEqual(args, []any{int64(1), float64(100)}) {
		t.Errorf("second branch: %s %v", text, args)
	}

	byTotal := base.OrderBy(I.Total.Desc())
	byNumber := base.OrderBy(I.Number.Asc())
	if text, _ := sqlOK(t)(byTotal.SQL(ctx)); !strings.HasSuffix(text, `ORDER BY "id", "total" DESC`) {
		t.Errorf("first ordering: %s", text)
	}
	if text, _ := sqlOK(t)(byNumber.SQL(ctx)); !strings.HasSuffix(text, `ORDER BY "id", "number"`) {
		t.Errorf("second ordering: %s", text)
	}

	// Three conditions and three orderings leave the base's slices with room
	// for a fourth, which the two branches must not both write to.
	roomy := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1), I.Paid.EQ(true), I.Total.GT(1)).
		OrderBy(I.ID.Asc(), I.Total.Desc(), I.Number.Asc()).Unbounded()
	low, high := roomy.Where(I.ID.LT(10)), roomy.Where(I.ID.GT(20))
	if text, _ := sqlOK(t)(low.SQL(ctx)); !strings.Contains(text, `AND "id" < $4 ORDER`) {
		t.Errorf("first branch of a base with room: %s", text)
	}
	if text, _ := sqlOK(t)(high.SQL(ctx)); !strings.Contains(text, `AND "id" > $4 ORDER`) {
		t.Errorf("second branch of a base with room: %s", text)
	}
	byPaid, byOrg := roomy.OrderBy(I.Paid.Asc()), roomy.OrderBy(I.OrgID.Asc())
	if text, _ := sqlOK(t)(byPaid.SQL(ctx)); !strings.HasSuffix(text, `ORDER BY "id", "total" DESC, "number", "paid"`) {
		t.Errorf("first ordering of a base with room: %s", text)
	}
	if text, _ := sqlOK(t)(byOrg.SQL(ctx)); !strings.HasSuffix(text, `ORDER BY "id", "total" DESC, "number", "org_id"`) {
		t.Errorf("second ordering of a base with room: %s", text)
	}

	base.Limit(5).Offset(2).Unbounded().Columns(I.ID).Omit(I.Note)
	if text, args := sqlOK(t)(base.SQL(ctx)); text != baseSQL || !reflect.DeepEqual(args, baseArgs) {
		t.Errorf("the base query changed:\n got %s %v\nwant %s %v", text, args, baseSQL, baseArgs)
	}
}

// TestBaseQueryIsSafeToExtendConcurrently; run it with -race.
func TestBaseQueryIsSafeToExtendConcurrently(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	base := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1))
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 100 {
				id := int64(g*1000 + i)
				q := base.Where(I.ID.EQ(id)).OrderBy(I.ID.Desc())
				text, args, err := q.Unbounded().SQL(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if want := `WHERE "org_id" = $1 AND "id" = $2 ORDER BY "id" DESC`; !strings.HasSuffix(text, want) || !reflect.DeepEqual(args, []any{int64(1), id}) {
					t.Errorf("goroutine %d: %s %v", g, text, args)
					return
				}
				if _, err := q.Unbounded().All(ctx); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}

// TestBrokenMetadataIsAnErrorOnEveryUse: a Desc that panics, or
// generated code with no scan plan, makes every query on the entity return a
// *SchemaError naming it, not just the first, and none panics.
func TestBrokenMetadataIsAnErrorOnEveryUse(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}}
	db := openFake(velox.Postgres, e)
	check := func(name, entity, reason string, run func() error) {
		t.Helper()
		for i := range 2 {
			var err error
			panicked := true
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Errorf("%s, query %d panicked: %v", name, i+1, p)
					}
				}()
				err = run()
				panicked = false
			}()
			if panicked {
				continue
			}
			var se *velox.SchemaError
			if !errors.As(err, &se) || se.Entity != entity || !strings.Contains(se.Reason, reason) || !strings.HasPrefix(err.Error(), "velox: ") {
				t.Errorf("%s, query %d: %v", name, i+1, err)
			}
		}
	}
	check("Desc panics", "apitest.panickyEntity", "the generated metadata panicked: no schema today", func() error {
		_, err := db.Query[panickyEntity]().All(ctx)
		return err
	})
	check("no Addr", "apitest.addrlessEntity", "Addr", func() error {
		_, err := db.Query[addrlessEntity]().All(ctx)
		return err
	})
	if e.Calls != 0 {
		t.Errorf("%d queries reached the database", e.Calls)
	}
}

func TestHasThroughTheAPI(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	text, args := sqlOK(t)(db.Query[testmodel.Invoice]().Where(I.Items.HasWhere(It.Qty.GT(2)), velox.Not(I.Items.Has())).Limit(5).SQL(ctx))
	want := `SELECT ` + allCols + ` FROM "invoices" WHERE EXISTS (SELECT 1 FROM "items" AS "vx1" WHERE "vx1"."invoice_id" = "invoices"."id" AND "qty" > $1) AND NOT (EXISTS (SELECT 1 FROM "items" AS "vx1" WHERE "vx1"."invoice_id" = "invoices"."id")) LIMIT $2`
	if text != want || len(args) != 2 {
		t.Errorf("\n got %s\nwant %s", text, want)
	}
	if _, err := db.Delete[testmodel.Invoice]().Where(I.Items.Has()).Exec(ctx); err != nil || !strings.Contains(e.SQL, `DELETE FROM "invoices" WHERE EXISTS`) {
		t.Errorf("%v %s", err, e.SQL)
	}
}

func TestABrokenRelationIsAnError(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	bad := velox.Rel[testmodel.Invoice, testmodel.Item]{I: 9}
	badItem := velox.Rel[testmodel.Item, testmodel.Invoice]{I: 9}
	const msg = ": has no relation 9"
	check := func(name string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v, want an error containing %q", name, err, msg)
		}
	}
	_, err := db.Query[testmodel.Invoice]().With(bad).Limit(1).All(ctx)
	if want := "velox: testmodel.Invoice: has no relation 9; run go tool velox-gen"; err == nil || err.Error() != want {
		t.Errorf("With: %v, want %q", err, want)
	}
	for name, p := range map[string]velox.Pred[testmodel.Invoice]{
		"direct":            bad.Has(),
		"inside Not":        velox.Not(bad.Has()),
		"inside HasWhere":   I.Items.HasWhere(badItem.Has()),
		"inside And and Or": velox.Or(I.Paid.EQ(true), velox.And(bad.Has())),
	} {
		q := db.Query[testmodel.Invoice]().Where(p).Limit(1)
		_, err := q.All(ctx)
		check(name+" All", err)
		_, err = q.One(ctx)
		check(name+" One", err)
		_, err = q.Count(ctx)
		check(name+" Count", err)
		for _, err := range q.Iter(ctx) {
			check(name+" Iter", err)
		}
		text, args, err := q.SQL(ctx)
		check(name+" Query.SQL", err)
		if text != "" || args != nil {
			t.Errorf("%s Query.SQL: %q %v", name, text, args)
		}
		u := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(p)
		_, err = u.Exec(ctx)
		check(name+" Update", err)
		text, _, err = u.SQL(ctx)
		check(name+" Update.SQL", err)
		if text != "" {
			t.Errorf("%s Update.SQL: %q", name, text)
		}
		d := db.Delete[testmodel.Invoice]().Where(p)
		_, err = d.Exec(ctx)
		check(name+" Delete", err)
		text, _, err = d.SQL(ctx)
		check(name+" Delete.SQL", err)
		if text != "" {
			t.Errorf("%s Delete.SQL: %q", name, text)
		}
	}
}

func TestQueryOrdered(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	q := db.Query[testmodel.Invoice]()
	if q.Ordered() {
		t.Error("a new query is ordered")
	}
	if q.Where(I.OrgID.EQ(1)).Limit(1).Ordered() {
		t.Error("a query with no OrderBy is ordered")
	}
	o := q.OrderBy(I.ID.Asc())
	if !o.Ordered() || !o.Limit(1).Ordered() {
		t.Error("a query with an OrderBy is not ordered")
	}
	if q.Ordered() {
		t.Error("OrderBy changed the query it was called on")
	}
	if q.OrderBy().Ordered() {
		t.Error("OrderBy with no orderings makes the query ordered")
	}
}

func TestFirst(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(5)}}
	db := openFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Desc()).First(ctx)
	if err != nil || got == nil || got.ID != 5 {
		t.Fatalf("%+v, %v", got, err)
	}
	if want := `ORDER BY "id" DESC LIMIT $2`; !strings.HasSuffix(e.SQL, want) {
		t.Errorf("SQL %s", e.SQL)
	}
	if want := []any{true, 1}; !reflect.DeepEqual(e.Args, want) {
		t.Errorf("args %v, want %v", e.Args, want)
	}
	e.Rows = nil
	_, err = db.Query[testmodel.Invoice]().First(ctx)
	if !errors.Is(err, velox.ErrNotFound) || err.Error() != "velox: testmodel.Invoice: not found" {
		t.Errorf("no rows: %v", err)
	}
	// With loads the relations of the row it found.
	le := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 1)}}}
	ldb := openFake(velox.Postgres, le)
	inv, err := ldb.Query[testmodel.Invoice]().With(I.Items).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items, loaded := inv.Items.Get(); !loaded || len(items) != 1 || len(le.Log) != 2 {
		t.Errorf("items %v loaded %v, %d statements", items, loaded, len(le.Log))
	}
}

func TestExists(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}}
	db := openFake(velox.Postgres, e)
	q := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).Limit(50).Offset(5).Columns(I.Number)
	ok, err := q.Exists(ctx)
	if err != nil || !ok {
		t.Fatalf("%v, %v", ok, err)
	}
	if want := `SELECT 1 FROM "invoices" WHERE "paid" = $1 LIMIT $2`; e.SQL != want {
		t.Errorf("SQL %s", e.SQL)
	}
	if want := []any{true, 1}; !reflect.DeepEqual(e.Args, want) {
		t.Errorf("args %v, want %v", e.Args, want)
	}
	e.Rows = nil
	if ok, err := q.Exists(ctx); err != nil || ok {
		t.Errorf("no rows: %v, %v", ok, err)
	}
	// No Limit needed, as for Count.
	if _, err := db.Query[testmodel.Invoice]().Exists(ctx); err != nil {
		t.Errorf("unbounded Exists: %v", err)
	}
	// After's rules apply, as for Count.
	after := &testmodel.Invoice{ID: 3}
	if _, err := db.Query[testmodel.Invoice]().After(after).Exists(ctx); err == nil || !strings.Contains(err.Error(), "After needs an OrderBy") {
		t.Errorf("unordered After: %v", err)
	}
}

func TestRewriteCannotChangeExists(t *testing.T) {
	flip := func(_ context.Context, st *velox.Statement) error { st.Exists = !st.Exists; return nil }
	for name, run := range map[string]func(db *velox.DB) error{
		"set on All": func(db *velox.DB) error {
			_, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx)
			return err
		},
		"cleared on Exists": func(db *velox.DB) error {
			_, err := db.Query[testmodel.Invoice]().Exists(ctx)
			return err
		},
	} {
		e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
		err := run(openFake(velox.Postgres, e, velox.WithRewrite(flip)))
		if err == nil || err.Error() != "velox: testmodel.Invoice: a Rewrite hook changed the statement's Exists" || e.Calls != 0 {
			t.Errorf("%s: %v, %d sent", name, err, e.Calls)
		}
	}
}

// A Rewrite hook may not set or clear Count: it renders before Exists and an
// aggregate, so a flip would make Exists true on no row, an aggregate return
// a count, and a Count return a column; on a locking read it would skip the
// guard's Count refusal.
func TestRewriteCannotChangeCount(t *testing.T) {
	const want = "velox: testmodel.Invoice: a Rewrite hook changed the statement's Count"
	set := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { st.Count = true; return nil })
	clearAgg := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Count, st.Columns = false, []int{0}
		return nil
	})
	for name, c := range map[string]struct {
		opt velox.Option
		run func(db *velox.DB) error
	}{
		"set on All": {set, func(db *velox.DB) error {
			_, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx)
			return err
		}},
		"set on Exists": {set, func(db *velox.DB) error {
			_, err := db.Query[testmodel.Invoice]().Exists(ctx)
			return err
		}},
		"set on Sum": {set, func(db *velox.DB) error {
			_, err := velox.Sum(ctx, db.Query[testmodel.Invoice](), I.Total)
			return err
		}},
		"set on Min": {set, func(db *velox.DB) error {
			_, _, err := velox.Min(ctx, db.Query[testmodel.Invoice](), I.Total)
			return err
		}},
		"set on Max": {set, func(db *velox.DB) error {
			_, _, err := velox.Max(ctx, db.Query[testmodel.Invoice](), I.Total)
			return err
		}},
		"cleared on Count, with Columns set": {clearAgg, func(db *velox.DB) error {
			_, err := db.Query[testmodel.Invoice]().Count(ctx)
			return err
		}},
		"set on a locking read": {set, func(db *velox.DB) error {
			var err error
			_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				_, err = tx.Query[testmodel.Invoice]().Limit(1).ForUpdate().All(ctx)
				return err
			})
			return err
		}},
	} {
		e := &fakeconn.Exec{Rows: [][]any{{int64(42)}}}
		err := c.run(openFake(velox.Postgres, e, c.opt))
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v", name, err)
		}
		if s := sentStatements(e); len(s) != 0 {
			t.Errorf("%s: sent %q", name, s)
		}
	}
}

// TestAroundSeesExists: an Around hook sees Statement.Exists on an Exists
// query, and not on All.
func TestAroundSeesExists(t *testing.T) {
	var seen []bool
	db := openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		seen = append(seen, st.Exists)
		return next(ctx)
	}))
	if _, err := db.Query[testmodel.Invoice]().Exists(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []bool{true, false}) {
		t.Errorf("Exists seen %v, want [true false]", seen)
	}
}
