package engine

import (
	"strings"
	"testing"
)

func TestNullCell(t *testing.T) {
	n := 1
	in := &EntityInfo{Columns: []ColumnInfo{
		{Nullable: true},
		{Nullable: true, Codec: true},
		{},
	}}
	for _, c := range []struct {
		name string
		col  int
		v    any
		want bool
	}{
		{"nil interface", 0, nil, true},
		{"nil bytes", 0, []byte(nil), true},
		{"empty bytes", 0, []byte{}, false},
		{"nil pointer", 0, (*int)(nil), true},
		{"pointer", 0, &n, false},
		{"a plain value", 0, 4, false},
		{"codec column: nil encodes as JSON null", 1, nil, false},
		{"not nullable", 2, nil, false},
	} {
		if got := nullCell(in, c.col, c.v); got != c.want {
			t.Errorf("%s: nullCell = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPairUnique(t *testing.T) {
	for _, c := range []struct {
		name string
		j    EntityInfo
		want bool
	}{
		{"primary key of the pair", EntityInfo{PrimaryKey: []int{1, 0}}, true},
		{"primary key of more", EntityInfo{PrimaryKey: []int{0, 1, 2}}, false},
		{"no key", EntityInfo{}, false},
		{"unique index of the pair", EntityInfo{PrimaryKey: []int{2}, Indexes: []IndexInfo{{Columns: []int{0}}, {Unique: true, Columns: []int{0, 1}}}}, true},
		{"index that is not unique", EntityInfo{PrimaryKey: []int{2}, Indexes: []IndexInfo{{Columns: []int{0, 1}}}}, false},
		{"unique index of more", EntityInfo{PrimaryKey: []int{2}, Indexes: []IndexInfo{{Unique: true, Columns: []int{0, 1, 2}}}}, false},
	} {
		if got := pairUnique(&c.j, 0, 1); got != c.want {
			t.Errorf("%s: pairUnique = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTableRowsAtOutOfRange(t *testing.T) {
	for _, i := range []int{-1, 0, 3} {
		func() {
			defer func() {
				r := recover()
				msg, _ := r.(string)
				if !strings.Contains(msg, "TableRows.At(") || !strings.Contains(msg, "of 0 rows") {
					t.Errorf("at(%d) panicked with %v, want a TableRows.At range message", i, r)
				}
			}()
			TableRows{}.at(i)
		}()
	}
}
