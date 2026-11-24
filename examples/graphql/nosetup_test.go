package main

import (
	"net/http"
	"slices"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/examples/graphql/veloxgen"
)

// testServe makes the server newServer serves: the example's, unless a test
// swaps it.
var testServe = server

// plainServer is minimalServer with gqlgenx.ErrorPresenter and
// introspection, as the tests' expected errors and their __type queries
// need: no extension, no setup. (TestFieldDirective is not run on it: its
// errors are without the suggestions gqlgenx.Setup leaves out.)
func plainServer(db *velox.DB, _ ...gqlgenx.Option) http.Handler {
	srv := gqlgenServer(db)
	srv.AddTransport(transport.POST{})
	srv.SetErrorPresenter(gqlgenx.ErrorPresenter(nil))
	srv.Use(extension.Introspection{})
	return srv
}

// TestNoSetupServer (decision 0173) runs the tests of reads, scope and
// batching against a server with no extension and no setup: the same
// responses, and the same statements, one for each edge of each level,
// with the same columns and the same organization's condition.
func TestNoSetupServer(t *testing.T) {
	defer func(s func(*velox.DB, ...gqlgenx.Option) http.Handler) { testServe = s }(testServe)
	testServe = plainServer
	tests := map[string]func(*testing.T){
		"ByID": TestByID, "Paging": TestPaging, "OrderAndWhere": TestOrderAndWhere,
		"NestedEdges": TestNestedEdges, "NoNPlusOne": TestNoNPlusOne, "ColumnsCollected": TestColumnsCollected,
		"AliasesBatch": TestAliasesBatch, "AppFieldReadsAllColumns": TestAppFieldReadsAllColumns,
		"BatchInMutation": TestBatchInMutation, "TotalCountOnly": TestTotalCountOnly, "OrgIsolation": TestOrgIsolation,
		"SensitiveFieldAbsent": TestSensitiveFieldAbsent, "PublishedPosts": TestPublishedPosts,
	}
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range tests {
			if !yield(k) {
				return
			}
		}
	}) {
		t.Run(name, tests[name])
	}
}

// TestConnectionFromRows: a test's fake builds a generated connection of
// rows it has, with no DB: its fields resolve from them.
func TestConnectionFromRows(t *testing.T) {
	posts := []*veloxgen.Post{{ID: 1, Title: "Alpha"}, {ID: 2, Title: "Beta"}}
	conn := &veloxgen.PostConnection{Conn: gqlgraph.ConnectionFromRows(posts, 5, veloxgql.PageInfo{HasNextPage: true})}
	ctx := t.Context()
	edges, err := conn.Edges(ctx)
	if err != nil || len(edges) != 2 || edges[1].Node != posts[1] {
		t.Fatalf("Edges: %v, %v", edges, err)
	}
	info, err := conn.PageInfo(ctx)
	if err != nil || !info.HasNextPage || info.EndCursor == nil {
		t.Errorf("PageInfo: %+v, %v", info, err)
	}
	if n, err := conn.TotalCount(ctx); n != 5 || err != nil {
		t.Errorf("TotalCount: %d, %v, want 5", n, err)
	}
}
