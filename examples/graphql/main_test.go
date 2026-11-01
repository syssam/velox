package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
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
func userCursor(key int64) string { return cursorOf("User", key) }

// cursorOf is the cursor of a row of GraphQL type typ, in its default order.
func cursorOf(typ string, key int64) string {
	s, err := veloxgql.EncodeCursor(veloxgql.Cursor{Type: typ, Key: key})
	if err != nil {
		panic(err) // the type and the key are given
	}
	return s
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
	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "graphql.db"))
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
	srv := httptest.NewServer(orgFromHeader(roleFromHeader(server(db))))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// request is one GraphQL request and what it must give: the response, and
// the statements it sends. org is the X-Org-ID header and role the X-Role
// header, none when empty.
type request struct {
	org   string
	role  string
	query string
	vars  map[string]any
	want  string   // the response's JSON, compared as canon gives it
	stmts []string // the statements sent, "<SQL> [<args>]", sorted
}

// do sends r, checks the response and the statements, and returns the
// response's body. The statements, and the response's errors, are
// compared sorted: gqlgen resolves sibling fields concurrently, so the
// per-parent queries of an edge run, and their errors arrive, in no fixed
// order.
func (s *testServer) do(t *testing.T, r request) string {
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
	if r.role != "" {
		req.Header.Set("X-Role", r.role)
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
	if g, w := canon(t, string(got)), canon(t, r.want); g != w {
		t.Errorf("%s\nresponse:\n got  %s\n want %s", r.query, g, w)
	}
	s.mu.Lock()
	stmts := slices.Sorted(slices.Values(s.stmts))
	s.mu.Unlock()
	want := slices.Sorted(slices.Values(r.stmts))
	if !slices.Equal(stmts, want) {
		t.Errorf("%s\nstatements:\n got  %s\n want %s", r.query, strings.Join(stmts, "\n      "), strings.Join(want, "\n      "))
	}
	return string(got)
}

// canon is s, a response's JSON, compacted, with its object keys sorted and
// its errors sorted by their JSON. An error's "locations" are gqlgen's, and
// differ between its versions, so they are dropped before the comparison.
func canon(t *testing.T, s string) string {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	if m, ok := v.(map[string]any); ok {
		if errs, ok := m["errors"].([]any); ok {
			for _, e := range errs {
				if em, ok := e.(map[string]any); ok {
					delete(em, "locations")
				}
			}
			key := func(e any) string { b, _ := json.Marshal(e); return string(b) }
			slices.SortFunc(errs, func(a, b any) int { return strings.Compare(key(a), key(b)) })
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The SELECTs of each entity, up to WHERE. A page or a node reads the columns
// its selection needs: its key, its foreign keys and its order fields'
// columns always (users, posts and tags), and each selected field's. A
// mutation's returned row, and a page whose selection holds a field
// velox-graphql did not write, read every column (selUsers, selPosts and
// selTags).
const (
	selUsers = `SELECT "id", "name", "email", "role", "joined_at", "password_hash" FROM "users"`
	selPosts = `SELECT "id", "org_id", "author_id", "title", "status", "views", "published_at" FROM "posts"`
	selTags  = `SELECT "id", "name", "rank" FROM "tags"`
	users    = `SELECT "id", "name", "joined_at" FROM "users"`
	posts    = `SELECT "id", "author_id", "title", "views", "org_id" FROM "posts"`
	tags     = `SELECT "id", "name" FROM "tags"`
)

// postsOf is the windowed load of the posts of users, ordered by order (its
// SQL), at most n of each; in is the users' placeholders. Its arguments
// are the users' keys, the tenant and n.
func postsOf(in, order string) string {
	return `SELECT "id", "author_id", "title", "views", "org_id" FROM (SELECT "id", "author_id", "title", "views", "org_id", ROW_NUMBER() OVER (PARTITION BY "author_id" ORDER BY ` + order + `) AS "vx_rn" FROM "posts" WHERE "author_id" IN (` + in + `) AND "org_id" = ?) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`
}

// tagsOf is the windowed load of the name of the tags of posts, through the
// join table, in key order; in is the posts' placeholders. Its arguments
// are the posts' keys and the most of each, MaxPage + 1.
func tagsOf(in string) string {
	return `SELECT "id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."name", "vx_j"."post_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."post_id" ORDER BY "tags"."id") AS "vx_rn" FROM "tags" JOIN "post_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."post_id" IN (` + in + `)) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`
}

// TestNode looks up one node of each type by its global ID: one query on its
// entity each, reading the columns selected, and the post's author edge one
// more, a batch of the one post.
func TestNode(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:  "1",
		role: "admin", // email is @admin
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
			`SELECT "id", "name", "joined_at", "email", "role" FROM "users" WHERE "id" = ? LIMIT ? [1 2]`,
			`SELECT "id", "author_id", "title", "views", "org_id", "status", "published_at" FROM "posts" WHERE "id" = ? AND "org_id" = ? LIMIT ? [1 1 2]`,
			users + ` WHERE "id" IN (?) ORDER BY "id" [1]`, // the author
			tags + ` WHERE "id" = ? LIMIT ? [1 2]`,
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
			users + ` WHERE "id" IN (?, ?, ?) LIMIT ? [1 9 3 4]`,
			posts + ` WHERE "id" IN (?) AND "org_id" = ? LIMIT ? [5 1 2]`,
			tags + ` WHERE "id" IN (?) LIMIT ? [2 2]`,
		},
	})
}

// TestBadIDs: an ID of another type where a User ID is expected, a
// malformed ID, and a cursor taken on another type each fail before any
// query runs. They are the client's errors, and none is logged.
func TestBadIDs(t *testing.T) {
	logged := captureLog(t)
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `{ posts(where: {authorID: "` + post1 + `"}) { totalCount } }`,
		want: `{"errors": [{"message": "PostWhereInput.authorID: User ID: ID of another type", "path": ["posts"], "extensions": {"code": "BAD_USER_INPUT"}}],
			"data": null}`,
	})
	s.do(t, request{
		query: `{ node(id: "VXNlcjox!") { id } }`,
		want: `{"errors": [{"message": "ID: malformed ID: not canonical base64url without padding", "path": ["node"], "extensions": {"code": "BAD_USER_INPUT"}}],
			"data": {"node": null}}`,
	})
	postCursor := cursorOf("Post", 1)
	s.do(t, request{
		query: `query($c: Cursor) { users(first: 1, after: $c) { totalCount } }`,
		vars:  map[string]any{"c": postCursor},
		want:  `{"errors": [{"message": "User cursor: malformed cursor: the key is an ID of another type", "path": ["users"], "extensions": {"code": "BAD_USER_INPUT"}}], "data": null}`,
	})
	if logged.Len() != 0 {
		t.Errorf("logged:\n%s", logged)
	}
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
		stmts: []string{users + ` ORDER BY "id" LIMIT ? [3]`},
	})
	s.do(t, request{
		query: q,
		vars:  map[string]any{"first": 2, "after": userCursor(2)},
		want: fmt.Sprintf(`{"data": {"users": {
			"edges": [{"cursor": %[1]q, "node": {"id": %[2]q, "name": "Chen"}}],
			"pageInfo": {"hasNextPage": false, "hasPreviousPage": false, "startCursor": %[1]q, "endCursor": %[1]q}
		}}}`, userCursor(3), user3),
		stmts: []string{users + ` WHERE ("id" > ?) ORDER BY "id" LIMIT ? [2 3]`},
	})
	// Backward: last reverses the order and the rows read.
	s.do(t, request{
		query: q,
		vars:  map[string]any{"last": 1},
		want: fmt.Sprintf(`{"data": {"users": {
			"edges": [{"cursor": %[1]q, "node": {"id": %[2]q, "name": "Chen"}}],
			"pageInfo": {"hasNextPage": false, "hasPreviousPage": true, "startCursor": %[1]q, "endCursor": %[1]q}
		}}}`, userCursor(3), user3),
		stmts: []string{users + ` ORDER BY "id" DESC LIMIT ? [2]`},
	})
	s.do(t, request{
		query: q,
		vars:  map[string]any{"last": 2, "before": userCursor(3)},
		want: fmt.Sprintf(`{"data": {"users": {
			"edges": [{"cursor": %[1]q, "node": {"id": %[3]q, "name": "Ada"}}, {"cursor": %[2]q, "node": {"id": %[4]q, "name": "Brian"}}],
			"pageInfo": {"hasNextPage": false, "hasPreviousPage": false, "startCursor": %[1]q, "endCursor": %[2]q}
		}}}`, userCursor(1), userCursor(2), user1, user2),
		stmts: []string{users + ` WHERE ("id" < ?) ORDER BY "id" DESC LIMIT ? [3 3]`},
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
			posts + ` WHERE ("title" <> ? AND "status" = ? AND "published_at" >= ?) AND "org_id" = ? ORDER BY "views" DESC, "title", "id" LIMIT ? [Delta published 2026-04-01 00:00:00 +0000 UTC 1 101]`,
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
			users + ` WHERE (EXISTS (SELECT 1 FROM "posts" AS "vx1" WHERE "vx1"."author_id" = "users"."id" AND "title" LIKE ? ESCAPE '!' AND "org_id" = ?) OR "role" = ?) ORDER BY "name" DESC, "id" LIMIT ? [G% 1 admin 101]`,
		},
	})
}

