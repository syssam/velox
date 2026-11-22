package engine

import (
	"fmt"
	"strings"
	"unicode"
)

// JSONKind says what a JSON path condition reads at its path.
type JSONKind uint8

// JSONKindAny and the rest are the kinds of JSONPath.
const (
	// JSONKindAny reads the value of any type: SQL NULL only where the
	// path is missing, so a JSON null is a value (HasKey).
	JSONKindAny JSONKind = iota
	// JSONKindText reads a string: SQL NULL where the path is missing or
	// holds no string.
	JSONKindText
	// JSONKindNumber reads a number: SQL NULL where the path is missing or
	// holds no number.
	JSONKindNumber
	// JSONKindBool reads a boolean: SQL NULL where the path is missing or
	// holds no boolean.
	JSONKindBool
	// JSONKindLen reads the number of elements of an array: SQL NULL where
	// the path is missing or holds no array. Its path may be empty: the
	// column's own document.
	JSONKindLen
)

// JSONPath is a path of object keys into a JSON column, and what a
// condition reads there.
type JSONPath struct {
	Kind JSONKind
	Keys []string
}

// JSONNode is the condition leaf applied to the value at path p in the JSON
// column col: leaf's operator and values, with its column replaced by that
// value. A path that cannot be written alike on every dialect is an invalid
// condition.
func JSONNode(col int32, p JSONPath, leaf Node) Node {
	if err := p.check(); err != nil {
		return Node{Op: OpInvalid, V: err}
	}
	return Node{Op: OpJSON, Col: col, V: p, Kids: []Node{leaf}}
}

