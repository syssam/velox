package migrate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestPostgresLongNameRefused: PostgreSQL cuts a name over 63 bytes to 63, with
// only a notice, so a hand-built model's long column was applied under another
// name and the database drifted from the snapshot from then on. The plan is
// refused now, naming the column, as codegen refuses such a name.
func TestPostgresLongNameRefused(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	long := strings.Repeat("c", 64)
	to := oneTable(d, migrate.Column{Name: long, Type: "text", Nullable: true})
	_, err := migrate.NewPlan(oneTable(d), to, migrate.PlanOptions{})
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "schema" || !strings.Contains(err.Error(), `the column "`+long+`" of 64 bytes, over PostgreSQL's 63`) {
		t.Errorf("a 64-byte column: %v", err)
		db, _ := freshDB(t, d)
		fsys := dirOf(t, d, step{to: to, name: "long"})
		mustUp(t, db, fsys)
		if got := oneValue(t, db, `SELECT column_name FROM information_schema.columns WHERE table_name = 't' AND column_name <> 'id'`); got != long {
			t.Errorf("the column is %q in the database", got)
		}
	}
	if _, err := migrate.NewPlan(oneTable(d), oneTable(d, migrate.Column{Name: long[:63], Type: "text", Nullable: true}), migrate.PlanOptions{}); err != nil {
		t.Errorf("a 63-byte column: %v", err)
	}
}

// TestLongNamesRefused: each named object of a model is held to the server's
// limit, PostgreSQL's 63 bytes and MySQL's 64 characters; SQLite has none.
func TestLongNamesRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d    velox.Dialect
		name string
		want string
	}{
		{velox.Postgres, strings.Repeat("é", 32), "of 64 bytes, over PostgreSQL's 63"},
		{velox.MySQL, strings.Repeat("é", 65), "of 65 characters, over MySQL's 64"},
	} {
		ok := c.name[:len(c.name)-len("é")]
		for _, set := range []struct {
			what string
			set  func(s *migrate.Schema, n string)
		}{
			{"table", func(s *migrate.Schema, n string) { s.Tables[0].Name = n }},
			{"column", func(s *migrate.Schema, n string) {
				s.Tables[0].Columns = append(s.Tables[0].Columns, migrate.Column{Name: n, Type: "text", Nullable: true})
			}},
			{"index", func(s *migrate.Schema, n string) {
				s.Tables[0].Indexes = append(s.Tables[0].Indexes, migrate.Index{Name: n, Columns: []string{"id"}})
			}},
			{"constraint", func(s *migrate.Schema, n string) {
				s.Tables[0].Checks = append(s.Tables[0].Checks, migrate.Check{Name: n, Expr: "id > 0"})
			}},
			{"primary key", func(s *migrate.Schema, n string) {
				if c.d == velox.Postgres {
					s.Tables[0].PrimaryKeyName = n
				}
			}},
		} {
			long, fine := oneTable(c.d), oneTable(c.d)
			set.set(long, c.name)
			set.set(fine, ok)
			if _, err := migrate.Diff(oneTable(c.d), fine, migrate.DiffOptions{}); err != nil {
				t.Errorf("%s: a %s at the limit: %v", c.d, set.what, err)
			}
			if set.what == "primary key" && c.d != velox.Postgres {
				continue
			}
			_, err := migrate.Diff(oneTable(c.d), long, migrate.DiffOptions{})
			if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "schema" || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: a %s over the limit: %v", c.d, set.what, err)
			}
		}
	}
	long := oneTable(velox.SQLite, migrate.Column{Name: strings.Repeat("c", 100), Type: "text", Nullable: true})
	if _, err := migrate.Diff(oneTable(velox.SQLite), long, migrate.DiffOptions{}); err != nil {
		t.Errorf("SQLite: %v", err)
	}
}