// nestedQuery reads edge connections two levels down under users(first:
// $n), the many-to-many Tags among them.
const nestedQuery = `query($n: Int) { users(first: $n, orderBy: {field: NAME}) { edges { node {
	name
	posts(first: 1, orderBy: [{field: TITLE}]) { totalCount edges { node { title tags { edges { node { name } } } } } }
} } } }`

// nestedStmts are the statements of nestedQuery under users(first: n): the
// users page, one windowed load of every user's posts page, one grouped
// count of their posts, and one windowed load of the tags of every post
// read. Each reads only the columns asked for. userKeys and postKeys are
// the keys of the users and the posts read.
func nestedStmts(n int, userKeys, postKeys string) []string {
	return []string{
		fmt.Sprintf(`%s ORDER BY "name", "id" LIMIT ? [%d]`, users, n+1),
		fmt.Sprintf(`%s [%s 1 2]`, postsOf(marks(userKeys), `"title", "id"`), userKeys),
		fmt.Sprintf(`SELECT "author_id", COUNT(*) FROM "posts" WHERE "author_id" IN (%s) AND "org_id" = ? GROUP BY "author_id" [%s 1]`, marks(userKeys), userKeys),
		fmt.Sprintf(`%s [%s 101]`, tagsOf(marks(postKeys)), postKeys),
	}
}

// marks are the placeholders of keys, a list of keys separated by spaces:
// "?, ?" for "1 2".
func marks(keys string) string {
	return strings.Repeat("?, ", len(strings.Fields(keys))-1) + "?"
}

// TestNestedEdges reads edge connections two levels down, the many-to-many
// Tags among them: one statement per edge per level, whatever the rows.
func TestNestedEdges(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: nestedQuery,
		vars:  map[string]any{"n": 3},
		want: `{"data": {"users": {"edges": [
			{"node": {"name": "Ada", "posts": {"totalCount": 2, "edges": [{"node": {"title": "Alpha", "tags": {"edges": [{"node": {"name": "go"}}, {"node": {"name": "graphql"}}]}}}]}}},
			{"node": {"name": "Brian", "posts": {"totalCount": 1, "edges": [{"node": {"title": "Gamma", "tags": {"edges": [{"node": {"name": "sql"}}]}}}]}}},
			{"node": {"name": "Chen", "posts": {"totalCount": 1, "edges": [{"node": {"title": "Delta", "tags": {"edges": []}}}]}}}
		]}}}`,
		stmts: nestedStmts(3, "1 2 3", "1 3 4"),
	})
}

