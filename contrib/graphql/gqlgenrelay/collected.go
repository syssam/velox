package gqlgenrelay

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
)

// CollectedField returns the collected field for the given path in the GraphQL context.
func CollectedField(ctx context.Context, path ...string) *graphql.CollectedField {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil {
		return nil
	}
	field := fc.Field
	oc := graphql.GetOperationContext(ctx)
walk:
	for _, name := range path {
		for _, f := range graphql.CollectFields(oc, field.Selections, nil) {
			if f.Alias == name {
				field = f
				continue walk
			}
		}
		return nil
	}
	return &field
}

// HasCollectedField reports whether the given field path exists in the GraphQL context.
func HasCollectedField(ctx context.Context, path ...string) bool {
	if graphql.GetFieldContext(ctx) == nil {
		return true
	}
	return CollectedField(ctx, path...) != nil
}
