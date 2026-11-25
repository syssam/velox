package gqlgraph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// MaxIDs is the most IDs one list field of an input may give: IDs refuses
// more. gqlgenx.RequestLimit's document limits bound a request's lists only
// loosely (Limits.MaxVariableNodes is 100000), and an input's Setters has no
// context to read the Limits from, so the bound is fixed here.
const MaxIDs = 1000

// IDs returns ids, the keys a list field of an input gives (at, as
// "CreatePostInput.tagIDs"), as a relation handle's Add and Remove take
// them: the first of each repeated key, in their order. For generated code.
// More than MaxIDs keys is BAD_USER_INPUT wrapping veloxgql.ErrPageTooLarge.
func IDs[K comparable](at string, ids []K) ([]any, error) {
	if len(ids) > MaxIDs {
		return nil, InputErrorf("%s: %w: %d IDs, and the most is %d", at, veloxgql.ErrPageTooLarge, len(ids), MaxIDs)
	}
	out := make([]any, 0, len(ids))
	seen := make(map[K]struct{}, len(ids))
	for _, k := range ids {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out, nil
}

// Validate returns set, the setters an input of c's type gives, once the
// validators of their columns pass. For generated code: the last step of an
// input's Setters. input is the input's GraphQL type, "CreatePostInput" or
// "UpdatePostInput", whose fields c's CreateInputs or UpdateInputs (else
// Inputs) name by column.
//
// A create input's values are checked as an insert checks a row (Table's
// Validate), an update input's as an update checks its setters (Table's
// ValidateSet). Edge setters are not checked: the write checks their keys.
// A validator's error on a column the input sets is BAD_USER_INPUT
// "<input>.<field>: invalid value: <the validator's text>", wrapping the
// *velox.ValidationError; any other error is returned as it is.
func Validate[E any](input string, c *Type, set []velox.Setter[E]) ([]velox.Setter[E], error) {
	if err := validate(input, c, untyped.Assignments(set)); err != nil {
		return nil, err
	}
	return set, nil
}

// validate is Validate, without the entity's type.
func validate(input string, c *Type, set []velox.Assignment) error {
	t := c.table()
	info := t.Info()
	if info == nil {
		return fmt.Errorf("veloxgql: %s: the Type %s has no Table", input, c.Name)
	}
	create := strings.HasPrefix(input, "Create")
	cols := make([]velox.Assignment, 0, len(set))
	for _, a := range set {
		// A create input's row is built from its Set values alone: a
		// skipped setter (a SetPtr of a nil pointer) sets nothing, and is
		// not stored as a zero and validated. ValidateSet passes it over.
		if a.Column >= 0 && (!create || a.Op == velox.AssignSet) {
			cols = append(cols, a)
		}
	}
	names := c.UpdateInputs
	if create {
		names = c.CreateInputs
	}
	if names == nil {
		names = c.Inputs
	}
	var err error
	if create {
		p := t.New()
		idx := make([]int, len(cols))
		for i, a := range cols {
			if err := t.SetField(p, a.Column, a.Value); err != nil {
				return err
			}
			idx[i] = a.Column
		}
		err = t.Validate(p, idx...)
	} else {
		err = t.ValidateSet(cols...)
	}
	if err == nil {
		return nil
	}
	return writeError(err, input, info.Name, inputNames(t, names))
}

// Show is what a mutation input's String prints for p, a field that is a
// pointer: the value it points to, or nil. For generated code.
func Show[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// ShowJSON is Show of a JSON field, as its text. For generated code.
func ShowJSON(p *veloxgql.JSON) any {
	if p == nil {
		return nil
	}
	return string(*p)
}

// writeError is err, a validator's, from the input named input of entity
// (the name velox reports, as "veloxgen.Post"). A *velox.ValidationError of entity
// on a field name gives a GraphQL name for becomes BAD_USER_INPUT
// "<input>.<name>: invalid value: <the validator's text>", wrapping err.
// Every other error is returned as it is. So is a validation error joined
// with another error (errors.Join): only err itself and the chain of errors
// it wraps one by one are searched, so that gqlgenx.ErrorPresenter still logs an
// internal error joined with the validation error.
func writeError(err error, input, entity string, name func(goField string) string) error {
	var v *velox.ValidationError
	for e := err; e != nil && v == nil; {
		switch x := e.(type) { //nolint:errorlint // searches only the chain of single wraps; a join is left alone, as documented
		case *velox.ValidationError:
			v = x
		case interface{ Unwrap() error }:
			e = x.Unwrap()
		default:
			e = nil
		}
	}
	if v == nil || v.Entity != entity {
		return err
	}
	field := name(v.Field)
	if field == "" {
		return err
	}
	msg := input + "." + field + ": invalid value"
	if v.Err != nil {
		msg += ": " + v.Err.Error()
	}
	return &gqlcore.Error{Code: gqlcore.CodeBadUserInput, Msg: msg, Err: err}
}

// FromJSON decodes j into a T for the input field at; on failure, a
// BAD_USER_INPUT error naming at, which does not echo the decoder's text (it
// names Go types). For generated code.
//
// What it stores is what the client gave, or it refuses. A number in an any is a
// float64, as encoding/json and a hook expect, when that says the same number
// (exactFloat), and a json.Number when it would not, so that it keeps its digits. A key
// that no field of a struct has is refused, as json.Unmarshal would drop it; so is a null
// where the type holds none, a string in a []string as much as a field of a struct, as
// it would store the zero value; and a list that is not the length of a Go array, which
// it would pad or cut. A type with its own UnmarshalJSON decides for itself. Reading a
// row back into an any is velox's scan, which gives a float64.
func FromJSON[T any](at string, j veloxgql.JSON) (T, error) {
	var t, zero T
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		if key, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return zero, InputErrorf("%s: invalid value: the key %s is not in the field's type", at, key)
		}
		return zero, InputErrorf("%s: invalid value: not a JSON value of the field's type", at)
	}
	typ := reflect.TypeFor[T]()
	if bytes.Contains(j, []byte("null")) || typeHolds(typ, reflect.Array, nil) {
		var v any
		dec := json.NewDecoder(bytes.NewReader(j))
		dec.UseNumber()
		// j decoded once into T, so it is a JSON value.
		if err := dec.Decode(&v); err == nil {
			if why := misfit(typ, v); why != "" {
				return zero, InputErrorf("%s: invalid value: %s", at, why)
			}
		}
	}
	if typeHolds(typ, reflect.Interface, nil) {
		floatNumbers(reflect.ValueOf(&t).Elem())
	}
	return t, nil
}

