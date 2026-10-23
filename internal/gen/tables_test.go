package gen

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
	"github.com/syssam/velox2/schema/mixin"
)

var allDialects = [...]velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite}

// tables builds ss and the database model of the graph for d.
func tables(t *testing.T, d velox.Dialect, ss ...schema.Interface) *migrate.Schema {
	t.Helper()
	s, err := Tables(mustBuild(t, ss...), d)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func table(t *testing.T, s *migrate.Schema, name string) *migrate.Table {
	t.Helper()
	for i := range s.Tables {
		if s.Tables[i].Name == name {
			return &s.Tables[i]
		}
	}
	t.Fatalf("%v: no table %s", s.Dialect, name)
	return nil
}

func column(t *testing.T, tb *migrate.Table, name string) *migrate.Column {
	t.Helper()
	for i := range tb.Columns {
		if tb.Columns[i].Name == name {
			return &tb.Columns[i]
		}
	}
	t.Fatalf("table %s: no column %s", tb.Name, name)
	return nil
}

func tableIndexNames(tb *migrate.Table) []string {
	var out []string
	for _, ix := range tb.Indexes {
		out = append(out, ix.Name)
	}
	return out
}

// --- the golden schemas: every field type and option, every relation kind.

type tbOrderStatus string

func (tbOrderStatus) Values() []string { return []string{"open", "paid", "void"} }

type tbMoney struct{}

func (tbMoney) Value() (driver.Value, error) { return nil, nil }

type tbUser struct{ schema.Schema }

func (tbUser) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Comment("a person")}
}
func (tbUser) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }
func (tbUser) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").Comment("the display name"),
		field.String("email").MaxLen(120).Unique(),
		field.Text("bio").Optional(),
		field.Bool("active").Nillable().Default(true),
		field.Bool("admin").Optional(),
		field.Int("age").Optional(),
		field.Float("score").Nillable().Default(1.5),
		field.Enum("role").Values("admin", "viewer").Default("viewer"),
		field.Enum("mood").Values("calm", "busy").SchemaType(map[string]string{field.Postgres: "mood"}),
		field.Enum("tier").Values("free", "pro").SchemaType(map[string]string{field.Postgres: "text", field.MySQL: "varchar(16)"}),
		field.String("locale").SchemaDefault(map[string]string{field.Postgres: "'en'::text", field.MySQL: "'en'", field.SQLite: "'en'"}),
		field.Strings("tags").Optional(),
	}
}
func (tbUser) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("posts", tbPost.Type).Comment("what the user wrote"),
		edge.To("tokens", tbToken.Type).OnDelete(edge.Cascade),
		edge.To("profile", tbProfile.Type).Unique(),
		edge.To("groups", tbGroup.Type).StorageKey(edge.Symbols("membership_user", "membership_group")),
		edge.To("friends", tbUser.Type),
		edge.To("spouse", tbUser.Type).Unique(),
	}
}
func (tbUser) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("name").StorageKey("users_by_name"),
		index.Fields("email").Where("active").Unique(),
	}
}

type tbPost struct{ schema.Schema }

func (tbPost) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("posts")} }
func (tbPost) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("author_id").Optional().Nillable(),
		field.String("title").MaxLen(200),
		field.EnumOf[tbOrderStatus]("status").Default("open"),
		field.Bytes("body").Optional().Nillable(),
		field.Bytes("digest").MaxLen(32).Optional().Nillable(),
		field.Time("published_at").Optional().Nillable(),
		field.UUID("ref", uuidValue{}).Optional().Nillable(),
		field.JSON("meta", jsonShape{}),
		field.Other("price", tbMoney{}).SchemaType(map[string]string{field.Postgres: "numeric(10,2)", field.MySQL: "decimal(10,2)", field.SQLite: "numeric"}),
		field.Enum("mood").Values("calm", "busy").SchemaType(map[string]string{field.Postgres: "mood"}),
	}
}
func (tbPost) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("author", tbUser.Type).Ref("posts").Unique().Field("author_id").OnDelete(edge.SetNull).StorageKey(edge.Symbol("post_author")),
	}
}

type tbToken struct{ schema.Schema }

func (tbToken) Fields() []schema.Field {
	return []schema.Field{field.String("id"), field.Int8("i8"), field.Int16("i16"), field.Int32("i32"), field.Uint8("u8"), field.Uint16("u16"), field.Uint32("u32"), field.Uint("u"), field.Uint64("u64"), field.Float32("f32")}
}

type tbProfile struct{ schema.Schema }

func (tbProfile) Fields() []schema.Field { return []schema.Field{field.String("handle")} }
func (tbProfile) Edges() []schema.Edge {
	return []schema.Edge{edge.From("user", tbUser.Type).Ref("profile").Unique().Required().Comment("whose profile")}
}

