package gqlgenrelay

import (
	"bytes"
	"context"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
)

// Importing the package is what makes collection read gqlgen: generated code
// relies on that and imports nothing else.
func TestImportRegistersGQLGen(t *testing.T) {
	oc := &graphql.OperationContext{Variables: map[string]any{}}
	fc := &graphql.FieldContext{Field: graphql.CollectedField{Field: &ast.Field{Name: "item", SelectionSet: ast.SelectionSet{
		&ast.Field{Name: "id", Alias: "id"},
		&ast.Field{Name: "__typename", Alias: "__typename"},
	}}}}
	ctx := graphql.WithFieldContext(graphql.WithOperationContext(context.Background(), oc), fc)
	assert.True(t, gqlrelay.SelectionCoveredByID(ctx, "Item"))

	_, ok := Source(context.Background())
	assert.False(t, ok, "outside a gqlgen resolver there is no field")
}

// Bytes travels as standard base64; nil is null, and anything that is not
// base64 is refused rather than read as empty.
func TestBytesRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	MarshalBytes([]byte("velox\x00")).MarshalGQL(&buf)
	assert.Equal(t, `"dmVsb3gA"`, buf.String())

	buf.Reset()
	MarshalBytes(nil).MarshalGQL(&buf)
	assert.Equal(t, "null", buf.String())

	b, err := UnmarshalBytes("dmVsb3gA")
	require.NoError(t, err)
	assert.Equal(t, []byte("velox\x00"), b)

	b, err = UnmarshalBytes(nil)
	require.NoError(t, err)
	assert.Nil(t, b)

	_, err = UnmarshalBytes("not base64!")
	assert.Error(t, err)
	_, err = UnmarshalBytes(42)
	assert.Error(t, err)
}
