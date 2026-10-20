package veloxgql

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Cursor is a connection cursor: the values of a row's order terms and its
// primary key. It is the Cursor scalar, a string on the wire that the client
// does not read: EncodeCursor's text.
type Cursor struct {
	// Values are the row's values of the query's order terms, in order. After
	// DecodeCursor they are JSON values; CursorValue converts each to its
	// column's Go type.
	Values []any
	// Key is the row's primary key; after DecodeCursor, a JSON value too.
	Key any
}

// cursorVersion is the version of the cursor's JSON document.
const cursorVersion = 1

// cursorDoc is the cursor's JSON document, {"v":1,"o":[…],"k":<key>}.
type cursorDoc struct {
	V *int   `json:"v"`
	O *[]any `json:"o"`
	K any    `json:"k"`
}

// EncodeCursor returns c as a cursor: base64url without padding of the JSON
// document {"v":1,"o":[…values…],"k":<key>}. Integers are JSON numbers with
// every digit, an int64 above 2^53 included; times are RFC 3339 strings with
// nanoseconds and the zone offset; UUIDs are their string form; NaN and
// infinite floats are the strings NaN, +Inf and -Inf. A key is a number when
// it is an integer and a string otherwise. A cursor with no Key, or a value
// json.Marshal cannot write, is a mistake in the generated code, and
// EncodeCursor panics.
func EncodeCursor(c Cursor) string {
	if c.Key == nil {
		panic("veloxgql: EncodeCursor: the cursor has no key")
	}
	v := cursorVersion
	o := make([]any, len(c.Values))
	for i, x := range c.Values {
		o[i] = jsonValue(x)
	}
	b, err := json.Marshal(cursorDoc{V: &v, O: &o, K: jsonValue(c.Key)})
	if err != nil {
		panic("veloxgql: EncodeCursor: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor decodes s, a cursor EncodeCursor wrote. Its Values and Key are
// JSON values: json.Number for numbers, string, bool and, in Values, nil.
// CursorValue converts them. A cursor that is not base64url without padding
// of a version 1 document with "o" an array of such values and "k" a number
// or a string, and nothing else, fails with ErrBadCursor.
func DecodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, badCursor("not base64url without padding")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var d cursorDoc
	if err := dec.Decode(&d); err != nil {
		return Cursor{}, badCursor("not a cursor document")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Cursor{}, badCursor("data after the document")
	}
	switch {
	case d.V == nil || *d.V != cursorVersion:
		return Cursor{}, badCursor("not version 1")
	case d.O == nil:
		return Cursor{}, badCursor("no order values")
	}
	for _, v := range *d.O {
		switch v.(type) {
		case json.Number, string, bool, nil:
		default:
			return Cursor{}, badCursor("an order value is not a number, string, bool or null")
		}
	}
	switch d.K.(type) {
	case json.Number, string:
	default:
		return Cursor{}, badCursor("the key is not a number or a string")
	}
	return Cursor{Values: *d.O, Key: d.K}, nil
}

// CursorValue converts v, a value or the key of a decoded cursor, to T, the
// Go type of its column: a Go integer or Uint64 from a number, a float from a
// number (or NaN, +Inf, -Inf), a string, a bool, a time.Time or Time from an
// RFC 3339 string, a [16]byte or UUID from the UUID string form, []byte or
// Bytes from standard base64, or a type whose pointer has UnmarshalText from
// a string. A v of another type fails with ErrBadCursor: the cursor was taken
// under another order.
func CursorValue[T any](v any) (T, error) {
	var t T
	switch err := setValue(&t, v); {
	case errors.Is(err, errUnsupported):
		var zero T
		return zero, fmt.Errorf("veloxgql: CursorValue: %T is not a supported type", zero)
	case err != nil:
		var zero T
		return zero, badCursor(fmt.Sprintf("a value is not a %T", zero))
	}
	return t, nil
}

func badCursor(why string) error {
	return fmt.Errorf("veloxgql: %w: %s", ErrBadCursor, why)
}

// MarshalGQL writes c as a JSON string, EncodeCursor's text.
func (c Cursor) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, `"`+EncodeCursor(c)+`"`)
}

// UnmarshalGQL decodes a cursor from v, a string; see DecodeCursor.
func (c *Cursor) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return badCursor("not a string")
	}
	d, err := DecodeCursor(s)
	if err != nil {
		return err
	}
	*c = d
	return nil
}

// PageInfo is the GraphQL PageInfo of a connection.
type PageInfo struct {
	HasNextPage     bool
	HasPreviousPage bool
	// StartCursor and EndCursor are the cursors of the page's first and last
	// rows, nil when the page is empty.
	StartCursor *Cursor
	EndCursor   *Cursor
}

// OrderDirection is the GraphQL enum OrderDirection: the direction of an
// order term.
type OrderDirection string

// The order directions.
const (
	OrderDirectionAsc  OrderDirection = "ASC"
	OrderDirectionDesc OrderDirection = "DESC"
)

// IsValid reports whether d is ASC or DESC.
func (d OrderDirection) IsValid() bool {
	return d == OrderDirectionAsc || d == OrderDirectionDesc
}

func (d OrderDirection) String() string { return string(d) }

// MarshalGQL writes d as a JSON string.
func (d OrderDirection) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, `"`+string(d)+`"`)
}

// UnmarshalGQL sets d from v, the string ASC or DESC.
func (d *OrderDirection) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok || !OrderDirection(s).IsValid() {
		return errors.New("veloxgql: OrderDirection must be ASC or DESC")
	}
	*d = OrderDirection(s)
	return nil
}
