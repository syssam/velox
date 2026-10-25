package gqlimpl

import (
	"context"
	"strconv"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
)

// Filter is a where input of E's being turned into a condition: the
// conditions the helpers added, or the first error. A where input's
// generated method Pred makes one, adds each predicate the input sets with
// one helper call, and returns Filter.Pred:
//
//	func (w *AccountWhereInput) Pred(ctx context.Context) (velox.Pred[User], error) {
//		f := gqlimpl.NewFilter[User](ctx, "AccountWhereInput")
//		if w == nil {
//			return f.Pred()
//		}
//		gqlimpl.Not(f, "not", w.Not)
//		gqlimpl.And(f, "and", w.And)
//		gqlimpl.Or(f, "or", w.Or)
//		gqlimpl.Value(f, "displayName", w.DisplayName, Users.Name.EQ)
//		gqlimpl.List(f, "displayNameIn", w.DisplayNameIn, Users.Name.In)
//		gqlimpl.Is(f, "hasPosts", w.HasPosts, Users.Posts.Has)
//		gqlimpl.HasWith(f, "hasPostsWith", w.HasPostsWith, Users.Posts.HasWhere)
//		return f.Pred()
//	}
//
// A helper does nothing for a field the input does not set, a nil pointer or
// a nil list, and nothing once the filter has an error: the first error
// stops it, and Pred returns it. None of them runs a query, so a refused
// filter fails before any statement is built.
type Filter[E any] struct {
	ctx    context.Context
	input  string // the where input's GraphQL type, for errors
	depth  int
	budget *budget // the where argument's
	ps     []velox.Pred[E]
	err    error
}

