package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
)

// PerParentLimit is how a to-many eager load keeps at most N rows per parent
// (runtime.Limit through WithEdgeLoad). Generated loaders obtain it from
// PlanPerParentLimit, rank the edge query's rows with Apply, and skip the
// rows Keep rejects while assigning them to parents.
//
// With Window set, Apply renders ROW_NUMBER() OVER (PARTITION BY ...) and
// the database returns each parent's first N rows. Otherwise Apply only
// orders the rows the same way the window ranks them, and Keep counts rows
// per parent in memory — so both paths load the same rows, row for row.
type PerParentLimit[K comparable] struct {
	// Active reports whether the edge query carries a per-parent limit at
	// all. The zero value is inactive: Apply is never needed and Keep
	// accepts every row.
	Active bool
	// N is the number of rows kept per parent.
	N int
	// Window reports whether the limit is applied in SQL with a window
	// function rather than by Keep.
	Window bool
	kept   map[K]int
}

// PlanPerParentLimit decides how the edge query described by qc applies its
// per-parent limit. It returns an inactive plan when qc has no
// PartitionLimit. A per-parent limit combined with Limit or Offset on the
// same edge query is an error: the window applied that Limit after ranking,
// the in-memory path before it, so servers with and without window
// functions returned different rows.
//
// The window is used when the server behind drv has window functions
// (dialect.DriverCapabilities: static on Postgres and SQLite, probed on
// MySQL) and windowPays approves the limit.
func PlanPerParentLimit[K comparable](ctx context.Context, qc *QueryContext, drv dialect.Driver, edge string) (PerParentLimit[K], error) {
	if qc == nil || qc.PartitionLimit == nil {
		return PerParentLimit[K]{}, nil
	}
	if qc.Limit != nil || qc.Offset != nil {
		return PerParentLimit[K]{}, errors.New("velox: a per-parent limit cannot be combined with Limit or Offset on the " + edge + " edge query")
	}
	caps, err := dialect.DriverCapabilities(ctx, drv)
	if err != nil {
		return PerParentLimit[K]{}, err
	}
	p := PerParentLimit[K]{Active: true, N: *qc.PartitionLimit}
	if caps.Has(dialect.CapWindowFunctions) {
		p.Window = true
	} else {
		p.kept = make(map[K]int)
	}
	return p, nil
}

// Apply ranks the rows of s for the limit: it orders them by tiebreak (the
// target's ID column; "" for a target without one) after the edge's own
// order, and on the window path keeps each partition's first N rows.
func (p PerParentLimit[K]) Apply(s *sql.Selector, partition, tiebreak string) {
	if tiebreak != "" {
		s.OrderBy(tiebreak)
	}
	if p.Window {
		s.LimitPerPartition(partition, p.N)
	}
}

// Keep reports whether a row of the parent with key k is within the limit,
// counting it when it is. It accepts every row unless the limit is applied
// in memory.
func (p PerParentLimit[K]) Keep(k K) bool {
	if p.kept == nil {
		return true
	}
	if p.kept[k] >= p.N {
		return false
	}
	p.kept[k]++
	return true
}

// M2MQuery is the target query of a many-to-many eager load.
type M2MQuery interface {
	velox.Query
	GetCtx() *QueryContext
	GetDriver() dialect.Driver
}

// M2MLoad eagerly loads a many-to-many edge from parents P to targets T. It
// is the generated M2M loader: the join through the edge table, the scan of
// the parent key alongside each target row, the per-parent limit, the
// target's policy and interceptors, the edges nested under the target, and
// the runtime config — one implementation for every edge, so a fix to any
// of them reaches all of them.
//
// Q is the target query type, PT the target's pointer type, K the parent ID
// type and TK the target ID type. Function fields take method expressions of
// the generated types, e.g. (*TagQuery).buildSelector.
type M2MLoad[Q M2MQuery, P, T any, PT ScannableOf[T], K, TK comparable] struct {
	// Edge is the edge name, used in errors.
	Edge string
	// JoinTable is the edge's join table; ParentColumn and TargetColumn its
	// columns holding the parent's and the target's key.
	JoinTable    string
	ParentColumn string
	TargetColumn string
	// TargetID is the target table's ID column.
	TargetID string
	// ParentKey and TargetKey return an entity's ID.
	ParentKey func(*P) K
	TargetKey func(*T) TK
	// NewPivot returns a fresh scan destination for the join table's parent
	// key; PivotKey converts a scanned destination to the key.
	NewPivot func() any
	PivotKey func(any) K
	// Selector returns the target query's selector: the one sqlAll scans.
	Selector func(Q, context.Context) (*sql.Selector, error)
	// Prepare evaluates the target's policy and runs its traversers.
	Prepare func(Q, context.Context) error
	// EagerLoad loads the edges requested under the target query.
	EagerLoad func(Q, context.Context, []*T) error
	// Inters are the target's interceptors.
	Inters []velox.Interceptor
	// SetConfig injects Config into each loaded target.
	SetConfig func(*T, Config)
	Config    Config
}

// m2mPair is one (target, parent) edge, deduplicated while scanning.
type m2mPair[TK, K comparable] struct {
	target TK
	parent K
}

