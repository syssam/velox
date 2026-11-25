package graphqlgo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/graphql-go"

	"github.com/syssam/velox/contrib/graphql/veloxgql/graphqlgo"
)

// plainServers are both engines' servers with no setup (decision 0173).
func plainServers(t *testing.T) map[string]server {
	return map[string]server{
		"graphqlgo": executorServer(newExecutor(graphqlgoSchema(t), graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(nil)))),
		"gqlgen":    gqlgenPlainServer(),
	}
}

// TestNoSetupLimits: with no setup, generated code holds a page to
// DefaultLimits' MaxPage 100.
func TestNoSetupLimits(t *testing.T) {
	db, log := modelDB(t)
	ctx := withResolverDB(context.Background(), db)
	for name, srv := range plainServers(t) {
		out := srv(t, ctx, `{ authors(first: 101) { totalCount } }`, nil)
		if !strings.Contains(out, "BAD_USER_INPUT") || !strings.Contains(out, "100") {
			t.Errorf("%s: first: 101: %s", name, out)
		}
		if st, _ := log.take(); len(st) != 0 {
			t.Errorf("%s: sent %q", name, st)
		}
	}
}

// TestNoSetupMutation: with no setup, a mutation runs in no transaction, on
// either engine: a resolver that fails after its insert keeps the row.
// gqlgenx.Transactioner is what runs a gqlgen mutation in one (its tests in
// gqlgenx).
func TestNoSetupMutation(t *testing.T) {
	for name, srv := range plainServers(t) {
		db, log := modelDB(t)
		ctx := withResolverDB(context.Background(), db)
		out := srv(t, ctx, `mutation { failPost(author: 1, title: "kept") { title } }`, nil)
		st, txs := log.take()
		if !strings.Contains(out, `"failPost":null`) || len(txs) != 0 || len(st) != 1 || strings.HasSuffix(st[0], " in tx") {
			t.Errorf("%s: %s, statements %q, transactions %q; want the insert outside a transaction", name, out, st, txs)
		}
		if n := postCount(t, db, log); n != 7 {
			t.Errorf("%s: %d posts, want the insert kept", name, n)
		}
	}
}
