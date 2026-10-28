package migrate_test

import (
	"cmp"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// usersOf is a users table of dialect d, edited by edit.
func usersOf(d velox.Dialect, edit func(*migrate.Table)) migrate.Table {
	tb := migrate.Table{
		Name: "users",
		Columns: []migrate.Column{
			{Name: "id", Type: "bigint", Identity: true},
			{Name: "email", Type: "text"},
			{Name: "status", Type: "text"},
			{Name: "qty", Type: "integer"},
		},
		PrimaryKey: []string{"id"},
	}
	switch d {
	case velox.Postgres:
		tb.PrimaryKeyName = "users_pkey"
	case velox.MySQL:
		tb.PrimaryKeyName = "PRIMARY"
		tb.Columns[1].Type, tb.Columns[2].Type = "varchar(255)", "varchar(255)"
	}
	if edit != nil {
		edit(&tb)
	}
	return tb
}

func schemaOf(d velox.Dialect, ts ...migrate.Table) *migrate.Schema {
	return &migrate.Schema{Dialect: d, Tables: ts}
}

func driftOf(t *testing.T, inspected, snapshot *migrate.Schema, notes ...migrate.Note) *migrate.DriftReport {
	t.Helper()
	r, err := migrate.DriftOf(inspected, snapshot, notes...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func changeStrings(cs []migrate.Change) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.String()
	}
	return out
}

func warningStrings(ws []migrate.Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.String()
	}
	return out
}

// wantDrift checks r's changes, as their String, and its warnings' objects.
func wantDrift(t *testing.T, r *migrate.DriftReport, changes []string, objects ...string) {
	t.Helper()
	if got := changeStrings(r.Changes); !slices.Equal(got, changes) {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(changes, "\n"))
	}
	var got []string
	for _, w := range r.Warnings {
		got = append(got, w.Object)
	}
	if !slices.Equal(got, objects) {
		t.Errorf("warnings:\n%s\nwant on %q", strings.Join(warningStrings(r.Warnings), "\n"), objects)
	}
}

func TestDriftSelf(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		for _, s := range []*migrate.Schema{from, to} {
			r := driftOf(t, s, s)
			if len(r.Changes) > 0 || len(r.Warnings) > 0 {
				t.Errorf("DriftOf(x, x):\n%s\n%s", strings.Join(changeStrings(r.Changes), "\n"), strings.Join(warningStrings(r.Warnings), "\n"))
			}
		}
	})
}

func withIndex(where string) func(*migrate.Table) {
	return func(tb *migrate.Table) {
		tb.Indexes = []migrate.Index{{Name: "users_active", Columns: []string{"email"}, Where: where}}
	}
}

func TestDriftPredicateSpelling(t *testing.T) {
	snap := schemaOf(velox.Postgres, usersOf(velox.Postgres, withIndex(`status = 'active'`)))
	db := func(where string) *migrate.Schema {
		return schemaOf(velox.Postgres, usersOf(velox.Postgres, withIndex(where)))
	}
	wantDrift(t, driftOf(t, db(`(status = 'active'::text)`), snap), nil)
	// A predicate whose normal form differs is another index.
	wantDrift(t, driftOf(t, db(`(status = 'banned'::text)`), snap),
		[]string{"drop-index users.users_active", "add-index users.users_active"})
	wantDrift(t, driftOf(t, db(""), snap), []string{"drop-index users.users_active", "add-index users.users_active"})
	// A predicate the normaliser cannot read is not compared: a warning
	// with both texts, and no change.
	snap = schemaOf(velox.Postgres, usersOf(velox.Postgres, withIndex(`status || 'x' = 'ax'`)))
	r := driftOf(t, db(`((status || 'x'::text) = 'ax'::text)`), snap)
	wantDrift(t, r, nil, "users.users_active")
	if m := r.Warnings[0].Message; !strings.Contains(m, `((status || 'x'::text) = 'ax'::text)`) || !strings.Contains(m, `status || 'x' = 'ax'`) {
		t.Errorf("the warning does not hold both texts: %s", m)
	}
	// Only white space apart, it is the same text.
	wantDrift(t, driftOf(t, db(`status  ||  'x' = 'ax'`), snap), nil)
}

