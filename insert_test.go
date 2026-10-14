package velox_test

import (
	"context"
	"errors"
	"reflect"
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
	mixed := []testmodel.Invoice{{ID: 1, OrgID: 1}, {OrgID: 1}}
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
	rows := []testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}, {OrgID: 1, Number: "C"}}
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

// Small entities with a hand-written scan plan, for what Invoice cannot show.

type keyOnly struct{ ID int64 }

func (*keyOnly) Schema(*velox.Schema) {}

var keyOnlyMeta velox.MetaOnce[keyOnly]

func (*keyOnly) VeloxMeta() *velox.Meta[keyOnly] {
	return keyOnlyMeta.Get(func() velox.Shape[keyOnly] {
		return velox.Shape[keyOnly]{Columns: []string{"ID"}, Addr: func(x *keyOnly, i int) any { return &x.ID }}
	})
}

type wideKey struct {
	ID  uint64
	Opt *bool
}

func (x *wideKey) Schema(s *velox.Schema) {
	yes := true
	s.Column(&x.Opt).Default(&yes)
}

var wideKeyMeta velox.MetaOnce[wideKey]

func (*wideKey) VeloxMeta() *velox.Meta[wideKey] {
	return wideKeyMeta.Get(func() velox.Shape[wideKey] {
		return velox.Shape[wideKey]{Columns: []string{"ID", "Opt"}, Addr: func(x *wideKey, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Opt
		}}
	})
}

type tinyKey struct {
	ID   int8
	Name string
}

func (*tinyKey) Schema(*velox.Schema) {}

var tinyKeyMeta velox.MetaOnce[tinyKey]

func (*tinyKey) VeloxMeta() *velox.Meta[tinyKey] {
	return tinyKeyMeta.Get(func() velox.Shape[tinyKey] {
		return velox.Shape[tinyKey]{Columns: []string{"ID", "Name"}, Addr: func(x *tinyKey, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Name
		}}
	})
}

func TestInsertOfANilPointerIsAnError(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Insert((*testmodel.Invoice)(nil)).Exec(ctx)
	if err == nil || !strings.HasPrefix(err.Error(), "velox: ") || !strings.Contains(err.Error(), "Invoice: Insert of a nil pointer") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}

func TestInsertOfAKeyOnlyEntityIsAnError(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	want := "nothing to insert: the only column is the generated key"
	if err := db.Insert(&keyOnly{}).Exec(ctx); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Insert: %v", err)
	}
	if err := db.InsertAll([]keyOnly{{}, {}}).Exec(ctx); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("InsertAll: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("calls %d", e.Calls)
	}
}

func TestDefaultOnAPointerFieldFillsNilOnly(t *testing.T) {
	e := &velox.FakeExec{Affected: 1, LastID: 1, HasLastID: true}
	db := velox.OpenFake(velox.MySQL, e)
	a := wideKey{}
	if err := db.Insert(&a).Exec(ctx); err != nil || a.Opt == nil || !*a.Opt {
		t.Fatalf("%v, Opt %v", err, a.Opt)
	}
	other := wideKey{}
	if err := db.Insert(&other).Exec(ctx); err != nil || other.Opt == a.Opt {
		t.Errorf("entities share the default's pointee: %v", err)
	}
	no := false
	b := wideKey{Opt: &no}
	if err := db.Insert(&b).Exec(ctx); err != nil || b.Opt != &no || *b.Opt {
		t.Errorf("an explicit false was overwritten: %v", err)
	}
}

func TestInsertMySQLUnsignedKeyAndOverflow(t *testing.T) {
	e := &velox.FakeExec{Affected: 1, LastID: 300, HasLastID: true}
	db := velox.OpenFake(velox.MySQL, e)
	w := wideKey{}
	if err := db.Insert(&w).Exec(ctx); err != nil || w.ID != 300 {
		t.Errorf("%v, ID %d", err, w.ID)
	}
	tk := tinyKey{Name: "x"}
	if err := db.Insert(&tk).Exec(ctx); err == nil || !strings.Contains(err.Error(), "overflows the key field ID") || tk.ID != 0 {
		t.Errorf("%v, ID %d", err, tk.ID)
	}
	e.LastID = -1
	if err := db.Insert(&wideKey{}).Exec(ctx); err == nil || !strings.Contains(err.Error(), "overflows the key field ID") {
		t.Errorf("negative id for an unsigned key: %v", err)
	}
}

