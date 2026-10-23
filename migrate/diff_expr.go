package migrate

import (
	"strings"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
)

// renameIdent is the SQL expression expr with the column from renamed to:
// each identifier that names it, quoted or not, outside string literals and
// not a function's name. An unquoted name compares without case, as the
// databases fold it; the new name is unquoted when it needs no quotes.
func renameIdent(d velox.Dialect, expr, from, to string) string {
	if expr == "" || from == to {
		return expr
	}
	var b strings.Builder
	for i := 0; i < len(expr); {
		c := expr[i]
		switch {
		case c == '\'' || c == '"' && d == velox.MySQL: // a string literal
			j := i + 1
			for j < len(expr) {
				if d == velox.MySQL && expr[j] == '\\' {
					j += 2
					continue
				}
				if expr[j] == c {
					if j+1 < len(expr) && expr[j+1] == c {
						j += 2
						continue
					}
					break
				}
				j++
			}
			j = min(j+1, len(expr))
			b.WriteString(expr[i:j])
			i = j
		case c == '"' || c == '`' && d != velox.Postgres: // a quoted identifier
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
			j = min(j+1, len(expr))
			if name.String() == from {
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
			k := j
			for k < len(expr) && expr[k] == ' ' {
				k++
			}
			isCall := k < len(expr) && expr[k] == '('
			switch {
			case c >= '0' && c <= '9', isCall, !strings.EqualFold(word, from):
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

// wordByte reports whether c can be part of an unquoted identifier or number.
func wordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// plainName reports whether name needs no quotes: lower-case letters, digits
// and _, not starting with a digit.
func plainName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := c == '_' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9'
		if !ok {
			return false
		}
	}
	return name != ""
}
