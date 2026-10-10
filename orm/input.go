package orm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
)

// This file is what a request-driven layer (GraphQL, or a REST handler that
// takes a filter object) needs on top of queries: the handle of the request
// carried in the context, and predicates and order terms made from input
// values. Nothing here imports a GraphQL engine.

type executorKey struct{}

// WithExecutor returns a context that carries ex, the database handle or the
// transaction of a request. A server sets it once per request; the methods a
// GraphQL engine calls on an entity to resolve its edges (u.Posts(ctx, ...))
// have nowhere else to take one from.
func WithExecutor(ctx context.Context, ex Executor) context.Context {
	return context.WithValue(ctx, executorKey{}, ex)
}

// ExecutorFrom returns the handle WithExecutor put in ctx, or nil: a query
// made from nil reports ErrNoExecutor when it runs.
func ExecutorFrom(ctx context.Context) Executor {
	ex, _ := ctx.Value(executorKey{}).(Executor)
	return ex
}

// TxFrom returns the transaction the context carries, or nil when it
// carries none or a handle that is not a transaction: for code that does
// something only inside a transaction (a check that must be rolled back
// with the write it follows).
func TxFrom(ctx context.Context) *Tx {
	tx, _ := ctx.Value(executorKey{}).(*Tx)
	return tx
}

// MaskNotFound returns nil for ErrNotFound and err for anything else: a
// to-one edge without a neighbor resolves to null, not to an error.
func MaskNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// IsNotLoaded reports whether err is the one an edge's OrErr method returns
// for an edge the query did not load.
func IsNotLoaded(err error) bool {
	var e *NotLoadedError
	return errors.As(err, &e)
}

// opInvalid is a predicate that could not be built; Val is the error the
// query or mutation reports.
const opInvalid Op = 254

// Invalid returns a predicate that makes the query or mutation it is given
// to fail with err: how a condition that could not be built from its input
// reports that, where the builder's chain has no place for an error.
func Invalid(err error) P { return P{Op: opInvalid, Val: err} }

// OrderTerm returns the order term of an order input: columns maps the
// values of the entity's OrderField enum to columns, field is the value the
// request gave ("" orders by the id), dir its direction ("" is ascending).
func OrderTerm(m *Meta, columns map[string]string, field string, dir OrderDirection) O {
	o := O{Desc: dir == OrderDirectionDesc}
	switch col, ok := columns[field]; {
	case field == "":
		if id := m.id(); id != nil {
			o.Col = id.Column
		}
	case ok:
		o.Col = col
		if f := m.Field(col); f != nil && f.Sensitive {
			o.Col, o.err = "", fmt.Errorf("velox: %s cannot be ordered by %q: the field is sensitive", m.Name, field)
		}
	default:
		// Not one of the fields the entity is ordered by. The name is not
		// tried as a column: that would order by, and put into the cursors
		// of a page, a column the schema did not offer.
		o.err = fmt.Errorf("velox: %s cannot be ordered by %q", m.Name, field)
	}
	return o
}

// MaxWhereDepth is how deep a where input may nest (and, or, not, has...With)
// before it is refused: a request must not be able to ask for a query of
// arbitrary size.
const MaxWhereDepth = 5

// ErrWhereDepth is the error of a where input nested deeper than MaxWhereDepth.
var ErrWhereDepth = fmt.Errorf("velox: where input nested deeper than %d levels", MaxWhereDepth)

// WhereField says what one field of a where input struct means: Op on Col,
// or, with Edge set, a condition on an edge (the neighbors' conditions when
// With is set). A generated where input has one per field, in the order of
// the struct, after its Predicates, Not, Or and And.
type WhereField struct {
	Col  string
	Op   Op
	Edge *EdgeMeta
	With bool
	// Neighbor is the plan of the where input a has...With field holds:
	// the edge's target's. The generated package sets it once every plan
	// exists, so that the nesting of an input is counted through its
	// edges and not from zero again on the other side of each.
	Neighbor []WhereField
	// Enum is, for a field of an enum, its stored values by their names
	// in the schema: what DecodeWhere reads DRAFT as.
	Enum map[string]string
}

// whereFixed is the number of fields every where input starts with.
const whereFixed = 4

// Where returns the predicate a where input describes, or the zero predicate
// (no condition) for a nil or an empty one. in is a pointer to a generated
// where input struct and plan its fields' meaning.
//
// A field says by its Go type whether it was given: a nil pointer or slice
// was not, a false bool was not. An input that nests an empty one, or nests
// too deep, gives a predicate that fails the query (Invalid).
func Where(in any, plan []WhereField) P {
	return where(reflect.ValueOf(in), plan, 0)
}

var predicateType = reflect.TypeFor[P]()

