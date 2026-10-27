package gen

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

type TbOrderStatus string

func (TbOrderStatus) Values() []string { return []string{"open", "paid", "void"} }

type TbMoney struct{}

func (TbMoney) Value() (driver.Value, error) { return nil, nil }

// tbUser's partial index is left out of the MySQL set, which has none.
type tbUser struct {
	schema.Schema
	noPartial bool
}

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
func (u tbUser) Indexes() []schema.Index {
	ixs := []schema.Index{index.Fields("name").StorageKey("users_by_name")}
	if !u.noPartial {
		ixs = append(ixs, index.Fields("email").Where("active").Unique())
	}
	return ixs
}

type tbPost struct{ schema.Schema }

func (tbPost) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("posts")} }
func (tbPost) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("author_id").Optional().Nillable(),
		field.String("title").MaxLen(200),
		field.EnumOf[TbOrderStatus]("status").Default("open"),
		field.Bytes("body").Optional().Nillable(),
		field.Bytes("digest").MaxLen(32).Optional().Nillable(),
		field.Time("published_at").Optional().Nillable(),
		field.UUID("ref", UUIDValue{}).Optional().Nillable(),
		field.JSON("meta", JSONShape{}),
		field.Other("price", TbMoney{}).SchemaType(map[string]string{field.Postgres: "numeric(10,2)", field.MySQL: "decimal(10,2)", field.SQLite: "numeric"}),
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
	return []schema.Field{field.EnumOf[TbOrderStatus]("status").Default("open")}
}
func (tbGroup) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", tbUser.Type).Ref("groups")}
}

// tbInvoice is in database billing. local leaves out its key to tbUser,
// which MySQL cannot name from billing.
type tbInvoice struct {
	schema.Schema
	local bool
}

func (tbInvoice) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("billing", "invoices"), schema.Comment("a bill")}
}
func (tbInvoice) Fields() []schema.Field {
	return []schema.Field{field.EnumOf[TbOrderStatus]("status"), field.Enum("mood").Values("calm", "busy").SchemaType(map[string]string{field.Postgres: "mood"})}
}
func (i tbInvoice) Edges() []schema.Edge {
	if i.local {
		return nil
	}
	return []schema.Edge{edge.To("payer", tbUser.Type).Unique().Required()}
}

func goldenSchemas(d velox.Dialect) []schema.Interface {
	ss := []schema.Interface{tbUser{noPartial: d == velox.MySQL}, tbPost{}, tbToken{}, tbProfile{}, tbGroup{}}
	if d != velox.SQLite {
		ss = append(ss, tbInvoice{local: d == velox.MySQL})
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
		field.String("s16382").MaxLen(16382), field.String("s16383").MaxLen(16383),
		field.String("s16384").MaxLen(16384), field.String("s65535").MaxLen(65535),
		field.String("s65536").MaxLen(65536), field.String("s16777215").MaxLen(16777215), field.String("s16777216").MaxLen(16777216),
		field.String("s10485760").MaxLen(10485760), field.String("s10485761").MaxLen(10485761),
		field.Bytes("by"), field.Bytes("by255").MaxLen(255), field.Bytes("by256").MaxLen(256),
		field.Bytes("by65536").MaxLen(65536), field.Bytes("by16777216").MaxLen(16777216),
		field.Time("t"), field.UUID("uu", UUIDValue{}), field.JSON("j", JSONShape{}),
		field.Other("o", TbMoney{}).SchemaType(map[string]string{field.Postgres: "MONEY", field.MySQL: "INT(11)", field.SQLite: "NUMERIC"}),
		field.Int32("st").SchemaType(map[string]string{field.MySQL: "INT(11)", field.Postgres: "int4"}),
		field.String("q").SchemaType(map[string]string{field.MySQL: "ENUM('Draft', 'Published')", field.Postgres: `VARCHAR(20) COLLATE "en_US"`, field.SQLite: `TEXT COLLATE "NoCase"`}),
	}
}

// tbTypesWide holds the widest varchar, which fills a MySQL row with a
// bigint ID (16381 * 4 + 2 + 8 = 65534 bytes).
type tbTypesWide struct{ schema.Schema }

func (tbTypesWide) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("tb_types_wide")}
}
func (tbTypesWide) Fields() []schema.Field {
	return []schema.Field{field.String("s16381").MaxLen(16381)}
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
		"s16381":     {"character varying(16381)", "varchar(16381)", "text"},
		"s16382":     {"character varying(16382)", "text", "text"},
		"s16383":     {"character varying(16383)", "text", "text"},
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
		"q":          {`character varying(20) collate "en_US"`, "enum('Draft','Published')", `text collate "NoCase"`},
	}
	for i, d := range allDialects {
		s := tables(t, d, tbTypes{}, tbTypesWide{})
		tb, wide := table(t, s, "tb_types"), table(t, s, "tb_types_wide")
		for name, w := range want {
			if name == "s16381" {
				if c := column(t, wide, name); c.Type != w[i] {
					t.Errorf("%v: %s is %q, want %q", d, name, c.Type, w[i])
				}
				continue
			}
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

func (tbIdentUUID) Fields() []schema.Field { return []schema.Field{field.UUID("id", UUIDValue{})} }

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
		field.JSON("shape", JSONShape{}).Optional().Nillable(),
		field.JSON("req", JSONShape{}),
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
		field.UUID("u", UUIDValue{}).Default(func() UUIDValue { return UUIDValue{} }),
		field.JSON("j", JSONShape{}).Default(JSONShape{W: 1}),
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

type tbIxC struct {
	schema.Schema
	noWhere bool // MySQL has no partial indexes
}

func (tbIxC) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", tbIxOwner.Type).Ref("c").Unique()}
}
func (c tbIxC) Indexes() []schema.Index {
	if c.noWhere {
		return []schema.Index{index.Edges("owner")}
	}
	return []schema.Index{index.Edges("owner").Where("tb_ix_owner_c > 0")}
}

type tbIxTag struct{ schema.Schema }

func (tbIxTag) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owners", tbIxOwner.Type).Ref("tags")}
}

