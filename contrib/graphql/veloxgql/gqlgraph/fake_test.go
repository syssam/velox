package gqlgraph_test

import (
	"context"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// TestConnectionFromRows: a connection of rows given has them as its page,
// with cursors that encode, the page info given, its start and end cursors
// filled, and the total given, with no DB.
func TestConnectionFromRows(t *testing.T) {
	ctx := context.Background()
	rows := []*child{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}}
	c := gqlgraph.ConnectionFromRows(rows, 7, veloxgql.PageInfo{HasNextPage: true})
	p, err := c.Page(ctx)
	if err != nil || len(p.Rows) != 2 || p.Node(1).(*child) != rows[1] {
		t.Fatalf("Page: %+v, %v", p, err)
	}
	if !p.Info.HasNextPage || p.Info.StartCursor != &p.Cursors[0] || p.Info.EndCursor != &p.Cursors[1] {
		t.Errorf("page info %+v", p.Info)
	}
	for _, cur := range p.Cursors {
		if _, err := veloxgql.EncodeCursor(cur); err != nil {
			t.Errorf("cursor %+v: %v", cur, err)
		}
	}
	if n, err := c.TotalCount(ctx); n != 7 || err != nil {
		t.Errorf("TotalCount = %d, %v, want 7", n, err)
	}
	empty := gqlgraph.ConnectionFromRows([]*child(nil), 0, veloxgql.PageInfo{})
	if p, err := empty.Page(ctx); err != nil || len(p.Rows) != 0 || p.Info.StartCursor != nil {
		t.Errorf("empty: %+v, %v", p, err)
	}
}
