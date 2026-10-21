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
//     field (meta); its edges are an O2M (articles), an M2M (circles), an
//     O2O whose key Profile holds (profile), and a tenant-scoped O2M to
//     itself (mentees, whose inverse is mentor); its policy, in
//     gen/writer_policy.go, lets no "frozen" writer change.
//   - Article's writer is an M2O whose nullable key is on its own row; its
//     policy, in gen/article_policy.go, refuses the title "forbidden" on
//     insert and lets no "locked" row change.
//   - Circle is the M2M's other side, with a unique name; its policy, in
//     gen/circle_policy.go, hides the circle named "hidden".
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
		field.Int64("mentor_id").Optional().Nillable(),
	}
}

func (Writer) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("articles", Article.Type),
		edge.To("circles", Circle.Type),
		edge.To("profile", Profile.Type).Unique(),
		edge.To("mentees", Writer.Type),
		edge.From("mentor", Writer.Type).Ref("mentees").Unique().Field("mentor_id"),
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

// e2eMutWriterPolicy is Writer's policy: its Write lets no "frozen" writer
// change, though every writer may be read.
const e2eMutWriterPolicy = `package gen

import (
	"context"

	"github.com/syssam/velox2"
)

// Policy lets no "frozen" writer change.
func (*Writer) Policy() velox.Policy[Writer] {
	return velox.Policy[Writer]{
		Write: func(context.Context) (velox.Pred[Writer], error) { return Writers.Name.NEQ("frozen"), nil },
	}
}
`

// e2eMutCirclePolicy is Circle's policy: its Read hides the circle named
// "hidden".
const e2eMutCirclePolicy = `package gen

import (
	"context"

	"github.com/syssam/velox2"
)

// Policy hides the circle named "hidden".
func (*Circle) Policy() velox.Policy[Circle] {
	return velox.Policy[Circle]{
		Read: func(context.Context) (velox.Pred[Circle], error) { return Circles.Name.NEQ("hidden"), nil },
	}
}
`

