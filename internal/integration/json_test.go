package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
)

// The JSON fields' model, written by hand as velox-gen writes a Desc: a
// record with a column of each kind of JSON value, a slice, a map, a struct
// and a nullable pointer to a slice. The table holds jsonb on PostgreSQL,
// JSON on MySQL and TEXT on SQLite (jsonDDL).

type jShape struct {
	W int    `json:"w"`
	H int    `json:"h"`
	N string `json:"n,omitempty"`
}

type jrec struct {
	ID    int64
	Tags  []string
	Attrs map[string]any
	Shape jShape
	Nums  *[]int
}

var JR = struct {
	ID    velox.NumberCol[jrec, int64]
	Tags  velox.JSONCol[jrec, []string]
	Attrs velox.JSONCol[jrec, map[string]any]
	Shape velox.JSONCol[jrec, jShape]
	Nums  velox.NullJSONCol[jrec, []int]
}{
	velox.NumberCol[jrec, int64]{I: 0}, velox.JSONCol[jrec, []string]{I: 1}, velox.JSONCol[jrec, map[string]any]{I: 2},
	velox.JSONCol[jrec, jShape]{I: 3}, velox.NullJSONCol[jrec, []int]{I: 4},
}

var jrecMeta velox.MetaOnce[jrec]

func (*jrec) VeloxMeta() *velox.Meta[jrec] { return jrecMeta.Load(jrecDesc) }

func jrecDesc() velox.Desc[jrec] {
	return velox.Desc[jrec]{
		Table: "json_recs",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "Tags", Name: "tags", Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Attrs", Name: "attrs", Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Shape", Name: "shape", Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Nums", Name: "nums", Encode: json.Marshal, Decode: json.Unmarshal},
		},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *jrec, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Tags
			case 2:
				return &x.Attrs
			case 3:
				return &x.Shape
			case 4:
				return &x.Nums
			}
			return nil
		},
	}
}

// jsonDDL is the table of jrec: the JSON type of the dialect, and a JSON
// document in every column but nums, which may be NULL.
func jsonDDL(d velox.Dialect) string {
	typ := map[velox.Dialect]string{velox.Postgres: "jsonb", velox.MySQL: "JSON", velox.SQLite: "TEXT"}[d]
	id := "BIGINT"
	if d == velox.SQLite {
		id = "INTEGER"
	}
	return fmt.Sprintf("CREATE TABLE json_recs (id %s PRIMARY KEY, tags %[2]s NOT NULL, attrs %[2]s NOT NULL, shape %[2]s NOT NULL, nums %[2]s NULL)", id, typ)
}

