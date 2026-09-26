package integration_test

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/query"
	"github.com/syssam/velox/tests/integration/tag"
	"github.com/syssam/velox/tests/integration/user"
)

// The sql/schemaconfig feature: AlternateSchema(SchemaConfig) must qualify
// every table a client reads or writes. It once did nothing — the option was
// stored on the root config and no builder ever read it, so every statement
// went to the default schema and the data landed in the wrong database.
//
// The harness runs the Postgres dialect (velox's SQL builder drops schema
// qualifiers on SQLite) against a real SQLite connection whose main database
// and two ATTACHed databases, "alt" and "joins", each hold the full schema.
// SQLite resolves "alt"."users" to the attached database, so the tests assert
// both the rendered SQL and where the rows actually land: a table the client
// forgot to qualify is read from or written to main, which must stay empty.

// sqlRecorder wraps a driver and records every statement it runs, including
// those inside a transaction.
type sqlRecorder struct {
	dialect.Driver
	mu    sync.Mutex
	stmts []string
}

func (r *sqlRecorder) record(q string) {
	r.mu.Lock()
	r.stmts = append(r.stmts, q)
	r.mu.Unlock()
}

func (r *sqlRecorder) Exec(ctx context.Context, q string, args, v any) error {
	r.record(q)
	return r.Driver.Exec(ctx, q, args, v)
}

func (r *sqlRecorder) Query(ctx context.Context, q string, args, v any) error {
	r.record(q)
	return r.Driver.Query(ctx, q, args, v)
}

func (r *sqlRecorder) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := r.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &recordedTx{Tx: tx, r: r}, nil
}

// BeginTx is what the generated client's Tx and BeginTx require.
func (r *sqlRecorder) BeginTx(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	b, ok := r.Driver.(interface {
		BeginTx(context.Context, *sql.TxOptions) (dialect.Tx, error)
	})
	if !ok {
		return r.Tx(ctx)
	}
	tx, err := b.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &recordedTx{Tx: tx, r: r}, nil
}

// take returns the statements recorded since the last call and resets.
func (r *sqlRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stmts
	r.stmts = nil
	return s
}

type recordedTx struct {
	dialect.Tx
	r *sqlRecorder
}

func (t *recordedTx) Exec(ctx context.Context, q string, args, v any) error {
	t.r.record(q)
	return t.Tx.Exec(ctx, q, args, v)
}

func (t *recordedTx) Query(ctx context.Context, q string, args, v any) error {
	t.r.record(q)
	return t.Tx.Query(ctx, q, args, v)
}

// altSchemas routes every table to "alt" and the post_tags join table to
// "joins", so a test can tell a node table from the M2M join table.
var altSchemas = integration.SchemaConfig{
	Comment:  "alt",
	Like:     "alt",
	Pet:      "alt",
	Post:     "alt",
	PostTags: "joins",
	Tag:      "alt",
	Token:    "alt",
	User:     "alt",
}

type schemaHarness struct {
	rec  *sqlRecorder
	db   *stdsql.DB
	main *integration.Client // plain SQLite client on the main database
}

// newSchemaHarness creates main.db, alt.db and joins.db with the full schema
// and returns a recorder speaking the Postgres dialect over one SQLite
// connection with alt and joins attached.
func newSchemaHarness(t *testing.T) *schemaHarness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	for _, name := range []string{"main", "alt", "joins"} {
		c, err := integration.Open(dialect.SQLite, "file:"+filepath.Join(dir, name+".db")+"?_pragma=foreign_keys(1)")
		require.NoError(t, err)
		require.NoError(t, c.Schema.Create(ctx))
		require.NoError(t, c.Close())
	}
	// Foreign keys stay off on this connection: SQLite resolves a foreign
	// key within the child table's own database, so joins.post_tags cannot
	// reference alt.posts the way a Postgres schema can.
	db, err := stdsql.Open("sqlite", "file:"+filepath.Join(dir, "main.db"))
	require.NoError(t, err)
	// ATTACH is per connection: pin the pool to the one connection.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, name := range []string{"alt", "joins"} {
		_, err := db.ExecContext(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS %s", filepath.Join(dir, name+".db"), name))
		require.NoError(t, err)
	}
	rec := &sqlRecorder{Driver: sql.OpenDB(dialect.Postgres, db)}
	mainClient := integration.NewClient(integration.Driver(sql.OpenDB(dialect.SQLite, db)))
	return &schemaHarness{rec: rec, db: db, main: mainClient}
}

