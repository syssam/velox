package orm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/syssam/velox/dialect"
)

// This file is what a list endpoint needs, whatever serves it (REST, GraphQL,
// RPC): keyset pagination with opaque cursors, and predicates and order terms
// built from field names that arrive in a request.

// Cursor is the position of a row in an ordering: an opaque string a page
// returns with each row and takes back as After or Before. A cursor made for
// one ordering is refused under another.
type Cursor string

// MarshalGQL and UnmarshalGQL are the methods a GraphQL engine binds a
// scalar by (gqlgen's and graphql-go's Marshaler); nothing here imports one.
func (c Cursor) MarshalGQL(w io.Writer) { _, _ = io.WriteString(w, strconv.Quote(string(c))) }

// UnmarshalGQL takes the cursor a client sent back.
func (c *Cursor) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("%w: it is a %T, not a string", ErrBadCursor, v)
	}
	*c = Cursor(s)
	return nil
}

// PageInfo says where a page lies in the whole: Relay's PageInfo.
type PageInfo struct {
	HasNextPage     bool    `json:"hasNextPage"`
	HasPreviousPage bool    `json:"hasPreviousPage"`
	StartCursor     *Cursor `json:"startCursor"`
	EndCursor       *Cursor `json:"endCursor"`
}

// OrderDirection is the direction of an ordering that arrives in a request.
type OrderDirection string

// The two directions, spelled as GraphQL spells them.
const (
	OrderDirectionAsc  OrderDirection = "ASC"
	OrderDirectionDesc OrderDirection = "DESC"
)

// Page is one page as the runtime returns it; the generated Paginate of an
// entity turns it into that entity's connection.
type Page struct {
	Rows []any
	// Cursors[i] is the cursor of Rows[i].
	Cursors []Cursor
	Info    PageInfo
	// Total is the number of rows the query matches, or -1 when the caller
	// said it does not want it (WithoutTotal).
	Total int
}

type noTotalKey struct{}

// WithoutTotal returns a context under which Paginate does not count the
// rows the query matches, and reports -1: for a caller that knows nobody
// reads the total (a GraphQL request that did not select totalCount, an
// endless list).
func WithoutTotal(ctx context.Context) context.Context {
	return context.WithValue(ctx, noTotalKey{}, true)
}

// ErrBadCursor is returned for a cursor that this query did not produce: it
// is malformed, or it belongs to another ordering.
var ErrBadCursor = errors.New("orm: invalid cursor")

