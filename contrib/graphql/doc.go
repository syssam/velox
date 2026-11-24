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
//	type User struct{ velox.Schema }
//
//	func (User) Fields() []velox.Field {
//		return []velox.Field{
//			field.String("name").Annotations(
//				graphql.OrderField("NAME"),
//				graphql.WhereOps(graphql.OpsEquality, graphql.OpsString),
//			),
//			field.Time("created_at").Default(time.Now).Annotations(
//				graphql.OrderField("CREATED_AT"),
//			),
//			field.String("password_hash").Sensitive(), // in no type, where or order
//		}
//	}
//
//	func (User) Edges() []velox.Edge {
//		return []velox.Edge{
//			edge.To("posts", Post.Type).Annotations(graphql.WhereEdge()),
//		}
//	}
//
//	func (User) Annotations() []velox.Annotation {
//		return []velox.Annotation{
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
// A cursor holds the type's name, so renaming the type with Type changes
// every cursor that clients hold, and a connection refuses a cursor of the
// old name with veloxgql.ErrBadCursor. An ID is the key alone, as in entgql,
// and does not change.
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
// hasWith filters and the key's predicates. A type's id is readable through
// the cursors and every edge to the type, so a directive on the primary key
// would guard nothing. The plugin refuses an application directive on either.
//
// An entity takes GraphQL's own directives only, too. gqlgen runs a type's
// directives on the fields that return the type, so one on an entity would
// guard its objects and leave its rows' values readable through the cursors,
// the where inputs, the order and totalCount. The plugin refuses an
// application directive on an entity: put it on the fields and edges it
// guards, or use the entity's policy for row access.
//
// A directive's object is the row as its page or edge read it, narrowed to
// the columns the selection needs: a column whose field is not selected
// holds its zero value, so a directive that reads a field of the object
// needs it selected (see "Loading").
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
//	go tool velox-gen -out veloxgen -plugin velox-graphql ./veloxschema
//
// That writes the entities, as velox-gen always does, and veloxgen/velox.graphql,
// the GraphQL schema: a type for each entity, connections, ordering, where
// inputs and mutation inputs. By default it has no Node interface and no node or nodes
// query (node= below); see "IDs". velox-gen owns the file like
// its own: -check fails when the schema changed, and dropping -plugin
// velox-graphql removes the file on the next run.
//
// An application that writes its own SDL, and binds it to velox-gen's types
// with gqlgen's autobind, asks for the enum marshalers alone:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:only=enums ./veloxschema
//
// That writes no velox.graphql and no other Go: a MarshalGQL and an
// UnmarshalGQL for each enum type velox-gen declares, in
// veloxgen/<entity>_gql.go. A stored value goes out upper-cased, open as
// OPEN, or as EnumValues names it, and comes back as the stored value; any
// other value is refused. The application's SDL declares the enum under
// velox-gen's type name with those values, so that autobind binds it.
// Without the marshalers, gqlgen writes the stored value as it is and hands
// the GraphQL value to the query unchanged.
//
// sdlfiles=entity writes the SDL as velox.graphql and one file per entity,
// velox_<label>.graphql:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:sdlfiles=entity ./veloxschema
//
// An entity's file has its type, connection, order, where and mutation
// inputs, and the enums only it uses. velox.graphql has the scalars,
// PageInfo, OrderDirection, the root Query and the enums two entities use.
// gqlc makes a Go package of each file, so Query's fields and the entities'
// name their argument structs apart. sdlfiles=one, one file, is the
// default. An entity's file is named by its name in lower case
// (velox_salesorder.graphql).
//
// entities= writes the SDL of the named schemas alone, separated by +:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:entities=Post+User ./veloxschema
//
// Each has its type, its inputs and its Query field; no other schema has a
// Query field. A field whose type is a schema left out, such as an edge to
// one, is left out. A where input keeps every predicate, as graphql-go binds
// it to its Go struct, so a where input it nests is written too, without its
// schema's type; under sdlfiles=entity it goes in velox.graphql. The Go is
// the whole schema's either way (ADR 0125).
//
// apptypes= names the application's own types, separated by +:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:entities=Post,apptypes=Decimal+Map ./veloxschema
//
// A scalar of velox-graphql's vocabulary it lists, JSON, UUID, Uint64,
// Bytes or Time, is left out of velox.graphql for the application to
// declare; any other type velox-graphql declares is refused. Under
// entities=, a field naming a type that neither velox-graphql declares nor
// apptypes= lists is left out, such as a graphql.Map field of an application type
// the subset does not serve. An input naming one is refused, as graphql-go
// binds every field of an input. Without apptypes= every such type stays,
// and the application declares each (ADR 0130).
//
// names= chooses how fields are named. names=go, the default, camel-cases
// the Go name, as entgql does: image_url is imageURL, and an edge's
// foreign key filters as customerID and customerIDIn. names=schema
// camel-cases the schema name, each word title-cased: imageUrl, customerId
// and customerIdIn. Predicates, inputs and the names veloxgql.DecodeWhere
// reads follow the field's name. Order values are graphql.OrderField's
// either way.
//
// filters= chooses what a where input means. filters=strict, the default,
// fails closed: an empty idIn or or matches no node, a false isNil means
// IS NOT NULL, and MaxFilterWidth applies. filters=lenient reads a where
// input leniently: an empty list or a false isNil adds nothing, a nested
// empty input is an error (veloxgql.ErrEmptyWhereInput), and the node
// budget is ten times larger (veloxgql's gqlgraph.Type.LenientFilters).
//
// node= chooses whether the schema has Relay's Node. node=off, the default,
// writes none (ADR 0061). node=on declares interface Node, bound to the
// generated Noder, makes every type with an ID implement it, and puts
// node(id:) and nodes(ids:) first in the root query, which is then written
// even with no QueryField. veloxgen/noder_gql.go holds Noder, ResolveNode
// and ResolveNodes for the application's resolvers. An ID stays the key,
// so node(id:) needs IDs unique across types, as snowflakes, UUIDs and
// prefixed IDs are; auto-increment keys collide, and node(id:) is then
// ambiguous. One statement finds the types that hold an ID. The generated
// WithNodeType and WithFixedNodeType, as entgql's, give an ID's type
// instead: only that type is read, with no lookup (ADR 0152).
//
// idops=, orderfields=, queryfields=, optional=, requirededges=, inputkeys=
// and jsontypes= each shape one more part, for an application that keeps
// an API another generator made; command velox-graphql lists their values,
// and docs/guide.md the set an application of the old velox gives (ADR
// 0155). Each refuses a value it does not know, and compat=, which they
// replace, is refused with them.
//
// An entity that has a where input, by a WhereOps field or a WhereEdge edge,
// has the predicates of its key in it, before the fields': the key has id,
// idNEQ, idIn, idNotIn, and for an ordered key (an integer or a string)
// idGT, idGTE, idLT and idLTE, typed ID and of the key's Go type. A string
// key's idGT, idGTE, idLT and idLTE compare by the database's collation, as
// any string column's do. A uuid or an enum key has the four of equality.
// WhereOps on the key chooses its predicates instead, and Skip(SkipWhereInput)
// on it drops them. The key alone does not give an entity a where input.
//
// The SDL binds to Go by gqlgen's own matching of names, and by its @goField
// directive where the names differ: a key field whose Go name is not ID, and
// a field whose Go type its scalar does not bind to its <Field>GQL getter.
// The id field binds to the key itself. An edge binds to the method of its Go
// name, Author or Tags, as entgql's does: the relations are in the
// struct's Edges field, so the name is free. An edge with graphql.MaxList
// keeps @goField, which holds the limit. Enums bind by name (velox-gen's
// Go enum types).
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
// type. Each entity's file has:
//   - the <Field>GQL getters;
//   - the connection, edge, order and where types, each named after its
//     GraphQL type, and the marshalers of velox-gen's enum types;
//   - the edge methods, named after the edges: a connection, a list or the
//     one node, each read for every row of its batch in one statement, on
//     the DB veloxgql.DBFromContext reads; a connection is the target's
//     new<Type>Connection of the edge, the function PaginateQuery calls
//     with its query;
//   - the entity's PaginateQuery, which a query's Paginate calls, as ent's
//     Paginate: db.Query[veloxgen.User]().Paginate(ctx, after, first, before,
//     last, veloxgen.WithUserOrder(orderBy), veloxgen.WithUserFilter(where)). The
//     options are named after the GraphQL type (WithAccountOrder for a User
//     exposed as Account). The connection sends nothing until gqlgen
//     resolves its fields, and a Query field's resolver returns it;
//   - the where input's method Pred, which turns a where argument into a
//     velox condition.
//
// They use the runtime package veloxgql, and its package gqlgraph for what
// only generated code calls; every file carries
// "const _ = veloxgql.GeneratedCodeIsVersion30", so that generated code and
// the module fail to compile together when they disagree. A connection is
// keyset-paged by its cursors, and runs its page query and its COUNT only
// when gqlgen asks for edges or pageInfo, and for totalCount; see "Loading"
// below for how it reads its rows and their edges. An OrderField, or an
// OrderVia of a to-one edge's target, may be on a nullable field: NULL sorts
// after every value ascending and before them descending, on every dialect,
// and a cursor holds it as null. An OrderVia reads the edge with a LEFT JOIN,
// so a row whose target is missing or hidden sorts as NULL, and the order
// changes neither the connection's rows nor its totalCount. An OrderField
// on a to-many edge orders by the edge's count, velox's Rel.Count under the
// target's policy, interceptors and filters; a cursor holds it as null. A where
// argument's Pred is gqlgraph.Predicate of the input, which adds each
// predicate the input sets as the type's WhereFields describe it; it refuses a filter nested deeper than MaxFilterDepth, wider than MaxFilterWidth
// or larger in all than MaxFilterNodes, before any query runs. A list edge
// (graphql.Connection(false)) reads at most MaxList rows and fails with
// velox.ErrTooManyRows beyond them, whatever the DB's cap; graphql.MaxList(n), which an
// edge may set under the limit, lowers that and what the edge costs, and a request
// refused for a list edge's cost names the edge and these two ways out: it or a connection. The limits are
// veloxgql.LimitsFromContext(ctx): the ones given to gqlgenx.NewExtension with
// gqlgenx.WithLimits, or veloxgql.DefaultLimits(), which graphqlgo always uses;
// under graphqlgo velox's cost is not applied, and the server bounds a
// request with graphql.WithQueryCost and graphql-go's other limits.
//
// Every one of these limits bounds one argument or one edge, not a request;
// see "The cost limit" below. A mutation's transaction does not cover @defer
// (see gqlgenx.Extension): do not defer fields of a mutation.
//
// The application writes the root query's resolvers: for each QueryField a
// query's Paginate on the request's DB, veloxgql.DB(ctx):
//
//	func (r *queryResolver) Users(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *veloxgen.UserOrder, where *veloxgen.UserWhereInput) (*veloxgen.UserConnection, error) {
//		return veloxgql.DB(ctx).Query[veloxgen.User]().Paginate(ctx, after, first, before, last, veloxgen.WithUserOrder(orderBy), veloxgen.WithUserFilter(where))
//	}
//
// The query may hold the application's own filters and With loads. It must
// not be ordered, and must select the key and the order columns. The example
// in examples/graphql in the velox repository is a whole server: a schema,
// its gqlgen.yml and its resolvers.
//
// # Fields the application resolves
//
// A type may have fields the model cannot give: one of a type the application
// declares in its own SDL that the entity holds a key to without an edge, or a
// value computed per row. graphql.Resolvers on the schema lists them, each a
// graphql.Map of the field's name, with its arguments when it takes any, and
// its type:
//
//	func (Asset) Annotations() []velox.Annotation {
//		return []velox.Annotation{
//			graphql.Resolvers(
//				graphql.Map("organization", "PublicOrganization").WithComment("Owning organization."),
//				graphql.Map("tags(first: Int)", "[String!]!"),
//			),
//		}
//	}
//
// velox.graphql then has the fields in the type, after its own, each with
// @goField(forceResolver: true), so gqlgen asks for a resolver method on the
// type's resolver: Organization(ctx, obj *veloxgen.Asset). The application
// declares PublicOrganization in its own SDL; velox-graphql does not look for
// it, and gqlgen refuses the schema when nothing declares it. It is the same
// as an extend type with those fields, written beside the entity. A
// selection that holds one of them reads every column of the entity, as one of
// an extend type's does (see "Loading"): the resolver may read any column.
//
// # Mutations
//
// velox-graphql writes inputs, as entgql does, not mutations: the application
// declares its Mutation type in a schema file of its own, beside velox.graphql,
// and each resolver writes the row with velox, from the setters the generated
// input gives. For an entity with graphql.MutationCreate, its file has the
// input type Create<T>Input, in the SDL and in Go, and with
// graphql.MutationUpdate, Update<T>Input; each has one method:
//
//	func (in Create<T>Input) Setters() ([]velox.Setter[<T>], error)
//	func (in Update<T>Input) Setters() ([]velox.Setter[<T>], error)
//
// Setters is plain generated Go, as entgql's Mutate is: one column setter
// for each field given, one relation handle's Add, Remove or Clear for each
// edge field given, and a last call to gqlgraph.Validate, which runs the
// columns' validators. It does no I/O. An input is a velox.Input, which
// db.Insert(x).SetInput and db.Update[T]().SetInput take, as ent's
// SetInput does; a Setters error is kept, and returned unchanged by Exec or
// One before anything is sent. The application names its mutations, their
// arguments and their result, and writes the resolvers:
//
//	type Mutation {
//	  createPost(input: CreatePostInput!): Post!
//	  updatePost(id: ID!, input: UpdatePostInput!): Post!
//	  deletePost(id: ID!): ID!
//	}
//
//	func (r *mutationResolver) CreatePost(ctx context.Context, input veloxgen.CreatePostInput) (*veloxgen.Post, error) {
//		x := &veloxgen.Post{AuthorID: viewer(ctx)}
//		if err := veloxgql.DB(ctx).Insert(x).SetInput(input).Exec(ctx); err != nil {
//			return nil, err
//		}
//		return x, nil
//	}
//
//	func (r *mutationResolver) UpdatePost(ctx context.Context, id int64, input veloxgen.UpdatePostInput) (*veloxgen.Post, error) {
//		x, err := veloxgql.DB(ctx).Update[veloxgen.Post]().Where(veloxgen.Posts.ID.EQ(id)).SetInput(input).One(ctx)
//		if velox.IsNotFound(err) {
//			return nil, notFound(ctx, "Post")
//		}
//		return x, err
//	}
//
// UpdateQuery.One is ent's UpdateOne: it writes the one row its conditions
// match and returns it as written, read back by its key without the
// policies, so even an update that took the row out of what the caller may
// read returns it. A row the caller may not read or change, or that does
// not exist, is velox.ErrNotFound, which the resolver shows as NOT_FOUND;
// an input that sets nothing returns the row as it is.
//
// A Sensitive field is in the inputs, write-only: no type, where input or
// order has it, and the input's String and GoString leave it out, so a
// logged input does not show it. Where the client should not choose its
// value, as a password's hash, graphql.Skip(SkipMutationCreateInput,
// SkipMutationUpdateInput) leaves it out, and the resolver sets it, on the
// row to insert or as a setter of its own, as examples/graphql's createUser
// and updateUser do from a password argument:
//
//	set = append(set, veloxgen.Users.PasswordHash.Set(hash))
//
// No delete is generated: deleting a row is one velox statement, which the
// application writes with the checks it wants (examples/graphql's deletePost):
//
//	n, err := tx.Delete[veloxgen.Post]().Where(veloxgen.Posts.ID.EQ(id)).Exec(ctx)
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
// In a Go input a required field is a value, an optional one a pointer, an
// ID the target's key in its Go type, a list of IDs a slice of keys and a
// clear field a bool. A nil
// pointer or list is a field not given, and Setters gives it no setter. A
// null is a field not given too, as in entgql: only clear<Field> or
// clear<Edge> sets NULL or unlinks. A clear field wins over its field: with
// both, the field is cleared. A create input's required fields are always
// set. An Immutable field is only in the create input.
//
// A create's setters are stored in x, the row the resolver passes to
// db.Insert, before the insert, so a field the input gives overwrites x's:
// to own a field server-side, leave it out of the input with
// graphql.Skip(graphql.SkipMutationCreateInput) (SkipMutationUpdateInput for
// an update), or set it in a velox.Hook. A field the input does not give
// keeps x's value, or takes the field's Default. x's own fields, its foreign
// keys included, are trusted: the resolver checks them if it must. A field
// the create input leaves out that the schema requires, not Optional, not
// Nillable and with no Default, would be inserted as its zero value, "" in a
// NOT NULL column, so the resolver, which is where entgql users set it, sets
// it on x:
//
//	x := &veloxgen.Page{Slug: slugify(input.Title)}
//
// A velox.Hook, which runs before the insert's validators, may set it instead,
// as a tenancy mixin's hook sets its column; with neither, the zero value is
// stored. The create input's Setters godoc names such a field unless the
// schema leaves it out explicitly, with Skip(SkipMutationCreateInput) or
// CreateInput(false): that says the application or a hook sets it. A foreign
// key whose edge's create input is skipped is named. An update's resolver
// appends its own setters to the input's, such as an updated_by the client
// must not choose; where both set a column, the UPDATE fails, so the
// resolver drops the input's setter of that column first.
//
// A field with a Default is optional in the create input and stays non-null
// in the type. The input sets it only when the client gives it, and an insert
// fills defaults before the setters, so active: false is stored and a missing
// active takes the Default (velox decision 0082). graphql.CreateDefault(true)
// is for a field with no schema Default: it gives the create input
// active: Boolean! = true, which gqlgen fills when the client leaves it out.
//
// An edge's inputs follow its foreign key. A required key that a hook fills,
// such as a scope column (package scope: Fill), has its create input left out
// with graphql.Skip(graphql.SkipMutationCreateInput) on the edge; the edge
// stays in the type. A key on the entity's own row is set by <edge>ID, and
// cleared by clear<Edge>: Setters gives the edge's velox.Rel.Set, which
// checks that the ID names a row the caller may read before the write sets
// the column, and the column's SetNull. A key on
// the target's rows is re-pointed from the owner by <edge>ID, <edge>IDs or
// add<Edge>IDs, but only a key that is nullable and mutable: one that is
// never NULL always holds an owner, and one that is immutable cannot move,
// so the owner's inputs have no such field, and the child is linked from its
// own side, by its <edge>ID. Those, and a many-to-many edge's IDs, are the
// relation handle's Add, Remove and Clear, which velox.InsertQuery.Set
// describes: an edge is cleared, then its remove<Edge>IDs removed, then its
// add<Edge>IDs added; an ID to add is checked against what the caller may
// read; a child another owner holds is refused, not taken from it; and
// clearing or removing unlinks only the targets the caller may read, or
// change for a key on the target's rows. An ID to remove that the edge does
// not lead to is ignored, and one to add that it already leads to is kept.
// An update with edge setters reads the rows it matches under the Read and
// Write policies, locked wherever the dialect can, before it writes their
// edges, so no edge changes on a row the caller cannot write; one with no
// column to set sends no UPDATE.
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
// still requires a Required edge's <edge>ID. When the owning side's key is one velox
// refuses nil, an update on the other side only adds: a one-to-many edge has
// add<Edge>IDs but no remove<Edge>IDs or clear<Edge>, and a one-to-one edge has
// neither <edge>ID nor clear<Edge>, since each would unlink a child and leave
// its key NULL. A child moves by its own <edge>ID, and the create inputs, which
// only link a child whose key is NULL, stay.
//
// A write with edge setters is atomic: velox runs the insert or update and
// its edges in a db.Tx, a savepoint inside the transaction gqlgenx.Extension
// opened for the operation, so an edge's error undoes the row at once. One
// with column setters alone is one statement. A resolver that returns the
// error fails the operation, and the Extension rolls all of it back; a
// resolver that swallows it keeps the operation's other writes, and the
// operation commits.
//
// Errors are coded before the presenter sees them. Setters refuses, before
// any statement, with BAD_USER_INPUT naming the input field: a malformed
// JSON value ("CreatePostInput.meta: ..."); a list of more than
// gqlgraph.MaxIDs IDs, wrapping veloxgql.ErrPageTooLarge; and a
// validator's refusal ("UpdatePostInput.title: invalid value: ..."), even
// when an ID with it names no row. The write's errors are velox's, as they
// are, and ErrorPresenter maps them as it does elsewhere; an edge's
// *velox.EdgeError is NOT_FOUND, "edge <edge>: no node has this ID", for an
// ID that names no row the caller may read or change, whether the row is
// missing, in another scope or hidden by a policy, so that a client cannot
// tell, and BAD_USER_INPUT, "edge <edge>: already linked to another owner",
// for a child another owner holds. gqlgen's own validation errors (a
// variable of the wrong shape) are left as gqlgen writes them,
// GRAPHQL_VALIDATION_FAILED with a path or locations. An ID that is no key,
// as "abc" for an int64 key, fails in its marshaler, before any resolver, and
// gqlgenx.ErrorPresenter shows it as BAD_USER_INPUT.
//
// Not written: a key the caller supplies in a create input and JSON append.
//
// # Differences from entgql
//
// What an entgql client or resolver relies on that velox-graphql does otherwise,
// each described above:
//   - The inputs' method is Setters(), which returns velox setters, where
//     entgql's is Mutate(builder) on the builder the resolver made. The
//     resolver writes them with db.Insert(x).SetInput or
//     db.Update[T]().SetInput, as entgql's resolver calls SetInput.
//   - A Required one-to-one or many-to-one edge is T, not T!, in the type.
//   - A field.Strings and the other JSON lists of scalars are [String!], as in
//     entgql; any other JSON is the JSON scalar.
//   - By default there is no Node interface, and no node or nodes query; a type does not
//     implement Node. An ID is the entity's key, in its own Go type, as in
//     entgql, and every key of a schema is of one Go type (see "IDs"). An
//     application finds a row by its ID through a where input's id or idIn,
//     or a query field of its own. node=on declares Node, as entgql does.
//   - A where input reads some values as SQL does, where entgql adds nothing
//     for them: <field>IsNil: false is IS NOT NULL, as <field>NotNil: true
//     is; an empty <field>In matches no row; or: [] matches no row; and not:
//     {}, the negation of an input that holds for every row, matches none,
//     where entgql refuses an empty input.
//   - An orderBy through an edge (graphql.OrderVia) keeps every row of the
//     connection, and its totalCount: a row whose target is missing or
//     hidden sorts as NULL.
//
// # Loading
//
// The rows one statement reads are a batch: a page's rows and the rows an
// edge read. The first edge field gqlgen resolves
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
// from the edge's own field, with the same arguments, those the edge method
// was given, compared by value (a variable and the same literal are equal).
// So the same edge under
// two aliases, or with two sets of arguments, is two batches, each with its
// own page, and a nested last, after and before batch too. An edge method
// called from any other field (an application's resolver calling
// x.Children, or two edges from one field) reads alone, one statement
// for its row; so does a call whose arguments are not its batch's first
// caller's (a forceResolver that computes them per row).
//
// A batch is read under its first caller's context. Its key holds the callers'
// row scope (velox.RowScopeFromContext: the scope values, a bypass, the shard), the
// DB and a list edge's MaxList, so callers that differ in them never share a
// read. Any other context value a Read policy or a Rewrite or Around hook
// reads, such as the viewer or a role, must be the same for every row of a
// field, or be returned by gqlgenx.WithBatchScope, whose value joins the key:
// a directive or a field middleware that gives an edge field's resolver another
// context for some parent rows needs it. Batches belong to one response: a
// subscription's event starts new ones, and outside a gqlgen field there is no
// batch, so a row read alone resolves its edges one row at a time.
//
// Outside any request, generated code reads only in a context
// veloxgql.Service(ctx, db) made, for a service's own list methods, calls
// between services and tests; there it reads every column, and each edge
// one row at a time. Elsewhere it fails with veloxgql.ErrNoSelectionSource.
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
// A page and an edge's rows read only the columns their selection needs:
// the selected fields', the key, every foreign key on the row and the
// columns of the page's own order. The foreign keys let an edge or an
// application's With work; the order's columns keep every cursor valid. A
// field the page is not ordered by is read only when it is selected. The columns of the other fields are left unread, so a row handed to
// an application's resolver or directive has them zero: a resolver or a
// directive that reads such a field must select it. A generated field with
// @goField(forceResolver: true) is still a generated field: its selection
// reads its own column only.
//
// The selection is the fields selected on the objects of the field's type:
// through edges and node for a connection field, and the field's own
// selection otherwise, with the fragments on the type and on every interface
// and union the type is a member of. Narrowing is turned off, and
// every column is read, when the selection cannot show what the objects'
// resolvers read:
//   - it holds a field the plugin did not write (an application's own, in
//     an extend type, whose resolver may read anything);
//   - a connection's selection holds a field other than edges, pageInfo,
//     totalCount and __typename, or an edge's one other than node, cursor
//     and __typename;
//   - outside a gqlgen server, which gives the schema, it holds a fragment
//     on an interface or a union.
//
// Narrowing never adds a column the application's query left out
// (velox.Query.Selects), and a New<Type>Connection whose query drops the key
// or a column an orderBy names is refused with an internal error. A
// mutation's returned row is not narrowed.
//
// # The cost limit
//
// The limits above each bound one argument. Connections nested in
// connections multiply, and aliases repeat a field, so a server also limits
// the cost of the whole request: veloxgql.Limits.MaxCost, 150000 by default.
// gqlgenx.Cost computes it, and gqlgenx.Extension refuses a request over it,
// before any resolver runs, with veloxgql.ErrTooCostly (the code
// COST_LIMIT_EXCEEDED, veloxgql.CodeTooCostly) and no statement. Under
// graphqlgo velox's cost is not applied: the server sets graphql-go's own,
// graphql.WithQueryCost.
//
// A cost has two parts, added. The rows: a field costs 1 plus its children's cost
// times its multiplier. A connection's edges multiply by its first or last, or
// by MaxPage when it has neither; any other list of objects by MaxList. The statements: a field that runs a statement adds 100,
// with no multiplier, as a batch reads all its parents in one statement. Those
// are a root field, a connection, its totalCount and any other field of an object
// or a list of objects, so first: 0 and an alias still count, and the default
// allows at most 1500 statements. An input object argument, as a where, adds its
// size at each use: one for each object and value in it, read from the variables,
// so one variable reused counts each time. In a where, a predicate no index serves
// adds 2000, 20 statements, at each use: <field>Contains, <field>HasPrefix,
// <field>HasSuffix, <field>EqualFold and <field>ContainsFold, a LIKE or a LOWER
// comparison that reads the whole table, and a non-empty
// has<Edge>With, a subquery for each row read. So 700 aliases of
// items(first: 1, where: {nameContains: "x"}), which took 12.5 s over 90000 rows,
// are refused, and the default allows about 70 such filters, while a page that
// filters on two or three costs about 10000. Cost cannot see which columns
// are indexed: a predicate on a column with no index scans the table too, and
// a server with large tables lowers MaxCost. On a mutation, an input object adds
// two statements, for the read of the row's key (UpdateQuery.One's) and the row
// it reads back; two more for a non-empty <edge>IDs, add<Edge>IDs or
// remove<Edge>IDs, five for an <edge>ID and three for a clear<Edge>: true, a page
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
// A server calls gqlgenx.Setup first, and serves what it returns:
//
//	srv := handler.New(graph.NewExecutableSchema(cfg))
//	h := gqlgenx.Setup(srv, db)
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
// allocates under 40 MB through gqlgenx.Setup, which also stops at MaxErrors errors and
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
// application's that does not call gqlgenx.ErrorPresenter makes the errors of fields
// that stop before they are dropped. Time and bytes are per request: many of them are for
// what is in front.
// docs/decisions/0027-graphql-limits.md has the measurements.
//
// # gqlgen.yml
//
// gqlgen binds a field only to a scalar model of the field's own Go type, so
// each scalar maps to a list of models, one for each Go type velox entities
// hold: gqlgenx's function marshalers (MarshalTime for time.Time, and so on)
// and gqlgen's own for the narrow integers. A field of another Go type of the
// application's, and a JSON field, binds to a generated <Field>GQL getter
// that returns a type of the list. A JSON field of []string, []int, []int64,
// []float64 or []bool is a list of String!, Int!, Float! or Boolean!, as entgql
// gives field.Strings, in the type and the inputs, and binds the Go slice; its
// filters, with WhereOps, are OpsJSONArray's: <field>Has, <field>HasSome,
// <field>HasEvery and <field>IsEmpty on its elements. Any other JSON field has
// none. With autobind to the generated package:
//
//	autobind:
//	  - <module>/gen
//	models:
//	  ID:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Int64ID
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
//	      - github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Float32
//	  Uint64:
//	    model:
//	      - github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Uint64
//	      - github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Uint
//	  Time:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Time
//	  UUID:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.UUID
//	  Bytes:
//	    model: github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Bytes
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
// String and Boolean keep gqlgen's own models; ID's is the key's, below.
// gqlgenx.Time, gqlgenx.UUID, gqlgenx.Bytes and gqlgenx.Uint64 resolve to
// the function marshalers of those names, which bind time.Time, uuid.UUID,
// []byte and uint64; gqlgenx.Uint and gqlgenx.Float32 to MarshalUint and
// MarshalFloat32. JSON, Cursor, OrderDirection and PageInfo are veloxgql's
// types, which every engine binds. gqlgen's narrow integers refuse an Int their Go type
// cannot hold (300 for an int8), and gqlgenx.ErrorPresenter shows the
// client gqlgen's text with BAD_USER_INPUT.
//
// # graphql-go and gqlc
//
// With engine=graphql-go the plugin writes for github.com/syssam/graphql-go,
// whose generator gqlc binds the SDL to the generated Go by autobind:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:engine=graphql-go ./veloxschema
//
// The Go is gqlgen's, with no graphql-go import: gqlc binds each field to the
// struct field or the method of its name, ignoring case, and a connection's
// arguments by their types. Every type velox.graphql declares but Query
// carries @goModel naming its Go type, so gqlc binds it whatever else
// autoBind lists: entities, enums, connections, edges, orders, where and
// mutation inputs to the generated package's types, PageInfo and
// OrderDirection to veloxgql's, and each scalar to the Go type entities
// hold (Time to time.Time, UUID to uuid.UUID, Bytes to []byte, Uint64 to
// uint64, Cursor and JSON to veloxgql's). An input's Go field whose GraphQL
// name is not the one graphql-go derives (authorID, which it derives as
// authorId) has a graphql tag naming it, and an optional list in a mutation
// input is a slice, nil when not given. Root fields are the application's:
// gqlc puts each on a Resolver, one line over the generated code, as in
// "Mutations".
//
// gqlc groups by SDL file and names an argument struct <Type><Field>Args,
// and a root field's <Field>Args. In one velox.graphql, Query.postTags and
// Post.tags then both declare PostTagsArgs, and the package does not build.
// sdlfiles=entity puts Query in package velox and Post in package post.
//
// gqlc's NewSchema registers a group whose Bindings takes no Resolver
// itself; passing it again makes graphql-go refuse its types as already
// bound. Pass only the groups whose Bindings takes a Resolver. velox-graphql
// writes no file of inputs alone, so under entities= every group but
// velox.graphql's holds a type (ADR 0130).
//
// A field gqlgen reads through a <Field>GQL getter, a JSON field or a field
// of the application's own Go type, carries @goField(forceResolver: true):
// gqlc puts it on the Resolver, gqlc -check lists it, and its method is one
// line. It returns the getter's value (obj.SettingsGQL()). Or it returns
// the field itself, when the resolver's return type holds the field's Go
// type: under apptypes=JSON that return type is the application's gqlc
// model of the scalar, such as any for JSON.
//
// gqlc does not read @goField(name:) yet, so velox-gen refuses, naming the
// schema, the field and the reason, a field or edge that graphql.FieldName
// renames, a non-null bytes field, whose getter writes an empty blob as ""
// where null is wrong, and a key not named ID. It names every refusal in
// one run, and writes nothing. A bytes field needs Nillable.
//
// graphql.Type binds a field to its struct field only when the
// application's gqlc model, and graphql.Scalar, of the type it names is
// exactly the field's Go type; else gqlc leaves the field to a Resolver
// stub, which gqlc -check lists, and graphql-go's NewSchema refuses an input
// field of it at start-up, naming the field.
//
// gqlc.yaml needs these keys:
//
//	schema: [veloxgen/*.graphql, graph/*.graphql]
//	autoBind: [<module>/veloxgen]
//	models: { ID: int64 }
//	modelDirective: { name: goModel, arg: model }
//	fieldDirective: { name: goField, forceResolverArg: forceResolver }
//	zeroForNullInputs: true   # where inputs' IsNil/NotNil and update inputs' clearX are bool
//	inputListPointers: true   # []*XOrder, []*XWhereInput, as the methods take them
//	inlineAccessors: true     # to-one edges answer from the batch
//	groups: { trimPrefix: velox_ }
//
// Without inputListPointers a multi-order edge's method no longer fits its
// arguments, and gqlc puts the field on a Resolver. The server adds
// graphqlgo.Scalars() to the schema and graphqlgo.With to the executor, with
// graphql-go's own limits:
//
//	s, err := graph.NewSchema(velox.Bindings(root), app.Bindings(root), graphqlgo.Scalars())
//	exec := graphql.NewExecutor(s, graphqlgo.With(db), graphql.WithMaxDepth(12))
//
// A nil []byte, a NULL bytes field, is null, as under gqlgen.
//
// Under apptypes=JSON+UUID+Uint64, velox.graphql declares none of those
// scalars, and the application does. Its gqlc models of UUID, Uint64 and
// Bytes must then be velox's Go types, uuid.UUID, uint64 and []byte, which
// graphqlgo.Scalars binds, or gqlc leaves the fields to Resolver stubs. A
// schema that declares only some of Time, UUID, Bytes and Uint64, such as
// one whose application declares UUID, binds them with
// graphqlgo.ScalarsOnly("Time", …): graphql-go's NewSchema refuses a
// binding of a scalar the schema does not declare. JSON fields are on the
// Resolver either way: with JSON modelled as any, each resolver returns
// the field (obj.Settings), and with it modelled as veloxgql.JSON, the
// getter's value.
//
// # IDs
//
// A GraphQL ID is the entity's key, in the key's own Go type: id on a type,
// an <edge>ID or <edge>IDs in an input, and the ID predicates of a where
// input are keys, which gqlgen decodes before any resolver runs. A resolver's
// id argument is the key, with no decoding of its own:
//
//	func (r *mutationResolver) UpdatePost(ctx context.Context, id int64, input veloxgen.UpdatePostInput) (*veloxgen.Post, error)
//
// gqlgen binds every ID argument to one Go type, the first model gqlgen.yml
// lists for ID, so every schema with a GraphQL type has keys of one Go type,
// and velox-graphql refuses a schema whose keys differ, naming two of them.
// A schema whose key has another Go type is left out of GraphQL with
// graphql.Skip(). The ID model is that Go type's:
//
//	key's Go type              ID: model:
//	int64 (velox's default)    github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Int64ID
//	uuid.UUID                  github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.UUID
//	string                     github.com/99designs/gqlgen/graphql.ID
//	int                        github.com/99designs/gqlgen/graphql.IntID
//	another, or a named type   the application's own marshalers
//
// gqlgenx.Int64ID writes the key as a JSON string, as GraphQL's ID is, and
// reads a string of decimal digits or an integer; anything else is
// BAD_USER_INPUT. gqlgenx.UUID writes and reads the UUID's text. gqlgen
// casts only named types whose base is string, so a key of a named integer
// type, as type UserID int64, needs the application's own model.
//
// An ID is not opaque and holds no type: the ID 7 of a Post and of a User
// are the same value, and an ID of one type given for another names the row
// of that key, if any. Without node=on there is no node or nodes query to look a row up by
// its ID alone. A cursor stays opaque, holds its type's name, and a
// connection refuses one of another type with veloxgql.ErrBadCursor. What a
// caller may read or change by an ID is the entity's policy's to decide, as
// for any key.
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
