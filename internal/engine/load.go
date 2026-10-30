package engine

import (
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
