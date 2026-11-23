package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

// DryRefused reports whether err, from the hooks of a statement under SQL(ctx),
// is a write a hook made that was refused with ErrDryRun, after the statement
// itself was rendered: SQL(ctx) then returns the statement, as the hook, not
// the dry run, is what asked for a write. A hook that wrote before next, and
// so before anything was rendered, gets the error, and so does one whose
// error joins another to ErrDryRun.
func DryRefused(mu *MutationState, err error) bool {
	return mu.Dry && mu.SQL != "" && onlyDryRun(err)
}

// onlyDryRun reports whether err is ErrDryRun, wrapped or joined, on every
// branch of its tree: errors.Join(ErrDryRun, other) is not.
func onlyDryRun(err error) bool {
	if err == nil {
		return false
	}
	if err == ErrDryRun { //nolint:errorlint // a branch is ErrDryRun itself; errors.Is would accept a join
		return true
	}
	switch x := err.(type) { //nolint:errorlint // walks the tree of joined and wrapped errors, branch by branch
	case interface{ Unwrap() []error }:
		errs := x.Unwrap()
		for _, e := range errs {
			if !onlyDryRun(e) {
				return false
			}
		}
		return len(errs) > 0
	case interface{ Unwrap() error }:
		return onlyDryRun(x.Unwrap())
	}
	return false
}

// bidiWhere is the condition that ps, a bidirectional join entity's
// conditions, hold for a row or for its mirror: Or(And(ps…), And(swap(ps)…)),
// or Or(p, swap(p)) for one p.
func bidiWhere(ps []Predicate) Predicate {
	if len(ps) == 1 {
		return Predicate{Group(OpOr, []Node{ps[0].n, swapCols(ps[0].n)})}
	}
	as, bs := make([]Node, len(ps)), make([]Node, len(ps))
	for i, p := range ps {
		as[i], bs[i] = p.n, swapCols(p.n)
	}
	return Predicate{Group(OpOr, []Node{Group(OpAnd, as), Group(OpAnd, bs)})}
}

// swapCols returns a copy of n, a condition on a bidirectional join entity,
// with its columns 0 and 1 exchanged, at every depth of And, Or and Not. The
// kids of an EXISTS (OpExists, OpThrough) hold another entity's columns: such
// a node is kept as it is.
func swapCols(n Node) Node {
	switch n.Op {
	case OpAnd, OpOr, OpNot:
		kids := make([]Node, len(n.Kids))
		for i, k := range n.Kids {
			kids[i] = swapCols(k)
		}
		m := n
		m.Kids = kids
		return m
	case OpExists:
		return swapExists(n)
	case OpThrough, OpAllow, OpInvalid:
		return n
	case OpExpr:
		return swapExpr(n)
	}
	m := n
	switch n.Col {
	case 0:
		m.Col = 1
	case 1:
		m.Col = 0
	}
	return m
}

// swapExists is swapCols of an EXISTS. A relation's is kept as it is. A
// ColRef's (col -1) correlates the join entity's own column with the
// target's, so that column is swapped as swapCols swaps a leaf's.
func swapExists(n Node) Node {
	if n.Col >= 0 {
		return n
	}
	m := n
	switch r := n.V.(type) {
	case ColRef:
		r.OwnerCol = swapped(r.OwnerCol)
		m.V = r
	case Link:
		r.OwnerCol = int(swapped(int32(r.OwnerCol)))
		m.V = r
	}
	return m
}

// swapped exchanges column 0 and column 1, and keeps any other.
func swapped(c int32) int32 {
	switch c {
	case 0:
		return 1
	case 1:
		return 0
	}
	return c
}

// checkAssignedOnce refuses a column assigned twice: PostgreSQL rejects it
// while MySQL and SQLite accept it, so velox refuses it everywhere.
func checkAssignedOnce(info *EntityInfo, set []Assignment) error {
	seen := make(map[int]bool, len(set))
	for _, a := range set {
		if seen[a.Column] {
			return fmt.Errorf("velox: %s: column %s is assigned twice", info.Name, info.Columns[a.Column].Name)
		}
		seen[a.Column] = true
	}
	return nil
}

// withUpdateDefaults appends an assignment for every column with an
// UpdateDefault that set does not assign and keep does not name. A column
// of keep without an UpdateDefault is an error, which names it.
func withUpdateDefaults(info *EntityInfo, set []Assignment, keep []int) ([]Assignment, error) {
	if err := checkKeep(info, keep); err != nil {
		return nil, err
	}
	named := make(map[int]bool, len(set)+len(keep))
	for _, a := range set {
		named[a.Column] = true
	}
	for _, c := range keep {
		named[c] = true
	}
	out := slices.Clip(set)
	for i := range info.Columns {
		if f := info.Columns[i].UpdateDefault; f != nil && !named[i] {
			out = append(out, Assignment{Column: i, Op: AssignSet, Value: f()})
		}
	}
	return out, nil
}

