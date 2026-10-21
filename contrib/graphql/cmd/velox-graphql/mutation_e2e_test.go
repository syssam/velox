package main

import (
	"strings"
	"testing"
)

// e2eMutSchema is the schema of the mutation tests: every kind of edge a
// create input sets, and a field of each kind the input treats apart. Its
// tables share no name with internal/itest's.
//   - Writer is tenant-scoped, with a validator (name), a Nillable field
//     (bio), an Optional one with a Default (nick), an Optional number
//     without one (score), a sensitive field (password) and an Optional JSON
//     field (meta); its edges are an O2M (articles), an M2M (circles) and an
//     O2O whose key Profile holds (profile).
//   - Article's writer is an M2O whose nullable key is on its own row; its
//     policy, in gen/article_policy.go, refuses the title "forbidden" on
//     insert and lets no "locked" row change.
//   - Circle is the M2M's other side, with a unique name.
//   - Profile holds the O2O's key.
const e2eMutSchema = `package schema

import (
	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
)

type Writer struct{ schema.Schema }

func (Writer) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("name").NotEmpty(),
		field.String("bio").Optional().Nillable(),
		field.String("nick").Optional().Default("anon"),
		field.Int("score").Optional(),
		field.String("password").Sensitive(),
		field.JSON("meta", map[string]any{}).Optional(),
	}
}

func (Writer) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("articles", Article.Type),
		edge.To("circles", Circle.Type),
		edge.To("profile", Profile.Type).Unique(),
	}
}

func (Writer) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete())}
}

type Article struct{ schema.Schema }

func (Article) Fields() []schema.Field {
	return []schema.Field{
		field.String("title"),
		field.Int64("writer_id").Optional().Nillable(),
	}
}

func (Article) Edges() []schema.Edge {
	return []schema.Edge{edge.From("writer", Writer.Type).Ref("articles").Unique().Field("writer_id")}
}

func (Article) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete())}
}

type Circle struct{ schema.Schema }

func (Circle) Fields() []schema.Field { return []schema.Field{field.String("name").Unique()} }

func (Circle) Edges() []schema.Edge {
	return []schema.Edge{edge.From("writers", Writer.Type).Ref("circles")}
}

func (Circle) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete())}
}

type Profile struct{ schema.Schema }

func (Profile) Fields() []schema.Field {
	return []schema.Field{
		field.String("handle"),
		field.Int64("writer_id").Optional().Nillable(),
	}
}

func (Profile) Edges() []schema.Edge {
	return []schema.Edge{edge.From("writer", Writer.Type).Ref("profile").Unique().Field("writer_id")}
}

func (Profile) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.Create())}
}
`

// e2eMutPolicy is Article's policy, written by hand in the generated
// package, where velox-gen finds and binds it.
const e2eMutPolicy = `package gen

import (
	"context"
	"errors"

	"github.com/syssam/velox2"
)

// Policy refuses to insert an article titled "forbidden", and lets no
// "locked" article change.
func (*Article) Policy() velox.Policy[Article] {
	return velox.Policy[Article]{
		Insert: func(_ context.Context, x *Article) error {
			if x.Title == "forbidden" {
				return errors.New("the title is forbidden")
			}
			return nil
		},
		Write: func(context.Context) (velox.Pred[Article], error) { return Articles.Title.NEQ("locked"), nil },
	}
}
`

