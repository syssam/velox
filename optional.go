package velox

import (
	"bytes"
	"encoding/json"
)

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
