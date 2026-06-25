// run_velox.go is one of the only two files in the runner package permitted to
// import the velox ORM — the architecture guard
// (architecture_test.go::TestBrainHasNoORMImports) excludes files prefixed
// "run_velox" / "run_ent". Everything ORM-specific to velox lives here: the
// in-memory client harness, the op→velox-call executor, and the result
// normalizer that maps velox entities back into ORM-neutral model.Result.

package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/dialect"

	"velox.test/parity/model"
	"velox.test/parity/op"
	velox "velox.test/parity/velox"
	"velox.test/parity/velox/author"
	tagclient "velox.test/parity/velox/client/tag"
	"velox.test/parity/velox/entity"
	"velox.test/parity/velox/post"
	"velox.test/parity/velox/tag"
)

// NewVeloxSQLite opens a fresh, migrated, in-memory velox client and registers
// its close on t.Cleanup. Each call gets a private :memory: database, so
// programs are isolated.
func NewVeloxSQLite(t testing.TB) *velox.Client {
	t.Helper()
	c, err := velox.Open(dialect.SQLite, sqliteMemoryDSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Schema.Create(context.Background()))
	return c
}

// newVeloxSQLiteTraced opens a fresh velox client whose SQL is captured into
// sink via the debug driver's log hook. Used by the three-way driver to record
// the SQL each op emits.
func newVeloxSQLiteTraced(t testing.TB, sink *sqlSink) *velox.Client {
	t.Helper()
	c, err := velox.Open(dialect.SQLite, sqliteMemoryDSN(), velox.Log(sink.record), velox.Debug())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Schema.Create(context.Background()))
	return c
}

// runVeloxTraced opens a traced velox client, runs prog while tagging each
// captured statement with its op index, and returns the normalized results.
// This is the ORM-free entry point the driver calls.
func runVeloxTraced(t testing.TB, sink *sqlSink, prog op.Program) []model.Result {
	t.Helper()
	c := newVeloxSQLiteTraced(t, sink)
	x := &veloxExec{c: c, reg: newHandleRegistry(), sink: sink}
	results := make([]model.Result, len(prog))
	for i, o := range prog {
		sink.setOp(i)
		results[i] = x.step(context.Background(), i, o)
	}
	return results
}

// RunVelox executes prog against a velox client and normalizes every op's
// outcome into a model.Result, one per op index. It maintains the
// handle↔db-id registry and injects the deterministic parity clock so that
// created_at order == insertion order across all three executors.
func RunVelox(ctx context.Context, c *velox.Client, prog op.Program) ([]model.Result, error) {
	x := &veloxExec{c: c, reg: newHandleRegistry()}
	results := make([]model.Result, len(prog))
	for i, o := range prog {
		results[i] = x.step(ctx, i, o)
	}
	return results, nil
}

// veloxExec carries the per-program executor state.
type veloxExec struct {
	c    *velox.Client
	reg  *handleRegistry
	sink *sqlSink // optional SQL-trace sink (nil when untraced)
}

// step applies one op and returns its normalized Result.
func (x *veloxExec) step(ctx context.Context, idx int, o op.Op) model.Result {
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
		panic("run_velox: unhandled op type")
	}
}

// --- Create ops -----------------------------------------------------------

func (x *veloxExec) createAuthor(ctx context.Context, idx int, v op.CreateAuthor) model.Result {
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
		return veloxErrResult(err)
	}
	x.reg.record(kindAuthor, idx, a.ID)
	return model.Result{Err: model.ErrOK}
}

func (x *veloxExec) createPost(ctx context.Context, idx int, v op.CreatePost) model.Result {
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
		return veloxErrResult(err)
	}
	x.reg.record(kindPost, idx, p.ID)
	return model.Result{Err: model.ErrOK}
}

func (x *veloxExec) createComment(ctx context.Context, idx int, v op.CreateComment) model.Result {
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
		return veloxErrResult(err)
	}
	x.reg.record(kindComment, idx, cm.ID)
	return model.Result{Err: model.ErrOK}
}

