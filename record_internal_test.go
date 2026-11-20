package velox

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// rinvoice is the entity of the Change tests: a total, a secret and a
// nullable note.
type rinvoice struct {
	ID     int64
	Total  float64
	Secret string `json:"-"`
	Note   *string
}

var rinvoiceMeta MetaOnce[rinvoice]

func (*rinvoice) VeloxMeta() *Meta[rinvoice] {
	return rinvoiceMeta.Load(func() Descriptor[rinvoice] {
		return Descriptor[rinvoice]{
			Table: "rinvoices",
			Columns: []ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Total", Name: "total"},
				{Field: "Secret", Name: "secret", Sensitive: true}, {Field: "Note", Name: "note"}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *rinvoice, i int) any {
				return [...]any{&x.ID, &x.Total, &x.Secret, &x.Note}[i]
			},
		}
	})
}

// rticket is another entity, for RowsOf of the wrong type.
type rticket struct{ ID int64 }

var rticketMeta MetaOnce[rticket]

func (*rticket) VeloxMeta() *Meta[rticket] {
	return rticketMeta.Load(func() Descriptor[rticket] {
		return Descriptor[rticket]{
			Table:      "rtickets",
			Columns:    []ColumnDescriptor{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *rticket, _ int) any { return &x.ID },
		}
	})
}

// rpair has a composite key.
type rpair struct {
	A, B int64
	N    string
}

var rpairMeta MetaOnce[rpair]

func (*rpair) VeloxMeta() *Meta[rpair] {
	return rpairMeta.Load(func() Descriptor[rpair] {
		return Descriptor[rpair]{
			Table:      "rpairs",
			Columns:    []ColumnDescriptor{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}, {Field: "N", Name: "n"}},
			PrimaryKey: []int{0, 1},
			FieldPtr: func(x *rpair, i int) any {
				return [...]any{&x.A, &x.B, &x.N}[i]
			},
		}
	})
}

// changeFor is the Change of a write of op on old and cur, rows of E.
func changeFor[E any, PE Entity[E]](t *testing.T, op Op, old, cur *E) Change {
	t.Helper()
	st := stateOf(PE(nil).VeloxMeta())
	if st.Err != nil {
		t.Fatal(st.Err)
	}
	return changeOf(engine.ChangeRow{Op: op, In: st.Info, Old: unsafe.Pointer(old), New: unsafe.Pointer(cur)})
}

// The handles of rinvoice's columns, and one of rticket's.
var (
	riID     = NumberCol[rinvoice, int64]{I: 0}
	riTotal  = NumberCol[rinvoice, float64]{I: 1}
	riSecret = StringCol[rinvoice, string]{I: 2}
	riNote   = NullStringCol[rinvoice, string]{I: 3}
	rtID     = NumberCol[rticket, int64]{I: 0}
)

// TestChangeTypedGetters: Old and New give a column's values typed through
// its handle, real for a Sensitive column, the element for a nullable one;
// Changed tells whether any of the columns differs. Another entity's handle
// gives the zero value and false.
func TestChangeTypedGetters(t *testing.T) {
	note := "n"
	old := &rinvoice{ID: 7, Total: 10, Secret: "a", Note: &note}
	cur := &rinvoice{ID: 7, Total: 15, Secret: "b"}
	c := changeFor(t, OpUpdate, old, cur)
	if c.Entity != "rinvoice" || c.Op != OpUpdate || c.Key != int64(7) || c.Soft {
		t.Errorf("header: %+v", c)
	}
	if got := riTotal.Old(c); got != 10.0 {
		t.Errorf("Old(total) = %v, want 10", got)
	}
	if got := riTotal.New(c); got != 15.0 {
		t.Errorf("New(total) = %v, want 15", got)
	}
	if riSecret.Old(c) != "a" || riSecret.New(c) != "b" {
		t.Errorf("secret: %q -> %q, want real values", riSecret.Old(c), riSecret.New(c))
	}
	if riNote.Old(c) != "n" || riNote.New(c) != "" {
		t.Errorf("note: %q -> %q, want n -> zero", riNote.Old(c), riNote.New(c))
	}
	if !c.Changed(riTotal) || c.Changed(riID) || !c.Changed(riID, riNote) {
		t.Errorf("Changed: total %v, id %v, id+note %v", c.Changed(riTotal), c.Changed(riID), c.Changed(riID, riNote))
	}
	if rtID.Old(c) != 0 || rtID.New(c) != 0 || c.Changed(rtID) {
		t.Errorf("another entity's handle: %v %v %v", rtID.Old(c), rtID.New(c), c.Changed(rtID))
	}
	if (NumberCol[rinvoice, int64]{I: 99}).Old(c) != 0 || c.Changed(NumberCol[rinvoice, int64]{I: 99}) {
		t.Error("a handle past the columns: want zero and false")
	}
	if z := (Change{}); riTotal.Old(z) != 0 || z.Changed(riTotal) || z.OldRow() != nil || z.NewRow() != nil || z.Diff() != nil {
		t.Error("a zero Change: want zero values")
	}
	if c.OldRow() != any(old) || c.NewRow() != any(cur) {
		t.Errorf("rows: %v %v", c.OldRow(), c.NewRow())
	}

	ins := changeFor(t, OpInsert, nil, cur)
	if ins.Key != int64(7) || riTotal.Old(ins) != 0 || riTotal.New(ins) != 15 || ins.OldRow() != nil || ins.NewRow() != any(cur) || ins.Changed(riTotal) {
		t.Errorf("insert: key %v, old %v, new %v, rows %v %v", ins.Key, riTotal.Old(ins), riTotal.New(ins), ins.OldRow(), ins.NewRow())
	}
	del := changeFor(t, OpDelete, old, nil)
	if del.Key != int64(7) || riTotal.New(del) != 0 || riTotal.Old(del) != 10 || del.NewRow() != nil || del.OldRow() != any(old) || del.Changed(riTotal) {
		t.Errorf("delete: key %v, old %v, new %v, rows %v %v", del.Key, riTotal.Old(del), riTotal.New(del), del.OldRow(), del.NewRow())
	}

	pair := changeFor(t, OpDelete, &rpair{A: 1, B: 2, N: "x"}, nil)
	if !reflect.DeepEqual(pair.Key, []any{int64(1), int64(2)}) {
		t.Errorf("composite Key = %#v", pair.Key)
	}
}