func TestTablesIndexes(t *testing.T) {
	for _, d := range allDialects {
		s := tables(t, d, tbIxOwner{}, tbIxA{}, tbIxB{}, tbIxC{noWhere: d == velox.MySQL}, tbIxTag{})
		owners := []string{"tb_ix_owners_code_key", "tb_ix_owners_slug_key", "tbixowner_slug"}
		ixas := "tb_ix_as_tb_ix_owner_a"
		ixcs := []string{"tb_ix_cs_tb_ix_owner_c", "tbixc_tb_ix_owner_c"}
		if d == velox.MySQL {
			owners = []string{"code", "slug", "tbixowner_slug"}
			ixas = "tb_ix_as_tb_ix_owners_a" // the foreign key's name
			ixcs = ixcs[1:]                  // the declared index, which the key needs no other beside
		}
		for tableName, want := range map[string][]string{
			"tb_ix_owners":     owners,
			"tb_ix_as":         {ixas},
			"tb_ix_bs":         {"tbixb_tb_ix_owner_b_name"},
			"tb_ix_cs":         ixcs,
			"tb_ix_owner_tags": {"tb_ix_owner_tags_tb_ix_tag_id"},
		} {
			tb := table(t, s, tableName)
			if got := tableIndexNames(tb); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
				t.Errorf("%v: %s indexes %q, want %q", d, tableName, got, want)
			}
		}
		// slug's Unique keeps its index beside the declared one, as ent's does.
		owner := table(t, s, "tb_ix_owners")
		if n := len(owner.Indexes); n != 3 {
			t.Fatalf("%v: owners %+v", d, owner.Indexes)
		}
		for _, ix := range owner.Indexes {
			if !ix.Unique || len(ix.Columns) != 1 {
				t.Errorf("%v: %+v", d, ix)
			}
		}
		if ix := table(t, s, "tb_ix_as").Indexes[0]; ix.Unique || !slices.Equal(ix.Columns, []string{"tb_ix_owner_a"}) {
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
			"tb_fk_bikes": {{Name: "bike_owner", Columns: []string{"tb_fk_user_bikes"}, RefTable: users, RefColumns: []string{"id"}, OnDelete: "SET NULL"}},
			"tb_fk_cars":  {{Name: "tb_fk_cars_tb_fk_users_cars", Columns: []string{"tb_fk_user_cars"}, RefTable: users, RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
			"tb_fk_user_groups": {
				{Name: "gm_group", Columns: []string{"tb_fk_group_id"}, RefTable: migrate.TableRef{Name: "tb_fk_groups"}, RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				{Name: "gm_user", Columns: []string{"tb_fk_user_id"}, RefTable: users, RefColumns: []string{"id"}, OnDelete: "CASCADE"},
			},
			"tb_fk_user_teams": {
				{Name: "tb_fk_user_teams_tb_fk_team_id", Columns: []string{"tb_fk_team_id"}, RefTable: migrate.TableRef{Name: "tb_fk_teams"}, RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				{Name: "tb_fk_user_teams_tb_fk_user_id", Columns: []string{"tb_fk_user_id"}, RefTable: users, RefColumns: []string{"id"}, OnDelete: "CASCADE"},
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
	partial := indexName("tbPartialUniq", []string{"email"})
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} { // MySQL has no partial indexes
		tb := table(t, tables(t, d, tbPartialUniq{}), "accounts")
		want := []migrate.Index{
			{Name: "accounts_email_key", Columns: []string{"email"}, Unique: true},
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

// --- refusals at plan time

// refusalLines holds the line of each element a refusal case names.
var refusalLines = map[string]int{}

type tbRfPartial struct{ schema.Schema }

func (tbRfPartial) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (tbRfPartial) Indexes() []schema.Index {
	ix, l := index.Fields("name").Where("name <> ''"), here()
	refusalLines["partial"] = l
	return []schema.Index{ix}
}

type tbRfTextIndex struct{ schema.Schema }

func (tbRfTextIndex) Fields() []schema.Field { return []schema.Field{field.Text("bio")} }
func (tbRfTextIndex) Indexes() []schema.Index {
	ix, l := index.Fields("bio"), here()
	refusalLines["text index"] = l
	return []schema.Index{ix}
}

type tbRfJSONUnique struct{ schema.Schema }

func (tbRfJSONUnique) Fields() []schema.Field {
	f, l := field.String("doc").SchemaType(map[string]string{field.MySQL: "json"}).Unique(), here()
	refusalLines["json unique"] = l
	return []schema.Field{f}
}

type tbRfLongValue struct{ schema.Schema }

func (tbRfLongValue) Fields() []schema.Field {
	f, l := field.Enum("e").NamedValues("Long", strings.Repeat("a", 256)), here()
	refusalLines["long value"] = l
	return []schema.Field{f}
}

type tbRfPGLongValue struct{ schema.Schema }

func (tbRfPGLongValue) Fields() []schema.Field {
	f, l := field.Enum("e").Values(strings.Repeat("b", 64)), here()
	refusalLines["pg long value"] = l
	return []schema.Field{f, field.Enum("ok").Values(strings.Repeat("é", 31) + "b")} // 63 bytes
}

type tbRfEnumType struct{ schema.Schema }

func (tbRfEnumType) Fields() []schema.Field {
	f, l := field.Enum("e").Values("a").SchemaType(map[string]string{field.MySQL: "int"}), here()
	refusalLines["mysql enum type"] = l
	return []schema.Field{f}
}

type tbRfColumnComment struct{ schema.Schema }

func (tbRfColumnComment) Fields() []schema.Field {
	f, l := field.String("s").Comment(strings.Repeat("c", 1025)), here()
	refusalLines["column comment"] = l
	return []schema.Field{f}
}

type tbRfTableComment struct{ schema.Schema }

func (tbRfTableComment) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Comment(strings.Repeat("c", 2049))}
}
func (tbRfTableComment) Fields() []schema.Field {
	f, l := field.String("s"), here()
	refusalLines["table comment"] = l
	return []schema.Field{f}
}

type tbRfPGEnumType struct{ schema.Schema }

func (tbRfPGEnumType) Fields() []schema.Field {
	f, l := field.Enum("e").Values("a").SchemaType(map[string]string{field.Postgres: "integer"}), here()
	refusalLines["pg enum type"] = l
	return []schema.Field{f}
}

type tbRfPGEnumBadName struct{ schema.Schema }

func (tbRfPGEnumBadName) Fields() []schema.Field {
	f, l := field.Enum("e").Values("a").SchemaType(map[string]string{field.Postgres: "My Mood"}), here()
	refusalLines["pg enum name"] = l
	return []schema.Field{f}
}

type tbRfNULDefault struct{ schema.Schema }

func (tbRfNULDefault) Fields() []schema.Field {
	f, l := field.String("s").Default("a\x00b"), here()
	refusalLines["nul default"] = l
	return []schema.Field{f}
}

type tbRfNULValue struct{ schema.Schema }

func (tbRfNULValue) Fields() []schema.Field {
	f, l := field.Enum("e").NamedValues("A", "a\x00"), here()
	refusalLines["nul value"] = l
	return []schema.Field{f}
}

type tbRfTableIn struct{ schema.Schema }

func (tbRfTableIn) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("billing", "rf_bills")}
}
func (tbRfTableIn) Fields() []schema.Field {
	f, l := field.String("s"), here()
	refusalLines["table in"] = l
	return []schema.Field{f}
}

// tbRfCrossDB is in database billing and references a table in the
// connection's default database.
type tbRfCrossDB struct{ schema.Schema }

func (tbRfCrossDB) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("billing", "rf_cross")}
}
func (tbRfCrossDB) Edges() []schema.Edge {
	e, l := edge.To("payer", tbRfCrossRef.Type).Unique().Required(), here()
	refusalLines["cross database"] = l
	return []schema.Edge{e}
}

type tbRfCrossRef struct{ schema.Schema }

func (tbRfCrossRef) Fields() []schema.Field { return []schema.Field{field.String("name")} }

type tbRfOther struct{ schema.Schema }

func (tbRfOther) Fields() []schema.Field {
	f, l := field.Other("o", TbMoney{}).SchemaType(map[string]string{field.Postgres: "money", field.SQLite: "numeric"}), here()
	refusalLines["other"] = l
	return []schema.Field{f}
}

type tbRfIDType struct{ schema.Schema }

func (tbRfIDType) Fields() []schema.Field {
	f, l := field.Int("id").SchemaType(map[string]string{field.MySQL: "varchar(20)"}), here()
	refusalLines["id type"] = l
	return []schema.Field{f}
}

type tbRfIDTypeSQLite struct{ schema.Schema }

func (tbRfIDTypeSQLite) Fields() []schema.Field {
	f, l := field.Int64("id").SchemaType(map[string]string{field.SQLite: "text"}), here()
	refusalLines["sqlite id type"] = l
	return []schema.Field{f}
}

// refusal is the one *Error of err.
func refusal(t *testing.T, err error) *Error {
	t.Helper()
	if err == nil {
		t.Fatal("no error")
	}
	errs := []error{err}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs = j.Unwrap()
	}
	if len(errs) != 1 {
		t.Fatalf("%d errors, want 1:\n%v", len(errs), err)
	}
	e, ok := errs[0].(*Error)
	if !ok {
		t.Fatalf("%T %v, want an *Error", errs[0], errs[0])
	}
	return e
}

func TestTablesRefusals(t *testing.T) {
	for _, c := range []struct {
		line   string
		d      velox.Dialect
		s      schema.Interface
		where  string
		reason string
		also   velox.Dialect    // another dialect that refuses the schema, in its own case
		with   schema.Interface // a schema it needs, or nil
	}{
		{"partial", velox.MySQL, tbRfPartial{}, "index 0", "MySQL has no partial indexes (Where); drop Where or use a separate MySQL schema", 0, nil},
		{"text index", velox.MySQL, tbRfTextIndex{}, "index 0", "column bio is longtext on MySQL, which an index covers only by a prefix; give the field a MaxLen or a SchemaType", 0, nil},
		{"json unique", velox.MySQL, tbRfJSONUnique{}, `field "doc"`, "column doc is json on MySQL, which an index covers only by a prefix; give the field a MaxLen or a SchemaType", 0, nil},
		{"long value", velox.MySQL, tbRfLongValue{}, `field "e"`, "enum value Long is 256 characters on MySQL, over its 255", velox.Postgres, nil},
		{"long value", velox.Postgres, tbRfLongValue{}, `field "e"`, "enum value Long is 256 bytes, over PostgreSQL's 63", velox.MySQL, nil},
		{"pg long value", velox.Postgres, tbRfPGLongValue{}, `field "e"`, `enum value "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb…" is 64 bytes, over PostgreSQL's 63`, 0, nil},
		{"mysql enum type", velox.MySQL, tbRfEnumType{}, `field "e"`, "its MySQL SchemaType int is not a string type; drop it to get an enum(…) column, or give a string type to get a CHECK constraint", 0, nil},
		{"column comment", velox.MySQL, tbRfColumnComment{}, `field "s"`, "its comment is 1025 characters on MySQL, over its 1024", 0, nil},
		{"table comment", velox.MySQL, tbRfTableComment{}, "", "the table comment is 2049 characters on MySQL, over its 2048", 0, nil},
		{"pg enum type", velox.Postgres, tbRfPGEnumType{}, `field "e"`, "integer is a built-in PostgreSQL type, not an enum type name", 0, nil},
		{"pg enum name", velox.Postgres, tbRfPGEnumBadName{}, `field "e"`, "My Mood is not a lower-case identifier", 0, nil},
		{"nul default", velox.Postgres, tbRfNULDefault{}, `field "s"`, "PostgreSQL text cannot hold a NUL byte", 0, nil},
		{"nul value", velox.Postgres, tbRfNULValue{}, `field "e"`, "PostgreSQL text cannot hold a NUL byte", 0, nil},
		{"table in", velox.SQLite, tbRfTableIn{}, "", "schema.TableIn puts the table in database schema billing, which SQLite does not have", 0, nil},
		{"other", velox.MySQL, tbRfOther{}, `field "o"`, "an Other field needs a SchemaType for MySQL", 0, nil},
		{"id type", velox.MySQL, tbRfIDType{}, `field "id"`, "the ID is an integer, but its SchemaType varchar(20) is not an integer type on MySQL; Tables makes the column an identity itself", 0, nil},
		{"sqlite id type", velox.SQLite, tbRfIDTypeSQLite{}, `field "id"`, "the ID is an integer, but its SchemaType text is not an integer type on SQLite; Tables makes the column an identity itself", 0, nil},
		{"cross database", velox.MySQL, tbRfCrossDB{}, `edge "payer"`, "table billing.rf_cross references tb_rf_cross_refs in the connection's default database, which MySQL cannot name from another database; give tb_rf_cross_refs a schema.TableIn", velox.SQLite, tbRfCrossRef{}},
	} {
		t.Run(c.line, func(t *testing.T) {
			ss := []schema.Interface{c.s}
			if c.with != nil {
				ss = append(ss, c.with)
			}
			g := mustBuild(t, ss...)
			_, err := Tables(g, c.d)
			e := refusal(t, err)
			if !strings.Contains(e.Reason, c.reason) || e.Where != c.where || e.Schema != g.Entities[0].Name {
				t.Errorf("%v: %+v\nwant where %q, reason containing %q", c.d, e, c.where, c.reason)
			}
			if line := refusalLines[c.line]; line == 0 || !strings.HasSuffix(e.Pos, "tables_test.go:"+strconv.Itoa(line)) {
				t.Errorf("%v: pos %q, want line %d", c.d, e.Pos, line)
			}
			for _, d := range allDialects {
				if d == c.d || d == c.also {
					continue
				}
				if _, err := Tables(g, d); err != nil {
					t.Errorf("%v: %v", d, err)
				}
			}
		})
	}
}

// Errors are ordered by schema name, then by the order of the elements.
type tbRfOrderA struct{ schema.Schema }

func (tbRfOrderA) Fields() []schema.Field {
	return []schema.Field{field.Text("a").Unique(), field.String("b").Comment(strings.Repeat("c", 1025))}
}
func (tbRfOrderA) Edges() []schema.Edge {
	return []schema.Edge{edge.To("bs", tbRfOrderB.Type).Comment(strings.Repeat("c", 1025))}
}
func (tbRfOrderA) Indexes() []schema.Index {
	return []schema.Index{index.Fields("b").Where("b <> ''")}
}

type tbRfOrderB struct{ schema.Schema }

func (tbRfOrderB) Fields() []schema.Field { return []schema.Field{field.Text("a").Unique()} }

func TestTablesRefusalsOrder(t *testing.T) {
	_, err := Tables(mustBuild(t, tbRfOrderB{}, tbRfOrderA{}), velox.MySQL)
	if err == nil {
		t.Fatal("no error")
	}
	got := plain(err)
	want := strings.Join([]string{
		`schema tbRfOrderA: field "a": column a is longtext on MySQL, which an index covers only by a prefix; give the field a MaxLen or a SchemaType`,
		`schema tbRfOrderA: field "b": its comment is 1025 characters on MySQL, over its 1024`,
		`schema tbRfOrderA: edge "bs": its comment is 1025 characters on MySQL, over its 1024`,
		`schema tbRfOrderA: index 0: MySQL has no partial indexes (Where); drop Where or use a separate MySQL schema`,
		`schema tbRfOrderB: field "a": column a is longtext on MySQL, which an index covers only by a prefix; give the field a MaxLen or a SchemaType`,
	}, "\n")
	if got != want {
		t.Errorf("errors\n%s\nwant\n%s", got, want)
	}
	_, again := Tables(mustBuild(t, tbRfOrderB{}, tbRfOrderA{}), velox.MySQL)
	if again.Error() != err.Error() {
		t.Errorf("two calls differ:\n%v\n%v", err, again)
	}
}

// The ID (8), n (4) and the NULL bitmap (1), a (8165*4+2), b (8164*4+2) and
// c (201+1) are 65535 bytes.
type tbRfRow struct{ schema.Schema }

func (tbRfRow) Fields() []schema.Field {
	a, la := field.String("a").MaxLen(8165), here()
	b, lb := field.String("b").MaxLen(8164), here()
	c, lc := field.Bytes("c").MaxLen(201), here()
	refusalLines["row a"], refusalLines["row b"], refusalLines["row c"] = la, lb, lc
	return []schema.Field{field.Int32("n").Optional().Nillable(), a, b, c}
}

// tbRfRow with c one byte longer: 65536 bytes.
type tbRfRowOne struct{ schema.Schema }

func (tbRfRowOne) Fields() []schema.Field {
	a, la := field.String("a").MaxLen(8165), here()
	b, lb := field.String("b").MaxLen(8164), here()
	c, lc := field.Bytes("c").MaxLen(202), here()
	refusalLines["row1 a"], refusalLines["row1 b"], refusalLines["row1 c"] = la, lb, lc
	return []schema.Field{field.Int32("n").Optional().Nillable(), a, b, c}
}

// 16320*4+2 + 63*4+1 is 65535 bytes of varchar, and the ID is 8 more.
type tbRfRowFull struct{ schema.Schema }

func (tbRfRowFull) Fields() []schema.Field {
	return []schema.Field{field.String("a").MaxLen(16320), field.String("b").MaxLen(63)}
}

func TestTablesRowLimit(t *testing.T) {
	g := mustBuild(t, tbRfRow{})
	if _, err := Tables(g, velox.MySQL); err != nil {
		t.Errorf("a row of 65535 bytes: %v", err)
	}
	g = mustBuild(t, tbRfRowOne{})
	e := refusal(t, func() error { _, err := Tables(g, velox.MySQL); return err }())
	file := e.Pos
	if !strings.HasSuffix(file, "tables_test.go") || e.Schema != "tbRfRowOne" || e.Where != "" {
		t.Errorf("error %+v, want the schema's file as its Pos", e)
	}
	at := func(k string) string { return "  " + file + ":" + strconv.Itoa(refusalLines[k]) }
	want := "MySQL rows hold at most 65535 bytes; these need 65536:\n" +
		at("row1 a") + `: field "a": varchar(8165), 32662 bytes` + "\n" +
		at("row1 b") + `: field "b": varchar(8164), 32658 bytes` + "\n" +
		at("row1 c") + `: field "c": varbinary(202), 203 bytes` + "\n" +
		"  the other columns and the NULL bitmap: 13 bytes\n" +
		"  use Text() or a smaller MaxLen"
	if e.Reason != want {
		t.Errorf("reason\n%s\nwant\n%s", e.Reason, want)
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		if _, err := Tables(g, d); err != nil {
			t.Errorf("%v: %v", d, err)
		}
	}
	e = refusal(t, func() error { _, err := Tables(mustBuild(t, tbRfRowFull{}), velox.MySQL); return err }())
	if !strings.Contains(e.Reason, "these need 65543:") || !strings.Contains(e.Reason, "the other columns and the NULL bitmap: 8 bytes") {
		t.Errorf("65535 bytes of varchar and an ID: %s", e.Reason)
	}
}

// Text and JSON count their in-row pointers, 12 bytes each: the ID (8), k
// (1), t, j and a (16375*4+2) are 65535 bytes.
type tbRfRowPtr struct{ schema.Schema }

func (tbRfRowPtr) Fields() []schema.Field {
	return []schema.Field{field.Int8("k"), field.Text("t"), field.JSON("j", JSONShape{}), field.String("a").MaxLen(16375)}
}

type tbRfRowPtrOver struct{ schema.Schema }

func (tbRfRowPtrOver) Fields() []schema.Field {
	return []schema.Field{field.Int8("k"), field.Text("t"), field.JSON("j", JSONShape{}), field.String("a").MaxLen(16376)}
}

// A join table counts every column: a varchar(16350) ID (65402) and a
// char(36) one (144).
type tbRfRowJoinUser struct{ schema.Schema }

func (tbRfRowJoinUser) Fields() []schema.Field {
	return []schema.Field{field.String("id").MaxLen(16350)}
}
func (tbRfRowJoinUser) Edges() []schema.Edge {
	return []schema.Edge{edge.To("groups", tbRfRowJoinGroup.Type)}
}

type tbRfRowJoinGroup struct{ schema.Schema }

func (tbRfRowJoinGroup) Fields() []schema.Field {
	return []schema.Field{field.UUID("id", UUIDValue{})}
}
func (tbRfRowJoinGroup) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", tbRfRowJoinUser.Type).Ref("groups")}
}

