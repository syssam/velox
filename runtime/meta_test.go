package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollectMeta_FieldColumns(t *testing.T) {
	meta := &CollectMeta{
		FieldColumns: map[string]string{
			"name":  "name",
			"email": "email_address",
		},
	}

	t.Run("existing key", func(t *testing.T) {
		col, ok := meta.FieldColumns["email"]
		assert.True(t, ok)
		assert.Equal(t, "email_address", col)
	})

	t.Run("missing key", func(t *testing.T) {
		_, ok := meta.FieldColumns["phone"]
		assert.False(t, ok)
	})
}

func TestCollectMeta_Edges(t *testing.T) {
	meta := &CollectMeta{
		Edges: map[string]EdgeMeta{
			"posts": {
				Name:   "posts",
				Target: "posts",
				Unique: false,
				Relay:  true,
			},
		},
	}

	t.Run("existing edge", func(t *testing.T) {
		desc, ok := meta.Edges["posts"]
		assert.True(t, ok)
		assert.Equal(t, "posts", desc.Name)
		assert.Equal(t, "posts", desc.Target)
		assert.False(t, desc.Unique)
		assert.True(t, desc.Relay)
	})

	t.Run("missing edge", func(t *testing.T) {
		_, ok := meta.Edges["comments"]
		assert.False(t, ok)
	})
}

func TestNodeResolvers_ReturnsCopy(t *testing.T) {
	RegisterNodeResolver("test_copy_a", NodeResolver{Type: "A"})
	RegisterNodeResolver("test_copy_b", NodeResolver{Type: "B"})
	defer func() {
		nodeMu.Lock()
		delete(nodeRegistry, "test_copy_a")
		delete(nodeRegistry, "test_copy_b")
		nodeMu.Unlock()
	}()

	resolvers := NodeResolvers()
	assert.Contains(t, resolvers, "test_copy_a")
	assert.Contains(t, resolvers, "test_copy_b")

	// Modify the copy and verify the original is unchanged.
	delete(resolvers, "test_copy_a")
	nodeMu.RLock()
	_, ok := nodeRegistry["test_copy_a"]
	nodeMu.RUnlock()
	assert.True(t, ok, "deleting from copy should not affect global registry")
}
