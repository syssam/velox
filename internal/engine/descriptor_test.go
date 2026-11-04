package engine_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
)

func TestDescriptorMetadata(t *testing.T) {
	info := infoOf[fixture.DAuthor]() // helpers_test.go: the entity's EntityInfo, or the test fails on its error
	if info.Name != "fixture.DAuthor" || info.Table != "authors" || len(info.PrimaryKey) != 1 {
		t.Fatalf("%+v", info)
	}
	email, created := info.Columns[3], info.Columns[4]
	if !email.Nullable || !email.Sensitive || velox.GoType(email) != reflect.TypeFor[*string]() {
		t.Fatalf("email %+v", email)
	}
	if !created.Immutable || created.DefaultFunc == nil || created.Nullable {
		t.Fatalf("created_at %+v", created)
	}
}

// dRow is a model for the refusals of BuildDescriptor.
type dRow struct {
	ID     int64
	Flag   bool
	Opt    *bool
	Name   string
	Author velox.One[fixture.DAuthor]
}

var outside int64

// rowDescriptor is a valid Descriptor of dRow; each case of TestDescriptorRefusals breaks it.
func rowDescriptor() velox.Descriptor[dRow] {
	return velox.Descriptor[dRow]{
		Table:      "rows",
		Schema:     "app",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Flag", Name: "flag"}, {Field: "Opt", Name: "opt", Default: true}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		Indexes:    []velox.IndexInfo{{Columns: []int{3}, Unique: true}},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[fixture.DAuthor]("Author", 0, 0)},
		FieldPtr: func(x *dRow, i int) any {
			return [...]any{&x.ID, &x.Flag, &x.Opt, &x.Name}[i]
		},
		EdgeFieldPtr: func(x *dRow, i int) any { return &x.Author },
	}
}

