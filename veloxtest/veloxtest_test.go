package veloxtest_test

import (
	"context"
	"database/sql/driver"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/testmodel"
	ts "github.com/syssam/velox/internal/testmodel/schema"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
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

// decimal is a decimal's text, as shopspring's decimal.Decimal holds one: a
// Valuer that gives its text and a Scanner that reads it back.
type decimal string

func (d decimal) Value() (driver.Value, error) { return string(d), nil }

func (d *decimal) Scan(v any) error {
	switch v := v.(type) {
	case string:
		*d = decimal(v)
	case []byte:
		*d = decimal(v)
	default:
		return fmt.Errorf("decimal: scanned %T", v)
	}
	return nil
}

// price has decimal fields whose SchemaType names MySQL and PostgreSQL
// only, as the nextapp schemas write them.
type price struct{ schema.Schema }

func (price) Fields() []schema.Field {
	st := map[string]string{dialect.Postgres: "numeric(19,4)", dialect.MySQL: "decimal(19,4)"}
	return []schema.Field{
		field.Other("amount", decimal("")).SchemaType(st),
		field.Other("fee", decimal("")).SchemaType(st).Optional().Nillable(),
	}
}

// TestOpenOtherWithoutSQLiteType: on SQLite an Other field with no SQLite
// SchemaType is a typeless column (decision 0145), so Open builds it and a
// decimal's text comes back as written: "1.10" stays "1.10", where NUMERIC
// affinity would have stored 1.1.
func TestOpenOtherWithoutSQLiteType(t *testing.T) {
	db := veloxtest.Open(t, "sqlite", "", []schema.Interface{price{}})
	pool := velox.Pool(db)
	var typ string
	if err := pool.QueryRow(`SELECT type FROM pragma_table_info('prices') WHERE name = 'amount'`).Scan(&typ); err != nil || typ != "" {
		t.Fatalf("amount's type %q, %v; want none", typ, err)
	}
	for _, v := range []decimal{"1.10", "0.1000", "12345678901234567.8900", "-0.00"} {
		if _, err := pool.Exec(`INSERT INTO prices (amount, fee) VALUES (?, ?)`, v, v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(`INSERT INTO prices (amount, fee) VALUES (?, NULL)`, decimal("2.50")); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(`SELECT amount, fee FROM prices ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var amount decimal
		var fee *decimal
		if err := rows.Scan(&amount, &fee); err != nil {
			t.Fatal(err)
		}
		if fee == nil {
			got = append(got, string(amount)+"|NULL")
			continue
		}
		if *fee != amount {
			t.Errorf("fee %q differs from amount %q", *fee, amount)
		}
		got = append(got, string(amount))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"1.10", "0.1000", "12345678901234567.8900", "-0.00", "2.50|NULL"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("read %q, want %q", got, want)
	}
}

// recorder is a testing.TB that keeps its cleanups and errors.
type recorder struct {
	testing.TB
	cleanups []func()
	errs     []string
}

func (r *recorder) Helper()           {}
func (r *recorder) Cleanup(f func())  { r.cleanups = append(r.cleanups, f) }
func (r *recorder) Error(args ...any) { r.errs = append(r.errs, fmt.Sprint(args...)) }
func (r *recorder) end() {
	for _, f := range r.cleanups {
		f()
	}
}

// TestNoNPlusOne: a read of each sample's lines in a loop fails the test
// when it ends, naming the statement; the lines loaded with With pass; and
// Budget fails a context over its count of statements.
func TestNoNPlusOne(t *testing.T) {
	ctx := context.Background()
	db := veloxtest.Open(t, "sqlite", "", schemas)
	for range 2 {
		s := testmodel.Sample{Name: "a", Status: "new", When: time.Now(), Blob: []byte("b")}
		if err := db.Insert(&s).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	r := &recorder{}
	c := veloxtest.NoNPlusOne(r, ctx)
	samples, err := db.Query[testmodel.Sample]().Limit(10).All(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		if _, err := db.Query[testmodel.Line]().Where(testmodel.Lines.SampleID.EQ(s.ID)).Limit(10).All(c); err != nil {
			t.Fatal(err)
		}
	}
	r.end()
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], "ran 2 times") {
		t.Errorf("an N+1: %q", r.errs)
	}

	r = &recorder{}
	c = veloxtest.NoNPlusOne(r, ctx)
	if _, err := db.Query[testmodel.Sample]().With(testmodel.Samples.Lines).Limit(10).All(c); err != nil {
		t.Fatal(err)
	}
	r.end()
	if len(r.errs) != 0 {
		t.Errorf("With: %q", r.errs)
	}

	r = &recorder{}
	c = veloxtest.Budget(r, ctx, velox.StatementBudget{Statements: 1})
	for range 2 {
		if _, err := db.Query[testmodel.Sample]().Limit(1).All(c); err != nil {
			t.Fatal(err)
		}
	}
	r.end()
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], "2 statements ran, of 1 allowed") {
		t.Errorf("over the count: %q", r.errs)
	}
}