// count returns the number of rows in schema.table ("" for main).
func (h *schemaHarness) count(t *testing.T, schema, table string) int {
	t.Helper()
	name := table
	if schema != "" {
		name = schema + "." + table
	}
	var n int
	require.NoError(t, h.db.QueryRow("SELECT COUNT(*) FROM "+name).Scan(&n))
	return n
}

// requireAllQualified fails unless every recorded statement names a table,
// and every table reference in it is schema-qualified.
func requireAllQualified(t *testing.T, stmts []string, tables ...string) {
	t.Helper()
	require.NotEmpty(t, stmts)
	for _, s := range stmts {
		for _, tbl := range tables {
			q := `"` + tbl + `"`
			for i := strings.Index(s, q); i >= 0; {
				// A column reference ("users"."id") is not a table reference.
				after := s[i+len(q):]
				if !strings.HasPrefix(after, ".") {
					before := s[:i]
					require.True(t, strings.HasSuffix(before, `"alt".`) || strings.HasSuffix(before, `"joins".`),
						"unqualified table %s in: %s", q, s)
				}
				next := strings.Index(after, q)
				if next < 0 {
					break
				}
				i += len(q) + next
			}
		}
	}
}

func joined(stmts []string) string { return strings.Join(stmts, "\n") }

func TestAlternateSchema_Postgres(t *testing.T) {
	ctx := context.Background()
	h := newSchemaHarness(t)
	client := integration.NewClient(integration.Driver(h.rec), integration.AlternateSchema(altSchemas))
	tables := []string{"users", "posts", "tags", "post_tags", "comments", "pets", "likes", "tokens"}

	// --- create ---
	u, err := client.User.Create().SetName("alice").SetEmail("a@x.io").SetAge(30).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err)
	stmts := h.rec.take()
	require.Contains(t, joined(stmts), `INSERT INTO "alt"."users"`)
	requireAllQualified(t, stmts, tables...)

	// --- bulk create ---
	tags, err := client.Tag.CreateBulk(
		client.Tag.Create().SetName("go"),
		client.Tag.Create().SetName("sql"),
	).Save(ctx)
	require.NoError(t, err)
	require.Len(t, tags, 2)
	stmts = h.rec.take()
	require.Contains(t, joined(stmts), `INSERT INTO "alt"."tags"`)
	requireAllQualified(t, stmts, tables...)

	// --- create with an M2O edge and an M2M edge: the join table uses PostTags ---
	p, err := client.Post.Create().SetTitle("t").SetContent("c").SetStatus(post.StatusPublished).
		SetViewCount(0).SetAuthorID(u.ID).SetCreatedAt(now).SetUpdatedAt(now).
		AddTagIDs(tags[0].ID).Save(ctx)
	require.NoError(t, err)
	stmts = h.rec.take()
	require.Contains(t, joined(stmts), `INSERT INTO "alt"."posts"`)
	require.Contains(t, joined(stmts), `INSERT INTO "joins"."post_tags"`)
	requireAllQualified(t, stmts, tables...)

	// --- update (bulk) with an edge predicate ---
	n, err := client.User.Update().Where(user.HasPosts()).SetAge(31).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	stmts = h.rec.take()
	require.Contains(t, joined(stmts), `UPDATE "alt"."users"`)
	require.Contains(t, joined(stmts), `FROM "alt"."posts"`, "HasPosts subquery")
	requireAllQualified(t, stmts, tables...)

	// --- updateOne: SET, M2M add/remove, read-back ---
	p, err = client.Post.UpdateOneID(p.ID).SetTitle("t2").
		RemoveTagIDs(tags[0].ID).AddTagIDs(tags[1].ID).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "t2", p.Title)
	stmts = h.rec.take()
	require.Contains(t, joined(stmts), `UPDATE "alt"."posts"`)
	require.Contains(t, joined(stmts), `DELETE FROM "joins"."post_tags"`)
	require.Contains(t, joined(stmts), `INSERT INTO "joins"."post_tags"`)
	require.Contains(t, joined(stmts), `FROM "alt"."posts"`, "read-back")
	requireAllQualified(t, stmts, tables...)

	// --- query terminals: All, Count, Exist, IDs, Select, GroupBy ---
	users, err := client.User.Query().Where(user.HasPostsWith(post.TitleField.EQ("t2"))).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	cnt, err := client.User.Query().Where(user.HasPosts()).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, cnt)
	ok, err := client.Post.Query().Where(post.HasTags()).Exist(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	ids, err := client.Tag.Query().Where(tag.HasPosts()).IDs(ctx)
	require.NoError(t, err)
	require.Equal(t, []int{tags[1].ID}, ids)
	names, err := client.User.Query().Where(user.HasPosts()).Select(user.FieldName).Strings(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"alice"}, names)
	var groups []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	require.NoError(t, client.User.Query().Where(user.HasPosts()).GroupBy(user.FieldName).
		Aggregate(integration.Count()).Scan(ctx, &groups))
	require.Len(t, groups, 1)
	stmts = h.rec.take()
	require.Contains(t, joined(stmts), `FROM "alt"."users"`)
	require.Contains(t, joined(stmts), `FROM "joins"."post_tags"`, "HasTags / Tag.HasPosts subquery")
	requireAllQualified(t, stmts, tables...)

	// --- edge traversal: query level and entity level ---
	posts, err := client.User.Query().Where(user.IDField.EQ(u.ID)).(*query.UserQuery).QueryPosts().All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 1)
	tagged, err := posts[0].QueryTags().All(ctx)
	require.NoError(t, err)
	require.Len(t, tagged, 1)
	require.Equal(t, tags[1].ID, tagged[0].ID)
	stmts = h.rec.take()
	require.Contains(t, joined(stmts), `"alt"."posts"`)
	require.Contains(t, joined(stmts), `"joins"."post_tags"`)
	requireAllQualified(t, stmts, tables...)

	// --- eager loading: O2M, M2O, M2M and M2M inverse ---
	eager, err := client.User.Query().WithPosts(func(q entity.PostQuerier) {
		q.WithTags()
	}).All(ctx)
	require.NoError(t, err)
	require.Len(t, eager, 1)
	require.Len(t, eager[0].Edges.Posts, 1)
	require.Len(t, eager[0].Edges.Posts[0].Edges.Tags, 1)
	tagsWithPosts, err := client.Tag.Query().WithPosts().All(ctx)
	require.NoError(t, err)
	require.Len(t, tagsWithPosts, 2)
	postsWithAuthor, err := client.Post.Query().WithAuthor().All(ctx)
	require.NoError(t, err)
	require.Len(t, postsWithAuthor, 1)
	require.NotNil(t, postsWithAuthor[0].Edges.Author)
	stmts = h.rec.take()
	require.Contains(t, joined(stmts), `JOIN "joins"."post_tags"`)
	requireAllQualified(t, stmts, tables...)

	// --- rows landed in the configured databases, none in main ---
	require.Equal(t, 1, h.count(t, "alt", "users"))
	require.Equal(t, 2, h.count(t, "alt", "tags"))
	require.Equal(t, 1, h.count(t, "alt", "posts"))
	require.Equal(t, 1, h.count(t, "joins", "post_tags"))
	require.Equal(t, 0, h.count(t, "alt", "post_tags"))
	for _, tbl := range tables {
		require.Zero(t, h.count(t, "", tbl), "main.%s must stay empty", tbl)
	}

	// --- delete with an edge predicate ---
	_, err = client.Post.Delete().Where(post.HasTagsWith(tag.IDField.EQ(tags[1].ID))).Exec(ctx)
	require.NoError(t, err)
	stmts = h.rec.take()
	require.Contains(t, joined(stmts), `DELETE FROM "alt"."posts"`)
	requireAllQualified(t, stmts, tables...)
	require.Equal(t, 0, h.count(t, "alt", "posts"))

	// --- transactions carry the schema config too ---
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	_, err = tx.Tag.Create().SetName("tx").Save(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	stmts = h.rec.take()
	require.Contains(t, joined(stmts), `INSERT INTO "alt"."tags"`)
	require.Equal(t, 3, h.count(t, "alt", "tags"))
	require.Zero(t, h.count(t, "", "tags"))
}