func TestDescriptorAcceptsAndConverts(t *testing.T) {
	info, err := loadDescriptor(rowDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	if info.Schema != "app" || len(info.Indexes) != 1 {
		t.Fatalf("%+v", info)
	}
	// A pointer field's Default is given as a value of its element type and
	// held as a pointer, which FillDefault copies for every row.
	if opt := info.Columns[2]; !opt.HasDefault {
		t.Fatalf("opt %+v", opt)
	} else if p, _ := opt.Default.(*bool); p == nil || !*p {
		t.Fatalf("opt %+v", opt)
	}
}

// TestDescriptorDefaultRules: a Default is refused on a bool or numeric field that
// is not a pointer, and DefaultFunc and a pointer field's Default are not.
func TestDescriptorDefaultRules(t *testing.T) {
	d := rowDescriptor()
	d.Columns[0].DefaultFunc = func() any { return int64(5) } // a numeric field
	info, err := loadDescriptor(d)
	if err != nil {
		t.Fatalf("DefaultFunc on a numeric field: %v", err)
	}
	if c := info.Columns[0]; c.DefaultFunc == nil || c.HasDefault {
		t.Errorf("id %+v", c)
	}
	// Opt is a *bool: its Default is accepted (rowDescriptor gives it one).
	if info, err = loadDescriptor(rowDescriptor()); err != nil || !info.Columns[2].HasDefault {
		t.Errorf("Default on a pointer field: %v", err)
	}
	// A Default that is the zero value stores what an unset field stores, as
	// velox-gen allows Default(false) and Default(0).
	d = rowDescriptor()
	d.Columns[1].Default = false
	d.Columns[0].Default = int64(0)
	if info, err = loadDescriptor(d); err != nil || !info.Columns[1].HasDefault || !info.Columns[0].HasDefault {
		t.Errorf("a zero Default: %v", err)
	}
}

func TestDescriptorRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(d *velox.Descriptor[dRow])
		want string
	}{
		{"no Table", func(d *velox.Descriptor[dRow]) { d.Table = "" }, "dRow: the generated metadata has no Table"},
		{"no FieldPtr", func(d *velox.Descriptor[dRow]) { d.FieldPtr = nil }, "dRow: the generated metadata has no FieldPtr"},
		{"FieldPtr gives nil", func(d *velox.Descriptor[dRow]) {
			d.FieldPtr = func(x *dRow, i int) any { return nil }
		}, "dRow.ID: FieldPtr does not return a pointer into the entity for column 0"},
		{"FieldPtr gives a value", func(d *velox.Descriptor[dRow]) {
			d.FieldPtr = func(x *dRow, i int) any { return x.ID }
		}, "FieldPtr does not return a pointer"},
		{"FieldPtr gives a nil pointer", func(d *velox.Descriptor[dRow]) {
			d.FieldPtr = func(x *dRow, i int) any { return (*int64)(nil) }
		}, "FieldPtr does not return a pointer"},
		{"FieldPtr points outside", func(d *velox.Descriptor[dRow]) {
			d.FieldPtr = func(x *dRow, i int) any { return &outside }
		}, "FieldPtr does not return a pointer into the entity"},
		{"no primary key", func(d *velox.Descriptor[dRow]) { d.PrimaryKey = nil }, "the primary key names no fields"},
		{"primary key out of range", func(d *velox.Descriptor[dRow]) { d.PrimaryKey = []int{4} }, "the primary key names column 4, of 4"},
		{"index out of range", func(d *velox.Descriptor[dRow]) { d.Indexes = []velox.IndexInfo{{Columns: []int{-1}}} }, "an index names column -1, of 4"},
		{"Default on a bool", func(d *velox.Descriptor[dRow]) { d.Columns[1].Default = true }, "dRow.Flag: Default on bool/numeric field Flag would overwrite an explicit false or 0 on insert"},
		{"Default on a number", func(d *velox.Descriptor[dRow]) { d.Columns[0].Default = int64(3) }, "dRow.ID: Default on bool/numeric field ID would overwrite an explicit false or 0 on insert"},
		{"unique index naming no column", func(d *velox.Descriptor[dRow]) { d.Indexes = []velox.IndexInfo{{Unique: true}} }, "a unique index names no fields"},
		{"index naming a column twice", func(d *velox.Descriptor[dRow]) { d.Indexes = []velox.IndexInfo{{Columns: []int{1, 1}}} }, "dRow.Flag: an index names the field twice"},
		{"Default of another type", func(d *velox.Descriptor[dRow]) { d.Columns[3].Default = 7 }, "dRow.Name: Default is a int, not a string"},
		{"Immutable with an UpdateDefault", func(d *velox.Descriptor[dRow]) {
			d.Columns[3].Immutable = true
			d.Columns[3].UpdateDefault = func() any { return "x" }
		}, "dRow.Name: Immutable column Name has an UpdateDefault; every update would be refused"},
		{"duplicate column name", func(d *velox.Descriptor[dRow]) { d.Columns[3].Name = "flag" }, `column name "flag" is also the name of Flag`},
		{"panic in FieldPtr", func(d *velox.Descriptor[dRow]) {
			d.FieldPtr = func(x *dRow, i int) any { panic("addr") }
		}, "the generated metadata panicked: addr"},
		{"no EdgeFieldPtr", func(d *velox.Descriptor[dRow]) { d.EdgeFieldPtr = nil }, "has relations but no EdgeFieldPtr"},
		{"EdgeFieldPtr gives a column", func(d *velox.Descriptor[dRow]) {
			d.EdgeFieldPtr = func(x *dRow, i int) any { return &x.ID }
		}, "dRow.Author: EdgeFieldPtr does not return a *One[T] or *Many[T]"},
		{"relation not from EdgeTo", func(d *velox.Descriptor[dRow]) {
			d.Edges = []velox.EdgeDescriptor{{}} // a literal, not made by EdgeTo
		}, "relation 0 was not made by EdgeTo or EdgeThrough"},
		{"EdgeTo owner column out of range", func(d *velox.Descriptor[dRow]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeTo[fixture.DAuthor]("Author", 9, 0)}
		}, "dRow.Author: its key names column 9 of engine_test.dRow, which has 4"},
		{"EdgeTo target column out of range", func(d *velox.Descriptor[dRow]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeTo[fixture.DAuthor]("Author", 0, 5)}
		}, "its key names column 5 of fixture.DAuthor, which has 5"},
		{"EdgeTo key types differ", func(d *velox.Descriptor[dRow]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeTo[fixture.DAuthor]("Author", 3, 0)}
		}, "the key types differ"},
		{"EdgeThrough on a One", func(d *velox.Descriptor[dRow]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeThrough[fixture.DBook, fixture.DAuthor]("Author", 0, 0, 1, 0)}
		}, "dRow.Author: a many-to-many relation needs a Many field"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := rowDescriptor()
			c.edit(&d)
			// The relations of these cases all target dAuthor, as a use site
			// of the handle would say.
			_, err := loadDescriptor(d, targetOf[fixture.DAuthor]())
			var se *velox.SchemaError
			if !errors.As(err, &se) || !strings.HasPrefix(err.Error(), "velox: engine_test.dRow") || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want a *SchemaError containing %q", err, c.want)
			}
		})
	}
}

