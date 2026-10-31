package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tracker/gen"
)

func TestMain(m *testing.M) {
	hashIterations = 1000 // the work factor is not what these tests are about
	os.Exit(m.Run())
}

// targets runs f on a fresh database of each kind: SQLite always, PostgreSQL when
// VELOX_TEST_POSTGRES names one, in a schema of its own.
func targets(t *testing.T, f func(t *testing.T, c *client)) {
	t.Run("sqlite", func(t *testing.T) {
		db, closeDB, err := open(t.Context(), filepath.Join(t.TempDir(), "t.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(closeDB)
		f(t, serve(t, db))
	})
	dsn := os.Getenv("VELOX_TEST_POSTGRES")
	if dsn == "" {
		return
	}
	t.Run("postgres", func(t *testing.T) {
		admin, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = admin.Close() })
		schema := fmt.Sprintf("tracker_%d", time.Now().UnixNano())
		if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
				t.Error(err)
			}
		})
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal("VELOX_TEST_POSTGRES is not a URL")
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		db, closeDB, err := open(t.Context(), u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(closeDB)
		f(t, serve(t, db))
	})
}

func serve(t *testing.T, db *velox.DB) *client {
	t.Helper()
	srv := httptest.NewServer(newServer(db))
	t.Cleanup(srv.Close)
	return &client{t: t, url: srv.URL, db: db}
}

// client calls the API as one logged-in user, or as nobody before login.
type client struct {
	t     *testing.T
	url   string
	token string
	db    *velox.DB
}

func (c *client) as(token string) *client {
	d := *c
	d.token = token
	return &d
}

// do sends body as JSON, checks the status, and decodes the answer into out, if any.
func (c *client) do(method, path string, want int, body, out any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.url+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	got, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	if res.StatusCode != want {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, res.StatusCode, want, got)
	}
	if out != nil {
		if err := json.Unmarshal(got, out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, got)
		}
	}
}

type obj = map[string]any

// idOnly reads the id of a row that has loaded relations: an entity with them does
// not decode from JSON (velox.One.MarshalJSON).
type idOnly struct {
	ID int64 `json:"id"`
}

type tokenBody struct {
	Token string `json:"token"`
}

// signup creates an org and returns a client for its admin.
func (c *client) signup(org string) *client {
	c.t.Helper()
	var tk tokenBody
	c.do("POST", "/signup", 201, obj{"org": org, "org_name": org, "email": "admin@" + org, "name": "Admin", "password": "correct horse"}, &tk)
	return c.as(tk.Token)
}

// member adds a member to the admin's org and returns a client logged in as them.
func (c *client) member(org, name string) (*client, int64) {
	c.t.Helper()
	var u gen.User
	c.do("POST", "/users", 201, obj{"email": name + "@" + org, "name": name, "password": "correct horse"}, &u)
	var tk tokenBody
	c.as("").do("POST", "/login", 200, obj{"org": org, "email": name + "@" + org, "password": "correct horse"}, &tk)
	return c.as(tk.Token), u.ID
}

func (c *client) project(name string) int64 {
	c.t.Helper()
	var p gen.Project
	c.do("POST", "/projects", 201, obj{"name": name}, &p)
	return p.ID
}

func (c *client) issue(project int64, title string, labels ...string) int64 {
	c.t.Helper()
	var x gen.Issue
	c.do("POST", fmt.Sprintf("/projects/%d/issues", project), 201, obj{"title": title, "labels": labels}, &x)
	return x.ID
}