// eachJSON runs f on every target, as each does, and also, when MySQL is
// tested, on MySQL through a DSN with interpolateParams=true: the driver then
// writes the arguments into the statement's text, a []byte as a binary
// string, which a JSON column refuses, so velox must bind a string.
func eachJSON(t *testing.T, f func(t *testing.T, tg target)) {
	each(t, f)
	dsn := os.Getenv("VELOX_TEST_MYSQL")
	if dsn == "" {
		return
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	tg := mysqlTarget(t, dsn+sep+"interpolateParams=true", nil)
	tg.name = "mysql-interpolate"
	t.Run(tg.name, func(t *testing.T) {
		setup(t, tg)
		f(t, tg)
	})
}

// jsonSetup creates the table of jrec empty.
func jsonSetup(t *testing.T, tg target) {
	t.Helper()
	tg.exec(t, "DROP TABLE IF EXISTS json_recs")
	tg.exec(t, jsonDDL(tg.d))
	t.Cleanup(func() { tg.exec(t, "DROP TABLE IF EXISTS json_recs") })
}

// rawCol is column col of row id, read through database/sql without velox:
// its text, and whether it is SQL NULL. The Go type the driver handed over is
// logged with the text, when the test fails, since the dialects differ in
// both. A value is checked to be stored in the dialect's JSON type: jsonb on
// PostgreSQL, JSON on MySQL and TEXT on SQLite, so that a table of another
// type does not pass on the strength of its text.
func rawCol(t *testing.T, tg target, col string, id int64) (text string, null bool) {
	t.Helper()
	var v any
	var isNull bool
	sel := rebind(tg.d, fmt.Sprintf("SELECT %s, %[1]s IS NULL FROM json_recs WHERE id = ?", col))
	if err := tg.direct.QueryRowContext(ctx, sel, id).Scan(&v, &isNull); err != nil {
		t.Fatalf("%s: %v", sel, err)
	}
	switch x := v.(type) {
	case nil:
	case string:
		text = x
	case []byte:
		text = string(x)
	default:
		t.Fatalf("raw %s: the driver handed over a %T", col, v)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("raw %-5s id %d: %T %q (SQL NULL: %v)", col, id, v, text, isNull)
		}
	})
	if isNull {
		return text, isNull
	}
	// The stored type. On SQLite, the storage class: velox binds a string,
	// so the column holds TEXT; a []byte would be stored as a BLOB, which
	// SQLite's JSON functions read as JSONB, not as text.
	q, args, want := fmt.Sprintf("SELECT typeof(%s) FROM json_recs WHERE id = ?", col), []any{id}, "text"
	switch tg.d {
	case velox.Postgres:
		q, want = fmt.Sprintf("SELECT pg_typeof(%s)::text FROM json_recs WHERE id = ?", col), "jsonb"
	case velox.MySQL:
		// MySQL has no type of a value, only of its column.
		q, args, want = "SELECT DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() "+
			"AND TABLE_NAME = 'json_recs' AND COLUMN_NAME = ?", []any{col}, "json"
	}
	var typ string
	if err := tg.direct.QueryRowContext(ctx, rebind(tg.d, q), args...).Scan(&typ); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if !strings.EqualFold(typ, want) {
		t.Errorf("%s of %d is stored as %s on %s, want %s", col, id, typ, tg.name, want)
	}
	return text, isNull
}

// wantRaw checks column col of row id holds the JSON document want: the same
// JSON, though not the same text (jsonb and MySQL's JSON normalise it).
func wantRaw(t *testing.T, tg target, col string, id int64, want string) {
	t.Helper()
	text, null := rawCol(t, tg, col, id)
	if null {
		t.Errorf("%s of %d is SQL NULL, want the JSON %s", col, id, want)
		return
	}
	var got, w any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Errorf("%s of %d holds %q, not JSON text: %v", col, id, text, err)
		return
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want %q: %v", want, err)
	}
	if !reflect.DeepEqual(got, w) {
		t.Errorf("%s of %d holds %q, want the JSON %s", col, id, text, want)
	}
	if want == "null" && strings.TrimSpace(text) != "null" {
		t.Errorf("%s of %d holds %q, want the JSON null", col, id, text)
	}
}

func wantSQLNull(t *testing.T, tg target, col string, id int64) {
	t.Helper()
	if text, null := rawCol(t, tg, col, id); !null {
		t.Errorf("%s of %d holds %q, want SQL NULL", col, id, text)
	}
}

func readRec(t *testing.T, tg target, id int64) jrec {
	t.Helper()
	got, err := tg.db.Query[jrec]().Where(JR.ID.EQ(id)).One(ctx)
	if err != nil {
		t.Fatalf("read %d: %v", id, err)
	}
	return *got
}

func wantRec(t *testing.T, got, want jrec) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read %+v (nums %v), want %+v (nums %v)", got, deref(got.Nums), want, deref(want.Nums))
	}
}

func deref(p *[]int) any {
	if p == nil {
		return nil
	}
	return *p
}

func ints(xs ...int) *[]int { return &xs }

