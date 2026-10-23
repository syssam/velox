package ddl

import (
	"strings"

	"github.com/syssam/velox2"
)

// Literal is s as a string literal of the dialect: ' doubled, and on MySQL \
// doubled too.
func Literal(d velox.Dialect, s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	if d == velox.MySQL {
		s = strings.ReplaceAll(s, `\`, `\\`)
	}
	return "'" + s + "'"
}

// Unquote is the string a literal of Literal's form holds; false when lit is
// not one.
func Unquote(d velox.Dialect, lit string) (string, bool) {
	if len(lit) < 2 || lit[0] != '\'' || lit[len(lit)-1] != '\'' {
		return "", false
	}
	in := lit[1 : len(lit)-1]
	var b strings.Builder
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c == '\'' || d == velox.MySQL && c == '\\' {
			if i+1 == len(in) || in[i+1] != c {
				return "", false
			}
			i++
		}
		b.WriteByte(c)
	}
	return b.String(), true
}

// Ident is name quoted for the dialect: in backquotes on MySQL, else in
// double quotes, with the quote doubled inside it.
func Ident(d velox.Dialect, name string) string {
	q := `"`
	if d == velox.MySQL {
		q = "`"
	}
	return q + strings.ReplaceAll(name, q, q+q) + q
}
