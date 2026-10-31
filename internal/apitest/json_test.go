package apitest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// jDocCols are Col handles of jDoc's encoded columns, as a hand-written Desc
// might declare them: their conditions and orders compile, and the statement
// refuses them, the backstop behind JSONCol, which has none.
var jDocCols = struct {
	Tags  velox.Col[fixture.JDoc, []string]
	Shape velox.Col[fixture.JDoc, fixture.JShape]
	Nums  velox.NullCol[fixture.JDoc, []int]
}{velox.Col[fixture.JDoc, []string]{I: 2}, velox.Col[fixture.JDoc, fixture.JShape]{I: 4}, velox.NullCol[fixture.JDoc, []int]{I: 5}}

// jOwner has many jDocs, to load them through With.
type jOwner struct {
	ID   int64
	Docs velox.Many[fixture.JDoc]
}

var jOwners = struct {
	ID   velox.NumberCol[jOwner, int64]
	Docs velox.Rel[jOwner, fixture.JDoc]
}{velox.NumberCol[jOwner, int64]{I: 0}, velox.Rel[jOwner, fixture.JDoc]{I: 0}}

var jOwnerMeta velox.MetaOnce[jOwner]

func (*jOwner) VeloxMeta() *velox.Meta[jOwner] {
	return jOwnerMeta.Load(func() velox.Desc[jOwner] {
		return velox.Desc[jOwner]{
			Table:      "j_owners",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Edges:      []velox.EdgeDesc{velox.EdgeTo[fixture.JDoc]("Docs", 0, 1)},
			Addr:       func(x *jOwner, i int) any { return &x.ID },
			EdgeAddr:   func(x *jOwner, i int) any { return &x.Docs },
		}
	})
}

// jRow is a stored jDoc row: tags as []byte, attrs as string (drivers hand
// JSON over as either), shape as []byte, nums NULL.
func jRow(id int64) []any {
	return []any{id, int64(2), []byte(`["a"]`), `{"k":"v"}`, []byte(`{"w":1,"h":2}`), nil}
}

func jWant(id int64) fixture.JDoc {
	return fixture.JDoc{ID: id, OwnerID: 2, Tags: []string{"a"}, Attrs: map[string]any{"k": "v"}, Shape: fixture.JShape{W: 1, H: 2}}
}

// TestJSONReads: every read decodes a JSON column into its field, from
// []byte or string; NULL leaves the field at its zero value.
func TestJSONReads(t *testing.T) {
	t.Run("All", func(t *testing.T) {
		e := &fakeconn.Exec{Rows: [][]any{jRow(1), {int64(2), int64(2), nil, nil, nil, []byte(`[1,2]`)}}}
		db := openFake(velox.Postgres, e)
		xs, err := db.Query[fixture.JDoc]().Limit(2).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if want := (fixture.JDoc{ID: 2, OwnerID: 2, Nums: fixture.Ints(1, 2)}); len(xs) != 2 || !reflect.DeepEqual(xs[0], jWant(1)) || !reflect.DeepEqual(xs[1], want) {
			t.Fatalf("All read %+v", xs)
		}
	})
	t.Run("Columns", func(t *testing.T) {
		e := &fakeconn.Exec{Rows: [][]any{{"[\"z\"]", int64(4)}}}
		db := openFake(velox.Postgres, e)
		xs, err := db.Query[fixture.JDoc]().Columns(fixture.JDocs.Tags, fixture.JDocs.ID).Limit(1).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(xs) != 1 || !reflect.DeepEqual(xs[0], fixture.JDoc{ID: 4, Tags: []string{"z"}}) {
			t.Fatalf("Columns read %+v", xs)
		}
	})
	t.Run("One", func(t *testing.T) {
		db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{jRow(1)}})
		x, err := db.Query[fixture.JDoc]().Where(fixture.JDocs.ID.EQ(1)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*x, jWant(1)) {
			t.Fatalf("One read %+v", x)
		}
	})
	t.Run("Iter", func(t *testing.T) {
		db := openFake(velox.SQLite, &fakeconn.Exec{Rows: [][]any{jRow(1), jRow(2)}})
		var got []fixture.JDoc
		for x, err := range db.Query[fixture.JDoc]().Limit(2).Iter(ctx) {
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
		e := &fakeconn.Exec{Results: [][][]any{{{int64(2)}}, {jRow(1), jRow(3)}}}
		db := openFake(velox.Postgres, e)
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
		e := &fakeconn.Exec{Rows: [][]any{{int64(9)}}}
		db := openFake(velox.Postgres, e)
		x, want := fixture.JFull()
		x.ID = 0
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(e.SQL, `RETURNING "id"`) || x.ID != 9 {
			t.Fatalf("RETURNING: %s, ID %d", e.SQL, x.ID)
		}
		fixture.SameArgs(t, "Insert RETURNING", e.Args, want[1:])
		y, _ := fixture.JFull()
		y.ID = 9
		if !reflect.DeepEqual(x, y) {
			t.Errorf("the entity after RETURNING: %+v", x)
		}
	})
	t.Run("Invalid", func(t *testing.T) {
		for _, bad := range []any{[]byte(`{"k":`), `not json`, []byte(`"a string"`)} {
			row := jRow(1)
			row[3] = bad
			db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{row}})
			_, err := db.Query[fixture.JDoc]().Limit(1).All(ctx)
			if err == nil || !strings.Contains(err.Error(), "JDoc") || !strings.Contains(err.Error(), "attrs") {
				t.Errorf("%s: got %v, want an error naming the entity and the column", bad, err)
			}
		}
	})
}