func TestTablesRowSize(t *testing.T) {
	if _, err := Tables(mustBuild(t, tbRfRowPtr{}), velox.MySQL); err != nil {
		t.Errorf("a row of 65535 bytes: %v", err)
	}
	e := refusal(t, func() error { _, err := Tables(mustBuild(t, tbRfRowPtrOver{}), velox.MySQL); return err }())
	if !strings.Contains(e.Reason, "these need 65539:") || !strings.Contains(e.Reason, "the other columns and the NULL bitmap: 33 bytes") {
		t.Errorf("text and json: %s", e.Reason)
	}
	_, err := Tables(mustBuild(t, tbRfRowJoinUser{}, tbRfRowJoinGroup{}), velox.MySQL)
	var row *Error
	for _, e := range refusals(t, err) {
		if strings.HasPrefix(e.Reason, "MySQL rows hold") {
			row = e
		}
	}
	if row == nil || row.Where != `edge "groups"` || !strings.Contains(row.Reason, "these need 65546:") ||
		!strings.Contains(row.Reason, `: column "tb_rf_row_join_user_id": varchar(16350), 65402 bytes`) ||
		!strings.Contains(row.Reason, "the other columns and the NULL bitmap: 144 bytes") {
		t.Errorf("join row: %v", err)
	}
}

