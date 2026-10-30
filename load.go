package velox

import (
	"context"
	"fmt"
	"slices"
	"unsafe"

	"github.com/syssam/velox2/internal/engine"
)

// loadPin returns the target a query's With loads read from: on a DB with
// replicas or shards, at, the connection the query ran on; the zero target
// otherwise. Parents and children are then read from one replica, with one
// replication lag: children read from a replica further behind could be
// missing for a parent read from one further ahead. On a sharded DB the loads
// run on the query's shard without looking it up again.
func (db *DB) loadPin(at engine.Target) engine.Target {
	if db.rt == nil || db.inTx || at.Ex == nil {
		return engine.Target{} // every read runs on one connection already
	}
	return at
}

// loadRelations loads each spec's relation for the owners at ptrs. maxRows is
// the owner query's MaxRows, which its loads, nested ones too, share unless a
// load says MaxRows; 0 is the DB's cap. pin is the target their reads go
// to (DB.loadPin).
func loadRelations(ctx context.Context, db *DB, pin engine.Target, owner *EntityInfo, ptrs []unsafe.Pointer, specs []engine.LoadSpec, maxRows int) error {
	for _, sp := range specs {
		if err := loadOne(ctx, db, pin, owner, ptrs, sp, maxRows); err != nil {
			return err
		}
	}
	return nil
}

func loadOne(ctx context.Context, db *DB, pin engine.Target, owner *EntityInfo, ptrs []unsafe.Pointer, sp engine.LoadSpec, maxRows int) error {
	if sp.Err != nil {
		return sp.Err
	}
	r, l, err := engine.SpecRelation(owner, sp)
	if err != nil {
		return err
	}
	t := l.Target
	if sp.MaxRows > 0 {
		maxRows = sp.MaxRows // for its nested loads too
	}
	if engine.InfoKit(owner) == nil || engine.InfoKit(t) == nil || (l.Join != nil && engine.InfoKit(l.Join) == nil) {
		return fmt.Errorf("velox: %s: relation %s: the entities have no scan plan; run go tool velox-gen", owner.Name, r.Name)
	}
	// The distinct keys, in the order the owners have them; and each owner's,
	// as its index into them, -1 for none.
	group := make(map[any]int, len(ptrs))
	keys := make([]any, 0, len(ptrs))
	ownerKey := make([]int, len(ptrs))
	for i, p := range ptrs {
		ownerKey[i] = -1
		if k, ok := engine.KeyOf(engine.InfoKit(owner).Value(p, l.OwnerCol)); ok {
			g, added := engine.AddKey(group, k)
			if added {
				keys = append(keys, k)
			}
			ownerKey[i] = g
		}
	}
	// Every owner is loaded, even one with no key or no related row.
	var x engine.KeyRows // each key's rows
	var all []any
	var targets []unsafe.Pointer
	switch {
	case len(keys) == 0:
	case l.Join != nil && sp.Each > 0:
		var owners []any
		if all, targets, owners, err = fetchEach(ctx, db, pin, l, sp, keys, maxRows); err != nil {
			return &engine.LoadError{Owner: owner.Name, Rel: r.Name, Err: err}
		}
		x.Fill(group, len(owners), func(i int) (any, bool) { return engine.KeyOf(owners[i]) })
	case l.Join != nil:
		if all, targets, err = fetchThrough(ctx, db, pin, owner, r, l, sp, keys, maxRows, func(own any, rows []int) { x.Set(group, own, rows) }); err != nil {
			return err
		}
	default:
		var ec *EachClause
		if sp.Each > 0 {
			ec = &EachClause{Column: l.TargetCol, Limit: sp.Each}
		}
		if all, targets, err = fetch(ctx, db, pin, t, []int{l.TargetCol}, sp, keys, maxRows, ec); err != nil {
			return &engine.LoadError{Owner: owner.Name, Rel: r.Name, Err: err}
		}
		x.Fill(group, len(targets), func(i int) (any, bool) { return engine.KeyOf(engine.InfoKit(t).Value(targets[i], l.TargetCol)) })
	}
	// When a query ran, all holds its parts, so it is not nil even when they
	// hold no row; it is nil only when nothing was fetched (no owner had a
	// key, or no join row held a target key), and there is nothing to load.
	if all != nil && len(sp.With) > 0 {
		if err := loadRelations(ctx, db, pin, t, targets, sp.With, maxRows); err != nil {
			return err
		}
	}
	var picked []any // each owner's []T, when r is to-many and a query ran
	if r.Many && all != nil {
		idx := make([][]int, len(ptrs))
		for i, g := range ownerKey {
			idx[i] = x.Of(g)
		}
		picked = engine.InfoKit(t).Pick(all, targets, idx)
	}
	for i, p := range ptrs {
		h := (*engine.Holder)(unsafe.Add(p, r.Off))
		idx := x.Of(ownerKey[i])
		switch {
		case r.Many && all != nil:
			h.V = picked[i]
		case r.Many:
			h.V = engine.InfoKit(t).Empty() // no keys, so nothing was fetched: still loaded, and empty
		case len(idx) > 0:
			h.V = engine.InfoKit(t).Ptr(targets[idx[0]])
		default:
			h.V = nil
		}
		h.Loaded = true
	}
	return nil
}

