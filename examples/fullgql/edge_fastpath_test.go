package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	productclient "example.com/fullgql/velox/client/product"
	tagclient "example.com/fullgql/velox/client/tag"
	"example.com/fullgql/velox/entity"
	"example.com/fullgql/velox/tag"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
)

// TestEntityEdgeMethod_FastPathMatchesPaginate pins that a connection edge
// method answers the same whether or not the edge was eager-loaded. The fast
// path reuses the loaded slice; it must not change which rows come back,
// their order, or totalCount.
func TestEntityEdgeMethod_FastPathMatchesPaginate(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t)
	cfg := client.RuntimeConfig()

	tg, err := tagclient.NewTagClient(cfg).Create().
		SetInput(tagclient.CreateTagInput{Name: "fast-vs-slow"}).Save(ctx)
	require.NoError(t, err)
	// IDs ascend with price, so price-descending order is the reverse of the
	// order an unordered eager load returns.
	for _, p := range []struct {
		name  string
		price float64
	}{{"p1", 10}, {"p2", 20}, {"p3", 30}} {
		_, err := productclient.NewProductClient(cfg).Create().
			SetInput(productclient.CreateProductInput{Name: p.name, Price: p.price, TagIDs: []int{tg.ID}}).
			Save(ctx)
		require.NoError(t, err)
	}

	var byPrice entity.ProductOrderField
	require.NoError(t, byPrice.UnmarshalGQL("PRICE"))
	priceDesc := &entity.ProductOrder{Direction: gqlrelay.OrderDirectionDesc, Field: &byPrice}

	cases := []struct {
		name        string
		first, last *int
		order       *entity.ProductOrder
	}{
		{name: "first page", first: ptr(2)},
		{name: "last page", last: ptr(2)},
		{name: "ordered", first: ptr(1), order: priceDesc},
		{name: "ordered last", last: ptr(1), order: priceDesc},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slowTag, err := client.Tag.Query().Where(tag.IDField.EQ(tg.ID)).Only(ctx)
			require.NoError(t, err)
			slow, err := slowTag.Products(ctx, nil, tc.first, nil, tc.last, tc.order, nil)
			require.NoError(t, err)

			fastTag, err := client.Tag.Query().Where(tag.IDField.EQ(tg.ID)).WithProducts().Only(ctx)
			require.NoError(t, err)
			fast, err := fastTag.Products(ctx, nil, tc.first, nil, tc.last, tc.order, nil)
			require.NoError(t, err)

			require.Equal(t, productNames(slow), productNames(fast), "rows and their order")
			require.Equal(t, slow.TotalCount, fast.TotalCount, "totalCount")
			require.Equal(t, slow.PageInfo.HasNextPage, fast.PageInfo.HasNextPage, "hasNextPage")
			require.Equal(t, slow.PageInfo.HasPreviousPage, fast.PageInfo.HasPreviousPage, "hasPreviousPage")
		})
	}
}

func productNames(c *entity.ProductConnection) []string {
	names := make([]string, 0, len(c.Edges))
	for _, e := range c.Edges {
		names = append(names, e.Node.Name)
	}
	return names
}