type tbGroup struct{ schema.Schema }

func (tbGroup) Fields() []schema.Field {
	return []schema.Field{field.EnumOf[tbOrderStatus]("status").Default("open")}
}
func (tbGroup) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", tbUser.Type).Ref("groups")}
}

type tbInvoice struct{ schema.Schema }

func (tbInvoice) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("billing", "invoices"), schema.Comment("a bill")}
}
func (tbInvoice) Fields() []schema.Field {
	return []schema.Field{field.EnumOf[tbOrderStatus]("status"), field.Enum("mood").Values("calm", "busy").SchemaType(map[string]string{field.Postgres: "mood"})}
}
func (tbInvoice) Edges() []schema.Edge {
	return []schema.Edge{edge.To("payer", tbUser.Type).Unique().Required()}
}

func goldenSchemas(d velox.Dialect) []schema.Interface {
	ss := []schema.Interface{tbUser{}, tbPost{}, tbToken{}, tbProfile{}, tbGroup{}}
	if d != velox.SQLite {
		ss = append(ss, tbInvoice{})
	}
	return ss
}

func TestTablesGolden(t *testing.T) {
	for _, d := range allDialects {
		s := tables(t, d, goldenSchemas(d)...)
		got, err := json.MarshalIndent(s, "", "\t")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join("testdata", "tables", d.String()+".json")
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%v: the model differs from %s; run with -update and read the diff:\n%s", d, path, got)
		}
	}
}

// The model holds positions: a table's is its first declared field's.
func TestTablesPositions(t *testing.T) {
	g := mustBuild(t, goldenSchemas(velox.Postgres)...)
	s, err := Tables(g, velox.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	u, p := g.Entity("tbUser"), g.Entity("tbPost")
	users, posts := table(t, s, "tb_users"), table(t, s, "posts")
	if users.Pos == "" || users.Pos != u.Fields[1].Pos {
		t.Errorf("table pos %q, want the first declared field's %q", users.Pos, u.Fields[1].Pos)
	}
	if c := column(t, users, "email"); c.Pos != u.Field("email").Pos {
		t.Errorf("column pos %q", c.Pos)
	}
	if fk := posts.ForeignKeys[0]; fk.Pos != p.Edge("author").Pos {
		t.Errorf("foreign key pos %q, want the edge's %q", fk.Pos, p.Edge("author").Pos)
	}
	if ix := users.Indexes[slices.IndexFunc(users.Indexes, func(ix migrate.Index) bool { return ix.Name == "users_by_name" })]; ix.Pos != u.Indexes[0].Pos || ix.Pos == "" {
		t.Errorf("index pos %q", ix.Pos)
	}
	if e := s.Enums[slices.IndexFunc(s.Enums, func(e migrate.Enum) bool { return e.Name == "tb_users_role" })]; e.Pos != u.Field("role").Pos {
		t.Errorf("enum pos %q", e.Pos)
	}
	// An implicit foreign key, and its index, are at the edge that declares them.
	tokens, at := table(t, s, "tb_tokens"), u.Edge("tokens").Pos
	if c := column(t, tokens, "tb_user_tokens"); c.Pos != at || at == "" {
		t.Errorf("implicit column pos %q, want the edge's %q", c.Pos, at)
	}
	if ix := tokens.Indexes[0]; ix.Name != "tb_tokens_tb_user_tokens" || ix.Pos != at {
		t.Errorf("foreign-key index %+v, want the edge's pos %q", ix, at)
	}
}

// --- types

type tbTypes struct{ schema.Schema }

func (tbTypes) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("tb_types")}
}

func (tbTypes) Fields() []schema.Field {
	return []schema.Field{
		field.Bool("b"), field.Int8("i8"), field.Int16("i16"), field.Int32("i32"), field.Int("i"), field.Int64("i64"),
		field.Uint8("u8"), field.Uint16("u16"), field.Uint32("u32"), field.Uint("u"), field.Uint64("u64"),
		field.Float32("f32"), field.Float("f64"),
		field.String("s"), field.String("s1").MaxLen(1), field.Text("txt"),
		field.String("s16383").MaxLen(16383), field.String("s16384").MaxLen(16384), field.String("s65535").MaxLen(65535),
		field.String("s65536").MaxLen(65536), field.String("s16777215").MaxLen(16777215), field.String("s16777216").MaxLen(16777216),
		field.String("s10485760").MaxLen(10485760), field.String("s10485761").MaxLen(10485761),
		field.Bytes("by"), field.Bytes("by255").MaxLen(255), field.Bytes("by256").MaxLen(256),
		field.Bytes("by65536").MaxLen(65536), field.Bytes("by16777216").MaxLen(16777216),
		field.Time("t"), field.UUID("uu", uuidValue{}), field.JSON("j", jsonShape{}),
		field.Other("o", tbMoney{}).SchemaType(map[string]string{field.Postgres: "MONEY", field.MySQL: "INT(11)", field.SQLite: "NUMERIC"}),
		field.Int32("st").SchemaType(map[string]string{field.MySQL: "INT(11)", field.Postgres: "int4"}),
	}
}

