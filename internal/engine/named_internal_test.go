package engine

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// kinds is a row of every field type a mutation by name stores, with and
// without its fast path.
type kinds struct {
	I64  int64
	I32  int32
	I    int
	S    string
	B    bool
	F    float64
	T    time.Time
	PI32 *int32
	PI   *int
	PB   *bool
	PF   *float64
	PT   *time.Time
	PI8  *int8
	U16  uint16
	Raw  []byte
	M    map[string]int
}

// kindsInfo is the metadata of kinds, as buildDescriptor makes it.
func kindsInfo(t *testing.T) *EntityInfo {
	t.Helper()
	x := new(kinds)
	base := unsafe.Pointer(x)
	ptrs := []any{&x.I64, &x.I32, &x.I, &x.S, &x.B, &x.F, &x.T, &x.PI32, &x.PI, &x.PB, &x.PF, &x.PT, &x.PI8, &x.U16, &x.Raw, &x.M}
	names := []string{"i64", "i32", "i", "s", "b", "f", "t", "pi32", "pi", "pb", "pf", "pt", "pi8", "u16", "raw", "m"}
	cols := make([]ColumnDescriptor, len(ptrs))
	for i, n := range names {
		cols[i] = ColumnDescriptor{Field: n, Name: n, Nullable: n == "raw" || n == "m"}
	}
	info, err := buildDescriptor(reflect.TypeFor[kinds](), base, func() DescriptorView {
		return DescriptorView{D: &DescriptorFields{Table: "kinds", Columns: cols, PrimaryKey: []int{0}},
			FieldPtr: func(i int) any { return ptrs[i] }}
	})
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// TestColumnsByNameEveryKind: each field type stores, loads and reports
// zero by name, through its fast path or reflect.
func TestColumnsByNameEveryKind(t *testing.T) {
	info := kindsInfo(t)
	var x kinds
	p := unsafe.Pointer(&x)
	var rec error
	r := NewRow(info, p, &rec, false)
	for _, c := range info.Columns {
		if !r.IsZero(c.Name) || (c.Nullable && r.Value(c.Name) != nil) {
			t.Errorf("%s: not zero in a zero row", c.Name)
		}
	}
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	vals := map[string]any{"i64": int64(1), "i32": int32(2), "i": 3, "s": "s", "b": true, "f": 1.5, "t": at,
		"pi32": int32(4), "pi": 5, "pb": true, "pf": 2.5, "pt": at, "pi8": int8(6), "u16": uint16(7), "raw": []byte("r"), "m": map[string]int{"a": 1}}
	for name, v := range vals {
		if err := r.Set(name, v); err != nil {
			t.Fatalf("Set(%q): %v", name, err)
		}
	}
	for name, v := range vals {
		if got := r.Value(name); !reflect.DeepEqual(got, v) {
			t.Errorf("Value(%q) = %#v, want %#v", name, got, v)
		}
		if r.IsZero(name) {
			t.Errorf("IsZero(%q) after Set", name)
		}
	}
	// Converted values take reflect's path even on a fast field.
	if err := r.Set("i32", int64(9)); err != nil || x.I32 != 9 {
		t.Errorf("Set(i32, int64): %v, %d", err, x.I32)
	}
	if err := r.Set("pi8", 8); err != nil || *x.PI8 != 8 {
		t.Errorf("Set(pi8, int): %v", err)
	}
	for _, name := range []string{"pi32", "pt", "raw", "m"} {
		if err := r.Set(name, nil); err != nil || r.Value(name) != nil || !r.IsZero(name) {
			t.Errorf("Set(%q, nil): %v, %v", name, err, r.Value(name))
		}
	}
	if rec != nil {
		t.Fatal(rec)
	}
}

// TestResolveConflictWhereByName: an upsert's update-branch conditions by
// name resolve on a copy of the clause.
func TestResolveConflictWhereByName(t *testing.T) {
	info := kindsInfo(t)
	cf := &ConflictClause{Columns: []int{0}, Update: []int{1}, Where: []Predicate{FieldEQ("s", "x")}}
	st := &Statement{Op: OpInsert, Entity: info, Conflict: cf}
	if err := ResolveStatement(st); err != nil {
		t.Fatal(err)
	}
	if st.Conflict == cf || st.Conflict.Where[0].n.Op != OpEQ || cf.Where[0].n.Op != opNamed {
		t.Errorf("the clause %+v, the caller's %+v", st.Conflict.Where, cf.Where)
	}
	st = &Statement{Op: OpInsert, Entity: info, Conflict: &ConflictClause{Where: []Predicate{FieldEQ("nope", 1)}}}
	if err := ResolveStatement(st); err == nil || !strings.Contains(err.Error(), `no column "nope"`) {
		t.Errorf("err %v", err)
	}
	st = &Statement{Op: OpSelect, Entity: info, Where: []Predicate{FieldEQ("nope", 1)}}
	if err := ResolveStatement(st); err == nil {
		t.Error("a Where of no column: no error")
	}
}

// TestResolveNamedLeftovers: what no constructor makes still fails closed.
func TestResolveNamedLeftovers(t *testing.T) {
	info := kindsInfo(t)
	n := FieldEQ("s", "x").n
	n.Col = n.Col&^0xff | 99
	if _, err := resolveNamed(info, n); err == nil || !strings.Contains(err.Error(), "no condition operator 99") {
		t.Errorf("err %v", err)
	}
	if article(nil) != "nil" || article(reflect.TypeFor[int64]()) != "an int64" {
		t.Error(article(nil))
	}
	// A group without a name in it is returned as it is.
	g := Group(OpAnd, []Node{{Op: OpEQ, Col: 0, V: int64(1)}})
	if r, err := resolveNamed(info, g); err != nil || r.Kids[0].Op != OpEQ {
		t.Errorf("%v %v", r, err)
	}
}

// TestMutationByNameMisuse: a mutation with no entity, and the rows a range
// stops early.
func TestMutationByNameMisuse(t *testing.T) {
	var mu MutationState
	mu.Op = OpInsert
	if err := mu.SetByName("x", 1); err == nil || mu.Err == nil {
		t.Errorf("Set with no entity: %v", err)
	}
	info := kindsInfo(t)
	a, b := new(kinds), new(kinds)
	mu = MutationState{Op: OpInsert, In: info, Rows: []unsafe.Pointer{unsafe.Pointer(a), unsafe.Pointer(b)}}
	n := 0
	for range mu.RowsByName() {
		n++
		break
	}
	if n != 1 {
		t.Errorf("%d rows after break", n)
	}
	st := &Statement{Op: OpUpdate, Entity: info}
	mu = MutationState{St: st}
	if err := mu.SetByName("s", nil); err == nil {
		t.Error("Set(s, nil) on an update: no error")
	}
	if err := mu.SetByName("raw", nil); err != nil || len(st.Set) != 1 || st.Set[0].Value != nil {
		t.Errorf("Set(raw, nil): %v, %v", err, st.Set)
	}
}
