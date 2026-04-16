package sqljson

import (
	"testing"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
)

// FuzzValueEQ tests that ValueEQ never panics with arbitrary inputs.
func FuzzValueEQ(f *testing.F) {
	f.Add("data", "value", "key")
	f.Add("", "", "")
	f.Add("col", "val", "nested.path")

	f.Fuzz(func(t *testing.T, col, val, path string) {
		for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
			s := sql.Dialect(d).Select("*").From(sql.Table("t"))
			p := ValueEQ(col, val, Path(path))
			s.Where(p)
			query, _ := s.Query()
			if query == "" {
				t.Error("empty query from ValueEQ")
			}
		}
	})
}

// FuzzHasKey tests that HasKey never panics.
func FuzzHasKey(f *testing.F) {
	f.Add("data", "key")
	f.Add("", "")
	f.Add("col", "deeply.nested.key")

	f.Fuzz(func(t *testing.T, col, key string) {
		for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
			s := sql.Dialect(d).Select("*").From(sql.Table("t"))
			p := HasKey(col, DotPath(key))
			s.Where(p)
			query, _ := s.Query()
			if query == "" {
				t.Error("empty query from HasKey")
			}
		}
	})
}

// FuzzStringContains tests that StringContains never panics.
func FuzzStringContains(f *testing.F) {
	f.Add("data", "substr", "key")
	f.Add("", "", "")
	f.Add("col", "%special%", "path")

	f.Fuzz(func(t *testing.T, col, substr, path string) {
		for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
			s := sql.Dialect(d).Select("*").From(sql.Table("t"))
			p := StringContains(col, substr, Path(path))
			s.Where(p)
			query, _ := s.Query()
			if query == "" {
				t.Error("empty query from StringContains")
			}
		}
	})
}