func TestTablesTypes(t *testing.T) {
	want := map[string][3]string{ // PostgreSQL, MySQL, SQLite
		"b":          {"boolean", "tinyint(1)", "boolean"},
		"i8":         {"smallint", "tinyint", "integer"},
		"i16":        {"smallint", "smallint", "integer"},
		"i32":        {"integer", "int", "integer"},
		"i":          {"bigint", "bigint", "integer"},
		"i64":        {"bigint", "bigint", "integer"},
		"u8":         {"smallint", "tinyint unsigned", "integer"},
		"u16":        {"integer", "smallint unsigned", "integer"},
		"u32":        {"bigint", "int unsigned", "integer"},
		"u":          {"numeric(20,0)", "bigint unsigned", "integer"},
		"u64":        {"numeric(20,0)", "bigint unsigned", "integer"},
		"f32":        {"real", "float", "real"},
		"f64":        {"double precision", "double", "real"},
		"s":          {"text", "varchar(255)", "text"},
		"s1":         {"character varying(1)", "varchar(1)", "text"},
		"txt":        {"text", "longtext", "text"},
		"s16383":     {"character varying(16383)", "varchar(16383)", "text"},
		"s16384":     {"character varying(16384)", "text", "text"},
		"s65535":     {"character varying(65535)", "text", "text"},
		"s65536":     {"character varying(65536)", "mediumtext", "text"},
		"s16777215":  {"text", "mediumtext", "text"},
		"s16777216":  {"text", "longtext", "text"},
		"s10485760":  {"character varying(10485760)", "mediumtext", "text"},
		"s10485761":  {"text", "mediumtext", "text"},
		"by":         {"bytea", "blob", "blob"},
		"by255":      {"bytea", "varbinary(255)", "blob"},
		"by256":      {"bytea", "blob", "blob"},
		"by65536":    {"bytea", "mediumblob", "blob"},
		"by16777216": {"bytea", "longblob", "blob"},
		"t":          {"timestamp with time zone", "datetime(6)", "datetime"},
		"uu":         {"uuid", "char(36)", "text"},
		"j":          {"jsonb", "json", "text"},
		"o":          {"money", "int", "numeric"},
		"st":         {"integer", "int", "integer"},
	}
	for i, d := range allDialects {
		tb := table(t, tables(t, d, tbTypes{}), "tb_types")
		for name, w := range want {
			if c := column(t, tb, name); c.Type != w[i] {
				t.Errorf("%v: %s is %q, want %q", d, name, c.Type, w[i])
			}
		}
	}
}

// --- identity

type tbIdentU64 struct{ schema.Schema }

func (tbIdentU64) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("users")}
}
func (tbIdentU64) Fields() []schema.Field { return []schema.Field{field.Uint64("id")} }

type tbIdentStr struct{ schema.Schema }

func (tbIdentStr) Fields() []schema.Field { return []schema.Field{field.String("id")} }

type tbIdentUUID struct{ schema.Schema }

func (tbIdentUUID) Fields() []schema.Field { return []schema.Field{field.UUID("id", uuidValue{})} }

type tbIdentI64 struct{ schema.Schema }

func (tbIdentI64) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("users")}
}

func TestTablesIdentity(t *testing.T) {
	id := column(t, table(t, tables(t, velox.Postgres, tbIdentI64{}), "users"), "id")
	if id.Type != "bigint" || !id.Identity || id.Sequence != "users_id_seq" || id.Default != "" || id.Nullable {
		t.Errorf("int64 ID on PostgreSQL: %+v", id)
	}
	for d, want := range map[velox.Dialect]migrate.Column{
		velox.Postgres: {Name: "id", Type: "bigint", Identity: true, Sequence: "users_id_seq"},
		velox.MySQL:    {Name: "id", Type: "bigint unsigned", Identity: true},
		velox.SQLite:   {Name: "id", Type: "integer", Identity: true},
	} {
		got := *column(t, table(t, tables(t, d, tbIdentU64{}), "users"), "id")
		if got.Pos = ""; got != want {
			t.Errorf("%v: uint64 ID %+v, want %+v", d, got, want)
		}
	}
	for _, d := range allDialects {
		for _, s := range []schema.Interface{tbIdentStr{}, tbIdentUUID{}} {
			m := tables(t, d, s)
			if c := m.Tables[0].Columns[0]; c.Identity || c.Sequence != "" {
				t.Errorf("%v: %s ID %+v has an identity", d, m.Tables[0].Name, c)
			}
		}
	}
}

