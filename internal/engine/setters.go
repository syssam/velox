package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"unsafe"
)

// This file is what velox's InsertQuery and UpdateQuery do with their
// setters that does not depend on the entity's type, so that it is compiled
// once, not in every entity's instantiation.

// AssignSkip is the Op of the Assignment a column handle's SetPtr makes of
// a nil pointer: the builders drop it, so no column is written. Like
// AssignEdge it is no operator of an UPDATE, and no statement holds it.
const AssignSkip AssignOp = 0xfe

// Setters are a write's setters: its column assignments and its edge
// setters, apart, and the columns of the skipped setters (AssignSkip).
type Setters struct {
	Set     []Assignment
	Edges   []*EdgeSet
	Skipped []int
}

// Add returns s with the setters ss, each in its list. s's lists are not
// changed.
func (s Setters) Add(ss []Assignment) Setters {
	s.Set, s.Edges, s.Skipped = slices.Clip(s.Set), slices.Clip(s.Edges), slices.Clip(s.Skipped)
	for _, a := range ss {
		switch e := EdgeOf(a); {
		case a.Op == AssignSkip:
			s.Skipped = append(s.Skipped, a.Column)
		case e != nil:
			s.Edges = append(s.Edges, e)
		default:
			s.Set = append(s.Set, a)
		}
	}
	return s
}

// InsertNulls returns the columns of in that s's column setters leave
// NULL: a nullable column whose last setter is a Set of nil, SetNull. A
// SetPtr of nil is skipped and sets nothing. An insert writes such a
// column as NULL, not leaving it to its DEFAULT (ADR 0124). It is nil when
// there is none.
func (s Setters) InsertNulls(in *EntityInfo) []int {
	var cols []int
	for i, a := range s.Set {
		if a.Op != AssignSet || a.Column < 0 || a.Column >= len(in.Columns) || !in.Columns[a.Column].Nullable || slices.Contains(cols, a.Column) {
			continue
		}
		last := a
		for _, b := range s.Set[i+1:] {
			if b.Column == a.Column {
				last = b
			}
		}
		if last.Op == AssignSet && last.Value == nil {
			cols = append(cols, a.Column)
		}
	}
	return cols
}

// UpdateSet adds the setters ss to st, an UPDATE, and edges, its edge
// setters, as UpdateQuery.Set does, and returns the edge setters. A skipped
// setter (AssignSkip) is dropped, and marks st as given one.
func UpdateSet(st *Statement, edges []*EdgeSet, ss []Assignment) []*EdgeSet {
	set := Setters{Set: st.Set, Edges: edges}.Add(ss)
	st.Set = set.Set
	st.skipped = st.skipped || len(set.Skipped) > 0
	return set.Edges
}

// CheckUpdateSet refuses st, an UPDATE with edges edge setters, that was
// given no setter at all: "Update has no Set". One whose every setter was
// skipped passes, and sends nothing (skippedUpdate).
func CheckUpdateSet(st *Statement, edges int) error {
	if len(st.Set) == 0 && edges == 0 && !st.skipped {
		return fmt.Errorf("velox: %s: Update has no Set", st.Entity.Name)
	}
	return nil
}

// skippedOnly reports whether st, an UPDATE with edges edge setters, was
// given setters that were all skipped, so that it sends nothing.
func skippedOnly(st *Statement, edges int) bool {
	return len(st.Set) == 0 && edges == 0 && st.skipped
}

// skippedUpdate is the work of an UPDATE whose every setter was skipped:
// it checks the KeepDefaults columns and runs the guard, and sends nothing.
// No hook runs, no UpdateDefault fires, and nothing is recorded.
func skippedUpdate(db *DB, st *Statement) error {
	if err := checkKeep(st.Entity, st.keep); err != nil {
		return err
	}
	return db.guard(st)
}

// Unskipped returns as without its skipped setters (AssignSkip): as itself
// when it has none.
func Unskipped(as []Assignment) []Assignment {
	if !slices.ContainsFunc(as, isSkip) {
		return as
	}
	return slices.DeleteFunc(slices.Clone(as), isSkip)
}

func isSkip(a Assignment) bool { return a.Op == AssignSkip }

