// Package gqlgenx serves the GraphQL code velox-graphql generates through
// gqlgen: the handler extension that puts velox's state in each operation's
// context, the error presenter, the handler that bounds a request's body,
// and the function marshalers gqlgen.yml names. The runtime generated code
// calls is in package veloxgql and its package gqlgraph, which import no
// engine; this package is gqlgen's adapter to them.
//
// # The server
//
// A server needs no setup (decision 0173). The package registers a reader
// of gqlgen's context when it is linked, as gqlgen's generated code links
// it through the models gqlgen.yml binds here (Int64ID, Time and the other
// scalars); a server that binds none imports the package blank. The reader
// tells generated code the field each resolver resolves, what it selects,
// and the batch of its root field, so a server whose Resolver holds the DB
// pages, projects and batches with nothing installed.
//
// Setup is opt-in, for a server open to clients:
//
//	srv := handler.New(graph.NewExecutableSchema(cfg))
//	h := gqlgenx.Setup(srv, db)
//	srv.AddTransport(transport.POST{})
//
// It changes no statement a request sends. It uses NewExtension(db, opts...), first of the server's extensions, and
// ErrorPresenter(nil), and returns a handler that bounds a request's body and
// a websocket's messages. The options are this package's: WithLimits,
// WithTxOptions, WithTxFilter and WithBatchScope.
//
// # The Extension
//
// The Extension puts the DB, the limits and gqlgen's selection source in
// each operation's context, where veloxgql.DBFromContext and
// veloxgql.LimitsFromContext read them and generated code finds the field it
// resolves. It runs a mutation operation in one transaction: it commits when
// the response has no errors, and rolls back otherwise or on a panic.
// WithTxOptions sets the transaction's options, and it refuses
// velox.TxRetrySerializable, since gqlgen makes the response once.
// WithTxFilter leaves a mutation out of a transaction: its
// statements are not atomic and its field resolvers are not serialized, but
// it keeps read-your-writes in a velox session. Extension.NewContext puts the
// same state in a context for generated code called outside a gqlgen server,
// in a job or a test: there no field is resolving, and generated code reads
// alone.
//
// Before gqlgen parses a request, the Extension refuses one whose document or
// variables are over the limits, and before any resolver one whose
// Cost is over MaxCost. It holds a response to MaxResponseBytes and
// MaxErrors, and a query or a mutation to Timeout.
//
// # Selections
//
// gqlgen's selection source reads the field of a resolver's context
// (graphql.GetFieldContext): generated code reads only the columns the
// field's selection needs, and the first edge field resolved on any row of a
// page reads that edge for every row of it. A field's occurrence is its
// *ast.Field with the selections gqlgen merged under its response key, so
// two aliases batch apart.
//
// # Errors
//
// ErrorPresenter is the presenter to give the server
// (srv.SetErrorPresenter(gqlgenx.ErrorPresenter(nil))). It shows a client
// what veloxgql.Present gives, and gqlgen's text for an integer that an
// Int8…Uint32 argument cannot hold (veloxgql.CodeBadUserInput); it logs
// every internal error. An error that is only the cancellation of the
// request, the client gone, is not logged as an internal error. gqlgen's
// default presenter shows every error's text, and no code.
//
// # Scalars
//
// The function marshalers MarshalTime, MarshalUUID, MarshalBytes,
// MarshalUint64, MarshalUint and MarshalFloat32, with their Unmarshal
// functions, bind veloxgql's scalars to the Go types velox entities hold
// (time.Time, uuid.UUID, []byte, uint64, uint, float32): gqlgen prefers a
// model's function marshalers to its type. MarshalInt64ID and
// UnmarshalInt64ID are the ID scalar of an int64 key. The gqlgen.yml models
// that map each scalar are in the documentation of package
// github.com/syssam/velox/contrib/graphql.
//
// Nothing in the package runs at program start.
package gqlgenx