// checkKeep refuses a column of keep, KeepDefaults' columns, that is nil
// (-1), that info lacks, or that has no UpdateDefault, naming it.
func checkKeep(info *EntityInfo, keep []int) error {
	for _, c := range keep {
		if c < 0 {
			return fmt.Errorf("velox: %s: KeepDefaults names a nil column", info.Name)
		}
		if c >= len(info.Columns) {
			return fmt.Errorf("velox: %s: KeepDefaults names column %d, which the entity does not have", info.Name, c)
		}
		if info.Columns[c].UpdateDefault == nil {
			return fmt.Errorf("velox: %s: KeepDefaults names %s, which has no UpdateDefault", info.Name, info.Columns[c].Field)
		}
	}
	return nil
}

// SetElem stores v where p points: nil as the zero value, a value of the
// pointed-to type of a pointer field through a new pointer, and nothing when
// the field cannot hold v (a Rewrite hook may assign any value). Like elem it
// is not generic, so that the reflect.Value it uses is not compiled into every
// package that writes.
func SetElem(p, v any) {
	dst := reflect.ValueOf(p).Elem()
	if v == nil {
		dst.SetZero()
		return
	}
	val := reflect.ValueOf(v)
	switch t := dst.Type(); {
	case val.Type().AssignableTo(t):
		dst.Set(val)
	case t.Kind() == reflect.Pointer && val.Type().AssignableTo(t.Elem()):
		ptr := reflect.New(t.Elem())
		ptr.Elem().Set(val)
		dst.Set(ptr)
	}
}

// Write runs st, an update or delete (an update that must find its row, with
// save), inside its entity's hooks, or alone when it has none, without
// allocating for them. Under SQL(ctx) (dry) it renders st instead of sending
// it. It returns the rows affected, and what dry rendered.
//
// The guard runs on st as the caller built it before the hooks see it, and
// again inside next, in the pipeline: a condition a hook adds never stands in
// for the caller's Where. Without hooks, the pipeline's guard is the only one.
//
// An update or Save on the join entity of a bidirectional edge is refused
// first, before any hook runs, so that a refused write has no hook's side
// effects.
func Write(ctx context.Context, db *DB, st *Statement, dry, save bool) (int64, string, []any, error) {
	return writeWith(ctx, db, st, dry, save, nil)
}

// DeleteOne is DeleteQuery.One: st, a delete, must remove one row. No row
// removed is ErrNotFound: none matched, the policies or an interceptor hid
// it, or a hook skipped the delete without SetAffected. A count of -1, which
// the driver could not report, is taken as one row.
//
// When st's conditions pin every primary-key column (pinnedWhere), st is
// one DELETE, sent as Exec sends it: it matches one row at most, so no
// transaction is needed to undo a second. A count over one can then come
// only from a hook's SetAffected or a bidirectional edge's mirror row, and
// is taken as the one row. Otherwise st runs in a transaction, a savepoint
// inside one, which a count over one rolls back with ErrNotSingular.
func DeleteOne(ctx context.Context, db *DB, st *Statement) error {
	if err := db.guard(st); err != nil {
		return err
	}
	in := st.Entity
	if _, ok := pinnedWhere(in, st.Where); ok {
		n, _, _, err := Write(ctx, db, st, false, false)
		if err == nil && n == 0 {
			err = fmt.Errorf("velox: %s: %w", in.Name, ErrNotFound)
		}
		return err
	}
	return atomically(ctx, db, func(ctx context.Context, tx *DB) error {
		st.InTx = true
		n, _, _, err := Write(ctx, tx, st, false, false)
		switch {
		case err != nil:
			return err
		case n == 0:
			return fmt.Errorf("velox: %s: %w", in.Name, ErrNotFound)
		case n > 1:
			return fmt.Errorf("velox: %s: the delete matched %d rows: %w", in.Name, n, ErrNotSingular)
		}
		return nil
	})
}

// writeWith is Write of st with the edge setters edges, which its hooks see
// (Mutation.Edges) and which the caller writes.
func writeWith(ctx context.Context, db *DB, st *Statement, dry, save bool, edges []*EdgeSet) (int64, string, []any, error) {
	if st.Op == OpUpdate && InfoBidi(st.Entity) {
		return 0, "", nil, errBidiUpdate(st.Entity)
	}
	if !db.HasHooks(st.Entity) {
		var err error
		mu := MutationState{St: st, Dry: dry}
		if save {
			err = saveCore(ctx, db, &mu)
		} else {
			err = writeCore(ctx, db, &mu)
		}
		return mu.Affected, mu.SQL, mu.Args, err
	}
	if err := db.guard(st); err != nil {
		return 0, "", nil, err
	}
	core := func(ctx context.Context, mu *MutationState) error { return writeCore(ctx, db, mu) }
	if save {
		core = func(ctx context.Context, mu *MutationState) error { return saveCore(ctx, db, mu) }
	}
	mu := &MutationState{Op: st.Op, St: st, Dry: dry, Edges: edges}
	if err := Mutate(ctx, db, st.Entity, mu, core); err != nil && !DryRefused(mu, err) {
		return 0, "", nil, err
	}
	if save && mu.Own {
		// A hook made the write itself (SetAffected): velox wrote nothing,
		// so Save's Exec, which returns no count, must not store st.Set in
		// *x. 0 tells it so, as for a hook that skipped next.
		return 0, "", nil, nil
	}
	return mu.Affected, mu.SQL, mu.Args, nil
}

