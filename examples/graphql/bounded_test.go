package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// postVars posts a query with its variables, and returns the response.
func (s *testServer) postVars(t *testing.T, query string, vars map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		t.Fatal(err)
	}
	_, got := s.postBody(t, bytes.NewReader(body))
	return got
}

// seedRows adds n users, each the author of n posts in the tenant the requests name.
func (s *testServer) seedRows(t *testing.T, n int) {
	t.Helper()
	tx, err := s.sql.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	id := 100
	for u := range n {
		user := 100 + u
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO users (id, name, email, role, joined_at, password_hash) VALUES (?, 'name', 'mail', 'member', '2026-01-01T00:00:00Z', 'x')`, user); err != nil {
			t.Fatal(err)
		}
		for range n {
			id++
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO posts (id, org_id, author_id, title, status, views) VALUES (?, 1, ?, 'title', 'draft', 1)`, id, user); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// listOf is n of v, as a variable.
func listOf(n int, v any) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// deepWhere is depth nots.
func deepWhere(depth int) string {
	return strings.Repeat("{not:", depth) + "{}" + strings.Repeat("}", depth)
}

// deepWhereVar is deepWhere, as a variable.
func deepWhereVar(depth int) map[string]any {
	v := map[string]any{}
	for range depth {
		v = map[string]any{"not": v}
	}
	return v
}

// longName is a name of the most bytes a query may give one, ended by i.
func longName(i int) string {
	s := fmt.Sprintf("a%d", i)
	return s + strings.Repeat("a", 128-len(s))
}

// TestRequestBounded: the claim of decision 0027. A request allocates under a stated
// budget, whatever its shape, under the defaults: each shape is what the limits let
// through at their most, or just over, with what it does to gqlgen measured from the
// outside. A shape marked refused is first put to the check alone, so that one which
// stops being refused fails here, before it can allocate what it would.
func TestRequestBounded(t *testing.T) {
	s := newServer(t)
	s.seedRows(t, 100)
	e := veloxgql.NewExtension(&velox.DB{})
	frags := func(n int, f func(i int) string) string { return repeat(n, f) }
	spreads := func(n int) string { return repeat(n, func(i int) string { return fmt.Sprintf("...F%d", i) }) }
	field := `id name role joinedAt`
	alias := func(n int) string { return repeat(n, func(i int) string { return longName(i) + ":name" }) }
	for _, c := range []struct {
		name    string
		query   string
		vars    map[string]any
		want    string // in the response
		refused bool
		mb      int
	}{
		// Finding by finding, first the attack as it was, then what the limit leaves.
		{name: "7000 distinct spreads", query: onUser(spreads(7000)), want: "BAD_USER_INPUT", refused: true, mb: 8},
		{name: "500 distinct spreads, the most", query: `{ users(first: 1) { edges { node { ` + spreads(500) + ` } } } }` + frags(500, func(i int) string { return fmt.Sprintf("fragment F%d on User { id name }", i) }), want: `"data":{"users"`, mb: 100},
		{name: "500 spreads of nothing", query: onUser(spreads(500)), want: "GRAPHQL_VALIDATION_FAILED", mb: 100},
		{name: "2000 spreads of one fragment", query: `{ users(first: 1) { edges { node { ` + strings.Repeat("...F ", 2000) + ` } } } } fragment F on User { id }`, want: `"data":{"users"`, mb: 50},
		{name: "a chain of 490 fragments", query: onUser("...F0") + frags(490, func(i int) string { return fmt.Sprintf("fragment F%d on User { ...F%d }", i, i+1) }) + `fragment F490 on User { id }`, want: `"data":{"users"`, mb: 100},
		{name: "200 operations over 400 fragments", query: frags(200, func(i int) string { return fmt.Sprintf("query Q%d { users { edges { node { ...F0 } } } }", i) }) + frags(400, func(i int) string { return fmt.Sprintf("fragment F%d on User { ...F%d }", i, i+1) }) + `fragment F400 on User { id }`, want: "GRAPHQL_VALIDATION_FAILED", mb: 100},

		{name: "a where 3700 deep", query: `{ posts(where: ` + deepWhere(3700) + `) { totalCount } }`, want: "BAD_USER_INPUT", refused: true, mb: 8},
		{name: "a where 60 deep", query: `{ posts(where: ` + deepWhere(60) + `) { totalCount } }`, want: "BAD_USER_INPUT", mb: 20},
		{name: "a where 60 deep, by a variable", query: `query($w: PostWhereInput) { posts(where: $w) { totalCount } }`, vars: map[string]any{"w": deepWhereVar(60)}, want: "BAD_USER_INPUT", mb: 20},
		{name: "a where 3700 deep, by a variable", query: `query($w: PostWhereInput) { posts(where: $w) { totalCount } }`, vars: map[string]any{"w": deepWhereVar(3700)}, want: "BAD_USER_INPUT", refused: true, mb: 8},
		{name: "a list 60 deep and 6000 wide", query: `{ posts(where: {titleIn: ` + strings.Repeat("[", 60) + strings.Repeat(`"x",`, 6000) + strings.Repeat("]", 60) + `}) { totalCount } }`, want: "GRAPHQL_VALIDATION_FAILED", mb: 100},
		{name: "selections 64 deep", query: `{ ` + strings.Repeat("a { ", 63) + `b` + strings.Repeat(" }", 63) + ` }`, want: "GRAPHQL_VALIDATION_FAILED", mb: 8},
		{name: "a list type 64 deep", query: `query($v: ` + strings.Repeat("[", 63) + `Int` + strings.Repeat("]", 63) + `) { users { totalCount } }`, want: "GRAPHQL_VALIDATION_FAILED", mb: 8},

		{name: "an alias of 1 MB", query: `{ __schema { types { inputFields { ` + strings.Repeat("a", 1<<20-100) + `: name } } } }`, want: "BAD_USER_INPUT", refused: true, mb: 16},
		{name: "1990 long aliases", query: `{ users(first: 1) { edges { node { ` + alias(1990) + ` } } } }`, want: `"data":{"users"`, mb: 100},
		{name: "long aliases at MaxCost, on rows", query: `{ users(first: 100) { edges { node { ` + alias(990) + ` } } } }`, want: `"data":{"users"`, mb: 400},
		{name: "long aliases at MaxIntrospectionCost", query: `{ __schema { types { inputFields { ` + repeat(800, func(i int) string { return longName(i) + ":name" }) + ` } } } }`, want: "__schema", mb: 400},
		{name: "141 conflicts under a long alias", query: onUser(repeat(141, func(i int) string { return longName(0) + ":" + aliasField(i)[2:] })), want: "GRAPHQL_VALIDATION_FAILED", mb: 50},
		{name: "141 conflicts in 141 fragments", query: onUser(spreads(141)) + frags(141, func(i int) string {
			return fmt.Sprintf("fragment F%d on User { %s:%s }", i, longName(0), aliasField(i)[2:])
		}), want: "GRAPHQL_VALIDATION_FAILED", mb: 50},
		{name: "conflicts compared again under 8 inline fragments", query: onUser(strings.Repeat("... { ", 8) + repeat(47, aliasField) + strings.Repeat(" }", 8)), want: "GRAPHQL_VALIDATION_FAILED", mb: 50},
		{name: "800 fields that agree", query: onUser(strings.Repeat("a: id ", 800)), want: `"data":{"users"`, mb: 50},
		{name: "600 connections that agree", query: `{ users(first: 1) { edges { node { ` + strings.Repeat("a: posts(first: 1, where: {title: \"t\"}) { totalCount } ", 600) + ` } } } }`, want: "COST_LIMIT_EXCEEDED", mb: 100},
		{name: "1900 unknown fields", query: onUser(repeat(1900, func(i int) string { return longName(i) })), want: "GRAPHQL_VALIDATION_FAILED", mb: 50},
		{name: "3000 unknown arguments", query: `{ users(` + repeat(3000, func(i int) string { return fmt.Sprintf("a%d: 1", i) }) + `) { totalCount } }`, want: "GRAPHQL_VALIDATION_FAILED", mb: 50},
		{name: "4000 unknown directives", query: `{ users { totalCount ` + strings.Repeat("@d ", 4000) + ` } }`, want: "GRAPHQL_VALIDATION_FAILED", mb: 50},
		{name: "2000 undefined variables", query: `{ users(` + repeat(2000, func(i int) string { return fmt.Sprintf("a%d: $v%d", i, i) }) + `) { totalCount } }`, want: "GRAPHQL_VALIDATION_FAILED", mb: 50},
		{name: "2500 variables, never used", query: `query(` + repeat(2500, func(i int) string { return fmt.Sprintf("$v%d: Int", i) }) + `) { users { totalCount } }`, want: "GRAPHQL_VALIDATION_FAILED", mb: 50},
		{name: "1990 operations", query: repeat(1990, func(i int) string { return fmt.Sprintf("query Q%d { id }", i) }), want: "GRAPHQL_VALIDATION_FAILED", mb: 50},
		{name: "a string echoed by errors", query: `{ users(` + repeat(15, func(i int) string { return fmt.Sprintf(`first: "%s"`, strings.Repeat("s", 64<<10)) }) + `) { totalCount } }`, want: "GRAPHQL_VALIDATION_FAILED", mb: 100},
		{name: "or: [] of 4900 literal objects", query: `{ posts(where: {or: [` + strings.Repeat("{},", 4900) + `]}) { totalCount } }`, want: "BAD_USER_INPUT", mb: 100},

		{name: "or: [] of 95000 by a variable", query: `query($w: PostWhereInput) { posts(where: $w) { totalCount } }`, vars: map[string]any{"w": map[string]any{"or": listOf(95000, map[string]any{})}}, want: "BAD_USER_INPUT", refused: true, mb: 32},
		{name: "or: [] of 8300 by a variable", query: `query($w: PostWhereInput) { posts(where: $w) { totalCount } }`, vars: map[string]any{"w": map[string]any{"or": listOf(8300, map[string]any{})}}, want: "BAD_USER_INPUT", mb: 150},
		{name: "or: [] of 8300, at every use", query: `query($w: PostWhereInput) { ` + repeat(50, func(i int) string { return fmt.Sprintf("a%d: posts(where: $w) { totalCount }", i) }) + ` }`, vars: map[string]any{"w": map[string]any{"or": listOf(8300, map[string]any{})}}, want: "COST_LIMIT_EXCEEDED", mb: 150},
		{name: "or: [] of 1600, at each of 20 uses", query: `query($w: PostWhereInput) { ` + repeat(20, func(i int) string { return fmt.Sprintf("a%d: posts(where: $w) { totalCount }", i) }) + ` }`, vars: map[string]any{"w": map[string]any{"or": listOf(1600, map[string]any{})}}, want: "BAD_USER_INPUT", mb: 300},
		{name: "24000 IDs", query: `query($v: [ID!]!) { nodes(ids: $v) { id } }`, vars: map[string]any{"v": listOf(24000, post1)}, want: "BAD_USER_INPUT", mb: 50},
		{name: "a string of 1 MB at each use", query: `query($v: String!) { ` + repeat(30, func(i int) string { return fmt.Sprintf("a%d: posts(where: {title: $v}) { totalCount }", i) }) + ` }`, vars: map[string]any{"v": strings.Repeat("s", 1<<20-5000)}, want: `"data":{`, mb: 100},

		{name: "two levels of pages", query: `{ users(first: 100) { edges { node { ` + field + ` posts(first: 100) { edges { node { id title status views publishedAt } } } } } } }`, want: `"data":{"users"`, mb: 300},
	} {
		if c.refused {
			var vars map[string]any
			if c.vars != nil {
				vars = c.vars
			}
			if err := e.MutateOperationParameters(context.Background(), &graphql.RawParams{Query: c.query, Variables: vars}); err == nil {
				t.Errorf("%s: not refused", c.name)
				continue
			}
		}
		var got string
		n, d := allocated(func() { got = s.postVars(t, c.query, c.vars) })
		t.Logf("%-52s %8d B: %7.1f MB, %10v, %9d B of response, %5d errors", c.name, len(c.query), float64(n)/(1<<20), d.Round(time.Microsecond), len(got), strings.Count(got, `"message"`))
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: no %s in %.300s", c.name, c.want, got)
		}
		if n > uint64(c.mb)<<20 {
			t.Errorf("%s: allocated %d MB, the budget is %d", c.name, n>>20, c.mb)
		}
	}
}
