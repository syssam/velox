package migrate_test

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// quoteName is name quoted for d.
func quoteName(d velox.Dialect, name string) string {
	if d == velox.MySQL {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// tableName is ref quoted for d, qualified when it has a schema.
func tableName(d velox.Dialect, ref migrate.TableRef) string {
	if ref.Schema != "" {
		return quoteName(d, ref.Schema) + "." + quoteName(d, ref.Name)
	}
	return quoteName(d, ref.Name)
}

// insertRows puts one row in each table of s on d, with foreign keys off
// while it does (PostgreSQL: session_replication_role = replica; MySQL:
// FOREIGN_KEY_CHECKS = 0; SQLite: the pragma), each column a value its type
// and checks take that reads the same as text in every type the case gives
// the column (rowValue).
func insertRows(t *testing.T, db *sql.DB, d velox.Dialect, s *migrate.Schema) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	exec := func(q string) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	fk := map[velox.Dialect][2]string{
		velox.Postgres: {"SET session_replication_role = replica", "RESET session_replication_role"},
		velox.MySQL:    {"SET FOREIGN_KEY_CHECKS = 0", "SET FOREIGN_KEY_CHECKS = 1"},
		velox.SQLite:   {"PRAGMA foreign_keys = OFF", "PRAGMA foreign_keys = ON"},
	}[d]
	exec(fk[0])
	for _, tb := range s.Tables {
		var cols, vals []string
		for _, c := range tb.Columns {
			cols = append(cols, quoteName(d, c.Name))
			vals = append(vals, rowValue(d, s, &tb, c))
		}
		exec(`INSERT INTO ` + tableName(d, tb.Ref()) + ` (` + strings.Join(cols, ", ") + `) VALUES (` + strings.Join(vals, ", ") + `)`)
	}
	exec(fk[1])
	if d == velox.SQLite {
		if hasRows(t, conn, "PRAGMA foreign_key_check") {
			t.Fatal("the rows break a foreign key")
		}
	}
}

// integerType reports whether typ, a column type of any dialect, is an
// integer.
func integerType(typ string) bool {
	typ, _, _ = strings.Cut(typ, "(")
	switch strings.TrimSuffix(typ, " unsigned") {
	case "integer", "int", "bigint", "smallint", "tinyint", "mediumint":
		return true
	}
	return false
}

// rowValue is a value for column c of tb on d: 1 for a key, its default,
// the first value a check or an enum allows, or one of the column's type.
// Each reads as the same text in every type a case gives the column.
func rowValue(d velox.Dialect, s *migrate.Schema, tb *migrate.Table, c migrate.Column) string {
	if slices.Contains(tb.PrimaryKey, c.Name) || slices.ContainsFunc(tb.ForeignKeys, func(fk migrate.ForeignKey) bool { return slices.Contains(fk.Columns, c.Name) }) {
		if integerType(c.Type) {
			return "1"
		}
		return "'1'"
	}
	if c.Default != "" {
		return c.Default
	}
	for _, ck := range tb.Checks {
		if list, ok := strings.CutPrefix(ck.Expr, `"`+c.Name+`" IN (`); ok {
			v, _, _ := strings.Cut(list, ",")
			return strings.TrimSuffix(v, ")")
		}
	}
	if list, ok := strings.CutPrefix(c.Type, "enum("); ok {
		v, _, _ := strings.Cut(list, ",")
		return strings.TrimSuffix(v, ")")
	}
	for _, e := range s.Enums {
		if c.Type == `"`+e.Name+`"` || c.Type == `"`+e.Schema+`"."`+e.Name+`"` {
			return "'" + strings.ReplaceAll(e.Values[0], "'", "''") + "'"
		}
	}
	typ, _, _ := strings.Cut(c.Type, "(")
	switch {
	case integerType(c.Type):
		return "1"
	case typ == "boolean":
		if d == velox.Postgres {
			return "true"
		}
		return "1"
	case typ == "real", typ == "double precision", typ == "double", typ == "float", typ == "numeric", typ == "decimal":
		return "1.5"
	case typ == "date":
		return "'2026-01-01'"
	case strings.HasPrefix(typ, "timestamp"), typ == "datetime":
		return "'2026-01-01 00:00:00'"
	case typ == "json", typ == "jsonb":
		return "'{}'"
	case typ == "uuid":
		return "'00000000-0000-0000-0000-000000000001'"
	}
	return "'x'"
}

// textOf is column c read as text on d: a SQLite real as %g, so the
// integer it was read before a rebuild reads the same.
func textOf(d velox.Dialect, c migrate.Column) string {
	q := quoteName(d, c.Name)
	switch d {
	case velox.Postgres:
		return q + "::text"
	case velox.MySQL:
		return "CAST(" + q + " AS CHAR)"
	}
	if c.Type == "real" {
		return "printf('%g', " + q + ")"
	}
	return "CAST(" + q + " AS TEXT)"
}

// hasRows reports whether query has a row on db, a transaction or a
// connection.
func hasRows(t testing.TB, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string) bool {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	has := rows.Next()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return has
}

// queryColumn is the first column of each row that query returns on db, as
// T.
func queryColumn[T any](t testing.TB, db interface {
	Query(string, ...any) (*sql.Rows, error)
}, query string, args ...any) []T {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	var out []T
	for rows.Next() {
		var v T
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// scanRows runs q, which selects n columns, and returns the last row's
// values and the number of rows.
func scanRows(t testing.TB, db *sql.DB, q string, n int) ([]sql.NullString, int) {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	vals := make([]sql.NullString, n)
	ptrs := make([]any, n)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	count := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return vals, count
}

// rowsOf is each table's row as text by column, cast per dialect.
func rowsOf(t *testing.T, db *sql.DB, d velox.Dialect, s *migrate.Schema) map[migrate.TableRef]map[string]string {
	t.Helper()
	out := map[migrate.TableRef]map[string]string{}
	for _, tb := range s.Tables {
		var cols []string
		for _, c := range tb.Columns {
			cols = append(cols, textOf(d, c))
		}
		q := "SELECT " + strings.Join(cols, ", ") + " FROM " + tableName(d, tb.Ref())
		vals, n := scanRows(t, db, q, len(tb.Columns))
		if n != 1 {
			t.Errorf("table %s has %d rows; want 1", tb.Ref(), n)
			continue
		}
		row := map[string]string{}
		for i, c := range tb.Columns {
			row[c.Name] = vals[i].String
			if !vals[i].Valid {
				row[c.Name] = "NULL"
			}
		}
		out[tb.Ref()] = row
	}
	return out
}