// writeCore is an update's or delete's work inside the hooks, their next.
// For an update it adds the UpdateDefault assignments, refuses a column
// assigned twice or an Immutable one and runs the validators; then it sends
// the statement through the pipeline, whose guard runs first, or under
// SQL(ctx) renders it into mu.sql and mu.args. It sets mu.affected. A
// write through a recording handle is sent by trackedWrite, which reads the
// rows before and after it and reports them; under SQL(ctx) it renders as
// any other, and records nothing.
//
// An update that sets the key of a bidirectional one-to-one edge (EdgeBidi)
// must name one row by its primary key, and is sent with the statements that
// re-point both partners (bidiUpdate).
//
// On the join entity of a bidirectional edge (write refused an update), a
// delete's conditions also match each row's mirror (bidiWhere), so that both
// rows of a link go, as ent's clearM2MEdges removes them. It sends a copy of
// the statement with that condition, so that the hooks' statement keeps the
// Where they gave.
func writeCore(ctx context.Context, db *DB, mu *MutationState) error {
	st := mu.St
	if InfoBidi(st.Entity) && len(st.Where) > 0 {
		cp := *st
		cp.Where = []Predicate{bidiWhere(st.Where)}
		st = &cp
	}
	if st.Op == OpUpdate {
		in := st.Entity
		set, err := withUpdateDefaults(in, st.Set, st.keep)
		if err != nil {
			return err
		}
		st.Set = set
		if err := checkAssignedOnce(in, st.Set); err != nil {
			return err
		}
		if v, ok := in.VersionColumn(); ok {
			if err := refuseVersion(in, v, st.Set); err != nil {
				return err
			}
			st.Set = append(slices.Clip(st.Set), Assignment{Column: v, Op: AssignAdd, Value: int64(1)})
		}
		if err := ValidateSetters(in, st.Set); err != nil {
			return err
		}
		if len(infoBidiKeys(in)) > 0 {
			if keys, vs := bidiSet(in, st.Set); len(keys) > 0 {
				k, err := checkBidiUpdate(st, keys, vs)
				if err != nil {
					return err
				}
				st.InTx = true // Exec sends it in a transaction, bidiUpdate's
				if !mu.Dry {
					return bidiUpdate(ctx, db, mu, keys, k, vs)
				}
			}
		}
	}
	if mu.Dry {
		var err error
		mu.SQL, mu.Args, err = db.DryRun(ctx, st)
		return err
	}
	if db.rec != nil {
		return trackedWrite(ctx, db, mu, everyColumn(st.Entity), st)
	}
	res, err := ExecWrite(ctx, db, st)
	if err != nil {
		return err
	}
	mu.Affected = res.Affected
	return nil
}

// saveCore is Save's core: writeCore, failing with ErrNotFound when no row
// has the key and meets the conditions. A dry run sends nothing, so it cannot
// find that out.
func saveCore(ctx context.Context, db *DB, mu *MutationState) error {
	if err := writeCore(ctx, db, mu); err != nil {
		return err
	}
	if !mu.Dry && mu.Affected == 0 {
		if _, ok := mu.St.Entity.VersionColumn(); ok {
			return fmt.Errorf("velox: %s: %w (%w)", mu.St.Entity.Name, ErrStale, ErrNotFound)
		}
		return fmt.Errorf("velox: %s: %w", mu.St.Entity.Name, ErrNotFound)
	}
	return nil
}

// refuseVersion fails with ErrVersionSet when set assigns column v, in's
// Version column.
func refuseVersion(in *EntityInfo, v int, set []Assignment) error {
	for _, a := range set {
		if a.Column == v {
			return fmt.Errorf("velox: %s: %s: %w", in.Name, in.Columns[v].Field, ErrVersionSet)
		}
	}
	return nil
}

// BumpVersion adds 1 to the integer field p points to: a Version column's,
// after a Save wrote the row. Like SetElem it is not generic.
func BumpVersion(p any) {
	v := reflect.ValueOf(p).Elem()
	if v.CanInt() {
		v.SetInt(v.Int() + 1)
	} else {
		v.SetUint(v.Uint() + 1)
	}
}
