package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
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
	// JSONKindTextNumber reads a number, or a string that spells one in
	// JSON's grammar (jsonNumberText): SQL NULL for any other value and
	// where the path is missing.
	JSONKindTextNumber
	// JSONKindTextTime reads a string that spells an RFC 3339 time
	// (jsonTimeText) as its instant: SQL NULL for any other value and
	// where the path is missing.
	JSONKindTextTime
	// JSONKindNull reads 1 where the path holds a JSON null: SQL NULL where
	// it is missing or holds any other value (ValueIsNull).
	JSONKindNull
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
	if w.d == Postgres && w.filter && !w.neg && containable(p, kid) {
		w.jsonContain(n.Col, p, kid)
		return
	}
	w.path = &p
	w.pred(kid)
	w.path = nil
}

// containable reports whether PostgreSQL may write the condition kid on
// the value at path p as containment of the column (jsonContain): Text's,
// Number's and Bool's EQ, Text's In with values, and ValueIsNull, on a path
// with no key that jsonb_extract_path reads as an array index (indexKey).
// It depends on the shape alone, which nodeKey keys.
func containable(p JSONPath, kid Node) bool {
	switch kid.Op {
	case OpEQ:
		if p.Kind != JSONKindText && p.Kind != JSONKindNumber && p.Kind != JSONKindBool {
			return false
		}
	case OpIn:
		if p.Kind != JSONKindText || len(kid.V.([]any)) == 0 {
			return false
		}
	case OpNotNull:
		if p.Kind != JSONKindNull {
			return false
		}
	default:
		return false
	}
	return !slices.ContainsFunc(p.Keys, indexKey)
}

// indexKey reports whether PostgreSQL's jsonb_extract_path may read the key
// k as the index of an array element: optional leading spaces, an optional
// sign, then digits only, as strtoint reads it. Containment never indexes
// an array, nor do MySQL's and SQLite's paths; the typed form does on
// PostgreSQL, so a path holding such a key keeps the typed form everywhere,
// and its conditions and their negations stay complements.
func indexKey(k string) bool {
	k = strings.TrimLeft(k, " ")
	if k != "" && (k[0] == '+' || k[0] == '-') {
		k = k[1:]
	}
	if k == "" {
		return false
	}
	for i := range len(k) {
		if k[i] < '0' || k[i] > '9' {
			return false
		}
	}
	return true
}

// jsonContain writes the condition kid on the value at path p in column
// col as PostgreSQL's containment, col @> doc, which a GIN index on the
// column serves, jsonb_path_ops or jsonb_ops: doc is the object of p's
// keys, nested, holding the value. An In is an Or of one per value, padded
// to inLen as an IN list is, so that a bitmap scan of the index serves each.
//
// Where the typed form is true, so is containment, and where it is false or
// NULL, containment is false (or NULL, where the column is): a key's value
// is contained by a value of the same JSON type only, a string equal byte
// for byte, a number by numeric equality (1.50 is 1.5), and a scalar by no
// array or object. So it is written only where NULL and false are alike:
// in a filter (filterPred) under an even number of NOTs.
func (w *writer) jsonContain(col int32, p JSONPath, kid Node) {
	if kid.Op != OpIn {
		w.colRef(col)
		w.str(" @> ")
		w.containDoc(col, p, kid.V)
		return
	}
	vs := kid.V.([]any)
	w.str("(")
	for i := range inLen(len(vs)) {
		if i > 0 {
			w.str(" OR ")
		}
		w.colRef(col)
		w.str(" @> ")
		w.containDoc(col, p, vs[min(i, len(vs)-1)])
	}
	w.str(")")
}

// containDoc binds the document {"k1": {"k2": v}} of path p's keys holding
// v, cast to jsonb: a string, a float64 or a bool, by p's kind, or a JSON
// null for ValueIsNull. A float64 is written as its shortest decimal, the
// numeric that pgx binds for the typed form's float64. NaN and an infinity,
// which no JSON number equals, bind SQL NULL, which keeps no row, as the
// typed form keeps none. A string or key that is not valid UTF-8 fails the
// statement, as PostgreSQL refuses it as text.
func (w *writer) containDoc(col int32, p JSONPath, v any) {
	var leaf string
	switch p.Kind {
	case JSONKindNull:
		leaf = "null"
	case JSONKindText:
		s, ok := v.(string)
		if !ok || !utf8.ValidString(s) {
			w.fail(fmt.Errorf("velox: %s: a JSON text value %q is not a valid UTF-8 string", w.in.Name, v))
			return
		}
		b, _ := json.Marshal(s) // a valid string always encodes
		leaf = string(b)
	case JSONKindNumber:
		f, ok := v.(float64)
		if !ok {
			w.fail(fmt.Errorf("velox: %s: a JSON number value is a float64, not a %T", w.in.Name, v))
			return
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			w.argOf(col, nil)
			w.str("::jsonb")
			return
		}
		leaf = strconv.FormatFloat(f, 'g', -1, 64)
	default: // JSONKindBool
		b, ok := v.(bool)
		if !ok {
			w.fail(fmt.Errorf("velox: %s: a JSON boolean value is a bool, not a %T", w.in.Name, v))
			return
		}
		leaf = strconv.FormatBool(b)
	}
	var b strings.Builder
	b.Grow(len(leaf) + 6*len(p.Keys))
	for _, k := range p.Keys {
		// check refused a key holding a quote, a backslash or a control
		// character, so the rest is written as it is.
		if !utf8.ValidString(k) {
			w.fail(fmt.Errorf("velox: %s: JSON path key %q is not valid UTF-8", w.in.Name, k))
			return
		}
		b.WriteString(`{"`)
		b.WriteString(k)
		b.WriteString(`":`)
	}
	b.WriteString(leaf)
	for range p.Keys {
		b.WriteByte('}')
	}
	w.argOf(col, b.String())
	w.str("::jsonb")
}

// jsonValue writes the value at path p in column i, by its kind. Every
// key, or MySQL's and SQLite's whole path, is an argument. The forms read
// alike on every dialect: a string is compared as written, with case.
func (w *writer) jsonValue(i int32, p JSONPath) {
	switch p.Kind {
	case JSONKindLen:
		w.jsonLen(i, p)
		return
	case JSONKindTextNumber, JSONKindTextTime:
		w.jsonParsed(i, p)
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
		case JSONKindNull:
			w.str("CASE WHEN jsonb_typeof(")
			call("jsonb_extract_path")
			w.str(") = 'null' THEN 1 END")
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
		case JSONKindNull:
			w.str("CASE WHEN JSON_TYPE(")
			extract()
			w.str(") = 'NULL' THEN 1 END")
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
		case JSONKindNull:
			w.str("CASE WHEN ")
			call("json_type")
			w.str(" = 'null' THEN 1 END")
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
