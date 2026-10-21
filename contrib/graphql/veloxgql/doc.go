// Package veloxgql is the runtime the GraphQL code velox-graphql generates
// binds to, through gqlgen: global IDs, connection cursors, where filters,
// the scalars, and the limits on what a client may ask for.
//
// What only generated code calls is in package gqlimpl, whose names are
// stable by GeneratedCodeIsVersion1 and not by semantic versioning. Where a
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
// Generated code runs its queries on the DB that DBFrom reads from the
// request's context. The Extension (NewExtension) is the gqlgen handler
// extension that puts it there, with the limits, for every operation, and
// runs a mutation operation in one transaction: it commits when the response
// has no errors, and rolls back otherwise or on a panic. Extension.Context
// puts the same in a context for generated code called outside a gqlgen
// server, in a job or a test.
//
// WithTxOptions sets the options of a mutation's transaction (velox.TxIsolation,
// velox.TxReadOnly, velox.TxTimeout). It refuses velox.TxRetrySerializable,
// since gqlgen makes the response once and the operation cannot run again.
// WithTxFilter decides for each mutation operation, from its context and
// definition, whether it runs in a transaction; a nil filter means every
// mutation does, and one the filter leaves out runs on the DB with its field
// resolvers not serialized.
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
// its paging arguments (gqlimpl.Args) and its order (gqlimpl.OrderTerm, made
// by gqlimpl.Term), and runs nothing until asked: Page runs the page query
// once, whoever calls it first, and TotalCount the COUNT. The order always
// ends with the key. Paging is keyset, through velox's Query.After: last and
// before reverse every term and reverse the rows read, and one row more than
// the page sets hasNextPage or hasPreviousPage. gqlimpl.LoadedConnection
// pages rows a With loaded, and runs no query.
//
// # Where
//
// A where input's generated method Pred turns it into a velox.Pred: a
// gqlimpl.Filter (gqlimpl.NewFilter), one helper call for each field of the
// input, and Filter.Pred, the And of what the helpers added. Each helper
// adds nothing for a field the input does not set, and the first error stops
// the filter:
//   - gqlimpl.Not, And and Or nest inputs one level deeper, and HasWith
//     through an edge (the relation's HasWhere); an empty and, or or
//     has…With list adds nothing, and an input that sets nothing holds for
//     every row;
//   - gqlimpl.Value and List add a predicate of the column's handle (EQ, In,
//     Contains…), and ValueAs and ListAs convert the input's values to the
//     column's Go type first, with gqlimpl.AsString, AsInt, AsFloat, AsBool or
//     AsUUID;
//   - gqlimpl.ID, IDList, IDAs and IDListAs decode IDs with DecodeID, so an
//     ID of
//     another type fails with ErrWrongIDType;
//   - gqlimpl.Is adds a Boolean predicate, or its negation: IsNil, NotNil and
//     has<Edge>.
//
// The limits are LimitsFrom(ctx)'s, read at the top of the where argument.
// A nesting deeper than MaxFilterDepth fails with ErrFilterTooDeep.
// A list of more than MaxFilterWidth entries, of inputs or of In or NotIn
// values, fails with ErrFilterTooWide, and a where argument over its budget
// of MaxFilterNodes inputs, predicates and values in all with
// ErrFilterTooLarge. None of it runs a query, so a refused filter fails before any
// statement is built. An error names the field that held its cause, through
// the nesting: "veloxgql: OwnerWhereInput.hasEventsWith[0]:
// EventWhereInput.ownerID: Owner ID: ID of another type".
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
// github.com/syssam/velox2/contrib/graphql.
//
// Uint64 is a decimal string on the wire both ways, since a JSON number loses
// precision above 2^53 in many clients.
//
// # Limits
//
// Limits caps what a client may ask for: MaxPage, MaxList, MaxFilterDepth,
// MaxFilterWidth, MaxFilterNodes and MaxOrderTerms. An Extension has its
// own (WithLimits; a zero field is the default), which it puts in each
// operation's context, and generated code reads them with LimitsFrom, which
// gives DefaultLimits() in a context without them. The Extension refuses a
// negative limit. MaxList is how many rows a list edge reads, and more fail
// with velox.ErrTooManyRows.
//
// Every one of them bounds one argument or one edge, not a whole request:
// connections nested in connections multiply (MaxPage rows, and a query for
// each, at every level), and aliases repeat a field as often as the client
// writes it. A server needs a limit on the cost of the request as well, such
// as gqlgen's extension.FixedComplexityLimit.
//
// Nothing in the package runs at program start.
package veloxgql

// For generated code. GeneratedCodeIsVersion1 is referenced by every Go file
// velox-graphql writes, so that generated code and this module fail to
// compile together when they disagree. It is replaced by
// GeneratedCodeIsVersion2 when generated code must change.
const GeneratedCodeIsVersion1 = true
