package veloxgql

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// DecodeWhere sets dst, a pointer to a where input velox-graphql generated
// (*PostWhereInput), from v, a document of it: the object a JSON decoder
// gives for the input (map[string]any), or the JSON text itself ([]byte or
// json.RawMessage). It reads a filter that was kept, such as a rule of an
// access policy, as veloxe's generated UnmarshalGQL of a where input did.
//
// The document's keys are the input's GraphQL field names, as the SDL
// spells them ("authorID", "titleNEQ", "not", "hasTagsWith"). A value is
// read into its field's Go type:
//
//   - an integer from a number or a string of digits, as an ID travels; a
//     float beyond 2^53 is an error, since it has been rounded;
//   - a type with an UnmarshalGQL method by it (an enum by its GraphQL name,
//     an application's scalar), a time.Time from an RFC 3339 string, a
//     []byte from base64, and a type with an UnmarshalText method (a UUID,
//     a decimal) from a string or a number;
//   - a list from a list, or from a single value, which is a list of one,
//     as GraphQL coerces a list input;
//   - not, and, or and has…With from objects, read the same way.
//
// null is a field not given. A filter narrows what a caller sees, so what
// cannot be read is an error, BAD_USER_INPUT, and never an input that sets
// less: a key the input does not have, or a value of the wrong type. The
// error names the field through the nesting: "veloxgql: not.idIn[0]:
// expected a whole number, got bool". On an error dst is left as it was.
//
// Nothing here applies Limits: the input's Pred does, as for a where
// argument. DecodeWhere learns an input type's names once, from the input's
// Predicate, and keeps them.
func DecodeWhere(v any, dst any) error {
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
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("veloxgql: DecodeWhere decodes into a pointer to a where input, not a %T", dst)
	}
	if _, err := whereShapeOf(rv.Type().Elem()); err != nil {
		return err
	}
	read := reflect.New(rv.Type().Elem())
	if v != nil {
		if err := decodeWhere(read.Elem(), v, ""); err != nil {
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
		return nil, gqlcore.InputErrorf("a where input is not JSON: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, gqlcore.InputErrorf("a where input is one JSON value, and something follows it")
	}
	return v, nil
}

// predicater is a generated where input, through its Predicate.
type predicater interface {
	Predicate(ctx context.Context) (velox.Predicate, error)
}

// whereShape is the fields of a where input type by their GraphQL names.
type whereShape struct {
	name  string // the input's GraphQL name, "PostWhereInput"
	index map[string]int
}

// whereShapes caches the shape of each where input type, a reflect.Type of
// its struct.
var whereShapes sync.Map // reflect.Type -> *whereShape

// whereShapeOf is the shape of t, a where input's struct type. It asks the
// input's Predicate, called on a nil input, for the names: those of the
// gqlgraph.Type velox-graphql wrote for it.
func whereShapeOf(t reflect.Type) (*whereShape, error) {
	if s, ok := whereShapes.Load(t); ok {
		return s.(*whereShape), nil
	}
	p, ok := reflect.TypeAssert[predicater](reflect.Zero(reflect.PointerTo(t)))
	if !ok || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("veloxgql: %s is not a where input velox-graphql generated", t)
	}
	ctx, slot := gqlcore.AskWhereNames(context.Background())
	_, _ = p.Predicate(ctx) // a nil input sets nothing, and its error is none
	names := slot.Fields
	if slot.Input == "" || len(names) != t.NumField() {
		return nil, fmt.Errorf("veloxgql: %s is not a where input velox-graphql generated", t)
	}
	s := &whereShape{name: slot.Input, index: make(map[string]int, len(names))}
	for i, n := range names {
		s.index[n] = i
	}
	cached, _ := whereShapes.LoadOrStore(t, s)
	return cached.(*whereShape), nil
}

