package migrate_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/load"
)

// The base schema every case edits: organisations have users, users write
// posts and have a mentor, a tree whose key is mentor_id.

type caseStatus string

// pgEnum names a PostgreSQL enum type for an enum field, which is varchar
// unless its SchemaType names one (decision 0090): the cases keep the
// planner's enum types under test.
func pgEnum(name string) map[string]string { return map[string]string{field.Postgres: name} }

func (caseStatus) Values() []string { return []string{"active", "banned", "pending"} }

type Org struct{ schema.Schema }

func (Org) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (Org) Edges() []schema.Edge   { return []schema.Edge{edge.To("users", User.Type)} }

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id"),
		field.String("name").Comment("the display name"),
		field.String("email").MaxLen(120).Unique(),
		field.String("bio").Optional().Nillable(),
		field.Enum("role").Values("admin", "viewer").Default("viewer").SchemaType(pgEnum("users_role")),
		field.EnumOf[caseStatus]("status").Default("active").SchemaType(pgEnum("case_status")),
		field.Int32("logins").Optional().Default(0),
		field.Time("created_at"),
		field.Int64("mentor_id").Optional().Nillable(),
	}
}
func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("org", Org.Type).Ref("users").Unique().Required().Field("org_id"),
		edge.To("mentees", User.Type).From("mentor").Unique().Field("mentor_id").StorageKey(edge.Symbol("user_mentor")),
		edge.To("posts", Post.Type),
	}
}
func (User) Indexes() []schema.Index {
	return []schema.Index{index.Fields("name").StorageKey("users_by_name")}
}

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("author_id"),
		field.String("title").MaxLen(200),
		field.EnumOf[caseStatus]("status").Default("active").SchemaType(pgEnum("case_status")),
	}
}
func (Post) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("author", User.Type).Ref("posts").Unique().Required().Field("author_id").OnDelete(edge.Cascade),
	}
}

// Alpha and Beta reference each other.
type Alpha struct{ schema.Schema }

func (Alpha) Fields() []schema.Field {
	return []schema.Field{field.Int64("beta_id").Optional().Nillable()}
}
func (Alpha) Edges() []schema.Edge {
	return []schema.Edge{edge.To("beta", Beta.Type).Unique().Field("beta_id")}
}

type Beta struct{ schema.Schema }

func (Beta) Fields() []schema.Field {
	return []schema.Field{field.Int64("alpha_id").Optional().Nillable()}
}
func (Beta) Edges() []schema.Edge {
	return []schema.Edge{edge.To("alpha", Alpha.Type).Unique().Field("alpha_id")}
}

// Invoice is in the database schema billing.
type Invoice struct{ schema.Schema }

func (Invoice) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("billing", "invoices")}
}
func (Invoice) Fields() []schema.Field {
	return []schema.Field{field.Enum("state").Values("open", "paid").Default("open").SchemaType(pgEnum("invoices_state"))}
}
func (Invoice) Edges() []schema.Edge {
	return []schema.Edge{edge.To("payer", Payer.Type).Unique().Required()}
}

// Payer is in billing too: MySQL cannot name the default database from
// another one, so Invoice's key points within billing.
type Payer struct{ schema.Schema }

func (Payer) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("billing", "payers")}
}
func (Payer) Fields() []schema.Field { return []schema.Field{field.String("name")} }

// part carries one element into a load model.
type part struct {
	schema.Schema
	fields  []schema.Field
	edges   []schema.Edge
	indexes []schema.Index
	annos   []schema.Annotation
}

func (p part) Fields() []schema.Field           { return p.fields }
func (p part) Edges() []schema.Edge             { return p.edges }
func (p part) Indexes() []schema.Index          { return p.indexes }
func (p part) Annotations() []schema.Annotation { return p.annos }

var pkgPath = reflect.TypeFor[User]().PkgPath()

// marshal is the load model of ss.
func marshal(ss ...schema.Interface) *loadjson.Model {
	b, err := load.Marshal(pkgPath, ss...)
	if err != nil {
		panic(err)
	}
	var m loadjson.Model
	if err := json.Unmarshal(b, &m); err != nil {
		panic(err)
	}
	if len(m.Errors) > 0 {
		panic(fmt.Sprintf("load errors %+v", m.Errors))
	}
	return &m
}

