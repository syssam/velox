package veloxtest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/testmodel"
	ts "github.com/syssam/velox/internal/testmodel/schema"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/veloxtest"
)

// schemas are every schema of internal/testmodel/schema.
var schemas = []schema.Interface{
	ts.Event{}, ts.Invoice{}, ts.Item{}, ts.Ledger{}, ts.Line{}, ts.Paper{}, ts.Topic{},
	ts.Remark{}, ts.Player{}, ts.Team{}, ts.Roster{}, ts.Sample{}, ts.Ticket{},
}

// TestOpen: Open gives each call a SQLite database of its own with the
// model's tables, in which the generated entities insert and load.
func TestOpen(t *testing.T) {
	ctx := context.Background()
	db := veloxtest.Open(t, "sqlite", "", schemas)
	s := testmodel.Sample{Name: "a", Status: "new", When: time.Now(), Blob: []byte("b")}
	if err := db.Insert(&s).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Insert(&testmodel.Line{SampleID: s.ID}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := db.Query[testmodel.Sample]().With(testmodel.Samples.Lines).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %v", got)
	}
	if lines, ok := got[0].Edges.Lines.Get(); !ok || len(lines) != 1 {
		t.Errorf("loaded lines %v", lines)
	}

	other := veloxtest.Open(t, "sqlite", "", schemas)
	n, err := other.Query[testmodel.Sample]().Count(ctx)
	if err != nil || n != 0 {
		t.Errorf("a second Open sees %d samples, %v; want its own empty database", n, err)
	}
}

// TestOpenSameFile: Open on a file that holds the tables already makes
// nothing and keeps the rows, as migrate.Create does.
func TestOpenSameFile(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "same.db")
	db := veloxtest.Open(t, "sqlite", dsn, schemas)
	if err := db.Insert(&testmodel.Sample{Name: "kept", Status: "new", When: time.Now(), Blob: []byte("b")}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	again := veloxtest.Open(t, "sqlite", dsn, schemas)
	if n, err := again.Query[testmodel.Sample]().Count(ctx); err != nil || n != 1 {
		t.Errorf("after a second Open: %d samples, %v", n, err)
	}
}

// TestSchemaRefusals: no schema, a nil one, schemas of two packages and a
// relation to a schema left out are refused.
func TestSchemaRefusals(t *testing.T) {
	for _, c := range []struct {
		name    string
		schemas []schema.Interface
		want    string
	}{
		{"none", nil, "no schemas"},
		{"nil", []schema.Interface{nil}, "nil"},
		{"two packages", []schema.Interface{ts.Item{}, other{}}, "velox-gen reads one package"},
		{"left out", []schema.Interface{ts.Item{}}, "Invoice"},
	} {
		if _, err := veloxtest.Schema(velox.SQLite, c.schemas...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want %q", c.name, err, c.want)
		}
	}
}

// other is a schema of this package.
type other struct{ schema.Schema }

// TestSchemaIsVeloxGens: on each dialect, Schema gives the tables velox-gen
// plans for the package, which it loads by running a program.
func TestSchemaIsVeloxGens(t *testing.T) {
	if testing.Short() {
		t.Skip("velox-gen's loader builds and runs a program")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	m, err := codegen.Load(root, "./internal/testmodel/schema", nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := codegen.Build(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		want, err := codegen.Tables(g, d)
		if err != nil {
			t.Fatal(err)
		}
		got, err := veloxtest.Schema(d, schemas...)
		if err != nil {
			t.Fatal(err)
		}
		changes, err := migrate.Diff(want, got, migrate.DiffOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 0 || len(got.Tables) != len(want.Tables) {
			t.Errorf("%s: Schema differs from velox-gen's: %v", d, changes)
		}
	}
}