// --- foreign keys copy the referenced ID's type

type tbFKOwner struct{ schema.Schema }

func (tbFKOwner) Fields() []schema.Field { return []schema.Field{field.Uint64("id")} }
func (tbFKOwner) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", tbFKPet.Type), edge.To("toys", tbFKToy.Type), edge.To("tags", tbFKTag.Type)}
}

type tbFKPet struct{ schema.Schema }

func (tbFKPet) Fields() []schema.Field { return []schema.Field{field.Uint64("owner_id")} }
func (tbFKPet) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", tbFKOwner.Type).Ref("pets").Unique().Required().Field("owner_id")}
}

type tbFKToy struct{ schema.Schema }

type tbFKTag struct{ schema.Schema }

func (tbFKTag) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owners", tbFKOwner.Type).Ref("tags")}
}

func TestTablesForeignKeyCopiesType(t *testing.T) {
	for d, want := range map[velox.Dialect]string{velox.Postgres: "bigint", velox.MySQL: "bigint unsigned"} {
		s := tables(t, d, tbFKOwner{}, tbFKPet{}, tbFKToy{}, tbFKTag{})
		for _, c := range []struct{ table, column string }{
			{"tb_fk_pets", "owner_id"}, {"tb_fk_toys", "tb_fk_owner_toys"}, {"tb_fk_owner_tags", "tb_fk_owner_id"},
		} {
			if col := column(t, table(t, s, c.table), c.column); col.Type != want || col.Identity || col.Sequence != "" {
				t.Errorf("%v: %s.%s is %+v, want %s", d, c.table, c.column, col, want)
			}
		}
		if col := column(t, table(t, s, "tb_fk_owner_tags"), "tb_fk_tag_id"); col.Type != "bigint" && d == velox.Postgres {
			t.Errorf("%v: the join table's tag column is %q", d, col.Type)
		}
	}
}

// --- nullability

type tbNull struct{ schema.Schema }

func (tbNull) Fields() []schema.Field {
	return []schema.Field{
		field.String("nillable").Nillable(),
		field.String("optional").Optional(),
		field.Strings("slice").Optional(),
		field.JSON("shape", jsonShape{}).Optional().Nillable(),
		field.JSON("req", jsonShape{}),
	}
}

func TestTablesNullable(t *testing.T) {
	for _, d := range allDialects {
		tb := table(t, tables(t, d, tbNull{}), "tb_nulls")
		for name, want := range map[string]bool{"id": false, "nillable": true, "optional": false, "slice": true, "shape": true, "req": false} {
			if c := column(t, tb, name); c.Nullable != want {
				t.Errorf("%v: %s nullable %v, want %v", d, name, c.Nullable, want)
			}
		}
	}
}

// --- defaults

type tbDefaults struct{ schema.Schema }

func (tbDefaults) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("tb_defaults")}
}

func (tbDefaults) Fields() []schema.Field {
	return []schema.Field{
		field.Time("at").SchemaDefault(map[string]string{field.Postgres: "now()", field.MySQL: "CURRENT_TIMESTAMP(6)", field.SQLite: "CURRENT_TIMESTAMP"}),
		field.String("pg_only").SchemaDefault(map[string]string{field.Postgres: "'x'::text"}),
		field.String("s").Default(`it's a \ test`),
		field.Text("txt").Default(`it's a \ test`),
		field.Bool("flag").Nillable().Default(true),
		field.Bool("off").Nillable().Default(false),
		field.Float("f").Nillable().Default(1.5),
		field.Int("n").Nillable().Default(-3),
		field.Uint64("big").Nillable().Default(18446744073709551615),
		field.String("os").Optional(),
		field.Text("otxt").Optional(),
		field.Bool("ob").Optional(),
		field.Int("oi").Optional(),
		field.Float32("of").Optional(),
		field.String("ns").Optional().Nillable(),
		field.String("req"),
		field.String("fn").DefaultFunc(func() string { return "" }),
		field.Time("now").Default(func() time.Time { return time.Time{} }),
		field.UUID("u", uuidValue{}).Default(func() uuidValue { return uuidValue{} }),
		field.JSON("j", jsonShape{}).Default(jsonShape{W: 1}),
		field.Enum("role").Values("admin", "viewer").Default("viewer"),
		field.Enum("mode").Values("a", "b").Default("a").SchemaType(map[string]string{field.MySQL: "text"}),
		field.String("blobby").Default("x").SchemaType(map[string]string{field.MySQL: "blob"}),
		field.String("doc").Optional().SchemaType(map[string]string{field.MySQL: "json"}),
	}
}

