package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unsafe"
)

// LoadSpec is what Query.With loads: relation rel of the query's entity,
// filtered, ordered, and with relations of its own.
type LoadSpec struct {
	Rel     int32
	Target  RelTarget // the relation's target, named by Rel
	Where   []Predicate
	Order   []OrderTerm
	With    []LoadSpec
	Each    int // Rel.LimitEach's n: the most rows per owner; 0: no limit
	MaxRows int // Rel.MaxRows's n: the load's own cap; 0: the owner's
	Err     error
}

// SpecErr is the first error a load carries or one of its nested loads does.
func SpecErr(sp LoadSpec) error {
	if sp.Err != nil {
		return sp.Err
	}
	for _, n := range sp.With {
		if err := SpecErr(n); err != nil {
			return err
		}
	}
	return nil
}

// SpecRelation resolves the relation a load names on owner.
func SpecRelation(owner *EntityInfo, sp LoadSpec) (*Relation, Link, error) {
	if int(sp.Rel) < 0 || int(sp.Rel) >= len(owner.rels) {
		return nil, Link{}, fmt.Errorf("velox: %s: has no relation %d; run go tool velox-gen", owner.Name, sp.Rel)
	}
	r := owner.rels[sp.Rel]
	l, err := r.Resolve(owner, sp.Target)
	return r, l, err
}

// CheckLoads reports what is wrong with loads before the owner query runs: an
// error in their options, a relation that does not exist or resolve, and (for
// the owner query, whose columns are cols; nil selects all) a projection that
// leaves out the column a relation is keyed by. Nested loads are checked
// against their own entity.
func CheckLoads(owner *EntityInfo, cols []int, specs []LoadSpec) error {
	for _, sp := range specs {
		if sp.Err != nil {
			return sp.Err
		}
		// The load's conditions resolve with its statement, after the owner
		// query; a mistake in them is reported before it.
		for _, p := range sp.Where {
			if err := ResolveErr(p.n); err != nil {
				return err
			}
		}
		r, l, err := SpecRelation(owner, sp)
		if err != nil {
			return err
		}
		if cols != nil && !slices.Contains(cols, l.OwnerCol) {
			return fmt.Errorf("velox: %s: With(%s) needs the column %s selected", owner.Name, r.Name, owner.Columns[l.OwnerCol].Name)
		}
		if err := CheckLoads(l.Target, nil, sp.With); err != nil {
			return err
		}
	}
	return nil
}

// LoadError is the error of a relation load: err, the target's error, named
// by the owner and relation. Its text drops err's own "velox: ", so the
// prefix does not double; errors.Is and As still see err.
type LoadError struct {
	Owner, Rel string
	Err        error
}

func (e *LoadError) Error() string {
	return "velox: " + e.Owner + ": loading " + e.Rel + ": " + strings.TrimPrefix(e.Err.Error(), "velox: ")
}

func (e *LoadError) Unwrap() error { return e.Err }

// KeyOf returns a key value as a map key: a pointer is followed, and a nil
// pointer is no key.
func KeyOf(v any) (any, bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		return rv.Elem().Interface(), true
	}
	return v, true
}

// ThroughChunk is one statement of throughChunks: the distinct target keys of
// a run of whole owners.
type ThroughChunk struct{ Keys, Owners []any }