func TestMySQLRowBytes(t *testing.T) {
	list := func(kind string, n int) string {
		vs := make([]string, n)
		for i := range vs {
			vs[i] = "'v" + strconv.Itoa(i) + "'"
		}
		return kind + "(" + strings.Join(vs, ",") + ")"
	}
	enum := func(n int) string { return list("enum", n) }
	set := func(n int) string { return list("set", n) }
	for _, c := range []struct {
		typ string
		n   int
	}{
		{"tinyint", 1}, {"tinyint unsigned", 1}, {"tinyint(1)", 1}, {"smallint", 2}, {"smallint unsigned", 2},
		{"mediumint", 3}, {"mediumint unsigned", 3}, {"int", 4}, {"int unsigned", 4}, {"bigint", 8}, {"bigint unsigned", 8},
		{"float", 4}, {"double", 8},
		{"decimal(20,0)", 9}, {"decimal(10,2)", 5}, {"decimal(9,0)", 4}, {"decimal(18,9)", 8}, {"decimal(65,30)", 30}, {"decimal(1,1)", 1},
		{"decimal", 5}, {"decimal(12)", 6}, {"numeric", 5}, {"numeric(20,0)", 9}, {"numeric(10,2)", 5}, {"dec(10,2)", 5}, {"fixed(18,9)", 8},
		{"decimal(10,2) unsigned", 5}, {"numeric(65,30) unsigned zerofill", 30}, {"decimal unsigned", 5}, {"char", 4}, {"binary", 1}, {"bit", 1},
		{"date", 3}, {"datetime", 5}, {"datetime(1)", 6}, {"datetime(3)", 7}, {"datetime(6)", 8},
		{"timestamp", 4}, {"timestamp(6)", 7}, {"time", 3}, {"time(2)", 4}, {"time(6)", 6}, {"year", 1},
		{"char(36)", 144}, {"char(1)", 4}, {"binary(16)", 16},
		{"varchar(63)", 253}, {"varchar(64)", 258}, {"varbinary(255)", 256}, {"varbinary(256)", 258},
		{"enum('a','b')", 1}, {enum(255), 1}, {enum(256), 2},
		{"bit(1)", 1}, {"bit(9)", 2}, {"bit(64)", 8},
		{"tinytext", 9}, {"tinyblob", 9}, {"text", 10}, {"blob", 10}, {"mediumtext", 11}, {"mediumblob", 11},
		{"longtext", 12}, {"longblob", 12}, {"json", 12},
		{"set('a')", 1}, {set(8), 1}, {set(9), 2}, {set(16), 2}, {set(17), 3}, {set(25), 4}, {set(32), 4}, {set(33), 8}, {set(64), 8},
		{"float(24)", 4}, {"float(25)", 8}, {"float(53)", 8},
		{"geometry", 12}, {"point", 12}, {"linestring", 12}, {"polygon", 12}, {"multipoint", 12},
		{"multilinestring", 12}, {"multipolygon", 12}, {"geometrycollection", 12},
	} {
		if n, ok := mysqlRowBytes(c.typ); n != c.n || !ok {
			t.Errorf("%.40s: %d, %v, want %d", c.typ, n, ok, c.n)
		}
	}
	for _, typ := range []string{"varchar", "float(54)", "set(", "set()", set(65), "numeric(66,0)", "numeric(2,3)", "decimal(5,2) foo", "geometry(3)", "decimal(x,1)", "decimal(2,3)", "decimal(66,0)", "bit(65)", "datetime(7)", "enum(", ""} {
		if n, ok := mysqlRowBytes(typ); n != 0 || ok {
			t.Errorf("%s: %d, %v, want 0, false", typ, n, ok)
		}
	}
}

