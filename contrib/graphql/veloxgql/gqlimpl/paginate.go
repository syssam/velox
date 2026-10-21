package gqlimpl

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// Args are a connection's paging arguments, as the client gave them.
//
// first (with or without after) pages forward and last (with or without
// before) backward; with neither, the page is MaxPage rows forward, or
// backward from before when before is given. first and last together, after
// and before together, first with before and last with after all fail with
// ErrBadPagination: keyset paging bounds a page on one side only.
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
// A Connection is made by NewConnection or LoadedConnection, and must not be
// copied.
type Connection[E any] struct {
	query velox.Query[E] // the whole list, unordered
	typ   string         // the GraphQL type of the nodes
	n     int            // the page size
	back  bool           // paging backward: last, or before
	terms []OrderTerm[E] // the order's terms, then the key's columns
	key   int            // the index in terms of the key's first column
	after *E             // the row the page starts after, in the paging direction
	// loaded are the rows of a LoadedConnection, which runs no query.
	loaded   []E
	isLoaded bool

	pageOnce  sync.Once
	page      *Page[E]
	pageErr   error
	countOnce sync.Once
	count     int
	countErr  error
}

// NewConnection is the connection to the nodes of the GraphQL type typ that q
// returns, paged by args, in the order order and then key, the columns of
// the primary key (each ASC). q holds the connection's filters and no order:
// the connection orders it. It checks the arguments and decodes the cursor,
// and fails as paginate does, before any query runs; it runs none.
func NewConnection[E any](q velox.Query[E], typ string, args Args, order, key []OrderTerm[E]) (*Connection[E], error) {
	n, back, err := pageSize(args)
	if err != nil {
		return nil, fmt.Errorf("veloxgql: %s connection: %w", typ, err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("veloxgql: %s connection: no key to order by last", typ)
	}
	if len(order) > veloxgql.MaxOrderTerms {
		return nil, fmt.Errorf("veloxgql: %s connection: %w: orderBy has %d terms, and the most is %d", typ, veloxgql.ErrBadPagination, len(order), veloxgql.MaxOrderTerms)
	}
	c := &Connection[E]{query: q, typ: typ, n: n, back: back, key: len(order)}
	c.terms = make([]OrderTerm[E], 0, len(order)+len(key))
	for _, t := range slices.Concat(order, key) {
		switch t.Direction {
		case "":
			t.Direction = veloxgql.OrderDirectionAsc
		case veloxgql.OrderDirectionAsc, veloxgql.OrderDirectionDesc:
		default:
			return nil, fmt.Errorf("veloxgql: %s connection: %w: the order %s has the direction %q, not ASC or DESC", typ, veloxgql.ErrBadPagination, t.Field, t.Direction)
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

// LoadedConnection is a connection over rows, the related rows a With
// loaded, in key order (With's default) under the GraphQL type typ: its page
// is the first MaxPage rows, its cursors are taken under the key's order,
// and its TotalCount is the number of rows. It runs no query.
func LoadedConnection[E any](rows []E, typ string, key []OrderTerm[E]) *Connection[E] {
	terms := make([]OrderTerm[E], len(key))
	for i, t := range key {
		t.Direction = veloxgql.OrderDirectionAsc
		terms[i] = t
	}
	return &Connection[E]{typ: typ, n: veloxgql.MaxPage, terms: terms, loaded: rows, isLoaded: true}
}

// paginate runs the page query of the connection NewConnection makes, and
// returns the page:
//   - The order always ends with the key, so that it is total.
//   - Paging backward (last or before) reverses every term's direction,
//     continues after the cursor with velox's Query.After on that order, and
//     reverses the rows read.
//   - It reads one row more than the page: that row's presence is
//     hasNextPage forward and hasPreviousPage backward. The other is false:
//     Relay lets a server say false when it cannot tell cheaply.
//
// The page query reads at most n+1 rows whatever the DB's WithMaxRows cap:
// MaxPage bounds it. The query's With loads share that cap of n+1 rows, until
// GraphQL plan 4 gives loads their own; the generated code adds none yet.
//
// It fails before any query runs: with ErrBadPagination for first and last
// together, either below 0, another combination Args refuses, or an order
// of more than MaxOrderTerms terms; with
// ErrPageTooLarge for either over MaxPage; and with ErrBadCursor for a cursor
// of another type (ErrWrongIDType too), taken under another order, or whose
// values are not of the order's types.
func paginate[E any](ctx context.Context, q velox.Query[E], typ string, args Args, order, key []OrderTerm[E]) (*Page[E], error) {
	c, err := NewConnection(q, typ, args, order, key)
	if err != nil {
		return nil, err
	}
	return c.Page(ctx)
}

// Page returns the connection's page, running its query on the first call.
func (c *Connection[E]) Page[_ velox.Lazy[E]](ctx context.Context) (*Page[E], error) {
	c.pageOnce.Do(func() { c.page, c.pageErr = c.run(ctx) })
	return c.page, c.pageErr
}

// TotalCount returns the number of nodes in the whole connection, not only
// on its page, running a COUNT on the first call.
func (c *Connection[E]) TotalCount[_ velox.Lazy[E]](ctx context.Context) (int, error) {
	c.countOnce.Do(func() {
		if c.isLoaded {
			c.count = len(c.loaded)
			return
		}
		n, err := c.query.Count(ctx)
		c.count, c.countErr = int(n), err
	})
	return c.count, c.countErr
}

// run reads the page.
func (c *Connection[E]) run[_ velox.Lazy[E]](ctx context.Context) (*Page[E], error) {
	rows := c.loaded
	if !c.isLoaded {
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
		// MaxRows: the page is bounded by MaxPage, and the DB's row cap,
		// which may be below it, must not fail a full page.
		var err error
		if rows, err = q.Limit(c.n + 1).MaxRows(c.n + 1).All(ctx); err != nil {
			return nil, err
		}
	}
	more := len(rows) > c.n
	if more {
		rows = rows[:c.n]
	}
	if c.back {
		slices.Reverse(rows)
	}
	p := &Page[E]{Rows: make([]*E, len(rows)), Cursors: make([]veloxgql.Cursor, len(rows))}
	for i := range rows {
		p.Rows[i] = &rows[i]
		p.Cursors[i] = c.cursor(&rows[i])
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
			return nil, fmt.Errorf("veloxgql: %s cursor: %w", c.typ, err)
		}
	}
	if err := c.terms[last].set(x, cur.Key); err != nil {
		return nil, fmt.Errorf("veloxgql: %s cursor: %w", c.typ, err)
	}
	return x, nil
}

// pageSize is the page size args ask for, and whether they page backward.
func pageSize(args Args) (int, bool, error) {
	switch {
	case args.First != nil && args.Last != nil:
		return 0, false, fmt.Errorf("%w: first and last given together", veloxgql.ErrBadPagination)
	case args.After != nil && args.Before != nil:
		return 0, false, fmt.Errorf("%w: after and before given together", veloxgql.ErrBadPagination)
	case args.First != nil && args.Before != nil:
		return 0, false, fmt.Errorf("%w: first pages forward, and before goes with last", veloxgql.ErrBadPagination)
	case args.Last != nil && args.After != nil:
		return 0, false, fmt.Errorf("%w: last pages backward, and after goes with first", veloxgql.ErrBadPagination)
	}
	back := args.Last != nil || args.Before != nil
	n, name := args.First, "first"
	if back {
		n, name = args.Last, "last"
	}
	switch {
	case n == nil:
		return veloxgql.MaxPage, back, nil
	case *n < 0:
		return 0, false, fmt.Errorf("%w: %s is %d, below 0", veloxgql.ErrBadPagination, name, *n)
	case *n > veloxgql.MaxPage:
		return 0, false, fmt.Errorf("%w: %s is %d, and the most is %d", veloxgql.ErrPageTooLarge, name, *n, veloxgql.MaxPage)
	}
	return *n, back, nil
}