// e2eMutRun is the application's test of the generated create mutations,
// run on each of targets' databases.
const e2eMutRun = `package app_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"example.com/app/gen"
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// A target is a database the tests run on: how to open it, and its DDL.
type target struct {
	name    string
	dialect velox.Dialect
	ddl     []string
	open    func(t *testing.T) *sql.DB
}

// targets are the databases the tests run on: SQLite.
func targets(t *testing.T) []target {
	t.Helper()
	return []target{{
		name: "sqlite", dialect: velox.SQLite, ddl: sqliteDDL,
		open: func(t *testing.T) *sql.DB {
			db, err := sql.Open("sqlite", "file::memory:")
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			return db
		},
	}}
}

var sqliteDDL = []string{
	"CREATE TABLE writers (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, name TEXT NOT NULL, bio TEXT, nick TEXT NOT NULL, score INTEGER NOT NULL, password TEXT NOT NULL, meta TEXT)",
	"CREATE TABLE articles (id INTEGER PRIMARY KEY, title TEXT NOT NULL, writer_id INTEGER)",
	"CREATE TABLE circles (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE)",
	"CREATE TABLE profiles (id INTEGER PRIMARY KEY, handle TEXT NOT NULL, writer_id INTEGER UNIQUE)",
	"CREATE TABLE writer_circles (writer_id INTEGER NOT NULL, circle_id INTEGER NOT NULL, PRIMARY KEY (writer_id, circle_id))",
}

// env is a test's database: ctx is under tenant 7, with the DB put there by
// the extension's Context, as for a job outside a server; stmts are the
// statements sent since the last reset.
type env struct {
	ctx context.Context
	db  *velox.DB
	mu  sync.Mutex
	sql []string
}

func (e *env) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sql = nil
}

func (e *env) stmts() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.sql...)
}

// each runs fn on each target, with a fresh database.
func each(t *testing.T, fn func(t *testing.T, e *env)) {
	for _, tg := range targets(t) {
		t.Run(tg.name, func(t *testing.T) {
			sqlDB := tg.open(t)
			t.Cleanup(func() { _ = sqlDB.Close() })
			for _, s := range tg.ddl {
				if _, err := sqlDB.Exec(s); err != nil {
					t.Fatalf("%s: %v", s, err)
				}
			}
			e := &env{}
			e.db = velox.Open(tg.dialect, sqlDB, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
				e.mu.Lock()
				e.sql = append(e.sql, st.SQL)
				e.mu.Unlock()
				return next(ctx)
			}))
			e.ctx = velox.WithTenant(veloxgql.NewExtension(e.db).Context(context.Background()), int64(7))
			fn(t, e)
		})
	}
}

func ptr[T any](v T) *T { return &v }

func writerID(id int64) string  { return veloxgql.EncodeID("Writer", id) }
func articleID(id int64) string { return veloxgql.EncodeID("Article", id) }
func circleID(id int64) string  { return veloxgql.EncodeID("Circle", id) }
func profileID(id int64) string { return veloxgql.EncodeID("Profile", id) }

// code is the GraphQL code of err: of the first error in its chain that has
// one.
func code(err error) string {
	var c interface{ Extensions() map[string]any }
	if !errors.As(err, &c) {
		return ""
	}
	s, _ := c.Extensions()["code"].(string)
	return s
}

func insert[E any, PE velox.Entity[E]](t *testing.T, ctx context.Context, db *velox.DB, x PE) {
	t.Helper()
	if err := db.Insert(x).Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func count[E any](t *testing.T, ctx context.Context, q velox.Query[E]) int64 {
	t.Helper()
	n, err := q.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// circlesOf are the names of the circles writer id is in, loaded through
// the edge Writer.circles.
func circlesOf(t *testing.T, e *env, id int64) []string {
	t.Helper()
	w, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(id)).With(gen.Writers.Circles).One(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := w.Circles.Get()
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestCreate(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		c1, c2 := &gen.Circle{Name: "c1"}, &gen.Circle{Name: "c2"}
		a1, a2 := &gen.Article{Title: "a1"}, &gen.Article{Title: "a2"}
		p1 := &gen.Profile{Handle: "p1"}
		insert(t, e.ctx, e.db, c1)
		insert(t, e.ctx, e.db, c2)
		insert(t, e.ctx, e.db, a1)
		insert(t, e.ctx, e.db, a2)
		insert(t, e.ctx, e.db, p1)
		input := gen.CreateWriterInput{
			Name: "ann", Password: "s3cret", Meta: ptr(veloxgql.JSON(` + "`" + `{"k":"v"}` + "`" + `)),
			CircleIDs: []string{circleID(c1.ID), circleID(c2.ID)}, ArticleIDs: []string{articleID(a1.ID)}, ProfileID: ptr(profileID(p1.ID)),
		}
		w, err := gen.CreateWriter(e.ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if w.Nick != "anon" || w.Score != 0 || w.OrgID != 7 || w.Bio != nil || w.Password != "s3cret" || w.Meta["k"] != "v" || w.ID == 0 {
			t.Errorf("CreateWriter = %v (password %q, meta %v), want nick anon, score 0, org 7, no bio", w, w.Password, w.Meta)
		}
		got, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(w.ID)).One(e.ctx)
		if err != nil || got.Nick != "anon" || got.OrgID != 7 || got.Meta["k"] != "v" {
			t.Errorf("the stored writer: %v, %v", got, err)
		}
		art, err := e.db.Query[gen.Article]().Where(gen.Articles.ID.EQ(a1.ID)).One(e.ctx)
		if err != nil || art.WriterID == nil || *art.WriterID != w.ID {
			t.Errorf("article a1: %v, %v: want it re-pointed to %d", art, err, w.ID)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Article]().Where(gen.Articles.WriterID.IsNull())); n != 1 {
			t.Errorf("%d articles without a writer, want 1 (a2)", n)
		}
		prof, err := e.db.Query[gen.Profile]().Where(gen.Profiles.ID.EQ(p1.ID)).One(e.ctx)
		if err != nil || prof.WriterID == nil || *prof.WriterID != w.ID {
			t.Errorf("profile p1: %v, %v: want it re-pointed to %d", prof, err, w.ID)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]().Where(gen.WriterCircles.WriterID.EQ(w.ID))); n != 2 {
			t.Errorf("%d join rows, want 2", n)
		}
		for _, s := range []string{fmt.Sprint(input), fmt.Sprintf("%#v", input), fmt.Sprintf("%+v", input)} {
			if strings.Contains(s, "s3cret") || !strings.Contains(s, "ann") {
				t.Errorf("the input prints as %s", s)
			}
		}
	})
}

func TestCreateM2MBothSides(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w1 := &gen.Writer{Name: "w1", Password: "x"}
		insert(t, e.ctx, e.db, w1)
		c, err := gen.CreateCircle(e.ctx, gen.CreateCircleInput{Name: "from-circle", WriterIDs: []string{writerID(w1.ID)}})
		if err != nil {
			t.Fatal(err)
		}
		if got := circlesOf(t, e, w1.ID); len(got) != 1 || got[0] != c.Name {
			t.Errorf("w1's circles: %v, want [from-circle]", got)
		}
		c2 := &gen.Circle{Name: "c2"}
		insert(t, e.ctx, e.db, c2)
		w, err := gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "w2", Password: "x", CircleIDs: []string{circleID(c.ID), circleID(c2.ID)}})
		if err != nil {
			t.Fatal(err)
		}
		if got := circlesOf(t, e, w.ID); strings.Join(got, ",") != "from-circle,c2" && strings.Join(got, ",") != "c2,from-circle" {
			t.Errorf("w2's circles: %v, want from-circle and c2", got)
		}
	})
}

// TestRepeatedIDs: an ID given twice counts once.
func TestRepeatedIDs(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		c := &gen.Circle{Name: "c"}
		a := &gen.Article{Title: "a"}
		insert(t, e.ctx, e.db, c)
		insert(t, e.ctx, e.db, a)
		w, err := gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x",
			CircleIDs: []string{circleID(c.ID), circleID(c.ID)}, ArticleIDs: []string{articleID(a.ID), articleID(a.ID)}})
		if err != nil {
			t.Fatal(err)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]().Where(gen.WriterCircles.WriterID.EQ(w.ID))); n != 1 {
			t.Errorf("%d join rows, want 1", n)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Article]().Where(gen.Articles.WriterID.EQ(w.ID))); n != 1 {
			t.Errorf("%d articles of the writer, want 1", n)
		}
	})
}

// TestEdgeScope: an ID of a row the caller may not see, or change, is
// NOT_FOUND, and nothing is written.
func TestEdgeScope(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		other := &gen.Writer{Name: "other", Password: "x"}
		insert(t, velox.WithTenant(e.ctx, int64(8)), e.db, other)
		locked := &gen.Article{Title: "locked"}
		insert(t, e.ctx, e.db, locked)

		e.reset()
		_, err := gen.CreateArticle(e.ctx, gen.CreateArticleInput{Title: "t", WriterID: ptr(writerID(other.ID))})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateArticleInput.writerID: no Writer has this ID" {
			t.Errorf("tenant 8's writer: %v (code %q), want NOT_FOUND", err, code(err))
		}
		for _, s := range e.stmts() {
			if strings.HasPrefix(s, "INSERT") {
				t.Errorf("sent %s", s)
			}
		}
		if n := count(t, e.ctx, e.db.Query[gen.Article]()); n != 1 {
			t.Errorf("%d articles, want 1: the locked one", n)
		}

		_, err = gen.CreateCircle(e.ctx, gen.CreateCircleInput{Name: "c", WriterIDs: []string{writerID(other.ID)}})
		if code(err) != veloxgql.CodeNotFound {
			t.Errorf("a circle of tenant 8's writer: %v (code %q), want NOT_FOUND", err, code(err))
		}
		if n := count(t, e.ctx, e.db.Query[gen.Circle]()); n != 0 {
			t.Errorf("%d circles, want 0", n)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]()); n != 0 {
			t.Errorf("%d join rows, want 0", n)
		}

		// The Write policy hides the locked article from the UPDATE that
		// re-points it.
		_, err = gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", ArticleIDs: []string{articleID(locked.ID)}})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateWriterInput.articleIDs: no Article has this ID" {
			t.Errorf("the locked article: %v (code %q), want NOT_FOUND", err, code(err))
		}
		if n := count(t, e.ctx, e.db.Query[gen.Writer]()); n != 0 {
			t.Errorf("%d writers of tenant 7, want 0", n)
		}
	})
}

// TestAtomic: a create that fails after its first write keeps nothing,
// outside a server, where it runs in a transaction of its own.
func TestAtomic(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		c := &gen.Circle{Name: "c"}
		a := &gen.Article{Title: "a"}
		insert(t, e.ctx, e.db, c)
		insert(t, e.ctx, e.db, a)
		_, err := gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", CircleIDs: []string{circleID(c.ID), circleID(999)}})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateWriterInput.circleIDs: 1 of the 2 IDs name no Circle" {
			t.Errorf("a missing circle: %v (code %q), want NOT_FOUND", err, code(err))
		}
		// The second article is missing: the writer is inserted, and a
		// re-pointed, before the UPDATE's count fails the create.
		_, err = gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", ArticleIDs: []string{articleID(a.ID), articleID(999)}})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateWriterInput.articleIDs: 1 of the 2 IDs name no Article" {
			t.Errorf("a missing article: %v (code %q), want NOT_FOUND", err, code(err))
		}
		if n := count(t, e.ctx, e.db.Query[gen.Writer]()); n != 0 {
			t.Errorf("%d writers, want 0", n)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Article]().Where(gen.Articles.WriterID.NotNull())); n != 0 {
			t.Errorf("%d articles with a writer, want 0", n)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]()); n != 0 {
			t.Errorf("%d join rows, want 0", n)
		}
	})
}

func TestCreateErrors(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w := &gen.Writer{Name: "w", Password: "x"}
		insert(t, e.ctx, e.db, w)
		insert(t, e.ctx, e.db, &gen.Circle{Name: "taken"})

		_, err := gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "", Password: "x"})
		var ve *velox.ValidationError
		if code(err) != veloxgql.CodeBadUserInput || err == nil || !strings.HasPrefix(err.Error(), "veloxgql: CreateWriterInput.name: invalid value: ") || !errors.As(err, &ve) {
			t.Errorf("an empty name: %v (code %q)", err, code(err))
		}

		_, err = gen.CreateArticle(e.ctx, gen.CreateArticleInput{Title: "forbidden"})
		if !errors.Is(err, velox.ErrDenied) {
			t.Errorf("a forbidden title: %v, want velox.ErrDenied", err)
		}

		_, err = gen.CreateCircle(e.ctx, gen.CreateCircleInput{Name: "taken"})
		var ce *velox.ConstraintError
		if !errors.As(err, &ce) {
			t.Errorf("a taken name: %v, want a *velox.ConstraintError", err)
		}

		// Input errors fail before any statement.
		e.reset()
		for _, bad := range []veloxgql.JSON{veloxgql.JSON("[1, 2]"), veloxgql.JSON("{")} {
			_, err = gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "m", Password: "x", Meta: &bad})
			if code(err) != veloxgql.CodeBadUserInput || !strings.Contains(err.Error(), "CreateWriterInput.meta") {
				t.Errorf("meta %s: %v (code %q)", bad, err, code(err))
			}
		}
		_, err = gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "m", Password: "x", CircleIDs: []string{writerID(w.ID)}})
		if code(err) != veloxgql.CodeBadUserInput || !errors.Is(err, veloxgql.ErrWrongIDType) || !strings.Contains(err.Error(), "CreateWriterInput.circleIDs[0]") {
			t.Errorf("a writer's ID for a circle: %v (code %q)", err, code(err))
		}
		_, err = gen.CreateArticle(e.ctx, gen.CreateArticleInput{Title: "t", WriterID: ptr("not an ID")})
		if code(err) != veloxgql.CodeBadUserInput || !errors.Is(err, veloxgql.ErrBadID) {
			t.Errorf("a malformed ID: %v (code %q)", err, code(err))
		}
		many := make([]string, 10000)
		for i := range many {
			many[i] = circleID(int64(i + 1))
		}
		_, err = gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "m", Password: "x", CircleIDs: many})
		if code(err) != veloxgql.CodeBadUserInput || !errors.Is(err, veloxgql.ErrPageTooLarge) || !strings.Contains(err.Error(), "CreateWriterInput.circleIDs") {
			t.Errorf("10000 circle IDs: %v (code %q)", err, code(err))
		}
		if s := e.stmts(); len(s) != 0 {
			t.Errorf("input errors sent %v", s)
		}
	})
}
`

// TestMutationsRun generates e2eMutSchema's Go and runs e2eMutRun on it:
// createX with each kind of edge, the tenant and policy scope of the IDs it
// names, atomicity, and its errors.
func TestMutationsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{"schema/schema.go": e2eMutSchema, "gen/article_policy.go": e2eMutPolicy, "mutation_test.go": e2eMutRun})
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	if out, err := run("vet", "./gen"); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	out, err := run("test", "-count=1", "-v", ".")
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
	for _, name := range []string{"TestCreate", "TestCreateM2MBothSides", "TestRepeatedIDs", "TestEdgeScope", "TestAtomic", "TestCreateErrors"} {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			t.Errorf("no PASS of %s in\n%s", name, out)
		}
	}
}