func TestAuth(t *testing.T) {
	targets(t, func(t *testing.T, c *client) {
		c.do("GET", "/me", 401, nil, nil)
		c.as("not-a-token").do("GET", "/me", 401, nil, nil)
		admin := c.signup("acme")

		var me map[string]any
		admin.do("GET", "/me", 200, nil, &me)
		if me["email"] != "admin@acme" || me["role"] != "admin" {
			t.Errorf("/me: %v", me)
		}
		if _, leaked := me["password_hash"]; leaked {
			t.Error("/me shows the password hash")
		}

		// Unknown org, unknown email and a wrong password look the same.
		for _, in := range []obj{
			{"org": "nope", "email": "admin@acme", "password": "correct horse"},
			{"org": "acme", "email": "nobody@acme", "password": "correct horse"},
			{"org": "acme", "email": "admin@acme", "password": "wrong horse"},
		} {
			var e errorBody
			c.do("POST", "/login", 401, in, &e)
			if e.Error != errBadLogin.Error() {
				t.Errorf("login %v: %q", in, e.Error)
			}
		}

		// The org and its first user are one transaction: a second signup that
		// fails on the slug, or on validation, leaves nothing.
		c.do("POST", "/signup", 409, obj{"org": "acme", "org_name": "x", "email": "a@b", "name": "n", "password": "correct horse"}, nil)
		c.do("POST", "/signup", 422, obj{"org": "half", "org_name": "x", "email": "", "name": "n", "password": "correct horse"}, nil)
		c.do("POST", "/signup", 201, obj{"org": "half", "org_name": "x", "email": "a@b", "name": "n", "password": "correct horse"}, nil)
		c.do("POST", "/signup", 422, obj{"org": "short", "org_name": "x", "email": "a@b", "name": "n", "password": "short"}, nil)

		// Logging out ends this session, not another of the same user's.
		var second tokenBody
		c.do("POST", "/login", 200, obj{"org": "acme", "email": "admin@acme", "password": "correct horse"}, &second)
		admin.do("POST", "/logout", 204, nil, nil)
		admin.do("GET", "/me", 401, nil, nil)
		c.as(second.Token).do("GET", "/me", 200, nil, nil)

		// The scheme's name is case-insensitive, and a bare token is not a scheme.
		for header, want := range map[string]int{"bearer " + second.Token: 200, second.Token: 401, "Basic " + second.Token: 401} {
			req, err := http.NewRequestWithContext(t.Context(), "GET", c.url+"/me", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", header)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != want {
				t.Errorf("a scheme of %q: status %d, want %d", strings.SplitN(header, " ", 2)[0][:5], res.StatusCode, want)
			}
		}
	})
}

// TestDeletedUser: a session outlives no user: its token stops working.
// TestOrgNameCountsCharacters: the limit on an Org's name is in characters, so 100
// characters of Chinese, 300 bytes, are valid and 101 are not.
func TestOrgNameCountsCharacters(t *testing.T) {
	org := gen.Org{Slug: "x", Name: strings.Repeat("陳", 100)}
	if err := velox.Validate[gen.Org](&org, gen.Orgs.Name); err != nil {
		t.Errorf("100 characters: %v", err)
	}
	org.Name += "陳"
	if err := velox.Validate[gen.Org](&org, gen.Orgs.Name); err == nil {
		t.Error("101 characters are valid")
	}
}

// TestSlugCheck: the database holds a slug to lower case whatever code writes it.
func TestSlugCheck(t *testing.T) {
	targets(t, func(t *testing.T, c *client) {
		err := c.db.Insert(&gen.Org{Slug: "Upper", Name: "x"}).Exec(t.Context())
		var ce *velox.ConstraintError
		if !errors.As(err, &ce) || ce.Kind != velox.ConstraintCheck {
			t.Errorf("Insert = %v, want a check violation", err)
		}
	})
}