func TestDriftDefaultExpression(t *testing.T) {
	withDefault := func(def string) *migrate.Schema {
		return schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
			tb.Columns = append(tb.Columns, migrate.Column{Name: "created_at", Type: "timestamp with time zone", Default: def})
		}))
	}
	r := driftOf(t, withDefault("now()"), withDefault("CURRENT_TIMESTAMP"))
	wantDrift(t, r, nil, "users.created_at")
	if m := r.Warnings[0].Message; !strings.Contains(m, "now()") || !strings.Contains(m, "CURRENT_TIMESTAMP") {
		t.Errorf("the warning does not name both defaults: %s", m)
	}
	wantDrift(t, driftOf(t, withDefault("'x'"), withDefault("'y'")), []string{"alter-column users.created_at (default)"})
	wantDrift(t, driftOf(t, withDefault("now()"), withDefault("'y'")), []string{"alter-column users.created_at (default)"})
	wantDrift(t, driftOf(t, withDefault(""), withDefault("now()")), []string{"alter-column users.created_at (default)"})
	// What only the normaliser removes is not worth a warning.
	wantDrift(t, driftOf(t, withDefault("NOW()"), withDefault("now()")), nil)
	// The regclass cast PostgreSQL adds is no difference; another cast is.
	wantDrift(t, driftOf(t, withDefault("nextval('users_id_seq'::regclass)"), withDefault("nextval('users_id_seq')")), nil)
	wantDrift(t, driftOf(t, withDefault("now()::date"), withDefault("now()")), nil, "users.created_at")
}

func TestDriftEnumCheck(t *testing.T) {
	withRole := func(values string) *migrate.Schema {
		return schemaOf(velox.SQLite, usersOf(velox.SQLite, func(tb *migrate.Table) {
			tb.Columns = append(tb.Columns, migrate.Column{Name: "role", Type: "text"})
			tb.Checks = []migrate.Check{{Name: "users_role_check", Expr: `"role" IN (` + values + `)`}}
		}))
	}
	wantDrift(t, driftOf(t, withRole(`'a', 'b'`), withRole(`'a', 'b', 'c'`)),
		[]string{"drop-check users.users_role_check", "add-check users.users_role_check"})
	// The same values, spelled apart, are one check.
	wantDrift(t, driftOf(t, withRole(`'a','b'`), withRole(`'a', 'b'`)), nil)
}

func TestDriftCheckSpelling(t *testing.T) {
	withCheck := func(expr string) *migrate.Schema {
		return schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
			tb.Checks = []migrate.Check{{Name: "users_qty_check", Expr: expr}}
		}))
	}
	snap := withCheck("qty > 0")
	wantDrift(t, driftOf(t, withCheck("(qty > 0)"), snap), nil)
	wantDrift(t, driftOf(t, withCheck("(qty > 5)"), snap),
		[]string{"drop-check users.users_qty_check", "add-check users.users_qty_check"})
	// One the normaliser cannot read is warned of, not compared.
	wantDrift(t, driftOf(t, withCheck("((email || 'x'::text) <> 'y'::text)"), withCheck("email || 'x' <> 'y'")), nil, "users.users_qty_check")
}

func TestDriftMySQLRestrict(t *testing.T) {
	posts := func(onDelete string) migrate.Table {
		return migrate.Table{
			Name:           "posts",
			Columns:        []migrate.Column{{Name: "id", Type: "bigint", Identity: true}, {Name: "user_id", Type: "bigint"}},
			PrimaryKey:     []string{"id"},
			PrimaryKeyName: "PRIMARY",
			Indexes:        []migrate.Index{{Name: "posts_user_id", Columns: []string{"user_id"}}},
			ForeignKeys: []migrate.ForeignKey{{
				Name: "posts_users_posts", Columns: []string{"user_id"}, RefTable: users, RefColumns: []string{"id"}, OnDelete: onDelete,
			}},
		}
	}
	of := func(onDelete string) *migrate.Schema {
		return schemaOf(velox.MySQL, posts(onDelete), usersOf(velox.MySQL, nil))
	}
	wantDrift(t, driftOf(t, of("RESTRICT"), of("")), nil)
	wantDrift(t, driftOf(t, of(""), of("RESTRICT")), nil)
	wantDrift(t, driftOf(t, of("CASCADE"), of("")),
		[]string{"drop-foreign-key posts.posts_users_posts", "add-foreign-key posts.posts_users_posts"})
	// PostgreSQL's RESTRICT is not its NO ACTION.
	pgOf := func(onDelete string) *migrate.Schema {
		p := posts(onDelete)
		p.PrimaryKeyName = "posts_pkey"
		return schemaOf(velox.Postgres, p, usersOf(velox.Postgres, nil))
	}
	wantDrift(t, driftOf(t, pgOf("RESTRICT"), pgOf("")),
		[]string{"drop-foreign-key posts.posts_users_posts", "add-foreign-key posts.posts_users_posts"})
}

