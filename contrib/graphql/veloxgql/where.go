package veloxgql

import (
	"context"
	"fmt"

	"github.com/syssam/velox2"
)

// Filter is a where input of E's being turned into a condition: the
// conditions the helpers added, or the first error. A where input's
// generated method Pred makes one, adds each predicate the input sets with
// one helper call, and returns Filter.Pred:
//
//	func (w *AccountWhereInput) Pred(ctx context.Context, depth int) (velox.Pred[User], error) {
//		f := veloxgql.NewFilter[User](ctx, "AccountWhereInput", depth)
//		if w == nil {
//			return f.Pred()
//		}
//		veloxgql.Not(f, w.Not)
//		veloxgql.And(f, w.And)
//		veloxgql.Or(f, w.Or)
//		veloxgql.Value(f, w.DisplayName, Users.Name.EQ)
//		veloxgql.List(f, w.DisplayNameIn, Users.Name.In)
//		veloxgql.Is(f, w.HasPosts, Users.Posts.Has)
//		veloxgql.HasWith(f, w.HasPostsWith, Users.Posts.HasWhere)
//		return f.Pred()
//	}
//
// A helper does nothing for a field the input does not set, a nil pointer or
// a nil list, and nothing once the filter has an error: the first error
// stops it, and Pred returns it. None of them runs a query, so a refused
// filter fails before any statement is built.
type Filter[E any] struct {
	ctx   context.Context
	input string // the where input's GraphQL type, for errors
	depth int
	ps    []velox.Pred[E]
	err   error
}

// NewFilter returns the Filter of a where input of the GraphQL type input,
// on E, at depth, how deeply it is nested in the where argument: 0 at the
// top, one more inside each not, and, or and has<Edge>With. A depth over
// MaxFilterDepth fails the filter with ErrFilterTooDeep. ctx is the
// context nested inputs' Pred is called with.
func NewFilter[E any](ctx context.Context, input string, depth int) *Filter[E] {
	f := &Filter[E]{ctx: ctx, input: input, depth: depth}
	if err := CheckDepth(depth); err != nil {
		f.refuse(err)
	}
	return f
}

// Pred is the condition the filter's input stands for: the And of its
// conditions, one of them alone, or the And of none, which holds for every
// row, for an input that sets nothing. It returns the filter's error
// instead, if any.
func (f *Filter[E]) Pred[_ velox.Lazy[E]]() (velox.Pred[E], error) {
	switch {
	case f.err != nil:
		return velox.Pred[E]{}, f.err
	case len(f.ps) == 1:
		return f.ps[0], nil
	}
	return velox.And(f.ps...), nil
}

// fail records err as the filter's error, unless it has one: a nested
// input's error, or DecodeID's, which name what they are about.
func fail[E any](f *Filter[E], err error) {
	if f.err == nil {
		f.err = err
	}
}

// refuse records err, an error of the filter's own input, as the filter's
// error: "veloxgql: <input>: <err>".
func (f *Filter[E]) refuse[_ velox.Lazy[E]](err error) {
	fail(f, fmt.Errorf("veloxgql: %s: %w", f.input, err))
}

// add adds p to the filter's conditions.
func add[E any](f *Filter[E], p velox.Pred[E]) { f.ps = append(f.ps, p) }

// CheckDepth checks depth, how deeply a where input is nested, against
// MaxFilterDepth: deeper fails with ErrFilterTooDeep.
func CheckDepth(depth int) error {
	if depth > MaxFilterDepth {
		return fmt.Errorf("%w: it is nested %d deep, and the most is %d", ErrFilterTooDeep, depth, MaxFilterDepth)
	}
	return nil
}

// CheckWidth checks n, the length of a list of where inputs, against
// MaxFilterWidth: more fails with ErrFilterTooWide.
func CheckWidth(n int) error {
	if n > MaxFilterWidth {
		return fmt.Errorf("%w: it has %d entries, and the most is %d", ErrFilterTooWide, n, MaxFilterWidth)
	}
	return nil
}

// Input is a where input of the GraphQL code: a pointer to the generated
// struct T, whose Pred is its condition on E at a depth.
type Input[E, T any] interface {
	*T
	Pred(ctx context.Context, depth int) (velox.Pred[E], error)
}

