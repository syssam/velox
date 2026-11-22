package main

import (
	"io/fs"
	"testing"

	"github.com/syssam/velox/examples/relations/veloxgen"
	"github.com/syssam/velox/examples/relations/veloxschema"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/veloxtest"
)

// schemas are every schema of veloxschema, which veloxtest builds the
// tables of.
var schemas = []schema.Interface{
	veloxschema.Author{}, veloxschema.Post{}, veloxschema.Tag{}, veloxschema.Comment{},
	veloxschema.Club{}, veloxschema.Membership{},
}

// TestVeloxtest: veloxtest.Open gives the test a SQLite database of its own
// with the schema's tables, made without the migrations directory, as
// ent's enttest.Open does; the example's rows go in.
func TestVeloxtest(t *testing.T) {
	ctx := t.Context()
	db := veloxtest.Open(t, "sqlite", "", schemas)
	if err := seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	n, err := db.Query[veloxgen.Post]().Count(ctx)
	if err != nil || n != 5 {
		t.Errorf("%d posts, %v; want 5", n, err)
	}
}

// TestVeloxtestIsTheDirectory: the tables veloxtest makes are the ones the
// migrations directory says it makes.
func TestVeloxtestIsTheDirectory(t *testing.T) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := migrate.ReadDir(sub)
	if err != nil {
		t.Fatal(err)
	}
	s, err := veloxtest.Schema(dir.Config.Dialect, schemas...)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := migrate.Diff(dir.Snapshot, s, migrate.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		t.Errorf("veloxtest differs from schema.json: %s", c)
	}
}
