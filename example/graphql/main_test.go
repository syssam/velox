package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// The global IDs of the seed's rows: base64url of "<Type>:<key>".
const (
	user1  = "VXNlcjox"   // User:1, Ada
	user2  = "VXNlcjoy"   // User:2, Brian
	user3  = "VXNlcjoz"   // User:3, Chen
	user9  = "VXNlcjo5"   // User:9, no such user
	post1  = "UG9zdDox"   // Post:1, acme's Alpha
	post5  = "UG9zdDo1"   // Post:5, globex's Globex plans
	label1 = "TGFiZWw6MQ" // Label:1, the Tag go
	label2 = "TGFiZWw6Mg" // Label:2, the Tag sql
)

// userCursor is the cursor of the user with this key in a connection to User
// in its default order, the key's.
func userCursor(key int64) string {
	return veloxgql.EncodeCursor(veloxgql.Cursor{Type: "User", Key: key})
}

// testServer is the example's GraphQL endpoint on a seeded database, behind
// the example's tenant middleware, which sets the tenant from X-Org-ID, and
// the statements each request sends, which an Around hook records.
type testServer struct {
	url   string
	mu    sync.Mutex
	stmts []string
}

func newServer(t *testing.T) *testServer {
	t.Helper()
	s := &testServer{}
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	recording := false
	db, err := open(t.Context(), sqlDB, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		s.mu.Lock()
		if recording {
			s.stmts = append(s.stmts, fmt.Sprintf("%s %v", st.SQL, st.Args))
		}
		s.mu.Unlock()
		return next(ctx)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := seed(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	recording = true
	srv := httptest.NewServer(orgFromHeader(server(db)))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// request is one GraphQL request and what it must give: the response, and
// the statements it sends. org is the X-Org-ID header, none when empty.
type request struct {
	org   string
	query string
	vars  map[string]any
	want  string   // the response's JSON, compared after compacting
	stmts []string // the statements sent, "<SQL> [<args>]", sorted
}

// do sends r and checks the response and the statements. The statements are
// compared sorted: gqlgen resolves sibling fields concurrently, so the
// per-parent queries of an edge run in no fixed order.
func (s *testServer) do(t *testing.T, r request) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": r.query, "variables": r.vars})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.org != "" {
		req.Header.Set("X-Org-ID", r.org)
	}
	s.mu.Lock()
	s.stmts = nil
	s.mu.Unlock()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if g, w := compact(t, string(got)), compact(t, r.want); g != w {
		t.Errorf("%s\nresponse:\n got  %s\n want %s", r.query, g, w)
	}
	s.mu.Lock()
	stmts := slices.Sorted(slices.Values(s.stmts))
	s.mu.Unlock()
	want := slices.Sorted(slices.Values(r.stmts))
	if !slices.Equal(stmts, want) {
		t.Errorf("%s\nstatements:\n got  %s\n want %s", r.query, strings.Join(stmts, "\n      "), strings.Join(want, "\n      "))
	}
}

func compact(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return b.String()
}

// The SELECTs of each entity, up to WHERE: velox reads every column (Plan 4
// reads only the selected ones).
const (
	selUsers = `SELECT "id", "name", "email", "role", "joined_at", "password_hash" FROM "users"`
	selPosts = `SELECT "id", "org_id", "author_id", "title", "status", "views", "published_at" FROM "posts"`
	selTags  = `SELECT "id", "name" FROM "tags"`
)

// TestNode looks up one node of each type by its global ID: one query on its
// entity each, and the post's author edge one more.
func TestNode(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org: "1",
		query: `{
			u: node(id: "` + user1 + `") { id ... on User { name email role joinedAt } }
			p: node(id: "` + post1 + `") { id ... on Post { title status views publishedAt author { name } } }
			l: node(id: "` + label1 + `") { id ... on Label { name } }
		}`,
		want: `{"data": {
			"u": {"id": "` + user1 + `", "name": "Ada", "email": "ada@example.com", "role": "ADMIN", "joinedAt": "2026-01-05T09:00:00Z"},
			"p": {"id": "` + post1 + `", "title": "Alpha", "status": "PUBLISHED", "views": 10, "publishedAt": "2026-04-01T09:00:00Z", "author": {"name": "Ada"}},
			"l": {"id": "` + label1 + `", "name": "go"}
		}}`,
		stmts: []string{
			selUsers + ` WHERE "id" = ? LIMIT ? [1 2]`,
			selPosts + ` WHERE "id" = ? AND "org_id" = ? LIMIT ? [1 1 2]`,
			selUsers + ` WHERE "id" = ? LIMIT ? [1 2]`, // the author
			selTags + ` WHERE "id" = ? LIMIT ? [1 2]`,
		},
	})
}