// e2eMutRun is the application's test of the generated create and update
// mutations, run on each of targets' databases.
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
	"CREATE TABLE writers (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, name TEXT NOT NULL, bio TEXT, nick TEXT NOT NULL, score INTEGER NOT NULL, password TEXT NOT NULL, meta TEXT, mentor_id INTEGER)",
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
	raw *sql.DB // the database under db, for what velox does not read
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
			e := &env{raw: sqlDB}
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

		// On update, a circle or an article already linked to the writer
		// succeeds and changes nothing, given once or twice: the article's
		// UPDATE matches no row, and the count of its rows that are the
		// writer's finds it.
		for _, in := range []gen.UpdateWriterInput{
			{AddCircleIDs: []string{circleID(c.ID)}, AddArticleIDs: []string{articleID(a.ID)}},
			{AddCircleIDs: []string{circleID(c.ID), circleID(c.ID)}, AddArticleIDs: []string{articleID(a.ID), articleID(a.ID)}},
		} {
			if _, err := gen.UpdateWriter(e.ctx, writerID(w.ID), in); err != nil {
				t.Errorf("%v: %v", in, err)
			}
			if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]().Where(gen.WriterCircles.WriterID.EQ(w.ID))); n != 1 {
				t.Errorf("%v: %d join rows, want 1", in, n)
			}
			if n := count(t, e.ctx, e.db.Query[gen.Article]().Where(gen.Articles.WriterID.EQ(w.ID))); n != 1 {
				t.Errorf("%v: %d articles of the writer, want 1", in, n)
			}
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

		// The Read policy hides a circle from the COUNT that checks the
		// M2M's IDs.
		hidden := &gen.Circle{Name: "hidden"}
		insert(t, e.ctx, e.db, hidden)
		_, err = gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", CircleIDs: []string{circleID(hidden.ID)}})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateWriterInput.circleIDs: no Circle has this ID" {
			t.Errorf("the hidden circle: %v (code %q), want NOT_FOUND", err, code(err))
		}
		if n := count(t, e.ctx, e.db.Query[gen.Writer]()); n != 0 {
			t.Errorf("%d writers of tenant 7, want 0", n)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]()); n != 0 {
			t.Errorf("%d join rows, want 0", n)
		}

		// The tenant scope hides tenant 8's writer from the UPDATE that
		// re-points a mentee, and from the count of linked mentees: it has
		// a mentor, and still reads as missing.
		ctx8 := velox.WithTenant(e.ctx, int64(8))
		mentor8 := &gen.Writer{Name: "mentor8", Password: "x"}
		insert(t, ctx8, e.db, mentor8)
		mentee8 := &gen.Writer{Name: "mentee8", Password: "x", MentorID: ptr(mentor8.ID)}
		insert(t, ctx8, e.db, mentee8)
		for _, id := range []int64{other.ID, mentee8.ID} {
			_, err = gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", MenteeIDs: []string{writerID(id)}})
			if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateWriterInput.menteeIDs: no Writer has this ID" {
				t.Errorf("tenant 8's writer %d as a mentee: %v (code %q), want NOT_FOUND", id, err, code(err))
			}
		}
		if n := count(t, e.ctx, e.db.Query[gen.Writer]()); n != 0 {
			t.Errorf("%d writers of tenant 7, want 0", n)
		}
		got, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(mentee8.ID)).One(ctx8)
		if err != nil || got.MentorID == nil || *got.MentorID != mentor8.ID {
			t.Errorf("tenant 8's mentee: %v, %v: want its mentor kept", got, err)
		}
		if n := count(t, ctx8, e.db.Query[gen.Writer]().Where(gen.Writers.MentorID.IsNull())); n != 2 {
			t.Errorf("%d writers of tenant 8 without a mentor, want 2", n)
		}

		// On update: tenant 8's writer is NOT_FOUND, whether the input sets
		// a field or none, and no join row of it is written; and so is a
		// mentee of tenant 8, or the locked article, which the Write policy
		// hides from the UPDATE that adds it. Nothing moves.
		visible := &gen.Circle{Name: "visible"}
		insert(t, e.ctx, e.db, visible)
		for _, in := range []gen.UpdateWriterInput{{Name: ptr("x")}, {}, {AddCircleIDs: []string{circleID(visible.ID)}}} {
			_, err = gen.UpdateWriter(e.ctx, writerID(other.ID), in)
			if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: updateWriter: no Writer has this ID" {
				t.Errorf("updating tenant 8's writer with %v: %v (code %q), want NOT_FOUND", in, err, code(err))
			}
		}
		if got, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(other.ID)).One(ctx8); err != nil || got.Name != "other" {
			t.Errorf("tenant 8's writer: %v, %v: want it unchanged", got, err)
		}
		w7 := &gen.Writer{Name: "w7", Password: "x"}
		insert(t, e.ctx, e.db, w7)
		for _, c := range []struct {
			in   gen.UpdateWriterInput
			want string
		}{
			{gen.UpdateWriterInput{AddMenteeIDs: []string{writerID(mentee8.ID)}}, "veloxgql: UpdateWriterInput.addMenteeIDs: no Writer has this ID"},
			{gen.UpdateWriterInput{AddMenteeIDs: []string{writerID(other.ID)}}, "veloxgql: UpdateWriterInput.addMenteeIDs: no Writer has this ID"},
			{gen.UpdateWriterInput{AddArticleIDs: []string{articleID(locked.ID)}}, "veloxgql: UpdateWriterInput.addArticleIDs: no Article has this ID"},
			{gen.UpdateWriterInput{AddCircleIDs: []string{circleID(hidden.ID)}}, "veloxgql: UpdateWriterInput.addCircleIDs: no Circle has this ID"},
			{gen.UpdateWriterInput{MentorID: ptr(writerID(other.ID))}, "veloxgql: UpdateWriterInput.mentorID: no Writer has this ID"},
		} {
			_, err = gen.UpdateWriter(e.ctx, writerID(w7.ID), c.in)
			if code(err) != veloxgql.CodeNotFound || err.Error() != c.want {
				t.Errorf("%v: %v (code %q), want %q", c.in, err, code(err), c.want)
			}
		}
		if got, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(mentee8.ID)).One(ctx8); err != nil || got.MentorID == nil || *got.MentorID != mentor8.ID {
			t.Errorf("tenant 8's mentee: %v, %v: want its mentor kept", got, err)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Article]().Where(gen.Articles.WriterID.NotNull())); n != 0 {
			t.Errorf("%d articles with a writer, want 0", n)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]()); n != 0 {
			t.Errorf("%d join rows, want 0", n)
		}
		if got, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(w7.ID)).One(e.ctx); err != nil || got.MentorID != nil {
			t.Errorf("w7: %v, %v: want no mentor", got, err)
		}
	})
}

