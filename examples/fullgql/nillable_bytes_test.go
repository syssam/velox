package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestQueryAll_NillableBytesAreNotSharedBetweenRows pins, on generated code,
// that every row of a field.Bytes().Nillable() column keeps its own value.
// The generated AssignValues stores the scan holder pointer itself
// (e.Thumbnail = value), so a runtime that scans every row into one shared
// *[]byte makes all of them read the last row's bytes — and a NULL in the
// last row turns every row's value into nil.
func TestQueryAll_NillableBytesAreNotSharedBetweenRows(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t)
	for _, name := range []string{"a", "b", "c"} {
		require.NoError(t, client.Product.Create().
			SetName(name).SetPrice(1).SetThumbnail([]byte("thumb-"+name)).Exec(ctx))
	}
	// The last row has no thumbnail.
	require.NoError(t, client.Product.Create().SetName("d").SetPrice(1).Exec(ctx))

	products, err := client.Product.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, products, 4)
	got := map[string]string{}
	seen := map[*[]byte]string{}
	for _, p := range products {
		// A NULL column scans as a non-nil pointer to a nil slice.
		require.NotNil(t, p.Thumbnail, p.Name)
		if other, dup := seen[p.Thumbnail]; dup {
			t.Fatalf("products %q and %q share one Thumbnail pointer", other, p.Name)
		}
		seen[p.Thumbnail] = p.Name
		got[p.Name] = string(*p.Thumbnail)
	}
	require.Equal(t, map[string]string{"a": "thumb-a", "b": "thumb-b", "c": "thumb-c", "d": ""}, got)
}
