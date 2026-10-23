package migrate_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// needPostgres skips t without VELOX_TEST_POSTGRES.
func needPostgres(t *testing.T) {
	t.Helper()
	if os.Getenv("VELOX_TEST_POSTGRES") == "" {
		t.Skip("needs VELOX_TEST_POSTGRES")
	}
}

// mustInspect is Inspect on PostgreSQL, or fails t.
func mustInspect(t *testing.T, db *sql.DB, tables ...migrate.TableRef) *migrate.Schema {
	t.Helper()
	s, err := migrate.Inspect(context.Background(), db, velox.Postgres, tables)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return s
}

// noDrift applies fsys to a fresh PostgreSQL database and checks that Drift
// against snapshot finds no change and no warning.
func noDrift(t *testing.T, fsys fs.FS, snapshot *migrate.Schema) {
	t.Helper()
	db, _ := freshDB(t, velox.Postgres)
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up: %v", err)
	}
	r, err := migrate.Drift(context.Background(), db, snapshot)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(r.Changes) > 0 || len(r.Warnings) > 0 {
		inspected := mustInspect(t, db)
		b, _ := json.MarshalIndent(inspected, "", "  ")
		t.Errorf("changes %v, warnings %v\ninspected: %s", r.Changes, r.Warnings, b)
	}
}

// predicateSchema is a hand-made model of what the cases lack: a serial
// key, partial indexes over dates, numbers and a zoned timestamp, an enum
// check beside a native enum, a foreign key that sets null, and comments.
func predicateSchema() *migrate.Schema {
	return &migrate.Schema{Dialect: velox.Postgres,
		Enums: []migrate.Enum{{Name: "g_role", Values: []string{"a", "b", "o'neil"}}},
		Tables: []migrate.Table{{
			Name: "g",
			Columns: []migrate.Column{
				{Name: "id", Type: "bigint", Default: "nextval('g_id_seq')", Sequence: "g_id_seq"},
				{Name: "qty", Type: "integer", Default: "-1"},
				{Name: "big", Type: "bigint", Default: "3000000000"},
				{Name: "price", Type: "numeric(10,2)", Nullable: true, Default: "0"},
				{Name: "ratio", Type: "double precision", Default: "1.5"},
				{Name: "created_on", Type: "date", Default: "'2020-01-01'"},
				{Name: "tstz", Type: "timestamp with time zone", Default: "now()"},
				{Name: "role", Type: `"g_role"`, Default: "'o''neil'"},
				{Name: "kind", Type: "character varying(8)", Default: "'a'", Comment: "it's a kind"},
				{Name: "parent_id", Type: "bigint", Nullable: true},
				{Name: "active", Type: "boolean", Default: "true"},
			},
			PrimaryKey:     []string{"id"},
			PrimaryKeyName: "g_pkey",
			Indexes: []migrate.Index{
				{Name: "g_created_on", Columns: []string{"created_on"}, Where: "created_on > '2020-01-01'"},
				{Name: "g_kind_role", Columns: []string{"kind", "role"}},
				{Name: "g_price", Columns: []string{"price"}, Where: "price > 1.5 AND NOT active"},
				{Name: "g_qty", Columns: []string{"qty"}, Unique: true, Where: "qty >= -1"},
				{Name: "g_role_admin", Columns: []string{"id"}, Where: "role = 'a'"},
				{Name: "g_tstz", Columns: []string{"tstz"}, Where: "tstz > '2020-01-01 10:00:00+02'"},
			},
			ForeignKeys: []migrate.ForeignKey{{Name: "g_parent", Columns: []string{"parent_id"},
				RefTable: migrate.TableRef{Name: "g"}, RefColumns: []string{"id"}, OnDelete: "SET NULL"}},
			Checks: []migrate.Check{
				{Name: "g_kind_check", Expr: `"kind" IN ('a', 'b')`},
				{Name: "g_qty_check", Expr: "qty >= -5 OR qty IS NULL"},
			},
			Comment: "it's g",
		}},
	}
}

// predicateDir is predicateSchema's directory: the sequence is created
// before the plan and owned by the column after it, as a baseline does.
func predicateDir(t *testing.T) fstest.MapFS {
	t.Helper()
	return dirOf(t, velox.Postgres,
		files("0001_sequence.sql", "CREATE SEQUENCE g_id_seq;\n"),
		step{to: predicateSchema(), name: "g"},
		files("0003_own.sql", "ALTER SEQUENCE g_id_seq OWNED BY g.id;\n"))
}

