package velox_test

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
	"github.com/syssam/velox2/internal/testmodel"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	I   = testmodel.Invoices
)

const allCols = `"created_at", "updated_at", "id", "org_id", "number", "total", "paid", "paid_at", "note", "data"`

func invoiceRow(id int64) []any {
	return []any{t0, t0, id, int64(1), fmt.Sprintf("INV-%d", id), 12.5, true, t0, nil, []byte("d")}
}

func TestAllScansEveryColumn(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1), invoiceRow(2)}}
	db := velox.OpenFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).OrderBy(I.ID.Asc()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != 2 || got[1].Number != "INV-2" || got[1].PaidAt == nil ||
		!got[1].PaidAt.Equal(t0) || got[1].Note != nil || string(got[1].Data) != "d" || !got[1].CreatedAt.Equal(t0) {
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
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(3)}}
	db := velox.OpenFake(velox.Postgres, e)
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
	e := &velox.FakeExec{Rows: [][]any{{int64(5), "INV-5"}}}
	db := velox.OpenFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Columns(I.ID, I.Number).All(ctx)
	if err != nil || len(got) != 1 || got[0].ID != 5 || got[0].Number != "INV-5" || got[0].Total != 0 {
		t.Fatalf("%+v, %v", got, err)
	}
	if want := `SELECT "id", "number" FROM "invoices"`; e.SQL != want {
		t.Errorf("Columns SQL %s", e.SQL)
	}
	row := invoiceRow(6)
	e.Rows = [][]any{append(row[:8:8], row[9])}
	got, err = db.Query[testmodel.Invoice]().Omit(I.Note).All(ctx)
	if err != nil || got[0].ID != 6 || got[0].Note != nil {
		t.Fatalf("%+v, %v", got, err)
	}
	if want := `SELECT "created_at", "updated_at", "id", "org_id", "number", "total", "paid", "paid_at", "data" FROM "invoices"`; e.SQL != want {
		t.Errorf("Omit SQL %s", e.SQL)
	}
}

func TestCount(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(3)}}}
	db := velox.OpenFake(velox.Postgres, e)
	n, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).Limit(5).Count(ctx)
	if err != nil || n != 3 {
		t.Fatalf("%d, %v", n, err)
	}
	if want := `SELECT COUNT(*) FROM "invoices" WHERE "paid" = $1`; e.SQL != want {
		t.Errorf("SQL %s", e.SQL)
	}
}

func TestIterStopsEarly(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3)}}
	db := velox.OpenFake(velox.Postgres, e)
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

func TestSQLNeedsNoDatabase(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.MySQL, e)
	text, args := db.Query[testmodel.Invoice]().Where(I.Number.HasPrefix("A")).Limit(3).SQL()
	want := "SELECT `created_at`, `updated_at`, `id`, `org_id`, `number`, `total`, `paid`, `paid_at`, `note`, `data` FROM `invoices` WHERE `number` LIKE ? ESCAPE '\\\\' LIMIT ?"
	if text != want || !reflect.DeepEqual(args, []any{"A%", 3}) || e.Calls != 0 {
		t.Errorf("%s %v calls %d", text, args, e.Calls)
	}
}

// TestQueryCanRunAgain is Review Focus 4.
func TestQueryCanRunAgain(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	tenant := func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.ColumnEQ(st.Entity.Tenant, int64(7)))
		return nil
	}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(tenant))
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
	if text, _ := q.SQL(); strings.Contains(text, `"org_id" =`) {
		t.Errorf("the rewrite hook changed the query itself: %s", text)
	}
}

type staleEntity struct {
	ID   int64
	Name string
}

func (*staleEntity) Schema(*velox.Schema) {}

var staleMeta velox.MetaOnce[staleEntity]

func (*staleEntity) VeloxMeta() *velox.Meta[staleEntity] {
	return staleMeta.Get(func() velox.Shape[staleEntity] {
		return velox.Shape[staleEntity]{Columns: []string{"ID"}}
	})
}

