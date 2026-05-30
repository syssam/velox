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
	"velox.test/parity/velox/entity"
	"velox.test/parity/velox/post"
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
	case op.AddTagToPost:
		return x.addTagToPost(ctx, v)
	case op.SetPostLabels:
		return x.setPostLabels(ctx, idx, v)
	case op.AppendPostLabels:
		return x.appendPostLabels(ctx, idx, v)
	case op.UpdatePostViewCount:
		return x.updateViewCount(ctx, idx, v)
	case op.DeletePost:
		return x.deletePost(ctx, v)
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
	b := x.c.Post.Create().
		SetTitle(v.Title).
		SetStatus(post.Status(v.Status)).
		SetViewCount(v.ViewCount).
		SetAuthorID(x.reg.handleToID[v.AuthorRef]).
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
	b := x.c.Comment.Create().
		SetContent(v.Content).
		SetPostID(x.reg.handleToID[v.PostRef]).
		SetAuthorID(x.reg.handleToID[v.AuthorRef]).
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

// classifyVeloxErr maps a velox runtime error to the canonical ErrCat.
func classifyVeloxErr(err error) model.ErrCat {
	switch {
	case err == nil:
		return model.ErrOK
	case velox.IsNotFound(err):
		return model.ErrNotFound
	case errors.Is(err, gqlrelay.ErrInvalidPagination):
		return model.ErrValidation
	default:
		return model.ErrValidation
	}
}
