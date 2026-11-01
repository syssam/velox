package field

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/internal/desc"
)

// common holds the options every field has. B is the builder that embeds it,
// so that each option returns the builder and a chain keeps its type.
type common[B any] struct {
	d    *desc.Field
	self B
}

// Optional makes the field not required on create. Its column stays NOT NULL
// and its Go field a value: an unset field is its zero value, and an insert
// writes its Default, or the zero value. The zero value means unset, so the
// field's validators let it pass. A JSON field is the exception: Optional
// makes its column NULL-able, as a slice or a map has no zero value to write,
// and NULL reads as nil.
//
// velox-gen refuses an Optional enum with neither a Default nor Nillable,
// whose zero value "" is no value; an Optional bytes field with neither,
// whose zero value nil binds NULL; an Optional time field with neither, whose
// zero time MySQL's strict mode refuses; and an Optional edge field that is
// not Nillable, whose zero value is no row's key. It refuses a Unique Optional
// field that is not Nillable, unless a DefaultFunc or a SchemaDefault gives each
// row its own value: every unset row would hold one value, and the second fail.
func (c common[B]) Optional() B { c.d.Optional = true; return c.self }

// Nillable makes the column NULL-able and the Go field a pointer, nil for
// NULL, with a Null* handle. A field whose Go type is nil-able already, such as
// a slice or a map, stays as it is. Optional().Nillable() is both: optional on
// create, and NULL as nil.
func (c common[B]) Nillable() B { c.d.Nillable = true; return c.self }

// Immutable refuses the field in updates. An Immutable field cannot have an
// UpdateDefault, which no update would call.
func (c common[B]) Immutable() B { c.d.Immutable = true; return c.self }

// Unique adds a single-column unique index, named as ent names it:
// <table>_<column>_key on PostgreSQL (cut at 63 bytes, as PostgreSQL cuts
// it) and on SQLite, and the column's name on MySQL. A field that is unique
// only when present needs Nillable: NULLs do not collide, but the zero values
// of unset Optional fields do. velox-gen refuses a Unique JSON field: no index
// covers a JSON column.
func (c common[B]) Unique() B { c.d.Unique = true; return c.self }

// Sensitive leaves the field out of String(), JSON, logs and spans.
func (c common[B]) Sensitive() B { c.d.Sensitive = true; return c.self }

// Tenant marks the column that scopes every row to a tenant.
func (c common[B]) Tenant() B { c.d.Tenant = true; return c.self }

// Comment sets the field's documentation.
func (c common[B]) Comment(s string) B { c.d.Comment = s; return c.self }

// StorageKey sets the column name.
func (c common[B]) StorageKey(s string) B { c.d.StorageKey = s; return c.self }

// The dialects SchemaType knows, by the names the core gives them.
const (
	Postgres = "postgres"
	MySQL    = "mysql"
	SQLite   = "sqlite"
)

// SchemaType sets the column type per dialect, such as
// {field.Postgres: "citext"}. The keys are Postgres, MySQL and SQLite; any
// other is a misuse.
//
// On an enum field, a PostgreSQL value that is not a string type names the
// enum type; the planner creates and manages it, and fields may share it. A
// string type opts that dialect out of the native enum, to text plus a CHECK.
func (c common[B]) SchemaType(m map[string]string) B {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if k != Postgres && k != MySQL && k != SQLite {
			fail(c.d, "SchemaType: unknown dialect %q; the keys are field.Postgres, field.MySQL and field.SQLite", k)
		}
	}
	c.d.SchemaType = maps.Clone(m)
	return c.self
}

// SchemaDefault sets the column's default in the database per dialect, as
// SQL written as the database prints it, such as {field.Postgres: "now()"}.
// velox's inserts write every column, so it serves rows written outside
// velox and existing rows when the column is added. The keys are Postgres,
// MySQL and SQLite; any other, or an empty value, is a misuse. A time's default
// on SQLite must write velox's form, not CURRENT_TIMESTAMP's: see the velox
// package's SQLite section.
func (c common[B]) SchemaDefault(m map[string]string) B {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		switch {
		case k != Postgres && k != MySQL && k != SQLite:
			fail(c.d, "SchemaDefault: unknown dialect %q; the keys are field.Postgres, field.MySQL and field.SQLite", k)
		case m[k] == "":
			fail(c.d, "SchemaDefault: the %s default is empty", k)
		}
	}
	c.d.SchemaDefault = maps.Clone(m)
	return c.self
}

