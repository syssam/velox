package orm

import "fmt"

// Op is the operator of a predicate.
type Op uint8

// Predicate operators.
const (
	OpEQ Op = iota + 1
	OpNEQ
	OpGT
	OpGTE
	OpLT
	OpLTE
	OpIn
	OpNotIn
	OpIsNull
	OpNotNull
	OpContains
	OpHasPrefix
	OpHasSuffix
	OpEqualFold
	OpContainsFold
	OpAnd
	OpOr
	OpNot
	OpHasEdge
)

// P is a predicate as data. The generated packages wrap it in one named type
// per entity (predicate.User), which is what makes a predicate of one entity
// a compile error in the query of another.
type P struct {
	Op   Op
	Col  string
	Val  any // one value, or []any for OpIn/OpNotIn
	Subs []P // operands of OpAnd/OpOr/OpNot; the target's predicates of OpHasEdge
	Edge *EdgeMeta
	// raw is a func(*sql.Selector) added by a privacy filter or an
	// interceptor. It is held as any so that this type, which every
	// generated package names, does not reach the SQL builder's types.
	raw any
}

// opRaw applies P.raw to the selector.
const opRaw Op = 101

// SetKind is what a setter does.
type SetKind uint8

// Setter kinds.
const (
	SetValue SetKind = iota + 1
	SetNull
	SetAdd
	EdgeAdd
	EdgeRemove
	EdgeClear
	// SetKeep leaves a column as it is in an update: nothing is written to
	// it, and its update default is not applied.
	SetKeep
	// setInvalid is a setter that could not be built (Fill of something that
	// is not a struct, or with a field that goes nowhere); Val is the error
	// the mutation reports.
	setInvalid SetKind = 255
)

// S is one assignment of a mutation as data: a column write or an edge change.
type S struct {
	Kind SetKind
	Col  string
	Val  any
	Edge *EdgeMeta
	IDs  []any
}

// O is one ORDER BY term.
type O struct {
	Col  string
	Desc bool
}

// EdgeLoad asks a query to eager-load one edge.
type EdgeLoad struct {
	Edge   *EdgeMeta
	Preds  []P
	Orders []O
	// Limit caps the neighbors loaded per parent; 0 loads all.
	Limit int
	With  []*EdgeLoad
	// Err is reported by the query that loads the edge.
	Err error
}

// Load asks the query to eager-load edge e, one of the edges of its entity.
// opts, when not nil, is a query of the edge's target that describes the
// load: its predicates, order, limit (applied per parent) and nested loads.
// given is how many such queries the caller passed; more than one is
// reported when the query runs.
//
//go:noinline
func (q *QueryCore) Load(e *EdgeMeta, opts *QueryCore, given int) {
	l := &EdgeLoad{Edge: e}
	if opts != nil {
		l.Preds = append([]P(nil), opts.Preds...)
		l.Orders = append([]O(nil), opts.Orders...)
		l.Limit = opts.Lim
		l.With = append([]*EdgeLoad(nil), opts.With...)
		if opts.Off != 0 || opts.Distinct {
			l.Err = fmt.Errorf("orm: loading edge %q of %s: Offset and Distinct are not supported on an eager load", e.Name, e.Owner.Name)
		}
	}
	if given > 1 {
		l.Err = fmt.Errorf("orm: loading edge %q of %s: one query describes the load, %d were given", e.Name, e.Owner.Name, given)
	}
	q.With = append(q.With, l)
}

// opNeighborOf matches the rows reached from one parent row through an edge;
// see NeighborOf.
const opNeighborOf Op = 100

// NeighborOf returns the predicate that selects, among the edge's target
// rows, the neighbors of parent. It is what the generated Query<Edge>
// methods of an entity's client put in the target's query.
func NeighborOf(e *EdgeMeta, parent any) P {
	id := e.Owner.get(parent, e.Owner.id().Column)
	if e.Rel == M2M || e.fkOnOwner() {
		return P{Op: opNeighborOf, Edge: e, Val: id}
	}
	return P{Op: OpEQ, Col: e.Columns[0], Val: id}
}

// NotLoadedError is returned by the OrErr method of an edge that the query
// did not load.
type NotLoadedError struct{ Edge string }

func (e *NotLoadedError) Error() string { return "orm: edge " + e.Edge + " was not loaded" }
