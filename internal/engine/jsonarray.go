package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
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

// JSONAtPath is n, a JSONHasNode or a JSONEmptyNode, on the array at the path
// of object keys in its column in place of the column's own document. It is
// false where the path is missing or holds no array, and NULL where the
// column is NULL, as n is on the column. A key that a JSON path refuses
// makes an invalid condition; no keys leaves n as it is.
func JSONAtPath(n Node, keys []string) Node {
	if n.Op == OpInvalid || len(keys) == 0 {
		return n
	}
	if n.Op != OpJSONHas && n.Op != OpJSONEmpty {
		return Node{Op: OpInvalid, V: errors.New("velox: a JSON array path on a condition that tests no JSON array")}
	}
	if err := (JSONPath{Kind: JSONKindLen, Keys: keys}).check(); err != nil {
		return Node{Op: OpInvalid, V: err}
	}
	n.W = slices.Clone(keys)
	return n
}

// arrayPath is the path of object keys of an OpJSONHas or OpJSONEmpty node:
// nil for the column's own document.
func arrayPath(n Node) []string {
	keys, _ := n.W.([]string)
	return keys
}

// jsonColumn reports whether column i is a JSON column, failing the statement
// when it is not: the array conditions read JSON, which no other column holds.
func (w *writer) jsonColumn(i int32) bool {
	if w.in.Columns[i].Codec {
		return true
	}
	w.fail(fmt.Errorf("velox: %s: column %s is not a JSON column, which a JSON array condition needs", w.in.Name, w.in.Columns[i].Name))
	return false
}

// jsonDoc writes the document at path keys in column i, as PostgreSQL's
// jsonb_extract_path and MySQL's JSON_EXTRACT read it, or the column itself
// for no keys. Every key, or MySQL's whole path, is an argument.
func (w *writer) jsonDoc(i int32, keys []string) {
	switch {
	case len(keys) == 0:
		w.colRef(i)
	case w.d == Postgres:
		w.str("jsonb_extract_path(")
		w.colRef(i)
		for _, k := range keys {
			w.str(", ")
			w.argOf(i, k)
		}
		w.str(")")
	default: // MySQL
		w.str("JSON_EXTRACT(")
		w.colRef(i)
		w.str(", ")
		w.argOf(i, JSONPath{Keys: keys}.quotedPath())
		w.str(")")
	}
}

// docArgs writes the arguments of SQLite's json_type, json_each and
// json_array_length, and MySQL's JSON_LENGTH, that name the document at path
// keys in column i: the column, then the path as an argument when there are
// keys.
func (w *writer) docArgs(i int32, keys []string) {
	w.colRef(i)
	if len(keys) > 0 {
		w.str(", ")
		w.argOf(i, JSONPath{Keys: keys}.quotedPath())
	}
}

// containsAt is the JSON document, as text, that holds the element of text
// elem in an array at path keys: {"a":{"b":[elem]}}. PostgreSQL's @> finds
// it in a document whose array at the path holds the element, which a GIN
// index on the column serves.
func containsAt(keys []string, elem string) string {
	s := "[" + elem + "]"
	for _, key := range slices.Backward(keys) {
		k, _ := json.Marshal(key) // a checked key always encodes
		s = "{" + string(k) + ":" + s + "}"
	}
	return s
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
//
// At a path, PostgreSQL asks for an array there, and finds the element by @>
// of the column against a document that holds it at the path (containsAt).
// That @> is false where the path is missing, so the conjunction is false
// there and NULL only for a NULL column, and the GIN index still serves it.
// MySQL's and SQLite's are a CASE, as their tests at a missing path are NULL.
func (w *writer) jsonHas(n Node) {
	if !w.jsonColumn(n.Col) {
		return
	}
	e := n.V.(*jsonElem)
	keys := arrayPath(n)
	switch w.d {
	case Postgres:
		w.str("(jsonb_typeof(")
		w.jsonDoc(n.Col, keys)
		w.str(") = 'array' AND ")
		w.colRef(n.Col)
		w.str(" @> ")
		if len(keys) == 0 {
			w.argOf(n.Col, e.text)
		} else {
			w.argOf(n.Col, containsAt(keys, e.text))
		}
		w.str("::jsonb)")
	case MySQL:
		if len(keys) == 0 {
			w.str("(JSON_TYPE(")
			w.colRef(n.Col)
			w.str(") = 'ARRAY' AND JSON_CONTAINS(")
			w.colRef(n.Col)
			w.str(", ")
			w.argOf(n.Col, e.text)
			w.str("))")
			return
		}
		w.str("CASE WHEN JSON_TYPE(")
		w.jsonDoc(n.Col, keys)
		w.str(") = 'ARRAY' THEN JSON_CONTAINS(")
		w.colRef(n.Col)
		w.str(", ")
		w.argOf(n.Col, e.text)
		w.str(", ")
		w.argOf(n.Col, JSONPath{Keys: keys}.quotedPath())
		w.str(") WHEN ")
		w.colRef(n.Col)
		w.str(" IS NULL THEN NULL ELSE 1 = 0 END")
	default: // SQLite
		w.str("CASE WHEN json_type(")
		w.docArgs(n.Col, keys)
		w.str(") = 'array' THEN EXISTS (SELECT 1 FROM json_each(")
		w.docArgs(n.Col, keys)
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

// jsonEmpty writes the OpJSONEmpty node n: the column, or the document at its
// path, is an array, and has no element. The length of a document that is no
// array is not asked, which PostgreSQL's jsonb_array_length refuses.
func (w *writer) jsonEmpty(n Node) {
	if !w.jsonColumn(n.Col) {
		return
	}
	keys := arrayPath(n)
	switch w.d {
	case Postgres:
		w.str("CASE WHEN jsonb_typeof(")
		w.jsonDoc(n.Col, keys)
		w.str(") = 'array' THEN jsonb_array_length(")
		w.jsonDoc(n.Col, keys)
	case MySQL:
		w.str("CASE WHEN JSON_TYPE(")
		w.jsonDoc(n.Col, keys)
		w.str(") = 'ARRAY' THEN JSON_LENGTH(")
		w.docArgs(n.Col, keys)
	default:
		w.str("CASE WHEN json_type(")
		w.docArgs(n.Col, keys)
		w.str(") = 'array' THEN json_array_length(")
		w.docArgs(n.Col, keys)
	}
	w.str(") = 0 WHEN ")
	w.colRef(n.Col)
	w.str(" IS NULL THEN NULL ELSE 1 = 0 END")
}
