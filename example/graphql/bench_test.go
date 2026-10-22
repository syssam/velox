package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/example/graphql/gen"
)

// nested is the request the benchmark serves: users, each with a page of its
// posts and their count, each post with its author and a page of its tags.
// Four levels, and with batching one statement for each of them, however many
// users the first argument asks for (the posts' count is the fifth).
const nested = `query ($n: Int) {
  users(first: $n) {
    edges { node { name posts(first: 10) { totalCount edges { node {
      title author { name } tags(first: 5) { edges { node { name } } }
    } } } } }
  }
}`

// The shape of the benchmark's data: a user has benchPosts posts under acme,
// more than the page's ten, and each post has benchTags of the benchTags tags.
const (
	benchUsers = 50
	benchPosts = 12
	benchTags  = 6
)

// nestedAllocsBudget is the most allocations one nested request may make at
// n = 50 under gqlgen v0.17.86, through testing.AllocsPerRun: the measured
// figure plus 10%, because gqlgen resolves sibling fields in goroutines and
// the count jitters by a few allocations from run to run (plan 4, ruling 17).
// Measured on 2026-10-22 on darwin/arm64 (Apple M3 Max), go test -bench
// Nested -benchmem, three runs (load average 5 to 6): at n=50, 5 statements
// and 158525, 158397 and 158460 allocations for 3550 nodes (44.6 per node);
// at n=10, 5 statements and 32041, 32037 and 32041 for 710 nodes (45.1 per
// node). The budget is the largest, 158525, plus 10%, rounded up.
// The same request before the edges were batched, at cafc372, the commit
// before plan 4's Task 5: 221 statements at n=10 and 1101 at n=50, which
// grew with the users, and 197807, 197889 and 197783 allocations at n=50.
// Batching makes the statements flat and takes 20% off the allocations.
const nestedAllocsBudget = 174400

// pinnedGqlgen is the gqlgen version the allocation budget was measured under;
// scripts/gqlgen-latest.sh runs the tests under a newer one, where the
// allocations are gqlgen's to change, and the check is skipped.
const pinnedGqlgen = "v0.17.86"

// seedBench inserts users users (named U1…), posts posts of each under acme,
// and tags tags (named T1…), and links every post to every tag.
func seedBench(t testing.TB, db *velox.DB, users, posts, tags int) {
	t.Helper()
	ctx := t.Context()
	us := make([]gen.User, users)
	for i := range us {
		us[i] = gen.User{ID: int64(i + 1), Name: fmt.Sprintf("U%d", i+1), Email: "u@example.com", Role: gen.UserRoleMember,
			JoinedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), PasswordHash: "x"}
	}
	ps := make([]gen.Post, 0, users*posts)
	for u := range users {
		for p := range posts {
			ps = append(ps, gen.Post{ID: int64(u*posts + p + 1), AuthorID: int64(u + 1), Title: fmt.Sprintf("P%d-%d", u+1, p+1),
				Status: gen.PostStatusPublished, Views: int64(p)})
		}
	}
	ts := make([]gen.Tag, tags)
	for i := range ts {
		ts[i] = gen.Tag{ID: int64(i + 1), Name: fmt.Sprintf("T%d", i+1), Rank: int8(i + 1)}
	}
	links := make([]gen.PostTag, 0, len(ps)*tags)
	for _, p := range ps {
		for _, tg := range ts {
			links = append(links, gen.PostTag{PostID: p.ID, TagID: tg.ID})
		}
	}
	for _, step := range []struct {
		name string
		do   func() error
	}{
		{"users", func() error { return db.InsertAll(us).Exec(ctx) }},
		{"posts", func() error { return db.InsertAll(ps).Exec(velox.WithTenant(ctx, acme)) }},
		{"tags", func() error { return db.InsertAll(ts).Exec(ctx) }},
		{"links", func() error { return db.InsertAll(links).Exec(ctx) }},
	} {
		if err := step.do(); err != nil {
			t.Fatalf("seeding %s: %v", step.name, err)
		}
	}
}

// benchHandler is the example's endpoint on a database seeded for the
// benchmark, and the counter of the statements it sends. The handler is
// called directly: no network.
func benchHandler(t testing.TB) (http.Handler, *atomic.Int64) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	var stmts atomic.Int64
	var counting atomic.Bool
	db, err := open(t.Context(), sqlDB, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		if counting.Load() {
			stmts.Add(1)
		}
		return next(ctx)
	}))
	if err != nil {
		t.Fatal(err)
	}
	seedBench(t, db, benchUsers, benchPosts, benchTags)
	counting.Store(true)
	return orgFromHeader(server(db)), &stmts
}

