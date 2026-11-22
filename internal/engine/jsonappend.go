package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// appendLen is the number of elements of v, the slice an AssignAppend
// appends: 0 for nil and for a value that is no slice or array, which
// jsonAppend refuses.
func appendLen(v any) int {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return 0
		}
		rv = rv.Elem()
	}
	if k := rv.Kind(); k == reflect.Slice || k == reflect.Array {
		return rv.Len()
	}
	return 0
}

// appendElems is the JSON of the AssignAppend a's value, which must encode as
// an array, and its elements; nil and the JSON null append nothing.
func (w *writer) appendElems(a Assignment) (string, []json.RawMessage, bool) {
	b, err := encodeCol(w.in, a.Column, a.Value)
	if err != nil {
		w.fail(err)
		return "", nil, false
	}
	doc, _ := b.([]byte)
	if doc == nil || bytes.Equal(bytes.TrimSpace(doc), []byte("null")) {
		return "[]", nil, true
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(doc, &elems); err != nil || elems == nil {
		w.fail(fmt.Errorf("velox: %s: Append to column %s takes a value that encodes as a JSON array; a %T encodes as %.40q", w.in.Name, w.in.Columns[a.Column].Name, a.Value, doc))
		return "", nil, false
	}
	if w.d == SQLite && len(elems) != appendLen(a.Value) {
		// The render cache keys SQLite's text by the slice's length.
		w.fail(fmt.Errorf("velox: %s: Append to column %s: a %T of %d elements encodes as %d", w.in.Name, w.in.Columns[a.Column].Name, a.Value, appendLen(a.Value), len(elems)))
		return "", nil, false
	}
	return string(doc), elems, true
}

// jsonAppend writes the value of the AssignAppend a: the column's JSON array
// with a's elements after its own, as ent's sqljson.Append writes it. A NULL
// column, and one holding the JSON null, is taken as the empty array, so it
// becomes the elements; a document that is no array is taken as the array of
// it alone, as PostgreSQL's || and MySQL's JSON_MERGE_PRESERVE take it.
// PostgreSQL's column must be jsonb, as velox-gen writes it.
func (w *writer) jsonAppend(a Assignment) {
	col := int32(a.Column)
	if !w.in.Columns[col].Codec {
		w.fail(fmt.Errorf("velox: %s: column %s is not a JSON column, which Append needs", w.in.Name, w.in.Columns[col].Name))
		return
	}
	doc, elems, ok := w.appendElems(a)
	if !ok {
		return
	}
	switch w.d {
	case Postgres:
		w.str("CASE WHEN ")
		w.col(col)
		w.str(" IS NULL OR jsonb_typeof(")
		w.col(col)
		w.str(") = 'null' THEN '[]'::jsonb ELSE ")
		w.col(col)
		w.str(" END || ")
		w.argOf(col, doc)
		w.str("::jsonb")
	case MySQL:
		w.str("JSON_MERGE_PRESERVE(CASE WHEN ")
		w.col(col)
		w.str(" IS NULL OR JSON_TYPE(")
		w.col(col)
		w.str(") = 'NULL' THEN JSON_ARRAY() ELSE ")
		w.col(col)
		w.str(" END, ")
		w.argOf(col, doc)
		w.str(")")
	default: // SQLite: json_insert appends one value per '$[#]'
		if len(elems) == 0 {
			w.str("json(")
		} else {
			w.str("json_insert(")
		}
		w.str("CASE WHEN ")
		w.col(col)
		w.str(" IS NULL OR json_type(")
		w.col(col)
		w.str(") = 'null' THEN '[]' WHEN json_type(")
		w.col(col)
		w.str(") = 'array' THEN ")
		w.col(col)
		w.str(" ELSE json_array(json(")
		w.col(col)
		w.str(")) END")
		for _, e := range elems {
			w.str(", '$[#]', json(")
			w.argOf(col, string(e))
			w.str(")")
		}
		w.str(")")
	}
}
