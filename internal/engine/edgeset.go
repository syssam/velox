package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
)

// This file is the edge setters' (velox.Rel's Set, Add, Remove and Clear):
// what an Insert or an Update writes of an edge whose rows are not the
// owner's own, after the owner's row, and the key Set checks before it
// sets the owner's own foreign key. Every statement goes through the untyped
// Table API, so the target's and the join entity's policies and hooks run.

// EdgeOp is what an edge setter does.
type EdgeOp uint8

// The edge setters' operations.
const (
	EdgeAdd    EdgeOp = iota + 1 // link the keys
	EdgeRemove                   // unlink the keys
	EdgeClear                    // unlink every row
	EdgeSetKey                   // set the owner's own foreign key to the one key
)

// AssignEdge is the Op of an Assignment that holds an edge setter: its
// Column is -1 and its Value an *EdgeSet. It is no operator of an UPDATE.
const AssignEdge AssignOp = 0xff

// EdgePage is the most links of a many-to-many edge that Clear reads in one
// statement.
const EdgePage = 500

// EdgeSet is an edge setter: Op on the relation Ref of keys, the target's
// key values as the caller gave them.
type EdgeSet struct {
	Ref  RelRef
	Op   EdgeOp
	Keys []any
}

// EdgeAssignment is the Assignment that holds the edge setter op of keys on
// the relation ref.
func EdgeAssignment(ref RelRef, op EdgeOp, keys []any) Assignment {
	return Assignment{Column: -1, Op: AssignEdge, Value: &EdgeSet{Ref: ref, Op: op, Keys: keys}}
}

// Column is the owner's column e assigns: the foreign key of a Set edge
// setter whose edge resolves, else -1.
func (e *EdgeSet) Column() int {
	if e.Op != EdgeSetKey {
		return -1
	}
	l, err := e.Ref.Link()
	if err != nil {
		return -1
	}
	return l.OwnerCol
}

// EdgeOf is the edge setter a holds, or nil for a column's assignment.
func EdgeOf(a Assignment) *EdgeSet {
	if a.Op != AssignEdge {
		return nil
	}
	e, _ := a.Value.(*EdgeSet)
	return e
}

// edgeKind is how an edge's rows hold its owner.
type edgeKind uint8

const (
	edgeThrough edgeKind = iota + 1 // join rows of a join entity
	edgeChild                       // a foreign key on the target's rows
)

// edgeWrite is what the edge setters of one relation write for each owner,
// resolved and checked: Clear, then Remove, then Add.
type edgeWrite struct {
	owner  *EntityInfo
	link   Link
	kind   edgeKind
	target Table
	join   Table // edgeThrough's join entity
	key    int   // the target's column the keys are values of
	clear  bool
	remove []any // the distinct keys, as the key column's Go type
	add    []any
}

// EdgePlan is an insert's or an update's edge setters, resolved and grouped
// by relation, ready to write.
type EdgePlan struct {
	w      []edgeWrite
	checks []keyCheck
	// Set are the owner's column assignments of the Set edge setters, which
	// the write makes as it makes the caller's own.
	Set []Assignment
}

// keyCheck is a Set edge setter's key, which must name a row of the target
// the caller may read.
type keyCheck struct {
	owner, edge string
	target      Table
	col         int
	key         any
}

// Writes reports whether the plan writes any edge after the owner's row:
// a plan of Set edge setters alone writes none.
func (p EdgePlan) Writes() bool { return len(p.w) > 0 }

// check fails with an *EdgeError of ErrNotFound unless each Set edge
// setter's key names a row of its target the caller may read.
func (p EdgePlan) check(ctx context.Context, db *DB) error {
	for _, c := range p.checks {
		ok, err := QueryTable(db, nil, c.target).Where(c.target.Pred(c.col, PredEQ, c.key)).Exists(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return &EdgeError{Entity: c.owner, Edge: c.edge, Keys: []any{c.key}, Err: ErrNotFound}
		}
	}
	return nil
}

