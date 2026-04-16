package gqlgen

import "example.com/fullgql/velox"

// Resolver is the root resolver for the GraphQL server.
type Resolver struct {
	Client *velox.Client
}
