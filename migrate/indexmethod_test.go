package migrate_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// methodSchema is one table with a jsonb column and a name, and the indexes.
func methodSchema(d velox.Dialect, ixs ...migrate.Index) *migrate.Schema {
	return &migrate.Schema{Dialect: d, Tables: []migrate.Table{{
		Name: "docs",
		Columns: []migrate.Column{
			{Name: "id", Type: "bigint"},
			{Name: "attrs", Type: "jsonb", Nullable: true},
			{Name: "name", Type: "text"},
		},
		PrimaryKey: []string{"id"}, PrimaryKeyName: "docs_pkey",
		Indexes: ixs,
	}}}
}

func renderSQL(t *testing.T, from, to *migrate.Schema) []string {
	t.Helper()
	cs, err := migrate.Diff(from, to, migrate.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	scripts, err := migrate.Render(from, to, cs, migrate.RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range scripts {
		for _, st := range s.Statements {
			out = append(out, st.SQL)
		}
	}
	return out
}

// An index with a method renders USING, is created on its own, and a change
// of method is a drop and a create: PostgreSQL cannot alter it.
func TestIndexMethodRender(t *testing.T) {
	t.Parallel()
	gin := migrate.Index{Name: "docs_attrs_gin", Columns: []string{"attrs"}, Method: "gin"}
	got := renderSQL(t, methodSchema(velox.Postgres), methodSchema(velox.Postgres, gin))
	// An index of a table that exists is built CONCURRENTLY.
	if !slices.Contains(got, `CREATE INDEX CONCURRENTLY "docs_attrs_gin" ON "docs" USING gin ("attrs")`) {
		t.Errorf("add: %q", got)
	}
	// Created with its table.
	got = renderSQL(t, &migrate.Schema{Dialect: velox.Postgres}, methodSchema(velox.Postgres, gin))
	if !slices.Contains(got, `CREATE INDEX "docs_attrs_gin" ON "docs" USING gin ("attrs")`) {
		t.Errorf("create table: %q", got)
	}
	// Partial and hash.
	hash := migrate.Index{Name: "docs_name_hash", Columns: []string{"name"}, Method: "hash", Where: "name <> ''"}
	got = renderSQL(t, methodSchema(velox.Postgres), methodSchema(velox.Postgres, hash))
	if !slices.Contains(got, `CREATE INDEX CONCURRENTLY "docs_name_hash" ON "docs" USING hash ("name") WHERE name <> ''`) {
		t.Errorf("partial hash: %q", got)
	}
	// Same method, no change; another method, a new index built beside the old
	// one, the old one dropped, and the new one renamed.
	if got := renderSQL(t, methodSchema(velox.Postgres, gin), methodSchema(velox.Postgres, gin)); len(got) != 0 {
		t.Errorf("same: %q", got)
	}
	gist := gin
	gist.Method = "gist"
	got = renderSQL(t, methodSchema(velox.Postgres, gin), methodSchema(velox.Postgres, gist))
	if all := strings.Join(got, "\n"); !strings.Contains(all, `USING gist ("attrs")`) || strings.Contains(all, `USING gin`) ||
		!strings.Contains(all, `DROP INDEX CONCURRENTLY IF EXISTS "docs_attrs_gin"`) || !strings.Contains(all, `RENAME TO "docs_attrs_gin"`) {
		t.Errorf("change: %q", got)
	}
	// A btree index, written as none, is not a change from one that was.
	plain := migrate.Index{Name: "docs_name", Columns: []string{"name"}}
	if got := renderSQL(t, methodSchema(velox.Postgres, plain), methodSchema(velox.Postgres, plain)); len(got) != 0 {
		t.Errorf("btree: %q", got)
	}
}

// A schema refuses a method outside PostgreSQL, a name PostgreSQL has not,
// btree written out, and a unique index that is not btree.
func TestIndexMethodRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		d      velox.Dialect
		ix     migrate.Index
		reason string
	}{
		{"mysql", velox.MySQL, migrate.Index{Method: "gin"}, `gives the index "k" the method "gin", which only PostgreSQL has`},
		{"sqlite", velox.SQLite, migrate.Index{Method: "hash"}, `gives the index "k" the method "hash", which only PostgreSQL has`},
		{"unknown", velox.Postgres, migrate.Index{Method: "rtree"}, `gives the index "k" the method "rtree"; the methods are hash, gin, gist, spgist and brin, and btree is the default, written as none`},
		{"btree", velox.Postgres, migrate.Index{Method: "btree"}, `gives the index "k" the method "btree"`},
		{"unique", velox.Postgres, migrate.Index{Method: "hash", Unique: true}, `gives the unique index "k" the method "hash", and only btree can enforce uniqueness`},
	} {
		c.ix.Name, c.ix.Columns = "k", []string{"name"}
		_, err := migrate.Diff(&migrate.Schema{Dialect: c.d}, methodSchema(c.d, c.ix), migrate.DiffOptions{})
		e, ok := err.(*migrate.Error)
		if !ok || e.Code != "schema" || !strings.Contains(e.Reason, c.reason) {
			t.Errorf("%s: %v, want a schema error with %q", c.name, err, c.reason)
		}
	}
}