// typeHolds reports whether a value of the type t may hold one of the kind, in a place
// the decoder fills itself.
func typeHolds(t reflect.Type, kind reflect.Kind, seen map[reflect.Type]bool) bool {
	if t.Kind() == kind {
		return !t.Implements(jsonUnmarshaler)
	}
	if t.Implements(jsonUnmarshaler) || reflect.PointerTo(t).Implements(jsonUnmarshaler) || seen[t] {
		return false
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
		if seen == nil {
			seen = map[reflect.Type]bool{}
		}
		seen[t] = true
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return typeHolds(t.Elem(), kind, seen)
	case reflect.Struct:
		for _, f := range reflect.VisibleFields(t) {
			if f.IsExported() && typeHolds(f.Type, kind, seen) {
				return true
			}
		}
	}
	return false
}

// floatNumbers makes each json.Number in an any under v a float64, when exactFloat says
// that keeps it. v is settable.
func floatNumbers(v reflect.Value) {
	if t := v.Type(); t.Kind() != reflect.Interface && (t.Implements(jsonUnmarshaler) || reflect.PointerTo(t).Implements(jsonUnmarshaler)) {
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.NumMethod() == 0 && !v.IsNil() {
			v.Set(reflect.ValueOf(floatsOf(v.Interface())).Convert(v.Type()))
		}
	case reflect.Pointer:
		if !v.IsNil() {
			floatNumbers(v.Elem())
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			floatNumbers(v.Index(i))
		}
	case reflect.Map:
		for it := v.MapRange(); it.Next(); {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(it.Value())
			floatNumbers(e)
			v.SetMapIndex(it.Key(), e)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				floatNumbers(v.Field(i))
			}
		}
	}
}

