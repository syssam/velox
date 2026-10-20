package velox_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2"
)

// jShape is a struct stored as JSON.
type jShape struct {
	W int `json:"w"`
	H int `json:"h"`
}

// jDoc has a column of each kind of JSON value: a slice, a map, a struct,
// and a nullable pointer to a slice.
type jDoc struct {
	ID      int64
	OwnerID int64
	Tags    []string
	Attrs   map[string]any
	Shape   jShape
	Nums    *[]int
}

// jDocs are jDoc's handles as velox-gen writes them: JSONCol and NullJSONCol
// for the encoded columns, which set them and compare or order by nothing.
var jDocs = struct {
	ID      velox.NumberCol[jDoc, int64]
	OwnerID velox.NumberCol[jDoc, int64]
	Tags    velox.JSONCol[jDoc, []string]
	Attrs   velox.JSONCol[jDoc, map[string]any]
	Shape   velox.JSONCol[jDoc, jShape]
	Nums    velox.NullJSONCol[jDoc, []int]
}{
	velox.NumberCol[jDoc, int64]{I: 0}, velox.NumberCol[jDoc, int64]{I: 1},
	velox.JSONCol[jDoc, []string]{I: 2}, velox.JSONCol[jDoc, map[string]any]{I: 3},
	velox.JSONCol[jDoc, jShape]{I: 4}, velox.NullJSONCol[jDoc, []int]{I: 5},
}

// jDocCols are Col handles of jDoc's encoded columns, as a hand-written Desc
// might declare them: their conditions and orders compile, and the statement
// refuses them, the backstop behind JSONCol, which has none.
var jDocCols = struct {
	Tags  velox.Col[jDoc, []string]
	Shape velox.Col[jDoc, jShape]
	Nums  velox.NullCol[jDoc, []int]
}{velox.Col[jDoc, []string]{I: 2}, velox.Col[jDoc, jShape]{I: 4}, velox.NullCol[jDoc, []int]{I: 5}}

var jDocMeta velox.MetaOnce[jDoc]

func (*jDoc) VeloxMeta() *velox.Meta[jDoc] { return jDocMeta.Load(jDocDesc) }

func jDocDesc() velox.Desc[jDoc] {
	return velox.Desc[jDoc]{
		Table: "j_docs",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "OwnerID", Name: "owner_id"},
			{Field: "Tags", Name: "tags", Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Attrs", Name: "attrs", Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Shape", Name: "shape", Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Nums", Name: "nums", Encode: json.Marshal, Decode: json.Unmarshal},
		},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *jDoc, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.OwnerID
			case 2:
				return &x.Tags
			case 3:
				return &x.Attrs
			case 4:
				return &x.Shape
			case 5:
				return &x.Nums
			}
			return nil
		},
	}
}

// jOwner has many jDocs, to load them through With.
type jOwner struct {
	ID   int64
	Docs velox.Many[jDoc]
}

var jOwners = struct {
	ID   velox.NumberCol[jOwner, int64]
	Docs velox.Rel[jOwner, jDoc]
}{velox.NumberCol[jOwner, int64]{I: 0}, velox.Rel[jOwner, jDoc]{I: 0}}

var jOwnerMeta velox.MetaOnce[jOwner]

func (*jOwner) VeloxMeta() *velox.Meta[jOwner] {
	return jOwnerMeta.Load(func() velox.Desc[jOwner] {
		return velox.Desc[jOwner]{
			Table:      "j_owners",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Relations:  []velox.Relation{velox.EdgeTo[jDoc]("Docs", 0, 1)},
			Addr:       func(x *jOwner, i int) any { return &x.ID },
			RelAddr:    func(x *jOwner, i int) any { return &x.Docs },
		}
	})
}

func ints(xs ...int) *[]int { return &xs }

// jFull is a jDoc with every JSON column set, and the bytes each binds.
func jFull() (jDoc, []any) {
	x := jDoc{ID: 1, OwnerID: 2, Tags: []string{"a", "b"}, Attrs: map[string]any{"k": 1}, Shape: jShape{3, 4}, Nums: ints(5, 6)}
	return x, []any{int64(1), int64(2), []byte(`["a","b"]`), []byte(`{"k":1}`), []byte(`{"w":3,"h":4}`), []byte(`[5,6]`)}
}

// bound is args as SQLite and MySQL bind them: each []byte, an encoded value
// that is valid UTF-8, as a string.
func bound(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		if b, ok := a.([]byte); ok {
			a = string(b)
		}
		out[i] = a
	}
	return out
}

func sameArgs(t *testing.T, what string, got, want []any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: args\n%s\nwant\n%s", what, showArgs(got), showArgs(want))
	}
}

// showArgs prints arguments with their types, and []byte ones as text.
func showArgs(args []any) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if bs, ok := a.([]byte); ok {
			parts[i] = "[]byte(" + string(bs) + ")"
		} else {
			parts[i] = fmt.Sprintf("%T(%v)", a, a)
		}
	}
	return strings.Join(parts, ", ")
}

