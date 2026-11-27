package gogen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/plugin"
)

// TestTermBaseOther: a connection orders by an Other field whose Go type is a
// struct (a decimal library's), held in a cursor as its own type, or by one
// whose kind is a number, a string or a bool, held as that kind; any other
// kind is refused, naming the schema, the field and the kind.
func TestTermBaseOther(t *testing.T) {
	e := &plugin.Entity{Name: "Ledger"}
	for _, c := range []struct {
		kind, elem, want, refused string
	}{
		{"struct", "decimal.Decimal", "decimal.Decimal", ""},
		{"string", "kind.Dec", "string", ""},
		{"int64", "kind.Cents", "int64", ""},
		{"float64", "kind.Ratio", "float64", ""},
		{"bool", "kind.Flag", "bool", ""},
		{"slice", "kind.Tags", "", `schema Ledger, field amount: a connection orders by it, and keyset paging cannot continue after an other value of Go type kind.Tags: its kind is "slice"`},
		{"", "kind.Mystery", "", `its kind is ""`},
	} {
		fd := &plugin.Field{Name: "amount", Type: "other", ElemType: c.elem, Kind: c.kind}
		got, err := termBase(newFile(), e, fd)
		switch {
		case c.refused == "" && (err != nil || got != c.want):
			t.Errorf("kind %q: %q, %v, want %q", c.kind, got, err, c.want)
		case c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)):
			t.Errorf("kind %q: %q, %v, want a refusal with %q", c.kind, got, err, c.refused)
		}
	}
}
