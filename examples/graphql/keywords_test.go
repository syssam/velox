package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

// postTitles are the titles of the posts of org that where selects, by title.
func postTitles(t *testing.T, s *testServer, org, where string) []string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": `{ posts(where: ` + where + `, orderBy: [{field: TITLE}]) { edges { node { title } } } }`})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Org-ID", org)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Data struct {
			Posts struct {
				Edges []struct{ Node struct{ Title string } }
			}
		}
		Errors []struct{ Message string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Errors) > 0 {
		t.Fatalf("%s: %v, %v", where, err, out.Errors)
	}
	titles := []string{}
	for _, e := range out.Data.Posts.Edges {
		titles = append(titles, e.Node.Title)
	}
	return titles
}

// TestKeywordFilters: Post.keywords is a JSON array of strings, and
// WhereOps(OpsJSONArray) gives posts keywordsHas, keywordsHasSome,
// keywordsHasEvery and keywordsIsEmpty. The seed's posts have none; Brian's
// new one has go and sql, and each filter finds it by its elements.
func TestKeywordFilters(t *testing.T) {
	s := newServer(t)
	s.do(t, request{
		org:   "1",
		user:  "2",
		query: `mutation { createPost(input: {title: "Keyed", keywords: ["go", "sql"]}) { keywords } }`,
		want:  `{"data": {"createPost": {"keywords": ["go", "sql"]}}}`,
		stmts: []string{
			viewerCheck + ` [2 1]`,
			`INSERT INTO "posts" ("org_id", "author_id", "title", "status", "views", "published_at", "keywords") VALUES (?, ?, ?, ?, ?, NULL, ?) RETURNING "id" [1 2 Keyed draft 0 ["go","sql"]]`,
		},
	})
	seeded := postTitles(t, s, "1", `{}`)
	if !slices.Contains(seeded, "Keyed") || len(seeded) < 2 {
		t.Fatalf("org 1's posts: %v", seeded)
	}
	others := slices.DeleteFunc(slices.Clone(seeded), func(s string) bool { return s == "Keyed" })
	for _, c := range []struct {
		name, where string
		want        []string
	}{
		{"has", `{keywordsHas: "sql"}`, []string{"Keyed"}},
		{"has, absent", `{keywordsHas: "rust"}`, []string{}},
		{"hasSome", `{keywordsHasSome: ["rust", "go"]}`, []string{"Keyed"}},
		{"hasSome of none", `{keywordsHasSome: []}`, []string{}},
		{"hasEvery", `{keywordsHasEvery: ["go", "sql"]}`, []string{"Keyed"}},
		{"hasEvery, one missing", `{keywordsHasEvery: ["go", "rust"]}`, []string{}},
		{"isEmpty", `{keywordsIsEmpty: true}`, others},
		{"not empty", `{keywordsIsEmpty: false}`, []string{"Keyed"}},
		{"not has", `{not: {keywordsHas: "go"}}`, others},
		{"other org", `{keywordsHas: "go"}`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			org := "1"
			if c.name == "other org" {
				org, c.want = "2", []string{}
			}
			if got := postTitles(t, s, org, c.where); !slices.Equal(got, c.want) {
				t.Errorf("%s: got %v, want %v", c.where, got, c.want)
			}
		})
	}
}
