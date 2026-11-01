// Package graphql holds the annotations that shape the GraphQL schema velox
// generates, and nothing else. A schema package imports it, so it imports only
// the standard library and velox/schema: the schema package, velox-gen's
// loader and the application's schema code compile no GraphQL library.
//
// A file that also imports gqlgen's graphql package names this one:
//
//	import vgraphql "github.com/syssam/velox/contrib/graphql"
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
//			graphql.Mutations(
//				graphql.MutationCreate,
//				graphql.MutationUpdate,
//				graphql.MutationDelete,
//			),
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
// An ID and a cursor hold the type's name, so renaming the type with Type
// changes every ID and cursor that clients hold. veloxgql.DecodeID refuses an
// ID of the old name with veloxgql.ErrWrongIDType.
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
//	go get -tool github.com/syssam/velox/cmd/velox-gen
//	go get -tool github.com/syssam/velox/contrib/graphql/cmd/velox-graphql
//	go tool velox-gen -out gen -plugin velox-graphql ./schema
//
// That writes the entities, as velox-gen always does, and gen/velox.graphqls,
// the GraphQL schema: a type for each entity, the Node interface, connections,
// ordering, where inputs and mutation inputs. velox-gen owns the file like
// its own: -check fails when the schema changed, and dropping -plugin
// velox-graphql removes the file on the next run.
//
// An entity that has a where input, by a WhereOps field or a WhereEdge edge,
// has the predicates of its node key in it, before the fields': the node key
// has id, idNEQ, idIn, idNotIn, and for an ordered key (an integer or a
// string) idGT, idGTE, idLT and idLTE, typed ID and decoded with the type
// check, so that an ID of another type fails as any other does. A string
// key's idGT, idGTE, idLT and idLTE compare by the database's collation, as
// any string column's do. A uuid or an enum key has the four of equality.
// WhereOps on the key chooses its predicates instead, and Skip(SkipWhereInput)
// on it drops them. The key alone does not give an entity a where input.
//
// The SDL binds to Go by gqlgen's @goField directive (a node's id to IDGQL, an
// edge to <Edge>GQL) and by enum names (velox-gen's Go enum types).
// velox.graphqls declares the directives @goField and @goModel itself, as
// gqlgen documents them (gqlgen v0.17.86, docs/content/config.md), so the
// application must not redeclare them: a second declaration fails gqlgen's
// load. Any other directive, @goTag for one, the application declares in its
// own schema files. @goField and @goModel are velox-graphql's own, so
// graphql.Directives refuses both on an entity, a skipped one and a join
// entity too, and on its fields, the primary key and a foreign key too, and
// its edges. @goField binds fields to the Go code velox generates, and
// @goModel binds types to it and to the application's enum types.
//
// The plugin also writes the Go that gqlgen's autobind binds the SDL's types
// to, in the generated package: gen/<label>_gql.go for each entity with a
// type, and gen/node_gql.go with Node and Nodes. Each entity's file has:
//   - IsNode and IDGQL on a Node, and the <Field>GQL getters;
//   - the connection, edge, order and where types, each named after its
//     GraphQL type, and the marshalers of velox-gen's enum types;
//   - the <Edge>GQL edge methods: a connection, a list or the one node,
//     each read for every row of its batch in one statement;
//   - the root connection function <GoPlural>Connection, named after ent's
//     plural of the GraphQL type (UsersConnection for User,
//     StatusSliceConnection for Status, AccountsConnection for a User exposed
//     as Account), which a Query field's resolver returns;
//   - the where input's method Pred, which turns a where argument into a
//     velox condition.
//
// They use the runtime package veloxgql, and its package gqlimpl for what
// only generated code calls; every file carries
// "const _ = veloxgql.GeneratedCodeIsVersion4", so that generated code and
// the module fail to compile together when they disagree. A connection is
// keyset-paged by its cursors, and runs its page query and its COUNT only
// when gqlgen asks for edges or pageInfo, and for totalCount; see "Loading"
// below for how it reads its rows and their edges. A where
// argument's Pred is one gqlimpl helper call for each predicate the input
// sets; it decodes IDs as their type's, and refuses an ID of another type,
// and a filter nested deeper than MaxFilterDepth, wider than MaxFilterWidth
// or larger in all than MaxFilterNodes, before any query runs. A list edge
// (graphql.Connection(false)) reads at most MaxList rows and fails with
// velox.ErrTooManyRows beyond them, whatever the DB's cap. The limits are
// veloxgql.LimitsFromContext(ctx): the ones given to veloxgql.NewExtension with
// veloxgql.WithLimits, or veloxgql.DefaultLimits().
//
// Every one of these limits bounds one argument or one edge, not a request;
// see "The cost limit" below. A mutation's transaction does not cover @defer
// (see veloxgql.Extension): do not defer fields of a mutation.
//
// The application writes the root query's resolvers, one call each: Node
// and Nodes for node and nodes, and the root connection function for each
// QueryField. The example in examples/graphql in the velox repository is a
// whole server: a schema, its gqlgen.yml and its resolvers.
//
// # Mutations
//
// For an entity with graphql.MutationCreate, its file also has Create<T>Input
// and Create<T>(ctx, input), which createX's resolver returns; with
// graphql.MutationUpdate, Update<T>Input and Update<T>(ctx, id, input), which
// updateX's resolver returns. A create input that gives a field with a Default
// its zero value ("" or the zero time) is refused with BAD_USER_INPUT, since an
// insert would store the Default in its place; an update stores it. In an update
// input a field not given is left as
// it is, clear<Field> sets a nullable one to NULL, and an edge is cleared, then
// its remove<Edge>IDs removed, then its add<Edge>IDs added; a field given with
// its clear, or an ID both to add and to remove, is refused with
// BAD_USER_INPUT; an ID to remove that the edge does not lead to is ignored,
// and one to add that it already leads to is kept. An update with no field to
// set sends no UPDATE, and checks its row with an Exists under the same scope
// as one with fields (Writable): the tenant, and the Read and Write policies.
// Clearing or removing children unlinks only those the caller may change; one
// the tenant scope or the child's Write policy hides stays linked. Clearing a
// many-to-many edge, or removing from it, deletes only the join rows to
// targets the caller may read, under the tenant scope and the target's Read
// policy; a link to one they hide stays.
//
// An edge's inputs follow its foreign key. A key that is the tenant column has
// none: the tenant scope fills it, and an edge to the tenants' entity reads it.
// A key on the entity's own row is set by <edge>ID. A key on the target's rows is re-pointed from the owner
// by <edge>ID, <edge>IDs or add<Edge>IDs, but only a key that is nullable
// and mutable: one that is never NULL always holds an owner, and one that is
// immutable cannot move, so the owner's inputs have no such field, and the
// child is linked from its own side, by its <edge>ID. Create<T> and
// Update<T> re-point only a child no owner holds, or one the row already
// holds: one that another owner holds is refused with BAD_USER_INPUT,
// "already linked", and not taken from it.
//
// A Required one-to-one or many-to-one edge is nullable in the SDL, author: T,
// where entgql gives T!. Required says a row is written with its key; it does
// not say the caller can read the target. A row reads the target in a statement
// of its own, under the target's tenant scope and Read policy, the DB's Rewrite
// hooks and a replica that may lag, and any of them can leave it empty. A
// non-null field that read null would null the page around the row, with an
// error for each such row, which tells the client that a hidden row exists. The
// generator cannot know them all (the plugin's model holds no policy, and a
// Rewrite hook is the DB's), so the field reads null, with no error, and a
// mutation that returns the edge commits. A NULL-able key can read null too: on
// the side that owns the key, as a Required self edge's parent does, velox
// refuses the key nil on every write (velox.ColumnDesc.RequiredEdge), but the
// database's ON DELETE SET NULL clears it when the target is deleted. On the
// other side velox does not refuse it: no child need hold the row. Create<T>Input
// still requires a Required edge's <edge>ID, which must name a row the caller may
// read. When the owning side's key is one velox
// refuses nil, an update on the other side only adds: a one-to-many edge has
// add<Edge>IDs but no remove<Edge>IDs or clear<Edge>, and a one-to-one edge has
// neither <edge>ID nor clear<Edge>, since each would unlink a child and leave
// its key NULL. A child moves by its own <edge>ID, and the create inputs, which
// only link a child whose key is NULL, stay.
//
// A link to a target needs what the write changes. A many-to-many link inserts
// a join row and changes no target row, so the target must be one the caller
// may read: the tenant scope and the target's Read policy. A one-to-many or
// one-to-one link from the owner sets the target's foreign key, so the target
// must be one the caller may change: the tenant scope and its Read and Write
// policies. A link by a key on the entity's own row, <edge>ID, changes no
// target row, so the target must be one the caller may read; a bidirectional
// one-to-one key is the exception, since its link writes the partner's row
// too (see the limits below). Unlinking follows the same rule, but for the
// bidirectional key, whose clear writes the old partner with no scope.
//
// Re-pointing a one-to-one edge from the owner checks the new child first: one
// the caller may not read is NOT_FOUND, as an ID that names no row is. Then,
// when the current child is one the caller may not change, hidden by the
// tenant scope or a policy, the re-point fails with BAD_USER_INPUT "already
// linked", and the child stays linked. The error names only the two types, no
// ID, tenant or column, and the hidden child's own ID is NOT_FOUND like any
// other the caller may not read, so the error does not confirm a guess of it.
// Clearing such an edge, when its current child is one the caller may not
// change, is a silent no-op, not an error: an error would tell the caller that
// a child it may not change is linked there.
//
// A mutation returns the row as written. Create<T> returns the row it
// inserted. Update<T> reads it back under the tenant scope without the Read
// policy: its UPDATE is scoped by the Read policy too, so the caller could read
// the row before it wrote it. A mutation that takes the row out of what the
// caller may read still returns it. This is a contract, and will not change.
//
// With graphql.MutationDelete, Delete<T>(ctx, id) deletes the row and every
// join row that holds its key, of its own many-to-many edges, and returns the
// ID encoded again from the key; deleteX's resolver returns it. velox-gen's own
// migrations give a join table's foreign keys ON DELETE CASCADE, as ent's do,
// so the database would remove them anyway; the delete matters for a schema
// written by hand. The join delete has no tenant scope (a join entity has no
// tenant column), and it goes only after an Exists under the DELETE's own
// scope (Writable: the tenant, and the Read and Write policies; locking the
// row with FOR UPDATE wherever the dialect can) has found the row, so a row the
// caller may not delete keeps them. It removes every join row of the row,
// whatever the tenant of the row at the other end, so a shared row is unlinked
// from every tenant's rows, unless a Write policy or a Delete hook that the
// application binds on a join entity that is not bidirectional narrows or
// refuses it. Rows of other entities that hold the row's key are left to the
// database's foreign keys: one that refuses the DELETE gives a
// *velox.ConstraintError, shown as BAD_USER_INPUT.
//
// The rows a write counts decide its result. The UPDATE of Update<T> that
// matched 0 rows is NOT_FOUND, and a count the driver did not report (-1) falls
// back to the locking existence check of an update with no field to set; an
// update that sets a bidirectional key fails first instead, with the core's
// internal error for a link whose count is unknown. The DELETE of Delete<T>
// that removed 0 rows is NOT_FOUND, and an unknown count is the internal error
// "veloxgql: <mutation>: row count unknown" (gqlimpl.CountUnknown), so that the
// mutation fails and its writes roll back. The UPDATE that links the children
// whose key is on their own rows gives the same kind of error for an unknown
// count, naming the input field.
//
// The generated functions read the DB from the context, as the root query's do
// (veloxgql.DBFromContext), and each is atomic: gqlimpl.Atomic runs it in a
// savepoint inside the transaction the Extension opened for the operation, or
// in a db.Tx of its own where there is none (outside a server, with
// Extension.NewContext, or in an operation WithTxFilter leaves out). A
// mutation's error rolls back its own writes at once, and the operation's when
// the resolver returns it; a resolver that swallows it keeps the operation's
// other writes, and the operation commits. In a Go input a required field is a
// value, an optional one a pointer (nil means not given), an ID a string, a
// list of IDs a []string and a clear field a bool.
//
// Errors are coded before the presenter sees them. A malformed or mistyped ID
// or JSON value, a conflicting input (a field given with its clear, an ID both
// to add and to remove), too many IDs (more than veloxgql.Limits.MaxPage,
// veloxgql.ErrPageTooLarge) and a child another owner holds are BAD_USER_INPUT,
// naming the input field; the first three are refused before any statement. A
// validator's refusal is BAD_USER_INPUT too, "UpdatePostInput.title: invalid
// value: ..."; the validators of the fields an input sets run before any
// statement, so an invalid field is refused even when an ID with it names no
// row. An ID that names no row the caller may read, or change, is NOT_FOUND
// (veloxgql.CodeNotFound), whether the row is missing, another tenant's or
// hidden by a policy, so that a client cannot tell; the mutation keeps nothing,
// and a delete deletes nothing (see above). Other errors of velox are returned
// as they are, and ErrorPresenter maps them as it does elsewhere. gqlgen's own
// validation errors (a variable of the wrong shape) are left as gqlgen writes
// them, GRAPHQL_VALIDATION_FAILED with a path or locations.
//
// Limits of the mutations:
//   - An update that sets no field, and a delete, check their row before they
//     write its edges or join rows, with a locking read (FOR UPDATE) wherever
//     the dialect has one: the row stays the caller's for the rest of the
//     mutation's transaction, so there is no time-of-check race. SQLite has
//     no locking reads, and its transactions lock the database.
//   - On MySQL the DSN needs clientFoundRows=true: UpdateX counts the rows
//     its UPDATE matched, and MySQL counts only changed rows without it, so
//     an UPDATE that changes nothing counts 0 and fails with a false
//     NOT_FOUND.
//   - A bidirectional self-edge (a self-referencing edge with no inverse) is
//     written both ways, as in ent: a many-to-many one writes and removes both
//     join rows, and a one-to-one one keeps both rows' keys pointing at each
//     other. A self-edge with an inverse is written one way. A one-to-one
//     one's link writes the new partner's row under the caller's Write scope,
//     so a partner the caller may read but not change fails the link, with
//     BAD_USER_INPUT "unique constraint violated". Its re-point, and its
//     clear, set the old partner's key NULL without the tenant scope or the
//     policies, as the core's bidirectional update does, for the symmetric
//     edge's integrity: no row is left pointing at one that does not point
//     back. That is unlike a key on the target's rows, whose hidden child
//     stays linked and fails the re-point (gqlimpl.StillLinked, above). A
//     partner that already has one is refused by the unique key, with
//     BAD_USER_INPUT "unique constraint violated", and a row named as its own
//     partner is refused as an invalid value of <edge>ID, with BAD_USER_INPUT
//     "<Input>.<edge>ID: invalid value: a row cannot be its own <edge>".
//   - Not written: a key the caller supplies in a create input and JSON append.
//
// # Loading
//
// The rows one statement reads are a batch: a page's rows, the nodes of node
// and nodes, and the rows an edge read. The first edge field gqlgen resolves
// on any row of a batch reads that edge for every row of it, in one
// statement, and the rows read become a batch in their turn. So a nested
// query sends one statement per edge per level, whatever its rows: a page
// with a window function (velox.LoadEach, with LimitEach of the page size and
// one more), a totalCount with a grouped count (velox.CountEach), a to-one
// edge by the target's key. Nothing waits for other resolvers to join a batch
// (a mutation runs its resolvers one at a time, and would deadlock), so a
// batch is read by the first caller, under its context, on the DB DBFromContext
// reads; its error is every row's.
//
// A call joins a batch only when it is the same read: the same edge, called
// from the edge's own field, with the same arguments. So the same edge under
// two aliases, or with two sets of arguments, is two batches, each with its
// own page, and a nested last, after and before batch too. An edge method
// called from any other field (an application's resolver calling
// x.ChildrenGQL, or two edges from one field) reads alone, one statement
// for its row; so does a call whose arguments are not its batch's first
// caller's (a forceResolver that computes them per row).
//
// A batch is read under its first caller's context. Its key holds the callers'
// row scope (velox.RowScopeFromContext: the tenant, a bypass, the shard), the
// DB and a list edge's MaxList, so callers that differ in them never share a
// read. Any other context value a Read policy or a Rewrite or Around hook
// reads, such as the viewer or a role, must be the same for every row of a
// field, or be returned by veloxgql.WithBatchScope, whose value joins the key:
// a directive or a field middleware that gives an edge field's resolver another
// context for some parent rows needs it. Batches belong to one response: a
// subscription's event starts new ones, and outside a gqlgen field there is no
// batch, so a row read alone resolves its edges one row at a time.
//
// A generated many-to-many edge reads the join table inside the target's
// statement, so the join entity's hooks and policy do not see the edge's
// reads.
//
// veloxgql.Batch makes the rows an application's own resolver returns a
// batch, so their edges batch too. Rows from anywhere else, such as a
// mutation's result, are one row each.
//
// The edge methods read the edge from the database, in the batch, for the
// arguments of the field. They do not read the row's With load (x.Posts):
// an application's With may hold a page its own query chose, which is not the
// edge. A With is not needed for batching, and the edge methods ignore it.
//
// A page, a node and an edge's nodes read only the columns their selection
// needs: the selected fields', the key, every foreign key on the row, the
// tenant column and the columns of the order fields. The foreign keys let an
// edge or an application's With work; the order columns keep every cursor
// valid. The columns of the other fields are left unread, so a row handed to
// an application's resolver or directive has them zero: a resolver or a
// directive that reads such a field must select it. A generated field with
// @goField(forceResolver: true) is still a generated field: its selection
// reads its own column only. Narrowing is turned off for a page, and every column
// is read, when the selection holds a field the plugin did not write (an
// application's own, in an extend type, whose resolver may read anything), or
// one of a connection or an edge that gqlimpl.Selected cannot see through
// (see its documentation). It never adds a column the application's query
// left out (velox.Query.Selects), and a <GoPlural>ConnectionOf whose base query
// drops the key or a column an orderBy names is refused with an internal
// error. A mutation's returned row is not narrowed.
//
// # The cost limit
//
// The limits above each bound one argument. Connections nested in
// connections multiply, and aliases repeat a field, so a server also limits
// the cost of the whole request: veloxgql.Limits.MaxCost, 100000 by default.
// veloxgql.Cost computes it, and the Extension refuses a request over it,
// before any resolver runs, with veloxgql.ErrTooCostly (the code
// COST_LIMIT_EXCEEDED, veloxgql.CodeTooCostly) and no statement.
//
// A field costs 1 plus its children's cost times its multiplier. A connection
// multiplies by its first or last, or by MaxPage when it has neither; nodes by
// the number of its ids; any other list of objects by MaxList. Every alias
// counts, and so does every branch of a fragment (an upper bound), except a
// selection that @skip(if: true) or @include(if: false) leaves out, which costs
// nothing, its condition a literal or a variable's value (a missing variable's
// default, when it has one); one whose condition is not a boolean, a variable
// missing with no default or null, counts. __schema and __type cost nothing, so
// introspection works. Variables are read; sums and products saturate, so huge
// arguments cannot wrap the cost, and a named fragment is costed once however
// often it is spread. Two levels of full connection pages cost about 30000 and
// pass the default; three do not. A list edge costs MaxList (1000) for each
// parent, so posts(first: 100) { edges { node { likes { id } } } } costs 100301
// and is refused by default: raise MaxCost, or lower MaxList. gqlgen's
// extension.FixedComplexityLimit counts fields, not rows, and is not needed
// with it.
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
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql.Noder
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
//	      - github.com/syssam/velox/contrib/graphql/veloxgql.Float32
//	  Uint64:
//	    model:
//	      - github.com/syssam/velox/contrib/graphql/veloxgql.Uint64
//	      - github.com/syssam/velox/contrib/graphql/veloxgql.Uint
//	  Time:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql.Time
//	  UUID:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql.UUID
//	  Bytes:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql.Bytes
//	  JSON:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql.JSON
//	  Cursor:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql.Cursor
//	  OrderDirection:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql.OrderDirection
//	  PageInfo:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql.PageInfo
//
// An enum field of the application's own Go type is an SDL enum that
// velox.graphqls binds to that type with @goModel(model: "<package
// path>.<type>"), so gqlgen needs no models entry for it. The type is the
// application's, so it implements gqlgen's graphql.Marshaler and
// graphql.Unmarshaler itself (MarshalGQL and UnmarshalGQL, which write and
// read the GraphQL value the SDL names): the plugin writes them only for
// velox-gen's own enum types.
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