// A decimal column rounds a Default that has more places than it keeps, and
// refuses one with more digits before the point.
type tbRoundedDefault struct{ schema.Schema }

var tbRoundedValue float64
var tbRoundedLine int

func (tbRoundedDefault) Fields() []schema.Field {
	f, l := field.Float("price").Nillable().Default(tbRoundedValue).SchemaType(map[string]string{field.Postgres: "numeric(5,2)", field.MySQL: "decimal(5,2)"}), here()
	tbRoundedLine = l
	return []schema.Field{f}
}

func TestTablesRoundedDefault(t *testing.T) {
	for _, c := range []struct {
		v    float64
		json string
		bad  bool
	}{
		{1.555, "1.555", true}, {1234.5, "1234.5", true}, {1000, "1000", true}, {-1234.5, "-1234.5", true}, {0.001, "0.001", true},
		{1.55, "1.55", false}, {-999.99, "-999.99", false}, {0, "0", false}, {999.99, "999.99", false}, {5, "5", false},
	} {
		tbRoundedValue = c.v
		g := mustBuild(t, tbRoundedDefault{})
		for _, d := range allDialects {
			_, err := Tables(g, d)
			if !c.bad || d == velox.SQLite {
				if err != nil {
					t.Errorf("%v: %s: %v", d, c.json, err)
				}
				continue
			}
			e := refusal(t, err)
			typ := map[velox.Dialect]string{velox.Postgres: "numeric(5,2)", velox.MySQL: "decimal(5,2)"}[d]
			want := "its Default " + c.json + " does not fit " + typ + ", which keeps 3 digits before the point and 2 after it; the database would round it or refuse it"
			if e.Reason != want || e.Where != `field "price"` || !strings.HasSuffix(e.Pos, "tables_test.go:"+strconv.Itoa(tbRoundedLine)) {
				t.Errorf("%v: %s: %+v\nwant reason %q", d, c.json, e, want)
			}
		}
	}
}

// The decimal types a Default is checked against: MySQL's bare decimal and
// numeric are decimal(10,0), its dec, fixed and numeric are decimal's
// synonyms, and unsigned or zerofill may follow; PostgreSQL's bare numeric
// keeps every digit.
type tbDecimalTypes struct{ schema.Schema }

var tbDecimalPG, tbDecimalMy, tbDecimalSD string
var tbDecimalValue float64

func (tbDecimalTypes) Fields() []schema.Field {
	f := field.Float("price").Nillable().Default(tbDecimalValue).SchemaType(map[string]string{field.Postgres: tbDecimalPG, field.MySQL: tbDecimalMy})
	if tbDecimalSD != "" {
		f = f.SchemaDefault(map[string]string{field.Postgres: tbDecimalSD, field.MySQL: tbDecimalSD})
	}
	return []schema.Field{f}
}

func TestTablesDecimalTypes(t *testing.T) {
	const ( // which dialects refuse the row
		none = 0
		pg   = 1
		my   = 2
		both = pg | my
	)
	for _, c := range []struct {
		pg, my string
		sd     string // a SchemaDefault for both
		v      float64
		bad    int
		neg    bool // the reason is a negative Default on an unsigned column
	}{
		{"numeric", "decimal", "", 1.5, my, false},
		{"numeric", "decimal", "", 12345678901, my, false},
		{"numeric", "decimal", "", 123, none, false},
		{"numeric", "numeric", "", 1.5, my, false},
		{"numeric", "numeric", "", 12345678901, my, false},
		{"numeric", "numeric", "", 123, none, false},
		{"numeric(5,2)", "dec(5,2)", "", 1.555, both, false},
		{"numeric(5,2)", "fixed(5,2)", "", 1.555, both, false},
		{"numeric(5,2)", "numeric(5,2)", "", 1.555, both, false},
		{"numeric(5,2)", "numeric(5,2)", "", 1.55, none, false},
		{"numeric(5)", "decimal(5)", "", 1.5, both, false},
		{"numeric(5)", "decimal(5)", "", 99999, none, false},
		{"numeric(5)", "decimal(5)", "", 100000, both, false},
		{"numeric(5,2)", "decimal(5,2) unsigned", "", 1.55, none, false},
		{"numeric(5,2)", "decimal(5,2) unsigned", "", -1.55, my, true},
		{"numeric(5,2)", "decimal(5,2) unsigned", "", 1234.5, both, false},
		{"numeric(5,2)", "decimal(5,2) unsigned zerofill", "", -1, my, true},
		{"numeric(5,2)", "decimal(5,2) signed", "", -1.55, none, false},
		{"numeric(5,2)", "decimal(5,2) zerofill", "", 1.555, both, false},
		{"numeric", "decimal unsigned", "", 1.5, my, false},
		{"numeric(999999999,0)", "decimal(999999999,0)", "", 5, none, false},
		{"numeric(999999999,0)", "numeric(999999999,0)", "", 1.5, none, false},
		{"numeric(5,2)", "decimal(5,2)", "1.555", 1.555, none, false}, // the SchemaDefault is the column's
	} {
		tbDecimalPG, tbDecimalMy, tbDecimalSD, tbDecimalValue = c.pg, c.my, c.sd, c.v
		g := mustBuild(t, tbDecimalTypes{})
		for _, d := range allDialects {
			bit := map[velox.Dialect]int{velox.Postgres: pg, velox.MySQL: my}[d]
			_, err := Tables(g, d)
			if c.bad&bit == 0 {
				if err != nil {
					t.Errorf("%v: %s / %s: %v: %v", d, c.pg, c.my, c.v, err)
				}
				continue
			}
			typ := c.my
			if d == velox.Postgres {
				typ = c.pg
			}
			want := "its Default " + strconv.FormatFloat(c.v, 'f', -1, 64) + " does not fit " + typ + ","
			if c.neg {
				want = "its Default " + strconv.FormatFloat(c.v, 'f', -1, 64) + " is negative, but its column " + typ + " is unsigned"
			}
			if e := refusal(t, err); !strings.Contains(e.Reason, want) {
				t.Errorf("%v: %s: %q, want it to contain %q", d, typ, e.Reason, want)
			}
		}
	}
}

