package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"unicode/utf8"
)

// jsonElem is the element an OpJSONHas looks for in a JSON array: a string, a
// number or a boolean, in the forms the dialects take it.
type jsonElem struct {
	kind JSONKind // JSONKindText, JSONKindNumber or JSONKindBool
	text string   // its JSON encoding, which PostgreSQL and MySQL bind as JSON
	v    any      // a string, an int64, a float64 or a bool, which SQLite compares
}

// newJSONElem returns v as an element: a string, a boolean or a number of any
// Go type of those kinds. Anything else, NaN and an infinity included, has no
// JSON encoding that every dialect reads alike.
func newJSONElem(v any) (*jsonElem, error) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		s := rv.String()
		if !utf8.ValidString(s) {
			return nil, fmt.Errorf("velox: a JSON array element %q is not valid UTF-8", s)
		}
		b, _ := json.Marshal(s) // a valid string always encodes
		return &jsonElem{kind: JSONKindText, text: string(b), v: s}, nil
	case reflect.Bool:
		return &jsonElem{kind: JSONKindBool, text: strconv.FormatBool(rv.Bool()), v: rv.Bool()}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &jsonElem{kind: JSONKindNumber, text: strconv.FormatInt(rv.Int(), 10), v: rv.Int()}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := rv.Uint()
		if u > math.MaxInt64 {
			return nil, fmt.Errorf("velox: a JSON array element %d is above the largest integer SQLite compares, 2^63-1", u)
		}
		return &jsonElem{kind: JSONKindNumber, text: strconv.FormatUint(u, 10), v: int64(u)}, nil
	case reflect.Float32, reflect.Float64:
		bits := 64
		if rv.Kind() == reflect.Float32 {
			bits = 32
		}
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("velox: a JSON array element %v is not a JSON number", f)
		}
		text := strconv.FormatFloat(f, 'g', -1, bits)
		native, _ := strconv.ParseFloat(text, 64) // so that a float32 is the number its text says
		return &jsonElem{kind: JSONKindNumber, text: text, v: native}, nil
	}
	return nil, fmt.Errorf("velox: a JSON array element is a string, a number or a boolean, not a %T", v)
}

// JSONHasNode is true where the JSON array in column col holds the element v:
// a string, a boolean or a number of any Go type of those kinds. A number
// matches the same number written otherwise (1 and 1.0), a string only itself,
// and the boolean true is not the number 1. It is false where the column holds
// a document that is no array, and NULL where the column is NULL, as a
// comparison is: neither Has nor its negation matches a NULL column. Any other
// v makes an invalid condition.
func JSONHasNode(col int32, v any) Node {
	e, err := newJSONElem(v)
	if err != nil {
		return Node{Op: OpInvalid, V: err}
	}
	return Node{Op: OpJSONHas, Col: col, V: e}
}

// JSONEmptyNode is true where the column holds the empty array, []. It is
// false for an array with an element and for a document that is no array, and
// NULL where the column is.
func JSONEmptyNode(col int32) Node { return Node{Op: OpJSONEmpty, Col: col} }

// jsonColumn reports whether column i is a JSON column, failing the statement
// when it is not: the array conditions read JSON, which no other column holds.
func (w *writer) jsonColumn(i int32) bool {
	if w.in.Columns[i].Codec {
		return true
	}
	w.fail(fmt.Errorf("velox: %s: column %s is not a JSON column, which a JSON array condition needs", w.in.Name, w.in.Columns[i].Name))
	return false
}

// jsonHas writes the OpJSONHas node n. PostgreSQL's @> and MySQL's
// JSON_CONTAINS take the element as JSON; SQLite has no such operator, so it
// searches json_each, by the element's JSON type as well as its value, so that
// the true of a boolean does not match the number 1, as it does not elsewhere.
// Each first asks for an array: @> and JSON_CONTAINS also match a document
// that is that very scalar, and json_each walks the values of an object. The
// type of a NULL column is NULL, so the condition is NULL there on every
// dialect. The bare @> stays a conjunct, so that a GIN index on a jsonb column
// still serves it. PostgreSQL's column must be jsonb, as velox-gen writes it.
func (w *writer) jsonHas(n Node) {
	if !w.jsonColumn(n.Col) {
		return
	}
	e := n.V.(*jsonElem)
	switch w.d {
	case Postgres:
		w.str("(jsonb_typeof(")
		w.colRef(n.Col)
		w.str(") = 'array' AND ")
		w.colRef(n.Col)
		w.str(" @> ")
		w.argOf(n.Col, e.text)
		w.str("::jsonb)")
	case MySQL:
		w.str("(JSON_TYPE(")
		w.colRef(n.Col)
		w.str(") = 'ARRAY' AND JSON_CONTAINS(")
		w.colRef(n.Col)
		w.str(", ")
		w.argOf(n.Col, e.text)
		w.str("))")
	default: // SQLite
		w.str("CASE WHEN json_type(")
		w.colRef(n.Col)
		w.str(") = 'array' THEN EXISTS (SELECT 1 FROM json_each(")
		w.colRef(n.Col)
		w.str(") AS je WHERE ")
		switch e.kind {
		case JSONKindText:
			w.str("je.type = 'text' AND je.value = ")
			w.argOf(n.Col, e.v)
		case JSONKindNumber:
			w.str("je.type IN ('integer', 'real') AND je.value = ")
			w.argOf(n.Col, e.v)
		default: // a boolean: the type says it, so the value is not compared
			w.str("je.type = ")
			w.argOf(n.Col, e.text)
		}
		w.str(") WHEN ")
		w.colRef(n.Col)
		w.str(" IS NULL THEN NULL ELSE 1 = 0 END")
	}
}

// jsonEmpty writes the OpJSONEmpty node n: the column is an array, and has no
// element. The length of a document that is no array is not asked, which
// PostgreSQL's jsonb_array_length refuses.
func (w *writer) jsonEmpty(n Node) {
	if !w.jsonColumn(n.Col) {
		return
	}
	switch w.d {
	case Postgres:
		w.str("CASE WHEN jsonb_typeof(")
		w.colRef(n.Col)
		w.str(") = 'array' THEN jsonb_array_length(")
	case MySQL:
		w.str("CASE WHEN JSON_TYPE(")
		w.colRef(n.Col)
		w.str(") = 'ARRAY' THEN JSON_LENGTH(")
	default:
		w.str("CASE WHEN json_type(")
		w.colRef(n.Col)
		w.str(") = 'array' THEN json_array_length(")
	}
	w.colRef(n.Col)
	w.str(") = 0 WHEN ")
	w.colRef(n.Col)
	w.str(" IS NULL THEN NULL ELSE 1 = 0 END")
}