// A client without AlternateSchema renders unqualified tables and writes to
// the default (main) database.
func TestAlternateSchema_UnsetRendersUnqualified(t *testing.T) {
	ctx := context.Background()
	h := newSchemaHarness(t)
	client := integration.NewClient(integration.Driver(h.rec))

	u, err := client.User.Create().SetName("bob").SetEmail("b@x.io").SetAge(30).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err)
	tg, err := client.Tag.Create().SetName("go").Save(ctx)
	require.NoError(t, err)
	_, err = client.Post.Create().SetTitle("t").SetContent("c").SetStatus(post.StatusPublished).
		SetViewCount(0).SetAuthorID(u.ID).SetCreatedAt(now).SetUpdatedAt(now).
		AddTagIDs(tg.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.User.Query().Where(user.HasPosts()).WithPosts(func(q entity.PostQuerier) { q.WithTags() }).All(ctx)
	require.NoError(t, err)

	all := joined(h.rec.take())
	require.Contains(t, all, `INSERT INTO "users"`)
	require.Contains(t, all, `INSERT INTO "post_tags"`)
	require.Contains(t, all, `FROM "users"`)
	require.NotContains(t, all, `"alt".`)
	require.NotContains(t, all, `"joins".`)
	require.Equal(t, 1, h.count(t, "", "users"))
	require.Equal(t, 1, h.count(t, "", "post_tags"))
	require.Zero(t, h.count(t, "alt", "users"))
}

