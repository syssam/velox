package velox_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/apitest/fixture"
)

// TestJSONWrites: every write binds a JSON column's value as the bytes of its
// JSON; a nil pointer binds NULL, a nil slice or map in a non-nullable field
// the JSON null. Hooks and the entity keep Go values.
func TestJSONWrites(t *testing.T) {
	t.Run("Insert", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e)
		x, want := fixture.JFull()
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, "Insert", e.Args, want)
		if y, _ := fixture.JFull(); !reflect.DeepEqual(x, y) {
			t.Errorf("Insert changed the entity: %+v", x)
		}
	})
	t.Run("InsertNil", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e)
		x := fixture.JDoc{ID: 1, OwnerID: 2}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, "Insert of nil values", e.Args, []any{int64(1), int64(2), []byte("null"), []byte("null"), []byte(`{"w":0,"h":0}`), nil})
	})
	t.Run("InsertAll", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 2}
		db := velox.OpenFake(velox.MySQL, e)
		x, want := fixture.JFull()
		y := fixture.JDoc{ID: 3, OwnerID: 2, Tags: []string{}, Nums: new([]int)} // a pointer to a nil slice: NULL
		if err := db.InsertAll([]fixture.JDoc{x, y}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		want = append(want, int64(3), int64(2), []byte(`[]`), []byte("null"), []byte(`{"w":0,"h":0}`), nil)
		fixture.SameArgs(t, "InsertAll", e.Args, fixture.Bound(want))
	})
	t.Run("Update", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e)
		_, err := db.Update[fixture.JDoc]().Set(fixture.JDocs.Tags.Set([]string{"x"}), fixture.JDocs.Shape.Set(fixture.JShape{W: 1, H: 2}), fixture.JDocs.Nums.Set([]int{7})).
			Where(fixture.JDocs.ID.EQ(1)).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, "Update", e.Args, []any{[]byte(`["x"]`), []byte(`{"w":1,"h":2}`), []byte(`[7]`), int64(1)})
		if _, err := db.Update[fixture.JDoc]().Set(fixture.JDocs.Nums.SetNull(), fixture.JDocs.Attrs.Set(nil)).Where(fixture.JDocs.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, "Update to NULL", e.Args, []any{nil, []byte("null"), int64(1)})
		if _, err := db.Update[fixture.JDoc]().Set(fixture.JDocs.Nums.Set(nil)).Where(fixture.JDocs.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, "Update of a nullable column to a nil slice", e.Args, []any{nil, int64(1)})
	})
	t.Run("Save", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e)
		x, _ := fixture.JFull()
		if err := db.Save(&x, fixture.JDocs.Tags, fixture.JDocs.Attrs, fixture.JDocs.Nums).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(e.SQL, `UPDATE "j_docs" SET "tags" = $1, "attrs" = $2, "nums" = $3 WHERE "id" = $4`) {
			t.Fatalf("Save SQL %s", e.SQL)
		}
		fixture.SameArgs(t, "Save", e.Args, []any{[]byte(`["a","b"]`), []byte(`{"k":1}`), []byte(`[5,6]`), int64(1)})
		// Save stores back what it assigned: the Go values, not their bytes.
		if y, _ := fixture.JFull(); !reflect.DeepEqual(x, y) {
			t.Errorf("Save stored %+v", x)
		}
		x.Nums = nil
		if err := db.Save(&x, fixture.JDocs.Nums).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, "Save of nil", e.Args, []any{nil, int64(1)})
	})
	t.Run("Upsert", func(t *testing.T) {
		for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
			e := &velox.FakeExec{Affected: 1}
			db := velox.OpenFake(d, e)
			x, want := fixture.JFull()
			if err := db.Insert(&x).OnConflict(fixture.JDocs.ID).Update(fixture.JDocs.Tags, fixture.JDocs.Attrs, fixture.JDocs.Nums).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(e.SQL, "UPDATE") {
				t.Fatalf("%v: no update branch: %s", d, e.SQL)
			}
			if d != velox.Postgres {
				want = fixture.Bound(want)
			}
			fixture.SameArgs(t, d.String()+" upsert", e.Args, want)
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
		x, want := fixture.JFull()
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		goValues := []any{int64(1), int64(2), x.Tags, x.Attrs, x.Shape, x.Nums}
		if !reflect.DeepEqual(rewritten, goValues) || !reflect.DeepEqual(around, goValues) {
			t.Errorf("the hooks saw %v and %v, want the Go values", rewritten, around)
		}
		fixture.SameArgs(t, "Statement.Args", args, want)
		if _, err := db.Update[fixture.JDoc]().Set(fixture.JDocs.Tags.Set([]string{"x"})).Where(fixture.JDocs.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(rewritten, []any{[]string{"x"}}) || !reflect.DeepEqual(around, []any{[]string{"x"}}) {
			t.Errorf("the hooks saw %v and %v, want the Go value", rewritten, around)
		}
		fixture.SameArgs(t, "Statement.Args", args, []any{[]byte(`["x"]`), int64(1)})
	})
	t.Run("SQL", func(t *testing.T) {
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
		x, want := fixture.JFull()
		_, args, err := db.Insert(&x).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.SameArgs(t, "Insert SQL", args, want)
	})
	t.Run("Unencodable", func(t *testing.T) {
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1})
		// The first insert renders the shape and caches it; the second
		// only collects its arguments.
		x := fixture.JDoc{ID: 1}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		x.Attrs = map[string]any{"f": func() {}}
		err := db.Insert(&x).Exec(ctx)
		if err == nil || !strings.Contains(err.Error(), "JDoc") || !strings.Contains(err.Error(), "attrs") {
			t.Fatalf("got %v, want an error naming the entity and the column", err)
		}
	})
	t.Run("AddRefused", func(t *testing.T) {
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1})
		st := &velox.Statement{Op: velox.OpUpdate, Entity: velox.InfoOf[fixture.JDoc](), Set: []velox.Assignment{{Column: 2, Op: velox.AssignAdd, Value: 1}}, AllRows: true}
		if _, _, err := velox.DryRun(ctx, db, st); err == nil || !strings.Contains(err.Error(), "tags") || !strings.Contains(err.Error(), "an encoded column") {
			t.Fatalf("Add on a JSON column: %v", err)
		}
	})
}

