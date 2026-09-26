// Package gqlgentx runs each gqlgen mutation in a velox transaction:
//
//	srv := handler.New(generated.NewExecutableSchema(resolver))
//	srv.Use(gqlgentx.Transactioner{TxOpener: client})
//
// The mutation's resolvers run one at a time, since a transaction is one
// connection, and it commits only when the response carries no errors. It is
// the equivalent of Ent's entgql.Transactioner, and it is its own package so
// that a server not built on gqlgen links no gqlgen.
package gqlgentx