// TestNoNPlusOne: nestedQuery sends the same four statements for one user
// as for three, only their arguments differing.
func TestNoNPlusOne(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: nestedQuery,
		vars:  map[string]any{"n": 1},
		want: `{"data": {"users": {"edges": [
			{"node": {"name": "Ada", "posts": {"totalCount": 2, "edges": [{"node": {"title": "Alpha", "tags": {"edges": [{"node": {"name": "go"}}, {"node": {"name": "graphql"}}]}}}]}}}
		]}}}`,
		stmts: nestedStmts(1, "1", "1"),
	})
	one, three := nestedStmts(1, "1", "1"), nestedStmts(3, "1 2 3", "1 3 4")
	// The SQL of each statement, with a list of placeholders as one.
	list := regexp.MustCompile(`\(\?(, \?)*\)`)
	sqlOf := func(stmts []string) []string {
		out := make([]string, len(stmts))
		for i, st := range stmts {
			sql, _, _ := strings.Cut(st, " [")
			out[i] = list.ReplaceAllString(sql, "(?…)")
		}
		slices.Sort(out)
		return out
	}
	if len(one) != 4 || !slices.Equal(sqlOf(one), sqlOf(three)) {
		t.Errorf("the statements of one user and of three differ:\n%s\n%s", strings.Join(one, "\n"), strings.Join(three, "\n"))
	}
}

// TestColumnsCollected: a page reads the columns of the fields selected, the
// key and the order fields' columns: never password_hash, nor email, which
// no one asked for.
func TestColumnsCollected(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		query: `{ users { edges { node { name } } } }`,
		want:  `{"data": {"users": {"edges": [{"node": {"name": "Ada"}}, {"node": {"name": "Brian"}}, {"node": {"name": "Chen"}}]}}}`,
		stmts: []string{users + ` ORDER BY "id" LIMIT ? [101]`},
	})
}

// TestAliasesBatch: the same edge twice under aliases, with different
// arguments, is two batches: one load each, and each alias its own page.
func TestAliasesBatch(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `{ users { edges { node { name a: posts(first: 1) { edges { node { title } } } b: posts(where: {title: "Beta"}) { edges { node { title } } } } } } }`,
		want: `{"data": {"users": {"edges": [
			{"node": {"name": "Ada", "a": {"edges": [{"node": {"title": "Alpha"}}]}, "b": {"edges": [{"node": {"title": "Beta"}}]}}},
			{"node": {"name": "Brian", "a": {"edges": [{"node": {"title": "Gamma"}}]}, "b": {"edges": []}}},
			{"node": {"name": "Chen", "a": {"edges": [{"node": {"title": "Delta"}}]}, "b": {"edges": []}}}
		]}}}`,
		stmts: []string{
			users + ` ORDER BY "id" LIMIT ? [101]`,
			// a, its first post each; b, the posts titled Beta.
			postsOf("?, ?, ?", `"id"`) + ` [1 2 3 1 2]`,
			`SELECT "id", "author_id", "title", "views", "org_id" FROM (SELECT "id", "author_id", "title", "views", "org_id", ROW_NUMBER() OVER (PARTITION BY "author_id" ORDER BY "id") AS "vx_rn" FROM "posts" WHERE "author_id" IN (?, ?, ?) AND "title" = ? AND "org_id" = ?) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn" [1 2 3 Beta 1 101]`,
		},
	})
}

// TestAppFieldReadsAllColumns: initials is the application's own field
// (app.graphqls), resolved from the name. velox-graphql cannot know what
// its resolver reads, so a page that selects it reads every column.
func TestAppFieldReadsAllColumns(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		query: `{ users(first: 1) { edges { node { initials } } } }`,
		want:  `{"data": {"users": {"edges": [{"node": {"initials": "A"}}]}}}`,
		stmts: []string{selUsers + ` ORDER BY "id" LIMIT ? [2]`},
	})
}

// TestBatchInMutation: a mutation's fields resolve one at a time, under the
// extension's lock, and its edges still batch, in its transaction: the new
// post is among Ada's, its tag counted, with one statement for the author,
// one posts load, one posts count and one tags count after the writes.
func TestBatchInMutation(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org: "1",
		query: `mutation { createPost(input: {title: "Zeta", authorID: "` + user1 + `", tagIDs: ["` + label1 + `"]}) {
			author { posts { totalCount edges { node { title tags { totalCount } } } } }
		} }`,
		want: `{"data": {"createPost": {"author": {"posts": {"totalCount": 3, "edges": [
			{"node": {"title": "Alpha", "tags": {"totalCount": 2}}},
			{"node": {"title": "Beta", "tags": {"totalCount": 0}}},
			{"node": {"title": "Zeta", "tags": {"totalCount": 1}}}
		]}}}}}`,
		stmts: []string{
			`SELECT COUNT(*) FROM "users" WHERE "id" = ? [1]`,
			`SELECT COUNT(*) FROM "tags" WHERE "id" IN (?) [1]`,
			`INSERT INTO "posts" ("org_id", "author_id", "title", "status", "views", "published_at") VALUES (?, ?, ?, ?, ?, ?) RETURNING "id" [1 1 Zeta draft 0 <nil>]`,
			`INSERT INTO "post_tags" ("post_id", "tag_id") VALUES (?, ?) ON CONFLICT ("post_id", "tag_id") DO NOTHING [6 1]`,
			// The author, the returned row's edge, alone; then its posts and
			// their tags, a batch each.
			users + ` WHERE "id" IN (?) LIMIT ? [1 1]`,
			postsOf("?", `"id"`) + ` [1 1 101]`,
			`SELECT "author_id", COUNT(*) FROM "posts" WHERE "author_id" IN (?) AND "org_id" = ? GROUP BY "author_id" [1 1]`,
			`SELECT "vx_j"."post_id", COUNT(*) FROM "tags" JOIN "post_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."post_id" IN (?, ?, ?) GROUP BY "vx_j"."post_id" [1 2 6]`,
		},
	})
}