// TestChangeRowsMarshal: OldRow and NewRow are the entity, whose JSON leaves
// a Sensitive field out (its generated tag is json:"-").
func TestChangeRowsMarshal(t *testing.T) {
	c := changeFor(t, OpInsert, nil, &rinvoice{ID: 7, Total: 15, Secret: "b"})
	b, err := json.Marshal(c.NewRow())
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"ID":7,"Total":15,"Note":null}`; string(b) != want {
		t.Errorf("NewRow JSON = %s, want %s", b, want)
	}
}

// TestChangeDiff: Diff lists an update's changed columns in column order,
// with real values, but no Sensitive column (RecordIgnore's are
// in apitest); it is nil for
// an insert, a delete and an update that changes none.
func TestChangeDiff(t *testing.T) {
	note := "n"
	old := &rinvoice{ID: 7, Total: 10, Secret: "a", Note: &note}
	cur := &rinvoice{ID: 7, Total: 15, Secret: "b"}
	c := changeFor(t, OpUpdate, old, cur)
	want := []FieldChange{{Name: "total", Old: 10.0, New: 15.0}, {Name: "note", Old: "n", New: nil}}
	if got := c.Diff(); !reflect.DeepEqual(got, want) {
		t.Errorf("Diff = %v, want %v", got, want)
	}
	if riSecret.Old(c) != "a" {
		t.Errorf("Old(secret) = %q, want the real value", riSecret.Old(c))
	}
	if got := changeFor(t, OpUpdate, old, old).Diff(); got != nil {
		t.Errorf("Diff of an unchanged row = %v, want nil", got)
	}
	if got := changeFor(t, OpInsert, nil, cur).Diff(); got != nil {
		t.Errorf("insert Diff = %v, want nil", got)
	}
	if got := changeFor(t, OpDelete, old, nil).Diff(); got != nil {
		t.Errorf("delete Diff = %v, want nil", got)
	}
}

// TestRowsOf: RowsOf gives the typed rows, with real values, and refuses
// another entity's Change.
func TestRowsOf(t *testing.T) {
	old := &rinvoice{ID: 7, Total: 10, Secret: "a"}
	cur := &rinvoice{ID: 7, Total: 15, Secret: "b"}
	c := changeFor(t, OpUpdate, old, cur)
	o, n, ok := RowsOf[rinvoice](c)
	if !ok || o != old || n != cur || o.Secret != "a" {
		t.Errorf("RowsOf[rinvoice] = %v, %v, %v", o, n, ok)
	}
	if o, n, ok := RowsOf[rticket](c); o != nil || n != nil || ok {
		t.Errorf("RowsOf[rticket] = %v, %v, %v", o, n, ok)
	}
	if o, n, ok := RowsOf[rticket](Change{}); o != nil || n != nil || ok {
		t.Errorf("RowsOf of a zero Change = %v, %v, %v", o, n, ok)
	}
	ins := changeFor(t, OpInsert, nil, cur)
	if o, n, ok := RowsOf[rinvoice](ins); o != nil || n != cur || !ok {
		t.Errorf("RowsOf of an insert = %v, %v, %v", o, n, ok)
	}
}

// TestDiff: Diff lists the columns that differ, with real values.
func TestDiff(t *testing.T) {
	got := Diff(&rinvoice{ID: 1, Total: 10, Secret: "a"}, &rinvoice{ID: 1, Total: 10, Secret: "b"})
	want := []FieldChange{{Name: "secret", Old: "a", New: "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Diff = %v, want %v", got, want)
	}
	if got := Diff[rinvoice](nil, &rinvoice{}); len(got) != 3 {
		t.Errorf("Diff(nil, zero) = %v, want id, total and secret", got)
	}
}

// TestRecorderDrop: drop drops the Changes of the ids given, whatever their
// position, and skips ids no longer kept.
func TestRecorderDrop(t *testing.T) {
	var rec Recorder
	a := rec.keep(Change{Key: 1})
	_ = rec.Record(t.Context(), nil, []Change{{Key: 2}})
	c := rec.keep(Change{Key: 3})
	_ = rec.keep(Change{Key: 4})
	rec.drop([]uint64{a, c, 99}) // 99: never kept
	if got := slices.Collect(rec.All()); len(got) != 2 || got[0].Key != 2 || got[1].Key != 4 {
		t.Errorf("after drop: %v", got)
	}
	rec.Reset()
	rec.drop([]uint64{a}) // after Reset: nothing to drop
	if e := rec.keep(Change{Key: 5}); e <= c || rec.Len() != 1 {
		t.Errorf("after Reset: id %d, %d Changes", e, rec.Len())
	}
}