func TestEdgeToHolderMismatch(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(d *velox.Descriptor[dRow])
		want string
	}{
		{"EdgeTo at a One of another entity", func(d *velox.Descriptor[dRow]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeTo[fixture.DBook]("Author", 0, 0)}
		}, "velox: engine_test.dRow.Author: relation Author is a velox.One[github.com/syssam/velox/internal/apitest/fixture.DAuthor], not a One or Many of github.com/syssam/velox/internal/apitest/fixture.DBook"},
		{"EdgeThrough at a One of another entity", func(d *velox.Descriptor[dRow]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeThrough[fixture.DBookTag, fixture.DBook]("Author", 0, 0, 1, 0)}
		}, "velox: engine_test.dRow.Author: relation Author is a velox.One[github.com/syssam/velox/internal/apitest/fixture.DAuthor], not a One or Many of github.com/syssam/velox/internal/apitest/fixture.DBook"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := rowDescriptor()
			c.edit(&d)
			_, err := loadDescriptor(d)
			var se *velox.SchemaError
			if !errors.As(err, &se) || err.Error() != c.want {
				t.Fatalf("got %v, want a *SchemaError %q", err, c.want)
			}
		})
	}
	// A One or a Many of the right entity is accepted.
	if _, err := loadDescriptor(rowDescriptor()); err != nil {
		t.Fatal(err)
	}
}

// dBidi is a model for the refusals of a bidirectional join entity's Descriptor.
type dBidi struct {
	A, B int64
	C    string
}

// bidiDescriptor is a valid Descriptor of dBidi's A and B, a bidirectional join entity;
// each case of TestBidiDescriptorRefused breaks it.
func bidiDescriptor() velox.Descriptor[dBidi] {
	return velox.Descriptor[dBidi]{
		Table:      "links",
		Columns:    []velox.ColumnDescriptor{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}},
		PrimaryKey: []int{0, 1},
		Bidi:       true,
		FieldPtr: func(x *dBidi, i int) any {
			return [...]any{&x.A, &x.B, &x.C}[i]
		},
	}
}

// dBidiSlice has keys == cannot compare.
type dBidiSlice struct{ A, B []int }

// ifaceKey is comparable, but == on it panics when X holds an uncomparable
// value: a []int, say.
type ifaceKey struct{ X struct{ Y any } }

// dBidiIface and dBidiArray have keys == can panic on.
type (
	dBidiIface struct{ A, B ifaceKey }
	dBidiArray struct{ A, B [2]any }
)

