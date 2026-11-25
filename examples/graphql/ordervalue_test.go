package main

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/examples/graphql/veloxgen"
	"github.com/syssam/velox/examples/graphql/veloxschema"
)

// TestPaginateByAComputedKey: Query.Paginate of a query ordered by an
// expression with bound values, NULLs last, pages through generated code
// forward and backward, one row a page, through cursors that went over the
// wire: ties by the key, the NULL last in either direction of paging.
func TestPaginateByAComputedKey(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", sqliteDSN(filepath.Join(t.TempDir(), "computed.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := open(t.Context(), sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	ctx := veloxschema.OrgKey.With(t.Context(), acme)
	// LENGTH(title), NULL for a title of 4 letters (Beta).
	key := velox.Calc[veloxgen.Post]("NULLIF(LENGTH(?), ?)", veloxgen.Posts.Title, int64(4))
	q := db.Query[veloxgen.Post]().OrderBy(key.Desc().NullsLast())
	wire := func(c *veloxgql.Cursor) *veloxgql.Cursor {
		s, err := veloxgql.EncodeCursor(*c)
		if err != nil {
			t.Fatal(err)
		}
		var d veloxgql.Cursor
		if err := d.UnmarshalGQL(s); err != nil {
			t.Fatal(err)
		}
		return &d
	}
	one := 1
	var forward, backward []string
	var after, before *veloxgql.Cursor
	for range 6 {
		conn, err := q.Paginate(ctx, after, &one, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		edges, err := conn.Edges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		info, err := conn.PageInfo(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			forward = append(forward, e.Node.Title)
		}
		if !info.HasNextPage {
			break
		}
		after = wire(info.EndCursor)
	}
	for range 6 {
		conn, err := q.Paginate(ctx, nil, nil, before, &one)
		if err != nil {
			t.Fatal(err)
		}
		edges, err := conn.Edges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		info, err := conn.PageInfo(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			backward = append([]string{e.Node.Title}, backward...)
		}
		if !info.HasPreviousPage {
			break
		}
		before = wire(info.StartCursor)
	}
	want := []string{"Alpha", "Gamma", "Delta", "Beta"}
	if !slices.Equal(forward, want) || !slices.Equal(backward, want) {
		t.Errorf("forward %q, backward %q, want %q", forward, backward, want)
	}
}