// fetchThrough reads the targets of a many-to-many relation for the owner
// keys: the join entity's rows for them, then the targets of the keys those
// rows hold, which are the ones sp's options apply to. It calls set with each
// owner key's targets, as indexes into the targets returned, in their
// fetched order and each once. It returns a nil all when the join rows hold
// no target key, and so no target was fetched. Its errors name the owner and
// the relation.
//
// The targets are read in statements that each hold the target keys of a run
// of whole owners, so that each owner's come from one query, in the order it
// asked for; a target of owners in two statements is read, and held, twice.
// When one statement has more parameters than the dialect allows, because a
// policy's or a hook's conditions take more than KeysPer left them, it was not
// sent: the statements already read are kept, and the chunks from its first
// owner on are built again, with as many fewer keys as it had too many, until
// a chunk holds one key.
func fetchThrough(ctx context.Context, db *DB, pin engine.Target, owner *EntityInfo, r *engine.Relation, l engine.Link, sp engine.LoadSpec, keys []any, maxRows int, set func(own any, rows []int)) ([]any, []unsafe.Pointer, error) {
	j := l.Join
	_, rows, err := fetch(ctx, db, pin, j, []int{l.JoinOwner, l.JoinTarget}, engine.LoadSpec{}, keys, maxRows, nil)
	if err != nil {
		return nil, nil, &engine.LoadError{Owner: owner.Name, Rel: r.Name, Err: err}
	}
	// Each owner key's target keys, in join-row order and each once.
	byOwner := make(map[any][]any, len(keys))
	seen := make(map[[2]any]bool, len(rows))
	for _, p := range rows {
		own, okOwner := engine.KeyOf(engine.InfoKit(j).Value(p, l.JoinOwner))
		tk, okTarget := engine.KeyOf(engine.InfoKit(j).Value(p, l.JoinTarget))
		if !okOwner || !okTarget || seen[[2]any{own, tk}] {
			continue
		}
		seen[[2]any{own, tk}] = true
		byOwner[own] = append(byOwner[own], tk)
	}
	return throughChunks(ctx, db, pin, owner, r, l, sp, keys, byOwner, maxRows, set)
}

