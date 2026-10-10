package orm

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DecodeWhere reads a where input from a document: the object a GraphQL
// engine hands over for the where argument, or a filter that was kept (a
// saved view, a rule of an access policy). v is the object as JSON decodes
// it, a map of the input's fields by their names in the schema, or the JSON
// text itself ([]byte or json.RawMessage). dst is a pointer to a generated
// where input and plan its fields' meaning; the generated UnmarshalGQL and
// UnmarshalJSON of every where input call it.
//
// A filter narrows what a caller sees, so what cannot be read is refused and
// never taken for no condition:
//
//   - a key the input does not have is an error;
//   - null, like an absent key, is no condition;
//   - a single value where a list goes is a list of one;
//   - an integer may arrive as a number or as a string of digits, which is
//     how a 64-bit id travels to a client that keeps numbers as floats. A
//     number that was already a float and is too large to be exact
//     (beyond 2^53) is an error: given as text, a document's numbers are
//     read whole;
//   - a value of an enum is given by its name in the schema (DRAFT), or as
//     the value stored ("draft"); the name is tried first.
//
// An empty list for and, or, or a has...With field is no condition, as an
// absent one is: this is what velox's and Ent's inputs do with it, and what
// a client that builds the list from a form sends when the form is empty. A
// rule that must match nothing when it has no alternatives has to say so.
//
// dst becomes what the document says, and is left as it was when the
// document is refused.
func DecodeWhere(dst any, plan []WhereField, v any) error {
	switch text := v.(type) {
	case json.RawMessage:
		doc, err := whereDocument(text)
		if err != nil {
			return err
		}
		v = doc
	case []byte:
		doc, err := whereDocument(text)
		if err != nil {
			return err
		}
		v = doc
	}
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("velox: a where input is decoded into a pointer to it, not a %T", dst)
	}
	read := reflect.New(rv.Type().Elem())
	if v != nil {
		if err := decodeWhere(read.Elem(), plan, v, 0); err != nil {
			return err
		}
	}
	rv.Elem().Set(read.Elem())
	return nil
}

// whereDocument parses the JSON text of a where input, keeping its numbers
// as they are written.
func whereDocument(text []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("velox: a where input is not JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("velox: a where input is one JSON value, and something follows it")
	}
	return v, nil
}

// whereErrorKey is the key of the document WriteWhere writes for an input
// that cannot be written. No field of a schema can have this name, so the
// document is refused when it is read.
const whereErrorKey = "!error"