// planSetKey resolves e, a Set edge setter of owner: the assignment of the
// owner's foreign key, and the check of its key. Set applies only to an
// edge whose foreign key is on the owner's row and names the target's
// single-column primary key; its key must not be nil.
func planSetKey(owner *EntityInfo, e *EdgeSet) (Assignment, keyCheck, error) {
	l, err := e.Ref.Link()
	if err != nil {
		return Assignment{}, keyCheck{}, err
	}
	in, err := e.Ref.Owner()
	if err != nil {
		return Assignment{}, keyCheck{}, err
	}
	if in != owner {
		return Assignment{}, keyCheck{}, fmt.Errorf("velox: %s: an edge setter of %s", owner.Name, in.Name)
	}
	fail := func(format string, args ...any) (Assignment, keyCheck, error) {
		return Assignment{}, keyCheck{}, fmt.Errorf("velox: %s: edge %s: "+format, append([]any{owner.Name, l.Name}, args...)...)
	}
	t := l.Target
	ownKey := len(owner.PrimaryKey) == 1 && owner.PrimaryKey[0] == l.OwnerCol
	if l.Join != nil || ownKey || len(t.PrimaryKey) != 1 || t.PrimaryKey[0] != l.TargetCol {
		return fail("Set applies to an edge whose key is on the %s row; link this one with Add, Remove and Clear", owner.Name)
	}
	if len(e.Keys) != 1 {
		return fail("Set takes one key, not %d", len(e.Keys))
	}
	v, ok := plain(e.Keys[0])
	if !ok {
		return fail("a nil key; clear the edge with its key column's SetNull")
	}
	tt := InfoTable(t)
	k, err := tt.Convert(l.TargetCol, v)
	if err != nil {
		return Assignment{}, keyCheck{}, err
	}
	fk, err := InfoTable(owner).Convert(l.OwnerCol, v)
	if err != nil {
		return Assignment{}, keyCheck{}, err
	}
	return Assignment{Column: l.OwnerCol, Op: AssignSet, Value: fk}, keyCheck{owner: owner.Name, edge: l.Name, target: tt, col: l.TargetCol, key: k}, nil
}

// PlanEdges resolves the edge setters of a write of owner and checks them:
// Add, Remove or Clear of an edge whose key is on the owner's row or of a
// bidirectional one-to-one edge, Set of any other, a foreign key that is not
// nullable, and an Add of more than one key to a one-to-one edge are
// refused. Keys are converted to the target's key column's Go type, as
// Table.Convert converts, and made distinct. A Set gives the plan the
// assignment of the owner's foreign key, in Set, and a check of its key.
func PlanEdges(owner *EntityInfo, edges []*EdgeSet) (EdgePlan, error) {
	var p EdgePlan
	at := map[int32]int{}
	for _, e := range edges {
		if e.Op == EdgeSetKey {
			a, c, err := planSetKey(owner, e)
			if err != nil {
				return EdgePlan{}, err
			}
			p.Set, p.checks = append(p.Set, a), append(p.checks, c)
			continue
		}
		i, ok := at[e.Ref.I]
		if !ok {
			w, err := resolveEdge(owner, e.Ref)
			if err != nil {
				return EdgePlan{}, err
			}
			i = len(p.w)
			at[e.Ref.I] = i
			p.w = append(p.w, w)
		}
		w := &p.w[i]
		var err error
		switch e.Op {
		case EdgeClear:
			w.clear = true
		case EdgeRemove:
			w.remove, err = w.keys(w.remove, e.Keys)
		case EdgeAdd:
			w.add, err = w.keys(w.add, e.Keys)
		default:
			err = fmt.Errorf("velox: %s: edge %s: no edge operation %d", owner.Name, w.link.Name, e.Op)
		}
		if err != nil {
			return EdgePlan{}, err
		}
	}
	for i := range p.w {
		w := &p.w[i]
		if w.kind == edgeChild && !w.link.Many && len(w.add) > 1 {
			return EdgePlan{}, fmt.Errorf("velox: %s: edge %s: Add of %d keys to a one-to-one edge, which holds one", owner.Name, w.link.Name, len(w.add))
		}
	}
	return p, nil
}