func TestTablesDefaults(t *testing.T) {
	want := map[string][3]string{ // PostgreSQL, MySQL, SQLite
		"id":      {"", "", ""},
		"at":      {"now()", "CURRENT_TIMESTAMP(6)", "CURRENT_TIMESTAMP"},
		"pg_only": {"'x'::text", "", ""},
		"s":       {`'it''s a \ test'`, `'it''s a \\ test'`, `'it''s a \ test'`},
		"txt":     {`'it''s a \ test'`, `('it''s a \\ test')`, `'it''s a \ test'`},
		"flag":    {"true", "1", "1"},
		"off":     {"false", "0", "0"},
		"f":       {"1.5", "1.5", "1.5"},
		"n":       {"-3", "-3", "-3"},
		"big":     {"18446744073709551615", "18446744073709551615", "18446744073709551615"},
		"os":      {"''", "''", "''"},
		"otxt":    {"''", "('')", "''"},
		"ob":      {"false", "0", "0"},
		"oi":      {"0", "0", "0"},
		"of":      {"0", "0", "0"},
		"ns":      {"", "", ""},
		"req":     {"", "", ""},
		"fn":      {"", "", ""},
		"now":     {"", "", ""},
		"u":       {"", "", ""},
		"j":       {"", "", ""},
		"role":    {"'viewer'", "'viewer'", "'viewer'"},
		"mode":    {"'a'", "('a')", "'a'"},
		"blobby":  {"'x'", "('x')", "'x'"},
		"doc":     {"''", "('')", "''"},
	}
	for i, d := range allDialects {
		tb := table(t, tables(t, d, tbDefaults{}), "tb_defaults")
		for name, w := range want {
			if c := column(t, tb, name); c.Default != w[i] {
				t.Errorf("%v: %s default %q, want %q", d, name, c.Default, w[i])
			}
		}
	}
}

// --- enums

type tbEnumUser struct{ schema.Schema }

func (tbEnumUser) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("users")}
}
func (tbEnumUser) Fields() []schema.Field {
	return []schema.Field{
		field.Enum("kind").NamedValues("X", "x", "ONeil", "o'neil"),
		field.EnumOf[OrderStatus]("status"),
		field.Enum("mood").Values("calm").SchemaType(map[string]string{field.Postgres: "mood"}),
		field.Enum("role").Values("a", "b").SchemaType(map[string]string{field.Postgres: "text"}),
	}
}

type tbEnumOrder struct{ schema.Schema }

func (tbEnumOrder) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("orders")}
}
func (tbEnumOrder) Fields() []schema.Field {
	return []schema.Field{field.EnumOf[OrderStatus]("status"), field.Enum("mood").Values("calm").SchemaType(map[string]string{field.Postgres: "mood"})}
}

type OrderStatus string

func (OrderStatus) Values() []string { return []string{"open", "paid"} }

func TestTablesEnums(t *testing.T) {
	pg := tables(t, velox.Postgres, tbEnumUser{}, tbEnumOrder{})
	users := table(t, pg, "users")
	for name, typ := range map[string]string{"kind": `"users_kind"`, "status": `"order_status"`, "mood": `"mood"`, "role": "text"} {
		if c := column(t, users, name); c.Type != typ {
			t.Errorf("PostgreSQL: %s is %q, want %q", name, c.Type, typ)
		}
	}
	wantEnums := []migrate.Enum{
		{Name: "mood", Values: []string{"calm"}},
		{Name: "order_status", Values: []string{"open", "paid"}},
		{Name: "users_kind", Values: []string{"x", "o'neil"}},
	}
	if len(pg.Enums) != len(wantEnums) {
		t.Fatalf("enums %+v", pg.Enums)
	}
	for i, e := range pg.Enums {
		if e.Schema != wantEnums[i].Schema || e.Name != wantEnums[i].Name || !slices.Equal(e.Values, wantEnums[i].Values) {
			t.Errorf("enum %d: %+v, want %+v", i, e, wantEnums[i])
		}
	}
	if want := []migrate.Check{{Name: "users_role_check", Expr: `"role" IN ('a', 'b')`}}; !checksEqual(users.Checks, want) {
		t.Errorf("PostgreSQL checks %+v, want %+v", users.Checks, want)
	}

	my := table(t, tables(t, velox.MySQL, tbEnumUser{}, tbEnumOrder{}), "users")
	if c := column(t, my, "kind"); c.Type != `enum('x','o''neil')` {
		t.Errorf("MySQL kind is %q", c.Type)
	}
	if len(my.Checks) != 0 {
		t.Errorf("MySQL checks %+v", my.Checks)
	}

	lite := tables(t, velox.SQLite, tbEnumUser{}, tbEnumOrder{})
	lu := table(t, lite, "users")
	if c := column(t, lu, "kind"); c.Type != "text" {
		t.Errorf("SQLite kind is %q", c.Type)
	}
	want := []migrate.Check{
		{Name: "users_kind_check", Expr: `"kind" IN ('x', 'o''neil')`},
		{Name: "users_mood_check", Expr: `"mood" IN ('calm')`},
		{Name: "users_role_check", Expr: `"role" IN ('a', 'b')`},
		{Name: "users_status_check", Expr: `"status" IN ('open', 'paid')`},
	}
	if !checksEqual(lu.Checks, want) || len(lite.Enums) != 0 {
		t.Errorf("SQLite checks %+v, enums %+v", lu.Checks, lite.Enums)
	}
}

