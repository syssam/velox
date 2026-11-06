package velox

import (
	"context"
	"fmt"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// InsertQuery is an INSERT of one entity or of many, built by chaining and
// run by Exec. Like Query it is a value: extending it leaves it as it was.
type InsertQuery[E any] struct {
	db       *DB
	meta     *Meta[E]
	one      *E
	single   bool // made by Insert, so a nil one is a mistake, not an empty InsertAll
	many     []E
	conflict *ConflictClause
	set      []Assignment      // Set's column setters
	edges    []*engine.EdgeSet // Set's edge setters
}

// Insert starts an INSERT of the entity x points to:
//
//	db.Insert(&inv).Exec(ctx)
//
// Exec changes *x: the entity's hooks (Hooks) may change it, Exec fills
// defaults and reads the generated key back, and it leaves those changes in place even when the
// insert then fails.
func (db *DB) Insert[E any, PE Entity[E]](x PE) InsertQuery[E] {
	return InsertQuery[E]{db: db, meta: x.VeloxMeta(), one: (*E)(x), single: true}
}

// InsertAll starts an INSERT of every entity in xs, in as few statements as
// the dialect's parameter limit allows. The entity's hooks (Hooks) run once,
// over all of xs. Exec fills defaults into xs[i], the caller's
// own slice elements, and leaves those changes in place even when the insert then fails.
func (db *DB) InsertAll[E any, PE Entity[E]](xs []E) InsertQuery[E] {
	return InsertQuery[E]{db: db, meta: PE(nil).VeloxMeta(), many: xs}
}

func columnIndexes[E any](cs []Column[E]) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = int(c.column(nil))
	}
	return out
}

// OnConflict names the columns of the unique key whose conflict the insert
// resolves; follow it with Update or DoNothing. Without either, the insert
// keeps the existing row (DO NOTHING). It replaces only the key columns: an
// Update or DoNothing already chained stays. On MySQL, ON DUPLICATE KEY UPDATE
// resolves a conflict on any unique key.
//
// An upsert's update branch writes only the columns Update names: an
// UpdateDefault, such as an updated_at column's, does not fire, as in ent.
func (q InsertQuery[E]) OnConflict[_ Lazy[E]](cols ...Column[E]) InsertQuery[E] {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Columns = columnIndexes(cols)
	q.conflict = &cf
	return q
}

// Update overwrites the named columns of the existing row with the inserted
// values when the insert conflicts.
func (q InsertQuery[E]) Update[_ Lazy[E]](cols ...Column[E]) InsertQuery[E] {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Update, cf.DoNothing = columnIndexes(cols), false
	q.conflict = &cf
	return q
}

// DoNothing keeps the existing row when the insert conflicts.
func (q InsertQuery[E]) DoNothing[_ Lazy[E]]() InsertQuery[E] {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Update, cf.DoNothing = nil, true
	q.conflict = &cf
	return q
}

// Set adds setters. A column setter, a column handle's Set or SetNull,
// stores its value in each entity before the insert, so it wins over the
// field; Add and Sub fail the insert. An edge setter, a relation handle's
// Add, Remove or Clear, is written after the insert, with each entity's key:
//
//	err := db.Insert(&post).Set(gen.Posts.Tags.Add(1, 2)).Exec(ctx)
//
// Then the insert and its edges run in one transaction, a savepoint inside
// one, and an edge's error undoes the insert. For each entity, an edge is
// cleared, then its keys to remove are unlinked, then those to add linked.
// Its rows are written through the untyped Table API, so the target's and
// the join entity's policies and hooks apply:
//
//   - A many-to-many edge's Add fails with an *EdgeError of ErrNotFound
//     unless every key names a row of the target the caller may read, and
//     inserts the join rows ON CONFLICT DO NOTHING. Remove and Clear delete
//     only the links to targets the caller may read; Clear reads them
//     EdgePage at a time.
//   - An edge whose foreign key the target holds, which must be nullable,
//     re-points on Add the children whose key is NULL: one another owner
//     holds fails with an *EdgeError of ErrLinked when the caller may read
//     it, and one the caller may not change, or read, with ErrNotFound.
//     Each link is checked by a read after the UPDATE. Remove sets NULL on
//     the given children the owner holds, and Clear on all of them.
//   - A one-to-one edge's Add, of one key, first lets go of the child the
//     owner holds; one the caller may not change fails with ErrLinked, and
//     one whose key is a Required edge's fails its validator.
//   - Add of an edge whose foreign key the target holds fails for more than
//     one owner at once, an InsertAll of several entities: a child has one
//     owner.
//
// Edge setters fail an upsert, whose row may be one it did not insert, and
// an InsertAll whose keys the database assigns, as it reads none back. An
// edge is written only when the insert affected a row: a hook that does not
// call next writes none.
func (q InsertQuery[E]) Set[_ Lazy[E]](ss ...Setter[E]) InsertQuery[E] {
	q.set, q.edges = slices.Clip(q.set), slices.Clip(q.edges)
	for _, s := range ss {
		if e := engine.EdgeOf(s.a); e != nil {
			q.edges = append(q.edges, e)
		} else {
			q.set = append(q.set, s.a)
		}
	}
	return q
}

