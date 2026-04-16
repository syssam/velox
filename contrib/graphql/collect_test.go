package graphql

import (
	"context"
	"testing"

	gqlgenGraphql "github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/runtime"
)

func init() {
	// Register field collector for tests (normally done by NewExtension).
	RegisterFieldCollector()
}

func TestCollectFields_NonGraphQLContext(t *testing.T) {
	ctx := context.Background()
	q := runtime.NewQueryBase(nil, "users", []string{"id", "name"}, "id", nil, "User")
	err := runtime.CollectFields(ctx, q, nil, nil)
	assert.NoError(t, err)
	// No fields should have been collected since there's no GraphQL field context.
	assert.Empty(t, q.Ctx.Fields)
}

func TestCollectFields_NoOperationContext(t *testing.T) {
	// A plain context.Background() has no GraphQL field context,
	// so CollectFields should return nil immediately.
	ctx := context.Background()
	q := runtime.NewQueryBase(nil, "posts", []string{"id", "title"}, "id", nil, "Post")
	fields := map[string]string{"title": "title"}
	edges := map[string]runtime.EdgeMeta{
		"author": {Name: "author", Target: "users", Unique: true},
	}
	err := runtime.CollectFields(ctx, q, fields, edges)
	assert.NoError(t, err)
	assert.Empty(t, q.Ctx.Fields)
	assert.Empty(t, q.Edges)
}

// newGQLContext creates a context with gqlgen field and operation contexts
// for the given selection set.
func newGQLContext(t *testing.T, selections ast.SelectionSet) context.Context {
	t.Helper()
	collected := gqlgenGraphql.CollectedField{
		Field: &ast.Field{
			Name:         "user",
			Alias:        "user",
			SelectionSet: selections,
		},
		Selections: selections,
	}
	fc := &gqlgenGraphql.FieldContext{
		Field: collected,
	}
	opCtx := &gqlgenGraphql.OperationContext{
		Variables: map[string]any{},
	}
	ctx := gqlgenGraphql.WithFieldContext(context.Background(), fc)
	ctx = gqlgenGraphql.WithOperationContext(ctx, opCtx)
	return ctx
}

func TestCollectFields_ScalarFieldProjection(t *testing.T) {
	selections := ast.SelectionSet{
		&ast.Field{Name: "name", Alias: "name"},
		&ast.Field{Name: "email", Alias: "email"},
	}
	ctx := newGQLContext(t, selections)

	fields := map[string]string{
		"name":  "name",
		"email": "email",
	}
	q := runtime.NewQueryBase(nil, "users", []string{"id", "name", "email", "age"}, "id", nil, "User")

	err := runtime.CollectFields(ctx, q, fields, nil)
	require.NoError(t, err)

	// Should project only id + name + email (not age).
	assert.Contains(t, q.Ctx.Fields, "id")
	assert.Contains(t, q.Ctx.Fields, "name")
	assert.Contains(t, q.Ctx.Fields, "email")
	assert.NotContains(t, q.Ctx.Fields, "age")
}

func TestCollectFields_IDAndTypenameSkipped(t *testing.T) {
	selections := ast.SelectionSet{
		&ast.Field{Name: "id", Alias: "id"},
		&ast.Field{Name: "__typename", Alias: "__typename"},
		&ast.Field{Name: "name", Alias: "name"},
	}
	ctx := newGQLContext(t, selections)

	fields := map[string]string{"name": "name"}
	q := runtime.NewQueryBase(nil, "users", []string{"id", "name"}, "id", nil, "User")

	err := runtime.CollectFields(ctx, q, fields, nil)
	require.NoError(t, err)

	// Should have id (always included) + name. No duplicates from "id" field.
	assert.Contains(t, q.Ctx.Fields, "id")
	assert.Contains(t, q.Ctx.Fields, "name")
}

func TestCollectFields_EdgeLoadScheduling(t *testing.T) {
	selections := ast.SelectionSet{
		&ast.Field{Name: "name", Alias: "name"},
		&ast.Field{Name: "posts", Alias: "posts"},
	}
	ctx := newGQLContext(t, selections)

	fields := map[string]string{"name": "name"}
	edges := map[string]runtime.EdgeMeta{
		"posts": {
			Name:      "posts",
			Target:    "posts",
			FKColumns: []string{"user_posts"},
		},
	}
	q := runtime.NewQueryBase(nil, "users", []string{"id", "name"}, "id", nil, "User")

	err := runtime.CollectFields(ctx, q, fields, edges)
	require.NoError(t, err)

	// Edge should be scheduled.
	require.Len(t, q.Edges, 1)
	assert.Equal(t, "posts", q.Edges[0].Name)

	// FK columns should be added to field projection.
	assert.Contains(t, q.Ctx.Fields, "user_posts")
}

func TestCollectFields_UnknownFieldFallsBackToSelectAll(t *testing.T) {
	selections := ast.SelectionSet{
		&ast.Field{Name: "name", Alias: "name"},
		&ast.Field{Name: "customResolver", Alias: "customResolver"},
	}
	ctx := newGQLContext(t, selections)

	fields := map[string]string{"name": "name"}
	q := runtime.NewQueryBase(nil, "users", []string{"id", "name", "email", "age"}, "id", nil, "User")

	err := runtime.CollectFields(ctx, q, fields, nil)
	require.NoError(t, err)

	// Unknown field causes fallback to SELECT * — no column projection applied.
	assert.Empty(t, q.Ctx.Fields, "unknown fields should prevent column projection")
}