// Load loads the edge for nodes into the given callbacks: init runs once
// per parent before any row is read, assign once per (parent, target) edge
// in the order the targets were read. query is the caller's copy of the
// stored edge query; Load narrows the selector it builds, never query.
func (l M2MLoad[Q, P, T, PT, K, TK]) Load(ctx context.Context, query Q, nodes []*P, init func(*P), assign func(*P, *T)) error {
	edgeIDs := make([]any, len(nodes))
	byID := make(map[K]*P, len(nodes))
	for i, node := range nodes {
		k := l.ParentKey(node)
		edgeIDs[i] = k
		byID[k] = node
		if init != nil {
			init(node)
		}
	}
	// edges lists every (target, parent) pair in the order the rows were
	// read, which for each parent is its own ranking order.
	var (
		edges   []m2mPair[TK, K]
		scanned []*T // what the last scan returned, before any interceptor
	)
	qr := velox.QuerierFunc(func(ctx context.Context, q velox.Query) (velox.Value, error) {
		tq, ok := q.(Q)
		if !ok {
			return nil, fmt.Errorf("velox: unexpected query type %T loading the %s edge", q, l.Edge)
		}
		var err error
		scanned, err = l.scan(ctx, tq, edgeIDs, &edges)
		return scanned, err
	})
	// Traversers and the policy run before the interceptor chain, as they
	// do for every other read of the target.
	if err := l.Prepare(query, ctx); err != nil {
		return err
	}
	neighbors, err := velox.WithInterceptors[[]*T](ctx, query, qr, l.Inters)
	if err != nil {
		return err
	}
	targets := make(map[TK]*T, len(neighbors))
	for _, n := range neighbors {
		l.SetConfig(n, l.Config)
		targets[l.TargetKey(n)] = n
	}
	if slices.Equal(neighbors, scanned) {
		// No interceptor reordered the targets: assign in row order. A
		// target shared by several parents is scanned once, at its first
		// row, and under a per-parent limit the window returns rows by rank
		// across all parents, so the first row of a shared target can
		// precede a row that ranks before it for another parent.
		for _, e := range edges {
			if n, ok := targets[e.target]; ok {
				assign(byID[e.parent], n)
			}
		}
		return nil
	}
	// An interceptor changed the result: follow its order, as the direct
	// edge query and Ent's loader do, assigning each target to its parents
	// in row order.
	parents := make(map[TK][]K, len(targets))
	for _, e := range edges {
		parents[e.target] = append(parents[e.target], e.parent)
	}
	for _, n := range neighbors {
		for _, k := range parents[l.TargetKey(n)] {
			assign(byID[k], n)
		}
	}
	return nil
}

// scan runs the join and returns each target once, appending to edges
// every (target, parent) pair in the order the rows were read.
func (l M2MLoad[Q, P, T, PT, K, TK]) scan(ctx context.Context, tq Q, edgeIDs []any, edges *[]m2mPair[TK, K]) ([]*T, error) {
	selector, err := l.Selector(tq, ctx)
	if err != nil {
		return nil, err
	}
	joinT := sql.Table(l.JoinTable)
	selector.Join(joinT).On(selector.C(l.TargetID), joinT.C(l.TargetColumn))
	selector.Where(sql.In(joinT.C(l.ParentColumn), edgeIDs...))
	cols := selector.SelectedColumns()
	selector.Select(joinT.C(l.ParentColumn))
	selector.AppendSelect(cols...)
	selector.SetDistinct(false)
	limit, err := PlanPerParentLimit[K](ctx, tq.GetCtx(), tq.GetDriver(), l.Edge)
	if err != nil {
		return nil, err
	}
	if limit.Active {
		limit.Apply(selector, joinT.C(l.ParentColumn), selector.C(l.TargetID))
	}
	rows := &sql.Rows{}
	query, args, qerr := sql.QueryErr(selector)
	if qerr != nil {
		return nil, qerr
	}
	if err = tq.GetDriver().Query(ctx, query, args, rows); err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	// An interceptor that runs the query again gets the edges of that run.
	*edges = (*edges)[:0]
	var (
		result []*T
		// seen holds, per target, the parents it was already paired with;
		// a target's first appearance is when its entry is created.
		seen = make(map[TK]map[K]struct{})
	)
	for rows.Next() {
		node := new(T)
		values, err := PT(node).ScanValues(columns[1:])
		if err != nil {
			return nil, err
		}
		pivot := l.NewPivot()
		if err := rows.Scan(append([]any{pivot}, values...)...); err != nil {
			return nil, err
		}
		if err := PT(node).AssignValues(columns[1:], values); err != nil {
			return nil, err
		}
		key := l.PivotKey(pivot)
		if !limit.Keep(key) {
			continue
		}
		tk := l.TargetKey(node)
		ps, ok := seen[tk]
		if !ok {
			ps = make(map[K]struct{}, 1)
			seen[tk] = ps
			result = append(result, node)
		}
		if _, dup := ps[key]; dup {
			continue
		}
		ps[key] = struct{}{}
		*edges = append(*edges, m2mPair[TK, K]{target: tk, parent: key})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Close before loading nested edges: a driver holding one connection
	// cannot run their queries while these rows are open.
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(result) > 0 {
		if err := l.EagerLoad(tq, ctx, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}