// TestRepoint: a child that another owner holds is refused, not taken from
// it, and nothing is kept; a child no owner holds is taken; a missing one is
// NOT_FOUND.
func TestRepoint(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w0 := &gen.Writer{Name: "w0", Password: "x"}
		insert(t, e.ctx, e.db, w0)
		held, free := &gen.Article{Title: "held", WriterID: ptr(w0.ID)}, &gen.Article{Title: "free"}
		heldP, freeP := &gen.Profile{Handle: "held", WriterID: ptr(w0.ID)}, &gen.Profile{Handle: "free"}
		insert(t, e.ctx, e.db, held)
		insert(t, e.ctx, e.db, free)
		insert(t, e.ctx, e.db, heldP)
		insert(t, e.ctx, e.db, freeP)
		writerOf := func(id int64) *int64 {
			t.Helper()
			a, err := e.db.Query[gen.Article]().Where(gen.Articles.ID.EQ(id)).One(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			return a.WriterID
		}

		for _, c := range []struct {
			name string
			in   gen.CreateWriterInput
			code string
			want string
		}{
			{"an article another writer holds", gen.CreateWriterInput{ArticleIDs: []string{articleID(free.ID), articleID(held.ID)}},
				veloxgql.CodeBadUserInput, "veloxgql: CreateWriterInput.articleIDs: an Article is already linked to another Writer"},
			{"a profile another writer holds", gen.CreateWriterInput{ProfileID: ptr(profileID(heldP.ID))},
				veloxgql.CodeBadUserInput, "veloxgql: CreateWriterInput.profileID: a Profile is already linked to another Writer"},
			{"a missing profile", gen.CreateWriterInput{ProfileID: ptr(profileID(999))},
				veloxgql.CodeNotFound, "veloxgql: CreateWriterInput.profileID: no Profile has this ID"},
			{"a missing article", gen.CreateWriterInput{ArticleIDs: []string{articleID(free.ID), articleID(999)}},
				veloxgql.CodeNotFound, "veloxgql: CreateWriterInput.articleIDs: 1 of the 2 IDs name no Article"},
		} {
			c.in.Name, c.in.Password = "w", "x"
			_, err := gen.CreateWriter(e.ctx, c.in)
			if code(err) != c.code || err.Error() != c.want {
				t.Errorf("%s: %v (code %q), want %q, %s", c.name, err, code(err), c.want, c.code)
			}
			if n := count(t, e.ctx, e.db.Query[gen.Writer]()); n != 1 {
				t.Errorf("%s: %d writers, want 1: w0", c.name, n)
			}
			if w := writerOf(held.ID); w == nil || *w != w0.ID {
				t.Errorf("%s: the held article's writer is %v, want w0", c.name, w)
			}
			if w := writerOf(free.ID); w != nil {
				t.Errorf("%s: the free article's writer is %d, want none", c.name, *w)
			}
			if n := count(t, e.ctx, e.db.Query[gen.Profile]().Where(gen.Profiles.WriterID.EQ(w0.ID))); n != 1 {
				t.Errorf("%s: w0 has %d profiles, want 1", c.name, n)
			}
		}

		w, err := gen.CreateWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", ArticleIDs: []string{articleID(free.ID)}, ProfileID: ptr(profileID(freeP.ID))})
		if err != nil {
			t.Fatal(err)
		}
		if got := writerOf(free.ID); got == nil || *got != w.ID {
			t.Errorf("the free article's writer is %v, want %d", got, w.ID)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Profile]().Where(gen.Profiles.WriterID.EQ(w.ID))); n != 1 {
			t.Errorf("the new writer has %d profiles, want 1", n)
		}

		// On update, add<Edge>IDs and <edge>ID take a child no owner holds,
		// or w's own; one w0 holds is refused, and nothing is kept.
		for _, c := range []struct {
			name string
			in   gen.UpdateWriterInput
			want string
		}{
			{"an article w0 holds", gen.UpdateWriterInput{Bio: ptr("b"), AddArticleIDs: []string{articleID(free.ID), articleID(held.ID)}},
				"veloxgql: UpdateWriterInput.addArticleIDs: an Article is already linked to another Writer"},
			{"a profile w0 holds", gen.UpdateWriterInput{Bio: ptr("b"), ProfileID: ptr(profileID(heldP.ID))},
				"veloxgql: UpdateWriterInput.profileID: a Profile is already linked to another Writer"},
		} {
			_, err := gen.UpdateWriter(e.ctx, writerID(w.ID), c.in)
			if code(err) != veloxgql.CodeBadUserInput || err.Error() != c.want {
				t.Errorf("%s: %v (code %q), want %q", c.name, err, code(err), c.want)
			}
			if w := writerOf(held.ID); w == nil || *w != w0.ID {
				t.Errorf("%s: the held article's writer is %v, want w0", c.name, w)
			}
			if n := count(t, e.ctx, e.db.Query[gen.Profile]().Where(gen.Profiles.WriterID.EQ(w0.ID))); n != 1 {
				t.Errorf("%s: w0 has %d profiles, want 1", c.name, n)
			}
			if n := count(t, e.ctx, e.db.Query[gen.Profile]().Where(gen.Profiles.WriterID.EQ(w.ID))); n != 1 {
				t.Errorf("%s: w has %d profiles, want 1: its own kept", c.name, n)
			}
			if got, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(w.ID)).One(e.ctx); err != nil || got.Bio != nil {
				t.Errorf("%s: w: %v, %v: want no bio", c.name, got, err)
			}
		}
		// w's own profile, and its own article, again: nothing changes.
		if _, err := gen.UpdateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{ProfileID: ptr(profileID(freeP.ID)), AddArticleIDs: []string{articleID(free.ID)}}); err != nil {
			t.Errorf("w's own profile and article: %v", err)
		}
		if got := writerOf(free.ID); got == nil || *got != w.ID {
			t.Errorf("the free article's writer is %v, want %d", got, w.ID)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Profile]().Where(gen.Profiles.WriterID.EQ(w.ID))); n != 1 {
			t.Errorf("w has %d profiles, want 1", n)
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

// writer is the stored writer id.
func writer(t *testing.T, e *env, id int64) *gen.Writer {
	t.Helper()
	w, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(id)).One(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// writerOfArticle is the writer_id of article id.
func writerOfArticle(t *testing.T, e *env, id int64) *int64 {
	t.Helper()
	a, err := e.db.Query[gen.Article]().Where(gen.Articles.ID.EQ(id)).One(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return a.WriterID
}

// writerOfProfile is the writer_id of profile id.
func writerOfProfile(t *testing.T, e *env, id int64) *int64 {
	t.Helper()
	p, err := e.db.Query[gen.Profile]().Where(gen.Profiles.ID.EQ(id)).One(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return p.WriterID
}

func is(p *int64, v int64) bool { return p != nil && *p == v }

func TestUpdate(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w := &gen.Writer{Name: "w", Password: "old"}
		insert(t, e.ctx, e.db, w)
		mentor := &gen.Writer{Name: "mentor", Password: "x"}
		insert(t, e.ctx, e.db, mentor)
		a1, a2, a3 := &gen.Article{Title: "a1", WriterID: ptr(w.ID)}, &gen.Article{Title: "a2"}, &gen.Article{Title: "a3"}
		c1, c2 := &gen.Circle{Name: "c1"}, &gen.Circle{Name: "c2"}
		p1, p2 := &gen.Profile{Handle: "p1", WriterID: ptr(w.ID)}, &gen.Profile{Handle: "p2"}
		for _, x := range []*gen.Article{a1, a2, a3} {
			insert(t, e.ctx, e.db, x)
		}
		insert(t, e.ctx, e.db, c1)
		insert(t, e.ctx, e.db, c2)
		insert(t, e.ctx, e.db, p1)
		insert(t, e.ctx, e.db, p2)
		insert(t, e.ctx, e.db, &gen.WriterCircle{WriterID: w.ID, CircleID: c1.ID})
		id := writerID(w.ID)

		// Fields: nil leaves one, a value sets it, clear sets NULL.
		in := gen.UpdateWriterInput{Bio: ptr("hi"), Password: ptr("n3w-s3cret"), Score: ptr(3), Meta: ptr(veloxgql.JSON(` + "`" + `{"k":"v"}` + "`" + `))}
		got, err := gen.UpdateWriter(e.ctx, id, in)
		if err != nil {
			t.Fatal(err)
		}
		if got.Bio == nil || *got.Bio != "hi" || got.Nick != "anon" || got.Name != "w" || got.Score != 3 || got.Password != "n3w-s3cret" || got.Meta["k"] != "v" || got.OrgID != 7 {
			t.Errorf("UpdateWriter = %v (password %q, meta %v), want bio hi, nick anon, score 3", got, got.Password, got.Meta)
		}
		for _, s := range []string{fmt.Sprint(in), fmt.Sprintf("%#v", in), fmt.Sprint(got), fmt.Sprintf("%+v", got)} {
			if strings.Contains(s, "n3w-s3cret") {
				t.Errorf("prints the password: %s", s)
			}
		}
		if st := writer(t, e, w.ID); st.Bio == nil || *st.Bio != "hi" || st.Password != "n3w-s3cret" || st.Nick != "anon" {
			t.Errorf("the stored writer: %v", st)
		}
		got, err = gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{ClearBio: true, ClearMeta: true, Nick: ptr("nick")})
		if err != nil {
			t.Fatal(err)
		}
		if st := writer(t, e, w.ID); st.Bio != nil || got.Bio != nil || st.Meta != nil || st.Nick != "nick" || st.Score != 3 {
			t.Errorf("after clearBio and clearMeta: stored %v, returned %v: want no bio, no meta, nick nick, score 3", st, got)
		}
		// Both columns hold SQL NULL: meta, an Optional JSON map, too.
		var nulls int
		if err := e.raw.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM writers WHERE id = %d AND bio IS NULL AND meta IS NULL", w.ID)).Scan(&nulls); err != nil || nulls != 1 {
			t.Errorf("w's bio and meta are SQL NULL: %d rows, %v; want 1", nulls, err)
		}

		// A unique edge whose key is on the row: set, then cleared.
		if _, err := gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{MentorID: ptr(writerID(mentor.ID))}); err != nil {
			t.Fatal(err)
		}
		if st := writer(t, e, w.ID); !is(st.MentorID, mentor.ID) {
			t.Errorf("mentor %v, want %d", st.MentorID, mentor.ID)
		}
		if _, err := gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{ClearMentor: true}); err != nil {
			t.Fatal(err)
		}
		if st := writer(t, e, w.ID); st.MentorID != nil {
			t.Errorf("mentor %v after clearMentor, want none", *st.MentorID)
		}

		// O2M: remove and add move the articles' writer_id; clear then add
		// replaces the set.
		if _, err := gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{AddArticleIDs: []string{articleID(a2.ID)}, RemoveArticleIDs: []string{articleID(a1.ID), articleID(a3.ID)}}); err != nil {
			t.Fatal(err)
		}
		if a, b, c := writerOfArticle(t, e, a1.ID), writerOfArticle(t, e, a2.ID), writerOfArticle(t, e, a3.ID); a != nil || !is(b, w.ID) || c != nil {
			t.Errorf("articles' writers %v %v %v, want none, %d, none", a, b, c, w.ID)
		}
		if _, err := gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{ClearArticles: true, AddArticleIDs: []string{articleID(a3.ID)}}); err != nil {
			t.Fatal(err)
		}
		if a, b, c := writerOfArticle(t, e, a1.ID), writerOfArticle(t, e, a2.ID), writerOfArticle(t, e, a3.ID); a != nil || b != nil || !is(c, w.ID) {
			t.Errorf("articles' writers %v %v %v, want none, none, %d", a, b, c, w.ID)
		}
		if _, err := gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{ClearArticles: true}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Article]().Where(gen.Articles.WriterID.NotNull())); n != 0 {
			t.Errorf("%d articles with a writer after clearArticles, want 0", n)
		}

		// M2M: add and remove change the join rows; clear removes them.
		if _, err := gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{AddCircleIDs: []string{circleID(c2.ID)}, RemoveCircleIDs: []string{circleID(c1.ID)}}); err != nil {
			t.Fatal(err)
		}
		if got := circlesOf(t, e, w.ID); len(got) != 1 || got[0] != "c2" {
			t.Errorf("circles %v, want [c2]", got)
		}
		if _, err := gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{ClearCircles: true, AddCircleIDs: []string{circleID(c1.ID)}}); err != nil {
			t.Fatal(err)
		}
		if got := circlesOf(t, e, w.ID); len(got) != 1 || got[0] != "c1" {
			t.Errorf("circles %v, want [c1]", got)
		}
		// From the M2M's other side.
		if _, err := gen.UpdateCircle(e.ctx, circleID(c2.ID), gen.UpdateCircleInput{AddWriterIDs: []string{id}}); err != nil {
			t.Fatal(err)
		}
		if got := circlesOf(t, e, w.ID); len(got) != 2 {
			t.Errorf("circles %v, want c1 and c2", got)
		}
		if _, err := gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{ClearCircles: true}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]()); n != 0 {
			t.Errorf("%d join rows after clearCircles, want 0", n)
		}

		// O2O whose key the profile holds: profileID moves it and clears the
		// one before; clearProfile clears it.
		if _, err := gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{ProfileID: ptr(profileID(p2.ID))}); err != nil {
			t.Fatal(err)
		}
		if a, b := writerOfProfile(t, e, p1.ID), writerOfProfile(t, e, p2.ID); a != nil || !is(b, w.ID) {
			t.Errorf("profiles' writers %v %v, want none, %d", a, b, w.ID)
		}
		if _, err := gen.UpdateWriter(e.ctx, id, gen.UpdateWriterInput{ClearProfile: true}); err != nil {
			t.Fatal(err)
		}
		if a, b := writerOfProfile(t, e, p1.ID), writerOfProfile(t, e, p2.ID); a != nil || b != nil {
			t.Errorf("profiles' writers %v %v after clearProfile, want none", a, b)
		}
	})
}