// TestNodes looks up several nodes: one query per type, in the input's order,
// with null for an ID with no node, and for globex's post, which acme cannot
// see.
func TestNodes(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `{ nodes(ids: ["` + user1 + `", "` + user9 + `", "` + post5 + `", "` + label2 + `", "` + user3 + `"]) { id } }`,
		want:  `{"data": {"nodes": [{"id": "` + user1 + `"}, null, null, {"id": "` + label2 + `"}, {"id": "` + user3 + `"}]}}`,
		stmts: []string{
			selUsers + ` WHERE "id" IN (?, ?, ?) LIMIT ? [1 9 3 4]`,
			selPosts + ` WHERE "id" IN (?) AND "org_id" = ? LIMIT ? [5 1 2]`,
			selTags + ` WHERE "id" IN (?) LIMIT ? [2 2]`,
		},
	})
}

// TestBadIDs: an ID of another type where a User ID is expected, a
// malformed ID, and a cursor taken on another type each fail before any
// query runs.
func TestBadIDs(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `{ posts(where: {authorID: "` + post1 + `"}) { totalCount } }`,
		want: `{"errors": [{"message": "veloxgql: PostWhereInput.authorID: User ID: ID of another type", "path": ["posts"]}],
			"data": null}`,
	})
	s.do(t, request{
		query: `{ node(id: "VXNlcjox!") { id } }`,
		want: `{"errors": [{"message": "veloxgql: ID: malformed ID: not canonical base64url without padding", "path": ["node"]}],
			"data": {"node": null}}`,
	})
	postCursor := veloxgql.EncodeCursor(veloxgql.Cursor{Type: "Post", Key: int64(1)})
	s.do(t, request{
		query: `query($c: Cursor) { users(first: 1, after: $c) { totalCount } }`,
		vars:  map[string]any{"c": postCursor},
		want:  `{"errors": [{"message": "veloxgql: User cursor: malformed cursor: the key is an ID of another type", "path": ["users"]}], "data": null}`,
	})
}