// Exec runs the insert.
//
// The entity's hooks (Hooks) run first, around all that follows; their next
// does the rest. An InsertAll of nothing runs nothing.
//
// Exec then fills, in each entity, every zero field whose column has Default or
// DefaultFunc (an UpdateDefault fills on update only). Then the
// validators run and the policy's Insert is asked, which is where a scope
// refuses a row of another scope (scope.ErrMismatch); every entity is
// prepared before anything is sent.
//
// An upsert's update branch gets the
// policy's Read and Write conditions, on the existing row; an upsert that
// changed fewer rows than it sent, because the conditions hid a conflicting
// row, fails with a *PolicyError. MySQL cannot apply them, so there such an upsert of an entity
// with a Read or Write policy fails with ErrPolicyUpsert. DoNothing is exempt
// from both.
//
// An integer primary key left at zero is not sent: the database assigns it.
// Insert reads that key back into *x: with RETURNING on PostgreSQL and
// SQLite, from LastInsertId on MySQL for an insert without OnConflict. After a conflict that kept the existing
// row, the key stays zero. A LastInsertId that does not fit the key's type is
// an error.
//
// InsertAll reads nothing back. Give every entity its key, or leave every key
// at zero for the database to assign. An InsertAll that needs several
// statements is atomic: outside a transaction it runs them in one, and inside
// one it runs them under a savepoint. So is an upsert of several rows whose
// update branch gets the policy's conditions, so that the error for a hidden
// row also undoes the rows written with it. An InsertAll of no entities sends
// nothing and returns nil under any context, a dry run's or another
// transaction's included: its hooks do not run.
func (q InsertQuery[E]) Exec[_ Lazy[E]](ctx context.Context) error {
	if err := q.check(); err != nil {
		return err
	}
	if q.one == nil && len(q.many) == 0 {
		return nil
	}
	q.apply()
	if len(q.edges) == 0 {
		_, err := q.run(ctx, &q.db.e)
		return err
	}
	plan, err := q.plan()
	if err != nil {
		return err
	}
	return engine.InsertEdges(ctx, &q.db.e, plan, q.mutation(false).Rows, q.run)
}

// run runs the insert on db, through the entity's hooks, and returns the
// rows it affected.
func (q InsertQuery[E]) run[_ Lazy[E]](ctx context.Context, db *engine.DB) (int64, error) {
	if db.HasHooks(q.meta.info) {
		mu := q.mutation(false)
		err := engine.Mutate(ctx, db, q.meta.info, mu, func(ctx context.Context, mu *engine.MutationState) error { return q.send(ctx, db, mu) })
		return mu.Affected, err
	}
	var mu engine.MutationState
	err := q.send(ctx, db, &mu)
	return mu.Affected, err
}

// apply stores the column setters' values in each entity.
func (q InsertQuery[E]) apply[_ Lazy[E]]() {
	if len(q.set) == 0 {
		return
	}
	for _, p := range q.mutation(false).Rows {
		for _, a := range q.set {
			setField(q.meta, (*E)(p), a.Column, a.Value)
		}
	}
}

// plan is the edge setters, resolved and checked.
func (q InsertQuery[E]) plan[_ Lazy[E]]() (engine.EdgePlan, error) {
	gen := false
	for i := range q.many {
		gen = gen || generatedKey(q.meta, &q.many[i]) >= 0
	}
	if err := engine.CheckEdgeInsert(q.meta.info, q.conflict != nil, q.one == nil, gen); err != nil {
		return engine.EdgePlan{}, err
	}
	return engine.PlanEdges(q.meta.info, q.edges)
}

