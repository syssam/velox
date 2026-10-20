package gen_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"

	"example.com/tmp/gen"
)

const ddl = `
CREATE TABLE users (
	id INTEGER PRIMARY KEY, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
	created_by TEXT NOT NULL, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE, role TEXT NOT NULL,
	nickname TEXT NOT NULL, age INTEGER NOT NULL, motto TEXT NOT NULL, password_hash TEXT NOT NULL, score REAL);
CREATE TABLE posts (
	id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL, title TEXT NOT NULL, status TEXT NOT NULL, views INTEGER NOT NULL, rank INTEGER);
CREATE TABLE notes (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, text TEXT NOT NULL);
CREATE TABLE groups (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE user_groups (user_id INTEGER NOT NULL, group_id INTEGER NOT NULL, PRIMARY KEY (user_id, group_id));`

var ctx = context.Background()

func open(t *testing.T) *velox.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1) // one connection keeps the in-memory database alive
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return velox.Open(velox.SQLite, raw)
}

func newUser(name string) gen.User {
	return gen.User{Name: name, Email: name + "@example.com", CreatedBy: "root", PasswordHash: "s3cret"}
}

func mustInsert[E any, PE velox.Entity[E]](t *testing.T, db *velox.DB, x PE) {
	t.Helper()
	if err := db.Insert(x).Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// seed inserts ada (posts "a1" draft and "a2" published; groups devs and ops),
// bob (post "b1" published; group ops) and carol (nothing).
func seed(t *testing.T, db *velox.DB) (ada, bob, carol gen.User, devs, ops gen.Group) {
	t.Helper()
	ada, bob, carol = newUser("ada"), newUser("bob"), newUser("carol")
	mustInsert(t, db, &ada)
	mustInsert(t, db, &bob)
	mustInsert(t, db, &carol)
	for _, p := range []gen.Post{
		{AuthorID: ada.ID, Title: "a1"},
		{AuthorID: ada.ID, Title: "a2", Status: gen.PostStatusPublished},
		{AuthorID: bob.ID, Title: "b1", Status: gen.PostStatusPublished},
	} {
		mustInsert(t, db, &p)
	}
	devs, ops = gen.Group{Name: "devs"}, gen.Group{Name: "ops"}
	mustInsert(t, db, &devs)
	mustInsert(t, db, &ops)
	for _, ug := range []gen.UserGroup{{UserID: ada.ID, GroupID: devs.ID}, {UserID: ada.ID, GroupID: ops.ID}, {UserID: bob.ID, GroupID: ops.ID}} {
		if err := db.Insert(&ug).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func names(us []gen.User) []string {
	var out []string
	for _, u := range us {
		out = append(out, u.Name)
	}
	return out
}

func titles(ps []gen.Post) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Title)
	}
	return out
}

func wantStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func TestDefaults(t *testing.T) {
	db := open(t)
	before := gen.HookRuns.Load()
	u := newUser("ada")
	mustInsert(t, db, &u)
	if u.ID == 0 || u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() || time.Since(u.CreatedAt) > time.Minute {
		t.Errorf("mixin.Time not filled: %+v", u)
	}
	if u.Role != gen.UserRoleViewer {
		t.Errorf("enum default: %q", u.Role)
	}
	if u.Score == nil || *u.Score != 1.5 {
		t.Errorf("literal default: %v", u.Score)
	}
	if u.Nickname != "hooked" || gen.HookRuns.Load() != before+1 {
		t.Errorf("hook: nickname %v, runs %d, before %d", u.Nickname, gen.HookRuns.Load(), before)
	}
	got, err := db.Query[gen.User]().Where(gen.Users.ID.EQ(u.ID)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != gen.UserRoleViewer || got.Score == nil || *got.Score != 1.5 || got.CreatedAt.IsZero() || got.Age != 0 {
		t.Errorf("read back: %+v", got)
	}
	// age (Range(1, 150)) and motto (NotEmpty) are Optional and were left
	// unset: their zero values mean unset, which the validators let pass, and
	// the NOT NULL columns hold them.
	if got.Age != 0 || got.Motto != "" {
		t.Errorf("unset Optional fields read back: %+v", got)
	}
	p := gen.Post{AuthorID: u.ID, Title: "t"}
	mustInsert(t, db, &p)
	gp, err := db.Query[gen.Post]().Where(gen.Posts.ID.EQ(p.ID)).One(ctx)
	if err != nil || gp.Status != gen.PostStatusDraft || gp.Views == nil || *gp.Views != 0 || gp.Rank == nil || *gp.Rank != 3 {
		t.Errorf("post defaults: %+v, %v", gp, err)
	}
}

func TestValidators(t *testing.T) {
	db := open(t)
	long := strings.Repeat("x", 21)
	age := 200
	negative := -1
	for _, c := range []struct {
		name  string
		field string
		build func() any
	}{
		{"not empty", "Name", func() any { u := newUser(""); return &u }},
		{"max length", "Name", func() any { return &gen.User{Name: long, Email: "a@b", CreatedBy: "root"} }},
		{"match", "Email", func() any { u := newUser("ada"); u.Email = "nope"; return &u }},
		{"range", "Age", func() any { u := newUser("ada"); u.Age = age; return &u }},
		{"optional max length", "Motto", func() any { u := newUser("ada"); u.Motto = long; return &u }},
		{"enum", "Role", func() any { u := newUser("ada"); u.Role = "bogus"; return &u }},
		{"mixin min length", "CreatedBy", func() any { u := newUser("ada"); u.CreatedBy = "r"; return &u }},
		{"mixin validate", "CreatedBy", func() any { u := newUser("ada"); u.CreatedBy = "Root"; return &u }},
		{"post title", "Title", func() any { return &gen.Post{AuthorID: 1} }},
		{"min", "Views", func() any { return &gen.Post{AuthorID: 1, Title: "t", Views: &negative} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			var err error
			switch x := c.build().(type) {
			case *gen.User:
				err = db.Insert(x).Exec(ctx)
			case *gen.Post:
				err = db.Insert(x).Exec(ctx)
			}
			var ve *velox.ValidationError
			if !errors.As(err, &ve) || ve.Field != c.field {
				t.Fatalf("got %v, want a ValidationError for %s", err, c.field)
			}
		})
	}
	t.Run("messages", func(t *testing.T) {
		u := newUser("")
		err := db.Insert(&u).Exec(ctx)
		if err == nil || !strings.Contains(err.Error(), "gen.User: Name: must not be empty") {
			t.Errorf("got %v", err)
		}
		u = newUser("ada")
		u.CreatedBy = "Root"
		err = db.Insert(&u).Exec(ctx)
		if err == nil || !strings.Contains(err.Error(), "CreatedBy: must be lower case") {
			t.Errorf("got %v", err)
		}
	})
	if n, err := db.Query[gen.User]().Count(ctx); err != nil || n != 0 {
		t.Errorf("a refused insert sent a row: %d, %v", n, err)
	}
	// An update validates what it sets.
	u := newUser("ada")
	mustInsert(t, db, &u)
	_, err := db.Update[gen.User]().Set(gen.Users.Name.Set("")).Where(gen.Users.ID.EQ(u.ID)).Exec(ctx)
	var ve *velox.ValidationError
	if !errors.As(err, &ve) || ve.Field != "Name" {
		t.Errorf("update: %v", err)
	}
}

func TestImmutable(t *testing.T) {
	db := open(t)
	u := newUser("ada")
	mustInsert(t, db, &u)
	_, err := db.Update[gen.User]().Set(gen.Users.CreatedAt.Set(time.Now())).Where(gen.Users.ID.EQ(u.ID)).Exec(ctx)
	if !errors.Is(err, velox.ErrImmutable) {
		t.Fatalf("got %v", err)
	}
	// A mutable column updates, and updated_at moves with it: the UpdateDefault
	// is read from the schema's descriptor.
	before, err := db.Query[gen.User]().Where(gen.Users.ID.EQ(u.ID)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	n, err := db.Update[gen.User]().Set(gen.Users.Name.Set("ada2")).Where(gen.Users.ID.EQ(u.ID)).Exec(ctx)
	if err != nil || n != 1 {
		t.Fatalf("update: %d, %v", n, err)
	}
	got, err := db.Query[gen.User]().Where(gen.Users.ID.EQ(u.ID)).One(ctx)
	if err != nil || got.Name != "ada2" || !got.UpdatedAt.After(before.UpdatedAt) || !got.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("after update: %+v, %v", got, err)
	}
}

func TestEnums(t *testing.T) {
	if gen.UserRoleAdmin != "admin" || gen.UserRoleEditor != "editor" || gen.UserRoleViewer != "viewer" || gen.PostStatusDraft != "draft" {
		t.Error("enum constants")
	}
	wantStrings(t, "UserRole", gen.UserRole("").Values(), []string{"admin", "editor", "viewer"})
	wantStrings(t, "PostStatus", gen.PostStatus("").Values(), []string{"draft", "published"})
}

func TestWith(t *testing.T) {
	db := open(t)
	ada, bob, _, _, _ := seed(t, db)

	// O2M
	us, err := db.Query[gen.User]().With(gen.Users.Posts.OrderBy(gen.Posts.Title.Asc())).OrderBy(gen.Users.ID.Asc()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantStrings(t, "users", names(us), []string{"ada", "bob", "carol"})
	got := map[string][]string{}
	for _, u := range us {
		ps, ok := u.Posts.Get()
		if !ok {
			t.Fatalf("%s: posts not loaded", u.Name)
		}
		got[u.Name] = titles(ps)
	}
	wantStrings(t, "ada's posts", got["ada"], []string{"a1", "a2"})
	wantStrings(t, "bob's posts", got["bob"], []string{"b1"})
	if len(got["carol"]) != 0 {
		t.Errorf("carol's posts: %v", got["carol"])
	}

	// M2O
	ps, err := db.Query[gen.Post]().With(gen.Posts.Author).OrderBy(gen.Posts.ID.Asc()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var authors []string
	for _, p := range ps {
		a, ok := p.Author.Get()
		if !ok || a == nil {
			t.Fatalf("%s: author not loaded", p.Title)
		}
		authors = append(authors, a.Name)
	}
	wantStrings(t, "authors", authors, []string{"ada", "ada", "bob"})
	if ps[0].AuthorID != ada.ID || ps[2].AuthorID != bob.ID {
		t.Errorf("author ids: %+v", ps)
	}

	// M2M, through the join entity, both ways.
	us, err = db.Query[gen.User]().With(gen.Users.Groups.OrderBy(gen.Groups.Name.Asc())).OrderBy(gen.Users.ID.Asc()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gs := map[string][]string{}
	for _, u := range us {
		groups, ok := u.Groups.Get()
		if !ok {
			t.Fatalf("%s: groups not loaded", u.Name)
		}
		for _, g := range groups {
			gs[u.Name] = append(gs[u.Name], g.Name)
		}
	}
	wantStrings(t, "ada's groups", gs["ada"], []string{"devs", "ops"})
	wantStrings(t, "bob's groups", gs["bob"], []string{"ops"})
	if len(gs["carol"]) != 0 {
		t.Errorf("carol's groups: %v", gs["carol"])
	}
	groups, err := db.Query[gen.Group]().With(gen.Groups.Users.OrderBy(gen.Users.Name.Asc())).OrderBy(gen.Groups.Name.Asc()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	members := map[string][]string{}
	for _, g := range groups {
		us, ok := g.Users.Get()
		if !ok {
			t.Fatalf("%s: users not loaded", g.Name)
		}
		members[g.Name] = names(us)
	}
	wantStrings(t, "devs", members["devs"], []string{"ada"})
	wantStrings(t, "ops", members["ops"], []string{"ada", "bob"})
}

func TestHas(t *testing.T) {
	db := open(t)
	seed(t, db)
	q := func(p velox.Pred[gen.User]) []string {
		t.Helper()
		us, err := db.Query[gen.User]().Where(p).OrderBy(gen.Users.ID.Asc()).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return names(us)
	}
	wantStrings(t, "Groups.Has", q(gen.Users.Groups.Has()), []string{"ada", "bob"})
	wantStrings(t, "Groups.HasWhere", q(gen.Users.Groups.HasWhere(gen.Groups.Name.EQ("devs"))), []string{"ada"})
	wantStrings(t, "not Groups.Has", q(velox.Not(gen.Users.Groups.Has())), []string{"carol"})
	wantStrings(t, "Posts.HasWhere", q(gen.Users.Posts.HasWhere(gen.Posts.Title.EQ("b1"))), []string{"bob"})
}

func TestPolicy(t *testing.T) {
	db := open(t)
	seed(t, db)
	all, err := db.Query[gen.Post]().OrderBy(gen.Posts.ID.Asc()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantStrings(t, "no viewer", titles(all), []string{"a1", "a2", "b1"})
	seen, err := db.Query[gen.Post]().OrderBy(gen.Posts.ID.Asc()).Limit(10).All(gen.AsViewer(ctx))
	if err != nil {
		t.Fatal(err)
	}
	wantStrings(t, "viewer", titles(seen), []string{"a2", "b1"})
	err = db.Insert(&gen.Post{AuthorID: 1, Title: "forbidden"}).Exec(ctx)
	if !errors.Is(err, velox.ErrDenied) || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("insert: %v", err)
	}
	if n, err := db.Query[gen.Post]().Count(ctx); err != nil || n != 3 {
		t.Errorf("a denied insert sent a row: %d, %v", n, err)
	}
}

func TestStringAndJSON(t *testing.T) {
	db := open(t)
	seed(t, db)
	u, err := db.Query[gen.User]().Where(gen.Users.Name.EQ("ada")).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u.PasswordHash != "s3cret" {
		t.Fatalf("password not read: %+v", u)
	}
	s := u.String()
	if !strings.HasPrefix(s, "User(ID=") || !strings.Contains(s, "Name=ada") || !strings.Contains(s, "Role=viewer") ||
		strings.Contains(s, "s3cret") || strings.Contains(s, "PasswordHash") {
		t.Errorf("String: %s", s)
	}
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if strings.Contains(js, "s3cret") || strings.Contains(js, "password") || strings.Contains(js, `"posts"`) || strings.Contains(js, `"groups"`) {
		t.Errorf("json: %s", js)
	}
	if !strings.Contains(js, `"name":"ada"`) || !strings.Contains(js, `"created_by":"root"`) {
		t.Errorf("json lacks fields: %s", js)
	}
	u, err = db.Query[gen.User]().Where(gen.Users.Name.EQ("ada")).With(gen.Users.Posts).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"posts":[`) || strings.Contains(string(b), `"groups"`) {
		t.Errorf("json with posts loaded: %s", b)
	}
}

// A Sensitive field is not printed by any verb, for an entity value (an
// element of the []E that All returns) as for a pointer, alone, in a slice or
// inside another value.
func TestSensitiveNeverPrints(t *testing.T) {
	db := open(t)
	seed(t, db)
	us, err := db.Query[gen.User]().OrderBy(gen.Users.ID.Asc()).Limit(10).All(ctx)
	if err != nil || len(us) != 3 {
		t.Fatalf("%d users, %v", len(us), err)
	}
	if us[0].PasswordHash != "s3cret" {
		t.Fatalf("password not read: %+v", us[0])
	}
	u := us[0] // a value, as All returns it
	for what, s := range map[string]string{
		"%v value":        fmt.Sprintf("%v", u),
		"%+v value":       fmt.Sprintf("%+v", u),
		"%#v value":       fmt.Sprintf("%#v", u),
		"%s value":        fmt.Sprintf("%s", u),
		"Sprint value":    fmt.Sprint(u),
		"%v pointer":      fmt.Sprintf("%v", &u),
		"%+v pointer":     fmt.Sprintf("%+v", &u),
		"%#v pointer":     fmt.Sprintf("%#v", &u),
		"%v slice":        fmt.Sprintf("%v", us),
		"%+v slice":       fmt.Sprintf("%+v", us),
		"%#v slice":       fmt.Sprintf("%#v", us),
		"%v element":      fmt.Sprintf("%v", us[1]),
		"%+v in a struct": fmt.Sprintf("%+v", struct{ U gen.User }{u}),
		"%v in a map":     fmt.Sprintf("%v", map[string]gen.User{"a": u}),
	} {
		if strings.Contains(s, "s3cret") || strings.Contains(s, "PasswordHash") {
			t.Errorf("%s prints the sensitive field: %s", what, s)
		}
		if !strings.Contains(s, "ada") && !strings.Contains(s, "bob") {
			t.Errorf("%s does not print the entity: %s", what, s)
		}
	}
}

// A column default does no descriptor read per call: the read happens once, in
// Desc. Boxing a time.Time is the one allocation a call may make;
// the read of mixin.Time's descriptor used to cost about sixteen more.
func TestColumnDefaultsDoNotReadTheDescriptorPerCall(t *testing.T) {
	var sink any
	n := 0
	for _, c := range gen.UserDesc().Columns {
		for kind, f := range map[string]func() any{"DefaultFunc": c.DefaultFunc, "UpdateDefault": c.UpdateDefault} {
			if f == nil {
				continue
			}
			n++
			if allocs := testing.AllocsPerRun(100, func() { sink = f() }); allocs > 1 {
				t.Errorf("%s of %s: %v allocations per call", kind, c.Name, allocs)
			}
		}
	}
	_ = sink
	// mixin.Time: created_at's DefaultFunc, updated_at's DefaultFunc and UpdateDefault.
	if n < 3 {
		t.Errorf("only %d defaults found", n)
	}
}

func TestCheckEntity(t *testing.T) {
	for name, f := range map[string]func() error{
		"User":      velox.CheckEntity[gen.User],
		"Post":      velox.CheckEntity[gen.Post],
		"Group":     velox.CheckEntity[gen.Group],
		"UserGroup": velox.CheckEntity[gen.UserGroup],
		"Note":      velox.CheckEntity[gen.Note],
	} {
		if err := f(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The generated metadata names no relation's target: CheckRelation
	// resolves each, with the target its handle names.
	for name, err := range map[string]error{
		"User.Posts":  velox.CheckRelation(gen.Users.Posts),
		"User.Groups": velox.CheckRelation(gen.Users.Groups),
		"Post.Author": velox.CheckRelation(gen.Posts.Author),
		"Group.Users": velox.CheckRelation(gen.Groups.Users),
	} {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTenant(t *testing.T) {
	db := open(t)
	org := func(id int64) context.Context { return velox.WithTenant(ctx, id) }
	// An insert under WithTenant stamps org_id.
	for _, n := range []struct {
		org  int64
		text string
	}{{1, "one"}, {1, "uno"}, {2, "two"}} {
		x := gen.Note{Text: n.text}
		if err := db.Insert(&x).Exec(org(n.org)); err != nil {
			t.Fatal(err)
		}
		if x.OrgID != n.org {
			t.Errorf("%s: OrgID %d, want %d", n.text, x.OrgID, n.org)
		}
	}
	// Reads see only the tenant's rows.
	texts := func(c context.Context) []string {
		t.Helper()
		ns, err := db.Query[gen.Note]().OrderBy(gen.Notes.ID.Asc()).Limit(10).All(c)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, n := range ns {
			out = append(out, n.Text)
		}
		return out
	}
	wantStrings(t, "org 1", texts(org(1)), []string{"one", "uno"})
	wantStrings(t, "org 2", texts(org(2)), []string{"two"})
	wantStrings(t, "org 3", texts(org(3)), nil)
	wantStrings(t, "all tenants", texts(velox.AllTenants(ctx, "e2e test")), []string{"one", "uno", "two"})
	// No tenant: refused.
	if err := db.Insert(&gen.Note{Text: "x"}).Exec(ctx); !errors.Is(err, velox.ErrNoTenant) {
		t.Errorf("insert without a tenant: %v", err)
	}
	if _, err := db.Query[gen.Note]().Limit(10).All(ctx); !errors.Is(err, velox.ErrNoTenant) {
		t.Errorf("query without a tenant: %v", err)
	}
	// Another tenant's id on the row is refused.
	if err := db.Insert(&gen.Note{OrgID: 2, Text: "x"}).Exec(org(1)); !errors.Is(err, velox.ErrTenantMismatch) {
		t.Errorf("insert for another tenant: %v", err)
	}
}
