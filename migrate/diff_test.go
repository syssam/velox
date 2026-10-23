package migrate_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// line is a change in one line, for the tests' messages and comparisons.
func line(c migrate.Change) string {
	s := fmt.Sprintf("%s %v", c.Kind(), c.Target())
	switch c := c.(type) {
	case migrate.RenameTable:
		s += fmt.Sprintf(" %v %v", c.From, c.To)
	case migrate.AddColumn:
		s += " " + c.Column.Name
	case migrate.DropColumn:
		s += " " + c.Column.Name
	case migrate.RenameColumn:
		s += " " + c.From + " " + c.To
	case migrate.AlterColumn:
		s += fmt.Sprintf(" %s %b", c.To.Name, c.What)
	case migrate.RenameSequence:
		s += " " + c.Column + " " + c.From + " " + c.To
	case migrate.AddIndex:
		s += " " + c.Index.Name
	case migrate.DropIndex:
		s += " " + c.Index.Name
	case migrate.RenameIndex:
		s += " " + c.From + " " + c.To
	case migrate.AddForeignKey:
		s += " " + c.ForeignKey.Name
	case migrate.DropForeignKey:
		s += " " + c.ForeignKey.Name
	case migrate.AddCheck:
		s += " " + c.Check.Name
	case migrate.DropCheck:
		s += " " + c.Check.Name
	case migrate.RenameConstraint:
		s += " " + c.From + " " + c.To
	case migrate.SetTableComment:
		s += fmt.Sprintf(" %q %q", c.From, c.To)
	case migrate.SetTableCollation:
		s += " " + c.From + " " + c.To
	case migrate.RenameEnum:
		s += fmt.Sprintf(" %v", c.From)
	case migrate.AlterEnum:
		s += fmt.Sprintf(" %q %q", c.From.Values, c.To.Values)
	}
	return s
}

func lines(cs []migrate.Change) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = line(c)
	}
	return out
}

func diff(t *testing.T, from, to *migrate.Schema, opts migrate.DiffOptions) []migrate.Change {
	t.Helper()
	cs, err := migrate.Diff(from, to, opts)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// eachCase runs f on every case on each of its dialects.
func eachCase(t *testing.T, f func(t *testing.T, c testCase, from, to *migrate.Schema)) {
	for _, c := range cases {
		for _, d := range c.on() {
			t.Run(c.name+"/"+d.String(), func(t *testing.T) {
				from, to := models(t, c, d)
				f(t, c, from, to)
			})
		}
	}
}

func TestDiffSelfIsEmpty(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		for _, s := range []*migrate.Schema{from, to} {
			if cs := diff(t, s, s, migrate.DiffOptions{}); len(cs) > 0 {
				t.Errorf("Diff(x, x):\n%s", strings.Join(lines(cs), "\n"))
			}
		}
	})
	paths, err := filepath.Glob("../internal/gen/testdata/tables/*.json")
	if err != nil || len(paths) != 3 {
		t.Fatalf("goldens %v, %v", paths, err)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var s migrate.Schema
		if err := s.UnmarshalJSON(b); err != nil {
			t.Fatal(err)
		}
		if cs := diff(t, &s, &s, migrate.DiffOptions{}); len(cs) > 0 {
			t.Errorf("%s: Diff(x, x):\n%s", p, strings.Join(lines(cs), "\n"))
		}
	}
}

func TestApplyDiffGivesTo(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		cs := diff(t, from, to, c.opts)
		if len(cs) == 0 {
			t.Fatal("no changes: the case changes nothing")
		}
		got, err := migrate.ApplyChanges(from, cs)
		if err != nil {
			t.Fatalf("%v\n%s", err, strings.Join(lines(cs), "\n"))
		}
		if !got.Equal(to) {
			g, _ := got.MarshalJSON()
			w, _ := to.MarshalJSON()
			t.Errorf("from with the changes is not to:\n%s\ngot  %s\nwant %s", strings.Join(lines(cs), "\n"), g, w)
		}
	})
}

