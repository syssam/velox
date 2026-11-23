package migrate_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// typelessSchema is a SQLite table with two columns of type price, "" for
// typeless: SQLite gives such a column BLOB affinity. It is a decimal field
// with no SQLite SchemaType (decision 0145).
func typelessSchema(price string) *migrate.Schema {
	return &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{{
		Name: "t",
		Columns: []migrate.Column{
			{Name: "id", Type: "integer", Identity: true},
			{Name: "price", Type: price},
			{Name: "fee", Type: price, Nullable: true},
			{Name: "qty", Type: "integer"},
		},
		PrimaryKey: []string{"id"},
	}}}
}

// TestTypelessColumnSQLite: a typeless column is written without a type
// name, inspect reads it back as type "", and a database made by Create or
// by a plan has no drift; the plan from one type to another and back
// rebuilds the table and keeps the rows.
func TestTypelessColumnSQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	want := typelessSchema("")

	t.Run("create", func(t *testing.T) {
		db, _ := freshDB(t, velox.SQLite)
		if err := migrate.Create(ctx, db, want); err != nil {
			t.Fatal(err)
		}
		got, err := migrate.Inspect(ctx, db, velox.SQLite, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range got.Tables[0].Columns {
			if (c.Name == "price" || c.Name == "fee") && c.Type != "" {
				t.Errorf("%s: inspected type %q, want none", c.Name, c.Type)
			}
		}
		if ch, err := migrate.Diff(got, want, migrate.DiffOptions{}); err != nil || len(ch) != 0 {
			t.Errorf("Diff of the inspected database: %v, %v", ch, err)
		}
		r, err := migrate.Drift(ctx, db, want)
		if err != nil || len(r.Changes) != 0 || len(r.Warnings) != 0 {
			t.Errorf("Drift: %+v, %v", r, err)
		}
	})

	t.Run("sql", func(t *testing.T) {
		p, err := migrate.NewPlan(&migrate.Schema{Dialect: velox.SQLite}, want, migrate.PlanOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got := sqls(p.Scripts)
		const create = "CREATE TABLE \"t\" (\n  \"id\" integer NOT NULL PRIMARY KEY AUTOINCREMENT,\n  \"price\" NOT NULL,\n  \"fee\",\n  \"qty\" integer NOT NULL\n)"
		if len(got) != 1 || got[0] != create {
			t.Errorf("create:\n%q\nwant\n%q", got, create)
		}
		cols := want.Tables[0].Columns
		base, grown := typelessSchema(""), typelessSchema("")
		base.Tables[0].Columns = []migrate.Column{cols[0], cols[3]}
		grown.Tables[0].Columns = []migrate.Column{cols[0], cols[3], cols[2]}
		p, err = migrate.NewPlan(base, grown, migrate.PlanOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got = sqls(p.Scripts)
		wantAdd := []string{`ALTER TABLE "t" ADD COLUMN "fee"`}
		if !slices.Equal(got, wantAdd) {
			t.Errorf("add column:\n%q\nwant\n%q", got, wantAdd)
		}
	})

	t.Run("plan", func(t *testing.T) {
		base := typelessSchema("")
		base.Tables[0].Columns = []migrate.Column{base.Tables[0].Columns[0], base.Tables[0].Columns[3]}
		fsys := dirOf(t, velox.SQLite, step{to: base, name: "base"})
		allow := migrate.PlanOptions{Allow: []migrate.Allow{{Code: "add-required-column", Target: "t.price"}}}
		fsys = extend(t, fsys, velox.SQLite, step{to: want, name: "typeless", opts: allow})
		var text string
		for p, f := range fsys {
			if strings.HasSuffix(p, ".sql") {
				text += string(f.Data)
			}
		}
		if !strings.Contains(text, `"price"`) {
			t.Fatalf("no price column in the files:\n%s", text)
		}
		if strings.Contains(text, `"price"  `) || strings.Contains(text, `"fee"  `) {
			t.Errorf("a double space after a typeless column's name:\n%s", text)
		}
		db, _ := freshDB(t, velox.SQLite)
		mustUp(t, db, fsys)
		r, err := migrate.Drift(ctx, db, want)
		if err != nil || len(r.Changes) != 0 || len(r.Warnings) != 0 {
			t.Fatalf("Drift after Up: %+v, %v", r, err)
		}
		empty, _ := freshDB(t, velox.SQLite)
		if err := migrate.Verify(ctx, empty, fsys); err != nil {
			t.Errorf("Verify: %v", err)
		}

		mustExec(t, db, `INSERT INTO t (price, fee, qty) VALUES ('1.10', '2.500', 1)`)
		_, err = migrate.NewPlan(want, typelessSchema("numeric"), migrate.PlanOptions{})
		if err == nil || !strings.Contains(err.Error(), "from no type to numeric") {
			t.Fatalf("a plan without alter-type: %v", err)
		}
		typed := extend(t, fsys, velox.SQLite, step{to: typelessSchema("numeric"), name: "numeric",
			opts: migrate.PlanOptions{Allow: []migrate.Allow{{Code: "alter-type", Target: "t.price"}, {Code: "alter-type", Target: "t.fee"}}}})
		mustUp(t, db, typed)
		back := extend(t, typed, velox.SQLite, step{to: want, name: "typeless_again",
			opts: migrate.PlanOptions{Allow: []migrate.Allow{{Code: "alter-type", Target: "t.price"}, {Code: "alter-type", Target: "t.fee"}}}})
		mustUp(t, db, back)
		r, err = migrate.Drift(ctx, db, want)
		if err != nil || len(r.Changes) != 0 {
			t.Fatalf("Drift after the round: %+v, %v", r, err)
		}
		var qty int
		if err := db.QueryRow(`SELECT qty FROM t`).Scan(&qty); err != nil || qty != 1 {
			t.Errorf("the row after the rebuilds: %d, %v", qty, err)
		}
	})
}