// TestNodeEdgesBatch: the nodes nodes returns are one batch: their posts are
// counted in one statement.
func TestNodeEdgesBatch(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `{ nodes(ids: ["` + user1 + `", "` + user2 + `", "` + user3 + `"]) { ... on User { posts { totalCount } } } }`,
		want:  `{"data": {"nodes": [{"posts": {"totalCount": 2}}, {"posts": {"totalCount": 1}}, {"posts": {"totalCount": 1}}]}}`,
		stmts: []string{
			users + ` WHERE "id" IN (?, ?, ?) LIMIT ? [1 2 3 4]`,
			`SELECT "author_id", COUNT(*) FROM "posts" WHERE "author_id" IN (?, ?, ?) AND "org_id" = ? GROUP BY "author_id" [1 2 3 1]`,
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
			fmt.Sprintf(posts+` WHERE "id" = ? AND "org_id" = ? LIMIT ? [5 %d 2]`, org),
			fmt.Sprintf(posts+` WHERE "org_id" = ? ORDER BY "id" LIMIT ? [%d 101]`, org),
			fmt.Sprintf(`SELECT COUNT(*) FROM "users" WHERE EXISTS (SELECT 1 FROM "posts" AS "vx1" WHERE "vx1"."author_id" = "users"."id" AND "title" = ? AND "org_id" = ?) [Globex plans %d]`, org),
			users + ` WHERE "name" = ? ORDER BY "id" LIMIT ? [Ada 101]`,
			fmt.Sprintf(postsOf("?", `"id"`)+` [1 %d 101]`, org),
			tags + ` WHERE "name" = ? ORDER BY "id" LIMIT ? [go 101]`,
			// The label's posts, through the join table: the tenant's
			// condition is inside the window.
			fmt.Sprintf(`SELECT "id", "author_id", "title", "views", "org_id", "vx_owner" FROM (SELECT "posts"."id", "posts"."author_id", "posts"."title", "posts"."views", "posts"."org_id", "vx_j"."tag_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."tag_id" ORDER BY "posts"."id") AS "vx_rn" FROM "posts" JOIN "post_tags" AS "vx_j" ON "vx_j"."post_id" = "posts"."id" WHERE "vx_j"."tag_id" IN (?) AND "posts"."org_id" = ?) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn" [1 %d 101]`, org),
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
	// failed non-null field nulls its parent, up to data. The client is told
	// unauthenticated, with no entity named (velox.ErrNoTenant's text holds
	// gen.Post).
	const noTenant = `"message": "unauthenticated", "path": %s, "extensions": {"code": "UNAUTHENTICATED"}`
	for _, c := range []struct{ query, want string }{
		{`{ node(id: "` + post1 + `") { id } }`, `{"errors": [{` + fmt.Sprintf(noTenant, `["node"]`) + `}], "data": {"node": null}}`},
		{`{ posts { edges { node { title } } } }`, `{"errors": [{` + fmt.Sprintf(noTenant, `["posts", "edges"]`) + `}], "data": null}`},
		{`{ labels(where: {name: "go"}) { edges { node { posts { totalCount } } } } }`, `{"errors": [{` + fmt.Sprintf(noTenant, `["labels", "edges", 0, "node", "posts", "totalCount"]`) + `}], "data": null}`},
	} {
		var stmts []string
		if strings.HasPrefix(c.query, "{ labels") {
			stmts = []string{tags + ` WHERE "name" = ? ORDER BY "id" LIMIT ? [go 101]`}
		}
		if body := s.do(t, request{query: c.query, want: c.want, stmts: stmts}); strings.Contains(body, "velox:") {
			t.Errorf("%s: the client sees velox's text: %s", c.query, body)
		}
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
			"user": {"fields": [{"name": "id"}, {"name": "name"}, {"name": "email"}, {"name": "role"}, {"name": "joinedAt"}, {"name": "posts"}, {"name": "initials"}]},
			"where": {"inputFields": [
				{"name": "not"}, {"name": "and"}, {"name": "or"},
				{"name": "id"}, {"name": "idNEQ"}, {"name": "idIn"}, {"name": "idNotIn"},
				{"name": "idGT"}, {"name": "idGTE"}, {"name": "idLT"}, {"name": "idLTE"},
				{"name": "name"}, {"name": "nameNEQ"}, {"name": "nameIn"}, {"name": "nameNotIn"},
				{"name": "nameContains"}, {"name": "nameHasPrefix"}, {"name": "nameHasSuffix"},
				{"name": "email"}, {"name": "emailNEQ"}, {"name": "emailIn"}, {"name": "emailNotIn"},
				{"name": "emailContains"}, {"name": "emailHasPrefix"}, {"name": "emailHasSuffix"},
				{"name": "role"}, {"name": "roleNEQ"}, {"name": "roleIn"}, {"name": "roleNotIn"},
				{"name": "hasPosts"}, {"name": "hasPostsWith"}
			]},
			"order": {"enumValues": [{"name": "NAME"}, {"name": "JOINED_AT"}]},
			"post": {"fields": [{"name": "id"}, {"name": "title"}, {"name": "status"}, {"name": "views"}, {"name": "publishedAt"}, {"name": "author"}, {"name": "tags"}]},
			"tag": null
		}}`,
	})
}

// TestRequestCost: veloxgql's limits each bound one argument, so
// Limits.MaxCost bounds a whole request. A request over it fails with
// COST_LIMIT_EXCEEDED before it runs a statement; the same shape with smaller
// pages runs; the introspection query GraphiQL sends is within the limit, and
// one that nests it over and over is not. A long request fails in the parser.
func TestRequestCost(t *testing.T) {
	s := newServer(t)
	// 100 users, each with 100 posts, each with 100 tags: a million rows.
	// users(first: 100) { edges { node { posts(first: 100) { edges { node {
	// tags(first: 100) { edges { node { name } } } } } } } } } } costs, in rows,
	// tags 1+1+100*2 = 202; posts 1+1+100*(1+202) = 20302; users
	// 1+1+100*(1+20302) = 2030302; and 3 statements, 300.
	s.do(t, request{
		query: `{ users(first: 100) { edges { node { posts(first: 100) { edges { node { tags(first: 100) { edges { node { name } } } } } } } } } }`,
		want: `{"errors": [{"message": "request is too costly: it costs 2030602, and the most is 100000",
			"extensions": {"code": "COST_LIMIT_EXCEEDED"}}], "data": null}`,
	})
	// Aliases of a COUNT with first: 0 run a statement each: 1000 of them cost
	// 2 fields and 2 statements each.
	var aliases strings.Builder
	for i := range 1000 {
		fmt.Fprintf(&aliases, "a%d: users(first: 0) { totalCount } ", i)
	}
	s.do(t, request{
		query: `{ ` + aliases.String() + ` }`,
		want: `{"errors": [{"message": "request is too costly: it costs 202000, and the most is 100000",
			"extensions": {"code": "COST_LIMIT_EXCEEDED"}}], "data": null}`,
	})
	// With first: 5 at each level the rows are few, and it runs.
	s.do(t, request{
		org:   "1",
		query: `{ users(first: 5) { edges { node { posts(first: 5) { edges { node { tags(first: 5) { totalCount } } } } } } } }`,
		want: `{"data": {"users": {"edges": [
			{"node": {"posts": {"edges": [{"node": {"tags": {"totalCount": 2}}}, {"node": {"tags": {"totalCount": 0}}}]}}},
			{"node": {"posts": {"edges": [{"node": {"tags": {"totalCount": 1}}}]}}},
			{"node": {"posts": {"edges": [{"node": {"tags": {"totalCount": 0}}}]}}}]}}}`,
		stmts: []string{
			users + ` ORDER BY "id" LIMIT ? [6]`,
			postsOf("?, ?, ?", `"id"`) + ` [1 2 3 1 6]`,
			`SELECT "vx_j"."post_id", COUNT(*) FROM "tags" JOIN "post_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."post_id" IN (?, ?, ?, ?) GROUP BY "vx_j"."post_id" [1 2 3 4]`,
		},
	})
	post := func(query string) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{"query": query})
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
		return string(got)
	}
	got := post(introspection)
	if !strings.HasPrefix(got, `{"data":{"__schema"`) {
		t.Errorf("the introspection query: %.300s", got)
	}
	// Introspection nested as deep as gqlparser's rule allows, under aliases.
	var nested strings.Builder
	for i := range 200 {
		fmt.Fprintf(&nested, "a%d: __schema { types { fields { name args { name } type { kind ofType { name fields { name type { kind name } } } } } } } ", i)
	}
	if got := post(`{ ` + nested.String() + ` }`); !strings.Contains(got, "COST_LIMIT_EXCEEDED") {
		t.Errorf("nested introspection: %.300s", got)
	}
	// More tokens than the server's limit: refused before it is parsed whole.
	if got := post(`{ ` + strings.Repeat("__typename ", 20000) + `}`); !strings.Contains(got, "exceeded token limit") {
		t.Errorf("a long request: %.300s", got)
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
// with the limit named, before any statement is sent.
func TestOrderTermsLimit(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `{ posts(orderBy: [` + strings.Repeat(`{field: TITLE, direction: ASC}, `, veloxgql.DefaultLimits().MaxOrderTerms+1) + `]) { totalCount } }`,
		want: `{"errors": [{"message": "Post connection: bad pagination arguments: orderBy has 6 terms, and the most is 5", "path": ["posts"], "extensions": {"code": "BAD_USER_INPUT"}}],
			"data": null}`,
	})
}