// TestJSONPredicatesRefused: a condition comparing a JSON column fails when
// the statement runs, naming the column; IsNull and NotNull work.
func TestJSONPredicatesRefused(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	refused := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), `tags`) || !strings.Contains(err.Error(), "an encoded column") {
			t.Errorf("%s: got %v, want the column refused", what, err)
		}
	}
	_, err := db.Query[fixture.JDoc]().Where(jDocCols.Tags.EQ([]string{"a"})).Limit(1).All(ctx)
	refused("EQ", err)
	_, _, err = db.Query[fixture.JDoc]().Where(jDocCols.Tags.NEQ(nil)).Limit(1).SQL(ctx)
	refused("NEQ under SQL", err)
	_, err = db.Query[fixture.JDoc]().Where(velox.Or(fixture.JDocs.ID.EQ(1), jDocCols.Tags.In([]string{"a"}))).Limit(1).All(ctx)
	refused("In under Or", err)
	_, err = db.Update[fixture.JDoc]().Set(fixture.JDocs.OwnerID.Set(1)).Where(jDocCols.Tags.NotIn([]string{"a"})).Exec(ctx)
	refused("NotIn in an update", err)
	_, err = db.Delete[fixture.JDoc]().Where(jDocCols.Tags.EQ(nil)).Exec(ctx)
	refused("EQ in a delete", err)
	_, err = db.Query[fixture.JDoc]().Where(jDocCols.Tags.EQ([]string{"a"})).Limit(1).Count(ctx)
	refused("EQ in a Count", err)
	hooked := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.ColumnEQ(2, []string{"a"}))
		return nil
	}))
	_, err = hooked.Query[fixture.JDoc]().Limit(1).All(ctx)
	refused("a Rewrite hook's ColumnEQ", err)
	_, err = db.Query[jOwner]().Where(jOwners.Docs.HasWhere(jDocCols.Tags.EQ([]string{"a"}))).Limit(1).All(ctx)
	refused("EQ under HasWhere", err)

	e.Rows = nil
	if _, err := db.Query[fixture.JDoc]().Where(fixture.JDocs.Nums.IsNull()).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `"nums" IS NULL`) {
		t.Errorf("IsNull: %s", e.SQL)
	}
	if _, err := db.Query[fixture.JDoc]().Where(fixture.JDocs.Nums.NotNull()).Limit(1).All(ctx); err != nil {
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
	e := &fakeconn.Exec{Rows: [][]any{row}}
	db := openFake(velox.Postgres, e)
	run := func() {
		if _, err := db.Query[fixture.JDoc]().Where(fixture.JDocs.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run() // fill the render cache
	vx := testing.AllocsPerRun(200, run)
	text, args := e.SQL, e.Args
	raw := testing.AllocsPerRun(200, func() {
		r, err := e.Query(ctx, text, args)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]fixture.JDoc, 0, 1)
		for r.Next() {
			out = append(out, fixture.JDoc{})
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

var jLists = struct {
	ID    velox.NumberCol[fixture.JList, int64]
	Words velox.Col[fixture.JList, []string]
}{velox.NumberCol[fixture.JList, int64]{I: 0}, velox.Col[fixture.JList, []string]{I: 1}}

// TestCustomCodec: a hand-written Desc may give a column a codec other than
// JSON's; velox binds what it encodes and decodes what it reads with it.
func TestCustomCodec(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.SQLite, e)
	x := fixture.JList{ID: 1, Words: []string{"a", "b"}}
	if err := db.Insert(&x).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.SameArgs(t, "Insert", e.Args, []any{int64(1), "<a,b>"})
	if _, err := db.Update[fixture.JList]().Set(jLists.Words.Set([]string{"c"})).Where(jLists.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.SameArgs(t, "Update", e.Args, []any{"<c>", int64(1)})
	e.Rows = [][]any{{int64(1), "<x,y>"}, {int64(2), []byte("<z>")}}
	xs, err := db.Query[fixture.JList]().Limit(2).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(xs) != 2 || !reflect.DeepEqual(xs[0].Words, []string{"x", "y"}) || !reflect.DeepEqual(xs[1].Words, []string{"z"}) {
		t.Fatalf("read %+v", xs)
	}
	e.Rows = [][]any{{int64(1), "x,y"}}
	if _, err := db.Query[fixture.JList]().Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "words") || !strings.Contains(err.Error(), "not wrapped") {
		t.Fatalf("an undecodable value: %v", err)
	}
	if _, err := db.Query[fixture.JList]().Where(jLists.Words.EQ([]string{"a"})).Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "words") {
		t.Fatalf("a condition on the encoded column: %v", err)
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
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	if _, err := db.Update[jStamp]().Set(jStamps.ID.Set(2)).Where(jStamps.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `"hist" = `) {
		t.Fatalf("the UpdateDefault did not fire: %s", e.SQL)
	}
	fixture.SameArgs(t, "UpdateDefault", e.Args, []any{int64(2), []byte(`["u"]`), int64(1)})
	if _, err := db.Update[jStamp]().Set(jStamps.Hist.Set([]string{"x"})).Where(jStamps.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.SameArgs(t, "an explicit value", e.Args, []any{[]byte(`["x"]`), int64(1)})
	x := jStamp{ID: 1}
	if err := db.Save(&x, jStamps.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.SameArgs(t, "Save", e.Args, []any{int64(1), []byte(`["u"]`), int64(1)})
}

// TestAfterOverAnEncodedColumn: keyset paging ordered by an encoded column
// is refused, naming the order column; a struct column is neither nullable
// nor a slice or map, so it is not caught by the checks before it.
func TestAfterOverAnEncodedColumn(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	last := fixture.JDoc{ID: 1}
	for _, c := range []struct {
		name string
		by   velox.Order[fixture.JDoc]
	}{{"shape", jDocCols.Shape.Asc()}, {"tags", jDocCols.Tags.Asc()}, {"nums", jDocCols.Nums.Asc()}} {
		name := c.name
		_, _, err := db.Query[fixture.JDoc]().OrderBy(c.by, fixture.JDocs.ID.Asc()).After(&last).Limit(2).SQL(ctx)
		if err == nil || !strings.Contains(err.Error(), "After") || !strings.Contains(err.Error(), "column "+name) || !strings.Contains(err.Error(), "an encoded column") {
			t.Errorf("%s: got %v, want After refused naming the column", name, err)
		}
	}
	if _, _, err := db.Query[fixture.JDoc]().OrderBy(fixture.JDocs.ID.Asc()).After(&last).Limit(2).SQL(ctx); err != nil {
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
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(d, e)
		x, want := fixture.JFull()
		wantAll := append(append([]any(nil), want...), want...)
		wantAll[len(want)] = int64(2) // the second row's key
		if d != velox.Postgres {
			want, wantAll = fixture.Bound(want), fixture.Bound(wantAll)
		}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, d.String()+" Insert", e.Args, want)
		y := x
		y.ID = 2
		if err := db.InsertAll([]fixture.JDoc{x, y}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, d.String()+" InsertAll", e.Args, wantAll)
		if _, err := db.Update[fixture.JDoc]().Set(fixture.JDocs.Tags.Set([]string{"x"}), fixture.JDocs.Nums.SetNull()).Where(fixture.JDocs.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantUp := []any{[]byte(`["x"]`), nil, int64(1)}
		if d != velox.Postgres {
			wantUp = fixture.Bound(wantUp)
		}
		fixture.SameArgs(t, d.String()+" Update", e.Args, wantUp)
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
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(d, e)
		x := jBin{ID: 1, Raw: []byte("ab")}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, d.String()+" Insert", e.Args, []any{int64(1), []byte("\xffab")})
		if _, err := db.Update[jBin]().Set(jBins.Raw.Set([]byte("c"))).Where(jBins.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, d.String()+" Update", e.Args, []any{[]byte("\xffc"), int64(1)})
	}
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.SQLite, e)
	if _, err := db.Update[fixture.JList]().Set(jLists.Words.Set([]string{"é"})).Where(jLists.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.SameArgs(t, "valid UTF-8", e.Args, []any{"<é>", int64(1)})
}

// TestJSONHandles: velox-gen's JSONCol and NullJSONCol are a Column[E], so
// they name columns in Columns, Omit, Save and an upsert; they set the
// column, and NullJSONCol sets and tests NULL.
func TestJSONHandles(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	if _, err := db.Update[fixture.JDoc]().Set(fixture.JDocs.Tags.Set([]string{"x"}), fixture.JDocs.Shape.Set(fixture.JShape{W: 1, H: 2}), fixture.JDocs.Nums.SetNull()).Where(fixture.JDocs.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.SameArgs(t, "Set and SetNull", e.Args, []any{[]byte(`["x"]`), []byte(`{"w":1,"h":2}`), nil, int64(1)})
	x, _ := fixture.JFull()
	if err := db.Save(&x, fixture.JDocs.Attrs, fixture.JDocs.Nums).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.SQL, `UPDATE "j_docs" SET "attrs" = $1, "nums" = $2 WHERE "id" = $3`) {
		t.Errorf("Save SQL %s", e.SQL)
	}
	if err := db.Insert(&x).OnConflict(fixture.JDocs.ID).Update(fixture.JDocs.Tags, fixture.JDocs.Nums).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `"tags" = EXCLUDED."tags"`) || !strings.Contains(e.SQL, `"nums" = EXCLUDED."nums"`) {
		t.Errorf("upsert SQL %s", e.SQL)
	}
	if _, _, err := db.Query[fixture.JDoc]().Columns(fixture.JDocs.ID, fixture.JDocs.Tags).Where(fixture.JDocs.Nums.IsNull()).Limit(1).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	q, _, err := db.Query[fixture.JDoc]().Columns(fixture.JDocs.ID, fixture.JDocs.Tags).Where(fixture.JDocs.Nums.IsNull()).Limit(1).SQL(ctx)
	if err != nil || !strings.HasPrefix(q, `SELECT "id", "tags" FROM "j_docs" WHERE "nums" IS NULL`) {
		t.Errorf("Columns and IsNull: %s, %v", q, err)
	}
	q, _, err = db.Query[fixture.JDoc]().Omit(fixture.JDocs.Attrs, fixture.JDocs.Shape).Where(fixture.JDocs.Nums.NotNull()).Limit(1).SQL(ctx)
	if err != nil || strings.Contains(q, "attrs") || strings.Contains(q, "shape") || !strings.Contains(q, `"nums" IS NOT NULL`) {
		t.Errorf("Omit and NotNull: %s, %v", q, err)
	}
}

// TestOrderByAnEncodedColumn: ordering by an encoded column is refused when
// the statement runs, naming the column, as the order of encoded values
// differs by dialect; so is ordering a relation's load by one.
func TestOrderByAnEncodedColumn(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	refused := func(what, col string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "column "+col) || !strings.Contains(err.Error(), "order by") {
			t.Errorf("%s: got %v, want ordering by %s refused", what, err, col)
		}
	}
	_, _, err := db.Query[fixture.JDoc]().OrderBy(jDocCols.Tags.Asc()).Limit(1).SQL(ctx)
	refused("Asc", "tags", err)
	_, err = db.Query[fixture.JDoc]().OrderBy(fixture.JDocs.ID.Asc(), jDocCols.Shape.Desc()).Limit(1).All(ctx)
	refused("Desc after the key", "shape", err)
	_, err = db.Query[fixture.JDoc]().OrderBy(jDocCols.Nums.Asc()).Limit(1).All(ctx)
	refused("a nullable column", "nums", err)
	e.Rows = [][]any{{int64(1)}}
	_, err = db.Query[jOwner]().With(jOwners.Docs.OrderBy(jDocCols.Tags.Asc())).Limit(1).All(ctx)
	refused("a relation's load", "tags", err)
}
