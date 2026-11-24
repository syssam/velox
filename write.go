package velox

import (
	"context"
	"fmt"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// UpdateQuery is an UPDATE of entity E, built by chaining and run by Exec.
// Like Query it is a value: extending it leaves it as it was.
type UpdateQuery[E any] struct {
	db    *DB
	meta  *metaState[E]
	st    Statement
	err   error             // a condition's error, as in Query
	edges []*engine.EdgeSet // Set's edge setters; st.Set holds its assignments
}

// Update starts an UPDATE of entity E:
//
//	db.Update[billing.Invoice]().Set(I.PaidAt.Set(now)).Where(I.ID.EQ(id)).Exec(ctx)
func (db *DB) Update[E any, PE Entity[E]]() UpdateQuery[E] {
	m := stateOf(PE(nil).VeloxMeta())
	return UpdateQuery[E]{db: db, meta: m, st: Statement{Op: OpUpdate, Entity: m.Info, InTx: db.e.InTx}}
}

// Set adds setters: assignments, made by the column handles' Set, SetNull,
// Add and Sub, and edge setters, made by the relation handles' Add, Remove
// and Clear. An update of only edge setters runs its hooks and its
// UpdateDefaults, as ent's does, and sends an UPDATE only when they assign
// something (ADR 0136). A SetPtr given
// a nil pointer is skipped. An update whose every setter was skipped, or
// that was given a Set of nothing, is an empty patch: as in ent, its hooks
// run and every UpdateDefault fires, so updated_at moves, and the UPDATE
// writes what they assign. When they assign nothing either, nothing is
// sent: Exec returns 0 and SQL "", once the guard and KeepDefaults' check
// pass, and One reads the row as Writable (ADR 0136). An update never given
// a Set fails with "Update has no Set".
//
// The edges are written for each row the update matches, in one
// transaction with the UPDATE, a savepoint inside one: the update first
// reads the keys of the rows its conditions match that the Read and Write
// policies let the caller change, locking them where the dialect can, then
// sends the UPDATE, then writes each edge for each row, as InsertQuery.Set
// says. The entity's hooks see the UPDATE alone, without the edges.
func (u UpdateQuery[E]) Set[_ Lazy[E]](ss ...Setter[E]) UpdateQuery[E] {
	u.edges = engine.UpdateSet(&u.st, u.edges, assignments(ss))
	return u
}

// SetInput adds the setters in.Setters returns, as Set does. When Setters fails,
// the update keeps its error, and Exec, One and SQL return it unchanged
// before anything is sent:
//
//	db.Update[veloxgen.Post]().Where(veloxgen.Posts.ID.EQ(id)).SetInput(input).One(ctx)
func (u UpdateQuery[E]) SetInput[_ Lazy[E]](in Input[E]) UpdateQuery[E] {
	ss, err := in.Setters()
	if err != nil {
		if u.err == nil {
			u.err = err
		}
		return u
	}
	return u.Set(ss...)
}

// KeepDefaults stops the UpdateDefault of each named column from firing, so
// the update writes nothing to it: a system
// write that must not move an updated_at column. A column the update
// assigns is still written as assigned; an assignment already won over the
// UpdateDefault, but it writes a value, where KeepDefaults writes nothing.
// A column without an UpdateDefault fails Exec, One and SQL with an error
// that names it, before anything is sent, even when no UPDATE would be: a
// One with no setter, an update of only edge setters or of only skipped
// ones. A hook that assigns the column writes it.
func (u UpdateQuery[E]) KeepDefaults[_ Lazy[E]](cols ...Column[E]) UpdateQuery[E] {
	engine.KeepDefaults(&u.st, keepIndexes(cols))
	return u
}

// keepIndexes is the index of each column of cols, -1 for a nil one, which
// the update then refuses.
func keepIndexes[E any](cols []Column[E]) []int {
	out := make([]int, len(cols))
	for i, c := range cols {
		out[i] = -1
		if c != nil {
			out[i] = int(c.column(nil))
		}
	}
	return out
}

// Where adds conditions; all of them must hold.
func (u UpdateQuery[E]) Where[_ Lazy[E]](ps ...Pred[E]) UpdateQuery[E] {
	u.st.Where = slices.Clip(u.st.Where)
	for _, p := range ps {
		u.st.Where = append(u.st.Where, Predicate(p))
		if u.err == nil {
			if err := engine.PredErr(engine.PredNode(Predicate(p))); err != nil {
				u.err = err
			}
		}
	}
	return u
}

// All declares that the update has no Where on purpose and changes every
// row. An update with neither a Where nor All fails with ErrNoWhere.
func (u UpdateQuery[E]) All[_ Lazy[E]]() UpdateQuery[E] {
	u.st.AllRows = true
	return u
}

// statement is the UPDATE Exec and SQL give the hooks: Set's assignments.
// The core adds the UpdateDefault ones and checks them all (writeCore).
func (u UpdateQuery[E]) statement[_ Lazy[E]]() (Statement, error) {
	if u.meta.Err != nil {
		return Statement{}, u.meta.Err
	}
	if u.err != nil {
		return Statement{}, u.err
	}
	if err := engine.CheckUpdateSet(&u.st, len(u.edges)); err != nil {
		return Statement{}, err
	}
	return engine.CloneStatement(&u.st), nil
}

// Exec runs the update and returns the number of rows it matched (on MySQL,
// with clientFoundRows=true; without it, the rows whose values changed), or
// -1 when the driver could not report its count. An update of only edge
// setters returns the number of rows whose edges it wrote.
//
// The guard runs first, on the statement as built, then the entity's hooks
// (Hooks). Then Exec keeps only the last setter of a column set more than
// once, as ent's mutation keeps one value per field, adds an assignment for
// every column with an UpdateDefault that the statement does not assign,
// refuses an Immutable column (ErrImmutable), and runs the validators,
// before the statement goes through the pipeline.
func (u UpdateQuery[E]) Exec[_ Lazy[E]](ctx context.Context) (int64, error) {
	st, err := u.statement()
	if err != nil {
		return 0, err
	}
	return engine.Update(ctx, &u.db.e, &st, u.edges)
}

// One runs the update on the one row it matches and returns that row as
// written, as ent's UpdateOne does. Its error wraps ErrNotFound when the
// conditions match no row the Read and Write policies let the caller
// change, and ErrNotSingular when they match more than one; then nothing
// is written.
//
// It runs in one transaction, a savepoint inside one unless it writes no
// edge. When the conditions pin every primary-key column (X.ID.EQ(id)) and
// it writes no edge, it sends the UPDATE with them, through the hooks as
// Exec does: an UPDATE has the Read and Write policies and locks the row.
// Otherwise it first reads the row's key, locked where the dialect can, and
// sends the UPDATE with that key as its only condition, then writes the
// edge setters' edges. Either way it then reads the whole row back by its
// key, the one the update assigned if it assigns one, without the Read
// policy, so a row the update moves out of the caller's view is still
// returned; an Interceptor still filters it. An UPDATE by key that affects
// no row is followed by a read of the row under the policies: a hook that
// skips the write still gets the row. An Add or Sub on a key column is
// refused. An update with no setter, or only skipped ones, runs its hooks
// and its UpdateDefaults as ent's UpdateOne does; when they assign nothing,
// it sends no UPDATE and returns the row read under the policies
// (ADR 0136).
func (u UpdateQuery[E]) One[_ Lazy[E]](ctx context.Context) (*E, error) {
	if u.meta.Err != nil {
		return nil, u.meta.Err
	}
	if u.err != nil {
		return nil, u.err
	}
	st := engine.CloneStatement(&u.st)
	p, err := engine.UpdateOne(ctx, &u.db.e, &st, u.edges)
	if err != nil {
		return nil, err
	}
	return (*E)(p), nil
}

// SQL returns the statement and arguments Exec would send for ctx, or the
// error Exec would fail with before sending it; it sends nothing. It runs
// every stage Exec runs before execution: the guard, the entity's hooks, the
// UpdateDefault assignments and the checks, the guard again, the
// Rewrite hooks, the policy scope and
// rendering. So the hooks and the policy run, and can tell from
// IsDryRun(ctx), or a Rewrite hook from Statement.DryRun, that the statement
// will not be sent. Around hooks, which wrap execution, do not run. A write a
// hook makes under ctx, an audit row through Mutation.DB say, is refused with
// ErrDryRun: a hook that returns that error after next leaves SQL(ctx) its
// statement; one that returns it before next, SQL(ctx) fails with it. An
// update that sets the key of a bidirectional one-to-one edge (EdgeBidi)
// renders as the caller's UPDATE alone, without the statements that re-point
// the partners. So does an update with edge setters, without the read of the
// rows it matches and the edges' statements; one of only edge setters
// renders what its hooks and UpdateDefaults assign, "" when nothing. The
// edge setters are checked as Exec checks them.
func (u UpdateQuery[E]) SQL[_ Lazy[E]](ctx context.Context) (string, []any, error) {
	ctx = engine.DryRunContext(ctx) // first, so that every hook sees it
	st, err := u.statement()
	if err != nil {
		return "", nil, err
	}
	return engine.UpdateSQL(ctx, &u.db.e, &st, u.edges)
}

// DeleteQuery is a DELETE of entity E, built by chaining and run by Exec.
type DeleteQuery[E any] struct {
	db   *DB
	meta *metaState[E]
	st   Statement
	err  error // a condition's error, as in Query
}

// Delete starts a DELETE of entity E.
func (db *DB) Delete[E any, PE Entity[E]]() DeleteQuery[E] {
	m := stateOf(PE(nil).VeloxMeta())
	return DeleteQuery[E]{db: db, meta: m, st: Statement{Op: OpDelete, Entity: m.Info, InTx: db.e.InTx}}
}

// Where adds conditions; all of them must hold.
func (d DeleteQuery[E]) Where[_ Lazy[E]](ps ...Pred[E]) DeleteQuery[E] {
	d.st.Where = slices.Clip(d.st.Where)
	for _, p := range ps {
		d.st.Where = append(d.st.Where, Predicate(p))
		if d.err == nil {
			if err := engine.PredErr(engine.PredNode(Predicate(p))); err != nil {
				d.err = err
			}
		}
	}
	return d
}

// All declares that the delete has no Where on purpose and removes every
// row. A delete with neither a Where nor All fails with ErrNoWhere.
func (d DeleteQuery[E]) All[_ Lazy[E]]() DeleteQuery[E] {
	d.st.AllRows = true
	return d
}

// Exec runs the delete and returns the number of rows it removed, or -1 when
// the driver could not report its count.
func (d DeleteQuery[E]) Exec[_ Lazy[E]](ctx context.Context) (int64, error) {
	if d.meta.Err != nil {
		return 0, d.meta.Err
	}
	if d.err != nil {
		return 0, d.err
	}
	st := engine.CloneStatement(&d.st)
	n, _, _, err := engine.Write(ctx, &d.db.e, &st, false, false)
	return n, err
}

// One runs the delete on the one row it matches, as ent's DeleteOne does.
// Its error wraps ErrNotFound when it removes no row: none matches, the
// Write policy or an Interceptor hides it, softdelete's included, or a hook
// skips the delete without SetAffected. A delete by key is
//
//	err := db.Delete[billing.Invoice]().Where(I.ID.EQ(id)).One(ctx)
//
// When the conditions pin every primary-key column, One sends the DELETE as
// Exec does, one statement. Otherwise it runs in a transaction, a savepoint
// inside one, and its error wraps ErrNotSingular, with nothing removed, when
// the delete matches more than one row. The hooks see the caller's
// conditions either way.
func (d DeleteQuery[E]) One[_ Lazy[E]](ctx context.Context) error {
	if d.meta.Err != nil {
		return d.meta.Err
	}
	if d.err != nil {
		return d.err
	}
	st := engine.CloneStatement(&d.st)
	return engine.DeleteOne(ctx, &d.db.e, &st)
}

// SQL returns the statement and arguments Exec would send for ctx, or the
// error Exec would fail with before sending it; it sends nothing. It runs
// every stage Exec runs before execution: the guard, the entity's hooks, the
// guard again, the Rewrite hooks, the
// policy scope and rendering. So the hooks and the policy run, and can tell
// from IsDryRun(ctx), or a Rewrite hook from Statement.DryRun, that the
// statement will not be sent. Around hooks, which wrap execution, do not run.
// A write a hook makes under ctx is refused with ErrDryRun, as in
// UpdateQuery.SQL.
func (d DeleteQuery[E]) SQL[_ Lazy[E]](ctx context.Context) (string, []any, error) {
	ctx = engine.DryRunContext(ctx) // first, so that every hook sees it
	if d.meta.Err != nil {
		return "", nil, d.meta.Err
	}
	if d.err != nil {
		return "", nil, d.err
	}
	st := engine.CloneStatement(&d.st)
	_, q, args, err := engine.Write(ctx, &d.db.e, &st, true, false)
	return q, args, err
}

// SaveQuery is a Save of one entity, built by chaining and run by Exec. Like
// Query it is a value: extending it leaves it as it was. It holds x, not its
// values: *x is read when Exec or SQL runs, not when Save is called, and
// after Exec it holds what was written, the UpdateDefault values included.
type SaveQuery[E any] struct {
	db    *DB
	meta  *metaState[E]
	x     *E
	cols  []Column[E]
	keep  []int // KeepDefaults' columns
	where []Pred[E]
}

// Save starts a write of the named columns of *x to the row with x's primary
// key:
//
//	db.Save(&inv, I.Number, I.Total).Exec(ctx)
//
// At least one column is required, which the signature enforces. Naming a
// column twice is an error, reported by Exec and SQL. *x is read when Exec or
// SQL runs, not here; after Exec, *x holds the values written, the
// UpdateDefault ones included.
func (db *DB) Save[E any, PE Entity[E]](x PE, col Column[E], more ...Column[E]) SaveQuery[E] {
	cols := make([]Column[E], 1+len(more))
	cols[0] = col
	copy(cols[1:], more)
	return SaveQuery[E]{db: db, meta: stateOf(x.VeloxMeta()), x: (*E)(x), cols: cols} // VeloxMeta works on a nil x
}

// Where adds conditions to the key's: the row is written only if all of them
// hold, and Exec fails with ErrNotFound when it is not. The conditions are
// part of the statement the hooks and the policy see. It makes a write
// conditional on what the caller read:
//
//	err := db.Save(&u, U.Email).Where(U.Status.EQ("draft")).Exec(ctx)
//
// fails with ErrNotFound when another writer moved the status. An entity with
// a Version field (field.Int64("version").Version()) needs no such condition:
// Save adds it, and fails with ErrStale.
func (s SaveQuery[E]) Where[_ Lazy[E]](ps ...Pred[E]) SaveQuery[E] {
	s.where = append(slices.Clip(s.where), ps...)
	return s
}

// KeepDefaults stops the UpdateDefault of each named column from firing, as
// UpdateQuery.KeepDefaults does: the Save writes nothing to it, and leaves
// its field in *x as it is. A column Save names is still written.
func (s SaveQuery[E]) KeepDefaults[_ Lazy[E]](cols ...Column[E]) SaveQuery[E] {
	s.keep = append(slices.Clip(s.keep), keepIndexes(cols)...)
	return s
}

// statement is the UPDATE Exec and SQL give the hooks: the named columns of
// *x, where the key and the conditions hold.
func (s SaveQuery[E]) statement[_ Lazy[E]]() (Statement, error) {
	if s.meta.Err != nil {
		return Statement{}, s.meta.Err
	}
	in := s.meta.Info
	if s.x == nil {
		return Statement{}, fmt.Errorf("velox: %s: Save of a nil pointer", in.Name)
	}
	st := Statement{Op: OpUpdate, Entity: in, InTx: s.db.e.InTx}
	for _, c := range s.cols {
		if c == nil {
			return Statement{}, fmt.Errorf("velox: %s: Save names a nil column", in.Name)
		}
		i := int(c.column(nil))
		st.Set = append(st.Set, Assignment{Column: i, Op: AssignSet, Value: fieldValue(s.meta, s.x, i)})
	}
	engine.KeepDefaults(&st, s.keep)
	for _, k := range in.PrimaryKey {
		st.Where = append(st.Where, ColumnEQ(k, fieldValue(s.meta, s.x, k)))
	}
	if v, ok := in.VersionColumn(); ok {
		st.Where = append(st.Where, ColumnEQ(v, fieldValue(s.meta, s.x, v)))
	}
	for _, p := range s.where {
		if err := engine.PredErr(engine.PredNode(Predicate(p))); err != nil {
			return Statement{}, err
		}
		st.Where = append(st.Where, Predicate(p))
	}
	return st, nil
}

// Exec writes the named columns of *x, and every column with an
// UpdateDefault, to the row with x's primary key, where the conditions of
// Where also hold. The entity's hooks (Hooks) see it as an update, OpUpdate.
// Once the row was written, it stores in *x every value the statement as sent
// assigned, the UpdateDefault values and those a hook (Hooks or Rewrite) set
// included, when the field can hold it; after an error, including
// ErrNotFound, *x is as it was, and so it is when a hook returns nil without
// calling next, whether or not it called Mutation.SetAffected.
// Its error wraps ErrNotFound when no row has that key and meets the
// conditions. On an entity with a Version field the row must also hold the
// version *x holds: Exec adds 1 to it, stores the new version in *x, and its
// error wraps ErrStale as well when no row matches. On MySQL, open the database with clientFoundRows=true:
// otherwise a row whose values did not change counts as not found.
func (s SaveQuery[E]) Exec[_ Lazy[E]](ctx context.Context) error {
	st, err := s.statement()
	if err != nil {
		return err
	}
	n, _, _, err := engine.Write(ctx, &s.db.e, &st, false, true)
	if err != nil || n == 0 {
		// n == 0 without an error: a hook skipped next, and nothing was written.
		return err
	}
	// The statement as sent: run leaves in st.Set what the hooks made of it.
	for _, a := range st.Set {
		if a.Op == AssignSet {
			setField(s.meta, s.x, a.Column, a.Value)
		}
	}
	if v, ok := s.meta.Info.VersionColumn(); ok {
		engine.BumpVersion(s.meta.Addr(s.x, v))
	}
	s.db.e.Source().Put(s.meta.Info, unsafe.Pointer(s.x))
	return nil
}

// SQL returns the statement and arguments Exec would send for ctx, or the
// error Exec would fail with before sending it; it sends nothing, and *x is
// not changed. It runs every stage Exec runs before execution, as
// UpdateQuery.SQL does: the guard, the entity's hooks, the UpdateDefault
// assignments and the checks, the Rewrite hooks, the policy scope
// and rendering. The hooks and the policy can tell from IsDryRun(ctx) that
// the statement will not be sent. Around hooks do not run. A write a hook
// makes under ctx is refused with ErrDryRun, as in UpdateQuery.SQL. A Save
// of the key of a bidirectional one-to-one edge (EdgeBidi) renders as the
// caller's UPDATE alone, as in UpdateQuery.SQL.
func (s SaveQuery[E]) SQL[_ Lazy[E]](ctx context.Context) (string, []any, error) {
	ctx = engine.DryRunContext(ctx) // first, so that every hook sees it
	st, err := s.statement()
	if err != nil {
		return "", nil, err
	}
	_, q, args, err := engine.Write(ctx, &s.db.e, &st, true, true)
	return q, args, err
}

// fieldValue is the value of column i's field in *p.
func fieldValue[E any](m *metaState[E], p *E, i int) any { return engine.Elem(m.Addr(p, i)) }

// setField stores v in column i's field in *p, as SetElem does.
func setField[E any](m *metaState[E], p *E, i int, v any) { engine.SetElem(m.Addr(p, i), v) }
