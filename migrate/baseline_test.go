package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// adoptOf is AdoptOf, or fails t.
func adoptOf(t *testing.T, inspected, want *migrate.Schema, notes ...migrate.Note) (*migrate.Schema, []migrate.Warning) {
	t.Helper()
	snap, ws, err := migrate.AdoptOf(inspected, want, notes...)
	if err != nil {
		t.Fatalf("AdoptOf: %v", err)
	}
	return snap, ws
}

// adoptWarnings is ws as their String.
func adoptWarnings(ws []migrate.Warning) string { return strings.Join(warningStrings(ws), "\n") }

// TestAdoptSpellings: an index, check or default the database spells its
// own way takes the schema's spelling; an index matched by definition keeps
// its name.
func TestAdoptSpellings(t *testing.T) {
	inspected := schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.Columns = append(tb.Columns, migrate.Column{Name: "active", Type: "boolean"},
			migrate.Column{Name: "price", Type: "numeric(10,2)", Default: "0.00"})
		tb.Indexes = []migrate.Index{{Name: "user_active", Columns: []string{"active"}, Where: "(active = true)"}}
		tb.Checks = []migrate.Check{{Name: "users_qty_check", Expr: "(qty > 0)"}}
	}))
	want := schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.Columns = append(tb.Columns, migrate.Column{Name: "active", Type: "boolean"},
			migrate.Column{Name: "price", Type: "numeric(10,2)", Default: "0"})
		tb.Indexes = []migrate.Index{{Name: "users_active", Columns: []string{"active"}, Where: "active = true"}}
		tb.Checks = []migrate.Check{{Name: "users_qty_check", Expr: "qty > 0"}}
	}))
	snap, ws := adoptOf(t, inspected, want)
	if len(ws) > 0 {
		t.Errorf("warnings:\n%s", adoptWarnings(ws))
	}
	tb := table(snap, "users")
	if got, want := tb.Indexes, []migrate.Index{{Name: "user_active", Columns: []string{"active"}, Where: "active = true"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("indexes %+v, want %+v", got, want)
	}
	if got := tb.Checks[0].Expr; got != "qty > 0" {
		t.Errorf("check %q", got)
	}
	if got := column(tb, "price").Default; got != "0" {
		t.Errorf("default %q", got)
	}
}

// TestAdoptKeepsDifferences: what differs keeps the database's form, with a
// warning only where the comparison cannot settle it.
func TestAdoptKeepsDifferences(t *testing.T) {
	adoptIndex := func(db, schema string) (*migrate.Schema, []migrate.Warning) {
		return adoptOf(t, schemaOf(velox.Postgres, usersOf(velox.Postgres, withIndex(db))),
			schemaOf(velox.Postgres, usersOf(velox.Postgres, withIndex(schema))))
	}
	// Another predicate: the next diff rebuilds the index.
	snap, ws := adoptIndex(`(status = 'banned'::text)`, `status = 'active'`)
	if got := table(snap, "users").Indexes[0].Where; got != `(status = 'banned'::text)` || len(ws) > 0 {
		t.Errorf("another predicate: %q, warnings:\n%s", got, adoptWarnings(ws))
	}
	// One the normaliser cannot read.
	snap, ws = adoptIndex(`((status || 'x'::text) = 'ax'::text)`, `status || 'x' = 'ax'`)
	if got := table(snap, "users").Indexes[0].Where; got != `((status || 'x'::text) = 'ax'::text)` || len(ws) != 1 || ws[0].Object != "users.users_active" {
		t.Errorf("an unreadable predicate: %q, warnings:\n%s", got, adoptWarnings(ws))
	}

	// A check the normaliser cannot read, and an expression default.
	inspected := schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.Checks = []migrate.Check{{Name: "users_qty_check", Expr: "((email || 'x'::text) <> 'y'::text)"}}
		tb.Columns = append(tb.Columns, migrate.Column{Name: "created_at", Type: "timestamp with time zone", Default: "now()::date"})
	}))
	want := schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.Checks = []migrate.Check{{Name: "users_qty_check", Expr: "email || 'x' <> 'y'"}}
		tb.Columns = append(tb.Columns, migrate.Column{Name: "created_at", Type: "timestamp with time zone", Default: "now()"})
	}))
	snap, ws = adoptOf(t, inspected, want)
	tb := table(snap, "users")
	if tb.Checks[0].Expr != "((email || 'x'::text) <> 'y'::text)" || column(tb, "created_at").Default != "now()::date" ||
		!slices.Equal(warningObjects(ws), []string{"users.created_at", "users.users_qty_check"}) {
		t.Errorf("check %q, default %q, warnings:\n%s", tb.Checks[0].Expr, column(tb, "created_at").Default, adoptWarnings(ws))
	}

	// A primary key of other columns, and of another name.
	inspected = schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.PrimaryKey = []string{"id", "qty"}
		tb.PrimaryKeyName = ""
	}))
	snap, ws = adoptOf(t, inspected, schemaOf(velox.Postgres, usersOf(velox.Postgres, nil)))
	tb = table(snap, "users")
	if !slices.Equal(tb.PrimaryKey, []string{"id", "qty"}) || tb.PrimaryKeyName != "" || !slices.Equal(warningObjects(ws), []string{"users", "users"}) {
		t.Errorf("primary key %v %q, warnings:\n%s", tb.PrimaryKey, tb.PrimaryKeyName, adoptWarnings(ws))
	}

	// A varchar where the schema has an enum is a change for the next diff.
	inspected = schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.Columns = append(tb.Columns, migrate.Column{Name: "role", Type: "character varying"})
	}))
	want = schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.Columns = append(tb.Columns, migrate.Column{Name: "role", Type: `"users_role"`})
	}))
	want.Enums = []migrate.Enum{{Name: "users_role", Values: []string{"a", "b"}}}
	snap, ws = adoptOf(t, inspected, want)
	if got := column(table(snap, "users"), "role").Type; got != "character varying" || len(snap.Enums) > 0 || len(ws) > 0 {
		t.Errorf("role %q, enums %v, warnings:\n%s", got, snap.Enums, adoptWarnings(ws))
	}
}