func TestDriftUnnamedConstraints(t *testing.T) {
	posts := func(named bool, extra bool) migrate.Table {
		name := func(s string) string {
			if named {
				return s
			}
			return ""
		}
		tb := migrate.Table{
			Name:        "posts",
			Columns:     []migrate.Column{{Name: "id", Type: "integer", Identity: true}, {Name: "user_id", Type: "integer"}, {Name: "qty", Type: "integer"}},
			PrimaryKey:  []string{"id"},
			Indexes:     []migrate.Index{{Name: name("posts_user_id"), Columns: []string{"user_id"}, Where: "qty > 0"}},
			ForeignKeys: []migrate.ForeignKey{{Name: name("posts_users_posts"), Columns: []string{"user_id"}, RefTable: users, RefColumns: []string{"id"}}},
			Checks:      []migrate.Check{{Name: name("posts_qty_check"), Expr: "qty >= 0"}},
		}
		if extra {
			tb.Checks = append(tb.Checks, migrate.Check{Name: "", Expr: "qty < 100"})
		}
		return tb
	}
	snap := schemaOf(velox.SQLite, posts(true, false), usersOf(velox.SQLite, nil))
	wantDrift(t, driftOf(t, schemaOf(velox.SQLite, posts(false, false), usersOf(velox.SQLite, nil)), snap), nil)
	// One the snapshot has no match for is a change.
	wantDrift(t, driftOf(t, schemaOf(velox.SQLite, posts(true, true), usersOf(velox.SQLite, nil)), snap), []string{"drop-check posts.check (qty < 100)"})
	// Several unnamed ones on a table are allowed.
	many := posts(true, true)
	many.Checks = append(many.Checks, migrate.Check{Expr: "qty < 50"})
	many.ForeignKeys = append(many.ForeignKeys, migrate.ForeignKey{Columns: []string{"qty"}, RefTable: users, RefColumns: []string{"id"}})
	wantDrift(t, driftOf(t, schemaOf(velox.SQLite, many, usersOf(velox.SQLite, nil)), snap),
		[]string{"drop-foreign-key posts.(qty)", "drop-check posts.check (qty < 100)", "drop-check posts.check (qty < 50)"})
	// An unnamed one of another definition is not given the name.
	other := posts(false, false)
	other.ForeignKeys[0].OnDelete = "CASCADE"
	wantDrift(t, driftOf(t, schemaOf(velox.SQLite, other, usersOf(velox.SQLite, nil)), snap),
		[]string{"drop-foreign-key posts.(user_id)", "add-foreign-key posts.posts_users_posts"})
}

func TestDriftPrimaryKey(t *testing.T) {
	snap := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))
	r := driftOf(t, schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) { tb.PrimaryKey = []string{"id", "email"} })), snap)
	wantDrift(t, r, nil, "users")
	if m := r.Warnings[0].Message; !strings.Contains(m, "(id, email)") || !strings.Contains(m, "(id)") {
		t.Errorf("the warning does not name both keys: %s", m)
	}
	wantDrift(t, driftOf(t, schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) { tb.PrimaryKeyName = "" })), snap), nil, "users")
	// Two names are a rename, which Diff plans.
	wantDrift(t, driftOf(t, schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) { tb.PrimaryKeyName = "users_pk" })), snap),
		[]string{"rename-constraint users.users_pk → users_pkey"})
}