func where(v reflect.Value, plan []WhereField, depth int) P {
	if !v.IsValid() {
		return P{}
	}
	if v.Kind() != reflect.Pointer {
		// A mistake of the caller's, and not "no condition".
		return Invalid(fmt.Errorf("velox: a where input is given by its address, not as a %s", v.Type()))
	}
	if v.IsNil() {
		return P{}
	}
	if depth > MaxWhereDepth {
		return Invalid(ErrWhereDepth)
	}
	rv := v.Elem()
	if rv.Kind() != reflect.Struct || rv.NumField() != whereFixed+len(plan) {
		return Invalid(fmt.Errorf("velox: %s is not the where input its plan describes", rv.Type()))
	}
	var ps []P
	// Predicates: conditions the application added in Go.
	for i, extra := 0, rv.Field(0); i < extra.Len(); i++ {
		p, _ := extra.Index(i).Convert(predicateType).Interface().(P)
		ps = append(ps, p)
	}
	// nested returns the predicate of a nested input, which must say
	// something: {not: {}} is a mistake, not "no condition".
	nested := func(e reflect.Value) P {
		p := where(e, plan, depth+1)
		if p.Op == 0 {
			return Invalid(fmt.Errorf("velox: empty %s inside a where input", rv.Type().Name()))
		}
		return p
	}
	if not := rv.Field(1); !not.IsNil() {
		ps = append(ps, P{Op: OpNot, Subs: []P{nested(not)}})
	}
	for i, op := range [...]Op{OpOr, OpAnd} {
		if list := rv.Field(2 + i); list.Len() > 0 {
			subs := make([]P, list.Len())
			for j := range subs {
				subs[j] = nested(list.Index(j))
			}
			ps = append(ps, P{Op: op, Subs: subs})
		}
	}
	for i := range plan {
		f, w := rv.Field(whereFixed+i), &plan[i]
		switch {
		case w.Edge != nil && w.With:
			if f.Len() == 0 {
				continue
			}
			subs := make([]P, f.Len())
			for j := range subs {
				if w.Neighbor == nil {
					subs[j] = neighborWhere(f.Index(j), depth+1)
					continue
				}
				subs[j] = where(f.Index(j), w.Neighbor, depth+1)
				if subs[j].Op == 0 {
					subs[j] = Invalid(fmt.Errorf("velox: empty %s inside a where input", f.Type().Elem().Elem().Name()))
				}
			}
			ps = append(ps, P{Op: OpHasEdge, Edge: w.Edge, Subs: subs})
		case w.Edge != nil:
			if f.IsNil() {
				continue
			}
			has := P{Op: OpHasEdge, Edge: w.Edge}
			if !f.Elem().Bool() {
				has = P{Op: OpNot, Subs: []P{has}}
			}
			ps = append(ps, has)
		case f.Kind() == reflect.Bool:
			if f.Bool() {
				ps = append(ps, P{Op: w.Op, Col: w.Col})
			}
		case f.Kind() == reflect.Slice:
			if f.IsNil() {
				continue
			}
			vals := make([]any, f.Len())
			for j := range vals {
				vals[j] = f.Index(j).Interface()
			}
			ps = append(ps, P{Op: w.Op, Col: w.Col, Val: vals})
		case f.Kind() == reflect.Pointer:
			if !f.IsNil() {
				ps = append(ps, P{Op: w.Op, Col: w.Col, Val: f.Elem().Interface()})
			}
		default:
			return Invalid(fmt.Errorf("velox: field %s of %s is neither a pointer, a slice nor a bool", rv.Type().Field(whereFixed+i).Name, rv.Type()))
		}
	}
	switch len(ps) {
	case 0:
		return P{}
	case 1:
		return ps[0]
	}
	return P{Op: OpAnd, Subs: ps}
}

// neighborWhere is the predicate of an element of a has...With list: a where
// input of the edge's target, which knows its own plan (its P method).
func neighborWhere(e reflect.Value, depth int) P {
	if depth > MaxWhereDepth {
		return Invalid(ErrWhereDepth)
	}
	m := e.MethodByName("P")
	if !m.IsValid() || m.Type().NumIn() != 0 || m.Type().NumOut() != 1 || !m.Type().Out(0).ConvertibleTo(predicateType) {
		return Invalid(fmt.Errorf("velox: %s has no P method", e.Type()))
	}
	p, _ := m.Call(nil)[0].Convert(predicateType).Interface().(P)
	if p.Op == 0 {
		return Invalid(fmt.Errorf("velox: empty %s inside a where input", e.Type().Elem().Name()))
	}
	return p
}

// IDAs returns id as a T: the id as it is when it is one, and an integer of
// another integer type converted when T holds its value. Anything else is
// not a T (a string is never a number, nor a number a string: "1" and 1 are
// the ids of different rows).
//
// It is for an id whose type is not known where it arrives, the argument of
// a GraphQL node(id:) field, against the id type of each entity in turn.
func IDAs[T any](id any) (T, bool) {
	if v, ok := id.(T); ok {
		return v, true
	}
	var zero T
	from, to := reflect.ValueOf(id), reflect.ValueOf(&zero).Elem()
	if !from.IsValid() {
		return zero, false
	}
	switch {
	case from.CanInt() && to.CanInt():
		if to.OverflowInt(from.Int()) {
			return zero, false
		}
		to.SetInt(from.Int())
	case from.CanInt() && to.CanUint():
		u := uint64(from.Int()) //nolint:gosec // a negative id is refused on the next line
		if from.Int() < 0 || to.OverflowUint(u) {
			return zero, false
		}
		to.SetUint(u)
	case from.CanUint() && to.CanUint():
		if to.OverflowUint(from.Uint()) {
			return zero, false
		}
		to.SetUint(from.Uint())
	case from.Kind() == reflect.String && to.Kind() == reflect.String:
		// An id type declared on string, and a plain string.
		to.SetString(from.String())
	case from.CanUint() && to.CanInt():
		i := int64(from.Uint()) //nolint:gosec // an id above MaxInt64 is refused on the next line
		if from.Uint() > math.MaxInt64 || to.OverflowInt(i) {
			return zero, false
		}
		to.SetInt(i)
	default:
		return zero, false
	}
	return zero, true
}
