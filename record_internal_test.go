package velox

import (
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
	Secret string
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

// TestChangeValues: a Change's snapshots and changed fields, plain, with a
// Sensitive column masked.
func TestChangeValues(t *testing.T) {
	note := "n"
	old := &rinvoice{ID: 7, Total: 10, Secret: "a", Note: &note}
	cur := &rinvoice{ID: 7, Total: 15, Secret: "b"}
	c := changeFor(t, OpUpdate, old, cur)
	if c.Entity != "rinvoice" || c.Op != OpUpdate || c.Key != int64(7) || c.Soft {
		t.Errorf("header: %+v", c)
	}
	wantOld := map[string]any{"id": int64(7), "total": 10.0, "secret": "***", "note": "n"}
	wantNew := map[string]any{"id": int64(7), "total": 15.0, "secret": "***", "note": nil}
	if got := c.OldValues(); !reflect.DeepEqual(got, wantOld) {
		t.Errorf("OldValues = %v, want %v", got, wantOld)
	}
	if got := c.NewValues(); !reflect.DeepEqual(got, wantNew) {
		t.Errorf("NewValues = %v, want %v", got, wantNew)
	}
	wantFields := []FieldChange{{Name: "total", Old: 10.0, New: 15.0}, {Name: "secret", Old: "***", New: "***"},
		{Name: "note", Old: "n", New: nil}}
	if got := c.ChangedFields(); !reflect.DeepEqual(got, wantFields) {
		t.Errorf("ChangedFields = %v, want %v", got, wantFields)
	}

	same := changeFor(t, OpUpdate, old, old)
	if got := same.ChangedFields(); got != nil {
		t.Errorf("ChangedFields of an unchanged row = %v, want nil", got)
	}

	ins := changeFor(t, OpInsert, nil, cur)
	if ins.Key != int64(7) || ins.OldValues() != nil || ins.ChangedFields() != nil {
		t.Errorf("insert: key %v, old %v, changed %v", ins.Key, ins.OldValues(), ins.ChangedFields())
	}
	if !reflect.DeepEqual(ins.NewValues(), wantNew) {
		t.Errorf("insert NewValues = %v", ins.NewValues())
	}

	del := changeFor(t, OpDelete, old, nil)
	if del.Key != int64(7) || del.NewValues() != nil || del.ChangedFields() != nil || !reflect.DeepEqual(del.OldValues(), wantOld) {
		t.Errorf("delete: key %v, new %v, changed %v, old %v", del.Key, del.NewValues(), del.ChangedFields(), del.OldValues())
	}

	pair := changeFor(t, OpDelete, &rpair{A: 1, B: 2, N: "x"}, nil)
	if !reflect.DeepEqual(pair.Key, []any{int64(1), int64(2)}) {
		t.Errorf("composite Key = %#v", pair.Key)
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

// TestRecorderTruncate: truncate drops what was recorded after a mark.
func TestRecorderTruncate(t *testing.T) {
	var rec Recorder
	_ = rec.Record(t.Context(), Change{Key: 1})
	m := rec.mark()
	_ = rec.Record(t.Context(), Change{Key: 2})
	rec.truncate(m)
	rec.truncate(5) // past the end: nothing to drop
	if got := slices.Collect(rec.All()); len(got) != 1 || got[0].Key != 1 {
		t.Errorf("after truncate: %v", got)
	}
}