func checksEqual(a, b []migrate.Check) bool {
	return slices.EqualFunc(a, b, func(x, y migrate.Check) bool { return x.Name == y.Name && x.Expr == y.Expr })
}

// A shared type in two database schemas is one type in each.
func TestTablesEnumsPerSchema(t *testing.T) {
	s := tables(t, velox.Postgres, tbUser{}, tbPost{}, tbToken{}, tbProfile{}, tbGroup{}, tbInvoice{})
	var got []string
	for _, e := range s.Enums {
		got = append(got, migrate.TableRef{Schema: e.Schema, Name: e.Name}.String())
	}
	want := []string{"tb_order_status", "tb_users_role", "mood", "billing.mood", "billing.tb_order_status"}
	slices.Sort(want)
	sorted := slices.Clone(got)
	slices.Sort(sorted)
	if !slices.Equal(sorted, want) {
		t.Errorf("enums %q, want %q", got, want)
	}
	if c := column(t, table(t, s, "invoices"), "status"); c.Type != `"billing"."tb_order_status"` {
		t.Errorf("invoices.status is %q", c.Type)
	}
}

// --- indexes

type tbIxOwner struct{ schema.Schema }

func (tbIxOwner) Fields() []schema.Field {
	return []schema.Field{field.String("code").Unique(), field.String("slug").Unique(), field.String("name")}
}
func (tbIxOwner) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("a", tbIxA.Type), edge.To("b", tbIxB.Type), edge.To("c", tbIxC.Type), edge.To("tags", tbIxTag.Type),
	}
}
func (tbIxOwner) Indexes() []schema.Index { return []schema.Index{index.Fields("slug").Unique()} }

// tbIxA's key has no index; tbIxB's leads one; tbIxC's has only a partial one.
type tbIxA struct{ schema.Schema }

type tbIxB struct{ schema.Schema }

func (tbIxB) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (tbIxB) Indexes() []schema.Index {
	return []schema.Index{index.Fields("tb_ix_owner_b", "name")}
}
func (tbIxB) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", tbIxOwner.Type).Ref("b").Unique()}
}

type tbIxC struct{ schema.Schema }

func (tbIxC) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", tbIxOwner.Type).Ref("c").Unique()}
}
func (tbIxC) Indexes() []schema.Index {
	return []schema.Index{index.Edges("owner").Where("tb_ix_owner_c > 0")}
}

type tbIxTag struct{ schema.Schema }

func (tbIxTag) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owners", tbIxOwner.Type).Ref("tags")}
}

func TestTablesIndexes(t *testing.T) {
	for _, d := range allDialects {
		s := tables(t, d, tbIxOwner{}, tbIxA{}, tbIxB{}, tbIxC{}, tbIxTag{})
		for tableName, want := range map[string][]string{
			"tb_ix_owners":     {"tb_ix_owners_code_unique", "tb_ix_owners_slug_unique"},
			"tb_ixas":          {"tb_ixas_tb_ix_owner_a"},
			"tb_ixbs":          {"tb_ixbs_tb_ix_owner_b_name"},
			"tb_ixcs":          {"tb_ixcs_tb_ix_owner_c", indexName("tb_ixcs", &Index{Columns: []string{"tb_ix_owner_c"}, Where: "tb_ix_owner_c > 0"})},
			"tb_ix_owner_tags": {"tb_ix_owner_tags_tb_ix_tag_id"},
		} {
			tb := table(t, s, tableName)
			if got := tableIndexNames(tb); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
				t.Errorf("%v: %s indexes %q, want %q", d, tableName, got, want)
			}
		}
		owners := table(t, s, "tb_ix_owners")
		if n := len(owners.Indexes); n != 2 {
			t.Fatalf("%v: owners %+v", d, owners.Indexes)
		}
		for _, ix := range owners.Indexes {
			if !ix.Unique || len(ix.Columns) != 1 {
				t.Errorf("%v: %+v", d, ix)
			}
		}
		if ix := table(t, s, "tb_ixas").Indexes[0]; ix.Unique || !slices.Equal(ix.Columns, []string{"tb_ix_owner_a"}) {
			t.Errorf("%v: foreign-key index %+v", d, ix)
		}
	}
}