// resolveEdge is the edgeWrite of the relation ref of owner, with nothing
// to write yet.
func resolveEdge(owner *EntityInfo, ref RelRef) (edgeWrite, error) {
	l, err := ref.Link()
	if err != nil {
		return edgeWrite{}, err
	}
	in, err := ref.Owner()
	if err != nil {
		return edgeWrite{}, err
	}
	if in != owner {
		return edgeWrite{}, fmt.Errorf("velox: %s: an edge setter of %s", owner.Name, in.Name)
	}
	fail := func(format string, args ...any) (edgeWrite, error) {
		return edgeWrite{}, fmt.Errorf("velox: %s: edge %s: "+format, append([]any{owner.Name, l.Name}, args...)...)
	}
	w := edgeWrite{owner: owner, link: l, target: InfoTable(l.Target)}
	if l.Join != nil {
		w.kind, w.join, w.key = edgeThrough, InfoTable(l.Join), l.TargetCol
		return w, nil
	}
	for _, bk := range owner.bidiKeys {
		if bk.Col == l.OwnerCol {
			return fail("a bidirectional one-to-one edge is set by Set, or its key's column handle, %s, not by Add, Remove or Clear", owner.Columns[l.OwnerCol].Field)
		}
	}
	t := l.Target
	ownKey := len(owner.PrimaryKey) == 1 && owner.PrimaryKey[0] == l.OwnerCol
	if !ownKey || len(t.PrimaryKey) != 1 || t.PrimaryKey[0] == l.TargetCol {
		return fail("its key is on the %s row, %s: set it with Set, or that column's handle, not Add, Remove or Clear", owner.Name, owner.Columns[l.OwnerCol].Field)
	}
	if !t.Columns[l.TargetCol].Nullable {
		return fail("its key %s.%s is not nullable, so a %s always has an owner: set that column instead", t.Name, t.Columns[l.TargetCol].Field, t.Name)
	}
	w.kind, w.key = edgeChild, t.PrimaryKey[0]
	return w, nil
}

// keys is have with each of ks, converted to w's key column's Go type, that
// it does not hold.
func (w *edgeWrite) keys(have, ks []any) ([]any, error) {
	seen := make(map[any]bool, len(have)+len(ks))
	for _, k := range have {
		seen[keyID(k)] = true
	}
	for _, k := range ks {
		v, ok := plain(k)
		if !ok {
			return nil, fmt.Errorf("velox: %s: edge %s: a nil key", w.owner.Name, w.link.Name)
		}
		c, err := w.target.Convert(w.key, v)
		if err != nil {
			return nil, err
		}
		if id := keyID(c); !seen[id] {
			seen[id] = true
			have = append(have, c)
		}
	}
	return have, nil
}

// keyID is k as a map key: a byte slice's text, or k.
func keyID(k any) any {
	if t := reflect.TypeOf(k); t != nil && !t.Comparable() {
		return fmt.Sprintf("%T:%x", k, k)
	}
	return k
}