func TestDiffDoesNotMutate(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		f, w := migrate.Clone(from), migrate.Clone(to)
		diff(t, from, to, c.opts)
		if !reflect.DeepEqual(from, f) || !reflect.DeepEqual(to, w) {
			t.Error("Diff changed its arguments")
		}
	})
}

func TestDiffDeterministic(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		a, b := diff(t, from, to, c.opts), diff(t, from, to, c.opts)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("two calls differ:\n%s\n--\n%s", strings.Join(lines(a), "\n"), strings.Join(lines(b), "\n"))
		}
	})
}

// caseChanges is the case's changes on d.
func caseChanges(t *testing.T, name string, d velox.Dialect) []migrate.Change {
	t.Helper()
	c := caseNamed(name)
	from, to := models(t, c, d)
	return diff(t, from, to, c.opts)
}

func wantLines(t *testing.T, what string, cs []migrate.Change, want []string) {
	t.Helper()
	if got := lines(cs); !slices.Equal(got, want) {
		t.Errorf("%s:\n%s\nwant\n%s", what, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// orderPair is a pair of PostgreSQL schemas that touches every step.
func orderPair() (from, to *migrate.Schema, opts migrate.DiffOptions) {
	id := func(seq string) migrate.Column {
		return migrate.Column{Name: "id", Type: "bigint", Identity: true, Sequence: seq}
	}
	text := func(name string) migrate.Column { return migrate.Column{Name: name, Type: "text"} }
	from = &migrate.Schema{
		Dialect: velox.Postgres,
		Tables: []migrate.Table{
			{
				Name:       "a",
				Columns:    []migrate.Column{id("a_id_seq_old"), text("x"), {Name: "gone", Type: "bigint"}, text("old_name"), {Name: "mood", Type: `"e_old"`}},
				PrimaryKey: []string{"id"}, PrimaryKeyName: "a_pkey_old",
				Indexes: []migrate.Index{
					{Name: "a_gone", Columns: []string{"gone"}},
					{Name: "a_id_x", Columns: []string{"id", "x"}},
					{Name: "a_x_old", Columns: []string{"x"}},
				},
				ForeignKeys: []migrate.ForeignKey{{Name: "a_gone_fkey", Columns: []string{"gone"}, RefTable: migrate.TableRef{Name: "d"}, RefColumns: []string{"id"}}},
				Checks:      []migrate.Check{{Name: "a_chk", Expr: `"x" <> ''`}},
			},
			{
				Name: "d", Columns: []migrate.Column{id("d_id_seq"), {Name: "x", Type: "bigint"}}, PrimaryKey: []string{"id"}, PrimaryKeyName: "d_pkey",
				ForeignKeys: []migrate.ForeignKey{{Name: "d_x_fkey", Columns: []string{"x"}, RefTable: migrate.TableRef{Name: "d2"}, RefColumns: []string{"id"}}},
			},
			{
				Name: "d2", Columns: []migrate.Column{id("d2_id_seq"), {Name: "y", Type: "bigint"}}, PrimaryKey: []string{"id"}, PrimaryKeyName: "d2_pkey",
				ForeignKeys: []migrate.ForeignKey{{Name: "d2_y_fkey", Columns: []string{"y"}, RefTable: migrate.TableRef{Name: "d"}, RefColumns: []string{"id"}}},
			},
			{
				Name: "r_old", Columns: []migrate.Column{id("r_old_id_seq"), text("v"), text("w")}, PrimaryKey: []string{"id"}, PrimaryKeyName: "r_old_pkey",
				Indexes: []migrate.Index{{Name: "r_old_w", Columns: []string{"w"}}},
			},
		},
		Enums: []migrate.Enum{
			{Name: "e_old", Values: []string{"p", "q"}},
			{Name: "gone", Values: []string{"z"}},
			{Name: "grow", Values: []string{"a", "b"}},
			{Name: "swap", Values: []string{"a", "b"}},
		},
	}
	to = &migrate.Schema{
		Dialect: velox.Postgres,
		Tables: []migrate.Table{
			{
				Name: "a",
				Columns: []migrate.Column{
					id("a_id_seq"), {Name: "x", Type: "text", Nullable: true}, text("new_name"), {Name: "mood", Type: `"e_new"`},
					{Name: "y", Type: "bigint", Nullable: true},
				},
				PrimaryKey: []string{"id"}, PrimaryKeyName: "a_pkey",
				Indexes: []migrate.Index{
					{Name: "a_x_new", Columns: []string{"x"}},
					{Name: "a_y", Columns: []string{"y"}},
				},
				ForeignKeys: []migrate.ForeignKey{{Name: "a_y_fkey", Columns: []string{"y"}, RefTable: migrate.TableRef{Name: "c"}, RefColumns: []string{"id"}}},
				Checks:      []migrate.Check{{Name: "a_chk2", Expr: `"y" > 0`}},
				Comment:     "c", Collation: "utf8mb4_bin",
			},
			{
				Name: "c", Columns: []migrate.Column{id("c_id_seq"), {Name: "x", Type: "bigint"}}, PrimaryKey: []string{"id"}, PrimaryKeyName: "c_pkey",
				ForeignKeys: []migrate.ForeignKey{{Name: "c_x_fkey", Columns: []string{"x"}, RefTable: migrate.TableRef{Name: "c2"}, RefColumns: []string{"id"}}},
			},
			{
				Name: "c2", Columns: []migrate.Column{id("c2_id_seq"), {Name: "y", Type: "bigint"}}, PrimaryKey: []string{"id"}, PrimaryKeyName: "c2_pkey",
				ForeignKeys: []migrate.ForeignKey{{Name: "c2_y_fkey", Columns: []string{"y"}, RefTable: migrate.TableRef{Name: "c"}, RefColumns: []string{"id"}}},
			},
			{
				Name: "r_new", Columns: []migrate.Column{id("r_old_id_seq"), text("v")}, PrimaryKey: []string{"id"}, PrimaryKeyName: "r_old_pkey",
				Indexes: []migrate.Index{{Name: "r_new_v", Columns: []string{"v"}}},
			},
		},
		Enums: []migrate.Enum{
			{Name: "e_new", Values: []string{"p", "q"}},
			{Name: "fresh", Values: []string{"f"}},
			{Name: "grow", Values: []string{"a", "b", "c"}},
			{Name: "swap", Values: []string{"b", "a"}},
		},
	}
	opts = migrate.DiffOptions{
		RenameTables:  map[migrate.TableRef]migrate.TableRef{{Name: "r_old"}: {Name: "r_new"}},
		RenameColumns: map[migrate.ColumnRef]string{{Table: migrate.TableRef{Name: "a"}, Column: "old_name"}: "new_name"},
	}
	return from, to, opts
}

func TestDiffOrder(t *testing.T) {
	from, to, opts := orderPair()
	cs := diff(t, from, to, opts)
	want := []string{
		"drop-foreign-key a a_gone_fkey",
		"drop-foreign-key d2 d2_y_fkey",
		"drop-check a a_chk",
		"drop-index a a_gone",
		"drop-index r_old r_old_w",
		"rename-table r_new r_old r_new",
		"rename-sequence a id a_id_seq_old a_id_seq",
		"rename-column a old_name new_name",
		"rename-index a a_x_old a_x_new",
		"rename-constraint a a_pkey_old a_pkey",
		"rename-enum e_new e_old",
		"create-enum fresh",
		`alter-enum grow ["a" "b"] ["a" "b" "c"]`,
		"create-table c",
		"create-table c2",
		"add-column a y",
		"alter-column a x 10",
		`set-table-comment a "" "c"`,
		"set-table-collation a  utf8mb4_bin",
		"drop-column a gone",
		"drop-column r_new w",
		"add-index a a_y",
		"add-index r_new r_new_v",
		"add-check a a_chk2",
		"add-foreign-key a a_y_fkey",
		"add-foreign-key c c_x_fkey",
		"drop-index a a_id_x",
		"drop-table d",
		"drop-table d2",
		`alter-enum swap ["a" "b"] ["b" "a"]`,
		"drop-enum gone",
	}
	wantLines(t, "the order", cs, want)
	got, err := migrate.ApplyChanges(from, cs)
	if err != nil || !got.Equal(to) {
		t.Errorf("from with the changes is not to: %v", err)
	}
}

// The drops before the renames name the table and the object as from does,
// and an object they drop is not renamed.
func TestDiffStepOneUsesFromNames(t *testing.T) {
	renames := []string{
		"rename-table accounts users accounts",
		"rename-sequence accounts id users_id_seq accounts_id_seq",
	}
	wantLines(t, "rename-table-drop-column", caseChanges(t, "rename-table-drop-column", velox.Postgres), append(append([]string{
		"drop-index users users_email_unique"}, renames...),
		"rename-index accounts users_mentor_id accounts_mentor_id",
		"rename-index accounts users_org_id accounts_org_id",
		"rename-constraint accounts users_org_id_fkey accounts_org_id_fkey",
		"rename-constraint accounts users_pkey accounts_pkey",
		"rename-enum accounts_role users_role",
		"drop-column accounts email",
	))
	wantLines(t, "rename-table-key-type", caseChanges(t, "rename-table-key-type", velox.Postgres), append(append([]string{
		"drop-foreign-key users users_org_id_fkey"}, renames...),
		"rename-index accounts users_email_unique accounts_email_unique",
		"rename-index accounts users_mentor_id accounts_mentor_id",
		"rename-index accounts users_org_id accounts_org_id",
		"rename-constraint accounts users_pkey accounts_pkey",
		"rename-enum accounts_role users_role",
		"alter-column accounts org_id 1",
		"alter-column orgs id 1",
		"add-foreign-key accounts accounts_org_id_fkey",
	))
}

// A name to takes from a table dropped at the end is freed first: an
// index's, or on MySQL, where they are per database, a foreign key's.
func TestDiffReusedName(t *testing.T) {
	for _, d := range allDialects {
		first := "drop-index posts shared_idx"
		if d == velox.MySQL {
			first = "drop-foreign-key posts shared_fk"
		}
		wantLines(t, d.String(), caseChanges(t, "reuse-dropped-name", d), []string{
			first,
			"rename-constraint users user_mentor shared_fk",
			"add-index users shared_idx",
			"drop-table posts",
		})
	}
}

// An index on a dropped column goes before it, except on MySQL when it keeps
// other columns: MySQL shrinks it, and it is dropped after the adds.
func TestDiffDropColumnInIndex(t *testing.T) {
	for _, d := range allDialects {
		want := []string{"drop-index users users_org_id_logins", "drop-column users logins", "add-index users users_org_id"}
		if d == velox.MySQL {
			want = []string{"drop-column users logins", "add-index users users_org_id", "drop-index users users_org_id_logins"}
		}
		wantLines(t, d.String(), caseChanges(t, "drop-column-in-index", d), want)
	}
}

// A column rename carries the checks and partial indexes that name the
// column: a derived check and index are renamed, not dropped and added.
func TestDiffRenameColumnRewrites(t *testing.T) {
	want := map[velox.Dialect][]string{
		velox.Postgres: {"rename-column users role kind", "rename-enum users_kind users_role"},
		velox.MySQL:    {"rename-column users role kind"},
		velox.SQLite:   {"rename-column users role kind", "rename-constraint users users_role_check users_kind_check"},
	}
	for _, d := range allDialects {
		wantLines(t, d.String(), caseChanges(t, "rename-column-check", d), want[d])
	}
	for _, d := range caseNamed("rename-column-where").on() {
		cs := caseChanges(t, "rename-column-where", d)
		var got []string
		for _, c := range cs {
			got = append(got, string(c.Kind()))
		}
		if !slices.Equal(got, []string{"rename-column", "rename-index", "rename-index"}) {
			t.Errorf("%v:\n%s", d, strings.Join(lines(cs), "\n"))
		}
	}
}

func TestDiffRenameTableCarriesDerivedNames(t *testing.T) {
	want := map[velox.Dialect][]string{
		velox.Postgres: {
			"rename-table accounts users accounts",
			"rename-sequence accounts id users_id_seq accounts_id_seq",
			"rename-index accounts users_email_unique accounts_email_unique",
			"rename-index accounts users_mentor_id accounts_mentor_id",
			"rename-index accounts users_org_id accounts_org_id",
			"rename-constraint accounts users_org_id_fkey accounts_org_id_fkey",
			"rename-constraint accounts users_pkey accounts_pkey",
			"rename-enum accounts_role users_role",
		},
		velox.MySQL: {
			"rename-table accounts users accounts",
			"rename-index accounts users_email_unique accounts_email_unique",
			"rename-index accounts users_mentor_id accounts_mentor_id",
			"rename-index accounts users_org_id accounts_org_id",
			"rename-constraint accounts users_org_id_fkey accounts_org_id_fkey",
		},
		velox.SQLite: {
			"rename-table accounts users accounts",
			"rename-index accounts users_email_unique accounts_email_unique",
			"rename-index accounts users_mentor_id accounts_mentor_id",
			"rename-index accounts users_org_id accounts_org_id",
			"rename-constraint accounts users_org_id_fkey accounts_org_id_fkey",
			"rename-constraint accounts users_role_check accounts_role_check",
			"rename-constraint accounts users_status_check accounts_status_check",
		},
	}
	for _, d := range allDialects {
		cs := caseChanges(t, "rename-table", d)
		wantLines(t, d.String(), cs, want[d])
		for _, l := range lines(cs) {
			// The StorageKey index, the Symbol key and the EnumOf type keep
			// their names.
			for _, name := range []string{"users_by_name", "user_mentor", "case_status"} {
				if strings.Contains(l, name) {
					t.Errorf("%v: %s renames %s", d, l, name)
				}
			}
		}
	}
}

func TestDiffMatchesByDefinition(t *testing.T) {
	u := migrate.Table{Name: "u", Columns: []migrate.Column{{Name: "id", Type: "bigint"}}, PrimaryKey: []string{"id"}}
	tb := func(ix migrate.Index, fk migrate.ForeignKey, ck migrate.Check) *migrate.Schema {
		return &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{
			{
				Name:       "t",
				Columns:    []migrate.Column{{Name: "id", Type: "integer"}, {Name: "x", Type: "integer"}, {Name: "y", Type: "integer"}},
				PrimaryKey: []string{"id"}, Indexes: []migrate.Index{ix}, ForeignKeys: []migrate.ForeignKey{fk}, Checks: []migrate.Check{ck},
			},
			u,
		}}
	}
	fk := func(name, onDelete string) migrate.ForeignKey {
		return migrate.ForeignKey{Name: name, Columns: []string{"x"}, RefTable: migrate.TableRef{Name: "u"}, RefColumns: []string{"id"}, OnDelete: onDelete}
	}
	x := []string{"x"}
	from := tb(migrate.Index{Name: "i1", Columns: x}, fk("f1", ""), migrate.Check{Name: "c1", Expr: `"x" > 0`})
	to := tb(migrate.Index{Name: "i2", Columns: x}, fk("f2", ""), migrate.Check{Name: "c2", Expr: `"x" > 0`})
	wantLines(t, "names alone differ", diff(t, from, to, migrate.DiffOptions{}), []string{
		"rename-index t i1 i2",
		"rename-constraint t c1 c2",
		"rename-constraint t f1 f2",
	})

	to = tb(migrate.Index{Name: "i1", Columns: []string{"y"}}, fk("f1", "CASCADE"), migrate.Check{Name: "c1", Expr: `"x" > 1`})
	cs := diff(t, from, to, migrate.DiffOptions{})
	wantLines(t, "definitions differ", cs, []string{
		"drop-foreign-key t f1",
		"drop-check t c1",
		"drop-index t i1",
		"add-index t i1",
		"add-check t c1",
		"add-foreign-key t f1",
	})
	if got, err := migrate.ApplyChanges(from, cs); err != nil || !got.Equal(to) {
		t.Errorf("from with the changes is not to: %v", err)
	}
}

func TestDiffFKIndexReplaced(t *testing.T) {
	for _, d := range allDialects {
		wantLines(t, d.String(), caseChanges(t, "fk-index-replaced", d), []string{
			"add-index users users_org_id_created_at",
			"drop-index users users_org_id",
		})
	}
}

func TestDiffCreateOrder(t *testing.T) {
	for _, d := range allDialects {
		cs := caseChanges(t, "create-tables", d)
		at := func(l string) int {
			i := slices.Index(lines(cs), l)
			if i < 0 {
				t.Fatalf("%v: no %q in\n%s", d, l, strings.Join(lines(cs), "\n"))
			}
			return i
		}
		if at("create-table orgs") > at("create-table users") || at("create-table users") > at("create-table posts") {
			t.Errorf("%v: a referenced table is not created first:\n%s", d, strings.Join(lines(cs), "\n"))
		}
		created := map[string]migrate.Table{}
		for _, c := range cs {
			if c, ok := c.(migrate.CreateTable); ok {
				created[c.Table.Name] = c.Table
			}
		}
		if fks := created["users"].ForeignKeys; !slices.ContainsFunc(fks, func(fk migrate.ForeignKey) bool { return fk.Name == "user_mentor" }) {
			t.Errorf("%v: the self-reference is not inline: %+v", d, fks)
		}
		inline := len(created["alphas"].ForeignKeys) + len(created["betas"].ForeignKeys)
		var added []string
		for _, c := range cs {
			if c, ok := c.(migrate.AddForeignKey); ok {
				added = append(added, line(c))
			}
		}
		if d == velox.SQLite {
			if inline != 2 || len(added) != 0 {
				t.Errorf("%v: the cycle's keys are %d inline and %q added; want both inline", d, inline, added)
			}
			continue
		}
		if inline != 1 || !slices.Equal(added, []string{"add-foreign-key alphas alphas_beta_id_fkey"}) {
			t.Errorf("%v: the cycle's keys are %d inline and %q added", d, inline, added)
		}
		if at("add-foreign-key alphas alphas_beta_id_fkey") < at("create-table betas") || at("create-table betas") < at("create-table alphas") {
			t.Errorf("%v: the cycle's key is not added after both tables:\n%s", d, strings.Join(lines(cs), "\n"))
		}
	}
}

func TestDiffEnumValues(t *testing.T) {
	for name, want := range map[string]string{
		"enum-append":  `alter-enum users_role ["admin" "viewer"] ["admin" "viewer" "owner"]`,
		"enum-insert":  `alter-enum users_role ["admin" "viewer"] ["admin" "editor" "viewer"]`,
		"enum-remove":  `alter-enum users_role ["admin" "viewer"] ["viewer"]`,
		"enum-reorder": `alter-enum users_role ["admin" "viewer"] ["viewer" "admin"]`,
	} {
		wantLines(t, name+" on PostgreSQL", caseChanges(t, name, velox.Postgres), []string{want})
		wantLines(t, name+" on MySQL", caseChanges(t, name, velox.MySQL), []string{"alter-column users role 1"})
		wantLines(t, name+" on SQLite", caseChanges(t, name, velox.SQLite), []string{
			"drop-check users users_role_check",
			"add-check users users_role_check",
		})
	}

	// Removing or reordering and adding: the new values are added first, and
	// the swap is after the columns are dropped.
	from := &migrate.Schema{Dialect: velox.Postgres, Tables: []migrate.Table{{
		Name:       "t",
		Columns:    []migrate.Column{{Name: "id", Type: "bigint"}, {Name: "e", Type: `"t_e"`}, {Name: "gone", Type: "text"}},
		PrimaryKey: []string{"id"},
	}}, Enums: []migrate.Enum{{Name: "t_e", Values: []string{"a", "b", "c"}}}}
	to := migrate.Clone(from)
	to.Tables[0].Columns = to.Tables[0].Columns[:2]
	to.Enums[0].Values = []string{"c", "x", "a"}
	cs := diff(t, from, to, migrate.DiffOptions{})
	wantLines(t, "a removal with an addition", cs, []string{
		`alter-enum t_e ["a" "b" "c"] ["a" "b" "c" "x"]`,
		"drop-column t gone",
		`alter-enum t_e ["a" "b" "c" "x"] ["c" "x" "a"]`,
	})
	if got, err := migrate.ApplyChanges(from, cs); err != nil || !got.Equal(to) {
		t.Errorf("from with the changes is not to: %v", err)
	}
}

func TestDiffErrors(t *testing.T) {
	users := func(pk ...string) *migrate.Schema {
		return &migrate.Schema{Dialect: velox.Postgres, Tables: []migrate.Table{{
			Name:       "users",
			Columns:    []migrate.Column{{Name: "id", Type: "bigint"}, {Name: "org_id", Type: "bigint"}},
			PrimaryKey: pk,
		}}}
	}
	two := users("id")
	two.Tables = append(two.Tables, migrate.Table{Name: "accounts", Columns: []migrate.Column{{Name: "id", Type: "bigint"}}})
	renamed := users("id")
	renamed.Tables[0].Name = "accounts"
	u, a, nope := migrate.TableRef{Name: "users"}, migrate.TableRef{Name: "accounts"}, migrate.TableRef{Name: "nope"}
	for _, c := range []struct {
		name     string
		from, to *migrate.Schema
		opts     migrate.DiffOptions
		code     string
		text     string
	}{
		{"nil from", nil, users("id"), migrate.DiffOptions{}, "nil", "migrate: Diff needs two schemas"},
		{"nil to", users("id"), nil, migrate.DiffOptions{}, "nil", "migrate: Diff needs two schemas"},
		{"dialect", users("id"), &migrate.Schema{Dialect: velox.MySQL}, migrate.DiffOptions{}, "dialect",
			"migrate: from is a postgres schema and to a mysql one; Diff compares two schemas of one dialect"},
		{"primary key", users("id"), users("id", "org_id"), migrate.DiffOptions{}, "primary-key",
			"migrate: users: the primary key changes from (id) to (id, org_id); write this migration by hand"},
		{"rename source", users("id"), users("id"), migrate.DiffOptions{RenameTables: map[migrate.TableRef]migrate.TableRef{nope: a}}, "rename",
			"migrate: nope: the table to rename is not in from"},
		{"rename target", users("id"), users("id"), migrate.DiffOptions{RenameTables: map[migrate.TableRef]migrate.TableRef{u: a}}, "rename",
			"migrate: users: the table is renamed to accounts, which is not in to"},
		{"rename target in from", two, two, migrate.DiffOptions{RenameTables: map[migrate.TableRef]migrate.TableRef{u: a}}, "rename",
			"migrate: users: the table is renamed to accounts, which from already has"},
		{"column source", users("id"), users("id"), migrate.DiffOptions{RenameColumns: map[migrate.ColumnRef]string{{Table: u, Column: "nope"}: "x"}}, "rename",
			"migrate: users.nope: the column to rename is not in from"},
		{"column target", users("id"), renamed, migrate.DiffOptions{
			RenameTables:  map[migrate.TableRef]migrate.TableRef{u: a},
			RenameColumns: map[migrate.ColumnRef]string{{Table: u, Column: "org_id"}: "team_id"},
		}, "rename", "migrate: users.org_id: the column is renamed to team_id, which accounts does not have"},
		{"column target in from", users("id"), users("id"), migrate.DiffOptions{RenameColumns: map[migrate.ColumnRef]string{{Table: u, Column: "org_id"}: "id"}}, "rename",
			"migrate: users.org_id: the column is renamed to id, which users already has in from"},
	} {
		_, err := migrate.Diff(c.from, c.to, c.opts)
		var me *migrate.Error
		if !errors.As(err, &me) || me.Code != c.code || err.Error() != c.text {
			t.Errorf("%s: %v (%#v), want code %q and\n%s", c.name, err, me, c.code, c.text)
		}
	}
}