// TestJSONWrites: every write binds a JSON column's value as the bytes of its
// JSON; a nil pointer binds NULL, a nil slice or map in a non-nullable field
// the JSON null. Hooks and the entity keep Go values.
func TestJSONWrites(t *testing.T) {
	t.Run("Insert", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e)
		x, want := jFull()
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sameArgs(t, "Insert", e.Args, want)
		if y, _ := jFull(); !reflect.DeepEqual(x, y) {
			t.Errorf("Insert changed the entity: %+v", x)
		}
	})
	t.Run("InsertNil", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e)
		x := jDoc{ID: 1, OwnerID: 2}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sameArgs(t, "Insert of nil values", e.Args, []any{int64(1), int64(2), []byte("null"), []byte("null"), []byte(`{"w":0,"h":0}`), nil})
	})
	t.Run("InsertAll", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 2}
		db := velox.OpenFake(velox.MySQL, e)
		x, want := jFull()
		y := jDoc{ID: 3, OwnerID: 2, Tags: []string{}, Nums: new([]int)} // a pointer to a nil slice: NULL
		if err := db.InsertAll([]jDoc{x, y}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		want = append(want, int64(3), int64(2), []byte(`[]`), []byte("null"), []byte(`{"w":0,"h":0}`), nil)
		sameArgs(t, "InsertAll", e.Args, bound(want))
	})
	t.Run("Update", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e)
		_, err := db.Update[jDoc]().Set(jDocs.Tags.Set([]string{"x"}), jDocs.Shape.Set(jShape{1, 2}), jDocs.Nums.Set([]int{7})).
			Where(jDocs.ID.EQ(1)).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sameArgs(t, "Update", e.Args, []any{[]byte(`["x"]`), []byte(`{"w":1,"h":2}`), []byte(`[7]`), int64(1)})
		if _, err := db.Update[jDoc]().Set(jDocs.Nums.SetNull(), jDocs.Attrs.Set(nil)).Where(jDocs.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sameArgs(t, "Update to NULL", e.Args, []any{nil, []byte("null"), int64(1)})
		if _, err := db.Update[jDoc]().Set(jDocs.Nums.Set(nil)).Where(jDocs.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sameArgs(t, "Update of a nullable column to a nil slice", e.Args, []any{nil, int64(1)})
	})
	t.Run("Save", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e)
		x, _ := jFull()
		if err := db.Save(ctx, &x, jDocs.Tags, jDocs.Attrs, jDocs.Nums); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(e.SQL, `UPDATE "j_docs" SET "tags" = $1, "attrs" = $2, "nums" = $3 WHERE "id" = $4`) {
			t.Fatalf("Save SQL %s", e.SQL)
		}
		sameArgs(t, "Save", e.Args, []any{[]byte(`["a","b"]`), []byte(`{"k":1}`), []byte(`[5,6]`), int64(1)})
		// Save stores back what it assigned: the Go values, not their bytes.
		if y, _ := jFull(); !reflect.DeepEqual(x, y) {
			t.Errorf("Save stored %+v", x)
		}
		x.Nums = nil
		if err := db.Save(ctx, &x, jDocs.Nums); err != nil {
			t.Fatal(err)
		}
		sameArgs(t, "Save of nil", e.Args, []any{nil, int64(1)})
	})
	t.Run("Upsert", func(t *testing.T) {
		for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
			e := &velox.FakeExec{Affected: 1}
			db := velox.OpenFake(d, e)
			x, want := jFull()
			if err := db.Insert(&x).OnConflict(jDocs.ID).Update(jDocs.Tags, jDocs.Attrs, jDocs.Nums).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(e.SQL, "UPDATE") {
				t.Fatalf("%v: no update branch: %s", d, e.SQL)
			}
			if d != velox.Postgres {
				want = bound(want)
			}
			sameArgs(t, d.String()+" upsert", e.Args, want)
		}
	})
	t.Run("Hooks", func(t *testing.T) {
		// A Rewrite hook sees the Go values; an Around hook sees them in
		// Rows and Set, and the bytes in Args.
		var rewritten, around []any
		var args []any
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e,
			velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
				if st.Op == velox.OpInsert {
					rewritten = append([]any(nil), st.Rows[0]...)
				} else {
					rewritten = []any{st.Set[0].Value}
				}
				return nil
			}),
			velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
				if st.Op == velox.OpInsert {
					around = append([]any(nil), st.Rows[0]...)
				} else {
					around = []any{st.Set[0].Value}
				}
				args = st.Args
				return next(ctx)
			}))
		x, want := jFull()
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		goValues := []any{int64(1), int64(2), x.Tags, x.Attrs, x.Shape, x.Nums}
		if !reflect.DeepEqual(rewritten, goValues) || !reflect.DeepEqual(around, goValues) {
			t.Errorf("the hooks saw %v and %v, want the Go values", rewritten, around)
		}
		sameArgs(t, "Statement.Args", args, want)
		if _, err := db.Update[jDoc]().Set(jDocs.Tags.Set([]string{"x"})).Where(jDocs.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(rewritten, []any{[]string{"x"}}) || !reflect.DeepEqual(around, []any{[]string{"x"}}) {
			t.Errorf("the hooks saw %v and %v, want the Go value", rewritten, around)
		}
		sameArgs(t, "Statement.Args", args, []any{[]byte(`["x"]`), int64(1)})
	})
	t.Run("SQL", func(t *testing.T) {
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
		x, want := jFull()
		_, args, err := db.Insert(&x).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sameArgs(t, "Insert SQL", args, want)
	})
	t.Run("Unencodable", func(t *testing.T) {
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1})
		// The first insert renders the shape and caches it; the second
		// only collects its arguments.
		x := jDoc{ID: 1}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		x.Attrs = map[string]any{"f": func() {}}
		err := db.Insert(&x).Exec(ctx)
		if err == nil || !strings.Contains(err.Error(), "jDoc") || !strings.Contains(err.Error(), "attrs") {
			t.Fatalf("got %v, want an error naming the entity and the column", err)
		}
	})
	t.Run("AddRefused", func(t *testing.T) {
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1})
		st := &velox.Statement{Op: velox.OpUpdate, Entity: velox.InfoOf[jDoc](), Set: []velox.Assignment{{Column: 2, Op: velox.AssignAdd, Value: 1}}, AllRows: true}
		if _, _, err := velox.DryRun(db, ctx, st); err == nil || !strings.Contains(err.Error(), "tags") || !strings.Contains(err.Error(), "an encoded column") {
			t.Fatalf("Add on a JSON column: %v", err)
		}
	})
}

