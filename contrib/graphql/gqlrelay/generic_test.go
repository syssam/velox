package gqlrelay

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConnection_ZeroValue(t *testing.T) {
	var conn Connection[string]
	assert.Nil(t, conn.Edges)
	assert.Equal(t, 0, conn.TotalCount)
}

func TestConnection_WithEdges(t *testing.T) {
	node1 := "hello"
	node2 := "world"
	conn := Connection[string]{
		Edges: []*Edge[string]{
			{Node: &node1, Cursor: Cursor{}},
			{Node: &node2, Cursor: Cursor{}},
		},
		TotalCount: 2,
		PageInfo:   PageInfo{HasNextPage: true},
	}
	assert.Len(t, conn.Edges, 2)
	assert.Equal(t, "hello", *conn.Edges[0].Node)
	assert.Equal(t, 2, conn.TotalCount)
	assert.True(t, conn.PageInfo.HasNextPage)
}

func TestEdge_NilNode(t *testing.T) {
	edge := Edge[int]{Node: nil, Cursor: Cursor{}}
	assert.Nil(t, edge.Node)
}

func TestEdge_WithNode(t *testing.T) {
	val := 42
	edge := Edge[int]{Node: &val, Cursor: Cursor{}}
	assert.Equal(t, 42, *edge.Node)
}
