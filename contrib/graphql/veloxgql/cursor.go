package veloxgql

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Cursor is a connection cursor: where a row is in a query's order. It is
// the Cursor scalar. On the wire it is a string, EncodeCursor's text, which
// the client passes back but does not read.
//
// A cursor is bound to the GraphQL type and the order it was taken under:
// a connection refuses it for another type or another order.
type Cursor struct {
	// Type is the GraphQL type of the connection's nodes, such as "User".
	Type string
	// Terms are the query's order terms with the row's values, in order. The
	// primary key, which ends every order, is Key and not a term.
	Terms []CursorTerm
	// Key is the row's primary key. After DecodeCursor it is a JSON value, as
	// each term's Value is; a connection converts them to their Go types.
	Key any
}

// CursorTerm is one order term of a cursor and the row's value of it.
type CursorTerm struct {
	// Field is the term's order field, the GraphQL enum value, such as "NAME".
	Field     string
	Direction OrderDirection
	Value     any
}

// cursorVersion is the version of the cursor's JSON document.
const cursorVersion = 1

// cursorDoc is the cursor's JSON document,
// {"v":1,"t":"<Type>","o":[{"f":"<FIELD>","d":"ASC","v":<value>}…],"k":<key>}.
type cursorDoc struct {
	V *int             `json:"v"`
	T *string          `json:"t"`
	O *[]cursorTermDoc `json:"o"`
	K any              `json:"k"`
}

type cursorTermDoc struct {
	F *string         `json:"f"`
	D *OrderDirection `json:"d"`
	V any             `json:"v"`
}

// EncodeCursor returns c as a cursor: base64url without padding of the JSON
// document {"v":1,"t":"<Type>","o":[{"f":"<FIELD>","d":"ASC|DESC","v":<value>}…],"k":<key>}.
// Integers are JSON numbers with every digit, an int64 above 2^53 included;
// times are RFC 3339 strings with nanoseconds and the zone offset; UUIDs are
// their string form; NaN and infinite floats are the strings NaN, +Inf and
// -Inf. A key is a number when it is an integer and a string otherwise.
//
// A cursor with no Type or Key, a term with no Field or an invalid Direction, or
// a value json.Marshal cannot write, such as a custom type's whose MarshalJSON
// fails, is an error.
func EncodeCursor(c Cursor) (string, error) {
	switch {
	case c.Type == "":
		return "", errors.New("veloxgql: EncodeCursor: the cursor has no type")
	case c.Key == nil:
		return "", errors.New("veloxgql: EncodeCursor: the cursor has no key")
	}
	if s, ok := encodeCursorPlain(c); ok {
		return s, nil
	}
	v := cursorVersion
	o := make([]cursorTermDoc, len(c.Terms))
	for i, t := range c.Terms {
		if t.Field == "" || !t.Direction.IsValid() {
			return "", fmt.Errorf("veloxgql: EncodeCursor: term %d has no field or an invalid direction", i)
		}
		o[i] = cursorTermDoc{F: &t.Field, D: &t.Direction, V: gqlcore.JSONValue(base(t.Value))}
	}
	b, err := json.Marshal(cursorDoc{V: &v, T: &c.Type, O: &o, K: gqlcore.JSONValue(base(c.Key))})
	if err != nil {
		return "", fmt.Errorf("veloxgql: EncodeCursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// encodeCursorPlain is EncodeCursor's text for a cursor of the common shape,
// written without reflection: a type and fields of plain text, and a key and
// values that are integers, plain strings, booleans or nil. It is byte for
// byte what json.Marshal of the document gives, which TestEncodeCursorPlain
// holds it to. For any other cursor, and for one EncodeCursor refuses, it
// reports false and EncodeCursor takes the general path.
func encodeCursorPlain(c Cursor) (string, bool) {
	var arr [160]byte
	b := append(arr[:0], `{"v":1,"t":"`...)
	if !plainText(c.Type) {
		return "", false
	}
	b = append(b, c.Type...)
	b = append(b, `","o":[`...)
	for i, t := range c.Terms {
		if !plainText(t.Field) || !t.Direction.IsValid() {
			return "", false
		}
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, `{"f":"`...)
		b = append(b, t.Field...)
		b = append(b, `","d":"`...)
		b = append(b, t.Direction...)
		b = append(b, `","v":`...)
		var ok bool
		if b, ok = appendPlainValue(b, t.Value); !ok {
			return "", false
		}
		b = append(b, '}')
	}
	b = append(b, `],"k":`...)
	var ok bool
	if b, ok = appendPlainValue(b, c.Key); !ok {
		return "", false
	}
	b = append(b, '}')
	var enc [216]byte
	return string(base64.RawURLEncoding.AppendEncode(enc[:0], b)), true
}

// appendPlainValue appends v as JSON when v is nil, a boolean, an integer or a
// plain string.
func appendPlainValue(b []byte, v any) ([]byte, bool) {
	switch x := base(v).(type) {
	case nil:
		return append(b, "null"...), true
	case bool:
		return strconv.AppendBool(b, x), true
	case int:
		return strconv.AppendInt(b, int64(x), 10), true
	case int8:
		return strconv.AppendInt(b, int64(x), 10), true
	case int16:
		return strconv.AppendInt(b, int64(x), 10), true
	case int32:
		return strconv.AppendInt(b, int64(x), 10), true
	case int64:
		return strconv.AppendInt(b, x, 10), true
	case uint:
		return strconv.AppendUint(b, uint64(x), 10), true
	case uint8:
		return strconv.AppendUint(b, uint64(x), 10), true
	case uint16:
		return strconv.AppendUint(b, uint64(x), 10), true
	case uint32:
		return strconv.AppendUint(b, uint64(x), 10), true
	case uint64:
		return strconv.AppendUint(b, x, 10), true
	case string:
		if !plainText(x) {
			return b, false
		}
		b = append(b, '"')
		b = append(b, x...)
		return append(b, '"'), true
	}
	return b, false
}

// plainText reports whether s is non-empty printable ASCII that json.Marshal
// writes as it is: none of the characters it escapes (quote, backslash, <, > and &).
func plainText(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		switch ch := s[i]; {
		case ch < 0x20 || ch > 0x7e, ch == '"', ch == '\\', ch == '<', ch == '>', ch == '&':
			return false
		}
	}
	return true
}

// DecodeCursor decodes s, a cursor EncodeCursor wrote. Its terms' Values and
// its Key are JSON values: json.Number for numbers, string, bool and, in a
// term, nil; a connection converts them, and checks the cursor against its
// type and order, before using it.
//
// s must be canonical base64url without padding (no line breaks, no stray
// trailing bits) of a version 1 document with a non-empty type, terms each
// with a non-empty field, ASC or DESC and a number, string, bool or null, and
// a key that is a number or a string, and no other field. Anything else fails
// with ErrBadCursor. Only the base64 is held to one spelling: the JSON may
// differ from what EncodeCursor writes in white space, member order and how a
// number is spelled, since a cursor is a position, not an identity.
func DecodeCursor(s string) (Cursor, error) {
	b, ok := decodeBase64(s)
	if !ok {
		return Cursor{}, badCursor("not canonical base64url without padding")
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
	case d.T == nil || *d.T == "":
		return Cursor{}, badCursor("no type")
	case d.O == nil:
		return Cursor{}, badCursor("no order terms")
	}
	c := Cursor{Type: *d.T, Terms: make([]CursorTerm, len(*d.O)), Key: d.K}
	for i, t := range *d.O {
		switch {
		case t.F == nil || *t.F == "":
			return Cursor{}, badCursor("an order term has no field")
		case t.D == nil || !t.D.IsValid():
			return Cursor{}, badCursor("an order term's direction is not ASC or DESC")
		}
		switch t.V.(type) {
		case json.Number, string, bool, nil:
		default:
			return Cursor{}, badCursor("an order value is not a number, string, bool or null")
		}
		c.Terms[i] = CursorTerm{Field: *t.F, Direction: *t.D, Value: t.V}
	}
	switch d.K.(type) {
	case json.Number, string:
	default:
		return Cursor{}, badCursor("the key is not a number or a string")
	}
	return c, nil
}

// decodeBase64 decodes s, base64url without padding, only when s is the
// canonical encoding of what it decodes to: the decoder ignores line breaks
// and accepts non-zero trailing bits, which would give one value many
// encodings.
func decodeBase64(s string) ([]byte, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, false
	}
	return b, true
}

