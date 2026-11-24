package engine

import (
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

// kindDec is a decimal type with Value and Scan, as shopspring's is: its
// column's kind is not known.
type kindDec struct{ s string }

func (d kindDec) Value() (driver.Value, error) { return d.s, nil }
func (d *kindDec) Scan(any) error              { return nil }

// kindInfo is an entity with a column of each kind columnKind tells.
func kindInfo() *EntityInfo {
	col := func(name string, typ reflect.Type) ColumnInfo { return ColumnInfo{Name: name, Type: typ} }
	dec := col("price", reflect.TypeFor[string]())
	dec.decimal = [2]uint8{10, 2}
	coded := col("doc", reflect.TypeFor[map[string]any]())
	coded.Codec = true
	return &EntityInfo{Name: "Kinds", Columns: []ColumnInfo{
		col("n", reflect.TypeFor[int64]()),       // 0
		col("u", reflect.TypeFor[*uint32]()),     // 1
		col("f", reflect.TypeFor[float64]()),     // 2
		col("s", reflect.TypeFor[string]()),      // 3
		col("b", reflect.TypeFor[bool]()),        // 4
		col("at", reflect.TypeFor[*time.Time]()), // 5
		col("raw", reflect.TypeFor[[]byte]()),    // 6
		col("d", reflect.TypeFor[kindDec]()),     // 7
		dec,                                      // 8
		coded,                                    // 9
		col("xs", reflect.TypeFor[[]int]()),      // 10
		{Name: "untyped"},                        // 11
	}}
}

// TestTermValueKind: what each term statically gives: a column by its Go
// type, a JSON path by its kind, a date bucket a time, a Decimal
// expression a number, a joined term by the joined entity's term, and a
// Calc or a JSON path's raw value nothing known.
func TestTermValueKind(t *testing.T) {
	in := kindInfo()
	target := kindInfo()
	rel := &relation{Name: "other", typ: reflect.TypeFor[int](), link: Link{Target: target}}
	rel.once.Do(func() {})
	in.rels = []*relation{rel}
	ref := RelRef{
		Owner:  func() (*EntityInfo, error) { return in, nil },
		Target: RelTarget{Get: func() (*EntityInfo, error) { return target, nil }, One: reflect.TypeFor[int]()},
	}
	bad := RelRef{Owner: func() (*EntityInfo, error) { return nil, errors.New("no") }}
	path := func(k JSONKind) Term { return JSONTerm(9, JSONPath{Kind: k, Keys: []string{"k"}}) }
	calc := ExprTerm("? + 1", []ExprSlot{{Col: 0}}, nil)
	for _, c := range []struct {
		name string
		t    Term
		want valueKind
	}{
		{"int64", ColumnTerm(0), kindNumber},
		{"*uint32", ColumnTerm(1), kindNumber},
		{"float64", ColumnTerm(2), kindNumber},
		{"string", ColumnTerm(3), kindText},
		{"bool", ColumnTerm(4), kindBool},
		{"*time.Time", ColumnTerm(5), kindTime},
		{"[]byte", ColumnTerm(6), kindBytes},
		{"Valuer", ColumnTerm(7), kindUnknown},
		{"MySQL decimal", ColumnTerm(8), kindNumber},
		{"encoded", ColumnTerm(9), kindUnknown},
		{"[]int", ColumnTerm(10), kindUnknown},
		{"no Go type", ColumnTerm(11), kindUnknown},
		{"no such column", ColumnTerm(99), kindUnknown},
		{"JSON any", path(JSONKindAny), kindUnknown},
		{"JSON text", path(JSONKindText), kindText},
		{"JSON number", path(JSONKindNumber), kindNumber},
		{"JSON bool", path(JSONKindBool), kindBool},
		{"JSON len", path(JSONKindLen), kindNumber},
		{"JSON text number", path(JSONKindTextNumber), kindNumber},
		{"JSON text time", path(JSONKindTextTime), kindTime},
		{"date bucket", DateTruncTerm(DateDay, ColumnTerm(5), "", 1), kindTime},
		{"calc", calc, kindUnknown},
		{"decimal calc", DecimalTerm(calc), kindNumber},
		{"joined text", JoinedTerm(ref, ColumnTerm(3)), kindText},
		{"joined JSON number", JoinedTerm(ref, path(JSONKindTextNumber)), kindNumber},
		{"joined, unresolved", JoinedTerm(bad, ColumnTerm(3)), kindUnknown},
	} {
		if got := termValueKind(in, c.t); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got.name(), c.want.name())
		}
	}
}

