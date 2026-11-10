package apitest

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func fiveInvoices() [][]any {
	return [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3), invoiceRow(4), invoiceRow(5)}
}

func TestRowCap(t *testing.T) {
	e := &fakeconn.Exec{Rows: fiveInvoices()}
	db := openFake(velox.Postgres, e, velox.WithMaxRows(3))
	_, err := db.Query[testmodel.Invoice]().AllowUnbounded().All(ctx)
	if !errors.Is(err, velox.ErrTooManyRows) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") || e.Closed != e.Calls {
		t.Errorf("%v; closed %d of %d", err, e.Closed, e.Calls)
	}
	if got, err := db.Query[testmodel.Invoice]().AllowUnbounded().MaxRows(10).All(ctx); err != nil || len(got) != 5 {
		t.Errorf("MaxRows(10): %d rows, %v", len(got), err)
	}
	if got, err := db.Query[testmodel.Invoice]().AllowUnbounded().MaxRows(10).MaxRows(0).All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
		t.Errorf("MaxRows(0) restores the DB's cap: %d rows, %v", len(got), err)
	}
	e.Rows = [][]any{{int64(5)}}
	if n, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 5 {
		t.Errorf("Count: %d, %v", n, err)
	}
}

func TestRowCapOfExactlyN(t *testing.T) {
	e := &fakeconn.Exec{Rows: fiveInvoices()[:3]}
	db := openFake(velox.Postgres, e, velox.WithMaxRows(3))
	if got, err := db.Query[testmodel.Invoice]().AllowUnbounded().All(ctx); err != nil || len(got) != 3 {
		t.Errorf("%d rows, %v", len(got), err)
	}
}

func TestMaxRowsBelowTwoPanics(t *testing.T) {
	velox.WithMaxRows(0) // no cap
	velox.WithMaxRows(2)
	defer func() {
		if r, _ := recover().(string); !strings.Contains(r, "WithMaxRows(1)") || !strings.Contains(r, "One") {
			t.Errorf("WithMaxRows(1): recovered %q", r)
		}
	}()
	velox.WithMaxRows(1)
}

// limitArg returns the argument of the LIMIT that sql ends with, or 0.
func limitArg(sql string, args []any) int {
	if !regexp.MustCompile(`LIMIT \$\d+$`).MatchString(sql) {
		return 0
	}
	return args[len(args)-1].(int)
}

// TestRowCapIsSentAsALimit: under a cap of n, velox sends LIMIT n+1, so the
// database stops at the row that trips the cap; a lower Limit is kept. The
// guard and the Rewrite hooks see the caller's Limit, the Around hooks the
// one sent.
func TestRowCapIsSentAsALimit(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	var rewrote, around []int
	db := openFake(velox.Postgres, e, velox.WithMaxRows(3))
	hooked := openFake(velox.Postgres, e, velox.WithMaxRows(3),
		velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			rewrote = append(rewrote, st.Limit)
			return nil
		}),
		velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			around = append(around, st.Limit)
			return next(ctx)
		}))
	for _, d := range []*velox.DB{db, hooked} {
		for _, c := range []struct {
			name string
			q    velox.Query[testmodel.Invoice]
			want int
		}{
			{"Unbounded", d.Query[testmodel.Invoice]().AllowUnbounded(), 4},
			{"Limit(2)", d.Query[testmodel.Invoice]().Limit(2), 2},
			{"Limit(10)", d.Query[testmodel.Invoice]().Limit(10), 4},
			{"MaxRows(10)", d.Query[testmodel.Invoice]().AllowUnbounded().MaxRows(10), 11},
		} {
			if _, err := c.q.All(ctx); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if got := limitArg(e.SQL, e.Args); got != c.want {
				t.Errorf("%s: sent %s %v, want LIMIT %d", c.name, e.SQL, e.Args, c.want)
			}
		}
	}
	if want := []int{0, 2, 10, 0}; !slices.Equal(rewrote, want) {
		t.Errorf("Rewrite hooks saw Limit %v, want %v", rewrote, want)
	}
	if want := []int{4, 2, 4, 11}; !slices.Equal(around, want) {
		t.Errorf("Around hooks saw Limit %v, want %v", around, want)
	}
	for range db.Query[testmodel.Invoice]().AllowUnbounded().Iter(ctx) {
	}
	if got := limitArg(e.SQL, e.Args); got != 4 {
		t.Errorf("Iter: sent %s %v", e.SQL, e.Args)
	}
	e.Results = [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 1)}}
	if _, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if got := limitArg(e.SQL, e.Args); !strings.Contains(e.SQL, `"items"`) || got != 4 {
		t.Errorf("With load: sent %s %v", e.SQL, e.Args)
	}
	e.Rows = [][]any{{int64(1)}}
	if _, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil || strings.Contains(e.SQL, "LIMIT") {
		t.Errorf("Count: %v, sent %s", err, e.SQL)
	}
}

// TestRowCapInIter is Review Focus 5.
func TestRowCapInIter(t *testing.T) {
	e := &fakeconn.Exec{Rows: fiveInvoices()}
	db := openFake(velox.Postgres, e, velox.WithMaxRows(3))
	var rows int
	var last error
	for _, err := range db.Query[testmodel.Invoice]().AllowUnbounded().Iter(ctx) {
		if err != nil {
			last = err
			continue
		}
		rows++
	}
	if rows != 3 || !errors.Is(last, velox.ErrTooManyRows) || e.Closed != 1 {
		t.Errorf("%d rows, then %v; closed %d", rows, last, e.Closed)
	}
}