// Paginate returns one page of the query, in the query's order with the id
// as the final tie-break: first rows after the cursor after, or last rows
// before the cursor before, as Relay's connections take them; with neither
// first nor last, every row from the cursor on. An order column may hold
// NULL: the walk passes the NULL rows where the database sorts them (first in
// ascending order on SQLite and MySQL, last on PostgreSQL).
//
// Limit and Offset of the query are ignored; the entity's policy and the
// interceptors apply as to any read.
//
//go:noinline
func (q *QueryCore) Paginate(ctx context.Context, after *Cursor, first *int, before *Cursor, last *int) (*Page, error) {
	var args struct {
		First, Last   *int
		After, Before string
	}
	args.First, args.Last = first, last
	if after != nil {
		args.After = string(*after)
	}
	if before != nil {
		args.Before = string(*before)
	}
	m := q.M
	if m.id() == nil {
		return nil, fmt.Errorf("orm: %s has no single-column id to paginate by", m.Name)
	}
	if args.First != nil && args.Last != nil {
		return nil, errors.New("orm: a page is asked for with First or with Last, not both")
	}
	for _, n := range []*int{args.First, args.Last} {
		if n != nil && *n < 0 {
			return nil, errors.New("orm: a page size cannot be negative")
		}
	}
	// The sort key: the query's order, then the id unless it is there already.
	for _, o := range q.Orders {
		if o.err != nil {
			return nil, o.err
		}
	}
	keys := slices.DeleteFunc(slices.Clone(q.Orders), func(o O) bool { return o.Col == "" })
	if !slices.ContainsFunc(keys, func(o O) bool { return o.Col == m.id().Column }) {
		keys = append(keys, O{Col: m.id().Column})
	}
	for _, k := range keys {
		if m.col(k.Col) < 0 {
			return nil, fmt.Errorf("orm: cannot paginate %s by %q: not a column", m.Name, k.Col)
		}
	}
	backward := args.Last != nil
	c, cerr := q.conn()
	if cerr != nil {
		return nil, cerr
	}
	nullsFirst := dialect.GetCapabilities(c.drv.Dialect()).Has(dialect.CapNullsFirst)
	v, err := q.guarded(ctx, c, func(ctx context.Context, q *QueryCore) (any, error) {
		page := &Page{Total: -1}
		if ctx.Value(noTotalKey{}) == nil {
			n, err := q.count(ctx, c)
			if err != nil {
				return nil, err
			}
			page.Total = n
		}
		win := *q
		win.Off, win.Lim = 0, 0
		win.Preds = slices.Clone(q.Preds)
		for _, cur := range []struct {
			s     string
			after bool
		}{{args.After, true}, {args.Before, false}} {
			if cur.s == "" {
				continue
			}
			vals, err := m.decodeCursor(keys, cur.s)
			if err != nil {
				return nil, err
			}
			win.Preds = append(win.Preds, beyond(keys, vals, cur.after, nullsFirst))
		}
		win.Orders = slices.Clone(keys)
		if backward {
			// Read from the end: the last n rows are the first n in reverse.
			for i := range win.Orders {
				win.Orders[i].Desc = !win.Orders[i].Desc
			}
		}
		limit := -1
		if args.First != nil {
			limit = *args.First
		} else if args.Last != nil {
			limit = *args.Last
		}
		if limit == 0 {
			return page, nil
		}
		if limit > 0 {
			win.Lim = limit + 1 // one more row tells whether a further page exists
		}
		// A key whose Go field cannot hold NULL (an optional column in a
		// field that is not a pointer) reads NULL as zero. The cursor has
		// to say NULL all the same, or the walk continues after "zero"
		// instead of after the NULLs: rows dropped, or a page repeated
		// for ever.
		watch := &nullWatch{cols: make([]int, len(keys))}
		for i, k := range keys {
			watch.cols[i] = m.col(k.Col)
			if kind := m.cols[watch.cols[i]]; kind.ptr || kind.dest != destNull {
				watch.cols[i] = -1 // the field says NULL itself
			}
		}
		win.nulls = watch
		rows, err := win.all(ctx, c)
		if err != nil {
			return nil, err
		}
		null := watch.rows
		more := limit > 0 && len(rows) > limit
		if more {
			rows, null = rows[:limit], null[:limit]
		}
		if backward {
			slices.Reverse(rows)
			slices.Reverse(null)
			page.Info.HasPreviousPage, page.Info.HasNextPage = more, args.Before != ""
		} else {
			page.Info.HasNextPage, page.Info.HasPreviousPage = more, args.After != ""
		}
		page.Rows = rows
		page.Cursors = make([]Cursor, len(rows))
		for i, e := range rows {
			cur, err := m.encodeCursor(keys, e, null[i])
			if err != nil {
				return nil, err
			}
			page.Cursors[i] = Cursor(cur)
		}
		if n := len(rows); n > 0 {
			page.Info.StartCursor, page.Info.EndCursor = &page.Cursors[0], &page.Cursors[n-1]
		}
		return page, nil
	})
	if err != nil {
		return nil, err
	}
	page, ok := v.(*Page)
	if !ok {
		return nil, fmt.Errorf("orm: an interceptor returned %T for a page of %s", v, m.Name)
	}
	return page, nil
}

