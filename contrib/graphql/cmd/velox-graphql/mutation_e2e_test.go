package main

import (
	"os"
	"strings"
	"testing"
)

// e2eMutSchema is the schema of the mutation tests: every kind of edge a
// create input sets, and a field of each kind the input treats apart. Its
// tables share no name with internal/integration's.
//   - Writer is scoped by org_id (schema/writer_policy.go), with a validator (name), a Nillable field
//     (bio), an Optional one with a Default (nick), an Optional number
//     without one (score), a sensitive field (password) and an Optional JSON
//     field (meta); its edges are an O2M (articles), an M2M (circles), an
//     O2O whose key Profile holds (profile), a scoped O2M to itself
//     (mentees, whose inverse is mentor) and an M2M to itself (friends,
//     whose inverse is friended_by: a join row holds the writer in either
//     column) and two bidirectional self edges, which have no inverse: an
//     M2M (peers, on writer_peers) and an O2O (partner, on writer_partner);
//     its policy, in schema/writer_policy.go, lets no "frozen" writer change.
//   - Article's writer is an M2O whose nullable key is on its own row; its
//     policy, in schema/article_policy.go, refuses the title "forbidden" on
//     insert and lets no "locked" row change.
//   - Circle is the M2M's other side, with a unique name; its policy, in
//     schema/circle_policy.go, hides the circle named "hidden" and lets no
//     "frozen" one change.
//   - Profile holds the O2O's key; its policy, in schema/profile_policy.go,
//     hides the profile whose handle is "hidden" and lets no "frozen" one
//     change.
//   - Folder has two Required self edges, whose implicit keys are NULL-able
//     (ON DELETE SET NULL): parent, of the O2M children, on folder_children,
//     and prev, of the O2O next, on folder_next.
const e2eMutSchema = `package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Writer struct{ schema.Schema }

func (Writer) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Immutable().Annotations(graphql.Skip()),
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
		edge.To("friends", Writer.Type).StorageKey(edge.Columns("writer_id", "friend_id")).From("friended_by"),
		edge.To("peers", Writer.Type),
		edge.To("partner", Writer.Type).Unique(),
	}
}

func (Writer) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
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
	return []schema.Annotation{graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}

type Circle struct{ schema.Schema }

func (Circle) Fields() []schema.Field { return []schema.Field{field.String("name").Unique()} }

func (Circle) Edges() []schema.Edge {
	return []schema.Edge{edge.From("writers", Writer.Type).Ref("circles")}
}

func (Circle) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
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
	return []schema.Annotation{graphql.Mutations(graphql.MutationCreate)}
}

type Folder struct{ schema.Schema }

func (Folder) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (Folder) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("children", Folder.Type).From("parent").Unique().Required(),
		edge.To("next", Folder.Type).Unique().From("prev").Unique().Required(),
	}
}

func (Folder) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}
`

// e2eMutPolicy is Article's policy, declared in the schema
// package, where velox-gen finds and binds it.
const e2eMutPolicy = `package schema

import (
	"context"
	"errors"

	"github.com/syssam/velox"

	"example.com/app/gen/entity"
)

// Policy refuses to insert an article titled "forbidden", and lets no
// "locked" article change.
func (Article) Policy() velox.Policy[entity.Article] {
	return velox.Policy[entity.Article]{
		Insert: func(_ context.Context, x *entity.Article) error {
			if x.Title == "forbidden" {
				return errors.New("the title is forbidden")
			}
			return nil
		},
		Write: func(context.Context) (velox.Pred[entity.Article], error) { return entity.Articles.Title.NEQ("locked"), nil },
	}
}
`

// e2eMutWriterPolicy is Writer's policy: the org scope, and a Write that lets
// no "frozen" writer change, though every writer of the org may be read.
const e2eMutWriterPolicy = `package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"

	"example.com/app/gen/entity"
)

// Org is the organization a request acts for.
var Org = scope.NewKey[int64]("org")

var writerScope = scope.By(Org, entity.Writers.OrgID.EQ, func(x *entity.Writer) *int64 { return &x.OrgID })

// Policy confines the writers to the context's org, and lets no "frozen"
// writer change.
func (Writer) Policy() velox.Policy[entity.Writer] {
	return writerScope.Wrap(velox.Policy[entity.Writer]{
		Write: func(context.Context) (velox.Pred[entity.Writer], error) { return entity.Writers.Name.NEQ("frozen"), nil },
	})
}

// Hooks fills org_id from the context.
func (Writer) Hooks() []velox.Hook[entity.Writer] { return []velox.Hook[entity.Writer]{writerScope.Fill()} }
`

// e2eMutCirclePolicy is Circle's policy: its Read hides the circle named
// "hidden", and its Write lets no circle named "frozen" change, though it
// may be read.
const e2eMutCirclePolicy = `package schema

import (
	"context"

	"github.com/syssam/velox"

	"example.com/app/gen/entity"
)

// Policy hides the circle named "hidden", and lets no "frozen" circle change.
func (Circle) Policy() velox.Policy[entity.Circle] {
	return velox.Policy[entity.Circle]{
		Read:  func(context.Context) (velox.Pred[entity.Circle], error) { return entity.Circles.Name.NEQ("hidden"), nil },
		Write: func(context.Context) (velox.Pred[entity.Circle], error) { return entity.Circles.Name.NEQ("frozen"), nil },
	}
}
`

// e2eMutProfilePolicy is Profile's policy: its Read hides the profile whose
// handle is "hidden", and its Write lets no profile whose handle is "frozen"
// change, though every one may be read.
const e2eMutProfilePolicy = `package schema

import (
	"context"

	"github.com/syssam/velox"

	"example.com/app/gen/entity"
)

// Policy hides the profile whose handle is "hidden", and lets no "frozen"
// profile change.
func (Profile) Policy() velox.Policy[entity.Profile] {
	return velox.Policy[entity.Profile]{
		Read:  func(context.Context) (velox.Pred[entity.Profile], error) { return entity.Profiles.Handle.NEQ("hidden"), nil },
		Write: func(context.Context) (velox.Pred[entity.Profile], error) { return entity.Profiles.Handle.NEQ("frozen"), nil },
	}
}
`

// e2eMutCountless is the application's test of the generated mutations on a
// driver whose results cannot report the rows a statement changed, for which
// velox's count is -1: SQLite behind a wrapper whose RowsAffected fails.
const e2eMutCountless = `package app_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"

	"example.com/app/gen"
	"example.com/app/schema"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// countless is a driver whose results report no count.
type countless struct{ driver.Driver }

// countlessInner is what the wrapped SQLite connection implements, and
// countlessConn passes on.
type countlessInner interface {
	driver.Conn
	driver.ExecerContext
	driver.QueryerContext
	driver.ConnBeginTx
	driver.ConnPrepareContext
}

type countlessConn struct{ countlessInner }

type countlessResult struct{ driver.Result }

func (d countless) Open(name string) (driver.Conn, error) {
	c, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return countlessConn{c.(countlessInner)}, nil
}

func (c countlessConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	r, err := c.countlessInner.ExecContext(ctx, q, args)
	if err != nil {
		return nil, err
	}
	return countlessResult{r}, nil
}

func (countlessResult) RowsAffected() (int64, error) { return 0, errors.New("countless: no count") }

var registerCountless sync.Once

// TestUnknownRowCount: with the count unknown, an update checks the row
// as when it has nothing to set, and a create that re-points articles fails
// closed with an internal error, not NOT_FOUND, and rolls back.
func TestUnknownRowCount(t *testing.T) {
	registerCountless.Do(func() {
		probe, err := sql.Open("sqlite", "")
		if err != nil {
			t.Fatal(err)
		}
		sql.Register("countless-sqlite", countless{probe.Driver()})
		_ = probe.Close()
	})
	raw := sqlOpen(t, "countless-sqlite", "file::memory:?_pragma=foreign_keys(1)")
	raw.SetMaxOpenConns(1)
	for _, s := range sqliteDDL {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	db := velox.Open(velox.SQLite, raw)
	ctx := schema.Org.With(veloxgql.NewExtension(db).NewContext(context.Background()), int64(7))
	w := &gen.Writer{Name: "w", Password: "x"}
	insert(t, ctx, db, w)
	a := &gen.Article{Title: "a"}
	insert(t, ctx, db, a)

	if _, err := updateWriter(ctx, writerID(w.ID), gen.UpdateWriterInput{Name: ptr("w2")}); err != nil {
		t.Errorf("updateWriter of a writer: %v", err)
	}
	_, err := updateWriter(ctx, writerID(w.ID+100), gen.UpdateWriterInput{Name: ptr("w3")})
	if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: UpdateWriterInput: no Writer has this ID" {
		t.Errorf("updateWriter of no writer: %v (code %q), want NOT_FOUND", err, code(err))
	}

	_, err = createWriter(ctx, gen.CreateWriterInput{Name: "new", Password: "x", ArticleIDs: []string{articleID(a.ID)}}, nil)
	if code(err) != "" || err == nil || err.Error() != "veloxgql: CreateWriterInput.articleIDs: row count unknown" {
		t.Errorf("createWriter re-pointing an article: %v (code %q), want the internal error", err, code(err))
	}
	if n := count(t, ctx, db.Query[gen.Writer]().Where(gen.Writers.Name.EQ("new"))); n != 0 {
		t.Errorf("%d writers named new, want 0: the create rolls back", n)
	}
}
`

