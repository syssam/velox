package velox

import (
	"context"
	"fmt"
	"reflect"
	"unsafe"
)

// loadSpec is what Query.With loads: relation rel of the query's entity,
// filtered, ordered, and with relations of its own.
type loadSpec struct {
	rel   int32
	where []Predicate
	order []OrderTerm
	with  []loadSpec
	err   error
}

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

// loadRelations loads each spec's relation for the owners at ptrs.
func loadRelations(ctx context.Context, db *DB, owner *EntityInfo, ptrs []unsafe.Pointer, specs []loadSpec) error {
	for _, sp := range specs {
		if err := loadOne(ctx, db, owner, ptrs, sp); err != nil {
			return err
		}
	}
	return nil
}

func loadOne(ctx context.Context, db *DB, owner *EntityInfo, ptrs []unsafe.Pointer, sp loadSpec) error {
	if sp.err != nil {
		return sp.err
	}
	if int(sp.rel) < 0 || int(sp.rel) >= len(owner.rels) {
		return fmt.Errorf("velox: %s has no relation %d; run go tool velox-gen", owner.Name, sp.rel)
	}
	r := owner.rels[sp.rel]
	l, err := r.resolve(owner)
	if err != nil {
		return err
	}
	t := l.target
	if owner.kit == nil || t.kit == nil {
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
	if len(keys) > 0 {
		if all, targets, err = fetch(ctx, db, t, l, sp, keys); err != nil {
			return fmt.Errorf("velox: %s: loading %s: %w", owner.Name, r.name, err)
		}
		for i, p := range targets {
			if k, ok := keyOf(t.kit.value(p, l.targetCol)); ok {
				byKey[k] = append(byKey[k], i)
			}
		}
		if len(sp.with) > 0 {
			if err := loadRelations(ctx, db, t, targets, sp.with); err != nil {
				return err
			}
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

// fetch reads the related rows for keys, in chunks that keep each statement
// under the dialect's parameter limit, ordered within each owner as asked
// (by the target's primary key when nothing is asked).
func fetch(ctx context.Context, db *DB, t *EntityInfo, l link, sp loadSpec, keys []any) (any, []unsafe.Pointer, error) {
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
	per := max(1, maxParams[db.d]-fixed)
	var parts []any // each chunk's []T
	for start := 0; start < len(keys); start += per {
		chunk := keys[start:min(start+per, len(keys))]
		st := Statement{Op: OpSelect, Entity: t, Unbounded: true, InTx: db.inTx, OrderBy: order,
			Where: append([]Predicate{{node{op: opIn, col: int32(l.targetCol), v: chunk}}}, sp.where...)}
		var part any
		read := func(r rows) (err error) {
			part, err = t.kit.scan(r)
			return err
		}
		var err error
		if db.hooked() {
			err = db.run(ctx, &st, read, nil)
		} else {
			err = db.plain(ctx, &st, read, nil)
		}
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, part)
	}
	all, ptrs := t.kit.join(parts) // pointers into the one slice pick and ptr index
	return all, ptrs, nil
}