// throughChunks is fetchThrough's reading of the targets, in the statements
// ThroughPlan makes of keys, the owner keys, with KeysPer keys at most.
// byOwner holds each owner key's target keys. A statement over the parameter
// limit is not sent: the chunks read are kept, and the ones from its first
// owner on are planned again, with as many fewer keys as it had too many.
func throughChunks(ctx context.Context, db *DB, pin engine.Target, owner *EntityInfo, r *engine.Relation, l engine.Link, sp engine.LoadSpec, keys []any, byOwner map[any][]any, maxRows int, set func(own any, rows []int)) ([]any, []unsafe.Pointer, error) {
	t := l.Target
	order, per := fetchPlan(db, t, sp)
	var done []engine.ThroughChunk // the chunks read
	var parts []any                // each one's *blocks
	var shrunk error               // the parameter error that last made per smaller
	rest := keys                   // the owners not read yet
plan:
	for len(rest) > 0 {
		chunks, n := engine.ThroughPlan(rest, byOwner, per)
		if n > 0 {
			err := fmt.Errorf("velox: %s: relation %s: an owner has %d related rows, more than one statement can load in order", owner.Name, r.Name, n)
			if shrunk != nil {
				// The limit that made the statements smaller.
				err = fmt.Errorf("%w: %w", err, shrunk)
			}
			return nil, nil, err
		}
		for _, c := range chunks {
			part, _, err := fetchPart(ctx, db, pin, t, []int{l.TargetCol}, sp, order, c.Keys, maxRows, nil)
			if pe, ok := engine.As[*engine.ParamsError](err); ok && per > 1 {
				per = max(1, min(per, len(c.Keys))-(pe.N-pe.Max))
				shrunk = pe
				rest = rest[slices.Index(rest, c.Owners[0]):]
				continue plan
			}
			if err != nil {
				return nil, nil, &engine.LoadError{Owner: owner.Name, Rel: r.Name, Err: err}
			}
			done, parts = append(done, c), append(parts, part)
		}
		break
	}
	if len(done) == 0 {
		return nil, nil, nil
	}
	all, targets := engine.JoinBlocks(parts)
	start := 0 // the chunk's first index into targets
	for i, c := range done {
		end := start + parts[i].(*engine.Blocks).N
		byTarget := make(map[any][]int, end-start) // target key → indexes into targets
		for k := start; k < end; k++ {
			if tk, ok := engine.KeyOf(engine.InfoKit(t).Value(targets[k], l.TargetCol)); ok {
				byTarget[tk] = append(byTarget[tk], k)
			}
		}
		for _, own := range c.Owners {
			var idx []int
			for _, tk := range byOwner[own] {
				idx = append(idx, byTarget[tk]...)
			}
			slices.Sort(idx) // the fetched order, which sp's OrderBy set
			set(own, idx)
		}
		start = end
	}
	return all, targets, nil
}

