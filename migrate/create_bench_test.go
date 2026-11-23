package migrate_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/benchschema"
	"github.com/syssam/velox/migrate"
)

// BenchmarkCreateSQLite is Create of bench/scale's 500 tables, plain and
// wide (benchschema.Tables), on a new SQLite database each time: a file of
// default settings, and one in memory in a pool of one connection, as
// velox.Connect opens both. Run it with -benchtime=5x; -cpuprofile shows
// where the time goes.
func BenchmarkCreateSQLite(b *testing.B) {
	for _, wide := range []bool{false, true} {
		model := benchschema.Tables(500, wide)
		shape := "plain"
		if wide {
			shape = "wide"
		}
		for _, c := range []struct {
			name string
			dsn  func(b *testing.B, i int) string
		}{
			{"file", func(b *testing.B, _ int) string { return filepath.Join(b.TempDir(), "velox.db") }},
			{"memory", func(_ *testing.B, i int) string { return fmt.Sprintf("file:bench%d?mode=memory&cache=shared", i) }},
		} {
			b.Run(shape+"/"+c.name, func(b *testing.B) {
				i := 0
				for b.Loop() {
					i++
					vdb, err := velox.Connect("sqlite", c.dsn(b, i))
					if err != nil {
						b.Fatal(err)
					}
					db := velox.Pool(vdb)
					if err := migrate.Create(context.Background(), db, model); err != nil {
						b.Fatal(err)
					}
					var n int
					if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&n); err != nil || n != 500 {
						b.Fatalf("%d tables, %v", n, err)
					}
					_ = vdb.Close()
				}
			})
		}
	}
}