// StructTag sets the struct tag of the generated Go field.
func (c common[B]) StructTag(s string) B { c.d.StructTag = s; return c.self }

// Annotations attaches annotations to the field.
func (c common[B]) Annotations(as ...schema.Annotation) B {
	c.d.Annotations = append(c.d.Annotations, as...)
	return c.self
}

// Descriptor returns the field's descriptor, for velox-gen; its type is
// internal to velox (see schema.Field). It checks the options that depend on
// one another, so it may be called more than once.
func (c common[B]) Descriptor() *desc.Field { finish(c.d); return c.d }

// fail records a misuse of a builder. The loader names the field.
func fail(d *desc.Field, format string, args ...any) {
	d.Errs = append(d.Errs, fmt.Errorf(format, args...))
}

// failOnce records a misuse that finish finds, unless an earlier Descriptor
// call recorded it already.
func failOnce(d *desc.Field, format string, args ...any) {
	err := fmt.Errorf(format, args...)
	for _, e := range d.Errs {
		if e.Error() == err.Error() {
			return
		}
	}
	d.Errs = append(d.Errs, err)
}

// typed holds the options of a field whose values are of the Go type V. A
// builder embeds it beside its common, which c points to: go/doc lists the
// options of both under the builder, as it would not those of a common that
// typed embedded.
type typed[B, V any] struct{ c *common[B] }

// newTyped returns the typed options of the builder whose common is c.
func newTyped[B, V any](c *common[B]) typed[B, V] { return typed[B, V]{c: c} }

// Default sets the value a new row gets when the field is not set. It never
// changes the Go type. A field that is not a pointer is unset when it holds its
// zero value, so velox-gen refuses a Default on a bool or number that is not
// Nillable, which could then never store false or 0: make the field Nillable
// or drop the Default. On a string that is not Nillable, "" is unset and takes
// the Default: Insert cannot store it, though Update can. A Default that the
// field's own built-in checks refuse is an error: every row that left the field
// unset would fail to insert. A custom validator is not called on it.
func (t typed[B, V]) Default(v V) B { t.c.d.Default = v; return t.c.self }

// DefaultFunc sets a func() returning the field's Go type, called when a new
// row does not set the field. As with Default, a field that is not a pointer
// is unset when it holds its zero value, so velox-gen refuses a DefaultFunc on
// a bool or number that is not Nillable.
func (t typed[B, V]) DefaultFunc(fn any) B { t.c.d.DefaultFunc = fn; return t.c.self }

// UpdateDefault sets a func() returning the field's Go type, called on every
// update that does not set the field. An upsert's update branch does not call
// it.
func (t typed[B, V]) UpdateDefault(fn any) B { t.c.d.UpdateDefault = fn; return t.c.self }

// Validate adds a function that checks the field's value.
// An Optional field's zero value means unset, and is not validated.
func (t typed[B, V]) Validate(fn func(V) error) B {
	if fn == nil {
		fail(t.c.d, "Validate is given a nil function")
	} else {
		t.c.d.Validators = append(t.c.d.Validators, fn)
	}
	return t.c.self
}

// GoType makes the field's Go type that of typ, which must have the field's kind.
func (t typed[B, V]) GoType(typ any) B {
	setGoType(t.c.d, typ, reflect.TypeFor[V]())
	return t.c.self
}

// setGoType makes the Go type of d that of typ, if typ has the kind of base,
// and reports whether it did and velox-gen can write it.
func setGoType(d *desc.Field, typ any, base reflect.Type) bool {
	if typ == nil {
		fail(d, "GoType is given a nil type")
		return false
	}
	t := reflect.TypeOf(typ)
	if !sameKind(t, base) {
		fail(d, "GoType %v has kind %s; the field needs %s", t, t.Kind(), base.Kind())
		return false
	}
	d.GoType = desc.GoTypeOf(t)
	d.RType = t
	return writable(d, t)
}

// writable fails d, and reports false, when velox-gen cannot write t, its
// Go type, as Go source.
func writable(d *desc.Field, t reflect.Type) bool {
	if r := desc.Unwritable(t); r != "" {
		fail(d, "GoType %v %s, which velox-gen cannot write; declare a named type for it", t, r)
		return false
	}
	return true
}