// TestInspectPostgresCases: every case's schema on PostgreSQL, the golden
// directories and the hand-made predicate schema, applied to a fresh
// database, inspect as their snapshot with no change and no warning.
func TestInspectPostgresCases(t *testing.T) {
	needPostgres(t)
	seen := map[string]bool{}
	for _, c := range cases {
		if !slices.Contains(c.on(), velox.Postgres) {
			continue
		}
		from, to := models(t, c, velox.Postgres)
		list := []*migrate.Schema{to}
		if c.fromDB == nil && !c.empty {
			list = append(list, from)
		}
		for _, s := range list {
			b, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if seen[string(b)] {
				continue
			}
			seen[string(b)] = true
			t.Run(c.name, func(t *testing.T) {
				noDrift(t, dirOf(t, velox.Postgres, step{to: s, name: "case"}), s)
			})
		}
	}
	for _, name := range goldenCases {
		t.Run("golden-"+name, func(t *testing.T) {
			fsys := os.DirFS(filepath.Join("testdata", "dir", name+".postgres"))
			dir, err := migrate.ReadDir(fsys)
			if err != nil {
				t.Fatal(err)
			}
			noDrift(t, fsys, dir.Snapshot)
		})
	}
	t.Run("predicates", func(t *testing.T) { noDrift(t, predicateDir(t), predicateSchema()) })
}

// TestInspectPostgresSequence: an identity has its sequence, named or
// PostgreSQL's; a serial is no identity, but its nextval default and its
// sequence; a nextval of a sequence no column owns is a plain default.
func TestInspectPostgresSequence(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	for _, q := range []string{
		`CREATE TABLE a (id bigint GENERATED BY DEFAULT AS IDENTITY (SEQUENCE NAME users_id_seq) PRIMARY KEY)`,
		`CREATE TABLE b (id integer GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY)`,
		`CREATE TABLE t (id serial PRIMARY KEY)`,
		`CREATE SEQUENCE free_seq`,
		`CREATE TABLE f (id bigint DEFAULT nextval('free_seq') PRIMARY KEY)`,
		`CREATE SCHEMA billing`,
		`CREATE TABLE billing.s (id bigserial PRIMARY KEY)`,
	} {
		pgExec(t, db, q)
	}
	s := mustInspect(t, db, migrate.TableRef{Name: "a"}, migrate.TableRef{Name: "b"}, migrate.TableRef{Name: "t"},
		migrate.TableRef{Name: "f"}, migrate.TableRef{Schema: "billing", Name: "s"})
	want := map[string]migrate.Column{
		"a":         {Name: "id", Type: "bigint", Identity: true, Sequence: "users_id_seq"},
		"b":         {Name: "id", Type: "integer", Identity: true, Sequence: "b_id_seq"},
		"t":         {Name: "id", Type: "integer", Default: "nextval('t_id_seq')", Sequence: "t_id_seq"},
		"f":         {Name: "id", Type: "bigint", Default: "nextval('free_seq')"},
		"billing.s": {Name: "id", Type: "bigint", Default: "nextval('billing.s_id_seq')", Sequence: "s_id_seq"},
	}
	if len(s.Tables) != len(want) {
		t.Fatalf("tables %+v", s.Tables)
	}
	for _, tb := range s.Tables {
		if got := tb.Columns; len(got) != 1 || got[0] != want[tb.Ref().String()] {
			t.Errorf("%s: %+v, want %+v", tb.Ref(), got, want[tb.Ref().String()])
		}
	}
}