// TestInsertAllUnderRowCap checks that a write is not refused by the cap;
// that rowCap spares an insert is checked in rowcap_internal_test.go.
func TestInsertAllUnderRowCap(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}, {int64(2)}, {int64(3)}}}
	db := openFake(velox.Postgres, e, velox.WithMaxRows(2))
	xs := []testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}, {OrgID: 1, Number: "C"}}
	if err := db.InsertAll(xs).Exec(ctx); err != nil {
		t.Errorf("InsertAll: %v", err)
	}
	one := testmodel.Invoice{OrgID: 1, Number: "A"}
	if err := db.Insert(&one).Exec(ctx); err != nil || one.ID != 1 {
		t.Errorf("Insert: %v, id %d", err, one.ID)
	}
}

func TestRowCapCoversRelationLoads(t *testing.T) {
	results := func() [][][]any {
		return [][][]any{
			{invoiceRow(1)},
			{itemRow(10, 1, "A", 1), itemRow(11, 1, "B", 1), itemRow(12, 1, "C", 1)},
		}
	}
	e := &fakeconn.Exec{Results: results()}
	db := openFake(velox.Postgres, e, velox.WithMaxRows(2))
	_, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	// The load's error names the owner once and keeps the item's error, without
	// its own "velox: ".
	const want = "velox: testmodel.Invoice: loading items: testmodel.Item: read more rows than the cap: more than 2; raise it with the query's MaxRows"
	if !errors.Is(err, velox.ErrTooManyRows) || err.Error() != want {
		t.Errorf("%v, want %q", err, want)
	}
	// The owner query's MaxRows covers its loads.
	e = &fakeconn.Exec{Results: results()}
	db = openFake(velox.Postgres, e, velox.WithMaxRows(2))
	got, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(1).MaxRows(10).All(ctx)
	if err != nil || len(got) != 1 {
		t.Errorf("MaxRows(10): %d rows, %v", len(got), err)
	}
}

// TestRowCapAcrossNestedWith: the cap covers a load inside a load. The error
// names the inner load only: the outer "loading items" context is lost
// (recorded for after 1.0). The statements' last arguments are the owner
// query's own LIMIT 1, then each load's cap+1.
func TestRowCapAcrossNestedWith(t *testing.T) {
	results := func() [][][]any {
		return [][][]any{
			{invoiceRow(1)},
			{itemRow(10, 1, "A", 1), itemRow(11, 1, "B", 1)},
			{invoiceRow(1), invoiceRow(2), invoiceRow(3)},
		}
	}
	lastArgs := func(e *fakeconn.Exec) []any {
		var last []any
		for _, args := range e.ArgLog {
			last = append(last, args[len(args)-1])
		}
		return last
	}
	query := func(db *velox.DB) velox.Query[testmodel.Invoice] {
		return db.Query[testmodel.Invoice]().With(I.Items.With(It.Invoice)).Limit(1)
	}
	e := &fakeconn.Exec{Results: results()}
	db := openFake(velox.Postgres, e, velox.WithMaxRows(2))
	_, err := query(db).All(ctx)
	const want = "velox: testmodel.Item: loading invoice: testmodel.Invoice: read more rows than the cap: more than 2; raise it with the query's MaxRows"
	if !errors.Is(err, velox.ErrTooManyRows) || err.Error() != want {
		t.Errorf("%v, want %q", err, want)
	}
	if got := lastArgs(e); !slices.Equal(got, []any{1, 3, 3}) {
		t.Errorf("last arguments %v, want [1 3 3]", got)
	}
	e = &fakeconn.Exec{Results: results()}
	db = openFake(velox.Postgres, e, velox.WithMaxRows(2))
	got, err := query(db).MaxRows(10).All(ctx)
	if err != nil || len(got) != 1 {
		t.Errorf("MaxRows(10): %d rows, %v", len(got), err)
	}
	if got := lastArgs(e); len(got) != 3 || got[1] != 11 || got[2] != 11 {
		t.Errorf("MaxRows(10): last arguments %v, want the loads' to be 11", got)
	}
}

// TestRowCapFromARewrite: a cap a Rewrite hook sets on the Statement is
// sent as a LIMIT and enforced.
func TestRowCapFromARewrite(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3)}}
	db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.MaxRows = 2
		return nil
	}))
	_, err := db.Query[testmodel.Invoice]().AllowUnbounded().All(ctx)
	const want = "velox: testmodel.Invoice: read more rows than the cap: more than 2; raise it with the query's MaxRows"
	if !errors.Is(err, velox.ErrTooManyRows) || err.Error() != want {
		t.Errorf("%v, want %q", err, want)
	}
	if !strings.HasSuffix(e.SQL, ` FROM "invoices" LIMIT $1`) || !slices.Equal(e.Args, []any{3}) {
		t.Errorf("sent %s %v, want ... FROM \"invoices\" LIMIT $1 [3]", e.SQL, e.Args)
	}
}
