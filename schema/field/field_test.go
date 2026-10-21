package field_test

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/internal/desc"
)

type id [16]byte

func (id) Value() (driver.Value, error) { return nil, nil }

type role string

func (role) Values() []string { return []string{"viewer", "admin"} }

type celsius float64

type cents int64

// stamp is a time type that converts from time.Time.
type stamp time.Time

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
		{field.Uint32("a"), "uint32"}, {field.Uint64("a"), "uint64"}, {field.Float32("a"), "float32"}, {field.Float("a"), "float64"},
		{field.Number[int16]("a"), "int16"}, {field.Number[cents]("a"), "int64"}, {field.Number[celsius]("a"), "float64"},
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
	d := field.Float("temp").GoType(celsius(0)).Descriptor()
	if len(d.Errs) != 0 || d.GoType.Ident != "field_test.celsius" {
		t.Fatalf("%+v %v", d.GoType, d.Errs)
	}
	if d := field.String("x").GoType(0).Descriptor(); len(d.Errs) == 0 {
		t.Fatal("an int GoType on a string field is an error")
	}
}

func TestDefaultFuncs(t *testing.T) {
	d := field.Time("created_at").Default(time.Now).UpdateDefault(time.Now).Descriptor()
	if len(d.Errs) != 0 || d.Default == nil || d.UpdateDefault == nil {
		t.Fatalf("%+v", d)
	}
	for name, f := range map[string]schema.Field{
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
		field.Number[cents]("a"),
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
		field.EnumOf[role]("a"),
		field.UUIDOf[id]("a"),
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

// Each exported builder lists its options in the package's documentation,
// also those it has from the embedded, unexported common and typed.
func TestBuildersAreDocumented(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, f)
	}
	pkg, err := doc.NewFromFiles(fset, parsed, "github.com/syssam/velox2/schema/field")
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string][]string{}
	for _, typ := range pkg.Types {
		for _, m := range typ.Methods {
			methods[typ.Name] = append(methods[typ.Name], m.Name)
		}
	}
	common := []string{"Optional", "Nillable", "Unique", "Comment", "StorageKey", "SchemaType", "Annotations", "Default"}
	for _, b := range []string{
		"BoolBuilder", "NumberBuilder", "StringBuilder", "BytesBuilder", "TimeBuilder",
		"UUIDBuilder", "UUIDOfBuilder", "JSONBuilder", "OtherBuilder", "EnumBuilder", "EnumOfBuilder",
	} {
		got, ok := methods[b]
		if !ok {
			t.Errorf("%s is not documented", b)
			continue
		}
		for _, m := range common {
			if !slices.Contains(got, m) {
				t.Errorf("%s: the doc lacks %s; it lists %v", b, m, got)
			}
		}
	}
}

// field.Number[Cents] is field.Int64 with the GoType Cents, whose options
// take a Cents.
func TestNumberValidateTakesT(t *testing.T) {
	var got cents
	d := field.Number[cents]("p").Nillable().Default(5).Min(1).Validate(func(c cents) error { got = c; return nil }).Descriptor()
	if len(d.Errs) != 0 || d.Type != "int64" || d.GoType == nil || d.GoType.Ident != "field_test.cents" || d.RType != reflect.TypeFor[cents]() {
		t.Fatalf("%+v %v", d, d.Errs)
	}
	if d.Default != cents(5) || d.Checks[0].Args[0] != cents(1) {
		t.Fatalf("default %#v, min %#v", d.Default, d.Checks[0].Args)
	}
	// The generated code calls a validator with the field's kind, as it does for
	// Int64("p").GoType(Cents(0)); the validator still receives a Cents.
	fn, ok := d.Validators[0].(func(int64) error)
	if !ok {
		t.Fatalf("validator %T, want func(int64) error", d.Validators[0])
	}
	if err := fn(7); err != nil || got != 7 {
		t.Fatalf("validator ran with %v: %v", got, err)
	}
	// A builtin T is that constructor, with no GoType.
	if d := field.Number[int64]("n").Validate(func(int64) error { return nil }).Descriptor(); d.GoType != nil || d.Type != "int64" {
		t.Fatalf("%+v", d)
	}
	if d := field.Number[cents]("p").Validate(nil).Descriptor(); len(d.Errs) != 1 || d.Errs[0].Error() != "Validate is given a nil function" {
		t.Fatalf("%v", d.Errs)
	}
}

