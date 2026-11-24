package main

import (
	"context"
	"testing"

	"github.com/syssam/velox/examples/graphql/veloxgen"
)

// TestZeroConnectionIsEmpty: the zero value of a generated connection type is
// the empty connection, which an application returns for a list it denies:
// no edges, a page info with no pages and no cursors, and a totalCount of 0,
// with no error and no statement (the type holds no DB).
func TestZeroConnectionIsEmpty(t *testing.T) {
	ctx := context.Background()
	c := &veloxgen.PostConnection{}

	edges, err := c.Edges(ctx)
	if err != nil || len(edges) != 0 {
		t.Fatalf("Edges() = %v, %v; want no edges and no error", edges, err)
	}
	info, err := c.PageInfo(ctx)
	if err != nil || info == nil {
		t.Fatalf("PageInfo() = %v, %v; want a page info and no error", info, err)
	}
	if info.HasNextPage || info.HasPreviousPage || info.StartCursor != nil || info.EndCursor != nil {
		t.Errorf("PageInfo() = %+v; want no pages and no cursors", *info)
	}
	n, err := c.TotalCount(ctx)
	if err != nil || n != 0 {
		t.Fatalf("TotalCount() = %d, %v; want 0 and no error", n, err)
	}
}
