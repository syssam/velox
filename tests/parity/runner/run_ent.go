// run_ent.go is one of the only two files in the runner package permitted to
// import the Ent ORM — the architecture guard
// (architecture_test.go::TestBrainHasNoORMImports) excludes files prefixed
// "run_velox" / "run_ent". It mirrors run_velox.go against the Ent client API:
// the in-memory client harness, the op→ent-call executor, and the normalizer.

package runner

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"velox.test/parity/ent"
	"velox.test/parity/ent/author"
	"velox.test/parity/ent/post"
	"velox.test/parity/ent/tag"
	"velox.test/parity/model"
	"velox.test/parity/op"
)

// entDialect is the dialect string Ent uses for SQL generation. Ent's
// dialect.SQLite is "sqlite3"; we open the underlying database/sql handle with
// the pure-Go modernc "sqlite" driver and tell Ent to generate "sqlite3"
// dialect SQL, so we get modernc execution with correct Ent SQL.
const entDialect = "sqlite3"

// NewEntSQLite opens a fresh, migrated, in-memory Ent client and registers its
// close on t.Cleanup. The database/sql handle uses the modernc "sqlite" driver
// (pure Go, no CGO); Ent is told to emit "sqlite3"-dialect SQL.
func NewEntSQLite(t testing.TB) *ent.Client {
	t.Helper()
	db, err := sql.Open(sqliteDriverName, sqliteMemoryDSN())
	require.NoError(t, err)
	drv := entsql.OpenDB(entDialect, db)
	c := ent.NewClient(ent.Driver(drv))
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Schema.Create(context.Background()))
	return c
}

// newEntSQLiteTraced opens a fresh Ent client whose SQL is captured into sink
// via the debug driver's log hook.
func newEntSQLiteTraced(t testing.TB, sink *sqlSink) *ent.Client {
	t.Helper()
	db, err := sql.Open(sqliteDriverName, sqliteMemoryDSN())
	require.NoError(t, err)
	drv := entsql.OpenDB(entDialect, db)
	c := ent.NewClient(ent.Driver(drv), ent.Log(sink.record), ent.Debug())
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Schema.Create(context.Background()))
	return c
}

// runEntTraced opens a traced Ent client, runs prog while tagging each captured
// statement with its op index, and returns the normalized results. ORM-free
// entry point for the driver.
func runEntTraced(t testing.TB, sink *sqlSink, prog op.Program) []model.Result {
	t.Helper()
	c := newEntSQLiteTraced(t, sink)
	x := &entExec{c: c, reg: newHandleRegistry(), sink: sink}
	results := make([]model.Result, len(prog))
	for i, o := range prog {
		sink.setOp(i)
		results[i] = x.step(context.Background(), i, o)
	}
	return results
}

// RunEnt executes prog against an Ent client and normalizes every op's outcome
// into a model.Result, one per op index. Same handle↔db-id registry, parity
// clock, and normalization as RunVelox.
func RunEnt(ctx context.Context, c *ent.Client, prog op.Program) ([]model.Result, error) {
	x := &entExec{c: c, reg: newHandleRegistry()}
	results := make([]model.Result, len(prog))
	for i, o := range prog {
		results[i] = x.step(ctx, i, o)
	}
	return results, nil
}

type entExec struct {
	c    *ent.Client
	reg  *handleRegistry
	sink *sqlSink // optional SQL-trace sink (nil when untraced)
}

func (x *entExec) step(ctx context.Context, idx int, o op.Op) model.Result {
	switch v := o.(type) {
	case op.CreateAuthor:
		return x.createAuthor(ctx, idx, v)
	case op.CreatePost:
		return x.createPost(ctx, idx, v)
	case op.CreateComment:
		return x.createComment(ctx, idx, v)
	case op.CreateTag:
		return x.createTag(ctx, idx, v)
	case op.UpsertTag:
		return x.upsertTag(ctx, v)
	case op.BulkCreateTags:
		return x.bulkCreateTags(ctx, v)
	case op.SumTagUsage:
		return x.sumTagUsage(ctx)
	case op.AddTagToPost:
		return x.addTagToPost(ctx, v)
	case op.RemoveTagFromPost:
		return x.removeTagFromPost(ctx, v)
	case op.CountPostTags:
		return x.countPostTags(ctx, v)
	case op.SetPostLabels:
		return x.setPostLabels(ctx, idx, v)
	case op.AppendPostLabels:
		return x.appendPostLabels(ctx, idx, v)
	case op.UpdatePostViewCount:
		return x.updateViewCount(ctx, idx, v)
	case op.SetAuthorBio:
		return x.setAuthorBio(ctx, idx, v)
	case op.CountAuthorsWithBio:
		return x.countAuthorsWithBio(ctx)
	case op.BulkAddViewCountByStatus:
		return x.bulkAddViewCountByStatus(ctx, idx, v)
	case op.DeletePost:
		return x.deletePost(ctx, v)
	case op.BulkDeletePostsByStatus:
		return x.bulkDeletePostsByStatus(ctx, v)
	case op.QueryPostsByStatus:
		return x.queryPostsByStatus(ctx, v)
	case op.CountPosts:
		return x.countPosts(ctx)
	case op.SumViewCount:
		return x.sumViewCount(ctx)
	case op.LoadAuthorPosts:
		return x.loadAuthorPosts(ctx, v)
	case op.PaginatePosts:
		return x.paginatePosts(ctx, v)
	default:
		panic("run_ent: unhandled op type")
	}
}

