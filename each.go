package velox

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"unsafe"

	"github.com/syssam/velox2/internal/engine"
)

// LoadEach returns, for each of owners in order, its rows of q related
// through r: q's conditions, After, Columns and scope, r's Where, and the
// order q's OrderBy, then r's, then the target's key. r's LimitEach is the
// most rows per owner, in that order, and r's With loads apply to the rows.
// One statement per chunk of owners under the dialect's parameter limit.
// q's Columns gain the relation's key columns when it leaves them out. A q
// with a Limit, an Offset or a lock is refused; a nil owner fails, naming
// the entity; an owner whose key is NULL gets no rows.
//
// q's own With loads apply to the rows too. Each owner gets a slice of its
// own, empty when it has no rows; owners with one key get equal slices. The
// slices share the arrays the rows were read into, and one array of the rows
// copied out of them, each capped at its length, so a row kept from one keeps
// the others' rows alive too: copy it to keep it alone. The
// statements are q's entity's, so its tenant scope, Read policy and hooks see
// them, and a row they hide takes no place in a page. Their cap is r's
// MaxRows, else q's, else LimitEach's bound (LimitEach rows per owner), else
// the DB's. A many-to-many relation is read in the same one statement, joined
// to its join table, as Rel.LimitEach reads it.
func LoadEach[E, T any](ctx context.Context, q Query[T], r Rel[E, T], owners []*E) ([][]T, error) {
	owner, err := ownerOf[E]()
	// A []*E has the layout of a []unsafe.Pointer, as in Rel.Of: the work is
	// not generic, so that it is compiled once.
	ps := unsafe.Slice((*unsafe.Pointer)(unsafe.Pointer(unsafe.SliceData(owners))), len(owners))
	parts, err := loadEach(ctx, eachOf(q), owner, err, r.spec(), ps)
	if err != nil {
		return nil, err
	}
	out := make([][]T, len(parts))
	for i, p := range parts {
		out[i] = p.([]T)
	}
	return out, nil
}

// CountEach returns, for each of owners in order, the number of q's rows
// related to it through r, as LoadEach would read them without a limit:
// SELECT <owner key>, COUNT(*) … GROUP BY <owner key>, one statement per
// chunk of owners. An owner with none gets 0, and so does an owner whose key
// is NULL. A nil owner fails the call, with an error naming the entity.
//
// r's LimitEach, OrderBy and With do not apply, nor q's order and columns.
// q's Limit, Offset or a lock is refused, as by LoadEach, unlike Query.Count,
// which ignores Limit and Offset. A many-to-many relation counts each target
// once per owner, as LoadEach reads it.
func CountEach[E, T any](ctx context.Context, q Query[T], r Rel[E, T], owners []*E) ([]int64, error) {
	owner, err := ownerOf[E]()
	ps := unsafe.Slice((*unsafe.Pointer)(unsafe.Pointer(unsafe.SliceData(owners))), len(owners))
	return countEach(ctx, eachOf(q), owner, err, r.spec(), ps)
}

// eachQuery is what LoadEach and CountEach read of their Query, without its
// type.
type eachQuery struct {
	db    *DB
	in    *EntityInfo
	err   error // the query's metadata's or conditions' error
	st    Statement
	after unsafe.Pointer // After's row, or nil
	loads []engine.LoadSpec
}

func eachOf[T any](q Query[T]) eachQuery {
	e := eachQuery{db: q.db, st: q.st, after: unsafe.Pointer(q.after), loads: q.loads, err: q.err}
	if q.meta != nil {
		e.in = q.meta.info
		if q.meta.err != nil {
			e.err = q.meta.err
		}
	}
	return e
}

// planEach checks fn's (LoadEach's or CountEach's) arguments and plans its
// statements.
func planEach(fn string, q eachQuery, owner *EntityInfo, ownerErr error, sp engine.LoadSpec, ps []unsafe.Pointer) (engine.EachPlan, error) {
	var p engine.EachPlan
	switch {
	case ownerErr != nil:
		return p, ownerErr
	case q.err != nil:
		return p, q.err
	case q.in == nil || q.db == nil:
		return p, fmt.Errorf("velox: %s was given a Query that DB.Query did not make", fn)
	}
	if err := engine.SpecErr(sp); err != nil {
		return p, err
	}
	if m := engine.EachRefused(&q.st); m != "" {
		return p, fmt.Errorf("velox: %s: %s refuses a query with %s", q.in.Name, fn, m)
	}
	for _, o := range ps {
		if o == nil {
			return p, fmt.Errorf("velox: %s: %s was given a nil owner", owner.Name, fn)
		}
	}
	r, l, err := engine.SpecRelation(owner, sp)
	if err != nil {
		return p, err
	}
	if engine.InfoKit(owner) == nil || engine.InfoKit(l.Target) == nil {
		return p, fmt.Errorf("velox: %s: relation %s: the entities have no scan plan; run go tool velox-gen", owner.Name, r.Name)
	}
	if l.Target != q.in {
		return p, fmt.Errorf("velox: %s: %s: the relation %s is to %s, not to the query's %s", owner.Name, fn, r.Name, l.Target.Name, q.in.Name)
	}
	for _, c := range sp.Where {
		if err := engine.ResolveErr(engine.PredNode(c)); err != nil {
			return p, err
		}
	}
	p.L = l
	p.OwnerKey = make([]any, len(ps))
	seen := make(map[any]bool, len(ps))
	for i, o := range ps {
		k, ok := engine.KeyOf(engine.InfoKit(owner).Value(o, l.OwnerCol))
		if !ok {
			continue
		}
		p.OwnerKey[i] = k
		if !seen[k] {
			seen[k] = true
			p.Keys = append(p.Keys, k)
		}
	}
	p.St = engine.CloneStatement(&q.st)
	if q.after != nil {
		if err := engine.AddAfter(&p.St, q.in, q.after); err != nil {
			return p, err
		}
	}
	p.St.Where = slices.Clip(append(p.St.Where, sp.Where...))
	return p, nil
}