// jRow is a stored jDoc row: tags as []byte, attrs as string (drivers hand
// JSON over as either), shape as []byte, nums NULL.
func jRow(id int64) []any {
	return []any{id, int64(2), []byte(`["a"]`), `{"k":"v"}`, []byte(`{"w":1,"h":2}`), nil}
}

func jWant(id int64) jDoc {
	return jDoc{ID: id, OwnerID: 2, Tags: []string{"a"}, Attrs: map[string]any{"k": "v"}, Shape: jShape{1, 2}}
}

// TestJSONReads: every read decodes a JSON column into its field, from
// []byte or string; NULL leaves the field at its zero value.
func TestJSONReads(t *testing.T) {
	t.Run("All", func(t *testing.T) {
		e := &velox.FakeExec{Rows: [][]any{jRow(1), {int64(2), int64(2), nil, nil, nil, []byte(`[1,2]`)}}}
		db := velox.OpenFake(velox.Postgres, e)
		xs, err := db.Query[jDoc]().Limit(2).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if want := (jDoc{ID: 2, OwnerID: 2, Nums: ints(1, 2)}); len(xs) != 2 || !reflect.DeepEqual(xs[0], jWant(1)) || !reflect.DeepEqual(xs[1], want) {
			t.Fatalf("All read %+v", xs)
		}
	})
	t.Run("Columns", func(t *testing.T) {
		e := &velox.FakeExec{Rows: [][]any{{"[\"z\"]", int64(4)}}}
		db := velox.OpenFake(velox.Postgres, e)
		xs, err := db.Query[jDoc]().Columns(jDocs.Tags, jDocs.ID).Limit(1).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(xs) != 1 || !reflect.DeepEqual(xs[0], jDoc{ID: 4, Tags: []string{"z"}}) {
			t.Fatalf("Columns read %+v", xs)
		}
	})
	t.Run("One", func(t *testing.T) {
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{jRow(1)}})
		x, err := db.Query[jDoc]().Where(jDocs.ID.EQ(1)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*x, jWant(1)) {
			t.Fatalf("One read %+v", x)
		}
	})
	t.Run("Iter", func(t *testing.T) {
		db := velox.OpenFake(velox.SQLite, &velox.FakeExec{Rows: [][]any{jRow(1), jRow(2)}})
		var got []jDoc
		for x, err := range db.Query[jDoc]().Limit(2).Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, x)
		}
		if len(got) != 2 || !reflect.DeepEqual(got[0], jWant(1)) || !reflect.DeepEqual(got[1], jWant(2)) {
			t.Fatalf("Iter read %+v", got)
		}
	})
	t.Run("With", func(t *testing.T) {
		e := &velox.FakeExec{Results: [][][]any{{{int64(2)}}, {jRow(1), jRow(3)}}}
		db := velox.OpenFake(velox.Postgres, e)
		owners, err := db.Query[jOwner]().With(jOwners.Docs).Limit(1).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		docs, ok := owners[0].Docs.Get()
		if !ok || len(docs) != 2 || !reflect.DeepEqual(docs[0], jWant(1)) || !reflect.DeepEqual(docs[1], jWant(3)) {
			t.Fatalf("With loaded %+v", docs)
		}
	})
	t.Run("Returning", func(t *testing.T) {
		// RETURNING reads the generated key back into an entity that has
		// JSON columns; the key is never JSON, so it is scanned as it is.
		e := &velox.FakeExec{Rows: [][]any{{int64(9)}}}
		db := velox.OpenFake(velox.Postgres, e)
		x, want := jFull()
		x.ID = 0
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(e.SQL, `RETURNING "id"`) || x.ID != 9 {
			t.Fatalf("RETURNING: %s, ID %d", e.SQL, x.ID)
		}
		sameArgs(t, "Insert RETURNING", e.Args, want[1:])
		y, _ := jFull()
		y.ID = 9
		if !reflect.DeepEqual(x, y) {
			t.Errorf("the entity after RETURNING: %+v", x)
		}
	})
	t.Run("Invalid", func(t *testing.T) {
		for _, bad := range []any{[]byte(`{"k":`), `not json`, []byte(`"a string"`)} {
			row := jRow(1)
			row[3] = bad
			db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{row}})
			_, err := db.Query[jDoc]().Limit(1).All(ctx)
			if err == nil || !strings.Contains(err.Error(), "jDoc") || !strings.Contains(err.Error(), "attrs") {
				t.Errorf("%s: got %v, want an error naming the entity and the column", bad, err)
			}
		}
	})
}

