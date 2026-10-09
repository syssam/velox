package sqlgraph

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/schema/field"
)

// TestCreateSpec_SetFieldKeepsEveryField pins the chunked backing store of
// CreateSpec.SetField across a chunk boundary: every FieldSpec keeps its own
// value (a store that grew by append would move the earlier ones, and an
// index bug would alias two fields), and mutating one through Fields is seen
// only by that field.
func TestCreateSpec_SetFieldKeepsEveryField(t *testing.T) {
	spec := NewCreateSpec("users", &FieldSpec{Column: "id", Type: field.TypeInt})
	const n = createFieldChunk*2 + 3
	for i := range n {
		spec.SetField(columnName(i), field.TypeInt, i)
	}
	require.Len(t, spec.Fields, n)
	seen := make(map[*FieldSpec]bool, n)
	for i, f := range spec.Fields {
		require.Equal(t, columnName(i), f.Column)
		require.Equal(t, i, f.Value)
		require.False(t, seen[f], "field %d shares its FieldSpec with another", i)
		seen[f] = true
	}
	spec.Fields[createFieldChunk-1].Value = "changed"
	require.Equal(t, createFieldChunk, spec.Fields[createFieldChunk].Value)
	require.Equal(t, createFieldChunk-2, spec.Fields[createFieldChunk-2].Value)
}

func columnName(i int) string { return "c" + string(rune('a'+i)) }
