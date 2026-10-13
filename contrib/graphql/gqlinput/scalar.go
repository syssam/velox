package gqlinput

import (
	"encoding/json"
	"fmt"
)

// A JSON field of a Go type of its own (field.JSON("specs", Specs{})) is a
// scalar of the GraphQL schema, scalar Specs: on the wire it is the JSON the
// column holds. The generated tree declares, for each, the type under the
// scalar's name and Marshal and Unmarshal functions an engine binds the
// scalar to; these are what they call.

// EncodeScalar returns the value of a JSON scalar on the wire: the JSON of v.
func EncodeScalar[T any](v T) ([]byte, error) { return json.Marshal(v) }

// EncodeScalarPtr is EncodeScalar for a field that may hold nothing: nil is
// null.
func EncodeScalarPtr[T any](v *T) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*v)
}

// DecodeScalar reads the value of a JSON scalar as an engine hands it over:
// the value as JSON decodes it (an object is a map, a list a slice), or its
// JSON text as a string, []byte or json.RawMessage. A string that is not
// JSON text is the value itself, for a type that is a string in JSON. What
// the type's own decoding refuses is refused, and null is not a value: a
// field that may hold nothing is read with DecodeScalarPtr.
func DecodeScalar[T any](v any) (T, error) {
	var out T
	var text []byte
	isText := false
	switch val := v.(type) {
	case nil:
		return out, fmt.Errorf("gqlinput: null is not a %T", out)
	case string:
		if err := json.Unmarshal([]byte(val), &out); err == nil {
			return out, nil
		}
		// Not JSON text: the string is the value. Decoding starts from the
		// zero value again, whatever the attempt above left behind.
		var zero T
		out = zero
	case []byte:
		text, isText = val, true
	case json.RawMessage:
		text, isText = val, true
	}
	if !isText {
		var err error
		if text, err = json.Marshal(v); err != nil {
			return out, fmt.Errorf("gqlinput: a %T cannot be read as a %T: %w", v, out, err)
		}
	}
	if err := json.Unmarshal(text, &out); err != nil {
		var zero T
		return zero, fmt.Errorf("gqlinput: a %T cannot be read as a %T: %w", v, zero, err)
	}
	return out, nil
}

// DecodeScalarPtr is DecodeScalar for a field that may hold nothing: null is
// nil.
func DecodeScalarPtr[T any](v any) (*T, error) {
	if v == nil {
		return nil, nil
	}
	out, err := DecodeScalar[T](v)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