// jsonRec is a record with every column set: text that needs escaping, an
// empty slice's neighbour, nesting, a number with a fraction, and a null
// inside the document.
func jsonRec(id int64) jrec {
	return jrec{
		ID:   id,
		Tags: []string{"a", "é<>&\"\\", "😀\u2028"},
		Attrs: map[string]any{
			"n": 1.5, "l": []any{1.0, "two", nil}, "o": map[string]any{"x": nil, "y": true}, "s": "q'\"\\",
		},
		Shape: jShape{W: 3, H: 4, N: "sq"},
		Nums:  ints(5, 6),
	}
}

// TestJSONInsertAndRead: a record with every column set is stored as JSON
// text in the dialect's type and reads back equal.
func TestJSONInsertAndRead(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		x := jsonRec(1)
		if err := tg.db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantRec(t, readRec(t, tg, 1), jsonRec(1))
		wantRaw(t, tg, "tags", 1, `["a","é<>&\"\\","😀\u2028"]`)
		wantRaw(t, tg, "attrs", 1, `{"n":1.5,"l":[1,"two",null],"o":{"x":null,"y":true},"s":"q'\"\\"}`)
		wantRaw(t, tg, "shape", 1, `{"w":3,"h":4,"n":"sq"}`)
		wantRaw(t, tg, "nums", 1, `[5,6]`)
		if x.Tags[0] != "a" || len(x.Tags) != 3 {
			t.Errorf("Insert changed the entity: %+v", x)
		}

		// All and Iter decode too, and InsertAll encodes each row.
		rows := []jrec{jsonRec(2), jsonRec(3)}
		rows[1].Tags, rows[1].Nums = []string{"z"}, nil
		if err := tg.db.InsertAll(rows).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		all, err := tg.db.Query[jrec]().OrderBy(JR.ID.Asc()).Unbounded().All(ctx)
		if err != nil || len(all) != 3 {
			t.Fatalf("All: %d rows, %v", len(all), err)
		}
		wantRec(t, all[2], rows[1])
		var n int
		for x, err := range tg.db.Query[jrec]().OrderBy(JR.ID.Asc()).Unbounded().Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			n++
			wantRec(t, x, all[n-1])
		}
		if n != 3 {
			t.Errorf("Iter saw %d rows", n)
		}
	})
}

// TestJSONNullAndNull is Review Focus 5: a nil pointer is SQL NULL, a nil
// slice or map in a field that is not a pointer is the JSON null, and each
// reads back as nil. They are different values in the database.
func TestJSONNullAndNull(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		x := jrec{ID: 1} // nil slice and map, zero struct, nil pointer
		if err := tg.db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantRaw(t, tg, "tags", 1, "null")
		wantRaw(t, tg, "attrs", 1, "null")
		wantRaw(t, tg, "shape", 1, `{"w":0,"h":0}`)
		wantSQLNull(t, tg, "nums", 1)
		wantRec(t, readRec(t, tg, 1), jrec{ID: 1})

		// Empty and nil differ in a field that is not a pointer: [] and null.
		y := jrec{ID: 2, Tags: []string{}, Attrs: map[string]any{}, Nums: new([]int)} // a pointer to a nil slice: NULL
		if err := tg.db.Insert(&y).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantRaw(t, tg, "tags", 2, `[]`)
		wantRaw(t, tg, "attrs", 2, `{}`)
		wantSQLNull(t, tg, "nums", 2)
		got := readRec(t, tg, 2)
		if got.Tags == nil || len(got.Tags) != 0 || got.Attrs == nil || len(got.Attrs) != 0 || got.Nums != nil {
			t.Errorf("read %+v (nums %v), want empty and non-nil, nums nil", got, deref(got.Nums))
		}

		// A pointer to an empty slice is the JSON [], not NULL.
		z := jrec{ID: 3, Nums: &[]int{}}
		if err := tg.db.Insert(&z).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantRaw(t, tg, "nums", 3, `[]`)
		if got := readRec(t, tg, 3); got.Nums == nil || len(*got.Nums) != 0 {
			t.Errorf("read nums %v, want a pointer to an empty slice", deref(got.Nums))
		}

		// IsNull and NotNull see SQL NULL only, not the JSON null; they are the
		// conditions a JSON column accepts.
		isNull, err := tg.db.Query[jrec]().Where(JR.Nums.IsNull()).OrderBy(JR.ID.Asc()).Unbounded().All(ctx)
		if err != nil || len(isNull) != 2 || isNull[0].ID != 1 || isNull[1].ID != 2 {
			t.Errorf("IsNull: %+v, %v", isNull, err)
		}
		notNull, err := tg.db.Query[jrec]().Where(JR.Nums.NotNull()).Unbounded().All(ctx)
		if err != nil || len(notNull) != 1 || notNull[0].ID != 3 {
			t.Errorf("NotNull: %+v, %v", notNull, err)
		}
	})
}