// TestJSONPredicatesRefused: a condition comparing a JSON column fails when
// the statement runs, naming the column; IsNull and NotNull work.
func TestJSONPredicatesRefused(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	refused := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), `tags`) || !strings.Contains(err.Error(), "an encoded column") {
			t.Errorf("%s: got %v, want the column refused", what, err)
		}
	}
	_, err := db.Query[jDoc]().Where(jDocCols.Tags.EQ([]string{"a"})).Limit(1).All(ctx)
	refused("EQ", err)
	_, _, err = db.Query[jDoc]().Where(jDocCols.Tags.NEQ(nil)).Limit(1).SQL(ctx)
	refused("NEQ under SQL", err)
	_, err = db.Query[jDoc]().Where(velox.Or(jDocs.ID.EQ(1), jDocCols.Tags.In([]string{"a"}))).Limit(1).All(ctx)
	refused("In under Or", err)
	_, err = db.Update[jDoc]().Set(jDocs.OwnerID.Set(1)).Where(jDocCols.Tags.NotIn([]string{"a"})).Exec(ctx)
	refused("NotIn in an update", err)
	_, err = db.Delete[jDoc]().Where(jDocCols.Tags.EQ(nil)).Exec(ctx)
	refused("EQ in a delete", err)
	_, err = db.Query[jDoc]().Where(jDocCols.Tags.EQ([]string{"a"})).Limit(1).Count(ctx)
	refused("EQ in a Count", err)
	hooked := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.ColumnEQ(2, []string{"a"}))
		return nil
	}))
	_, err = hooked.Query[jDoc]().Limit(1).All(ctx)
	refused("a Rewrite hook's ColumnEQ", err)
	_, err = db.Query[jOwner]().Where(jOwners.Docs.HasWhere(jDocCols.Tags.EQ([]string{"a"}))).Limit(1).All(ctx)
	refused("EQ under HasWhere", err)

	e.Rows = nil
	if _, err := db.Query[jDoc]().Where(jDocs.Nums.IsNull()).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `"nums" IS NULL`) {
		t.Errorf("IsNull: %s", e.SQL)
	}
	if _, err := db.Query[jDoc]().Where(jDocs.Nums.NotNull()).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `"nums" IS NOT NULL`) {
		t.Errorf("NotNull: %s", e.SQL)
	}
}

// jsonAllocs is the allocations of a one-row read of a jDoc whose four JSON
// columns hold values, measured: 18. Hand-written code that scans each JSON
// column into a []byte and decodes it makes 16: velox's +2 is its one-row
// budget's (TestOneRowAllocationBudget), as its one scanner per JSON column
// costs what hand-written code's []byte per column does.
const jsonAllocs = 18

