// Package graphql holds the annotations that shape the GraphQL schema velox
// generates, and nothing else. A schema package imports it, so it imports only
// the standard library and velox2/schema: the schema package, velox-gen's
// loader and the application's schema code compile no GraphQL library.
//
// Every constructor returns the same type, Annotation, named "graphql". A
// schema, field or edge lists as many as it likes in Annotations(), and the
// loader merges them into one (see Annotation.Merge). A short schema:
//
//	type User struct{ schema.Schema }
//
//	func (User) Fields() []schema.Field {
//		return []schema.Field{
//			field.String("name").Annotations(
//				graphql.OrderField("NAME"),
//				graphql.WhereOps(graphql.OpsEquality, graphql.OpsString),
//			),
//			field.Time("created_at").Default(time.Now).Annotations(
//				graphql.OrderField("CREATED_AT"),
//			),
//			field.String("password_hash").Sensitive(), // in no type or input
//		}
//	}
//
//	func (User) Edges() []schema.Edge {
//		return []schema.Edge{
//			edge.To("posts", Post.Type).Annotations(graphql.WhereEdge()),
//		}
//	}
//
//	func (User) Annotations() []schema.Annotation {
//		return []schema.Annotation{
//			graphql.QueryField("users"),
//			graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete()),
//			graphql.MultiOrder(),
//		}
//	}
//
// The annotations of an entity are Skip, Type, QueryField, Mutations,
// MultiOrder and Directives; of a field, FieldName, Skip, OrderField,
// WhereOps, CreateInput, UpdateInput, EnumValues and Directives; of an edge,
// Skip, FieldName, WhereEdge, Connection and Directives. One on an element it
// does not apply to makes the generator fail, naming the schema and the
// element.
//
// # Generating the SDL
//
// The generator is a velox-gen plugin, the tool cmd/velox-graphql of this
// module. Install both as tools of the application's module and run velox-gen
// with the plugin:
//
//	go get -tool github.com/syssam/velox2/cmd/velox-gen
//	go get -tool github.com/syssam/velox2/contrib/graphql/cmd/velox-graphql
//	go tool velox-gen -out gen -plugin velox-graphql ./schema
//
// That writes the entities, as velox-gen always does, and gen/velox.graphqls,
// the GraphQL schema: a type for each entity, the Node interface, connections,
// ordering, where inputs and mutation inputs. velox-gen owns the file like
// its own: -check fails when the schema changed, and dropping -plugin
// velox-graphql removes the file on the next run.
//
// The SDL binds to Go by gqlgen's @goField directive (a node's id to IDGQL, an
// edge to <Edge>GQL) and by enum names (velox-gen's Go enum types).
// velox.graphqls declares the directives @goField and @goModel itself, as
// gqlgen documents them (gqlgen v0.17.86, docs/content/config.md), so the
// application must not redeclare them: a second declaration fails gqlgen's
// load. Any other directive, @goTag for one, the application declares in its
// own schema files.
//
// The plugin also writes the Go that gqlgen's autobind binds the SDL's types
// to, in the generated package: gen/gql_<label>.go for each entity with a
// type, and gen/gql_node.go with Node and Nodes. Each entity's file has:
//   - IsNode and IDGQL on a Node, and the <Field>GQL getters;
//   - the connection, edge, order and where types, each named after its
//     GraphQL type, and the marshalers of velox-gen's enum types;
//   - the <Edge>GQL edge methods: a connection, a list or the one node;
//   - the root connection function <Plural>Connection, named after the
//     plural of the GraphQL type (UsersConnection for User, AccountsConnection
//     for a User exposed as Account), which a Query field's resolver returns;
//   - the where input's method Pred, which turns a where argument into a
//     velox condition.
//
// They use the runtime package veloxgql, and its package gqlimpl for what
// only generated code calls; every file carries
// "const _ = veloxgql.GeneratedCodeIsVersion1", so that generated code and
// the module fail to compile together when they disagree. A connection is
// keyset-paged by its cursors, and runs its page query and its COUNT only
// when gqlgen asks for edges or pageInfo, and for totalCount. A where
// argument's Pred is one gqlimpl helper call for each predicate the input
// sets; it decodes IDs as their type's, and refuses an ID of another type, and a filter nested
// deeper than veloxgql.MaxFilterDepth, wider than veloxgql.MaxFilterWidth
// or larger in all than veloxgql.MaxFilterNodes, before any query runs. A list
// edge (graphql.Connection(false)) reads at most veloxgql.MaxList rows and
// fails with velox.ErrTooManyRows beyond them, whatever the DB's cap.
//
// Every one of these limits bounds one argument or one edge, not a request:
// connections nested in connections multiply, and aliases repeat a field. A
// server also needs a limit on the cost of the whole request, such as gqlgen's
// extension.FixedComplexityLimit, which example/graphql sets. A mutation's
// transaction does not cover @defer (see veloxgql.Handler): do not defer
// fields of a mutation.
//
// The Go of mutations is not written yet. For an entity with Mutations,
// gqlgen writes models of the mutation inputs in the application's package,
// and the application resolves the mutation fields itself until it is.
//
// The application writes the root query's resolvers, one call each: Node
// and Nodes for node and nodes, and the root connection function for each
// QueryField. The example example/graphql in the velox2 repository is a
// whole server: a schema, its gqlgen.yml and its resolvers.
//
// # gqlgen.yml
//
// gqlgen binds a field only to a scalar model of the field's own Go type, so
// each scalar maps to a list of models, one for each Go type velox entities
// hold: veloxgql's function marshalers (MarshalTime for time.Time, and so on)
// and gqlgen's own for the narrow integers. A field of another Go type of the
// application's, and a JSON field, binds to a generated <Field>GQL getter
// that returns a type of the list. With autobind to the generated package:
//
//	autobind:
//	  - <module>/gen
//	models:
//	  Node:
//	    model: github.com/syssam/velox2/contrib/graphql/veloxgql.Noder
//	  Int:
//	    model:
//	      - github.com/99designs/gqlgen/graphql.Int
//	      - github.com/99designs/gqlgen/graphql.Int8
//	      - github.com/99designs/gqlgen/graphql.Int16
//	      - github.com/99designs/gqlgen/graphql.Int32
//	      - github.com/99designs/gqlgen/graphql.Int64
//	      - github.com/99designs/gqlgen/graphql.Uint8
//	      - github.com/99designs/gqlgen/graphql.Uint16
//	      - github.com/99designs/gqlgen/graphql.Uint32
//	  Float:
//	    model:
//	      - github.com/99designs/gqlgen/graphql.FloatContext
//	      - github.com/syssam/velox2/contrib/graphql/veloxgql.Float32
//	  Uint64:
//	    model:
//	      - github.com/syssam/velox2/contrib/graphql/veloxgql.Uint64
//	      - github.com/syssam/velox2/contrib/graphql/veloxgql.Uint
//	  Time:
//	    model: github.com/syssam/velox2/contrib/graphql/veloxgql.Time
//	  UUID:
//	    model: github.com/syssam/velox2/contrib/graphql/veloxgql.UUID
//	  Bytes:
//	    model: github.com/syssam/velox2/contrib/graphql/veloxgql.Bytes
//	  JSON:
//	    model: github.com/syssam/velox2/contrib/graphql/veloxgql.JSON
//	  Cursor:
//	    model: github.com/syssam/velox2/contrib/graphql/veloxgql.Cursor
//	  OrderDirection:
//	    model: github.com/syssam/velox2/contrib/graphql/veloxgql.OrderDirection
//	  PageInfo:
//	    model: github.com/syssam/velox2/contrib/graphql/veloxgql.PageInfo
//
// String, Boolean and ID keep gqlgen's own models. veloxgql.Time,
// veloxgql.UUID, veloxgql.Bytes and veloxgql.Uint64 resolve to the function
// marshalers of those names, which bind time.Time, uuid.UUID, []byte and
// uint64; veloxgql.Uint and veloxgql.Float32 to MarshalUint and
// MarshalFloat32.
//
// The annotation reaches the plugin as JSON under the key "graphql" of the
// element's annotations; the plugin decodes an Annotation from it. The JSON
// field names are therefore a contract.
package graphql