func decodeWhere(rv reflect.Value, plan []WhereField, v any, depth int) error {
	if depth > MaxWhereDepth {
		return ErrWhereDepth
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("velox: a where input is given as an object, got %T", v)
	}
	if rv.NumField() != whereFixed+len(plan) {
		return misfit(rv.Type())
	}
	if msg, failed := doc[whereErrorKey]; failed {
		return fmt.Errorf("velox: the document is a where input that could not be written: %v", msg)
	}
	shape := whereShapeOf(rv.Type())
	for key, raw := range doc {
		i, ok := shape.index[key]
		if !ok {
			return fmt.Errorf("velox: %s has no field %q", rv.Type().Name(), key)
		}
		if raw == nil {
			continue
		}
		f := rv.Field(i)
		if !f.CanSet() {
			return misfit(rv.Type())
		}
		switch {
		case i == 1: // not
			if !isInput(f.Type()) {
				return misfit(rv.Type())
			}
			p := reflect.New(f.Type().Elem())
			if err := decodeWhere(p.Elem(), plan, raw, depth+1); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			f.Set(p)
		case i < whereFixed: // or, and
			if err := decodeNestedList(f, plan, raw, key, depth+1); err != nil {
				return err
			}
		default:
			w := &plan[i-whereFixed]
			switch {
			case w.Edge != nil && w.With:
				if w.Neighbor == nil {
					return fmt.Errorf("velox: %s.%s: the where input of the neighbors is not known", rv.Type().Name(), key)
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

// misfit is the error of a struct that is not laid out as its plan says: a
// where input written by hand, or handed the plan of another.
func misfit(t reflect.Type) error {
	return fmt.Errorf("velox: %s is not the where input its plan describes", t)
}

// isInput reports whether t is a pointer to a struct, which a nested where
// input is.
func isInput(t reflect.Type) bool {
	return t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct
}

// whereShape is the fields of a where input type by their names in the
// schema (the json tags), both ways.
type whereShape struct {
	index map[string]int
	names []string
}

// whereShapes caches the shape per where input type: the structs are large,
// and every list request decodes one.
var whereShapes sync.Map // reflect.Type -> *whereShape

func whereShapeOf(t reflect.Type) *whereShape {
	if cached, ok := whereShapes.Load(t); ok {
		if s, ok := cached.(*whereShape); ok {
			return s
		}
	}
	s := &whereShape{index: make(map[string]int, t.NumField()), names: make([]string, t.NumField())}
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" { // Predicates is json:"-"
			s.index[name], s.names[i] = i, name
		}
	}
	whereShapes.Store(t, s)
	return s
}

// decodeNestedList fills a []*WhereInput field from a list of objects, or
// from one object.
func decodeNestedList(f reflect.Value, plan []WhereField, raw any, path string, depth int) error {
	if f.Kind() != reflect.Slice || !isInput(f.Type().Elem()) {
		return fmt.Errorf("velox: %s: a %s is not a list of where inputs", path, f.Type())
	}
	items, ok := raw.([]any)
	if !ok {
		items = []any{raw}
	}
	out := reflect.MakeSlice(f.Type(), 0, len(items))
	for i, it := range items {
		if it == nil {
			return fmt.Errorf("velox: %s[%d]: null where an input goes", path, i)
		}
		p := reflect.New(f.Type().Elem().Elem())
		if err := decodeWhere(p.Elem(), plan, it, depth); err != nil {
			return fmt.Errorf("%s[%d]: %w", path, i, err)
		}
		out = reflect.Append(out, p)
	}
	f.Set(out)
	return nil
}

var (
	timeType        = reflect.TypeFor[time.Time]()
	gqlUnmarshaler  = reflect.TypeFor[interface{ UnmarshalGQL(any) error }]()
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// decodeField sets a field of a where input from what a document holds for
// it. What a field is follows from its kind, as it does for Where: a slice
// is a list of values, a pointer one value, a bool a flag.
func decodeField(f reflect.Value, w *WhereField, raw any, path string) error {
	switch f.Kind() {
	case reflect.Slice:
		items, ok := raw.([]any)
		if !ok {
			items = []any{raw} // a single value where a list goes
		}
		out := reflect.MakeSlice(f.Type(), len(items), len(items))
		for i, it := range items {
			if it == nil {
				return fmt.Errorf("velox: %s[%d]: null in a list of values", path, i)
			}
			if err := decodeValue(out.Index(i), it, fmt.Sprintf("%s[%d]", path, i), w.Enum); err != nil {
				return err
			}
		}
		f.Set(out)
		return nil
	case reflect.Pointer:
		p := reflect.New(f.Type().Elem())
		if err := decodeValue(p.Elem(), raw, path, w.Enum); err != nil {
			return err
		}
		f.Set(p)
		return nil
	case reflect.Bool:
		return decodeValue(f, raw, path, nil) // a flag: isNil, notNil
	}
	return fmt.Errorf("velox: %s: a %s is neither a pointer, a slice nor a bool", path, f.Type())
}

// maxExact is the largest integer a float64 holds without having rounded it:
// what a reader that keeps numbers as floats can be trusted with.
const maxExact = 1<<53 - 1

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
			return fmt.Errorf("velox: %s: %q is not a value of %s", path, name, dst.Type())
		}
		dst.SetString(value)
		return nil
	}
	float := dst.Kind() == reflect.Float32 || dst.Kind() == reflect.Float64
	// A value of the field's own type, as a caller in Go gives it. A float
	// is looked at below whatever its type: not every float is a number.
	if sv := reflect.ValueOf(src); sv.Type() == dst.Type() && !float {
		dst.Set(sv)
		return nil
	}
	if dst.Type() == timeType {
		s, ok := src.(string)
		if !ok {
			return fmt.Errorf("velox: %s: a time is given as an RFC 3339 string, got %T", path, src)
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return fmt.Errorf("velox: %s: %q is not an RFC 3339 time", path, s)
		}
		dst.Set(reflect.ValueOf(t))
		return nil
	}
	// A type that reads itself: a scalar of the application's, a decimal, a
	// UUID.
	if dst.CanAddr() {
		if u, ok := dst.Addr().Interface().(interface{ UnmarshalGQL(any) error }); ok {
			if err := u.UnmarshalGQL(src); err != nil {
				return fmt.Errorf("velox: %s: %w", path, err)
			}
			return nil
		}
		if u, ok := dst.Addr().Interface().(encoding.TextUnmarshaler); ok {
			text, ok := scalarText(src)
			if !ok {
				return fmt.Errorf("velox: %s: cannot read a %s from %T", path, dst.Type(), src)
			}
			if err := u.UnmarshalText([]byte(text)); err != nil {
				return fmt.Errorf("velox: %s: %w", path, err)
			}
			return nil
		}
	}
	switch dst.Kind() {
	case reflect.String:
		s, ok := src.(string)
		if !ok {
			return fmt.Errorf("velox: %s: expected a string, got %T", path, src)
		}
		dst.SetString(s)
		return nil
	case reflect.Bool:
		b, ok := src.(bool)
		if !ok {
			return fmt.Errorf("velox: %s: expected a boolean, got %T", path, src)
		}
		dst.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := wholeNumber(src)
		if err != nil {
			return fmt.Errorf("velox: %s: %w", path, err)
		}
		if dst.OverflowInt(n) {
			return fmt.Errorf("velox: %s: %d overflows %s", path, n, dst.Type())
		}
		dst.SetInt(n)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// The upper half of a uint64 does not fit the int64 a whole number
		// is read as: digits are read as what they are.
		if digits, ok := numberText(src); ok {
			if u, err := strconv.ParseUint(digits, 10, 64); err == nil {
				if dst.OverflowUint(u) {
					return fmt.Errorf("velox: %s: %d overflows %s", path, u, dst.Type())
				}
				dst.SetUint(u)
				return nil
			}
		}
		n, err := wholeNumber(src)
		if err != nil {
			return fmt.Errorf("velox: %s: %w", path, err)
		}
		if n < 0 || dst.OverflowUint(uint64(n)) {
			return fmt.Errorf("velox: %s: %d overflows %s", path, n, dst.Type())
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
				return fmt.Errorf("velox: %s: %q is not a number", path, x.String())
			}
			f = v
		default:
			if sv := reflect.ValueOf(src); sv.Type() == dst.Type() {
				f = sv.Float()
				break
			}
			return fmt.Errorf("velox: %s: expected a number, got %T", path, src)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) || dst.OverflowFloat(f) {
			return fmt.Errorf("velox: %s: %v is not a number a %s holds", path, f, dst.Type())
		}
		dst.SetFloat(f)
		return nil
	}
	// Anything else (a struct of the application's, bytes): as JSON reads it,
	// and no more leniently than the rest is read.
	b, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("velox: %s: cannot read a %s from %T: %w", path, dst.Type(), src, err)
	}
	if string(b) == "null" {
		return fmt.Errorf("velox: %s: null where a %s goes", path, dst.Type())
	}
	if list, ok := src.([]any); ok && dst.Kind() == reflect.Array && len(list) != dst.Len() {
		return fmt.Errorf("velox: %s: %d values for a %s", path, len(list), dst.Type())
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst.Addr().Interface()); err != nil {
		return fmt.Errorf("velox: %s: cannot read a %s from %T: %w", path, dst.Type(), src, err)
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
		if x != math.Trunc(x) {
			return 0, fmt.Errorf("%v is not a whole number", x)
		}
		// Beyond this a float has been rounded already, and the integer
		// it was is not known any more.
		if math.Abs(x) > maxExact {
			return 0, fmt.Errorf("%v is too large to be exact as a number: give it as a string of digits", x)
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

// EncodeWhere writes a where input as the document DecodeWhere reads: what
// the two make of an input is the same input, with the same conditions. in is
// a generated where input or a pointer to one and plan its fields' meaning;
// the generated MarshalJSON of every where input calls it, so that
// json.Marshal of an input is this document and not what the struct's tags
// alone would give (which leaves out an empty list of values, and with it
// the condition that nothing matches).
//
// A value of an enum is written by its name in the schema, a time as RFC
// 3339, an integer beyond 2^53 as a string of digits, so that a reader that
// keeps numbers as floats gets it whole. An input that cannot be written is
// an error: one that holds Predicates, which are conditions added in Go and
// have no form in a document, or a value with no JSON form (a float that is
// not a number, a time outside the years 0 to 9999).
func EncodeWhere(in any, plan []WhereField) ([]byte, error) {
	rv := reflect.ValueOf(in)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return []byte("null"), nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("velox: a %T is not a where input", in)
	}
	if !rv.CanAddr() { // so that a value's own methods on its pointer are found
		c := reflect.New(rv.Type()).Elem()
		c.Set(rv)
		rv = c
	}
	var b bytes.Buffer
	if err := encodeWhere(&b, rv, plan, 0); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// WriteWhere writes the document of a where input to w. It is what the
// generated MarshalGQL of a where input calls, which has no error to return:
// for an input that cannot be written it writes a document that says why,
// and that DecodeWhere refuses, so that what is read back is never a filter
// with fewer conditions than the one written.
func WriteWhere(w io.Writer, in any, plan []WhereField) {
	b, err := EncodeWhere(in, plan)
	if err != nil {
		b, _ = json.Marshal(map[string]string{whereErrorKey: err.Error()})
	}
	_, _ = w.Write(b)
}

func encodeWhere(b *bytes.Buffer, rv reflect.Value, plan []WhereField, depth int) error {
	if depth > MaxWhereDepth {
		return ErrWhereDepth
	}
	t := rv.Type()
	if rv.NumField() != whereFixed+len(plan) || rv.Field(0).Kind() != reflect.Slice || !isInput(rv.Field(1).Type()) {
		return misfit(t)
	}
	if rv.Field(0).Len() > 0 {
		return fmt.Errorf("velox: %s holds conditions added in Go (Predicates), which a document cannot say", t.Name())
	}
	names := whereShapeOf(t).names
	first := true
	key := func(i int) error {
		if names[i] == "" {
			return misfit(t)
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		writeString(b, names[i])
		b.WriteByte(':')
		return nil
	}
	b.WriteByte('{')
	if not := rv.Field(1); !not.IsNil() {
		if err := key(1); err != nil {
			return err
		}
		if err := encodeWhere(b, not.Elem(), plan, depth+1); err != nil {
			return fmt.Errorf("%s: %w", names[1], err)
		}
	}
	list := func(i int, f reflect.Value, plan []WhereField) error {
		if f.Kind() != reflect.Slice || !isInput(f.Type().Elem()) {
			return misfit(t)
		}
		if f.Len() == 0 { // no condition, as an absent list is
			return nil
		}
		if err := key(i); err != nil {
			return err
		}
		b.WriteByte('[')
		for j := range f.Len() {
			if j > 0 {
				b.WriteByte(',')
			}
			if f.Index(j).IsNil() {
				return fmt.Errorf("velox: %s[%d]: nil where an input goes", names[i], j)
			}
			if err := encodeWhere(b, f.Index(j).Elem(), plan, depth+1); err != nil {
				return fmt.Errorf("%s[%d]: %w", names[i], j, err)
			}
		}
		b.WriteByte(']')
		return nil
	}
	for i := 2; i < whereFixed; i++ { // or, and
		if err := list(i, rv.Field(i), plan); err != nil {
			return err
		}
	}
	for j := range plan {
		i, w := whereFixed+j, &plan[j]
		f := rv.Field(i)
		switch {
		case w.Edge != nil && w.With:
			if f.Kind() == reflect.Slice && f.Len() > 0 && w.Neighbor == nil {
				return fmt.Errorf("velox: %s.%s: the where input of the neighbors is not known", t.Name(), names[i])
			}
			if err := list(i, f, w.Neighbor); err != nil {
				return err
			}
		case f.Kind() == reflect.Bool:
			if f.Bool() {
				if err := key(i); err != nil {
					return err
				}
				b.WriteString("true")
			}
		case f.Kind() == reflect.Slice:
			if f.IsNil() {
				continue
			}
			if err := key(i); err != nil {
				return err
			}
			// An empty list is written: of values to be among, it is the
			// condition that nothing matches.
			b.WriteByte('[')
			for k := range f.Len() {
				if k > 0 {
					b.WriteByte(',')
				}
				if err := encodeValue(b, f.Index(k), w.Enum); err != nil {
					return fmt.Errorf("velox: %s[%d]: %w", names[i], k, err)
				}
			}
			b.WriteByte(']')
		case f.Kind() == reflect.Pointer:
			if f.IsNil() {
				continue
			}
			if err := key(i); err != nil {
				return err
			}
			if err := encodeValue(b, f.Elem(), w.Enum); err != nil {
				return fmt.Errorf("velox: %s: %w", names[i], err)
			}
		default:
			return misfit(t)
		}
	}
	b.WriteByte('}')
	return nil
}

// encodeValue writes one value of a field the way decodeValue reads it, each
// branch the counterpart of one of its.
func encodeValue(b *bytes.Buffer, v reflect.Value, enum map[string]string) error {
	t := v.Type()
	own := reflect.PointerTo(t)
	if enum != nil && v.Kind() == reflect.String && !own.Implements(gqlUnmarshaler) {
		var names []string
		for name, value := range enum {
			if value == v.String() {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return fmt.Errorf("%q is not a value of %s", v.String(), t)
		}
		writeString(b, slices.Min(names))
		return nil
	}
	if t == timeType {
		at, _ := v.Interface().(time.Time)
		if y := at.Year(); y < 0 || y > 9999 {
			return fmt.Errorf("a time in the year %d has no RFC 3339 form", y)
		}
		writeString(b, at.Format(time.RFC3339Nano))
		return nil
	}
	self := v.Interface()
	if v.CanAddr() {
		self = v.Addr().Interface()
	}
	if own.Implements(gqlUnmarshaler) {
		m, ok := self.(interface{ MarshalGQL(w io.Writer) })
		if !ok {
			return fmt.Errorf("a %s reads itself (UnmarshalGQL) and has no MarshalGQL to write itself with", t)
		}
		var out bytes.Buffer
		m.MarshalGQL(&out)
		if text := bytes.TrimSpace(out.Bytes()); !json.Valid(text) || string(text) == "null" {
			return fmt.Errorf("a %s wrote %q for itself, which is not a value", t, out.String())
		}
		b.Write(out.Bytes())
		return nil
	}
	if own.Implements(textUnmarshaler) {
		m, ok := self.(encoding.TextMarshaler)
		if !ok {
			return fmt.Errorf("a %s reads itself (UnmarshalText) and has no MarshalText to write itself with", t)
		}
		text, err := m.MarshalText()
		if err != nil {
			return err
		}
		writeString(b, string(text))
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		writeString(b, v.String())
		return nil
	case reflect.Bool:
		b.WriteString(strconv.FormatBool(v.Bool()))
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		digits := strconv.FormatInt(v.Int(), 10)
		if n := v.Int(); n > maxExact || n < -maxExact {
			digits = strconv.Quote(digits)
		}
		b.WriteString(digits)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		digits := strconv.FormatUint(v.Uint(), 10)
		if v.Uint() > maxExact {
			digits = strconv.Quote(digits)
		}
		b.WriteString(digits)
		return nil
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("%v is not a number", f)
		}
		b.WriteString(strconv.FormatFloat(f, 'g', -1, t.Bits()))
		return nil
	}
	out, err := json.Marshal(v.Interface())
	if err != nil {
		return err
	}
	if string(out) == "null" {
		return fmt.Errorf("a %s that JSON writes as null", t)
	}
	b.Write(out)
	return nil
}

// writeString writes s as a JSON string.
func writeString(b *bytes.Buffer, s string) {
	out, _ := json.Marshal(s) // a string always has a JSON form
	b.Write(out)
}
