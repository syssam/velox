package schema

import (
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"

	"github.com/syssam/velox/tests/orm/shapes/types"
)

// Awkward names. Each entity only has to generate, compile and survive a
// create, a query and an update; names_test.go does that for every one.
//
// Names the velox schema loader itself rejects (Query, Type, Schema, Client,
// Config, ...) cannot be part of this list; names the generator rejects or
// miscompiles are pinned in ../known_gaps_test.go.

func labelOnly() []velox.Field { return []velox.Field{field.String("label")} }

// Time collides with package time, which its own package imports.
type Time struct{ velox.Schema }

// Fields of the Time.
func (Time) Fields() []velox.Field {
	return []velox.Field{
		field.String("label"),
		field.Time("time").Default(time.Now),
	}
}

// UUID collides with package uuid, which its own package imports.
type UUID struct{ velox.Schema }

// Fields of the UUID.
func (UUID) Fields() []velox.Field {
	return []velox.Field{
		field.String("label"),
		field.UUID("uuid", uuid.UUID{}).Default(uuid.New),
	}
}

// Field collides with schema/field.
type Field struct{ velox.Schema }

// Fields of the Field.
func (Field) Fields() []velox.Field { return labelOnly() }

// Errors collides with package errors.
type Errors struct{ velox.Schema }

// Fields of the Errors.
func (Errors) Fields() []velox.Field { return labelOnly() }

// Strings collides with package strings.
type Strings struct{ velox.Schema }

// Fields of the Strings.
func (Strings) Fields() []velox.Field { return labelOnly() }

// Math collides with package math.
type Math struct{ velox.Schema }

// Fields of the Math.
func (Math) Fields() []velox.Field { return labelOnly() }

// Runtime collides with package runtime.
type Runtime struct{ velox.Schema }

// Fields of the Runtime.
func (Runtime) Fields() []velox.Field { return labelOnly() }

// Context collides with package context, which the model package imports.
type Context struct{ velox.Schema }

// Fields of the Context.
func (Context) Fields() []velox.Field { return labelOnly() }

// Tx is named like velox.Tx.
type Tx struct{ velox.Schema }

// Fields of the Tx.
func (Tx) Fields() []velox.Field { return labelOnly() }

// Entity is named like the generated entity package.
type Entity struct{ velox.Schema }

// Fields of the Entity.
func (Entity) Fields() []velox.Field { return labelOnly() }

// Migrate is named like the generated migrate package.
type Migrate struct{ velox.Schema }

// Fields of the Migrate.
func (Migrate) Fields() []velox.Field { return labelOnly() }

// Predicate is named like the generated predicate package.
type Predicate struct{ velox.Schema }

// Fields of the Predicate.
func (Predicate) Fields() []velox.Field { return labelOnly() }

// Db is named like the root package of a generated tree in a directory db.
// This tree is in velox, the name the entity Velox has.
type Db struct{ velox.Schema }

// Fields of the Db.
func (Db) Fields() []velox.Field { return labelOnly() }

// UserSetter and UserOrderTerm are named like the setter and order types the
// predicate package gives User.
type UserSetter struct{ velox.Schema }

// Fields of the UserSetter.
func (UserSetter) Fields() []velox.Field { return labelOnly() }

// UserOrderTerm is named like the order type of User.
type UserOrderTerm struct{ velox.Schema }

// Fields of the UserOrderTerm.
func (UserOrderTerm) Fields() []velox.Field { return labelOnly() }

// UserClient is the name the root package would give the client of User (an
// entity cannot be called Client: the schema loader reserves that name).
type UserClient struct{ velox.Schema }

// Fields of the UserClient.
func (UserClient) Fields() []velox.Field { return labelOnly() }

// Orm is named like the runtime package.
type Orm struct{ velox.Schema }

// Fields of the Orm.
func (Orm) Fields() []velox.Field { return labelOnly() }

// Velox is named like the library's root package, which generated code
// imports for the database handle and the errors.
type Velox struct{ velox.Schema }

// Fields of the Velox.
func (Velox) Fields() []velox.Field { return labelOnly() }

// Internal would get a package the application cannot import: the go command
// lets only the generated tree itself import a directory called internal.
type Internal struct{ velox.Schema }

// Fields of the Internal.
func (Internal) Fields() []velox.Field { return labelOnly() }

// Schemainit is named like the generated schemainit package.
type Schemainit struct{ velox.Schema }

// Fields of the Schemainit.
func (Schemainit) Fields() []velox.Field { return labelOnly() }

// Order is named like a suffix of generated code, as are Pred, Set, Edge,
// Edges, Meta, Load, Table, And, Or, Not, Create, Update, Delete and ID below.
type Order struct{ velox.Schema }

// Fields of the Order.
func (Order) Fields() []velox.Field { return labelOnly() }

// Pred is a generated suffix.
type Pred struct{ velox.Schema }

// Fields of the Pred.
func (Pred) Fields() []velox.Field { return labelOnly() }

// Set is a generated suffix.
type Set struct{ velox.Schema }

