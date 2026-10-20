package field

import (
	"fmt"
	"maps"
	"reflect"

	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/internal/desc"
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
// not Nillable, whose zero value is no row's key. A field that is unique only
// when present needs Nillable: every unset row holds the same zero value.
func (c common[B]) Optional() B { c.d.Optional = true; return c.self }

// Nillable makes the column NULL-able and the Go field a pointer, nil for
// NULL, with a Null* handle. A field whose Go type is nil-able already, such as
// a slice or a map, stays as it is. Optional().Nillable() is both: optional on
// create, and NULL as nil.
func (c common[B]) Nillable() B { c.d.Nillable = true; return c.self }

// Immutable refuses the field in updates.
func (c common[B]) Immutable() B { c.d.Immutable = true; return c.self }

// Unique adds a single-column unique index. A field that is unique only when
// present needs Nillable: NULLs do not collide, but the zero values of unset
// Optional fields do.
func (c common[B]) Unique() B { c.d.Unique = true; return c.self }

// Sensitive leaves the field out of String(), JSON, logs and spans.
func (c common[B]) Sensitive() B { c.d.Sensitive = true; return c.self }

// Tenant marks the column that scopes every row to a tenant.
func (c common[B]) Tenant() B { c.d.Tenant = true; return c.self }

// Comment sets the field's documentation.
func (c common[B]) Comment(s string) B { c.d.Comment = s; return c.self }

// StorageKey sets the column name.
func (c common[B]) StorageKey(s string) B { c.d.StorageKey = s; return c.self }

// SchemaType sets the column type per dialect, such as {"postgres": "citext"}.
func (c common[B]) SchemaType(m map[string]string) B {
	c.d.SchemaType = maps.Clone(m)
	return c.self
}

// StructTag sets the struct tag of the generated Go field.
func (c common[B]) StructTag(s string) B { c.d.StructTag = s; return c.self }

// Annotations attaches annotations to the field.
func (c common[B]) Annotations(as ...schema.Annotation) B {
	c.d.Annotations = append(c.d.Annotations, as...)
	return c.self
}

// Descriptor returns the field's descriptor. It checks the options that depend
// on one another, so it may be called more than once.
func (c common[B]) Descriptor() *desc.Field { finish(c.d); return c.d }

// fail records the first misuse of a builder.
func fail(d *desc.Field, format string, args ...any) {
	if d.Err == nil {
		d.Err = fmt.Errorf("field %q: "+format, append([]any{d.Name}, args...)...)
	}
}

// typed holds the options of a field whose values are of the Go type V.
type typed[B, V any] struct{ common[B] }

// Default sets the value a new row gets when the field is not set. It never
// changes the Go type. A field that is not a pointer is unset when it holds its
// zero value, so velox-gen refuses a Default on a bool or number that is not
// Nillable, which could then never store false or 0: make the field Nillable
// or drop the Default.
func (t typed[B, V]) Default(v V) B { t.d.Default = v; return t.self }

// DefaultFunc sets a func() returning the field's Go type, called when a new
// row does not set the field.
func (t typed[B, V]) DefaultFunc(fn any) B { t.d.DefaultFunc = fn; return t.self }

// UpdateDefault sets a func() returning the field's Go type, called on every
// update that does not set the field.
func (t typed[B, V]) UpdateDefault(fn any) B { t.d.UpdateDefault = fn; return t.self }

// Validate adds a function that checks the field's value.
// An Optional field's zero value means unset, and is not validated.
func (t typed[B, V]) Validate(fn func(V) error) B {
	if fn == nil {
		fail(t.d, "Validate is given a nil function")
	} else {
		t.d.Validators = append(t.d.Validators, fn)
	}
	return t.self
}

// GoType makes the field's Go type that of typ, which must have the field's kind.
func (t typed[B, V]) GoType(typ any) B {
	setGoType(t.d, typ, reflect.TypeFor[V]())
	return t.self
}

// setGoType makes the Go type of d that of typ, if typ has the kind of base,
// and reports whether it did.
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
// Descriptor call and records an error only once.
func finish(d *desc.Field) {
	if d.Default != nil && d.DefaultFunc != nil {
		fail(d, "Default and DefaultFunc are both set")
	}
	checkFunc(d, "DefaultFunc", d.DefaultFunc)
	checkFunc(d, "UpdateDefault", d.UpdateDefault)
	switch d.Type {
	case "time", "uuid":
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
		fail(d, "Default %v is not one of the enum values", d.Default)
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
		fail(d, "%s must be a func() %v", option, rt)
	}
}
