// Package veloxgql is the runtime the GraphQL code velox-graphql generates
// binds to, through gqlgen: global IDs, connection cursors, the scalars, and
// the limits on what a client may ask for.
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
// request's context, where WithDB puts it.
//
// # Cursors
//
// A cursor is base64url without padding of the versioned JSON document
// {"v":1,"t":"<Type>","o":[{"f":"<FIELD>","d":"ASC","v":<value>}…],"k":<key>}
// (EncodeCursor, DecodeCursor): the GraphQL type, the order terms with the
// row's values, and the primary key. Integers keep every digit and times
// their nanoseconds and zone offset. Cursor.Check refuses a cursor taken on
// another type or under another order with ErrBadCursor, and CursorValue
// converts a decoded value back to its column's Go type.
//
// # Connections
//
// A Connection (NewConnection) holds a connection's query, its paging
// arguments (Args) and its order (OrderTerm, made by Term), and runs nothing
// until asked: Page runs the page query once, whoever calls it first, and
// TotalCount the COUNT. The order always ends with the key. Paging is
// keyset, through velox's Query.After: last and before reverse every term
// and reverse the rows read, and one row more than the page sets
// hasNextPage or hasPreviousPage. Paginate is NewConnection and Page in one
// call. LoadedConnection pages rows a With loaded, and runs no query.
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
// MaxPage, MaxFilterDepth, MaxFilterWidth and MaxOrderTerms cap what a
// client may ask for.
// They are variables an application may set at start-up, before it serves a
// request.
//
// Nothing in the package runs at program start.
package veloxgql