// warningObjects is the Object of each of ws.
func warningObjects(ws []migrate.Warning) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Object)
	}
	return out
}

// TestAdoptRefusals: a foreign key to a table the schema does not name and
// a nextval of a sequence the column does not own are refused together; one
// alone is a bare *Error.
func TestAdoptRefusals(t *testing.T) {
	fk := migrate.ForeignKey{Name: "users_org_id_fkey", Columns: []string{"qty"}, RefTable: migrate.TableRef{Name: "orgs"}, RefColumns: []string{"id"}}
	inspected := schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.ForeignKeys = []migrate.ForeignKey{fk}
		tb.Columns = append(tb.Columns, migrate.Column{Name: "n", Type: "bigint", Default: "nextval('users_n_seq')"})
	}))
	want := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))
	snap, ws, err := migrate.AdoptOf(inspected, want)
	if snap != nil || ws != nil {
		t.Errorf("a refusal gives %v and %v", snap, ws)
	}
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "baseline" {
		t.Fatalf("AdoptOf: %v", err)
	}
	if _, bare := err.(*migrate.Error); bare {
		t.Errorf("two refusals are one *Error")
	}
	if got, want := err.Error(), "migrate: users.n: the default uses the sequence users_n_seq, which the column does not own; "+
		"run ALTER SEQUENCE users_n_seq OWNED BY users.n, then baseline again\n"+
		"migrate: users.users_org_id_fkey: references orgs, which the schema does not name; add the table to the schema, or drop the key first"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}

	inspected = schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) { tb.ForeignKeys = []migrate.ForeignKey{fk} }))
	_, _, err = migrate.AdoptOf(inspected, want)
	if e, ok := err.(*migrate.Error); !ok || e.Code != "baseline" || e.Object != "users.users_org_id_fkey" ||
		e.Reason != "references orgs, which the schema does not name; add the table to the schema, or drop the key first" {
		t.Errorf("one refusal: %#v", err)
	}

	// A key to a table of the schema the database lacks is refused too.
	want = schemaOf(velox.Postgres, usersOf(velox.Postgres, nil), migrate.Table{Name: "orgs"})
	if _, _, err := migrate.AdoptOf(inspected, want); fmt.Sprint(err) != "migrate: users.users_org_id_fkey: references orgs, "+
		"which the database does not have; create the table, or drop the key first" {
		t.Errorf("a key to a missing table: %v", err)
	}
}