type tbRfKey769 struct{ schema.Schema }

func (tbRfKey769) Fields() []schema.Field { return []schema.Field{field.String("k").MaxLen(769)} }
func (tbRfKey769) Indexes() []schema.Index {
	ix, l := index.Fields("k"), here()
	refusalLines["key 769"] = l
	return []schema.Index{ix}
}

type tbRfKey768 struct{ schema.Schema }

func (tbRfKey768) Fields() []schema.Field  { return []schema.Field{field.String("k").MaxLen(768)} }
func (tbRfKey768) Indexes() []schema.Index { return []schema.Index{index.Fields("k")} }

// Every counted type: 4*700 + 1+2+4+8+4+8+8+2 + 36*4 is 2981 bytes; the
// varbinary(100) makes it 3081.
type tbRfKeyMixed struct{ schema.Schema }

func (tbRfKeyMixed) Fields() []schema.Field {
	return []schema.Field{
		field.String("s").MaxLen(700), field.Int8("i8"), field.Int16("i16"), field.Int32("i32"), field.Int64("i64"),
		field.Float32("f32"), field.Float("f64"), field.Time("t"), field.Enum("e").Values("a"), field.UUID("u", UUIDValue{}),
		field.Bytes("b").MaxLen(100), field.Other("o", TbMoney{}).SchemaType(map[string]string{field.MySQL: "decimal(10,2)", field.Postgres: "numeric", field.SQLite: "numeric"}),
	}
}
func (tbRfKeyMixed) Indexes() []schema.Index {
	return []schema.Index{index.Fields("s", "i8", "i16", "i32", "i64", "f32", "f64", "t", "e", "u", "o"), index.Fields("s", "i8", "i16", "i32", "i64", "f32", "f64", "t", "e", "u", "o", "b")}
}

func TestTablesKeyLimit(t *testing.T) {
	g := mustBuild(t, tbRfKey769{})
	e := refusal(t, func() error { _, err := Tables(g, velox.MySQL); return err }())
	if e.Reason != "the key is 3076 bytes on MySQL, over its 3072" || e.Where != "index 0" ||
		!strings.HasSuffix(e.Pos, "tables_test.go:"+strconv.Itoa(refusalLines["key 769"])) {
		t.Errorf("error %+v", e)
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		if _, err := Tables(g, d); err != nil {
			t.Errorf("%v: %v", d, err)
		}
	}
	if _, err := Tables(mustBuild(t, tbRfKey768{}), velox.MySQL); err != nil {
		t.Errorf("a key of 3072 bytes: %v", err)
	}
	e = refusal(t, func() error { _, err := Tables(mustBuild(t, tbRfKeyMixed{}), velox.MySQL); return err }())
	if e.Reason != "the key is 3081 bytes on MySQL, over its 3072" || e.Where != "index 1" {
		t.Errorf("mixed key: %+v", e)
	}
}

// An implicit foreign key's comment is its edge's, so a long one is the
// edge's schema's error, at the edge.
type tbRfFKCommentOwner struct{ schema.Schema }

func (tbRfFKCommentOwner) Edges() []schema.Edge {
	ed, l := edge.To("pets", tbRfFKCommentPet.Type).Comment(strings.Repeat("c", 1025)), here()
	refusalLines["fk comment"] = l
	return []schema.Edge{ed}
}

type tbRfFKCommentPet struct{ schema.Schema }

func TestTablesRefusalsImplicitComment(t *testing.T) {
	e := refusal(t, func() error {
		_, err := Tables(mustBuild(t, tbRfFKCommentOwner{}, tbRfFKCommentPet{}), velox.MySQL)
		return err
	}())
	if e.Schema != "tbRfFKCommentOwner" || e.Where != `edge "pets"` || e.Reason != "its comment is 1025 characters on MySQL, over its 1024" ||
		!strings.HasSuffix(e.Pos, "tables_test.go:"+strconv.Itoa(refusalLines["fk comment"])) {
		t.Errorf("error %+v", e)
	}
}

// refusals is every *Error of err, a join.
func refusals(t *testing.T, err error) []*Error {
	t.Helper()
	if err == nil {
		t.Fatal("no error")
	}
	var out []*Error
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		out = append(out, e.(*Error))
	}
	return out
}

// Primary keys and foreign-key indexes are keys too; a key refusal on a
// foreign-key index is its edge's.
type tbRfKeyRef struct{ schema.Schema }

func (tbRfKeyRef) Fields() []schema.Field {
	f, l := field.String("id").MaxLen(1000), here()
	refusalLines["key ref id"] = l
	return []schema.Field{f}
}
func (tbRfKeyRef) Edges() []schema.Edge {
	ed, l := edge.To("kids", tbRfKeyKid.Type), here()
	refusalLines["key ref kids"] = l
	return []schema.Edge{ed}
}

type tbRfKeyKid struct{ schema.Schema }

type tbRfTextRef struct{ schema.Schema }

func (tbRfTextRef) Fields() []schema.Field {
	f, l := field.Text("id"), here()
	refusalLines["text ref id"] = l
	return []schema.Field{f}
}
func (tbRfTextRef) Edges() []schema.Edge {
	ed, l := edge.To("kids", tbRfTextKid.Type), here()
	refusalLines["text ref kids"] = l
	return []schema.Edge{ed}
}

type tbRfTextKid struct{ schema.Schema }

type tbRfUniqueWide struct{ schema.Schema }

func (tbRfUniqueWide) Fields() []schema.Field {
	f, l := field.String("u").MaxLen(800).Unique(), here()
	refusalLines["unique wide"] = l
	return []schema.Field{f}
}

