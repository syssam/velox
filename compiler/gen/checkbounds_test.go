package gen

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// A field's validator bounds reach its column only with FeatureCheckBounds:
// the constraint is opt-in, because adding it fails on a table whose rows
// already violate it.
func TestTablesCarryBoundsOnlyWithFeature(t *testing.T) {
	item := func() *load.Schema {
		return &load.Schema{Name: "Item", Fields: []*load.Field{
			{Name: "stock", Info: &field.TypeInfo{Type: field.TypeInt}, Bounds: []field.Bound{{Op: ">=", Value: "0"}}},
			{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
		}}
	}
	bounds := func(features ...Feature) map[string][]field.Bound {
		g, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"], Features: features}, item())
		require.NoError(t, err)
		ts, err := g.Tables()
		require.NoError(t, err)
		out := map[string][]field.Bound{}
		for _, c := range ts[0].Columns {
			out[c.Name] = c.Bounds
		}
		return out
	}
	require.Empty(t, bounds()["stock"])
	got := bounds(FeatureCheckBounds)
	require.Equal(t, []field.Bound{{Op: ">=", Value: "0"}}, got["stock"])
	require.Empty(t, got["name"])
}
