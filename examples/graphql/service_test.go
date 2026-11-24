package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/examples/graphql/veloxgen"
	"github.com/syssam/velox/examples/graphql/veloxschema"
)

// TestPaginateInAService: a service pages through generated code outside a
// GraphQL request, by a filter, an order and a cursor, and reads an edge of
// a row, with no setup and under veloxgql.Service alike.
func TestPaginateInAService(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", sqliteDSN(filepath.Join(t.TempDir(), "service.db")))
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
	org := veloxschema.OrgKey.With(t.Context(), acme)
	published := veloxgen.PostStatusPublished
	order := []*veloxgen.PostOrder{{Field: veloxgen.PostOrderFieldTitle, Direction: veloxgql.OrderDirectionAsc}}
	where := &veloxgen.PostWhereInput{Status: &published}
	two := 2
	page := func(ctx context.Context, after *veloxgql.Cursor) ([]string, *veloxgql.PageInfo, []*veloxgen.PostEdge, error) {
		conn, err := db.Query[veloxgen.Post]().Paginate(ctx, after, &two, nil, nil,
			veloxgen.WithPostOrder(order), veloxgen.WithPostFilter(where))
		if err != nil {
			return nil, nil, nil, err
		}
		edges, err := conn.Edges(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		info, err := conn.PageInfo(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		var titles []string
		for _, e := range edges {
			titles = append(titles, e.Node.Title)
		}
		return titles, info, edges, nil
	}

	// With no setup (decision 0173), and under veloxgql.Service, which a
	// service may still call: the same pages and the same edge.
	for name, ctx := range map[string]context.Context{"no setup": org, "veloxgql.Service": veloxgql.Service(org, db)} {
		titles, info, edges, err := page(ctx, nil)
		if err != nil || !slices.Equal(titles, []string{"Alpha", "Delta"}) || !info.HasNextPage {
			t.Fatalf("%s: first page: %v %q %+v, want Alpha and Delta and a next page", name, err, titles, info)
		}
		if author, err := edges[1].Node.Author(ctx); err != nil || author == nil || author.Name != "Chen" {
			t.Errorf("%s: Delta's author: %+v, %v, want Chen", name, author, err)
		}
		titles, info, _, err = page(ctx, info.EndCursor)
		if err != nil || !slices.Equal(titles, []string{"Gamma"}) || info.HasNextPage {
			t.Fatalf("%s: after Delta: %v %q %+v, want Gamma alone", name, err, titles, info)
		}
	}
}
