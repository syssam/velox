package orm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// Fill adds the setters for what in gives: a struct, or a pointer to one,
// whose exported fields are named like the entity's (the request type of an
// endpoint, a GraphQL input, a row of an import). Each field says, by its
// type, whether it was given:
//
//   - a pointer, slice or map that is nil gives nothing;
//   - a type with a method ValueOK() (T, bool), the shape of an optional that
//     tells "absent" from "null", gives nothing when the bool is false, and
//     when it is true and the value is nil it clears the column on an update
//     (where the column can be NULL);
//   - a bool field Clear<Name> that is true clears the column <Name>;
//   - anything else is a value and is written, zero included.
//
// Every field of in must name a field of the entity: a field that silently
// went nowhere would be a write that silently did not happen. A field that is
// not meant for the row says so in the struct, with the tag `orm:"-"`, or is
// listed in skip by a caller who does not own the struct. The tag
// `orm:"column"` gives a field whose name differs the column it is for. A
// value must be of the entity field's type or convert to it without loss.
func (m *MutCore) Fill(in any, update bool, skip []string) {
	meta := m.M
	meta.prepare()
	fail := func(format string, a ...any) {
		m.Sets = append(m.Sets, S{Kind: setInvalid, Val: fmt.Errorf(format, a...)})
	}
	rv := reflect.ValueOf(in)
	for rv.IsValid() && rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		fail("orm: Fill of %s takes a struct or a pointer to one, not %T", meta.Name, in)
		return
	}
	if rv.Type() == meta.typ.Elem() {
		// An entity cannot say which of its fields are given: its Active
		// field is false both when the caller wants false and when the
		// caller wants the schema's default of true. Written from one, a row
		// would get the wrong value without an error.
		fail("orm: Fill of %[1]s takes an input struct, not a %[1]s: an entity's fields cannot say \"not given\"; give the values with Set, or in a struct whose optional fields are pointers", meta.Name)
		return
	}
	plan := meta.fillPlan(rv.Type())
	for _, name := range plan.unknown {
		if !slices.Contains(skip, name) {
			fail("orm: Fill of %s: %s has a field %s that %s does not; name it in skip if it is not meant for the row", meta.Name, rv.Type(), name, meta.Name)
			return
		}
	}
	for _, name := range skip {
		if !plan.names[name] {
			fail("orm: Fill of %s: skip names %s, which %s does not have", meta.Name, name, rv.Type())
			return
		}
	}
	m.Sets = slices.Grow(m.Sets, len(plan.fields))
	for i := range plan.fields {
		p := &plan.fields[i]
		if slices.Contains(skip, p.name) {
			continue
		}
		v := rv.Field(p.index)
		if p.clear {
			if v.Bool() {
				m.Sets = append(m.Sets, S{Kind: SetNull, Col: p.f.Column})
			}
			continue
		}
		explicit := false
		if p.optional >= 0 {
			out := v.Method(p.optional).Call(nil)
			if !out[1].Bool() {
				continue
			}
			v, explicit = out[0], true
		}
		for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
			if v.IsNil() {
				break
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
			if v.IsNil() {
				// Nothing, or an explicit null: that clears a column that can
				// be NULL and leaves any other alone.
				if explicit && update && p.f.Optional && !p.f.NotNull {
					m.Sets = append(m.Sets, S{Kind: SetNull, Col: p.f.Column})
				}
				continue
			}
		}
		val, err := meta.convertTo(p.f, v)
		if err != nil {
			fail("%w", err)
			return
		}
		m.Sets = append(m.Sets, S{Kind: SetValue, Col: p.f.Column, Val: val})
	}
}

// convertTo is convert for a value still held by reflection, which also takes
// the values convert cannot compare (a slice, a map).
func (m *Meta) convertTo(f *FieldMeta, v reflect.Value) (any, error) {
	t := m.goType(m.col(f.Column))
	if v.Type() == t || v.Type().AssignableTo(t) {
		return v.Interface(), nil
	}
	if !v.Comparable() && v.Type().ConvertibleTo(t) && v.Kind() == t.Kind() {
		return v.Convert(t).Interface(), nil
	}
	return m.convert(f, v.Interface())
}

// fillField is one field of an input struct and where it goes.
type fillField struct {
	name     string
	index    int
	f        *FieldMeta
	clear    bool // a Clear<Name> flag
	optional int  // index of the ValueOK method, or -1
}

type fillPlan struct {
	fields  []fillField
	unknown []string        // exported fields that name nothing of the entity
	names   map[string]bool // every exported field
}

var fillPlans sync.Map // fillKey -> *fillPlan

type fillKey struct {
	m *Meta
	t reflect.Type
}

// fillPlan maps the fields of struct type t to the fields of the entity. It
// is computed once per type.
func (m *Meta) fillPlan(t reflect.Type) *fillPlan {
	key := fillKey{m, t}
	if p, ok := fillPlans.Load(key); ok {
		return p.(*fillPlan) //nolint:errcheck // stored below
	}
	p := &fillPlan{names: map[string]bool{}}
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() || sf.Anonymous {
			continue
		}
		p.names[sf.Name] = true
		ff := fillField{name: sf.Name, index: i, optional: -1}
		tag, tagged := sf.Tag.Lookup("orm")
		if tag == "-" {
			continue
		}
		if tagged && tag != "" {
			// The column the field is for, by name.
			if ff.f = m.byColumn[tag]; ff.f == nil {
				p.unknown = append(p.unknown, sf.Name)
				continue
			}
		} else if rest, ok := strings.CutPrefix(sf.Name, "Clear"); ok && sf.Type.Kind() == reflect.Bool && m.byGoName[rest] != nil && m.byGoName[sf.Name] == nil {
			ff.f, ff.clear = m.byGoName[rest], true
			p.fields = append(p.fields, ff)
			continue
		} else if ff.f = m.byGoName[sf.Name]; ff.f == nil {
			p.unknown = append(p.unknown, sf.Name)
			continue
		}
		if mth, ok := sf.Type.MethodByName("ValueOK"); ok && mth.Type.NumIn() == 1 && mth.Type.NumOut() == 2 && mth.Type.Out(1).Kind() == reflect.Bool {
			ff.optional = mth.Index
		}
		p.fields = append(p.fields, ff)
	}
	fillPlans.Store(key, p)
	return p
}

// Opt is a value that may be absent, present and null, or present: the type
// of a field of an input struct that must tell the three apart, as the body
// of a PATCH request must. Its zero value is "absent". Decoded from JSON, a
// key that is missing leaves it absent, null makes it a present null, and
// anything else a present value; tag the field `json:",omitzero"` to encode
// an absent one as a missing key.
type Opt[T any] struct {
	v    *T
	some bool
}

// Some is a present value.
func Some[T any](v T) Opt[T] { return Opt[T]{v: &v, some: true} }

// Null is a present null: on an update it clears a column that can be NULL.
func Null[T any]() Opt[T] { return Opt[T]{some: true} }

// ValueOK returns the value (nil for null) and whether the Opt is present.
// It is the method Fill recognizes an optional by.
func (o Opt[T]) ValueOK() (*T, bool) { return o.v, o.some }

// IsZero reports whether the Opt is absent.
func (o Opt[T]) IsZero() bool { return !o.some }

// UnmarshalJSON makes the Opt present: null, or the decoded value.
func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.some, o.v = true, nil
	if string(bytes.TrimSpace(b)) == "null" {
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.v = &v
	return nil
}

// MarshalJSON encodes the value, and null for an Opt that has none.
func (o Opt[T]) MarshalJSON() ([]byte, error) {
	if o.v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*o.v)
}
