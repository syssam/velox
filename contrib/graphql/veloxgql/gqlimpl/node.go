package gqlimpl

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// NodeKind is one Node type, as generated code hands it to NodeByID and
// NodesByID: what looks up the type's nodes by their IDs. NodeOf is the one
// implementation. For generated code.
type NodeKind interface {
	// one is the node with the ID id, or nil when no row has its key.
	one(ctx context.Context, id string) (veloxgql.Noder, error)
	// set starts the lookup of several IDs of the type.
	set() nodeSet
}

// nodeSet is the IDs of one Node type that NodesByID looks up with one query.
type nodeSet interface {
	// add decodes id, the ID at index at of the request's ids.
	add(id string, at int) error
	// load reads the rows of the IDs added, and puts each at its index in out.
	load(ctx context.Context, out []veloxgql.Noder) error
}

// NodeOf is the Node type of the entity E, whose pointer PE is a Noder, with
// the key type K. NewNode makes one.
type NodeOf[E any, PE interface {
	*E
	veloxgql.Noder
}, K comparable] struct {
	typ     string
	decode  func(id string) (K, error)
	query   func(db *velox.DB) velox.Query[E]
	columns func(fields []string) []velox.Column[E]
	eq      func(K) velox.Pred[E]
	in      func(...K) velox.Pred[E]
	key     func(*E) K
}

// NewNode returns the Node type of the entity E, for generated code, which
// writes one for each Node type in a function, so that nothing runs at start.
// typ is the GraphQL type's name, the one an ID of the type holds; decode
// decodes an ID of the type into the key; query is the query of every row of
// the entity on db; columns is the columns a selection of fields reads (see
// Narrow); eq and in are the predicates of the key column, and key returns the
// key of a row.
//
// It takes the predicates as arguments, not as fields of a struct, since a
// generic method's value is not assignable to a field.
func NewNode[E any, PE interface {
	*E
	veloxgql.Noder
}, K comparable](typ string, decode func(id string) (K, error), query func(db *velox.DB) velox.Query[E], columns func(fields []string) []velox.Column[E], eq func(K) velox.Pred[E], in func(...K) velox.Pred[E], key func(*E) K) NodeKind {
	return NodeOf[E, PE, K]{typ, decode, query, columns, eq, in, key}
}

// base is the query of the type's rows, reading only the columns the
// selection of the field in ctx needs.
func (n NodeOf[E, PE, K]) base(ctx context.Context) (velox.Query[E], error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return velox.Query[E]{}, err
	}
	q := n.query(db)
	if fields, ok := Selected(ctx, n.typ); ok {
		q = Narrow(q, n.columns(fields))
	}
	return q, nil
}

func (n NodeOf[E, PE, K]) one(ctx context.Context, id string) (veloxgql.Noder, error) {
	k, err := n.decode(id)
	if err != nil {
		return nil, err
	}
	q, err := n.base(ctx)
	if err != nil {
		return nil, err
	}
	x, err := q.Where(n.eq(k)).One(ctx)
	switch {
	case velox.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	veloxgql.Batch(ctx, []*E{x})
	return PE(x), nil
}

func (n NodeOf[E, PE, K]) set() nodeSet { return &nodeRows[E, PE, K]{n: n} }

// nodeRows is the keys of one type, and the indexes of their IDs.
type nodeRows[E any, PE interface {
	*E
	veloxgql.Noder
}, K comparable] struct {
	n    NodeOf[E, PE, K]
	keys []K
	at   []int
}

func (s *nodeRows[E, PE, K]) add(id string, at int) error {
	k, err := s.n.decode(id)
	if err != nil {
		return err
	}
	s.keys = append(s.keys, k)
	s.at = append(s.at, at)
	return nil
}

func (s *nodeRows[E, PE, K]) load(ctx context.Context, out []veloxgql.Noder) error {
	q, err := s.n.base(ctx)
	if err != nil {
		return err
	}
	rows, err := q.Where(s.n.in(s.keys...)).Unbounded().MaxRows(len(s.keys)).All(ctx)
	if err != nil {
		return err
	}
	by := make(map[K]*E, len(rows))
	batch := make([]*E, len(rows))
	for i := range rows {
		by[s.n.key(&rows[i])] = &rows[i]
		batch[i] = &rows[i]
	}
	veloxgql.Batch(ctx, batch)
	for j, k := range s.keys {
		if x := by[k]; x != nil {
			out[s.at[j]] = PE(x)
		}
	}
	return nil
}

// NodeByID returns the node with the global ID id, or nil when no row has its
// key: the root field node. kind returns the Node type an ID's type names, or
// nil for none. For generated code; see the Node function it writes.
func NodeByID(ctx context.Context, id string, kind func(typ string) NodeKind) (veloxgql.Noder, error) {
	typ, err := veloxgql.IDType(id)
	if err != nil {
		return nil, err
	}
	k := kind(typ)
	if k == nil {
		return nil, noNodeType("Node")
	}
	return k.one(ctx, id)
}

// NodesByID returns the nodes with the global IDs ids, in their order, with
// nil for an ID no row has: the root field nodes. It decodes every ID first,
// and then reads each type's nodes with one query, in the order of the types'
// first IDs. It fails as NodeByID does on an ID, before any query runs, and
// with veloxgql.ErrPageTooLarge on more IDs than the MaxPage of
// veloxgql.LimitsFromContext(ctx). For generated code; see the Nodes function
// it writes.
func NodesByID(ctx context.Context, ids []string, kind func(typ string) NodeKind) ([]veloxgql.Noder, error) {
	if most := veloxgql.LimitsFromContext(ctx).MaxPage; len(ids) > most {
		return nil, InputErrorf("Nodes: %w: %d IDs, and the most is %d", veloxgql.ErrPageTooLarge, len(ids), most)
	}
	var (
		order []nodeSet
		sets  = map[string]nodeSet{}
	)
	for i, id := range ids {
		typ, err := veloxgql.IDType(id)
		if err != nil {
			return nil, err
		}
		s := sets[typ]
		if s == nil {
			k := kind(typ)
			if k == nil {
				return nil, noNodeType("Nodes")
			}
			s = k.set()
			sets[typ] = s
			order = append(order, s)
		}
		if err := s.add(id, i); err != nil {
			return nil, err
		}
	}
	out := make([]veloxgql.Noder, len(ids))
	for _, s := range order {
		if err := s.load(ctx, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func noNodeType(fn string) error {
	return InputErrorf("%s: %w: the ID is of no Node type", fn, veloxgql.ErrWrongIDType)
}
