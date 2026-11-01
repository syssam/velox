package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/examples/graphql/graph"
	"github.com/vektah/gqlparser/v2"
)

// allocated is what the process allocated while f ran, and how long f took.
func allocated(f func()) (uint64, time.Duration) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	f()
	d := time.Since(start)
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc, d
}

// postBody posts body and returns the status and the response.
func (s *testServer) postBody(t *testing.T, body io.Reader) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Org-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(got)
}

func (s *testServer) postQuery(t *testing.T, query string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	_, got := s.postBody(t, bytes.NewReader(body))
	return got
}

// repeat is f(0) to f(n-1), joined by a space.
func repeat(n int, f func(i int) string) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(f(i))
		b.WriteByte(' ')
	}
	return b.String()
}

// onUser is sels on a User, in the root field users.
func onUser(sels string) string { return `{ users { edges { node { ` + sels + ` } } } }` }

// aliasField is one of two fields under one response name: each pair of
// different ones conflicts.
func aliasField(i int) string { return "a:" + []string{"id", "name"}[i%2] }

// TestValidationAttacks: what would make gqlparser's validation quadratic is
// refused before it, cleanly and under 50 MB each, up to the bound of what it lets
// through. An unbounded one took 3.49 GB and returned 250 MB.
func TestValidationAttacks(t *testing.T) {
	s := newServer(t)
	const most = 50 << 20
	for _, c := range []struct{ name, query, want string }{
		{"12 KB of conflicts", onUser(repeat(2000, aliasField)), "BAD_USER_INPUT"},
		{"as many tokens as the limit", onUser(repeat(4990, func(int) string { return "a:id" })), "BAD_USER_INPUT"},
		{"as many distinct fields as the limit", onUser(repeat(5100, func(i int) string { return fmt.Sprintf("f%d", i) })), "BAD_USER_INPUT"},
		{"over the token limit", onUser(repeat(16000, func(int) string { return "id" })), "GRAPHQL_PARSE_FAILED"},
		// Over the pairs by one, then at them: validation's own errors, and all it makes of them.
		{"one over the pairs", onUser(repeat(202, aliasField)), "BAD_USER_INPUT"},
		{"at the pairs", onUser(repeat(200, aliasField)), "GRAPHQL_VALIDATION_FAILED"},
		{"at the pairs, each pair conflicting", onUser(repeat(99, func(i int) string {
			return fmt.Sprintf("a:posts(first: %d) { totalCount }", i)
		})), "GRAPHQL_VALIDATION_FAILED"},
		{"at the pairs, in fragments", onUser(repeat(200, func(i int) string { return fmt.Sprintf("...F%d", i) })) +
			repeat(200, func(i int) string { return fmt.Sprintf("fragment F%d on User { %s }", i, aliasField(i)) }), "GRAPHQL_VALIDATION_FAILED"},
		{"at the pairs, merged below", `{ ` + repeat(100, func(i int) string {
			return "u: users { edges { node { " + aliasField(i) + " } } }"
		}) + ` }`, "GRAPHQL_VALIDATION_FAILED"},
		{"unknown fields at the limit", onUser(repeat(4990, func(i int) string { return fmt.Sprintf("f%d", i) })), "GRAPHQL_VALIDATION_FAILED"},
		{"a DAG of fragments", onUser("...F0") + repeat(40, func(i int) string {
			return fmt.Sprintf("fragment F%d on User { friends: id x: id y { ...F%d } z { ...F%d } }", i, i+1, i+1)
		}) + `fragment F40 on User { id }`, "BAD_USER_INPUT"},
	} {
		var got string
		n, d := allocated(func() { got = s.postQuery(t, c.query) })
		t.Logf("%-40s %6d B of query: %7.1f MB, %10v, %d B of response", c.name, len(c.query), float64(n)/(1<<20), d.Round(time.Microsecond), len(got))
		if !strings.Contains(got, `"code":"`+c.want+`"`) {
			t.Errorf("%s: no %s in %.300s", c.name, c.want, got)
		}
		if n > most {
			t.Errorf("%s: allocated %d MB", c.name, n>>20)
		}
	}
}