// Fields of the Set.
func (Set) Fields() []velox.Field { return labelOnly() }

// Edge is a generated suffix and prefix.
type Edge struct{ velox.Schema }

// Fields of the Edge.
func (Edge) Fields() []velox.Field { return labelOnly() }

// Edges is a generated suffix.
type Edges struct{ velox.Schema }

// Annotations of the Edges: velox names the tables of Edge and Edges alike.
func (Edges) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Annotation{Table: "edges_list"}}
}

// Fields of the Edges.
func (Edges) Fields() []velox.Field { return labelOnly() }

// Meta is a generated suffix.
type Meta struct{ velox.Schema }

// Fields of the Meta.
func (Meta) Fields() []velox.Field { return labelOnly() }

// Load is a generated suffix.
type Load struct{ velox.Schema }

// Fields of the Load.
func (Load) Fields() []velox.Field { return labelOnly() }

// Table is an identifier of every entity package.
type Table struct{ velox.Schema }

// Fields of the Table.
func (Table) Fields() []velox.Field { return labelOnly() }

// And is an identifier of every entity package.
type And struct{ velox.Schema }

// Fields of the And.
func (And) Fields() []velox.Field { return labelOnly() }

// Or is an identifier of every entity package.
type Or struct{ velox.Schema }

// Fields of the Or.
func (Or) Fields() []velox.Field { return labelOnly() }

// Not is an identifier of every entity package.
type Not struct{ velox.Schema }

// Fields of the Not.
func (Not) Fields() []velox.Field { return labelOnly() }

// Bulk makes CreateBulk, the name of Create's bulk builder... and of its own
// constructor.
type Bulk struct{ velox.Schema }

// Fields of the Bulk.
func (Bulk) Fields() []velox.Field { return labelOnly() }

// Id is the name of every entity's key.
type Id struct{ velox.Schema } //nolint:revive,staticcheck // the awkward spelling is the point

// Fields of the Id.
func (Id) Fields() []velox.Field { return labelOnly() }

// Json is spelled as velox's own generator trips over an initialism, as are
// Sql and Http below.
type Json struct{ velox.Schema } //nolint:revive,staticcheck // the awkward spelling is the point

// Fields of the Json.
func (Json) Fields() []velox.Field { return labelOnly() }

// Sql is an initialism.
type Sql struct{ velox.Schema } //nolint:revive,staticcheck // the awkward spelling is the point

// Fields of the Sql.
func (Sql) Fields() []velox.Field { return labelOnly() }

// Http is an initialism.
type Http struct{ velox.Schema } //nolint:revive,staticcheck // the awkward spelling is the point

// Fields of the Http.
func (Http) Fields() []velox.Field { return labelOnly() }

// Kind has an enum field shape, whose generated type would be KindShape:
// the name of the next entity.
type Kind struct{ velox.Schema }

// Fields of the Kind.
func (Kind) Fields() []velox.Field {
	return []velox.Field{
		field.String("label"),
		field.Enum("shape").Values("round", "flat").Default("round"),
	}
}

// Edges of the Kind.
func (Kind) Edges() []velox.Edge {
	return []velox.Edge{edge.To("shapes", KindShape.Type)}
}

// KindShape is entity X + field y spelled as one entity XY.
type KindShape struct{ velox.Schema }

// Fields of the KindShape.
func (KindShape) Fields() []velox.Field { return labelOnly() }

// Edges of the KindShape.
func (KindShape) Edges() []velox.Edge {
	return []velox.Edge{edge.From("kind", Kind.Type).Ref("shapes").Unique()}
}

// Keywords has fields named like Go keywords, SQL keywords, predeclared
// identifiers and identifiers of generated code, and edges named likewise.
type Keywords struct{ velox.Schema }

// Annotations of the Keywords: its table is an SQL keyword too.
func (Keywords) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Annotation{Table: "select"}}
}

// Fields of the Keywords.
func (Keywords) Fields() []velox.Field {
	return []velox.Field{
		field.String("type").Default("t"),
		field.String("func").Default("f"),
		field.String("range").Default("r"),
		field.String("select").Default("s"),
		field.String("from").Default("f"),
		field.String("where").Default("w"),
		field.String("order").Default("o"),
		field.String("group").Default("g"),
		field.String("default").Default("d"),
		field.String("table").Default("t"),
		field.String("index").Default("i"),
		field.String("key").Default("k"),
		field.String("values").Default("v"),
		field.String("query").Default("q"),
		field.String("create").Default("c"),
		field.String("create_bulk").Default("cb"),
		field.String("update").Default("u"),
		field.String("delete").Default("d"),
		field.String("set").Default("s"),
		field.String("and").Default("a"),
		field.String("or").Default("o"),
		field.String("not").Default("n"),
		field.String("meta").Default("m"),
		field.String("load").Default("l"),
		field.String("pred").Default("p"),
		field.String("edge").Default("e"),
		field.String("model").Default("m"),
		field.String("orm").Default("o"),
		field.String("context").Default("c"),
		field.String("tx").Default("t"),
		field.String("string").Default("s"),
		field.Int("int").Default(1),
		field.String("error").Default("e"),
		field.String("len").Default("l"),
		field.String("new").Default("n"),
		field.String("string_column").Default("sc"),
		field.Int("int_column").Default(2),
		field.String("to").Default("t"),
		field.String("clear").Optional().Nillable(),
		field.String("asc").Default("a"),
		field.String("desc").Default("d"),
		field.String("eq").Default("e"),
		field.String("in").Default("i"),
		field.String("has").Default("h"),
	}
}

