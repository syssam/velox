package field_test

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/internal/desc"
)

type id [16]byte

func (id) Value() (driver.Value, error) { return nil, nil }

type role string

func (role) Values() []string { return []string{"viewer", "admin"} }

type celsius float64

var _ schema.Field = field.String("x") // every builder is a schema.Field

func TestStringOptions(t *testing.T) {
	d := field.String("name").NotEmpty().MaxLen(100).Match(regexp.MustCompile(`^[a-z]+$`)).
		Optional().Nillable().Immutable().Unique().Sensitive().Comment("c").StorageKey("nm").
		SchemaType(map[string]string{"postgres": "citext"}).StructTag(`json:"-"`).Tenant().Default("x").Descriptor()
	if len(d.Errs) != 0 {
		t.Fatal(d.Errs)
	}
	if d.Name != "name" || d.Type != "string" || d.Size != 100 || !d.Optional || !d.Nillable || !d.Immutable ||
		!d.Unique || !d.Sensitive || !d.Tenant || d.Comment != "c" || d.StorageKey != "nm" ||
		d.SchemaType["postgres"] != "citext" || d.StructTag != `json:"-"` || d.Default != "x" {
		t.Fatalf("descriptor %+v", d)
	}
	want := []string{"NotEmpty", "MaxLen", "Match"}
	for i, c := range d.Checks {
		if c.Kind != want[i] {
			t.Fatalf("checks %+v, want kinds %v", d.Checks, want)
		}
	}
	if d.Checks[2].Args[0] != `^[a-z]+$` {
		t.Fatalf("Match records the pattern: %+v", d.Checks[2])
	}
}

func TestTextSize(t *testing.T) {
	if d := field.Text("body").Descriptor(); d.Type != "string" || d.Size != math.MaxInt32 {
		t.Fatalf("%+v", d)
	}
}

func TestNumberTypes(t *testing.T) {
	for _, c := range []struct {
		f    schema.Field
		want string
	}{
		{field.Int("a"), "int"}, {field.Int8("a"), "int8"}, {field.Int16("a"), "int16"}, {field.Int32("a"), "int32"},
		{field.Int64("a"), "int64"}, {field.Uint("a"), "uint"}, {field.Uint8("a"), "uint8"}, {field.Uint16("a"), "uint16"},
		{field.Uint32("a"), "uint32"}, {field.Uint64("a"), "uint64"}, {field.Float32("a"), "float32"}, {field.Float64("a"), "float64"},
		{field.Float("a"), "float64"},
	} {
		if got := c.f.Descriptor().Type; got != c.want {
			t.Errorf("got %s, want %s", got, c.want)
		}
	}
	d := field.Int64("n").Range(1, 9).Positive().Default(3).Descriptor()
	if d.Default != int64(3) || d.Checks[0].Kind != "Range" || d.Checks[0].Args[1] != int64(9) || d.Checks[1].Kind != "Positive" {
		t.Fatalf("%+v", d)
	}
}

func TestGoType(t *testing.T) {
	d := field.Float64("temp").GoType(celsius(0)).Descriptor()
	if len(d.Errs) != 0 || d.GoType.Ident != "field_test.celsius" {
		t.Fatalf("%+v %v", d.GoType, d.Errs)
	}
	if d := field.String("x").GoType(0).Descriptor(); len(d.Errs) == 0 {
		t.Fatal("an int GoType on a string field is an error")
	}
}

func TestDefaultFuncs(t *testing.T) {
	d := field.Time("created_at").Default(time.Now).UpdateDefault(time.Now).Immutable().Descriptor()
	if len(d.Errs) != 0 || d.Default == nil || d.UpdateDefault == nil {
		t.Fatalf("%+v", d)
	}
	for name, f := range map[string]schema.Field{
		"time default not a func":   field.Time("t").Default(time.Time{}),
		"wrong result type":         field.String("s").DefaultFunc(func() int { return 0 }),
		"func with an argument":     field.Int64("n").DefaultFunc(func(int) int64 { return 0 }),
		"update default not a func": field.Int64("n").UpdateDefault(3),
	} {
		if errors.Join(f.Descriptor().Errs...) == nil {
			t.Errorf("%s: no error", name)
		}
	}
	type name string
	if d := field.String("s").GoType(name("")).DefaultFunc(func() name { return "x" }).Descriptor(); len(d.Errs) != 0 {
		t.Errorf("a DefaultFunc of the GoType is allowed: %v", d.Errs)
	}
}