// nested is the condition of w, an input nested in f's, one level deeper;
// a nil w is an input that sets nothing, which holds for every row. ok is
// false when w fails, and f has its error.
func nested[E, U, T any, W Input[U, T]](f *Filter[E], w W) (p velox.Pred[U], ok bool) {
	if w == nil {
		return velox.And[U](), true
	}
	p, err := w.Pred(f.ctx, f.depth+1)
	if err != nil {
		fail(f, err)
		return p, false
	}
	return p, true
}

// list is the conditions of ws, inputs nested in f's, after CheckWidth;
// ok is false when one fails, and f has its error.
func list[E, U, T any, W Input[U, T]](f *Filter[E], ws []W) (ps []velox.Pred[U], ok bool) {
	if err := CheckWidth(len(ws)); err != nil {
		f.refuse(err)
		return nil, false
	}
	ps = make([]velox.Pred[U], 0, len(ws))
	for _, w := range ws {
		p, ok := nested(f, w)
		if !ok {
			return nil, false
		}
		ps = append(ps, p)
	}
	return ps, true
}

// Not adds the negation of w, the input's not: a node matches when it does
// not match w. A nil w adds nothing.
func Not[E, T any, W Input[E, T]](f *Filter[E], w W) {
	if f.err != nil || w == nil {
		return
	}
	if p, ok := nested(f, w); ok {
		add(f, velox.Not(p))
	}
}

// And adds ws, the input's and: a node matches when it matches each of ws.
// An empty ws adds nothing. More than MaxFilterWidth fails with
// ErrFilterTooWide.
func And[E, T any, W Input[E, T]](f *Filter[E], ws []W) {
	if f.err != nil || ws == nil {
		return
	}
	if ps, ok := list(f, ws); ok {
		f.ps = append(f.ps, ps...)
	}
}

// Or adds ws, the input's or: a node matches when it matches one of ws. An
// empty ws adds nothing, as an empty and does, rather than matching no
// node. More than MaxFilterWidth fails with ErrFilterTooWide.
func Or[E, T any, W Input[E, T]](f *Filter[E], ws []W) {
	if f.err != nil || ws == nil {
		return
	}
	ps, ok := list(f, ws)
	switch {
	case !ok || len(ps) == 0:
	case len(ps) == 1:
		add(f, ps[0])
	default:
		add(f, velox.Or(ps...))
	}
}

// HasWith adds has<Edge>With: a node matches when a node of the edge
// matches each of ws, through has, the relation's HasWhere. An empty ws
// adds nothing. More than MaxFilterWidth fails with ErrFilterTooWide.
func HasWith[E, U, T any, W Input[U, T]](f *Filter[E], ws []W, has func(...velox.Pred[U]) velox.Pred[E]) {
	if f.err != nil || len(ws) == 0 {
		return
	}
	if ps, ok := list(f, ws); ok {
		add(f, has(ps...))
	}
}

// Is adds a Boolean predicate: p() when b is true and its negation when b
// is false. It is IsNil (p is the column's IsNull), NotNil (NotNull) and
// has<Edge> (the relation's Has).
func Is[E any](f *Filter[E], b *bool, p func() velox.Pred[E]) {
	switch {
	case f.err != nil || b == nil:
	case *b:
		add(f, p())
	default:
		add(f, velox.Not(p()))
	}
}

// Value adds op(*v), a predicate on one value of the column's Go type: EQ,
// NEQ, GT, GTE, LT, LTE, Contains, HasPrefix or HasSuffix.
func Value[E, V any](f *Filter[E], v *V, op func(V) velox.Pred[E]) {
	if f.err == nil && v != nil {
		add(f, op(*v))
	}
}

// ValueAs is Value of a value the input holds as W, which conv converts to
// the column's Go type V: a field of the application's own Go type, whose
// GraphQL scalar binds to its base type.
func ValueAs[E, W, V any](f *Filter[E], v *W, conv func(W) V, op func(V) velox.Pred[E]) {
	if f.err == nil && v != nil {
		add(f, op(conv(*v)))
	}
}

