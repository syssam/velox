// Package veloxgql is the runtime the GraphQL code velox-graphql generates
// binds to, whichever GraphQL engine serves it: connection cursors, where
// filters, the scalars, the errors and their codes, and the limits on what a
// client may ask for. It imports no engine.
//
// What only generated code calls is in package gqlgraph, whose names are
// stable by GeneratedCodeIsVersion31 and not by semantic versioning. Where a
// name below belongs to gqlgraph it is written gqlgraph.Name. An engine is
// served through its adapter: package gqlgenx for gqlgen, package graphqlgo
// for github.com/syssam/graphql-go. Generated code imports this package and
// gqlgraph alone, so a program that only queries links no engine.
//
// # Adapters
//
// A server needs no setup (decision 0173). Each adapter registers, when
// its package is linked, a reader of its engine's context. A gqlgen server
// links gqlgenx through the models gqlgen.yml binds to it (ID, Time and
// velox's other scalars), and a graphql-go one links graphqlgo through
// graphqlgo.Scalars; one that binds none imports it blank. The reader tells
// generated code the field each resolver resolves, what it selects, and
// the batch its edge joins, one for each root field under gqlgen and one
// for each operation under graphql-go. So a server whose resolvers hold
// the DB pages, projects and batches with nothing installed, under the
// DB's limits (WithLimits).
//
// Nothing needs installing (decision 0175). gqlgenx has opt-in pieces for a
// server open to clients, each one feature: gqlgenx.Transactioner runs a
// mutation in one transaction, gqlgenx.RequestLimit, gqlgenx.CostLimit and
// gqlgenx.ResponseLimit bound a request, gqlgenx.MaxBodyBytes its body, and
// gqlgenx.ErrorPresenter shows a client only what is safe. graphqlgo needs
// none: graphql-go's own options bound a request.
//
// Outside a request a service pages the same way, as a job or a test does:
// generated code there reads every column, and each edge alone.
//
//	conn, err := db.Query[veloxgen.Post]().Paginate(ctx, nil, &first, nil, nil)
//
// # IDs
//
// A GraphQL ID is the entity's primary key, in the key's own Go type, as in
// entgql. gqlgen binds the field id to the entity's key field, and an ID
// argument or input field to the key's Go type, so a resolver takes the key
// as it is. gqlgen binds every ID argument to the first model gqlgen.yml
// lists for ID, so the keys of a schema's GraphQL types are of one Go type.
// gqlgenx.MarshalInt64ID and gqlgenx.UnmarshalInt64ID are the ID scalar of an
// int64 key: a JSON string of the decimal digits out, a string or an integer
// in. A UUID key binds to gqlgenx.MarshalUUID and a string key to gqlgen's
// graphql.ID.
//
// An ID names a row of its type only where its type is known: the key 7 is
// both Post 7 and User 7. By default there is no Node interface and no node query. A
// client may send any key, so a policy or a scope decides what it may read
// or change, not the ID.
//
// # The DB
//
// Generated code runs a connection's queries on the DB of the query it was
// given, and an edge's on the DB that read its row (decision 0171): it
// reads no DB from the context. A resolver uses the DB its Resolver holds;
// under gqlgenx.Transactioner a statement on it in a mutation runs in the
// mutation's transaction.
//
// # Cursors
//
// A cursor is base64url without padding of the versioned JSON document
// {"v":1,"t":"<Type>","o":[{"f":"<FIELD>","d":"ASC","v":<value>}…],"k":<key>}
// (EncodeCursor, DecodeCursor): the GraphQL type, the order terms with the
// row's values, and the primary key. Integers keep every digit and times
// their nanoseconds and zone offset. A connection refuses a cursor taken on
// another type or under another order with ErrBadCursor, and converts a
// decoded value back to its column's Go type.
//
// # Connections
//
// A gqlgraph.Connection (gqlgraph.Paginate) holds a connection's query,
// its paging arguments (gqlgraph.Args) and its order (gqlgraph.Order, of the
// columns of the type's gqlgraph.Type), and runs nothing until asked: Page
// runs the page query once, whoever calls it first, and TotalCount the COUNT.
// It is one non-generic runtime for every type, on velox's untyped
// TableQuery, and the generated code only names the columns. The order always
// ends with the key. Paging is keyset, through velox's Query.After: last and
// before reverse every term and reverse the rows read, and one row more than
// the page sets hasNextPage or hasPreviousPage. With both after and before the
// connection reads the window between them, the second cursor as Before's
// condition (ADR 0187).
//
// # Batches
//
// The rows one statement reads are a batch, and the first edge field gqlgen
// resolves on any of them reads that edge for all of them in one statement,
// so a nested query sends one statement per edge per level. Generated pages
// make their rows a batch; Batch does so for the rows an
// application's own resolver returns. A batch is per response, per field and
// per edge, so each alias of an edge is its own batch; a call joins it only
// from the edge's own field and with its first caller's arguments, and
// reads alone otherwise. It is read under its first caller's context, and
// keyed by the callers' velox.RowScope, DB and limits, so callers with
// different scope values, bypasses or shards never share a read; any other
// context value a policy or a hook reads must be the same for every row of a
// field. Nothing waits for other
// resolvers, which a mutation could not allow. Outside a field Batch does
// nothing.
//
// # Where
//
// A where input's generated method Pred turns it into a velox.Pred, through
// an unexported method that builds the same condition untyped: a
// gqlgraph.Filter (gqlgraph.NewFilter) on the entity's untyped.Table, one
// helper call for each not, and, or and edge of the input and for each
// field's predicates, and Filter.Pred, the And of what the helpers added.
// The helpers take columns and relations by index, and a nested input's
// condition as a method expression, and none is generic over the entity, so
// they are compiled once for every where input. Each helper adds nothing
// for a field the input does not set, and the first error stops the filter:
//   - gqlgraph.Not, And and Or nest inputs one level deeper, and HasWith
//     through an edge (untyped.Table.Has); an empty and or has…With list
//     adds nothing, an empty or matches no row, as an empty In does, and an
//     input that sets nothing holds for every row, so not: {} matches none;
//   - gqlgraph.Field adds the predicates a gqlgraph.Ops sets on a column (EQ,
//     In, Contains…, IsNil, untyped.Table.Pred), converting the input's values
//     to the column's Go type where they differ (untyped.Table.Convert);
//   - gqlgraph.IDField is Field of the key's predicates, on keys gqlgen
//     decoded, and Has adds has<Edge>;
//   - gqlgraph.EdgeIDField is IDField of a foreign key, with its IsNil and
//     NotNil: each the column's alone, as SQL and entgql have them.
//
// The limits are those of the DB of the connection the where argument
// filters (LimitsOf), or DefaultLimits() for a Pred called outside one. A nesting deeper than MaxFilterDepth fails with ErrFilterTooDeep. A
// list of more than MaxFilterWidth entries, of inputs or of In or NotIn values,
// fails with ErrFilterTooWide, and a where argument over its budget of
// MaxFilterNodes inputs, predicates and values in all with ErrFilterTooLarge.
// Under filters=lenient no width cap applies, as in entgql, and the budget
// is 10 times MaxFilterNodes. The budget is never over 30000, whatever the limits: each
// predicate and each In value binds a parameter, and the statement must
// stay under every dialect's limit on them (SQLite's 32766).
// None of it runs a query, so a refused filter fails before any statement is
// built. An error names the field that held its cause, through the nesting:
// "veloxgql: OwnerWhereInput.hasEventsWith[0]: EventWhereInput.titleIn: filter
// list is too long".
//
// A filter kept as a JSON document, such as a rule of an access policy,
// reads into a where input with DecodeWhere, by the input's GraphQL field
// names, and lowers with the input's Pred, where the limits apply. An
// unknown key or a value of the wrong type is an error, never a filter
// that sets less.
//
// The predicates are SQL's, with its three-valued logic, as entgql's are:
// on a nullable column, NEQ and NotIn leave out the rows whose column is
// NULL, and so does not, so a not is not the complement of its input (use
// IsNil to take the NULL rows in). Contains, HasPrefix, HasSuffix and
// ContainsFold take the client's text literally, % and _ included. The first
// three follow the database for case, as velox's handles do: PostgreSQL's
// does not ignore it; MySQL's default collations and SQLite, for ASCII, do.
// EqualFold and ContainsFold compare LOWER of both sides, so they ignore case
// on every dialect (SQLite's LOWER folds ASCII letters only).
//
// # Scalars
//
// Time, UUID, Bytes, JSON, Uint64 and Cursor are the GraphQL scalars of
// those names, and OrderDirection the enum: each has the methods MarshalGQL
// and UnmarshalGQL, which gqlgen and graphql-go call (Cursor has a context
// pair too, which gqlgen prefers). PageInfo is the object type, which the
// engine binds by its fields. gqlgenx's function marshalers bind the scalars
// to the Go types velox entities hold (time.Time, uuid.UUID, []byte, uint64,
// uint, float32). The gqlgen.yml models that map each scalar are in the
// documentation of package github.com/syssam/velox/contrib/graphql.
//
// Uint64 is a decimal string on the wire both ways, since a JSON number loses
// precision above 2^53 in many clients.
//
// # Errors
//
// Every error veloxgql, gqlgraph and generated code return for a client's
// input (an ID, a cursor, paging arguments, a filter, a scalar or an enum)
// has the code CodeBadUserInput: a method Extensions() map[string]any,
// which gives {"code": "BAD_USER_INPUT"}, in the shape of gqlgen's former
// ExtendedError. Its text starts "veloxgql: ", and errors.Is finds its
// sentinel. gqlgraph.InputErrorf makes one, for generated code.
//
// Present is the code and the message a client may see for an error, which
// an adapter's presenter (gqlgenx.ErrorPresenter) shows: a coded error's
// message and code; a fixed message for velox's errors without a scope value
// (CodeUnauthenticated), denied by a policy (CodeForbidden), of a validator or
// a violated constraint (CodeBadUserInput); and "internal error"
// (CodeInternal) for anything else, which the presenter logs: a driver's
// error, a deadline of the application's, a list edge over MaxList, or
// a row with no DB to read its edges on. So no driver text, SQL, scope, entity or constraint
// name reaches a client.
//
// # Limits
//
// Limits caps what a client may ask for: MaxPage, MaxList, MaxFilterDepth,
// MaxFilterWidth, MaxFilterNodes, MaxOrderTerms, MaxCost, MaxIntrospectionCost,
// MaxTokens, MaxFields, MaxFragments, MaxDepth, MaxNameBytes, MaxLiteralBytes,
// MaxVariableNodes, MaxErrors, MaxBodyBytes, MaxResponseBytes and Timeout. They
// are the DB's: WithLimits gives them (a zero field is the default, a negative
// one panics), and LimitsOf reads them, DefaultLimits() for a DB given none.
// A connection reads those of its query's DB, and an edge those of the DB
// that read its row, a transaction's included. MaxList is how many rows a list edge reads, and more fail
// with velox.ErrTooManyRows; graphql.MaxList(n) lowers it for one edge, and its cost.
//
// Every one of them but MaxCost bounds one argument or one edge, not a whole
// request: connections nested in connections multiply (MaxPage rows, and a
// query for each, at every level), and aliases repeat a field as often as the
// client writes it. MaxCost bounds the request under gqlgenx.CostLimit; under
// graphqlgo it is not applied, and the server sets graphql.WithQueryCost.
// gqlgenx.Cost computes what an operation costs, as gqlgen's complexity recipe does: a field costs 1 plus
// its children's cost times its multiplier, which is first or last for a
// connection's edges and MaxList for another list of objects. On top of that, with no multiplier, a field that runs a statement
// adds 100, so first: 0 and aliases count, and an input object adds its size at
// each use. A where predicate no index serves adds 2000 at each use: a
// <field>Contains, <field>HasPrefix, <field>HasSuffix, <field>EqualFold or
// <field>ContainsFold, which reads the whole table, and a has<Edge>With, whose subquery runs for each row. Every alias and every branch of a fragment counts, except a
// selection that @skip(if: true) or @include(if: false) leaves out, which
// costs nothing, its condition a literal or a variable's value (a missing
// variable's default, when it has one; a condition that is not a boolean
// counts); the value above it is read all the same, and costs as if it selected
// __typename. Introspection costs one for each field of its response, counted over
// the schema, and answers to MaxIntrospectionCost. gqlgenx.CostLimit refuses a
// request over either with ErrTooCostly (CodeTooCostly, COST_LIMIT_EXCEEDED),
// before any resolver, so before any statement. The default MaxCost, 150000,
// lets two levels of full connection pages through (about 20000) and not
// three, and at most 1500 statements. A list edge costs MaxList for each parent:
// under the defaults, a: likes { id } b: likes { id } under posts(first: 100) cost
// 200602 and are refused; raise MaxCost, or lower MaxList, to let them through.
//
// MaxTokens, MaxFields, MaxFragments, MaxDepth, MaxNameBytes, MaxLiteralBytes and
// MaxVariableNodes bound the document and its variables, and MaxBodyBytes their bytes,
// before gqlgen parses or validates it, under gqlgenx.RequestLimit. gqlgenx.MaxBodyBytes,
// with a limit of its own, bounds the HTTP body and a websocket's message.
//
// gqlgenx.Cost cannot see the size of a value, or how long a statement takes. MaxResponseBytes
// holds a response's strings, bytes and JSON, counted as its fields resolve; MaxErrors its
// errors; and Timeout a query or a mutation's time, under gqlgenx.ResponseLimit. Over any
// of them the request stops, with one error (ErrResponseTooLarge, ErrTooManyErrors,
// ErrTimeout) and data null, and a mutation under gqlgenx.Transactioner rolls back.
//
// Nothing in the package runs at program start.
package veloxgql

// GeneratedCodeIsVersion31 is referenced by every Go file velox-graphql writes,
// so that generated code and this module fail to compile together when they
// disagree. For generated code. It is replaced by the next number when generated
// code must change.
const GeneratedCodeIsVersion31 = true