// TestJSONUpdate: Set encodes, SetNull stores SQL NULL, and a nil slice set
// into a field that is not a pointer stores the JSON null.
func TestJSONUpdate(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		x := jsonRec(1)
		if err := tg.db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		n, err := tg.db.Update[jrec]().Set(
			JR.Tags.Set([]string{"x"}), JR.Attrs.Set(map[string]any{"k": "v"}), JR.Shape.Set(jShape{W: 1, H: 2}), JR.Nums.SetNull(),
		).Where(JR.ID.EQ(1)).Exec(ctx)
		if err != nil || n != 1 {
			t.Fatalf("update: %d, %v", n, err)
		}
		wantRec(t, readRec(t, tg, 1), jrec{ID: 1, Tags: []string{"x"}, Attrs: map[string]any{"k": "v"}, Shape: jShape{W: 1, H: 2}})
		wantRaw(t, tg, "tags", 1, `["x"]`)
		wantSQLNull(t, tg, "nums", 1)

		if _, err := tg.db.Update[jrec]().Set(JR.Nums.Set([]int{7}), JR.Tags.Set(nil)).Where(JR.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantRaw(t, tg, "nums", 1, `[7]`)
		wantRaw(t, tg, "tags", 1, "null")
		if got := readRec(t, tg, 1); got.Tags != nil || got.Nums == nil || (*got.Nums)[0] != 7 {
			t.Errorf("read %+v (nums %v)", got, deref(got.Nums))
		}
		// A nil slice set into the nullable column is NULL, as an insert's is.
		if _, err := tg.db.Update[jrec]().Set(JR.Nums.Set(nil)).Where(JR.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantSQLNull(t, tg, "nums", 1)
	})
}

// TestJSONUpsert: the update branch of an upsert encodes the columns it
// takes from the row proposed, and the insert branch encodes the row.
func TestJSONUpsert(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		first := jsonRec(1)
		if err := tg.db.Insert(&first).OnConflict(JR.ID).Update(JR.Tags, JR.Attrs, JR.Nums).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantRec(t, readRec(t, tg, 1), jsonRec(1))

		over := jrec{ID: 1, Tags: []string{"new"}, Attrs: map[string]any{"u": 2.0}, Shape: jShape{W: 9, H: 9}, Nums: nil}
		if err := tg.db.Insert(&over).OnConflict(JR.ID).Update(JR.Tags, JR.Attrs, JR.Nums).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		// The shape is not in the update branch: it keeps the stored value.
		want := over
		want.Shape = jsonRec(1).Shape
		wantRec(t, readRec(t, tg, 1), want)
		wantRaw(t, tg, "tags", 1, `["new"]`)
		wantRaw(t, tg, "attrs", 1, `{"u":2}`)
		wantSQLNull(t, tg, "nums", 1)

		keep := jrec{ID: 1, Tags: []string{"ignored"}}
		if err := tg.db.Insert(&keep).OnConflict(JR.ID).DoNothing().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantRaw(t, tg, "tags", 1, `["new"]`)
	})
}

