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
//			),
//			graphql.MultiOrder(),
//		}
//	}
//
// The annotations of an entity are Skip, Type, QueryField, Mutations,
// MultiOrder and Directives; of a field, FieldName, Skip, OrderField,
// OrderUnguarded, WhereOps, CreateInput, UpdateInput, CreateDefault,
// EnumValues and Directives; of an edge, Skip, FieldName, WhereEdge, Connection, MaxList and
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
// That writes the entities, as velox-gen always does, and gen/velox.graphql,
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
// velox.graphql declares the directives @goField and @goModel itself, as
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
// "const _ = veloxgql.GeneratedCodeIsVersion11", so that generated code and
// the module fail to compile together when they disagree. A connection is
// keyset-paged by its cursors, and runs its page query and its COUNT only
// when gqlgen asks for edges or pageInfo, and for totalCount; see "Loading"
// below for how it reads its rows and their edges. An OrderField may be on a
// nullable field: NULL sorts after every value ascending and before them
// descending, on every dialect, and a cursor holds it as null. A where
// argument's Pred is one gqlimpl helper call for each predicate the input
// sets; it decodes IDs as their type's, and refuses an ID of another type,
// and a filter nested deeper than MaxFilterDepth, wider than MaxFilterWidth
// or larger in all than MaxFilterNodes, before any query runs. A list edge
// (graphql.Connection(false)) reads at most MaxList rows and fails with
// velox.ErrTooManyRows beyond them, whatever the DB's cap; graphql.MaxList(n), which an
// edge may set under the limit, lowers that and what the edge costs, and a request
// refused for a list edge's cost names the edge and these two ways out: it or a connection. The limits are
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
// velox-graphql writes inputs, as entgql does, not mutations: the application
// declares its Mutation type in a schema file of its own, beside velox.graphql,
// and each resolver calls the generated input's method. For an entity with
// graphql.MutationCreate, its file has the input type Create<T>Input, in the
// SDL and in Go, and its method
//
//	func (in *Create<T>Input) Create(ctx context.Context, x *<T>) (*<T>, error)
//
// and with graphql.MutationUpdate, Update<T>Input and
//
//	func (in *Update<T>Input) Update(ctx context.Context, id string, set ...velox.Setter[<T>]) (*<T>, error)
//
// The application names its mutations, their arguments and their result, and
// writes the resolvers:
//
//	type Mutation {
//	  createPost(input: CreatePostInput!): Post!
//	  updatePost(id: ID!, input: UpdatePostInput!): Post!
//	  deletePost(id: ID!): ID!
//	}
//
//	func (r *mutationResolver) CreatePost(ctx context.Context, input gen.CreatePostInput) (*gen.Post, error) {
//		return input.Create(ctx, &gen.Post{AuthorID: viewer(ctx)})
//	}
//
//	func (r *mutationResolver) UpdatePost(ctx context.Context, id string, input gen.UpdatePostInput) (*gen.Post, error) {
//		return input.Update(ctx, id)
//	}
//
// No delete is generated: deleting a row is one velox statement, which the
// application writes with the checks it wants (examples/graphql's deletePost):
//
//	n, err := tx.Delete[gen.Post]().Where(gen.Posts.ID.EQ(key)).Exec(ctx)
//
// n 0 is a row the caller may not delete, missing, in another scope or hidden
// by a policy: NOT_FOUND. n below 0 is a driver that did not report the rows
// it deleted: the resolver returns an internal error, which rolls the
// transaction back, not a success it cannot know. The row's join rows go with
// it where their foreign keys have ON DELETE CASCADE, as velox-gen's
// migrations declare them; otherwise the resolver deletes them too. A
// self-referencing many-to-many edge's join rows hold the row's key in either
// column, so both are deleted; the cascade covers both where each column's
// foreign key has it.
//
// Create sets the input's fields on x, a nil x being a new row, and inserts
// it: the fields the input does not set are inserted as the resolver set them
// on x. A field the input sets overwrites x's, whatever the resolver set
// there: to own a field server-side, leave it out of the input with
// graphql.Skip(graphql.SkipMutationCreateInput) (SkipMutationUpdateInput for
// an update), or set it in a velox.Hook. x's own fields, its foreign keys
// included, are trusted: Create checks the input's IDs against what the
// caller may read, and not x's, which the resolver checks if it must. A field
// the create input leaves out that the schema requires, not Optional, not
// Nillable and with no Default, would be inserted as its zero value, "" in a
// NOT NULL column, so the resolver, which is where entgql users set it, sets
// it on x:
//
//	return input.Create(ctx, &gen.Page{Slug: slugify(input.Title)})
//
// A velox.Hook, which runs before the insert's validators, may set it instead,
// as a tenancy mixin's hook sets its column; with neither, the zero value is
// stored. Create's godoc names such a field unless the schema leaves it out
// explicitly, with Skip(SkipMutationCreateInput) or CreateInput(false): that
// says the application or a hook sets it. A foreign key whose edge's create
// input is skipped is named. A validator's error on such a field is
// the resolver's, not the client's: it names no input field. Create changes x
// even when it fails. Update applies the input's fields and set, the
// resolver's own setters, such as an updated_by the client must not choose. A
// setter of set wins over the input's field, or edge key, that sets the same
// column: the input's setter is dropped, as an UPDATE sets a column once. The
// validators run on both, apart, before any statement: an error on the input's
// fields is BAD_USER_INPUT, named as the input names the field, and one on a
// setter of set is velox's *velox.ValidationError as it is, the resolver's
// error, not the client's.
//
// A field with a Default, or a bool or number with
// graphql.CreateDefault, is optional in the create input and stays non-null in
// the type: velox-gen refuses a Default other than false or 0 on a bool or
// number that is not Nillable, as a Go struct's false could not be told from
// unset, so CreateDefault(true) gives the create input active: Boolean! = true,
// which gqlgen fills when the client leaves it out, and the column no Default.
// A create input that gives a field with a Default
// its zero value ("" or the zero time) is refused with BAD_USER_INPUT, unless the
// Default is that zero value, since an
// insert would store the Default in its place; an update stores it. So is a null
// given to a Nillable field with a Default, literal or variable: leave it out
// for the Default, or clear it with an update, as an insert
// does not store NULL there. In an update input a field not given is left as
// it is, and clear<Field> or a null given sets a nullable one to NULL:
// updatePost(input: {publishedAt: null}) clears publishedAt, where entgql
// ignores the null. A client that sends every field of a form, null for those
// the user did not touch, clears them; it leaves them out instead. A null given to an
// edge's <edge>ID is refused with BAD_USER_INPUT, naming clear<Edge>; a Required field
// has no clear input, and its null is a field not given. Create and Update find
// the null, which the Go input cannot hold, in the arguments of the field that
// runs them, by the input's GraphQL type, through input objects and lists,
// literals and variables alike: where the arguments give the input type once,
// as a mutation's input argument does, its null is read; where they give it
// more than once, in a list or in two arguments, a null on such a field is
// refused with BAD_USER_INPUT naming clear<Field>, since it cannot tell which
// one is this input's; where they give it not at all, as when Go code builds
// the input, no field is null. Two cases remain: Go code that builds an input
// of that type, in a field whose arguments hold one, reads the client's
// explicit nulls of that one; and a value nested in a variable of another
// input type is found only with the server's schema, which the Extension
// keeps, so without it its nulls are not seen.
// An edge is cleared, then
// its remove<Edge>IDs removed, then its add<Edge>IDs added; a field given with
// its clear, or an ID both to add and to remove, is refused with
// BAD_USER_INPUT; an ID to remove that the edge does not lead to is ignored,
// and one to add that it already leads to is kept. An update with no field to
// set sends no UPDATE, and checks its row with an Exists under the same scope
// as one with fields (Writable): the Read and Write policies. Clearing or
// removing children unlinks only those the caller may change; one the
// child's Read or Write policy hides stays linked. Clearing a many-to-many
// edge, or removing from it, deletes only the join rows to targets the caller
// may read, under the target's Read policy; a link to one it hides stays.
//
// An edge's inputs follow its foreign key. A required key that a hook fills,
// such as a scope column (package scope: Fill), has its create input left out
// with graphql.Skip(graphql.SkipMutationCreateInput) on the edge; the edge
// stays in the type. A key on the entity's own row is set by <edge>ID. A key on the target's rows is re-pointed from the owner
// by <edge>ID, <edge>IDs or add<Edge>IDs, but only a key that is nullable
// and mutable: one that is never NULL always holds an owner, and one that is
// immutable cannot move, so the owner's inputs have no such field, and the
// child is linked from its own side, by its <edge>ID. Create and
// Update re-point only a child no owner holds, or one the row already
// holds: one that another owner holds is refused with BAD_USER_INPUT,
// "already linked", and not taken from it.
//
// A Required one-to-one or many-to-one edge is nullable in the SDL, author: T,
// where entgql gives T!. Required says a row is written with its key; it does
// not say the caller can read the target. A row reads the target in a statement
// of its own, under the target's Read policy, the DB's Rewrite
// hooks and a replica that may lag, and any of them can leave it empty. A
// non-null field that read null would null the page around the row, with an
// error for each such row, which tells the client that a hidden row exists. The
// generator cannot know them all (the plugin's model holds no policy, and a
// Rewrite hook is the DB's), so the field reads null, with no error, and a
// mutation that returns the edge commits. A NULL-able key can read null too: on
// the side that owns the key, as a Required self edge's parent does, velox
// refuses the key nil on every write (velox.ColumnDescriptor.RequiredEdge), but the
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
// may read: the target's Read policy. A one-to-many or
// one-to-one link from the owner sets the target's foreign key, so the target
// must be one the caller may change: its Read and Write policies. A link by a key on the entity's own row, <edge>ID, changes no
// target row, so the target must be one the caller may read; a bidirectional
// one-to-one key is the exception, since its link writes the partner's row
// too (see the limits below). Unlinking follows the same rule, but for the
// bidirectional key, whose clear writes the old partner with no scope.
//
// Re-pointing a one-to-one edge from the owner checks the new child first: one
// the caller may not read is NOT_FOUND, as an ID that names no row is. Then,
// when the current child is one the caller may not change, hidden by a
// policy, the re-point fails with BAD_USER_INPUT "already
// linked", and the child stays linked. The error names only the two types, no
// ID or column, and the hidden child's own ID is NOT_FOUND like any
// other the caller may not read, so the error does not confirm a guess of it.
// Clearing such an edge, when its current child is one the caller may not
// change, is a silent no-op, not an error: an error would tell the caller that
// a child it may not change is linked there.
//
// Create and Update return the row as written. Create returns the row it
// inserted. Update reads it back without the Read policy: its UPDATE is scoped by the Read policy too, so the caller could read
// the row before it wrote it. A mutation that takes the row out of what the
// caller may read still returns it. This is a contract, and will not change.
//
// The rows a write counts decide its result. The UPDATE of Update that
// matched 0 rows is NOT_FOUND, "veloxgql: Update<T>Input: no <T> has this ID",
// and a count the driver did not report (-1) falls back to the locking
// existence check of an update with no field to set; an update that sets a
// bidirectional key fails first instead, with the core's internal error for a
// link whose count is unknown. The UPDATE that links the children whose key is
// on their own rows gives the internal error "veloxgql: <Input>.<field>: row
// count unknown" (gqlimpl.CountUnknown) for an unknown count, so that the write
// fails and rolls back.
//
// Create and Update read the DB from the context, as the root query's
// functions do (veloxgql.DBFromContext), and each is atomic: gqlimpl.Atomic runs it in a
// savepoint inside the transaction the Extension opened for the operation, or
// in a db.Tx of its own where there is none (outside a server, with
// Extension.NewContext, or in an operation WithTxFilter leaves out). A
// failed Create or Update rolls back its own writes at once, and the operation's when
// the resolver returns it; a resolver that swallows it keeps the operation's
// other writes, and the operation commits. In a Go input a required field is a
// value, an optional one a pointer (nil means not given), an ID a string, a
// list of IDs a []string and a clear field a bool.
//
// Errors are coded before the presenter sees them. A malformed or mistyped ID
// or JSON value, a conflicting input (a field given with its clear, an ID both
// to add and to remove), too many IDs (more than veloxgql.Limits.MaxPage,
// veloxgql.ErrPageTooLarge) and a child another owner holds are BAD_USER_INPUT,
// naming the input field, "CreatePostInput.tagIDs: ..."; the first three are
// refused before any statement. Update's id, malformed or of another type, is
// named "id". A
// validator's refusal is BAD_USER_INPUT too, "UpdatePostInput.title: invalid
// value: ..."; the validators of the fields an input sets run before any
// statement, so an invalid field is refused even when an ID with it names no
// row. An ID that names no row the caller may read, or change, is NOT_FOUND
// (veloxgql.CodeNotFound), whether the row is missing, in another scope or
// hidden by a policy, so that a client cannot tell; the write keeps nothing. Other errors of velox are returned
// as they are, and ErrorPresenter maps them as it does elsewhere. gqlgen's own
// validation errors (a variable of the wrong shape) are left as gqlgen writes
// them, GRAPHQL_VALIDATION_FAILED with a path or locations.
//
// Limits of Create and Update:
//   - An update that sets no field checks its row before it writes its edges
//     or join rows, with a locking read (FOR UPDATE) wherever
//     the dialect has one: the row stays the caller's for the rest of the
//     mutation's transaction, so there is no time-of-check race. SQLite has
//     no locking reads, and its transactions lock the database.
//   - On MySQL the DSN needs clientFoundRows=true: Update counts the rows
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
//     clear, set the old partner's key NULL without the
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
// # Differences from entgql
//
// What an entgql client or resolver relies on that velox-graphql does otherwise,
// each described above:
//   - An explicit null on a nullable field of an update input clears it, as
//     clear<Field>: true does. entgql treats null as not given. A client that
//     sends null for fields it means to keep must leave them out.
//   - The inputs' methods are Create(ctx, x) and Update(ctx, id, set...),
//     where entgql's are Mutate(builder) on the builder the resolver made. A
//     field the create input leaves out is set on x, which entgql's resolver
//     does with the builder's SetX.
//   - A Required one-to-one or many-to-one edge is T, not T!, in the type.
//   - A null on an update's <edge>ID is refused, naming clear<Edge>.
//   - A field.Strings and the other JSON lists of scalars are [String!], as in
//     entgql; any other JSON is the JSON scalar.
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
// row scope (velox.RowScopeFromContext: the scope values, a bypass, the shard), the
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
// needs: the selected fields', the key, every foreign key on the row and
// the columns of the order fields. The foreign keys let an
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
// the cost of the whole request: veloxgql.Limits.MaxCost, 150000 by default.
// veloxgql.Cost computes it, and the Extension refuses a request over it,
// before any resolver runs, with veloxgql.ErrTooCostly (the code
// COST_LIMIT_EXCEEDED, veloxgql.CodeTooCostly) and no statement.
//
// A cost has two parts, added. The rows: a field costs 1 plus its children's cost
// times its multiplier. A connection's edges multiply by its first or last, or
// by MaxPage when it has neither; nodes by the number of its ids; any other list
// of objects by MaxList. The statements: a field that runs a statement adds 100,
// with no multiplier, as a batch reads all its parents in one statement. Those
// are a root field, a connection, its totalCount and any other field of an object
// or a list of objects, so first: 0 and an alias still count, and the default
// allows at most 1500 statements. An input object argument, as a where, adds its
// size at each use: one for each object and value in it, read from the variables,
// so one variable reused counts each time. In a where, a predicate no index serves
// adds 2000, 20 statements, at each use: <field>Contains, <field>HasPrefix and
// <field>HasSuffix, a LIKE that reads the whole table, and a non-empty
// has<Edge>With, a subquery for each row read. So 700 aliases of
// items(first: 1, where: {nameContains: "x"}), which took 12.5 s over 90000 rows,
// are refused, and the default allows about 70 such filters, while a page that
// filters on two or three costs about 10000. Cost cannot see which columns
// are indexed: a predicate on a column with no index scans the table too, and
// a server with large tables lowers MaxCost. On a mutation, an input object adds a
// statement for the row it reads back, two for a non-empty <edge>IDs, add<Edge>IDs
// or remove<Edge>IDs, one for an <edge>ID and three for a clear<Edge>: true, a page
// of links, which a longer edge repeats.
//
// Every alias counts, and so does every branch of a fragment (an upper bound),
// except a selection that @skip(if: true) or @include(if: false) leaves out,
// which costs nothing, its condition a literal or a variable's value (a missing
// variable's default, when it has one); one whose condition is not a boolean, a
// variable missing with no default or null, counts. The value above it is read
// all the same, so an object whose every selection is left out costs what one
// that selects __typename does. Variables are read; sums and products saturate,
// so huge arguments cannot wrap the cost, and a named fragment is costed once for
// each page size it is spread under. Two levels of full connection pages cost
// about 20000 and pass the default; three do not. A list edge costs MaxList (1000, or its graphql.MaxList)
// for each parent, so a: likes { id } b: likes { id } under posts(first: 100) cost
// 200602 and are refused by default: raise MaxCost, or lower MaxList. gqlgen's
// extension.FixedComplexityLimit counts fields, not rows, and is not needed
// with it.
//
// __schema and __type have a limit of their own, veloxgql.Limits.MaxIntrospectionCost,
// 250000 by default: they cost what they select, counted over the schema, one
// for each field of the response, and a walk that passes the limit stops.
// GraphiQL's query costs 3478 on the example's schema of three entities, about
// 1400 for each rich entity, and about 2500 with graphql.WhereOps on every field, so the
// default admits it on 150 of the first and about 100 of the second. Neither limit
// loosens the other. A request at this one allocates about 160 MB through gqlgen:
// a larger schema raises it, at about 640 bytes a unit, and a server open to
// strangers lowers it, or has no extension.Introspection.
//
// # What a server must call
//
// A server calls veloxgql.Setup first, and serves what it returns:
//
//	srv := handler.New(graph.NewExecutableSchema(cfg))
//	h := veloxgql.Setup(srv, db)
//	srv.AddTransport(transport.POST{})
//	http.Handle("/graphql", h)
//
// Setup uses the Extension and ErrorPresenter on srv, and refuses a body over
// Limits.MaxBodyBytes, 1 MiB by default, which nothing else in a request bounds. It holds
// a websocket's messages to the same: a connection whose message goes over is closed
// before the message is read, and no message is compressed. So a public websocket is
// bounded in the size of a message, under Setup; a server that calls only the Extension
// has no such limit. How many connections and messages a client sends is outside the
// bound at either: a rate limit in front has that.
//
// gqlgen validates a request before any resolver, and before the extension's cost,
// and validation is quadratic in the fields that share a response name: 12 KB of
// a: id a: name took 3.49 GB and returned 250 MB of errors. So before gqlgen parses
// a request, on every transport, the Extension parses it, a second parse that a
// query cache of gqlgen's does not spare, and checks all of it and its variables that a
// later stage grows with, refusing one over any limit with BAD_USER_INPUT:
// Limits.MaxTokens, 15000 by default; MaxFields, 5000, of fields, spreads and inline
// fragments; MaxFragments, 500; MaxDepth, 64, of selections, values and types;
// MaxNameBytes, 128, or the schema's longest; MaxLiteralBytes, 64 KiB; and
// MaxVariableNodes, 100000, where an object of an input type weighs at least 4. MaxFields
// also bounds the pairs of fields under one response name, the fragments spread where they
// are, at 2 times it for pairs that differ and 200 times for pairs that agree, so that 340
// colocated fragments of 14 common fields pass. In TestRequestBounded's shapes, validation
// allocates under 40 MB through veloxgql.Setup, which also stops at MaxErrors errors and
// shows no suggestions, and what runs after it answers to the cost. GraphiQL's query is
// far under each limit.
//
// Cost does not see a value's size: one string of 900 KB under 300 aliases took 3.7 GB.
// So the Extension counts a response as its fields resolve, its strings as they are
// escaped, its bytes and its JSON, and stops at Limits.MaxResponseBytes, 32 MiB; it stops
// at MaxErrors of the errors its fields make, where 97000 of them took 516 MB; and it
// stops a query or a mutation at Limits.Timeout, 30 s, cancelling the statements' context.
// Each answers one clean error and data null, COST_LIMIT_EXCEEDED or TIMEOUT, and a
// mutation rolls back. What remains, plainly: 844 MB is the most a request was measured
// to allocate at the defaults, with introspection on, as MaxCost and MaxIntrospectionCost
// are separate budgets and one request spends both: long aliases of titles at MaxCost and
// aliased introspection at its own limit, a 59 MB response, as aliases and numbers are not
// counted. Without extension.Introspection it was 583 MB and 47 MB; rows being read when the limit
// is passed are read before they are counted, one per connection a client aliased; a
// custom scalar's value that is not a string, bytes or JSON kind is not counted; a resolver
// that ignores its context holds the response until it returns; a presenter of the
// application's that does not call veloxgql.ErrorPresenter makes the errors of fields
// that stop before they are dropped. Time and bytes are per request: many of them are for
// what is in front.
// docs/decisions/0027-graphql-limits.md has the measurements.
//
// # gqlgen.yml
//
// gqlgen binds a field only to a scalar model of the field's own Go type, so
// each scalar maps to a list of models, one for each Go type velox entities
// hold: veloxgql's function marshalers (MarshalTime for time.Time, and so on)
// and gqlgen's own for the narrow integers. A field of another Go type of the
// application's, and a JSON field, binds to a generated <Field>GQL getter
// that returns a type of the list. A JSON field of []string, []int, []int64,
// []float64 or []bool is a list of String!, Int!, Float! or Boolean!, as entgql
// gives field.Strings, in the type and the inputs, and binds the Go slice; its
// filters stay JSON's, none. With autobind to the generated package:
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
// velox.graphql binds to that type with @goModel(model: "<package
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