func TestTablesKeyRefusals(t *testing.T) {
	type want struct{ schema, where, line, reason string }
	for _, c := range []struct {
		ss   []schema.Interface
		want []want
	}{
		{[]schema.Interface{tbRfKeyRef{}, tbRfKeyKid{}}, []want{
			{"tbRfKeyRef", `field "id"`, "key ref id", "the key is 4000 bytes on MySQL, over its 3072"},
			{"tbRfKeyRef", `edge "kids"`, "key ref kids", "the key is 4000 bytes on MySQL, over its 3072"},
		}},
		{[]schema.Interface{tbRfTextRef{}, tbRfTextKid{}}, []want{
			{"tbRfTextRef", `field "id"`, "text ref id", "column id is longtext on MySQL, which an index covers only by a prefix; give the field a MaxLen or a SchemaType"},
			{"tbRfTextRef", `edge "kids"`, "text ref kids", "column tb_rf_text_ref_kids is longtext on MySQL, which an index covers only by a prefix; give the field a MaxLen or a SchemaType"},
		}},
		{[]schema.Interface{tbRfUniqueWide{}}, []want{
			{"tbRfUniqueWide", `field "u"`, "unique wide", "the key is 3200 bytes on MySQL, over its 3072"},
		}},
	} {
		g := mustBuild(t, c.ss...)
		_, err := Tables(g, velox.MySQL)
		got := refusals(t, err)
		if len(got) != len(c.want) {
			t.Fatalf("%d errors, want %d:\n%v", len(got), len(c.want), err)
		}
		for i, w := range c.want {
			e := got[i]
			if e.Schema != w.schema || e.Where != w.where || e.Reason != w.reason ||
				!strings.HasSuffix(e.Pos, "tables_test.go:"+strconv.Itoa(refusalLines[w.line])) {
				t.Errorf("error %d: %+v, want %+v at line %d", i, e, w, refusalLines[w.line])
			}
		}
		for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
			if _, err := Tables(g, d); err != nil {
				t.Errorf("%v: %v", d, err)
			}
		}
	}
}

// A join table's refusals are its assoc edge's: its key, and its row.
type tbRfJoinUser struct{ schema.Schema }

func (tbRfJoinUser) Fields() []schema.Field { return []schema.Field{field.String("id").MaxLen(700)} }
func (tbRfJoinUser) Edges() []schema.Edge {
	ed, l := edge.To("groups", tbRfJoinGroup.Type), here()
	refusalLines["join groups"] = l
	return []schema.Edge{ed}
}

type tbRfJoinGroup struct{ schema.Schema }

func (tbRfJoinGroup) Fields() []schema.Field { return []schema.Field{field.String("id").MaxLen(100)} }
func (tbRfJoinGroup) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", tbRfJoinUser.Type).Ref("groups")}
}

type tbRfJoinRowUser struct{ schema.Schema }

func (tbRfJoinRowUser) Fields() []schema.Field {
	return []schema.Field{field.String("id").MaxLen(9000)}
}
func (tbRfJoinRowUser) Edges() []schema.Edge {
	return []schema.Edge{edge.To("groups", tbRfJoinRowGroup.Type)}
}

type tbRfJoinRowGroup struct{ schema.Schema }

func (tbRfJoinRowGroup) Fields() []schema.Field {
	return []schema.Field{field.String("id").MaxLen(9000)}
}
func (tbRfJoinRowGroup) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", tbRfJoinRowUser.Type).Ref("groups")}
}

func TestTablesJoinTableRefusals(t *testing.T) {
	// 700*4 + 100*4 is 3200 bytes; each ID alone fits.
	e := refusal(t, func() error { _, err := Tables(mustBuild(t, tbRfJoinUser{}, tbRfJoinGroup{}), velox.MySQL); return err }())
	if e.Schema != "tbRfJoinUser" || e.Where != `edge "groups"` || e.Reason != "the key is 3200 bytes on MySQL, over its 3072" ||
		!strings.HasSuffix(e.Pos, "tables_test.go:"+strconv.Itoa(refusalLines["join groups"])) {
		t.Errorf("join key: %+v", e)
	}
	// Each ID is over the key limit, and the join row is 2*36002 bytes.
	_, err := Tables(mustBuild(t, tbRfJoinRowUser{}, tbRfJoinRowGroup{}), velox.MySQL)
	var row *Error
	for _, e := range refusals(t, err) {
		if strings.HasPrefix(e.Reason, "MySQL rows hold") {
			row = e
		}
	}
	if row == nil || row.Schema != "tbRfJoinRowUser" || row.Where != `edge "groups"` ||
		!strings.Contains(row.Reason, "these need 72004:") || !strings.Contains(row.Reason, `: column "tb_rf_join_row_group_id": varchar(9000), 36002 bytes`) {
		t.Errorf("join row: %v", err)
	}
}

// The length bytes: a varchar of 63 characters (252 bytes) has 1, of 64
// (256 bytes) 2; and a row of 65536 bytes is refused.
type tbRfRowBounds struct{ schema.Schema }

func (tbRfRowBounds) Fields() []schema.Field {
	return []schema.Field{field.String("a").MaxLen(16320), field.String("b").MaxLen(63), field.String("c").MaxLen(64)}
}

// 16320*4+2 + 253+1 and the ID's 8 are 65544 bytes.
type tbRfRowOver struct{ schema.Schema }

func (tbRfRowOver) Fields() []schema.Field {
	return []schema.Field{field.String("a").MaxLen(16320), field.Bytes("b").MaxLen(253)}
}

func TestTablesRowLimitBounds(t *testing.T) {
	e := refusal(t, func() error { _, err := Tables(mustBuild(t, tbRfRowBounds{}), velox.MySQL); return err }())
	for _, s := range []string{"these need 65801:", `field "b": varchar(63), 253 bytes`, `field "c": varchar(64), 258 bytes`} {
		if !strings.Contains(e.Reason, s) {
			t.Errorf("reason lacks %q:\n%s", s, e.Reason)
		}
	}
	e = refusal(t, func() error { _, err := Tables(mustBuild(t, tbRfRowOver{}), velox.MySQL); return err }())
	if !strings.Contains(e.Reason, "these need 65544:") || !strings.Contains(e.Reason, `field "b": varbinary(253), 254 bytes`) {
		t.Errorf("reason %s", e.Reason)
	}
}

// --- ent's names

type Writer struct{ schema.Schema }

func (Writer) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.String("email").Unique()}
}
func (Writer) Edges() []schema.Edge {
	return []schema.Edge{edge.To("stories", Story.Type).StorageKey(edge.Column("writer_stories"))}
}
func (Writer) Indexes() []schema.Index { return []schema.Index{index.Fields("name")} }

type Story struct{ schema.Schema }

func (Story) Edges() []schema.Edge {
	return []schema.Edge{edge.From("writer", Writer.Type).Ref("stories").Unique(), edge.To("topics", Topic.Type)}
}

type Topic struct{ schema.Schema }

func (Topic) Edges() []schema.Edge {
	return []schema.Edge{edge.From("stories", Story.Type).Ref("topics")}
}

type UserProfile struct{ schema.Schema }

func (UserProfile) Fields() []schema.Field  { return []schema.Field{field.String("bio")} }
func (UserProfile) Indexes() []schema.Index { return []schema.Index{index.Fields("bio")} }

