package gen

// Tests for Graph-level accessors (graph.go, graph_tables.go, storage.go, config.go).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/dialect/sql/schema"
	"github.com/syssam/velox/schema/edge"
)

// =============================================================================
// graph.go — SupportMigrate, SchemaSnapshot
// =============================================================================

func TestGraph_SupportMigrate(t *testing.T) {
	// No storage → false.
	g := &Graph{Config: &Config{}}
	assert.False(t, g.SupportMigrate())

	// Storage with Migrate mode.
	g2 := &Graph{Config: &Config{Storage: &Storage{SchemaMode: Migrate}}}
	assert.True(t, g2.SupportMigrate())

	// Storage without Migrate mode.
	g3 := &Graph{Config: &Config{Storage: &Storage{SchemaMode: Unique}}}
	assert.False(t, g3.SupportMigrate())
}

func TestGraph_SchemaSnapshot(t *testing.T) {
	graph, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"]}, T1, T2)
	require.NoError(t, err)

	snap, err := graph.SchemaSnapshot()
	require.NoError(t, err)
	assert.NotEmpty(t, snap)
	// Should be a JSON-quoted string containing the schema nodes.
	assert.Contains(t, snap, "T1")
	assert.Contains(t, snap, "T2")
}

// =============================================================================
// graph_tables.go — fkSymbols
// =============================================================================

func TestFkSymbols(t *testing.T) {
	c1, c2 := &schema.Column{Name: "user_id"}, &schema.Column{Name: "group_id"}
	rel := Relation{Type: M2M, Table: "user_groups"}

	// No storage key: "<join table>_<column>" for both sides.
	s1, s2 := fkSymbols(&Edge{Name: "groups", Rel: rel}, c1, c2)
	assert.Equal(t, "user_groups_user_id", s1)
	assert.Equal(t, "user_groups_group_id", s2)

	// One storage-key symbol overrides only the first constraint.
	one := &Edge{Name: "groups", Rel: rel, def: &load.Edge{StorageKey: &edge.StorageKey{Symbols: []string{"fk_user"}}}}
	s1, s2 = fkSymbols(one, c1, c2)
	assert.Equal(t, "fk_user", s1)
	assert.Equal(t, "user_groups_group_id", s2)

	// Two symbols override both.
	two := &Edge{Name: "groups", Rel: rel, def: &load.Edge{StorageKey: &edge.StorageKey{Symbols: []string{"fk_user", "fk_group"}}}}
	s1, s2 = fkSymbols(two, c1, c2)
	assert.Equal(t, "fk_user", s1)
	assert.Equal(t, "fk_group", s2)
}

// =============================================================================
// storage.go — TableSchemas error path
// =============================================================================

func TestGraph_TableSchemas_MissingAnnotation(t *testing.T) {
	graph, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"]}, T1, T2)
	require.NoError(t, err)
	_, err = graph.TableSchemas()
	assert.Error(t, err)
}
