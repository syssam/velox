package gqlgraph_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// note is a hand-written entity with a JSON array column, as velox-gen would
// write it, for ArrayField's tests.
type note struct {
	ID   int64
	Tags []string
}

var notes = struct {
	ID   velox.NumberCol[note, int64]
	Tags velox.NullJSONCol[note, []string]
}{
	ID:   velox.NumberCol[note, int64]{I: 0},
	Tags: velox.NullJSONCol[note, []string]{I: 1},
}

var noteMeta velox.MetaOnce[note]

func (*note) VeloxMeta() *velox.Meta[note] { return noteMeta.Load(noteDescriptor) }

func noteDescriptor() velox.Descriptor[note] {
	return velox.Descriptor[note]{
		Table: "notes",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"},
			{Field: "Tags", Name: "tags", Encode: json.Marshal, Decode: json.Unmarshal, Nullable: true},
		},
		PrimaryKey: []int{0},
		FieldPtr: func(x *note, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Tags
			}
			return nil
		},
	}
}

// noteWhere is a where input of note, as velox-generated code writes one: the
// four predicates of a JSON array and its IsNil and NotNil.
type noteWhere struct {
	Not *noteWhere

	TagsHas      *string
	TagsHasSome  []string
	TagsHasEvery []string
	TagsIsEmpty  *bool
	TagsIsNil    *bool
	TagsNotNil   *bool
}

func (w *noteWhere) predicate(ctx context.Context) (velox.Predicate, error) {
	f := gqlgraph.NewFilter(ctx, gqlgraph.TableType[note]("Note"))
	if w == nil {
		return f.Pred()
	}
	gqlgraph.Not(f, "not", w.Not, (*noteWhere).predicate)
	gqlgraph.ArrayField(f, "tags", 1, gqlgraph.ArrayOps[string]{
		Has: w.TagsHas, HasSome: w.TagsHasSome, HasEvery: w.TagsHasEvery,
		IsEmpty: w.TagsIsEmpty, IsNil: w.TagsIsNil, NotNil: w.TagsNotNil,
	})
	return f.Pred()
}

// notesDB holds five notes: 1 has x and y, 2 has none ([]), 3 is NULL, 4 has y
// alone, and 5 is an object, which is no array.
func notesDB(t *testing.T) *velox.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, tags TEXT)`); err != nil {
		t.Fatal(err)
	}
	for id, tags := range map[int64]any{1: `["x","y"]`, 2: `[]`, 3: nil, 4: `["y"]`, 5: `{"x":1}`} {
		if _, err := sqlDB.Exec(`INSERT INTO notes (id, tags) VALUES (?, ?)`, id, tags); err != nil {
			t.Fatal(err)
		}
	}
	return velox.Open(velox.SQLite, sqlDB)
}

// noteIDs are the keys of the notes w selects, read as keys only: the object
// of note 5 does not decode into the tags' slice.
func noteIDs(t *testing.T, db *velox.DB, w *noteWhere) []int64 {
	t.Helper()
	ctx := context.Background()
	p, err := w.predicate(ctx)
	if err != nil {
		t.Fatalf("predicate: %v", err)
	}
	rows, err := db.Query[note]().Columns(notes.ID).Where(velox.Pred[note](p)).OrderBy(notes.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// TestArrayField: each predicate of a JSON array, the empty lists, and NULL
// and a document that is no array, which hold no element and are not empty.
func TestArrayField(t *testing.T) {
	db := notesDB(t)
	yes, no := new(true), new(false)
	for _, c := range []struct {
		name string
		w    *noteWhere
		want []int64
	}{
		{"Has", &noteWhere{TagsHas: new("y")}, []int64{1, 4}},
		{"Has, nowhere", &noteWhere{TagsHas: new("z")}, nil},
		{"HasSome", &noteWhere{TagsHasSome: []string{"x", "q"}}, []int64{1}},
		{"HasSome of many", &noteWhere{TagsHasSome: []string{"x", "y"}}, []int64{1, 4}},
		{"HasSome of none", &noteWhere{TagsHasSome: []string{}}, nil},
		{"HasEvery", &noteWhere{TagsHasEvery: []string{"x", "y"}}, []int64{1}},
		{"HasEvery, one missing", &noteWhere{TagsHasEvery: []string{"y", "q"}}, nil},
		// HasEvery of nothing is an And of nothing, true, so it holds for every
		// row, NULL and the object too: no element is asked of any of them.
		{"HasEvery of none", &noteWhere{TagsHasEvery: []string{}}, []int64{1, 2, 3, 4, 5}},
		{"IsEmpty", &noteWhere{TagsIsEmpty: yes}, []int64{2}},
		{"IsEmpty false", &noteWhere{TagsIsEmpty: no}, []int64{1, 4, 5}},
		{"IsNil", &noteWhere{TagsIsNil: yes}, []int64{3}},
		{"NotNil", &noteWhere{TagsNotNil: yes}, []int64{1, 2, 4, 5}},
		{"not Has", &noteWhere{Not: &noteWhere{TagsHas: new("y")}}, []int64{2, 5}},
		{"Has and IsEmpty", &noteWhere{TagsHas: new("y"), TagsIsEmpty: no}, []int64{1, 4}},
	} {
		if got := noteIDs(t, db, c.w); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestArrayFieldWidth: more values than MaxFilterWidth fail the filter with
// ErrFilterTooWide, for HasSome and for HasEvery, before a statement runs.
func TestArrayFieldWidth(t *testing.T) {
	ctx := context.Background()
	vs := make([]string, veloxgql.DefaultLimits().MaxFilterWidth+1)
	for i := range vs {
		vs[i] = "v"
	}
	for name, w := range map[string]*noteWhere{
		"HasSome":  {TagsHasSome: vs},
		"HasEvery": {TagsHasEvery: vs},
	} {
		if _, err := w.predicate(ctx); !errors.Is(err, veloxgql.ErrFilterTooWide) {
			t.Errorf("%s of %d values: %v, want ErrFilterTooWide", name, len(vs), err)
		}
	}
	// A first error stops the rest: Has after it is not added.
	w := &noteWhere{TagsHasSome: vs, TagsHas: new("x"), TagsIsEmpty: new(true)}
	if _, err := w.predicate(ctx); !errors.Is(err, veloxgql.ErrFilterTooWide) {
		t.Errorf("a refused filter: %v", err)
	}
}
