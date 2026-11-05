// Package veloxgql is the runtime the GraphQL code velox-graphql generates
// binds to, through gqlgen: global IDs, connection cursors, where filters,
// the scalars, and the limits on what a client may ask for.
//
// What only generated code calls is in package gqlimpl, whose names are
// stable by GeneratedCodeIsVersion12 and not by semantic versioning. Where a
// name below belongs to gqlimpl it is written gqlimpl.Name.
//
// # IDs
//
// Every GraphQL ID is opaque and global: base64url without padding of
// "<Type>:<key>", where Type is the GraphQL type name and key the primary
// key's canonical text (EncodeID). In Go an ID is a string, gqlgen's built-in
// ID. Generated code decodes one with DecodeID[K](id, "<Type>"), which
// returns the typed key, and fails with ErrWrongIDType for an ID of another
// type, before anything is looked up. IDType reads an ID's type, for a
// lookup that does not know it, as Node's. Every Node entity implements
// Noder.
//
// # The DB
//
// Generated code runs its queries on the DB that DBFromContext reads from the
// request's context. The Extension (NewExtension) is the gqlgen handler
// extension that puts it there, with the limits, for every operation, and
// runs a mutation operation in one transaction: it commits when the response
// has no errors, and rolls back otherwise or on a panic. Extension.NewContext
// puts the same in a context for generated code called outside a gqlgen
// server, in a job or a test.
//
// WithTxOptions sets the options of a mutation's transaction
// (velox.TxIsolation, velox.TxReadOnly, velox.TxTimeout). It refuses
// velox.TxRetrySerializable, since gqlgen makes the response once and the
// operation cannot run again. WithTxFilter decides for each mutation
// operation, from its context and definition, whether it runs in a
// transaction; a nil filter means every mutation does. Use it for mutations
// that only call other services, which would hold a pooled connection in a
// transaction for no statement. One the filter leaves out runs on the DB: its
// statements are not atomic and its field resolvers are not serialized, but
// it keeps read-your-writes, since it runs in a velox session
// (velox.WithSession) and a DB with replicas reads its own writes from the
// primary.
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
// A gqlimpl.Connection (gqlimpl.NewConnection) holds a connection's query,
// its paging arguments (gqlimpl.Args) and its order (gqlimpl.Order, of the
// columns of the type's gqlimpl.Conn), and runs nothing until asked: Page
// runs the page query once, whoever calls it first, and TotalCount the COUNT.
// It is one non-generic runtime for every type, on velox's untyped
// TableQuery, and the generated code only names the columns. The order always
// ends with the key. Paging is keyset, through velox's Query.After: last and
// before reverse every term and reverse the rows read, and one row more than
// the page sets hasNextPage or hasPreviousPage.
//
// # Batches
//
// The rows one statement reads are a batch, and the first edge field gqlgen
// resolves on any of them reads that edge for all of them in one statement,
// so a nested query sends one statement per edge per level. Generated pages,
// node and nodes make their rows a batch; Batch does so for the rows an
// application's own resolver returns. A batch is per response, per field and
// per edge, so each alias of an edge is its own batch; a call joins it only
// from the edge's own field and with its first caller's arguments, and
// reads alone otherwise. It is read under its first caller's context, and
// keyed by the callers' velox.RowScope, DB and limits, so callers with
// different scope values, bypasses or shards never share a read; any other
// context value a policy or a hook reads must be the same for every row of a
// field, or be returned by WithBatchScope. Nothing waits for other
// resolvers, which a mutation could not allow. Outside a gqlgen field Batch
// does nothing.
//
// # Where
//
// A where input's generated method Pred turns it into a velox.Pred, and
// PredicateGQL into the same condition untyped: a gqlimpl.Filter
// (gqlimpl.NewFilter) on the entity's velox.Table, one helper call for each
// field of the input, and Filter.Pred, the And of what the helpers added.
// The helpers take columns and relations by index, and none is generic over
// the entity, so they are compiled once for every where input. Each helper
// adds nothing for a field the input does not set, and the first error stops
// the filter:
//   - gqlimpl.Not, And and Or nest inputs one level deeper, and HasWith
//     through an edge (velox.Table.Has); an empty and, or or has…With list
//     adds nothing, and an input that sets nothing holds for every row;
//   - gqlimpl.Value and List add a predicate on a column (EQ, In,
//     Contains…, velox.Table.Pred), converting the input's values to the
//     column's Go type where they differ (velox.Table.Convert);
//   - gqlimpl.ID and IDList decode IDs with DecodeID, so an ID of another
//     type fails with ErrWrongIDType;
//   - gqlimpl.Is adds IsNil or NotNil, or its negation, and Has has<Edge>;
//   - gqlimpl.FKID, FKIDList and FKIs are ID, IDList and Is of a foreign
//     key, which with a Read policy on the edge's target also hold the
//     edge's EXISTS.
//
// The limits are LimitsFromContext(ctx)'s, read at the top of the where
// argument. A nesting deeper than MaxFilterDepth fails with ErrFilterTooDeep. A
// list of more than MaxFilterWidth entries, of inputs or of In or NotIn values,
// fails with ErrFilterTooWide, and a where argument over its budget of
// MaxFilterNodes inputs, predicates and values in all with ErrFilterTooLarge.
// None of it runs a query, so a refused filter fails before any statement is
// built. An error names the field that held its cause, through the nesting:
// "veloxgql: OwnerWhereInput.hasEventsWith[0]: EventWhereInput.ownerID: Owner
// ID: ID of another type".
//
// The predicates are SQL's, with its three-valued logic, as entgql's are:
// on a nullable column, NEQ and NotIn leave out the rows whose column is
// NULL, and so does not, so a not is not the complement of its input (use
// IsNil to take the NULL rows in). Contains, HasPrefix and HasSuffix take
// the client's text literally, % and _ included, and whether they ignore
// case follows the database, as velox's handles do: PostgreSQL's does not
// ignore it; MySQL's default collations and SQLite, for ASCII, do.
//
// # Scalars
//
// Time, UUID, Bytes, JSON, Uint64 and Cursor are the GraphQL scalars of
// those names, and OrderDirection the enum: each implements gqlgen's
// graphql.Marshaler and graphql.Unmarshaler. PageInfo is the object type,
// which gqlgen binds by its fields. The function marshalers MarshalTime,
// MarshalUUID, MarshalBytes, MarshalUint64, MarshalUint and MarshalFloat32,
// with their Unmarshal functions, bind the scalars to the Go types velox
// entities hold (time.Time, uuid.UUID, []byte, uint64, uint, float32): gqlgen
// prefers a model's function marshalers to its type. The gqlgen.yml models
// that map each scalar are in the documentation of package
// github.com/syssam/velox/contrib/graphql.
//
// Uint64 is a decimal string on the wire both ways, since a JSON number loses
// precision above 2^53 in many clients.
//
// # Errors
//
// Every error veloxgql, gqlimpl and generated code return for a client's
// input (an ID, a cursor, paging arguments, a filter, a scalar or an enum)
// has the code CodeBadUserInput: a method Extensions() map[string]any,
// which gives {"code": "BAD_USER_INPUT"}, in the shape of gqlgen's former
// ExtendedError. Its text starts "veloxgql: ", and errors.Is finds its
// sentinel. gqlimpl.InputErrorf makes one, for generated code.
//
// ErrorPresenter is the presenter to give the server
// (srv.SetErrorPresenter(veloxgql.ErrorPresenter(nil))). It shows a client a
// coded error's message and code; a fixed message for velox's errors without
// a scope value (CodeUnauthenticated), denied by a policy (CodeForbidden), of a
// validator or a violated constraint (CodeBadUserInput); gqlgen's text for an
// integer that an Int8…Uint32 argument cannot hold (CodeBadUserInput); and
// "internal error" (CodeInternal) for anything else, which it logs: a
// driver's error, a deadline of the application's, a list edge over MaxList, ErrNoDB. An error
// that is only the cancellation of the request, the client gone, is not
// logged as an internal error. So no driver text, SQL, scope, entity or constraint name reaches a
// client. gqlgen's default presenter shows every error's text, and no code.
//
// # Limits
//
// Limits caps what a client may ask for: MaxPage, MaxList, MaxFilterDepth,
// MaxFilterWidth, MaxFilterNodes, MaxOrderTerms, MaxCost, MaxIntrospectionCost,
// MaxTokens, MaxFields, MaxFragments, MaxDepth, MaxNameBytes, MaxLiteralBytes,
// MaxVariableNodes, MaxErrors, MaxBodyBytes, MaxResponseBytes and Timeout. An Extension has
// its own (WithLimits; a zero field is the default), which it puts in each
// operation's context, and generated code reads them with LimitsFromContext,
// which gives DefaultLimits() in a context without them. The Extension refuses
// a negative limit. MaxList is how many rows a list edge reads, and more fail
// with velox.ErrTooManyRows; graphql.MaxList(n) lowers it for one edge, and its cost.
//
// Every one of them but MaxCost bounds one argument or one edge, not a whole
// request: connections nested in connections multiply (MaxPage rows, and a
// query for each, at every level), and aliases repeat a field as often as the
// client writes it. MaxCost bounds the request. Cost computes what an
// operation costs, as gqlgen's complexity recipe does: a field costs 1 plus
// its children's cost times its multiplier, which is first or last for a
// connection's edges, the number of ids for nodes and MaxList for another list
// of objects. On top of that, with no multiplier, a field that runs a statement
// adds 100, so first: 0 and aliases count, and an input object adds its size at
// each use. A where predicate no index serves adds 2000 at each use: a
// <field>Contains, <field>HasPrefix or <field>HasSuffix, which reads the whole
// table, and a has<Edge>With, whose subquery runs for each row. Every alias and every branch of a fragment counts, except a
// selection that @skip(if: true) or @include(if: false) leaves out, which
// costs nothing, its condition a literal or a variable's value (a missing
// variable's default, when it has one; a condition that is not a boolean
// counts); the value above it is read all the same, and costs as if it selected
// __typename. Introspection costs one for each field of its response, counted over
// the schema, and answers to MaxIntrospectionCost. The Extension refuses a
// request over either with ErrTooCostly (CodeTooCostly, COST_LIMIT_EXCEEDED),
// before any resolver, so before any statement. The default MaxCost, 150000,
// lets two levels of full connection pages through (about 20000) and not
// three, and at most 1500 statements. A list edge costs MaxList for each parent:
// under the defaults, a: likes { id } b: likes { id } under posts(first: 100) cost
// 200602 and are refused; raise MaxCost, or lower MaxList, to let them through.
//
// MaxTokens, MaxFields, MaxFragments, MaxDepth, MaxNameBytes, MaxLiteralBytes and
// MaxVariableNodes bound the document and its variables, before gqlgen parses or validates
// it, which the Extension does on its own. MaxBodyBytes bounds the request's body and a
// websocket's message, in the handler Setup returns, the one call a server needs.
//
// Cost cannot see the size of a value, or how long a statement takes. MaxResponseBytes
// holds a response's strings, bytes and JSON, counted as its fields resolve; MaxErrors its
// errors; and Timeout a query or a mutation's time. Over any of them the request stops,
// with one error (ErrResponseTooLarge, ErrTooManyErrors, ErrTimeout) and data null, and a
// mutation rolls back.
//
// Nothing in the package runs at program start.
package veloxgql

// GeneratedCodeIsVersion12 is referenced by every Go file velox-graphql writes,
// so that generated code and this module fail to compile together when they
// disagree. For generated code. It is replaced by GeneratedCodeIsVersion13 when
// generated code must change.
const GeneratedCodeIsVersion12 = true