func TestCollectFields_RelayEdgePagination(t *testing.T) {
	// Simulate a Relay connection edge with "first" argument.
	postField := &ast.Field{
		Name:  "posts",
		Alias: "posts",
		Arguments: ast.ArgumentList{
			{
				Name: "first",
				Value: &ast.Value{
					Kind: ast.IntValue,
					Raw:  "10",
				},
			},
		},
		Definition: &ast.FieldDefinition{
			Name: "posts",
			Arguments: ast.ArgumentDefinitionList{
				{
					Name: "first",
					Type: ast.NamedType("Int", nil),
				},
			},
		},
	}
	selections := ast.SelectionSet{postField}
	ctx := newGQLContext(t, selections)

	fields := map[string]string{}
	edges := map[string]runtime.EdgeMeta{
		"posts": {
			Name:      "posts",
			Target:    "posts",
			Relay:     true,
			FKColumns: []string{"user_posts"},
		},
	}
	q := runtime.NewQueryBase(nil, "users", []string{"id"}, "id", nil, "User")

	err := runtime.CollectFields(ctx, q, fields, edges)
	require.NoError(t, err)

	require.Len(t, q.Edges, 1)
	assert.Equal(t, "posts", q.Edges[0].Name)

	// The Relay edge should have a Limit load option (first + 1 for hasNextPage).
	cfg := &runtime.LoadConfig{}
	for _, opt := range q.Edges[0].Opts {
		opt(cfg)
	}
	require.NotNil(t, cfg.Limit, "Relay edge should have Limit from 'first' arg")
	assert.Equal(t, 11, *cfg.Limit, "should be first+1 for hasNextPage probe")
}

func TestCollectFields_MultipleEdgesAndScalars(t *testing.T) {
	selections := ast.SelectionSet{
		&ast.Field{Name: "name", Alias: "name"},
		&ast.Field{Name: "email", Alias: "email"},
		&ast.Field{Name: "posts", Alias: "posts"},
		&ast.Field{Name: "groups", Alias: "groups"},
	}
	ctx := newGQLContext(t, selections)

	fields := map[string]string{
		"name":  "name",
		"email": "email",
	}
	edges := map[string]runtime.EdgeMeta{
		"posts":  {Name: "posts", Target: "posts", FKColumns: []string{"user_posts"}},
		"groups": {Name: "groups", Target: "groups", FKColumns: []string{"user_groups"}},
	}
	q := runtime.NewQueryBase(nil, "users", []string{"id", "name", "email"}, "id", nil, "User")

	err := runtime.CollectFields(ctx, q, fields, edges)
	require.NoError(t, err)

	// Both edges scheduled.
	require.Len(t, q.Edges, 2)
	edgeNames := make(map[string]bool)
	for _, e := range q.Edges {
		edgeNames[e.Name] = true
	}
	assert.True(t, edgeNames["posts"])
	assert.True(t, edgeNames["groups"])

	// Scalars + FK columns projected.
	assert.Contains(t, q.Ctx.Fields, "name")
	assert.Contains(t, q.Ctx.Fields, "email")
	assert.Contains(t, q.Ctx.Fields, "user_posts")
	assert.Contains(t, q.Ctx.Fields, "user_groups")
}

func TestGqlToInt(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		want   int
		wantOk bool
	}{
		{"int", 10, 10, true},
		{"int64", int64(42), 42, true},
		{"int32", int32(7), 7, true},
		{"float64 whole number", float64(10), 10, true},
		{"float64 with fraction returns false", 3.14, 0, false},
		{"string returns false", "10", 0, false},
		{"nil returns false", nil, 0, false},
		{"negative int", -5, -5, true},
		{"float64 negative whole", float64(-3), -3, true},
		{"float64 zero", float64(0), 0, true},
		// int64 overflow check (only relevant on 32-bit platforms, but logic is exercised)
		{"int64 max int", int64(maxInt), maxInt, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := gqlToInt(tt.input)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCollectFields_RelayLastArg(t *testing.T) {
	lastField := &ast.Field{
		Name:  "posts",
		Alias: "posts",
		Arguments: ast.ArgumentList{
			{
				Name: "last",
				Value: &ast.Value{
					Kind: ast.IntValue,
					Raw:  "5",
				},
			},
		},
		Definition: &ast.FieldDefinition{
			Name: "posts",
			Arguments: ast.ArgumentDefinitionList{
				{
					Name: "last",
					Type: ast.NamedType("Int", nil),
				},
			},
		},
	}
	selections := ast.SelectionSet{lastField}
	ctx := newGQLContext(t, selections)

	edges := map[string]runtime.EdgeMeta{
		"posts": {Name: "posts", Target: "posts", Relay: true, FKColumns: []string{"user_posts"}},
	}
	q := runtime.NewQueryBase(nil, "users", []string{"id"}, "id", nil, "User")

	err := runtime.CollectFields(ctx, q, map[string]string{}, edges)
	require.NoError(t, err)

	require.Len(t, q.Edges, 1)
	cfg := &runtime.LoadConfig{}
	for _, opt := range q.Edges[0].Opts {
		opt(cfg)
	}
	require.NotNil(t, cfg.Limit, "Relay edge should have Limit from 'last' arg")
	assert.Equal(t, 6, *cfg.Limit, "should be last+1 for hasPreviousPage probe")
}

func TestCollectFields_EmptySelections(t *testing.T) {
	selections := ast.SelectionSet{}
	ctx := newGQLContext(t, selections)

	fields := map[string]string{"name": "name"}
	q := runtime.NewQueryBase(nil, "users", []string{"id", "name"}, "id", nil, "User")

	err := runtime.CollectFields(ctx, q, fields, nil)
	require.NoError(t, err)

	// With empty selections, only ID is added but since len(selectedFields) == 1
	// and no unknownSeen, the single ID field should be projected.
	// However, the ID is always included but selectedFields only adds it.
	assert.Empty(t, q.Edges)
}