var baseJSON = sync.OnceValue(func() []byte {
	b, err := json.Marshal(marshal(Org{}, User{}, Post{}))
	if err != nil {
		panic(err)
	}
	return b
})

// base is a fresh copy of the base load model.
func base() *loadjson.Model {
	var m loadjson.Model
	if err := json.Unmarshal(baseJSON(), &m); err != nil {
		panic(err)
	}
	return &m
}

func ent(m *loadjson.Model, name string) *loadjson.Schema {
	for _, s := range m.Schemas {
		if s.Name == name {
			return s
		}
	}
	panic("no schema " + name)
}

func fieldOf(m *loadjson.Model, s, name string) *loadjson.Field {
	for _, f := range ent(m, s).Fields {
		if f.Name == name {
			return f
		}
	}
	panic("no field " + s + "." + name)
}

func edgeOf(m *loadjson.Model, s, name string) *loadjson.Edge {
	for _, e := range ent(m, s).Edges {
		if e.Name == name {
			return e
		}
	}
	panic("no edge " + s + "." + name)
}

func addField(m *loadjson.Model, s string, f schema.Field) {
	e := ent(m, s)
	lf := marshal(part{fields: []schema.Field{f}}).Schemas[0].Fields[0]
	lf.Position.Index = len(e.Fields)
	e.Fields = append(e.Fields, lf)
}

func dropField(m *loadjson.Model, s, name string) {
	e := ent(m, s)
	e.Fields = slices.DeleteFunc(e.Fields, func(f *loadjson.Field) bool { return f.Name == name })
	for i, f := range e.Fields {
		f.Position.Index = i
	}
}

func addEdge(m *loadjson.Model, s string, ed schema.Edge) {
	e := ent(m, s)
	le := marshal(part{edges: []schema.Edge{ed}}).Schemas[0].Edges[0]
	le.Position.Index = len(e.Edges)
	e.Edges = append(e.Edges, le)
}

func dropEdge(m *loadjson.Model, s, name string) {
	e := ent(m, s)
	e.Edges = slices.DeleteFunc(e.Edges, func(ed *loadjson.Edge) bool { return ed.Name == name })
	for i, ed := range e.Edges {
		ed.Position.Index = i
	}
}

func addIndex(m *loadjson.Model, s string, ix schema.Index) {
	e := ent(m, s)
	li := marshal(part{indexes: []schema.Index{ix}}).Schemas[0].Indexes[0]
	li.Position.Index = len(e.Indexes)
	e.Indexes = append(e.Indexes, li)
}

func dropIndex(m *loadjson.Model, s string, i int) {
	e := ent(m, s)
	e.Indexes = slices.Delete(e.Indexes, i, i+1)
	for i, ix := range e.Indexes {
		ix.Position.Index = i
	}
}

func annotate(m *loadjson.Model, s string, a schema.Annotation) {
	e := ent(m, s)
	if e.Annotations == nil {
		e.Annotations = map[string]json.RawMessage{}
	}
	e.Annotations[a.Name()] = marshal(part{annos: []schema.Annotation{a}}).Schemas[0].Annotations[a.Name()]
}