// TestInspectPostgresEnums: a type two tables use is read once, its values
// in order; a type in another schema is named with it; an unused type is not
// read.
func TestInspectPostgresEnums(t *testing.T) {
	needPostgres(t)
	_, to := models(t, caseNamed("enum-shared"), velox.Postgres)
	db, _ := freshDB(t, velox.Postgres)
	mustUp(t, db, dirOf(t, velox.Postgres, step{to: to, name: "shared"}))
	pgExec(t, db, `CREATE TYPE unused AS ENUM ('z')`)
	pgExec(t, db, `CREATE SCHEMA billing`)
	pgExec(t, db, `CREATE TYPE billing.status AS ENUM ('y', 'x')`)
	pgExec(t, db, `CREATE TABLE billing.inv (s billing.status NOT NULL DEFAULT 'x')`)

	s := mustInspect(t, db)
	var shared []migrate.Enum
	for _, e := range s.Enums {
		if e.Name == "case_status" {
			shared = append(shared, e)
		}
		if e.Name == "unused" || e.Schema == "billing" {
			t.Errorf("read %+v", e)
		}
	}
	if len(shared) != 1 || shared[0].Schema != "" || !slices.Equal(shared[0].Values, []string{"active", "pending"}) {
		t.Errorf("case_status: %+v", shared)
	}
	if !s.Equal(mustInspect(t, db, refsOf(to)...)) ||
		!(&migrate.Schema{Dialect: velox.Postgres, Enums: s.Enums}).Equal(&migrate.Schema{Dialect: velox.Postgres, Enums: to.Enums}) {
		t.Errorf("enums %+v, want %+v", s.Enums, to.Enums)
	}

	b := mustInspect(t, db, migrate.TableRef{Schema: "billing", Name: "inv"})
	want := migrate.Column{Name: "s", Type: `"billing"."status"`, Default: "'x'"}
	if len(b.Tables) != 1 || len(b.Tables[0].Columns) != 1 || b.Tables[0].Columns[0] != want {
		t.Errorf("billing.inv: %+v", b.Tables)
	}
	if !reflect.DeepEqual(b.Enums, []migrate.Enum{{Schema: "billing", Name: "status", Values: []string{"y", "x"}}}) {
		t.Errorf("billing's enums %+v", b.Enums)
	}
}

// refsOf is the refs of s's tables.
func refsOf(s *migrate.Schema) []migrate.TableRef {
	var out []migrate.TableRef
	for _, tb := range s.Tables {
		out = append(out, tb.Ref())
	}
	return out
}

// TestInspectPostgresNotes: an invalid index, a NOT VALID foreign key and a
// GENERATED ALWAYS identity are notes; the invalid index is left out of the
// schema, so Drift adds it and warns, and warns on the other two alone.
func TestInspectPostgresNotes(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.Postgres)
	pgExec(t, db, `CREATE TABLE n (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, x integer, p bigint)`)
	pgExec(t, db, `INSERT INTO n (x) VALUES (1), (1)`)
	if _, err := db.Exec(`CREATE UNIQUE INDEX CONCURRENTLY n_x ON n (x)`); err == nil {
		t.Fatal("the index over duplicates was built")
	}
	pgExec(t, db, `ALTER TABLE n ADD CONSTRAINT n_p_fkey FOREIGN KEY (p) REFERENCES n (id) NOT VALID`)

	s, notes, err := migrate.InspectNotes(ctx, db, velox.Postgres, nil)
	if err != nil {
		t.Fatal(err)
	}
	var objects []string
	for _, n := range notes {
		if n.Table != (migrate.TableRef{Name: "n"}) || n.Message == "" {
			t.Errorf("note %+v", n)
		}
		objects = append(objects, n.Object)
	}
	slices.Sort(objects)
	if !slices.Equal(objects, []string{"n.id", "n.n_p_fkey", "n.n_x"}) {
		t.Errorf("notes %+v", notes)
	}
	if len(s.Tables) != 1 || len(s.Tables[0].Indexes) != 0 || len(s.Tables[0].ForeignKeys) != 1 || !s.Tables[0].Columns[0].Identity {
		t.Fatalf("schema %+v", s.Tables)
	}

	snapshot := migrate.Clone(s)
	snapshot.Tables[0].Indexes = []migrate.Index{{Name: "n_x", Columns: []string{"x"}, Unique: true}}
	r, err := migrate.Drift(ctx, db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 1 || r.Changes[0].String() != "add-index n.n_x" {
		t.Errorf("changes %v", r.Changes)
	}
	var warned []string
	for _, w := range r.Warnings {
		warned = append(warned, w.Object)
	}
	if !slices.Equal(warned, []string{"n.id", "n.n_p_fkey", "n.n_x"}) {
		t.Errorf("warnings %v", r.Warnings)
	}
	snapshot.Tables[0].Indexes = nil
	if r, err := migrate.Drift(ctx, db, snapshot); err != nil || len(r.Changes) != 0 || len(r.Warnings) != 3 {
		t.Errorf("without the index: %+v, %v", r, err)
	}
}

