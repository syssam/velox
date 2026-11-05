package codegen

import (
	"go/constant"
	"go/types"
	"testing"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

// A column's kind and Go type are read from its field: its Go type, else
// its field type; a pointer or an interface Go type has no kind velox-gen
// compares.
func TestNamedColumnKind(t *testing.T) {
	e := &Entity{Name: "User"}
	for _, c := range []struct {
		f          Field
		kind, name string
	}{
		{Field{Type: "bool"}, "bool", "bool"},
		{Field{Type: "uint16"}, "uint16", "uint16"},
		{Field{Type: "enum", GoName: "Status"}, "string", "UserStatus"},
		{Field{Type: "bytes"}, "slice", "[]byte"},
		{Field{Type: "time"}, "struct", "time.Time"},
		{Field{Type: "json"}, "", "json"},
		{Field{Type: "uuid", GoType: &loadjson.GoType{Ident: "uuid.UUID", Kind: "array"}}, "array", "uuid.UUID"},
		{Field{Type: "json", GoType: &loadjson.GoType{Ident: "*T", Kind: "ptr"}}, "", "*T"},
		{Field{Type: "other", GoType: &loadjson.GoType{Ident: "any", Kind: "interface"}}, "", "any"},
	} {
		col := namedColumn{e: e, f: &c.f}
		if k, n := col.kind(), col.typeName(); k != c.kind || n != c.name {
			t.Errorf("%+v: %q %q, want %q %q", c.f, k, n, c.kind, c.name)
		}
	}
}

// typeKind gives the reflect kind of a Go type.
func TestTypeKind(t *testing.T) {
	str := types.Typ[types.String]
	for typ, want := range map[types.Type]string{
		types.Typ[types.Bool]:              "bool",
		str:                                "string",
		types.Typ[types.Uint32]:            "uint32",
		types.NewStruct(nil, nil):          "struct",
		types.NewArray(str, 2):             "array",
		types.NewSlice(str):                "slice",
		types.NewMap(str, str):             "map",
		types.NewPointer(str):              "ptr",
		types.NewChan(types.SendRecv, str): "chan",
		types.Typ[types.UnsafePointer]:     "",
		types.NewSignatureType(nil, nil, nil, nil, nil, false): "func",
	} {
		if got := typeKind(typ); got != want {
			t.Errorf("%v: %q, want %q", typ, got, want)
		}
	}
}

// constFits says whether a constant fits an integer or a float kind; a
// constant that is not an integer is left to velox's conversion.
func TestConstFits(t *testing.T) {
	big := constant.MakeUint64(1 << 63)
	for _, c := range []struct {
		v    constant.Value
		kind string
		want bool
	}{
		{constant.MakeInt64(-32768), "int16", true}, {constant.MakeInt64(32768), "int16", false},
		{constant.MakeInt64(1 << 31), "int32", false}, {constant.MakeInt64(65535), "uint16", true},
		{constant.MakeInt64(1 << 32), "uint32", false}, {constant.MakeInt64(-1), "uint", false},
		{big, "uint64", true}, {big, "int64", false}, {big, "int", false},
		{constant.MakeFloat64(1.5), "int64", true},
		{constant.MakeFloat64(1e300), "float64", true}, {constant.MakeString("x"), "string", true},
	} {
		if got := constFits(c.v, c.kind); got != c.want {
			t.Errorf("%v in %s: %v, want %v", c.v, c.kind, got, c.want)
		}
	}
}

// byLine orders *FileErrors by file and line, and leaves other errors in
// place.
func TestByLine(t *testing.T) {
	a := &FileError{Path: "a.go", Line: 9}
	b := &FileError{Path: "a.go", Line: 10}
	c := &FileError{Path: "b.go", Line: 1}
	got := byLine([]error{c, b, a})
	if got[0] != a || got[1] != b || got[2] != c {
		t.Errorf("%v", got)
	}
	other := &Error{Reason: "x"}
	if got := byLine([]error{other, a}); got[0] != other {
		t.Errorf("%v", got)
	}
}
