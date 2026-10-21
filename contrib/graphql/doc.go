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
// OrderUnguarded, WhereOps, CreateInput, UpdateInput, EnumValues and
// Directives; of an edge, Skip, FieldName, WhereEdge, Connection and
// Directives. One on an element it does not apply to makes the generator
// fail, naming the schema and the element.
//
// # Directives
//
// Directives puts the application's directives on the element's GraphQL
// field, and on every where-input field that filters on it: a field's
// predicates (salary, salaryGT, salaryIsNil…), an edge's has<Edge> and
// has<Edge>With, and a foreign key's <edge>ID and <edge>IDIn, which carry the
// key field's directives, then the edge's, each name once. So a directive
// that guards a field guards its filters too, by which a client could
// otherwise find its value a prefix or a bisection at a time.
//
// They also go on every mutation-input field that sets it: a field's
// <field> and clear<Field> in CreateXInput and UpdateXInput, and an edge's
// <edge>ID, <edge>IDs, add<Edge>IDs, remove<Edge>IDs and clear<Edge>, which
// carry the key field's directives first when the key is on the row. A
// directive that guards a field's value guards its writes too. @deprecated
// stays off a required input field, where GraphQL forbids it.
//
// A key field takes GraphQL's own directives only (@deprecated…). A foreign
// key is in no type: its edge stands for it, so a directive that guards it
// goes on the edge, which carries it to the edge's field, its has and
// hasWith filters and the key's predicates. A Node's id is readable through
// node, nodes and the cursors, so a directive on the primary key would guard
// nothing. The plugin refuses an application directive on either.
//
// An entity takes GraphQL's own directives only, too. gqlgen runs a type's
// directives on the fields that return the type, so one on an entity would
// guard its objects and leave its rows' values readable through the cursors,
// the where inputs, the order and totalCount. The plugin refuses an
// application directive on an entity: put it on the fields and edges it
// guards, or use the entity's policy for row access.
//
// The application declares such a directive on FIELD_DEFINITION |
// INPUT_FIELD_DEFINITION, or gqlgen refuses the schema:
//
//	directive @admin on FIELD_DEFINITION | INPUT_FIELD_DEFINITION
//
// Upgrading: this is a breaking change for an application that has a
// directive on a filterable field or edge, or on one a mutation input sets.
// It must also declare that directive on INPUT_FIELD_DEFINITION, since the
// where and mutation inputs now carry it. Before, those filters and writes
// were unguarded.
//
// An order is not guarded: the cursors hold the field's values and the order
// of the nodes reveals them. So OrderField on a field with an application
// directive, any but GraphQL's own (@deprecated…), fails generation unless
// OrderUnguarded allows it.
//
// A directive is field-level access, as is gqlgen's srv.AroundFields;
// row-level access, which rows a caller may read, is the entity's
// velox.Policy, which every query applies.
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
// sets; it decodes IDs as their type's, and refuses an ID of another type,
// and a filter nested deeper than MaxFilterDepth, wider than MaxFilterWidth
// or larger in all than MaxFilterNodes, before any query runs. A list edge
// (graphql.Connection(false)) reads at most MaxList rows and fails with
// velox.ErrTooManyRows beyond them, whatever the DB's cap. The limits are
// veloxgql.LimitsFrom(ctx): the ones given to veloxgql.NewExtension with
// veloxgql.WithLimits, or veloxgql.DefaultLimits().
//
// Every one of these limits bounds one argument or one edge, not a request:
// connections nested in connections multiply, and aliases repeat a field. A
// server also needs a limit on the cost of the whole request, such as gqlgen's
// extension.FixedComplexityLimit, which example/graphql sets. A mutation's
// transaction does not cover @defer (see veloxgql.Extension): do not defer
// fields of a mutation.
//
// For an entity with graphql.Create(), its file also has Create<T>Input and
// Create<T>(ctx, input), which createX's resolver returns; with
// graphql.Update(), Update<T>Input and Update<T>(ctx, id, input), which
// updateX's resolver returns. In an update input a field not given is left
// as it is, clear<Field> sets a nullable one to NULL, and an edge is
// cleared, then its remove<Edge>IDs removed, then its add<Edge>IDs added; a
// field given with its clear, or an ID both to add and to remove, is refused
// with BAD_USER_INPUT. The Go of deleteX is not written yet: the application
// resolves it itself until it is.
//
// An edge's inputs follow its foreign key. A key on the entity's own row is
// set by <edge>ID. A key on the target's rows is re-pointed from the owner
// by <edge>ID, <edge>IDs or add<Edge>IDs, but only a key that is nullable
// and mutable: one that is never NULL always holds an owner, and one that is
// immutable cannot move, so the owner's inputs have no such field, and the
// child is linked from its own side, by its <edge>ID. Create<T> and
// Update<T> re-point only a child no owner holds, or one the row already
// holds: one that another owner holds is refused with BAD_USER_INPUT,
// "already linked", and not taken from it.
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
// MarshalFloat32. gqlgen's narrow integers refuse an Int their Go type
// cannot hold (300 for an int8), and veloxgql.ErrorPresenter shows the
// client gqlgen's text with BAD_USER_INPUT.
//
// # Supported gqlgen versions
//
// velox-graphql supports gqlgen v0.17.86 and later. CI tests v0.17.86 and the
// latest release. The @goField and @goModel declarations, and the mutation
// lock (gqlgen calls field interceptors around the resolver alone), are what
// a new gqlgen could break.
//
// The annotation reaches the plugin as JSON under the key "graphql" of the
// element's annotations; the plugin decodes an Annotation from it. The JSON
// field names are therefore a contract.
package graphql
