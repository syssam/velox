package velox

import (
	"context"
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
//			Write: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
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
// A policy function decides from ctx, db and, for Insert, the row. db is
// the DB the statement runs on, never nil: inside Tx it is the
// transaction's, as Mutation.DB is for a hook and the db a Filter gets.
// An insert that velox runs in a transaction of its own, such as a batch
// of several statements or a recording DB's insert, asks Insert inside
// it, with that transaction's DB; a hook around the insert, which runs
// before velox begins it, gets the outer DB. Prefer a condition that reads nothing itself, such as HasEdgeWith, which
// runs inside the statement. A function that must read to decide, such as
// a lookup of an organization's subtree, reads through db with ctx, so it
// gets the same answer inside a transaction and outside:
//
//	Read: func(ctx context.Context, db *velox.DB) (velox.Predicate, error) {
//		ids, err := subtree(ctx, db, OrgFrom(ctx))
//		if err != nil {
//			return velox.Predicate{}, err
//		}
//		return velox.FieldIn("org_id", ids...), nil
//	},
//
// A statement made under a policy function's ctx is not judged by that
// function, so a policy that reads its own entity ends. A rule that
// refuses a write after seeing other rows may also be a hook (Hook), which
// reads through Mutation.DB.
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
// Read, Write and Insert fail closed. Query and Mutation hold rule lists
// that follow ent's privacy; package privacy has their
// types and says how they apply. A list whose rules all skip allows, and
// privacy.DecisionContext lifts them; to deny by default, end a list with
// privacy.AlwaysDenyRule(). Query's conditions apply to reads only, after
// Read's; Mutation's rules judge inserted rows, after Insert, and narrow
// updates and deletes, after Write.
type Policy struct {
	Read     func(ctx context.Context, db *DB) (Predicate, error)
	Write    func(ctx context.Context, db *DB) (Predicate, error)
	Insert   func(ctx context.Context, db *DB, r Row) error
	Query    QueryPolicy
	Mutation MutationPolicy
}

// erasePolicy is p as the engine keeps it: each function given the
// engine's DB, which a *DB is cast from (see DB), and each rule list given
// a query or mutation that names the DB. It is a function, not a method:
// every package with a Policy() describes Policy's methods.
func erasePolicy(p Policy) engine.Policy {
	var e engine.Policy
	if f := p.Read; f != nil {
		e.Read = func(ctx context.Context, db *engine.DB) (Predicate, error) { return f(ctx, dbOf(db)) }
	}
	if f := p.Write; f != nil {
		e.Write = func(ctx context.Context, db *engine.DB) (Predicate, error) { return f(ctx, dbOf(db)) }
	}
	if f := p.Insert; f != nil {
		e.Insert = func(ctx context.Context, db *engine.DB, r Row) error { return f(ctx, dbOf(db), r) }
	}
	if q := p.Query; len(q) > 0 {
		e.Query = func(ctx context.Context, db *engine.DB, f PrivacyFilter) error {
			return q.EvalQuery(ctx, privacyQuery{f, dbOf(db)})
		}
	}
	if m := p.Mutation; len(m) > 0 {
		e.Mutation = func(ctx context.Context, db *engine.DB, op Op, f PrivacyFilter, row *Row) error {
			return m.EvalMutation(ctx, privacyMutation{op, f, row, dbOf(db)})
		}
	}
	return e
}

// dbOf is db as a *DB: DB holds an engine.DB as its only field.
func dbOf(db *engine.DB) *DB { return (*DB)(unsafe.Pointer(db)) }

// Row is a row of an insert, by its columns' names: what a hook ranges over
// in Mutation.Rows, and what Policy.Insert checks. It is a value, cheap to
// copy, valid while the write runs. Value reads a column (nil for NULL; a
// nullable column's value, not its pointer), Set sets one, converting the
// value as a Predicate's is, IsZero reports a Go zero value, and Entity returns
// the row as its *E, for application code that can name E. A wrong name, a
// value of another kind, or a Set in Policy.Insert is a misuse, which fails
// the write.
//
// A column handle reads and writes a row by its type: Posts.Title.Get(r)
// returns the title as a string, and Posts.Title.Put(r, "x") sets it, so a
// value of the wrong type fails to compile. A handle of another entity is a
// misuse.
type Row = engine.Row

// Mutation is the write that an entity's hooks see and may change, by
// column name. A misuse (a name the entity has no column of, a value of
// another kind, Set on a delete, Value or Where on an insert, SetAffected
// on an insert or with next) is recorded and fails the write: before next
// sends it, or once the hook returns.
//
// What a write changed, row by row with old and new values, is reported by a
// recording handle (DB.Record), not by a hook.
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

// Entity returns the name of the write's entity: its Go type's, without
// the package, which for a generated entity is its schema's name ("Invoice"),
// as ent's Mutation.Type gives. WithFilter's filters are given the same name.
func (m *Mutation) Entity() string { return m.mu.EntityName() }

// Rows returns an insert's rows, which a hook may change before next;
// none for an update or delete. Insert and InsertAll hand the hooks all
// their rows.
func (m *Mutation) Rows() iter.Seq[Row] { return m.mu.RowsByName() }

// Set sets the column of the given name: in every row of an insert, or in
// an update's assignment, which it replaces. v is converted as a
// Predicate's value is, and nil sets NULL on a nullable column. On a delete it is a
// misuse. A misuse is recorded, and returned. A column handle's Assign is
// Set by type: Posts.Title.Assign(m, v).
func (m *Mutation) Set(name string, v any) error { return m.mu.SetByName(name, v) }

// Fill sets the column of the given name to v in each row of an insert
// that leaves it at its Go zero value (nil, for a nullable column), as Set
// sets it. On an update or a delete it is a misuse. It is how a mixin fills
// a column the caller may also set, such as a tenant's. An insert fills the
// columns' Defaults before the hooks run, so Fill of a column that has one
// finds it set; Set replaces it. A column handle's Fill is Fill by type.
func (m *Mutation) Fill(name string, v any) error { return m.mu.FillByName(name, v) }

// Value returns the value an update assigns to the column of the given
// name with Set: nil for NULL, and for a nullable column the value, not a
// pointer to it. Once next has run it also sees the UpdateDefault
// assignments. It reports false when the update does not assign the
// column, or assigns it with Add, Sub or SetExpr, and on a delete. On an insert it
// is a misuse: read an insert's values from Rows. A slice or map it returns
// is shared with the statement, and often with the caller: change the
// assignment with Set, not in place. A column handle's Value is Value by
// type: Posts.Title.Value(m) returns a string.
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

// Columns returns the names of the columns the write assigns, in the
// entity's order, as ent's Mutation.Fields does: an update's assignments,
// Set, SetNull, Add, Sub and SetExpr alike, the UpdateDefault ones once next
// has run; an insert's columns that some row holds a value other than the
// Go zero value in, its Defaults included, as an insert counts a zero field
// as unset. A delete assigns none. A Set edge setter's foreign key is an
// assigned column.
func (m *Mutation) Columns() []string { return m.mu.ColumnNames() }

// Cleared reports whether an update sets the column of the given name to
// NULL, as ent's FieldCleared does: SetNull, Set of nil, or a Save of a nil
// field. It is false on an insert and a delete. A name the entity has no
// column of is a misuse.
func (m *Mutation) Cleared(name string) bool { return m.mu.ClearedByName(name) }

// Edges returns the write's edge setters, a relation handle's Add, Remove,
// Clear and Set, in the order given, as ent's AddedEdges, RemovedEdges and
// ClearedEdges tell them. The rows they change are written after next, so a
// hook sees what will change, not what did. An update whose setters are all
// edge setters sends no UPDATE and runs no hook; a Set edge setter is an
// assignment of its foreign key there, as Columns reports.
func (m *Mutation) Edges() []EdgeChange { return m.mu.EdgeChanges() }

// EdgeChange is one edge setter of a write, which Mutation.Edges returns.
// Its fields:
//
//	Name string // the edge's name, as the schema gives it
//	Op   EdgeOp // what the setter does
//	Keys []any  // its keys; none for EdgeClear
type EdgeChange = engine.EdgeChange

// EdgeOp is what an edge setter does.
type EdgeOp = engine.EdgeOp

// The edge setters' operations: a relation handle's Add, Remove, Clear and
// Set.
const (
	EdgeAdd    = engine.EdgeAdd    // link the keys
	EdgeRemove = engine.EdgeRemove // unlink the keys
	EdgeClear  = engine.EdgeClear  // unlink every row
	EdgeSet    = engine.EdgeSetKey // set the owner's foreign key to the one key
)

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
// Filter is given the statement's ctx and db, the DB the statement runs
// on, never nil: inside Tx the transaction's, as a Policy function's db
// is. One that reads, reads through db. A statement made under its ctx is
// not narrowed by it. WithoutPolicy does not lift an interceptor, so db
// has no WithoutPolicy reason: a read through it is judged by the
// policies, even under a statement that skips them.
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
type Interceptor struct {
	Filter func(ctx context.Context, db *DB) (Predicate, error)
}

// eraseFilters is the Filters of is as the engine runs them, given the
// engine's DB, which a *DB is cast from (see DB). A nil Filter stays nil,
// for FiltersFor to refuse by its index.
func eraseFilters(is []Interceptor) []func(context.Context, *engine.DB) (Predicate, error) {
	fs := make([]func(context.Context, *engine.DB) (Predicate, error), len(is))
	for i, x := range is {
		if f := x.Filter; f != nil {
			fs[i] = func(ctx context.Context, db *engine.DB) (Predicate, error) { return f(ctx, dbOf(db)) }
		}
	}
	return fs
}

// mutValue is a handle's Value: the value the update m assigns to column
// col of entity E, with Mutation.Value's rules; the zero V for NULL.
func mutValue[E, V any](m *Mutation, col int32) (V, bool) {
	var zero V
	in, err := ownerOf[E]()
	if err != nil {
		m.mu.Record(err)
		return zero, false
	}
	v, ok := m.mu.ValueAt("Value", in, col)
	if !ok || v == nil {
		return zero, ok
	}
	x, ok := v.(V)
	return x, ok
}

// mutValuePtr is a Null handle's ValuePtr: mutValue as a fresh *V, nil for
// NULL.
func mutValuePtr[E, V any](m *Mutation, col int32) (*V, bool) {
	in, err := ownerOf[E]()
	if err != nil {
		m.mu.Record(err)
		return nil, false
	}
	v, ok := m.mu.ValueAt("ValuePtr", in, col)
	if !ok || v == nil {
		return nil, ok
	}
	x, ok := v.(V)
	if !ok {
		return nil, false
	}
	return &x, true
}

// mutSet is a handle's Assign (and a Null handle's AssignNull, with a nil
// v): Mutation.Set of column col of entity E.
func mutSet[E any](what string, m *Mutation, col int32, v any) error {
	in, err := ownerOf[E]()
	if err != nil {
		m.mu.Record(err)
		return err
	}
	return m.mu.SetAt(what, in, col, v)
}

// mutFill is a handle's Fill: Mutation.Fill of column col of entity E.
func mutFill[E any](m *Mutation, col int32, v any) error {
	in, err := ownerOf[E]()
	if err != nil {
		m.mu.Record(err)
		return err
	}
	return m.mu.FillAt("Fill", in, col, v)
}

// rowValue is a handle's Get: Row.Value of column col of entity E, as a V;
// the zero V for NULL.
func rowValue[E, V any](r Row, col int32) V {
	var zero V
	in, err := ownerOf[E]()
	if err != nil {
		return zero
	}
	x, _ := engine.RowValueAt(r, "Get", in, col).(V)
	return x
}

// rowPtr is a Null handle's GetPtr: rowValue as a fresh *V, nil for NULL.
func rowPtr[E, V any](r Row, col int32) *V {
	in, err := ownerOf[E]()
	if err != nil {
		return nil
	}
	x, ok := engine.RowValueAt(r, "GetPtr", in, col).(V)
	if !ok {
		return nil
	}
	return &x
}

// rowSet is a handle's Put (and a Null handle's PutNull, with a nil v):
// Row.Set of column col of entity E.
func rowSet[E any](what string, r Row, col int32, v any) error {
	in, err := ownerOf[E]()
	if err != nil {
		return err
	}
	return engine.RowSetAt(r, what, in, col, v)
}