// TestCodecNeedsBoth: a column with one of Encode and Decode is refused when
// the metadata is built, naming the column.
func TestCodecNeedsBoth(t *testing.T) {
	for _, c := range []velox.ColumnDesc{
		{Field: "Words", Name: "words", Encode: fixture.WrapWords},
		{Field: "Words", Name: "words", Decode: fixture.UnwrapWords},
	} {
		_, err := velox.LoadDesc(velox.Desc[fixture.JList]{
			Table:      "j_lists",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, c},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *fixture.JList, i int) any {
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
	if info := velox.InfoOf[fixture.JList](); info.Columns[0].Codec || !info.Columns[1].Codec {
		t.Errorf("Codec is not derived from the codec: %+v", info.Columns)
	}
}

// TestCodecFlagWithoutCodec: an EntityInfo a hook built with Codec set on a
// column that has no codec fails the write, naming the column, as does a
// value a hook set to an untyped nil in a column that has one: NULL.
func TestCodecFlagWithoutCodec(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	in := *velox.InfoOf[fixture.JDoc]()
	in.Columns = slices.Clone(in.Columns)
	in.Columns[1].Codec = true // owner_id: no Encode or Decode
	st := &velox.Statement{Op: velox.OpUpdate, Entity: &in, Set: []velox.Assignment{{Column: 1, Op: velox.AssignSet, Value: int64(7)}}, AllRows: true}
	_, _, err := velox.DryRun(ctx, db, st)
	if err == nil || !strings.Contains(err.Error(), "owner_id") || !strings.Contains(err.Error(), "no Encode and Decode") {
		t.Fatalf("got %v, want an error naming the column and the missing codec", err)
	}
	// An untyped nil binds NULL, in a column that is not nullable too.
	st = &velox.Statement{Op: velox.OpUpdate, Entity: velox.InfoOf[fixture.JDoc](), Set: []velox.Assignment{{Column: 2, Op: velox.AssignSet, Value: nil}}, AllRows: true}
	_, args, err := velox.DryRun(ctx, db, st)
	if err != nil {
		t.Fatal(err)
	}
	fixture.SameArgs(t, "an untyped nil", args, []any{nil})
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
		Edges: []velox.EdgeDesc{velox.EdgeTo[jKid]("Kids", 2, 1)},
		Addr: func(x *jKey, i int) any {
			return [...]any{&x.ID, &x.Org, &x.Parent}[i]
		},
		EdgeAddr: func(x *jKey, i int) any { return &x.Kids },
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

// jOpt has the JSON columns of Optional fields, as velox-gen writes them: a
// slice and a map whose ColumnDesc says Nullable, as their columns can hold
// NULL though the fields are no pointers; and a slice that is not. Blob is a
// Nillable bytes field: no codec, and Nullable.
type jOpt struct {
	ID    int64
	Tags  []string
	Attrs map[string]any
	Fixed []string
	Blob  []byte
}

var jOpts = struct {
	ID    velox.NumberCol[jOpt, int64]
	Tags  velox.JSONCol[jOpt, []string]
	Attrs velox.JSONCol[jOpt, map[string]any]
	Fixed velox.JSONCol[jOpt, []string]
	Blob  velox.Col[jOpt, []byte]
}{velox.NumberCol[jOpt, int64]{I: 0}, velox.JSONCol[jOpt, []string]{I: 1}, velox.JSONCol[jOpt, map[string]any]{I: 2}, velox.JSONCol[jOpt, []string]{I: 3}, velox.Col[jOpt, []byte]{I: 4}}

var jOptMeta velox.MetaOnce[jOpt]

func (*jOpt) VeloxMeta() *velox.Meta[jOpt] { return jOptMeta.Load(jOptDesc) }

func jOptDesc() velox.Desc[jOpt] {
	return velox.Desc[jOpt]{
		Table: "j_opts",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "Tags", Name: "tags", Nullable: true, Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Attrs", Name: "attrs", Nullable: true, Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Fixed", Name: "fixed", Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Blob", Name: "blob", Nullable: true},
		},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *jOpt, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Tags
			case 2:
				return &x.Attrs
			case 3:
				return &x.Fixed
			}
			return &x.Blob
		},
	}
}

