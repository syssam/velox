package gqlgraph

import (
	"context"
	"errors"
	"strconv"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// Filter is a where input being turned into a condition on the entity of a
// untyped.Table: the conditions the helpers added, or the first error. A
// generated where input's Predicate is the function Predicate, which makes
// one and adds the predicates the input sets with the helper of each field
// (WhereField), by column or relation index. A where input written by hand
// calls the helpers itself, and nests inputs through their method
// expressions:
//
//	func (w *AccountWhereInput) predicate(ctx context.Context) (velox.Predicate, error) {
//		f := gqlgraph.NewFilter(ctx, &userType)
//		if w == nil {
//			return f.Pred()
//		}
//		gqlgraph.Not(f, "not", w.Not, (*AccountWhereInput).predicate)
//		gqlgraph.And(f, "and", w.And, (*AccountWhereInput).predicate)
//		gqlgraph.Or(f, "or", w.Or, (*AccountWhereInput).predicate)
//		gqlgraph.IDField(f, "id", userColID, gqlgraph.Ops[int64]{EQ: w.ID, In: w.IDIn})
//		gqlgraph.Field(f, "displayName", userColName, gqlgraph.Ops[string]{EQ: w.DisplayName, In: w.DisplayNameIn})
//		gqlgraph.EdgeIDField(f, "teamID", userColTeamID, userEdgeTeam, &teamType, gqlgraph.Ops[int64]{EQ: w.TeamID})
//		gqlgraph.Has(f, "hasPosts", w.HasPosts, userEdgePosts, untyped.TableOf[Post]())
//		gqlgraph.HasWith(f, "hasPostsWith", w.HasPostsWith, (*PostWhereInput).Predicate, userEdgePosts, untyped.TableOf[Post]())
//		return f.Pred()
//	}
//
// A helper does nothing for a field the input does not set, a nil pointer or
// a nil list, and nothing once the filter has an error: the first error
// stops it, and Pred returns it. None of them runs a query, so a refused
// filter fails before any statement is built. None of them is generic over
// the entity: they are compiled once, for every where input, and those
// generic over a field's Go type once for each type's shape.
type Filter struct {
	ctx    context.Context
	typ    string // the GraphQL type: errors name its where input, "<Type>WhereInput"
	t      untyped.Table
	depth  int
	budget *budget // the where argument's
	veloxe bool    // Type.Veloxe: veloxe's semantics, see Type
	ps     []velox.Predicate
	err    error
}

// name is the GraphQL name of a field of a where input, as its errors name
// it: base, the name of the field or edge the input field is about, and op,
// the suffix of its predicate ("NEQ"; "" for EQ and for a field that is not
// a predicate's), which are joined only for an error.
type name struct{ base, op string }

func (n name) String() string { return n.base + n.op }

// NewFilter returns the Filter of a where input of c's type,
// "<Type>WhereInput", on the entity of c's Table.
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
// The filter fails, before any query runs, on a key the column cannot hold;
// on a nesting deeper than MaxFilterDepth (veloxgql.ErrFilterTooDeep); on an and, an
// or, a has…With, an In or a NotIn of more than MaxFilterWidth entries
// (veloxgql.ErrFilterTooWide); and on a where argument of more than
// MaxFilterNodes inputs, predicates and values in all
// (veloxgql.ErrFilterTooLarge).
func NewFilter(ctx context.Context, c *Type) *Filter {
	st, ok := ctx.Value(stateKey{}).(*state)
	if !ok {
		lim := veloxgql.LimitsFromContext(ctx)
		st = &state{budget: &budget{left: lim.MaxFilterNodes, limits: lim}}
	}
	f := &Filter{ctx: ctx, typ: c.Name, t: c.table(), depth: st.depth, budget: st.budget, veloxe: c.Veloxe}
	if err := checkDepth(f.depth, f.budget.limits.MaxFilterDepth); err != nil {
		f.refuse(name{}, err)
		return f
	}
	f.charge(name{}, 1)
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
	case f.veloxe && f.depth > 0 && len(f.ps) == 0:
		// veloxe refused a nested input that sets no predicate; its and
		// skips one (list), its not, or and has<Edge>With fail.
		return velox.Predicate{}, &gqlcore.Error{Code: gqlcore.CodeBadUserInput, Msg: "empty predicate " + f.typ + "WhereInput", Err: errEmptyPredicate}
	case len(f.ps) == 1:
		return f.ps[0], nil
	}
	return untyped.And(f.ps...), nil
}