// List adds op(vs...), a predicate on a list of the column's Go type: In or
// NotIn. A non-nil empty list is a list of nothing, as the client sent it:
// In matches no node, and NotIn every node.
func List[E, V any](f *Filter[E], vs []V, op func(...V) velox.Pred[E]) {
	if f.err == nil && vs != nil {
		add(f, op(vs...))
	}
}

// ListAs is List of values the input holds as W, which conv converts to
// the column's Go type V.
func ListAs[E, W, V any](f *Filter[E], vs []W, conv func(W) V, op func(...V) velox.Pred[E]) {
	if f.err != nil || vs == nil {
		return
	}
	out := make([]V, len(vs))
	for i, v := range vs {
		out[i] = conv(v)
	}
	add(f, op(out...))
}

// ID adds op(k), k the key of id, an ID of the GraphQL type typ decoded with
// DecodeID[K]: EQ or NEQ on the key, or on a foreign key that holds typ's
// keys. An ID of another type fails with ErrWrongIDType, and a malformed one
// with ErrBadID.
func ID[K, E any](f *Filter[E], id *string, typ string, op func(K) velox.Pred[E]) {
	if f.err != nil || id == nil {
		return
	}
	if k, ok := decode[K](f, *id, typ); ok {
		add(f, op(k))
	}
}

// IDAs is ID of a column whose Go type V is not K, the Go type typ's keys
// decode as: conv converts.
func IDAs[K, E, V any](f *Filter[E], id *string, typ string, conv func(K) V, op func(V) velox.Pred[E]) {
	if f.err != nil || id == nil {
		return
	}
	if k, ok := decode[K](f, *id, typ); ok {
		add(f, op(conv(k)))
	}
}

// IDList adds op(ks...), ks the keys of ids, as ID decodes them: In or NotIn
// on the key, or on a foreign key. One ID of another type, or malformed,
// fails the filter.
func IDList[K, E any](f *Filter[E], ids []string, typ string, op func(...K) velox.Pred[E]) {
	if f.err != nil || ids == nil {
		return
	}
	if ks, ok := decodeAll(f, ids, typ, func(k K) K { return k }); ok {
		add(f, op(ks...))
	}
}

// IDListAs is IDList of a column whose Go type V is not K: conv converts.
func IDListAs[K, E, V any](f *Filter[E], ids []string, typ string, conv func(K) V, op func(...V) velox.Pred[E]) {
	if f.err != nil || ids == nil {
		return
	}
	if vs, ok := decodeAll(f, ids, typ, conv); ok {
		add(f, op(vs...))
	}
}

// decode is DecodeID[K](id, typ), with its error recorded in f.
func decode[K, E any](f *Filter[E], id, typ string) (K, bool) {
	k, err := DecodeID[K](id, typ)
	if err != nil {
		fail(f, err)
		return k, false
	}
	return k, true
}

// decodeAll is the keys of ids, converted by conv.
func decodeAll[K, E, V any](f *Filter[E], ids []string, typ string, conv func(K) V) ([]V, bool) {
	out := make([]V, len(ids))
	for i, id := range ids {
		k, ok := decode[K](f, id, typ)
		if !ok {
			return nil, false
		}
		out[i] = conv(k)
	}
	return out, true
}

// The conversions from the Go type a where input holds, the base type its
// scalar binds to, to the column's Go type of the application's: generated
// code passes them, instantiated, to ValueAs, ListAs, IDAs and IDListAs.

// integer is the Go integer types, and the types defined on them.
type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// AsString converts a string to the string type V.
func AsString[V, W ~string](w W) V { return V(w) }

// AsInt converts an integer to the integer type V.
func AsInt[V, W integer](w W) V { return V(w) }

// AsFloat converts a float to the float type V.
func AsFloat[V, W ~float32 | ~float64](w W) V { return V(w) }

// AsBool converts a bool to the bool type V.
func AsBool[V, W ~bool](w W) V { return V(w) }

// AsUUID converts a UUID, or the [16]byte of a key, to the UUID type V.
func AsUUID[V, W ~[16]byte](w W) V { return V(w) }
