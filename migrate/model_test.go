package migrate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// full is a schema with every field of every type set.
func full() *Schema {
	return &Schema{
		Dialect: velox.Postgres,
		Tables: []Table{
			{
				Schema: "billing", Name: "invoices",
				Columns: []Column{
					{Name: "id", Type: "bigint", Identity: true, Sequence: "invoices_id_seq", Pos: "schema/invoice.go:3"},
					{Name: "org_id", Type: "bigint", Nullable: true, Default: "0", Comment: "the owner", Pos: "schema/invoice.go:4"},
				},
				PrimaryKey: []string{"id"}, PrimaryKeyName: "invoices_pkey",
				Indexes: []Index{
					{Name: "invoices_a", Columns: []string{"org_id", "id"}, Unique: true, Where: "org_id > 0", Pos: "schema/invoice.go:9"},
					{Name: "invoices_b", Columns: []string{"id"}},
				},
				ForeignKeys: []ForeignKey{
					{Name: "invoices_org_id_fkey", Columns: []string{"org_id"}, RefTable: TableRef{Schema: "billing", Name: "orgs"}, RefColumns: []string{"id"}, OnDelete: "CASCADE", Pos: "schema/invoice.go:5"},
				},
				Checks:  []Check{{Name: "c", Expr: "org_id >= 0", Pos: "schema/invoice.go:11"}},
				Comment: "invoices", Collation: "C", Pos: "schema/invoice.go:1",
			},
			{Name: "orgs", Columns: []Column{{Name: "id", Type: "bigint"}}, PrimaryKey: []string{"id"}, PrimaryKeyName: "orgs_pkey"},
		},
		Enums: []Enum{
			{Schema: "billing", Name: "invoices_state", Values: []string{"open", "paid"}, Pos: "schema/invoice.go:6"},
			{Name: "orgs_kind", Values: []string{"b", "a"}},
		},
	}
}