func TestBidiDescriptorRefused(t *testing.T) {
	_, err := loadDescriptor(velox.Descriptor[dBidiSlice]{
		Table:      "links",
		Columns:    []velox.ColumnDescriptor{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}},
		PrimaryKey: []int{0, 1},
		Bidi:       true,
		FieldPtr:   func(x *dBidiSlice, i int) any { return [...]any{&x.A, &x.B}[i] },
	})
	if want := "velox: engine_test.dBidiSlice: the join entity of a bidirectional edge has columns of type []int, which cannot be compared; use a comparable type or a byte slice"; err == nil || err.Error() != want {
		t.Errorf("a slice key: %v, want %s", err, want)
	}
	_, err = loadDescriptor(velox.Descriptor[dBidiIface]{
		Table:      "links",
		Columns:    []velox.ColumnDescriptor{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}},
		PrimaryKey: []int{0, 1},
		Bidi:       true,
		FieldPtr:   func(x *dBidiIface, i int) any { return [...]any{&x.A, &x.B}[i] },
	})
	if want := "velox: engine_test.dBidiIface: the join entity of a bidirectional edge has columns of type engine_test.ifaceKey, which cannot be compared; use a comparable type or a byte slice"; err == nil || err.Error() != want {
		t.Errorf("a struct key holding an interface: %v, want %s", err, want)
	}
	_, err = loadDescriptor(velox.Descriptor[dBidiArray]{
		Table:      "links",
		Columns:    []velox.ColumnDescriptor{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}},
		PrimaryKey: []int{0, 1},
		Bidi:       true,
		FieldPtr:   func(x *dBidiArray, i int) any { return [...]any{&x.A, &x.B}[i] },
	})
	if want := "velox: engine_test.dBidiArray: the join entity of a bidirectional edge has columns of type [2]interface {}, which cannot be compared; use a comparable type or a byte slice"; err == nil || err.Error() != want {
		t.Errorf("an array key of interfaces: %v, want %s", err, want)
	}
	allow := func(context.Context) (velox.Pred[dBidi], error) { return velox.Allow[dBidi](), nil }
	if _, err := loadDescriptor(bidiDescriptor()); err != nil {
		t.Fatalf("a valid Desc: %v", err)
	}
	ok := bidiDescriptor()
	ok.Policy = []func() velox.BoundPolicy{func() velox.BoundPolicy {
		return velox.BindPolicy(velox.Policy[dBidi]{Insert: func(context.Context, *dBidi) error { return nil }})
	}}
	if _, err := loadDescriptor(ok); err != nil {
		t.Fatalf("a Policy with Insert only: %v", err)
	}
	for _, c := range []struct {
		name string
		edit func(d *velox.Descriptor[dBidi])
		want string
	}{
		{"three columns", func(d *velox.Descriptor[dBidi]) {
			d.Columns = append(d.Columns, velox.ColumnDescriptor{Field: "C", Name: "c"})
		}, "the join entity of a bidirectional edge has 3 columns; it has two, its primary key"},
		{"primary key", func(d *velox.Descriptor[dBidi]) { d.PrimaryKey = []int{1, 0} }, "the join entity of a bidirectional edge has the primary key [1 0]; its two columns are its primary key, [0 1]"},
		{"two types", func(d *velox.Descriptor[dBidi]) {
			d.Columns[1] = velox.ColumnDescriptor{Field: "C", Name: "c"}
			d.FieldPtr = func(x *dBidi, i int) any { return [...]any{&x.A, &x.C}[i] }
		}, "the join entity of a bidirectional edge has columns of two types, int64 and string"},
		{"read policy", func(d *velox.Descriptor[dBidi]) {
			d.Policy = []func() velox.BoundPolicy{func() velox.BoundPolicy { return velox.BindPolicy(velox.Policy[dBidi]{Read: allow}) }}
		}, "the join entity of a bidirectional edge cannot have a Read or Write policy: its conditions would show or delete one row of a link; check links in Policy.Insert and deletes in a hook"},
		{"write policy", func(d *velox.Descriptor[dBidi]) {
			d.Policy = []func() velox.BoundPolicy{func() velox.BoundPolicy { return velox.BindPolicy(velox.Policy[dBidi]{Write: allow}) }}
		}, "the join entity of a bidirectional edge cannot have a Read or Write policy: its conditions would show or delete one row of a link; check links in Policy.Insert and deletes in a hook"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := bidiDescriptor()
			c.edit(&d)
			_, err := loadDescriptor(d)
			var se *velox.SchemaError
			if !errors.As(err, &se) || err.Error() != "velox: engine_test.dBidi: "+c.want {
				t.Errorf("err = %v,\nwant a SchemaError: velox: engine_test.dBidi: %s", err, c.want)
			}
		})
	}
}

// dSpouse is a model for the refusals of a bidirectional one-to-one
// relation's Descriptor (EdgeBidi).
type dSpouse struct {
	ID       int64
	SpouseID *int64
	Number   int64
	Small    *int32
	Blob     []byte
	Spouse   velox.One[dSpouse]
	Author   velox.One[fixture.DAuthor]
}

var dSpouseMeta velox.MetaOnce[dSpouse]

func (*dSpouse) VeloxMeta() *velox.Meta[dSpouse] { return dSpouseMeta.Load(spouseDescriptor) }

