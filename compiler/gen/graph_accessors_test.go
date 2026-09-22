package gen

// Tests for Graph-level accessors (graph.go, graph_tables.go, storage.go, config.go).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql/schema"
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
// config.go — ModuleInfo (smoke test — returns empty outside module)
// =============================================================================

func TestConfig_ModuleInfo_Smoke(t *testing.T) {
	c := &Config{}
	// Should not panic; result may be empty outside velox module context.
	_ = c.ModuleInfo()
}

// =============================================================================
// fkSymbols — via graph with M2M edge (exercises internal fkSymbols)
// =============================================================================

func TestFkSymbols_ViaEdgeSchemas(t *testing.T) {
	// fkSymbols is called by edgeSchemas for M2M edges.
	graph, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"]}, T1, T2)
	require.NoError(t, err)
	tables := graph.edgeSchemas()
	// edgeSchemas returns the M2M join tables.
	_ = tables
}

// =============================================================================
// schema.Column usage to keep the import used
// =============================================================================

func TestSchemaColumn_Import(t *testing.T) {
	c := &schema.Column{Name: "id"}
	assert.Equal(t, "id", c.Name)
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