// SQL returns the first statement and arguments Exec would send for ctx, or
// the error Exec would fail with before sending it; it sends nothing, and
// returns "" for an InsertAll of nothing. It runs every stage Exec runs
// before execution: like Exec, it runs the entity's hooks, fills defaults
// into the caller's structs (the *E or the []E you passed, which
// it changes although it sends nothing; the generated key is the one field it
// leaves), then runs the validators, asks the policy's Insert and runs the
// Rewrite hooks, the scope and rendering. So the
// hooks and the policy run, and can tell from IsDryRun(ctx), or a Rewrite
// hook from Statement.DryRun, that the statement will not be sent. Around
// hooks, which wrap execution, do not run. A write a hook makes under ctx is
// refused with ErrDryRun, as in UpdateQuery.SQL. An InsertAll too large for one
// statement gives the first. InTx is set when Exec sends the statement in a
// transaction: inside one, for an InsertAll too large for one statement, for
// one whose update branch gets the policy's conditions, and for an Insert
// that sets the key of a bidirectional one-to-one edge (EdgeBidi), whose
// partner Exec then links. Set's column setters are stored in the entities,
// as Exec stores them. An insert with edge setters renders the INSERT alone:
// the edges' statements depend on what it inserts, and are not rendered;
// the setters are checked as Exec checks them.
func (q InsertQuery[E]) SQL[_ Lazy[E]](ctx context.Context) (string, []any, error) {
	ctx = engine.DryRunContext(ctx) // first, so that every hook sees it
	if err := q.check(); err != nil {
		return "", nil, err
	}
	if q.one == nil && len(q.many) == 0 {
		return "", nil, nil
	}
	q.apply()
	if len(q.edges) > 0 {
		if _, err := q.plan(); err != nil {
			return "", nil, err
		}
	}
	if q.db.e.HasHooks(q.meta.info) {
		mu := q.mutation(true)
		if err := engine.Mutate(ctx, &q.db.e, q.meta.info, mu, func(ctx context.Context, mu *engine.MutationState) error { return q.render(ctx, mu) }); err != nil && !engine.DryRefused(mu, err) {
			return "", nil, err
		}
		return mu.SQL, mu.Args, nil
	}
	mu := engine.MutationState{Dry: true}
	if err := q.render(ctx, &mu); err != nil {
		return "", nil, err
	}
	return mu.SQL, mu.Args, nil
}

// mutation is the insert as its entity's hooks see it: its rows.
func (q InsertQuery[E]) mutation[_ Lazy[E]](dry bool) *engine.MutationState {
	mu := &engine.MutationState{Op: OpInsert, Dry: dry}
	if q.one != nil {
		mu.Rows = []unsafe.Pointer{unsafe.Pointer(q.one)}
		return mu
	}
	mu.Rows = make([]unsafe.Pointer, len(q.many))
	for i := range q.many {
		mu.Rows[i] = unsafe.Pointer(&q.many[i])
	}
	return mu
}

// send is Exec's work inside the hooks, their next: it prepares every row
// (defaults, the validators and the
// policy's Insert) and sends the statements. It sets mu.affected only when
// they all succeeded, so that Mutation.Affected is 0 after an error whatever
// order insertOne sets Affected and checks an upsert in.
func (q InsertQuery[E]) send[_ Lazy[E]](ctx context.Context, db *engine.DB, mu *engine.MutationState) error {
	var res Result
	var err error
	if q.one != nil {
		err = insertOne(ctx, db, q.meta, q.one, q.conflict, &res)
	} else {
		err = insertMany(ctx, db, q.meta, q.many, q.conflict, &res)
	}
	if err == nil {
		mu.Affected = res.Affected
	}
	return err
}

// render is SQL's work inside the hooks: it prepares the rows as send does,
// and renders the first statement into mu.sql and mu.args.
func (q InsertQuery[E]) render[_ Lazy[E]](ctx context.Context, mu *engine.MutationState) error {
	var st Statement
	if q.one != nil {
		var err error
		if st, _, err = oneStatement(ctx, &q.db.e, q.meta, q.one, q.conflict); err != nil {
			return err
		}
	} else {
		cols, per, err := prepareMany(ctx, &q.db.e, q.meta, q.many)
		if err != nil {
			return err
		}
		inTx := q.db.e.InTx || len(q.many) > per || engine.ConditionedUpsert(ctx, &q.db.e, q.meta.info, q.conflict, len(q.many))
		st = batch(q.meta, q.many[:min(per, len(q.many))], cols, q.conflict, inTx)
	}
	var err error
	mu.SQL, mu.Args, err = q.db.e.DryRun(ctx, &st)
	return err
}