// TestUpdateConflicts: an input with no single meaning is BAD_USER_INPUT,
// before any statement.
func TestUpdateConflicts(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w := &gen.Writer{Name: "w", Password: "x"}
		insert(t, e.ctx, e.db, w)
		c := &gen.Circle{Name: "c"}
		insert(t, e.ctx, e.db, c)
		e.reset()
		for _, x := range []struct {
			in   gen.UpdateWriterInput
			want string
		}{
			{gen.UpdateWriterInput{Bio: ptr("b"), ClearBio: true}, "veloxgql: UpdateWriterInput: bio and clearBio together"},
			{gen.UpdateWriterInput{MentorID: ptr(writerID(w.ID)), ClearMentor: true}, "veloxgql: UpdateWriterInput: mentorID and clearMentor together"},
			{gen.UpdateWriterInput{ProfileID: ptr(profileID(1)), ClearProfile: true}, "veloxgql: UpdateWriterInput: profileID and clearProfile together"},
			{gen.UpdateWriterInput{AddCircleIDs: []string{circleID(c.ID)}, RemoveCircleIDs: []string{circleID(c.ID)}}, "veloxgql: UpdateWriterInput: an ID is in both addCircleIDs and removeCircleIDs"},
		} {
			_, err := gen.UpdateWriter(e.ctx, writerID(w.ID), x.in)
			if code(err) != veloxgql.CodeBadUserInput || err.Error() != x.want {
				t.Errorf("%v: %v (code %q), want %q", x.in, err, code(err), x.want)
			}
		}
		if s := e.stmts(); len(s) != 0 {
			t.Errorf("conflicts sent %v", s)
		}
	})
}