// fetch reads the related rows for keys, in chunks that keep each statement
// under the dialect's parameter limit, ordered within each owner as asked
// (by the target's primary key when nothing is asked). cols are the columns
// of t its caller reads, the keys' column first. With ec, each statement reads
// each owner's rows apart, as ec says (fetchPart).
func fetch(ctx context.Context, db *DB, pin engine.Target, t *EntityInfo, cols []int, sp engine.LoadSpec, keys []any, maxRows int, ec *EachClause) ([]any, []unsafe.Pointer, error) {
	order, per := fetchPlan(db, t, sp)
	if ec != nil {
		order = engine.TieBreak(t, order)
	}
	var parts []any // each chunk's *blocks
	err := byChunks(len(keys), per, func(lo, hi int) error {
		part, _, err := fetchPart(ctx, db, pin, t, cols, sp, order, keys[lo:hi], maxRows, ec)
		if err == nil {
			parts = append(parts, part)
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	all, ptrs := engine.JoinBlocks(parts) // all holds the rows; pick and ptr read them through ptrs
	return all, ptrs, nil
}

// fetchEach reads the targets of a many-to-many relation with LimitEach for
// the owner keys, each owner's first sp.each in sp's order, in one statement
// per chunk of keys that joins the join entity and reads each row's owner key
// from it. It returns each target's owner key too, in the targets' order; a
// target of two owners is read, and held, once for each.
func fetchEach(ctx context.Context, db *DB, pin engine.Target, l engine.Link, sp engine.LoadSpec, keys []any, maxRows int) ([]any, []unsafe.Pointer, []any, error) {
	t := l.Target
	order, per := fetchPlan(db, t, sp)
	order = engine.TieBreak(t, order)
	ec := &EachClause{Join: l.Join, JoinOwner: l.JoinOwner, JoinTarget: l.JoinTarget, Target: l.TargetCol, Limit: sp.Each}
	var parts, owners []any
	err := byChunks(len(keys), per, func(lo, hi int) error {
		part, own, err := fetchPart(ctx, db, pin, t, nil, sp, order, keys[lo:hi], maxRows, ec)
		if err == nil {
			parts = append(parts, part)
			owners = append(owners, own...)
		}
		return err
	})
	if err != nil {
		return nil, nil, nil, err
	}
	all, targets := engine.JoinBlocks(parts)
	return all, targets, owners, nil
}

// fetchPlan returns the order a relation's rows are read in, and how many
// keys one statement holds (KeysPer).
func fetchPlan(db *DB, t *EntityInfo, sp engine.LoadSpec) ([]OrderTerm, int) {
	order := sp.Order
	if len(order) == 0 {
		for _, k := range t.PrimaryKey {
			order = append(order, OrderTerm{Column: k})
		}
	}
	return order, engine.KeysPer(db.d, t, sp.Where)
}

// byChunks runs read on keys[lo:hi] in chunks of at most per keys, in order.
// A chunk whose statement had too many parameters (*ParamsError) and more than
// one key is read again, from lo, in chunks smaller by the excess, and the rest
// use the smaller size; with one key, the error is returned.
func byChunks(n, per int, read func(lo, hi int) error) error {
	for lo := 0; lo < n; {
		hi := min(lo+per, n)
		err := read(lo, hi)
		if pe, ok := engine.As[*engine.ParamsError](err); ok && hi-lo > 1 {
			per = max(1, hi-lo-(pe.N-pe.Max))
			continue
		}
		if err != nil {
			return err
		}
		lo = hi
	}
	return nil
}

// fetchPart reads, in one statement, t's rows whose column cols[0] holds one
// of keys, with sp's options, in order. A Rewrite hook may not leave out any
// of cols, the columns the caller reads.
//
// With ec, the statement's Each is ec with keys as its Keys, and reads each
// owner's rows apart; its cap is the load's own MaxRows, else the bound of
// ec's Limit. When ec has a Join, cols is nil and fetchPart returns each
// row's owner key too, read from the join entity.
func fetchPart(ctx context.Context, db *DB, pin engine.Target, t *EntityInfo, cols []int, sp engine.LoadSpec, order []OrderTerm, keys []any, maxRows int, ec *EachClause) (any, []any, error) {
	st := Statement{Op: OpSelect, Entity: t, Unbounded: true, MaxRows: maxRows, InTx: db.inTx, OrderBy: order}
	if ec != nil {
		each := *ec
		each.Keys = keys
		st.Each, st.MaxRows, st.Where = &each, sp.MaxRows, slices.Clip(sp.Where)
	} else {
		st.Where = append([]Predicate{engine.NewPredicate(engine.Node{Op: engine.OpIn, Col: int32(cols[0]), V: keys})}, sp.Where...)
	}
	return readPart(ctx, db, pin, &st, t, cols, sp.With)
}

// readPart runs st, a read of t's rows, and scans them. A Rewrite hook may
// not leave out any of cols, the columns the caller reads, nor a column the
// loads with need. When st's Each has a Join, readPart returns each row's
// owner key too, read from the join entity. st holds, once it returns, where
// it ran. pin, when set, is where it runs: a With load's (DB.loadPin).
func readPart(ctx context.Context, db *DB, pin engine.Target, st *Statement, t *EntityInfo, cols []int, with []engine.LoadSpec) (any, []any, error) {
	engine.SetStmtPin(st, pin)
	var part any
	var owners []any
	read := func(r engine.Rows) (err error) {
		// st is as the hooks left it.
		if st.Columns != nil {
			for _, c := range cols {
				if !slices.Contains(st.Columns, c) {
					return fmt.Errorf("velox: %s: a Rewrite hook left out the relation's key column %s", t.Name, t.Columns[c].Name)
				}
			}
		}
		if len(with) > 0 && st.Columns != nil {
			if err := engine.CheckLoads(t, st.Columns, with); err != nil {
				return err
			}
		}
		if ec := st.Each; ec != nil && ec.Join != nil {
			or := &engine.OwnerRows{Rows: r, Typ: GoType(ec.Join.Columns[ec.JoinOwner])}
			part, err = engine.InfoKit(t).Scan(or, st.Columns)
			owners = or.Keys
			return err
		}
		part, err = engine.InfoKit(t).Scan(r, st.Columns)
		return err
	}
	var err error
	if db.hooked() {
		err = db.run(ctx, st, read, nil)
	} else {
		err = db.plain(ctx, st, read, nil)
	}
	return part, owners, err
}