// TestPaging pages the root connection to User forward, then backward, in
// its default order, the key's. Each page reads one row more than it returns,
// to set hasNextPage or hasPreviousPage.
func TestPaging(t *testing.T) {
	s := newServer(t)
	const q = `query($first: Int, $after: Cursor, $last: Int, $before: Cursor) {
		users(first: $first, after: $after, last: $last, before: $before) {
			edges { cursor node { id name } }
			pageInfo { hasNextPage hasPreviousPage startCursor endCursor }
		}
	}`
	s.do(t, request{
		query: q,
		vars:  map[string]any{"first": 2},
		want: fmt.Sprintf(`{"data": {"users": {
			"edges": [{"cursor": %[1]q, "node": {"id": %[3]q, "name": "Ada"}}, {"cursor": %[2]q, "node": {"id": %[4]q, "name": "Brian"}}],
			"pageInfo": {"hasNextPage": true, "hasPreviousPage": false, "startCursor": %[1]q, "endCursor": %[2]q}
		}}}`, userCursor(1), userCursor(2), user1, user2),
		stmts: []string{selUsers + ` ORDER BY "id" LIMIT ? [3]`},
	})
	s.do(t, request{
		query: q,
		vars:  map[string]any{"first": 2, "after": userCursor(2)},
		want: fmt.Sprintf(`{"data": {"users": {
			"edges": [{"cursor": %[1]q, "node": {"id": %[2]q, "name": "Chen"}}],
			"pageInfo": {"hasNextPage": false, "hasPreviousPage": false, "startCursor": %[1]q, "endCursor": %[1]q}
		}}}`, userCursor(3), user3),
		stmts: []string{selUsers + ` WHERE ("id" > ?) ORDER BY "id" LIMIT ? [2 3]`},
	})
	// Backward: last reverses the order and the rows read.
	s.do(t, request{
		query: q,
		vars:  map[string]any{"last": 1},
		want: fmt.Sprintf(`{"data": {"users": {
			"edges": [{"cursor": %[1]q, "node": {"id": %[2]q, "name": "Chen"}}],
			"pageInfo": {"hasNextPage": false, "hasPreviousPage": true, "startCursor": %[1]q, "endCursor": %[1]q}
		}}}`, userCursor(3), user3),
		stmts: []string{selUsers + ` ORDER BY "id" DESC LIMIT ? [2]`},
	})
	s.do(t, request{
		query: q,
		vars:  map[string]any{"last": 2, "before": userCursor(3)},
		want: fmt.Sprintf(`{"data": {"users": {
			"edges": [{"cursor": %[1]q, "node": {"id": %[3]q, "name": "Ada"}}, {"cursor": %[2]q, "node": {"id": %[4]q, "name": "Brian"}}],
			"pageInfo": {"hasNextPage": false, "hasPreviousPage": false, "startCursor": %[1]q, "endCursor": %[2]q}
		}}}`, userCursor(1), userCursor(2), user1, user2),
		stmts: []string{selUsers + ` WHERE ("id" < ?) ORDER BY "id" DESC LIMIT ? [3 3]`},
	})
}

// TestOrderAndWhere orders and filters root connections: a list orderBy
// (MultiOrder) whose tie on views the title breaks, and a where input
// through an edge.
func TestOrderAndWhere(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org: "1",
		query: `{ posts(
			orderBy: [{field: VIEWS, direction: DESC}, {field: TITLE}],
			where: {status: PUBLISHED, titleNEQ: "Delta", publishedAtGTE: "2026-04-01T00:00:00Z"}
		) { edges { node { title views } } } }`,
		want: `{"data": {"posts": {"edges": [{"node": {"title": "Alpha", "views": 10}}, {"node": {"title": "Gamma", "views": 10}}]}}}`,
		stmts: []string{
			selPosts + ` WHERE ("title" <> ? AND "status" = ? AND "published_at" >= ?) AND "org_id" = ? ORDER BY "views" DESC, "title", "id" LIMIT ? [Delta published 2026-04-01 00:00:00 +0000 UTC 1 101]`,
		},
	})
	s.do(t, request{
		org: "1",
		query: `{ users(
			where: {or: [{hasPostsWith: [{titleHasPrefix: "G"}]}, {role: ADMIN}]},
			orderBy: {field: NAME, direction: DESC}
		) { edges { node { name } } } }`,
		want: `{"data": {"users": {"edges": [{"node": {"name": "Brian"}}, {"node": {"name": "Ada"}}]}}}`,
		stmts: []string{
			selUsers + ` WHERE (EXISTS (SELECT 1 FROM "posts" AS "vx1" WHERE "vx1"."author_id" = "users"."id" AND "title" LIKE ? ESCAPE '\' AND "org_id" = ?) OR "role" = ?) ORDER BY "name" DESC, "id" LIMIT ? [G% 1 admin 101]`,
		},
	})
}