// NewFilter returns the Filter of a where input of the GraphQL type input,
// on E.
//
// The depth, how deeply the input is nested in the where argument, and the
// argument's node budget travel in ctx, under one unexported key. A context
// without them starts a where argument: depth 0, a full budget of
// MaxFilterNodes, and the limits of veloxgql.LimitsFromContext(ctx). So each
// call of a where input's Pred with a context of the application's has its own
// budget, however often the context is used. A nested input's Pred is called
// with a context of the helpers', one level deeper (inside each not, and, or
// and has<Edge>With) and with the same budget and limits, so that every input
// of the argument has them; that context never leaves the helpers. A depth over
// MaxFilterDepth fails the filter with ErrFilterTooDeep. The input, each input
// nested in it, each predicate they set and each value of their In and NotIn
// lists count one against the budget, and over it the filter fails with
// ErrFilterTooLarge. So the whole argument is bounded, even when it holds one
// input many times over, as a GraphQL variable used at many places does. One
// where argument's Pred calls run on one goroutine.
func NewFilter[E any](ctx context.Context, input string) *Filter[E] {
	st, ok := ctx.Value(stateKey{}).(*state)
	if !ok {
		lim := veloxgql.LimitsFromContext(ctx)
		st = &state{budget: &budget{left: lim.MaxFilterNodes, limits: lim}}
	}
	f := &Filter[E]{ctx: ctx, input: input, depth: st.depth, budget: st.budget}
	if err := checkDepth(f.depth, f.budget.limits.MaxFilterDepth); err != nil {
		f.refuse("", err)
		return f
	}
	charge(f, "", 1)
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

// budget is what is left of a where argument's MaxFilterNodes, and the
// argument's limits.
type budget struct {
	left   int
	limits veloxgql.Limits
}

// state is what a where argument's context carries to a nested input: its
// depth and the argument's budget.
type state struct {
	depth  int
	budget *budget
}

// stateKey is the context key of a state.
type stateKey struct{}

// charge counts n against the where argument's budget, for the field of
// f's input; ok is false when that is over it, and f has the error.
func charge[E any](f *Filter[E], field string, n int) (ok bool) {
	f.budget.left -= n
	if f.budget.left < 0 {
		f.refuse(field, InputErrorf("%w: the where argument has more than %d inputs, predicates and list values", veloxgql.ErrFilterTooLarge, f.budget.limits.MaxFilterNodes))
		return false
	}
	return true
}

// refuse records err, an error of field of the filter's input ("" for the
// input itself), as the filter's error, unless it has one already. The
// error's text is "veloxgql: <Input>.<field>: <err>", err's own "veloxgql: "
// left out, so that the error of an input nested in a field names the path
// to it, and it keeps err's code.
func (f *Filter[E]) refuse[_ velox.Lazy[E]](field string, err error) {
	if f.err != nil {
		return
	}
	at := f.input
	if field != "" {
		at += "." + field
	}
	f.err = shared.Within(at, err)
}

// add adds p to the filter's conditions, a predicate of field, which counts
// one against the budget, and n more for its values.
func add[E any](f *Filter[E], field string, n int, p velox.Pred[E]) {
	if charge(f, field, 1+n) {
		f.ps = append(f.ps, p)
	}
}

// checkDepth checks depth, how deeply a where input is nested, against
// most, the MaxFilterDepth: deeper fails with ErrFilterTooDeep.
func checkDepth(depth, most int) error {
	if depth > most {
		return InputErrorf("%w: it is nested %d deep, and the most is %d", veloxgql.ErrFilterTooDeep, depth, most)
	}
	return nil
}

// checkWidth checks n, the length of a list in a where input (of inputs, or
// of In or NotIn values), against most, the MaxFilterWidth: more fails with
// ErrFilterTooWide.
func checkWidth(n, most int) error {
	if n > most {
		return InputErrorf("%w: it has %d entries, and the most is %d", veloxgql.ErrFilterTooWide, n, most)
	}
	return nil
}

// Input is a where input of the GraphQL code: a pointer to the generated
// struct T, whose Pred is its condition on E.
type Input[E, T any] interface {
	*T
	Pred(ctx context.Context) (velox.Pred[E], error)
}

// nested is the condition of w, an input nested in f's at field, one level
// deeper (in the context Pred is called with); a nil w is an input that sets
// nothing, which holds for every row. ok is false when w fails, and f has its
// error.
func nested[E, U, T any, W Input[U, T]](f *Filter[E], field string, w W) (p velox.Pred[U], ok bool) {
	if w == nil {
		return velox.And[U](), true
	}
	p, err := w.Pred(context.WithValue(f.ctx, stateKey{}, &state{depth: f.depth + 1, budget: f.budget}))
	if err != nil {
		f.refuse(field, err)
		return p, false
	}
	return p, true
}

// list is the conditions of ws, the inputs nested in f's at field, after
// checkWidth; ok is false when one fails, and f has its error, which names
// the entry, as "or[3]".
func list[E, U, T any, W Input[U, T]](f *Filter[E], field string, ws []W) (ps []velox.Pred[U], ok bool) {
	if err := checkWidth(len(ws), f.budget.limits.MaxFilterWidth); err != nil {
		f.refuse(field, err)
		return nil, false
	}
	ps = make([]velox.Pred[U], 0, len(ws))
	for i, w := range ws {
		p, ok := nested(f, field+"["+strconv.Itoa(i)+"]", w)
		if !ok {
			return nil, false
		}
		ps = append(ps, p)
	}
	return ps, true
}

// Each helper takes the filter, the GraphQL name of the input's field,
// which the errors of the field name, and the field's value.

// Not adds the negation of w, the input's not: a node matches when it does
// not match w. A nil w adds nothing.
func Not[E, T any, W Input[E, T]](f *Filter[E], field string, w W) {
	if f.err != nil || w == nil {
		return
	}
	if p, ok := nested(f, field, w); ok {
		add(f, field, 0, velox.Not(p))
	}
}

// And adds ws, the input's and: a node matches when it matches each of ws.
// An empty ws adds nothing. More than MaxFilterWidth fails with
// ErrFilterTooWide.
func And[E, T any, W Input[E, T]](f *Filter[E], field string, ws []W) {
	if f.err != nil || ws == nil {
		return
	}
	if ps, ok := list(f, field, ws); ok && charge(f, field, 1) {
		f.ps = append(f.ps, ps...)
	}
}

// Or adds ws, the input's or: a node matches when it matches one of ws. An
// empty ws adds nothing, as an empty and does, rather than matching no
// node. More than MaxFilterWidth fails with ErrFilterTooWide.
func Or[E, T any, W Input[E, T]](f *Filter[E], field string, ws []W) {
	if f.err != nil || ws == nil {
		return
	}
	ps, ok := list(f, field, ws)
	switch {
	case !ok || len(ps) == 0:
	case len(ps) == 1:
		add(f, field, 0, ps[0])
	default:
		add(f, field, 0, velox.Or(ps...))
	}
}

// HasWith adds has<Edge>With: a node matches when a node of the edge
// matches each of ws, through has, the relation's HasWhere. An empty ws
// adds nothing. More than MaxFilterWidth fails with ErrFilterTooWide.
func HasWith[E, U, T any, W Input[U, T]](f *Filter[E], field string, ws []W, has func(...velox.Pred[U]) velox.Pred[E]) {
	if f.err != nil || len(ws) == 0 {
		return
	}
	if ps, ok := list(f, field, ws); ok {
		add(f, field, 0, has(ps...))
	}
}

// Is adds a Boolean predicate: p() when b is true and its negation when b
// is false. It is IsNil (p is the column's IsNull), NotNil (NotNull) and
// has<Edge> (the relation's Has).
func Is[E any](f *Filter[E], field string, b *bool, p func() velox.Pred[E]) {
	switch {
	case f.err != nil || b == nil:
	case *b:
		add(f, field, 0, p())
	default:
		add(f, field, 0, velox.Not(p()))
	}
}

// Value adds op(*v), a predicate on one value of the column's Go type: EQ,
// NEQ, GT, GTE, LT, LTE, Contains, HasPrefix or HasSuffix.
func Value[E, V any](f *Filter[E], field string, v *V, op func(V) velox.Pred[E]) {
	if f.err == nil && v != nil {
		add(f, field, 0, op(*v))
	}
}

// ValueAs is Value of a value the input holds as W, which conv converts to
// the column's Go type V: a field of the application's own Go type, whose
// GraphQL scalar binds to its base type.
func ValueAs[E, W, V any](f *Filter[E], field string, v *W, conv func(W) V, op func(V) velox.Pred[E]) {
	if f.err == nil && v != nil {
		add(f, field, 0, op(conv(*v)))
	}
}

// List adds op(vs...), a predicate on a list of the column's Go type: In or
// NotIn. A non-nil empty list is a list of nothing, as the client sent it:
// In matches no node, and NotIn every node. More than MaxFilterWidth values
// fail with ErrFilterTooWide.
func List[E, V any](f *Filter[E], field string, vs []V, op func(...V) velox.Pred[E]) {
	if f.err == nil && vs != nil && width(f, field, len(vs)) {
		add(f, field, len(vs), op(vs...))
	}
}

// ListAs is List of values the input holds as W, which conv converts to
// the column's Go type V.
func ListAs[E, W, V any](f *Filter[E], field string, vs []W, conv func(W) V, op func(...V) velox.Pred[E]) {
	if f.err != nil || vs == nil || !width(f, field, len(vs)) {
		return
	}
	out := make([]V, len(vs))
	for i, v := range vs {
		out[i] = conv(v)
	}
	add(f, field, len(vs), op(out...))
}

// width is checkWidth of field's list of n, with its error recorded in f.
func width[E any](f *Filter[E], field string, n int) bool {
	if err := checkWidth(n, f.budget.limits.MaxFilterWidth); err != nil {
		f.refuse(field, err)
		return false
	}
	return true
}

// ID adds op(k), k the key of id, an ID of the GraphQL type typ decoded with
// DecodeID[K]: EQ or NEQ on the key, or on a foreign key that holds typ's
// keys. An ID of another type fails with ErrWrongIDType, and a malformed one
// with ErrBadID.
func ID[K, E any](f *Filter[E], field string, id *string, typ string, op func(K) velox.Pred[E]) {
	if f.err != nil || id == nil {
		return
	}
	if k, ok := decode[K](f, field, *id, typ); ok {
		add(f, field, 0, op(k))
	}
}

// IDAs is ID of a column whose Go type V is not K, the Go type typ's keys
// decode as: conv converts.
func IDAs[K, E, V any](f *Filter[E], field string, id *string, typ string, conv func(K) V, op func(V) velox.Pred[E]) {
	if f.err != nil || id == nil {
		return
	}
	if k, ok := decode[K](f, field, *id, typ); ok {
		add(f, field, 0, op(conv(k)))
	}
}

// IDList adds op(ks...), ks the keys of ids, as ID decodes them: In or NotIn
// on the key, or on a foreign key. One ID of another type, or malformed,
// fails the filter, and so do more than MaxFilterWidth IDs.
func IDList[K, E any](f *Filter[E], field string, ids []string, typ string, op func(...K) velox.Pred[E]) {
	if f.err != nil || ids == nil || !width(f, field, len(ids)) {
		return
	}
	if ks, ok := decodeAll(f, field, ids, typ, func(k K) K { return k }); ok {
		add(f, field, len(ks), op(ks...))
	}
}

// IDListAs is IDList of a column whose Go type V is not K: conv converts.
func IDListAs[K, E, V any](f *Filter[E], field string, ids []string, typ string, conv func(K) V, op func(...V) velox.Pred[E]) {
	if f.err != nil || ids == nil || !width(f, field, len(ids)) {
		return
	}
	if vs, ok := decodeAll(f, field, ids, typ, conv); ok {
		add(f, field, len(vs), op(vs...))
	}
}

// decode is DecodeID[K](id, typ), with its error recorded in f as field's.
func decode[K, E any](f *Filter[E], field, id, typ string) (K, bool) {
	k, err := veloxgql.DecodeID[K](id, typ)
	if err != nil {
		f.refuse(field, err)
		return k, false
	}
	return k, true
}

// decodeAll is the keys of ids, converted by conv.
func decodeAll[K, E, V any](f *Filter[E], field string, ids []string, typ string, conv func(K) V) ([]V, bool) {
	out := make([]V, len(ids))
	for i, id := range ids {
		k, ok := decode[K](f, field, id, typ)
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

// Integer is the Go integer types, and the types defined on them.
type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// AsString converts a string to the string type V.
func AsString[V, W ~string](w W) V { return V(w) }

// AsInt converts an integer to the integer type V.
func AsInt[V, W Integer](w W) V { return V(w) }

// AsFloat converts a float to the float type V.
func AsFloat[V, W ~float32 | ~float64](w W) V { return V(w) }

// AsBool converts a bool to the bool type V.
func AsBool[V, W ~bool](w W) V { return V(w) }

// AsUUID converts a UUID, or the [16]byte of a key, to the UUID type V.
func AsUUID[V, W ~[16]byte](w W) V { return V(w) }