// TestJSONAllocations pins the cost of a JSON column: a one-row read makes at
// most one allocation per JSON column beyond what hand-written code that
// decodes the same columns makes, over velox's one-row budget (+2 over
// hand-written code, TestOneRowAllocationBudget).
func TestJSONAllocations(t *testing.T) {
	row := []any{int64(1), int64(2), []byte(`["a"]`), []byte(`{"k":1}`), []byte(`{"w":1,"h":2}`), []byte(`[1]`)}
	e := &velox.FakeExec{Rows: [][]any{row}}
	db := velox.OpenFake(velox.Postgres, e)
	run := func() {
		if _, err := db.Query[jDoc]().Where(jDocs.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run() // fill the render cache
	vx := testing.AllocsPerRun(200, run)
	text, args := e.SQL, e.Args
	raw := testing.AllocsPerRun(200, func() {
		r, err := velox.RawRows(db, text, args)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]jDoc, 0, 1)
		for r.Next() {
			out = append(out, jDoc{})
			x := &out[len(out)-1]
			var tags, attrs, shape, nums []byte
			if err := r.Scan(&x.ID, &x.OwnerID, &tags, &attrs, &shape, &nums); err != nil {
				t.Fatal(err)
			}
			if err := errors.Join(json.Unmarshal(tags, &x.Tags), json.Unmarshal(attrs, &x.Attrs),
				json.Unmarshal(shape, &x.Shape), json.Unmarshal(nums, &x.Nums)); err != nil {
				t.Fatal(err)
			}
		}
		r.Close()
	})
	t.Logf("a one-row read with 4 JSON columns: velox %.0f allocations, hand-written %.0f", vx, raw)
	if vx > raw+2+4 {
		t.Errorf("velox makes %.0f allocations and hand-written code %.0f: over the one-row budget of +2 and one per JSON column", vx, raw)
	}
	if vx > jsonAllocs && !raceEnabled {
		t.Errorf("a one-row read with 4 JSON columns makes %.0f allocations, over the %d pinned", vx, jsonAllocs)
	}
}

// jList's Words column has a codec of its own, not JSON: the words joined
// by commas inside angle brackets.
type jList struct {
	ID    int64
	Words []string
}

var jLists = struct {
	ID    velox.NumberCol[jList, int64]
	Words velox.Col[jList, []string]
}{velox.NumberCol[jList, int64]{I: 0}, velox.Col[jList, []string]{I: 1}}

var jListMeta velox.MetaOnce[jList]

func (*jList) VeloxMeta() *velox.Meta[jList] {
	return jListMeta.Load(func() velox.Desc[jList] {
		return velox.Desc[jList]{
			Table: "j_lists",
			Columns: []velox.ColumnDesc{
				{Field: "ID", Name: "id"},
				{Field: "Words", Name: "words", Encode: wrapWords, Decode: unwrapWords},
			},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *jList, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Words
			},
		}
	})
}

func wrapWords(v any) ([]byte, error) {
	ws, ok := v.([]string)
	if !ok {
		return nil, fmt.Errorf("not words: %T", v)
	}
	return []byte("<" + strings.Join(ws, ",") + ">"), nil
}

func unwrapWords(data []byte, v any) error {
	s, pre := strings.CutPrefix(string(data), "<")
	s, suf := strings.CutSuffix(s, ">")
	if !pre || !suf {
		return errors.New("not wrapped")
	}
	*v.(*[]string) = strings.Split(s, ",")
	return nil
}

// TestCustomCodec: a hand-written Desc may give a column a codec other than
// JSON's; velox binds what it encodes and decodes what it reads with it.
func TestCustomCodec(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.SQLite, e)
	x := jList{ID: 1, Words: []string{"a", "b"}}
	if err := db.Insert(&x).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	sameArgs(t, "Insert", e.Args, []any{int64(1), "<a,b>"})
	if _, err := db.Update[jList]().Set(jLists.Words.Set([]string{"c"})).Where(jLists.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	sameArgs(t, "Update", e.Args, []any{"<c>", int64(1)})
	e.Rows = [][]any{{int64(1), "<x,y>"}, {int64(2), []byte("<z>")}}
	xs, err := db.Query[jList]().Limit(2).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(xs) != 2 || !reflect.DeepEqual(xs[0].Words, []string{"x", "y"}) || !reflect.DeepEqual(xs[1].Words, []string{"z"}) {
		t.Fatalf("read %+v", xs)
	}
	e.Rows = [][]any{{int64(1), "x,y"}}
	if _, err := db.Query[jList]().Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "words") || !strings.Contains(err.Error(), "not wrapped") {
		t.Fatalf("an undecodable value: %v", err)
	}
	if _, err := db.Query[jList]().Where(jLists.Words.EQ([]string{"a"})).Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "words") {
		t.Fatalf("a condition on the encoded column: %v", err)
	}
}

// TestCodecNeedsBoth: a column with one of Encode and Decode is refused when
// the metadata is built, naming the column.
func TestCodecNeedsBoth(t *testing.T) {
	for _, c := range []velox.ColumnDesc{
		{Field: "Words", Name: "words", Encode: wrapWords},
		{Field: "Words", Name: "words", Decode: unwrapWords},
	} {
		_, err := velox.LoadDesc(velox.Desc[jList]{
			Table:      "j_lists",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, c},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *jList, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Words
			},
		})
		var se *velox.SchemaError
		if !errors.As(err, &se) || se.Field != "Words" || !strings.Contains(err.Error(), "words") {
			t.Errorf("got %v, want a *SchemaError naming the column", err)
		}
	}
	if info := velox.InfoOf[jList](); info.Columns[0].Codec || !info.Columns[1].Codec {
		t.Errorf("Codec is not derived from the codec: %+v", info.Columns)
	}
}