// errEmptyPredicate is what Pred's error wraps, under Type.Veloxe, for a
// nested input that sets no predicate.
var errEmptyPredicate = errors.New("empty predicate")

// emptyPredicate reports whether err is Pred's own error for an input that
// sets no predicate, not one nested deeper that a helper wrapped: veloxe's
// and skipped only an entry that was itself empty.
func emptyPredicate(err error) bool {
	e, ok := err.(*gqlcore.Error)           //nolint:errorlint // the input's own error, never a wrapped one
	return ok && e.Err == errEmptyPredicate //nolint:errorlint // likewise
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
func (f *Filter) charge(field name, n int) (ok bool) {
	if f.veloxe {
		return true // veloxe had no budget
	}
	f.budget.left -= n
	if f.budget.left < 0 {
		f.refuse(field, InputErrorf("%w: the where argument has more than %d inputs, predicates and list values", veloxgql.ErrFilterTooLarge, f.budget.limits.MaxFilterNodes))
		return false
	}
	return true
}

// refuse records err, an error of field of the filter's input (none for the
// input itself), as the filter's error, unless it has one already. The
// error's text is "veloxgql: <Input>.<field>: <err>", err's own "veloxgql: "
// left out, so that the error of an input nested in a field names the path
// to it, and it keeps err's code.
func (f *Filter) refuse(field name, err error) {
	if f.err != nil {
		return
	}
	at := f.typ + "WhereInput"
	if field != (name{}) {
		at += "." + field.String()
	}
	f.err = gqlcore.Within(at, err)
}

// add adds p to the filter's conditions, a predicate of field, which counts
// one against the budget, and n more for its values.
func (f *Filter) add(field name, n int, p velox.Predicate) {
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

// Cond is the condition of a where input W, untyped: a method expression,
// such as a generated input's (*XWhereInput).Predicate, whose Pred is it,
// typed. The helpers that nest inputs take one. W is a pointer, or the
// nested input as an any, so that they are compiled once for every input.
type Cond[W comparable] func(w W, ctx context.Context) (velox.Predicate, error)

// nested is the condition of w, an input nested in f's at field, one level
// deeper (in the context cond is called with); a nil w is an input that sets
// nothing, which holds for every row. ok is false when w fails, and f has its
// error.
func nested[W comparable](f *Filter, field name, w W, cond Cond[W]) (p velox.Predicate, ok bool) {
	p, err := nestedCond(f, w, cond)
	if err != nil {
		f.refuse(field, err)
		return p, false
	}
	return p, true
}

// nestedCond is the condition of w, nested in f's input, without f's error.
func nestedCond[W comparable](f *Filter, w W, cond Cond[W]) (velox.Predicate, error) {
	var none W
	if w == none {
		return untyped.And(), nil
	}
	return cond(w, context.WithValue(f.ctx, stateKey{}, &state{depth: f.depth + 1, budget: f.budget}))
}

// list is the conditions of ws, the inputs nested in f's at field, after
// checkWidth; ok is false when one fails, and f has its error, which names
// the entry, as "or[3]". Under Type.Veloxe a null entry is left out, as
// veloxe left it out, and with skipEmpty (an and's) so is an entry that sets
// no predicate.
func list[W comparable](f *Filter, field string, ws []W, cond Cond[W], skipEmpty bool) (ps []velox.Predicate, ok bool) {
	if !f.width(name{base: field}, len(ws)) {
		return nil, false
	}
	var none W
	ps = make([]velox.Predicate, 0, len(ws))
	for i, w := range ws {
		if f.veloxe && w == none {
			continue
		}
		p, err := nestedCond(f, w, cond)
		if skipEmpty && f.veloxe && emptyPredicate(err) {
			continue
		}
		if err != nil {
			f.refuse(name{base: field + "[" + strconv.Itoa(i) + "]"}, err)
			return nil, false
		}
		ps = append(ps, p)
	}
	return ps, true
}

// Each helper takes the filter, the GraphQL name of the input's field,
// which the errors of the field name, and the field's value; one on a column
// takes the column's index, as velox-gen numbers it, and one on a relation
// the relation's index and its target.

// Not adds the negation of w, the input's not: a node matches when it does
// not match w. A nil w adds nothing.
func Not[W comparable](f *Filter, field string, w W, cond Cond[W]) {
	var none W
	if f.err != nil || w == none {
		return
	}
	if p, ok := nested(f, name{base: field}, w, cond); ok {
		f.add(name{base: field}, 0, untyped.Not(p))
	}
}

// And adds ws, the input's and: a node matches when it matches each of ws.
// An empty ws adds nothing. More than MaxFilterWidth fails with
// ErrFilterTooWide.
func And[W comparable](f *Filter, field string, ws []W, cond Cond[W]) {
	if f.err != nil || ws == nil {
		return
	}
	if ps, ok := list(f, field, ws, cond, true); ok && f.charge(name{base: field}, 1) {
		f.ps = append(f.ps, ps...)
	}
}

// Or adds ws, the input's or: a node matches when it matches one of ws. An
// empty ws matches no node, as an empty In does; a nil ws adds nothing. More
// than MaxFilterWidth fails with ErrFilterTooWide. Under Type.Veloxe an
// empty ws, or one of only nulls, adds nothing.
func Or[W comparable](f *Filter, field string, ws []W, cond Cond[W]) {
	if f.err != nil || ws == nil {
		return
	}
	ps, ok := list(f, field, ws, cond, false)
	switch {
	case !ok, f.veloxe && len(ps) == 0:
	case len(ps) == 1:
		f.add(name{base: field}, 0, ps[0])
	default:
		f.add(name{base: field}, 0, untyped.Or(ps...))
	}
}

// HasWith adds has<Edge>With: a node matches when a node of relation edge,
// of target, matches each of ws. An empty ws adds nothing. More than
// MaxFilterWidth fails with ErrFilterTooWide.
func HasWith[W comparable](f *Filter, field string, ws []W, cond Cond[W], edge int, target untyped.Table) {
	if f.err != nil || len(ws) == 0 {
		return
	}
	if ps, ok := list(f, field, ws, cond, false); ok {
		f.add(name{base: field}, 0, f.t.Has(edge, target, ps...))
	}
}

// Has adds has<Edge>: a node matches when relation edge holds a node of
// target, for a true b, and when it holds none, for a false one.
func Has(f *Filter, field string, b *bool, edge int, target untyped.Table) {
	if f.err == nil && b != nil {
		f.is(name{base: field}, *b, f.t.Has(edge, target))
	}
}

// is adds p for a true b, and its negation for a false one.
func (f *Filter) is(field name, b bool, p velox.Predicate) {
	if !b {
		p = untyped.Not(p)
	}
	f.add(field, 0, p)
}

// Ops are the values of the predicates a where input offers on one field, as
// the generated code gives them to Field, IDField and EdgeIDField: each a field
// of the input, nil when the client does not set it or the input does not
// offer it. The GraphQL name of each is the field's name with the
// predicate's suffix, which is the name of the field of Ops ("titleNEQ"), but
// for EQ, which is the field's name alone ("title").
type Ops[V any] struct {
	EQ, NEQ *V
	// In and NotIn match one of the values, and none of them. A non-nil empty
	// list is a list of nothing, as the client sent it: In matches no node,
	// and NotIn every node. More than MaxFilterWidth values fail with
	// ErrFilterTooWide.
	In, NotIn                      []V
	GT, GTE, LT, LTE               *V
	Contains, HasPrefix, HasSuffix *V
	// EqualFold and ContainsFold ignore the case of the column and of the value.
	EqualFold, ContainsFold *V
	// IsNil and NotNil test the column for NULL: the predicate for true, and
	// its negation for false.
	IsNil, NotNil *bool
}

// Field adds the predicates o sets on column col, of the input's field
// field: each with its operator, in the order of Ops's fields, each counted
// against the budget. A value of the base type the field's scalar binds to
// is converted to the column's Go type, as untyped.Table.Convert does, and one
// the column's type cannot hold is a BAD_USER_INPUT error of the predicate.
// V is the Go type of the input's values: the function is compiled once for
// each shape of V, not for each entity.
func Field[V any](f *Filter, field string, col int, o Ops[V]) {
	one(f, name{field, ""}, col, untyped.PredEQ, o.EQ)
	one(f, name{field, "NEQ"}, col, untyped.PredNEQ, o.NEQ)
	many(f, name{field, "In"}, col, untyped.PredIn, o.In)
	many(f, name{field, "NotIn"}, col, untyped.PredNotIn, o.NotIn)
	one(f, name{field, "GT"}, col, untyped.PredGT, o.GT)
	one(f, name{field, "GTE"}, col, untyped.PredGTE, o.GTE)
	one(f, name{field, "LT"}, col, untyped.PredLT, o.LT)
	one(f, name{field, "LTE"}, col, untyped.PredLTE, o.LTE)
	one(f, name{field, "Contains"}, col, untyped.PredContains, o.Contains)
	one(f, name{field, "HasPrefix"}, col, untyped.PredHasPrefix, o.HasPrefix)
	one(f, name{field, "HasSuffix"}, col, untyped.PredHasSuffix, o.HasSuffix)
	one(f, name{field, "EqualFold"}, col, untyped.PredEqualFold, o.EqualFold)
	one(f, name{field, "ContainsFold"}, col, untyped.PredContainsFold, o.ContainsFold)
	f.null(name{field, "IsNil"}, col, untyped.PredIsNull, o.IsNil)
	f.null(name{field, "NotNil"}, col, untyped.PredNotNull, o.NotNil)
}

// one adds op on column col with *v, unless v is nil.
func one[V any](f *Filter, field name, col int, op untyped.PredOp, v *V) {
	if f.err == nil && v != nil {
		f.add(field, 0, f.pred(field, col, op, *v))
	}
}

// many adds op, In or NotIn, on column col with the values of vs, unless vs
// is nil.
func many[V any](f *Filter, field name, col int, op untyped.PredOp, vs []V) {
	if f.err != nil || vs == nil || f.veloxe && len(vs) == 0 || !f.width(field, len(vs)) {
		return
	}
	xs := make([]any, len(vs))
	for i, v := range vs {
		xs[i] = v
	}
	f.add(field, len(xs), f.pred(field, col, op, xs))
}

// null adds op, IsNull or NotNull, on column col, for a true *b, and its
// negation for a false one, unless b is nil.
func (f *Filter) null(field name, col int, op untyped.PredOp, b *bool) {
	if f.err == nil && b != nil && (*b || !f.veloxe) {
		f.is(field, *b, f.t.Pred(col, op, nil))
	}
}

// pred is the condition op on column col with v, a value or for In and
// NotIn a []any, each converted to the column's Go type. A value the
// column's type cannot hold is the client's error, of field.
func (f *Filter) pred(field name, col int, op untyped.PredOp, v any) velox.Predicate {
	if xs, ok := v.([]any); ok {
		for i, x := range xs {
			c, err := f.convert(field, col, x)
			if err != nil {
				return f.t.Pred(col, op, x) // the condition's error
			}
			xs[i] = c
		}
		return f.t.Pred(col, op, xs)
	}
	c, err := f.convert(field, col, v)
	if err != nil {
		return f.t.Pred(col, op, v)
	}
	return f.t.Pred(col, op, c)
}

// convert is v converted to column col's Go type; a number the type cannot
// hold is refused as a BAD_USER_INPUT error of field.
func (f *Filter) convert(field name, col int, v any) (any, error) {
	c, err := f.t.Convert(col, v)
	if errors.Is(err, velox.ErrOutOfRange) {
		f.refuse(field, InputErrorf("invalid value: %v is out of the field's range", v))
	}
	return c, err
}

// width is checkWidth of field's list of n, with its error recorded in f.
func (f *Filter) width(field name, n int) bool {
	if f.veloxe {
		return true // veloxe had no cap
	}
	if err := checkWidth(n, f.budget.limits.MaxFilterWidth); err != nil {
		f.refuse(field, err)
		return false
	}
	return true
}

// ArrayOps are the values of the predicates a where input offers on a field that
// is a JSON array of strings, numbers or booleans, as the generated code gives
// them to ArrayField: each a field of the input, nil when the client does not
// set it or the input does not offer it. V is the Go type of the elements. The
// GraphQL name of each is the field's name with the predicate's suffix.
type ArrayOps[V any] struct {
	// Has matches an array that holds the element.
	Has *V
	// HasSome and HasEvery match an array that holds one of the elements, and
	// one that holds all of them. A non-nil empty list is a list of nothing, as
	// the client sent it: HasSome matches no node, and HasEvery every node. More
	// than MaxFilterWidth values fail with ErrFilterTooWide.
	HasSome, HasEvery []V
	// IsEmpty matches the empty array for true, and any other for false: a
	// column that is NULL matches neither, as it holds no array.
	IsEmpty *bool
	// IsNil and NotNil test the column for NULL, as Ops's do.
	IsNil, NotNil *bool
}

// ArrayField adds the predicates o sets on column col, a JSON array column, of
// the input's field field: each counted against the budget, the elements of
// HasSome and HasEvery too. The elements need no conversion: GraphQL's scalars
// of a string, a number and a boolean are what the column's JSON holds. V is
// the Go type of the input's values: the function is compiled once for each
// shape of V, not for each entity.
func ArrayField[V any](f *Filter, field string, col int, o ArrayOps[V]) {
	if f.err == nil && o.Has != nil {
		f.add(name{field, "Has"}, 0, f.t.Pred(col, untyped.PredJSONHas, *o.Has))
	}
	elements(f, name{field, "HasSome"}, col, o.HasSome, untyped.Or)
	elements(f, name{field, "HasEvery"}, col, o.HasEvery, untyped.And)
	if f.err == nil && o.IsEmpty != nil && (*o.IsEmpty || !f.veloxe) {
		f.is(name{field, "IsEmpty"}, *o.IsEmpty, f.t.Pred(col, untyped.PredJSONEmpty, nil))
	}
	f.null(name{field, "IsNil"}, col, untyped.PredIsNull, o.IsNil)
	f.null(name{field, "NotNil"}, col, untyped.PredNotNull, o.NotNil)
}

// elements adds the Has of each of vs on column col, joined by join (Or or And),
// unless vs is nil. An empty list joins to nothing, which is false for Or and
// true for And.
func elements[V any](f *Filter, field name, col int, vs []V, join func(...velox.Predicate) velox.Predicate) {
	if f.err != nil || vs == nil || f.veloxe && len(vs) == 0 || !f.width(field, len(vs)) {
		return
	}
	ps := make([]velox.Predicate, len(vs))
	for i, v := range vs {
		ps[i] = f.t.Pred(col, untyped.PredJSONHas, v)
	}
	f.add(field, len(vs), join(ps...))
}

// IDField is Field of the filter's own type's key, on column col: the
// values are keys, in the key's Go type, which gqlgen decodes from the
// client's IDs. The predicates on IDs are EQ, NEQ, In, NotIn, GT, GTE, LT
// and LTE; the others of o are ignored.
func IDField[V any](f *Filter, field string, col int, o Ops[V]) {
	ids(f, field, col, o, fkScope{})
}

// EdgeIDField is IDField of a foreign key, on column col, which holds the keys
// of target's type, through the relation edge, and its IsNil and NotNil. Its
// predicates are scoped to what the relation lets the reader see, the
// target's interceptors and Read policy: a row they hide reads as null, to
// the edge and so to every predicate of its key, or a filter would say which
// hidden row another row links to. With a row filter on target, a
// predicate on IDs is the key's predicate and the edge's Has, IsNil is the
// edge's Has negated and NotNil the edge's Has; with none, each is the
// column's alone, and there is no EXISTS.
//
// A WithRewrite in the application's own query is not known here, nor is it
// to the edge's Has.
func EdgeIDField[V any](f *Filter, field string, col, edge int, target *Type, o Ops[V]) {
	fk := fkScope{edge: edge, target: target.table(), on: true}
	ids(f, field, col, o, fk)
	f.fkNull(name{field, "IsNil"}, col, untyped.PredIsNull, o.IsNil, fk)
	f.fkNull(name{field, "NotNil"}, col, untyped.PredNotNull, o.NotNil, fk)
}

// fkScope is the relation of a foreign key's predicates, when on.
type fkScope struct {
	edge   int
	target untyped.Table
	on     bool
}

// ids adds the predicates on keys that o sets on column col, scoped by fk.
func ids[V any](f *Filter, field string, col int, o Ops[V], fk fkScope) {
	id(f, name{field, ""}, col, untyped.PredEQ, o.EQ, fk)
	id(f, name{field, "NEQ"}, col, untyped.PredNEQ, o.NEQ, fk)
	idList(f, name{field, "In"}, col, untyped.PredIn, o.In, fk)
	idList(f, name{field, "NotIn"}, col, untyped.PredNotIn, o.NotIn, fk)
	id(f, name{field, "GT"}, col, untyped.PredGT, o.GT, fk)
	id(f, name{field, "GTE"}, col, untyped.PredGTE, o.GTE, fk)
	id(f, name{field, "LT"}, col, untyped.PredLT, o.LT, fk)
	id(f, name{field, "LTE"}, col, untyped.PredLTE, o.LTE, fk)
}

// id adds op on column col with the key *v, unless v is nil.
func id[V any](f *Filter, field name, col int, op untyped.PredOp, v *V, fk fkScope) {
	if f.err == nil && v != nil {
		f.add(field, 0, f.scoped(f.pred(field, col, op, *v), fk))
	}
}

// idList adds op, In or NotIn, on column col with the keys vs, unless vs is
// nil. More than MaxFilterWidth keys fail the filter.
func idList[V any](f *Filter, field name, col int, op untyped.PredOp, vs []V, fk fkScope) {
	if f.err != nil || vs == nil || f.veloxe && len(vs) == 0 || !f.width(field, len(vs)) {
		return
	}
	ks := make([]any, len(vs))
	for i, v := range vs {
		ks[i] = v
	}
	f.add(field, len(ks), f.scoped(f.pred(field, col, op, ks), fk))
}

// fkNull is null of a foreign key: with a row filter on fk's target (an
// interceptor or a Read policy), IsNil is the edge's Has negated, and NotNil
// the edge's Has, as a hidden target counts as none.
func (f *Filter) fkNull(field name, col int, op untyped.PredOp, b *bool, fk fkScope) {
	if f.err != nil || b == nil || !*b && f.veloxe {
		return
	}
	p := f.t.Pred(col, op, nil)
	if fk.target.HasRowFilter() {
		p = f.t.Has(fk.edge, fk.target)
		if op == untyped.PredIsNull {
			p = untyped.Not(p)
		}
	}
	f.is(field, *b, p)
}

// scoped is p, a predicate on the key of fk's relation, and with a row
// filter on its target (an interceptor or a Read policy) the edge's Has too.
func (f *Filter) scoped(p velox.Predicate, fk fkScope) velox.Predicate {
	if !fk.on || !fk.target.HasRowFilter() {
		return p
	}
	return untyped.And(p, f.t.Has(fk.edge, fk.target))
}
