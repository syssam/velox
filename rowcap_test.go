package velox_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func fiveInvoices() [][]any {
	return [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3), invoiceRow(4), invoiceRow(5)}
}

func TestRowCap(t *testing.T) {
	e := &velox.FakeExec{Rows: fiveInvoices()}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(3))
	_, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx)
	if !errors.Is(err, velox.ErrTooManyRows) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") || e.Closed != e.Calls {
		t.Errorf("%v; closed %d of %d", err, e.Closed, e.Calls)
	}
	if got, err := db.Query[testmodel.Invoice]().Unbounded().MaxRows(10).All(ctx); err != nil || len(got) != 5 {
		t.Errorf("MaxRows(10): %d rows, %v", len(got), err)
	}
	if got, err := db.Query[testmodel.Invoice]().Unbounded().MaxRows(10).MaxRows(0).All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
		t.Errorf("MaxRows(0) restores the DB's cap: %d rows, %v", len(got), err)
	}
	e.Rows = [][]any{{int64(5)}}
	if n, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 5 {
		t.Errorf("Count: %d, %v", n, err)
	}
}

// TestRowCapInIter is Review Focus 5.
func TestRowCapInIter(t *testing.T) {
	e := &velox.FakeExec{Rows: fiveInvoices()}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(3))
	var rows int
	var last error
	for _, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
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
	e := &velox.FakeExec{Rows: [][]any{{int64(1)}, {int64(2)}, {int64(3)}}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(1))
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
	e := &velox.FakeExec{Results: results()}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(2))
	_, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if !errors.Is(err, velox.ErrTooManyRows) || !strings.Contains(err.Error(), "Items") {
		t.Errorf("%v", err)
	}
	// The owner query's MaxRows covers its loads.
	e = &velox.FakeExec{Results: results()}
	db = velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(2))
	got, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(1).MaxRows(10).All(ctx)
	if err != nil || len(got) != 1 {
		t.Errorf("MaxRows(10): %d rows, %v", len(got), err)
	}
}