// TestBaselineNoTables: a database that holds none of the schema's tables
// is refused; a missing table beside a found one is a warning, and left out.
func TestBaselineNoTables(t *testing.T) {
	inspected := schemaOf(velox.Postgres, migrate.Table{Name: "audit"})
	snap, ws, err := migrate.AdoptOf(inspected, schemaOf(velox.Postgres, usersOf(velox.Postgres, nil)))
	if e, ok := err.(*migrate.Error); !ok || e.Code != "baseline" || snap != nil || ws != nil ||
		e.Error() != "migrate: the database holds none of the schema's tables; use migrate diff to start a directory" {
		t.Errorf("no tables: %v, %v, %v", err, snap, ws)
	}

	inspected = schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))
	want := schemaOf(velox.Postgres, migrate.Table{Name: "posts"}, usersOf(velox.Postgres, nil))
	snap, ws = adoptOf(t, inspected, want)
	if len(snap.Tables) != 1 || snap.Tables[0].Name != "users" {
		t.Errorf("tables %v", snap.Tables)
	}
	if got := adoptWarnings(ws); got != "posts: the database has no table posts; the next migrate diff creates it" {
		t.Errorf("warnings:\n%s", got)
	}
}

// TestAdoptNotes: a baseline note is a warning of Baseline's, not Drift's.
func TestAdoptNotes(t *testing.T) {
	s := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))
	notes := []migrate.Note{
		{Table: migrate.TableRef{Name: "users"}, Object: "users.id", Message: "loses its start", Baseline: true},
		{Table: migrate.TableRef{Name: "users"}, Object: "users", Message: "is UNLOGGED"},
	}
	_, ws := adoptOf(t, s, s, notes...)
	if got := adoptWarnings(ws); got != "users: is UNLOGGED\nusers.id: loses its start" {
		t.Errorf("AdoptOf:\n%s", got)
	}
	if got := adoptWarnings(driftOf(t, s, s, notes...).Warnings); got != "users: is UNLOGGED" {
		t.Errorf("DriftOf:\n%s", got)
	}
}

// TestBaselineDoesNotMutate: AdoptOf never writes through want.
func TestBaselineDoesNotMutate(t *testing.T) {
	inspected := schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.Indexes = []migrate.Index{{Name: "user_active", Columns: []string{"email"}, Where: "(status = 'a'::text)"}}
		tb.PrimaryKey = []string{"id", "qty"}
	}))
	want := schemaOf(velox.Postgres, usersOf(velox.Postgres, withIndex("status = 'a'")), migrate.Table{Name: "posts"})
	before := migrate.Clone(want)
	adoptOf(t, inspected, want)
	if !reflect.DeepEqual(want, before) {
		t.Errorf("want changed:\n%+v\nwas\n%+v", want, before)
	}
}

// sqliteDB is a fresh SQLite database made by stmts.
func sqliteDB(t *testing.T, stmts ...string) *sql.DB {
	t.Helper()
	db, _ := freshDB(t, velox.SQLite)
	for _, s := range stmts {
		pgExec(t, db, s)
	}
	return db
}

// sqliteUsers is a SQLite users table: id, the key, and email.
func sqliteUsers() migrate.Table {
	return migrate.Table{Name: "users", PrimaryKey: []string{"id"}, Columns: []migrate.Column{
		{Name: "id", Type: "integer", Identity: true},
		{Name: "email", Type: "text"},
	}}
}

// TestBaselineUnknownTables: a table the schema does not name is not read,
// even one that references a schema table; an index the schema lacks is
// kept.
func TestBaselineUnknownTables(t *testing.T) {
	db := sqliteDB(t,
		`CREATE TABLE users (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, email text NOT NULL)`,
		`CREATE INDEX users_extra ON users (email)`,
		`CREATE TABLE audit (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, user_id integer NOT NULL,
			CONSTRAINT audit_user FOREIGN KEY (user_id) REFERENCES users (id))`)
	snap, ws, err := migrate.Baseline(context.Background(), db, schemaOf(velox.SQLite, sqliteUsers()))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tables) != 1 || snap.Tables[0].Name != "users" {
		t.Fatalf("tables %v", snap.Tables)
	}
	if got := snap.Tables[0].Indexes; len(got) != 1 || got[0].Name != "users_extra" {
		t.Errorf("indexes %v", got)
	}
	if got := adoptWarnings(ws); strings.Contains(got, "audit") {
		t.Errorf("warnings:\n%s", got)
	}
}

