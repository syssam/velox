package engine

import (
	"strconv"
	"strings"
)

// valid reports whether d is Postgres, MySQL or SQLite.
func validDialect(d Dialect) bool { return d == Postgres || d == MySQL || d == SQLite }

// quote writes ident quoted, with the quote character doubled inside it.
func quote(d Dialect, b *strings.Builder, ident string) {
	q := byte('"')
	if d == MySQL {
		q = '`'
	}
	b.WriteByte(q)
	for i := range len(ident) {
		if ident[i] == q {
			b.WriteByte(q)
		}
		b.WriteByte(ident[i])
	}
	b.WriteByte(q)
}

// placeholder writes the n-th (from 1) parameter placeholder.
func placeholder(d Dialect, b *strings.Builder, n int) {
	if d == Postgres {
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(n))
		return
	}
	b.WriteByte('?')
}

// noLimit is what goes after LIMIT when a query has an offset and no limit:
// MySQL and SQLite accept OFFSET only after LIMIT.
func noLimit(d Dialect) string {
	if d == MySQL {
		return "18446744073709551615"
	}
	return "-1"
}

// QuoteIdent writes the identifier s to b, quoted for d.
func QuoteIdent(d Dialect, b *strings.Builder, s string) { quote(d, b, s) }