// TestPublishedPosts is the application's own connection, composed from a
// query it wrote (app.graphqls): under tenant 1 the published posts are
// Alpha, Gamma and Delta, and the connection keeps the base query's filter
// and the tenant scope.
func TestPublishedPosts(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `{ publishedPosts { totalCount edges { node { title } } } }`,
		want:  `{"data": {"publishedPosts": {"totalCount": 3, "edges": [{"node": {"title": "Alpha"}}, {"node": {"title": "Gamma"}}, {"node": {"title": "Delta"}}]}}}`,
		stmts: []string{
			posts + ` WHERE "status" = ? AND "org_id" = ? ORDER BY "id" LIMIT ? [published 1 101]`,
			`SELECT COUNT(*) FROM "posts" WHERE "status" = ? AND "org_id" = ? [published 1]`,
		},
	})
}

// TestFieldDirective: User.email is @admin, which main.go implements from the
// X-Role header. The directive guards the field and its where predicates
// alike: without it, a client could find an email it may not read by
// filtering on it, a prefix at a time. Refused, neither runs a statement.
func TestFieldDirective(t *testing.T) {
	s := newServer(t)
	const filter = `{ users(where: {emailHasPrefix: "a"}) { totalCount } }`
	const read = `{ users { edges { node { email } } } }`
	const forbidden = `"message": "the admin role is required", "extensions": {"code": "FORBIDDEN"}`
	for _, role := range []string{"", "member"} {
		s.do(t, request{
			role:  role,
			query: filter,
			want:  `{"errors": [{` + forbidden + `, "path": ["users", "where", "emailHasPrefix"]}], "data": null}`,
		})
		// Nested in not or in or, the predicate is guarded alike.
		s.do(t, request{
			role:  role,
			query: `{ users(where: {not: {emailHasPrefix: "a"}}) { totalCount } }`,
			want:  `{"errors": [{` + forbidden + `, "path": ["users", "where", "not", "emailHasPrefix"]}], "data": null}`,
		})
		s.do(t, request{
			role:  role,
			query: `{ users(where: {or: [{role: ADMIN}, {emailHasSuffix: "@example.com"}]}) { totalCount } }`,
			want:  `{"errors": [{` + forbidden + `, "path": ["users", "where", "or", 1, "emailHasSuffix"]}], "data": null}`,
		})
		s.do(t, request{
			role:  role,
			query: read,
			want: `{"errors": [
				{` + forbidden + `, "path": ["users", "edges", 0, "node", "email"]},
				{` + forbidden + `, "path": ["users", "edges", 1, "node", "email"]},
				{` + forbidden + `, "path": ["users", "edges", 2, "node", "email"]}
			], "data": null}`,
			stmts: []string{`SELECT "id", "name", "joined_at", "email" FROM "users" ORDER BY "id" LIMIT ? [101]`},
		})
	}
	s.do(t, request{
		role:  "admin",
		query: filter,
		want:  `{"data": {"users": {"totalCount": 1}}}`,
		stmts: []string{`SELECT COUNT(*) FROM "users" WHERE "email" LIKE ? ESCAPE '!' [a%]`},
	})
	s.do(t, request{
		role:  "admin",
		query: read,
		want: `{"data": {"users": {"edges": [
			{"node": {"email": "ada@example.com"}}, {"node": {"email": "brian@example.com"}}, {"node": {"email": "chen@example.com"}}
		]}}}`,
		stmts: []string{`SELECT "id", "name", "joined_at", "email" FROM "users" ORDER BY "id" LIMIT ? [101]`},
	})
}