// TestInspectPostgresFilter: nil reads every table of the default schema but
// the history table; a named table that is not there is absent; a table
// only another schema holds is read only when named with it, and a foreign
// key from there to the default schema names its table without a schema.
func TestInspectPostgresFilter(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	mustUp(t, db, dirOf(t, velox.Postgres, files("0001_tables.sql", "CREATE TABLE b (id integer PRIMARY KEY);\nCREATE TABLE a (id integer PRIMARY KEY, b_id integer REFERENCES b);\n")))
	pgExec(t, db, `CREATE SCHEMA other`)
	pgExec(t, db, `CREATE TABLE other.c (id integer PRIMARY KEY, b_id integer REFERENCES public.b ON DELETE CASCADE)`)
	pgExec(t, db, `CREATE VIEW v AS SELECT 1 AS x`)

	names := func(s *migrate.Schema) []string {
		var out []string
		for _, tb := range s.Tables {
			out = append(out, tb.Ref().String())
		}
		return out
	}
	s := mustInspect(t, db)
	if got := names(s); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("nil: %v", got)
	}
	if fk := s.Tables[0].ForeignKeys; len(fk) != 1 || fk[0].RefTable != (migrate.TableRef{Name: "b"}) {
		t.Errorf("a's keys %+v", fk)
	}
	if got := names(mustInspect(t, db, migrate.TableRef{Name: "missing"}, migrate.TableRef{Name: "a"}, migrate.TableRef{Name: "c"}, migrate.TableRef{Name: "v"})); !slices.Equal(got, []string{"a"}) {
		t.Errorf("named: %v", got)
	}
	c := mustInspect(t, db, migrate.TableRef{Schema: "other", Name: "c"})
	if got := names(c); !slices.Equal(got, []string{"other.c"}) {
		t.Fatalf("other.c: %v", got)
	}
	want := []migrate.ForeignKey{{Name: "c_b_id_fkey", Columns: []string{"b_id"}, RefTable: migrate.TableRef{Name: "b"}, RefColumns: []string{"id"}, OnDelete: "CASCADE"}}
	if !reflect.DeepEqual(c.Tables[0].ForeignKeys, want) {
		t.Errorf("other.c's keys %+v", c.Tables[0].ForeignKeys)
	}
}

// TestInspectDoesNotMutate: Inspect leaves the tables it is given as they
// were.
func TestInspectDoesNotMutate(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	mustUp(t, db, predicateDir(t))
	tables := []migrate.TableRef{{Name: "missing"}, {Name: "g"}, {Schema: "public", Name: "g"}}
	was := slices.Clone(tables)
	s := mustInspect(t, db, tables...)
	if !slices.Equal(tables, was) {
		t.Errorf("tables %v, was %v", tables, was)
	}
	if len(s.Tables) != 1 {
		t.Errorf("tables %+v", s.Tables)
	}
}

// TestDriftDoesNotMutate: Drift leaves the snapshot as it was, though the
// database spells its predicates otherwise.
func TestDriftDoesNotMutate(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	mustUp(t, db, predicateDir(t))
	snapshot := predicateSchema()
	snapshot.Tables[0].Pos = "schema.go:1"
	was := migrate.Clone(snapshot)
	if _, err := migrate.Drift(context.Background(), db, snapshot); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, was) {
		t.Errorf("the snapshot changed")
	}
}

// TestInspectDeterministic: Inspect and Drift give the same twice.
func TestInspectDeterministic(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	_, to := models(t, caseNamed("enum-swap-dependents"), velox.Postgres)
	mustUp(t, db, dirOf(t, velox.Postgres, step{to: to, name: "case"}))
	a, b := mustInspect(t, db), mustInspect(t, db)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if !reflect.DeepEqual(a, b) || string(ja) != string(jb) {
		t.Errorf("two inspections differ:\n%s\n%s", ja, jb)
	}
	snapshot := migrate.Clone(to)
	table(snapshot, "users").Indexes = nil // changes for Drift to report
	ra, err := migrate.Drift(context.Background(), db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := migrate.Drift(context.Background(), db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(ra.Changes) == 0 || !reflect.DeepEqual(ra, rb) {
		t.Errorf("two drifts differ, or find nothing:\n%v\n%v", ra, rb)
	}
}