// jStamp has an encoded column that every update writes: its UpdateDefault.
type jStamp struct {
	ID   int64
	Hist []string
}

var jStamps = struct {
	ID   velox.NumberCol[jStamp, int64]
	Hist velox.Col[jStamp, []string]
}{velox.NumberCol[jStamp, int64]{I: 0}, velox.Col[jStamp, []string]{I: 1}}

var jStampMeta velox.MetaOnce[jStamp]

func (*jStamp) VeloxMeta() *velox.Meta[jStamp] {
	return jStampMeta.Load(func() velox.Desc[jStamp] {
		return velox.Desc[jStamp]{
			Table: "j_stamps",
			Columns: []velox.ColumnDesc{
				{Field: "ID", Name: "id"},
				{Field: "Hist", Name: "hist", Encode: json.Marshal, Decode: json.Unmarshal, UpdateDefault: func() any { return []string{"u"} }},
			},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *jStamp, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Hist
			},
		}
	})
}

// TestJSONUpdateDefault: an update fills the encoded column with its
// UpdateDefault, encoded; naming the column keeps the default out.
func TestJSONUpdateDefault(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Update[jStamp]().Set(jStamps.ID.Set(2)).Where(jStamps.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `"hist" = `) {
		t.Fatalf("the UpdateDefault did not fire: %s", e.SQL)
	}
	sameArgs(t, "UpdateDefault", e.Args, []any{int64(2), []byte(`["u"]`), int64(1)})
	if _, err := db.Update[jStamp]().Set(jStamps.Hist.Set([]string{"x"})).Where(jStamps.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	sameArgs(t, "an explicit value", e.Args, []any{[]byte(`["x"]`), int64(1)})
	x := jStamp{ID: 1}
	if err := db.Save(ctx, &x, jStamps.ID); err != nil {
		t.Fatal(err)
	}
	sameArgs(t, "Save", e.Args, []any{int64(1), []byte(`["u"]`), int64(1)})
}

// TestCodecFlagWithoutCodec: an EntityInfo a hook built with Codec set on a
// column that has no codec fails the write, naming the column, as does a
// value a hook set to an untyped nil in a column that has one: NULL.
func TestCodecFlagWithoutCodec(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	in := *velox.InfoOf[jDoc]()
	in.Columns = slices.Clone(in.Columns)
	in.Columns[1].Codec = true // owner_id: no Encode or Decode
	st := &velox.Statement{Op: velox.OpUpdate, Entity: &in, Set: []velox.Assignment{{Column: 1, Op: velox.AssignSet, Value: int64(7)}}, AllRows: true}
	_, _, err := velox.DryRun(db, ctx, st)
	if err == nil || !strings.Contains(err.Error(), "owner_id") || !strings.Contains(err.Error(), "no Encode and Decode") {
		t.Fatalf("got %v, want an error naming the column and the missing codec", err)
	}
	// An untyped nil binds NULL, in a column that is not nullable too.
	st = &velox.Statement{Op: velox.OpUpdate, Entity: velox.InfoOf[jDoc](), Set: []velox.Assignment{{Column: 2, Op: velox.AssignSet, Value: nil}}, AllRows: true}
	_, args, err := velox.DryRun(db, ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	sameArgs(t, "an untyped nil", args, []any{nil})
}

// TestAfterOverAnEncodedColumn: keyset paging ordered by an encoded column
// is refused, naming the order column; a struct column is neither nullable
// nor a slice or map, so it is not caught by the checks before it.
func TestAfterOverAnEncodedColumn(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	last := jDoc{ID: 1}
	for _, c := range []struct {
		name string
		by   velox.Order[jDoc]
	}{{"shape", jDocCols.Shape.Asc()}, {"tags", jDocCols.Tags.Asc()}, {"nums", jDocCols.Nums.Asc()}} {
		name := c.name
		_, _, err := db.Query[jDoc]().OrderBy(c.by, jDocs.ID.Asc()).After(&last).Limit(2).SQL(ctx)
		if err == nil || !strings.Contains(err.Error(), "After") || !strings.Contains(err.Error(), "column "+name) || !strings.Contains(err.Error(), "an encoded column") {
			t.Errorf("%s: got %v, want After refused naming the column", name, err)
		}
	}
	if _, _, err := db.Query[jDoc]().OrderBy(jDocs.ID.Asc()).After(&last).Limit(2).SQL(ctx); err != nil {
		t.Errorf("paging over the key: %v", err)
	}
}

// TestJSONBindsPerDialect: an encoded value is bound as a string on SQLite,
// so that the column holds TEXT (a []byte would be stored as a BLOB, which
// SQLite's JSON functions read as JSONB), and on MySQL, whose driver with
// interpolateParams=true writes a []byte as a binary string that a JSON
// column refuses; as []byte on PostgreSQL. NULL stays nil.
func TestJSONBindsPerDialect(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(d, e)
		x, want := jFull()
		wantAll := append(append([]any(nil), want...), want...)
		wantAll[len(want)] = int64(2) // the second row's key
		if d != velox.Postgres {
			want, wantAll = bound(want), bound(wantAll)
		}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sameArgs(t, d.String()+" Insert", e.Args, want)
		y := x
		y.ID = 2
		if err := db.InsertAll([]jDoc{x, y}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sameArgs(t, d.String()+" InsertAll", e.Args, wantAll)
		if _, err := db.Update[jDoc]().Set(jDocs.Tags.Set([]string{"x"}), jDocs.Nums.SetNull()).Where(jDocs.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantUp := []any{[]byte(`["x"]`), nil, int64(1)}
		if d != velox.Postgres {
			wantUp = bound(wantUp)
		}
		sameArgs(t, d.String()+" Update", e.Args, wantUp)
	}
}

// jBin has a column with a binary codec, whose bytes are not UTF-8.
type jBin struct {
	ID  int64
	Raw []byte
}

var jBins = struct {
	ID  velox.NumberCol[jBin, int64]
	Raw velox.JSONCol[jBin, []byte]
}{velox.NumberCol[jBin, int64]{I: 0}, velox.JSONCol[jBin, []byte]{I: 1}}

var jBinMeta velox.MetaOnce[jBin]

func (*jBin) VeloxMeta() *velox.Meta[jBin] {
	return jBinMeta.Load(func() velox.Desc[jBin] {
		return velox.Desc[jBin]{
			Table: "j_bins",
			Columns: []velox.ColumnDesc{
				{Field: "ID", Name: "id"},
				{Field: "Raw", Name: "raw",
					// Encode prefixes 0xff, never valid UTF-8.
					Encode: func(v any) ([]byte, error) { return append([]byte{0xff}, v.([]byte)...), nil },
					Decode: func(data []byte, v any) error { *v.(*[]byte) = append([]byte(nil), data[1:]...); return nil }},
			},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *jBin, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Raw
			},
		}
	})
}

// TestBinaryCodecStaysBytes: bytes that are not valid UTF-8, which only a
// binary codec returns, are bound as []byte on every dialect (a BLOB on
// SQLite), never as a string that is not text; valid UTF-8 from the same
// codec is a string on SQLite and MySQL.
func TestBinaryCodecStaysBytes(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(d, e)
		x := jBin{ID: 1, Raw: []byte("ab")}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sameArgs(t, d.String()+" Insert", e.Args, []any{int64(1), []byte("\xffab")})
		if _, err := db.Update[jBin]().Set(jBins.Raw.Set([]byte("c"))).Where(jBins.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sameArgs(t, d.String()+" Update", e.Args, []any{[]byte("\xffc"), int64(1)})
	}
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.SQLite, e)
	if _, err := db.Update[jList]().Set(jLists.Words.Set([]string{"é"})).Where(jLists.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	sameArgs(t, "valid UTF-8", e.Args, []any{"<é>", int64(1)})
}

// TestJSONHandles: velox-gen's JSONCol and NullJSONCol are a Column[E], so
// they name columns in Columns, Omit, Save and an upsert; they set the
// column, and NullJSONCol sets and tests NULL.
func TestJSONHandles(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Update[jDoc]().Set(jDocs.Tags.Set([]string{"x"}), jDocs.Shape.Set(jShape{1, 2}), jDocs.Nums.SetNull()).Where(jDocs.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	sameArgs(t, "Set and SetNull", e.Args, []any{[]byte(`["x"]`), []byte(`{"w":1,"h":2}`), nil, int64(1)})
	x, _ := jFull()
	if err := db.Save(ctx, &x, jDocs.Attrs, jDocs.Nums); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.SQL, `UPDATE "j_docs" SET "attrs" = $1, "nums" = $2 WHERE "id" = $3`) {
		t.Errorf("Save SQL %s", e.SQL)
	}
	if err := db.Insert(&x).OnConflict(jDocs.ID).Update(jDocs.Tags, jDocs.Nums).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `"tags" = EXCLUDED."tags"`) || !strings.Contains(e.SQL, `"nums" = EXCLUDED."nums"`) {
		t.Errorf("upsert SQL %s", e.SQL)
	}
	if _, _, err := db.Query[jDoc]().Columns(jDocs.ID, jDocs.Tags).Where(jDocs.Nums.IsNull()).Limit(1).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	q, _, err := db.Query[jDoc]().Columns(jDocs.ID, jDocs.Tags).Where(jDocs.Nums.IsNull()).Limit(1).SQL(ctx)
	if err != nil || !strings.HasPrefix(q, `SELECT "id", "tags" FROM "j_docs" WHERE "nums" IS NULL`) {
		t.Errorf("Columns and IsNull: %s, %v", q, err)
	}
	q, _, err = db.Query[jDoc]().Omit(jDocs.Attrs, jDocs.Shape).Where(jDocs.Nums.NotNull()).Limit(1).SQL(ctx)
	if err != nil || strings.Contains(q, "attrs") || strings.Contains(q, "shape") || !strings.Contains(q, `"nums" IS NOT NULL`) {
		t.Errorf("Omit and NotNull: %s, %v", q, err)
	}
}

// TestOrderByAnEncodedColumn: ordering by an encoded column is refused when
// the statement runs, naming the column, as the order of encoded values
// differs by dialect; so is ordering a relation's load by one.
func TestOrderByAnEncodedColumn(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	refused := func(what, col string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "column "+col) || !strings.Contains(err.Error(), "order by") {
			t.Errorf("%s: got %v, want ordering by %s refused", what, err, col)
		}
	}
	_, _, err := db.Query[jDoc]().OrderBy(jDocCols.Tags.Asc()).Limit(1).SQL(ctx)
	refused("Asc", "tags", err)
	_, err = db.Query[jDoc]().OrderBy(jDocs.ID.Asc(), jDocCols.Shape.Desc()).Limit(1).All(ctx)
	refused("Desc after the key", "shape", err)
	_, err = db.Query[jDoc]().OrderBy(jDocCols.Nums.Asc()).Limit(1).All(ctx)
	refused("a nullable column", "nums", err)
	e.Rows = [][]any{{int64(1)}}
	_, err = db.Query[jOwner]().With(jOwners.Docs.OrderBy(jDocCols.Tags.Asc())).Limit(1).All(ctx)
	refused("a relation's load", "tags", err)
}