func (x *veloxExec) createTag(ctx context.Context, idx int, v op.CreateTag) model.Result {
	tg, err := x.c.Tag.Create().SetName(v.Name).Save(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	x.reg.record(kindTag, idx, tg.ID)
	return model.Result{Err: model.ErrOK}
}

// upsertTag inserts a tag by unique name, or adds AddUsage onto the existing
// tag's usage_count via the per-column AddXxx upsert path (the eager-bind
// conflict resolver). It reads usage_count back so the harness compares the
// value DO UPDATE SET actually produced against the reference and ent.
func (x *veloxExec) upsertTag(ctx context.Context, v op.UpsertTag) model.Result {
	if _, err := x.c.Tag.Create().
		SetName(v.Name).
		SetUsageCount(v.AddUsage).
		OnConflictColumns(tag.FieldName).
		AddUsageCount(v.AddUsage).
		ID(ctx); err != nil {
		return veloxErrResult(err)
	}
	tg, err := x.c.Tag.Query().Where(tag.NameField.EQ(v.Name)).Only(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	n := tg.UsageCount
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

// bulkCreateTags inserts every spec in a single batch (CreateBulk mutator
// chain). Rows are observed in aggregate via sumTagUsage, so no handles are
// registered.
func (x *veloxExec) bulkCreateTags(ctx context.Context, v op.BulkCreateTags) model.Result {
	builders := make([]*tagclient.TagCreate, len(v.Specs))
	for i, spec := range v.Specs {
		builders[i] = x.c.Tag.Create().SetName(spec.Name).SetUsageCount(spec.UsageCount)
	}
	if _, err := x.c.Tag.CreateBulk(builders...).Save(ctx); err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// sumTagUsage returns SUM(usage_count) over the tags table. SUM over no rows is
// SQL NULL; scan into *int and default to 0 to match the reference.
func (x *veloxExec) sumTagUsage(ctx context.Context) model.Result {
	var rows []struct {
		Sum *int `json:"sum"`
	}
	if err := x.c.Tag.Query().Aggregate(velox.Sum(tag.FieldUsageCount)).Scan(ctx, &rows); err != nil {
		return veloxErrResult(err)
	}
	sum := 0
	if len(rows) > 0 && rows[0].Sum != nil {
		sum = *rows[0].Sum
	}
	return model.Result{Scalar: &sum, Err: model.ErrOK}
}

// --- Edge / mutation ops --------------------------------------------------

func (x *veloxExec) addTagToPost(ctx context.Context, v op.AddTagToPost) model.Result {
	postID, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	tagID, ok := x.reg.handleToID[v.TagRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	if err := x.c.Post.UpdateOneID(postID).AddTagIDs(tagID).Exec(ctx); err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// removeTagFromPost detaches a tag from a post (M2M edge removal). Detaching a
// tag that is not attached is a no-op in the ORM (deletes 0 join rows).
func (x *veloxExec) removeTagFromPost(ctx context.Context, v op.RemoveTagFromPost) model.Result {
	postID, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	tagID, ok := x.reg.handleToID[v.TagRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	if err := x.c.Post.UpdateOneID(postID).RemoveTagIDs(tagID).Exec(ctx); err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// countPostTags returns the post's M2M edge degree by traversing post -> tags.
func (x *veloxExec) countPostTags(ctx context.Context, v op.CountPostTags) model.Result {
	postID, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	p, err := x.c.Post.Get(ctx, postID)
	if err != nil {
		return veloxErrResult(err)
	}
	n, err := x.c.Post.QueryTags(p).Count(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

func (x *veloxExec) setPostLabels(ctx context.Context, idx int, v op.SetPostLabels) model.Result {
	id, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	err := x.c.Post.UpdateOneID(id).
		SetLabels(v.Labels).
		SetUpdatedAt(parityClock(idx)).
		Exec(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

func (x *veloxExec) appendPostLabels(ctx context.Context, idx int, v op.AppendPostLabels) model.Result {
	id, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	err := x.c.Post.UpdateOneID(id).
		AppendLabels(v.Labels).
		SetUpdatedAt(parityClock(idx)).
		Exec(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

func (x *veloxExec) updateViewCount(ctx context.Context, idx int, v op.UpdatePostViewCount) model.Result {
	id, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	err := x.c.Post.UpdateOneID(id).
		SetViewCount(v.ViewCount).
		SetUpdatedAt(parityClock(idx)).
		Exec(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// setAuthorBio sets the author's bio, or — when Bio is nil — clears it to SQL
// NULL via the generated ClearBio update path (bio is Optional().Nillable()).
// updated_at is pinned to the deterministic parity clock: without it, the
// UpdateDefault stamps wall-clock time, and on MySQL (TIMESTAMP precision 0,
// changed-rows RowsAffected) two clears within the same second produce zero
// changed rows, which UpdateOne reports as not-found — a nondeterministic,
// dialect-specific divergence.
func (x *veloxExec) setAuthorBio(ctx context.Context, idx int, v op.SetAuthorBio) model.Result {
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
		return veloxErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// countAuthorsWithBio counts authors whose bio IS NOT NULL.
func (x *veloxExec) countAuthorsWithBio(ctx context.Context) model.Result {
	n, err := x.c.Author.Query().Where(author.BioField.NotNull()).Count(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

func (x *veloxExec) deletePost(ctx context.Context, v op.DeletePost) model.Result {
	id, ok := x.reg.handleToID[v.PostRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	if err := x.c.Post.DeleteOneID(id).Exec(ctx); err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Err: model.ErrOK}
}

// bulkAddViewCountByStatus increments view_count for every post matching status
// in one predicate-scoped UPDATE, returning the affected-row count. updated_at
// is pinned to the deterministic parity clock (overriding the UpdateDefault) so
// a later ordered read still agrees across executors.
func (x *veloxExec) bulkAddViewCountByStatus(ctx context.Context, idx int, v op.BulkAddViewCountByStatus) model.Result {
	n, err := x.c.Post.Update().
		Where(post.StatusField.EQ(post.Status(v.Status))).
		AddViewCount(v.Delta).
		SetUpdatedAt(parityClock(idx)).
		Save(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

// bulkDeletePostsByStatus deletes every post matching status in one
// predicate-scoped DELETE (cascading comments), returning the deleted-row count.
func (x *veloxExec) bulkDeletePostsByStatus(ctx context.Context, v op.BulkDeletePostsByStatus) model.Result {
	n, err := x.c.Post.Delete().
		Where(post.StatusField.EQ(post.Status(v.Status))).
		Exec(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

// --- Query / read ops -----------------------------------------------------

func (x *veloxExec) queryPostsByStatus(ctx context.Context, v op.QueryPostsByStatus) model.Result {
	posts, err := x.c.Post.Query().
		Where(post.StatusField.EQ(post.Status(v.Status))).
		Order(post.ByCreatedAt()).
		WithAuthor().
		All(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Rows: x.postRows(posts), Err: model.ErrOK}
}

func (x *veloxExec) countPosts(ctx context.Context) model.Result {
	n, err := x.c.Post.Query().Count(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Scalar: &n, Err: model.ErrOK}
}

func (x *veloxExec) sumViewCount(ctx context.Context) model.Result {
	// SUM over an empty set is SQL NULL; scan into *int and default to 0 to
	// match the reference (which sums from 0).
	var rows []struct {
		Sum *int `json:"sum"`
	}
	err := x.c.Post.Query().
		Aggregate(velox.Sum(post.FieldViewCount)).
		Scan(ctx, &rows)
	if err != nil {
		return veloxErrResult(err)
	}
	sum := 0
	if len(rows) > 0 && rows[0].Sum != nil {
		sum = *rows[0].Sum
	}
	return model.Result{Scalar: &sum, Err: model.ErrOK}
}

func (x *veloxExec) loadAuthorPosts(ctx context.Context, v op.LoadAuthorPosts) model.Result {
	id, ok := x.reg.handleToID[v.AuthorRef]
	if !ok {
		return model.Result{Err: model.ErrNotFound}
	}
	posts, err := x.c.Post.Query().
		Where(post.HasAuthorWith(author.IDField.EQ(id))).
		Order(post.ByCreatedAt()).
		WithAuthor().
		All(ctx)
	if err != nil {
		return veloxErrResult(err)
	}
	return model.Result{Rows: x.postRows(posts), Err: model.ErrOK}
}

// --- normalization --------------------------------------------------------

// postRows projects velox posts into normalized rows, translating db ids back
// to creation handles.
func (x *veloxExec) postRows(posts []*entity.Post) []model.Row {
	if len(posts) == 0 {
		return nil
	}
	rows := make([]model.Row, len(posts))
	for i, p := range posts {
		rows[i] = x.postRow(p)
	}
	return rows
}

// postRow normalizes a single velox post. The author Ref is taken from the
// eager-loaded author edge (every read loads .WithAuthor()). created_at/
// updated_at are recovered back to their integer op-index clock by inverting
// the parity-epoch injection, so they match the reference's monotone clock
// exactly (the driver additionally strips them before comparison, but matching
// them faithfully keeps the raw executor tests honest).
func (x *veloxExec) postRow(p *entity.Post) model.Row {
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

// parityClock and opIndexFromClock live in db_sqlite.go (ORM-free), shared by
// both executors.

// normalizeLabels matches the reference's empty-vs-nil choice: the reference's
// cloneStrings returns nil for an empty/zero label slice, so executors must too.
func normalizeLabels(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	return labels
}

// paginatePosts is implemented in run_velox_paginate.go (Task 3).

// veloxErrResult maps a velox error into a normalized error Result.
func veloxErrResult(err error) model.Result {
	return model.Result{Err: classifyVeloxErr(err)}
}

// classifyVeloxErr maps a velox runtime error to the canonical ErrCat. Only the
// KNOWN error shapes are mapped to a specific category: typed not-found and the
// pagination-validation sentinel. Everything else is ErrInternal — an
// unexpected/internal failure must NOT be relabeled ErrValidation, or a genuine
// crash on a validation-expected op would falsely Pass.
func classifyVeloxErr(err error) model.ErrCat {
	switch {
	case err == nil:
		return model.ErrOK
	case velox.IsNotFound(err):
		return model.ErrNotFound
	case errors.Is(err, gqlrelay.ErrInvalidPagination):
		return model.ErrValidation
	default:
		return model.ErrInternal
	}
}
