package runner

import (
	"context"

	"velox.test/parity/ent"
	"velox.test/parity/model"
	"velox.test/parity/op"
)

// paginatePosts translates an op.PaginatePosts into Ent's Relay pagination and
// normalizes the resulting connection into a model.Result {Rows, Page}. Cursor
// recovery uses the same capture-from-a-full-page approach as the velox
// executor: an AfterRef/BeforeRef is a creation handle, so we first page the
// full ordered set and capture each edge's cursor keyed by node→handle, then
// run the real windowed Paginate with the recovered cursors. This exercises
// Ent's actual cursor encode/decode path.
func (x *entExec) paginatePosts(ctx context.Context, v op.PaginatePosts) model.Result {
	orderOpts, err := entOrderOptions(v.OrderBy)
	if err != nil {
		return entErrResult(err)
	}

	var after, before *ent.Cursor
	if v.AfterRef != nil || v.BeforeRef != nil {
		cursors, cerr := x.entCaptureCursors(ctx, orderOpts)
		if cerr != nil {
			return entErrResult(cerr)
		}
		if v.AfterRef != nil {
			after = cursors[*v.AfterRef]
		}
		if v.BeforeRef != nil {
			before = cursors[*v.BeforeRef]
		}
	}

	// WithAuthor eager-loads the owner edge so paginated rows carry the author
	// Ref exactly like the non-paginated reads.
	conn, err := x.c.Post.Query().WithAuthor().Paginate(ctx, after, v.First, before, v.Last, orderOpts...)
	if err != nil {
		return entErrResult(err)
	}
	return x.entConnResult(conn)
}

// entCaptureCursors pages the full ordered set and returns a map from each
// post's creation handle to its Ent cursor for that ordering. captureWindow
// pulls every live post in one page (the curated suite stays well under it).
func (x *entExec) entCaptureCursors(ctx context.Context, orderOpts []ent.PostPaginateOption) (map[int]*ent.Cursor, error) {
	window := captureWindow
	conn, err := x.c.Post.Query().Paginate(ctx, nil, &window, nil, nil, orderOpts...)
	if err != nil {
		return nil, err
	}
	out := make(map[int]*ent.Cursor, len(conn.Edges))
	for _, e := range conn.Edges {
		cur := e.Cursor
		out[x.reg.handleForID(kindPost, e.Node.ID)] = &cur
	}
	return out, nil
}

// entConnResult normalizes an Ent connection into a model.Result.
func (x *entExec) entConnResult(conn *ent.PostConnection) model.Result {
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

// entOrderOptions builds Ent pagination order options from op order terms.
// Empty order yields no options, so Ent falls back to its default id-ascending
// order — matching the reference's handle-ascending default. Ent only generates
// an order field for view_count (the only field with an OrderField annotation),
// so other order columns are rejected by UnmarshalGQL.
func entOrderOptions(terms []op.OrderTerm) ([]ent.PostPaginateOption, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	orders := make([]*ent.PostOrder, 0, len(terms))
	for _, t := range terms {
		var field ent.PostOrderField
		if err := field.UnmarshalGQL(gqlEntOrderName(t.Field)); err != nil {
			return nil, err
		}
		dir := ent.OrderDirection("ASC")
		if t.Desc {
			dir = ent.OrderDirection("DESC")
		}
		f := field
		orders = append(orders, &ent.PostOrder{Direction: dir, Field: &f})
	}
	return []ent.PostPaginateOption{ent.WithPostOrder(orders)}, nil
}
