// Package graph contains GraphQL resolvers.
package graph

import (
	"example.com/shop/velox"
)

// Resolver is the root resolver for all GraphQL operations.
type Resolver struct {
	client *velox.Client
}

// NewResolver creates a new resolver with the given client.
func NewResolver(client *velox.Client) *Resolver {
	return &Resolver{client: client}
}