func TestDeletedUser(t *testing.T) {
	targets(t, func(t *testing.T, c *client) {
		admin := c.signup("acme")
		ann, annID := admin.member("acme", "ann")
		ctx := velox.WithAllTenants(t.Context(), "test")
		if _, err := c.db.Delete[gen.User]().Where(gen.Users.ID.EQ(annID)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		ann.do("GET", "/me", 401, nil, nil)
	})
}

// TestExpiredSession: a session past its time no longer logs in.
func TestExpiredSession(t *testing.T) {
	targets(t, func(t *testing.T, c *client) {
		admin := c.signup("acme")
		S := gen.Sessions
		n, err := c.db.Update[gen.Session]().
			Set(S.ExpiresAt.Set(time.Now().UTC().Add(-time.Minute))).
			Where(S.TokenHash.EQ(hashToken(admin.token))).
			Exec(t.Context())
		if err != nil || n != 1 {
			t.Fatalf("expiring: %d, %v", n, err)
		}
		admin.do("GET", "/me", 401, nil, nil)
	})
}

// TestTenants: one org's rows do not exist for another, by id or through a key.
func TestTenants(t *testing.T) {
	targets(t, func(t *testing.T, c *client) {
		acme, globex := c.signup("acme"), c.signup("globex")
		_, globexMember := globex.member("globex", "gina")
		project := acme.project("Rocket")
		issue := acme.issue(project, "Fuel leak")

		// The same name in another org is no conflict.
		globex.project("Rocket")
		acme.do("POST", "/projects", 409, obj{"name": "Rocket"}, nil)

		path := fmt.Sprintf("/issues/%d", issue)
		globex.do("GET", path, 404, nil, nil)
		globex.do("PATCH", path, 404, obj{"title": "mine now"}, nil)
		globex.do("DELETE", path, 404, nil, nil)
		globex.do("POST", path+"/comments", 404, obj{"body": "hello"}, nil)
		globex.do("GET", fmt.Sprintf("/projects/%d/issues", project), 404, nil, nil)
		globex.do("POST", fmt.Sprintf("/projects/%d/issues", project), 404, obj{"title": "mine now"}, nil)

		// Another org's user is no assignee.
		acme.do("PATCH", path, 422, obj{"assignee_id": globexMember}, nil)

		// One email can be in two orgs, and logs in to the org it names.
		acme.do("POST", "/users", 201, obj{"email": "gina@globex", "name": "Other Gina", "password": "a different one"}, nil)
		c.do("POST", "/login", 200, obj{"org": "acme", "email": "gina@globex", "password": "a different one"}, nil)
		c.do("POST", "/login", 401, obj{"org": "globex", "email": "gina@globex", "password": "a different one"}, nil)

		// One label name in two orgs is two labels: acme's filter finds only acme's
		// issue, and globex's only globex's, each with its own label's id.
		acme.issue(project, "Tagged", "shared")
		globexProject := globex.project("Tagged")
		globexIssue := globex.issue(globexProject, "Tagged too", "shared")
		type labelled struct {
			ID     int64 `json:"id"`
			Labels []struct {
				ID int64 `json:"id"`
			} `json:"labels"`
		}
		var a, g pageBody[labelled]
		acme.do("GET", fmt.Sprintf("/projects/%d/issues?label=shared", project), 200, nil, &a)
		globex.do("GET", fmt.Sprintf("/projects/%d/issues?label=shared", globexProject), 200, nil, &g)
		if len(a.Items) != 1 || len(g.Items) != 1 || g.Items[0].ID != globexIssue {
			t.Fatalf("issues labelled shared: acme %+v, globex %+v", a.Items, g.Items)
		}
		if len(a.Items[0].Labels) != 1 || len(g.Items[0].Labels) != 1 || a.Items[0].Labels[0].ID == g.Items[0].Labels[0].ID {
			t.Errorf("the orgs' labels: acme %+v, globex %+v; want one each, different", a.Items[0].Labels, g.Items[0].Labels)
		}

		// A count covers the org's own issues.
		var page pageBody[projectView]
		globex.do("GET", "/projects", 200, nil, &page)
		got := map[int64]int64{}
		for _, p := range page.Items {
			got[p.ID] = p.OpenIssues
		}
		if _, leaked := got[project]; leaked || got[globexProject] != 1 {
			t.Errorf("globex's projects: %v", got)
		}
	})
}

// TestPolicy: within an org, roles and authorship decide who changes what.
func TestPolicy(t *testing.T) {
	targets(t, func(t *testing.T, c *client) {
		admin := c.signup("acme")
		ann, annID := admin.member("acme", "ann")
		bob, bobID := admin.member("acme", "bob")

		ann.do("POST", "/projects", 403, obj{"name": "Mine"}, nil)
		ann.do("POST", "/users", 403, obj{"email": "x@y", "name": "x", "password": "correct horse"}, nil)
		project := admin.project("Rocket")

		issue := ann.issue(project, "Fuel leak")
		path := fmt.Sprintf("/issues/%d", issue)

		// Everyone reads; bob is neither its author nor its assignee.
		bob.do("GET", path, 200, nil, nil)
		bob.do("PATCH", path, 403, obj{"status": "closed"}, nil)
		bob.do("DELETE", path, 403, nil, nil)

		// The admin assigns it to bob, who can then change it.
		var x gen.Issue
		admin.do("PATCH", path, 200, obj{"assignee_id": bobID}, &x)
		if x.AssigneeID == nil || *x.AssigneeID != bobID {
			t.Errorf("assignee: %v", x.AssigneeID)
		}
		bob.do("PATCH", path, 200, obj{"status": "closed"}, &x)
		if x.Status != gen.IssueStatusClosed || x.AuthorID != annID {
			t.Errorf("after bob's close: %+v", x)
		}
		// Unassigned, bob loses it again.
		var cleared gen.Issue
		ann.do("PATCH", path, 200, obj{"assignee_id": nil}, &cleared)
		if cleared.AssigneeID != nil {
			t.Errorf("assignee after null: %v", *cleared.AssigneeID)
		}
		bob.do("PATCH", path, 403, obj{"status": "open"}, nil)

		// A write the body cannot express is refused before any statement.
		ann.do("PATCH", path, 400, obj{"author_id": bobID}, nil)
		ann.do("PATCH", path, 400, obj{}, nil)
		ann.do("PATCH", path, 422, obj{"status": "maybe"}, nil)
		ann.do("PATCH", path, 422, obj{"title": ""}, nil)

		// A comment by anyone; deleting the issue deletes it, whoever wrote it.
		bob.do("POST", path+"/comments", 201, obj{"body": "on it"}, nil)
		ann.do("DELETE", path, 204, nil, nil)
		ann.do("GET", path, 404, nil, nil)
		n, err := c.db.Query[gen.Comment]().Where(gen.Comments.IssueID.EQ(issue)).Count(velox.WithAllTenants(gen.WithPrincipal(t.Context(), gen.Principal{Admin: true}), "test"))
		if err != nil || n != 0 {
			t.Errorf("comments after the delete: %d, %v", n, err)
		}
		admin.do("DELETE", path, 404, nil, nil)
	})
}

// TestIssueTransaction: an issue whose labels fail is not created: the issue's
// insert, already sent, rolls back.
func TestIssueTransaction(t *testing.T) {
	targets(t, func(t *testing.T, c *client) {
		admin := c.signup("acme")
		project := admin.project("Rocket")
		path := fmt.Sprintf("/projects/%d/issues", project)
		admin.do("POST", path, 422, obj{"title": "Half made", "labels": []string{"ok", ""}}, nil)
		var p pageBody[idOnly]
		admin.do("GET", path, 200, nil, &p)
		if len(p.Items) != 0 {
			t.Errorf("the failed transaction left %d issues", len(p.Items))
		}
		// The name that came with the failed issue stays usable.
		admin.issue(project, "Whole", "ok")
		admin.do("POST", path, 422, obj{"title": "Too many", "labels": strings.Split(strings.Repeat("x,", maxLabels)+"x", ",")}, nil)
	})
}

// TestIssueDetail: an issue comes with everything it relates to.
func TestIssueDetail(t *testing.T) {
	targets(t, func(t *testing.T, c *client) {
		admin := c.signup("acme")
		ann, annID := admin.member("acme", "ann")
		project := admin.project("Rocket")
		issue := ann.issue(project, "Fuel leak", "urgent", "bug")
		// A second issue reuses a label; a name given twice, in any order, is one.
		other := ann.issue(project, "Valve", "bug", "valve", "bug")
		path := fmt.Sprintf("/issues/%d", issue)
		admin.do("POST", path+"/comments", 201, obj{"body": "first"}, nil)
		ann.do("POST", path+"/comments", 201, obj{"body": "second"}, nil)
		ann.do("POST", path+"/comments", 422, obj{"body": ""}, nil)
		admin.do("PATCH", path, 200, obj{"assignee_id": annID}, nil)

		var x struct {
			Title  string `json:"title"`
			Author struct {
				Name string `json:"name"`
			} `json:"author"`
			Assignee struct {
				Name string `json:"name"`
			} `json:"assignee"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
			Comments []struct {
				Body   string `json:"body"`
				Author struct {
					Name string `json:"name"`
				} `json:"author"`
			} `json:"comments"`
		}
		ann.do("GET", path, 200, nil, &x)
		if x.Title != "Fuel leak" || x.Author.Name != "ann" || x.Assignee.Name != "ann" {
			t.Errorf("issue: %+v", x)
		}
		if len(x.Labels) != 2 || x.Labels[0].Name != "bug" || x.Labels[1].Name != "urgent" {
			t.Errorf("labels: %+v", x.Labels)
		}
		if len(x.Comments) != 2 || x.Comments[0].Body != "first" || x.Comments[0].Author.Name != "Admin" || x.Comments[1].Author.Name != "ann" {
			t.Errorf("comments: %+v", x.Comments)
		}
		var o struct {
			Labels []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"labels"`
		}
		ann.do("GET", fmt.Sprintf("/issues/%d", other), 200, nil, &o)
		if len(o.Labels) != 2 || o.Labels[0].Name != "bug" || o.Labels[1].Name != "valve" {
			t.Errorf("the other issue's labels: %+v", o.Labels)
		}
		var issue1 struct {
			Labels []struct {
				ID int64 `json:"id"`
			} `json:"labels"`
		}
		ann.do("GET", path, 200, nil, &issue1)
		if issue1.Labels[0].ID != o.Labels[0].ID {
			t.Errorf("bug has ids %d and %d: not reused", issue1.Labels[0].ID, o.Labels[0].ID)
		}
	})
}

// TestPages: keyset pages cover every row once, filters apply, and counts agree.
func TestPages(t *testing.T) {
	targets(t, func(t *testing.T, c *client) {
		admin := c.signup("acme")
		project, empty := admin.project("Rocket"), admin.project("Empty")
		var ids []int64
		for i := range 5 {
			label := "even"
			if i%2 == 1 {
				label = "odd"
			}
			ids = append(ids, admin.issue(project, fmt.Sprintf("Issue %d", i), label))
		}
		admin.do("PATCH", fmt.Sprintf("/issues/%d", ids[0]), 200, obj{"status": "closed"}, nil)

		var seen []int64
		var after int64
		pages := 0
		for {
			var p pageBody[idOnly]
			admin.do("GET", fmt.Sprintf("/projects/%d/issues?limit=2&after=%d", project, after), 200, nil, &p)
			pages++
			for _, x := range p.Items {
				seen = append(seen, x.ID)
			}
			if p.Next == nil {
				break
			}
			after = *p.Next
		}
		if fmt.Sprint(seen) != fmt.Sprint(ids) || pages != 3 {
			t.Errorf("paged %v in %d pages, want %v in 3", seen, pages, ids)
		}

		for query, want := range map[string]int{"status=closed": 1, "status=open": 4, "label=odd": 2, "label=even&status=open": 2, "label=none": 0} {
			var p pageBody[idOnly]
			admin.do("GET", fmt.Sprintf("/projects/%d/issues?%s", project, query), 200, nil, &p)
			if len(p.Items) != want || p.Items == nil {
				t.Errorf("%s: %d items, want %d", query, len(p.Items), want)
			}
		}
		for _, bad := range []string{"limit=0", "limit=101", "limit=x", "after=x"} {
			admin.do("GET", fmt.Sprintf("/projects/%d/issues?%s", project, bad), 400, nil, nil)
		}
		admin.do("GET", fmt.Sprintf("/projects/%d/issues?status=maybe", project), 422, nil, nil)

		var ps pageBody[projectView]
		admin.do("GET", "/projects", 200, nil, &ps)
		got := map[int64]int64{}
		for _, p := range ps.Items {
			got[p.ID] = p.OpenIssues
		}
		if len(got) != 2 || got[project] != 4 || got[empty] != 0 {
			t.Errorf("open issues: %v, want 4 for %d and 0 for %d", got, project, empty)
		}
	})
}

func TestBadRequests(t *testing.T) {
	targets(t, func(t *testing.T, c *client) {
		admin := c.signup("acme")
		admin.do("GET", "/issues/x", 404, nil, nil)
		admin.do("POST", "/projects", 400, obj{"name": "x", "extra": 1}, nil)
		for body, want := range map[string]int{
			`{"org":"` + strings.Repeat("x", maxBody) + `"}`: 413,
			`{"org":"acme"} {"org":"other"}`:                 400,
		} {
			req, err := http.NewRequestWithContext(t.Context(), "POST", c.url+"/login", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != want {
				t.Errorf("a body of %d bytes: status %d, want %d", len(body), res.StatusCode, want)
			}
		}
	})
}

func TestPassword(t *testing.T) {
	h, err := hashPassword("secret value")
	if err != nil {
		t.Fatal(err)
	}
	if !passwordMatches(h, "secret value") || passwordMatches(h, "other value") {
		t.Error("passwordMatches is wrong")
	}
	for _, bad := range []string{"", "not base64!", "1$c2FsdA$c2hvcnQ", "x$c2FsdA$c2hvcnQ", "0$c2FsdA$c2hvcnQ", "1$!$c2hvcnQ"} {
		if passwordMatches(bad, "secret value") {
			t.Errorf("%q matches", bad)
		}
	}
	// A hash made under an older work factor still matches after it rises.
	defer func(n int) { hashIterations = n }(hashIterations)
	hashIterations *= 2
	if !passwordMatches(h, "secret value") {
		t.Error("raising hashIterations broke an older hash")
	}
	if h2, _ := hashPassword("secret value"); h2 == h {
		t.Error("two hashes of one password are equal: no salt")
	}
}

// TestShutdownDrains: serveUntil returns only after a request in flight finished,
// so that closing the database cannot cut it off.
func TestShutdownDrains(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var finished atomic.Bool
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		finished.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	served := make(chan error, 1)
	go func() { served <- serveUntil(ctx, ln, h) }()
	got := make(chan int, 1)
	go func() {
		res, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			got <- 0
			return
		}
		_ = res.Body.Close()
		got <- res.StatusCode
	}()
	<-started
	stop()
	select {
	case err := <-served:
		t.Fatalf("serveUntil returned with a request in flight: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	if !finished.Load() {
		t.Error("serveUntil returned before the handler finished")
	}
	if code := <-got; code != http.StatusNoContent {
		t.Errorf("the request in flight got %d", code)
	}
}

// TestSQLiteDSNWithQuery: foreign keys are on when the path already has a query.
func TestSQLiteDSNWithQuery(t *testing.T) {
	db, closeDB, err := open(t.Context(), filepath.Join(t.TempDir(), "t.db")+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	c := serve(t, db)
	admin := c.signup("acme")
	project := admin.project("Rocket")
	issue := admin.issue(project, "Fuel leak")
	path := fmt.Sprintf("/issues/%d", issue)
	admin.do("POST", path+"/comments", 201, obj{"body": "first"}, nil)
	admin.do("DELETE", path, 204, nil, nil)
	ctx := velox.WithAllTenants(gen.WithPrincipal(t.Context(), gen.Principal{Admin: true}), "test")
	if n, err := db.Query[gen.Comment]().Where(gen.Comments.IssueID.EQ(issue)).Count(ctx); err != nil || n != 0 {
		t.Errorf("comments after the delete: %d, %v; foreign keys are off", n, err)
	}
}