// e2eMutRun is the application's test of the generated create and update
// inputs' Create and Update, run on each of targets' databases through
// resolver-like functions, as an application's mutation resolvers call them.
const e2eMutRun = `package app_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"example.com/app/gen"
	"example.com/app/schema"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/veloxpgx"
)

// TestMain refuses to run, when VELOX_ITEST_REQUIRE=1, as CI and
// scripts/ci-local.sh set it, without both databases: a missing variable would
// leave out the PostgreSQL or the MySQL subtests without a failure.
func TestMain(m *testing.M) {
	if os.Getenv("VELOX_ITEST_REQUIRE") == "1" {
		var missing []string
		for _, v := range []string{"VELOX_TEST_POSTGRES", "VELOX_TEST_MYSQL"} {
			if os.Getenv(v) == "" {
				missing = append(missing, v)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "app: VELOX_ITEST_REQUIRE=1 but %s unset: the PostgreSQL and MySQL tests would be left out\n", strings.Join(missing, ", "))
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// A target is a database the tests run on: how to open it, and its DDL;
// fkDDL is the same tables with foreign keys the database enforces, for
// the tests of what they refuse. drop drops the tables, in an order their
// foreign keys allow, before either is run: the server databases outlive a
// test.
type target struct {
	name  string
	ddl   []string
	fkDDL []string
	drop  []string
	// open opens the database: raw, through database/sql, for the DDL and
	// what velox does not read, and db, through which the tests write.
	open func(t *testing.T, opts ...velox.Option) (raw *sql.DB, db *velox.DB)
}

// targets are the databases the tests run on: SQLite always; PostgreSQL,
// through native pgx and through database/sql, when VELOX_TEST_POSTGRES
// names it; and MySQL when VELOX_TEST_MYSQL does. Its DSN needs
// clientFoundRows=true, as velox's Open says.
func targets(t *testing.T) []target {
	t.Helper()
	ts := []target{{
		name: "sqlite", ddl: sqliteDDL, fkDDL: sqliteFKDDL,
		open: func(t *testing.T, opts ...velox.Option) (*sql.DB, *velox.DB) {
			// Foreign keys are on: only fkDDL declares any.
			db := sqlOpen(t, "sqlite", "file::memory:?_pragma=foreign_keys(1)")
			db.SetMaxOpenConns(1)
			return db, velox.Open(velox.SQLite, db, opts...)
		},
	}}
	if dsn := os.Getenv("VELOX_TEST_POSTGRES"); dsn != "" {
		ts = append(ts, target{
			name: "pgx", ddl: pgDDL, fkDDL: pgFKDDL, drop: dropDDL,
			open: func(t *testing.T, opts ...velox.Option) (*sql.DB, *velox.DB) {
				pool, err := pgxpool.New(context.Background(), dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(pool.Close)
				return sqlOpen(t, "pgx", dsn), veloxpgx.Open(pool, opts...)
			},
		}, target{
			name: "postgres", ddl: pgDDL, fkDDL: pgFKDDL, drop: dropDDL,
			open: func(t *testing.T, opts ...velox.Option) (*sql.DB, *velox.DB) {
				db := sqlOpen(t, "pgx", dsn)
				return db, velox.Open(velox.Postgres, db, opts...)
			},
		})
	}
	if dsn := os.Getenv("VELOX_TEST_MYSQL"); dsn != "" {
		ts = append(ts, target{
			name: "mysql", ddl: mysqlDDL, fkDDL: mysqlFKDDL, drop: dropDDL,
			open: func(t *testing.T, opts ...velox.Option) (*sql.DB, *velox.DB) {
				db := sqlOpen(t, "mysql", dsn)
				return db, velox.Open(velox.MySQL, db, opts...)
			},
		})
	}
	return ts
}

// sqlOpen opens a database/sql handle, closed when the test ends.
func sqlOpen(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// The tables of gen: writer_circles is Writer.circles' join table,
// writer_friends Writer.friends' and writer_peers Writer.peers', as
// gen/writer_circle_velox.go, gen/writer_friend_velox.go and
// gen/writer_peer_velox.go name them; writers' writer_partner is the key of
// Writer.partner.
var sqliteDDL = []string{
	"CREATE TABLE writers (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, name TEXT NOT NULL, bio TEXT, nick TEXT NOT NULL, score INTEGER NOT NULL, password TEXT NOT NULL, meta TEXT, mentor_id INTEGER, writer_partner INTEGER UNIQUE)",
	"CREATE TABLE articles (id INTEGER PRIMARY KEY, title TEXT NOT NULL, writer_id INTEGER)",
	"CREATE TABLE circles (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE)",
	"CREATE TABLE profiles (id INTEGER PRIMARY KEY, handle TEXT NOT NULL, writer_id INTEGER UNIQUE)",
	"CREATE TABLE writer_circles (writer_id INTEGER NOT NULL, circle_id INTEGER NOT NULL, PRIMARY KEY (writer_id, circle_id))",
	"CREATE TABLE writer_friends (writer_id INTEGER NOT NULL, friend_id INTEGER NOT NULL, PRIMARY KEY (writer_id, friend_id))",
	"CREATE TABLE writer_peers (writer_id INTEGER NOT NULL, peer_id INTEGER NOT NULL, PRIMARY KEY (writer_id, peer_id))",
	"CREATE TABLE folders (id INTEGER PRIMARY KEY, name TEXT NOT NULL, folder_children INTEGER, folder_next INTEGER UNIQUE)",
}

// sqliteFKDDL is sqliteDDL with foreign keys from articles and the join
// rows to writers and circles, which refuse a DELETE of a row they point at,
// and from folders to folders, which a DELETE sets NULL.
var sqliteFKDDL = []string{
	"CREATE TABLE writers (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, name TEXT NOT NULL, bio TEXT, nick TEXT NOT NULL, score INTEGER NOT NULL, password TEXT NOT NULL, meta TEXT, mentor_id INTEGER, writer_partner INTEGER UNIQUE REFERENCES writers (id) ON DELETE SET NULL)",
	"CREATE TABLE articles (id INTEGER PRIMARY KEY, title TEXT NOT NULL, writer_id INTEGER REFERENCES writers (id))",
	sqliteDDL[2],
	sqliteDDL[3],
	"CREATE TABLE writer_circles (writer_id INTEGER NOT NULL REFERENCES writers (id), circle_id INTEGER NOT NULL REFERENCES circles (id), PRIMARY KEY (writer_id, circle_id))",
	"CREATE TABLE writer_friends (writer_id INTEGER NOT NULL REFERENCES writers (id), friend_id INTEGER NOT NULL REFERENCES writers (id), PRIMARY KEY (writer_id, friend_id))",
	"CREATE TABLE writer_peers (writer_id INTEGER NOT NULL REFERENCES writers (id), peer_id INTEGER NOT NULL REFERENCES writers (id), PRIMARY KEY (writer_id, peer_id))",
	"CREATE TABLE folders (id INTEGER PRIMARY KEY, name TEXT NOT NULL, folder_children INTEGER REFERENCES folders (id) ON DELETE SET NULL, folder_next INTEGER UNIQUE REFERENCES folders (id) ON DELETE SET NULL)",
}

var pgDDL = []string{
	"CREATE TABLE writers (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, org_id bigint NOT NULL, name text NOT NULL, bio text, nick text NOT NULL, score bigint NOT NULL, password text NOT NULL, meta jsonb, mentor_id bigint, writer_partner bigint UNIQUE)",
	"CREATE TABLE articles (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, title text NOT NULL, writer_id bigint)",
	"CREATE TABLE circles (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, name text NOT NULL UNIQUE)",
	"CREATE TABLE profiles (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, handle text NOT NULL, writer_id bigint UNIQUE)",
	"CREATE TABLE writer_circles (writer_id bigint NOT NULL, circle_id bigint NOT NULL, PRIMARY KEY (writer_id, circle_id))",
	"CREATE TABLE writer_friends (writer_id bigint NOT NULL, friend_id bigint NOT NULL, PRIMARY KEY (writer_id, friend_id))",
	"CREATE TABLE writer_peers (writer_id bigint NOT NULL, peer_id bigint NOT NULL, PRIMARY KEY (writer_id, peer_id))",
	"CREATE TABLE folders (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, name text NOT NULL, folder_children bigint, folder_next bigint UNIQUE)",
}

// pgFKDDL is pgDDL with sqliteFKDDL's foreign keys.
var pgFKDDL = []string{
	"CREATE TABLE writers (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, org_id bigint NOT NULL, name text NOT NULL, bio text, nick text NOT NULL, score bigint NOT NULL, password text NOT NULL, meta jsonb, mentor_id bigint, writer_partner bigint UNIQUE REFERENCES writers (id) ON DELETE SET NULL)",
	"CREATE TABLE articles (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, title text NOT NULL, writer_id bigint REFERENCES writers (id))",
	pgDDL[2],
	pgDDL[3],
	"CREATE TABLE writer_circles (writer_id bigint NOT NULL REFERENCES writers (id), circle_id bigint NOT NULL REFERENCES circles (id), PRIMARY KEY (writer_id, circle_id))",
	"CREATE TABLE writer_friends (writer_id bigint NOT NULL REFERENCES writers (id), friend_id bigint NOT NULL REFERENCES writers (id), PRIMARY KEY (writer_id, friend_id))",
	"CREATE TABLE writer_peers (writer_id bigint NOT NULL REFERENCES writers (id), peer_id bigint NOT NULL REFERENCES writers (id), PRIMARY KEY (writer_id, peer_id))",
	"CREATE TABLE folders (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, name text NOT NULL, folder_children bigint REFERENCES folders (id) ON DELETE SET NULL, folder_next bigint UNIQUE REFERENCES folders (id) ON DELETE SET NULL)",
}

var mysqlDDL = []string{
	"CREATE TABLE writers (id BIGINT AUTO_INCREMENT PRIMARY KEY, org_id BIGINT NOT NULL, name VARCHAR(255) NOT NULL, bio TEXT NULL, nick VARCHAR(255) NOT NULL, score BIGINT NOT NULL, password VARCHAR(255) NOT NULL, meta JSON NULL, mentor_id BIGINT NULL, writer_partner BIGINT NULL UNIQUE)",
	"CREATE TABLE articles (id BIGINT AUTO_INCREMENT PRIMARY KEY, title VARCHAR(255) NOT NULL, writer_id BIGINT NULL)",
	"CREATE TABLE circles (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255) NOT NULL UNIQUE)",
	"CREATE TABLE profiles (id BIGINT AUTO_INCREMENT PRIMARY KEY, handle VARCHAR(255) NOT NULL, writer_id BIGINT NULL UNIQUE)",
	"CREATE TABLE writer_circles (writer_id BIGINT NOT NULL, circle_id BIGINT NOT NULL, PRIMARY KEY (writer_id, circle_id))",
	"CREATE TABLE writer_friends (writer_id BIGINT NOT NULL, friend_id BIGINT NOT NULL, PRIMARY KEY (writer_id, friend_id))",
	"CREATE TABLE writer_peers (writer_id BIGINT NOT NULL, peer_id BIGINT NOT NULL, PRIMARY KEY (writer_id, peer_id))",
	"CREATE TABLE folders (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255) NOT NULL, folder_children BIGINT NULL, folder_next BIGINT NULL UNIQUE)",
}

// mysqlFKDDL is mysqlDDL with sqliteFKDDL's foreign keys.
var mysqlFKDDL = []string{
	"CREATE TABLE writers (id BIGINT AUTO_INCREMENT PRIMARY KEY, org_id BIGINT NOT NULL, name VARCHAR(255) NOT NULL, bio TEXT NULL, nick VARCHAR(255) NOT NULL, score BIGINT NOT NULL, password VARCHAR(255) NOT NULL, meta JSON NULL, mentor_id BIGINT NULL, writer_partner BIGINT NULL UNIQUE, FOREIGN KEY (writer_partner) REFERENCES writers (id) ON DELETE SET NULL)",
	"CREATE TABLE articles (id BIGINT AUTO_INCREMENT PRIMARY KEY, title VARCHAR(255) NOT NULL, writer_id BIGINT NULL, FOREIGN KEY (writer_id) REFERENCES writers (id))",
	mysqlDDL[2],
	mysqlDDL[3],
	"CREATE TABLE writer_circles (writer_id BIGINT NOT NULL, circle_id BIGINT NOT NULL, PRIMARY KEY (writer_id, circle_id), FOREIGN KEY (writer_id) REFERENCES writers (id), FOREIGN KEY (circle_id) REFERENCES circles (id))",
	"CREATE TABLE writer_friends (writer_id BIGINT NOT NULL, friend_id BIGINT NOT NULL, PRIMARY KEY (writer_id, friend_id), FOREIGN KEY (writer_id) REFERENCES writers (id), FOREIGN KEY (friend_id) REFERENCES writers (id))",
	"CREATE TABLE writer_peers (writer_id BIGINT NOT NULL, peer_id BIGINT NOT NULL, PRIMARY KEY (writer_id, peer_id), FOREIGN KEY (writer_id) REFERENCES writers (id), FOREIGN KEY (peer_id) REFERENCES writers (id))",
	"CREATE TABLE folders (id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255) NOT NULL, folder_children BIGINT NULL, folder_next BIGINT NULL UNIQUE, FOREIGN KEY (folder_children) REFERENCES folders (id) ON DELETE SET NULL, FOREIGN KEY (folder_next) REFERENCES folders (id) ON DELETE SET NULL)",
}

// dropDDL drops the tables of a server database, those whose foreign keys
// point at another first.
var dropDDL = []string{
	"DROP TABLE IF EXISTS folders",
	"DROP TABLE IF EXISTS writer_peers",
	"DROP TABLE IF EXISTS writer_friends",
	"DROP TABLE IF EXISTS writer_circles",
	"DROP TABLE IF EXISTS articles",
	"DROP TABLE IF EXISTS profiles",
	"DROP TABLE IF EXISTS circles",
	"DROP TABLE IF EXISTS writers",
}

// env is a test's database: ctx is under org 7, with the DB put there
// by the extension's Context, as for a job outside a server; stmts are the
// statements sent since the last reset.
type env struct {
	target string // the target's name
	ctx    context.Context
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
func each(t *testing.T, fn func(t *testing.T, e *env)) { run(t, false, fn) }

// eachFK runs fn on each target, with a fresh database of its fkDDL.
func eachFK(t *testing.T, fn func(t *testing.T, e *env)) { run(t, true, fn) }

func run(t *testing.T, fk bool, fn func(t *testing.T, e *env)) {
	for _, tg := range targets(t) {
		t.Run(tg.name, func(t *testing.T) {
			ddl := tg.ddl
			if fk {
				ddl = tg.fkDDL
			}
			e := &env{target: tg.name}
			raw, db := tg.open(t, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
				e.mu.Lock()
				e.sql = append(e.sql, st.SQL)
				e.mu.Unlock()
				return next(ctx)
			}))
			for _, s := range append(slices.Clone(tg.drop), ddl...) {
				if _, err := raw.Exec(s); err != nil {
					t.Fatalf("%s: %v", s, err)
				}
			}
			e.raw, e.db = raw, db
			e.ctx = schema.Org.With(veloxgql.NewExtension(e.db).NewContext(context.Background()), int64(7))
			fn(t, e)
		})
	}
}

func ptr[T any](v T) *T { return &v }

// The resolvers of the mutations an application declares, as one writes
// them: each calls its input's Create or Update. A writer's org_id, which
// CreateWriterInput leaves out, its scope's hook fills.
func createWriter(ctx context.Context, in gen.CreateWriterInput, x *gen.Writer) (*gen.Writer, error) {
	return in.Create(ctx, x)
}
func createArticle(ctx context.Context, in gen.CreateArticleInput) (*gen.Article, error) {
	return in.Create(ctx, nil)
}
func createCircle(ctx context.Context, in gen.CreateCircleInput) (*gen.Circle, error) {
	return in.Create(ctx, nil)
}
func createFolder(ctx context.Context, in gen.CreateFolderInput) (*gen.Folder, error) {
	return in.Create(ctx, nil)
}
func updateWriter(ctx context.Context, id string, in gen.UpdateWriterInput) (*gen.Writer, error) {
	return in.Update(ctx, id)
}
func updateArticle(ctx context.Context, id string, in gen.UpdateArticleInput) (*gen.Article, error) {
	return in.Update(ctx, id)
}
func updateCircle(ctx context.Context, id string, in gen.UpdateCircleInput) (*gen.Circle, error) {
	return in.Update(ctx, id)
}
func updateFolder(ctx context.Context, id string, in gen.UpdateFolderInput) (*gen.Folder, error) {
	return in.Update(ctx, id)
}

func writerID(id int64) string  { return veloxgql.EncodeID("Writer", id) }
func articleID(id int64) string { return veloxgql.EncodeID("Article", id) }
func circleID(id int64) string  { return veloxgql.EncodeID("Circle", id) }
func profileID(id int64) string { return veloxgql.EncodeID("Profile", id) }
func folderID(id int64) string  { return veloxgql.EncodeID("Folder", id) }

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
		w, err := createWriter(e.ctx, input, nil)
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
		c, err := createCircle(e.ctx, gen.CreateCircleInput{Name: "from-circle", WriterIDs: []string{writerID(w1.ID)}})
		if err != nil {
			t.Fatal(err)
		}
		if got := circlesOf(t, e, w1.ID); len(got) != 1 || got[0] != c.Name {
			t.Errorf("w1's circles: %v, want [from-circle]", got)
		}
		c2 := &gen.Circle{Name: "c2"}
		insert(t, e.ctx, e.db, c2)
		w, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "w2", Password: "x", CircleIDs: []string{circleID(c.ID), circleID(c2.ID)}}, nil)
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
		w, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x",
			CircleIDs: []string{circleID(c.ID), circleID(c.ID)}, ArticleIDs: []string{articleID(a.ID), articleID(a.ID)}}, nil)
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
			if _, err := updateWriter(e.ctx, writerID(w.ID), in); err != nil {
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
		insert(t, schema.Org.With(e.ctx, int64(8)), e.db, other)
		locked := &gen.Article{Title: "locked"}
		insert(t, e.ctx, e.db, locked)

		e.reset()
		_, err := createArticle(e.ctx, gen.CreateArticleInput{Title: "t", WriterID: ptr(writerID(other.ID))})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateArticleInput.writerID: no Writer has this ID" {
			t.Errorf("org 8's writer: %v (code %q), want NOT_FOUND", err, code(err))
		}
		for _, s := range e.stmts() {
			if strings.HasPrefix(s, "INSERT") {
				t.Errorf("sent %s", s)
			}
		}
		if n := count(t, e.ctx, e.db.Query[gen.Article]()); n != 1 {
			t.Errorf("%d articles, want 1: the locked one", n)
		}

		_, err = createCircle(e.ctx, gen.CreateCircleInput{Name: "c", WriterIDs: []string{writerID(other.ID)}})
		if code(err) != veloxgql.CodeNotFound {
			t.Errorf("a circle of org 8's writer: %v (code %q), want NOT_FOUND", err, code(err))
		}
		if n := count(t, e.ctx, e.db.Query[gen.Circle]()); n != 0 {
			t.Errorf("%d circles, want 0", n)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]()); n != 0 {
			t.Errorf("%d join rows, want 0", n)
		}

		// The Write policy hides the locked article from the UPDATE that
		// re-points it.
		_, err = createWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", ArticleIDs: []string{articleID(locked.ID)}}, nil)
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateWriterInput.articleIDs: no Article has this ID" {
			t.Errorf("the locked article: %v (code %q), want NOT_FOUND", err, code(err))
		}
		if n := count(t, e.ctx, e.db.Query[gen.Writer]()); n != 0 {
			t.Errorf("%d writers of org 7, want 0", n)
		}

		// The Read policy hides a circle from the COUNT that checks the
		// M2M's IDs.
		hidden := &gen.Circle{Name: "hidden"}
		insert(t, e.ctx, e.db, hidden)
		_, err = createWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", CircleIDs: []string{circleID(hidden.ID)}}, nil)
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateWriterInput.circleIDs: no Circle has this ID" {
			t.Errorf("the hidden circle: %v (code %q), want NOT_FOUND", err, code(err))
		}
		if n := count(t, e.ctx, e.db.Query[gen.Writer]()); n != 0 {
			t.Errorf("%d writers of org 7, want 0", n)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]()); n != 0 {
			t.Errorf("%d join rows, want 0", n)
		}

		// The org scope hides org 8's writer from the UPDATE that
		// re-points a mentee, and from the count of linked mentees: it has
		// a mentor, and still reads as missing.
		ctx8 := schema.Org.With(e.ctx, int64(8))
		mentor8 := &gen.Writer{Name: "mentor8", Password: "x"}
		insert(t, ctx8, e.db, mentor8)
		mentee8 := &gen.Writer{Name: "mentee8", Password: "x", MentorID: ptr(mentor8.ID)}
		insert(t, ctx8, e.db, mentee8)
		for _, id := range []int64{other.ID, mentee8.ID} {
			_, err = createWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", MenteeIDs: []string{writerID(id)}}, nil)
			if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateWriterInput.menteeIDs: no Writer has this ID" {
				t.Errorf("org 8's writer %d as a mentee: %v (code %q), want NOT_FOUND", id, err, code(err))
			}
		}
		if n := count(t, e.ctx, e.db.Query[gen.Writer]()); n != 0 {
			t.Errorf("%d writers of org 7, want 0", n)
		}
		got, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(mentee8.ID)).One(ctx8)
		if err != nil || got.MentorID == nil || *got.MentorID != mentor8.ID {
			t.Errorf("org 8's mentee: %v, %v: want its mentor kept", got, err)
		}
		if n := count(t, ctx8, e.db.Query[gen.Writer]().Where(gen.Writers.MentorID.IsNull())); n != 2 {
			t.Errorf("%d writers of org 8 without a mentor, want 2", n)
		}

		// On update: org 8's writer is NOT_FOUND, whether the input sets
		// a field or none, and no join row of it is written; and so is a
		// mentee of org 8, or the locked article, which the Write policy
		// hides from the UPDATE that adds it. Nothing moves.
		visible := &gen.Circle{Name: "visible"}
		insert(t, e.ctx, e.db, visible)
		for _, in := range []gen.UpdateWriterInput{{Name: ptr("x")}, {}, {AddCircleIDs: []string{circleID(visible.ID)}}} {
			_, err = updateWriter(e.ctx, writerID(other.ID), in)
			if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: UpdateWriterInput: no Writer has this ID" {
				t.Errorf("updating org 8's writer with %v: %v (code %q), want NOT_FOUND", in, err, code(err))
			}
		}
		if got, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(other.ID)).One(ctx8); err != nil || got.Name != "other" {
			t.Errorf("org 8's writer: %v, %v: want it unchanged", got, err)
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
			_, err = updateWriter(e.ctx, writerID(w7.ID), c.in)
			if code(err) != veloxgql.CodeNotFound || err.Error() != c.want {
				t.Errorf("%v: %v (code %q), want %q", c.in, err, code(err), c.want)
			}
		}
		if got, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(mentee8.ID)).One(ctx8); err != nil || got.MentorID == nil || *got.MentorID != mentor8.ID {
			t.Errorf("org 8's mentee: %v, %v: want its mentor kept", got, err)
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

// TestM2MLinkNeedsReadOnly: a many-to-many link writes a join row, not the
// target, so a circle the caller may read but not change can be linked, by
// create and by update. The circle is left as it was, and updating it is
// NOT_FOUND, which shows its Write policy is live.
func TestM2MLinkNeedsReadOnly(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		frozen := &gen.Circle{Name: "frozen"}
		insert(t, e.ctx, e.db, frozen)

		w, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", CircleIDs: []string{circleID(frozen.ID)}}, nil)
		if err != nil {
			t.Fatalf("CreateWriter with the frozen circle: %v", err)
		}
		if got := circlesOf(t, e, w.ID); !reflect.DeepEqual(got, []string{"frozen"}) {
			t.Errorf("the created writer's circles: %v, want [frozen]", got)
		}

		w2 := &gen.Writer{Name: "w2", Password: "x"}
		insert(t, e.ctx, e.db, w2)
		if _, err := updateWriter(e.ctx, writerID(w2.ID), gen.UpdateWriterInput{AddCircleIDs: []string{circleID(frozen.ID)}}); err != nil {
			t.Fatalf("UpdateWriter adding the frozen circle: %v", err)
		}
		if got := circlesOf(t, e, w2.ID); !reflect.DeepEqual(got, []string{"frozen"}) {
			t.Errorf("the updated writer's circles: %v, want [frozen]", got)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]()); n != 2 {
			t.Errorf("%d join rows, want 2", n)
		}

		c, err := e.db.Query[gen.Circle]().Where(gen.Circles.ID.EQ(frozen.ID)).One(e.ctx)
		if err != nil || c.Name != "frozen" {
			t.Errorf("the frozen circle: %+v, %v", c, err)
		}
		_, err = updateCircle(e.ctx, circleID(frozen.ID), gen.UpdateCircleInput{Name: ptr("thawed")})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: UpdateCircleInput: no Circle has this ID" {
			t.Errorf("updating the frozen circle: %v (code %q), want NOT_FOUND", err, code(err))
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
			_, err := createWriter(e.ctx, c.in, nil)
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

		w, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", ArticleIDs: []string{articleID(free.ID)}, ProfileID: ptr(profileID(freeP.ID))}, nil)
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
			_, err := updateWriter(e.ctx, writerID(w.ID), c.in)
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
		if _, err := updateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{ProfileID: ptr(profileID(freeP.ID)), AddArticleIDs: []string{articleID(free.ID)}}); err != nil {
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

// TestRepointHiddenChild: re-pointing a writer's profile, when the profile it
// holds now is hidden by the Read policy, so that the scoped unlink leaves it
// linked, is BAD_USER_INPUT "already linked", not the unique key's error, and
// keeps nothing, not even the writer's new name, written before the check;
// the hidden profile's own ID is NOT_FOUND, as an ID that names no profile is,
// so the error does not confirm a guess of it. Once the caller may see that
// profile, the same update moves the link.
func TestRepointHiddenChild(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w := &gen.Writer{Name: "w", Password: "x"}
		insert(t, e.ctx, e.db, w)
		p1, p2 := &gen.Profile{Handle: "hidden", WriterID: ptr(w.ID)}, &gen.Profile{Handle: "free"}
		insert(t, e.ctx, e.db, p1)
		insert(t, e.ctx, e.db, p2)
		all := velox.WithoutPolicy(e.ctx, "test: every profile")
		writerOf := func(id int64) *int64 {
			t.Helper()
			p, err := e.db.Query[gen.Profile]().Where(gen.Profiles.ID.EQ(id)).One(all)
			if err != nil {
				t.Fatal(err)
			}
			return p.WriterID
		}
		in := gen.UpdateWriterInput{Name: ptr("renamed"), ProfileID: ptr(profileID(p2.ID))}

		_, err := updateWriter(e.ctx, writerID(w.ID), in)
		const want = "veloxgql: UpdateWriterInput.profileID: the Writer is already linked to a Profile"
		if code(err) != veloxgql.CodeBadUserInput || err.Error() != want {
			t.Errorf("over a hidden profile: %v (code %q), want %q", err, code(err), want)
		}
		if got, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(w.ID)).One(e.ctx); err != nil || got.Name != "w" {
			t.Errorf("w after the refusal: %v, %v: want the name w kept", got, err)
		}
		// The hidden profile's own ID, and one that names no profile: the
		// same error.
		_, missing := updateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{ProfileID: ptr(profileID(999))})
		const notFound = "veloxgql: UpdateWriterInput.profileID: no Profile has this ID"
		if code(missing) != veloxgql.CodeNotFound || missing.Error() != notFound {
			t.Errorf("a missing profile: %v (code %q), want %q", missing, code(missing), notFound)
		}
		_, own := updateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{ProfileID: ptr(profileID(p1.ID))})
		if code(own) != code(missing) || fmt.Sprint(own) != fmt.Sprint(missing) {
			t.Errorf("the hidden profile's own ID: %v (code %q), want %v (code %q)", own, code(own), missing, code(missing))
		}
		if got := writerOf(p1.ID); got == nil || *got != w.ID {
			t.Errorf("the hidden profile's writer is %v, want %d", got, w.ID)
		}
		if got := writerOf(p2.ID); got != nil {
			t.Errorf("the free profile's writer is %d, want none", *got)
		}

		if _, err := e.raw.Exec(fmt.Sprintf("UPDATE profiles SET handle = 'seen' WHERE id = %d", p1.ID)); err != nil {
			t.Fatal(err)
		}
		if _, err := updateWriter(e.ctx, writerID(w.ID), in); err != nil {
			t.Fatalf("over a profile the caller sees: %v", err)
		}
		if got := writerOf(p2.ID); got == nil || *got != w.ID {
			t.Errorf("the free profile's writer is %v, want %d", got, w.ID)
		}
		if got := writerOf(p1.ID); got != nil {
			t.Errorf("the seen profile's writer is %d, want none", *got)
		}
	})
}

// TestWriteHiddenProfile: a profile the caller may read but not change, the
// "frozen" one of Profile's Write policy, cannot be linked or taken from its
// writer, and the errors do not tell it apart from a profile that is not there
// or hidden:
//   - createWriter and updateWriter linking it are NOT_FOUND, as for an ID that
//     names no profile, and a failed create leaves no writer;
//   - re-pointing a writer whose current profile it is to a free one is
//     BAD_USER_INPUT "already linked", the unlink matching no row; the
//     transaction rolls back, so the link stays, the free profile stays
//     unlinked and the writer keeps its name;
//   - clearProfile over it is a silent no-op, not an error: an error would
//     tell the caller that a profile it may not change is linked there.
func TestWriteHiddenProfile(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w, other := &gen.Writer{Name: "w", Password: "x"}, &gen.Writer{Name: "other", Password: "x"}
		insert(t, e.ctx, e.db, w)
		insert(t, e.ctx, e.db, other)
		frozen, free := &gen.Profile{Handle: "frozen"}, &gen.Profile{Handle: "free"}
		insert(t, e.ctx, e.db, frozen)
		insert(t, e.ctx, e.db, free)

		// Create and update, linking the frozen profile: NOT_FOUND.
		const notFound = "veloxgql: %s.profileID: no Profile has this ID"
		_, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "new", Password: "x", ProfileID: ptr(profileID(frozen.ID))}, nil)
		if want := fmt.Sprintf(notFound, "CreateWriterInput"); code(err) != veloxgql.CodeNotFound || err == nil || err.Error() != want {
			t.Errorf("createWriter linking the frozen profile: %v (code %q), want %q", err, code(err), want)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Writer]().Where(gen.Writers.Name.EQ("new"))); n != 0 {
			t.Errorf("%d writers new after the refused create, want 0", n)
		}
		_, err = updateWriter(e.ctx, writerID(other.ID), gen.UpdateWriterInput{ProfileID: ptr(profileID(frozen.ID))})
		if want := fmt.Sprintf(notFound, "UpdateWriterInput"); code(err) != veloxgql.CodeNotFound || err == nil || err.Error() != want {
			t.Errorf("updateWriter linking the frozen profile: %v (code %q), want %q", err, code(err), want)
		}
		if p := writerOfProfile(t, e, frozen.ID); p != nil {
			t.Errorf("the frozen profile's writer is %d, want none", *p)
		}

		// w holds the frozen profile (the link made as the database's owner
		// would, outside the policy).
		if _, err := e.raw.Exec(fmt.Sprintf("UPDATE profiles SET writer_id = %d WHERE id = %d", w.ID, frozen.ID)); err != nil {
			t.Fatal(err)
		}

		// Re-pointing: the unlink of the frozen profile matches no row.
		_, err = updateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{Name: ptr("renamed"), ProfileID: ptr(profileID(free.ID))})
		const linked = "veloxgql: UpdateWriterInput.profileID: the Writer is already linked to a Profile"
		if code(err) != veloxgql.CodeBadUserInput || err == nil || err.Error() != linked {
			t.Errorf("re-pointing over the frozen profile: %v (code %q), want %q", err, code(err), linked)
		}
		check := func(when string) {
			t.Helper()
			if p := writerOfProfile(t, e, frozen.ID); !is(p, w.ID) {
				t.Errorf("%s: the frozen profile's writer is %v, want %d", when, p, w.ID)
			}
			if p := writerOfProfile(t, e, free.ID); p != nil {
				t.Errorf("%s: the free profile's writer is %d, want none", when, *p)
			}
			if got := writer(t, e, w.ID); got.Name != "w" {
				t.Errorf("%s: w is named %q, want w", when, got.Name)
			}
		}
		check("after the refused re-point")

		// clearProfile: no error, and the link stays.
		if _, err := updateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{ClearProfile: true}); err != nil {
			t.Errorf("clearProfile over the frozen profile: %v, want none", err)
		}
		check("after clearProfile")
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
		_, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", CircleIDs: []string{circleID(c.ID), circleID(999)}}, nil)
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: CreateWriterInput.circleIDs: 1 of the 2 IDs name no Circle" {
			t.Errorf("a missing circle: %v (code %q), want NOT_FOUND", err, code(err))
		}
		// The second article is missing: the writer is inserted, and a
		// re-pointed, before the UPDATE's count fails the create.
		_, err = createWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", ArticleIDs: []string{articleID(a.ID), articleID(999)}}, nil)
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

		_, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "", Password: "x"}, nil)
		var ve *velox.ValidationError
		if code(err) != veloxgql.CodeBadUserInput || err == nil || !strings.HasPrefix(err.Error(), "veloxgql: CreateWriterInput.name: invalid value: ") || !errors.As(err, &ve) {
			t.Errorf("an empty name: %v (code %q)", err, code(err))
		}

		_, err = createArticle(e.ctx, gen.CreateArticleInput{Title: "forbidden"})
		if !errors.Is(err, velox.ErrDenied) {
			t.Errorf("a forbidden title: %v, want velox.ErrDenied", err)
		}

		_, err = createCircle(e.ctx, gen.CreateCircleInput{Name: "taken"})
		var ce *velox.ConstraintError
		if !errors.As(err, &ce) {
			t.Errorf("a taken name: %v, want a *velox.ConstraintError", err)
		}

		// Input errors fail before any statement.
		e.reset()
		for _, bad := range []veloxgql.JSON{veloxgql.JSON("[1, 2]"), veloxgql.JSON("{")} {
			_, err = createWriter(e.ctx, gen.CreateWriterInput{Name: "m", Password: "x", Meta: &bad}, nil)
			if code(err) != veloxgql.CodeBadUserInput || !strings.Contains(err.Error(), "CreateWriterInput.meta") {
				t.Errorf("meta %s: %v (code %q)", bad, err, code(err))
			}
		}
		_, err = createWriter(e.ctx, gen.CreateWriterInput{Name: "m", Password: "x", CircleIDs: []string{writerID(w.ID)}}, nil)
		if code(err) != veloxgql.CodeBadUserInput || !errors.Is(err, veloxgql.ErrWrongIDType) || !strings.Contains(err.Error(), "CreateWriterInput.circleIDs[0]") {
			t.Errorf("a writer's ID for a circle: %v (code %q)", err, code(err))
		}
		_, err = createArticle(e.ctx, gen.CreateArticleInput{Title: "t", WriterID: ptr("not an ID")})
		if code(err) != veloxgql.CodeBadUserInput || !errors.Is(err, veloxgql.ErrBadID) {
			t.Errorf("a malformed ID: %v (code %q)", err, code(err))
		}
		many := make([]string, 10000)
		for i := range many {
			many[i] = circleID(int64(i + 1))
		}
		_, err = createWriter(e.ctx, gen.CreateWriterInput{Name: "m", Password: "x", CircleIDs: many}, nil)
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

		// A value the row already holds: the UPDATE matches the row, and
		// changes nothing. MySQL counts it only with clientFoundRows=true,
		// which velox's Open asks of its DSN.
		if got, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{Name: ptr("w")}); err != nil || got.Name != "w" {
			t.Errorf("setting the name w holds: %v, %v (MySQL: does the DSN have clientFoundRows=true?)", got, err)
		}

		// Fields: nil leaves one, a value sets it, clear sets NULL.
		in := gen.UpdateWriterInput{Bio: ptr("hi"), Password: ptr("n3w-s3cret"), Score: ptr(3), Meta: ptr(veloxgql.JSON(` + "`" + `{"k":"v"}` + "`" + `))}
		got, err := updateWriter(e.ctx, id, in)
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
		got, err = updateWriter(e.ctx, id, gen.UpdateWriterInput{ClearBio: true, ClearMeta: true, Nick: ptr("nick")})
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
		if _, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{MentorID: ptr(writerID(mentor.ID))}); err != nil {
			t.Fatal(err)
		}
		if st := writer(t, e, w.ID); !is(st.MentorID, mentor.ID) {
			t.Errorf("mentor %v, want %d", st.MentorID, mentor.ID)
		}
		if _, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{ClearMentor: true}); err != nil {
			t.Fatal(err)
		}
		if st := writer(t, e, w.ID); st.MentorID != nil {
			t.Errorf("mentor %v after clearMentor, want none", *st.MentorID)
		}

		// O2M: remove and add move the articles' writer_id; clear then add
		// replaces the set.
		if _, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{AddArticleIDs: []string{articleID(a2.ID)}, RemoveArticleIDs: []string{articleID(a1.ID), articleID(a3.ID)}}); err != nil {
			t.Fatal(err)
		}
		if a, b, c := writerOfArticle(t, e, a1.ID), writerOfArticle(t, e, a2.ID), writerOfArticle(t, e, a3.ID); a != nil || !is(b, w.ID) || c != nil {
			t.Errorf("articles' writers %v %v %v, want none, %d, none", a, b, c, w.ID)
		}
		if _, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{ClearArticles: true, AddArticleIDs: []string{articleID(a3.ID)}}); err != nil {
			t.Fatal(err)
		}
		if a, b, c := writerOfArticle(t, e, a1.ID), writerOfArticle(t, e, a2.ID), writerOfArticle(t, e, a3.ID); a != nil || b != nil || !is(c, w.ID) {
			t.Errorf("articles' writers %v %v %v, want none, none, %d", a, b, c, w.ID)
		}
		if _, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{ClearArticles: true}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Article]().Where(gen.Articles.WriterID.NotNull())); n != 0 {
			t.Errorf("%d articles with a writer after clearArticles, want 0", n)
		}

		// M2M: add and remove change the join rows; clear removes them.
		if _, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{AddCircleIDs: []string{circleID(c2.ID)}, RemoveCircleIDs: []string{circleID(c1.ID)}}); err != nil {
			t.Fatal(err)
		}
		if got := circlesOf(t, e, w.ID); len(got) != 1 || got[0] != "c2" {
			t.Errorf("circles %v, want [c2]", got)
		}
		if _, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{ClearCircles: true, AddCircleIDs: []string{circleID(c1.ID)}}); err != nil {
			t.Fatal(err)
		}
		if got := circlesOf(t, e, w.ID); len(got) != 1 || got[0] != "c1" {
			t.Errorf("circles %v, want [c1]", got)
		}
		// From the M2M's other side.
		if _, err := updateCircle(e.ctx, circleID(c2.ID), gen.UpdateCircleInput{AddWriterIDs: []string{id}}); err != nil {
			t.Fatal(err)
		}
		if got := circlesOf(t, e, w.ID); len(got) != 2 {
			t.Errorf("circles %v, want c1 and c2", got)
		}
		if _, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{ClearCircles: true}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterCircle]()); n != 0 {
			t.Errorf("%d join rows after clearCircles, want 0", n)
		}

		// O2O whose key the profile holds: profileID moves it and clears the
		// one before; clearProfile clears it.
		if _, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{ProfileID: ptr(profileID(p2.ID))}); err != nil {
			t.Fatal(err)
		}
		if a, b := writerOfProfile(t, e, p1.ID), writerOfProfile(t, e, p2.ID); a != nil || !is(b, w.ID) {
			t.Errorf("profiles' writers %v %v, want none, %d", a, b, w.ID)
		}
		if _, err := updateWriter(e.ctx, id, gen.UpdateWriterInput{ClearProfile: true}); err != nil {
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
			_, err := updateWriter(e.ctx, writerID(w.ID), x.in)
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
		_, err := updateArticle(e.ctx, articleID(locked.ID), gen.UpdateArticleInput{Title: ptr("free")})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: UpdateArticleInput: no Article has this ID" {
			t.Errorf("the locked article: %v (code %q), want NOT_FOUND", err, code(err))
		}
		if n := count(t, e.ctx, e.db.Query[gen.Article]().Where(gen.Articles.Title.EQ("locked"))); n != 1 {
			t.Errorf("%d locked articles, want 1", n)
		}
		got, err := updateArticle(e.ctx, articleID(open.ID), gen.UpdateArticleInput{Title: ptr("renamed")})
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
		const want = "veloxgql: UpdateWriterInput: no Writer has this ID"
		for _, in := range []gen.UpdateWriterInput{
			{},
			{AddCircleIDs: []string{circleID(c.ID)}},
			{AddArticleIDs: []string{articleID(a.ID)}},
			{ClearProfile: true},
			{Name: ptr("thawed")},
		} {
			_, err := updateWriter(e.ctx, writerID(frozen.ID), in)
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
		got, err := updateCircle(e.ctx, circleID(c.ID), gen.UpdateCircleInput{Name: ptr("hidden")})
		if err != nil || got == nil || got.ID != c.ID || got.Name != "hidden" {
			t.Fatalf("UpdateCircle = %v, %v: want the circle, named hidden", got, err)
		}
		if n := count(t, e.ctx, e.db.Query[gen.Circle]()); n != 0 {
			t.Errorf("%d circles the caller may read, want 0", n)
		}
		// Hidden now, it is NOT_FOUND to the next update.
		_, err = updateCircle(e.ctx, circleID(c.ID), gen.UpdateCircleInput{Name: ptr("back")})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: UpdateCircleInput: no Circle has this ID" {
			t.Errorf("updating the hidden circle: %v (code %q), want NOT_FOUND", err, code(err))
		}
	})
}

// isExists reports whether s is the existence check of a write: SELECT 1,
// locked with FOR UPDATE except on SQLite, which has no locking reads.
func (e *env) isExists(s string) bool {
	return strings.HasPrefix(s, "SELECT 1 ") && strings.HasSuffix(s, " FOR UPDATE") == (e.target != "sqlite")
}

// TestUpdateEmpty: an input that sets nothing sends no write: one locking
// existence check (SELECT 1 ... FOR UPDATE, without the lock on SQLite)
// checks the row, and one SELECT reads it.
func TestUpdateEmpty(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w := &gen.Writer{Name: "w", Password: "x", Bio: ptr("b")}
		insert(t, e.ctx, e.db, w)
		e.reset()
		got, err := updateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{})
		if err != nil || got.ID != w.ID || got.Name != "w" || got.Bio == nil || *got.Bio != "b" {
			t.Errorf("UpdateWriter = %v, %v: want w unchanged", got, err)
		}
		s := e.stmts()
		if len(s) != 2 || !e.isExists(s[0]) || !strings.HasPrefix(s[1], "SELECT ") || strings.HasPrefix(s[1], "SELECT 1 ") {
			t.Errorf("sent %q, want an existence check (locking, except on SQLite) and a SELECT", s)
		}
		_, err = updateWriter(e.ctx, writerID(999), gen.UpdateWriterInput{})
		if code(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: UpdateWriterInput: no Writer has this ID" {
			t.Errorf("an unknown writer: %v (code %q), want NOT_FOUND", err, code(err))
		}
	})
}

// TestMutationWaitsOnTheLock: on PostgreSQL and MySQL a transaction holding
// the writer's row (FOR UPDATE) keeps an update without setters waiting
// until it ends: its check locks the row too. SQLite
// has no locking reads, and its writers lock the database, so it is not tested.
func TestMutationWaitsOnTheLock(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		if e.target == "sqlite" {
			return // no locking reads there; its writers lock the database
		}
		w := &gen.Writer{Name: "w", Password: "x"}
		insert(t, e.ctx, e.db, w)
		for _, c := range []struct {
			name   string
			mutate func() error
		}{
			{"update", func() error {
				_, err := updateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{})
				return err
			}},
		} {
			name, mutate := c.name, c.mutate
			held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				done <- e.db.Tx(e.ctx, func(ctx context.Context, tx *velox.DB) error {
					if ok, err := tx.Query[gen.Writer]().Where(gen.Writers.ID.EQ(w.ID)).ForUpdate().Exists(ctx); err != nil || !ok {
						return fmt.Errorf("locking the writer: %v, %v", ok, err)
					}
					close(held)
					<-release
					return nil
				})
			}()
			<-held
			res := make(chan error, 1)
			go func() { res <- mutate() }()
			select {
			case err := <-res:
				close(release)
				t.Fatalf("%s: finished while the row was locked: %v", name, err)
			case <-time.After(500 * time.Millisecond):
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-res:
				if err != nil {
					t.Errorf("%s after the lock: %v", name, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: still waiting after the lock ended", name)
			}
		}
	})
}

func TestUpdateErrors(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w := &gen.Writer{Name: "w", Password: "x"}
		insert(t, e.ctx, e.db, w)
		_, err := updateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{Name: ptr("")})
		var ve *velox.ValidationError
		if code(err) != veloxgql.CodeBadUserInput || err == nil || !strings.HasPrefix(err.Error(), "veloxgql: UpdateWriterInput.name: invalid value: ") || !errors.As(err, &ve) {
			t.Errorf("an empty name: %v (code %q)", err, code(err))
		}
		e.reset()
		_, err = updateWriter(e.ctx, circleID(w.ID), gen.UpdateWriterInput{})
		if code(err) != veloxgql.CodeBadUserInput || !errors.Is(err, veloxgql.ErrWrongIDType) || !strings.HasPrefix(err.Error(), "veloxgql: id: ") {
			t.Errorf("a circle's ID for a writer: %v (code %q)", err, code(err))
		}
		_, err = updateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{RemoveCircleIDs: []string{"not an ID"}})
		if code(err) != veloxgql.CodeBadUserInput || !errors.Is(err, veloxgql.ErrBadID) || !strings.Contains(err.Error(), "UpdateWriterInput.removeCircleIDs[0]") {
			t.Errorf("a malformed ID: %v (code %q)", err, code(err))
		}
		if s := e.stmts(); len(s) != 0 {
			t.Errorf("input errors sent %v", s)
		}
	})
}

// TestUpdateAppSetters: the application's setter of a column the input sets
// too wins, the input's dropped (a field, and an edge's key on the row), so
// no UPDATE assigns a column twice; an application's setter that its
// validator refuses is velox's error, uncoded, the server's, and writes
// nothing.
func TestUpdateAppSetters(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		m1, m2 := &gen.Writer{Name: "m1", Password: "x"}, &gen.Writer{Name: "m2", Password: "x"}
		w := &gen.Writer{Name: "w", Password: "x"}
		insert(t, e.ctx, e.db, m1)
		insert(t, e.ctx, e.db, m2)
		insert(t, e.ctx, e.db, w)
		in := gen.UpdateWriterInput{Name: ptr("client"), Bio: ptr("bio"), MentorID: ptr(writerID(m1.ID))}
		got, err := in.Update(e.ctx, writerID(w.ID), gen.Writers.Name.Set("server"), gen.Writers.MentorID.Set(m2.ID))
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "server" || got.Bio == nil || *got.Bio != "bio" || got.MentorID == nil || *got.MentorID != m2.ID {
			t.Errorf("the row = %+v, want the server's name and mentor, and the client's bio", got)
		}
		// The client's empty name is dropped for the server's.
		if got, err := (&gen.UpdateWriterInput{Name: ptr("")}).Update(e.ctx, writerID(w.ID), gen.Writers.Name.Set("server 2")); err != nil || got.Name != "server 2" {
			t.Errorf("an invalid client name the server's replaces: %+v, %v", got, err)
		}
		e.reset()
		_, err = (&gen.UpdateWriterInput{Bio: ptr("other")}).Update(e.ctx, writerID(w.ID), gen.Writers.Name.Set(""))
		var ve *velox.ValidationError
		if !errors.As(err, &ve) || code(err) != "" || strings.Contains(err.Error(), "UpdateWriterInput") {
			t.Errorf("an invalid server setter: %v (code %q), want velox's *velox.ValidationError, uncoded", err, code(err))
		}
		if s := e.stmts(); len(s) != 0 {
			t.Errorf("an invalid server setter sent %v", s)
		}
	})
}

// friendsOf are the IDs of writer id's friends, loaded through the edge
// Writer.friends: the writers its join rows name in friend_id.
func friendsOf(t *testing.T, e *env, id int64) []int64 {
	t.Helper()
	w, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(id)).With(gen.Writers.Friends).One(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	fs, _ := w.Friends.Get()
	var out []int64
	for _, f := range fs {
		out = append(out, f.ID)
	}
	slices.Sort(out)
	return out
}

// TestSelfM2M: Writer.friends joins Writer to itself. Create and update
// write its join rows one way, the writer in writer_id; adding a friend
// already linked changes nothing.
func TestSelfM2M(t *testing.T) {
	eachFK(t, func(t *testing.T, e *env) {
		w1, w2, w3 := &gen.Writer{Name: "w1", Password: "x"}, &gen.Writer{Name: "w2", Password: "x"}, &gen.Writer{Name: "w3", Password: "x"}
		for _, w := range []*gen.Writer{w1, w2, w3} {
			insert(t, e.ctx, e.db, w)
		}
		insert(t, e.ctx, e.db, &gen.WriterFriend{WriterID: w1.ID, FriendID: w2.ID})
		w, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "w", Password: "x", FriendIDs: []string{writerID(w1.ID), writerID(w2.ID), writerID(w1.ID)}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := friendsOf(t, e, w.ID); !slices.Equal(got, []int64{w1.ID, w2.ID}) {
			t.Errorf("w's friends: %v, want %d and %d", got, w1.ID, w2.ID)
		}
		if _, err := updateWriter(e.ctx, writerID(w3.ID), gen.UpdateWriterInput{AddFriendIDs: []string{writerID(w.ID)}}); err != nil {
			t.Fatal(err)
		}
		// Already linked: succeeds, and changes nothing.
		if _, err := updateWriter(e.ctx, writerID(w.ID), gen.UpdateWriterInput{AddFriendIDs: []string{writerID(w1.ID)}}); err != nil {
			t.Errorf("adding w1 again: %v", err)
		}
		if n := count(t, e.ctx, e.db.Query[gen.WriterFriend]()); n != 4 {
			t.Errorf("%d join rows, want 4", n)
		}
		if got := friendsOf(t, e, w3.ID); !slices.Equal(got, []int64{w.ID}) {
			t.Errorf("w3's friends: %v, want %d", got, w.ID)
		}
		if got := friendsOf(t, e, w1.ID); !slices.Equal(got, []int64{w2.ID}) {
			t.Errorf("w1's friends: %v, want %d: its own row kept", got, w2.ID)
		}
	})
}

// TestUnlinkScope: removeXIDs and clearX of an M2M edge delete only the
// join rows to rows the caller may read, under the
// target's Read policy, which no join row has: a link to org 8's writer,
// or to the circle the Read policy hides, stays. clearX reads the join rows
// a page at a time, and the links it leaves do not stop it.
func TestUnlinkScope(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		ctx8 := schema.Org.With(e.ctx, int64(8))
		all := velox.WithoutPolicy(e.ctx, "the test's view")
		linked := func(w, c int64) bool {
			t.Helper()
			return count(t, all, e.db.Query[gen.WriterCircle]().Where(gen.WriterCircles.WriterID.EQ(w), gen.WriterCircles.CircleID.EQ(c))) == 1
		}
		// As the org of the writer.
		linkIn := func(ctx context.Context, w, c int64) { t.Helper(); insert(t, ctx, e.db, &gen.WriterCircle{WriterID: w, CircleID: c}) }
		link := func(w, c int64) { t.Helper(); linkIn(e.ctx, w, c) }

		// From the shared circle: org 8's writer keeps its link.
		c := &gen.Circle{Name: "c"}
		insert(t, e.ctx, e.db, c)
		mine, other := &gen.Writer{Name: "mine", Password: "x"}, &gen.Writer{Name: "other", Password: "x"}
		insert(t, e.ctx, e.db, mine)
		insert(t, ctx8, e.db, other)
		link(mine.ID, c.ID)
		linkIn(ctx8, other.ID, c.ID)
		if _, err := updateCircle(e.ctx, circleID(c.ID), gen.UpdateCircleInput{RemoveWriterIDs: []string{writerID(other.ID), writerID(mine.ID)}}); err != nil {
			t.Fatal(err)
		}
		if linked(mine.ID, c.ID) || !linked(other.ID, c.ID) {
			t.Errorf("after removeWriterIDs: mine linked %v, org 8's %v; want false, true", linked(mine.ID, c.ID), linked(other.ID, c.ID))
		}
		link(mine.ID, c.ID)
		if _, err := updateCircle(e.ctx, circleID(c.ID), gen.UpdateCircleInput{ClearWriters: true}); err != nil {
			t.Fatal(err)
		}
		if linked(mine.ID, c.ID) || !linked(other.ID, c.ID) {
			t.Errorf("after clearWriters: mine linked %v, org 8's %v; want false, true", linked(mine.ID, c.ID), linked(other.ID, c.ID))
		}

		// From the writer: the hidden circle keeps its link.
		hidden, visible := &gen.Circle{Name: "hidden"}, &gen.Circle{Name: "visible"}
		insert(t, e.ctx, e.db, hidden)
		insert(t, e.ctx, e.db, visible)
		link(mine.ID, hidden.ID)
		link(mine.ID, visible.ID)
		if _, err := updateWriter(e.ctx, writerID(mine.ID), gen.UpdateWriterInput{RemoveCircleIDs: []string{circleID(hidden.ID), circleID(visible.ID)}}); err != nil {
			t.Fatal(err)
		}
		if !linked(mine.ID, hidden.ID) || linked(mine.ID, visible.ID) {
			t.Errorf("after removeCircleIDs: hidden linked %v, visible %v; want true, false", linked(mine.ID, hidden.ID), linked(mine.ID, visible.ID))
		}
		link(mine.ID, visible.ID)
		if _, err := updateWriter(e.ctx, writerID(mine.ID), gen.UpdateWriterInput{ClearCircles: true}); err != nil {
			t.Fatal(err)
		}
		if !linked(mine.ID, hidden.ID) || linked(mine.ID, visible.ID) {
			t.Errorf("after clearCircles: hidden linked %v, visible %v; want true, false", linked(mine.ID, hidden.ID), linked(mine.ID, visible.ID))
		}

		// Pages of 2: five writers, of orgs 7 and 8 in turn, in one
		// circle; clearWriters unlinks org 7's three.
		paged := schema.Org.With(veloxgql.NewExtension(e.db, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 2})).NewContext(context.Background()), int64(7))
		big := &gen.Circle{Name: "big"}
		insert(t, e.ctx, e.db, big)
		for i := range 5 {
			w, in := &gen.Writer{Name: fmt.Sprint("w", i), Password: "x"}, e.ctx
			if i%2 == 1 {
				in = ctx8
			}
			insert(t, in, e.db, w)
			linkIn(in, w.ID, big.ID)
		}
		if _, err := updateCircle(paged, circleID(big.ID), gen.UpdateCircleInput{ClearWriters: true}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, all, e.db.Query[gen.WriterCircle]().Where(gen.WriterCircles.CircleID.EQ(big.ID))); n != 2 {
			t.Errorf("%d join rows of the big circle after clearWriters, want org 8's 2", n)
		}
		if n := count(t, ctx8, e.db.Query[gen.Writer]().Where(gen.Writers.Circles.Has())); n != 3 {
			t.Errorf("%d of org 8's writers in a circle, want 3", n)
		}
	})
}

// TestValidateFirst: CreateX and UpdateX run the validators of the fields
// their input sets before any statement, so an invalid field with IDs that
// name no row is the validator's BAD_USER_INPUT, and nothing is sent.
func TestValidateFirst(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		w := &gen.Writer{Name: "w", Password: "x"}
		insert(t, e.ctx, e.db, w)
		e.reset()
		bogus := writerID(999)
		var ve *velox.ValidationError
		_, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "", Password: "x", MentorID: &bogus, CircleIDs: []string{circleID(999)}}, nil)
		if code(err) != veloxgql.CodeBadUserInput || err == nil || !strings.HasPrefix(err.Error(), "veloxgql: CreateWriterInput.name: invalid value: ") || !errors.As(err, &ve) {
			t.Errorf("create, an empty name and a bogus mentor: %v (code %q)", err, code(err))
		}
		for _, id := range []string{writerID(w.ID), writerID(998)} {
			_, err = updateWriter(e.ctx, id, gen.UpdateWriterInput{Name: ptr(""), MentorID: &bogus, AddCircleIDs: []string{circleID(999)}})
			if code(err) != veloxgql.CodeBadUserInput || err == nil || !strings.HasPrefix(err.Error(), "veloxgql: UpdateWriterInput.name: invalid value: ") || !errors.As(err, &ve) {
				t.Errorf("update %s, an empty name and a bogus mentor: %v (code %q)", id, err, code(err))
			}
		}
		if s := e.stmts(); len(s) != 0 {
			t.Errorf("an invalid field sent %q, want nothing", s)
		}
	})
}

// TestRequiredSelfEdge: Folder's parent and prev are Required self edges,
// whose keys are NULL-able and ON DELETE SET NULL. Create sets them, the
// owning side's update re-parents, the other side's update only adds, and
// after the parent's delete the edges read nil. Then the update sets prev
// (a prev another folder holds is a unique ConstraintError, presented as
// BAD_USER_INPUT), and a create takes children and a next (a next that has a
// prev is BAD_USER_INPUT).
func TestRequiredSelfEdge(t *testing.T) {
	eachFK(t, func(t *testing.T, e *env) {
		// rawFolder inserts a folder by raw SQL, its keys NULL, which velox
		// refuses for a Required edge.
		rawFolder := func(name string) *gen.Folder {
			t.Helper()
			if _, err := e.raw.Exec("INSERT INTO folders (name) VALUES ('" + name + "')"); err != nil {
				t.Fatal(err)
			}
			f, err := e.db.Query[gen.Folder]().Where(gen.Folders.Name.EQ(name)).One(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			return f
		}
		// keys are folder id's folder_children and folder_next.
		keys := func(id int64) (children, next *int64) {
			t.Helper()
			f, err := e.db.Query[gen.Folder]().Where(gen.Folders.ID.EQ(id)).One(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			return f.FolderChildren, f.FolderNext
		}
		is := func(p *int64, id int64) bool { return p != nil && *p == id }

		r := rawFolder("r")
		c, err := createFolder(e.ctx, gen.CreateFolderInput{Name: "c", ParentID: folderID(r.ID), PrevID: folderID(r.ID)})
		if err != nil {
			t.Fatal(err)
		}
		if ch, nx := keys(c.ID); !is(ch, r.ID) || !is(nx, r.ID) {
			t.Errorf("c's folder_children %v, folder_next %v, want r %d", ch, nx, r.ID)
		}

		// The other side's update has only add.
		in := reflect.TypeFor[gen.UpdateFolderInput]()
		for _, f := range []string{"AddChildIDs", "ParentID", "PrevID"} {
			if _, ok := in.FieldByName(f); !ok {
				t.Errorf("UpdateFolderInput has no %s", f)
			}
		}
		for _, f := range []string{"RemoveChildIDs", "ClearChildren", "NextID", "ClearNext"} {
			if _, ok := in.FieldByName(f); ok {
				t.Errorf("UpdateFolderInput has %s", f)
			}
		}

		// The owning side re-parents.
		c2, err := createFolder(e.ctx, gen.CreateFolderInput{Name: "c2", ParentID: folderID(r.ID), PrevID: folderID(c.ID)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := updateFolder(e.ctx, folderID(c2.ID), gen.UpdateFolderInput{ParentID: ptr(folderID(c.ID))}); err != nil {
			t.Fatal(err)
		}
		if ch, _ := keys(c2.ID); !is(ch, c.ID) {
			t.Errorf("c2's folder_children %v, want c %d", ch, c.ID)
		}

		// The other side adds a child whose key is NULL.
		d := rawFolder("d")
		if _, err := updateFolder(e.ctx, folderID(r.ID), gen.UpdateFolderInput{AddChildIDs: []string{folderID(d.ID)}}); err != nil {
			t.Fatal(err)
		}
		if ch, _ := keys(d.ID); !is(ch, r.ID) {
			t.Errorf("d's folder_children %v, want r %d", ch, r.ID)
		}

		// The parent's delete sets the keys NULL, and the edges read nil.
		if _, err := e.raw.Exec(fmt.Sprintf("DELETE FROM folders WHERE id = %d", r.ID)); err != nil {
			t.Fatal(err)
		}
		c, err = e.db.Query[gen.Folder]().Where(gen.Folders.ID.EQ(c.ID)).One(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if p, err := c.ParentGQL(e.ctx); p != nil || err != nil {
			t.Errorf("c's ParentGQL after r's delete = %v, %v; want nil, nil", p, err)
		}
		if p, err := c.PrevGQL(e.ctx); p != nil || err != nil {
			t.Errorf("c's PrevGQL after r's delete = %v, %v; want nil, nil", p, err)
		}

		// The owning side's update sets its prev: a free folder s takes it, and
		// c's children are not touched.
		s := rawFolder("s")
		if _, err := updateFolder(e.ctx, folderID(c.ID), gen.UpdateFolderInput{PrevID: ptr(folderID(s.ID))}); err != nil {
			t.Fatalf("UpdateFolder of c, prev s: %v", err)
		}
		if ch, nx := keys(c.ID); ch != nil || !is(nx, s.ID) {
			t.Errorf("c's folder_children %v, folder_next %v, want NULL, s %d", ch, nx, s.ID)
		}
		if ch, _ := keys(c2.ID); !is(ch, c.ID) {
			t.Errorf("c2's folder_children %v, want c %d", ch, c.ID)
		}

		// A prev another folder already holds, y of z (z.folder_next is y): the key is unique, so
		// the database refuses, and the error is the ConstraintError velox
		// reports, presented as BAD_USER_INPUT without the driver's text. Each
		// database names the constraint its own way, so only SQLite's text is
		// compared.
		y := rawFolder("y")
		z, err := createFolder(e.ctx, gen.CreateFolderInput{Name: "z", ParentID: folderID(s.ID), PrevID: folderID(y.ID)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = updateFolder(e.ctx, folderID(c.ID), gen.UpdateFolderInput{PrevID: ptr(folderID(y.ID))})
		var ce *velox.ConstraintError
		if !errors.As(err, &ce) || ce.Kind != velox.ConstraintUnique {
			t.Fatalf("UpdateFolder of c, prev y held by z: %v, want a unique ConstraintError", err)
		}
		if e.target == "sqlite" && !strings.HasPrefix(err.Error(), "velox: gen.Folder: unique constraint folders.folder_next violated: ") {
			t.Errorf("the error is %q, want it to start with the folders.folder_next constraint", err)
		}
		ge := veloxgql.ErrorPresenter(nil)(e.ctx, err)
		if ge.Message != "unique constraint violated" || ge.Extensions["code"] != veloxgql.CodeBadUserInput {
			t.Errorf("presented as %q %v, want BAD_USER_INPUT unique constraint violated", ge.Message, ge.Extensions)
		}
		if _, nx := keys(c.ID); !is(nx, s.ID) {
			t.Errorf("c's folder_next %v after the refusal, want s %d", nx, s.ID)
		}
		if _, nx := keys(z.ID); !is(nx, y.ID) {
			t.Errorf("z's folder_next %v after the refusal, want y %d", nx, y.ID)
		}

		// A create takes d as a child and as its next: d's keys are both the
		// new folder.
		n, err := createFolder(e.ctx, gen.CreateFolderInput{Name: "n", ParentID: folderID(s.ID), PrevID: folderID(c2.ID), ChildIDs: []string{folderID(d.ID)}, NextID: ptr(folderID(d.ID))})
		if err != nil {
			t.Fatalf("CreateFolder with childIDs and nextID d: %v", err)
		}
		if ch, nx := keys(d.ID); !is(ch, n.ID) || !is(nx, n.ID) {
			t.Errorf("d's folder_children %v, folder_next %v, want n %d", ch, nx, n.ID)
		}

		// A next that already has a prev, z of y, is BAD_USER_INPUT on a create;
		// nothing is created.
		f := rawFolder("f")
		_, err = createFolder(e.ctx, gen.CreateFolderInput{Name: "m", ParentID: folderID(s.ID), PrevID: folderID(f.ID), NextID: ptr(folderID(z.ID))})
		const held = "veloxgql: CreateFolderInput.nextID: a Folder is already linked to another Folder"
		if code(err) != veloxgql.CodeBadUserInput || err == nil || err.Error() != held {
			t.Errorf("CreateFolder with the held next z: %v (code %q), want %q", err, code(err), held)
		}
		if k := count(t, e.ctx, e.db.Query[gen.Folder]().Where(gen.Folders.Name.EQ("m"))); k != 0 {
			t.Errorf("%d folders m after the refusal, want 0", k)
		}
	})
}

// peerRows are the rows of writer_peers, as [writer_id, peer_id] pairs in
// order, read with raw SQL: every row of the table, not those a load of one
// writer reads.
func peerRows(t *testing.T, e *env) [][2]int64 {
	t.Helper()
	rs, err := e.raw.Query("SELECT writer_id, peer_id FROM writer_peers ORDER BY writer_id, peer_id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var out [][2]int64
	for rs.Next() {
		var r [2]int64
		if err := rs.Scan(&r[0], &r[1]); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// wantPeers fails unless writer_peers holds exactly the pairs, in any order.
func wantPeers(t *testing.T, e *env, when string, pairs ...[2]int64) {
	t.Helper()
	slices.SortFunc(pairs, func(a, b [2]int64) int {
		if a[0] != b[0] {
			return int(a[0] - b[0])
		}
		return int(a[1] - b[1])
	})
	if got := peerRows(t, e); !slices.Equal(got, pairs) {
		t.Errorf("%s: writer_peers holds %v, want %v", when, got, pairs)
	}
}

// peersGQL are the IDs of the writers PeersGQL of writer id lists.
func peersGQL(t *testing.T, e *env, id int64) []int64 {
	t.Helper()
	w, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(id)).One(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := w.PeersGQL(e.ctx, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	es, err := c.Edges(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := []int64{}
	for _, x := range es {
		out = append(out, x.Node.ID)
	}
	slices.Sort(out)
	return out
}

// TestBidiPeers: Writer.peers is a self M2M with no inverse, so it is
// bidirectional: every link is two join rows, written in one INSERT, and
// every unlink removes both. The foreign keys of fkDDL would refuse a
// DELETE of a writer a row still names in either column.
func TestBidiPeers(t *testing.T) {
	eachFK(t, func(t *testing.T, e *env) {
		a, b := &gen.Writer{Name: "a", Password: "x"}, &gen.Writer{Name: "b", Password: "x"}
		insert(t, e.ctx, e.db, a)
		insert(t, e.ctx, e.db, b)

		// Linking a to b writes (a, b) and its mirror (b, a), in one INSERT.
		e.reset()
		if _, err := updateWriter(e.ctx, writerID(a.ID), gen.UpdateWriterInput{AddPeerIDs: []string{writerID(b.ID)}}); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, s := range e.stmts() {
			if strings.HasPrefix(s, "INSERT") && strings.Contains(s, "writer_peers") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("sent %q, want one INSERT INTO writer_peers", e.stmts())
		}
		wantPeers(t, e, "a adds b", [2]int64{a.ID, b.ID}, [2]int64{b.ID, a.ID})
		if got := peersGQL(t, e, b.ID); !slices.Equal(got, []int64{a.ID}) {
			t.Errorf("b's PeersGQL: %v, want a %d", got, a.ID)
		}
		if got := peersGQL(t, e, a.ID); !slices.Equal(got, []int64{b.ID}) {
			t.Errorf("a's PeersGQL: %v, want b %d", got, b.ID)
		}

		// Adding it again changes nothing.
		if _, err := updateWriter(e.ctx, writerID(b.ID), gen.UpdateWriterInput{AddPeerIDs: []string{writerID(a.ID)}}); err != nil {
			t.Errorf("b adds a, already its peer: %v", err)
		}
		wantPeers(t, e, "b adds a", [2]int64{a.ID, b.ID}, [2]int64{b.ID, a.ID})

		// Removing b from a's peers removes both rows.
		if _, err := updateWriter(e.ctx, writerID(a.ID), gen.UpdateWriterInput{RemovePeerIDs: []string{writerID(b.ID)}}); err != nil {
			t.Fatal(err)
		}
		wantPeers(t, e, "a removes b")

		// Clearing b's peers, which a added, removes both rows.
		if _, err := updateWriter(e.ctx, writerID(a.ID), gen.UpdateWriterInput{AddPeerIDs: []string{writerID(b.ID)}}); err != nil {
			t.Fatal(err)
		}
		wantPeers(t, e, "a adds b again", [2]int64{a.ID, b.ID}, [2]int64{b.ID, a.ID})
		if _, err := updateWriter(e.ctx, writerID(b.ID), gen.UpdateWriterInput{ClearPeers: true}); err != nil {
			t.Fatal(err)
		}
		wantPeers(t, e, "b clears its peers")

		// A create with peerIDs writes both rows.
		c, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "c", Password: "x", PeerIDs: []string{writerID(a.ID)}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantPeers(t, e, "c is created with a", [2]int64{a.ID, c.ID}, [2]int64{c.ID, a.ID})
		if got := peersGQL(t, e, a.ID); !slices.Equal(got, []int64{c.ID}) {
			t.Errorf("a's PeersGQL: %v, want c %d", got, c.ID)
		}
	})
}

// partnerOf is the writer_partner of writer id: nil for NULL.
func partnerOf(t *testing.T, e *env, id int64) *int64 {
	t.Helper()
	w, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(id)).One(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return w.WriterPartner
}

// wantPartners fails unless each writer's writer_partner is the one given:
// ids come in pairs, a writer and its partner, 0 for NULL.
func wantPartners(t *testing.T, e *env, when string, ids ...int64) {
	t.Helper()
	for i := 0; i < len(ids); i += 2 {
		got := partnerOf(t, e, ids[i])
		switch {
		case ids[i+1] == 0 && got != nil:
			t.Errorf("%s: writer %d's writer_partner = %d, want NULL", when, ids[i], *got)
		case ids[i+1] != 0 && (got == nil || *got != ids[i+1]):
			t.Errorf("%s: writer %d's writer_partner = %v, want %d", when, ids[i], got, ids[i+1])
		}
	}
}

// TestBidiPartner: Writer.partner is a self O2O with no inverse, so it is
// bidirectional: a link sets the key of both rows, a re-point frees the old
// partner's, and a clear frees both. A partner that has one is refused by
// the unique index, and a writer cannot be its own partner.
func TestBidiPartner(t *testing.T) {
	eachFK(t, func(t *testing.T, e *env) {
		a, b, d := &gen.Writer{Name: "a", Password: "x"}, &gen.Writer{Name: "b", Password: "x"}, &gen.Writer{Name: "d", Password: "x"}
		for _, w := range []*gen.Writer{a, b, d} {
			insert(t, e.ctx, e.db, w)
		}

		// A create with a partnerID links both rows.
		c, err := createWriter(e.ctx, gen.CreateWriterInput{Name: "c", Password: "x", PartnerID: ptr(writerID(a.ID))}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantPartners(t, e, "c is created with a", c.ID, a.ID, a.ID, c.ID, b.ID, 0, d.ID, 0)

		// Re-pointing c to b frees a.
		if _, err := updateWriter(e.ctx, writerID(c.ID), gen.UpdateWriterInput{PartnerID: ptr(writerID(b.ID))}); err != nil {
			t.Fatal(err)
		}
		wantPartners(t, e, "c is re-pointed to b", c.ID, b.ID, b.ID, c.ID, a.ID, 0, d.ID, 0)

		// Setting the partner it has changes nothing.
		if _, err := updateWriter(e.ctx, writerID(c.ID), gen.UpdateWriterInput{PartnerID: ptr(writerID(b.ID))}); err != nil {
			t.Errorf("c's partner b again: %v", err)
		}
		wantPartners(t, e, "c's partner b again", c.ID, b.ID, b.ID, c.ID)

		// Clearing b's partner frees c too, though c's row was not named.
		if _, err := updateWriter(e.ctx, writerID(b.ID), gen.UpdateWriterInput{ClearPartner: true}); err != nil {
			t.Fatal(err)
		}
		wantPartners(t, e, "b clears its partner", b.ID, 0, c.ID, 0, a.ID, 0, d.ID, 0)

		// A partner that has one: a is d's, and d's key holds a, so c cannot
		// take a. The unique index refuses it, whatever the partner's own
		// key says, and a, c and d are as they were.
		if _, err := updateWriter(e.ctx, writerID(a.ID), gen.UpdateWriterInput{PartnerID: ptr(writerID(d.ID))}); err != nil {
			t.Fatal(err)
		}
		wantPartners(t, e, "a is linked to d", a.ID, d.ID, d.ID, a.ID, c.ID, 0)
		_, err = updateWriter(e.ctx, writerID(c.ID), gen.UpdateWriterInput{PartnerID: ptr(writerID(a.ID))})
		var ce *velox.ConstraintError
		if !errors.As(err, &ce) || ce.Kind != velox.ConstraintUnique {
			t.Fatalf("c takes a, d's partner: %v, want a unique ConstraintError", err)
		}
		ge := veloxgql.ErrorPresenter(nil)(e.ctx, err)
		if ge.Message != "unique constraint violated" || ge.Extensions["code"] != veloxgql.CodeBadUserInput {
			t.Errorf("presented as %q %v", ge.Message, ge.Extensions)
		}
		wantPartners(t, e, "c fails to take a", a.ID, d.ID, d.ID, a.ID, c.ID, 0)

		// A partner of another org is not the caller's to name: the
		// mutation's existence check, under the org, finds no such writer.
		h := &gen.Writer{Name: "h", Password: "x"}
		insert(t, schema.Org.With(e.ctx, int64(8)), e.db, h)
		_, err = updateWriter(e.ctx, writerID(c.ID), gen.UpdateWriterInput{PartnerID: ptr(writerID(h.ID))})
		if code(err) != veloxgql.CodeNotFound || err == nil || err.Error() != "veloxgql: UpdateWriterInput.partnerID: no Writer has this ID" {
			t.Errorf("c takes h, of org 8: %v (code %q), want NOT_FOUND", err, code(err))
		}
		wantPartners(t, e, "c fails to take h", c.ID, 0, a.ID, d.ID, d.ID, a.ID)
		if hw, err := e.db.Query[gen.Writer]().Where(gen.Writers.ID.EQ(h.ID)).One(schema.Org.With(e.ctx, int64(8))); err != nil {
			t.Fatal(err)
		} else if hw.WriterPartner != nil {
			t.Errorf("c fails to take h: h's writer_partner = %d, want NULL", *hw.WriterPartner)
		}

		// A partner the caller may read but not change, the "frozen" writer
		// of Writer's Write policy, passes that check; the link of its key
		// matches no row, and finds it unreachable. The three steps run in
		// one transaction, so c's own key, set before the link, is rolled back.
		f := &gen.Writer{Name: "frozen", Password: "x"}
		insert(t, e.ctx, e.db, f)
		_, err = updateWriter(e.ctx, writerID(c.ID), gen.UpdateWriterInput{PartnerID: ptr(writerID(f.ID))})
		if !errors.As(err, &ce) || ce.Kind != velox.ConstraintUnique {
			t.Fatalf("c takes the frozen f: %v, want a unique ConstraintError", err)
		}
		if want := fmt.Sprintf("row %d cannot be linked", f.ID); ce.Err == nil || ce.Err.Error() != want {
			t.Errorf("c takes f: the constraint's error is %v, want %q", ce.Err, want)
		}
		wantPartners(t, e, "c fails to take f", c.ID, 0, f.ID, 0, a.ID, d.ID, d.ID, a.ID)

		// A writer cannot be its own partner: a validation error on the
		// key's field, and nothing is written.
		_, err = updateWriter(e.ctx, writerID(c.ID), gen.UpdateWriterInput{PartnerID: ptr(writerID(c.ID))})
		var ve *velox.ValidationError
		if code(err) != veloxgql.CodeBadUserInput || err == nil || !strings.HasPrefix(err.Error(), "veloxgql: UpdateWriterInput.partnerID: invalid value: a row cannot be its own ") || !errors.As(err, &ve) {
			t.Errorf("c as its own partner: %v (code %q)", err, code(err))
		}
		wantPartners(t, e, "c fails to take itself", c.ID, 0, a.ID, d.ID, d.ID, a.ID)
	})
}
`