// ThroughPlan returns the chunks of the owner keys keys, each the target keys
// of a run of whole owners, in the owners' order, at most per of them. An
// owner with more than per, whose rows cannot come from one statement, ends
// it: it returns no chunk, and that owner's count of target keys (over).
// byOwner holds each owner key's target keys; an owner with none is in no
// chunk.
func ThroughPlan(keys []any, byOwner map[any][]any, per int) (chunks []ThroughChunk, over int) {
	var cur ThroughChunk
	inCur := map[any]bool{}
	for _, own := range keys {
		tks := byOwner[own]
		if len(tks) > per {
			return nil, len(tks)
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
		if len(cur.Keys)+added > per {
			chunks = append(chunks, cur)
			cur, inCur = ThroughChunk{}, map[any]bool{}
		}
		for _, tk := range tks {
			if !inCur[tk] {
				inCur[tk] = true
				cur.Keys = append(cur.Keys, tk)
			}
		}
		cur.Owners = append(cur.Owners, own)
	}
	if len(cur.Keys) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks, 0
}

// TieBreak returns order with t's primary key columns it lacks added, so
// that rows equal in order are ranked, and paged, the same way every time.
func TieBreak(t *EntityInfo, order []OrderTerm) []OrderTerm {
	out := slices.Clip(order)
	for _, k := range t.PrimaryKey {
		if !slices.ContainsFunc(order, func(o OrderTerm) bool { return o.Column == k }) {
			out = append(out, OrderTerm{Column: k})
		}
	}
	return out
}

// KeysPer is how many keys one statement on t holds beside the conditions
// ps: 64 parameters are left for conditions that rewrite hooks add, such as a
// tenant's. A statement that still overflows, because a policy's or a hook's
// conditions take more, is split again (byChunks).
func KeysPer(d Dialect, t *EntityInfo, ps []Predicate) int {
	// The relations in ps resolve with the statement; one that does not
	// fails it there. Writing needs them resolved.
	ps, err := resolvePreds(ps)
	if err != nil {
		return 1
	}
	fixed := 0 // the parameters of the filter
	for _, p := range ps {
		w := writer{d: d, in: t, argsOnly: true}
		w.pred(p.n)
		fixed += len(w.args)
	}
	return max(1, MaxParams[d]-fixed-64)
}

// OwnerRows reads, after the columns its Scan is given, one more: the owner
// key a many-to-many read selects last, into a new value of type typ (the join
// column's), which it keeps in keys, one per row.
type OwnerRows struct {
	Rows
	Typ  reflect.Type
	Keys []any
}

func (r *OwnerRows) Scan(dst ...any) error {
	p := reflect.New(r.Typ)
	all := make([]any, len(dst)+1)
	copy(all, dst)
	all[len(dst)] = p.Interface()
	if err := r.Rows.Scan(all...); err != nil {
		return err
	}
	r.Keys = append(r.Keys, p.Elem().Interface())
	return nil
}

// Blocks holds the rows a kit's scan reads, in arrays of its type that never
// move once made, each twice the size of the one before up to blockMax rows,
// so that a pointer to a row stays valid and no row is copied as more arrive.
// It is not generic, so that it is compiled once, not in every entity's
// package, and its fields name no reflect type.
type Blocks struct {
	typ  any     // a row's reflect.Type
	size uintptr // a row's size
	bs   []block
	N    int      // rows in all of bs
	in   [6]block // bs's first arrays, up to 252 rows, without an allocation
}

// NewBlocks returns an empty blocks of rows of typ, a reflect.Type held as
// any, as ElemType returns it. It and next are not inlined: inlined into a
// kit's generic scan, their reflect calls would compile reflect's methods into
// every entity's package (TestGenericTypesCompileSmall).
//
//go:noinline
func NewBlocks(typ any) *Blocks {
	b := &Blocks{typ: typ, size: typ.(reflect.Type).Size()}
	b.bs = b.in[:0]
	return b
}

// block is one array of blocks: its first row, its rows in use and its size.
type block struct {
	at        unsafe.Pointer
	used, cap int
}

// blockMax is the most rows in one array of blocks.
const blockMax = 1024

// next returns a pointer to a new zero row after the others.
//
//go:noinline
func (b *Blocks) Next() unsafe.Pointer {
	if len(b.bs) == 0 || b.bs[len(b.bs)-1].used == b.bs[len(b.bs)-1].cap {
		size := 4
		if len(b.bs) > 0 {
			size = min(2*b.bs[len(b.bs)-1].cap, blockMax)
		}
		v := reflect.MakeSlice(reflect.SliceOf(b.typ.(reflect.Type)), size, size)
		b.bs = append(b.bs, block{at: v.UnsafePointer(), cap: size})
	}
	k := &b.bs[len(b.bs)-1]
	p := unsafe.Add(k.at, uintptr(k.used)*b.size)
	k.used++
	b.N++
	return p
}

// JoinBlocks returns the parts a kit's scans read, each a *blocks, as one
// value that keeps their rows, and a pointer to each row, in order. A kit's
// pick and ptr read the rows through the pointers.
func JoinBlocks(parts []any) ([]any, []unsafe.Pointer) {
	n := 0
	for _, p := range parts {
		n += p.(*Blocks).N
	}
	ptrs := make([]unsafe.Pointer, 0, n)
	for _, p := range parts {
		b := p.(*Blocks)
		for _, k := range b.bs {
			for i := range k.used {
				ptrs = append(ptrs, unsafe.Add(k.at, uintptr(i)*b.size))
			}
		}
	}
	return parts, ptrs
}

// InPlace sets keep[i] when the rows of owner i in a kit's pick, idx[i], as
// indexes into the rows that JoinBlocks gave of all, lie one after another in
// one array of all's blocks, in that order, with no row another such owner
// has: the owner's slice can then be that run of the array, without a copy.
// used holds a flag for each row, all false. An owner with no row is not in
// place. Rows next to each other in memory but in two arrays are not one run:
// a slice of them would keep only the first array alive. It returns how many
// rows the owners not in place have, which pick copies.
func InPlace(all []any, idx [][]int, keep, used []bool) (copied int) {
	for i, x := range idx {
		run := len(x) > 0 && x[len(x)-1] < arrayEnd(all, x[0])
		for k := 0; run && k < len(x); k++ {
			run = x[k] == x[0]+k && !used[x[k]]
		}
		if !run {
			copied += len(x)
			continue
		}
		for _, j := range x {
			used[j] = true
		}
		keep[i] = true
	}
	return copied
}

// arrayEnd returns the index, into the rows that JoinBlocks gives of parts,
// after the last row of the array that holds row j.
func arrayEnd(parts []any, j int) int {
	n := 0
	for _, p := range parts {
		for _, k := range p.(*Blocks).bs {
			n += k.used
			if j < n {
				return n
			}
		}
	}
	return n
}

// AddKey gives k, when it is new to group, the next index, and returns its
// index, and whether it is new. group holds a load's distinct owner keys, each
// with its index in the order the owners have them; the caller holds the keys
// as a slice too.
func AddKey(group map[any]int, k any) (int, bool) {
	g, ok := group[k]
	if !ok {
		g = len(group)
		group[k] = g
	}
	return g, !ok
}

// KeyRows holds each owner key's rows, as indexes into the targets read for
// the keys, by the key's index (AddKey). It does not hold the key map: escape
// analysis follows a struct's fields as one, so the rows, which reach the
// heap, would take the map, which need not leave the stack, with them.
type KeyRows struct {
	// As fill leaves them: those of key g are flat[off[g]:off[g+1]]. set sets
	// rows instead.
	flat, off []int
	rows      [][]int
}

// fill sets the rows of each key in group from n targets, in their order, with
// one allocation: keyAt gives the key of the target at an index, or false when
// it has none. A target whose key is not in group is in no key's rows.
func (x *KeyRows) Fill(group map[any]int, n int, keyAt func(i int) (any, bool)) {
	k := len(group)
	buf := make([]int, 2*n+k+1)
	at, off, flat := buf[:n], buf[n:n+k+1], buf[n+k+1:]
	for i := range n {
		at[i] = -1
		if key, ok := keyAt(i); ok {
			if g, ok := group[key]; ok {
				at[i] = g
				off[g]++
			}
		}
	}
	sum := 0
	for g := range off {
		sum += off[g]
		off[g] = sum // the end of g's rows, until the loop below
	}
	for i := n - 1; i >= 0; i-- {
		if g := at[i]; g >= 0 {
			off[g]--
			flat[off[g]] = i
		}
	}
	x.flat, x.off = flat, off
}

// set sets the rows of k, a key in group.
func (x *KeyRows) Set(group map[any]int, k any, rows []int) {
	if x.rows == nil {
		x.rows = make([][]int, len(group))
	}
	x.rows[group[k]] = rows
}

// of returns the rows of the key at index g; g is -1 for an owner with no
// key, which has none.
func (x *KeyRows) Of(g int) []int {
	switch {
	case g < 0:
		return nil
	case x.rows != nil:
		return x.rows[g]
	case x.off != nil:
		return x.flat[x.off[g]:x.off[g+1]:x.off[g+1]]
	}
	return nil
}

// LoadPin returns the target a query's With loads read from: on a DB with
// replicas or shards, at, the connection the query ran on; the zero target
// otherwise. Parents and children are then read from one replica, with one
// replication lag: children read from a replica further behind could be
// missing for a parent read from one further ahead. On a sharded DB the loads
// run on the query's shard without looking it up again.
func (db *DB) LoadPin(at Target) Target {
	if db.Rt == nil || db.InTx || at.Ex == nil {
		return Target{} // every read runs on one connection already
	}
	return at
}

// LoadRelations loads each spec's relation for the owners at ptrs. maxRows is
// the owner query's MaxRows, which its loads, nested ones too, share unless a
// load says MaxRows; 0 is the DB's cap. pin is the target their reads go
// to (DB.LoadPin).
func LoadRelations(ctx context.Context, db *DB, pin Target, owner *EntityInfo, ptrs []unsafe.Pointer, specs []LoadSpec, maxRows int) error {
	for _, sp := range specs {
		if err := loadOne(ctx, db, pin, owner, ptrs, sp, maxRows); err != nil {
			return err
		}
	}
	return nil
}

func loadOne(ctx context.Context, db *DB, pin Target, owner *EntityInfo, ptrs []unsafe.Pointer, sp LoadSpec, maxRows int) error {
	if sp.Err != nil {
		return sp.Err
	}
	r, l, err := SpecRelation(owner, sp)
	if err != nil {
		return err
	}
	t := l.Target
	if sp.MaxRows > 0 {
		maxRows = sp.MaxRows // for its nested loads too
	}
	if InfoKit(owner) == nil || InfoKit(t) == nil || (l.Join != nil && InfoKit(l.Join) == nil) {
		return fmt.Errorf("velox: %s: relation %s: the entities have no scan plan; run go tool velox-gen", owner.Name, r.Name)
	}
	// The distinct keys, in the order the owners have them; and each owner's,
	// as its index into them, -1 for none.
	group := make(map[any]int, len(ptrs))
	keys := make([]any, 0, len(ptrs))
	ownerKey := make([]int, len(ptrs))
	for i, p := range ptrs {
		ownerKey[i] = -1
		if k, ok := KeyOf(InfoKit(owner).Value(p, l.OwnerCol)); ok {
			g, added := AddKey(group, k)
			if added {
				keys = append(keys, k)
			}
			ownerKey[i] = g
		}
	}
	// Every owner is loaded, even one with no key or no related row.
	var x KeyRows // each key's rows
	var all []any
	var targets []unsafe.Pointer
	switch {
	case len(keys) == 0:
	case l.Join != nil && sp.Each > 0:
		var owners []any
		if all, targets, owners, err = fetchEach(ctx, db, pin, l, sp, keys, maxRows); err != nil {
			return &LoadError{Owner: owner.Name, Rel: r.Name, Err: err}
		}
		x.Fill(group, len(owners), func(i int) (any, bool) { return KeyOf(owners[i]) })
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
			return &LoadError{Owner: owner.Name, Rel: r.Name, Err: err}
		}
		x.Fill(group, len(targets), func(i int) (any, bool) { return KeyOf(InfoKit(t).Value(targets[i], l.TargetCol)) })
	}
	// When a query ran, all holds its parts, so it is not nil even when they
	// hold no row; it is nil only when nothing was fetched (no owner had a
	// key, or no join row held a target key), and there is nothing to load.
	if all != nil && len(sp.With) > 0 {
		if err := LoadRelations(ctx, db, pin, t, targets, sp.With, maxRows); err != nil {
			return err
		}
	}
	var picked []any // each owner's []T, when r is to-many and a query ran
	if r.Many && all != nil {
		idx := make([][]int, len(ptrs))
		for i, g := range ownerKey {
			idx[i] = x.Of(g)
		}
		picked = InfoKit(t).Pick(all, targets, idx)
	}
	for i, p := range ptrs {
		h := (*Holder)(unsafe.Add(p, r.Off))
		idx := x.Of(ownerKey[i])
		switch {
		case r.Many && all != nil:
			h.V = picked[i]
		case r.Many:
			h.V = InfoKit(t).Empty() // no keys, so nothing was fetched: still loaded, and empty
		case len(idx) > 0:
			h.V = InfoKit(t).Ptr(targets[idx[0]])
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
func fetchThrough(ctx context.Context, db *DB, pin Target, owner *EntityInfo, r *Relation, l Link, sp LoadSpec, keys []any, maxRows int, set func(own any, rows []int)) ([]any, []unsafe.Pointer, error) {
	j := l.Join
	_, rows, err := fetch(ctx, db, pin, j, []int{l.JoinOwner, l.JoinTarget}, LoadSpec{}, keys, maxRows, nil)
	if err != nil {
		return nil, nil, &LoadError{Owner: owner.Name, Rel: r.Name, Err: err}
	}
	// Each owner key's target keys, in join-row order and each once.
	byOwner := make(map[any][]any, len(keys))
	seen := make(map[[2]any]bool, len(rows))
	for _, p := range rows {
		own, okOwner := KeyOf(InfoKit(j).Value(p, l.JoinOwner))
		tk, okTarget := KeyOf(InfoKit(j).Value(p, l.JoinTarget))
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
func throughChunks(ctx context.Context, db *DB, pin Target, owner *EntityInfo, r *Relation, l Link, sp LoadSpec, keys []any, byOwner map[any][]any, maxRows int, set func(own any, rows []int)) ([]any, []unsafe.Pointer, error) {
	t := l.Target
	order, per := fetchPlan(db, t, sp)
	var done []ThroughChunk // the chunks read
	var parts []any         // each one's *blocks
	var shrunk error        // the parameter error that last made per smaller
	rest := keys            // the owners not read yet
plan:
	for len(rest) > 0 {
		chunks, n := ThroughPlan(rest, byOwner, per)
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
			if pe, ok := as[*ParamsError](err); ok && per > 1 {
				per = max(1, min(per, len(c.Keys))-(pe.N-pe.Max))
				shrunk = pe
				rest = rest[slices.Index(rest, c.Owners[0]):]
				continue plan
			}
			if err != nil {
				return nil, nil, &LoadError{Owner: owner.Name, Rel: r.Name, Err: err}
			}
			done, parts = append(done, c), append(parts, part)
		}
		break
	}
	if len(done) == 0 {
		return nil, nil, nil
	}
	all, targets := JoinBlocks(parts)
	start := 0 // the chunk's first index into targets
	for i, c := range done {
		end := start + parts[i].(*Blocks).N
		byTarget := make(map[any][]int, end-start) // target key → indexes into targets
		for k := start; k < end; k++ {
			if tk, ok := KeyOf(InfoKit(t).Value(targets[k], l.TargetCol)); ok {
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
func fetch(ctx context.Context, db *DB, pin Target, t *EntityInfo, cols []int, sp LoadSpec, keys []any, maxRows int, ec *EachClause) ([]any, []unsafe.Pointer, error) {
	order, per := fetchPlan(db, t, sp)
	if ec != nil {
		order = TieBreak(t, order)
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
	all, ptrs := JoinBlocks(parts) // all holds the rows; pick and ptr read them through ptrs
	return all, ptrs, nil
}

// fetchEach reads the targets of a many-to-many relation with LimitEach for
// the owner keys, each owner's first sp.each in sp's order, in one statement
// per chunk of keys that joins the join entity and reads each row's owner key
// from it. It returns each target's owner key too, in the targets' order; a
// target of two owners is read, and held, once for each.
func fetchEach(ctx context.Context, db *DB, pin Target, l Link, sp LoadSpec, keys []any, maxRows int) ([]any, []unsafe.Pointer, []any, error) {
	t := l.Target
	order, per := fetchPlan(db, t, sp)
	order = TieBreak(t, order)
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
	all, targets := JoinBlocks(parts)
	return all, targets, owners, nil
}

// fetchPlan returns the order a relation's rows are read in, and how many
// keys one statement holds (KeysPer).
func fetchPlan(db *DB, t *EntityInfo, sp LoadSpec) ([]OrderTerm, int) {
	order := sp.Order
	if len(order) == 0 {
		for _, k := range t.PrimaryKey {
			order = append(order, OrderTerm{Column: k})
		}
	}
	return order, KeysPer(db.D, t, sp.Where)
}

// byChunks runs read on keys[lo:hi] in chunks of at most per keys, in order.
// A chunk whose statement had too many parameters (*ParamsError) and more than
// one key is read again, from lo, in chunks smaller by the excess, and the rest
// use the smaller size; with one key, the error is returned.
func byChunks(n, per int, read func(lo, hi int) error) error {
	for lo := 0; lo < n; {
		hi := min(lo+per, n)
		err := read(lo, hi)
		if pe, ok := as[*ParamsError](err); ok && hi-lo > 1 {
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
func fetchPart(ctx context.Context, db *DB, pin Target, t *EntityInfo, cols []int, sp LoadSpec, order []OrderTerm, keys []any, maxRows int, ec *EachClause) (any, []any, error) {
	st := Statement{Op: OpSelect, Entity: t, Unbounded: true, MaxRows: maxRows, InTx: db.InTx, OrderBy: order}
	if ec != nil {
		each := *ec
		each.Keys = keys
		st.Each, st.MaxRows, st.Where = &each, sp.MaxRows, slices.Clip(sp.Where)
	} else {
		st.Where = append([]Predicate{NewPredicate(Node{Op: OpIn, Col: int32(cols[0]), V: keys})}, sp.Where...)
	}
	return readPart(ctx, db, pin, &st, t, cols, sp.With)
}

// readPart runs st, a read of t's rows, and scans them. A Rewrite hook may
// not leave out any of cols, the columns the caller reads, nor a column the
// loads with need. When st's Each has a Join, readPart returns each row's
// owner key too, read from the join entity. st holds, once it returns, where
// it ran. pin, when set, is where it runs: a With load's (DB.LoadPin).
func readPart(ctx context.Context, db *DB, pin Target, st *Statement, t *EntityInfo, cols []int, with []LoadSpec) (any, []any, error) {
	SetStmtPin(st, pin)
	var part any
	var owners []any
	read := func(r Rows) (err error) {
		// st is as the hooks left it.
		if st.Columns != nil {
			for _, c := range cols {
				if !slices.Contains(st.Columns, c) {
					return fmt.Errorf("velox: %s: a Rewrite hook left out the relation's key column %s", t.Name, t.Columns[c].Name)
				}
			}
		}
		if len(with) > 0 && st.Columns != nil {
			if err := CheckLoads(t, st.Columns, with); err != nil {
				return err
			}
		}
		if ec := st.Each; ec != nil && ec.Join != nil {
			or := &OwnerRows{Rows: r, Typ: GoType(ec.Join.Columns[ec.JoinOwner])}
			part, err = InfoKit(t).Scan(or, st.Columns)
			owners = or.Keys
			return err
		}
		part, err = InfoKit(t).Scan(r, st.Columns)
		return err
	}
	var err error
	if db.Hooked() {
		err = db.Run(ctx, st, read, nil)
	} else {
		err = db.Plain(ctx, st, read, nil)
	}
	return part, owners, err
}