func TestEnumOf(t *testing.T) {
	d := field.EnumOf[role]("r").Default("admin").Descriptor()
	if len(d.Errs) != 0 || d.Type != "enum" || d.GoType.Ident != "field_test.role" || len(d.Enums) != 2 || d.Default != "admin" {
		t.Fatalf("%+v %v", d, d.Errs)
	}
	if d := field.EnumOf[role]("r").Default("owner").Descriptor(); len(d.Errs) == 0 {
		t.Fatal("a default outside the values is an error")
	}
}

func TestUUIDOf(t *testing.T) {
	d := field.UUIDOf[id]("id").Default(func() id { return id{1} }).Descriptor()
	if len(d.Errs) != 0 || d.Type != "uuid" || d.GoType.Ident != "field_test.id" || d.Default.(func() id)() != (id{1}) {
		t.Fatalf("%+v %v", d, d.Errs)
	}
	if d := field.UUIDOf[id]("id").Default(nil).Descriptor(); len(d.Errs) == 0 {
		t.Fatal("a nil Default is an error")
	}
}

func TestTimeDefaultTyped(t *testing.T) {
	d := field.Time("t").Default(time.Now).UpdateDefault(time.Now).Descriptor()
	if len(d.Errs) != 0 || d.Default == nil || d.UpdateDefault == nil {
		t.Fatalf("%+v %v", d, d.Errs)
	}
	if d := field.Time("t").GoType(stamp{}).Default(time.Now).UpdateDefault(time.Now).Descriptor(); len(d.Errs) != 0 {
		t.Fatalf("a GoType convertible from time.Time: %v", d.Errs)
	}
	type wrapped struct{ time.Time }
	d = field.Time("t").GoType(wrapped{}).Default(time.Now).UpdateDefault(time.Now).Descriptor()
	var got []string
	for _, err := range d.Errs {
		got = append(got, err.Error())
	}
	want := []string{
		"Default on a time field needs a GoType convertible from time.Time",
		"UpdateDefault on a time field needs a GoType convertible from time.Time",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if d := field.Time("t").GoType(wrapped{}).Descriptor(); len(d.Errs) != 0 {
		t.Fatalf("a GoType with no default is not checked: %v", d.Errs)
	}
}

func TestBytesMaxLenSetsSize(t *testing.T) {
	d := field.Bytes("b").MaxLen(16).Descriptor()
	if d.Size != 16 || len(d.Checks) != 1 || d.Checks[0].Kind != "MaxLen" {
		t.Fatalf("%+v", d)
	}
}

func TestSchemaTypeDialects(t *testing.T) {
	// The keys are the core's dialect names; the external test package can
	// import the core, which the field package must not.
	for key, d := range map[string]velox.Dialect{field.Postgres: velox.Postgres, field.MySQL: velox.MySQL, field.SQLite: velox.SQLite} {
		if key != d.String() {
			t.Errorf("key %q, the core's name %q", key, d.String())
		}
	}
	d := field.String("s").SchemaType(map[string]string{field.Postgres: "citext", field.MySQL: "varchar(10)", field.SQLite: "text"}).Descriptor()
	if len(d.Errs) != 0 {
		t.Fatal(d.Errs)
	}
	d = field.String("s").SchemaType(map[string]string{"postgress": "citext", field.Postgres: "citext"}).Descriptor()
	if err := errors.Join(d.Errs...); err == nil || err.Error() != `SchemaType: unknown dialect "postgress"; the keys are field.Postgres, field.MySQL and field.SQLite` {
		t.Fatalf("got %v", err)
	}
}

// Options that contradict one another are builder errors, found once all are
// set, each reported once however often Descriptor is called.
func TestContradictions(t *testing.T) {
	now := func() int64 { return 0 }
	for _, c := range []struct {
		name string
		f    interface{ Descriptor() *desc.Field }
		want []string
	}{
		{"Min above Max", field.Int("n").Min(10).Max(1), []string{"Min 10 is above Max 1"}},
		{"Max before Min", field.Number[cents]("n").Max(1).Min(10), []string{"Min 10 is above Max 1"}},
		{"Min above Max, float", field.Float("n").Min(1.5).Max(0.5), []string{"Min 1.5 is above Max 0.5"}},
		{"Min above Max, uint64", field.Uint64("n").Min(math.MaxUint64).Max(1), []string{"Min 18446744073709551615 is above Max 1"}},
		{"empty Range", field.Int64("n").Range(10, 1), []string{"Range 10..1 is empty"}},
		{"Min twice", field.Int("n").Min(1).Min(2), []string{"Min is given twice"}},
		{"Max twice", field.Int("n").Max(1).Max(2), []string{"Max is given twice"}},
		{"Range twice", field.Int("n").Range(1, 2).Range(1, 3), []string{"Range is given twice"}},
		{"Min and Range", field.Int("n").Range(1, 5).Min(2), []string{"Min is given twice"}},
		{"Max and Range", field.Int("n").Max(5).Range(1, 4), []string{"Max is given twice"}},
		{"Range then a Min above its Max", field.Int("n").Range(1, 5).Min(9), []string{"Min is given twice"}},
		{"Min, Max and Range", field.Int("n").Min(1).Max(9).Range(2, 8), []string{"Min is given twice", "Max is given twice"}},
		{"Range twice and Min", field.Int("n").Range(1, 5).Range(1, 6).Min(2), []string{"Range is given twice", "Min is given twice"}},
		{"MinLen twice", field.String("s").MinLen(1).MinLen(2), []string{"MinLen is given twice"}},
		{"MaxLen twice", field.String("s").MaxLen(5).MaxLen(9), []string{"MaxLen is given twice"}},
		{"bytes MaxLen twice", field.Bytes("b").MaxLen(5).MaxLen(9), []string{"MaxLen is given twice"}},
		{"MinLen above MaxLen", field.String("s").MinLen(10).MaxLen(5), []string{"MinLen 10 is above MaxLen 5"}},
		{"bytes MinLen above MaxLen", field.Bytes("b").MaxLen(5).MinLen(10), []string{"MinLen 10 is above MaxLen 5"}},
		{"Immutable with UpdateDefault", field.Int64("n").UpdateDefault(now).Immutable(), []string{"an Immutable field cannot have an UpdateDefault"}},
		{"Immutable time with UpdateDefault", field.Time("t").Immutable().UpdateDefault(time.Now), []string{"an Immutable field cannot have an UpdateDefault"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.f.Descriptor()
			var got []string
			for _, err := range c.f.Descriptor().Errs {
				got = append(got, err.Error())
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
	for name, f := range map[string]interface{ Descriptor() *desc.Field }{
		"Min equal to Max":     field.Int("n").Min(3).Max(3),
		"one-value Range":      field.Int("n").Range(3, 3),
		"MinLen equal MaxLen":  field.String("s").MinLen(4).MaxLen(4),
		"Immutable Default":    field.Time("t").Default(time.Now).Immutable(),
		"UpdateDefault alone":  field.Time("t").UpdateDefault(time.Now),
		"bounds of each kind":  field.Int("n").Min(1).Max(9).Positive(),
		"negative Min and Max": field.Int8("n").Min(-9).Max(-1),
	} {
		if errs := f.Descriptor().Errs; len(errs) != 0 {
			t.Errorf("%s: %v", name, errs)
		}
	}
}