// TestMutationsRun generates e2eMutSchema's Go and runs e2eMutRun on it:
// the inputs' Create and Update with each kind of edge (a bidirectional self
// edge too), the policy scope of the IDs they name, atomicity, and
// their errors. Each test passes
// on SQLite, and on PostgreSQL (through pgx and database/sql) and MySQL when
// VELOX_TEST_POSTGRES and VELOX_TEST_MYSQL name them; with
// VELOX_ITEST_REQUIRE=1, the application's TestMain fails unless both do.
func TestMutationsRun(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{"schema/schema.go": e2eMutSchema, "schema/article_policy.go": e2eMutPolicy, "schema/circle_policy.go": e2eMutCirclePolicy, "schema/writer_policy.go": e2eMutWriterPolicy, "schema/profile_policy.go": e2eMutProfilePolicy, "mutation_test.go": e2eMutRun, "countless_test.go": e2eMutCountless})
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	if out, err := run("vet", "./gen"); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	// The databases each test runs on: the server ones when their
	// variables name them, which run passes on, as it does CI.
	targets := []string{"sqlite"}
	if os.Getenv("VELOX_TEST_POSTGRES") != "" {
		targets = append(targets, "pgx", "postgres")
	}
	if os.Getenv("VELOX_TEST_MYSQL") != "" {
		targets = append(targets, "mysql")
	}
	t.Logf("targets: %v", targets)
	// The generated module's tests run under the race detector when this
	// run does: the mutations take locks and nest transactions.
	args := []string{"test", "-count=1", "-v"}
	if raceEnabled {
		args = append(args, "-race")
	}
	out, err := run(append(args, ".")...)
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
	for _, name := range []string{"TestCreate", "TestCreateM2MBothSides", "TestRepeatedIDs", "TestEdgeScope", "TestM2MLinkNeedsReadOnly", "TestAtomic", "TestCreateErrors", "TestRepoint", "TestRepointHiddenChild",
		"TestUpdate", "TestUpdateAppSetters", "TestUpdateConflicts", "TestUpdatePolicy", "TestUpdateWriteScope", "TestUpdateReadBack", "TestUpdateEmpty", "TestUpdateErrors", "TestMutationWaitsOnTheLock",
		"TestSelfM2M", "TestUnlinkScope", "TestValidateFirst", "TestRequiredSelfEdge", "TestBidiPeers", "TestBidiPartner", "TestWriteHiddenProfile"} {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			t.Errorf("no PASS of %s in\n%s", name, out)
		}
		for _, tg := range targets {
			if !strings.Contains(out, "--- PASS: "+name+"/"+tg+" (") {
				t.Errorf("no PASS of %s/%s in\n%s", name, tg, out)
			}
		}
	}
	// It runs on SQLite only, behind a driver that reports no count.
	if !strings.Contains(out, "--- PASS: TestUnknownRowCount ") {
		t.Errorf("no PASS of TestUnknownRowCount in\n%s", out)
	}
	for _, tg := range targets {
		t.Logf("%s: %d tests passed", tg, strings.Count(out, "/"+tg+" ("))
	}
	if strings.Contains(out, "--- SKIP") {
		t.Errorf("a test skipped:\n%s", out)
	}
}