// plain is v without a pointer; false for nil or a nil pointer.
func plain(v any) (any, bool) {
	if v == nil {
		return nil, false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer {
		return v, true
	}
	if rv.IsNil() {
		return nil, false
	}
	return rv.Elem().Interface(), true
}

// InfoTable is the Table of the entity in, built from its metadata: what
// velox's TableOf builds from the entity's Meta, but for no relation's use.
func InfoTable(in *EntityInfo) Table {
	addr := func(p unsafe.Pointer, col int) any { return fieldAddr(in, p, col) }
	typ := reflect.TypeOf(in.kit.Ptr(nil)).Elem()
	return NewTable(in, nil, addr, NilColumns(in), typ, RelTarget{})
}

// OwnerCols are the owner's columns the plan reads of each owner.
func (p EdgePlan) OwnerCols() []int {
	var cols []int
	for _, w := range p.w {
		if !slices.Contains(cols, w.link.OwnerCol) {
			cols = append(cols, w.link.OwnerCol)
		}
	}
	return cols
}

// InsertEdges checks the plan's Set keys, runs insert, an insert of the rows
// at owners, and writes the plan's edges for each of them after it, in one
// transaction on db (a savepoint inside one). insert returns the rows it
// affected: 0, as when a hook did not call next, writes no edge.
func InsertEdges(ctx context.Context, db *DB, p EdgePlan, owners []unsafe.Pointer, insert func(ctx context.Context, tx *DB) (int64, error)) error {
	return db.Tx(ctx, func(ctx context.Context, tx *DB) error {
		if err := p.check(ctx, tx); err != nil {
			return err
		}
		n, err := insert(ctx, tx)
		if err != nil || n == 0 {
			return err
		}
		return p.write(ctx, tx, owners)
	})
}

// UpdateEdges runs st, an update, and writes the plan's edges for each row
// it matches, in one transaction on db (a savepoint inside one). It first
// reads the matched rows' keys, under st's conditions and the Read and Write
// policies, locked where the dialect can; an st with no assignment is not
// sent. It returns the UPDATE's count, or with no assignment the rows read.
// The plan's Set keys are checked first, once, and its Set assignments are
// st's; a plan of Set edge setters alone reads no keys and sends st.
func UpdateEdges(ctx context.Context, db *DB, st *Statement, p EdgePlan) (int64, error) {
	if err := db.guard(st); err != nil {
		return 0, err
	}
	var n int64
	if !p.Writes() {
		err := db.Tx(ctx, func(ctx context.Context, tx *DB) error {
			if err := p.check(ctx, tx); err != nil {
				return err
			}
			st.InTx = true
			var err error
			n, _, _, err = Write(ctx, tx, st, false, false)
			return err
		})
		return n, err
	}
	err := db.Tx(ctx, func(ctx context.Context, tx *DB) error {
		if err := p.check(ctx, tx); err != nil {
			return err
		}
		q := QueryTable(tx, nil, InfoTable(st.Entity)).Where(st.Where...).Columns(p.OwnerCols()...).Unbounded().Writable()
		if tx.D != SQLite {
			q = q.ForUpdate()
		}
		rows, err := q.All(ctx)
		if err != nil {
			return err
		}
		owners := make([]unsafe.Pointer, rows.Len())
		for i := range owners {
			owners[i] = rows.At(i)
		}
		n = int64(len(owners))
		if len(st.Set) > 0 {
			st.InTx = true
			if n, _, _, err = Write(ctx, tx, st, false, false); err != nil {
				return err
			}
		}
		return p.write(ctx, tx, owners)
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// write writes each relation's edges for owners.
func (p EdgePlan) write(ctx context.Context, db *DB, owners []unsafe.Pointer) error {
	if len(owners) == 0 {
		return nil
	}
	for i := range p.w {
		w := &p.w[i]
		var err error
		if w.kind == edgeThrough {
			err = w.writeThrough(ctx, db, owners)
		} else {
			err = w.writeChild(ctx, db, owners)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ownerKeys are the distinct values of w's owner column of owners, each
// converted to column col of t; an owner whose value is NULL has none.
func (w *edgeWrite) ownerKeys(owners []unsafe.Pointer, t Table, col int) ([]any, error) {
	kit := w.owner.kit
	seen := map[any]bool{}
	var out []any
	for _, o := range owners {
		v, ok := plain(kit.Value(o, w.link.OwnerCol))
		if !ok {
			continue
		}
		c, err := t.Convert(col, v)
		if err != nil {
			return nil, err
		}
		if id := keyID(c); !seen[id] {
			seen[id] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// fail is the *EdgeError of w on keys.
func (w *edgeWrite) fail(keys []any, err error) error {
	return &EdgeError{Entity: w.owner.Name, Edge: w.link.Name, Keys: keys, Err: err}
}

// in is the condition that t's column col is one of ks.
func in(t Table, col int, ks []any) Predicate { return t.Pred(col, PredIn, ks) }

// readable returns the values of w's key column of the rows of the target
// whose key is one of ks that the caller may read: at most len(ks).
func (w *edgeWrite) readable(ctx context.Context, db *DB, ks []any, more ...Predicate) ([]any, error) {
	if len(ks) == 0 {
		return nil, nil
	}
	t := w.target
	rows, err := QueryTable(db, nil, t).Where(in(t, w.key, ks)).Where(more...).Columns(w.key).Limit(len(ks)).MaxRows(len(ks)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]any, rows.Len())
	for i := range out {
		out[i] = t.Value(rows.At(i), w.key)
	}
	return out, nil
}

// missing are the keys of ks that got does not hold.
func missing(ks, got []any) []any {
	have := make(map[any]bool, len(got))
	for _, k := range got {
		have[keyID(k)] = true
	}
	var out []any
	for _, k := range ks {
		if !have[keyID(k)] {
			out = append(out, k)
		}
	}
	return out
}

// checkExists fails with an *EdgeError of ErrNotFound unless every key of ks
// names a row of the target the caller may read.
func (w *edgeWrite) checkExists(ctx context.Context, db *DB, ks []any) error {
	got, err := w.readable(ctx, db, ks)
	if err != nil {
		return err
	}
	if len(got) < len(ks) {
		return w.fail(missing(ks, got), ErrNotFound)
	}
	return nil
}

// writeThrough writes a many-to-many edge. Join rows are scoped by no
// policy of their own, so Clear and Remove delete only the links to targets
// the caller may read, and a link to a hidden target stays. Add checks its
// targets under the target's Read policy and inserts the join rows ON
// CONFLICT DO NOTHING, so a link that exists is kept.
func (w *edgeWrite) writeThrough(ctx context.Context, db *DB, owners []unsafe.Pointer) error {
	j, l := w.join, w.link
	os, err := w.ownerKeys(owners, j, l.JoinOwner)
	if err != nil || len(os) == 0 {
		return err
	}
	// unlink deletes the join rows of o to the targets of ks the caller may
	// read.
	unlink := func(o any, ks []any) error {
		vs, err := w.readable(ctx, db, ks)
		if err != nil || len(vs) == 0 {
			return err
		}
		for i, v := range vs {
			if vs[i], err = j.Convert(l.JoinTarget, v); err != nil {
				return err
			}
		}
		_, err = DeleteTable(db, j).Where(j.Pred(l.JoinOwner, PredEQ, o), in(j, l.JoinTarget, vs)).Exec(ctx)
		return err
	}
	for _, o := range os {
		if w.clear {
			if err := w.unlinkAll(ctx, db, o, unlink); err != nil {
				return err
			}
		}
		if len(w.remove) > 0 {
			if err := unlink(o, w.remove); err != nil {
				return err
			}
		}
	}
	if len(w.add) == 0 {
		return nil
	}
	if err := w.checkExists(ctx, db, w.add); err != nil {
		return err
	}
	rows := j.NewRows(len(os) * len(w.add))
	at := 0
	for _, o := range os {
		for _, k := range w.add {
			if err := j.SetField(rows.At(at), l.JoinOwner, o); err != nil {
				return err
			}
			if err := j.SetField(rows.At(at), l.JoinTarget, k); err != nil {
				return err
			}
			at++
		}
	}
	return InsertRows(db, j, rows).OnConflict(l.JoinOwner, l.JoinTarget).DoNothing().Exec(ctx)
}

// unlinkAll is Clear of a many-to-many edge for the owner o, as the join
// entity holds it: it reads o's join rows EdgePage at a time, in the order
// of the join's two columns, and gives unlink the target keys of each page.
// Each page starts after the last row of the one before, so a join row
// unlink leaves is not read again.
func (w *edgeWrite) unlinkAll(ctx context.Context, db *DB, o any, unlink func(o any, ks []any) error) error {
	j, l := w.join, w.link
	q := QueryTable(db, nil, j).Where(j.Pred(l.JoinOwner, PredEQ, o)).
		OrderBy(OrderTerm{Column: l.JoinOwner}, OrderTerm{Column: l.JoinTarget}).Limit(EdgePage).MaxRows(EdgePage)
	for {
		rows, err := q.All(ctx)
		if err != nil || rows.Len() == 0 {
			return err
		}
		ks := make([]any, rows.Len())
		for i := range ks {
			v, _ := plain(j.Value(rows.At(i), l.JoinTarget))
			if ks[i], err = w.target.Convert(w.key, v); err != nil {
				return err
			}
		}
		if err := unlink(o, ks); err != nil {
			return err
		}
		if rows.Len() < EdgePage {
			return nil
		}
		q = q.After(rows.At(rows.Len() - 1))
	}
}

// writeChild writes an edge whose foreign key the target's rows hold. Clear
// sets NULL on every child an owner holds that the caller may change, and
// Remove on the given ones. Add re-points children no owner holds, for one
// owner only: a child has one owner.
func (w *edgeWrite) writeChild(ctx context.Context, db *DB, owners []unsafe.Pointer) error {
	t, fk := w.target, w.link.TargetCol
	vs, err := w.ownerKeys(owners, t, fk)
	if err != nil || len(vs) == 0 {
		return err
	}
	if len(w.add) > 0 && len(vs) > 1 {
		return fmt.Errorf("velox: %s: edge %s: Add for %d owners at once: a %s has one owner", w.owner.Name, w.link.Name, len(vs), t.Info().Name)
	}
	for _, v := range vs {
		held := t.Pred(fk, PredEQ, v)
		if w.clear {
			if _, err := UpdateTable(db, t).SetNull(fk).Where(held).Exec(ctx); err != nil {
				return err
			}
		}
		if len(w.remove) > 0 {
			if _, err := UpdateTable(db, t).SetNull(fk).Where(in(t, w.key, w.remove), held).Exec(ctx); err != nil {
				return err
			}
		}
		if len(w.add) == 0 {
			continue
		}
		if !w.link.Many {
			if err := w.replaceChild(ctx, db, w.add[0], v); err != nil {
				return err
			}
			continue
		}
		if err := w.relink(ctx, db, w.add, v); err != nil {
			return err
		}
	}
	return nil
}

// replaceChild links the child k to the owner whose key, as the foreign key
// holds it, is v, through a one-to-one edge. The child must be one the
// caller may read, or it is not found, as a key that names no child: the
// error does not tell a hidden child from a missing one. The child the owner
// holds now is let go first, as the key is unique, under the caller's scope:
// one that stays linked is one the caller may not change, and fails with
// ErrLinked. A key of a Required edge, which velox refuses to set NULL, is
// let go of only when a child holds it: an owner that holds none, as a row
// just inserted, links k at once.
func (w *edgeWrite) replaceChild(ctx context.Context, db *DB, k, v any) error {
	t, fk := w.target, w.link.TargetCol
	if err := w.checkExists(ctx, db, []any{k}); err != nil {
		return err
	}
	const reason = "velox: a hidden child of a one-to-one edge setter"
	held, other := t.Pred(fk, PredEQ, v), t.Pred(w.key, PredNEQ, k)
	if info := t.Info(); info != nil && info.Columns[fk].RequiredEdge != "" {
		holds, err := QueryTable(db, nil, t).Where(held, other).Exists(WithoutPolicy(ctx, reason))
		if err != nil {
			return err
		}
		if !holds {
			return w.relink(ctx, db, []any{k}, v)
		}
	}
	if _, err := UpdateTable(db, t).SetNull(fk).Where(held, other).Exec(ctx); err != nil {
		return err
	}
	still, err := QueryTable(db, nil, t).Where(held, other).Exists(WithoutPolicy(ctx, reason))
	if err != nil {
		return err
	}
	if still {
		return w.fail([]any{k}, ErrLinked)
	}
	return w.relink(ctx, db, []any{k}, v)
}

// relink links the children ks whose key is NULL to v, the owner's key as
// the foreign key holds it, and checks, by a read after the UPDATE under
// the Read policy, that each of them holds v: the UPDATE's own count is not
// used, as MySQL counts only the rows it changed, and a child linked here
// already that the Write policy hides is a link that exists. A child the
// caller may read that another owner holds fails with ErrLinked; one the
// caller may not change, or that does not exist, with ErrNotFound.
func (w *edgeWrite) relink(ctx context.Context, db *DB, ks []any, v any) error {
	t, fk := w.target, w.link.TargetCol
	ids := in(t, w.key, ks)
	if _, err := UpdateTable(db, t).Set(fk, v).Where(ids, t.Pred(fk, PredIsNull, nil)).Exec(ctx); err != nil {
		return err
	}
	got, err := w.readable(ctx, db, ks, t.Pred(fk, PredEQ, v))
	if err != nil {
		return err
	}
	if len(got) >= len(ks) {
		return nil
	}
	bad := missing(ks, got)
	linked, err := QueryTable(db, nil, t).Where(in(t, w.key, bad), t.Pred(fk, PredNotNull, nil), t.Pred(fk, PredNEQ, v)).Exists(ctx)
	if err != nil {
		return err
	}
	if linked {
		return w.fail(bad, ErrLinked)
	}
	return w.fail(bad, ErrNotFound)
}

// errEdgeKeyless is the error of an edge setter on an InsertAll whose keys
// the database assigns, which it does not read back.
func errEdgeKeyless(in *EntityInfo) error {
	return fmt.Errorf("velox: %s: an InsertAll with edge setters needs every entity's key set: it reads no generated key back", in.Name)
}

// CheckEdgeInsert refuses edge setters on an insert velox cannot write them
// for: an upsert, whose row may not be the caller's, and an InsertAll of
// rows whose keys the database assigns (gen is true).
func CheckEdgeInsert(in *EntityInfo, upsert, many, gen bool) error {
	switch {
	case upsert:
		return fmt.Errorf("velox: %s: an upsert cannot take edge setters: its row may be one it did not insert", in.Name)
	case many && gen:
		return errEdgeKeyless(in)
	}
	return nil
}