// TestLargeQueriesPass: what the limits leave to ordinary clients: GraphiQL's
// query, and a large page of aliases, a shared fragment and a repeated field.
func TestLargeQueriesPass(t *testing.T) {
	s := newServer(t)
	if got := s.postQuery(t, introspection); !strings.HasPrefix(got, `{"data":{"__schema"`) {
		t.Errorf("GraphiQL's query: %.300s", got)
	}
	large := `query Large { ` + repeat(200, func(i int) string {
		return fmt.Sprintf("a%d: users(first: 2) { edges { cursor node { id name id ...U } } pageInfo { hasNextPage } }", i)
	}) + ` } fragment U on User { id name role joinedAt posts(first: 1) { totalCount } }`
	if got := s.postQuery(t, large); !strings.HasPrefix(got, `{"data":{"a0":`) || strings.Contains(got, `"errors"`) {
		t.Errorf("a large query, of %d B: %.300s", len(large), got)
	}
	s.seedRows(t, 100)
	// As a client's code generator writes one: fragments colocated with their
	// components, each repeating the common fields.
	common := `__typename id name role joinedAt a: name b: name c: name d: name e: name f: name g: name h: name i: name`
	for name, c := range map[string]struct {
		frags, spread int
		mb            uint64
	}{"50 fragments of 14 common fields": {50, 50, 32}, "150 fragments of 14 common fields": {150, 150, 64}, "340 fragments of 14 common fields": {340, 340, 200}} {
		codegen := `query Page { users(first: 2) { edges { node { ` + repeat(c.spread, func(i int) string { return fmt.Sprintf("...C%d", i) }) + ` } } } } ` +
			repeat(c.frags, func(i int) string { return fmt.Sprintf("fragment C%d on User { %s }", i, common) })
		var got string
		n, d := allocated(func() { got = s.postQuery(t, codegen) })
		t.Logf("%s: %d B: %.1f MB, %v", name, len(codegen), float64(n)/(1<<20), d.Round(time.Millisecond))
		if !strings.HasPrefix(got, `{"data":{"users":`) || strings.Contains(got, `"errors"`) {
			t.Errorf("%s: %.300s", name, got)
		}
		if n > c.mb<<20 {
			t.Errorf("%s: allocated %d MB", name, n>>20)
		}
	}
	// Two levels of full pages, 10 fields selected at each, that read all they ask.
	fields := func(of string) string {
		return `__typename id ` + repeat(8, func(i int) string { return fmt.Sprintf("a%d: %s", i, of) })
	}
	two := `{ users(first: 100) { edges { cursor node { ` + fields("name") + ` posts(first: 100) { edges { cursor node { ` + fields("title") + ` } } } } } } }`
	var got string
	n, d := allocated(func() { got = s.postQuery(t, two) })
	t.Logf("two levels of pages: %.1f MB, %v, %d B of response", float64(n)/(1<<20), d.Round(time.Millisecond), len(got))
	if !strings.HasPrefix(got, `{"data":{"users":`) || strings.Contains(got, `"errors"`) {
		t.Errorf("two levels of pages: %.300s", got)
	}
	if n > 200<<20 {
		t.Errorf("two levels of pages: allocated %d MB", n>>20)
	}
}

// TestBodyLimit: a body over MaxBodyBytes is refused, its length known or not, and
// one under it serves.
func TestBodyLimit(t *testing.T) {
	s := newServer(t)
	ids := `{"query":"{ __typename }","variables":{"ids":[` + strings.Repeat(`"abcdefgh",`, 200000) + `"x"]}}` // 2.2 MB
	for name, r := range map[string]func() io.Reader{
		"length known":   func() io.Reader { return strings.NewReader(ids) },
		"length unknown": func() io.Reader { return io.MultiReader(strings.NewReader(ids)) },
	} {
		var status int
		var got string
		n, _ := allocated(func() { status, got = s.postBody(t, r()) })
		if status != http.StatusRequestEntityTooLarge || !strings.Contains(got, `"code":"BAD_USER_INPUT"`) || !strings.Contains(got, "request is too large") {
			t.Errorf("%s: %d %.300s", name, status, got)
		}
		if n > 16<<20 {
			t.Errorf("%s: allocated %d MB", name, n>>20)
		}
	}
	small := `{"query":"{ __typename }","variables":{"ids":[` + strings.Repeat(`"abcdefgh",`, 24000) + `"x"]}}`
	if status, got := s.postBody(t, io.MultiReader(strings.NewReader(small))); status != http.StatusOK || !strings.Contains(got, `"__typename":"Query"`) {
		t.Errorf("under the limit: %d %.300s", status, got)
	}
}