// --- foreign keys

type tbFkUser struct{ schema.Schema }

func (tbFkUser) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("pets", tbFkPet.Type),
		edge.To("cars", tbFkCar.Type).OnDelete(edge.Cascade),
		edge.To("bikes", tbFkBike.Type).StorageKey(edge.Symbol("bike_owner")),
		edge.To("groups", tbFkGroup.Type).StorageKey(edge.Symbols("gm_user", "gm_group")),
		edge.To("teams", tbFkTeam.Type),
	}
}

type tbFkPet struct{ schema.Schema }

func (tbFkPet) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", tbFkUser.Type).Ref("pets").Unique().StorageKey(edge.Symbol("pet_owner")).OnDelete(edge.SetNull)}
}

type tbFkCar struct{ schema.Schema }

// tbFkBike holds the key; the Symbol is on the other edge of the pair.
type tbFkBike struct{ schema.Schema }

func (tbFkBike) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", tbFkUser.Type).Ref("bikes").Unique()}
}

type tbFkGroup struct{ schema.Schema }

func (tbFkGroup) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", tbFkUser.Type).Ref("groups")}
}

type tbFkTeam struct{ schema.Schema }

func (tbFkTeam) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", tbFkUser.Type).Ref("teams")}
}

func TestTablesForeignKeys(t *testing.T) {
	for _, d := range allDialects {
		s := tables(t, d, tbFkUser{}, tbFkPet{}, tbFkCar{}, tbFkBike{}, tbFkGroup{}, tbFkTeam{})
		users := migrate.TableRef{Name: "tb_fk_users"}
		for tableName, want := range map[string][]migrate.ForeignKey{
			"tb_fk_pets":  {{Name: "pet_owner", Columns: []string{"tb_fk_user_pets"}, RefTable: users, RefColumns: []string{"id"}, OnDelete: "SET NULL"}},
			"tb_fk_bikes": {{Name: "bike_owner", Columns: []string{"tb_fk_user_bikes"}, RefTable: users, RefColumns: []string{"id"}}},
			"tb_fk_cars":  {{Name: "tb_fk_cars_tb_fk_user_cars_fkey", Columns: []string{"tb_fk_user_cars"}, RefTable: users, RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
			"tb_fk_user_groups": {
				{Name: "gm_group", Columns: []string{"tb_fk_group_id"}, RefTable: migrate.TableRef{Name: "tb_fk_groups"}, RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				{Name: "gm_user", Columns: []string{"tb_fk_user_id"}, RefTable: users, RefColumns: []string{"id"}, OnDelete: "CASCADE"},
			},
			"tb_fk_user_teams": {
				{Name: "tb_fk_user_teams_tb_fk_team_id_fkey", Columns: []string{"tb_fk_team_id"}, RefTable: migrate.TableRef{Name: "tb_fk_teams"}, RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				{Name: "tb_fk_user_teams_tb_fk_user_id_fkey", Columns: []string{"tb_fk_user_id"}, RefTable: users, RefColumns: []string{"id"}, OnDelete: "CASCADE"},
			},
		} {
			got := table(t, s, tableName).ForeignKeys
			if !slices.EqualFunc(got, want, func(x, y migrate.ForeignKey) bool {
				return x.Name == y.Name && x.RefTable == y.RefTable && x.OnDelete == y.OnDelete &&
					slices.Equal(x.Columns, y.Columns) && slices.Equal(x.RefColumns, y.RefColumns)
			}) {
				t.Errorf("%v: %s foreign keys\n%+v\nwant\n%+v", d, tableName, got, want)
			}
		}
		jt := table(t, s, "tb_fk_user_groups")
		if !slices.Equal(jt.PrimaryKey, []string{"tb_fk_user_id", "tb_fk_group_id"}) {
			t.Errorf("%v: join table key %q", d, jt.PrimaryKey)
		}
	}
}

// --- comments and collation

type tbCmUser struct{ schema.Schema }

func (tbCmUser) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Comment("people")}
}
func (tbCmUser) Fields() []schema.Field {
	return []schema.Field{field.String("name").Comment("full name")}
}
func (tbCmUser) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", tbCmPet.Type).Comment("owned pets"), edge.To("groups", tbCmGroup.Type).Comment("member of")}
}

type tbCmPet struct{ schema.Schema }

type tbCmGroup struct{ schema.Schema }

func (tbCmGroup) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", tbCmUser.Type).Ref("groups")}
}

