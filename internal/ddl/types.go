package ddl

import (
	"strings"

	"github.com/syssam/velox"
)

// pgAliases maps PostgreSQL's aliases of built-in types to the names it
// prints.
var pgAliases = map[string]string{
	"varchar": "character varying",
	"varbit":  "bit varying",
	"char":    "character",
	"int2":    "smallint",
	"int4":    "integer",
	"int8":    "bigint",
	"float4":  "real",
	"float8":  "double precision",
	"float":   "double precision",
	"bool":    "boolean",
	"decimal": "numeric",
	"int":     "integer",
}

// pgClocks are PostgreSQL's time types as it prints them: a name, the precision
// after it, then the zone.
var pgClocks = map[string][2]string{
	"timestamp":   {"timestamp", " without time zone"},
	"time":        {"time", " without time zone"},
	"timestamptz": {"timestamp", " with time zone"},
	"timetz":      {"time", " with time zone"},
}

// mysqlAliases are MySQL's aliases of built-in types, to the names it prints.
var mysqlAliases = map[string]string{
	"integer": "int",
	"numeric": "decimal",
	"dec":     "decimal",
	"fixed":   "decimal",
	"real":    "double",
}

// mysqlInts are MySQL's integer types, whose display width it no longer
// prints.
var mysqlInts = map[string]bool{"tinyint": true, "smallint": true, "mediumint": true, "int": true, "integer": true, "bigint": true}

// NormalizeType is t in the one form the dialect's server prints, so that what a
// schema states and what the server reports compare: lower case and white space
// collapsed, but quoted text kept as written. On PostgreSQL an alias is its
// printed name (varchar, varbit, char, int2, int4, int8, float, float4, float8,
// bool, decimal, int, with a length or precision kept), a bare char is
// character(1), and a time type says its zone after its precision: timestamp(6)
// is timestamp(6) without time zone, timestamptz timestamp with time zone. On
// MySQL an alias is its printed name (integer, numeric, dec, fixed, real, bool,
// boolean, double precision, character varying, character), an integer loses its
// display width but for tinyint(1), a bare decimal is decimal(10,0), a bare char,
// binary or bit has its (1), and a time type loses a precision of 0.
func NormalizeType(d velox.Dialect, t string) string {
	t = collapse(d, t)
	word, rest := t, ""
	if i := strings.IndexFunc(t, notWord); i >= 0 {
		word, rest = t[:i], t[i:]
	}
	switch d {
	case velox.Postgres:
		return pgType(t, word, rest)
	case velox.MySQL:
		return mysqlType(t, word, rest)
	}
	return t
}

// pgType is NormalizeType on PostgreSQL of t, its leading word and the rest.
func pgType(t, word, rest string) string {
	if clock, ok := pgClocks[word]; ok {
		precision := ""
		if strings.HasPrefix(rest, "(") {
			j := strings.IndexByte(rest, ')')
			if j < 0 {
				return t
			}
			precision, rest = rest[:j+1], rest[j+1:]
		}
		if strings.HasPrefix(rest, " with") { // said already
			return t
		}
		return clock[0] + precision + clock[1] + rest
	}
	if (word == "char" || word == "character") && (rest == "" || strings.HasPrefix(rest, "[")) {
		return "character(1)" + rest
	}
	if alias, ok := pgAliases[word]; ok {
		return alias + rest
	}
	return t
}

// mysqlType is NormalizeType on MySQL of t, its leading word and the rest.
func mysqlType(t, word, rest string) string {
	switch word {
	case "bool", "boolean":
		return "tinyint(1)" + rest
	case "double":
		return "double" + strings.TrimPrefix(rest, " precision")
	case "character":
		word = "char"
		if r, ok := strings.CutPrefix(rest, " varying"); ok {
			word, rest = "varchar", r
		}
	default:
		if alias, ok := mysqlAliases[word]; ok {
			word = alias
		}
	}
	switch {
	case mysqlInts[word] && strings.HasPrefix(rest, "(") && !strings.HasPrefix(t, "tinyint(1)"):
		if j := strings.IndexByte(rest, ')'); j >= 0 {
			return word + rest[j+1:]
		}
	case word == "decimal" && !strings.HasPrefix(rest, "("):
		return "decimal(10,0)" + rest
	case rest == "" && (word == "char" || word == "binary" || word == "bit"):
		return word + "(1)"
	case (word == "datetime" || word == "timestamp" || word == "time") && strings.HasPrefix(rest, "(0)"):
		return word + rest[3:]
	}
	return word + rest
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