// spouseDescriptor is a valid Descriptor of dSpouse's Spouse; each case of
// TestBidiOneDescriptorRefused breaks it.
func spouseDescriptor() velox.Descriptor[dSpouse] {
	return velox.Descriptor[dSpouse]{
		Table: "spouses",
		Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "SpouseID", Name: "spouse_id"},
			{Field: "Number", Name: "number"}, {Field: "Small", Name: "small"}, {Field: "Blob", Name: "blob", Nullable: true}},
		PrimaryKey: []int{0},
		Indexes:    []velox.IndexInfo{{Columns: []int{1}, Unique: true}, {Columns: []int{2}, Unique: true}, {Columns: []int{3}, Unique: true}},
		Edges:      []velox.EdgeDescriptor{velox.EdgeBidi[dSpouse]("Spouse", 1, 0)},
		FieldPtr: func(x *dSpouse, i int) any {
			return [...]any{&x.ID, &x.SpouseID, &x.Number, &x.Small, &x.Blob}[i]
		},
		EdgeFieldPtr: func(x *dSpouse, i int) any { return [...]any{&x.Spouse, &x.Author}[i] },
	}
}

func TestBidiOneDescriptorRefused(t *testing.T) {
	if _, err := loadDescriptor(spouseDescriptor()); err != nil {
		t.Fatalf("a valid Desc: %v", err)
	}
	for _, c := range []struct {
		name string
		edit func(d *velox.Descriptor[dSpouse])
		want string
	}{
		{"another entity", func(d *velox.Descriptor[dSpouse]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeTo[dSpouse]("Spouse", 1, 0), velox.EdgeBidi[fixture.DAuthor]("Author", 1, 0)}
		}, "Author: EdgeBidi relates an entity to itself, not to fixture.DAuthor"},
		{"not a pointer", func(d *velox.Descriptor[dSpouse]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeBidi[dSpouse]("Spouse", 2, 0)}
		}, "Spouse: EdgeBidi's key column number is a int64; make its field a pointer, which a NULL key leaves nil"},
		{"a Nullable slice", func(d *velox.Descriptor[dSpouse]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeBidi[dSpouse]("Spouse", 4, 0)}
		}, "Spouse: EdgeBidi's key column blob is a []uint8; make its field a pointer, which a NULL key leaves nil"},
		{"a RequiredEdge", func(d *velox.Descriptor[dSpouse]) { d.Columns[1].RequiredEdge = "dSpouse.spouse" },
			"Spouse: EdgeBidi's key column spouse_id has a RequiredEdge, and velox clears a partner's key whenever its partner re-points"},
		{"another type", func(d *velox.Descriptor[dSpouse]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeBidi[dSpouse]("Spouse", 3, 0)}
		}, "Spouse: EdgeBidi's key column small holds int32, and the primary key id int64"},
		{"no unique index", func(d *velox.Descriptor[dSpouse]) { d.Indexes = d.Indexes[1:] },
			"Spouse: EdgeBidi's key column spouse_id has no unique index of its own, which refuses a partner that already has one"},
		{"a non-unique index", func(d *velox.Descriptor[dSpouse]) { d.Indexes[0].Unique = false },
			"Spouse: EdgeBidi's key column spouse_id has no unique index of its own, which refuses a partner that already has one"},
		{"the primary key", func(d *velox.Descriptor[dSpouse]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeBidi[dSpouse]("Spouse", 0, 0)}
		}, "Spouse: EdgeBidi's key column id is a primary-key column"},
		{"the id is not the key", func(d *velox.Descriptor[dSpouse]) {
			d.Edges = []velox.EdgeDescriptor{velox.EdgeBidi[dSpouse]("Spouse", 1, 2)}
		}, "Spouse: EdgeBidi's id column number is not the entity's one-column primary key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := spouseDescriptor()
			c.edit(&d)
			_, err := loadDescriptor(d)
			var se *velox.SchemaError
			if !errors.As(err, &se) || err.Error() != "velox: engine_test.dSpouse."+c.want {
				t.Errorf("err = %v,\nwant a SchemaError: velox: engine_test.dSpouse.%s", err, c.want)
			}
		})
	}
}
