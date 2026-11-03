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
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
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

// predicateDir is predicateSchema's directory, whose plan creates the
// serial's sequence.
func predicateDir(t *testing.T) fstest.MapFS {
	t.Helper()
	return dirOf(t, velox.Postgres, step{to: predicateSchema(), name: "g"})
}

// TestInspectPostgresCases: every case's schema on PostgreSQL, the golden
// directories and the hand-made predicate schema, applied to a fresh
// database, inspect as their snapshot with no change and no warning.
func TestInspectPostgresCases(t *testing.T) {
	t.Parallel()
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
				t.Parallel()
				noDrift(t, dirOf(t, velox.Postgres, step{to: s, name: "case"}), s)
			})
		}
	}
	for _, name := range goldenCases {
		t.Run("golden-"+name, func(t *testing.T) {
			t.Parallel()
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
// sequence; a nextval of a sequence no column owns is a plain default. An
// identity's own sequence wins over another the column owns.
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
		// An identity that also owns a sequence of an earlier name.
		`CREATE SEQUENCE a_aaa_seq OWNED BY a.id`,
	} {
		mustExec(t, db, q)
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
// read. A check on an enum column, its type qualified or not, is in the
// model's form.
func TestInspectPostgresEnums(t *testing.T) {
	needPostgres(t)
	_, to := models(t, caseNamed("enum-shared"), velox.Postgres)
	db, _ := freshDB(t, velox.Postgres)
	mustUp(t, db, dirOf(t, velox.Postgres, step{to: to, name: "shared"}))
	mustExec(t, db, `CREATE TYPE unused AS ENUM ('z')`)
	mustExec(t, db, `CREATE SCHEMA billing`)
	mustExec(t, db, `CREATE TYPE billing.status AS ENUM ('y', 'x')`)
	mustExec(t, db, `CREATE TABLE billing.inv (s billing.status NOT NULL DEFAULT 'x', CONSTRAINT inv_s_known CHECK (s IN ('x', 'y')))`)
	mustExec(t, db, `ALTER TABLE users ADD CONSTRAINT users_role_known CHECK (role IN ('admin', 'viewer'))`)

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
	// A check on an enum column is in the model's form.
	if ck := table(s, "users").Checks; len(ck) != 1 || ck[0].Expr != `"role" IN ('admin', 'viewer')` {
		t.Errorf("users' checks %+v", ck)
	}
	table(s, "users").Checks = nil
	inspected := mustInspect(t, db, refsOf(to)...)
	table(inspected, "users").Checks = nil
	if !s.Equal(inspected) ||
		!(&migrate.Schema{Dialect: velox.Postgres, Enums: s.Enums}).Equal(&migrate.Schema{Dialect: velox.Postgres, Enums: to.Enums}) {
		t.Errorf("enums %+v, want %+v", s.Enums, to.Enums)
	}

	b := mustInspect(t, db, migrate.TableRef{Schema: "billing", Name: "inv"})
	want := migrate.Column{Name: "s", Type: `"billing"."status"`, Default: "'x'"}
	if len(b.Tables) != 1 || len(b.Tables[0].Columns) != 1 || b.Tables[0].Columns[0] != want {
		t.Errorf("billing.inv: %+v", b.Tables)
	}
	if ck := b.Tables[0].Checks; len(ck) != 1 || ck[0].Expr != `"s" IN ('x', 'y')` {
		t.Errorf("billing.inv's checks %+v", ck)
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
	mustExec(t, db, `CREATE TABLE n (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, x integer, p bigint)`)
	mustExec(t, db, `INSERT INTO n (x) VALUES (1), (1)`)
	if _, err := db.Exec(`CREATE UNIQUE INDEX CONCURRENTLY n_x ON n (x)`); err == nil {
		t.Fatal("the index over duplicates was built")
	}
	mustExec(t, db, `ALTER TABLE n ADD CONSTRAINT n_p_fkey FOREIGN KEY (p) REFERENCES n (id) NOT VALID`)

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
	mustExec(t, db, `CREATE SCHEMA other`)
	mustExec(t, db, `CREATE TABLE other.c (id integer PRIMARY KEY, b_id integer REFERENCES public.b ON DELETE CASCADE)`)
	mustExec(t, db, `CREATE VIEW v AS SELECT 1 AS x`)

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
		t.Fatalf("tables %+v", s.Tables)
	}
	// The varchar column's enum check is in the model's form.
	if ck := s.Tables[0].Checks; len(ck) != 2 || ck[0].Expr != `"kind" IN ('a', 'b')` {
		t.Errorf("checks %+v", ck)
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

// TestInspectPostgresUnheld: what an index, a foreign key or a constraint
// holds that the model cannot is one note per object naming it, so Drift
// warns; the index or key itself is still read in the form the model has.
func TestInspectPostgresUnheld(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.Postgres)
	for _, q := range []string{
		`CREATE TABLE p (id integer PRIMARY KEY, a text, b integer, c integer, r tsrange)`,
		`CREATE UNIQUE INDEX p_b_c ON p (b, c)`,
		`CREATE TABLE u (id integer PRIMARY KEY, p1 integer, p2 integer, p3 integer, p4 integer, k integer,
			CONSTRAINT u_p1_fkey FOREIGN KEY (p1) REFERENCES p (id) ON UPDATE CASCADE,
			CONSTRAINT u_p2_fkey FOREIGN KEY (p2) REFERENCES p (id) DEFERRABLE INITIALLY DEFERRED,
			CONSTRAINT u_p3_fkey FOREIGN KEY (p3, p4) REFERENCES p (b, c) MATCH FULL,
			CONSTRAINT u_k_key UNIQUE (k) DEFERRABLE)`,
		`CREATE INDEX p_hash ON p USING hash (a)`,
		`CREATE INDEX p_desc ON p (b DESC, c NULLS FIRST)`,
		`CREATE INDEX p_ops ON p (a text_pattern_ops)`,
		`CREATE INDEX p_coll ON p (a COLLATE "C")`,
		`CREATE INDEX p_incl ON p (b) INCLUDE (c)`,
		`CREATE INDEX p_expr ON p (lower(a))`,
		`ALTER TABLE p ADD CONSTRAINT p_r_excl EXCLUDE USING gist (r WITH &&)`,
		`CREATE INDEX p_plain ON p (a)`,
		`CREATE TABLE c (id integer PRIMARY KEY, e text COLLATE "C", f text COLLATE "default", g text)`,
		`CREATE UNLOGGED TABLE l (id integer PRIMARY KEY)`,
		`CREATE TABLE k (id integer, a integer, CONSTRAINT k_pkey PRIMARY KEY (id) INCLUDE (a))`,
	} {
		mustExec(t, db, q)
	}
	// NULLS NOT DISTINCT is PostgreSQL 15's.
	var version int
	if err := db.QueryRow(`SELECT current_setting('server_version_num')::int`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version >= 150000 {
		mustExec(t, db, `CREATE UNIQUE INDEX p_nnd ON p (c) NULLS NOT DISTINCT`)
	}
	s, notes, err := migrate.InspectNotes(ctx, db, velox.Postgres, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"p.p_hash":    {"hash"},
		"p.p_desc":    {"DESC", "NULLS FIRST"},
		"p.p_ops":     {"operator class"},
		"p.p_coll":    {"collation"},
		"p.p_incl":    {"INCLUDE"},
		"p.p_expr":    {"expression"},
		"p.p_r_excl":  {"exclusion"},
		"u.u_p1_fkey": {"ON UPDATE CASCADE"},
		"u.u_p2_fkey": {"DEFERRABLE"},
		"u.u_p3_fkey": {"MATCH FULL"},
		"u.u_k_key":   {"DEFERRABLE"},
		"c.e":         {`COLLATE "C"`},
		"l":           {"UNLOGGED"},
		"k.k_pkey":    {"primary key", "INCLUDE"},
	}
	if version >= 150000 {
		want["p.p_nnd"] = []string{"NULLS NOT DISTINCT"}
	}
	got := map[string]string{}
	for _, n := range notes {
		if n.Baseline {
			continue // Baseline's alone, as TestInspectPostgresBaselineNotes checks
		}
		if _, twice := got[n.Object]; twice {
			t.Errorf("two notes on %s", n.Object)
		}
		got[n.Object] = n.Message
	}
	for object, words := range want {
		for _, w := range words {
			if !strings.Contains(got[object], w) {
				t.Errorf("%s: note %q, want it to name %s", object, got[object], w)
			}
		}
	}
	if len(got) != len(want) {
		t.Errorf("notes %v", got)
	}
	// The texts in full, each naming its subject, so a merged warning reads.
	for object, text := range map[string]string{
		"k.k_pkey":  "the primary key's index has INCLUDE columns, which the model cannot hold; the key is compared without",
		"u.u_k_key": "the constraint is DEFERRABLE, which the model cannot hold; it is compared without",
	} {
		if got[object] != text {
			t.Errorf("%s: note %q, want %q", object, got[object], text)
		}
	}
	var indexes []string
	for _, ix := range table(s, "p").Indexes {
		indexes = append(indexes, ix.Name)
	}
	wantIndexes := []string{"p_b_c", "p_coll", "p_desc", "p_hash", "p_incl", "p_ops", "p_plain"}
	if version >= 150000 {
		wantIndexes = []string{"p_b_c", "p_coll", "p_desc", "p_hash", "p_incl", "p_nnd", "p_ops", "p_plain"}
	}
	if !slices.Equal(indexes, wantIndexes) {
		t.Errorf("p's indexes %v", indexes)
	}
	if fk := table(s, "u").ForeignKeys; len(fk) != 3 {
		t.Errorf("u's keys %+v", fk)
	}
	r, err := migrate.Drift(ctx, db, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 0 || len(r.Warnings) != len(want) {
		t.Errorf("drift: changes %v, warnings %v", r.Changes, r.Warnings)
	}
}

// TestDriftPostgresLive: a hand change to the database after Up is the one
// change that undoes it, and what the model cannot hold is a warning.
func TestDriftPostgresLive(t *testing.T) {
	needPostgres(t)
	_, base := models(t, caseNamed("add-index"), velox.Postgres)
	for _, c := range []struct {
		name    string
		snap    *migrate.Schema
		alter   []string
		changes []string
		warns   []string
	}{
		{name: "type", alter: []string{`ALTER TABLE g ALTER COLUMN kind TYPE character varying(20)`},
			changes: []string{"alter-column g.kind (type)"}},
		{name: "nullability", alter: []string{`ALTER TABLE g ALTER COLUMN qty DROP NOT NULL`},
			changes: []string{"alter-column g.qty (nullable)"}},
		{name: "default", alter: []string{`ALTER TABLE g ALTER COLUMN kind SET DEFAULT 'b'`},
			changes: []string{"alter-column g.kind (default)"}},
		{name: "on-delete", alter: []string{`ALTER TABLE g DROP CONSTRAINT g_parent, ADD CONSTRAINT g_parent FOREIGN KEY (parent_id) REFERENCES g (id) ON DELETE CASCADE`},
			changes: []string{"drop-foreign-key g.g_parent", "add-foreign-key g.g_parent"}},
		{name: "predicate", alter: []string{`DROP INDEX g_price`, `CREATE INDEX g_price ON g (price) WHERE price > 2.5 AND NOT active`},
			changes: []string{"drop-index g.g_price", "add-index g.g_price"}},
		{name: "check", alter: []string{`ALTER TABLE g DROP CONSTRAINT g_qty_check, ADD CONSTRAINT g_qty_check CHECK (qty >= -6 OR qty IS NULL)`},
			changes: []string{"drop-check g.g_qty_check", "add-check g.g_qty_check"}},
		{name: "enum-label", alter: []string{`ALTER TYPE g_role ADD VALUE 'c'`},
			changes: []string{"alter-enum g_role (a, b, o'neil, c → a, b, o'neil)"}},
		{name: "hash", alter: []string{`DROP INDEX g_created_on`, `CREATE INDEX g_created_on ON g USING hash (created_on) WHERE created_on > '2020-01-01'`},
			warns: []string{"g.g_created_on"}},
		{name: "on-update", alter: []string{`ALTER TABLE g DROP CONSTRAINT g_parent, ADD CONSTRAINT g_parent FOREIGN KEY (parent_id) REFERENCES g (id) ON DELETE SET NULL ON UPDATE CASCADE`},
			warns: []string{"g.g_parent"}},
		// An enum column made text, its type still there: the column alone.
		{name: "enum-to-text", snap: base, alter: []string{
			`ALTER TABLE users ALTER COLUMN role DROP DEFAULT, ALTER COLUMN role TYPE text, ALTER COLUMN role SET DEFAULT 'viewer'`},
			changes: []string{"alter-column users.role (type)"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, _ := freshDB(t, velox.Postgres)
			snap := c.snap
			if snap == nil {
				snap = predicateSchema()
				mustUp(t, db, predicateDir(t))
			} else {
				mustUp(t, db, dirOf(t, velox.Postgres, step{to: snap, name: "case"}))
			}
			for _, q := range c.alter {
				mustExec(t, db, q)
			}
			r, err := migrate.Drift(context.Background(), db, snap)
			if err != nil {
				t.Fatal(err)
			}
			var changes, warns []string
			for _, ch := range r.Changes {
				changes = append(changes, ch.String())
			}
			for _, w := range r.Warnings {
				warns = append(warns, w.Object)
			}
			if !slices.Equal(changes, c.changes) || !slices.Equal(warns, c.warns) {
				t.Errorf("changes %q, warnings %v; want %q, %q", changes, r.Warnings, c.changes, c.warns)
			}
		})
	}
}

// TestInspectPostgresBaselineNotes: a serial's sequence that does not start
// at 1 is a baseline note, and a unique constraint, whose index has
// Constraint set, is none.
func TestInspectPostgresBaselineNotes(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	mustExec(t, db, `CREATE TABLE u (id serial PRIMARY KEY, k integer CONSTRAINT u_k_key UNIQUE)`)
	mustExec(t, db, `ALTER SEQUENCE u_id_seq INCREMENT BY 2`)
	s, notes, err := migrate.InspectNotes(context.Background(), db, velox.Postgres, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ixs := table(s, "u").Indexes; !reflect.DeepEqual(ixs, []migrate.Index{{Name: "u_k_key", Columns: []string{"k"}, Unique: true, Constraint: true}}) {
		t.Errorf("indexes %+v", ixs)
	}
	want := []migrate.Note{
		{Table: migrate.TableRef{Name: "u"}, Object: "u.id", Baseline: true,
			Message: "the sequence u_id_seq starts at 1 and increments by 2; 0001_baseline.sql creates it with the defaults"},
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes\n%+v\nwant\n%+v", notes, want)
	}
}

// TestInspectPostgresExtensionThrough: an extension's type behind an array or
// a domain is found, and named as the type the column's type is over when a
// domain lies between; a domain, which 0001_baseline.sql does not create, is
// a baseline note naming the nearest domain, also behind an array or another
// domain, but not one an extension creates or one of information_schema's.
func TestInspectPostgresExtensionThrough(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	if _, err := db.Exec(`CREATE EXTENSION citext`); err != nil {
		t.Skipf("CREATE EXTENSION citext: %v", err)
	}
	for _, q := range []string{
		`CREATE TABLE tag_sets (id integer PRIMARY KEY, labels citext[])`,
		`CREATE DOMAIN tag_name AS citext`,
		`CREATE TABLE tag_names (id integer PRIMARY KEY, name tag_name)`,
		`CREATE TABLE tag_lists (id integer PRIMARY KEY, names tag_name[])`,
		`CREATE DOMAIN tag_arr AS citext[]`,
		`CREATE TABLE tag_arrs (id integer PRIMARY KEY, names tag_arr)`,
		`CREATE DOMAIN tag_word AS tag_name`,
		`CREATE TABLE tag_words (id integer PRIMARY KEY, word tag_word)`,
		`CREATE TABLE info_names (id integer PRIMARY KEY, name information_schema.sql_identifier)`,
	} {
		mustExec(t, db, q)
	}
	refs := []migrate.TableRef{{Name: "tag_sets"}, {Name: "tag_names"}, {Name: "tag_lists"}, {Name: "tag_arrs"}, {Name: "tag_words"}, {Name: "info_names"}}
	// lo's type is a domain over oid that CREATE EXTENSION lo creates.
	lo := true
	if _, err := db.Exec(`CREATE EXTENSION lo`); err != nil {
		t.Logf("CREATE EXTENSION lo: %v; its row is skipped", err)
		lo = false
	} else {
		mustExec(t, db, `CREATE TABLE blobs (id integer PRIMARY KEY, b lo)`)
		refs = append(refs, migrate.TableRef{Name: "blobs"})
	}
	// Whether citext's array type is the extension's member, as on PostgreSQL
	// 18, or only citext is, as on 14: tag_arr is over the nearest member.
	var arrMember bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_depend WHERE classid = 'pg_type'::regclass
		AND objid = 'citext[]'::regtype AND deptype = 'e')`).Scan(&arrMember); err != nil {
		t.Fatal(err)
	}
	arrOver := "citext"
	if arrMember {
		arrOver = "citext[]"
	}
	t.Logf("citext[] is an extension member: %v", arrMember)
	_, notes, err := migrate.InspectNotes(context.Background(), db, velox.Postgres, refs)
	if err != nil {
		t.Fatal(err)
	}
	var got []migrate.Note
	for _, n := range notes {
		if n.Baseline {
			got = append(got, n)
		}
	}
	add := "; add CREATE EXTENSION citext to 0001_baseline.sql before the tables, then run migrate hash"
	domain := " is a domain, which 0001_baseline.sql does not create; add its CREATE DOMAIN before the tables, then run migrate hash"
	want := []migrate.Note{
		{Table: migrate.TableRef{Name: "tag_sets"}, Object: "tag_sets", Baseline: true, Message: "the type citext[] comes from the extension citext" + add},
		{Table: migrate.TableRef{Name: "tag_names"}, Object: "tag_names", Baseline: true, Message: "the type tag_name is over citext, from the extension citext" + add},
		{Table: migrate.TableRef{Name: "tag_names"}, Object: "tag_names", Baseline: true, Message: "the type tag_name" + domain},
		{Table: migrate.TableRef{Name: "tag_lists"}, Object: "tag_lists", Baseline: true, Message: "the type tag_name[] is over citext, from the extension citext" + add},
		{Table: migrate.TableRef{Name: "tag_lists"}, Object: "tag_lists", Baseline: true, Message: "the type tag_name" + domain},
		{Table: migrate.TableRef{Name: "tag_arrs"}, Object: "tag_arrs", Baseline: true, Message: "the type tag_arr is over " + arrOver + ", from the extension citext" + add},
		{Table: migrate.TableRef{Name: "tag_arrs"}, Object: "tag_arrs", Baseline: true, Message: "the type tag_arr" + domain},
		{Table: migrate.TableRef{Name: "tag_words"}, Object: "tag_words", Baseline: true, Message: "the type tag_word is over citext, from the extension citext" + add},
		{Table: migrate.TableRef{Name: "tag_words"}, Object: "tag_words", Baseline: true, Message: "the type tag_word" + domain},
	}
	if lo {
		want = append(want, migrate.Note{Table: migrate.TableRef{Name: "blobs"}, Object: "blobs", Baseline: true,
			Message: "the type lo comes from the extension lo; add CREATE EXTENSION lo to 0001_baseline.sql before the tables, then run migrate hash"})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("baseline notes\n%+v\nwant\n%+v", got, want)
	}
}

// TestInspectPostgresNestedDomain: a domain over a user's domain is one
// baseline note, naming the nearest domain, d_outer; d_inner, which
// 0001_baseline.sql does not create either, is not named.
func TestInspectPostgresNestedDomain(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	for _, q := range []string{
		`CREATE DOMAIN d_inner AS text`,
		`CREATE DOMAIN d_outer AS d_inner`,
		`CREATE TABLE nd (id integer PRIMARY KEY, v d_outer)`,
	} {
		mustExec(t, db, q)
	}
	_, notes, err := migrate.InspectNotes(context.Background(), db, velox.Postgres, []migrate.TableRef{{Name: "nd"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []migrate.Note{
		{Table: migrate.TableRef{Name: "nd"}, Object: "nd", Baseline: true,
			Message: "the type d_outer is a domain, which 0001_baseline.sql does not create; add its CREATE DOMAIN before the tables, then run migrate hash"},
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes\n%+v\nwant\n%+v", notes, want)
	}
}
