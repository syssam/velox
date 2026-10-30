package velox

import (
	"context"
	"unsafe"

	"github.com/syssam/velox2/internal/engine"
)

// Policy is entity E's row-level policy, declared once: by a method on the
// entity's pointer type,
//
//	func (*E) Policy() velox.Policy[E]
//
// velox-gen binds the method in the generated metadata (Desc.Policy calls
// BindPolicy on it): a method with another signature does not compile, and
// one the metadata does not bind is refused at the entity's first use. velox calls it once, when it
// builds the metadata, on a nil *E, so the method must not read its receiver.
// Nor may its body, outside the functions it returns, read the entity's own
// metadata (a query on it, say): that waits on the build in progress and
// deadlocks. A Has or HasWhere reads none, so the body may build them, and the
// functions it returns may share them: a relation resolves when a statement
// holding the condition runs.
//
// Any field may be nil. A predicate function returns Allow[E]() for no
// restriction; its zero Pred[E]{} fails the statement, naming the entity, so
// that a code path that forgot to return a condition never lets a caller
// through. Any error it returns denies the statement.
//
// A policy function decides from ctx and, for Insert, the row; it does not
// query. A rule that must see other rows, such as that an inserted comment's
// parent is one the caller may read, is a hook (Hooks): it queries through
// Mutation.DB, in the write's transaction, and returns the error that refuses
// the write. This is ent's split: privacy rules decide, hooks query.
type Policy[E any] struct {
	// Read is ANDed into every SELECT, Count, With load and Has/HasWhere
	// subquery on the entity, into every UPDATE, DELETE and Save, and into
	// the update branch of an upsert, on the existing row.
	Read func(ctx context.Context) (Pred[E], error)
	// Write is ANDed into every UPDATE, DELETE and Save, and into an upsert's
	// update branch, with Read. It decides which existing rows may change; it
	// does not check the new values (there is no WITH CHECK), so a writer may
	// set a column the policy reads, such as an owner. Only the tenant column
	// cannot be changed.
	//
	// A row Write hides is left alone, and that is not an error: an Update or
	// Delete that matches only such rows affects 0 rows, as with SQL row-level
	// security.
	Write func(ctx context.Context) (Pred[E], error)
	// Insert checks every inserted or upserted row, after the entity's
	// hooks, the defaults, the tenant fill and the
	// validators.
	Insert func(ctx context.Context, x *E) error
}

// Mutation is the write that an entity's hooks (Hooks) see and may change.
// A misuse (Set on a delete, a zero or invalid condition, a handle's
// ValueIn, Set, Where or SetAffected on an insert, ValueIn of a value of
// another type, SetAffected with next) is recorded and fails the write: before next sends
// it, or once the hook returns. A hook reads an assignment with the column
// handle's ValueIn, the conditions with Conds, and once next has returned
// the rows written with Affected.
//
// A hook may make the write itself instead of calling next. A soft delete
// is a delete hook that updates the rows the delete would remove, and
// reports the update's count with SetAffected, which the delete's Exec then
// returns:
//
//	q := m.DB().Update[Task]().Set(Tasks.DeletedAt.Set(now))
//	if cs := m.Conds(); len(cs) > 0 {
//		q = q.Where(cs...)
//	} else {
//		q = q.All()
//	}
//	n, err := q.Exec(ctx)
//	m.SetAffected(n)
//	return err
//
// The update adds the tenant's and the policy's conditions itself. Under
// SQL(ctx) such a delete fails with ErrDryRun: the hook's update is a write,
// and there is no DELETE to render.
type Mutation[E any] struct{ mu engine.MutationState }

// Op returns the write's kind: OpInsert, OpUpdate or OpDelete.
func (m *Mutation[E]) Op[_ Lazy[E]]() Op {
	if m.mu.Op != 0 {
		return m.mu.Op
	}
	return m.mu.St.Op
}

// Rows returns an insert's rows, which a hook may change before next; nil
// for an update or delete.
func (m *Mutation[E]) Rows[_ Lazy[E]]() []*E {
	if m.mu.Rows == nil {
		return nil
	}
	out := make([]*E, len(m.mu.Rows))
	for i, p := range m.mu.Rows {
		out[i] = (*E)(p)
	}
	return out
}

// DB returns the DB the write runs on: the transaction's, inside db.Tx, or
// else the database's. A hook that reads or writes other rows uses it, so
// that its work commits or rolls back with the write:
//
//	err := m.DB().Insert(&audit).Exec(ctx)
//
// It is set only for the writes of entities that have hooks, which are the
// only ones that see a Mutation.
func (m *Mutation[E]) DB[_ Lazy[E]]() *DB {
	db, _ := m.mu.DB.(*engine.DB)
	return (*DB)(unsafe.Pointer(db))
}