// check refuses an insert that cannot be built: invalid metadata, Insert of
// a nil pointer, a column setter that is not Set or SetNull, an upsert
// without OnConflict's columns, and an upsert that sets the key of a
// bidirectional one-to-one edge (EdgeBidi).
func (q InsertQuery[E]) check[_ Lazy[E]]() error {
	if q.meta.err != nil {
		return q.meta.err
	}
	if q.single && q.one == nil {
		return fmt.Errorf("velox: %s: Insert of a nil pointer", q.meta.info.Name)
	}
	for _, a := range q.set {
		if a.Op != AssignSet {
			return fmt.Errorf("velox: %s: an insert's setter of %s is Add or Sub, which needs a row; use Set", q.meta.info.Name, q.meta.info.Columns[a.Column].Field)
		}
	}
	if q.conflict != nil {
		return engine.CheckConflict(q.meta.info, q.conflict, unsafe.Pointer(q.one))
	}
	return nil
}

// fillDefaults sets, in *p, every zero field whose column has Default or
// DefaultFunc. An UpdateDefault fills on update only.
//
// A zero value means unset. On a pointer field (velox-gen's Nillable) that is
// nil, so any value can be stored. On any other field, such as an Optional
// one, it is the zero value itself: a string with a Default can never store
// "", which is why velox-gen refuses a Default or DefaultFunc on a bool or
// number that is not Nillable, unless the Default is the zero value.
func fillDefaults[E any](m *Meta[E], p *E) {
	for i := range m.info.Columns {
		engine.FillDefault(&m.info.Columns[i], m.addr(p, i))
	}
}

// generatedKey is the index of a single integer primary key column holding
// zero in *p, which the database assigns; -1 when there is none.
func generatedKey[E any](m *Meta[E], p *E) int {
	if len(m.info.PrimaryKey) != 1 {
		return -1
	}
	k := m.info.PrimaryKey[0]
	if engine.ZeroInt(m.addr(p, k)) {
		return k
	}
	return -1
}

func rowValues[E any](m *Meta[E], p *E, cols []int) []any {
	vals := make([]any, len(cols))
	for j, c := range cols {
		vals[j] = fieldValue(m, p, c)
	}
	return vals
}

// oneStatement prepares the entity at p, as Insert's Exec does, and returns
// the statement that inserts it and its generated key column, -1 for none. On
// PostgreSQL and SQLite the statement reads that key back with RETURNING.
func oneStatement[E any](ctx context.Context, db *engine.DB, m *Meta[E], p *E, cf *ConflictClause) (Statement, int, error) {
	fillDefaults(m, p)
	gen := generatedKey(m, p)
	cols := engine.InsertColumns(m.info, gen)
	if len(cols) == 0 {
		return Statement{}, -1, engine.ErrNothingToInsert(m.info)
	}
	if err := engine.ValidateRow(m.info, unsafe.Pointer(p), cols); err != nil {
		return Statement{}, -1, err
	}
	if err := engine.InsertAllowed(ctx, m.info, unsafe.Pointer(p)); err != nil {
		return Statement{}, -1, err
	}
	st := Statement{Op: OpInsert, Entity: m.info, Columns: cols, Rows: [][]any{rowValues(m, p, cols)}, Conflict: cf, InTx: db.InTx}
	if engine.InfoBidi(m.info) {
		engine.MirrorRows(&st)
	}
	if gen >= 0 && db.D != MySQL {
		st.Returning = []int{gen}
	}
	engine.BidiInTx(&st, unsafe.Pointer(p))
	return st, gen, nil
}

// insertOne inserts the entity at p and sets out.Affected.
func insertOne[E any](ctx context.Context, db *engine.DB, m *Meta[E], p *E, cf *ConflictClause, out *Result) error {
	st, gen, err := oneStatement(ctx, db, m, p, cf)
	if err != nil {
		return err
	}
	return engine.InsertRow(ctx, db, unsafe.Pointer(p), &st, gen, out)
}

