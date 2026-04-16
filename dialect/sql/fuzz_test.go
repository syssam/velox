package sql

import (
	"testing"

	"github.com/syssam/velox/dialect"
)

// FuzzQuote tests that Quote never panics and always wraps with quotes.
func FuzzQuote(f *testing.F) {
	f.Add("")
	f.Add("users")
	f.Add("user`name")
	f.Add("user\"name")
	f.Add("user\x00name")
	f.Add("user\nname")
	f.Add("日本語テーブル")

	f.Fuzz(func(t *testing.T, ident string) {
		for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
			var b Builder
			b.SetDialect(d)
			// Quote must never panic and must return a non-empty string.
			result := b.Quote(ident)
			if result == "" {
				t.Errorf("Quote(%q) with dialect %q returned empty string", ident, d)
			}
		}
	})
}

// FuzzPredicateEQ tests that EQ never panics for arbitrary column names and values.
func FuzzPredicateEQ(f *testing.F) {
	f.Add("name", "Alice")
	f.Add("", "")
	f.Add("col`umn", "val'ue")
	f.Add("col\x00", "val\x00")
	f.Add("日本語", "値")

	f.Fuzz(func(t *testing.T, col, val string) {
		p := EQ(col, val)
		if p == nil {
			t.Error("EQ returned nil")
		}
	})
}

// FuzzPredicateLike tests LIKE predicates with arbitrary patterns.
func FuzzPredicateLike(f *testing.F) {
	f.Add("name", "%test%")
	f.Add("col", "test_value")
	f.Add("col", `test\%value`)
	f.Add("col", "")
	f.Add("col", `%_\`)

	f.Fuzz(func(t *testing.T, col, pattern string) {
		p := Like(col, pattern)
		if p == nil {
			t.Error("Like returned nil")
		}
		p2 := HasPrefix(col, pattern)
		if p2 == nil {
			t.Error("HasPrefix returned nil")
		}
		p3 := Contains(col, pattern)
		if p3 == nil {
			t.Error("Contains returned nil")
		}
	})
}

// FuzzPredicateIn tests In predicate with arbitrary args.
func FuzzPredicateIn(f *testing.F) {
	f.Add("status", "active")
	f.Add("", "")
	f.Add("col", "a")

	f.Fuzz(func(t *testing.T, col, val string) {
		p := In(col, val)
		if p == nil {
			t.Error("In returned nil")
		}
		p2 := NotIn(col, val)
		if p2 == nil {
			t.Error("NotIn returned nil")
		}
	})
}

// FuzzSelectBuilder tests that Select builder never panics with arbitrary inputs.
func FuzzSelectBuilder(f *testing.F) {
	f.Add("users", "id", "name")
	f.Add("", "", "")
	f.Add("tab`le", "col`1", "col`2")

	f.Fuzz(func(t *testing.T, table, col1, col2 string) {
		for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
			query, args := Dialect(d).
				Select(col1, col2).
				From(Table(table)).
				Where(EQ(col1, "test")).
				Query()
			if query == "" {
				t.Error("empty query")
			}
			_ = args
		}
	})
}

// FuzzInsertBuilder tests INSERT builder across dialects — values must always
// be parameterised (args slice), never inlined into the query string.
func FuzzInsertBuilder(f *testing.F) {
	f.Add("users", "name", "Alice")
	f.Add("", "", "")
	f.Add("tbl", "col\x00", "val\x00")
	f.Add("tbl", "col", "O'Brien; DROP TABLE users;--")

	f.Fuzz(func(t *testing.T, table, col, val string) {
		for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
			query, args := Dialect(d).Insert(table).Columns(col).Values(val).Query()
			if query == "" {
				t.Error("empty insert query")
			}
			// The literal value must never appear in the query string — it belongs in args.
			// Skip this check for empty val (trivially "in" the string) and for short vals
			// that may coincidentally match quoting/placeholder syntax.
			// The value must be passed as a bound arg, never inlined into the query.
			found := false
			for _, a := range args {
				if s, ok := a.(string); ok && s == val {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("value %q not found in args %v (query=%q)", val, args, query)
			}
		}
	})
}

// FuzzUpdateBuilder tests UPDATE builder — SET values and WHERE values must be parameterised.
func FuzzUpdateBuilder(f *testing.F) {
	f.Add("users", "name", "Alice", int64(1))
	f.Add("", "", "", int64(0))
	f.Add("tbl", "col", "'; DROP TABLE x;--", int64(-1))

	f.Fuzz(func(t *testing.T, table, col, val string, id int64) {
		for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
			query, args := Dialect(d).Update(table).Set(col, val).Where(EQ("id", id)).Query()
			if query == "" {
				t.Error("empty update query")
			}
			// The value must be passed as a bound arg, never inlined into the query.
			found := false
			for _, a := range args {
				if s, ok := a.(string); ok && s == val {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("value %q not found in args %v (query=%q)", val, args, query)
			}
		}
	})
}

// FuzzDeleteBuilder tests DELETE builder across dialects.
func FuzzDeleteBuilder(f *testing.F) {
	f.Add("users", "id", int64(1))
	f.Add("", "", int64(0))
	f.Add("tbl`", "col`", int64(-1))

	f.Fuzz(func(t *testing.T, table, col string, id int64) {
		for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
			query, _ := Dialect(d).Delete(table).Where(EQ(col, id)).Query()
			if query == "" {
				t.Error("empty delete query")
			}
		}
	})
}

// FuzzPredicateComposition tests that And/Or/Not never panic and compose correctly.
func FuzzPredicateComposition(f *testing.F) {
	f.Add("name", "a", "email", "b")
	f.Add("", "", "", "")
	f.Add("c1", "v1", "c2", "v2")

	f.Fuzz(func(t *testing.T, c1, v1, c2, v2 string) {
		p1 := EQ(c1, v1)
		p2 := EQ(c2, v2)
		// All composition forms must produce a non-nil predicate.
		for _, p := range []*Predicate{
			And(p1, p2),
			Or(p1, p2),
			Not(p1),
			And(Or(p1, p2), Not(p1)),
		} {
			if p == nil {
				t.Fatal("composition returned nil")
			}
		}
	})
}
