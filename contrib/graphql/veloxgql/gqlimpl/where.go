package gqlimpl

import (
	"context"
	"reflect"
	"strconv"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Filter is a where input being turned into a condition on the entity of a
// velox.Table: the conditions the helpers added, or the first error. A where
// input's generated method PredicateGQL makes one, adds each predicate the
// input sets with one helper call, by column or relation index, and returns
// Filter.Pred; its method Pred is PredicateGQL, typed:
//
//	func (w *AccountWhereInput) PredicateGQL(ctx context.Context) (velox.Predicate, error) {
//		f := gqlimpl.NewFilter(ctx, "AccountWhereInput", velox.TableOf[User]())
//		if w == nil {
//			return f.Pred()
//		}
//		gqlimpl.Not(f, "not", w.Not)
//		gqlimpl.And(f, "and", w.And)
//		gqlimpl.Or(f, "or", w.Or)
//		gqlimpl.Value(f, "displayName", w.DisplayName, userColName, velox.PredEQ)
//		gqlimpl.List(f, "displayNameIn", w.DisplayNameIn, userColName, velox.PredIn)
//		gqlimpl.Has(f, "hasPosts", w.HasPosts, userEdgePosts, velox.TableOf[Post]())
//		gqlimpl.HasWith(f, "hasPostsWith", w.HasPostsWith, userEdgePosts, velox.TableOf[Post]())
//		return f.Pred()
//	}
//
// A helper does nothing for a field the input does not set, a nil pointer or
// a nil list, and nothing once the filter has an error: the first error
// stops it, and Pred returns it. None of them runs a query, so a refused
// filter fails before any statement is built. None of them is generic over
// the entity: they are compiled once, for every where input.
type Filter struct {
	ctx    context.Context
	input  string // the where input's GraphQL type, for errors
	t      velox.Table
	depth  int
	budget *budget // the where argument's
	ps     []velox.Predicate
	err    error
}

// NewFilter returns the Filter of a where input of the GraphQL type input,
// on t's entity.
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
//
// The filter fails, before any query runs, on an ID of another type
// (veloxgql.ErrWrongIDType) or a malformed one (veloxgql.ErrBadID); on a
// nesting deeper than MaxFilterDepth (veloxgql.ErrFilterTooDeep); on an and, an
// or, a has…With, an In or a NotIn of more than MaxFilterWidth entries
// (veloxgql.ErrFilterTooWide); and on a where argument of more than
// MaxFilterNodes inputs, predicates and values in all
// (veloxgql.ErrFilterTooLarge).
func NewFilter(ctx context.Context, input string, t velox.Table) *Filter {
	st, ok := ctx.Value(stateKey{}).(*state)
	if !ok {
		lim := veloxgql.LimitsFromContext(ctx)
		st = &state{budget: &budget{left: lim.MaxFilterNodes, limits: lim}}
	}
	f := &Filter{ctx: ctx, input: input, t: t, depth: st.depth, budget: st.budget}
	if err := checkDepth(f.depth, f.budget.limits.MaxFilterDepth); err != nil {
		f.refuse("", err)
		return f
	}
	f.charge("", 1)
	return f
}

// Pred is the condition the filter's input stands for: the And of its
// conditions, one of them alone, or the And of none, which holds for every
// row, for an input that sets nothing. It returns the filter's error
// instead, if any.
func (f *Filter) Pred() (velox.Predicate, error) {
	switch {
	case f.err != nil:
		return velox.Predicate{}, f.err
	case len(f.ps) == 1:
		return f.ps[0], nil
	}
	return velox.AndPredicates(f.ps...), nil
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
func (f *Filter) charge(field string, n int) (ok bool) {
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
func (f *Filter) refuse(field string, err error) {
	if f.err != nil {
		return
	}
	at := f.input
	if field != "" {
		at += "." + field
	}
	f.err = gqlcore.Within(at, err)
}

// add adds p to the filter's conditions, a predicate of field, which counts
// one against the budget, and n more for its values.
func (f *Filter) add(field string, n int, p velox.Predicate) {
	if f.charge(field, 1+n) {
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

// Where is a where input of the GraphQL code: a pointer to a generated
// struct, whose PredicateGQL is its condition, untyped. The helpers that
// nest inputs are generic over it, a pointer, so that they are compiled once
// for every input.
type Where interface {
	comparable
	PredicateGQL(ctx context.Context) (velox.Predicate, error)
}

// nested is the condition of w, an input nested in f's at field, one level
// deeper (in the context PredicateGQL is called with); a nil w is an input
// that sets nothing, which holds for every row. ok is false when w fails, and
// f has its error.
func nested[W Where](f *Filter, field string, w W) (p velox.Predicate, ok bool) {
	var none W
	if w == none {
		return velox.AndPredicates(), true
	}
	p, err := w.PredicateGQL(context.WithValue(f.ctx, stateKey{}, &state{depth: f.depth + 1, budget: f.budget}))
	if err != nil {
		f.refuse(field, err)
		return p, false
	}
	return p, true
}

// list is the conditions of ws, the inputs nested in f's at field, after
// checkWidth; ok is false when one fails, and f has its error, which names
// the entry, as "or[3]".
func list[W Where](f *Filter, field string, ws []W) (ps []velox.Predicate, ok bool) {
	if !f.width(field, len(ws)) {
		return nil, false
	}
	ps = make([]velox.Predicate, 0, len(ws))
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
// which the errors of the field name, and the field's value; one on a column
// takes the column's index, as velox-gen numbers it, and the operator, and
// one on a relation the relation's index and its target's Table.

// Not adds the negation of w, the input's not: a node matches when it does
// not match w. A nil w adds nothing.
func Not[W Where](f *Filter, field string, w W) {
	var none W
	if f.err != nil || w == none {
		return
	}
	if p, ok := nested(f, field, w); ok {
		f.add(field, 0, velox.NotPredicate(p))
	}
}

// And adds ws, the input's and: a node matches when it matches each of ws.
// An empty ws adds nothing. More than MaxFilterWidth fails with
// ErrFilterTooWide.
func And[W Where](f *Filter, field string, ws []W) {
	if f.err != nil || ws == nil {
		return
	}
	if ps, ok := list(f, field, ws); ok && f.charge(field, 1) {
		f.ps = append(f.ps, ps...)
	}
}

// Or adds ws, the input's or: a node matches when it matches one of ws. An
// empty ws matches no node, as an empty In does; a nil ws adds nothing. More
// than MaxFilterWidth fails with ErrFilterTooWide.
func Or[W Where](f *Filter, field string, ws []W) {
	if f.err != nil || ws == nil {
		return
	}
	ps, ok := list(f, field, ws)
	switch {
	case !ok:
	case len(ps) == 1:
		f.add(field, 0, ps[0])
	default:
		f.add(field, 0, velox.OrPredicates(ps...))
	}
}

// HasWith adds has<Edge>With: a node matches when a node of relation edge,
// of target, matches each of ws. An empty ws adds nothing. More than
// MaxFilterWidth fails with ErrFilterTooWide.
func HasWith[W Where](f *Filter, field string, ws []W, edge int, target velox.Table) {
	if f.err != nil || len(ws) == 0 {
		return
	}
	if ps, ok := list(f, field, ws); ok {
		f.add(field, 0, f.t.Has(edge, target, ps...))
	}
}

// Has adds has<Edge>: a node matches when relation edge holds a node of
// target, for a true b, and when it holds none, for a false one.
func Has(f *Filter, field string, b *bool, edge int, target velox.Table) {
	if f.err == nil && b != nil {
		f.is(field, *b, f.t.Has(edge, target))
	}
}

// Is adds IsNil (op velox.PredIsNull) or NotNil (velox.PredNotNull) of
// column col: the predicate for a true b, and its negation for a false one.
func Is(f *Filter, field string, b *bool, col int, op velox.PredOp) {
	if f.err == nil && b != nil {
		f.is(field, *b, f.t.Pred(col, op, nil))
	}
}

// is adds p for a true b, and its negation for a false one.
func (f *Filter) is(field string, b bool, p velox.Predicate) {
	if !b {
		p = velox.NotPredicate(p)
	}
	f.add(field, 0, p)
}

// Value adds op on column col with *v, v a pointer to one value: EQ, NEQ,
// GT, GTE, LT, LTE, Contains, HasPrefix or HasSuffix. A value of the base
// type the field's scalar binds to is converted to the column's Go type, as
// velox.Table.Convert does.
func Value(f *Filter, field string, v any, col int, op velox.PredOp) {
	if f.err != nil {
		return
	}
	x, ok := deref(v)
	if ok {
		f.add(field, 0, f.pred(col, op, x))
	}
}

// List adds op on column col with the values of vs, a slice: In or NotIn. A
// non-nil empty list is a list of nothing, as the client sent it: In
// matches no node, and NotIn every node. More than MaxFilterWidth values
// fail with ErrFilterTooWide. The values are converted as Value's are.
func List(f *Filter, field string, vs any, col int, op velox.PredOp) {
	if f.err != nil {
		return
	}
	xs, ok := values(vs)
	if ok && f.width(field, len(xs)) {
		f.add(field, len(xs), f.pred(col, op, xs))
	}
}

// pred is the condition op on column col with v, a value or for In and
// NotIn a []any, each converted to the column's Go type.
func (f *Filter) pred(col int, op velox.PredOp, v any) velox.Predicate {
	if xs, ok := v.([]any); ok {
		for i, x := range xs {
			c, err := f.t.Convert(col, x)
			if err != nil {
				return f.t.Pred(col, op, x) // the condition's error
			}
			xs[i] = c
		}
		return f.t.Pred(col, op, xs)
	}
	c, err := f.t.Convert(col, v)
	if err != nil {
		return f.t.Pred(col, op, v)
	}
	return f.t.Pred(col, op, c)
}

// deref is the value v points to; ok is false for a nil pointer.
func deref(v any) (x any, ok bool) {
	switch v := v.(type) {
	case *string:
		return deref1(v)
	case *int64:
		return deref1(v)
	case *int:
		return deref1(v)
	case *int32:
		return deref1(v)
	case *bool:
		return deref1(v)
	case *float64:
		return deref1(v)
	case *time.Time:
		return deref1(v)
	case nil:
		return nil, false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, false
	}
	return rv.Elem().Interface(), true
}

func deref1[V any](p *V) (any, bool) {
	if p == nil {
		return nil, false
	}
	return *p, true
}

// values is the values of vs, a slice, as a []any; ok is false for a nil
// slice.
func values(vs any) (xs []any, ok bool) {
	switch vs := vs.(type) {
	case []string:
		return anys(vs)
	case []int64:
		return anys(vs)
	case []int:
		return anys(vs)
	case []int32:
		return anys(vs)
	case []float64:
		return anys(vs)
	case nil:
		return nil, false
	}
	rv := reflect.ValueOf(vs)
	if rv.Kind() != reflect.Slice || rv.IsNil() {
		return nil, false
	}
	xs = make([]any, rv.Len())
	for i := range xs {
		xs[i] = rv.Index(i).Interface()
	}
	return xs, true
}

func anys[V any](vs []V) ([]any, bool) {
	if vs == nil {
		return nil, false
	}
	xs := make([]any, len(vs))
	for i, v := range vs {
		xs[i] = v
	}
	return xs, true
}

// width is checkWidth of field's list of n, with its error recorded in f.
func (f *Filter) width(field string, n int) bool {
	if err := checkWidth(n, f.budget.limits.MaxFilterWidth); err != nil {
		f.refuse(field, err)
		return false
	}
	return true
}

// ID adds op on column col with the key of id, an ID of the GraphQL type typ
// decoded with DecodeID[K]: EQ, NEQ, GT, GTE, LT or LTE on the key, or on a
// foreign key that holds typ's keys. The key is converted to the column's Go
// type as Value's values are. An ID of another type fails with
// ErrWrongIDType, and a malformed one with ErrBadID.
func ID[K any](f *Filter, field string, id *string, typ string, col int, op velox.PredOp) {
	if f.err != nil || id == nil {
		return
	}
	if k, ok := decode[K](f, field, *id, typ); ok {
		f.add(field, 0, f.pred(col, op, k))
	}
}

// IDList adds op on column col with the keys of ids, as ID decodes them: In
// or NotIn on the key, or on a foreign key. One ID of another type, or
// malformed, fails the filter, and so do more than MaxFilterWidth IDs.
func IDList[K any](f *Filter, field string, ids []string, typ string, col int, op velox.PredOp) {
	if f.err != nil || ids == nil || !f.width(field, len(ids)) {
		return
	}
	if ks, ok := decodeAll[K](f, field, ids, typ); ok {
		f.add(field, len(ks), f.pred(col, op, ks))
	}
}

// decode is DecodeID[K](id, typ), with its error recorded in f as field's.
func decode[K any](f *Filter, field, id, typ string) (K, bool) {
	k, err := veloxgql.DecodeID[K](id, typ)
	if err != nil {
		f.refuse(field, err)
		return k, false
	}
	return k, true
}

// decodeAll is the keys of ids, as a []any.
func decodeAll[K any](f *Filter, field string, ids []string, typ string) ([]any, bool) {
	out := make([]any, len(ids))
	for i, id := range ids {
		k, ok := decode[K](f, field, id, typ)
		if !ok {
			return nil, false
		}
		out[i] = k
	}
	return out, true
}

// The FK helpers are ID, IDList and Is of a foreign key, scoped to what its
// relation edge lets the reader see, the target's Read policy: a row it
// hides reads as null, to the edge and so to every predicate of its key, or
// a filter would say which hidden row another row links to. With no Read
// policy on target, the predicate is the column's alone, and there is no
// EXISTS.
//
// A WithRewrite in the application's own query is not known here, nor is it
// to the edge's Has.

// FKID is ID of a foreign key: with a Read policy on target, the key's
// predicate and the edge's Has.
func FKID[K any](f *Filter, field string, id *string, typ string, col int, op velox.PredOp, edge int, target velox.Table) {
	if f.err != nil || id == nil {
		return
	}
	if k, ok := decode[K](f, field, *id, typ); ok {
		f.add(field, 0, f.scoped(f.pred(col, op, k), edge, target))
	}
}

// FKIDList is IDList of a foreign key, scoped as FKID is.
func FKIDList[K any](f *Filter, field string, ids []string, typ string, col int, op velox.PredOp, edge int, target velox.Table) {
	if f.err != nil || ids == nil || !f.width(field, len(ids)) {
		return
	}
	if ks, ok := decodeAll[K](f, field, ids, typ); ok {
		f.add(field, len(ks), f.scoped(f.pred(col, op, ks), edge, target))
	}
}

// FKIs is Is of a foreign key: with a Read policy on target, IsNil is the
// edge's Has negated, and NotNil the edge's Has, as a hidden target counts
// as none.
func FKIs(f *Filter, field string, b *bool, col int, op velox.PredOp, edge int, target velox.Table) {
	if f.err != nil || b == nil {
		return
	}
	p := f.t.Pred(col, op, nil)
	if target.HasReadPolicy() {
		p = f.t.Has(edge, target)
		if op == velox.PredIsNull {
			p = velox.NotPredicate(p)
		}
	}
	f.is(field, *b, p)
}

// scoped is p, a predicate on the key of relation edge, and with a Read
// policy on target the edge's Has too.
func (f *Filter) scoped(p velox.Predicate, edge int, target velox.Table) velox.Predicate {
	if !target.HasReadPolicy() {
		return p
	}
	return velox.AndPredicates(p, f.t.Has(edge, target))
}

// The conversions from the Go type an input holds, the base type its scalar
// binds to, to the field's Go type of the application's: generated code
// passes them, instantiated, to the mutation helpers.

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