// sameKind reports whether t has the kind of base. A slice must also have an
// element of base's element kind, so that []byte is not any slice.
func sameKind(t, base reflect.Type) bool {
	if t.Kind() != base.Kind() {
		return false
	}
	return base.Kind() != reflect.Slice || t.Elem().Kind() == base.Elem().Kind()
}

// finish checks the options that depend on one another. It runs on every
// Descriptor call and records each error only once.
func finish(d *desc.Field) {
	contradictions(d)
	defaultPasses(d)
	if d.Default != nil && d.DefaultFunc != nil {
		failOnce(d, "Default and DefaultFunc are both set")
	}
	checkFunc(d, "DefaultFunc", d.DefaultFunc)
	if d.Type == "time" {
		checkTimeFunc(d, "Default", d.Default)
		checkTimeFunc(d, "UpdateDefault", d.UpdateDefault)
		return
	}
	checkFunc(d, "UpdateDefault", d.UpdateDefault)
	switch d.Type {
	case "uuid":
		checkFunc(d, "Default", d.Default)
	case "enum":
		if d.Default == nil {
			return
		}
		for _, e := range d.Enums {
			if e.Value == d.Default {
				return
			}
		}
		failOnce(d, "Default %v is not one of the enum values", d.Default)
	}
}

// checkTimeFunc fails unless fn, when set, is a non-nil func() time.Time, and
// the field's Go type converts from time.Time, as the generated code converts
// what fn returns.
func checkTimeFunc(d *desc.Field, option string, fn any) {
	if fn == nil {
		return
	}
	if f, ok := fn.(func() time.Time); !ok || f == nil {
		failOnce(d, "%s must be a func() time.Time", option)
		return
	}
	if rt, ok := d.RType.(reflect.Type); ok && !reflect.TypeFor[time.Time]().ConvertibleTo(rt) {
		failOnce(d, "%s on a time field needs a GoType convertible from time.Time", option)
	}
}

// checkFunc fails unless fn, when set, is a non-nil func() returning the
// field's Go type. A typed nil, such as (func() int64)(nil), is refused.
func checkFunc(d *desc.Field, option string, fn any) {
	if fn == nil {
		return
	}
	rt, _ := d.RType.(reflect.Type)
	t := reflect.TypeOf(fn)
	if t.Kind() != reflect.Func || reflect.ValueOf(fn).IsNil() || t.NumIn() != 0 || t.NumOut() != 1 || (rt != nil && !t.Out(0).AssignableTo(rt)) {
		failOnce(d, "%s must be a func() %v", option, rt)
	}
}

// contradictions records the options of d that contradict one another: a
// bound given twice, a lower bound above the upper one, and an Immutable
// field with an UpdateDefault, which no update would call. Range gives both
// Min and Max, so a Min beside it is a lower bound given twice.
func contradictions(d *desc.Field) {
	if d.Immutable && d.UpdateDefault != nil {
		failOnce(d, "an Immutable field cannot have an UpdateDefault")
	}
	type bound struct {
		n       int // how many checks give it
		v       any // the last value given
		byRange bool
	}
	bs := map[string]*bound{"Min": {}, "Max": {}, "MinLen": {}, "MaxLen": {}, "MinRuneLen": {}, "MaxRuneLen": {}}
	set := func(kind string, v any, byRange bool) {
		x := bs[kind]
		x.n, x.v, x.byRange = x.n+1, v, byRange
	}
	ranges := 0
	for _, c := range d.Checks {
		switch c.Kind {
		case "Min", "Max", "MinLen", "MaxLen", "MinRuneLen", "MaxRuneLen":
			set(c.Kind, c.Args[0], false)
		case "Range":
			ranges++
			set("Min", c.Args[0], true)
			set("Max", c.Args[1], true)
			if above(c.Args[0], c.Args[1]) {
				failOnce(d, "Range %v..%v is empty", c.Args[0], c.Args[1])
			}
		}
	}
	if ranges > 1 {
		failOnce(d, "Range is given twice")
	}
	for _, k := range []string{"Min", "Max", "MinLen", "MaxLen", "MinRuneLen", "MaxRuneLen"} {
		// The bounds of a repeated Range alone are Range's error.
		if n := bs[k].n; n > 1 && (ranges < 2 || n != ranges || (k != "Min" && k != "Max")) {
			failOnce(d, "%s is given twice", k)
		}
	}
	if bs["MaxLen"].n > 0 && bs["MaxRuneLen"].n > 0 {
		failOnce(d, "MaxLen and MaxRuneLen both set the column's size; give one")
	}
	for _, p := range [][2]string{{"Min", "Max"}, {"MinLen", "MaxLen"}, {"MinRuneLen", "MaxRuneLen"}} {
		lo, hi := bs[p[0]], bs[p[1]]
		// A bound given twice is reported already; one Range is "empty".
		if lo.n == 1 && hi.n == 1 && (!lo.byRange || !hi.byRange) && above(lo.v, hi.v) {
			failOnce(d, "%s %v is above %s %v", p[0], lo.v, p[1], hi.v)
		}
	}
}

