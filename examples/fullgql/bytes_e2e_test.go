package main

import (
	"testing"

	gqlclient "github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"
)

// TestBytesFieldRoundTrip pins that a Bytes field reads and writes through
// GraphQL as base64. The SDL declared `scalar Bytes` without a Go binding,
// so gqlgen bound it to string and generated resolver stubs that panicked:
// every read or write of Product.thumbnail answered "internal system error".
// Reading NULL from the Nillable (*[]byte) field goes through the generated
// ThumbnailOrNil accessor: gqlgen dereferences a nil *[]byte itself.
func TestBytesFieldRoundTrip(t *testing.T) {
	_, gql, _ := openCountingClient(t)
	var created struct {
		CreateProduct struct {
			ID        string
			Thumbnail string
		}
	}
	gql.MustPost(`mutation { createProduct(input: {name: "p", price: 1, thumbnail: "aGk="}) { id thumbnail } }`, &created)
	require.Equal(t, "aGk=", created.CreateProduct.Thumbnail, "base64 of \"hi\"")

	var updated struct {
		UpdateProduct struct{ Thumbnail string }
	}
	gql.MustPost(`mutation($id: ID!) { updateProduct(id: $id, input: {thumbnail: "AAEC"}) { thumbnail } }`, &updated,
		gqlclient.Var("id", created.CreateProduct.ID))
	require.Equal(t, "AAEC", updated.UpdateProduct.Thumbnail)

	var resp map[string]any
	err := gql.Post(`mutation { createProduct(input: {name: "q", price: 1, thumbnail: "not base64!"}) { id } }`, &resp)
	require.Error(t, err, "a value that is not base64 is rejected")

	var unset struct {
		CreateProduct struct{ Thumbnail *string }
	}
	gql.MustPost(`mutation { createProduct(input: {name: "r", price: 1}) { thumbnail } }`, &unset)
	require.Nil(t, unset.CreateProduct.Thumbnail, "an unset Bytes field reads as null")
}