// floatsOf is x, a value the decoder put in an any, with floatNumbers's change made.
func floatsOf(x any) any {
	switch x := x.(type) {
	case json.Number:
		if f, ok := exactFloat(string(x)); ok {
			return f
		}
	case []any:
		for i := range x {
			x[i] = floatsOf(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = floatsOf(x[k])
		}
	}
	return x
}

// exactFloat is the number s as a float64, and whether that says the same number: its
// shortest decimal, which is what JSON writes of it, has the value s has. So 0.1 is
// kept, and 9007199254740993, 1e400 and 1e-400 are not. It works on the text, whatever
// the exponent, as a big.Rat of 1e999999 takes milliseconds.
func exactFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, decimal(s) == decimal(strconv.FormatFloat(f, 'e', -1, 64))
}

// decimal is the JSON number s as its digits, without zeros at either end, and the
// exponent of the last: two numbers are equal when these are. Its sign is no part of
// it, as ParseFloat keeps that. An exponent out of range is "", which equals no number
// ParseFloat took.
func decimal(s string) string {
	s = strings.TrimPrefix(s, "-")
	exp := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var err error
		if exp, err = strconv.Atoi(s[i+1:]); err != nil {
			return ""
		}
		s = s[:i]
	}
	whole, frac, _ := strings.Cut(s, ".")
	digits := strings.TrimLeft(whole+frac, "0")
	exp -= len(frac)
	trimmed := strings.TrimRight(digits, "0")
	exp += len(digits) - len(trimmed)
	if trimmed == "" {
		return "0"
	}
	return trimmed + "e" + strconv.Itoa(exp)
}

// misfit says what in v, decoded JSON, a value of the type t cannot hold, so that
// decoding it gives another value: a null it has no way to hold, or a list that is not
// the length of its array. It is "" for none.
func misfit(t reflect.Type, v any) string {
	if t.Implements(jsonUnmarshaler) || reflect.PointerTo(t).Implements(jsonUnmarshaler) {
		return ""
	}
	const null = "null where the field's type holds none"
	switch t.Kind() {
	case reflect.Interface:
		return ""
	case reflect.Pointer:
		if v == nil {
			return ""
		}
		return misfit(t.Elem(), v)
	case reflect.Slice, reflect.Array:
		list, isList := v.([]any)
		if t.Kind() == reflect.Array {
			if v == nil {
				return null
			}
			if isList && len(list) != t.Len() {
				return fmt.Sprintf("a list of %d where the field's type holds %d", len(list), t.Len())
			}
		}
		for _, e := range list {
			if why := misfit(t.Elem(), e); why != "" {
				return why
			}
		}
		return ""
	case reflect.Map:
		m, _ := v.(map[string]any)
		for _, e := range m {
			if why := misfit(t.Elem(), e); why != "" {
				return why
			}
		}
		return ""
	case reflect.Struct:
		if v == nil {
			return null
		}
		m, _ := v.(map[string]any)
		for k, e := range m {
			if f, ok := jsonField(t, k); ok {
				if why := misfit(f.Type, e); why != "" {
					return why
				}
			}
		}
		return ""
	}
	if v == nil {
		return null
	}
	return ""
}

// jsonField is the field of the struct t that encoding/json decodes the key k into:
// by its tag or its name, the exact one first, then in any case.
func jsonField(t reflect.Type, k string) (reflect.StructField, bool) {
	var fold reflect.StructField
	found := false
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch name {
		case "-":
			continue
		case "":
			name = f.Name
		}
		if name == k {
			return f, true
		}
		if !found && strings.EqualFold(name, k) {
			fold, found = f, true
		}
	}
	return fold, found
}

var jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
