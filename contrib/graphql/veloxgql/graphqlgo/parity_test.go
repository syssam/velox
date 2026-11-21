package graphqlgo_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql/graphqlgo"
)

// parityCase is a request both servers answer.
type parityCase struct {
	name  string
	query string
	vars  map[string]any
	// want is a part of the response both must hold, and stmts the number
	// of statements each must send.
	want  string
	stmts int
}

// parityCases are the parity suite's requests: a connection with nested
// edges (a connection, a to-one and a list, batched), paging by a cursor, a
// where filter, a Node lookup, aliases, and an input error of velox's.
var parityCases = []parityCase{
	{
		name: "nested",
		query: `{ authors(first: 10) { totalCount pageInfo { hasNextPage endCursor }
			edges { cursor node { id name posts(first: 2) { totalCount edges { node { id title author { name } tags { name } } } } } } } }`,
		want: `"tags":[{"name":"go"},{"name":"sql"}]`,
		// The page and its count, then one statement each for the posts'
		// pages, their counts, their authors and their tags.
		stmts: 6,
	},
	{
		name:  "after a cursor",
		query: `query($after: Cursor) { authors(first: 1, after: $after) { edges { node { name } } pageInfo { hasNextPage hasPreviousPage startCursor } } }`,
		vars:  map[string]any{"after": nil},
		want:  `{"node":{"name":"ann"}}`,
		stmts: 1,
	},
	{
		name:  "where",
		query: `{ authors(first: 5, where: {or: [{nameHasPrefix: "b"}, {name: "cy"}], not: {name: "ann"}}) { totalCount edges { node { name posts(first: 5) { edges { node { title } } } } } } }`,
		want:  `"totalCount":2`,
		stmts: 3,
	},
	{
		name:  "node",
		query: `query($id: ID!) { node(id: $id) { __typename id ... on Post { title author { name } tags { name } } } }`,
		vars:  map[string]any{"id": "11"},
		want:  `"__typename":"Post"`,
		// A probe of each type, then the post's author and its tags, each
		// alone.
		stmts: 5,
	},
	{
		name:  "aliases",
		query: `{ authors(first: 3) { edges { node { a: posts(first: 1) { edges { node { title } } } b: posts(first: 2) { totalCount edges { node { id } } } } } } }`,
		want:  `"b":{"totalCount":2`,
		// The page, then each alias's own batch: two pages and a count.
		stmts: 4,
	},
	{
		// An input error of velox's: a scalar's error, such as a malformed
		// cursor, is not one, as graphql-go refuses a literal when it
		// validates the document, and gqlgen when it decodes the argument.
		name:  "a page over MaxPage",
		query: `{ authors(first: 1000) { totalCount } }`,
		want:  `"BAD_USER_INPUT"`,
		stmts: 0,
	},
}

// TestParity: the same requests served through gqlgenx and graphqlgo give
// the same JSON (errors' locations aside) and send the same statements,
// the same in number and in SQL.
func TestParity(t *testing.T) {
	ctx := context.Background()
	gdb, glog := modelDB(t)
	qdb, qlog := modelDB(t)
	servers := []struct {
		name string
		srv  server
		log  *stmtLog
	}{
		{"gqlgenx", gqlgenServer(t, gdb), glog},
		{"graphqlgo", graphqlgoServer(t, qdb), qlog},
	}
	// The first after-a-cursor run finds the cursor the second pages from.
	for _, c := range parityCases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			var stmts [][]string
			for _, s := range servers {
				out := s.srv(t, ctx, c.query, c.vars)
				st, _ := s.log.take()
				if !strings.Contains(out, c.want) {
					t.Errorf("%s: %s, want it to hold %s", s.name, out, c.want)
				}
				if len(st) != c.stmts {
					t.Errorf("%s: %d statements, want %d: %q", s.name, len(st), c.stmts, st)
				}
				got, stmts = append(got, out), append(stmts, st)
			}
			if !reflect.DeepEqual(normal(t, got[0]), normal(t, got[1])) {
				t.Errorf("responses differ:\n gqlgenx   %s\n graphqlgo %s", got[0], got[1])
			}
			if !slices.Equal(stmts[0], stmts[1]) {
				t.Errorf("statements differ:\n gqlgenx   %q\n graphqlgo %q", stmts[0], stmts[1])
			}
		})
	}
}

// TestParityMutation: a mutation in a transaction, gqlgenx's by default and
// graphqlgo's under MutationTx, sends the same statements in it and
// commits; one whose resolver fails after its insert rolls back on both,
// with data null and the same error.
func TestParityMutation(t *testing.T) {
	ctx := context.Background()
	gdb, glog := modelDB(t)
	qdb, qlog := modelDB(t)
	servers := []struct {
		name string
		srv  server
		log  *stmtLog
	}{
		{"gqlgenx", gqlgenServer(t, gdb), glog},
		{"graphqlgo", graphqlgoServer(t, qdb, graphqlgo.MutationTx()), qlog},
	}
	for _, c := range []struct {
		query, want string
		txs         []string
		posts       int
	}{
		{`mutation { addPost(author: 2, title: "new") { title author { name posts(first: 5) { totalCount } } tags { name } } }`, `{"data":{"addPost":{"title":"new","author":{"name":"bob","posts":{"totalCount":4}},"tags":[]}}}`, []string{"commit"}, 7},
		{`mutation { failPost(author: 2, title: "lost") { title } }`, `"data":null`, []string{"rollback"}, 7},
	} {
		var got []string
		var stmts [][]string
		for _, s := range servers {
			out := s.srv(t, ctx, c.query, nil)
			st, txs := s.log.take()
			if !strings.Contains(out, c.want) {
				t.Errorf("%s: %s, want it to hold %s", s.name, out, c.want)
			}
			if !slices.Equal(txs, c.txs) {
				t.Errorf("%s: transactions %q, want %q", s.name, txs, c.txs)
			}
			for _, x := range st {
				if !strings.HasSuffix(x, " in tx") {
					t.Errorf("%s: %q ran outside the transaction", s.name, x)
				}
			}
			got, stmts = append(got, out), append(stmts, st)
		}
		if n := postCount(t, gdb, glog); n != c.posts {
			t.Errorf("gqlgenx: %d posts, want %d", n, c.posts)
		}
		if n := postCount(t, qdb, qlog); n != c.posts {
			t.Errorf("graphqlgo: %d posts, want %d", n, c.posts)
		}
		if !reflect.DeepEqual(normal(t, got[0]), normal(t, got[1])) {
			t.Errorf("responses differ:\n gqlgenx   %s\n graphqlgo %s", got[0], got[1])
		}
		if !slices.Equal(stmts[0], stmts[1]) {
			t.Errorf("statements differ:\n gqlgenx   %q\n graphqlgo %q", stmts[0], stmts[1])
		}
	}
}

// TestConcurrentRequests: graphql-go resolves fields concurrently, and
// requests run in parallel: under -race no data race, and each request
// sends what one alone does.
func TestConcurrentRequests(t *testing.T) {
	db, log := modelDB(t)
	srv := graphqlgoServer(t, db)
	c := parityCases[0]
	want := srv(t, context.Background(), c.query, nil)
	log.take()
	const workers, rounds = 4, 5
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range rounds {
				if got := srv(t, context.Background(), c.query, nil); got != want {
					t.Errorf("\n got %s\nwant %s", got, want)
				}
			}
		})
	}
	wg.Wait()
	if st, _ := log.take(); len(st) != workers*rounds*c.stmts {
		t.Errorf("%d statements, want %d per request", len(st), c.stmts)
	}
}
