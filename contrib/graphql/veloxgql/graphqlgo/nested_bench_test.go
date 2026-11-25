package graphqlgo_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/graphql-go"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/contrib/graphql/veloxgql/graphqlgo"
)

// resolverDBKey is the context key of the DB a test's root resolver reads
// on, as an application's Resolver holds it: a resolver reads on the DB it
// holds, never one from the context.
type resolverDBKey struct{}

// withResolverDB is ctx with db for the root resolvers.
func withResolverDB(ctx context.Context, db *velox.DB) context.Context {
	return context.WithValue(ctx, resolverDBKey{}, db)
}

// resolverDB is the DB withResolverDB put in ctx.
func resolverDB(ctx context.Context) *velox.DB {
	db, _ := ctx.Value(resolverDBKey{}).(*velox.DB)
	return db
}

// nestedQuery reads three levels: authors, a page of each one's posts with
// their count, and each post's author and tags.
const nestedQuery = `{ authors(first: 50) { edges { node { name posts(first: 10) { totalCount edges { node { title author { name } tags { name } } } } } } } }`

// nestedStmts is what nestedQuery sends with batching: the authors' page,
// the posts' pages and their counts, the posts' authors and their tags, one
// statement each however many rows.
const nestedStmts = 5

// seedNested adds 47 authors (100…146), ten posts of each (1000…) and gives
// each post the three tags.
func seedNested(t testing.TB, db *velox.DB) {
	t.Helper()
	var a, p, pt []string
	for i := range 47 {
		a = append(a, fmt.Sprintf("(%d, 'n%d')", 100+i, i))
		for j := range 10 {
			id := 1000 + 10*i + j
			p = append(p, fmt.Sprintf("(%d, %d, 't%d')", id, 100+i, id))
			for _, tag := range []int{21, 22, 23} {
				pt = append(pt, fmt.Sprintf("(%d, %d)", id, tag))
			}
		}
	}
	for _, s := range []string{
		"INSERT INTO authors VALUES " + strings.Join(a, ", "),
		"INSERT INTO posts VALUES " + strings.Join(p, ", "),
		"INSERT INTO post_tags VALUES " + strings.Join(pt, ", "),
	} {
		if _, err := db.Exec(context.Background(), "seed", s); err != nil {
			t.Fatal(err)
		}
	}
}

// nestedServers are the servers the nested query is measured on: each
// engine with no setup (decision 0173), whose root resolver reads on the DB
// it is given, and gqlgen hardened with the opt-in extensions, which
// graphql-go has no counterpart of. The DB has the cost limit lifted: the
// query costs more than the default admits.
func nestedServers(t testing.TB, db *velox.DB) []struct {
	name string
	srv  server
} {
	return []struct {
		name string
		srv  server
	}{
		{"graphqlgo/nosetup", executorServer(newExecutor(graphqlgoSchema(t), graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(nil))))},
		{"gqlgen/nosetup", gqlgenPlainServer()},
		{"gqlgen/hardened", gqlgenServer(db,
			gqlgenx.Transactioner{DB: db}, gqlgenx.RequestLimit(db), gqlgenx.CostLimit(db), gqlgenx.ResponseLimit(db))},
	}
}

// TestNestedNoSetup: the nested query answers the same, with as many
// statements, through each engine with no setup as through gqlgen with the
// opt-in extensions.
func TestNestedNoSetup(t *testing.T) {
	db, log := modelDB(t, veloxgql.WithLimits(veloxgql.Limits{MaxCost: 1 << 30}))
	seedNested(t, db)
	ctx := withResolverDB(context.Background(), db)
	var want string
	for _, s := range nestedServers(t, db) {
		log.take()
		out := s.srv(t, ctx, nestedQuery, nil)
		st, _ := log.take()
		if strings.Contains(out, `"errors"`) {
			t.Fatalf("%s: %s", s.name, out)
		}
		if len(st) != nestedStmts {
			t.Errorf("%s: %d statements, want %d: %q", s.name, len(st), nestedStmts, st)
		}
		if want == "" {
			want = out
		} else if out != want {
			t.Errorf("%s answers otherwise:\n%s\nwant\n%s", s.name, out, want)
		}
	}
}

// BenchmarkNested serves nestedQuery through each of nestedServers.
func BenchmarkNested(b *testing.B) {
	db, log := modelDB(b, veloxgql.WithLimits(veloxgql.Limits{MaxCost: 1 << 30}))
	seedNested(b, db)
	ctx := withResolverDB(context.Background(), db)
	for _, s := range nestedServers(b, db) {
		b.Run(s.name, func(b *testing.B) {
			log.take()
			b.ReportAllocs()
			for b.Loop() {
				s.srv(b, ctx, nestedQuery, nil)
			}
			st, _ := log.take()
			b.ReportMetric(float64(len(st))/float64(b.N), "stmts/op")
		})
	}
}
