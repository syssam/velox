// Package gqlshapes is the generated project the GraphQL layer's less common
// shapes are tested on: what the schema needs Go for beyond a where input, an
// order input and a method per edge.
//
//	schema/   the shapes
//	types/    the Go type of a JSON field, deliberately outside schema/
//	velox/    generated output, with the layer
//	graphql/  the generated schema files
//
// Regenerate, from the repository root:
//
//	go generate ./tests/orm/gqlshapes/
package gqlshapes

//go:generate go run ../../../cmd/velox-gen -schema ./schema -target ./velox -package github.com/syssam/velox/tests/orm/gqlshapes/velox -graphql ./graphql/velox.graphql