// prepareMany prepares every entity in xs, as InsertAll's Exec does, and
// returns the columns to insert and the most rows one statement holds.
func prepareMany[E any](ctx context.Context, db *engine.DB, m *Meta[E], xs []E) (cols []int, per int, err error) {
	for i := range xs {
		fillDefaults(m, &xs[i])
	}
	if err := engine.RefuseBidiRows(m.info, unsafe.Pointer(unsafe.SliceData(xs)), unsafe.Sizeof(xs[0]), len(xs)); err != nil {
		return nil, 0, err
	}
	gen := generatedKey(m, &xs[0])
	for i := range xs {
		if (generatedKey(m, &xs[i]) >= 0) != (gen >= 0) {
			return nil, 0, fmt.Errorf("velox: %s: InsertAll needs every entity's key set, or every key left at zero", m.info.Name)
		}
	}
	cols = engine.InsertColumns(m.info, gen)
	if len(cols) == 0 {
		return nil, 0, engine.ErrNothingToInsert(m.info)
	}
	for i := range xs {
		if err := engine.ValidateRow(m.info, unsafe.Pointer(&xs[i]), cols); err != nil {
			return nil, 0, err
		}
	}
	for i := range xs {
		if err := engine.InsertAllowed(ctx, m.info, unsafe.Pointer(&xs[i])); err != nil {
			return nil, 0, err
		}
	}
	return cols, engine.PerStatement(db.D, m.info, len(cols)), nil
}

// batch is the statement that inserts xs, prepared, in one statement.
func batch[E any](m *Meta[E], xs []E, cols []int, cf *ConflictClause, inTx bool) Statement {
	st := Statement{Op: OpInsert, Entity: m.info, Columns: cols, Conflict: cf, InTx: inTx, Rows: make([][]any, 0, len(xs))}
	for i := range xs {
		st.Rows = append(st.Rows, rowValues(m, &xs[i], cols))
	}
	if engine.InfoBidi(m.info) {
		engine.MirrorRows(&st)
	}
	return st
}

// insertMany inserts every entity in xs and sets out.Affected.
func insertMany[E any](ctx context.Context, db *engine.DB, m *Meta[E], xs []E, cf *ConflictClause, out *Result) error {
	if len(xs) == 0 {
		return nil
	}
	// Before the batches' Tx, which would fail as the transaction's: the
	// error names the entity on every path. The dry run first, as run and
	// plain check it.
	if IsDryRun(ctx) {
		return fmt.Errorf("velox: %s: %w", m.info.Name, ErrDryRun)
	}
	if db.Foreign(ctx) {
		return fmt.Errorf("velox: %s: %w", m.info.Name, ErrOutsideTx)
	}
	cols, per, err := prepareMany(ctx, db, m, xs)
	if err != nil {
		return err
	}
	if len(xs) <= per && !engine.ConditionedUpsert(ctx, db, m.info, cf, len(xs)) {
		n, err := insertBatches(ctx, db, m, xs, cols, per, cf)
		if err == nil {
			out.Affected = n
		}
		return err
	}
	var n int64 // in the transaction's closure: only this path pays for it
	err = db.Tx(ctx, func(ctx context.Context, db *engine.DB) error {
		var err error
		n, err = insertBatches(ctx, db, m, xs, cols, per, cf)
		return err
	})
	if err == nil {
		out.Affected = n
	}
	return err
}

// insertBatches sends xs, prepared, in statements of at most per rows, and
// returns the rows they affected: -1 when a batch's count is unknown.
func insertBatches[E any](ctx context.Context, db *engine.DB, m *Meta[E], xs []E, cols []int, per int, cf *ConflictClause) (int64, error) {
	var affected int64
	unknown := false
	for start := 0; start < len(xs); start += per {
		st := batch(m, xs[start:min(start+per, len(xs))], cols, cf, db.InTx)
		res, err := engine.ExecWrite(ctx, db, &st)
		if err != nil {
			return 0, err
		}
		if err := engine.CheckUpserted(&st, res.Affected); err != nil {
			return 0, err
		}
		if res.Affected < 0 {
			unknown = true
		}
		affected += res.Affected
	}
	if unknown {
		return -1, nil
	}
	return affected, nil
}
