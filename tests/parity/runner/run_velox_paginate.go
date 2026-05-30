package runner

import (
	"context"
	"strings"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"

	"velox.test/parity/model"
	"velox.test/parity/op"
	"velox.test/parity/velox/entity"
)

// paginatePosts translates an op.PaginatePosts into velox's Relay pagination
// and normalizes the resulting connection into a model.Result {Rows, Page}.
//
// Cursor recovery: an AfterRef/BeforeRef is a creation handle, not an opaque
// cursor token. velox cursors are produced by the order field's ToCursor on a
// loaded *entity.Post, so the robust way to recover the cursor for a target
// handle is to first page the full ordered set (no cursor, a large window) and
// capture each edge's cursor keyed by node→handle. Then look up the cursor for
// the referenced handle and run the real, windowed Paginate. This mirrors what
// a GraphQL client does (it always receives cursors from a prior page), so it
// exercises the exact same cursor encode/decode path velox ships.
func (x *veloxExec) paginatePosts(ctx context.Context, v op.PaginatePosts) model.Result {
	orderOpt, err := veloxOrderOption(v.OrderBy)
	if err != nil {
		return veloxErrResult(err)
	}

	var after, before *gqlrelay.Cursor
	if v.AfterRef != nil || v.BeforeRef != nil {
		cursors, cerr := x.veloxCaptureCursors(ctx, v.OrderBy)
		if cerr != nil {
			return veloxErrResult(cerr)
		}
		if v.AfterRef != nil {
			after = cursors[*v.AfterRef]
		}
		if v.BeforeRef != nil {
			before = cursors[*v.BeforeRef]
		}
	}

	conn, err := x.veloxPaginate(ctx, after, v.First, before, v.Last, orderOpt)
	if err != nil {
		return veloxErrResult(err)
	}
	return x.veloxConnResult(conn)
}

// veloxPaginate runs Paginate on a fresh query, optionally with an order option.
// WithAuthor eager-loads the owner edge so paginated rows carry the author Ref
// exactly like the non-paginated reads (Paginate does not load edges itself).
func (x *veloxExec) veloxPaginate(
	ctx context.Context,
	after *gqlrelay.Cursor, first *int,
	before *gqlrelay.Cursor, last *int,
	orderOpt entity.PostPaginateOption,
) (*entity.PostConnection, error) {
	q := x.c.Post.Query().WithAuthor()
	if orderOpt != nil {
		return q.Paginate(ctx, after, first, before, last, orderOpt)
	}
	return q.Paginate(ctx, after, first, before, last)
}

// captureWindow is the page size used to pull every live post in one capture
// page. velox caps a single page at gqlrelay.MaxPaginationLimit (1000); the
// curated suite stays well under that, so one page captures the full set.
const captureWindow = 1000

// veloxCaptureCursors pages the full ordered set and returns a map from each
// post's creation handle to its velox cursor for that ordering. This recovers
// the real cursor token velox would hand a GraphQL client, so the windowed
// Paginate below decodes exactly what velox encodes.
func (x *veloxExec) veloxCaptureCursors(ctx context.Context, orderBy []op.OrderTerm) (map[int]*gqlrelay.Cursor, error) {
	orderOpt, err := veloxOrderOption(orderBy)
	if err != nil {
		return nil, err
	}
	window := captureWindow
	conn, err := x.veloxPaginate(ctx, nil, &window, nil, nil, orderOpt)
	if err != nil {
		return nil, err
	}
	out := make(map[int]*gqlrelay.Cursor, len(conn.Edges))
	for _, e := range conn.Edges {
		cur := e.Cursor
		out[x.reg.handleForID(kindPost, e.Node.ID)] = &cur
	}
	return out, nil
}

// veloxConnResult normalizes a velox connection into a model.Result, mapping
// edge nodes back to creation handles and PageInfo to model.PageInfo.
func (x *veloxExec) veloxConnResult(conn *entity.PostConnection) model.Result {
	rows := make([]model.Row, len(conn.Edges))
	for i, e := range conn.Edges {
		rows[i] = x.postRow(e.Node)
	}
	page := &model.PageInfo{
		HasNext: conn.PageInfo.HasNextPage,
		HasPrev: conn.PageInfo.HasPreviousPage,
	}
	if len(conn.Edges) > 0 {
		start := x.reg.handleForID(kindPost, conn.Edges[0].Node.ID)
		end := x.reg.handleForID(kindPost, conn.Edges[len(conn.Edges)-1].Node.ID)
		page.StartHandle = &start
		page.EndHandle = &end
	}
	return model.Result{Rows: rows, Page: page, Err: model.ErrOK}
}

// veloxOrderOption builds the velox pagination order option from op order terms.
// Default (empty) order yields a nil option, so velox falls back to its default
// id-ascending order — which matches the reference's handle-ascending default.
func veloxOrderOption(terms []op.OrderTerm) (entity.PostPaginateOption, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	orders := make([]*entity.PostOrder, 0, len(terms))
	for _, t := range terms {
		var field entity.PostOrderField
		if err := field.UnmarshalGQL(gqlOrderName(t.Field)); err != nil {
			return nil, err
		}
		dir := gqlrelay.OrderDirectionAsc
		if t.Desc {
			dir = gqlrelay.OrderDirectionDesc
		}
		f := field
		orders = append(orders, &entity.PostOrder{Direction: dir, Field: &f})
	}
	return entity.WithPostOrder(orders)
}

// gqlOrderName maps a snake_case column name to the generated GraphQL order
// enum name (e.g. "view_count" -> "VIEW_COUNT").
func gqlOrderName(field string) string {
	return strings.ToUpper(field)
}
