package orm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// This file is what a list endpoint needs, whatever serves it (REST, GraphQL,
// RPC): keyset pagination with opaque cursors, and predicates and order terms
// built from field names that arrive in a request.

// PageArgs asks for one page of a query.
//
// First with After pages forward, Last with Before pages backward; the
// cursors are the ones a previous page returned. With neither First nor Last
// the page is every row from the cursor on.
type PageArgs struct {
	First  *int
	After  string
	Last   *int
	Before string
	// Total asks for the number of rows the query matches in all, which
	// costs a COUNT.
	Total bool
}

// Page is one page of entities of type T, a *Entity.
//
// It has fields and no methods, on purpose. A generic type's methods are
// compiled by every package that can reach one of its instantiations, used
// or not, and a query's Paginate method puts Page[*User] in reach of every
// package that names UserQuery. With StartCursor and EndCursor as methods, a
// package from which the queries of 450 entities could be reached compiled
// them 450 times: half its compile time (benchmarks/fixtures/realistic).
type Page[T any] struct {
	Items []T
	// Cursors[i] is the cursor of Items[i]: pass it as After or Before to
	// continue from that row.
	Cursors []string
	// StartCursor and EndCursor are the cursors of the first and last item,
	// or "" for an empty page.
	StartCursor string
	EndCursor   string
	HasNext     bool
	HasPrev     bool
	// Total is the number of rows the query matches, or -1 when it was not
	// asked for.
	Total int
}

// PageOf converts the page of any entity the runtime returns into the typed
// page of T. Generated Paginate methods call it.
func PageOf[T any](p *Page[any]) *Page[T] {
	out := &Page[T]{Items: make([]T, len(p.Items)), Cursors: p.Cursors, HasNext: p.HasNext, HasPrev: p.HasPrev, Total: p.Total}
	if n := len(p.Cursors); n > 0 {
		out.StartCursor, out.EndCursor = p.Cursors[0], p.Cursors[n-1]
	}
	for i, e := range p.Items {
		out.Items[i], _ = e.(T)
	}
	return out
}

// ErrBadCursor is returned for a cursor that this query did not produce: it
// is malformed, or it belongs to another ordering.
var ErrBadCursor = errors.New("orm: invalid cursor")

// Paginate returns one page of the query, in the query's order with the id
// as the final tie-break. The order columns must not hold NULL: a row is
// located by comparing with the cursor's values.
//
// Limit and Offset of the query are ignored; the entity's policy and the
// interceptors apply as to any read.
//
//go:noinline
func (q *QueryCore) Paginate(ctx context.Context, args PageArgs) (*Page[any], error) {
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
	keys := slices.Clone(q.Orders)
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
	v, err := q.guarded(ctx, c, func(ctx context.Context, q *QueryCore) (any, error) {
		page := &Page[any]{Total: -1}
		if args.Total {
			n, err := q.count(ctx, c)
			if err != nil {
				return nil, err
			}
			page.Total = n
		}
		win := *q
		win.Off, win.Lim, win.Lock = 0, 0, LockNone
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
			win.Preds = append(win.Preds, beyond(keys, vals, cur.after))
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
		rows, err := win.all(ctx, c)
		if err != nil {
			return nil, err
		}
		more := limit > 0 && len(rows) > limit
		if more {
			rows = rows[:limit]
		}
		if backward {
			slices.Reverse(rows)
			page.HasPrev, page.HasNext = more, args.Before != ""
		} else {
			page.HasNext, page.HasPrev = more, args.After != ""
		}
		page.Items = rows
		page.Cursors = make([]string, len(rows))
		for i, e := range rows {
			cur, err := m.encodeCursor(keys, e)
			if err != nil {
				return nil, err
			}
			page.Cursors[i] = cur
		}
		return page, nil
	})
	if err != nil {
		return nil, err
	}
	page, ok := v.(*Page[any])
	if !ok {
		return nil, fmt.Errorf("orm: an interceptor returned %T for a page of %s", v, m.Name)
	}
	return page, nil
}

// beyond is the condition "the row sorts after (or before) the cursor":
//
//	a > x OR (a = x AND b > y) OR (a = x AND b = y AND id > z)
//
// with each comparison turned around for a descending column.
func beyond(keys []O, vals []any, after bool) P {
	ors := make([]P, 0, len(keys))
	for i, k := range keys {
		op := OpGT
		if k.Desc == after {
			op = OpLT
		}
		and := make([]P, 0, i+1)
		for j := range i {
			and = append(and, P{Op: OpEQ, Col: keys[j].Col, Val: vals[j]})
		}
		and = append(and, P{Op: op, Col: k.Col, Val: vals[i]})
		ors = append(ors, P{Op: OpAnd, Subs: and})
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

func (m *Meta) encodeCursor(keys []O, e any) (string, error) {
	c := cursor{K: make([]string, len(keys)), V: make([]json.RawMessage, len(keys))}
	for i, k := range keys {
		v := m.get(e, k.Col)
		if v == nil {
			return "", fmt.Errorf("orm: cannot paginate %s by %q: the column holds NULL", m.Name, k.Col)
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