func TestInsertPostgresDoNothingWithoutARow(t *testing.T) {
	e := &velox.FakeExec{} // ON CONFLICT DO NOTHING RETURNING returns no row when the row exists
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).DoNothing().Exec(ctx); err != nil || inv.ID != 0 {
		t.Errorf("%v, ID %d", err, inv.ID)
	}
	if !strings.Contains(e.SQL, "DO NOTHING") {
		t.Error(e.SQL)
	}
	// OnConflict alone keeps the existing row; chaining order does not matter.
	if err := db.Insert(&inv).DoNothing().OnConflict(I.OrgID, I.Number).Exec(ctx); err != nil || !strings.Contains(e.SQL, `ON CONFLICT ("org_id", "number") DO NOTHING`) {
		t.Errorf("%v, %s", err, e.SQL)
	}
}

func TestInsertAllAnExactlyFullBatchIsOneStatement(t *testing.T) {
	defer velox.SetMaxParams(velox.Postgres, 18)()
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.InsertAll([]testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 1 || strings.Count(e.Log[0], "(") != 3 {
		t.Errorf("%q", e.Log)
	}
}

func TestInsertAllInsideATransactionUsesASavepoint(t *testing.T) {
	defer velox.SetMaxParams(velox.Postgres, 18)()
	rows := []testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}, {OrgID: 1, Number: "C"}}
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.Tx(ctx, func(tx *velox.DB) error { return tx.InsertAll(rows).Exec(ctx) }); err != nil {
		t.Fatal(err)
	}
	l := e.Log
	if len(l) != 6 || l[0] != "BEGIN" || l[1] != "SAVEPOINT velox_1" || !strings.HasPrefix(l[2], "INSERT") || !strings.HasPrefix(l[3], "INSERT") || l[4] != "RELEASE SAVEPOINT velox_1" || l[5] != "COMMIT" {
		t.Errorf("%q", l)
	}

	boom := errors.New("second batch failed")
	failing := &velox.FakeExec{Errs: []error{nil, nil, boom}} // SAVEPOINT, first INSERT, second INSERT
	db = velox.OpenFake(velox.Postgres, failing)
	var inner error
	err := db.Tx(ctx, func(tx *velox.DB) error {
		inner = tx.InsertAll(rows).Exec(ctx)
		return nil // the caller swallows the error: the savepoint must already have undone batch one
	})
	if err != nil || !errors.Is(inner, boom) {
		t.Fatalf("%v, %v", err, inner)
	}
	want := []string{"BEGIN", "SAVEPOINT velox_1"}
	if l := failing.Log; len(l) != 7 || !reflect.DeepEqual(l[:2], want) || l[4] != "ROLLBACK TO SAVEPOINT velox_1" || l[5] != "RELEASE SAVEPOINT velox_1" || l[6] != "COMMIT" {
		t.Errorf("%q", failing.Log)
	}
}

func TestInsertReadsTheKeyBackUnderHooks(t *testing.T) {
	var sawRewrite, sawAround bool
	rewrite := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { sawRewrite = true; return nil })
	around := velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		sawAround = true
		return next(ctx)
	})
	e := &velox.FakeExec{Rows: [][]any{{int64(42)}}}
	db := velox.OpenFake(velox.Postgres, e, rewrite, around)
	inv := testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 42 || !sawRewrite || !sawAround {
		t.Errorf("%v, ID %d, hooks %v %v", err, inv.ID, sawRewrite, sawAround)
	}

	// A hook that drops RETURNING leaves the key alone: the rows do not hold it.
	drop := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { st.Returning = nil; return nil })
	db = velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{{int64(42)}}}, drop)
	inv = testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 0 {
		t.Errorf("dropped RETURNING: %v, ID %d", err, inv.ID)
	}

	// A hook that turns the insert into an upsert on MySQL leaves the key alone.
	upsert := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Conflict = &velox.ConflictClause{Columns: []int{2, 3}, DoNothing: true}
		return nil
	})
	db = velox.OpenFake(velox.MySQL, &velox.FakeExec{Affected: 1, LastID: 9, HasLastID: true}, upsert)
	inv = testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 0 {
		t.Errorf("upsert added by a hook: %v, ID %d", err, inv.ID)
	}
}