// TestJSONSave: Save writes the columns named, encoded, from the entity's Go
// values.
func TestJSONSave(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		x := jsonRec(1)
		if err := tg.db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		x.Tags = append(x.Tags, "more")
		x.Attrs = nil
		x.Shape = jShape{W: 8}
		x.Nums = nil
		if err := tg.db.Save(&x, JR.Tags, JR.Attrs, JR.Nums).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		// The shape was not named: it keeps the stored value.
		want := x
		want.Shape = jsonRec(1).Shape
		wantRec(t, readRec(t, tg, 1), want)
		wantRaw(t, tg, "tags", 1, `["a","é<>&\"\\","😀\u2028","more"]`)
		wantRaw(t, tg, "attrs", 1, "null")
		wantSQLNull(t, tg, "nums", 1)
		if err := tg.db.Save(&x, JR.Shape, JR.Nums).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantRaw(t, tg, "shape", 1, `{"w":8,"h":0}`)
	})
}

// TestJSONConditions: a condition comparing a JSON column, or an order by
// one, is refused by the statement, on every dialect, before the database
// sees it.
func TestJSONConditions(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		x := jsonRec(1)
		if err := tg.db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		// JR's JSONCol has no EQ: Col handles, as a hand-written Desc might
		// declare, reach the statement's refusal.
		tags, shape := velox.Col[jrec, []string]{I: 1}, velox.Col[jrec, jShape]{I: 3}
		_, err := tg.db.Query[jrec]().Where(tags.EQ([]string{"a"})).Unbounded().All(ctx)
		if err == nil || !strings.Contains(err.Error(), "tags") || !strings.Contains(err.Error(), "an encoded column") {
			t.Errorf("EQ: got %v, want the column refused", err)
		}
		_, err = tg.db.Delete[jrec]().Where(shape.NEQ(jShape{})).Exec(ctx)
		if err == nil || !strings.Contains(err.Error(), "shape") {
			t.Errorf("NEQ in a delete: got %v", err)
		}
		_, err = tg.db.Query[jrec]().OrderBy(shape.Asc()).Unbounded().All(ctx)
		if err == nil || !strings.Contains(err.Error(), "shape") || !strings.Contains(err.Error(), "order by") {
			t.Errorf("ORDER BY: got %v, want the column refused", err)
		}
		if n, err := tg.db.Query[jrec]().Count(ctx); err != nil || n != 1 {
			t.Errorf("the row is gone: %d, %v", n, err)
		}
	})
}

// TestJSONDecodeError: a stored document that does not fit its field fails
// the read with an error naming the entity and the column, not a panic or a
// silent zero. A document of the wrong type is valid JSON, so every dialect
// stores it.
func TestJSONDecodeError(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		tg.exec(t, `INSERT INTO json_recs (id, tags, attrs, shape, nums) VALUES (1, '[]', '{}', '"not a struct"', NULL)`)
		_, err := tg.db.Query[jrec]().Where(JR.ID.EQ(1)).One(ctx)
		if err == nil || !strings.Contains(err.Error(), "jrec") || !strings.Contains(err.Error(), "shape") {
			t.Errorf("got %v, want an error naming the entity and the column", err)
		}
	})
}

// jopt is the record of an Optional JSON slice and map, as velox-gen writes
// their Desc: Nullable, as their columns hold NULL though the fields are no
// pointers; and fixed, a required slice, whose column does not.
type jopt struct {
	ID    int64
	Tags  []string
	Attrs map[string]any
	Fixed []string
}

var JO = struct {
	ID    velox.NumberCol[jopt, int64]
	Tags  velox.JSONCol[jopt, []string]
	Attrs velox.JSONCol[jopt, map[string]any]
	Fixed velox.JSONCol[jopt, []string]
}{velox.NumberCol[jopt, int64]{I: 0}, velox.JSONCol[jopt, []string]{I: 1}, velox.JSONCol[jopt, map[string]any]{I: 2}, velox.JSONCol[jopt, []string]{I: 3}}

