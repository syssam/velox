package velox

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unsafe"
)

// loadSpec is what Query.With loads: relation rel of the query's entity,
// filtered, ordered, and with relations of its own.
type loadSpec struct {
	rel     int32
	target  useTarget // the relation's target, named by Rel
	where   []Predicate
	order   []OrderTerm
	with    []loadSpec
	each    int // Rel.LimitEach's n: the most rows per owner; 0: no limit
	maxRows int // Rel.MaxRows's n: the load's own cap; 0: the owner's
	err     error
}

// specErr is the first error a load carries or one of its nested loads does.
func specErr(sp loadSpec) error {
	if sp.err != nil {
		return sp.err
	}
	for _, n := range sp.with {
		if err := specErr(n); err != nil {
			return err
		}
	}
	return nil
}

// specRelation resolves the relation a load names on owner.
func specRelation(owner *EntityInfo, sp loadSpec) (*relation, link, error) {
	if int(sp.rel) < 0 || int(sp.rel) >= len(owner.rels) {
		return nil, link{}, fmt.Errorf("velox: %s: has no relation %d; run go tool velox-gen", owner.Name, sp.rel)
	}
	r := owner.rels[sp.rel]
	l, err := r.resolve(owner, sp.target)
	return r, l, err
}

// checkLoads reports what is wrong with loads before the owner query runs: an
// error in their options, a relation that does not exist or resolve, and (for
// the owner query, whose columns are cols; nil selects all) a projection that
// leaves out the column a relation is keyed by. Nested loads are checked
// against their own entity.
func checkLoads(owner *EntityInfo, cols []int, specs []loadSpec) error {
	for _, sp := range specs {
		if sp.err != nil {
			return sp.err
		}
		// The load's conditions resolve with its statement, after the owner
		// query; a mistake in them is reported before it.
		for _, p := range sp.where {
			if err := resolveErr(p.n); err != nil {
				return err
			}
		}
		r, l, err := specRelation(owner, sp)
		if err != nil {
			return err
		}
		if cols != nil && !slices.Contains(cols, l.ownerCol) {
			return fmt.Errorf("velox: %s: With(%s) needs the column %s selected", owner.Name, r.name, owner.Columns[l.ownerCol].Name)
		}
		if err := checkLoads(l.target, nil, sp.with); err != nil {
			return err
		}
	}
	return nil
}

// loadError is the error of a relation load: err, the target's error, named
// by the owner and relation. Its text drops err's own "velox: ", so the
// prefix does not double; errors.Is and As still see err.
type loadError struct {
	owner, rel string
	err        error
}

func (e *loadError) Error() string {
	return "velox: " + e.owner + ": loading " + e.rel + ": " + strings.TrimPrefix(e.err.Error(), "velox: ")
}

func (e *loadError) Unwrap() error { return e.err }

// keyOf returns a key value as a map key: a pointer is followed, and a nil
// pointer is no key.
func keyOf(v any) (any, bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		return rv.Elem().Interface(), true
	}
	return v, true
}

// pinnedTo returns the DB a query's With loads run on: db, or on a DB with
// replicas or shards a copy whose reads go to t, the connection the query ran
// on. Parents and children are then read from one replica, with one replication
// lag: children read from a replica further behind could be missing for a
// parent read from one further ahead. On a sharded DB the loads run on the
// query's shard without looking it up again.
func (db *DB) pinnedTo(t target) *DB {
	if db.rt == nil || db.inTx || t.ex == nil {
		return db // every read runs on one connection already
	}
	p := *db
	p.pin = t
	return &p
}

// loadRelations loads each spec's relation for the owners at ptrs. maxRows is
// the owner query's MaxRows, which its loads, nested ones too, share unless a
// load says MaxRows; 0 is the DB's cap.
func loadRelations(ctx context.Context, db *DB, owner *EntityInfo, ptrs []unsafe.Pointer, specs []loadSpec, maxRows int) error {
	for _, sp := range specs {
		if err := loadOne(ctx, db, owner, ptrs, sp, maxRows); err != nil {
			return err
		}
	}
	return nil
}