// e2eMutServeResolvers are the resolvers of the mutation schema served by
// gqlgen that the test uses, one line each; gqlgen stubs the others, and keeps
// these when it generates.
const e2eMutServeResolvers = `package graph

import (
	"context"

	"example.com/app/gen"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Node is the resolver for the node field.
func (r *queryResolver) Node(ctx context.Context, id string) (veloxgql.Noder, error) {
	return gen.Node(ctx, id)
}

// Nodes is the resolver for the nodes field.
func (r *queryResolver) Nodes(ctx context.Context, ids []string) ([]veloxgql.Noder, error) {
	return gen.Nodes(ctx, ids)
}

// UpdateFolder is the resolver for the updateFolder field.
func (r *mutationResolver) UpdateFolder(ctx context.Context, id string, input gen.UpdateFolderInput) (*gen.Folder, error) {
	return input.Update(ctx, id)
}

// Mutation returns MutationResolver implementation.
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
`

// e2eMutServeRun is the application's test of the mutation schema served by
// gqlgen, on SQLite with foreign keys on: Folder's Required self edges, whose
// keys are NULL-able, read through a query and written through a mutation.
const e2eMutServeRun = `package app_test

import (
	"database/sql"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	_ "modernc.org/sqlite"

	"example.com/app/graph"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// TestFolderServed: a folder's parent and prev, which are Required, read as
// the folders they name, and as null once the folder they name is deleted (the
// keys are ON DELETE SET NULL); and updateFolder moves both.
func TestFolderServed(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE folders (id INTEGER PRIMARY KEY, name TEXT NOT NULL, folder_children INTEGER REFERENCES folders (id) ON DELETE SET NULL, folder_next INTEGER UNIQUE REFERENCES folders (id) ON DELETE SET NULL)",
		"INSERT INTO folders (id, name) VALUES (1, 'r'), (2, 'o')",
		"INSERT INTO folders (id, name, folder_children, folder_next) VALUES (3, 'c', 1, 1)",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
	srv.Use(veloxgql.NewExtension(velox.Open(velox.SQLite, db)))
	srv.AddTransport(transport.POST{})
	do := func(query string) string {
		t.Helper()
		body := fmt.Sprintf("{\"query\":%q}", query)
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Body.String()
	}
	c := veloxgql.EncodeID("Folder", 3)
	o := veloxgql.EncodeID("Folder", 2)
	read := fmt.Sprintf("{ node(id: %q) { ... on Folder { name parent { name } prev { name } } } }", c)
	for _, step := range []struct {
		name, query, want string
		before         string
	}{
		{"read", read, ` + "`" + `{"data":{"node":{"name":"c","parent":{"name":"r"},"prev":{"name":"r"}}}}` + "`" + `, ""},
		{"read after r is deleted", read, ` + "`" + `{"data":{"node":{"name":"c","parent":null,"prev":null}}}` + "`" + `, "DELETE FROM folders WHERE id = 1"},
		{"update", fmt.Sprintf("mutation { updateFolder(id: %q, input: {parentID: %q, prevID: %q}) { name parent { name } prev { name } } }", c, o, o),
			` + "`" + `{"data":{"updateFolder":{"name":"c","parent":{"name":"o"},"prev":{"name":"o"}}}}` + "`" + `, ""},
	} {
		if step.before != "" {
			if _, err := db.Exec(step.before); err != nil {
				t.Fatalf("%s: %s: %v", step.name, step.before, err)
			}
		}
		if got := do(step.query); got != step.want {
			t.Errorf("%s:\ngot  %s\nwant %s", step.name, got, step.want)
		}
	}
}
`