// Conds returns the conditions of an update or a delete as the caller and
// the hooks gave them: the caller's Where and those the hooks before this one
// added, in order, as copies (changing the slice changes nothing). Once the
// innermost hook has called next, it returns them as they were then, so an
// outer hook whose next has returned also sees the conditions the hooks
// inside it added. It never holds the tenant's, the policy's or a Rewrite
// hook's conditions, which next adds. It is empty only when the statement
// said All, and nil for an insert. Mutation shows a soft delete built on it.
func (m *Mutation[E]) Conds[_ Lazy[E]]() []Pred[E] {
	if m.mu.St == nil {
		return nil
	}
	w := m.mu.St.Where
	if m.mu.Called {
		w = m.mu.Where
	}
	out := make([]Pred[E], len(w))
	for i, p := range w {
		out[i] = Pred[E](p)
	}
	return out
}

// Affected returns the rows the write inserted, matched or deleted, once
// next has returned, or the count the hook gave SetAffected. It is 0 before
// then, after next returned an error (an insert sets it only when every
// statement succeeded and no upsert was denied), under SQL(ctx), which sends
// nothing, and when a hook skipped next without calling SetAffected. It is
// -1 for a write whose driver could not report its count.
func (m *Mutation[E]) Affected[_ Lazy[E]]() int64 { return m.mu.Affected }

// SetAffected is for a hook that makes an update or a delete itself instead
// of calling next, such as a soft delete (see Mutation): n is the rows its
// own write affected, which the update's or delete's Exec returns. A Save's
// Exec returns no count and stores nothing in its entity, as velox wrote
// nothing. An n of -1 is a count the driver did not report, as the hook's
// own Exec returns it, and is passed on. Calling it and next both, in either
// order, with a negative n other than -1, or on an insert, whose count is its
// rows, is a misuse.
func (m *Mutation[E]) SetAffected[_ Lazy[E]](n int64) {
	m.mu.SetAffected(n)
}

// Set adds assignments to an update; each replaces the statement's
// assignment of the same column, if there is one. Set on a delete or an
// insert is an error.
func (m *Mutation[E]) Set[_ Lazy[E]](ss ...Setter[E]) {
	if m.mu.St == nil {
		m.misuse()
		return
	}
	for _, s := range ss {
		engine.MutateSet(&m.mu, s.a)
	}
}

// Where narrows the rows the statement changes: all of the conditions must
// hold, with the statement's own. Where on an insert is an error.
func (m *Mutation[E]) Where[_ Lazy[E]](ps ...Pred[E]) {
	if m.mu.St == nil {
		m.misuse()
		return
	}
	for _, p := range ps {
		engine.MutateWhere(m.mu.St, &m.mu.Err, Predicate(p))
	}
}

// misuse records the error of ValueIn, Set or Where on an insert, keeping
// the first.
func (m *Mutation[E]) misuse[_ Lazy[E]]() { m.mu.Record(engine.ErrInsertMutation) }

// valueIn is a handle's ValueIn: the value m's update assigns to column col
// with AssignSet, as a V. On an insert it records the misuse; a value of
// another type (an untyped UpdateDefault, a Rewrite hook's) it records as
// one too, and reports no value.
func valueIn[E, V any](m *Mutation[E], col int32) (V, bool) {
	var zero V
	if m.mu.St == nil {
		m.misuse()
		return zero, false
	}
	a, ok := engine.Assigned(m.mu.St, int(col))
	if !ok {
		return zero, false
	}
	v, ok := a.(V)
	if !ok {
		m.mu.WrongType(int(col), a, (*V)(nil))
		return zero, false
	}
	return v, true
}

// nullValueIn is a nullable handle's ValueIn: nil for SetNull, and else a
// pointer to a copy of the value, which Set gives as a V and Save as a *V.
// Any other value it records as valueIn does.
func nullValueIn[E, V any](m *Mutation[E], col int32) (*V, bool) {
	if m.mu.St == nil {
		m.misuse()
		return nil, false
	}
	a, ok := engine.Assigned(m.mu.St, int(col))
	if !ok {
		return nil, false
	}
	switch v := a.(type) {
	case nil:
		return nil, true
	case V:
		return &v, true
	case *V:
		if v == nil {
			return nil, true
		}
		c := *v
		return &c, true
	}
	m.mu.WrongType(int(col), a, (*V)(nil))
	return nil, false
}