// TestIntegerRange: Label.rank is an int8, and gqlgen's Int8 refuses an Int
// it cannot hold. The client sees gqlgen's text with BAD_USER_INPUT, its own
// input's error, before any statement; nothing is logged.
func TestIntegerRange(t *testing.T) {
	logged := captureLog(t)
	s := newServer(t)
	const tooBig = `"extensions": {"code": "BAD_USER_INPUT"}, "path": ["labels", "where", `
	s.do(t, request{
		query: `{ labels(where: {rankGT: 300}) { totalCount } }`,
		want:  `{"errors": [{"message": "300 overflows signed 8-bit integer", ` + tooBig + `"rankGT"]}], "data": null}`,
	})
	s.do(t, request{
		query: `query($r: Int) { labels(where: {rankGT: $r}) { totalCount } }`,
		vars:  map[string]any{"r": -129},
		want:  `{"errors": [{"message": "-129 overflows signed 8-bit integer", ` + tooBig + `"rankGT"]}], "data": null}`,
	})
	s.do(t, request{
		query: `{ labels(where: {rankIn: [1, 128]}) { totalCount } }`,
		want:  `{"errors": [{"message": "128 overflows signed 8-bit integer", ` + tooBig + `"rankIn", 1]}], "data": null}`,
	})
	s.do(t, request{
		query: `{ labels(where: {rankGT: 1}) { totalCount } }`,
		want:  `{"data": {"labels": {"totalCount": 2}}}`,
		stmts: []string{`SELECT COUNT(*) FROM "tags" WHERE "rank" > ? [1]`},
	})
	if logged.Len() != 0 {
		t.Errorf("logged:\n%s", logged)
	}
}

// captureLog makes slog's default logger write to the buffer it returns,
// at level info and above, until the test ends.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestCreatePost creates an acme post with an author and two tags, one of
// them named twice, which links once. Each ID is checked against what the
// caller may read before anything is written: one COUNT per edge. status
// takes its Default, views, Optional, its zero value, and the post is
// returned as written.
func TestCreatePost(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org: "1",
		query: `mutation { createPost(input: {title: "Epsilon", authorID: "` + user2 + `", tagIDs: ["` + label1 + `", "` + label2 + `", "` + label1 + `"]}) {
			title status author { name } tags { totalCount }
		} }`,
		want: `{"data": {"createPost": {"title": "Epsilon", "status": "DRAFT", "author": {"name": "Brian"}, "tags": {"totalCount": 2}}}}`,
		stmts: []string{
			`SELECT COUNT(*) FROM "users" WHERE "id" = ? [2]`,
			`SELECT COUNT(*) FROM "tags" WHERE "id" IN (?, ?) [1 2]`,
			`INSERT INTO "posts" ("org_id", "author_id", "title", "status", "views", "published_at") VALUES (?, ?, ?, ?, ?, ?) RETURNING "id" [1 2 Epsilon draft 0 <nil>]`,
			`INSERT INTO "post_tags" ("post_id", "tag_id") VALUES (?, ?), (?, ?) ON CONFLICT ("post_id", "tag_id") DO NOTHING [6 1 6 2]`,
			// The selection, of the returned row alone: the author, and the
			// tags' count, through the join table.
			users + ` WHERE "id" IN (?) LIMIT ? [2 1]`,
			`SELECT COUNT(*) FROM "tags" WHERE EXISTS (SELECT 1 FROM "post_tags" AS "vx1" WHERE "vx1"."tag_id" = "tags"."id" AND "post_id" IN (?)) [6]`,
		},
	})
}

