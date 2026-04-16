package tree_test

import (
	"context"
	"testing"

	"example.com/tree/velox"
	"example.com/tree/velox/category"
	_ "example.com/tree/velox/query" // registers query factories

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// TestTree walks through a tiny category hierarchy:
//
//	Electronics
//	├── Phones
//	│   ├── iPhone
//	│   └── Android
//	└── Laptops
//
// It demonstrates the three things users want when modelling a tree:
//  1. creating children under a parent
//  2. finding the root(s) of the tree
//  3. walking from a child up to its parent, and down to descendants
func TestTree(t *testing.T) {
	ctx := context.Background()
	client, err := velox.Open("sqlite", "file:tree.db?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, client.Close())
	}()

	require.NoError(t, client.Schema.Create(ctx))

	// --- Build the tree top-down. ---
	electronics := client.Category.Create().
		SetName("Electronics").SetSlug("electronics").
		SaveX(ctx)

	phones := client.Category.Create().
		SetName("Phones").SetSlug("phones").
		SetParentID(electronics.ID).
		SaveX(ctx)

	laptops := client.Category.Create().
		SetName("Laptops").SetSlug("laptops").
		SetParentID(electronics.ID).
		SaveX(ctx)

	iphone := client.Category.Create().
		SetName("iPhone").SetSlug("iphone").
		SetParentID(phones.ID).
		SaveX(ctx)

	client.Category.Create().
		SetName("Android").SetSlug("android").
		SetParentID(phones.ID).
		SaveX(ctx)

	// --- 1. Find the root(s). A root is a category with no parent. ---
	roots, err := client.Category.Query().
		Where(category.Not(category.HasParent())).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, roots, 1)
	assert.Equal(t, "Electronics", roots[0].Name)

	// --- 2. Walk from a child up to its parent. ---
	parent, err := client.Category.QueryParent(iphone).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Phones", parent.Name)

	grandparent, err := client.Category.QueryParent(parent).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Electronics", grandparent.Name)

	// --- 3. Walk down from a parent to its direct children. ---
	children, err := client.Category.QueryChildren(phones).All(ctx)
	require.NoError(t, err)
	names := []string{children[0].Name, children[1].Name}
	assert.ElementsMatch(t, []string{"iPhone", "Android"}, names)

	// Leaf check — a leaf has no children.
	leaves, err := client.Category.Query().
		Where(category.Not(category.HasChildren())).
		All(ctx)
	require.NoError(t, err)
	var leafNames []string
	for _, c := range leaves {
		leafNames = append(leafNames, c.Name)
	}
	assert.ElementsMatch(t, []string{"iPhone", "Android", "Laptops"}, leafNames)

	_ = laptops
}
