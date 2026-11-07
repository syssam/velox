package migrate

import (
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
)

// renameIdent is the SQL expression expr with the column from renamed to:
// each identifier that names it, quoted or not, outside string literals and
// not a function's name. On PostgreSQL an unquoted name is folded to lower
// case and a quoted one compares exactly; MySQL and SQLite compare both
// without case. The new name is unquoted when it needs no quotes.
//
// It reads identifiers, not the grammar: a keyword spelled like the column,
// such as year in EXTRACT(year FROM …), is renamed too. Diff only compares
// what it returns, so a miss makes a rename a drop and an add, never wrong
// SQL.
func renameIdent(d velox.Dialect, expr, from, to string) string {
	if expr == "" || from == to {
		return expr
	}
	same := func(name string, quoted bool) bool {
		switch {
		case d != velox.Postgres:
			return strings.EqualFold(name, from)
		case quoted:
			return name == from
		}
		return strings.ToLower(name) == from
	}
	var b strings.Builder
	for i := 0; i < len(expr); {
		c := expr[i]
		if j := stringAt(d, expr, i); j > 0 {
			b.WriteString(expr[i:j])
			i = j
			continue
		}
		switch {
		case c == '"' || c == '`' && d != velox.Postgres: // a quoted identifier
			j, name := quotedIdent(expr, i)
			if same(name, true) {
				b.WriteString(ddl.Ident(d, to))
			} else {
				b.WriteString(expr[i:j])
			}
			i = j
		case wordByte(c):
			j := i
			for j < len(expr) && (wordByte(expr[j]) || expr[j] == '$') {
				j++
			}
			word := expr[i:j]
			switch {
			case c >= '0' && c <= '9', callAt(expr, j), !same(word, false):
				b.WriteString(word)
			case plainName(to):
				b.WriteString(to)
			default:
				b.WriteString(ddl.Ident(d, to))
			}
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// stringAt is the index after the string literal that starts at expr[i],
// or 0 when none does: a quoted string, a MySQL double-quoted one, and a
// PostgreSQL escape or dollar-quoted one.
func stringAt(d velox.Dialect, expr string, i int) int {
	c := expr[i]
	switch {
	case c == '\'' || c == '"' && d == velox.MySQL: // a string literal
		return stringEnd(expr, i, d == velox.MySQL)
	case d != velox.Postgres:
		return 0
	case (c == 'E' || c == 'e') && i+1 < len(expr) && expr[i+1] == '\'' &&
		(i == 0 || !wordByte(expr[i-1])): // an escape string
		return stringEnd(expr, i+1, true)
	case c == '$': // a dollar-quoted string
		return dollarEnd(expr, i)
	}
	return 0
}

// quotedIdent reads the identifier quoted by expr[i]'s quote, a doubled
// quote standing for one, and returns the index after it and its name.
func quotedIdent(expr string, i int) (int, string) {
	c := expr[i]
	j, name := i+1, strings.Builder{}
	for j < len(expr) {
		if expr[j] == c {
			if j+1 < len(expr) && expr[j+1] == c {
				name.WriteByte(c)
				j += 2
				continue
			}
			break
		}
		name.WriteByte(expr[j])
		j++
	}
	return min(j+1, len(expr)), name.String()
}

// callAt reports whether a '(' follows expr[j], past spaces: the word that
// ends at j names a function.
func callAt(expr string, j int) bool {
	for j < len(expr) && expr[j] == ' ' {
		j++
	}
	return j < len(expr) && expr[j] == '('
}

// stringEnd is the index after the string literal that starts with the quote
// at expr[i]: a doubled quote stays inside it, and so does a quote after \
// when backslash escapes.
func stringEnd(expr string, i int, backslash bool) int {
	q := expr[i]
	j := i + 1
	for j < len(expr) {
		switch {
		case backslash && expr[j] == '\\':
			j += 2
			continue
		case expr[j] == q && j+1 < len(expr) && expr[j+1] == q:
			j += 2
			continue
		case expr[j] == q:
			return j + 1
		}
		j++
	}
	return len(expr)
}

// dollarEnd is the index after the PostgreSQL $tag$…$tag$ string at expr[i],
// or 0 when there is none.
func dollarEnd(expr string, i int) int {
	j := i + 1
	for j < len(expr) && expr[j] != '$' {
		if !wordByte(expr[j]) || expr[j] >= '0' && expr[j] <= '9' && j == i+1 {
			return 0
		}
		j++
	}
	if j == len(expr) {
		return 0
	}
	tag := expr[i : j+1]
	k := strings.Index(expr[j+1:], tag)
	if k < 0 {
		return 0
	}
	return j + 1 + k + len(tag)
}

// wordByte reports whether c can be part of an unquoted identifier or number.
func wordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// plainName reports whether name needs no quotes: lower-case letters, digits
// and _, not starting with a digit.
func plainName(name string) bool {
	for i := range len(name) {
		c := name[i]
		ok := c == '_' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9'
		if !ok {
			return false
		}
	}
	return name != ""
}

// namesColumn reports whether the SQL expression expr names the column col.
func namesColumn(d velox.Dialect, expr, col string) bool {
	return renameIdent(d, expr, col, col+"_") != expr
}