// at is the path of key in the input at path.
func at(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// decodeWhere sets rv, a where input's struct, from v, the document at path.
func decodeWhere(rv reflect.Value, v any, path string) error {
	doc, ok := v.(map[string]any)
	if !ok {
		return within(path, gqlcore.InputErrorf("a where input is an object, got %s", jsonType(v)))
	}
	shape, err := whereShapeOf(rv.Type())
	if err != nil {
		return err
	}
	// In order of the keys, so that of two faults the same one is told.
	for _, key := range slices.Sorted(maps.Keys(doc)) {
		i, ok := shape.index[key]
		if !ok {
			return gqlcore.InputErrorf("%s: %s has no field %q", at(path, key), shape.name, key)
		}
		if doc[key] == nil {
			continue
		}
		if err := decodeField(rv.Field(i), doc[key], at(path, key)); err != nil {
			return err
		}
	}
	return nil
}

// within is err of the input field at path; err itself at the top.
func within(path string, err error) error {
	if path == "" {
		return err
	}
	return gqlcore.Within(path, err)
}

// decodeField sets f, a field of a where input, from raw, a value that is
// not null: a pointer field from a value, a slice from a list.
func decodeField(f reflect.Value, raw any, path string) error {
	if f.Kind() != reflect.Slice || f.Type() == reflect.TypeFor[[]byte]() {
		return decodeElem(f, raw, path)
	}
	items, ok := raw.([]any)
	if !ok {
		items = []any{raw}
	}
	out := reflect.MakeSlice(f.Type(), len(items), len(items))
	for i, it := range items {
		p := path + "[" + strconv.Itoa(i) + "]"
		if it == nil {
			return gqlcore.InputErrorf("%s: null in a list", p)
		}
		if err := decodeElem(out.Index(i), it, p); err != nil {
			return err
		}
	}
	f.Set(out)
	return nil
}

// decodeElem sets dst from raw: a nested where input, a pointer to a value,
// or a value.
func decodeElem(dst reflect.Value, raw any, path string) error {
	if dst.Kind() != reflect.Pointer {
		return decodeValue(dst, raw, path)
	}
	p := reflect.New(dst.Type().Elem())
	var err error
	if _, ok := reflect.TypeAssert[predicater](p); ok {
		err = decodeWhere(p.Elem(), raw, path)
	} else {
		err = decodeValue(p.Elem(), raw, path)
	}
	if err != nil {
		return err
	}
	dst.Set(p)
	return nil
}

// maxExact is the largest integer a float64 holds without having rounded it.
const maxExact = 1<<53 - 1

// decodeValue sets dst, one value of a field's Go type, from src.
func decodeValue(dst reflect.Value, src any, path string) error {
	if reflect.TypeOf(src) == dst.Type() && !isFloat(dst.Kind()) {
		// A value of the field's own type, as a caller in Go gives it.
		dst.Set(reflect.ValueOf(src))
		return nil
	}
	switch u := dst.Addr().Interface().(type) {
	case interface{ UnmarshalGQL(v any) error }:
		return scalarErr(path, dst.Type(), src, u.UnmarshalGQL(src))
	case *time.Time:
		var t Time
		if err := t.UnmarshalGQL(src); err != nil {
			return gqlcore.Within(path, err)
		}
		*u = time.Time(t)
		return nil
	case *[]byte:
		var b Bytes
		if err := b.UnmarshalGQL(src); err != nil {
			return gqlcore.Within(path, err)
		}
		*u = b
		return nil
	case encoding.TextUnmarshaler:
		text, ok := scalarText(src)
		if !ok {
			return gqlcore.InputErrorf("%s: expected a %s as a string, got %s", path, dst.Type(), jsonType(src))
		}
		return scalarErr(path, dst.Type(), src, u.UnmarshalText([]byte(text)))
	}
	if err := decodeKind(dst, src); err != nil {
		return gqlcore.Within(path, err)
	}
	return nil
}

// scalarErr is err, the error of reading a dst from src, at path: a coded
// error as it is, any other as BAD_USER_INPUT.
func scalarErr(path string, dst reflect.Type, src any, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(gqlcore.Coding); ok { //nolint:errorlint // only err itself being coded keeps its text
		return gqlcore.Within(path, err)
	}
	return gqlcore.InputErrorf("%s: %v is not a %s", path, src, dst)
}

// decodeKind sets dst, a value of a basic kind, from src.
func decodeKind(dst reflect.Value, src any) error {
	switch dst.Kind() {
	case reflect.String:
		s, ok := src.(string)
		if !ok {
			return gqlcore.InputErrorf("expected a string, got %s", jsonType(src))
		}
		dst.SetString(s)
	case reflect.Bool:
		b, ok := src.(bool)
		if !ok {
			return gqlcore.InputErrorf("expected a boolean, got %s", jsonType(src))
		}
		dst.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := wholeNumber(src)
		if err != nil {
			return err
		}
		if dst.OverflowInt(n) {
			return gqlcore.InputErrorf("%d overflows %s", n, dst.Type())
		}
		dst.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := wholeUnsigned(src)
		if err != nil {
			return err
		}
		if dst.OverflowUint(u) {
			return gqlcore.InputErrorf("%d overflows %s", u, dst.Type())
		}
		dst.SetUint(u)
	case reflect.Float32, reflect.Float64:
		f, err := number(src)
		if err != nil {
			return err
		}
		if math.IsNaN(f) || math.IsInf(f, 0) || dst.OverflowFloat(f) {
			return gqlcore.InputErrorf("%v is not a number a %s holds", f, dst.Type())
		}
		dst.SetFloat(f)
	default:
		return gqlcore.InputErrorf("cannot read a %s from %s", dst.Type(), jsonType(src))
	}
	return nil
}

// isFloat reports whether k is a float's kind.
func isFloat(k reflect.Kind) bool { return k == reflect.Float32 || k == reflect.Float64 }

// wholeNumber reads an integer from a number, or from a string of digits.
func wholeNumber(src any) (int64, error) {
	switch x := src.(type) {
	case float64:
		if x != math.Trunc(x) || math.IsInf(x, 0) {
			return 0, gqlcore.InputErrorf("%s is not a whole number", strconv.FormatFloat(x, 'f', -1, 64))
		}
		if math.Abs(x) > maxExact {
			return 0, gqlcore.InputErrorf("%s is too large to be exact as a number: give it as a string of digits", strconv.FormatFloat(x, 'f', -1, 64))
		}
		return int64(x), nil
	case json.Number:
		n, err := strconv.ParseInt(x.String(), 10, 64)
		if err != nil {
			return 0, gqlcore.InputErrorf("%s is not a whole number", x)
		}
		return n, nil
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return 0, gqlcore.InputErrorf("%q is not a whole number", x)
		}
		return n, nil
	}
	if v := reflect.ValueOf(src); v.CanInt() {
		return v.Int(), nil
	}
	return 0, gqlcore.InputErrorf("expected a whole number, got %s", jsonType(src))
}