func loadOne(ctx context.Context, db *DB, owner *EntityInfo, ptrs []unsafe.Pointer, sp loadSpec, maxRows int) error {
	if sp.err != nil {
		return sp.err
	}
	r, l, err := specRelation(owner, sp)
	if err != nil {
		return err
	}
	t := l.target
	if sp.maxRows > 0 {
		maxRows = sp.maxRows // for its nested loads too
	}
	if owner.kit == nil || t.kit == nil || (l.join != nil && l.join.kit == nil) {
		return fmt.Errorf("velox: %s: relation %s: the entities have no scan plan; run go tool velox-gen", owner.Name, r.name)
	}
	// The distinct keys, in the order the owners have them; and each owner's key.
	keys := make([]any, 0, len(ptrs))
	ownerKey := make([]any, len(ptrs))
	seen := make(map[any]bool, len(ptrs))
	for i, p := range ptrs {
		k, ok := keyOf(owner.kit.value(p, l.ownerCol))
		if !ok {
			continue
		}
		ownerKey[i] = k
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	// Every owner is loaded, even one with no key or no related row.
	byKey := make(map[any][]int, len(keys)) // key → indexes into all
	var all any
	var targets []unsafe.Pointer
	switch {
	case len(keys) == 0:
	case l.join != nil && sp.each > 0:
		if all, targets, err = fetchEach(ctx, db, l, sp, keys, maxRows, byKey); err != nil {
			return &loadError{owner: owner.Name, rel: r.name, err: err}
		}
	case l.join != nil:
		if all, targets, err = fetchThrough(ctx, db, owner, r, l, sp, keys, maxRows, byKey); err != nil {
			return err
		}
	default:
		var ec *EachClause
		if sp.each > 0 {
			ec = &EachClause{Column: l.targetCol, Limit: sp.each}
		}
		if all, targets, err = fetch(ctx, db, t, []int{l.targetCol}, sp, keys, maxRows, ec); err != nil {
			return &loadError{owner: owner.Name, rel: r.name, err: err}
		}
		for i, p := range targets {
			if k, ok := keyOf(t.kit.value(p, l.targetCol)); ok {
				byKey[k] = append(byKey[k], i)
			}
		}
	}
	// When a query ran, all holds its []T, so it is not nil even when the
	// []T is empty; it is nil only when nothing was fetched (no owner had a
	// key, or no join row held a target key), and there is nothing to load.
	if all != nil && len(sp.with) > 0 {
		if err := loadRelations(ctx, db, t, targets, sp.with, maxRows); err != nil {
			return err
		}
	}
	for i, p := range ptrs {
		h := (*holder)(unsafe.Add(p, r.off))
		idx := byKey[ownerKey[i]]
		switch {
		case r.many && all != nil:
			h.v = t.kit.pick(all, idx)
		case r.many:
			h.v = t.kit.empty() // no keys, so nothing was fetched: still loaded, and empty
		case len(idx) > 0:
			h.v = t.kit.ptr(all, idx[0])
		default:
			h.v = nil
		}
		h.loaded = true
	}
	return nil
}

// fetchThrough reads the targets of a many-to-many relation for the owner
// keys: the join entity's rows for them, then the targets of the keys those
// rows hold, which are the ones sp's options apply to. It fills byKey with
// each owner key's targets, as indexes into the targets returned, in their
// fetched order and each once. It returns a nil all when the join rows hold
// no target key, and so no target was fetched. Its errors name the owner and
// the relation.
//
// The targets are read in statements that each hold the target keys of a run
// of whole owners, so that each owner's come from one query, in the order it
// asked for; a target of owners in two statements is read, and held, twice.
func fetchThrough(ctx context.Context, db *DB, owner *EntityInfo, r *relation, l link, sp loadSpec, keys []any, maxRows int, byKey map[any][]int) (any, []unsafe.Pointer, error) {
	j, t := l.join, l.target
	_, rows, err := fetch(ctx, db, j, []int{l.joinOwner, l.joinTarget}, loadSpec{}, keys, maxRows, nil)
	if err != nil {
		return nil, nil, &loadError{owner: owner.Name, rel: r.name, err: err}
	}
	// Each owner key's target keys, in join-row order and each once.
	byOwner := make(map[any][]any, len(keys))
	seen := make(map[[2]any]bool, len(rows))
	for _, p := range rows {
		own, okOwner := keyOf(j.kit.value(p, l.joinOwner))
		tk, okTarget := keyOf(j.kit.value(p, l.joinTarget))
		if !okOwner || !okTarget || seen[[2]any{own, tk}] {
			continue
		}
		seen[[2]any{own, tk}] = true
		byOwner[own] = append(byOwner[own], tk)
	}
	// The statements: each holds the distinct target keys of a run of
	// owners, in the owners' order, at most per of them.
	type chunk struct{ keys, owners []any }
	order, per := fetchPlan(db, t, sp)
	var chunks []chunk
	var cur chunk
	inCur := map[any]bool{}
	for _, own := range keys {
		tks := byOwner[own]
		if len(tks) > per {
			return nil, nil, fmt.Errorf("velox: %s: relation %s: an owner has %d related rows, more than one statement can load in order", owner.Name, r.name, len(tks))
		}
		if len(tks) == 0 {
			continue
		}
		added := 0
		for _, tk := range tks {
			if !inCur[tk] {
				added++
			}
		}
		if len(cur.keys)+added > per {
			chunks = append(chunks, cur)
			cur, inCur = chunk{}, map[any]bool{}
		}
		for _, tk := range tks {
			if !inCur[tk] {
				inCur[tk] = true
				cur.keys = append(cur.keys, tk)
			}
		}
		cur.owners = append(cur.owners, own)
	}
	if len(cur.keys) == 0 {
		return nil, nil, nil
	}
	chunks = append(chunks, cur)
	parts := make([]any, len(chunks)) // each chunk's []T
	for i, c := range chunks {
		if parts[i], _, err = fetchPart(ctx, db, t, []int{l.targetCol}, sp, order, c.keys, maxRows, nil); err != nil {
			return nil, nil, &loadError{owner: owner.Name, rel: r.name, err: err}
		}
	}
	all, targets := t.kit.join(parts)
	start := 0 // the chunk's first index into all
	for i, c := range chunks {
		end := start + reflect.ValueOf(parts[i]).Len()
		byTarget := make(map[any][]int, end-start) // target key → indexes into all
		for k := start; k < end; k++ {
			if tk, ok := keyOf(t.kit.value(targets[k], l.targetCol)); ok {
				byTarget[tk] = append(byTarget[tk], k)
			}
		}
		for _, own := range c.owners {
			var idx []int
			for _, tk := range byOwner[own] {
				idx = append(idx, byTarget[tk]...)
			}
			slices.Sort(idx) // the fetched order, which sp's OrderBy set
			byKey[own] = idx
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
func fetch(ctx context.Context, db *DB, t *EntityInfo, cols []int, sp loadSpec, keys []any, maxRows int, ec *EachClause) (any, []unsafe.Pointer, error) {
	order, per := fetchPlan(db, t, sp)
	if ec != nil {
		order = tieBreak(t, order)
	}
	var parts []any // each chunk's []T
	for start := 0; start < len(keys); start += per {
		part, _, err := fetchPart(ctx, db, t, cols, sp, order, keys[start:min(start+per, len(keys))], maxRows, ec)
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, part)
	}
	all, ptrs := t.kit.join(parts) // pointers into the one slice pick and ptr index
	return all, ptrs, nil
}

// fetchEach reads the targets of a many-to-many relation with LimitEach for
// the owner keys, each owner's first sp.each in sp's order, in one statement
// per chunk of keys that joins the join entity and reads each row's owner key
// from it. It fills byKey with each owner key's targets, as indexes into the
// targets returned, in their order; a target of two owners is read, and held,
// once for each.
func fetchEach(ctx context.Context, db *DB, l link, sp loadSpec, keys []any, maxRows int, byKey map[any][]int) (any, []unsafe.Pointer, error) {
	t := l.target
	order, per := fetchPlan(db, t, sp)
	order = tieBreak(t, order)
	ec := &EachClause{Join: l.join, JoinOwner: l.joinOwner, JoinTarget: l.joinTarget, Target: l.targetCol, Limit: sp.each}
	var parts, owners []any
	for start := 0; start < len(keys); start += per {
		part, own, err := fetchPart(ctx, db, t, nil, sp, order, keys[start:min(start+per, len(keys))], maxRows, ec)
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, part)
		owners = append(owners, own...)
	}
	all, targets := t.kit.join(parts)
	for i, own := range owners {
		if k, ok := keyOf(own); ok {
			byKey[k] = append(byKey[k], i)
		}
	}
	return all, targets, nil
}

// tieBreak returns order with t's primary key columns it lacks added, so
// that rows equal in order are ranked, and paged, the same way every time.
func tieBreak(t *EntityInfo, order []OrderTerm) []OrderTerm {
	out := slices.Clip(order)
	for _, k := range t.PrimaryKey {
		if !slices.ContainsFunc(order, func(o OrderTerm) bool { return o.Column == k }) {
			out = append(out, OrderTerm{Column: k})
		}
	}
	return out
}

// fetchPlan returns the order a relation's rows are read in, and how many
// keys one statement holds. 64 parameters are left for conditions that
// rewrite hooks add, such as a tenant's.
func fetchPlan(db *DB, t *EntityInfo, sp loadSpec) ([]OrderTerm, int) {
	order := sp.order
	if len(order) == 0 {
		for _, k := range t.PrimaryKey {
			order = append(order, OrderTerm{Column: k})
		}
	}
	fixed := 0 // the parameters of the filter
	for _, p := range sp.where {
		w := writer{d: db.d, in: t, argsOnly: true}
		w.pred(p.n)
		fixed += len(w.args)
	}
	return order, max(1, maxParams[db.d]-fixed-64)
}

// fetchPart reads, in one statement, t's rows whose column cols[0] holds one
// of keys, with sp's options, in order. A Rewrite hook may not leave out any
// of cols, the columns the caller reads.
//
// With ec, the statement's Each is ec with keys as its Keys, and reads each
// owner's rows apart; its cap is the load's own MaxRows, else the bound of
// ec's Limit. When ec has a Join, cols is nil and fetchPart returns each
// row's owner key too, read from the join entity.
func fetchPart(ctx context.Context, db *DB, t *EntityInfo, cols []int, sp loadSpec, order []OrderTerm, keys []any, maxRows int, ec *EachClause) (any, []any, error) {
	st := Statement{Op: OpSelect, Entity: t, Unbounded: true, MaxRows: maxRows, InTx: db.inTx, OrderBy: order}
	if ec != nil {
		each := *ec
		each.Keys = keys
		st.Each, st.MaxRows, st.Where = &each, sp.maxRows, slices.Clip(sp.where)
	} else {
		st.Where = append([]Predicate{{node{op: opIn, col: int32(cols[0]), v: keys}}}, sp.where...)
	}
	var part any
	var owners []any
	read := func(r rows) (err error) {
		// st is as the hooks left it.
		if st.Columns != nil {
			for _, c := range cols {
				if !slices.Contains(st.Columns, c) {
					return fmt.Errorf("velox: %s: a Rewrite hook left out the relation's key column %s", t.Name, t.Columns[c].Name)
				}
			}
		}
		if len(sp.with) > 0 && st.Columns != nil {
			if err := checkLoads(t, st.Columns, sp.with); err != nil {
				return err
			}
		}
		if ec != nil && ec.Join != nil {
			or := &ownerRows{rows: r, typ: GoType(ec.Join.Columns[ec.JoinOwner])}
			part, err = t.kit.scan(or, st.Columns)
			owners = or.keys
			return err
		}
		part, err = t.kit.scan(r, st.Columns)
		return err
	}
	var err error
	if db.hooked() {
		err = db.run(ctx, &st, read, nil)
	} else {
		err = db.plain(ctx, &st, read, nil)
	}
	return part, owners, err
}

// ownerRows reads, after the columns its Scan is given, one more: the owner
// key a many-to-many read selects last, into a new value of type typ (the join
// column's), which it keeps in keys, one per row.
type ownerRows struct {
	rows
	typ  reflect.Type
	keys []any
}

func (r *ownerRows) Scan(dst ...any) error {
	p := reflect.New(r.typ)
	all := make([]any, len(dst)+1)
	copy(all, dst)
	all[len(dst)] = p.Interface()
	if err := r.rows.Scan(all...); err != nil {
		return err
	}
	r.keys = append(r.keys, p.Elem().Interface())
	return nil
}
