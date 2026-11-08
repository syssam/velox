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

// check refuses an empty path, and a key that is empty or holds a double
// quote, a backslash or a control character: MySQL's and SQLite's paths
// quote each key in double quotes, and SQLite's has no escape inside them.
func (p JSONPath) check() error {
	if len(p.Keys) == 0 {
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
	switch w.d {
	case Postgres:
		call := func(fn string) {
			w.str(fn + "(")
			w.colRef(i)
			for _, k := range p.Keys {
				w.str(", ")
				w.arg(k)
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
		}
	case MySQL:
		extract := func() {
			w.str("JSON_EXTRACT(")
			w.colRef(i)
			w.str(", ")
			w.arg(p.quotedPath())
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
		}
	default: // SQLite
		call := func(fn string) {
			w.str(fn + "(")
			w.colRef(i)
			w.str(", ")
			w.arg(p.quotedPath())
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
		}
	}
}
