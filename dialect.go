package velox

import (
	"strconv"
	"strings"
)

// Dialect is a SQL dialect.
type Dialect uint8

// The dialects velox supports.
const (
	Postgres Dialect = iota + 1
	MySQL
	SQLite
)

func (d Dialect) String() string {
	switch d {
	case Postgres:
		return "postgres"
	case MySQL:
		return "mysql"
	case SQLite:
		return "sqlite"
	}
	return "Dialect(" + strconv.Itoa(int(d)) + ")"
}

// quote writes ident quoted, with the quote character doubled inside it.
func (d Dialect) quote(b *strings.Builder, ident string) {
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
func (d Dialect) placeholder(b *strings.Builder, n int) {
	if d == Postgres {
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(n))
		return
	}
	b.WriteByte('?')
}

// noLimit is what goes after LIMIT when a query has an offset and no limit:
// MySQL and SQLite accept OFFSET only after LIMIT.
func (d Dialect) noLimit() string {
	if d == MySQL {
		return "18446744073709551615"
	}
	return "-1"
}
