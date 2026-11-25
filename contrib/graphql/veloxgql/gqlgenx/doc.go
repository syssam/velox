// Package gqlgenx serves the GraphQL code velox-graphql generates through
// gqlgen: the reader of gqlgen's context that generated code needs, the
// function marshalers gqlgen.yml names, and opt-in pieces for a server open
// to clients: a mutation transaction, request, cost and response limits, a
// body limit and an error presenter. The runtime generated code calls is in
// package veloxgql and its package gqlgraph, which import no engine; this
// package is gqlgen's adapter to them.
//
// # The server
//
// A server needs no setup (decisions 0173 and 0175). The package registers
// a reader of gqlgen's context when it is linked, as gqlgen's generated code
// links it through the models gqlgen.yml binds here (Int64ID, Time and the
// other scalars); a server that binds none imports the package blank. The
// reader tells generated code the field each resolver resolves, what it
// selects, and the batch of its root field, so a server whose Resolver
// holds the DB pages, projects and batches with nothing installed:
//
//	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{DB: db}}))
//	srv.AddTransport(transport.POST{})
//
// The limits generated code holds to are the DB's (veloxgql.WithLimits).
//
// # Opt-in pieces
//
// Each piece does one thing, and none is needed to page, batch or read:
//
//   - Transactioner{DB: db} runs each mutation in one transaction, as
//     entgql's Transactioner does, and its field resolvers one at a time.
//   - RequestLimit(db) refuses a document or variables over the DB's limits
//     before gqlgen parses them.
//   - CostLimit(db) refuses a request whose Cost is over MaxCost before any
//     resolver runs.
//   - ResponseLimit(db) holds a response to MaxResponseBytes, MaxErrors and
//     Timeout.
//   - MaxBodyBytes(n) is the HTTP middleware that bounds a request's body
//     and a websocket's messages.
//   - ErrorPresenter is the presenter to give the server.
//
// A server open to clients uses them with gqlgen's own knobs:
//
//	srv.Use(gqlgenx.Transactioner{DB: db})
//	srv.Use(gqlgenx.RequestLimit(db))
//	srv.Use(gqlgenx.CostLimit(db))
//	srv.Use(gqlgenx.ResponseLimit(db))
//	srv.SetErrorPresenter(gqlgenx.ErrorPresenter(nil))
//	srv.SetDisableSuggestion(true)
//	srv.SetValidationRulesFn(gqlgenx.QuietValidation(db))
//	http.Handle("/graphql", gqlgenx.MaxBodyBytes(1<<20)(srv))
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
// what veloxgql.Present gives, gqlgen's text for an integer that an
// Int8…Uint32 argument cannot hold (veloxgql.CodeBadUserInput), and
// "introspection is disabled" where it is; it logs
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
