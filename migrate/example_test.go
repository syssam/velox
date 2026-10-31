package migrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing/fstest"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// exampleUsers is a SQLite schema of one table, as velox-gen builds it
// from a schema package.
func exampleUsers(withEmail bool) *migrate.Schema {
	t := migrate.Table{
		Name: "users",
		Columns: []migrate.Column{
			{Name: "id", Type: "integer", Identity: true},
			{Name: "name", Type: "text"},
		},
		PrimaryKey: []string{"id"},
	}
	if withEmail {
		t.Columns = append(t.Columns, migrate.Column{Name: "email", Type: "text", Default: "''"})
	}
	return &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{t}}
}

// NewPlan compares two schemas and renders the statements that turn one
// into the other.
func ExampleNewPlan() {
	from, to := exampleUsers(false), exampleUsers(true)
	p, err := migrate.NewPlan(from, to, migrate.PlanOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, c := range p.Changes {
		fmt.Println(c)
	}
	for _, s := range p.Scripts {
		for _, st := range s.Statements {
			fmt.Println(st.SQL)
		}
	}
	// Output:
	// add-column users.email
	// ALTER TABLE "users" ADD COLUMN "email" text NOT NULL DEFAULT ''
}

// A directory starts with a plan from the empty schema. Up applies its
// pending files to a database.
func ExampleUp() {
	ctx := context.Background()
	d, err := migrate.NewDir(migrate.Config{Dialect: velox.SQLite})
	if err != nil {
		fmt.Println(err)
		return
	}
	want := exampleUsers(false)
	p, err := migrate.NewPlan(d.Snapshot, want, migrate.PlanOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}
	files, err := d.Add("init", p, want)
	if err != nil {
		fmt.Println(err)
		return
	}
	dir := fstest.MapFS{}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		fmt.Println(name)
		dir[name] = &fstest.MapFile{Data: files[name]}
	}
	tmp, err := os.MkdirTemp("", "migrate-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	db, err := sql.Open("sqlite", filepath.Join(tmp, "app.db"))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = db.Close() }()
	r, err := migrate.Up(ctx, db, dir)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, a := range r.Applied {
		fmt.Println(a.Version, a.Name)
	}
	st, err := migrate.Status(ctx, db, dir)
	fmt.Println(len(st.Applied), len(st.Pending), err)
	// Output:
	// 0001_init.sql
	// schema.json
	// velox.json
	// velox.sum
	// 1 init
	// 1 0 <nil>
}