// TestUpdatePolicy: the Write policy scopes the UPDATE: a row it leaves out
// is NOT_FOUND.
func TestUpdatePolicy(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		locked, open := &gen.Article{Title: "locked"}, &gen.Article{Title: "open"}
		insert(t, e.ctx, e.db, locked)
		insert(t, e.ctx, e.db, open)
		_, err := gen.UpdateArticle(e.ctx, articleID(locked.ID), gen.UpdateArticleInput{Title: ptr("free")})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: updateArticle: no Article has this ID" {
			t.Errorf("the locked article: %v (code %q), want NOT_FOUND", err, code(err))
		}
		if n := count(t, e.ctx, e.db.Query[gen.Article]().Where(gen.Articles.Title.EQ("locked"))); n != 1 {
			t.Errorf("%d locked articles, want 1", n)
		}
		got, err := gen.UpdateArticle(e.ctx, articleID(open.ID), gen.UpdateArticleInput{Title: ptr("renamed")})
		if err != nil || got.Title != "renamed" || got.ID != open.ID {
			t.Errorf("UpdateArticle = %v, %v: want it renamed", got, err)
		}
	})
}

// TestUpdateWriteScope: an update that sets no field checks its row under
// the Write policy, as the UPDATE of a field would: a writer the caller may
// read but not change is NOT_FOUND, as a missing one is, and no edge of it
// changes.
func TestUpdateWriteScope(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		frozen := &gen.Writer{Name: "frozen", Password: "x"}
		insert(t, e.ctx, e.db, frozen)
		p := &gen.Profile{Handle: "p", WriterID: ptr(frozen.ID)}
		insert(t, e.ctx, e.db, p)
		a, c := &gen.Article{Title: "a"}, &gen.Circle{Name: "c"}
		insert(t, e.ctx, e.db, a)
		insert(t, e.ctx, e.db, c)
		const want = "veloxgql: updateWriter: no Writer has this ID"
		for _, in := range []gen.UpdateWriterInput{
			{},
			{AddCircleIDs: []string{circleID(c.ID)}},
			{AddArticleIDs: []string{articleID(a.ID)}},
			{ClearProfile: true},
			{Name: ptr("thawed")},
		} {
			_, err := gen.UpdateWriter(e.ctx, writerID(frozen.ID), in)
			if code(err) != veloxgql.CodeNotFound || err.Error() != want {
				t.Errorf("%v: %v (code %q), want %q", in, err, code(err), want)
			}
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]()); n != 0 {
			t.Errorf("%d join rows, want 0", n)
		}
		if w := writerOfArticle(t, e, a.ID); w != nil {
			t.Errorf("the article's writer is %d, want none", *w)
		}
		if w := writerOfProfile(t, e, p.ID); !is(w, frozen.ID) {
			t.Errorf("the profile's writer is %v, want the frozen writer", w)
		}
		if st := writer(t, e, frozen.ID); st.Name != "frozen" {
			t.Errorf("the frozen writer is now %q", st.Name)
		}
	})
}