// TestIntrospectionAtItsLimit: what one request costs at MaxIntrospectionCost,
// as a tenth of it, on the example's schema: GraphiQL's query, repeated under
// aliases to about 100000 units. decision 0027 holds the full figure.
func TestIntrospectionAtItsLimit(t *testing.T) {
	s := newServer(t)
	frags := introspection[strings.Index(introspection, "fragment FullType"):]
	const aliases = 29 // about 3500 units each
	q := `{ ` + repeat(aliases, func(i int) string { return fmt.Sprintf("a%d: __schema { types { ...FullType } }", i) }) + ` } ` + frags
	var got string
	n, d := allocated(func() { got = s.postQuery(t, q) })
	t.Logf("%d aliases: %.1f MB, %v, %d B of response", aliases, float64(n)/(1<<20), d.Round(time.Millisecond), len(got))
	if !strings.HasPrefix(got, `{"data":{"a0":`) {
		t.Errorf("%.300s", got)
	}
	if n > 256<<20 {
		t.Errorf("allocated %d MB", n>>20)
	}
}

// TestMutationStatementsCounted: what a mutation's input makes it send counts in
// its cost, as an upper bound: 30 updatePost with clearTags and addTagIDs cost
// what they send, not one statement each.
func TestMutationStatementsCounted(t *testing.T) {
	s := newServer(t)
	schema := graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}, Directives: graph.DirectiveRoot{Admin: admin}}).Schema()
	tag := `["` + label1 + `"]`
	for _, in := range []string{
		`title: "x"`,
		`addTagIDs: ` + tag,
		`removeTagIDs: ` + tag,
		`clearTags: true`,
		`title: "x", clearTags: true, addTagIDs: ` + tag,
	} {
		query := `mutation { ` + repeat(30, func(i int) string {
			return fmt.Sprintf(`a%d: updatePost(id: "%s", input: {%s}) { id }`, i, post1, in)
		}) + ` }`
		doc, errs := gqlparser.LoadQueryWithRules(schema, query, nil)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		s.mu.Lock()
		s.stmts = nil
		s.mu.Unlock()
		if got := s.postQuery(t, query); strings.Contains(got, `"errors"`) {
			t.Fatalf("%s: %.300s", in, got)
		}
		s.mu.Lock()
		sent := len(s.stmts)
		s.mu.Unlock()
		cost := veloxgql.Cost(schema, doc.Operations[0], nil, veloxgql.DefaultLimits())
		t.Logf("%-70s sent %3d, cost %d", in, sent, cost)
		if cost < 100*sent {
			t.Errorf("%s: sent %d statements, and cost %d", in, sent, cost)
		}
	}
	// nodes sends one for each type among its IDs, and a delete of a Post with tags
	// three, as a review measured when they counted as one each: 960 and 950 of them
	// fit the limit, and sent 2880 and 2850.
	for name, query := range map[string]string{
		"nodes of three types": `{ ` + repeat(30, func(i int) string {
			return fmt.Sprintf(`a%d: nodes(ids: ["%s", "%s", "%s"]) { id }`, i, user1, post1, label1)
		}) + ` }`,
		"deletePost": `mutation { ` + repeat(1, func(i int) string { return fmt.Sprintf(`a%d: deletePost(id: "%s")`, i, post1) }) + ` }`,
	} {
		doc, errs := gqlparser.LoadQueryWithRules(schema, query, nil)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		s.mu.Lock()
		s.stmts = nil
		s.mu.Unlock()
		if got := s.postQuery(t, query); strings.Contains(got, `"errors"`) {
			t.Fatalf("%s: %.300s", name, got)
		}
		s.mu.Lock()
		sent := len(s.stmts)
		s.mu.Unlock()
		cost := veloxgql.Cost(schema, doc.Operations[0], nil, veloxgql.DefaultLimits())
		t.Logf("%-70s sent %3d, cost %d", name, sent, cost)
		if cost < 100*sent || sent < 3 {
			t.Errorf("%s: sent %d statements, and cost %d", name, sent, cost)
		}
	}
}