// serveNested sends nested with n users to h and returns the response's body.
func serveNested(tb testing.TB, h http.Handler, n int) []byte {
	body, err := json.Marshal(map[string]any{"query": nested, "variables": map[string]any{"n": n}})
	if err != nil {
		tb.Fatal(err)
	}
	req := httptest.NewRequestWithContext(tb.Context(), http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Org-ID", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		tb.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	return rec.Body.Bytes()
}

// countNodes is the number of nodes in a response: the objects that have a
// name or a title, which are the users, the posts, their authors and their
// tags. It fails the test on a response with errors.
func countNodes(tb testing.TB, resp []byte) int {
	tb.Helper()
	var doc struct {
		Data   any   `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal(resp, &doc); err != nil || len(doc.Errors) > 0 {
		tb.Fatalf("response: %v, errors %v\n%s", err, doc.Errors, resp)
	}
	var walk func(v any) int
	walk = func(v any) int {
		n := 0
		switch v := v.(type) {
		case map[string]any:
			_, name := v["name"]
			_, title := v["title"]
			if name || title {
				n++
			}
			for _, c := range v {
				n += walk(c)
			}
		case []any:
			for _, c := range v {
				n += walk(c)
			}
		}
		return n
	}
	return walk(doc.Data)
}

// BenchmarkNestedRequest serves nested at 10 and at 50 users. stmts/op is the
// statements a request sends, which must not grow with the users, and
// nodes/op the nodes the response holds, which the allocations follow.
func BenchmarkNestedRequest(b *testing.B) {
	h, stmts := benchHandler(b)
	for _, n := range []int{10, 50} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			nodes := countNodes(b, serveNested(b, h, n))
			stmts.Store(0)
			b.ReportAllocs()
			for b.Loop() {
				serveNested(b, h, n)
			}
			b.ReportMetric(float64(stmts.Load())/float64(b.N), "stmts/op")
			b.ReportMetric(float64(nodes), "nodes/op")
		})
	}
}

// TestNestedRequestBudget is the benchmark's target as a test: the statements
// of a nested request do not grow with its parents, and its allocations grow
// with its rows and not with its parents.
func TestNestedRequestBudget(t *testing.T) {
	h, stmts := benchHandler(t)
	type measure struct {
		stmts  int64
		nodes  int
		allocs float64
	}
	run := func(n int) measure {
		nodes := countNodes(t, serveNested(t, h, n))
		stmts.Store(0)
		serveNested(t, h, n)
		sent := stmts.Load()
		return measure{sent, nodes, testing.AllocsPerRun(20, func() { serveNested(t, h, n) })}
	}
	small, large := run(10), run(50)
	t.Logf("n=10: %d statements, %d nodes, %.0f allocs (%.2f per node)", small.stmts, small.nodes, small.allocs, small.allocs/float64(small.nodes))
	t.Logf("n=50: %d statements, %d nodes, %.0f allocs (%.2f per node)", large.stmts, large.nodes, large.allocs, large.allocs/float64(large.nodes))

	// users, the posts' page, the posts' count, the authors, the tags' page.
	const want = 5
	if small.stmts != want || large.stmts != want {
		t.Errorf("statements: %d at n=10 and %d at n=50, want %d at both, flat in the parents", small.stmts, large.stmts, want)
	}
	perSmall, perLarge := small.allocs/float64(small.nodes), large.allocs/float64(large.nodes)
	if perLarge > 1.1*perSmall {
		t.Errorf("allocations per node grew with the parents: %.2f at n=10, %.2f at n=50", perSmall, perLarge)
	}
	if !pinnedGqlgenBuilt() {
		t.Skipf("the allocation budget is for gqlgen %s only", pinnedGqlgen)
	}
	if large.allocs > nestedAllocsBudget {
		t.Errorf("a request at n=50 made %.0f allocations, over the budget of %d", large.allocs, nestedAllocsBudget)
	}
}

// pinnedGqlgenBuilt reports whether this binary was built with the gqlgen the
// allocation budget is for.
func pinnedGqlgenBuilt() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, d := range info.Deps {
		if d.Path == "github.com/99designs/gqlgen" {
			if d.Replace != nil {
				d = d.Replace
			}
			return d.Version == pinnedGqlgen
		}
	}
	return false
}