// TestUpdateReadBack: an update that moves its row out of the caller's Read
// view succeeds and returns the row as written: Circle's Read policy hides
// the circle named "hidden".
func TestUpdateReadBack(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		c := &gen.Circle{Name: "c"}
		insert(t, e.ctx, e.db, c)
		got, err := gen.UpdateCircle(e.ctx, circleID(c.ID), gen.UpdateCircleInput{Name: ptr("hidden")})
		if err != nil || got == nil || got.ID != c.ID || got.Name != "hidden" {
			t.Fatalf("UpdateCircle = %v, %v: want the circle, named hidden", got, err)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Circle]()); n != 0 {
			t.Errorf("%d circles the caller may read, want 0", n)
		}
		// Hidden now, it is NOT_FOUND to the next update.
		_, err = gen.UpdateCircle(e.ctx, circleID(c.ID), gen.UpdateCircleInput{Name: ptr("back")})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: updateCircle: no Circle has this ID" {
			t.Errorf("updating the hidden circle: %v (code %q), want NOT_FOUND", err, code(err))
		}
	})
}

// TestUpdateEmpty: an input that sets nothing sends no write: one COUNT
// checks the row, and one SELECT reads it.
func TestUpdateEmpty(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w := &gen.Writer{Name: "w", Password: "x", Bio: ptr("b")}
		insert(t, e.ctx, e.db, w)
		e.reset()
		got, err := gen.UpdateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{})
		if err != nil || got.ID != w.ID || got.Name != "w" || got.Bio == nil || *got.Bio != "b" {
			t.Errorf("UpdateWriter = %v, %v: want w unchanged", got, err)
		}
		s := e.stmts()
		if len(s) != 2 || !strings.HasPrefix(s[0], "SELECT COUNT") || !strings.HasPrefix(s[1], "SELECT ") || strings.HasPrefix(s[1], "SELECT COUNT") {
			t.Errorf("sent %q, want a SELECT COUNT and a SELECT", s)
		}
		_, err = gen.UpdateWriter(e.ctx, writerID(999), gen.UpdateWriterInput{})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: updateWriter: no Writer has this ID" {
			t.Errorf("an unknown writer: %v (code %q), want NOT_FOUND", err, code(err))
		}
	})
}