// beyond is the condition "the row sorts after (or before) the cursor":
//
//	a > x OR (a = x AND b > y) OR (a = x AND b = y AND id > z)
//
// with each comparison turned around for a descending column, "= NULL" being
// IS NULL, and "after" taking NULL where the database sorts it (nullsFirst:
// before every value in ascending order). A row comparison (a, id) > (x, z)
// is NULL whenever a or x is, so a walk written that way stops at the first
// NULL.
func beyond(keys []O, vals []any, after, nullsFirstAsc bool) P {
	ors := make([]P, 0, len(keys))
	for i, k := range keys {
		// asc is the direction the walk moves in on this column.
		asc := k.Desc != after
		nullsFirst := asc == nullsFirstAsc
		op := OpGT
		if !asc {
			op = OpLT
		}
		var next P
		switch {
		case vals[i] == nil && nullsFirst:
			next = P{Op: OpNotNull, Col: k.Col}
		case vals[i] == nil:
			continue // NULLs sort last here: nothing follows one on this column
		case nullsFirst:
			next = P{Op: op, Col: k.Col, Val: vals[i]}
		default:
			next = P{Op: OpOr, Subs: []P{{Op: op, Col: k.Col, Val: vals[i]}, {Op: OpIsNull, Col: k.Col}}}
		}
		and := make([]P, 0, i+1)
		for j := range i {
			if vals[j] == nil {
				and = append(and, P{Op: OpIsNull, Col: keys[j].Col})
			} else {
				and = append(and, P{Op: OpEQ, Col: keys[j].Col, Val: vals[j]})
			}
		}
		ors = append(ors, P{Op: OpAnd, Subs: append(and, next)})
	}
	return P{Op: OpOr, Subs: ors}
}

// A cursor is the row's values of the sort key, as JSON, base64-encoded. It
// names the columns too, so that a cursor of another ordering is refused
// rather than compared against the wrong columns.
type cursor struct {
	K []string          `json:"k"`
	V []json.RawMessage `json:"v"`
}

// zonedTime is a time in a cursor, with the name of its zone. A driver that
// stores times as text (SQLite) writes the zone's name, and the cursor's
// value is compared with what is stored: the same instant under another
// zone name is, there, another string.
type zonedTime struct {
	T    time.Time `json:"t"`
	Zone string    `json:"z"`
}

func zoneName(t time.Time) string {
	name, _ := t.Zone()
	return name
}