// TestNestedEdges reads edge connections two levels down, the many-to-many
// Tags among them: with no batching yet (Plan 4), each parent's edge runs its
// own queries.
func TestNestedEdges(t *testing.T) {
	s := newServer(t)
	postsOf := selPosts + ` WHERE "author_id" = ? AND "org_id" = ? ORDER BY "title", "id" LIMIT ? [%d 1 2]`
	countOf := `SELECT COUNT(*) FROM "posts" WHERE "author_id" = ? AND "org_id" = ? [%d 1]`
	tagsOf := selTags + ` WHERE EXISTS (SELECT 1 FROM "post_tags" AS "vx1" WHERE "vx1"."tag_id" = "tags"."id" AND EXISTS (SELECT 1 FROM "posts" AS "vx2" WHERE "vx2"."id" = "vx1"."post_id" AND "id" = ? AND "org_id" = ?)) ORDER BY "id" LIMIT ? [%d 1 101]`
	s.do(t, request{
		org: "1",
		query: `{ users(orderBy: {field: NAME}) { edges { node {
			name
			posts(first: 1, orderBy: [{field: TITLE}]) { totalCount edges { node { title tags { edges { node { name } } } } } }
		} } } }`,
		want: `{"data": {"users": {"edges": [
			{"node": {"name": "Ada", "posts": {"totalCount": 2, "edges": [{"node": {"title": "Alpha", "tags": {"edges": [{"node": {"name": "go"}}, {"node": {"name": "graphql"}}]}}}]}}},
			{"node": {"name": "Brian", "posts": {"totalCount": 1, "edges": [{"node": {"title": "Gamma", "tags": {"edges": [{"node": {"name": "sql"}}]}}}]}}},
			{"node": {"name": "Chen", "posts": {"totalCount": 1, "edges": [{"node": {"title": "Delta", "tags": {"edges": []}}}]}}}
		]}}}`,
		stmts: []string{
			selUsers + ` ORDER BY "name", "id" LIMIT ? [101]`,
			fmt.Sprintf(postsOf, 1), fmt.Sprintf(countOf, 1), fmt.Sprintf(tagsOf, 1),
			fmt.Sprintf(postsOf, 2), fmt.Sprintf(countOf, 2), fmt.Sprintf(tagsOf, 3),
			fmt.Sprintf(postsOf, 3), fmt.Sprintf(countOf, 3), fmt.Sprintf(tagsOf, 4),
		},
	})
}

// TestTotalCountOnly selects only totalCount: one COUNT, and no page query.
func TestTotalCountOnly(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `{ posts { totalCount } }`,
		want:  `{"data": {"posts": {"totalCount": 4}}}`,
		stmts: []string{`SELECT COUNT(*) FROM "posts" WHERE "org_id" = ? [1]`},
	})
}

