package velox_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

const insertCols = `"created_at", "updated_at", "org_id", "number", "total", "paid", "paid_at", "note", "data"`

func TestInsertReadsBackTheKey(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(42)}}}
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N", Data: []byte("d")}
	if err := db.Insert(&inv).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := `INSERT INTO "invoices" (` + insertCols + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING "id"`
	if inv.ID != 42 || e.SQL != want {
		t.Fatalf("ID %d\n got %s\nwant %s", inv.ID, e.SQL, want)
	}
	if inv.CreatedAt.IsZero() || inv.UpdatedAt.IsZero() || e.Args[0] != inv.CreatedAt {
		t.Errorf("defaults not filled: %v %v", inv.CreatedAt, inv.UpdatedAt)
	}
}

func TestInsertKeepsAGivenKeyAndCreatedAt(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 7, OrgID: 1, Number: "N", CreatedAt: t0}
	if err := db.Insert(&inv).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SQL, "RETURNING") || !strings.Contains(e.SQL, `"id"`) || inv.ID != 7 || !inv.CreatedAt.Equal(t0) {
		t.Errorf("%s, ID %d, CreatedAt %v", e.SQL, inv.ID, inv.CreatedAt)
	}
	if time.Since(inv.UpdatedAt) > time.Minute {
		t.Error("OnUpdate column not set on insert")
	}
}

func TestInsertMySQLReadsLastInsertID(t *testing.T) {
	e := &velox.FakeExec{Affected: 1, LastID: 9, HasLastID: true}
	db := velox.OpenFake(velox.MySQL, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 9 || strings.Contains(e.SQL, "RETURNING") {
		t.Errorf("%v, ID %d, %s", err, inv.ID, e.SQL)
	}
}

// TestInsertMySQLUpsertReadsNothingBack is Review Focus 1.
func TestInsertMySQLUpsertReadsNothingBack(t *testing.T) {
	for _, affected := range []int64{1, 2} {
		e := &velox.FakeExec{Affected: affected, LastID: 9, HasLastID: true}
		db := velox.OpenFake(velox.MySQL, e)
		inv := testmodel.Invoice{OrgID: 1, Number: "N"}
		if err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).DoNothing().Exec(ctx); err != nil || inv.ID != 0 {
			t.Errorf("affected %d: %v, ID %d", affected, err, inv.ID)
		}
	}
}

func TestUpsertSQL(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(1)}}}
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N", Total: 3}
	if err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).Update(I.Total).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `ON CONFLICT ("org_id", "number") DO UPDATE SET "total" = EXCLUDED."total" RETURNING "id"`; !strings.HasSuffix(e.SQL, want) || inv.ID != 1 {
		t.Errorf("%s, ID %d", e.SQL, inv.ID)
	}
	if err := db.Insert(&inv).Update(I.Total).Exec(ctx); err == nil || !strings.Contains(err.Error(), "an upsert needs OnConflict") {
		t.Errorf("Update without OnConflict: %v", err)
	}
}

func TestInsertAll(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.InsertAll([]testmodel.Invoice(nil)).Exec(ctx); err != nil || e.Calls != 0 {
		t.Errorf("empty: %v, calls %d", err, e.Calls)
	}
	mixed := []testmodel.Invoice{{ID: 1}, {}}
	if err := db.InsertAll(mixed).Exec(ctx); err == nil || !strings.Contains(err.Error(), "every entity's key set, or every key left at zero") {
		t.Errorf("mixed keys: %v", err)
	}
	two := []testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}}
	if err := db.InsertAll(two).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.SQL, "($10, $11, $12, $13, $14, $15, $16, $17, $18)") || len(e.Args) != 18 || two[0].CreatedAt.IsZero() {
		t.Errorf("%s (%d args)", e.SQL, len(e.Args))
	}
}

// TestInsertAllBatchesInOneTransaction is Review Focus 3.
func TestInsertAllBatchesInOneTransaction(t *testing.T) {
	defer velox.SetMaxParams(velox.Postgres, 18)() // two rows of nine columns per statement
	rows := []testmodel.Invoice{{Number: "A"}, {Number: "B"}, {Number: "C"}}
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.InsertAll(rows).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 4 || e.Log[0] != "BEGIN" || e.Log[3] != "COMMIT" || strings.Count(e.Log[1], "(") != 3 || strings.Count(e.Log[2], "(") != 2 {
		t.Errorf("%q", e.Log)
	}
	secondBatch := errors.New("second batch failed")
	failing := &velox.FakeExec{Errs: []error{nil, secondBatch}} // BEGIN consumes nothing; the first INSERT succeeds
	db = velox.OpenFake(velox.Postgres, failing)
	if err := db.InsertAll(rows).Exec(ctx); !errors.Is(err, secondBatch) || failing.Log[len(failing.Log)-1] != "ROLLBACK" {
		t.Errorf("%v %q", err, failing.Log)
	}
}