func TestDriftNotes(t *testing.T) {
	for _, n := range []migrate.Note{
		{Table: users, Object: "users", Message: "table users uses engine MyISAM, not InnoDB"},
		{Table: users, Object: "users.users_x", Message: "index users_x is invalid: a CREATE INDEX CONCURRENTLY failed; run migrate apply -resume, or drop it"},
		{Table: users, Object: "users.users_qty_check", Message: "constraint users_qty_check is NOT VALID"},
		{Table: users, Object: "users.id", Message: "column id is GENERATED ALWAYS, the model has BY DEFAULT"},
	} {
		s := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))
		r := driftOf(t, s, s, n)
		wantDrift(t, r, nil, n.Object)
		if len(r.Warnings) == 1 && r.Warnings[0].Message != n.Message {
			t.Errorf("message %q, want %q", r.Warnings[0].Message, n.Message)
		}
		r = driftOf(t, s, s, migrate.Note{Table: migrate.TableRef{Name: "legacy"}, Object: "legacy", Message: n.Message})
		wantDrift(t, r, nil)
		wantDrift(t, driftOf(t, s, s), nil)
	}
}

func TestDriftUnknownTables(t *testing.T) {
	legacy := migrate.Table{
		Name:           "legacy",
		Columns:        []migrate.Column{{Name: "id", Type: "bigint"}, {Name: "mood", Type: `"legacy_mood"`}},
		PrimaryKey:     []string{"id"},
		PrimaryKeyName: "legacy_pkey",
		Indexes:        []migrate.Index{{Name: "legacy_mood", Columns: []string{"mood"}}},
	}
	snap := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))
	db := schemaOf(velox.Postgres, legacy, usersOf(velox.Postgres, nil))
	db.Enums = []migrate.Enum{{Name: "legacy_mood", Values: []string{"a"}}}
	wantDrift(t, driftOf(t, db, snap), nil)

	posts := migrate.Table{Name: "posts", Columns: []migrate.Column{{Name: "id", Type: "bigint"}}, PrimaryKey: []string{"id"}, PrimaryKeyName: "posts_pkey"}
	wantDrift(t, driftOf(t, db, schemaOf(velox.Postgres, posts, usersOf(velox.Postgres, nil))), []string{"create-table posts"})

	extra := schemaOf(velox.Postgres, usersOf(velox.Postgres, withIndex("")))
	wantDrift(t, driftOf(t, extra, snap), []string{"drop-index users.users_active"})

	// An enum type a snapshot table uses is compared.
	mood := func(values ...string) *migrate.Schema {
		s := schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
			tb.Columns = append(tb.Columns, migrate.Column{Name: "mood", Type: `"mood"`})
		}))
		s.Enums = []migrate.Enum{{Name: "mood", Values: values}}
		return s
	}
	wantDrift(t, driftOf(t, mood("a"), mood("a", "b")), []string{"alter-enum mood (a → a, b)"})
}

// spelled is a pair of schemas apart in every spelling drift copies over.
func spelled() (db, snap *migrate.Schema) {
	db = schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.PrimaryKey = []string{"id", "email"}
		tb.Columns = append(tb.Columns, migrate.Column{Name: "created_at", Type: "timestamp with time zone", Default: "now()"})
		tb.Indexes = []migrate.Index{{Name: "users_active", Columns: []string{"email"}, Where: `(status = 'active'::text)`}}
		tb.Checks = []migrate.Check{{Name: "users_qty_check", Expr: "(qty > 0)"}, {Name: "users_a_check", Expr: "(qty < 9)"}}
	}))
	snap = schemaOf(velox.Postgres, usersOf(velox.Postgres, func(tb *migrate.Table) {
		tb.Columns = append(tb.Columns, migrate.Column{Name: "created_at", Type: "timestamp with time zone", Default: "CURRENT_TIMESTAMP"})
		tb.Indexes = []migrate.Index{{Name: "users_active", Columns: []string{"email"}, Where: `status = 'active'`}}
		tb.Checks = []migrate.Check{{Name: "users_qty_check", Expr: "qty > 0"}, {Name: "users_a_check", Expr: "qty < 9"}}
	}))
	return db, snap
}

func TestDriftOfDoesNotMutate(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		f, w := migrate.Clone(from), migrate.Clone(to)
		driftOf(t, from, to)
		if !reflect.DeepEqual(from, f) || !reflect.DeepEqual(to, w) {
			t.Error("DriftOf changed its arguments")
		}
	})
	db, snap := spelled()
	d, s := migrate.Clone(db), migrate.Clone(snap)
	if r := driftOf(t, db, snap); len(r.Warnings) == 0 {
		t.Fatal("no warning: the schemas are not apart")
	}
	if !reflect.DeepEqual(db, d) || !reflect.DeepEqual(snap, s) {
		t.Error("DriftOf changed its arguments")
	}
}