func TestTablesComments(t *testing.T) {
	for _, d := range allDialects {
		s := tables(t, d, tbCmUser{}, tbCmPet{}, tbCmGroup{})
		on := func(c string) string {
			if d == velox.SQLite {
				return ""
			}
			return c
		}
		users := table(t, s, "tb_cm_users")
		if users.Comment != on("people") || column(t, users, "name").Comment != on("full name") || column(t, users, "id").Comment != "" {
			t.Errorf("%v: users %+v", d, users)
		}
		if c := column(t, table(t, s, "tb_cm_pets"), "tb_cm_user_pets"); c.Comment != on("owned pets") {
			t.Errorf("%v: implicit key comment %q", d, c.Comment)
		}
		jt := table(t, s, "tb_cm_user_groups")
		if jt.Comment != "" || jt.Columns[0].Comment != "" || jt.Columns[1].Comment != "" {
			t.Errorf("%v: join table %+v", d, jt)
		}
	}
}

func TestTablesMySQLCollation(t *testing.T) {
	for _, d := range allDialects {
		for _, tb := range tables(t, d, tbCmUser{}, tbCmPet{}, tbCmGroup{}).Tables {
			want := ""
			if d == velox.MySQL {
				want = "utf8mb4_bin"
			}
			if tb.Collation != want {
				t.Errorf("%v: %s collation %q, want %q", d, tb.Name, tb.Collation, want)
			}
		}
	}
}

func TestTablesPrimaryKey(t *testing.T) {
	for d, want := range map[velox.Dialect]string{velox.Postgres: "tb_cm_users_pkey", velox.MySQL: "PRIMARY", velox.SQLite: ""} {
		tb := table(t, tables(t, d, tbCmUser{}, tbCmPet{}, tbCmGroup{}), "tb_cm_users")
		if tb.PrimaryKeyName != want || !slices.Equal(tb.PrimaryKey, []string{"id"}) {
			t.Errorf("%v: %q %q", d, tb.PrimaryKey, tb.PrimaryKeyName)
		}
	}
}

// --- the graph is not changed, and the output is the same every time

func TestTablesDoesNotChangeGraph(t *testing.T) {
	for _, d := range allDialects {
		g := mustBuild(t, goldenSchemas(d)...)
		before, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Tables(g, d); err != nil {
			t.Fatal(err)
		}
		after, _ := json.Marshal(g)
		if !bytes.Equal(before, after) {
			t.Errorf("%v: Tables changed the graph", d)
		}
	}
}

func TestTablesDeterministic(t *testing.T) {
	for _, d := range allDialects {
		g := mustBuild(t, goldenSchemas(d)...)
		a, err := Tables(g, d)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := Tables(g, d)
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if !a.Equal(b) || !bytes.Equal(ja, jb) {
			t.Errorf("%v: two calls differ", d)
		}
		if strings.Contains(string(ja), `"dialect":"`+d.String()+`"`) == false {
			t.Errorf("%v: %s", d, ja)
		}
	}
}

// A partial unique index is not unique for the whole table: a Unique field
// keeps its own index beside one on its column.
type tbPartialUniq struct{ schema.Schema }

func (tbPartialUniq) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("accounts")}
}
func (tbPartialUniq) Fields() []schema.Field {
	return []schema.Field{field.String("email").Unique(), field.Bool("active").Nillable()}
}
func (tbPartialUniq) Indexes() []schema.Index {
	return []schema.Index{index.Fields("email").Where("active").Unique()}
}

func TestTablesPartialUniqueKeepsFieldIndex(t *testing.T) {
	partial := indexName("accounts", &Index{Columns: []string{"email"}, Unique: true, Where: "active"})
	for _, d := range allDialects {
		tb := table(t, tables(t, d, tbPartialUniq{}), "accounts")
		want := []migrate.Index{
			{Name: "accounts_email_unique", Columns: []string{"email"}, Unique: true},
			{Name: partial, Columns: []string{"email"}, Unique: true, Where: "active"},
		}
		slices.SortFunc(want, func(a, b migrate.Index) int { return strings.Compare(a.Name, b.Name) })
		if !slices.EqualFunc(tb.Indexes, want, func(x, y migrate.Index) bool {
			return x.Name == y.Name && x.Unique == y.Unique && x.Where == y.Where && slices.Equal(x.Columns, y.Columns)
		}) {
			t.Errorf("%v: indexes %+v, want %+v", d, tb.Indexes, want)
		}
	}
}

func TestTablesUnknownDialect(t *testing.T) {
	if s, err := Tables(mustBuild(t, tbCmUser{}, tbCmPet{}, tbCmGroup{}), velox.Dialect(0)); err == nil || s != nil {
		t.Errorf("Tables of Dialect(0) = %v, %v; want an error", s, err)
	}
}
