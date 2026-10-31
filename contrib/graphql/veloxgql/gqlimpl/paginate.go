package gqlimpl

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/plugin"
)

// Args are a connection's paging arguments, as the client gave them.
//
// first (with or without after) pages forward and last (with or without before)
// backward; with neither, the page is the MaxPage of
// veloxgql.LimitsFromContext(ctx) rows forward, or backward from before when
// before is given. first and last together, after and before together, first
// with before and last with after all fail with ErrBadPagination: keyset paging
// bounds a page on one side only.
type Args struct {
	After, Before *veloxgql.Cursor
	First, Last   *int
}

// OrderTerm is one term of a connection's order, or one column of its key:
// the field, its direction, and what paging needs of the column. Make one
// with Term.
type OrderTerm[E any] struct {
	// Field is the term's order field, the GraphQL enum value such as
	// "NAME", which the cursors record; for a key column, the column's name.
	Field string
	// Direction is the term's direction. The zero direction is ASC, as the
	// SDL's default for XOrder.direction.
	Direction veloxgql.OrderDirection

	asc, desc velox.Order[E]
	get       func(*E) any
	set       func(*E, any) error
}

// Term is the order term field, in direction dir, of the column whose handle
// gives asc and desc (gen.Users.Name.Asc() and .Desc()). get returns a row's
// value of the column and set sets it, as V: velox's standard Go type for
// the column (an integer, a float, string, bool, time.Time or [16]byte), to
// which the generated code converts a field of another Go type.
func Term[E, V any](field string, dir veloxgql.OrderDirection, asc, desc velox.Order[E], get func(*E) V, set func(*E, V)) OrderTerm[E] {
	return OrderTerm[E]{
		Field: field, Direction: dir, asc: asc, desc: desc,
		get: func(x *E) any { return get(x) },
		set: func(x *E, v any) error {
			// A cursor from a page holds V; one from the wire, JSON values.
			if t, ok := v.(V); ok {
				set(x, t)
				return nil
			}
			t, err := cursorValue[V](v)
			if err != nil {
				return err
			}
			set(x, t)
			return nil
		},
	}
}

// Page is a page of a connection: its rows in the connection's order, each
// row's cursor, and the page info.
type Page[E any] struct {
	Rows    []*E
	Cursors []veloxgql.Cursor
	// Info is the page info. Its StartCursor and EndCursor point into
	// Cursors.
	Info veloxgql.PageInfo
}

// Connection is a connection to a list of E: the query of the whole list
// and what one page of it is. It runs nothing until it is asked: Page runs
// the page query, once however often and from however many goroutines it
// is called, and TotalCount the COUNT, once too. So a GraphQL selection of
// only totalCount runs no page query, and one without it no COUNT.
//
// A Connection is made by NewConnection, and must not be copied.
type Connection[E any] struct {
	query velox.Query[E] // the whole list, unordered
	typ   string         // the GraphQL type of the nodes
	n     int            // the page size
	back  bool           // paging backward: last, or before
	terms []OrderTerm[E] // the order's terms, then the key's columns
	key   int            // the index in terms of the key's first column
	after *E             // the row the page starts after, in the paging direction
	// edge is set by EdgeConnection when the connection is its part of a
	// batch.
	edge *edge[E]

	pageOnce  sync.Once
	page      *Page[E]
	pageErr   error
	countOnce sync.Once
	count     int
	countErr  error
}