func TestDriftDeterministic(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		a, b := driftOf(t, from, to), driftOf(t, from, to)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("two calls differ:\n%s\n--\n%s", strings.Join(changeStrings(a.Changes), "\n"), strings.Join(changeStrings(b.Changes), "\n"))
		}
	})
	db, snap := spelled()
	notes := []migrate.Note{
		{Table: users, Object: "users.users_x", Message: "b"},
		{Table: users, Object: "users.users_x", Message: "a"},
		{Table: users, Object: "users", Message: "z"},
	}
	a, b := driftOf(t, db, snap, notes...), driftOf(t, db, snap, notes...)
	if !reflect.DeepEqual(a, b) {
		t.Error("two calls differ")
	}
	sorted := slices.IsSortedFunc(a.Warnings, func(x, y migrate.Warning) int {
		return cmp.Or(cmp.Compare(x.Object, y.Object), cmp.Compare(x.Message, y.Message))
	})
	if !sorted || len(a.Warnings) != 4 {
		t.Errorf("warnings not sorted by object and message, or not 4:\n%s", strings.Join(warningStrings(a.Warnings), "\n"))
	}
	if !slices.Contains(warningStrings(a.Warnings), "users.users_x: b; a") {
		t.Errorf("the notes on users.users_x are not one warning, b; a:\n%s", strings.Join(warningStrings(a.Warnings), "\n"))
	}
	if len(a.Changes) > 0 {
		t.Errorf("changes: %s", strings.Join(changeStrings(a.Changes), "\n"))
	}
}

// TestDriftNotesMerged: the notes on one object are one warning, joined
// with "; also, " in their order; baseline notes count only when adopting; a
// warning of drift's own stays apart.
func TestDriftNotesMerged(t *testing.T) {
	s := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))
	identity := "the column is GENERATED ALWAYS AS IDENTITY, which the model holds as BY DEFAULT: a value given on insert is refused"
	sequence := "the sequence users_id_seq starts at 5 and increments by 1; 0001_baseline.sql creates it with the defaults"

	r := driftOf(t, s, s, migrate.Note{Table: users, Object: "users.id", Message: "a"}, migrate.Note{Table: users, Object: "users.id", Message: "b"})
	if got := warningStrings(r.Warnings); !slices.Equal(got, []string{"users.id: a; also, b"}) {
		t.Errorf("drift: %q, want one warning users.id: a; also, b", got)
	}

	notes := []migrate.Note{
		{Table: users, Object: "users.id", Message: identity},
		{Table: users, Object: "users.id", Message: sequence, Baseline: true},
	}
	_, ws := adoptOf(t, s, s, notes...)
	if got, want := warningStrings(ws), []string{"users.id: " + identity + "; also, " + sequence}; !slices.Equal(got, want) {
		t.Errorf("adopt: %q, want %q", got, want)
	}
	r = driftOf(t, s, s, notes...)
	if got, want := warningStrings(r.Warnings), []string{"users.id: " + identity}; !slices.Equal(got, want) {
		t.Errorf("drift with a baseline note: %q, want %q", got, want)
	}

	db, snap := spelled()
	r = driftOf(t, db, snap, migrate.Note{Table: users, Object: "users", Message: "z"})
	var own []string
	for _, w := range r.Warnings {
		if w.Object == "users" {
			own = append(own, w.String())
		}
	}
	if len(own) != 2 || !strings.HasPrefix(own[0], "users: the database has the primary key") || own[1] != "users: z" {
		t.Errorf("drift's own warning and a note on users:\n%s", strings.Join(own, "\n"))
	}
}