func addSchema(m *loadjson.Model, ss ...schema.Interface) {
	m.Schemas = append(m.Schemas, marshal(ss...).Schemas...)
	slices.SortFunc(m.Schemas, func(a, b *loadjson.Schema) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
}

func dropSchema(m *loadjson.Model, name string) {
	m.Schemas = slices.DeleteFunc(m.Schemas, func(s *loadjson.Schema) bool { return s.Name == name })
}

// setEnum gives the enum field s.name the values vs.
func setEnum(m *loadjson.Model, s, name string, vs ...string) {
	f := fieldOf(m, s, name)
	f.Enums = nil
	for _, v := range vs {
		f.Enums = append(f.Enums, loadjson.Enum{Name: goName(v), Value: v})
	}
}

// goName is a Go name for the enum value v: its letters, the first upper-case.
func goName(v string) string {
	var b strings.Builder
	for _, r := range v {
		if unicode.IsLetter(r) {
			if b.Len() == 0 {
				r = unicode.ToUpper(r)
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tables builds the model of the base edited by edit, for d.
func tables(d velox.Dialect, edit func(*loadjson.Model)) (*migrate.Schema, error) {
	m := base()
	if edit != nil {
		edit(m)
	}
	g, err := codegen.Build(m)
	if err != nil {
		return nil, err
	}
	return codegen.Tables(g, d)
}

// mustTables is tables for a hand-made from.
func mustTables(d velox.Dialect, edit func(*loadjson.Model)) *migrate.Schema {
	s, err := tables(d, edit)
	if err != nil {
		panic(err)
	}
	return s
}

// table is the table name of s, to edit.
func table(s *migrate.Schema, name string) *migrate.Table {
	for i := range s.Tables {
		if s.Tables[i].Name == name {
			return &s.Tables[i]
		}
	}
	panic("no table " + name)
}

func column(tb *migrate.Table, name string) *migrate.Column {
	for i := range tb.Columns {
		if tb.Columns[i].Name == name {
			return &tb.Columns[i]
		}
	}
	panic("no column " + tb.Name + "." + name)
}

type testCase struct {
	name     string
	dialects []velox.Dialect                       // nil: all three
	from     func(*loadjson.Model)                 // edits the base for from; nil: the base
	to       func(*loadjson.Model)                 // edits the base for to
	fromDB   func(d velox.Dialect) *migrate.Schema // a hand-made from (serial, ent-shaped); nil: from the load model
	both     func(*migrate.Schema)                 // a hand edit of from and to both, for what the DSL cannot say
	empty    bool                                  // from is the empty schema
	opts     migrate.DiffOptions
	allow    []migrate.Allow // covers every destructive finding of the case, on any dialect
}

var allDialects = []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite}

// on is the dialects c runs on.
func (c testCase) on() []velox.Dialect {
	if c.dialects == nil {
		return allDialects
	}
	return c.dialects
}

// models is c's from and to on d: Build and Tables of the edited copies.
func models(t *testing.T, c testCase, d velox.Dialect) (from, to *migrate.Schema) {
	t.Helper()
	var err error
	switch {
	case c.empty:
		from = &migrate.Schema{Dialect: d}
	case c.fromDB != nil:
		from = c.fromDB(d)
	default:
		if from, err = tables(d, c.from); err != nil {
			t.Fatalf("%s on %v: from: %v", c.name, d, err)
		}
	}
	if to, err = tables(d, c.to); err != nil {
		t.Fatalf("%s on %v: to: %v", c.name, d, err)
	}
	if c.both != nil {
		c.both(from)
		c.both(to)
	}
	return from, to
}

// caseNamed is the case of that name.
func caseNamed(name string) testCase {
	for _, c := range cases {
		if c.name == name {
			return c
		}
	}
	panic("no case " + name)
}

var (
	pg        = []velox.Dialect{velox.Postgres}
	pgMySQL   = []velox.Dialect{velox.Postgres, velox.MySQL}
	mysqlOnly = []velox.Dialect{velox.MySQL}
	sqlite    = []velox.Dialect{velox.SQLite}
	users     = migrate.TableRef{Name: "users"}
	accounts  = migrate.TableRef{Name: "accounts"}
)

// withFKIndex declares the index (org_id, created_at), which covers the
// foreign key users.org_id.
func withFKIndex(m *loadjson.Model) { addIndex(m, "User", index.Fields("org_id", "created_at")) }

// optOutRole makes users.role a varchar on MySQL.
func optOutRole(m *loadjson.Model) {
	fieldOf(m, "User", "role").SchemaType = map[string]string{field.MySQL: "varchar(16)"}
}

// roleCheck gives users a declared check on its role column, or on kind where
// the column is renamed, quoted for the dialect. velox-gen makes no enum check
// any more (decision 0090), so the cases that exercise the planner's checks
// declare one.
func roleCheck(s *migrate.Schema) {
	tb := table(s, "users")
	col, q := "role", `"`
	if slices.ContainsFunc(tb.Columns, func(c migrate.Column) bool { return c.Name == "kind" }) {
		col = "kind"
	}
	if s.Dialect == velox.MySQL {
		q = "`"
	}
	tb.Checks = append(tb.Checks, migrate.Check{Name: "users_" + col + "_check", Expr: q + col + q + " IN ('admin', 'viewer')"})
}

// sharedIdx names the indexes called <table>_shared_idx shared_idx, as they
// would be without the table prefix velox-gen gives every index (decision
// 0090): the case reuse-dropped-name needs one name in two tables.
func sharedIdx(s *migrate.Schema) {
	for i := range s.Tables {
		for j := range s.Tables[i].Indexes {
			if ix := &s.Tables[i].Indexes[j]; ix.Name == s.Tables[i].Name+"_shared_idx" {
				ix.Name = "shared_idx"
			}
		}
	}
}

// withReviewer gives posts a reviewer_id column.
func withReviewer(m *loadjson.Model) {
	addField(m, "Post", field.Int64("reviewer_id").Optional().Nillable())
}

// withSmallKey gives orgs an int32 ID, and users.org_id the same type.
func withSmallKey(m *loadjson.Model) {
	addField(m, "Org", field.Int32("id"))
	fieldOf(m, "User", "org_id").Type = "int32"
}

// allow covers the destructive codes of a case.
func allow(codes ...string) []migrate.Allow {
	var out []migrate.Allow
	for _, c := range codes {
		out = append(out, migrate.Allow{Code: c})
	}
	return out
}

var renameUsers = migrate.DiffOptions{RenameTables: map[migrate.TableRef]migrate.TableRef{users: accounts}}

var cases = []testCase{
	{name: "create-tables", empty: true, to: func(m *loadjson.Model) { addSchema(m, Alpha{}, Beta{}) }},
	{name: "drop-table", allow: allow("drop-table"), to: func(m *loadjson.Model) {
		dropSchema(m, "Post")
		dropEdge(m, "User", "posts")
	}},
	{name: "rename-table", to: func(m *loadjson.Model) { annotate(m, "User", schema.Table("accounts")) }, opts: renameUsers},
	{name: "add-column-optional", to: func(m *loadjson.Model) { addField(m, "User", field.String("nickname").Optional().Default("")) }},
	{name: "add-column-nillable", to: func(m *loadjson.Model) {
		addField(m, "User", field.String("nickname").Optional().Nillable())
	}},
	{name: "add-column-required", allow: allow("add-required-column"), to: func(m *loadjson.Model) { addField(m, "User", field.Time("joined_at")) }},
	{name: "add-column-default", to: func(m *loadjson.Model) { addField(m, "User", field.String("nickname").Default("anon")) }},
	{name: "add-column-schema-default", to: func(m *loadjson.Model) {
		addField(m, "User", field.Time("seen_at").SchemaDefault(map[string]string{
			field.Postgres: "CURRENT_TIMESTAMP", field.MySQL: "CURRENT_TIMESTAMP(6)", field.SQLite: "CURRENT_TIMESTAMP",
		}))
	}},
	{name: "drop-column", allow: allow("drop-column"), to: func(m *loadjson.Model) { dropField(m, "User", "email") }},
	{name: "rename-column", to: func(m *loadjson.Model) { fieldOf(m, "User", "name").StorageKey = "full_name" },
		opts: migrate.DiffOptions{RenameColumns: map[migrate.ColumnRef]string{{Table: users, Column: "name"}: "full_name"}}},
	{name: "widen-int", to: func(m *loadjson.Model) { fieldOf(m, "User", "logins").Type = "int64" }, dialects: pgMySQL},
	{name: "widen-varchar", to: func(m *loadjson.Model) { fieldOf(m, "User", "email").Size = 255 }, dialects: pgMySQL},
	{name: "narrow-varchar", allow: allow("alter-type"), to: func(m *loadjson.Model) { fieldOf(m, "User", "email").Size = 64 }, dialects: pgMySQL},
	{name: "set-not-null", allow: allow("set-not-null"), to: func(m *loadjson.Model) {
		f := fieldOf(m, "User", "bio")
		f.Optional, f.Nillable = false, false
	}},
	{name: "drop-not-null", to: func(m *loadjson.Model) {
		f := fieldOf(m, "User", "name")
		f.Optional, f.Nillable = true, true
	}},
	{name: "change-default", to: func(m *loadjson.Model) { fieldOf(m, "User", "role").Default = json.RawMessage(`"admin"`) }},
	{name: "change-comment", to: func(m *loadjson.Model) { fieldOf(m, "User", "name").Comment = "the name shown" }, dialects: pgMySQL},
	{name: "table-comment", to: func(m *loadjson.Model) { annotate(m, "User", schema.Comment("a person")) }, dialects: pgMySQL},
	{name: "key-type-change", from: withSmallKey, dialects: pgMySQL},
	{name: "add-index", to: func(m *loadjson.Model) { addIndex(m, "User", index.Fields("created_at")) }},
	{name: "drop-index", to: func(m *loadjson.Model) { dropIndex(m, "User", 0) }},
	{name: "rename-index", to: func(m *loadjson.Model) { ent(m, "User").Indexes[0].StorageKey = "users_name_idx" }},
	{name: "add-unique", allow: allow("add-unique"), to: func(m *loadjson.Model) { fieldOf(m, "User", "name").Unique = true }},
	{name: "add-check", to: func(m *loadjson.Model) { annotate(m, "User", schema.Check("users_logins_positive", "logins >= 0")) }},
	{name: "fk-index-replaced", to: withFKIndex},
	{name: "fk-index-restored", from: withFKIndex},
	{name: "add-foreign-key", from: withReviewer, to: func(m *loadjson.Model) {
		withReviewer(m)
		addEdge(m, "Post", edge.To("reviewer", User.Type).Unique().Field("reviewer_id"))
	}},
	{name: "drop-foreign-key", to: withReviewer, from: func(m *loadjson.Model) {
		withReviewer(m)
		addEdge(m, "Post", edge.To("reviewer", User.Type).Unique().Field("reviewer_id"))
	}},
	{name: "on-delete-change", to: func(m *loadjson.Model) { edgeOf(m, "Post", "author").OnDelete = "RESTRICT" }},
	// The enum cases are on PostgreSQL and MySQL: velox-gen stores an enum as
	// SQLite text with no check, so a changed value list changes nothing there
	// (decision 0090).
	{name: "enum-append", dialects: pgMySQL, to: func(m *loadjson.Model) { setEnum(m, "User", "role", "admin", "viewer", "owner") }},
	{name: "enum-insert", dialects: pgMySQL, allow: allow("reorder-enum"), to: func(m *loadjson.Model) { setEnum(m, "User", "role", "admin", "editor", "viewer") }},
	{name: "enum-remove", dialects: pgMySQL, allow: allow("drop-enum-value"), to: func(m *loadjson.Model) { setEnum(m, "User", "role", "viewer") }},
	{name: "enum-reorder", dialects: pgMySQL, allow: allow("reorder-enum"), to: func(m *loadjson.Model) { setEnum(m, "User", "role", "viewer", "admin") }},
	{name: "enum-shared", dialects: pgMySQL, allow: allow("drop-enum-value"), to: func(m *loadjson.Model) {
		setEnum(m, "User", "status", "active", "pending")
		setEnum(m, "Post", "status", "active", "pending")
	}},
	{name: "enum-opt-out", allow: allow("alter-type"), dialects: pgMySQL, to: func(m *loadjson.Model) {
		fieldOf(m, "User", "role").SchemaType = map[string]string{field.Postgres: "text", field.MySQL: "varchar(16)"}
	}},
	{name: "literals", to: func(m *loadjson.Model) {
		addField(m, "User", field.String("motto").Default(`it's a \ test`).Comment(`it's a \ comment`))
		setEnum(m, "User", "role", "admin", "viewer", "o'neil")
	}},
	{name: "schema-qualified", dialects: pgMySQL, to: func(m *loadjson.Model) { addSchema(m, Invoice{}, Payer{}) }},
	{name: "add-identity", dialects: pg, fromDB: func(d velox.Dialect) *migrate.Schema {
		s := mustTables(d, nil)
		id := column(table(s, "users"), "id")
		id.Identity, id.Sequence = false, ""
		return s
	}},
	{name: "serial-to-identity", dialects: pg, fromDB: func(d velox.Dialect) *migrate.Schema {
		s := mustTables(d, nil)
		id := column(table(s, "users"), "id")
		id.Identity, id.Default = false, "nextval('users_id_seq')"
		return s
	}},
	// The swap rebuilds the partial index and the check that cast to the
	// type, which PostgreSQL would otherwise rebuild against the old one.
	{name: "enum-swap-dependents", dialects: pg, allow: allow("drop-enum-value"), from: func(m *loadjson.Model) {
		setEnum(m, "User", "role", "admin", "viewer", "guest")
		addIndex(m, "User", index.Fields("name").Where("role = 'admin'"))
	}, to: func(m *loadjson.Model) {
		addIndex(m, "User", index.Fields("name").Where("role = 'admin'"))
	}, both: func(s *migrate.Schema) {
		tb := table(s, "users")
		tb.Checks = append(tb.Checks, migrate.Check{Name: "users_role_known", Expr: `"role" IN ('admin', 'viewer')`})
	}},
	// An index whose name a new table takes is dropped before the table
	// is created.
	{name: "index-name-to-table", dialects: pg, fromDB: func(d velox.Dialect) *migrate.Schema {
		s := mustTables(d, nil)
		tb := table(s, "users")
		tb.Indexes = append(tb.Indexes, migrate.Index{Name: "tags", Columns: []string{"created_at"}})
		return s
	}, to: func(m *loadjson.Model) { addSchema(m, Tag{}) }},
	{name: "collation", allow: allow("convert-collation"), dialects: mysqlOnly, fromDB: func(d velox.Dialect) *migrate.Schema {
		s := mustTables(d, nil)
		table(s, "users").Collation = "utf8mb4_0900_ai_ci"
		return s
	}, to: func(m *loadjson.Model) { annotate(m, "User", schema.Collation("utf8mb4_bin")) }},
	{name: "rebuild-parent", dialects: sqlite, allow: allow("alter-type"), to: func(m *loadjson.Model) { fieldOf(m, "User", "logins").Type = "float64" }},
	{name: "rename-table-drop-column", allow: allow("drop-column"), to: func(m *loadjson.Model) {
		annotate(m, "User", schema.Table("accounts"))
		dropField(m, "User", "email")
	}, opts: renameUsers},
	{name: "rename-table-key-type", dialects: pgMySQL, from: withSmallKey,
		to: func(m *loadjson.Model) { annotate(m, "User", schema.Table("accounts")) }, opts: renameUsers},
	{name: "reuse-dropped-name", allow: allow("drop-table"), both: sharedIdx, from: func(m *loadjson.Model) {
		addIndex(m, "Post", index.Fields("title").StorageKey("shared_idx"))
		edgeOf(m, "Post", "author").Symbols = []string{"shared_fk"}
	}, to: func(m *loadjson.Model) {
		dropSchema(m, "Post")
		dropEdge(m, "User", "posts")
		addIndex(m, "User", index.Fields("created_at").StorageKey("shared_idx"))
		edgeOf(m, "User", "mentor").Symbols = []string{"shared_fk"}
	}},
	{name: "drop-column-in-index", allow: allow("drop-column"), from: func(m *loadjson.Model) {
		addIndex(m, "User", index.Fields("org_id", "logins"))
	}, to: func(m *loadjson.Model) { dropField(m, "User", "logins") }},
	{name: "drop-column-in-unique", allow: allow("drop-column"), from: func(m *loadjson.Model) {
		addIndex(m, "User", index.Fields("name", "logins").Unique())
	}, to: func(m *loadjson.Model) { dropField(m, "User", "logins") }},
	{name: "drop-column-in-unique-key", allow: allow("drop-column"), from: func(m *loadjson.Model) {
		addIndex(m, "User", index.Fields("org_id", "logins").Unique())
	}, to: func(m *loadjson.Model) { dropField(m, "User", "logins") }},
	{name: "rename-column-opt-out", both: roleCheck, from: optOutRole, to: func(m *loadjson.Model) {
		optOutRole(m)
		fieldOf(m, "User", "role").StorageKey = "kind"
	}, opts: migrate.DiffOptions{RenameColumns: map[migrate.ColumnRef]string{{Table: users, Column: "role"}: "kind"}}},
	{name: "rename-column-check", both: roleCheck, to: func(m *loadjson.Model) { fieldOf(m, "User", "role").StorageKey = "kind" },
		opts: migrate.DiffOptions{RenameColumns: map[migrate.ColumnRef]string{{Table: users, Column: "role"}: "kind"}}},
	{name: "rename-column-where", dialects: []velox.Dialect{velox.Postgres, velox.SQLite}, from: func(m *loadjson.Model) {
		addIndex(m, "User", index.Fields("email").Where(`"bio" <> 'bio'`))
		addIndex(m, "User", index.Fields("name").Where(`bio IS NOT NULL`))
	}, to: func(m *loadjson.Model) {
		fieldOf(m, "User", "bio").StorageKey = "about"
		addIndex(m, "User", index.Fields("email").Where(`"about" <> 'bio'`))
		addIndex(m, "User", index.Fields("name").Where(`about IS NOT NULL`))
	}, opts: migrate.DiffOptions{RenameColumns: map[migrate.ColumnRef]string{{Table: users, Column: "bio"}: "about"}}},
}
