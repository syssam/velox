package velox

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

// UpdateQuery is an UPDATE of entity E, built by chaining and run by Exec.
// Like Query it is a value: extending it leaves it as it was.
type UpdateQuery[E any] struct {
	db   *DB
	meta *Meta[E]
	st   Statement
	err  error // a condition's error, as in Query
}

// Update starts an UPDATE of entity E:
//
//	db.Update[billing.Invoice]().Set(I.PaidAt.Set(now)).Where(I.ID.EQ(id)).Exec(ctx)
func (db *DB) Update[E any, PE Entity[E]]() UpdateQuery[E] {
	m := PE(nil).VeloxMeta()
	return UpdateQuery[E]{db: db, meta: m, st: Statement{Op: OpUpdate, Entity: m.info, InTx: db.inTx}}
}

// Set adds assignments, made by the handles' Set, SetNull, Add and Sub.
func (u UpdateQuery[E]) Set[_ Lazy[E]](ss ...Setter[E]) UpdateQuery[E] {
	u.st.Set = slices.Clip(u.st.Set)
	for _, s := range ss {
		u.st.Set = append(u.st.Set, s.a)
	}
	return u
}

// Where adds conditions; all of them must hold.
func (u UpdateQuery[E]) Where[_ Lazy[E]](ps ...Pred[E]) UpdateQuery[E] {
	u.st.Where = slices.Clip(u.st.Where)
	for _, p := range ps {
		u.st.Where = append(u.st.Where, Predicate(p))
		if u.err == nil {
			if err := predErr(p.n); err != nil {
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
	if u.meta.err != nil {
		return Statement{}, u.meta.err
	}
	if u.err != nil {
		return Statement{}, u.err
	}
	if len(u.st.Set) == 0 {
		return Statement{}, fmt.Errorf("velox: %s: Update has no Set", u.meta.info.Name)
	}
	return cloneStatement(&u.st), nil
}

// Exec runs the update and returns the number of rows it matched (on MySQL,
// with clientFoundRows=true; without it, the rows whose values changed).
//
// The guard runs first, on the statement as built, then the entity's hooks
// (Hooks). Then Exec adds an assignment for every column with an
// UpdateDefault that the statement does not assign, refuses a column
// assigned twice or an Immutable one (ErrImmutable), and runs the
// validators, before the statement goes through the pipeline.
func (u UpdateQuery[E]) Exec[_ Lazy[E]](ctx context.Context) (int64, error) {
	st, err := u.statement()
	if err != nil {
		return 0, err
	}
	n, _, _, err := write(ctx, u.db, &st, false, false)
	return n, err
}

// SQL returns the statement and arguments Exec would send for ctx, or the
// error Exec would fail with before sending it; it sends nothing. It runs
// every stage Exec runs before execution: the guard, the entity's hooks, the
// UpdateDefault assignments and the checks, the guard again, the
// Rewrite hooks, the tenant and policy scope and
// rendering. So the hooks and the policy run, and can tell from
// IsDryRun(ctx), or a Rewrite hook from Statement.DryRun, that the statement
// will not be sent. Around hooks, which wrap execution, do not run. A write a
// hook makes under ctx, an audit row through Mutation.DB say, is refused with
// ErrDryRun: a hook that returns that error after next leaves SQL(ctx) its
// statement; one that returns it before next, SQL(ctx) fails with it.
func (u UpdateQuery[E]) SQL[_ Lazy[E]](ctx context.Context) (string, []any, error) {
	ctx = dryRunContext(ctx) // first, so that every hook sees it
	st, err := u.statement()
	if err != nil {
		return "", nil, err
	}
	_, q, args, err := write(ctx, u.db, &st, true, false)
	return q, args, err
}

// DeleteQuery is a DELETE of entity E, built by chaining and run by Exec.
type DeleteQuery[E any] struct {
	db   *DB
	meta *Meta[E]
	st   Statement
	err  error // a condition's error, as in Query
}

// Delete starts a DELETE of entity E.
func (db *DB) Delete[E any, PE Entity[E]]() DeleteQuery[E] {
	m := PE(nil).VeloxMeta()
	return DeleteQuery[E]{db: db, meta: m, st: Statement{Op: OpDelete, Entity: m.info, InTx: db.inTx}}
}

// Where adds conditions; all of them must hold.
func (d DeleteQuery[E]) Where[_ Lazy[E]](ps ...Pred[E]) DeleteQuery[E] {
	d.st.Where = slices.Clip(d.st.Where)
	for _, p := range ps {
		d.st.Where = append(d.st.Where, Predicate(p))
		if d.err == nil {
			if err := predErr(p.n); err != nil {
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

// Exec runs the delete and returns the number of rows it removed.
func (d DeleteQuery[E]) Exec[_ Lazy[E]](ctx context.Context) (int64, error) {
	if d.meta.err != nil {
		return 0, d.meta.err
	}
	if d.err != nil {
		return 0, d.err
	}
	st := cloneStatement(&d.st)
	n, _, _, err := write(ctx, d.db, &st, false, false)
	return n, err
}

// SQL returns the statement and arguments Exec would send for ctx, or the
// error Exec would fail with before sending it; it sends nothing. It runs
// every stage Exec runs before execution: the guard, the entity's hooks, the
// guard again, the Rewrite hooks, the tenant and
// policy scope and rendering. So the hooks and the policy run, and can tell
// from IsDryRun(ctx), or a Rewrite hook from Statement.DryRun, that the
// statement will not be sent. Around hooks, which wrap execution, do not run.
// A write a hook makes under ctx is refused with ErrDryRun, as in
// UpdateQuery.SQL.
func (d DeleteQuery[E]) SQL[_ Lazy[E]](ctx context.Context) (string, []any, error) {
	ctx = dryRunContext(ctx) // first, so that every hook sees it
	if d.meta.err != nil {
		return "", nil, d.meta.err
	}
	if d.err != nil {
		return "", nil, d.err
	}
	st := cloneStatement(&d.st)
	_, q, args, err := write(ctx, d.db, &st, true, false)
	return q, args, err
}

// SaveQuery is a Save of one entity, built by chaining and run by Exec. Like
// Query it is a value: extending it leaves it as it was. It holds x, not its
// values: *x is read when Exec or SQL runs, not when Save is called, and
// after Exec it holds what was written, the UpdateDefault values included.
type SaveQuery[E any] struct {
	db    *DB
	meta  *Meta[E]
	x     *E
	cols  []Column[E]
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
	return SaveQuery[E]{db: db, meta: x.VeloxMeta(), x: (*E)(x), cols: cols} // VeloxMeta works on a nil x
}

// Where adds conditions to the key's: the row is written only if all of them
// hold, and Exec fails with ErrNotFound when it is not. The conditions are
// part of the statement the hooks and the policy see. It makes a write
// conditional on what the caller read:
//
//	err := db.Save(&u, U.Email, U.Version).Where(U.Version.EQ(v)).Exec(ctx)
//
// fails with ErrNotFound when another writer moved the version.
func (s SaveQuery[E]) Where[_ Lazy[E]](ps ...Pred[E]) SaveQuery[E] {
	s.where = append(slices.Clip(s.where), ps...)
	return s
}

// statement is the UPDATE Exec and SQL give the hooks: the named columns of
// *x, where the key and the conditions hold.
func (s SaveQuery[E]) statement[_ Lazy[E]]() (Statement, error) {
	if s.meta.err != nil {
		return Statement{}, s.meta.err
	}
	in := s.meta.info
	if s.x == nil {
		return Statement{}, fmt.Errorf("velox: %s: Save of a nil pointer", in.Name)
	}
	st := Statement{Op: OpUpdate, Entity: in, InTx: s.db.inTx}
	for _, c := range s.cols {
		if c == nil {
			return Statement{}, fmt.Errorf("velox: %s: Save names a nil column", in.Name)
		}
		i := int(c.column(nil))
		st.Set = append(st.Set, Assignment{Column: i, Op: AssignSet, Value: fieldValue(s.meta, s.x, i)})
	}
	for _, k := range in.PrimaryKey {
		st.Where = append(st.Where, ColumnEQ(k, fieldValue(s.meta, s.x, k)))
	}
	for _, p := range s.where {
		if err := predErr(p.n); err != nil {
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
// calling next.
// Its error wraps ErrNotFound when no row has that key and meets the
// conditions. On MySQL, open the database with clientFoundRows=true:
// otherwise a row whose values did not change counts as not found.
func (s SaveQuery[E]) Exec[_ Lazy[E]](ctx context.Context) error {
	st, err := s.statement()
	if err != nil {
		return err
	}
	n, _, _, err := write(ctx, s.db, &st, false, true)
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
	return nil
}

// SQL returns the statement and arguments Exec would send for ctx, or the
// error Exec would fail with before sending it; it sends nothing, and *x is
// not changed. It runs every stage Exec runs before execution, as
// UpdateQuery.SQL does: the guard, the entity's hooks, the UpdateDefault
// assignments and the checks, the Rewrite hooks, the tenant and policy scope
// and rendering. The hooks and the policy can tell from IsDryRun(ctx) that
// the statement will not be sent. Around hooks do not run. A write a hook
// makes under ctx is refused with ErrDryRun, as in UpdateQuery.SQL.
func (s SaveQuery[E]) SQL[_ Lazy[E]](ctx context.Context) (string, []any, error) {
	ctx = dryRunContext(ctx) // first, so that every hook sees it
	st, err := s.statement()
	if err != nil {
		return "", nil, err
	}
	_, q, args, err := write(ctx, s.db, &st, true, true)
	return q, args, err
}

// write runs st, an update or delete (an update that must find its row, with
// save), inside its entity's hooks, or alone when it has none, without
// allocating for them. Under SQL(ctx) (dry) it renders st instead of sending
// it. It returns the rows affected, and what dry rendered.
//
// The guard runs on st as the caller built it before the hooks see it, and
// again inside next, in the pipeline: a condition a hook adds never stands in
// for the caller's Where. Without hooks, the pipeline's guard is the only one.
func write(ctx context.Context, db *DB, st *Statement, dry, save bool) (int64, string, []any, error) {
	if len(st.Entity.hooks) == 0 {
		var err error
		mu := mutation{st: st, dry: dry}
		if save {
			err = saveCore(ctx, db, &mu)
		} else {
			err = writeCore(ctx, db, &mu)
		}
		return mu.res.Affected, mu.sql, mu.args, err
	}
	if err := db.guard(st); err != nil {
		return 0, "", nil, err
	}
	core := func(ctx context.Context, mu *mutation) error { return writeCore(ctx, db, mu) }
	if save {
		core = func(ctx context.Context, mu *mutation) error { return saveCore(ctx, db, mu) }
	}
	mu := &mutation{op: st.Op, st: st, dry: dry}
	if err := mutate(ctx, db, st.Entity, mu, core); err != nil && !dryRefused(mu, err) {
		return 0, "", nil, err
	}
	return mu.res.Affected, mu.sql, mu.args, nil
}

// dryRefused reports whether err, from the hooks of a statement under SQL(ctx),
// is a write a hook made that was refused with ErrDryRun, after the statement
// itself was rendered: SQL(ctx) then returns the statement, as the hook, not
// the dry run, is what asked for a write. A hook that wrote before next, and
// so before anything was rendered, gets the error, and so does one whose
// error joins another to ErrDryRun.
func dryRefused(mu *mutation, err error) bool {
	return mu.dry && mu.sql != "" && onlyDryRun(err)
}

// onlyDryRun reports whether err is ErrDryRun, wrapped or joined, on every
// branch of its tree: errors.Join(ErrDryRun, other) is not.
func onlyDryRun(err error) bool {
	switch x := err.(type) {
	case nil:
		return false
	case interface{ Unwrap() []error }:
		errs := x.Unwrap()
		for _, e := range errs {
			if !onlyDryRun(e) {
				return false
			}
		}
		return len(errs) > 0
	case interface{ Unwrap() error }:
		return err == ErrDryRun || onlyDryRun(x.Unwrap())
	}
	return err == ErrDryRun
}

// writeCore is an update's or delete's work inside the hooks, their next.
// For an update it adds the UpdateDefault assignments, refuses a column
// assigned twice or an Immutable one and runs the validators; then it sends
// the statement through the pipeline, whose guard runs first, or under
// SQL(ctx) renders it into mu.sql and mu.args. It sets mu.res.
func writeCore(ctx context.Context, db *DB, mu *mutation) error {
	st := mu.st
	if st.Op == OpUpdate {
		in := st.Entity
		st.Set = withUpdateDefaults(in, st.Set)
		if err := checkAssignedOnce(in, st.Set); err != nil {
			return err
		}
		if err := validateSet(in, st.Set); err != nil {
			return err
		}
	}
	if mu.dry {
		var err error
		mu.sql, mu.args, err = db.dryRun(ctx, st)
		return err
	}
	res, err := execWrite(ctx, db, st)
	if err != nil {
		return err
	}
	mu.res.Affected = res.affected
	return nil
}

// saveCore is Save's core: writeCore, failing with ErrNotFound when no row
// has the key and meets the conditions. A dry run sends nothing, so it cannot
// find that out.
func saveCore(ctx context.Context, db *DB, mu *mutation) error {
	if err := writeCore(ctx, db, mu); err != nil {
		return err
	}
	if !mu.dry && mu.res.Affected == 0 {
		return fmt.Errorf("velox: %s: %w", mu.st.Entity.Name, ErrNotFound)
	}
	return nil
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
// UpdateDefault that set does not assign.
func withUpdateDefaults(info *EntityInfo, set []Assignment) []Assignment {
	named := make(map[int]bool, len(set))
	for _, a := range set {
		named[a.Column] = true
	}
	out := slices.Clip(set)
	for i := range info.Columns {
		if f := info.Columns[i].UpdateDefault; f != nil && !named[i] {
			out = append(out, Assignment{Column: i, Op: AssignSet, Value: f()})
		}
	}
	return out
}

// fieldValue is the value of column i's field in *p.
func fieldValue[E any](m *Meta[E], p *E, i int) any { return elem(m.addr(p, i)) }

// setField stores v in column i's field in *p, as setElem does.
func setField[E any](m *Meta[E], p *E, i int, v any) { setElem(m.addr(p, i), v) }

// setElem stores v where p points: nil as the zero value, a value of the
// pointed-to type of a pointer field through a new pointer, and nothing when
// the field cannot hold v (a Rewrite hook may assign any value). Like elem it
// is not generic, so that the reflect.Value it uses is not compiled into every
// package that writes.
func setElem(p, v any) {
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