// encodeCursor makes the cursor of row e; null says which of the keys were
// NULL in the row although e's field holds a zero value.
func (m *Meta) encodeCursor(keys []O, e any, null []bool) (string, error) {
	c := cursor{K: make([]string, len(keys)), V: make([]json.RawMessage, len(keys))}
	for i, k := range keys {
		v := m.get(e, k.Col)
		if i < len(null) && null[i] {
			v = nil
		}
		if t, ok := v.(time.Time); ok {
			v = zonedTime{T: t, Zone: zoneName(t)}
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("orm: cursor of %s.%s: %w", m.Name, k.Col, err)
		}
		c.K[i], c.V[i] = k.Col, raw
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (m *Meta) decodeCursor(keys []O, s string) ([]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrBadCursor
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil || len(c.K) != len(keys) || len(c.V) != len(keys) {
		return nil, ErrBadCursor
	}
	vals := make([]any, len(keys))
	for i, k := range keys {
		if c.K[i] != k.Col {
			return nil, fmt.Errorf("%w: it was made for another ordering", ErrBadCursor)
		}
		if string(c.V[i]) == "null" {
			// The row's value was NULL. An id never is. Any other column
			// may be, whatever the schema says of it: a table made by
			// something else can hold NULL where this schema would have
			// written a zero value, and its rows are paged through too.
			if m.Field(k.Col) == m.id() {
				return nil, ErrBadCursor
			}
			continue
		}
		t := m.goType(m.col(k.Col))
		if t == reflect.TypeFor[time.Time]() {
			var z zonedTime
			if err := json.Unmarshal(c.V[i], &z); err != nil {
				return nil, ErrBadCursor
			}
			_, offset := z.T.Zone()
			vals[i] = z.T.In(time.FixedZone(z.Zone, offset))
			continue
		}
		p := reflect.New(t)
		if err := json.Unmarshal(c.V[i], p.Interface()); err != nil {
			return nil, ErrBadCursor
		}
		vals[i] = p.Elem().Interface()
	}
	return vals, nil
}

// goType returns the Go type of the value of Columns[i], without the pointer
// of a nillable field.
func (m *Meta) goType(i int) reflect.Type {
	t := reflect.TypeOf(m.Ptr(m.New(), i)).Elem()
	if m.cols[i].ptr {
		t = t.Elem()
	}
	return t
}

// convert returns v as a value of the Go type of field f, when that loses
// nothing: 41.9 is not an int, and a value that overflows the field is not
// that value.
func (m *Meta) convert(f *FieldMeta, v any) (any, error) {
	t := m.goType(m.col(f.Column))
	rv := reflect.ValueOf(v)
	switch {
	case !rv.IsValid():
		return nil, fmt.Errorf("orm: %s.%s: nil is not a value", m.Name, f.Name)
	case rv.Type() == t:
		return v, nil
	case rv.Type().ConvertibleTo(t) && t.ConvertibleTo(rv.Type()) && (t.Kind() != reflect.String || rv.Kind() == reflect.String) && rv.Comparable():
		c := rv.Convert(t)
		if back := c.Convert(rv.Type()); back.Comparable() && back.Interface() == v {
			return c.Interface(), nil
		}
	}
	return nil, fmt.Errorf("orm: %s.%s: cannot use %T(%v) as a %s", m.Name, f.Name, v, v, t)
}

// FieldPredicate builds a predicate from a field name, an operator and a
// value that arrive at run time, as the filter of a list endpoint does. The
// field must exist, the operator must suit it, and the value must be of the
// field's type or convert to it without loss; value is a slice for OpIn and
// OpNotIn and ignored for OpIsNull and OpNotNull.
//
// Convert the result to the entity's predicate type: predicate.User(p).
func FieldPredicate(m *Meta, field string, op Op, value any) (P, error) {
	m.prepare()
	f := m.byName[field]
	if f == nil {
		return P{}, fmt.Errorf("orm: unknown %s field %q", m.Name, field)
	}
	if f.Sensitive {
		return P{}, fmt.Errorf("orm: %s.%s cannot be filtered by: it is sensitive", m.Name, field)
	}
	switch op {
	case OpIsNull, OpNotNull:
		return P{Op: op, Col: f.Column}, nil
	case OpEQ, OpNEQ, OpGT, OpGTE, OpLT, OpLTE:
		v, err := m.convert(f, value)
		if err != nil {
			return P{}, err
		}
		return P{Op: op, Col: f.Column, Val: v}, nil
	case OpIn, OpNotIn:
		rv := reflect.ValueOf(value)
		if rv.Kind() != reflect.Slice {
			return P{}, fmt.Errorf("orm: %s.%s: the operand of In is a list, not a %T", m.Name, field, value)
		}
		vals := make([]any, rv.Len())
		for i := range vals {
			v, err := m.convert(f, rv.Index(i).Interface())
			if err != nil {
				return P{}, err
			}
			vals[i] = v
		}
		return P{Op: op, Col: f.Column, Val: vals}, nil
	case OpContains, OpHasPrefix, OpHasSuffix, OpEqualFold, OpContainsFold:
		s, ok := value.(string)
		if !ok || m.goType(m.col(f.Column)).Kind() != reflect.String {
			return P{}, fmt.Errorf("orm: %s.%s: a text operator needs a text field and a string", m.Name, field)
		}
		return P{Op: op, Col: f.Column, Val: s}, nil
	}
	return P{}, fmt.Errorf("orm: %s.%s: operator %d cannot be built from a field name", m.Name, field, op)
}

// FieldOrder builds an order term from a field name that arrives at run time.
// Convert the result to the entity's order type: predicate.UserOrder(o).
func FieldOrder(m *Meta, field string, desc bool) (O, error) {
	m.prepare()
	f := m.byName[field]
	if f == nil {
		return O{}, fmt.Errorf("orm: unknown %s field %q", m.Name, field)
	}
	if f.Sensitive {
		return O{}, fmt.Errorf("orm: %s.%s cannot be ordered by: it is sensitive", m.Name, field)
	}
	return O{Col: f.Column, Desc: desc}, nil
}