func TestUpdateErrors(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w := &gen.Writer{Name: "w", Password: "x"}
		insert(t, e.ctx, e.db, w)
		_, err := gen.UpdateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{Name: ptr("")})
		var ve *velox.ValidationError
		if code(err) != veloxgql.CodeBadUserInput || err == nil || !strings.HasPrefix(err.Error(), "veloxgql: UpdateWriterInput.name: invalid value: ") || !errors.As(err, &ve) {
			t.Errorf("an empty name: %v (code %q)", err, code(err))
		}
		e.reset()
		_, err = gen.UpdateWriter(e.ctx, circleID(w.ID), gen.UpdateWriterInput{})
		if code(err) != veloxgql.CodeBadUserInput || !errors.Is(err, veloxgql.ErrWrongIDType) || !strings.Contains(err.Error(), "updateWriter.id") {
			t.Errorf("a circle's ID for a writer: %v (code %q)", err, code(err))
		}
		_, err = gen.UpdateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{RemoveCircleIDs: []string{"not an ID"}})
		if code(err) != veloxgql.CodeBadUserInput || !errors.Is(err, veloxgql.ErrBadID) || !strings.Contains(err.Error(), "UpdateWriterInput.removeCircleIDs[0]") {
			t.Errorf("a malformed ID: %v (code %q)", err, code(err))
		}
		if s := e.stmts(); len(s) != 0 {
			t.Errorf("input errors sent %v", s)
		}
	})
}
`

// TestMutationsRun generates e2eMutSchema's Go and runs e2eMutRun on it:
// createX and updateX with each kind of edge, the tenant and policy scope of
// the IDs they name, atomicity, and their errors.
func TestMutationsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{"schema/schema.go": e2eMutSchema, "gen/article_policy.go": e2eMutPolicy, "gen/circle_policy.go": e2eMutCirclePolicy, "gen/writer_policy.go": e2eMutWriterPolicy, "mutation_test.go": e2eMutRun})
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
	for _, name := range []string{"TestCreate", "TestCreateM2MBothSides", "TestRepeatedIDs", "TestEdgeScope", "TestAtomic", "TestCreateErrors", "TestRepoint",
		"TestUpdate", "TestUpdateConflicts", "TestUpdatePolicy", "TestUpdateWriteScope", "TestUpdateReadBack", "TestUpdateEmpty", "TestUpdateErrors"} {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			t.Errorf("no PASS of %s in\n%s", name, out)
		}
	}
}