// TestBaselineSQLiteVerify: a hand-made database's snapshot, planned from
// empty, makes what it says; so it does with a UNIQUE and a REFERENCES key
// SQLite keeps no name for.
func TestBaselineSQLiteVerify(t *testing.T) {
	ctx := context.Background()
	posts := migrate.Table{Name: "posts", PrimaryKey: []string{"id"},
		Columns: []migrate.Column{
			{Name: "id", Type: "integer", Identity: true},
			{Name: "user_id", Type: "integer"},
			{Name: "editor_id", Type: "integer", Nullable: true},
			{Name: "title", Type: "text", Default: "''"},
		},
		Indexes: []migrate.Index{{Name: "posts_user_id", Columns: []string{"user_id"}}},
		ForeignKeys: []migrate.ForeignKey{{Name: "posts_user_id_fkey", Columns: []string{"user_id"},
			RefTable: migrate.TableRef{Name: "users"}, RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
	}
	want := schemaOf(velox.SQLite, posts, sqliteUsers())
	for name, ddl := range map[string][2]string{
		"named": {
			`CREATE TABLE users (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, email text NOT NULL)`,
			`CREATE TABLE posts (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, user_id integer NOT NULL, editor_id integer,
				title text NOT NULL DEFAULT '',
				CONSTRAINT posts_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE)`,
		},
		"unnamed": {
			`CREATE TABLE users (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, email text NOT NULL UNIQUE)`,
			`CREATE TABLE posts (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, user_id integer NOT NULL,
				editor_id integer REFERENCES users (id), title text NOT NULL DEFAULT '',
				CONSTRAINT posts_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE)`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := sqliteDB(t, ddl[0], ddl[1], `CREATE INDEX posts_user_id ON posts (user_id)`)
			snap, ws, err := migrate.Baseline(ctx, db, want)
			if err != nil {
				t.Fatal(err)
			}
			if len(ws) > 0 {
				t.Errorf("warnings:\n%s", adoptWarnings(ws))
			}
			fsys := dirOf(t, velox.SQLite, step{to: snap, name: "baseline"})
			if name == "unnamed" {
				if ix := table(snap, "users").Indexes; len(ix) != 1 || ix[0].Name != "" || !ix[0].Unique {
					t.Errorf("users' indexes %+v", ix)
				}
				if fks := table(snap, "posts").ForeignKeys; len(fks) != 2 || fks[0].Name != "" {
					t.Errorf("posts' keys %+v", fks)
				}
				text := sqlFiles(fsys)
				if !strings.Contains(text, `UNIQUE ("email")`) || !strings.Contains(text, `FOREIGN KEY ("editor_id") REFERENCES "users" ("id")`) ||
					strings.Contains(text, `""`) {
					t.Errorf("the files:\n%s", text)
				}
			}
			fresh, _ := freshDB(t, velox.SQLite)
			if err := migrate.Verify(ctx, fresh, fsys); err != nil {
				t.Errorf("Verify: %v\n%s", err, sqlFiles(fsys))
			}
			noChanges(t, db, snap)
		})
	}
}

// noChanges checks that Drift of db against snap finds no change.
func noChanges(t *testing.T, db *sql.DB, snap *migrate.Schema) {
	t.Helper()
	r, err := migrate.Drift(context.Background(), db, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) > 0 {
		t.Errorf("Drift against the snapshot:\n%s", reportText(r))
	}
}

// TestBaselineSQLiteConstraintIndex: a UNIQUE constraint's index, adopted
// unnamed, is dropped or renamed by the next diff through a rebuild of its
// table, never a DROP INDEX, and the result applies with no drift; a
// schema that names it by definition does not name it in the snapshot.
func TestBaselineSQLiteConstraintIndex(t *testing.T) {
	ctx := context.Background()
	named := sqliteUsers()
	named.Indexes = []migrate.Index{{Name: "users_email", Columns: []string{"email"}, Unique: true}}
	for name, c := range map[string]struct{ baseline, next migrate.Table }{
		"drop":                {sqliteUsers(), sqliteUsers()},
		"rename-to-named":     {sqliteUsers(), named},
		"named-by-the-schema": {named, named},
	} {
		t.Run(name, func(t *testing.T) {
			db := sqliteDB(t, `CREATE TABLE users (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, email text NOT NULL UNIQUE)`)
			snap, _, err := migrate.Baseline(ctx, db, schemaOf(velox.SQLite, c.baseline))
			if err != nil {
				t.Fatal(err)
			}
			if ix := table(snap, "users").Indexes; len(ix) != 1 || ix[0].Name != "" {
				t.Fatalf("the snapshot's indexes %+v", ix)
			}
			noChanges(t, db, snap)
			next := schemaOf(velox.SQLite, c.next)
			fsys := extend(t, dirOf(t, velox.SQLite, step{to: snap, name: "baseline"}), velox.SQLite, step{to: next, name: "next"})
			text := sqlFiles(fsys)
			if strings.Contains(text, "DROP INDEX") || !strings.Contains(text, "sqlite-rebuild") {
				t.Errorf("the files:\n%s", text)
			}
			fresh, _ := freshDB(t, velox.SQLite)
			if _, err := migrate.Up(ctx, fresh, fsys); err != nil {
				t.Fatalf("Up: %v\n%s", err, text)
			}
			noChanges(t, fresh, next)
		})
	}
}

// TestBaselineSQLiteUnnamedRefusals: keys SQLite keeps no name for are
// named by their columns in the refusals.
func TestBaselineSQLiteUnnamedRefusals(t *testing.T) {
	db := sqliteDB(t, `CREATE TABLE orgs (id integer NOT NULL PRIMARY KEY AUTOINCREMENT)`,
		`CREATE TABLE users (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, email text NOT NULL,
			a integer REFERENCES orgs (id), b integer REFERENCES orgs (id))`)
	want := sqliteUsers()
	want.Columns = append(want.Columns, migrate.Column{Name: "a", Type: "integer", Nullable: true},
		migrate.Column{Name: "b", Type: "integer", Nullable: true})
	_, _, err := migrate.Baseline(context.Background(), db, schemaOf(velox.SQLite, want))
	reason := ": references orgs, which the schema does not name; add the table to the schema, or drop the key first"
	if got, want := fmt.Sprint(err), "migrate: users.(a)"+reason+"\nmigrate: users.(b)"+reason; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestBaselinePostgres: what PostgreSQL's baseline warns about and refuses,
// and that its snapshot, planned from empty, makes what it says.
func TestBaselinePostgres(t *testing.T) {
	needPostgres(t)
	ctx := context.Background()
	db, _ := freshDB(t, velox.Postgres)
	for _, q := range []string{
		`CREATE TABLE accounts (id bigint GENERATED BY DEFAULT AS IDENTITY (START WITH 1000) PRIMARY KEY, name text NOT NULL)`,
		`CREATE SCHEMA billing`,
		`CREATE TABLE billing.invoices (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			account_id bigint NOT NULL CONSTRAINT invoices_account_id_fkey REFERENCES accounts (id))`,
	} {
		pgExec(t, db, q)
	}
	identity := func(name string) migrate.Column {
		return migrate.Column{Name: "id", Type: "bigint", Identity: true, Sequence: name + "_id_seq"}
	}
	want := schemaOf(velox.Postgres,
		migrate.Table{Name: "accounts", PrimaryKey: []string{"id"}, PrimaryKeyName: "accounts_pkey",
			Columns: []migrate.Column{identity("accounts"), {Name: "name", Type: "text"}}},
		migrate.Table{Schema: "billing", Name: "invoices", PrimaryKey: []string{"id"}, PrimaryKeyName: "invoices_pkey",
			Columns: []migrate.Column{identity("invoices"), {Name: "account_id", Type: "bigint"}},
			ForeignKeys: []migrate.ForeignKey{{Name: "invoices_account_id_fkey", Columns: []string{"account_id"},
				RefTable: migrate.TableRef{Name: "accounts"}, RefColumns: []string{"id"}}}},
	)
	wantWarnings := []string{"accounts.id: the sequence accounts_id_seq starts at 1000 and increments by 1; 0001_baseline.sql creates it with the defaults"}
	citext := true
	t.Run("extension", func(t *testing.T) {
		if _, err := db.Exec(`CREATE EXTENSION citext`); err != nil {
			citext = false
			t.Skipf("CREATE EXTENSION citext: %v", err)
		}
		pgExec(t, db, `CREATE TABLE tags (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, name citext NOT NULL, alias citext)`)
		want.Tables = append(want.Tables, migrate.Table{Name: "tags", PrimaryKey: []string{"id"}, PrimaryKeyName: "tags_pkey",
			Columns: []migrate.Column{identity("tags"), {Name: "name", Type: "citext"}, {Name: "alias", Type: "citext", Nullable: true}}})
		wantWarnings = append(wantWarnings, "tags: the type citext comes from the extension citext; add CREATE EXTENSION citext to 0001_baseline.sql before the tables, then run migrate hash")
	})

	snap, ws, err := migrate.Baseline(ctx, db, want)
	if err != nil {
		t.Fatal(err)
	}
	if got := warningStrings(ws); !slices.Equal(got, wantWarnings) {
		t.Errorf("warnings:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantWarnings, "\n"))
	}
	noChanges(t, db, snap)
	fsys := dirOf(t, velox.Postgres, step{to: snap, name: "baseline"})
	fresh, _ := freshDB(t, velox.Postgres)
	if citext {
		pgExec(t, fresh, `CREATE EXTENSION citext`)
	}
	mustUp(t, fresh, fsys)
	r, err := migrate.Drift(ctx, fresh, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) > 0 {
		t.Errorf("Drift:\n%s\nfiles:\n%s", reportText(r), sqlFiles(fsys))
	}

	// A nextval of a sequence the column does not own.
	other, _ := freshDB(t, velox.Postgres)
	pgExec(t, other, `CREATE SEQUENCE shared_seq`)
	pgExec(t, other, `CREATE TABLE counters (id bigint NOT NULL DEFAULT nextval('shared_seq') PRIMARY KEY)`)
	_, _, err = migrate.Baseline(ctx, other, schemaOf(velox.Postgres, migrate.Table{Name: "counters", PrimaryKey: []string{"id"},
		PrimaryKeyName: "counters_pkey", Columns: []migrate.Column{{Name: "id", Type: "bigint", Default: "nextval('shared_seq')"}}}))
	if e, ok := err.(*migrate.Error); !ok || e.Code != "baseline" || e.Object != "counters.id" ||
		!strings.Contains(e.Reason, "ALTER SEQUENCE shared_seq OWNED BY counters.id") {
		t.Errorf("an unowned sequence: %v", err)
	}
}

// TestBaselineNil: a nil schema or database is Code "nil".
func TestBaselineNil(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	for _, c := range []struct {
		db   *sql.DB
		want *migrate.Schema
	}{{db, nil}, {nil, schemaOf(velox.SQLite, sqliteUsers())}} {
		if _, _, err := migrate.Baseline(context.Background(), c.db, c.want); err == nil || err.(*migrate.Error).Code != "nil" {
			t.Errorf("Baseline: %v", err)
		}
	}
}

// TestAdoptKeepsEnumTypes: an enum type the schema names that the database
// has is in the snapshot, with the database's values, though the table
// using it is missing; the next diff creates only the table.
func TestAdoptKeepsEnumTypes(t *testing.T) {
	inspected := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))
	inspected.Enums = []migrate.Enum{{Name: "posts_state", Values: []string{"draft", "live"}}}
	want := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil), migrate.Table{Name: "posts",
		Columns: []migrate.Column{{Name: "state", Type: `"posts_state"`}}})
	want.Enums = []migrate.Enum{{Name: "posts_state", Values: []string{"draft", "live"}}}
	snap, ws := adoptOf(t, inspected, want)
	if !reflect.DeepEqual(snap.Enums, inspected.Enums) {
		t.Errorf("enums %+v", snap.Enums)
	}
	if got := warningObjects(ws); !slices.Equal(got, []string{"posts"}) {
		t.Errorf("warnings:\n%s", adoptWarnings(ws))
	}
	cs, err := migrate.Diff(snap, want, migrate.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := changeStrings(cs); !slices.Equal(got, []string{"create-table posts"}) {
		t.Errorf("the next diff: %v", got)
	}
}