func TestDefaultAndDefaultFunc(t *testing.T) {
	err := errors.Join(field.Int64("n").Default(1).DefaultFunc(func() int64 { return 2 }).Descriptor().Errs...)
	if err == nil || err.Error() != "Default and DefaultFunc are both set" {
		t.Fatalf("got %v", err)
	}
	// A time or uuid field's Default is a func and counts as the default. Their
	// builders have no DefaultFunc, so set it on the descriptor; Descriptor
	// checks again on every call.
	for name, f := range map[string]schema.Field{
		"time": field.Time("t").Default(time.Now),
		"uuid": field.UUID("t", id{}).Default(func() id { return id{} }),
	} {
		d := f.Descriptor()
		if len(d.Errs) != 0 {
			t.Fatalf("%s: %v", name, d.Errs)
		}
		d.DefaultFunc = d.Default
		if err := errors.Join(f.Descriptor().Errs...); err == nil || err.Error() != "Default and DefaultFunc are both set" {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestTypedNilFuncs(t *testing.T) {
	for name, c := range map[string]struct {
		f      schema.Field
		option string
	}{
		"DefaultFunc":   {field.Int64("n").DefaultFunc((func() int64)(nil)), "DefaultFunc"},
		"UpdateDefault": {field.Int64("n").UpdateDefault((func() int64)(nil)), "UpdateDefault"},
		"time Default":  {field.Time("t").Default((func() time.Time)(nil)), "Default"},
		"uuid Default":  {field.UUID("u", id{}).Default((func() id)(nil)), "Default"},
	} {
		err := errors.Join(c.f.Descriptor().Errs...)
		if err == nil || !strings.Contains(err.Error(), c.option+" must be a func() ") {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestEnum(t *testing.T) {
	d := field.Enum("role").Values("viewer", "admin").Default("viewer").Descriptor()
	if len(d.Errs) != 0 || d.Type != "enum" || !reflect.DeepEqual(d.Enums, []desc.Enum{{Name: "viewer", Value: "viewer"}, {Name: "admin", Value: "admin"}}) || d.Default != "viewer" {
		t.Fatalf("%+v", d)
	}
	if d := field.Enum("s").NamedValues("InProgress", "in-progress").Descriptor(); d.Enums[0].Name != "InProgress" || d.Enums[0].Value != "in-progress" {
		t.Fatalf("%+v", d.Enums)
	}
	if d := field.Enum("r").Values("a").Default("b").Descriptor(); len(d.Errs) == 0 {
		t.Fatal("a default outside the values is an error")
	}
	if d := field.Enum("r").NamedValues("A").Descriptor(); len(d.Errs) == 0 {
		t.Fatal("odd NamedValues is an error")
	}
	if d := field.Enum("r").GoType(role("")).Descriptor(); len(d.Errs) != 0 || len(d.Enums) != 2 || d.GoType.Ident != "field_test.role" {
		t.Fatalf("%+v %v", d, d.Errs)
	}
}

func TestTypedConstructors(t *testing.T) {
	if d := field.UUID("id", id{}).Descriptor(); d.Type != "uuid" || d.GoType.Ident != "field_test.id" {
		t.Fatalf("%+v", d)
	}
	if d := field.UUID("id", nil).Descriptor(); len(d.Errs) == 0 {
		t.Fatal("a nil UUID type is an error")
	}
	if d := field.Strings("tags").Descriptor(); d.Type != "json" || d.GoType.Ident != "[]string" {
		t.Fatalf("%+v", d)
	}
	if d := field.Other("link", id{}).SchemaType(map[string]string{"postgres": "bytea"}).Descriptor(); d.Type != "other" || len(d.Errs) != 0 {
		t.Fatalf("%+v", d)
	}
}

func TestValidate(t *testing.T) {
	bad := errors.New("bad")
	d := field.String("s").Validate(func(string) error { return bad }).Descriptor()
	if len(d.Validators) != 1 || d.Validators[0].(func(string) error)("") != bad {
		t.Fatalf("%+v", d)
	}
}

func TestErrorNamesOption(t *testing.T) {
	if err := errors.Join(field.Int64("n").DefaultFunc(1).Descriptor().Errs...); err == nil || !strings.Contains(err.Error(), "DefaultFunc") {
		t.Fatalf("%v", err)
	}
}

// Every constructor records the line that calls it, also one that calls
// another constructor, such as Text, which calls String.
func TestPos(t *testing.T) {
	_, file, line, _ := runtime.Caller(0)
	for i, f := range []interface{ Descriptor() *desc.Field }{
		field.Bool("a"),
		field.Int("a"),
		field.Int8("a"),
		field.Int16("a"),
		field.Int32("a"),
		field.Int64("a"),
		field.Uint("a"),
		field.Uint8("a"),
		field.Uint16("a"),
		field.Uint32("a"),
		field.Uint64("a"),
		field.Float("a"),
		field.Float32("a"),
		field.Float64("a"),
		field.String("a"),
		field.Text("a"),
		field.Bytes("a"),
		field.Time("a"),
		field.UUID("a", id{}),
		field.JSON("a", map[string]int{}),
		field.Strings("a"),
		field.Ints("a"),
		field.Floats("a"),
		field.Other("a", id{}),
		field.Enum("a"),
	} {
		if got, want := desc.PosOf(f.Descriptor().PC), fmt.Sprintf("%s:%d", file, line+2+i); got != want {
			t.Errorf("%d: pos %q, want %q", i, got, want)
		}
	}
}

// A builder records every misuse, and Descriptor, called again, adds none.
func TestEveryMisuse(t *testing.T) {
	b := field.Int64("n").Validate(nil).DefaultFunc(1).GoType("x")
	b.Descriptor()
	var got []string
	for _, err := range b.Descriptor().Errs {
		got = append(got, err.Error())
	}
	want := []string{"Validate is given a nil function", "GoType string has kind string; the field needs int64", "DefaultFunc must be a func() int64"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
