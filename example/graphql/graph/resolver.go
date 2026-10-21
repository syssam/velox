// Package graph is the GraphQL server gqlgen generates from gen/velox.graphqls
// (generated.go, models_gen.go), and its resolvers: Resolver here, and the
// root query's in schema.resolvers.go, each one call into the gen package.
// Every other field binds to gen's methods, so it needs no resolver.
package graph

// Resolver is the root resolver. It holds nothing: the generated code reads
// the *velox.DB from the request context, where veloxgql.NewExtension puts
// it.
type Resolver struct{}