var joptMeta velox.MetaOnce[jopt]

func (*jopt) VeloxMeta() *velox.Meta[jopt] {
	return joptMeta.Load(func() velox.Desc[jopt] {
		return velox.Desc[jopt]{
			Table: "json_opts",
			Columns: []velox.ColumnDesc{
				{Field: "ID", Name: "id"},
				{Field: "Tags", Name: "tags", Nullable: true, Encode: json.Marshal, Decode: json.Unmarshal},
				{Field: "Attrs", Name: "attrs", Nullable: true, Encode: json.Marshal, Decode: json.Unmarshal},
				{Field: "Fixed", Name: "fixed", Encode: json.Marshal, Decode: json.Unmarshal},
			},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *jopt, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.Tags
				case 2:
					return &x.Attrs
				}
				return &x.Fixed
			},
		}
	})
}

// optNull reports whether column col of json_opts' row id is SQL NULL, read
// without velox; and its text when it is not.
func optNull(t *testing.T, tg target, col string, id int64) (bool, string) {
	t.Helper()
	var v any
	q := rebind(tg.d, fmt.Sprintf("SELECT %s FROM json_opts WHERE id = ?", col))
	if err := tg.direct.QueryRowContext(ctx, q, id).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	switch x := v.(type) {
	case nil:
		return true, ""
	case []byte:
		return false, string(x)
	default:
		return false, fmt.Sprint(x)
	}
}

// TestJSONOptionalNil: an Optional JSON slice or map set to nil stores SQL
// NULL, on insert and on update, and reads back as nil; a required one
// stores the JSON null, as before.
func TestJSONOptionalNil(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		typ := map[velox.Dialect]string{velox.Postgres: "jsonb", velox.MySQL: "JSON", velox.SQLite: "TEXT"}[tg.d]
		id := "BIGINT"
		if tg.d == velox.SQLite {
			id = "INTEGER"
		}
		tg.exec(t, "DROP TABLE IF EXISTS json_opts")
		tg.exec(t, fmt.Sprintf("CREATE TABLE json_opts (id %s PRIMARY KEY, tags %[2]s NULL, attrs %[2]s NULL, fixed %[2]s NOT NULL)", id, typ))
		t.Cleanup(func() { tg.exec(t, "DROP TABLE IF EXISTS json_opts") })

		check := func(what string, id int64) {
			t.Helper()
			for _, col := range []string{"tags", "attrs"} {
				if null, text := optNull(t, tg, col, id); !null {
					t.Errorf("%s: %s of %d holds %q, want SQL NULL", what, col, id, text)
				}
			}
			if null, text := optNull(t, tg, "fixed", id); null || strings.TrimSpace(text) != "null" {
				t.Errorf("%s: fixed of %d holds %q (NULL %v), want the JSON null", what, id, text, null)
			}
			got, err := tg.db.Query[jopt]().Where(JO.ID.EQ(id)).One(ctx)
			if err != nil || got.Tags != nil || got.Attrs != nil || got.Fixed != nil {
				t.Errorf("%s: read %+v, %v: want nil values", what, got, err)
			}
		}
		if err := tg.db.Insert(&jopt{ID: 1}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		check("insert", 1)

		if err := tg.db.Insert(&jopt{ID: 2, Tags: []string{"a"}, Attrs: map[string]any{"k": "v"}, Fixed: []string{}}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if null, _ := optNull(t, tg, "tags", 2); null {
			t.Error("tags of 2 is SQL NULL, want [\"a\"]")
		}
		if _, err := tg.db.Update[jopt]().Set(JO.Tags.Set(nil), JO.Attrs.Set(nil), JO.Fixed.Set(nil)).Where(JO.ID.EQ(2)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		check("update", 2)
		if n, err := tg.db.Query[jopt]().Where(JO.ID.EQ(2)).Count(ctx); err != nil || n != 1 {
			t.Errorf("count %d, %v", n, err)
		}
	})
}
