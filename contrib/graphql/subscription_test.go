package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

func TestSubscriptionAnnotation(t *testing.T) {
	ann := Subscription(
		SubscriptionField("onUserCreated", "User!"),
	)
	require.Len(t, ann.Subscriptions, 1)
	assert.Equal(t, "onUserCreated", ann.Subscriptions[0].Name)
	assert.Equal(t, "User!", ann.Subscriptions[0].ReturnType)
}

func TestSubscriptionAnnotation_WithDescription(t *testing.T) {
	ann := Subscription(
		SubscriptionField("onPostPublished", "Post!").WithDescription("Fires when a post is published"),
	)
	assert.Equal(t, "Fires when a post is published", ann.Subscriptions[0].Description)
}

func TestSubscriptionAnnotation_WithArgs(t *testing.T) {
	ann := Subscription(
		SubscriptionField("onUserUpdated", "User!").WithArgs("id: ID!"),
	)
	assert.Equal(t, "id: ID!", ann.Subscriptions[0].Args)
}

func TestSubscriptionAnnotation_Multiple(t *testing.T) {
	ann := Subscription(
		SubscriptionField("onUserCreated", "User!"),
		SubscriptionField("onUserDeleted", "User!"),
	)
	require.Len(t, ann.Subscriptions, 2)
}

func TestSubscriptionAnnotation_MergeDeduplicates(t *testing.T) {
	a := Subscription(SubscriptionField("onUserCreated", "User!"))
	b := Subscription(SubscriptionField("onUserDeleted", "User!"))
	merged := a.Merge(b).(Annotation)
	assert.Len(t, merged.Subscriptions, 2)
}

func TestCollectSubscriptions(t *testing.T) {
	graph := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent", Target: t.TempDir()},
		Nodes: []*entgen.Type{
			{
				Name: "User",
				ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
				Annotations: map[string]any{
					AnnotationName: Annotation{
						Subscriptions: []SubscriptionConfig{
							{Name: "onUserCreated", ReturnType: "User!"},
						},
					},
				},
			},
			{
				Name: "Post",
				ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
				Annotations: map[string]any{
					AnnotationName: Annotation{
						Subscriptions: []SubscriptionConfig{
							{Name: "onPostPublished", ReturnType: "Post!"},
						},
					},
				},
			},
		},
	}

	gen := &Generator{graph: graph, config: Config{}}
	subs := gen.collectSubscriptions()
	require.Len(t, subs, 2)
}

func TestCollectSubscriptions_Empty(t *testing.T) {
	graph := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent", Target: t.TempDir()},
		Nodes: []*entgen.Type{
			{
				Name: "User",
				ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
			},
		},
	}

	gen := &Generator{graph: graph, config: Config{}}
	subs := gen.collectSubscriptions()
	assert.Empty(t, subs)
}

func TestGenSubscriptionType(t *testing.T) {
	graph := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent", Target: t.TempDir()},
		Nodes: []*entgen.Type{
			{
				Name: "User",
				ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
				Annotations: map[string]any{
					AnnotationName: Annotation{
						Subscriptions: []SubscriptionConfig{
							{Name: "onUserCreated", ReturnType: "User!", Description: "Fires on user creation"},
						},
					},
				},
			},
		},
	}

	gen := &Generator{graph: graph, config: Config{}}
	sdl := gen.genSubscriptionType()
	assert.Contains(t, sdl, "type Subscription {")
	assert.Contains(t, sdl, "onUserCreated: User!")
	assert.Contains(t, sdl, "Fires on user creation")
}

func TestGenSubscriptionType_WithArgs(t *testing.T) {
	graph := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent", Target: t.TempDir()},
		Nodes: []*entgen.Type{
			{
				Name: "User",
				ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
				Annotations: map[string]any{
					AnnotationName: Annotation{
						Subscriptions: []SubscriptionConfig{
							{Name: "onUserUpdated", ReturnType: "User!", Args: "id: ID!"},
						},
					},
				},
			},
		},
	}

	gen := &Generator{graph: graph, config: Config{}}
	sdl := gen.genSubscriptionType()
	assert.Contains(t, sdl, "onUserUpdated(id: ID!): User!")
}

func TestGenSubscriptionType_Empty(t *testing.T) {
	graph := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent", Target: t.TempDir()},
		Nodes: []*entgen.Type{
			{
				Name: "User",
				ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
			},
		},
	}

	gen := &Generator{graph: graph, config: Config{}}
	sdl := gen.genSubscriptionType()
	assert.Empty(t, sdl)
}