// Upsert returns cf without the columns of its update branch whose setters
// were all skipped: a column a skipped setter names, and no other setter
// sets, is not updated. With none left the update branch assigns the first
// conflict column its own value (ConflictClause.Ignore), as ent's Ignore
// does, so the conflicting row's key is read back as after an update. It
// returns cf itself when nothing changes.
func (s Setters) Upsert(cf *ConflictClause) *ConflictClause {
	if cf == nil || len(s.Skipped) == 0 || len(cf.Update) == 0 {
		return cf
	}
	drop := func(c int) bool {
		return slices.Contains(s.Skipped, c) && !slices.ContainsFunc(s.Set, func(a Assignment) bool { return a.Column == c })
	}
	if !slices.ContainsFunc(cf.Update, drop) {
		return cf
	}
	out := *cf
	out.Update = slices.DeleteFunc(slices.Clone(cf.Update), drop)
	if len(out.Update) == 0 && len(out.Set) == 0 {
		out.Update, out.Ignore = nil, true
	}
	return &out
}

// UpsertSet returns a copy of cf, a new clause when nil, whose update
// branch also assigns as: UpdateSet's setters. DoNothing is cleared. A
// skipped setter (AssignSkip) is kept, for CheckInsertQuery to drop
// (upsertSkips); an edge setter is refused, as the row may be one the
// insert did not write.
func UpsertSet(cf *ConflictClause, as []Assignment) (*ConflictClause, error) {
	var out ConflictClause
	if cf != nil {
		out = *cf
	}
	for _, a := range as {
		if EdgeOf(a) != nil {
			return nil, errors.New("velox: UpdateSet takes column setters; an edge setter of an upsert's update branch would change a row the insert may not have written")
		}
	}
	out.Set = append(slices.Clip(out.Set), as...)
	out.DoNothing = false
	return &out, nil
}

// upsertSkips returns cf without the skipped setters of its update branch's
// Set (a SetPtr of nil): such a column is not assigned. When they were all
// it had, and Update copies nothing, the branch keeps the existing row
// through Ignore's no-op assignment, as Setters.Upsert makes it.
func upsertSkips(cf *ConflictClause) *ConflictClause {
	if cf == nil || !slices.ContainsFunc(cf.Set, isSkip) {
		return cf
	}
	out := *cf
	out.Set = slices.DeleteFunc(slices.Clone(cf.Set), isSkip)
	if len(out.Set) == 0 {
		out.Set = nil
		if len(out.Update) == 0 && !out.DoNothing {
			out.Ignore = true
		}
	}
	return &out
}

// Empty reports whether s holds no setter.
func (s Setters) Empty() bool { return len(s.Set) == 0 && len(s.Edges) == 0 }

// CheckInsert refuses a column setter of an insert into in that is not a
// Set: Add, Sub and an expression need a row.
func (s Setters) CheckInsert(in *EntityInfo) error {
	for _, a := range s.Set {
		if a.Op == AssignExpr {
			return fmt.Errorf("velox: %s: an insert's setter of %s: %w", in.Name, in.Columns[a.Column].Field, errSetExprInsert)
		}
		if a.Op != AssignSet {
			return fmt.Errorf("velox: %s: an insert's setter of %s is Add, Sub or Append, which needs a row; use Set", in.Name, in.Columns[a.Column].Field)
		}
	}
	return nil
}

// CheckInsertQuery is InsertQuery's check of an insert into in with the
// setters s and the conflict clause cf, of the row one (nil for an
// InsertAll); nilOne says Insert was given a nil pointer, and all says
// UpdateAll. It refuses what cannot be built, and returns the clause the
// insert sends: cf with UpdateAll's columns (resolveUpdateAll), as
// Setters.Upsert makes it. It is not generic, so that every entity's
// InsertQuery shares it.
func CheckInsertQuery(in *EntityInfo, s Setters, cf *ConflictClause, one unsafe.Pointer, nilOne, all bool) (*ConflictClause, error) {
	if nilOne {
		return nil, fmt.Errorf("velox: %s: Insert of a nil pointer", in.Name)
	}
	if err := s.CheckInsert(in); err != nil {
		return nil, err
	}
	cf = upsertSkips(cf)
	if all {
		cf = resolveUpdateAll(in, cf)
	}
	cf = s.Upsert(cf)
	if cf != nil {
		if err := CheckConflict(in, cf, one); err != nil {
			return nil, err
		}
	}
	return cf, nil
}