// TestAdoptWarningsNameTheSchema: a warning of Baseline's names the schema
// where Drift's names the snapshot.
func TestAdoptWarningsNameTheSchema(t *testing.T) {
	inspected := schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) { tb.PrimaryKey = []string{"id", "qty"} }))
	want := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))
	_, ws := adoptOf(t, inspected, want)
	if got := adoptWarnings(ws); got != "users: the database has the primary key (id, qty) and the schema the primary key (id); change it by hand" {
		t.Errorf("AdoptOf:\n%s", got)
	}
	if got := adoptWarnings(driftOf(t, inspected, want).Warnings); got != "users: the database has the primary key (id, qty) and the snapshot the primary key (id); change it by hand" {
		t.Errorf("DriftOf:\n%s", got)
	}
}

// TestAdoptUnnamedCheck: a check SQLite keeps no name for is named in a
// warning by its place in the table.
func TestAdoptUnnamedCheck(t *testing.T) {
	withCheck := func(expr string) *migrate.Schema {
		tb := sqliteUsers()
		tb.Checks = []migrate.Check{{Expr: expr}}
		return schemaOf(velox.SQLite, tb)
	}
	_, ws := adoptOf(t, withCheck("email || 'x' <> 'y'"), withCheck("email || 'z' <> 'y'"))
	if got := warningObjects(ws); !slices.Equal(got, []string{"users.check#1"}) {
		t.Errorf("warnings:\n%s", adoptWarnings(ws))
	}
}
