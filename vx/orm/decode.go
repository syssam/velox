package orm

import (
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DecodeWhere fills a where input from the object a GraphQL engine hands over
// for it, or from a filter kept as a document (a saved view, a rule of an
// access policy): a map of the input's fields, by their names in the schema.
// dst is a pointer to a generated where input and plan its fields' meaning;
// the generated UnmarshalGQL of every where input calls it.
//
// It is what an engine's generated decoder would do, written once instead of
// once per input (a where input of a wide entity has hundreds of fields):
//
//   - a key the input does not have is an error, never a condition dropped;
//   - null, like an absent key, is no condition;
//   - a single value where a list goes is a list of one;
//   - an integer may arrive as a number or as a string of digits, which is
//     how a 64-bit id travels to a client that keeps numbers as floats;
//   - a value of an enum is given by its name in the schema (DRAFT), or as
//     the value stored ("draft"); anything else is an error.
func DecodeWhere(dst any, plan []WhereField, v any) error {
	return decodeWhere(dst, plan, v, 0)
}

func decodeWhere(dst any, plan []WhereField, v any, depth int) error {
	if v == nil {
		return nil
	}
	if depth > MaxWhereDepth {
		return ErrWhereDepth
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("orm: a where input is given as an object, got %T", v)
	}
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("orm: a where input is decoded into a pointer to it, not a %T", dst)
	}
	rv = rv.Elem()
	if rv.NumField() != whereFixed+len(plan) {
		return fmt.Errorf("orm: %s is not the where input its plan describes", rv.Type())
	}
	fields := whereFieldsOf(rv.Type())
	for key, raw := range doc {
		i, ok := fields[key]
		if !ok {
			return fmt.Errorf("orm: %s has no field %q", rv.Type().Name(), key)
		}
		if raw == nil {
			continue
		}
		f := rv.Field(i)
		switch {
		case i == 1: // not
			if err := decodeNested(f, plan, raw, key, depth+1); err != nil {
				return err
			}
		case i < whereFixed: // or, and
			if err := decodeNestedList(f, plan, raw, key, depth+1); err != nil {
				return err
			}
		default:
			w := &plan[i-whereFixed]
			switch {
			case w.Edge != nil && w.With:
				if w.Neighbor == nil {
					return fmt.Errorf("orm: %s.%s: the where input of the neighbors is not known", rv.Type().Name(), key)
				}
				if err := decodeNestedList(f, w.Neighbor, raw, key, depth+1); err != nil {
					return err
				}
			default:
				if err := decodeField(f, w, raw, key); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// EncodeWhere writes a where input as JSON. An input is never part of a
// response; the generated MarshalGQL that calls this is there because an
// engine takes a type with both MarshalGQL and UnmarshalGQL as one that
// decodes itself, and then generates no decoder for it.
func EncodeWhere(w io.Writer, in any) {
	b, err := json.Marshal(in)
	if err != nil {
		_, _ = io.WriteString(w, "null")
		return
	}
	_, _ = w.Write(b)
}

// whereFields caches, per where input type, the index of each field by its
// name in the schema (the json tag): the structs are large, and every list
// request decodes one.
var whereFields sync.Map // reflect.Type -> map[string]int

func whereFieldsOf(t reflect.Type) map[string]int {
	if cached, ok := whereFields.Load(t); ok {
		if m, ok := cached.(map[string]int); ok {
			return m
		}
	}
	m := make(map[string]int, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" { // Predicates is json:"-"
			m[name] = i
		}
	}
	whereFields.Store(t, m)
	return m
}

// decodeNested fills a *WhereInput field from an object.
func decodeNested(f reflect.Value, plan []WhereField, raw any, path string, depth int) error {
	p := reflect.New(f.Type().Elem())
	if err := decodeWhere(p.Interface(), plan, raw, depth); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	f.Set(p)
	return nil
}

// decodeNestedList fills a []*WhereInput field from a list of objects, or
// from one object.
func decodeNestedList(f reflect.Value, plan []WhereField, raw any, path string, depth int) error {
	items, ok := raw.([]any)
	if !ok {
		items = []any{raw}
	}
	out := reflect.MakeSlice(f.Type(), 0, len(items))
	for i, it := range items {
		if it == nil {
			return fmt.Errorf("orm: %s[%d]: null where an input goes", path, i)
		}
		p := reflect.New(f.Type().Elem().Elem())
		if err := decodeWhere(p.Interface(), plan, it, depth); err != nil {
			return fmt.Errorf("%s[%d]: %w", path, i, err)
		}
		out = reflect.Append(out, p)
	}
	f.Set(out)
	return nil
}

var (
	timeType       = reflect.TypeFor[time.Time]()
	gqlUnmarshaler = reflect.TypeFor[interface{ UnmarshalGQL(any) error }]()
)

// decodeField sets a field of a where input from what a document holds for
// it: a list for In and NotIn, one value otherwise.
func decodeField(f reflect.Value, w *WhereField, raw any, path string) error {
	if w.Op == OpIn || w.Op == OpNotIn {
		items, ok := raw.([]any)
		if !ok {
			items = []any{raw} // a single value where a list goes
		}
		out := reflect.MakeSlice(f.Type(), len(items), len(items))
		for i, it := range items {
			if it == nil {
				return fmt.Errorf("orm: %s[%d]: null in a list of values", path, i)
			}
			if err := decodeValue(out.Index(i), it, fmt.Sprintf("%s[%d]", path, i), w.Enum); err != nil {
				return err
			}
		}
		f.Set(out)
		return nil
	}
	if f.Kind() != reflect.Pointer {
		return decodeValue(f, raw, path, w.Enum) // a flag: isNil, notNil
	}
	p := reflect.New(f.Type().Elem())
	if err := decodeValue(p.Elem(), raw, path, w.Enum); err != nil {
		return err
	}
	f.Set(p)
	return nil
}

// decodeValue sets one value of a field's type from what a document holds
// for it. enum is the field's values by name, for a field of an enum.
func decodeValue(dst reflect.Value, src any, path string, enum map[string]string) error {
	// A type with an UnmarshalGQL of its own says itself what a name means.
	if name, ok := src.(string); ok && enum != nil && dst.Kind() == reflect.String &&
		!reflect.PointerTo(dst.Type()).Implements(gqlUnmarshaler) {
		value, known := enum[name]
		if !known {
			for _, v := range enum {
				if v == name {
					value, known = v, true
					break
				}
			}
		}
		if !known {
			return fmt.Errorf("orm: %s: %q is not a value of %s", path, name, dst.Type())
		}
		dst.SetString(value)
		return nil
	}
	// A value of the field's own type, as a caller in Go gives it.
	if sv := reflect.ValueOf(src); sv.Type() == dst.Type() {
		dst.Set(sv)
		return nil
	}
	if dst.Type() == timeType {
		s, ok := src.(string)
		if !ok {
			return fmt.Errorf("orm: %s: a time is given as an RFC 3339 string, got %T", path, src)
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return fmt.Errorf("orm: %s: %q is not an RFC 3339 time", path, s)
		}
		dst.Set(reflect.ValueOf(t))
		return nil
	}
	// A type that reads itself: a scalar of the application's, a decimal, a
	// UUID.
	if dst.CanAddr() {
		if u, ok := dst.Addr().Interface().(interface{ UnmarshalGQL(any) error }); ok {
			if err := u.UnmarshalGQL(src); err != nil {
				return fmt.Errorf("orm: %s: %w", path, err)
			}
			return nil
		}
		if u, ok := dst.Addr().Interface().(encoding.TextUnmarshaler); ok && dst.Kind() != reflect.String {
			text, ok := scalarText(src)
			if !ok {
				return fmt.Errorf("orm: %s: cannot read a %s from %T", path, dst.Type(), src)
			}
			if err := u.UnmarshalText([]byte(text)); err != nil {
				return fmt.Errorf("orm: %s: %w", path, err)
			}
			return nil
		}
	}
	switch dst.Kind() {
	case reflect.String:
		s, ok := src.(string)
		if !ok {
			return fmt.Errorf("orm: %s: expected a string, got %T", path, src)
		}
		dst.SetString(s)
		return nil
	case reflect.Bool:
		b, ok := src.(bool)
		if !ok {
			return fmt.Errorf("orm: %s: expected a boolean, got %T", path, src)
		}
		dst.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := wholeNumber(src)
		if err != nil {
			return fmt.Errorf("orm: %s: %w", path, err)
		}
		if dst.OverflowInt(n) {
			return fmt.Errorf("orm: %s: %d overflows %s", path, n, dst.Type())
		}
		dst.SetInt(n)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// The upper half of a uint64 does not fit the int64 a whole number
		// is read as: digits are read as what they are.
		if digits, ok := numberText(src); ok {
			if u, err := strconv.ParseUint(digits, 10, 64); err == nil {
				if dst.OverflowUint(u) {
					return fmt.Errorf("orm: %s: %d overflows %s", path, u, dst.Type())
				}
				dst.SetUint(u)
				return nil
			}
		}
		n, err := wholeNumber(src)
		if err != nil {
			return fmt.Errorf("orm: %s: %w", path, err)
		}
		if n < 0 || dst.OverflowUint(uint64(n)) {
			return fmt.Errorf("orm: %s: %d overflows %s", path, n, dst.Type())
		}
		dst.SetUint(uint64(n))
		return nil
	case reflect.Float32, reflect.Float64:
		var f float64
		switch x := src.(type) {
		case float64:
			f = x
		case float32:
			f = float64(x)
		case int:
			f = float64(x)
		case int64:
			f = float64(x)
		case json.Number:
			v, err := x.Float64()
			if err != nil {
				return fmt.Errorf("orm: %s: %q is not a number", path, x.String())
			}
			f = v
		default:
			return fmt.Errorf("orm: %s: expected a number, got %T", path, src)
		}
		dst.SetFloat(f)
		return nil
	}
	// Anything else (a struct of the application's, bytes): as JSON reads it.
	b, err := json.Marshal(src)
	if err == nil {
		err = json.Unmarshal(b, dst.Addr().Interface())
	}
	if err != nil {
		return fmt.Errorf("orm: %s: cannot read a %s from %T: %w", path, dst.Type(), src, err)
	}
	return nil
}

// wholeNumber reads an integer from a number, or from a string of digits.
func wholeNumber(src any) (int64, error) {
	switch x := src.(type) {
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case int32:
		return int64(x), nil
	case float64:
		if x != math.Trunc(x) || x < -(1<<63) || x >= 1<<63 {
			return 0, fmt.Errorf("%v is not a whole number", x)
		}
		return int64(x), nil
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number", x.String())
		}
		return n, nil
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number", x)
		}
		return n, nil
	}
	return 0, fmt.Errorf("expected a whole number, got %T", src)
}

// numberText returns the digits of a number given as text.
func numberText(src any) (string, bool) {
	switch x := src.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case uint64:
		return strconv.FormatUint(x, 10), true
	}
	return "", false
}

// scalarText returns a scalar as the text a type reads itself from.
func scalarText(src any) (string, bool) {
	switch x := src.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	}
	return "", false
}
