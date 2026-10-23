package ddl

import (
	"strings"

	"github.com/syssam/velox2"
)

// pgAliases maps PostgreSQL's aliases of built-in types to the names it
// prints.
var pgAliases = map[string]string{
	"varchar":     "character varying",
	"char":        "character",
	"int4":        "integer",
	"int8":        "bigint",
	"float8":      "double precision",
	"bool":        "boolean",
	"timestamptz": "timestamp with time zone",
	"decimal":     "numeric",
	"int":         "integer",
}

// mysqlInts are MySQL's integer types, whose display width it no longer
// prints.
var mysqlInts = map[string]bool{"tinyint": true, "smallint": true, "mediumint": true, "int": true, "integer": true, "bigint": true}

// NormalizeType is t in the dialect's canonical spelling: lower case, white
// space collapsed, but quoted text kept as written; PostgreSQL's aliases varchar, char, int4, int8, float8,
// bool, timestamptz, decimal and int mapped to character varying, character,
// integer, bigint, double precision, boolean, timestamp with time zone,
// numeric and integer (with a length or precision kept); MySQL's integer
// display widths dropped, but for tinyint(1).
func NormalizeType(d velox.Dialect, t string) string {
	t = collapse(d, t)
	word, rest := t, ""
	if i := strings.IndexFunc(t, notWord); i >= 0 {
		word, rest = t[:i], t[i:]
	}
	switch d {
	case velox.Postgres:
		alias, ok := pgAliases[word]
		if !ok {
			return t
		}
		if word == "timestamptz" && strings.HasPrefix(rest, "(") {
			// PostgreSQL prints the precision after timestamp.
			if j := strings.IndexByte(rest, ')'); j >= 0 {
				return "timestamp" + rest[:j+1] + " with time zone" + rest[j+1:]
			}
		}
		return alias + rest
	case velox.MySQL:
		if mysqlInts[word] && strings.HasPrefix(rest, "(") && !strings.HasPrefix(t, "tinyint(1)") {
			if j := strings.IndexByte(rest, ')'); j >= 0 {
				return word + rest[j+1:]
			}
		}
	}
	return t
}

// notWord reports whether r ends a type's leading word.
func notWord(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' }

// collapse is t in lower case, with runs of white space made one space, none
// at either end, and none around parentheses and commas. Quoted text, '…'
// and "…" and on MySQL `…`, keeps its bytes.
func collapse(d velox.Dialect, t string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(t); {
		c := t[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' {
			space = b.Len() > 0
			i++
			continue
		}
		if space && !strings.ContainsRune("(),", rune(c)) {
			if last := b.String()[b.Len()-1]; last != '(' && last != ',' {
				b.WriteByte(' ')
			}
		}
		space = false
		if c == '\'' || c == '"' || c == '`' && d == velox.MySQL {
			j := quoteEnd(d, t, i)
			b.WriteString(t[i:j])
			i = j
			continue
		}
		j := i + 1
		for j < len(t) && !strings.ContainsRune(" \t\n\r\f\v'\"`", rune(t[j])) {
			j++
		}
		b.WriteString(strings.ToLower(t[i:j]))
		i = j
	}
	return b.String()
}

// quoteEnd is the index just past the quoted text that starts at t[i]: a
// doubled quote stays inside it, and so on MySQL does a character after a
// backslash in '…'. It is len(t) when the quote is not closed.
func quoteEnd(d velox.Dialect, t string, i int) int {
	q := t[i]
	for j := i + 1; j < len(t); j++ {
		switch {
		case t[j] == '\\' && q == '\'' && d == velox.MySQL:
			j++
		case t[j] == q:
			if j+1 < len(t) && t[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(t)
}

// StringType reports whether t, normalised, is a built-in string type:
// text, character varying[(n)] and character(n) on PostgreSQL; varchar(n),
// char(n), tinytext, text, mediumtext and longtext on MySQL; text on SQLite.
func StringType(d velox.Dialect, t string) bool {
	t = NormalizeType(d, t)
	switch d {
	case velox.Postgres:
		return t == "text" || t == "character varying" || sized(t, "character varying") || sized(t, "character")
	case velox.MySQL:
		switch t {
		case "tinytext", "text", "mediumtext", "longtext":
			return true
		}
		return sized(t, "varchar") || sized(t, "char")
	case velox.SQLite:
		return t == "text"
	}
	return false
}

// sized reports whether t is base(n), for a number n.
func sized(t, base string) bool {
	n, ok := strings.CutPrefix(t, base+"(")
	if !ok {
		return false
	}
	n, ok = strings.CutSuffix(n, ")")
	if !ok || n == "" {
		return false
	}
	for _, r := range n {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
