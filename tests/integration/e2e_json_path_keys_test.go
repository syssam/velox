package integration_test

import (
	stdsql "database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sql/sqljson"
)

// TestMultiDialect_JSONPathKeys pins that a JSON path key reaches the
// database as written, whatever it contains. Keys were written raw into
// the MySQL/SQLite path literal ('$."key"'): a quote ended the literal and
// the rest of the key ran as SQL, a double quote or backslash ended or
// escaped the quoted key, and PostgreSQL's text[] path split a key on a
// comma. Each key is stored in a document and must be found by ValueEQ
// and HasKey, on every dialect — MySQL additionally reads backslash
// escapes in string literals.
func TestMultiDialect_JSONPathKeys(t *testing.T) {
	keys := []string{`a'b`, `a"b`, `a\b`, `a\\b`, ``, `a b`, `NULL`, `ü`, `a,b`, `a{b}`, `a.b`, `it' OR '1'='1`}
	type target struct{ name, driver, dsn, typ string }
	targets := []target{{dialect.SQLite, "sqlite", ":memory:", "TEXT"}}
	if dsn := os.Getenv("VELOX_TEST_POSTGRES"); dsn != "" {
		targets = append(targets, target{dialect.Postgres, "postgres", dsn, "JSONB"})
	}
	if dsn := os.Getenv("VELOX_TEST_MYSQL"); dsn != "" {
		targets = append(targets, target{dialect.MySQL, "mysql", dsn, "JSON"})
	}
	for _, tg := range targets {
		t.Run(tg.name, func(t *testing.T) {
			db, err := stdsql.Open(tg.driver, tg.dsn)
			require.NoError(t, err)
			defer db.Close()
			_, _ = db.Exec(`DROP TABLE IF EXISTS json_path_keys`)
			_, err = db.Exec(`CREATE TABLE json_path_keys (id INT, doc ` + tg.typ + `)`)
			require.NoError(t, err)
			defer db.Exec(`DROP TABLE IF EXISTS json_path_keys`) //nolint:errcheck
			for i, k := range keys {
				doc, err := json.Marshal(map[string]any{k: map[string]any{"v": i}})
				require.NoError(t, err)
				insert := sql.Dialect(tg.name).Insert("json_path_keys").Columns("id", "doc").Values(i, string(doc))
				q, args := insert.Query()
				_, err = db.Exec(q, args...)
				require.NoError(t, err)
			}
			for i, k := range keys {
				for name, p := range map[string]*sql.Predicate{
					"ValueEQ": sqljson.ValueEQ("doc", i, sqljson.Path(k, "v")),
					"HasKey":  sqljson.HasKey("doc", sqljson.Path(k)),
				} {
					q, args := sql.Dialect(tg.name).Select("id").From(sql.Table("json_path_keys")).Where(p).Query()
					rows, err := db.Query(q, args...)
					require.NoError(t, err, "%s(%q): %s", name, k, q)
					var got []int
					for rows.Next() {
						var id int
						require.NoError(t, rows.Scan(&id))
						got = append(got, id)
					}
					require.NoError(t, rows.Close())
					require.Equal(t, []int{i}, got, fmt.Sprintf("%s(%q): %s", name, k, q))
				}
			}
		})
	}
}