// TestUpdatePost clears a post's publishedAt and removes one of its two
// tags, which it may read. An empty title fails Post's NotEmpty validator, named by its input
// field, before any statement.
func TestUpdatePost(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `mutation { updatePost(id: "` + post1 + `", input: {clearPublishedAt: true, removeTagIDs: ["` + label1 + `"]}) { title publishedAt tags { edges { node { name } } } } }`,
		want:  `{"data": {"updatePost": {"title": "Alpha", "publishedAt": null, "tags": {"edges": [{"node": {"name": "graphql"}}]}}}}`,
		stmts: []string{
			`UPDATE "posts" SET "published_at" = ? WHERE "id" = ? AND "org_id" = ? [<nil> 1 1]`,
			// The tags to remove the caller may read, then their join rows.
			`SELECT "id" FROM "tags" WHERE "id" IN (?) LIMIT ? [1 1]`,
			`DELETE FROM "post_tags" WHERE "post_id" = ? AND "tag_id" IN (?) [1 1]`,
			// The row read back, as written, and the selection's tags.
			selPosts + ` WHERE "id" = ? AND "org_id" = ? LIMIT ? [1 1 2]`,
			tags + ` WHERE EXISTS (SELECT 1 FROM "post_tags" AS "vx1" WHERE "vx1"."tag_id" = "tags"."id" AND "post_id" IN (?)) ORDER BY "id" LIMIT ? [1 101]`,
		},
	})
	s.do(t, request{
		org:   "1",
		query: `mutation { updatePost(id: "` + post1 + `", input: {title: ""}) { title } }`,
		want: `{"errors": [{"message": "UpdatePostInput.title: invalid value: must not be empty", "path": ["updatePost"], "extensions": {"code": "BAD_USER_INPUT"}}],
			"data": null}`,
	})
}

// TestUnlinkOtherTenant: the label go is linked to acme's post 1 and to
// globex's post 5. Acme's removePostIDs and clearPosts unlink only the posts
// acme may read: the join rows, which are not tenant-scoped, are deleted
// only for the posts the tenant scope selects, and globex's link stays.
func TestUnlinkOtherTenant(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `mutation { updateLabel(id: "` + label1 + `", input: {removePostIDs: ["` + post5 + `"]}) { name } }`,
		want:  `{"data": {"updateLabel": {"name": "go"}}}`,
		stmts: []string{
			`SELECT 1 FROM "tags" WHERE "id" = ? LIMIT ? [1 1]`,
			// Post 5 is not acme's: no join row is deleted.
			`SELECT "id" FROM "posts" WHERE "id" IN (?) AND "org_id" = ? LIMIT ? [5 1 1]`,
			selTags + ` WHERE "id" = ? LIMIT ? [1 2]`,
		},
	})
	s.do(t, request{
		org:   "1",
		query: `mutation { updateLabel(id: "` + label1 + `", input: {clearPosts: true}) { name } }`,
		want:  `{"data": {"updateLabel": {"name": "go"}}}`,
		stmts: []string{
			`SELECT 1 FROM "tags" WHERE "id" = ? LIMIT ? [1 1]`,
			// The label's join rows a page at a time, the posts of them
			// acme may read, and only their join rows.
			`SELECT "post_id", "tag_id" FROM "post_tags" WHERE "tag_id" = ? ORDER BY "tag_id", "post_id" LIMIT ? [1 100]`,
			`SELECT "id" FROM "posts" WHERE "id" IN (?, ?) AND "org_id" = ? LIMIT ? [1 5 1 2]`,
			`DELETE FROM "post_tags" WHERE "tag_id" = ? AND "post_id" IN (?) [1 1]`,
			selTags + ` WHERE "id" = ? LIMIT ? [1 2]`,
		},
	})
	s.do(t, request{
		org:   "2",
		query: `{ node(id: "` + post5 + `") { ... on Post { tags { edges { node { name } } } } } }`,
		want:  `{"data": {"node": {"tags": {"edges": [{"node": {"name": "go"}}]}}}}`,
		stmts: []string{
			posts + ` WHERE "id" = ? AND "org_id" = ? LIMIT ? [5 2 2]`,
			tagsOf("?") + ` [5 101]`,
		},
	})
	s.do(t, request{
		org:   "1",
		query: `{ node(id: "` + post1 + `") { ... on Post { tags { edges { node { name } } } } } }`,
		want:  `{"data": {"node": {"tags": {"edges": [{"node": {"name": "graphql"}}]}}}}`,
		stmts: []string{
			posts + ` WHERE "id" = ? AND "org_id" = ? LIMIT ? [1 1 2]`,
			tagsOf("?") + ` [1 101]`,
		},
	})
}

// TestDeleteOtherTenant: acme cannot delete globex's post 5, which it cannot
// see: NOT_FOUND, as for a post that does not exist, and the post keeps its
// join row, which is not tenant-scoped.
func TestDeleteOtherTenant(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `mutation { deletePost(id: "` + post5 + `") }`,
		want: `{"errors": [{"message": "deletePost: no Post has this ID", "path": ["deletePost"], "extensions": {"code": "NOT_FOUND"}}],
			"data": null}`,
		stmts: []string{`SELECT 1 FROM "posts" WHERE "id" = ? AND "org_id" = ? LIMIT ? [5 1 1]`},
	})
	s.do(t, request{
		org:   "2",
		query: `{ node(id: "` + post5 + `") { ... on Post { title tags { edges { node { name } } } } } }`,
		want:  `{"data": {"node": {"title": "Globex plans", "tags": {"edges": [{"node": {"name": "go"}}]}}}}`,
		stmts: []string{
			posts + ` WHERE "id" = ? AND "org_id" = ? LIMIT ? [5 2 2]`,
			tagsOf("?") + ` [5 101]`,
		},
	})
}

// TestDeletePost deletes acme's post 1, its join rows first, and returns its
// ID; the post is then gone.
func TestDeletePost(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `mutation { deletePost(id: "` + post1 + `") }`,
		want:  `{"data": {"deletePost": "` + post1 + `"}}`,
		stmts: []string{
			`SELECT 1 FROM "posts" WHERE "id" = ? AND "org_id" = ? LIMIT ? [1 1 1]`,
			`DELETE FROM "post_tags" WHERE "post_id" = ? [1]`,
			`DELETE FROM "posts" WHERE "id" = ? AND "org_id" = ? [1 1]`,
		},
	})
	s.do(t, request{
		org:   "1",
		query: `{ node(id: "` + post1 + `") { id } }`,
		want:  `{"data": {"node": null}}`,
		stmts: []string{posts + ` WHERE "id" = ? AND "org_id" = ? LIMIT ? [1 1 2]`},
	})
}

