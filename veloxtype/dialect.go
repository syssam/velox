package veloxtype

import "strconv"

// Dialect is a SQL dialect.
type Dialect uint8

// The dialects velox supports.
const (
	Postgres Dialect = iota + 1
	MySQL
	SQLite
)

// String is "postgres", "mysql" or "sqlite"; "Dialect(<n>)" for any other
// value.
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