// TestCheckValueKind: on each dialect a value of another kind than its
// term's is refused with ErrOrderValue, and one of the kind each driver
// reads is taken; NULL is every kind's, and a term of unknown kind takes
// anything.
func TestCheckValueKind(t *testing.T) {
	in := kindInfo()
	number := JSONTerm(9, JSONPath{Kind: JSONKindTextNumber, Keys: []string{"k"}})
	when := JSONTerm(9, JSONPath{Kind: JSONKindTextTime, Keys: []string{"k"}})
	text := JSONTerm(9, JSONPath{Kind: JSONKindText, Keys: []string{"k"}})
	flag := JSONTerm(9, JSONPath{Kind: JSONKindBool, Keys: []string{"k"}})
	raw := ColumnTerm(6)
	calc := ExprTerm("? + 1", []ExprSlot{{Col: 0}}, nil)
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	all, none := [3]bool{true, true, true}, [3]bool{}
	for _, c := range []struct {
		term Term
		v    any
		ok   [3]bool // SQLite, PostgreSQL, MySQL
	}{
		{number, int64(3), all},
		{number, uint64(math.MaxUint64), all},
		{number, int32(3), all},
		{number, 2.5, all},
		{number, float32(2.5), all},
		{number, DecimalText("1.25"), all},
		{number, nil, all},
		{number, "abc", none},
		{number, "1.5", none},
		{number, true, none},
		{number, at, none},
		{number, TimeText("2026-03-01"), none},
		{when, at, all},
		{when, TimeText("2026-03-01 10:00:00"), all},
		{when, "2026-03-01 10:00:00.000000000+00:00", [3]bool{true, false, false}},
		{when, int64(1), none},
		{when, DecimalText("1"), none},
		{text, "abc", all},
		{text, int64(1), none},
		{text, DecimalText("1"), none},
		{text, TimeText("2026-03-01"), none},
		{text, at, none},
		{text, []byte("abc"), none},
		{flag, true, all},
		{flag, int64(1), [3]bool{true, false, true}},
		{flag, "true", none},
		{flag, 1.0, none},
		{raw, "ab", all},
		{raw, []byte("ab"), all},
		{raw, int64(1), none},
		{calc, "abc", all},
		{calc, int64(1), all},
		{calc, at, all},
	} {
		for i, d := range []Dialect{SQLite, Postgres, MySQL} {
			err := checkValueKind(d, in, c.term, c.v)
			if (err == nil) != c.ok[i] || (err != nil && !errors.Is(err, ErrOrderValue)) {
				t.Errorf("%v %s %#v: %v", d, termValueKind(in, c.term).name(), c.v, err)
			}
		}
	}
}

// TestValueSourceKind: AfterValues' source of an ordering by a JSON path's
// Number refuses text, as a client's edited cursor may hold, and takes a
// number.
func TestValueSourceKind(t *testing.T) {
	in := kindInfo()
	o := OrderTerm{Expr: JSONTerm(9, JSONPath{Kind: JSONKindTextNumber, Keys: []string{"k"}})}
	for _, d := range []Dialect{SQLite, Postgres, MySQL} {
		if _, err := valueSource(in, o, "abc", d); !errors.Is(err, ErrOrderValue) {
			t.Errorf("%v text: %v, want ErrOrderValue", d, err)
		}
		if _, err := valueSource(in, o, 1.5, d); err != nil {
			t.Errorf("%v number: %v", d, err)
		}
	}
}