// loadEach is LoadEach's body: each owner's rows, as a []T in an any.
func loadEach(ctx context.Context, q eachQuery, owner *EntityInfo, ownerErr error, sp engine.LoadSpec, ps []unsafe.Pointer) ([]any, error) {
	p, err := planEach("LoadEach", q, owner, ownerErr, sp, ps)
	if err != nil {
		return nil, err
	}
	l, t, db := p.L, p.L.Target, q.db
	loads := append(slices.Clip(q.loads), sp.With...)
	st := p.St
	if st.Columns != nil && !slices.Contains(st.Columns, l.TargetCol) {
		st.Columns = append(slices.Clip(st.Columns), l.TargetCol)
	}
	if err := engine.CheckLoads(t, st.Columns, loads); err != nil {
		return nil, err
	}
	st.Unbounded = true
	st.OrderBy = engine.TieBreak(t, append(slices.Clip(st.OrderBy), sp.Order...))
	if sp.MaxRows > 0 {
		st.MaxRows = sp.MaxRows
	}
	var key []int // the column a Rewrite hook may not leave out
	if l.Join == nil {
		key = []int{l.TargetCol}
	}
	ec := engine.MakeEachClause(l, sp.Each)
	per := engine.KeysPer(db.e.D, t, st.Where)
	var parts, owners []any
	var at engine.Target
	err = byChunks(len(p.Keys), per, func(lo, hi int) error {
		c := engine.Chunk(st, ec, p.Keys[lo:hi])
		part, own, err := readPart(ctx, db, engine.Target{}, &c, t, key, loads)
		if err != nil {
			return err
		}
		if lo == 0 {
			at = engine.StmtRan(&c)
		}
		parts = append(parts, part)
		owners = append(owners, own...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]any, len(ps))
	if len(parts) == 0 {
		for i := range out {
			out[i] = engine.InfoKit(t).Empty()
		}
		return out, nil
	}
	all, targets := engine.JoinBlocks(parts)
	if len(loads) > 0 && len(targets) > 0 {
		if err := loadRelations(ctx, db, db.loadPin(at), t, targets, loads, st.MaxRows); err != nil {
			return nil, err
		}
	}
	// Each owner key's rows, as indexes into targets, in the order read.
	group := make(map[any]int, len(p.Keys))
	for _, k := range p.Keys {
		engine.AddKey(group, k)
	}
	var x engine.KeyRows
	x.Fill(group, len(targets), func(i int) (any, bool) {
		if l.Join != nil {
			return engine.KeyOf(owners[i])
		}
		return engine.KeyOf(engine.InfoKit(t).Value(targets[i], l.TargetCol))
	})
	idx := make([][]int, len(p.OwnerKey))
	for i, k := range p.OwnerKey {
		if k != nil {
			idx[i] = x.Of(group[k])
		}
	}
	picked := engine.InfoKit(t).Pick(all, targets, idx)
	for i, k := range p.OwnerKey {
		if k == nil {
			out[i] = engine.InfoKit(t).Empty()
			continue
		}
		out[i] = picked[i]
	}
	return out, nil
}

// countEach is CountEach's body.
func countEach(ctx context.Context, q eachQuery, owner *EntityInfo, ownerErr error, sp engine.LoadSpec, ps []unsafe.Pointer) ([]int64, error) {
	p, err := planEach("CountEach", q, owner, ownerErr, sp, ps)
	if err != nil {
		return nil, err
	}
	l, t, db := p.L, p.L.Target, q.db
	st := p.St
	st.Count, st.Columns, st.OrderBy, st.MaxRows = true, nil, nil, 0
	// The owner's key, as the statement reads it: the join's or the target's.
	typ := GoType(t.Columns[l.TargetCol])
	if l.Join != nil {
		typ = GoType(l.Join.Columns[l.JoinOwner])
	}
	ec := engine.MakeEachClause(l, 0)
	per := engine.KeysPer(db.e.D, t, st.Where)
	counts := make(map[any]int64, len(p.Keys))
	read := func(r engine.Rows) error {
		for r.Next() {
			k := reflect.New(typ)
			var n int64
			if err := r.Scan(k.Interface(), &n); err != nil {
				return err
			}
			if key, ok := engine.KeyOf(k.Elem().Interface()); ok {
				counts[key] = n // an around hook's retry reads the same
			}
		}
		return nil
	}
	err = byChunks(len(p.Keys), per, func(lo, hi int) error {
		c := engine.Chunk(st, ec, p.Keys[lo:hi])
		if db.e.Hooked() {
			return db.run(ctx, &c, read, nil)
		}
		return db.plain(ctx, &c, read, nil)
	})
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(ps))
	for i, k := range p.OwnerKey {
		if k != nil {
			out[i] = counts[k]
		}
	}
	return out, nil
}
