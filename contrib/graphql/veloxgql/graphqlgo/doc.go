// Package graphqlgo serves the GraphQL code velox-graphql generates through
// github.com/syssam/graphql-go: the executor options that put velox's state
// in each operation's context, the scalars, the limits and the error
// presenter. The runtime generated code calls is in package veloxgql and
// its package gqlgraph, which import no engine; this package is
// graphql-go's adapter to them, as package gqlgenx is gqlgen's. It imports
// no gqlgen.
//
// # The executor
//
// gqlc binds the SDL velox-graphql writes to the generated Go; the server
// adds the scalars to the schema and With's options to the executor:
//
//	s, err := graph.NewSchema(post.Bindings(h), user.Bindings(h), graphqlgo.Scalars())
//	exec := graphql.NewExecutor(s,
//		append(graphqlgo.With(db, veloxgql.WithLimits(l)), graphqlgo.Limits(l)...),
//		graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(nil)))
//
// With puts the DB, the limits and graphql-go's selection source in each
// operation's context, where veloxgql.DB and veloxgql.LimitsFromContext read
// them and generated code finds the field it resolves, with a new table of
// rows per operation and per event of a subscription. Its options are
// veloxgql's (veloxgql.WithLimits, veloxgql.WithTxOptions,
// veloxgql.WithTxFilter, veloxgql.WithBatchScope), which every adapter
// takes, and two of this package's, both off by default: WithMutationTx
// runs a mutation in one transaction, and WithCost refuses a request over
// veloxgql.Cost. Without With, generated code fails with veloxgql.ErrNoDB
// and veloxgql.ErrNoSelectionSource, before any statement.
//
// # Selections
//
// graphql-go's selection source reads the field of a resolver's context
// (graphql.FieldFrom), which graphql-go gives a Resolve or ResolveArgs
// function: generated code reads only the columns the field's selection
// needs, and the first edge field resolved on any row of a page reads that
// edge for every row of it, in one statement. A field's occurrence is its
// parent type, its name and its sub-selection (graphql.Selection), so two
// aliases batch apart. graphql-go resolves an object's Resolve fields
// concurrently; a resolver that waits on another's batch is parked in the
// operation's wave coordinator, so it holds back no loader of the
// application's. A Field accessor gets no context, and generated code
// never reads through one.
//
// # Limits
//
// The limits of a request as a whole are graphql-go's: Limits maps
// veloxgql's MaxDepth, MaxTokens, MaxErrors, MaxResponseBytes and Timeout
// onto graphql-go's options. gqlgraph holds each connection, list and
// filter to MaxPage, MaxList, MaxOrderTerms and the filter limits, from the
// state. graphql.WithQueryCost is graphql-go's cost model; WithCost adds
// velox's, which knows velox's connections and statements.
//
// # Errors
//
// ErrorPresenter is the presenter to give the executor. It shows a client
// what veloxgql.Present gives, and graphql-go's own errors as graphql-go
// shows them; it logs every internal error.
//
// # Scalars
//
// Scalars binds Time, UUID, Bytes and Uint64 to time.Time, uuid.UUID,
// []byte and uint64, the Go types velox entities hold.
//
// Nothing in the package runs at program start.
package graphqlgo