// TestJSONNullableNoPointer: in a column whose ColumnDesc says Nullable, a nil
// slice or map binds NULL, on insert and on update, and NULL reads back as
// nil; in one that does not, nil binds the JSON null, as before. A nil
// []byte in a Nullable column without a codec binds an untyped nil: NULL,
// whatever the driver makes of a nil []byte.
func TestJSONNullableNoPointer(t *testing.T) {
	info := velox.InfoOf[jOpt]()
	if !info.Columns[1].Nullable || !info.Columns[2].Nullable || info.Columns[3].Nullable || !info.Columns[4].Nullable {
		t.Errorf("Nullable: %v %v %v, want true true false", info.Columns[1].Nullable, info.Columns[2].Nullable, info.Columns[3].Nullable)
	}
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.Insert(&jOpt{ID: 1}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.SameArgs(t, "Insert of nil values", e.Args, []any{int64(1), nil, nil, []byte("null"), nil})
	if e.Args[4] != nil {
		t.Errorf("the nil blob binds a %T, want an untyped nil", e.Args[4])
	}
	if err := db.Insert(&jOpt{ID: 2, Tags: []string{}, Attrs: map[string]any{"k": 1}, Blob: []byte{}}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.SameArgs(t, "Insert of values", e.Args, []any{int64(2), []byte(`[]`), []byte(`{"k":1}`), []byte("null"), []byte{}})
	if _, err := db.Update[jOpt]().Set(jOpts.Tags.Set(nil), jOpts.Attrs.Set(nil), jOpts.Fixed.Set(nil), jOpts.Blob.Set(nil)).Where(jOpts.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.SameArgs(t, "Update to nil", e.Args, []any{nil, nil, []byte("null"), nil, int64(1)})
	if e.Args[3] != nil {
		t.Errorf("the nil blob binds a %T, want an untyped nil", e.Args[3])
	}

	e.Rows = [][]any{{int64(1), nil, nil, []byte("null"), []byte(nil)}}
	x, err := db.Query[jOpt]().Where(jOpts.ID.EQ(1)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if x.Tags != nil || x.Attrs != nil || x.Fixed != nil || x.Blob != nil {
		t.Errorf("read %+v, want nil values", x)
	}
}

// TestNullableNeedsNil: Nullable on a column whose field cannot be nil, nor
// is a pointer, is refused when the metadata is built, naming the column.
func TestNullableNeedsNil(t *testing.T) {
	_, err := velox.LoadDesc(velox.Desc[fixture.JList]{
		Table:      "j_lists",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id", Nullable: true}, {Field: "Words", Name: "words"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *fixture.JList, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Words
		},
	})
	var se *velox.SchemaError
	if !errors.As(err, &se) || se.Field != "ID" || !strings.Contains(err.Error(), "Nullable") {
		t.Errorf("got %v, want a *SchemaError naming the column", err)
	}
}
