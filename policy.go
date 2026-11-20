package velox

import (
	"iter"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// Policy is an entity's row-level policy. A generated entity's is declared
// in the schema package, on the entity's schema or on a mixin it lists (see
// package schema), and velox-gen binds it into the generated metadata. It
// names columns by name (FieldEQ), so the schema package never imports the
// generated one, and a mixin's Policy serves every entity that lists it:
//
//	func (Issue) Policy() velox.Policy {
//		return velox.Policy{
//			Write: func(ctx context.Context) (velox.Predicate, error) {
//				u, ok := UserFrom(ctx)
//				if !ok {
//					return velox.Predicate{}, velox.Deny("not logged in")
//				}
//				return velox.FieldEQ("author_id", u), nil
//			},
//		}
//	}
//
// An entity written by hand declares it with a method on its pointer type,
// func (*E) Policy() velox.Policy, and names itself in its Descriptor,
// Schema: (*E)(nil); a method the metadata does not reach is refused at the
// entity's first use, as is a Policy method of another signature. velox calls
// the method once, when it builds the metadata: a hand-written one on a nil
// *E, so it must not read its receiver.
//
// Any field may be nil. Read and Write return Allow() for no restriction;
// their zero Predicate fails the statement, naming the entity, so that a code
// path that forgot to return a condition never lets a caller through. Any
// error they return denies the statement.
//
// A policy function decides from ctx and, for Insert, the row; it does not
// query. A rule that must see other rows, such as that an inserted comment's
// parent is one the caller may read, is a hook (Hook): it queries through
// Mutation.DB, on the write's DB, and returns the error that refuses the
// write. This is ent's split: privacy rules decide, hooks query.
//
// Read is ANDed into every SELECT, Count, With load and Has/HasWith
// subquery on the entity, into every UPDATE, DELETE and Save, and into the
// update branch of an upsert, on the existing row. Write is ANDed into
// every UPDATE, DELETE and Save, and into an upsert's update branch, with
// Read. It decides which existing rows may change; it does not check the new
// values (there is no WITH CHECK), so a writer may set a column the policy
// reads, such as an owner. Only an Immutable column cannot be changed
// (ErrImmutable). A row Write hides is left alone, and that is not an
// error: an Update or Delete that matches only such rows affects 0 rows, as
// with SQL row-level security.
//
// Insert checks every inserted or upserted row, after the entity's hooks,
// the defaults and the validators, so it sees what they set. It reads the
// row with Row.Value. A Row.Set in it is a misuse, which fails the insert: a
// policy decides, and does not change the row.
//
// Query and Mutation hold veloxe's privacy rules, for a schema written for
// veloxe; package privacy has their types and says how they apply. Query's
// conditions apply to reads only, after Read's; Mutation's rules judge
// inserted rows, after Insert, and narrow updates and deletes, after Write.
type Policy = engine.Policy

// Row is a row of an insert, by its columns' names: what a hook ranges over
// in Mutation.Rows, and what Policy.Insert checks. It is a value, cheap to
// copy, valid while the write runs. Value reads a column (nil for NULL; a
// nullable column's value, not its pointer), Set sets one, converting the
// value as a Predicate's is, IsZero reports a Go zero value, and Entity returns
// the row as its *E, for application code that can name E. A wrong name, a
// value of another kind, or a Set in Policy.Insert is a misuse, which fails
// the write.
type Row = engine.Row

// Mutation is the write that an entity's hooks see and may change, by
// column name. A misuse (a name the entity has no column of, a value of
// another kind, Set on a delete, Value or Where on an insert, SetAffected
// on an insert or with next) is recorded and fails the write: before next
// sends it, or once the hook returns.
//
// A hook may make the write itself instead of calling next. A soft delete
// is a delete hook that updates the rows the delete would remove, and
// reports the update's count with SetAffected, which the delete's Exec then
// returns:
//
//	t := untyped.MutationTable(m)
//	col, _ := t.Info().Column("deleted_at")
//	set, err := t.Assign(col, now)
//	...
//	u := untyped.Update(m.DB(), t).Assign(set)
//	if cs := m.Predicates(); len(cs) > 0 {
//		u = u.Where(cs...)
//	} else {
//		u = u.All()
//	}
//	n, err := u.Exec(ctx)
//	m.SetAffected(n)
//	return err
//
// The update adds the policy's conditions itself. Under SQL(ctx) such a
// delete fails with ErrDryRun: the hook's update is a write, and there is
// no DELETE to render.
type Mutation struct{ mu engine.MutationState }

// Mutation is the size of its engine.MutationState, which a *Mutation is cast
// from (erase in hook.go).
var _ [unsafe.Sizeof(Mutation{}) - unsafe.Sizeof(engine.MutationState{})]struct{} = [0]struct{}{}

// Op returns the write's kind: OpInsert, OpUpdate or OpDelete.
func (m *Mutation) Op() Op {
	if m.mu.Op != 0 {
		return m.mu.Op
	}
	return m.mu.St.Op
}

// Rows returns an insert's rows, which a hook may change before next;
// none for an update or delete. Insert and InsertAll hand the hooks all
// their rows.
func (m *Mutation) Rows() iter.Seq[Row] { return m.mu.RowsByName() }

// Set sets the column of the given name: in every row of an insert, or in
// an update's assignment, which it replaces. v is converted as a
// Predicate's value is, and nil sets NULL on a nullable column. On a delete it is a
// misuse. A misuse is recorded, and returned.
func (m *Mutation) Set(name string, v any) error { return m.mu.SetByName(name, v) }

// Fill sets the column of the given name to v in each row of an insert
// that leaves it at its Go zero value (nil, for a nullable column), as Set
// sets it. On an update or a delete it is a misuse. It is how a mixin fills
// a column the caller may also set, such as a tenant's. An insert fills the
// columns' Defaults before the hooks run, so Fill of a column that has one
// finds it set; Set replaces it.
func (m *Mutation) Fill(name string, v any) error { return m.mu.FillByName(name, v) }

// Value returns the value an update assigns to the column of the given
// name with Set: nil for NULL, and for a nullable column the value, not a
// pointer to it. Once next has run it also sees the UpdateDefault
// assignments. It reports false when the update does not assign the
// column, or assigns it with Add, Sub or SetExpr, and on a delete. On an insert it
// is a misuse: read an insert's values from Rows. A slice or map it returns
// is shared with the statement, and often with the caller: change the
// assignment with Set, not in place.
func (m *Mutation) Value(name string) (any, bool) { return m.mu.ValueByName(name) }

// Where narrows the rows an update or a delete changes: all of ps must
// hold, with the statement's own conditions. Where on an insert is a
// misuse.
func (m *Mutation) Where(ps ...Predicate) {
	if m.mu.St == nil {
		m.mu.Record(engine.ErrInsertMutation)
		return
	}
	for _, p := range ps {
		engine.MutateWhere(m.mu.St, &m.mu.Err, p)
	}
}

// Predicates returns the conditions of an update or a delete as the caller
// and the hooks gave them: the caller's Where and those the hooks before
// this one added, in order, as a copy (changing the slice changes nothing).
// Once the innermost hook has called next, it returns them as they were
// then, so an outer hook whose next has returned also sees the conditions
// the hooks inside it added. It never holds the policy's or a Rewrite
// hook's conditions, which next adds. It is empty only when the statement
// said All, and nil for an insert. Mutation shows a soft delete built on
// it.
func (m *Mutation) Predicates() []Predicate {
	if m.mu.St == nil {
		return nil
	}
	w := m.mu.St.Where
	if m.mu.Called {
		w = m.mu.Where
	}
	return slices.Clone(w)
}

// DB returns the DB the write runs on: the transaction's, inside db.Tx, or
// else the database's. A hook that reads or writes other rows uses it:
//
//	err := m.DB().Insert(&audit).Exec(ctx)
//
// Inside db.Tx, the hook's work commits or rolls back with the write.
// Outside it there is no transaction to share: each statement commits
// alone, so an audit row survives a write that then fails, and a write
// that must be atomic with its hook's work runs in db.Tx.
func (m *Mutation) DB() *DB {
	db, _ := m.mu.DB.(*engine.DB)
	return (*DB)(unsafe.Pointer(db))
}

// Affected returns the rows the write inserted, matched or deleted, once
// next has returned, or the count the hook gave SetAffected. It is 0
// before then, after next returned an error (an insert sets it only when
// every statement succeeded and no upsert was denied), under SQL(ctx),
// which sends nothing, and when a hook skipped next without calling
// SetAffected. It is -1 for a write whose driver could not report its
// count.
func (m *Mutation) Affected() int64 { return m.mu.Affected }

// SetAffected is for a hook that makes an update or a delete itself instead
// of calling next, such as a soft delete (see Mutation): n is the rows its
// own write affected, which the update's or delete's Exec returns. A Save's
// Exec returns no count and stores nothing in its entity, as velox wrote
// nothing. An n of -1 is a count the driver did not report, as the hook's
// own Exec returns it, and is passed on. Calling it and next both, in
// either order, with a negative n other than -1, or on an insert, whose
// count is its rows, is a misuse.
func (m *Mutation) SetAffected(n int64) { m.mu.SetAffected(n) }

// Interceptor filters an entity's rows for the application. Filter's
// condition is ANDed into every statement that reads or changes the rows:
// SELECT, Count, the aggregates, Each and Iter, a With load, an EXISTS on
// the entity (Has, HasWith), a join, UPDATE, DELETE, Save and an upsert's
// update branch. Raw SQL is not filtered.
//
// The interceptors' conditions come before the Read policy's in the WHERE.
// WithoutPolicy does not lift them: an interceptor offers its own way to be
// skipped, such as a context value its Filter reads. A Filter that reads a
// context value records it with WithScopeValue, so that the GraphQL batcher
// keeps callers apart.
//
// Filter returns Allow() for no restriction. Its zero Predicate fails the
// statement, as a policy's does. An error fails the statement and is not a
// denial: errors.Is(err, ErrDenied) holds only when the error is a Deny.
//
// A generated entity's interceptors are declared in the schema package, on
// the entity's schema or a mixin, and bound by velox-gen. An entity written
// by hand declares them with a method on its pointer type,
// func (*E) Interceptors() []velox.Interceptor, as Policy is declared.
// velox calls the method once, when it builds the metadata, under the rules
// of Policy. An Interceptor with a nil Filter is refused then,
// by its index.
//
// A join entity of a many-to-many edge cannot have interceptors, as it
// cannot have a Read policy: it is read through its two sides. On MySQL, an
// upsert that updates an entity with interceptors is refused with
// ErrPolicyUpsert.
type Interceptor = engine.Interceptor