// check refuses an empty path but a length's, and a key that is empty or
// holds a double quote, a backslash or a control character: MySQL's and
// SQLite's paths quote each key in double quotes, and SQLite's has no escape
// inside them.
func (p JSONPath) check() error {
	if len(p.Keys) == 0 && p.Kind != JSONKindLen {
		return fmt.Errorf("velox: a JSON path needs at least one key")
	}
	for _, k := range p.Keys {
		if k == "" || strings.ContainsAny(k, `"\`) || strings.IndexFunc(k, unicode.IsControl) >= 0 {
			return fmt.Errorf("velox: JSON path key %q: a key may not be empty or hold a double quote, a backslash or a control character", k)
		}
	}
	return nil
}

// quotedPath is p's keys as a MySQL and SQLite path: $."a"."b".
func (p JSONPath) quotedPath() string {
	var b strings.Builder
	b.WriteByte('$')
	for _, k := range p.Keys {
		b.WriteString(`."`)
		b.WriteString(k)
		b.WriteByte('"')
	}
	return b.String()
}

// json writes the value that the OpJSON node n's path reads from its
// column, then the kid's operator on it, as pred writes it on a column.
func (w *writer) json(n Node) {
	if !w.in.Columns[n.Col].Codec {
		w.fail(fmt.Errorf("velox: %s: column %s is not a JSON column, which a JSON path needs", w.in.Name, w.in.Columns[n.Col].Name))
		return
	}
	p := n.V.(JSONPath)
	kid := n.Kids[0]
	kid.Col = n.Col
	w.path = &p
	w.pred(kid)
	w.path = nil
}

// jsonValue writes the value at path p in column i, by its kind. Every
// key, or MySQL's and SQLite's whole path, is an argument. The forms read
// alike on every dialect: a string is compared as written, with case.
func (w *writer) jsonValue(i int32, p JSONPath) {
	if p.Kind == JSONKindLen {
		w.jsonLen(i, p)
		return
	}
	switch w.d {
	case Postgres:
		call := func(fn string) {
			w.str(fn + "(")
			w.colRef(i)
			for _, k := range p.Keys {
				w.str(", ")
				w.argOf(i, k)
			}
			w.str(")")
		}
		switch p.Kind {
		case JSONKindAny:
			call("jsonb_extract_path")
		case JSONKindText:
			w.str("CASE WHEN jsonb_typeof(")
			call("jsonb_extract_path")
			w.str(") = 'string' THEN ")
			call("jsonb_extract_path_text")
			w.str(" END")
		case JSONKindNumber:
			w.str("CASE WHEN jsonb_typeof(")
			call("jsonb_extract_path")
			w.str(") = 'number' THEN (")
			call("jsonb_extract_path_text")
			w.str(")::numeric END")
		case JSONKindBool:
			w.str("CASE WHEN jsonb_typeof(")
			call("jsonb_extract_path")
			w.str(") = 'boolean' THEN (")
			call("jsonb_extract_path_text")
			w.str(")::boolean END")
		}
	case MySQL:
		extract := func() {
			w.str("JSON_EXTRACT(")
			w.colRef(i)
			w.str(", ")
			w.argOf(i, p.quotedPath())
			w.str(")")
		}
		switch p.Kind {
		case JSONKindAny:
			extract()
		case JSONKindText:
			w.str("CASE WHEN JSON_TYPE(")
			extract()
			w.str(") = 'STRING' THEN JSON_UNQUOTE(")
			extract()
			w.str(") END")
		case JSONKindNumber:
			w.str("CASE WHEN JSON_TYPE(")
			extract()
			w.str(") IN ('INTEGER', 'UNSIGNED INTEGER', 'DOUBLE', 'DECIMAL') THEN CAST(")
			extract()
			w.str(" AS DOUBLE) END")
		case JSONKindBool:
			w.str("CASE WHEN JSON_TYPE(")
			extract()
			w.str(") = 'BOOLEAN' THEN ")
			extract()
			w.str(" = CAST('true' AS JSON) END")
		}
	default: // SQLite
		call := func(fn string) {
			w.str(fn + "(")
			w.colRef(i)
			w.str(", ")
			w.argOf(i, p.quotedPath())
			w.str(")")
		}
		switch p.Kind {
		case JSONKindAny:
			call("json_type")
		case JSONKindText:
			w.str("CASE WHEN ")
			call("json_type")
			w.str(" = 'text' THEN ")
			call("json_extract")
			w.str(" END")
		case JSONKindNumber:
			w.str("CASE WHEN ")
			call("json_type")
			w.str(" IN ('integer', 'real') THEN ")
			call("json_extract")
			w.str(" END")
		case JSONKindBool:
			w.str("CASE WHEN ")
			call("json_type")
			w.str(" IN ('true', 'false') THEN ")
			call("json_extract")
			w.str(" END")
		}
	}
}

// jsonLen writes the length of the array at path p in column i: NULL where
// the path is missing or holds no array, as PostgreSQL's jsonb_array_length
// refuses a scalar and MySQL's JSON_LENGTH counts an object's keys. An empty
// path is the column's own document. PostgreSQL's length is numeric, as
// JSONKindNumber's value is, so that a float64 argument compares with it.
func (w *writer) jsonLen(i int32, p JSONPath) {
	switch w.d {
	case Postgres:
		doc := func() {
			if len(p.Keys) == 0 {
				w.colRef(i)
				return
			}
			w.str("jsonb_extract_path(")
			w.colRef(i)
			for _, k := range p.Keys {
				w.str(", ")
				w.argOf(i, k)
			}
			w.str(")")
		}
		w.str("CASE WHEN jsonb_typeof(")
		doc()
		w.str(") = 'array' THEN (jsonb_array_length(")
		doc()
		w.str("))::numeric END")
	case MySQL:
		w.str("CASE WHEN JSON_TYPE(JSON_EXTRACT(")
		w.colRef(i)
		w.str(", ")
		w.argOf(i, p.quotedPath())
		w.str(")) = 'ARRAY' THEN JSON_LENGTH(")
		w.colRef(i)
		w.str(", ")
		w.argOf(i, p.quotedPath())
		w.str(") END")
	default: // SQLite
		w.str("CASE WHEN json_type(")
		w.colRef(i)
		w.str(", ")
		w.argOf(i, p.quotedPath())
		w.str(") = 'array' THEN json_array_length(")
		w.colRef(i)
		w.str(", ")
		w.argOf(i, p.quotedPath())
		w.str(") END")
	}
}

// jsonLike is the pattern of an opJSONLike: the same match as a LIKE pattern,
// escaped with !, and as a SQLite GLOB pattern.
type jsonLike struct{ like, glob string }

// The places of a string in the string at a JSON path that JSONLikeLeaf
// matches.
const (
	JSONContains = iota
	JSONHasPrefix
	JSONHasSuffix
)

// JSONLikeLeaf is the kid of an OpJSON that is true where the string at the
// path contains s (JSONContains), begins with it (JSONHasPrefix) or ends with
// it (JSONHasSuffix), s taken literally and with case on every dialect:
// PostgreSQL's LIKE and MySQL's, whose JSON_UNQUOTE gives utf8mb4_bin text,
// keep case, and SQLite, whose LIKE ignores the case of ASCII letters, gets a
// GLOB.
func JSONLikeLeaf(where int, s string) Node {
	like, glob := EscapeLike(s), escapeGlob(s)
	switch where {
	case JSONContains:
		like, glob = "%"+like+"%", "*"+glob+"*"
	case JSONHasPrefix:
		like, glob = like+"%", glob+"*"
	default:
		like, glob = "%"+like, "*"+glob
	}
	return Node{Op: opJSONLike, V: jsonLike{like, glob}}
}

// escapeGlob is s as a GLOB pattern that matches s alone: each of *, ? and [
// in a class of its own.
func escapeGlob(s string) string {
	if !strings.ContainsAny(s, "*?[") {
		return s
	}
	var b strings.Builder
	for i := range len(s) {
		switch c := s[i]; c {
		case '*', '?', '[':
			b.WriteByte('[')
			b.WriteByte(c)
			b.WriteByte(']')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// jsonLike writes the opJSONLike node n on the value at the path an OpJSON
// set: LIKE with the ! escape, or SQLite's GLOB, which keeps case.
func (w *writer) jsonLike(n Node) {
	if w.path == nil {
		w.fail(fmt.Errorf("velox: %s: a JSON text pattern outside a JSON path", w.in.Name))
		return
	}
	pat := n.V.(jsonLike)
	w.predCol(n.Col)
	if w.d == SQLite {
		w.str(" GLOB ")
		w.argOf(n.Col, pat.glob)
		return
	}
	w.str(" LIKE ")
	w.argOf(n.Col, pat.like)
	w.str(" ESCAPE '!'")
}