// PrepareInsert resolves s's edge setters for an insert of rows of in (an
// upsert when upsert, an InsertAll when many), checks them, and stores the
// column setters, Set's included, in each row. A plan that writes edges
// after the rows needs their keys: an upsert, or an InsertAll of a row
// whose key the database assigns, fails.
func PrepareInsert(in *EntityInfo, s Setters, rows []unsafe.Pointer, upsert, many bool) (EdgePlan, error) {
	var p EdgePlan
	set := s.Set
	if len(s.Edges) > 0 {
		var err error
		if p, err = PlanEdges(in, s.Edges); err != nil {
			return EdgePlan{}, err
		}
		if p.Writes() {
			gen := false
			if many && len(in.PrimaryKey) == 1 {
				for _, r := range rows {
					gen = gen || ZeroInt(fieldAddr(in, r, in.PrimaryKey[0]))
				}
			}
			if err := CheckEdgeInsert(in, upsert, many, gen); err != nil {
				return EdgePlan{}, err
			}
		}
		set = append(slices.Clip(set), p.Set...)
	}
	for _, r := range rows {
		for _, a := range set {
			SetElem(fieldAddr(in, r, a.Column), a.Value)
		}
	}
	return p, nil
}

// Insert runs an insert of rows of in with the setters s: PrepareInsert,
// then send, the insert's work inside the entity's hooks, which it runs
// through them. An insert with edge setters runs as InsertEdges says.
func Insert(ctx context.Context, db *DB, in *EntityInfo, s Setters, rows []unsafe.Pointer, upsert, many bool, send func(ctx context.Context, db *DB, mu *MutationState) error) error {
	p, err := PrepareInsert(in, s, rows, upsert, many)
	if err != nil {
		return err
	}
	run := func(ctx context.Context, db *DB) (int64, error) {
		mu := &MutationState{Op: OpInsert, Rows: rows, Edges: s.Edges}
		if !db.HasHooks(in) {
			err := send(ctx, db, mu)
			return mu.Affected, err
		}
		err := Mutate(ctx, db, in, mu, func(ctx context.Context, mu *MutationState) error { return send(ctx, db, mu) })
		return mu.Affected, err
	}
	if len(s.Edges) == 0 {
		_, err := run(ctx, db)
		return err
	}
	return InsertEdges(ctx, db, p, rows, run)
}

// Update runs st, an update, with the edge setters edges, as UpdateEdges
// says; with none, it is Write's.
func Update(ctx context.Context, db *DB, st *Statement, edges []*EdgeSet) (int64, error) {
	if skippedOnly(st, len(edges)) {
		return 0, skippedUpdate(db, st)
	}
	if err := checkKeep(st.Entity, st.keep); err != nil {
		return 0, err
	}
	if len(edges) == 0 {
		n, _, _, err := Write(ctx, db, st, false, false)
		return n, err
	}
	p, err := PlanEdges(st.Entity, edges)
	if err != nil {
		return 0, err
	}
	st.Set = append(st.Set, p.Set...)
	return UpdateEdges(ctx, db, st, p)
}

// UpdateSQL is UpdateQuery.SQL's work for st with the edge setters edges:
// the statement and arguments, "" for an update of only edge setters.
func UpdateSQL(ctx context.Context, db *DB, st *Statement, edges []*EdgeSet) (string, []any, error) {
	if skippedOnly(st, len(edges)) {
		return "", nil, skippedUpdate(db, st)
	}
	if err := checkKeep(st.Entity, st.keep); err != nil {
		return "", nil, err
	}
	if len(edges) > 0 {
		p, err := PlanEdges(st.Entity, edges)
		if err != nil {
			return "", nil, err
		}
		st.Set = append(st.Set, p.Set...)
		if len(st.Set) == 0 {
			return "", nil, db.guard(st)
		}
	}
	_, q, args, err := writeWith(ctx, db, st, true, false, edges)
	return q, args, err
}