// NewConnection is the connection to the nodes of the GraphQL type typ that q
// returns, paged by args, in the order order and then key, the columns of the
// primary key (each ASC). q holds the connection's filters and no order: the
// connection orders it, and a q with an OrderBy is refused with an internal
// error, since its order would come first and break the cursors. q must also
// select the key and the order's columns (no Columns or Omit that drops
// them): a cursor is taken from the rows. Its Offset is cleared and its Limit
// replaced, as the page is the connection's. It checks the arguments and
// decodes the cursor, and runs no query.
//
// It fails with ErrBadPagination for first and last together, either below
// 0, another combination Args refuses, or an order of more than
// MaxOrderTerms terms; with ErrPageTooLarge for either over MaxPage (both
// the limits of veloxgql.LimitsFromContext(ctx)); and with ErrBadCursor for a
// cursor of another type (ErrWrongIDType too), taken under another order,
// or whose values are not of the order's types.
func NewConnection[E any](ctx context.Context, q velox.Query[E], typ string, args Args, order, key []OrderTerm[E]) (*Connection[E], error) {
	if q.Ordered() {
		// The application's bug, not the client's: an uncoded error.
		return nil, fmt.Errorf("veloxgql: %sConnectionOf: the base query has an OrderBy; the connection orders by its orderBy argument", plugin.GoPlural(typ))
	}
	lim := veloxgql.LimitsFromContext(ctx)
	n, back, err := pageSize(args, lim.MaxPage)
	if err != nil {
		return nil, gqlcore.Within(typ+" connection", err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("veloxgql: %s connection: no key to order by last", typ)
	}
	if len(order) > lim.MaxOrderTerms {
		return nil, InputErrorf("%s connection: %w: orderBy has %d terms, and the most is %d", typ, veloxgql.ErrBadPagination, len(order), lim.MaxOrderTerms)
	}
	c := &Connection[E]{query: q.Offset(0), typ: typ, n: n, back: back, key: len(order)}
	c.terms = make([]OrderTerm[E], 0, len(order)+len(key))
	for _, t := range slices.Concat(order, key) {
		switch t.Direction {
		case "":
			t.Direction = veloxgql.OrderDirectionAsc
		case veloxgql.OrderDirectionAsc, veloxgql.OrderDirectionDesc:
		default:
			return nil, InputErrorf("%s connection: %w: the order %s has the direction %q, not ASC or DESC", typ, veloxgql.ErrBadPagination, t.Field, t.Direction)
		}
		c.terms = append(c.terms, t)
	}
	cur := args.After
	if back {
		cur = args.Before
	}
	if cur != nil {
		if c.after, err = c.row(*cur); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Page returns the connection's page, running its query on the first call:
//   - The order always ends with the key, so that it is total.
//   - Paging backward (last or before) reverses every term's direction,
//     continues after the cursor with velox's Query.After on that order, and
//     reverses the rows read.
//   - It reads one row more than the page: that row's presence is
//     hasNextPage forward and hasPreviousPage backward. The other is false:
//     Relay lets a server say false when it cannot tell cheaply.
//
// The page query reads at most n+1 rows whatever the DB's WithMaxRows cap: the
// MaxPage of veloxgql.LimitsFromContext(ctx) bounds it. The base query's With
// loads share that cap of n+1 rows, unless they have Rel.MaxRows or
// Rel.LimitEach. The page's rows become a group of the operation, whose edges
// batch (see EdgeConnection); in the connection of an edge (EdgeConnection) the
// page is the connection's part of the batch, which may return n+1 rows per
// owner.
func (c *Connection[E]) Page[_ velox.Lazy[E]](ctx context.Context) (*Page[E], error) {
	c.pageOnce.Do(func() {
		c.pageErr = errPanicked // the other callers' error, when run panics
		c.page, c.pageErr = c.run(ctx)
	})
	return c.page, c.pageErr
}

// TotalCount returns the number of nodes in the whole connection, not only
// on its page, running a COUNT on the first call.
func (c *Connection[E]) TotalCount[_ velox.Lazy[E]](ctx context.Context) (int, error) {
	c.countOnce.Do(func() {
		c.countErr = errPanicked // the other callers' error, when the count panics
		if c.edge != nil {
			c.count, c.countErr = c.edge.count(c.query)
			return
		}
		n, err := c.query.Count(ctx)
		c.count, c.countErr = int(n), err
	})
	return c.count, c.countErr
}

// run reads the page.
func (c *Connection[E]) run[_ velox.Lazy[E]](ctx context.Context) (*Page[E], error) {
	orders := make([]velox.Order[E], len(c.terms))
	for i, t := range c.terms {
		orders[i] = t.asc
		if (t.Direction == veloxgql.OrderDirectionDesc) != c.back {
			orders[i] = t.desc
		}
	}
	q := c.query.OrderBy(orders...)
	if c.after != nil {
		q = q.After(c.after)
	}
	var rows []E
	var err error
	if c.edge != nil {
		rows, err = c.edge.rows(q)
	} else {
		// MaxRows: the page is bounded by MaxPage, and the DB's row
		// cap, which may be below it, must not fail a full page.
		rows, err = q.Limit(c.n + 1).MaxRows(c.n + 1).All(ctx)
	}
	if err != nil {
		return nil, err
	}
	more := len(rows) > c.n
	if more {
		rows = rows[:c.n]
	}
	p := &Page[E]{Rows: make([]*E, len(rows)), Cursors: make([]veloxgql.Cursor, len(rows))}
	for i := range rows {
		p.Rows[i] = &rows[i]
	}
	if c.back {
		// The pointers, not the rows: a batch's rows are shared.
		slices.Reverse(p.Rows)
	}
	for i, x := range p.Rows {
		p.Cursors[i] = c.cursor(x)
	}
	if c.edge == nil {
		// A batch made its rows a group already.
		gqlcore.Add(ctx, p.Rows)
	}
	if c.back {
		p.Info.HasPreviousPage = more
	} else {
		p.Info.HasNextPage = more
	}
	if len(rows) > 0 {
		p.Info.StartCursor, p.Info.EndCursor = &p.Cursors[0], &p.Cursors[len(rows)-1]
	}
	return p, nil
}

// cursor is x's cursor: the order's terms with x's values, the key's columns
// but the last as terms named "key:<column>", and the last column as the key.
func (c *Connection[E]) cursor[_ velox.Lazy[E]](x *E) veloxgql.Cursor {
	last := len(c.terms) - 1
	cur := veloxgql.Cursor{Type: c.typ, Terms: make([]veloxgql.CursorTerm, last), Key: c.terms[last].get(x)}
	for i, t := range c.terms[:last] {
		cur.Terms[i] = veloxgql.CursorTerm{Field: c.field(i), Direction: t.Direction, Value: t.get(x)}
	}
	return cur
}

// field is the field of term i in a cursor.
func (c *Connection[E]) field[_ velox.Lazy[E]](i int) string {
	if i >= c.key {
		return "key:" + c.terms[i].Field
	}
	return c.terms[i].Field
}

// row checks cur against the connection's type and order, and returns the
// row it stands for: a zero E with the order's and the key's columns set
// from it, for Query.After.
func (c *Connection[E]) row[_ velox.Lazy[E]](cur veloxgql.Cursor) (*E, error) {
	last := len(c.terms) - 1
	order := make([]cursorOrder, last)
	for i, t := range c.terms[:last] {
		order[i] = cursorOrder{Field: c.field(i), Direction: t.Direction}
	}
	if err := checkCursor(cur, c.typ, order); err != nil {
		return nil, err
	}
	x := new(E)
	for i, t := range c.terms[:last] {
		if err := t.set(x, cur.Terms[i].Value); err != nil {
			return nil, gqlcore.Within(c.typ+" cursor", err)
		}
	}
	if err := c.terms[last].set(x, cur.Key); err != nil {
		return nil, gqlcore.Within(c.typ+" cursor", err)
	}
	return x, nil
}

// pageSize is the page size args ask for, and whether they page backward;
// maxPage is the most, and the size when args give none.
func pageSize(args Args, maxPage int) (int, bool, error) {
	switch {
	case args.First != nil && args.Last != nil:
		return 0, false, InputErrorf("%w: first and last given together", veloxgql.ErrBadPagination)
	case args.After != nil && args.Before != nil:
		return 0, false, InputErrorf("%w: after and before given together", veloxgql.ErrBadPagination)
	case args.First != nil && args.Before != nil:
		return 0, false, InputErrorf("%w: first pages forward, and before goes with last", veloxgql.ErrBadPagination)
	case args.Last != nil && args.After != nil:
		return 0, false, InputErrorf("%w: last pages backward, and after goes with first", veloxgql.ErrBadPagination)
	}
	back := args.Last != nil || args.Before != nil
	n, name := args.First, "first"
	if back {
		n, name = args.Last, "last"
	}
	switch {
	case n == nil:
		return maxPage, back, nil
	case *n < 0:
		return 0, false, InputErrorf("%w: %s is %d, below 0", veloxgql.ErrBadPagination, name, *n)
	case *n > maxPage:
		return 0, false, InputErrorf("%w: %s is %d, and the most is %d", veloxgql.ErrPageTooLarge, name, *n, maxPage)
	}
	return *n, back, nil
}