// TestTenantIsolation reads the tenant-scoped Post through node, a root
// connection, a where input and the edges of User and Label: with acme's
// tenant, globex's post is never returned, and with globex's it is the only
// one. Without a tenant each fails with velox.ErrNoTenant.
func TestTenantIsolation(t *testing.T) {
	s := newServer(t)
	const q = `{
		node(id: "` + post5 + `") { id }
		posts { edges { node { title } } }
		globex: users(where: {hasPostsWith: [{title: "Globex plans"}]}) { totalCount }
		users(where: {name: "Ada"}) { edges { node { posts { edges { node { title } } } } } }
		labels(where: {name: "go"}) { edges { node { posts { edges { node { title } } } } } }
	}`
	stmts := func(org int64) []string {
		return []string{
			fmt.Sprintf(selPosts+` WHERE "id" = ? AND "org_id" = ? LIMIT ? [5 %d 2]`, org),
			fmt.Sprintf(selPosts+` WHERE "org_id" = ? ORDER BY "id" LIMIT ? [%d 101]`, org),
			fmt.Sprintf(`SELECT COUNT(*) FROM "users" WHERE EXISTS (SELECT 1 FROM "posts" AS "vx1" WHERE "vx1"."author_id" = "users"."id" AND "title" = ? AND "org_id" = ?) [Globex plans %d]`, org),
			selUsers + ` WHERE "name" = ? ORDER BY "id" LIMIT ? [Ada 101]`,
			fmt.Sprintf(selPosts+` WHERE "author_id" = ? AND "org_id" = ? ORDER BY "id" LIMIT ? [1 %d 101]`, org),
			selTags + ` WHERE "name" = ? ORDER BY "id" LIMIT ? [go 101]`,
			fmt.Sprintf(selPosts+` WHERE EXISTS (SELECT 1 FROM "post_tags" AS "vx1" WHERE "vx1"."post_id" = "posts"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id" AND "id" = ?)) AND "org_id" = ? ORDER BY "id" LIMIT ? [1 %d 101]`, org),
		}
	}
	s.do(t, request{
		org:   "1",
		query: q,
		want: `{"data": {
			"node": null,
			"posts": {"edges": [{"node": {"title": "Alpha"}}, {"node": {"title": "Beta"}}, {"node": {"title": "Gamma"}}, {"node": {"title": "Delta"}}]},
			"globex": {"totalCount": 0},
			"users": {"edges": [{"node": {"posts": {"edges": [{"node": {"title": "Alpha"}}, {"node": {"title": "Beta"}}]}}}]},
			"labels": {"edges": [{"node": {"posts": {"edges": [{"node": {"title": "Alpha"}}]}}}]}
		}}`,
		stmts: stmts(acme),
	})
	s.do(t, request{
		org:   "2",
		query: q,
		want: `{"data": {
			"node": {"id": "` + post5 + `"},
			"posts": {"edges": [{"node": {"title": "Globex plans"}}]},
			"globex": {"totalCount": 1},
			"users": {"edges": [{"node": {"posts": {"edges": [{"node": {"title": "Globex plans"}}]}}}]},
			"labels": {"edges": [{"node": {"posts": {"edges": [{"node": {"title": "Globex plans"}}]}}}]}
		}}`,
		stmts: stmts(globex),
	})
	// Without a tenant, every statement on Post fails before it is sent. A
	// failed non-null field nulls its parent, up to data.
	const noTenant = "velox: gen.Post: no tenant in the context"
	for _, c := range []struct{ query, want string }{
		{`{ node(id: "` + post1 + `") { id } }`, `{"errors": [{"message": "` + noTenant + `", "path": ["node"]}], "data": {"node": null}}`},
		{`{ posts { edges { node { title } } } }`, `{"errors": [{"message": "` + noTenant + `", "path": ["posts", "edges"]}], "data": null}`},
		{`{ labels(where: {name: "go"}) { edges { node { posts { totalCount } } } } }`, `{"errors": [{"message": "` + noTenant + `", "path": ["labels", "edges", 0, "node", "posts", "totalCount"]}], "data": null}`},
	} {
		var stmts []string
		if strings.HasPrefix(c.query, "{ labels") {
			stmts = []string{selTags + ` WHERE "name" = ? ORDER BY "id" LIMIT ? [go 101]`}
		}
		s.do(t, request{query: c.query, want: c.want, stmts: stmts})
	}
}