// On SQLite velox's builder drops schema qualifiers, so a client with
// AlternateSchema still works against a plain SQLite database.
func TestAlternateSchema_SQLiteIgnoresQualifier(t *testing.T) {
	ctx := context.Background()
	drv, err := sql.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { drv.Close() })
	rec := &sqlRecorder{Driver: drv}
	client := integration.NewClient(integration.Driver(rec), integration.AlternateSchema(altSchemas))
	require.NoError(t, client.Schema.Create(ctx))

	u, err := client.User.Create().SetName("carol").SetEmail("c@x.io").SetAge(30).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err)
	tg, err := client.Tag.Create().SetName("go").Save(ctx)
	require.NoError(t, err)
	_, err = client.Post.Create().SetTitle("t").SetContent("c").SetStatus(post.StatusPublished).
		SetViewCount(0).SetAuthorID(u.ID).SetCreatedAt(now).SetUpdatedAt(now).
		AddTagIDs(tg.ID).Save(ctx)
	require.NoError(t, err)
	users, err := client.User.Query().Where(user.HasPostsWith(post.HasTags())).
		WithPosts(func(q entity.PostQuerier) { q.WithTags() }).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Len(t, users[0].Edges.Posts, 1)
	require.Len(t, users[0].Edges.Posts[0].Edges.Tags, 1)

	require.NotContains(t, joined(rec.take()), `"alt".`)
}
