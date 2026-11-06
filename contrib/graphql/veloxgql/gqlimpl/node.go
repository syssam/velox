package gqlimpl

import (
	"context"
	"fmt"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// NodeKind is one Node type, as generated code hands it to NodeByID and
// NodesByID: what looks up the type's nodes by their IDs. NewNode makes one.
// For generated code.
type NodeKind struct {
	typ     string
	table   velox.Table
	key     int
	decode  func(id string) (any, error)
	columns func(fields []string) []int
}

// NewNode returns the Node type c.Name, the GraphQL type's name, the one an
// ID of the type holds, of the entity of c's Table, whose rows are Noders,
// for generated code, which writes one for each Node type in a function, so
// that nothing runs at start. An ID of the type decodes into a K
// (veloxgql.DecodeID), the Go type of the key, c.Key's one column; a
// selection of fields reads c.Columns (see Narrow).
func NewNode[K any](c *Type) NodeKind {
	return newNode(c, func(id string) (any, error) {
		k, err := veloxgql.DecodeID[K](id, c.Name)
		return k, err
	})
}

// NewNodeAs is NewNode of a key whose Go type V is a named type DecodeID
// does not take: an ID decodes into a K, converted by conv.
func NewNodeAs[K, V any](c *Type, conv func(K) V) NodeKind {
	return newNode(c, func(id string) (any, error) {
		k, err := veloxgql.DecodeID[K](id, c.Name)
		if err != nil {
			return nil, err
		}
		return conv(k), nil
	})
}

func newNode(c *Type, decode func(id string) (any, error)) NodeKind {
	key := -1
	if len(c.Key) == 1 {
		key = c.Key[0].Col
	}
	return NodeKind{typ: c.Name, table: c.table(), key: key, decode: decode, columns: c.Columns}
}

// base is the query of the type's rows, reading only the columns the
// selection of the field in ctx needs.
func (n NodeKind) base(ctx context.Context) (velox.TableQuery, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return velox.TableQuery{}, err
	}
	q := db.QueryTable(n.table)
	if fields, ok := Selected(ctx, n.typ); ok {
		q = Narrow(q, n.columns(fields))
	}
	return q, nil
}

// noder is the row at p as a Noder.
func (n NodeKind) noder(p unsafe.Pointer) (veloxgql.Noder, error) {
	x, ok := n.table.Ptr(p).(veloxgql.Noder)
	if !ok {
		return nil, fmt.Errorf("veloxgql: %s: the entity %s is no Node; run go tool velox-gen", n.typ, n.table.Info().Name)
	}
	return x, nil
}

func (n NodeKind) one(ctx context.Context, id string) (veloxgql.Noder, error) {
	k, err := n.decode(id)
	if err != nil {
		return nil, err
	}
	q, err := n.base(ctx)
	if err != nil {
		return nil, err
	}
	p, err := q.Where(n.table.Pred(n.key, velox.PredEQ, k)).One(ctx)
	switch {
	case velox.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	gqlcore.Add(ctx, []unsafe.Pointer{p})
	return n.noder(p)
}

// nodeSet is the keys of one type that NodesByID looks up with one query,
// and the indexes of their IDs.
type nodeSet struct {
	n    NodeKind
	keys []any
	at   []int
}

// add decodes id, the ID at index at of the request's ids.
func (s *nodeSet) add(id string, at int) error {
	k, err := s.n.decode(id)
	if err != nil {
		return err
	}
	s.keys = append(s.keys, k)
	s.at = append(s.at, at)
	return nil
}

// load reads the rows of the IDs added, and puts each at its index in out.
func (s *nodeSet) load(ctx context.Context, out []veloxgql.Noder) error {
	q, err := s.n.base(ctx)
	if err != nil {
		return err
	}
	rows, err := q.Where(s.n.table.Pred(s.n.key, velox.PredIn, s.keys)).Unbounded().MaxRows(len(s.keys)).All(ctx)
	if err != nil {
		return err
	}
	by := make(map[any]unsafe.Pointer, rows.Len())
	batch := make([]unsafe.Pointer, rows.Len())
	for i := range batch {
		p := rows.At(i)
		by[s.n.table.Value(p, s.n.key)] = p
		batch[i] = p
	}
	gqlcore.Add(ctx, batch)
	for j, k := range s.keys {
		if p := by[k]; p != nil {
			x, err := s.n.noder(p)
			if err != nil {
				return err
			}
			out[s.at[j]] = x
		}
	}
	return nil
}

// NodeByID returns the node with the global ID id, or nil when no row has its
// key: the root field node. kind returns the Node type an ID's type names,
// and false for none. For generated code; see the Node function it writes.
//
// It looks the row up with one query on the ID's entity, on the DB
// veloxgql.DBFromContext reads from ctx, reading only the columns the
// field's selection needs; its edges batch (veloxgql.Batch). A column the
// selection does not need is zero in the node, except the key and the
// foreign keys: an application's resolver must not read one it did not
// select. An ID of no Node type fails with veloxgql.ErrWrongIDType and a
// malformed one with veloxgql.ErrBadID; neither runs a query.
func NodeByID(ctx context.Context, id string, kind func(typ string) (NodeKind, bool)) (veloxgql.Noder, error) {
	typ, err := veloxgql.IDType(id)
	if err != nil {
		return nil, err
	}
	k, ok := kind(typ)
	if !ok {
		return nil, noNodeType("Node")
	}
	return k.one(ctx, id)
}

// NodesByID returns the nodes with the global IDs ids, in their order, with
// nil for an ID no row has: the root field nodes. It decodes every ID first,
// and then reads each type's nodes with one query, in the order of the types'
// first IDs. Each query reads at most one row for each of its IDs, whatever
// the DB's WithMaxRows, and only the columns the field's selection needs; a
// type's nodes are one batch for their edges, and their unselected columns
// are zero as NodeByID's are. It fails as NodeByID does on an ID, before any
// query runs, and with veloxgql.ErrPageTooLarge on more IDs than the MaxPage
// of veloxgql.LimitsFromContext(ctx). For generated code; see the Nodes
// function it writes.
func NodesByID(ctx context.Context, ids []string, kind func(typ string) (NodeKind, bool)) ([]veloxgql.Noder, error) {
	if most := veloxgql.LimitsFromContext(ctx).MaxPage; len(ids) > most {
		return nil, InputErrorf("Nodes: %w: %d IDs, and the most is %d", veloxgql.ErrPageTooLarge, len(ids), most)
	}
	var (
		order []*nodeSet
		sets  = map[string]*nodeSet{}
	)
	for i, id := range ids {
		typ, err := veloxgql.IDType(id)
		if err != nil {
			return nil, err
		}
		s := sets[typ]
		if s == nil {
			k, ok := kind(typ)
			if !ok {
				return nil, noNodeType("Nodes")
			}
			s = &nodeSet{n: k}
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