// UpdateOne is UpdateQuery.One's work for st with the edge setters edges:
// it updates the one row st's conditions match and returns it as written.
// It runs in one transaction on db: a savepoint inside one, unless it
// writes no edge, as Exec.
//
// When st's conditions pin every primary-key column to a value, so they
// match one row at most, and it assigns something and writes no edge, it
// sends st as it is (updateByKey). Otherwise it first reads the row's key
// under st's conditions and the Read and Write policies, locked where the
// dialect can, failing with ErrNotFound when no row matches and
// ErrNotSingular when more than one does; it then sends st with that key
// as its only condition and writes the edges for the row.
//
// Either way it reads the row back by its key, as the update left it
// (writtenKey), without the Read policy, so a row the update moves out of
// the caller's view is returned too. An st with no assignment sends no
// UPDATE; with no setter at all, the row is read back under the policies.
func UpdateOne(ctx context.Context, db *DB, st *Statement, edges []*EdgeSet) (unsafe.Pointer, error) {
	in := st.Entity
	if len(in.PrimaryKey) == 0 {
		return nil, fmt.Errorf("velox: %s: One needs a primary key to read the row back", in.Name)
	}
	if err := checkKeyAssigned(in, st.Set); err != nil {
		return nil, err
	}
	if err := checkKeep(in, st.keep); err != nil {
		return nil, err
	}
	var p EdgePlan
	if len(edges) > 0 {
		var err error
		if p, err = PlanEdges(in, edges); err != nil {
			return nil, err
		}
		st.Set = append(st.Set, p.Set...)
	}
	if err := db.guard(st); err != nil {
		return nil, err
	}
	t := InfoTable(in)
	if vals, ok := pinnedKeys(in, st); ok && len(edges) == 0 {
		return updateByKey(ctx, db, t, st, vals)
	}
	cols := slices.Clone(in.PrimaryKey)
	for _, c := range p.OwnerCols() {
		if !slices.Contains(cols, c) {
			cols = append(cols, c)
		}
	}
	var out unsafe.Pointer
	run := func(ctx context.Context, tx *DB) error {
		if err := p.check(ctx, tx); err != nil {
			return err
		}
		q := QueryTable(tx, t).Where(st.Where...).Columns(cols...).Writable()
		if tx.D != SQLite {
			q = q.ForUpdate()
		}
		row, err := q.one(ctx)
		if err != nil {
			return err
		}
		vals := make([]any, len(in.PrimaryKey))
		for i, k := range in.PrimaryKey {
			vals[i], _ = plain(in.kit.Value(row, k))
		}
		read, key := tx, keyPreds(t, vals)
		if len(st.Set) > 0 {
			st.InTx = true
			st.Where = key // the row read, locked, matches st's conditions
			n, _, _, err := writeWith(ctx, tx, st, false, false, p.sets)
			if err != nil {
				return err
			}
			if n > 0 {
				if key, err = writtenKey(t, st, vals); err != nil {
					return err
				}
			}
			read = tx.WithoutPolicy("velox: the row UpdateQuery.One wrote")
		}
		if p.Writes() {
			if err := p.write(ctx, tx, []unsafe.Pointer{row}); err != nil {
				return err
			}
			read = tx.WithoutPolicy("velox: the row UpdateQuery.One wrote")
		}
		back := QueryTable(read, t).Where(key...)
		back.st.unscoped = read != tx // the row written: no interceptor hides it
		out, err = back.one(ctx)
		return err
	}
	var err error
	if p.Writes() {
		err = atomically(ctx, db, run)
	} else {
		// In the caller's transaction, run there with no savepoint of its
		// own, as Exec (a recorded write takes one, recordedTx); outside one,
		// in velox's, whose sink runs before its COMMIT.
		err = ownTx(ctx, db, run)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// updateByKey is UpdateOne for an st whose conditions pin the row's key to
// vals (pinnedKeys), that assigns something and writes no edge. It sends st
// as it is. An UPDATE is scoped by the entity's interceptors and its Read
// and Write policies, as the Writable read of the key would have been, and
// it locks the row it changes, so the key read would add nothing.
//
// One row affected: it reads the row back by its key as written, without
// the Read policy. None: a hook skipped the write, or no row matched. It
// then reads the row under st's conditions and both policies, and returns
// it, or ErrNotFound. More than one, which only a hook's SetAffected or the
// driver can report, as the key matches one row: ErrNotSingular, and the
// transaction rolls back.
func updateByKey(ctx context.Context, db *DB, t Table, st *Statement, vals []any) (unsafe.Pointer, error) {
	in := t.Info()
	where := slices.Clone(st.Where)
	var out unsafe.Pointer
	run := func(ctx context.Context, tx *DB) error {
		st.InTx = true
		n, _, _, err := Write(ctx, tx, st, false, false)
		switch {
		case err != nil:
			return err
		case n > 1:
			return fmt.Errorf("velox: %s: the update affected %d rows: %w", in.Name, n, ErrNotSingular)
		case n == 0:
			out, err = QueryTable(tx, t).Where(where...).Writable().one(ctx)
			return err
		}
		key, err := writtenKey(t, st, vals)
		if err != nil {
			return err
		}
		// The row written, read with no row condition: an interceptor such as
		// softdelete's must not hide a row the update moved out of its view.
		q := QueryTable(tx.WithoutPolicy("velox: the row UpdateQuery.One wrote"), t).Where(key...)
		q.st.unscoped = true
		out, err = q.one(ctx)
		return err
	}
	// In the caller's transaction, run there with no savepoint of its own,
	// as Exec (a recorded write takes one, recordedTx); outside one, in
	// velox's, whose sink runs before its COMMIT.
	if err := ownTx(ctx, db, run); err != nil {
		return nil, err
	}
	return out, nil
}

// pinnedKeys returns the value of each primary-key column when st assigns
// something and its conditions hold, at their top level, <column> = v for
// every key column, v not nil: then they match one row at most.
func pinnedKeys(in *EntityInfo, st *Statement) ([]any, bool) {
	if len(st.Set) == 0 {
		return nil, false
	}
	return pinnedWhere(in, st.Where)
}

// pinnedWhere returns the value of each primary-key column when where
// holds, at its top level, <column> = v for every key column, v not nil.
func pinnedWhere(in *EntityInfo, where []Predicate) ([]any, bool) {
	if len(in.PrimaryKey) == 0 {
		return nil, false
	}
	vals := make([]any, len(in.PrimaryKey))
	for i, k := range in.PrimaryKey {
		v, ok := pinnedValue(int32(k), where)
		if !ok {
			return nil, false
		}
		vals[i] = v
	}
	return vals, true
}

// pinnedValue returns v when where holds col = v, a non-nil v, at its top
// level: in a condition of where or in an And among them.
func pinnedValue(col int32, where []Predicate) (any, bool) {
	var pinned func(n Node) (any, bool)
	pinned = func(n Node) (any, bool) {
		switch n.Op {
		case OpEQ:
			if n.Col == col {
				if v, ok := keyOf(n.V); ok && v != nil {
					return v, true
				}
			}
		case OpAnd:
			for _, k := range n.Kids {
				if v, ok := pinned(k); ok {
					return v, true
				}
			}
		}
		return nil, false
	}
	for _, p := range where {
		if v, ok := pinned(p.n); ok {
			return v, true
		}
	}
	return nil, false
}

// keyPreds is the condition that each primary-key column equals its value
// in vals.
func keyPreds(t Table, vals []any) []Predicate {
	in := t.Info()
	key := make([]Predicate, len(in.PrimaryKey))
	for i, k := range in.PrimaryKey {
		key[i] = t.Pred(k, PredEQ, vals[i])
	}
	return key
}

// writtenKey is keyPreds of the row's key after st, which held vals before
// it: a key column st assigns holds the value assigned. st is the statement
// as the hooks left it, so a key a hook assigns counts too.
func writtenKey(t Table, st *Statement, vals []any) ([]Predicate, error) {
	in := t.Info()
	if err := checkKeyAssigned(in, st.Set); err != nil {
		return nil, err
	}
	vals = slices.Clone(vals)
	for _, a := range st.Set {
		if i := slices.Index(in.PrimaryKey, a.Column); i >= 0 {
			vals[i], _ = keyOf(a.Value)
		}
	}
	return keyPreds(t, vals), nil
}

// checkKeyAssigned refuses an Add, Sub or expression on a primary-key column: One could
// not tell the key of the row it wrote to read it back.
func checkKeyAssigned(in *EntityInfo, set []Assignment) error {
	for _, a := range set {
		if a.Op != AssignSet && slices.Contains(in.PrimaryKey, a.Column) {
			return fmt.Errorf("velox: %s: One cannot read back a row whose key %s it changes by Add or Sub, or by an expression; use Exec", in.Name, in.Columns[a.Column].Name)
		}
	}
	return nil
}
