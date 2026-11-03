package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
)

// eachPlan is what LoadEach and CountEach share: the relation, the owners'
// distinct keys and each owner's (nil for an owner whose key is NULL), and
// the statement every chunk starts from, with q's conditions, After's and
// r's.
type eachPlan struct {
	L        Link
	Keys     []any
	OwnerKey []any
	St       Statement
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

// makeEachClause is the Each of a per-owner statement of l with per-owner limit
// n, without its keys.
func makeEachClause(l Link, n int) EachClause {
	if l.Join != nil {
		return EachClause{Join: l.Join, JoinOwner: l.JoinOwner, JoinTarget: l.JoinTarget, Target: l.TargetCol, Limit: n}
	}
	return EachClause{Column: l.TargetCol, Limit: n}
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

// EachQuery is what LoadEach and CountEach read of their Query, without its
// type.
type EachQuery struct {
	DB    *DB
	In    *EntityInfo
	Err   error // the query's metadata's or conditions' error
	St    Statement
	After unsafe.Pointer // After's row, or nil
	Loads []LoadSpec
	// Joins reads the columns of St's joins, which follow the target's own in
	// each row; nil when St has none.
	Joins *JoinScan
}

// planEach checks fn's (LoadEach's or CountEach's) arguments and plans its
// statements.
func planEach(fn string, q EachQuery, owner *EntityInfo, ownerErr error, sp LoadSpec, ps []unsafe.Pointer) (eachPlan, error) {
	var p eachPlan
	switch {
	case ownerErr != nil:
		return p, ownerErr
	case q.Err != nil:
		return p, q.Err
	case q.In == nil || q.DB == nil:
		return p, fmt.Errorf("velox: %s was given a Query that DB.Query did not make", fn)
	}
	if err := SpecErr(sp); err != nil {
		return p, err
	}
	if m := eachRefused(&q.St); m != "" {
		return p, fmt.Errorf("velox: %s: %s refuses a query with %s", q.In.Name, fn, m)
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
	if infoKit(owner) == nil || infoKit(l.Target) == nil {
		return p, fmt.Errorf("velox: %s: relation %s: the entities have no scan plan; run go tool velox-gen", owner.Name, r.Name)
	}
	if l.Target != q.In {
		return p, fmt.Errorf("velox: %s: %s: the relation %s is to %s, not to the query's %s", owner.Name, fn, r.Name, l.Target.Name, q.In.Name)
	}
	for _, c := range sp.Where {
		if err := ResolveErr(PredNode(c)); err != nil {
			return p, err
		}
	}
	p.L = l
	p.OwnerKey = make([]any, len(ps))
	seen := make(map[any]bool, len(ps))
	for i, o := range ps {
		k, ok := keyOf(infoKit(owner).Value(o, l.OwnerCol))
		if !ok {
			continue
		}
		p.OwnerKey[i] = k
		if !seen[k] {
			seen[k] = true
			p.Keys = append(p.Keys, k)
		}
	}
	p.St = CloneStatement(&q.St)
	if q.After != nil {
		if err := AddAfter(&p.St, q.In, q.After); err != nil {
			return p, err
		}
	}
	p.St.Where = slices.Clip(append(p.St.Where, sp.Where...))
	return p, nil
}

// LoadEach is LoadEach's body: each owner's rows, as a []T in an any.
func LoadEach(ctx context.Context, q EachQuery, owner *EntityInfo, ownerErr error, sp LoadSpec, ps []unsafe.Pointer) ([]any, error) {
	p, err := planEach("LoadEach", q, owner, ownerErr, sp, ps)
	if err != nil {
		return nil, err
	}
	l, t, db := p.L, p.L.Target, q.DB
	loads := append(slices.Clip(q.Loads), sp.With...)
	st := p.St
	if st.Columns != nil && !slices.Contains(st.Columns, l.TargetCol) {
		st.Columns = append(slices.Clip(st.Columns), l.TargetCol)
	}
	if err := CheckLoads(t, st.Columns, loads); err != nil {
		return nil, err
	}
	st.Unbounded = true
	st.OrderBy = tieBreak(t, append(slices.Clip(st.OrderBy), sp.Order...))
	if sp.MaxRows > 0 {
		st.MaxRows = sp.MaxRows
	}
	var key []int // the column a Rewrite hook may not leave out
	if l.Join == nil {
		key = []int{l.TargetCol}
	}
	ec := makeEachClause(l, sp.Each)
	per := keysPer(db.D, t, st.Where)
	var parts, owners []any
	var at Target
	err = byChunks(len(p.Keys), per, func(lo, hi int) error {
		c := chunk(st, ec, p.Keys[lo:hi])
		part, own, err := readPart(ctx, db, Target{}, &c, t, key, loads, q.Joins)
		if err != nil {
			return err
		}
		if lo == 0 {
			at = StmtRan(&c)
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
			out[i] = infoKit(t).Empty()
		}
		return out, nil
	}
	all, targets := joinBlocks(parts)
	if len(loads) > 0 && len(targets) > 0 {
		if err := LoadRelations(ctx, db, db.LoadPin(at), t, targets, loads, st.MaxRows); err != nil {
			return nil, err
		}
	}
	// Each owner key's rows, as indexes into targets, in the order read.
	group := make(map[any]int, len(p.Keys))
	for _, k := range p.Keys {
		addKey(group, k)
	}
	var x keyRows
	x.Fill(group, len(targets), func(i int) (any, bool) {
		if l.Join != nil {
			return keyOf(owners[i])
		}
		return keyOf(infoKit(t).Value(targets[i], l.TargetCol))
	})
	idx := make([][]int, len(p.OwnerKey))
	for i, k := range p.OwnerKey {
		if k != nil {
			idx[i] = x.Of(group[k])
		}
	}
	picked := infoKit(t).Pick(all, targets, idx)
	for i, k := range p.OwnerKey {
		if k == nil {
			out[i] = infoKit(t).Empty()
			continue
		}
		out[i] = picked[i]
	}
	return out, nil
}

// CountEach is CountEach's body.
func CountEach(ctx context.Context, q EachQuery, owner *EntityInfo, ownerErr error, sp LoadSpec, ps []unsafe.Pointer) ([]int64, error) {
	p, err := planEach("CountEach", q, owner, ownerErr, sp, ps)
	if err != nil {
		return nil, err
	}
	l, t, db := p.L, p.L.Target, q.DB
	st := p.St
	st.Count, st.Columns, st.OrderBy, st.MaxRows = true, nil, nil, 0
	// The owner's key, as the statement reads it: the join's or the target's.
	typ := GoType(t.Columns[l.TargetCol])
	if l.Join != nil {
		typ = GoType(l.Join.Columns[l.JoinOwner])
	}
	ec := makeEachClause(l, 0)
	per := keysPer(db.D, t, st.Where)
	counts := make(map[any]int64, len(p.Keys))
	read := func(r Rows) error {
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
	err = byChunks(len(p.Keys), per, func(lo, hi int) error {
		c := chunk(st, ec, p.Keys[lo:hi])
		if db.Hooked() {
			return db.Run(ctx, &c, read, nil)
		}
		return db.Plain(ctx, &c, read, nil)
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