func badCursor(why string) error {
	return gqlcore.InputErrorf("%w: %s", ErrBadCursor, why)
}

// MarshalGQLContext writes c as a JSON string, EncodeCursor's text. gqlgen reports
// its error on the field.
func (c Cursor) MarshalGQLContext(_ context.Context, w io.Writer) error {
	s, err := EncodeCursor(c)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, `"`+s+`"`)
	return err
}

// UnmarshalGQLContext decodes a cursor from v, a string; see DecodeCursor.
func (c *Cursor) UnmarshalGQLContext(_ context.Context, v any) error {
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

// MarshalGQL writes c as MarshalGQLContext does, for graphql-go, whose
// scalar marshaler takes no context. gqlgen calls MarshalGQLContext, which
// it prefers when a type has both. A cursor that does not encode (a value of
// a type EncodeCursor refuses) writes null.
func (c Cursor) MarshalGQL(w io.Writer) {
	if err := c.MarshalGQLContext(context.Background(), w); err != nil {
		_, _ = io.WriteString(w, "null")
	}
}

// UnmarshalGQL decodes a cursor from v, as UnmarshalGQLContext does.
func (c *Cursor) UnmarshalGQL(v any) error {
	return c.UnmarshalGQLContext(context.Background(), v)
}

// PageInfo is the GraphQL type PageInfo of a connection, an object gqlgen
// and graphql-go bind by its fields.
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

// String is d as it is.
func (d OrderDirection) String() string { return string(d) }

// MarshalGQL writes d as an escaped JSON string, so that even an invalid d
// leaves the response valid JSON.
func (d OrderDirection) MarshalGQL(w io.Writer) {
	b, _ := json.Marshal(string(d)) // never fails for a string
	_, _ = w.Write(b)
}

// UnmarshalGQL sets d from v, the string ASC or DESC.
func (d *OrderDirection) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok || !OrderDirection(s).IsValid() {
		return gqlcore.InputErrorf("OrderDirection must be ASC or DESC")
	}
	*d = OrderDirection(s)
	return nil
}
