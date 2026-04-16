package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

func TestHasFederationEntities_WithKey(t *testing.T) {
	graph := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent", Target: t.TempDir()},
		Nodes: []*entgen.Type{
			{
				Name: "User",
				ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
				Annotations: map[string]any{
					AnnotationName: Annotation{
						Directives: []Directive{
							{Name: "key", Args: map[string]any{"fields": `"id"`}},
						},
					},
				},
			},
		},
	}
	gen := &Generator{graph: graph, config: Config{}}
	assert.True(t, gen.hasFederationEntities())
}

func TestHasFederationEntities_NoFederation(t *testing.T) {
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
	assert.False(t, gen.hasFederationEntities())
}

func TestHasFederationEntities_ExplicitFlag(t *testing.T) {
	graph := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent", Target: t.TempDir()},
		Nodes:  []*entgen.Type{},
	}
	gen := &Generator{graph: graph, config: Config{Federation: true}}
	assert.True(t, gen.hasFederationEntities())
}

func TestWithFederation(t *testing.T) {
	ext, err := NewExtension(WithFederation())
	require.NoError(t, err)
	assert.True(t, ext.config.Federation)
}

func TestFederationLinkDirective(t *testing.T) {
	link := federationLinkDirective()
	assert.Contains(t, link, "extend schema @link")
	assert.Contains(t, link, "federation/v2.0")
	assert.Contains(t, link, `"@key"`)
	assert.Contains(t, link, `"@external"`)
}