// jKey is an entity whose key columns a test gives a codec, which the
// metadata refuses. Kids is keyed jKey.parent = jKid.ref.
type jKey struct {
	ID     int64
	Org    int64
	Parent int64
	Kids   velox.Many[jKid]
}

// jKeyDesc is jKey's Desc, with a codec on the columns codec names.
func jKeyDesc(tenant int, codec ...int) velox.Desc[jKey] {
	cols := []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Org", Name: "org"}, {Field: "Parent", Name: "parent"}}
	for _, c := range codec {
		cols[c].Encode, cols[c].Decode = json.Marshal, json.Unmarshal
	}
	return velox.Desc[jKey]{
		Table: "j_keys", Columns: cols, PrimaryKey: []int{0}, Tenant: tenant,
		Relations: []velox.Relation{velox.EdgeTo[jKid]("Kids", 2, 1)},
		Addr: func(x *jKey, i int) any {
			return [...]any{&x.ID, &x.Org, &x.Parent}[i]
		},
		RelAddr: func(x *jKey, i int) any { return &x.Kids },
	}
}

// jKid is the target of jKey's Kids, keyed by ref, an encoded column.
type jKid struct {
	ID  int64
	Ref int64
}

var jKidMeta velox.MetaOnce[jKid]

func (*jKid) VeloxMeta() *velox.Meta[jKid] {
	return jKidMeta.Load(func() velox.Desc[jKid] {
		return velox.Desc[jKid]{
			Table:      "j_kids",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Ref", Name: "ref", Encode: json.Marshal, Decode: json.Unmarshal}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *jKid, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Ref
			},
		}
	})
}

