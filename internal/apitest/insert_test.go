package apitest

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// insertCols are the columns an insert of an Invoice with a generated key
// and a nil PaidAt writes: the nil column is left out (ADR 0093).
const insertCols = `"created_at", "updated_at", "org_id", "number", "total", "paid", "note", "data"`

func TestInsertReadsBackTheKey(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(42)}}}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N", Data: []byte("d")}
	if err := db.Insert(&inv).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := `INSERT INTO "invoices" (` + insertCols + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING "id"`
	if inv.ID != 42 || e.SQL != want {
		t.Fatalf("ID %d\n got %s\nwant %s", inv.ID, e.SQL, want)
	}
	if inv.CreatedAt.IsZero() || inv.UpdatedAt.IsZero() || e.Args[0] != inv.CreatedAt {
		t.Errorf("defaults not filled: %v %v", inv.CreatedAt, inv.UpdatedAt)
	}
}

func TestInsertKeepsAGivenKeyAndCreatedAt(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 7, OrgID: 1, Number: "N", CreatedAt: t0}
	if err := db.Insert(&inv).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SQL, "RETURNING") || !strings.Contains(e.SQL, `"id"`) || inv.ID != 7 || !inv.CreatedAt.Equal(t0) {
		t.Errorf("%s, ID %d, CreatedAt %v", e.SQL, inv.ID, inv.CreatedAt)
	}
	if time.Since(inv.UpdatedAt) > time.Minute {
		t.Error("updated_at (its DefaultFunc) not set on insert")
	}
}

// TestInsertFillsADefaultNotAnUpdateDefault: an UpdateDefault fills on update
// only. updated_at has both a DefaultFunc and an UpdateDefault, so
// an insert fills it when it is zero and keeps a value the caller gave;
// hFlex's Stamp has an UpdateDefault alone, so an insert leaves it as given.
func TestInsertFillsADefaultNotAnUpdateDefault(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 7, OrgID: 1, Number: "N", UpdatedAt: t0}
	if err := db.Insert(&inv).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !inv.UpdatedAt.Equal(t0) || e.Args[2] != t0 { // the args are id, created_at, updated_at, ...
		t.Errorf("an updated_at the caller gave was replaced: %v, sent %v", inv.UpdatedAt, e.Args[2])
	}

	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		return next.Mutate(ctx, m)
	}
	for _, stamp := range []int64{0, 4} {
		x := hFlex{ID: 1, Title: "t", Stamp: stamp}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if x.Stamp != stamp || e.Args[2] != stamp {
			t.Errorf("Stamp %d: an insert applied the UpdateDefault: row %d, sent %v", stamp, x.Stamp, e.Args[2])
		}
	}
}

func TestInsertMySQLReadsLastInsertID(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1, LastID: 9, HasLastID: true}
	db := openFake(velox.MySQL, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 9 || strings.Contains(e.SQL, "RETURNING") {
		t.Errorf("%v, ID %d, %s", err, inv.ID, e.SQL)
	}
}

// TestInsertMySQLReadsTheKeyWithAnUnknownCount: on MySQL, an insert whose
// driver could not report its count (affected -1, as conn.SQL gives for a
// RowsAffected error) still stores the key LastInsertId gave.
func TestInsertMySQLReadsTheKeyWithAnUnknownCount(t *testing.T) {
	e := &fakeconn.Exec{Affected: -1, LastID: 9, HasLastID: true}
	db := openFake(velox.MySQL, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 9 {
		t.Errorf("%v, ID %d; want ID 9", err, inv.ID)
	}
}

// TestInsertMySQLUpsertReadsNothingBack is Review Focus 1.
func TestInsertMySQLUpsertReadsNothingBack(t *testing.T) {
	for _, affected := range []int64{1, 2} {
		e := &fakeconn.Exec{Affected: affected, LastID: 9, HasLastID: true}
		db := openFake(velox.MySQL, e)
		inv := testmodel.Invoice{OrgID: 1, Number: "N"}
		if err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).DoNothing().Exec(ctx); err != nil || inv.ID != 0 {
			t.Errorf("affected %d: %v, ID %d", affected, err, inv.ID)
		}
	}
}

func TestUpsertSQL(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}}
	db := openFake(velox.Postgres, e)
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
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
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
	if !strings.HasSuffix(e.SQL, `($9, $10, $11, $12, $13, $14, $15, $16) RETURNING "id"`) || len(e.Args) != 16 || two[0].CreatedAt.IsZero() {
		t.Errorf("%s (%d args)", e.SQL, len(e.Args))
	}
}

func TestInsertOfANilPointerIsAnError(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	err := db.Insert((*testmodel.Invoice)(nil)).Exec(ctx)
	if err == nil || !strings.HasPrefix(err.Error(), "velox: ") || !strings.Contains(err.Error(), "Invoice: Insert of a nil pointer") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}

func TestInsertOfAKeyOnlyEntityIsAnError(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
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
	e := &fakeconn.Exec{Affected: 1, LastID: 1, HasLastID: true}
	db := openFake(velox.MySQL, e)
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
	e := &fakeconn.Exec{Affected: 1, LastID: 300, HasLastID: true}
	db := openFake(velox.MySQL, e)
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
	e := &fakeconn.Exec{} // ON CONFLICT DO NOTHING RETURNING returns no row when the row exists
	db := openFake(velox.Postgres, e)
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

func TestInsertReadsTheKeyBackUnderHooks(t *testing.T) {
	var sawRewrite, sawAround bool
	rewrite := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { sawRewrite = true; return nil })
	around := velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		sawAround = true
		return next(ctx)
	})
	e := &fakeconn.Exec{Rows: [][]any{{int64(42)}}}
	db := openFake(velox.Postgres, e, rewrite, around)
	inv := testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 42 || !sawRewrite || !sawAround {
		t.Errorf("%v, ID %d, hooks %v %v", err, inv.ID, sawRewrite, sawAround)
	}

	// A hook that drops RETURNING leaves the key alone: the rows do not hold it.
	drop := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { st.Returning = nil; return nil })
	db = openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{{int64(42)}}}, drop)
	inv = testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 0 {
		t.Errorf("dropped RETURNING: %v, ID %d", err, inv.ID)
	}

	// A hook that turns the insert into an upsert on MySQL leaves the key alone.
	upsert := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Conflict = &velox.ConflictClause{Columns: []int{2, 3}, DoNothing: true}
		return nil
	})
	db = openFake(velox.MySQL, &fakeconn.Exec{Affected: 1, LastID: 9, HasLastID: true}, upsert)
	inv = testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 0 {
		t.Errorf("upsert added by a hook: %v, ID %d", err, inv.ID)
	}
}

// dshared has a Default of each kind that holds memory: entities given one
// must not share it.
type dshared struct {
	ID int64
	B  []byte
	S  []string
	M  map[string]int
	N  map[string][]string
	A  map[string]any
}

var dsharedMeta velox.MetaOnce[dshared]

func (*dshared) VeloxMeta() *velox.Meta[dshared] {
	return dsharedMeta.Load(func() velox.Descriptor[dshared] {
		enc := func(v any) ([]byte, error) { return json.Marshal(v) }
		dec := func(data []byte, v any) error { return json.Unmarshal(data, v) }
		return velox.Descriptor[dshared]{
			Table: "dshared",
			Columns: []velox.ColumnDescriptor{
				{Field: "ID", Name: "id"},
				{Field: "B", Name: "b", Default: []byte{1, 2}},
				{Field: "S", Name: "s", Default: []string{"a"}, Encode: enc, Decode: dec},
				{Field: "M", Name: "m", Default: map[string]int{"a": 1}, Encode: enc, Decode: dec},
				{Field: "N", Name: "n", Default: map[string][]string{"k": {"v"}}, Encode: enc, Decode: dec},
				{Field: "A", Name: "a", Default: map[string]any{"k": []int{1}}, Encode: enc, Decode: dec},
			},
			PrimaryKey: []int{0},
			FieldPtr: func(x *dshared, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.B
				case 2:
					return &x.S
				case 3:
					return &x.M
				case 4:
					return &x.N
				}
				return &x.A
			},
		}
	})
}

// TestDefaultsAreNotShared: a Default that holds memory is copied into each
// entity, so changing one entity's changes neither another's nor the
// Default.
func TestDefaultsAreNotShared(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{{int64(1)}}})
	insert := func() dshared {
		var x dshared
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		return x
	}
	want := dshared{ID: 1, B: []byte{1, 2}, S: []string{"a"}, M: map[string]int{"a": 1}, N: map[string][]string{"k": {"v"}}, A: map[string]any{"k": []int{1}}}
	x1, x2 := insert(), insert()
	x1.B[0] = 9
	x1.S[0] = "z"
	x1.M["a"] = 9
	x1.N["k"][0] = "z"
	x1.A["k"].([]int)[0] = 9
	if !reflect.DeepEqual(x2, want) {
		t.Errorf("the second entity: %+v, want %+v", x2, want)
	}
	if x3 := insert(); !reflect.DeepEqual(x3, want) {
		t.Errorf("a third entity: %+v, want %+v", x3, want)
	}
}

// TestInsertAllSplitCountsTheParameters: the rows one statement holds count the
// parameters a row sends, so a column that is NULL in every row, which is no
// parameter, is not counted; one with a value in any row is, in every row.
func TestInsertAllSplitCountsTheParameters(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.SQLite, e)
	const nonNull = 6 // Ticket's columns but the key and parent_id, which Parent fills below
	rows := func(n int, withParent bool) []testmodel.Ticket {
		tks := make([]testmodel.Ticket, n)
		for i := range tks {
			tks[i] = testmodel.Ticket{Owner: "ann", Title: fmt.Sprintf("T-%d", i), InvoiceID: 1}
		}
		if withParent {
			p := int64(1)
			tks[0].ParentID = &p
		}
		return tks
	}
	for _, c := range []struct {
		name       string
		withParent bool
		params     int // per row
	}{{"parent_id NULL in every row", false, nonNull}, {"parent_id set in one row", true, nonNull + 1}} {
		per := 32766 / c.params
		_, args, err := db.InsertAll(rows(per+1, c.withParent)).SQL(annIn7())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(args) != per*c.params {
			t.Errorf("%s: the first statement has %d arguments, want %d rows of %d", c.name, len(args), per, c.params)
		}
	}
}