// Edges of the Keywords.
func (Keywords) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("fields", Field.Type),
		edge.To("columns", Field.Type).Unique(),
		edge.To("loaded", Field.Type),
		edge.To("with", Field.Type),
		edge.To("all", Field.Type).Unique(),
	}
}

// Odd collects shapes whose generated identifiers come close to each other:
// JSON types that name another package inside a composite type, an enum next
// to a field named like its type alias, and edges named like identifiers of
// generated code.
type Odd struct{ velox.Schema }

// Fields of the Odd.
func (Odd) Fields() []velox.Field {
	return []velox.Field{
		field.String("label").Default("odd"),
		field.JSON("by_name", map[string]types.Address{}).Optional(),
		field.JSON("list", []types.Address{}).Optional(),
		field.JSON("ptrs", []*types.Address{}).Optional(),
		field.Enum("role").Values("admin", "user").Default("user"),
		field.String("role_type").Default("rt"),
		field.String("role_admin").Default("ra"),
		field.Enum("tier").GoType(types.Tier("")).Optional().Default("free"),
	}
}

// Edges of the Odd.
func (Odd) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("id", Field.Type),
		edge.To("query", Field.Type),
		edge.To("edges", Field.Type),
		edge.To("table", Field.Type).Unique(),
		edge.To("model", Field.Type).Unique(),
	}
}

// Create and Update are prefixes and suffixes of generated builder names.
type Create struct{ velox.Schema }

// Fields of the Create.
func (Create) Fields() []velox.Field { return labelOnly() }

// Update is a generated prefix and suffix.
type Update struct{ velox.Schema }

// Fields of the Update.
func (Update) Fields() []velox.Field { return labelOnly() }

// Entityx is the package name the generator gives Entity.
type Entityx struct{ velox.Schema }

// Fields of the Entityx.
func (Entityx) Fields() []velox.Field {
	return []velox.Field{field.String("label"), field.String("other").Default("o")}
}

// QueryUser is named like a constructor of the entity package would be for
// an entity User... which is the next entity.
type QueryUser struct{ velox.Schema }

// Fields of the QueryUser.
func (QueryUser) Fields() []velox.Field { return labelOnly() }

// User exists for QueryUser.
type User struct{ velox.Schema }

// Fields of the User.
func (User) Fields() []velox.Field { return labelOnly() }

// Clash has enum fields named like the per-entity generated types
// (ClashOrder, ClashSet, ...), enum values that collide with the enum's type
// alias and with each other, a field named like the QueryXxx method of an
// edge and an edge named like the XxxOrErr method of another.
type Clash struct{ velox.Schema }

// Fields of the Clash.
func (Clash) Fields() []velox.Field {
	return []velox.Field{
		field.String("label").Default("c"),
		field.Enum("order").Values("asc", "desc").Default("asc"),
		field.Enum("set").Values("a", "b").Default("a"),
		field.Enum("query").Values("a", "b").Default("a"),
		field.Enum("create").Values("bulk", "single").Default("single"),
		field.Enum("update").Values("a", "b").Default("a"),
		field.Enum("load").Values("low", "high").Default("low"),
		field.Enum("edge").Values("a", "b").Default("a"),
		field.Enum("meta").Values("a", "b").Default("a"),
		field.Enum("pred").Values("a", "b").Default("a"),
		field.Enum("kind").Values("type", "other").Default("other"),
		field.Enum("a").Values("b_c", "x").Default("x"),
		field.Enum("a_b").Values("c", "y").Default("y"),
		field.String("user_name").Default("u1"),
		field.String("query_others").Default("qo"),
		field.String("price").GoType(types.Decimal{}).Default("1.00"),
	}
}

// Edges of the Clash.
func (Clash) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("others", Field.Type),
		edge.To("others_or_err", Field.Type),
	}
}

// EdgesField has a field named like the struct member that holds loaded
// edges, and an edge, so both reach JSON.
type EdgesField struct{ velox.Schema }

// Fields of the EdgesField.
func (EdgesField) Fields() []velox.Field {
	return []velox.Field{field.String("edges").Default("e")}
}

// Edges of the EdgesField.
func (EdgesField) Edges() []velox.Edge {
	return []velox.Edge{edge.To("fields", Field.Type)}
}

// EdgesEnum has an enum field named edges.
type EdgesEnum struct{ velox.Schema }

// Fields of the EdgesEnum.
func (EdgesEnum) Fields() []velox.Field {
	return []velox.Field{field.Enum("edges").Values("a", "b").Default("a")}
}