func TestTableRefString(t *testing.T) {
	for _, c := range []struct {
		ref  TableRef
		want string
	}{
		{TableRef{Schema: "billing", Name: "invoices"}, "billing.invoices"},
		{TableRef{Name: "invoices"}, "invoices"},
	} {
		if got := c.ref.String(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestTableRef(t *testing.T) {
	tb := Table{Schema: "s", Name: "t"}
	if got := tb.Ref(); got != (TableRef{Schema: "s", Name: "t"}) {
		t.Errorf("%+v", got)
	}
}

func TestSchemaTable(t *testing.T) {
	s := full()
	got, ok := s.Table(TableRef{Schema: "billing", Name: "invoices"})
	if !ok || got != &s.Tables[0] {
		t.Errorf("billing.invoices: %v, %v", got, ok)
	}
	got, ok = s.Table(TableRef{Name: "orgs"})
	if !ok || got.Name != "orgs" {
		t.Errorf("orgs: %v, %v", got, ok)
	}
	for _, ref := range []TableRef{{Name: "invoices"}, {Schema: "billing", Name: "orgs"}, {Name: "nope"}} {
		if got, ok := s.Table(ref); ok || got != nil {
			t.Errorf("%v: %v, %v; want nil, false", ref, got, ok)
		}
	}
}

func TestEqualIgnoresPosAndColumnOrder(t *testing.T) {
	a, b := full(), full()
	if !a.Equal(b) {
		t.Fatal("a clone is not Equal")
	}
	b.Tables[0].Pos = "elsewhere:1"
	for i := range b.Tables[0].Columns {
		b.Tables[0].Columns[i].Pos = "elsewhere:2"
	}
	b.Tables[0].Indexes[0].Pos = "x"
	b.Tables[0].ForeignKeys[0].Pos = "x"
	b.Tables[0].Checks[0].Pos = "x"
	b.Enums[0].Pos = "x"
	c := b.Tables[0].Columns
	c[0], c[1] = c[1], c[0]
	if !a.Equal(b) || !b.Equal(a) {
		t.Error("Pos and column order must not matter")
	}
}

func TestEqualNilAndEmpty(t *testing.T) {
	a, b := full(), full()
	b.Tables[1].Indexes = []Index{}
	b.Tables[1].Checks = []Check{}
	b.Tables[1].ForeignKeys = []ForeignKey{}
	a.Tables[1].Indexes, a.Tables[1].Checks = nil, nil
	if !a.Equal(b) {
		t.Error("a nil and an empty slice must be alike")
	}
	if !(&Schema{Dialect: velox.SQLite}).Equal(&Schema{Dialect: velox.SQLite, Tables: []Table{}, Enums: []Enum{}}) {
		t.Error("an empty schema")
	}
}

func TestEqualComparesEverythingElse(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*Schema)
	}{
		{"Dialect", func(s *Schema) { s.Dialect = velox.MySQL }},
		{"a table dropped", func(s *Schema) { s.Tables = s.Tables[:1] }},
		{"a table added", func(s *Schema) { s.Tables = append(s.Tables, Table{Name: "z"}) }},
		{"a table's schema", func(s *Schema) { s.Tables[0].Schema = "other" }},
		{"a table's name", func(s *Schema) { s.Tables[0].Name = "other" }},
		{"a column added", func(s *Schema) { s.Tables[0].Columns = append(s.Tables[0].Columns, Column{Name: "x", Type: "text"}) }},
		{"a column dropped", func(s *Schema) { s.Tables[0].Columns = s.Tables[0].Columns[:1] }},
		{"a column's name", func(s *Schema) { s.Tables[0].Columns[1].Name = "other" }},
		{"a column's Type", func(s *Schema) { s.Tables[0].Columns[1].Type = "integer" }},
		{"a column's Nullable", func(s *Schema) { s.Tables[0].Columns[1].Nullable = false }},
		{"a column's Default", func(s *Schema) { s.Tables[0].Columns[1].Default = "1" }},
		{"a column's Identity", func(s *Schema) { s.Tables[0].Columns[0].Identity = false }},
		{"a column's Sequence", func(s *Schema) { s.Tables[0].Columns[0].Sequence = "other" }},
		{"a column's Comment", func(s *Schema) { s.Tables[0].Columns[1].Comment = "other" }},
		{"PrimaryKey", func(s *Schema) { s.Tables[0].PrimaryKey = []string{"org_id"} }},
		{"PrimaryKey emptied", func(s *Schema) { s.Tables[0].PrimaryKey = nil }},
		{"PrimaryKeyName", func(s *Schema) { s.Tables[0].PrimaryKeyName = "other" }},
		{"Comment", func(s *Schema) { s.Tables[0].Comment = "other" }},
		{"Collation", func(s *Schema) { s.Tables[0].Collation = "other" }},
		{"an index added", func(s *Schema) { s.Tables[0].Indexes = append(s.Tables[0].Indexes, Index{Name: "z"}) }},
		{"an index dropped", func(s *Schema) { s.Tables[0].Indexes = s.Tables[0].Indexes[:1] }},
		{"an index's Name", func(s *Schema) { s.Tables[0].Indexes[0].Name = "other" }},
		{"an index's Columns", func(s *Schema) { s.Tables[0].Indexes[0].Columns = []string{"id", "org_id"} }},
		{"an index's Unique", func(s *Schema) { s.Tables[0].Indexes[0].Unique = false }},
		{"an index's Where", func(s *Schema) { s.Tables[0].Indexes[0].Where = "org_id > 1" }},
		{"a foreign key dropped", func(s *Schema) { s.Tables[0].ForeignKeys = nil }},
		{"a foreign key's Name", func(s *Schema) { s.Tables[0].ForeignKeys[0].Name = "other" }},
		{"a foreign key's Columns", func(s *Schema) { s.Tables[0].ForeignKeys[0].Columns = []string{"id"} }},
		{"a foreign key's RefTable schema", func(s *Schema) { s.Tables[0].ForeignKeys[0].RefTable.Schema = "" }},
		{"a foreign key's RefTable name", func(s *Schema) { s.Tables[0].ForeignKeys[0].RefTable.Name = "other" }},
		{"a foreign key's RefColumns", func(s *Schema) { s.Tables[0].ForeignKeys[0].RefColumns = []string{"x"} }},
		{"a foreign key's OnDelete", func(s *Schema) { s.Tables[0].ForeignKeys[0].OnDelete = "SET NULL" }},
		{"a check dropped", func(s *Schema) { s.Tables[0].Checks = nil }},
		{"a check's Name", func(s *Schema) { s.Tables[0].Checks[0].Name = "other" }},
		{"a check's Expr", func(s *Schema) { s.Tables[0].Checks[0].Expr = "true" }},
		{"an enum dropped", func(s *Schema) { s.Enums = s.Enums[:1] }},
		{"an enum's Schema", func(s *Schema) { s.Enums[0].Schema = "other" }},
		{"an enum's Name", func(s *Schema) { s.Enums[0].Name = "other" }},
		{"an enum's value order", func(s *Schema) { s.Enums[0].Values = []string{"paid", "open"} }},
		{"an enum's value added", func(s *Schema) { s.Enums[0].Values = append(s.Enums[0].Values, "void") }},
	} {
		a, b := full(), full()
		c.edit(b)
		if a.Equal(b) || b.Equal(a) {
			t.Errorf("%s: Equal after the edit", c.name)
		}
	}
}

func TestEqualIgnoresListOrder(t *testing.T) {
	a, b := full(), full()
	b.Tables[0], b.Tables[1] = b.Tables[1], b.Tables[0]
	b.Enums[0], b.Enums[1] = b.Enums[1], b.Enums[0]
	b.Tables[1].Indexes[0], b.Tables[1].Indexes[1] = b.Tables[1].Indexes[1], b.Tables[1].Indexes[0]
	if !a.Equal(b) {
		t.Error("the sorted lists are compared by key, not by position")
	}
}

func TestCloneIsDeep(t *testing.T) {
	a := full()
	b := a.clone()
	if !a.Equal(b) {
		t.Fatal("a clone is not Equal")
	}
	b.Tables[0].Columns[0].Name = "x"
	b.Tables[0].PrimaryKey[0] = "x"
	b.Tables[0].Indexes[0].Columns[0] = "x"
	b.Tables[0].ForeignKeys[0].Columns[0] = "x"
	b.Tables[0].ForeignKeys[0].RefColumns[0] = "x"
	b.Enums[0].Values[0] = "x"
	if !a.Equal(full()) {
		t.Error("editing the clone changed the original")
	}
}

func TestSortOrders(t *testing.T) {
	s := &Schema{
		Tables: []Table{
			{Schema: "b", Name: "a"}, {Name: "z"}, {Schema: "a", Name: "z"},
			{Name: "m", Columns: []Column{{Name: "z"}, {Name: "a"}},
				Indexes:     []Index{{Name: "b"}, {Name: "a"}},
				ForeignKeys: []ForeignKey{{Name: "b"}, {Name: "a"}},
				Checks:      []Check{{Name: "b"}, {Name: "a"}}},
		},
		Enums: []Enum{{Schema: "b", Name: "a"}, {Name: "z"}, {Schema: "a", Name: "z"}},
	}
	s.sort()
	var tables, enums []string
	for _, t := range s.Tables {
		tables = append(tables, t.Ref().String())
	}
	for _, e := range s.Enums {
		enums = append(enums, TableRef{Schema: e.Schema, Name: e.Name}.String())
	}
	if got, want := strings.Join(tables, " "), "m z a.z b.a"; got != want {
		t.Errorf("tables %s, want %s", got, want)
	}
	if got, want := strings.Join(enums, " "), "z a.z b.a"; got != want {
		t.Errorf("enums %s, want %s", got, want)
	}
	m := s.Tables[0]
	if m.Columns[0].Name != "z" || m.Indexes[0].Name != "a" || m.ForeignKeys[0].Name != "a" || m.Checks[0].Name != "a" {
		t.Errorf("%+v", m)
	}
}

func TestErrorText(t *testing.T) {
	for _, c := range []struct {
		e    Error
		want string
	}{
		{Error{Pos: "schema/user.go:3", Object: "users", Reason: "r"}, "migrate: schema/user.go:3: users: r"},
		{Error{Object: "users", Reason: "r"}, "migrate: users: r"},
		{Error{Reason: "r", Code: "rename"}, "migrate: r"},
	} {
		if got := c.e.Error(); got != c.want {
			t.Errorf("%q, want %q", got, c.want)
		}
	}
	var err error = &Error{Reason: "r"}
	if err.Error() != "migrate: r" {
		t.Error(err)
	}
}

// TestEqualIgnoresConstraint: Equal does not compare Index.Constraint,
// which only Inspect and Baseline set.
func TestEqualIgnoresConstraint(t *testing.T) {
	a := full()
	b := a.clone()
	b.Tables[0].Indexes[1].Constraint = true
	if !a.Equal(b) || !b.Equal(a) {
		t.Error("Equal compares Constraint")
	}
}

// TestSchemaRefusesConstraint: a constraint index outside PostgreSQL, and a
// non-unique or partial one, are refused.
func TestSchemaRefusesConstraint(t *testing.T) {
	schema := func(d velox.Dialect, ix Index) *Schema {
		ix.Name, ix.Columns, ix.Constraint = "k", []string{"a"}, true
		return &Schema{Dialect: d, Tables: []Table{{Name: "t", Columns: []Column{{Name: "a", Type: "text"}}, Indexes: []Index{ix}}}}
	}
	only := `x marks the index "k" a constraint, which only PostgreSQL holds`
	cannot := `x marks the index "k" a constraint, which a non-unique or partial index cannot be`
	for _, c := range []struct {
		name   string
		s      *Schema
		reason string
	}{
		{"mysql", schema(velox.MySQL, Index{Unique: true}), only},
		{"sqlite", schema(velox.SQLite, Index{Unique: true}), only},
		{"non-unique", schema(velox.Postgres, Index{}), cannot},
		{"partial", schema(velox.Postgres, Index{Unique: true, Where: "a <> ''"}), cannot},
	} {
		err := c.s.validate("x")
		if e, ok := err.(*Error); !ok || e.Code != "schema" || e.Object != "t" || e.Reason != c.reason {
			t.Errorf("%s: %#v, want %q", c.name, err, c.reason)
		}
	}
	if err := schema(velox.Postgres, Index{Unique: true}).validate("x"); err != nil {
		t.Errorf("a unique whole index: %v", err)
	}
}

// TestCarryConstraints: the snapshot's constraint flag is carried to an
// index of to that is the same one, after renames; a redefined, partial or
// other index is plain; to's own flag is kept.
func TestCarryConstraints(t *testing.T) {
	users := TableRef{Name: "users"}
	accounts := TableRef{Name: "accounts"}
	tbl := func(name string, ix ...Index) *Schema {
		return &Schema{Dialect: velox.Postgres, Tables: []Table{{
			Name: name, PrimaryKey: []string{"id"}, PrimaryKeyName: name + "_pkey",
			Columns: []Column{{Name: "id", Type: "bigint"}, {Name: "email", Type: "text"}, {Name: "mail", Type: "text"}, {Name: "name", Type: "text"}},
			Indexes: ix,
		}}}
	}
	email := func(name string, cols ...string) Index {
		if len(cols) == 0 {
			cols = []string{"email"}
		}
		return Index{Name: name, Columns: cols, Unique: true}
	}
	flagged := func(ix Index) Index { ix.Constraint = true; return ix }
	from := tbl("users", flagged(email("users_email_key")))
	partial := email("users_email_key")
	partial.Where = "email <> ''"
	for _, c := range []struct {
		name    string
		from    *Schema
		to      *Schema
		changes []Change
		carried bool
	}{
		{"same", from, tbl("users", email("users_email_key")), nil, true},
		{"renamed", from, tbl("users", email("users_mail_key")),
			[]Change{RenameIndex{Table: users, From: "users_email_key", To: "users_mail_key"}}, true},
		{"table renamed", from, tbl("accounts", email("accounts_email_key")),
			[]Change{RenameTable{From: users, To: accounts}, RenameIndex{Table: accounts, From: "users_email_key", To: "accounts_email_key"}}, true},
		{"column renamed", from, tbl("users", email("users_email_key", "mail")),
			[]Change{RenameColumn{Table: users, From: "email", To: "mail"}}, true},
		{"redefined", from, tbl("users", email("users_email_key", "email", "name")),
			[]Change{DropIndex{Table: users, Index: from.Tables[0].Indexes[0]}, AddIndex{Table: users, Index: email("users_email_key", "email", "name")}}, false},
		{"another index", from, tbl("users", Index{Name: "users_email_idx", Columns: []string{"email"}}), nil, false},
		{"partial", from, tbl("users", partial), nil, false},
		{"baseline", &Schema{Dialect: velox.Postgres}, tbl("users", flagged(email("users_email_key"))),
			[]Change{CreateTable{Table: tbl("users", flagged(email("users_email_key"))).Tables[0]}}, true},
	} {
		fromBefore, toBefore := c.from.clone(), c.to.clone()
		got := carryConstraints(c.from, c.to, c.changes)
		if !reflect.DeepEqual(c.from, fromBefore) || !reflect.DeepEqual(c.to, toBefore) {
			t.Errorf("%s: from or to changed", c.name)
		}
		if !got.Equal(c.to) {
			t.Errorf("%s: the result is not to", c.name)
		}
		ixs := got.Tables[0].Indexes
		if len(ixs) != 1 || ixs[0].Constraint != c.carried {
			t.Errorf("%s: indexes %+v, want Constraint %v", c.name, ixs, c.carried)
		}
	}

	// replaced: users is renamed to users_old and a new users is created;
	// the new table's index is plain, the renamed one's keeps the flag.
	old := tbl("users_old", email("users_old_email_key")).Tables[0]
	fresh := tbl("users", email("users_email_key")).Tables[0]
	to := &Schema{Dialect: velox.Postgres, Tables: []Table{old, fresh}}
	got := carryConstraints(from, to, []Change{
		RenameTable{From: users, To: TableRef{Name: "users_old"}},
		RenameIndex{Table: TableRef{Name: "users_old"}, From: "users_email_key", To: "users_old_email_key"},
		CreateTable{Table: fresh},
	})
	if ix := got.table(TableRef{Name: "users"}).Indexes[0]; ix.Constraint {
		t.Errorf("replaced: the new table's index %+v carries the flag", ix)
	}
	if ix := got.table(TableRef{Name: "users_old"}).Indexes[0]; !ix.Constraint {
		t.Errorf("replaced: the renamed table's index %+v lost the flag", ix)
	}
}