// defaultPasses records a literal Default that its own built-in checks refuse:
// every row that left the field unset would fail to insert. A zero Default on
// an Optional field is unset, which the checks let pass. A custom validator is
// not called: it is the application's code, and Descriptor runs at each start.
func defaultPasses(d *desc.Field) {
	if d.Default == nil {
		return
	}
	v := reflect.ValueOf(d.Default)
	if v.Kind() == reflect.Func || (d.Optional && v.IsZero()) {
		return
	}
	for _, c := range d.Checks {
		if why := refusal(c, v); why != "" {
			shown := d.Default
			switch v.Kind() {
			case reflect.Slice:
				shown = fmt.Sprintf("%q", v.Bytes())
			case reflect.String:
				shown = fmt.Sprintf("%q", v.String())
			}
			failOnce(d, "Default %v fails its own check: %s", shown, why)
			return
		}
	}
}

// refusal is what check c says of v, a string, a byte slice or a number: ""
// when it passes. The texts are the generated validators'.
func refusal(c desc.Check, v reflect.Value) string {
	var length int
	if v.Kind() == reflect.String || v.Kind() == reflect.Slice {
		length = v.Len()
	}
	zero := reflect.Zero(v.Type()).Interface()
	x := v.Interface()
	switch c.Kind {
	case "NotEmpty":
		if length == 0 {
			return "must not be empty"
		}
	case "MinLen":
		if n := c.Args[0].(int); length < n {
			return fmt.Sprintf("length is less than %d", n)
		}
	case "MaxLen":
		if n := c.Args[0].(int); length > n {
			return fmt.Sprintf("length is more than %d", n)
		}
	case "MinRuneLen":
		if n := c.Args[0].(int); utf8.RuneCountInString(v.String()) < n {
			return fmt.Sprintf("length in characters is less than %d", n)
		}
	case "MaxRuneLen":
		if n := c.Args[0].(int); utf8.RuneCountInString(v.String()) > n {
			return fmt.Sprintf("length in characters is more than %d", n)
		}
	case "Match":
		if p := c.Args[0].(string); !regexp.MustCompile(p).MatchString(v.String()) {
			return "does not match " + p
		}
	case "Min":
		if above(c.Args[0], x) {
			return fmt.Sprintf("is less than %v", c.Args[0])
		}
	case "Max":
		if above(x, c.Args[0]) {
			return fmt.Sprintf("is more than %v", c.Args[0])
		}
	case "Range":
		if above(c.Args[0], x) {
			return fmt.Sprintf("is less than %v", c.Args[0])
		}
		if above(x, c.Args[1]) {
			return fmt.Sprintf("is more than %v", c.Args[1])
		}
	case "Positive":
		if !above(x, zero) {
			return "is not positive"
		}
	case "Negative":
		if !above(zero, x) {
			return "is not negative"
		}
	case "NonNegative":
		if above(zero, x) {
			return "is negative"
		}
	}
	return ""
}

// above reports whether a > b, two values of one integer or float type.
func above(a, b any) bool {
	x, y := reflect.ValueOf(a), reflect.ValueOf(b)
	switch {
	case x.CanInt():
		return x.Int() > y.Int()
	case x.CanUint():
		return x.Uint() > y.Uint()
	default:
		return x.Float() > y.Float()
	}
}
