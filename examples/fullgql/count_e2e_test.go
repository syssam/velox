package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countQueries returns the recorded COUNT queries against table.
func countQueries(l *queryLog, table string) []string {
	var out []string
	for _, q := range l.snapshot() {
		if strings.Contains(q, "COUNT(") && strings.Contains(q, "`"+table+"`") {
			out = append(out, q)
		}
	}
	return out
}

// TestPaginate_CountsOnlyWhenTotalCountIsSelected pins that a generated
// Paginate runs its COUNT only when the connection's selection reads
// totalCount (Ent parity: hasCollectedField(totalCount)). pageInfo never
// needs it — hasNextPage and hasPreviousPage come from fetching one row past
// the page. Both the top-level connection and a per-row edge connection
// (orderBy forces the edge method through Paginate) are covered, and a
// Paginate called outside a GraphQL operation keeps counting.
func TestPaginate_CountsOnlyWhenTotalCountIsSelected(t *testing.T) {
	type response struct {
		Users struct {
			TotalCount int `json:"totalCount"`
			PageInfo   struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
			Edges []struct {
				Node struct {
					Name  string `json:"name"`
					Todos struct {
						TotalCount int `json:"totalCount"`
						PageInfo   struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
						Edges []struct {
							Node todoNode `json:"node"`
						} `json:"edges"`
					} `json:"todos"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"users"`
	}
	client, gql, log := openCountingClient(t)
	seedTodos(t, client, 3, 4, 0)

	t.Run("top level without totalCount", func(t *testing.T) {
		log.reset()
		var resp response
		gql.MustPost(`{ users(first: 2) { pageInfo { hasNextPage } edges { node { name } } } }`, &resp)
		require.Len(t, resp.Users.Edges, 2)
		assert.True(t, resp.Users.PageInfo.HasNextPage, "hasNextPage does not need the count")
		assert.Empty(t, countQueries(log, "users"), "no COUNT when totalCount is not selected: %v", log.snapshot())
		assert.Len(t, log.snapshot(), 1, "the page query only: %v", log.snapshot())
	})
	t.Run("top level with totalCount", func(t *testing.T) {
		log.reset()
		var resp response
		gql.MustPost(`{ users(first: 2) { totalCount edges { node { name } } } }`, &resp)
		require.Len(t, resp.Users.Edges, 2)
		assert.Equal(t, 3, resp.Users.TotalCount, "totalCount counts every row, not the page")
		assert.Len(t, countQueries(log, "users"), 1, "%v", log.snapshot())
	})
	const edgeArgs = `first: 2, orderBy: {field: CREATED_AT, direction: ASC}`
	t.Run("edge connection without totalCount", func(t *testing.T) {
		log.reset()
		var resp response
		gql.MustPost(`{ users(first: 10) { edges { node { name todos(`+edgeArgs+`) { pageInfo { hasNextPage } edges { node { title } } } } } } }`, &resp)
		require.Len(t, resp.Users.Edges, 3)
		for _, e := range resp.Users.Edges {
			assert.Len(t, e.Node.Todos.Edges, 2)
			assert.True(t, e.Node.Todos.PageInfo.HasNextPage)
		}
		assert.Empty(t, countQueries(log, "todos"), "no COUNT per parent when totalCount is not selected: %v", log.snapshot())
	})
	t.Run("edge connection with totalCount", func(t *testing.T) {
		log.reset()
		var resp response
		gql.MustPost(`{ users(first: 10) { edges { node { name todos(`+edgeArgs+`) { totalCount edges { node { title } } } } } } }`, &resp)
		require.Len(t, resp.Users.Edges, 3)
		for _, e := range resp.Users.Edges {
			assert.Len(t, e.Node.Todos.Edges, 2)
			assert.Equal(t, 4, e.Node.Todos.TotalCount)
		}
		assert.Len(t, countQueries(log, "todos"), 3, "one COUNT per parent: %v", log.snapshot())
	})
	t.Run("outside a GraphQL operation", func(t *testing.T) {
		log.reset()
		conn, err := client.User.Query().Paginate(context.Background(), nil, ptr(1), nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 3, conn.TotalCount, "a direct Paginate has no selection to consult and keeps counting")
		assert.Len(t, countQueries(log, "users"), 1, "%v", log.snapshot())
	})
}
