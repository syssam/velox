package velox

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
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
// slices share one array, each capped at its length, so a row kept from one
// keeps the others' rows alive too: copy it to keep it alone. The
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
	loads []loadSpec
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

// eachPlan is what LoadEach and CountEach share: the relation, the owners'
// distinct keys and each owner's (nil for an owner whose key is NULL), and
// the statement every chunk starts from, with q's conditions, After's and
// r's.
type eachPlan struct {
	l        link
	keys     []any
	ownerKey []any
	st       Statement
}

// planEach checks fn's (LoadEach's or CountEach's) arguments and plans its
// statements.
func planEach(fn string, q eachQuery, owner *EntityInfo, ownerErr error, sp loadSpec, ps []unsafe.Pointer) (eachPlan, error) {
	var p eachPlan
	switch {
	case ownerErr != nil:
		return p, ownerErr
	case q.err != nil:
		return p, q.err
	case q.in == nil || q.db == nil:
		return p, fmt.Errorf("velox: %s was given a Query that DB.Query did not make", fn)
	}
	if err := specErr(sp); err != nil {
		return p, err
	}
	if m := eachRefused(&q.st); m != "" {
		return p, fmt.Errorf("velox: %s: %s refuses a query with %s", q.in.Name, fn, m)
	}
	for _, o := range ps {
		if o == nil {
			return p, fmt.Errorf("velox: %s: %s was given a nil owner", owner.Name, fn)
		}
	}
	r, l, err := specRelation(owner, sp)
	if err != nil {
		return p, err
	}
	if owner.kit == nil || l.target.kit == nil {
		return p, fmt.Errorf("velox: %s: relation %s: the entities have no scan plan; run go tool velox-gen", owner.Name, r.name)
	}
	if l.target != q.in {
		return p, fmt.Errorf("velox: %s: %s: the relation %s is to %s, not to the query's %s", owner.Name, fn, r.name, l.target.Name, q.in.Name)
	}
	for _, c := range sp.where {
		if err := resolveErr(c.n); err != nil {
			return p, err
		}
	}
	p.l = l
	p.ownerKey = make([]any, len(ps))
	seen := make(map[any]bool, len(ps))
	for i, o := range ps {
		k, ok := keyOf(owner.kit.value(o, l.ownerCol))
		if !ok {
			continue
		}
		p.ownerKey[i] = k
		if !seen[k] {
			seen[k] = true
			p.keys = append(p.keys, k)
		}
	}
	p.st = cloneStatement(&q.st)
	if q.after != nil {
		if err := addAfter(&p.st, q.in, q.after); err != nil {
			return p, err
		}
	}
	p.st.Where = slices.Clip(append(p.st.Where, sp.where...))
	return p, nil
}

// eachRefused names what of st LoadEach and CountEach refuse, or is "".
func eachRefused(st *Statement) string {
	switch {
	case st.Limit > 0:
		return "Limit; Rel.LimitEach limits each owner's rows"
	case st.Offset > 0:
		return "Offset; page each owner's rows with After"
	case st.Lock == LockForUpdate:
		return "ForUpdate; a per-owner read locks nothing"
	case st.Lock == LockForShare:
		return "ForShare; a per-owner read locks nothing"
	case st.LockWait == LockSkipLocked:
		return "SkipLocked; a per-owner read locks nothing"
	case st.LockWait == LockNoWait:
		return "NoWait; a per-owner read locks nothing"
	}
	return ""
}

// eachClause is the Each of a per-owner statement of l with per-owner limit
// n, without its keys.
func eachClause(l link, n int) EachClause {
	if l.join != nil {
		return EachClause{Join: l.join, JoinOwner: l.joinOwner, JoinTarget: l.joinTarget, Target: l.targetCol, Limit: n}
	}
	return EachClause{Column: l.targetCol, Limit: n}
}

// chunk is st with Each ec holding keys, for one statement of a chunk; its
// slices are clipped, so that nothing appended to one chunk's reaches
// another's.
func chunk(st Statement, ec EachClause, keys []any) Statement {
	ec.Keys = keys
	st.Each = &ec
	st.Where, st.OrderBy, st.Columns = slices.Clip(st.Where), slices.Clip(st.OrderBy), slices.Clip(st.Columns)
	return st
}