func TestStaleGeneratedCodeIsReportedOnUse(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	_, err := db.Query[staleEntity]().All(ctx)
	if !errors.Is(err, velox.ErrStale) || !strings.Contains(err.Error(), "velox_test.staleEntity") || e.Calls != 0 {
		t.Fatalf("%v, calls %d", err, e.Calls)
	}
	if text, _ := db.Query[staleEntity]().SQL(); text != "" {
		t.Errorf("SQL of an invalid entity: %q", text)
	}
}

// TestChainingCopiesTheQuery: extending a query returns a new one and leaves
// the query extended as it was, so one base query can be extended many ways.
func TestChainingCopiesTheQuery(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	base := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).OrderBy(I.ID.Asc())
	baseSQL, baseArgs := base.SQL()

	paid := base.Where(I.Paid.EQ(true))
	big := base.Where(I.Total.GT(100))
	if text, args := paid.SQL(); !strings.HasSuffix(text, `WHERE "org_id" = $1 AND "paid" = $2 ORDER BY "id"`) || !reflect.DeepEqual(args, []any{int64(1), true}) {
		t.Errorf("first branch: %s %v", text, args)
	}
	if text, args := big.SQL(); !strings.HasSuffix(text, `WHERE "org_id" = $1 AND "total" > $2 ORDER BY "id"`) || !reflect.DeepEqual(args, []any{int64(1), float64(100)}) {
		t.Errorf("second branch: %s %v", text, args)
	}

	byTotal := base.OrderBy(I.Total.Desc())
	byNumber := base.OrderBy(I.Number.Asc())
	if text, _ := byTotal.SQL(); !strings.HasSuffix(text, `ORDER BY "id", "total" DESC`) {
		t.Errorf("first ordering: %s", text)
	}
	if text, _ := byNumber.SQL(); !strings.HasSuffix(text, `ORDER BY "id", "number"`) {
		t.Errorf("second ordering: %s", text)
	}

	// Three conditions and three orderings leave the base's slices with room
	// for a fourth, which the two branches must not both write to.
	roomy := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1), I.Paid.EQ(true), I.Total.GT(1)).
		OrderBy(I.ID.Asc(), I.Total.Desc(), I.Number.Asc())
	low, high := roomy.Where(I.ID.LT(10)), roomy.Where(I.ID.GT(20))
	if text, _ := low.SQL(); !strings.Contains(text, `AND "id" < $4 ORDER`) {
		t.Errorf("first branch of a base with room: %s", text)
	}
	if text, _ := high.SQL(); !strings.Contains(text, `AND "id" > $4 ORDER`) {
		t.Errorf("second branch of a base with room: %s", text)
	}
	byPaid, byOrg := roomy.OrderBy(I.Paid.Asc()), roomy.OrderBy(I.OrgID.Asc())
	if text, _ := byPaid.SQL(); !strings.HasSuffix(text, `ORDER BY "id", "total" DESC, "number", "paid"`) {
		t.Errorf("first ordering of a base with room: %s", text)
	}
	if text, _ := byOrg.SQL(); !strings.HasSuffix(text, `ORDER BY "id", "total" DESC, "number", "org_id"`) {
		t.Errorf("second ordering of a base with room: %s", text)
	}

	base.Limit(5).Offset(2).Unbounded().Columns(I.ID).Omit(I.Note)
	if text, args := base.SQL(); text != baseSQL || !reflect.DeepEqual(args, baseArgs) {
		t.Errorf("the base query changed:\n got %s %v\nwant %s %v", text, args, baseSQL, baseArgs)
	}
}

// TestBaseQueryIsSafeToExtendConcurrently; run it with -race.
func TestBaseQueryIsSafeToExtendConcurrently(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	base := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1))
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 100 {
				id := int64(g*1000 + i)
				q := base.Where(I.ID.EQ(id)).OrderBy(I.ID.Desc())
				text, args := q.SQL()
				if want := `WHERE "org_id" = $1 AND "id" = $2 ORDER BY "id" DESC`; !strings.HasSuffix(text, want) || !reflect.DeepEqual(args, []any{int64(1), id}) {
					t.Errorf("goroutine %d: %s %v", g, text, args)
					return
				}
				if _, err := q.All(ctx); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}