// TestSensitiveFieldAbsent introspects the schema: User's password_hash,
// Sensitive in the schema, is in no type, where input or order, and Post's
// tenant column and foreign key are not fields (the author edge stands for
// the key). Tag is renamed: its type is Label, and there is no type Tag.
func TestSensitiveFieldAbsent(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		query: `{
			user: __type(name: "User") { fields { name } }
			where: __type(name: "UserWhereInput") { inputFields { name } }
			order: __type(name: "UserOrderField") { enumValues { name } }
			post: __type(name: "Post") { fields { name } }
			tag: __type(name: "Tag") { name }
		}`,
		want: `{"data": {
			"user": {"fields": [{"name": "id"}, {"name": "name"}, {"name": "email"}, {"name": "role"}, {"name": "joinedAt"}, {"name": "posts"}]},
			"where": {"inputFields": [
				{"name": "not"}, {"name": "and"}, {"name": "or"},
				{"name": "name"}, {"name": "nameNEQ"}, {"name": "nameIn"}, {"name": "nameNotIn"},
				{"name": "nameContains"}, {"name": "nameHasPrefix"}, {"name": "nameHasSuffix"},
				{"name": "role"}, {"name": "roleNEQ"}, {"name": "roleIn"}, {"name": "roleNotIn"},
				{"name": "hasPosts"}, {"name": "hasPostsWith"}
			]},
			"order": {"enumValues": [{"name": "NAME"}, {"name": "JOINED_AT"}]},
			"post": {"fields": [{"name": "id"}, {"name": "title"}, {"name": "status"}, {"name": "views"}, {"name": "publishedAt"}, {"name": "author"}, {"name": "tags"}]},
			"tag": null
		}}`,
	})
}

// TestRequestCost: veloxgql's limits each bound one argument, so the server
// also limits the cost of a whole request (extension.FixedComplexityLimit).
// A request over complexityLimit fails before it runs a statement; the
// introspection query GraphiQL sends is not counted, and still works.
func TestRequestCost(t *testing.T) {
	s := newServer(t)
	var b strings.Builder
	b.WriteString("{")
	for i := range complexityLimit { // each alias costs 2: users and totalCount
		fmt.Fprintf(&b, " u%d: users(first: 1) { totalCount }", i)
	}
	b.WriteString(" }")
	s.do(t, request{
		query: b.String(),
		want: fmt.Sprintf(`{"errors": [{"message": "operation has complexity %d, which exceeds the limit of %d",
			"extensions": {"code": "COMPLEXITY_LIMIT_EXCEEDED"}}], "data": null}`, 2*complexityLimit, complexityLimit),
	})
	s.do(t, request{
		query: `{ users(first: 1) { totalCount } }`,
		want:  `{"data": {"users": {"totalCount": 3}}}`,
		stmts: []string{`SELECT COUNT(*) FROM "users" []`},
	})
	body, err := json.Marshal(map[string]any{"query": introspection})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), `{"data":{"__schema"`) {
		t.Errorf("the introspection query: %.300s", got)
	}
}

// introspection is the query GraphiQL and most clients send for the schema.
const introspection = `query IntrospectionQuery {
	__schema {
		queryType { name }
		mutationType { name }
		subscriptionType { name }
		types { ...FullType }
		directives { name description locations args { ...InputValue } }
	}
}
fragment FullType on __Type {
	kind name description
	fields(includeDeprecated: true) { name description args { ...InputValue } type { ...TypeRef } isDeprecated deprecationReason }
	inputFields { ...InputValue }
	interfaces { ...TypeRef }
	enumValues(includeDeprecated: true) { name description isDeprecated deprecationReason }
	possibleTypes { ...TypeRef }
}
fragment InputValue on __InputValue { name description type { ...TypeRef } defaultValue }
fragment TypeRef on __Type {
	kind name
	ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } }
}`

// TestOrderTermsLimit: an orderBy list over the default MaxOrderTerms fails
// with the limit named, before any term is built or statement sent.
func TestOrderTermsLimit(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `{ posts(orderBy: [` + strings.Repeat(`{field: TITLE, direction: ASC}, `, veloxgql.DefaultLimits().MaxOrderTerms+1) + `]) { totalCount } }`,
		want: `{"errors": [{"message": "veloxgql: Post connection: bad pagination arguments: orderBy has 6 terms, and the most is 5", "path": ["posts"]}],
			"data": null}`,
	})
}