// wholeUnsigned reads an unsigned integer as wholeNumber reads an integer;
// digits are read whole, the upper half of a uint64 too.
func wholeUnsigned(src any) (uint64, error) {
	if v := reflect.ValueOf(src); v.CanUint() {
		return v.Uint(), nil
	}
	var digits string
	switch x := src.(type) {
	case string:
		digits = x
	case json.Number:
		digits = x.String()
	default:
		n, err := wholeNumber(src)
		if err != nil {
			return 0, err
		}
		if n >= 0 {
			return uint64(n), nil
		}
		digits = strconv.FormatInt(n, 10)
	}
	u, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		if _, ok := src.(string); ok {
			digits = strconv.Quote(digits)
		}
		return 0, gqlcore.InputErrorf("%s is not a whole number of 0 or more", digits)
	}
	return u, nil
}

// number reads a float from a number.
func number(src any) (float64, error) {
	switch x := src.(type) {
	case float64:
		return x, nil
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, gqlcore.InputErrorf("%s is not a number", x)
		}
		return f, nil
	}
	v := reflect.ValueOf(src)
	switch {
	case v.CanFloat():
		return v.Float(), nil
	case v.CanInt():
		return float64(v.Int()), nil
	}
	return 0, gqlcore.InputErrorf("expected a number, got %s", jsonType(src))
}

// scalarText is a scalar as the text a type reads itself from.
func scalarText(src any) (string, bool) {
	switch x := src.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	}
	if v := reflect.ValueOf(src); v.CanInt() {
		return strconv.FormatInt(v.Int(), 10), true
	}
	return "", false
}

// jsonType is the JSON type of v, as a document holds it, for an error.
func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case float64, json.Number:
		return "a number"
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	}
	return fmt.Sprintf("a %T", v)
}