// loadEach is LoadEach's body: each owner's rows, as a []T in an any.
func loadEach(ctx context.Context, q eachQuery, owner *EntityInfo, ownerErr error, sp loadSpec, ps []unsafe.Pointer) ([]any, error) {
	p, err := planEach("LoadEach", q, owner, ownerErr, sp, ps)
	if err != nil {
		return nil, err
	}
	l, t, db := p.l, p.l.target, q.db
	loads := append(slices.Clip(q.loads), sp.with...)
	st := p.st
	if st.Columns != nil && !slices.Contains(st.Columns, l.targetCol) {
		st.Columns = append(slices.Clip(st.Columns), l.targetCol)
	}
	if err := checkLoads(t, st.Columns, loads); err != nil {
		return nil, err
	}
	st.Unbounded = true
	st.OrderBy = tieBreak(t, append(slices.Clip(st.OrderBy), sp.order...))
	if sp.maxRows > 0 {
		st.MaxRows = sp.maxRows
	}
	var key []int // the column a Rewrite hook may not leave out
	if l.join == nil {
		key = []int{l.targetCol}
	}
	ec := eachClause(l, sp.each)
	per := keysPer(db.d, t, st.Where)
	var parts, owners []any
	var at target
	err = byChunks(len(p.keys), per, func(lo, hi int) error {
		c := chunk(st, ec, p.keys[lo:hi])
		part, own, err := readPart(ctx, db, target{}, &c, t, key, loads)
		if err != nil {
			return err
		}
		if lo == 0 {
			at = c.ran
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
			out[i] = t.kit.empty()
		}
		return out, nil
	}
	_, targets := joinBlocks(parts)
	if len(loads) > 0 && len(targets) > 0 {
		if err := loadRelations(ctx, db, db.loadPin(at), t, targets, loads, st.MaxRows); err != nil {
			return nil, err
		}
	}
	// Each owner key's rows, as indexes into targets, in the order read.
	byKey := make(map[any][]int, len(p.keys))
	for i, ptr := range targets {
		var k any
		ok := false
		if l.join != nil {
			k, ok = keyOf(owners[i])
		} else {
			k, ok = keyOf(t.kit.value(ptr, l.targetCol))
		}
		if ok {
			byKey[k] = append(byKey[k], i)
		}
	}
	idx := make([][]int, len(p.ownerKey))
	for i, k := range p.ownerKey {
		if k != nil {
			idx[i] = byKey[k]
		}
	}
	picked := t.kit.pick(targets, idx)
	for i, k := range p.ownerKey {
		if k == nil {
			out[i] = t.kit.empty()
			continue
		}
		out[i] = picked[i]
	}
	return out, nil
}

// countEach is CountEach's body.
func countEach(ctx context.Context, q eachQuery, owner *EntityInfo, ownerErr error, sp loadSpec, ps []unsafe.Pointer) ([]int64, error) {
	p, err := planEach("CountEach", q, owner, ownerErr, sp, ps)
	if err != nil {
		return nil, err
	}
	l, t, db := p.l, p.l.target, q.db
	st := p.st
	st.Count, st.Columns, st.OrderBy, st.MaxRows = true, nil, nil, 0
	// The owner's key, as the statement reads it: the join's or the target's.
	typ := GoType(t.Columns[l.targetCol])
	if l.join != nil {
		typ = GoType(l.join.Columns[l.joinOwner])
	}
	ec := eachClause(l, 0)
	per := keysPer(db.d, t, st.Where)
	counts := make(map[any]int64, len(p.keys))
	read := func(r rows) error {
		for r.Next() {
			k := reflect.New(typ)
			var n int64
			if err := r.Scan(k.Interface(), &n); err != nil {
				return err
			}
			if key, ok := keyOf(k.Elem().Interface()); ok {
				counts[key] = n // an around hook's retry reads the same
			}
		}
		return nil
	}
	err = byChunks(len(p.keys), per, func(lo, hi int) error {
		c := chunk(st, ec, p.keys[lo:hi])
		if db.hooked() {
			return db.run(ctx, &c, read, nil)
		}
		return db.plain(ctx, &c, read, nil)
	})
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(ps))
	for i, k := range p.ownerKey {
		if k != nil {
			out[i] = counts[k]
		}
	}
	return out, nil
}