// Tables names indexes and foreign keys as ent does on each dialect.
func TestDDLNames(t *testing.T) {
	for _, d := range allDialects {
		s := tables(t, d, Writer{}, Story{}, Topic{}, UserProfile{})
		unique, fkIndex := "writers_email_key", "stories_writer_stories"
		if d == velox.MySQL {
			unique, fkIndex = "email", "stories_writers_stories"
		}
		for name, want := range map[string]struct{ indexes, fks []string }{
			"writers":       {[]string{unique, "writer_name"}, nil},
			"stories":       {[]string{fkIndex}, []string{"stories_writers_stories"}},
			"story_topics":  {[]string{"story_topics_topic_id"}, []string{"story_topics_story_id", "story_topics_topic_id"}},
			"user_profiles": {[]string{"userprofile_bio"}, nil},
		} {
			tb := table(t, s, name)
			var fks []string
			for _, fk := range tb.ForeignKeys {
				fks = append(fks, fk.Name)
			}
			if got := tableIndexNames(tb); !slices.Equal(got, slices.Sorted(slices.Values(want.indexes))) || !slices.Equal(fks, want.fks) {
				t.Errorf("%v: %s indexes %q, foreign keys %q; want %q, %q", d, name, got, fks, want.indexes, want.fks)
			}
		}
	}
}

type Subscriber struct {
	schema.Schema
	key string // the declared index's StorageKey
}

func (Subscriber) Fields() []schema.Field { return []schema.Field{field.String("email").Unique()} }
func (s Subscriber) Indexes() []schema.Index {
	ix := index.Fields("email").Unique()
	if s.key != "" {
		ix = ix.StorageKey(s.key)
	}
	return []schema.Index{ix}
}

// A Unique field beside a declared unique index on its column keeps its own
// index, as ent's does, unless the declared index has the name the dialect
// gives the field's.
func TestFieldUniqueBesideIndex(t *testing.T) {
	for _, c := range []struct {
		key                   string
		postgres, mysql, lite []string
	}{
		{"", []string{"subscriber_email", "subscribers_email_key"}, []string{"email", "subscriber_email"}, []string{"subscriber_email", "subscribers_email_key"}},
		{"subscribers_email_key", []string{"subscribers_email_key"}, []string{"email", "subscribers_email_key"}, []string{"subscribers_email_key"}},
		{"email", []string{"email", "subscribers_email_key"}, []string{"email"}, []string{"email"}},
	} {
		for d, want := range map[velox.Dialect][]string{velox.Postgres: c.postgres, velox.MySQL: c.mysql, velox.SQLite: c.lite} {
			tb := table(t, tables(t, d, Subscriber{key: c.key}), "subscribers")
			if got := tableIndexNames(tb); !slices.Equal(got, want) {
				t.Errorf("StorageKey %q, %v: indexes %q, want %q", c.key, d, got, want)
			}
			for _, ix := range tb.Indexes {
				if !ix.Unique || !slices.Equal(ix.Columns, []string{"email"}) {
					t.Errorf("StorageKey %q, %v: %+v", c.key, d, ix)
				}
			}
		}
	}
}

type Member struct{ schema.Schema }

var memberLine int

func (Member) Fields() []schema.Field {
	f, l := field.String("member_name").Unique(), here()
	memberLine = l
	return []schema.Field{field.String("name"), f}
}
func (Member) Indexes() []schema.Index { return []schema.Index{index.Fields("name")} }

// MySQL names a Unique field's index after its column, which may be a
// declared index's name.
func TestTablesMySQLIndexNameClash(t *testing.T) {
	_, err := Tables(mustBuild(t, Member{}), velox.MySQL)
	want := `schema Member: field "member_name": index name "member_name" of the unique index of field "member_name" is also that of index 0, in table members on MySQL, ` +
		`which names the unique index of field "member_name" after its column; give one a StorageKey`
	var ge *Error
	if got := plain(err); got != want || !errors.As(err, &ge) || !strings.HasSuffix(ge.Pos, "/tables_test.go:"+strconv.Itoa(memberLine)) {
		t.Fatalf("got %v, want %q", err, want)
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		tables(t, d, Member{})
	}
}

type Shelf struct{ schema.Schema }

func (Shelf) Edges() []schema.Edge { return []schema.Edge{edge.To("books", Book.Type)} }

// Book's index takes the name MySQL gives its foreign key's index.
type Book struct{ schema.Schema }

func (Book) Fields() []schema.Field { return []schema.Field{field.String("title")} }
func (Book) Edges() []schema.Edge {
	return []schema.Edge{edge.From("shelf", Shelf.Type).Ref("books").Unique()}
}
func (Book) Indexes() []schema.Index {
	return []schema.Index{index.Fields("title").StorageKey("books_shelves_books")}
}

// MySQL names a foreign key's index after the key, which may be a declared
// index's name.
func TestTablesMySQLForeignKeyIndexClash(t *testing.T) {
	_, err := Tables(mustBuild(t, Shelf{}, Book{}), velox.MySQL)
	want := `index name "books_shelves_books" of the foreign-key index of edge "shelf" is also that of index 0, in table books on MySQL, ` +
		`which names the foreign-key index of edge "shelf" after the key; give one a StorageKey`
	if got := plain(err); !strings.HasSuffix(got, want) || strings.Count(got, "\n") > 0 {
		t.Fatalf("got %v, want the suffix %q", err, want)
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		tables(t, d, Shelf{}, Book{})
	}
}

// Signup's unique index over org_id and email has a name a dialect gives
// email's Unique: it does not stand for that Unique, which is kept or
// refused, never lost.
type Signup struct {
	schema.Schema
	key string
}

func (Signup) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id"), field.String("email").Unique()}
}
func (s Signup) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "email").Unique().StorageKey(s.key)}
}

func TestFieldUniqueNotLostToWiderIndex(t *testing.T) {
	// On MySQL, email's index is email: a clash, refused.
	_, err := Tables(mustBuild(t, Signup{key: "email"}), velox.MySQL)
	if got := plain(err); !strings.Contains(got, `field "email": index name "email" of the unique index of field "email" is also that of index 0`) ||
		!strings.HasSuffix(got, "; give one a StorageKey") {
		t.Errorf("mysql: %v", err)
	}
	// On PostgreSQL it is signups_email_key, refused by rule 44 on every
	// dialect.
	_, err = build(t, Signup{key: "signups_email_key"})
	if got, want := plain(err), `schema Signup: field "email": name "signups_email_key" is also this schema's index 0; give index 0 another StorageKey`; got != want {
		t.Errorf("got %v, want %q", err, want)
	}
	// Where the names differ, both unique indexes are made.
	for _, c := range []struct {
		d    velox.Dialect
		key  string
		want []string
	}{
		{velox.Postgres, "email", []string{"email", "signups_email_key"}},
		{velox.SQLite, "email", []string{"email", "signups_email_key"}},
		{velox.SQLite, "sqlite_autoindex_signups_1", []string{"signups_email_key", "sqlite_autoindex_signups_1"}},
	} {
		tb := table(t, tables(t, c.d, Signup{key: c.key}), "signups")
		if got := tableIndexNames(tb); !slices.Equal(got, c.want) || !tb.Indexes[0].Unique || !tb.Indexes[1].Unique {
			t.Errorf("%v, StorageKey %q: indexes %+v, want %q", c.d, c.key, tb.Indexes, c.want)
		}
	}
}
