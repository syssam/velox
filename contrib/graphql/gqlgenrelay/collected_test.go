package gqlgenrelay

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestHasCollectedField_NilContext(t *testing.T) {
	// When there is no GraphQL field context, HasCollectedField returns true
	// (assumes field is present when outside GraphQL context).
	ctx := context.Background()
	assert.True(t, HasCollectedField(ctx, "edges"))
	assert.True(t, HasCollectedField(ctx, "edges", "node"))
}

func TestCollectedField_NilContext(t *testing.T) {
	// CollectedField returns nil when there is no GraphQL field context.
	ctx := context.Background()
	assert.Nil(t, CollectedField(ctx, "edges"))
}

func TestCollectedField_WithOperationContext(t *testing.T) {
	// Build a minimal OperationContext with field selections
	// so CollectFields can resolve them.
	nodeField := &ast.Field{Alias: "node", Name: "node"}
	edgesField := &ast.Field{
		Alias:        "edges",
		Name:         "edges",
		SelectionSet: ast.SelectionSet{nodeField},
	}

	fc := &graphql.FieldContext{
		Field: graphql.CollectedField{
			Field: &ast.Field{
				Alias: "users",
				Name:  "users",
			},
			Selections: ast.SelectionSet{edgesField},
		},
	}
	oc := &graphql.OperationContext{}
	ctx := graphql.WithOperationContext(context.Background(), oc)
	ctx = graphql.WithFieldContext(ctx, fc)

	// CollectFields on OperationContext with field selections.
	// The selections contain edgesField, so the walk should match "edges".
	result := CollectedField(ctx, "edges")
	assert.NotNil(t, result, "expected non-nil when edges field is in selections")
	assert.Equal(t, "edges", result.Alias)

	// Walk nested path: edges -> node
	nodeResult := CollectedField(ctx, "edges", "node")
	assert.NotNil(t, nodeResult, "expected non-nil for nested node field")
	assert.Equal(t, "node", nodeResult.Alias)

	// Non-existent path returns nil
	missingResult := CollectedField(ctx, "nonexistent")
	assert.Nil(t, missingResult, "expected nil for non-existent field")
}

func TestHasCollectedField_WithFieldContext(t *testing.T) {
	edgesField := &ast.Field{
		Alias: "edges",
		Name:  "edges",
	}
	fc := &graphql.FieldContext{
		Field: graphql.CollectedField{
			Field:      &ast.Field{Alias: "users", Name: "users"},
			Selections: ast.SelectionSet{edgesField},
		},
	}
	oc := &graphql.OperationContext{}
	ctx := graphql.WithOperationContext(context.Background(), oc)
	ctx = graphql.WithFieldContext(ctx, fc)

	assert.True(t, HasCollectedField(ctx, "edges"))
	assert.False(t, HasCollectedField(ctx, "nonexistent"))
}
