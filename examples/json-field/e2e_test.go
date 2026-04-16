package jsonfield_test

import (
	"context"
	"testing"

	"example.com/json-field/schema"
	"example.com/json-field/velox"
	_ "example.com/json-field/velox/query" // registers query factories

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// TestJSONField stores and retrieves three flavors of JSON column:
//   - a typed Go struct (Specs)
//   - an untyped map[string]any (metadata)
//   - a slice ([]string tags)
//
// Shows how velox round-trips each shape transparently through
// encoding/json, so Go code stays fully typed.
func TestJSONField(t *testing.T) {
	ctx := context.Background()
	client, err := velox.Open("sqlite", "file:json.db?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Close()) }()

	require.NoError(t, client.Schema.Create(ctx))

	// Create with typed struct + untyped map + slice.
	p := client.Product.Create().
		SetName("Widget").
		SetSpecs(schema.Specs{
			Weight:   1.25,
			Color:    "graphite",
			Features: []string{"waterproof", "wireless"},
		}).
		SetMetadata(map[string]any{
			"sku":      "WGT-001",
			"warranty": float64(24),
		}).
		SetTags([]string{"featured", "new"}).
		SaveX(ctx)

	// Reload from DB and verify every JSON shape survived the round trip.
	loaded := client.Product.GetX(ctx, p.ID)

	assert.Equal(t, "Widget", loaded.Name)

	// Typed struct — Go sees the concrete type, not map[string]any.
	assert.Equal(t, 1.25, loaded.Specs.Weight)
	assert.Equal(t, "graphite", loaded.Specs.Color)
	assert.Equal(t, []string{"waterproof", "wireless"}, loaded.Specs.Features)

	// Untyped map — flexible storage.
	require.NotNil(t, loaded.Metadata)
	assert.Equal(t, "WGT-001", loaded.Metadata["sku"])

	// Slice — stored as JSON array.
	assert.Equal(t, []string{"featured", "new"}, loaded.Tags)
}
