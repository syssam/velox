// Package graphqlgo serves the GraphQL code velox-graphql generates through
// github.com/syssam/graphql-go. Every function returns a graphql-go option
// or presenter, to compose with graphql-go's own. The runtime generated code
// calls is in package veloxgql and its package gqlgraph, which import no
// engine; this package is graphql-go's adapter to them, as package gqlgenx
// is gqlgen's. It imports no gqlgen.
//
// # The executor
//
// gqlc binds the SDL velox-graphql writes to the generated Go. The server
// adds Scalars to the schema, and With to the executor:
//
//	s, err := graph.NewSchema(post.Bindings(h), user.Bindings(h), graphqlgo.Scalars())
//	exec := graphql.NewExecutor(s,
//		graphqlgo.With(db),
//		graphql.WithMaxDepth(12),
//		graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(log)))
//
// With puts the DB, veloxgql.DefaultLimits() and graphql-go's selection
// source in each operation's context, with a new table of rows per
// operation and per event of a subscription. Without it, generated code
// fails with veloxgql.ErrNoDB and veloxgql.ErrNoSelectionSource, before any
// statement. A mutation runs in no transaction: a resolver that needs one
// opens it (db.Tx, velox.UseTx).
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
// The limits of a request are graphql-go's own options
// (graphql.WithMaxDepth, graphql.WithMaxComplexity, graphql.WithQueryCost,
// graphql.WithMaxResponseBytes, graphql.WithOperationTimeout and the rest),
// as ent and gqlgen leave them to the server. velox keeps only the bounds
// its own rules need, fixed at veloxgql.DefaultLimits(): a connection's page
// is at most MaxPage (100) rows, a list edge reads at most MaxList (1000),
// and an orderBy or a where filter is held to MaxOrderTerms and the filter
// limits. They are a backstop against unbounded reads, not configuration:
// an application that wants a smaller page refuses a larger first or last
// in an interceptor of its own.
//
// # Errors
//
// ErrorPresenter is the presenter to give the executor. It shows a client
// what veloxgql.Present gives, and graphql-go's own errors as graphql-go
// shows them. It logs each internal error, except a panic graphql-go
// recovered, which graphql-go has logged, and a canceled request, which
// it records at level debug.
//
// # Scalars
//
// Scalars binds Time, UUID, Bytes and Uint64 to time.Time, uuid.UUID,
// []byte and uint64, the Go types velox entities hold. A schema that does not
// declare all four, as under compat=veloxe or served in slices, binds the
// ones it declares with ScalarsOnly: graphql-go's NewSchema refuses a binding
// of a scalar the schema does not declare.
//
// Nothing in the package runs at program start.
package graphqlgo