func TestDriftErrors(t *testing.T) {
	pg := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))
	twice := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil), usersOf(velox.Postgres, nil))
	for _, c := range []struct {
		name                string
		inspected, snapshot *migrate.Schema
		code                string
	}{
		{"nil inspected", nil, pg, "nil"},
		{"nil snapshot", pg, nil, "nil"},
		{"two dialects", schemaOf(velox.SQLite, usersOf(velox.SQLite, nil)), pg, "dialect"},
		{"unknown dialect", schemaOf(velox.Dialect(99)), schemaOf(velox.Dialect(99)), "dialect"},
		{"unknown inspected dialect", schemaOf(velox.Dialect(99)), pg, "dialect"},
		{"snapshot twice", pg, twice, "schema"},
		{"inspected twice", twice, pg, "schema"},
	} {
		_, err := migrate.DriftOf(c.inspected, c.snapshot)
		var e *migrate.Error
		if !errors.As(err, &e) || e.Code != c.code {
			t.Errorf("%s: %v, want code %q", c.name, err, c.code)
		}
	}
}

func TestChangeStrings(t *testing.T) {
	billing := migrate.TableRef{Schema: "billing", Name: "invoices"}
	col := migrate.Column{Name: "email"}
	for _, c := range []struct {
		c    migrate.Change
		want string
	}{
		{migrate.CreateTable{Table: migrate.Table{Name: "users"}}, "create-table users"},
		{migrate.DropTable{Table: migrate.Table{Schema: "billing", Name: "invoices"}}, "drop-table billing.invoices"},
		{migrate.RenameTable{From: users, To: accounts}, "rename-table users → accounts"},
		{migrate.AddColumn{Table: users, Column: col}, "add-column users.email"},
		{migrate.DropColumn{Table: billing, Column: col}, "drop-column billing.invoices.email"},
		{migrate.RenameColumn{Table: users, From: "name", To: "full_name"}, "rename-column users.name → full_name"},
		{migrate.AlterColumn{Table: users, From: migrate.Column{Name: "name"}, To: migrate.Column{Name: "name"}, What: migrate.ColumnType | migrate.ColumnNullable}, "alter-column users.name (type|nullable)"},
		{migrate.RenameSequence{Table: users, Column: "id", From: "users_id_seq", To: "accounts_id_seq"}, "rename-sequence users.id (users_id_seq → accounts_id_seq)"},
		{migrate.AddIndex{Table: users, Index: migrate.Index{Name: "users_email"}}, "add-index users.users_email"},
		{migrate.DropIndex{Table: users, Index: migrate.Index{Name: "users_email"}}, "drop-index users.users_email"},
		{migrate.RenameIndex{Table: users, From: "users_a", To: "users_b"}, "rename-index users.users_a → users_b"},
		{migrate.AddForeignKey{Table: users, ForeignKey: migrate.ForeignKey{Name: "users_org"}}, "add-foreign-key users.users_org"},
		{migrate.DropForeignKey{Table: users, ForeignKey: migrate.ForeignKey{Name: "users_org"}}, "drop-foreign-key users.users_org"},
		{migrate.AddCheck{Table: users, Check: migrate.Check{Name: "users_role_check"}}, "add-check users.users_role_check"},
		{migrate.DropCheck{Table: users, Check: migrate.Check{Name: "users_role_check"}}, "drop-check users.users_role_check"},
		{migrate.RenameConstraint{Table: users, From: "users_pkey", To: "accounts_pkey"}, "rename-constraint users.users_pkey → accounts_pkey"},
		{migrate.SetTableComment{Table: users, From: "", To: "people"}, "set-table-comment users"},
		{migrate.SetTableCollation{Table: users, From: "a", To: "b"}, "set-table-collation users"},
		{migrate.CreateEnum{Enum: migrate.Enum{Schema: "billing", Name: "mood"}}, "create-enum billing.mood"},
		{migrate.DropEnum{Enum: migrate.Enum{Name: "mood"}}, "drop-enum mood"},
		{migrate.RenameEnum{From: migrate.TableRef{Name: "mood"}, To: migrate.TableRef{Name: "feeling"}}, "rename-enum mood → feeling"},
		{migrate.AlterEnum{From: migrate.Enum{Name: "mood", Values: []string{"a", "b"}}, To: migrate.Enum{Name: "mood", Values: []string{"b", "c"}}}, "alter-enum mood (a, b → b, c)"},
	} {
		if got := c.c.String(); got != c.want {
			t.Errorf("%s: %q, want %q", c.c.Kind(), got, c.want)
		}
	}
	if got := (migrate.Warning{Object: "users.users_active", Message: "m"}).String(); got != "users.users_active: m" {
		t.Errorf("Warning.String: %q", got)
	}
}