// e2eMutServeSDL is the application's own schema: the mutation it serves,
// which velox-graphql leaves to it.
const e2eMutServeSDL = `type Mutation {
  updateFolder(id: ID!, input: UpdateFolderInput!): Folder!
}
`

// TestMutationSchemaServes generates the mutation schema's application with
// velox-gen and the plugin, then gqlgen over it, with resolvers for node,
// nodes and updateFolder, which the application's own schema declares, and
// runs a test through gqlgen's handler: the schema's input types, with their
// Required self edges, bind and serve. It
// needs no database but SQLite.
func TestMutationSchemaServes(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{
		"schema/schema.go":          e2eMutSchema,
		"gqlgen.yml":                e2eCollectGqlgen,
		"graph/app.graphql":         e2eMutServeSDL,
		"graph/schema.resolvers.go": e2eMutServeResolvers,
		"serve_test.go":             e2eMutServeRun,
	}, true)
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if out, err := run("tool", "gqlgen", "generate"); err != nil {
		t.Fatalf("gqlgen generate: %v\n%s", err, out)
	}
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	if out, err := run("vet", "./..."); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	out, err := run("test", "-count=1", "-v", ".")
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
	for _, name := range []string{"TestFolderServed"} {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			t.Errorf("no PASS of %s in\n%s", name, out)
		}
	}
	if strings.Contains(out, "--- SKIP") {
		t.Errorf("a test skipped:\n%s", out)
	}
}