// TestEncodedKeyRefused: a primary-key, tenant or relation-key column with a
// codec is refused when the metadata is built, naming the column, as every
// Save, scope and load compares it; so is a relation keyed by its target's
// encoded column, when the relation is resolved.
func TestEncodedKeyRefused(t *testing.T) {
	if _, err := velox.LoadDesc(jKeyDesc(1)); err != nil {
		t.Fatalf("without a codec: %v", err)
	}
	for _, c := range []struct {
		name          string
		tenant, col   int
		field, reason string
	}{
		{"primary key", -1, 0, "ID", "primary key"},
		{"tenant", 1, 1, "Org", "tenant"},
		{"relation key", -1, 2, "Parent", "relation Kids"},
	} {
		_, err := velox.LoadDesc(jKeyDesc(c.tenant, c.col))
		var se *velox.SchemaError
		if !errors.As(err, &se) || se.Field != c.field || !strings.Contains(err.Error(), c.reason) || !strings.Contains(err.Error(), "codec") {
			t.Errorf("%s: got %v, want a *SchemaError naming %s", c.name, err, c.field)
		}
	}
	_, err := velox.LoadDesc(jKeyDesc(-1), velox.TargetOf[jKid]())
	if err == nil || !strings.Contains(err.Error(), "jKid.Ref") || !strings.Contains(err.Error(), "encoded column") {
		t.Errorf("a target's encoded key: got %v", err)
	}
}