// TestMutationTransaction: an operation's mutations run in one transaction.
// The second label's name is taken, so the operation fails, and the first
// label, written before, is rolled back with it.
func TestMutationTransaction(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		query: `mutation { a: createLabel(input: {name: "rust", rank: 4}) { id } b: createLabel(input: {name: "go", rank: 5}) { id } }`,
		want: `{"errors": [{"message": "unique constraint violated", "path": ["b"], "extensions": {"code": "BAD_USER_INPUT"}}],
			"data": null}`,
		stmts: []string{
			`INSERT INTO "tags" ("name", "rank") VALUES (?, ?) RETURNING "id" [rust 4]`,
			`INSERT INTO "tags" ("name", "rank") VALUES (?, ?) RETURNING "id" [go 5]`,
		},
	})
	s.do(t, request{
		query: `{ labels(where: {name: "rust"}) { totalCount } }`,
		want:  `{"data": {"labels": {"totalCount": 0}}}`,
		stmts: []string{`SELECT COUNT(*) FROM "tags" WHERE "name" = ? [rust]`},
	})
}

// TestCreateUserDirective: email is @admin, and the directive guards its
// input field as it guards the field: without X-Role: admin, a createUser
// that sets it is FORBIDDEN, and nothing is written. passwordHash, Sensitive,
// is an input only: no query can select it.
func TestCreateUserDirective(t *testing.T) {
	s := newServer(t)
	const create = `mutation { createUser(input: {name: "Dana", email: "dana@example.com", joinedAt: "2026-07-01T09:00:00Z", passwordHash: "y"}) { name role } }`
	s.do(t, request{
		query: create,
		want: `{"errors": [{"message": "the admin role is required", "path": ["createUser", "input", "email"], "extensions": {"code": "FORBIDDEN"}}],
			"data": null}`,
	})
	s.do(t, request{
		role:  "admin",
		query: create,
		want:  `{"data": {"createUser": {"name": "Dana", "role": "MEMBER"}}}`,
		stmts: []string{`INSERT INTO "users" ("name", "email", "role", "joined_at", "password_hash") VALUES (?, ?, ?, ?, ?) RETURNING "id" [Dana dana@example.com member 2026-07-01 09:00:00 +0000 UTC y]`},
	})
	s.do(t, request{
		role:  "admin",
		query: `{ users(where: {name: "Dana"}) { edges { node { email passwordHash } } } }`,
		want:  `{"errors": [{"message": "Cannot query field \"passwordHash\" on type \"User\".", "extensions": {"code": "GRAPHQL_VALIDATION_FAILED"}}], "data": null}`,
	})
}

// TestMutationInputShape: an input of the wrong shape is gqlgen's error,
// left as gqlgen writes it, code GRAPHQL_VALIDATION_FAILED, pointing at the
// field as the GraphQL spec has it: a variable's by its path, an inline
// literal's by its locations. Only velox's own validation errors name the
// input field in their message, with BAD_USER_INPUT. Neither runs a
// statement: velox's validators run on the fields an input sets before any
// statement, so an empty title with an author that does not exist is the
// validator's error. (gqlgen accepts a number for a String variable, as
// "3": its leniency.)
func TestMutationInputShape(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		query: `mutation($input: CreatePostInput!) { createPost(input: $input) { title } }`,
		vars:  map[string]any{"input": map[string]any{"title": true, "authorID": user2}},
		want: `{"errors": [{"message": "cannot use bool as String", "path": ["variable", "input", "title"],
			"extensions": {"code": "GRAPHQL_VALIDATION_FAILED"}}], "data": null}`,
	})
	// canon drops locations, so the body is checked for them.
	body := s.do(t, request{
		org:   "1",
		query: `mutation { createPost(input: {title: 3, authorID: "` + user2 + `"}) { title } }`,
		want: `{"errors": [{"message": "String cannot represent a non string value: 3",
			"extensions": {"code": "GRAPHQL_VALIDATION_FAILED"}}], "data": null}`,
	})
	if !strings.Contains(body, `"locations":[{"line":1,"column":38}]`) {
		t.Errorf("the inline literal's error has no locations pointing at title: %s", body)
	}
	s.do(t, request{
		org:   "1",
		query: `mutation($input: CreatePostInput!) { createPost(input: $input) { title } }`,
		vars:  map[string]any{"input": map[string]any{"title": "", "authorID": user9}},
		want: `{"errors": [{"message": "CreatePostInput.title: invalid value: must not be empty", "path": ["createPost"],
			"extensions": {"code": "BAD_USER_INPUT"}}], "data": null}`,
	})
}

// TestServeShutsDownGracefully: serve answers requests on its listener, and
// when its context ends it shuts the server down, returns nil and removes its
// temporary database directory.
func TestServeShutsDownGracefully(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // os.MkdirTemp reads it
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- serve(ctx, ln) }()

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post("http://"+ln.Addr().String()+"/graphql", "application/json",
		strings.NewReader(`{"query":"{ __typename }"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"__typename":"Query"`) {
		t.Fatalf("got %d %s", resp.StatusCode, body)
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "velox-graphql-*")); len(left) != 1 {
		t.Errorf("while serving, the database directory is %v, want one", left)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("serve returned %v after its context ended, want nil", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not return after its context ended")
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "velox-graphql-*")); len(left) != 0 {
		t.Errorf("serve left %v", left)
	}
	if _, err := net.Dial("tcp", ln.Addr().String()); err == nil {
		t.Error("the listener still accepts after serve returned")
	}
}