// --- Create ops -----------------------------------------------------------

func (x *entExec) createAuthor(ctx context.Context, idx int, v op.CreateAuthor) model.Result {
	b := x.c.Author.Create().
		SetName(v.Name).
		SetAge(v.Age).
		SetRole(author.Role(v.Role)).
		SetCreatedAt(parityClock(idx)).
		SetUpdatedAt(parityClock(idx))
	if v.Bio != nil {
		b.SetBio(*v.Bio)
	}
	if len(v.Labels) > 0 {
		b.SetLabels(v.Labels)
	}
	a, err := b.Save(ctx)
	if err != nil {
		return entErrResult(err)
	}
	x.reg.record(kindAuthor, idx, a.ID)
	return model.Result{Err: model.ErrOK}
}

func (x *entExec) createPost(ctx context.Context, idx int, v op.CreatePost) model.Result {
	authorID, ok := x.reg.handleToID[v.AuthorRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	b := x.c.Post.Create().
		SetTitle(v.Title).
		SetStatus(post.Status(v.Status)).
		SetViewCount(v.ViewCount).
		SetAuthorID(authorID).
		SetCreatedAt(parityClock(idx)).
		SetUpdatedAt(parityClock(idx))
	if len(v.Labels) > 0 {
		b.SetLabels(v.Labels)
	}
	p, err := b.Save(ctx)
	if err != nil {
		return entErrResult(err)
	}
	x.reg.record(kindPost, idx, p.ID)
	return model.Result{Err: model.ErrOK}
}

func (x *entExec) createComment(ctx context.Context, idx int, v op.CreateComment) model.Result {
	postID, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	authorID, ok := x.reg.handleToID[v.AuthorRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	b := x.c.Comment.Create().
		SetContent(v.Content).
		SetPostID(postID).
		SetAuthorID(authorID).
		SetCreatedAt(parityClock(idx)).
		SetUpdatedAt(parityClock(idx))
	if len(v.Labels) > 0 {
		b.SetLabels(v.Labels)
	}
	cm, err := b.Save(ctx)
	if err != nil {
		return entErrResult(err)
	}
	x.reg.record(kindComment, idx, cm.ID)
	return model.Result{Err: model.ErrOK}
}

func (x *entExec) createTag(ctx context.Context, idx int, v op.CreateTag) model.Result {
	tg, err := x.c.Tag.Create().SetName(v.Name).Save(ctx)
	if err != nil {
		return entErrResult(err)
	}
	x.reg.record(kindTag, idx, tg.ID)
	return model.Result{Err: model.ErrOK}
}

// upsertTag mirrors veloxExec.upsertTag against the ent client: insert-by-unique-
// name or add AddUsage onto the existing usage_count, then read it back so the
// comparator checks ent's DO UPDATE SET result against velox and the reference.
func (x *entExec) upsertTag(ctx context.Context, v op.UpsertTag) model.Result {
	if _, err := x.c.Tag.Create().
		SetName(v.Name).
		SetUsageCount(v.AddUsage).
		OnConflictColumns(tag.FieldName).
		AddUsageCount(v.AddUsage).
		ID(ctx); err != nil {
		return entErrResult(err)
	}
	tg, err := x.c.Tag.Query().Where(tag.NameEQ(v.Name)).Only(ctx)
	if err != nil {
		return entErrResult(err)
	}
	n := tg.UsageCount
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

// bulkCreateTags mirrors veloxExec.bulkCreateTags against the ent client.
func (x *entExec) bulkCreateTags(ctx context.Context, v op.BulkCreateTags) model.Result {
	builders := make([]*ent.TagCreate, len(v.Specs))
	for i, spec := range v.Specs {
		builders[i] = x.c.Tag.Create().SetName(spec.Name).SetUsageCount(spec.UsageCount)
	}
	if _, err := x.c.Tag.CreateBulk(builders...).Save(ctx); err != nil {
		return entErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// sumTagUsage returns SUM(usage_count) over the tags table, NULL-defaulted to 0.
func (x *entExec) sumTagUsage(ctx context.Context) model.Result {
	var rows []struct {
		Sum *int `json:"sum"`
	}
	if err := x.c.Tag.Query().Aggregate(ent.Sum(tag.FieldUsageCount)).Scan(ctx, &rows); err != nil {
		return entErrResult(err)
	}
	sum := 0
	if len(rows) > 0 && rows[0].Sum != nil {
		sum = *rows[0].Sum
	}
	return model.Result{Scalar: &sum, Err: model.ErrOK}
}

// --- Edge / mutation ops --------------------------------------------------

func (x *entExec) addTagToPost(ctx context.Context, v op.AddTagToPost) model.Result {
	postID, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	tagID, ok := x.reg.handleToID[v.TagRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	if err := x.c.Post.UpdateOneID(postID).AddTagIDs(tagID).Exec(ctx); err != nil {
		return entErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// removeTagFromPost mirrors veloxExec.removeTagFromPost against the ent client.
func (x *entExec) removeTagFromPost(ctx context.Context, v op.RemoveTagFromPost) model.Result {
	postID, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	tagID, ok := x.reg.handleToID[v.TagRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	if err := x.c.Post.UpdateOneID(postID).RemoveTagIDs(tagID).Exec(ctx); err != nil {
		return entErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// countPostTags returns the post's M2M edge degree by traversing post -> tags.
func (x *entExec) countPostTags(ctx context.Context, v op.CountPostTags) model.Result {
	postID, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	p, err := x.c.Post.Get(ctx, postID)
	if err != nil {
		return entErrResult(err)
	}
	n, err := x.c.Post.QueryTags(p).Count(ctx)
	if err != nil {
		return entErrResult(err)
	}
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

func (x *entExec) setPostLabels(ctx context.Context, idx int, v op.SetPostLabels) model.Result {
	id, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	err := x.c.Post.UpdateOneID(id).
		SetLabels(v.Labels).
		SetUpdatedAt(parityClock(idx)).
		Exec(ctx)
	if err != nil {
		return entErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

func (x *entExec) appendPostLabels(ctx context.Context, idx int, v op.AppendPostLabels) model.Result {
	id, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	err := x.c.Post.UpdateOneID(id).
		AppendLabels(v.Labels).
		SetUpdatedAt(parityClock(idx)).
		Exec(ctx)
	if err != nil {
		return entErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

func (x *entExec) updateViewCount(ctx context.Context, idx int, v op.UpdatePostViewCount) model.Result {
	id, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	err := x.c.Post.UpdateOneID(id).
		SetViewCount(v.ViewCount).
		SetUpdatedAt(parityClock(idx)).
		Exec(ctx)
	if err != nil {
		return entErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// setAuthorBio mirrors veloxExec.setAuthorBio against the ent client, pinning
// updated_at to the deterministic parity clock (see the velox doc comment).
func (x *entExec) setAuthorBio(ctx context.Context, idx int, v op.SetAuthorBio) model.Result {
	id, ok := x.reg.handleToID[v.AuthorRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	u := x.c.Author.UpdateOneID(id).SetUpdatedAt(parityClock(idx))
	if v.Bio != nil {
		u.SetBio(*v.Bio)
	} else {
		u.ClearBio()
	}
	if err := u.Exec(ctx); err != nil {
		return entErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// countAuthorsWithBio counts authors whose bio IS NOT NULL.
func (x *entExec) countAuthorsWithBio(ctx context.Context) model.Result {
	n, err := x.c.Author.Query().Where(author.BioNotNil()).Count(ctx)
	if err != nil {
		return entErrResult(err)
	}
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

func (x *entExec) deletePost(ctx context.Context, v op.DeletePost) model.Result {
	id, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	if err := x.c.Post.DeleteOneID(id).Exec(ctx); err != nil {
		return entErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// bulkAddViewCountByStatus mirrors veloxExec.bulkAddViewCountByStatus.
func (x *entExec) bulkAddViewCountByStatus(ctx context.Context, idx int, v op.BulkAddViewCountByStatus) model.Result {
	n, err := x.c.Post.Update().
		Where(post.StatusEQ(post.Status(v.Status))).
		AddViewCount(v.Delta).
		SetUpdatedAt(parityClock(idx)).
		Save(ctx)
	if err != nil {
		return entErrResult(err)
	}
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

// bulkDeletePostsByStatus mirrors veloxExec.bulkDeletePostsByStatus.
func (x *entExec) bulkDeletePostsByStatus(ctx context.Context, v op.BulkDeletePostsByStatus) model.Result {
	n, err := x.c.Post.Delete().
		Where(post.StatusEQ(post.Status(v.Status))).
		Exec(ctx)
	if err != nil {
		return entErrResult(err)
	}
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

// --- Query / read ops -----------------------------------------------------

func (x *entExec) queryPostsByStatus(ctx context.Context, v op.QueryPostsByStatus) model.Result {
	posts, err := x.c.Post.Query().
		Where(post.StatusEQ(post.Status(v.Status))).
		Order(post.ByCreatedAt()).
		WithAuthor().
		All(ctx)
	if err != nil {
		return entErrResult(err)
	}
	return model.Result{Rows: x.postRows(posts), Err: model.ErrOK}
}

func (x *entExec) countPosts(ctx context.Context) model.Result {
	n, err := x.c.Post.Query().Count(ctx)
	if err != nil {
		return entErrResult(err)
	}
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

func (x *entExec) sumViewCount(ctx context.Context) model.Result {
	var rows []struct {
		Sum *int `json:"sum"`
	}
	err := x.c.Post.Query().
		Aggregate(ent.Sum(post.FieldViewCount)).
		Scan(ctx, &rows)
	if err != nil {
		return entErrResult(err)
	}
	sum := 0
	if len(rows) > 0 && rows[0].Sum != nil {
		sum = *rows[0].Sum
	}
	return model.Result{Scalar: &sum, Err: model.ErrOK}
}

func (x *entExec) loadAuthorPosts(ctx context.Context, v op.LoadAuthorPosts) model.Result {
	id, ok := x.reg.handleToID[v.AuthorRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	posts, err := x.c.Post.Query().
		Where(post.HasAuthorWith(author.IDEQ(id))).
		Order(post.ByCreatedAt()).
		WithAuthor().
		All(ctx)
	if err != nil {
		return entErrResult(err)
	}
	return model.Result{Rows: x.postRows(posts), Err: model.ErrOK}
}

// --- normalization --------------------------------------------------------

func (x *entExec) postRows(posts []*ent.Post) []model.Row {
	if len(posts) == 0 {
		return nil
	}
	rows := make([]model.Row, len(posts))
	for i, p := range posts {
		rows[i] = x.postRow(p)
	}
	return rows
}

func (x *entExec) postRow(p *ent.Post) model.Row {
	authorHandle := -1
	if a := p.Edges.Author; a != nil {
		authorHandle = x.reg.handleForID(kindAuthor, a.ID)
	}
	return model.Row{
		"id":         model.Ref{Handle: x.reg.handleForID(kindPost, p.ID)},
		"title":      model.Value(p.Title),
		"status":     model.Value(string(p.Status)),
		"view_count": model.Value(p.ViewCount),
		"labels":     model.Value(normalizeLabels(p.Labels)),
		"created_at": model.Value(opIndexFromClock(p.CreatedAt)),
		"updated_at": model.Value(opIndexFromClock(p.UpdatedAt)),
		"author":     model.Ref{Handle: authorHandle},
	}
}

// entErrResult maps an Ent error into a normalized error Result.
func entErrResult(err error) model.Result {
	return model.Result{Err: classifyEntErr(err)}
}

// classifyEntErr maps an Ent runtime error to the canonical ErrCat. Only the
// KNOWN error shapes are mapped to a specific category: typed not-found
// (*ent.NotFoundError) and pagination validation. Ent's pagination validation
// (first+last, negative first/last) surfaces as a *gqlerror.Error returned by
// validateFirstLast in ent/gql_pagination.go — the first+last case carries no
// errcode, so the typed error is the reliable discriminator. Everything else is
// ErrInternal: an unexpected/internal failure must NOT be relabeled
// ErrValidation, or a genuine crash on a validation-expected op would falsely
// Pass.
func classifyEntErr(err error) model.ErrCat {
	var gqlErr *gqlerror.Error
	switch {
	case err == nil:
		return model.ErrOK
	case ent.IsNotFound(err):
		return model.ErrNotFound
	case errors.As(err, &gqlErr):
		return model.ErrValidation
	default:
		return model.ErrInternal
	}
}

// gqlEntOrderName maps a snake_case column name to the generated GraphQL order
// enum name (e.g. "view_count" -> "VIEW_COUNT").
func gqlEntOrderName(field string) string {
	return strings.ToUpper(field)
}
