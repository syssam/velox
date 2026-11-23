package velox

import (
	"context"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// InsertQuery is an INSERT of one entity or of many, built by chaining and
// run by Exec. Like Query it is a value: extending it leaves it as it was.
type InsertQuery[E any] struct {
	db       *DB
	meta     *metaState[E]
	one      *E
	single   bool // made by Insert, so a nil one is a mistake, not an empty InsertAll
	all      bool // UpdateAll, which check resolves into conflict's Update
	many     []E
	conflict *ConflictClause
	set      engine.Setters // Set's setters
	err      error          // SetInput's error
}

// Insert starts an INSERT of the entity x points to:
//
//	db.Insert(&inv).Exec(ctx)
//
// Exec changes *x: the entity's hooks (Hooks) may change it, Exec fills
// defaults and reads the generated key back, and it leaves those changes in place even when the
// insert then fails.
func (db *DB) Insert[E any, PE Entity[E]](x PE) InsertQuery[E] {
	return InsertQuery[E]{db: db, meta: stateOf(x.VeloxMeta()), one: (*E)(x), single: true}
}

// InsertAll starts an INSERT of every entity in xs, in as few statements as
// the dialect's parameter limit allows. The entity's hooks (Hooks) run once,
// over all of xs. Exec fills defaults into xs[i], the caller's
// own slice elements, and leaves those changes in place even when the insert then fails.
func (db *DB) InsertAll[E any, PE Entity[E]](xs []E) InsertQuery[E] {
	return InsertQuery[E]{db: db, meta: stateOf(PE(nil).VeloxMeta()), many: xs}
}

func columnIndexes[E any](cs []Column[E]) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = int(c.column(nil))
	}
	return out
}

// OnConflict names the columns of the unique key whose conflict the insert
// resolves; follow it with Update, UpdateAll, UpdateSet or DoNothing. Without either, the insert
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
// values when the insert conflicts. It replaces an earlier Update's
// columns; UpdateSet's setters stay.
func (q InsertQuery[E]) Update[_ Lazy[E]](cols ...Column[E]) InsertQuery[E] {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Update, cf.DoNothing = columnIndexes(cols), false
	q.conflict, q.all = &cf, false
	return q
}

// UpdateAll overwrites every column of the existing row with the inserted
// values when the insert conflicts, as ent's UpdateNewValues does, but the
// primary key's, OnConflict's columns, the Immutable columns, the Version
// column and a column whose SetPtr setter was skipped:
//
//	db.Insert(&user).OnConflict(veloxgen.Users.Email).UpdateAll().Exec(ctx)
//
// An UpdateDefault, such as updated_at's, does not fire, as for Update
// (ADR 0017): its column takes the inserted row's value, which is its
// Default unless the row sets it. A column UpdateAll writes is sent with the
// row's value, never left to the database's default. With no column left,
// the upsert keeps the existing row. Update or DoNothing after it replaces
// it.
func (q InsertQuery[E]) UpdateAll[_ Lazy[E]]() InsertQuery[E] {
	q.all = true
	return q
}

// UpdateSet adds setters to the upsert's update branch, which runs on the
// existing row when the insert conflicts, as ent's UpdateX, AddX and
// ResolveWith do:
//
//	db.Insert(&stock).OnConflict(S.SKU).UpdateSet(
//		S.Qty.Add(stock.Qty),                                 // qty = stock.qty + $1
//		S.Note.Set("restocked"),                              // note = $2
//		velox.SetExpr(S.Peak, "GREATEST(?, ?)", S.Peak, velox.Excluded(S.Qty)),
//	).Exec(ctx)
//
// A column handle's Set and SetNull write a value. Add and Sub add to, or
// subtract from, the existing row's value, as an UPDATE's do; to add the
// inserted value instead, write SetExpr(c, "? + ?", c, Excluded(c)).
// SetExpr writes an expression: a column handle there is the existing
// row's column, and Excluded(c) the inserted row's. A SetPtr of nil is
// skipped; with no other assignment left, the upsert keeps the existing row,
// as for Set. An edge setter fails the insert.
//
// It combines with Update and UpdateAll, which copy the inserted values of
// other columns; a column is assigned once, by one of them, and UpdateAll
// leaves out the columns UpdateSet assigns. DoNothing drops the setters.
// Immutable and Version columns are refused as for Update, and the
// validators run on Set's values. On MySQL, ON DUPLICATE KEY UPDATE runs
// its assignments in order, so an expression after an assignment of the
// same row reads the assigned value; PostgreSQL and SQLite read the row as
// it was.
//
// Exec reads back into the entity what the update branch stored in these
// columns: with RETURNING on PostgreSQL and SQLite, and on MySQL for an
// Insert, not an InsertAll.
func (q InsertQuery[E]) UpdateSet[_ Lazy[E]](ss ...Setter[E]) InsertQuery[E] {
	cf, err := engine.UpsertSet(q.conflict, assignments(ss))
	if err != nil {
		if q.err == nil {
			q.err = err
		}
		return q
	}
	q.conflict = cf
	return q
}

// UpdateWhere adds conditions to the upsert's update branch, on the
// existing row, as ent's UpdateWhere does: a conflicting row they reject
// is neither updated nor replaced, and Exec leaves the entity's generated
// key zero. A condition may name the inserted row with Excluded:
//
//	db.Insert(&doc).OnConflict(D.Slug).Update(D.Body, D.Rev).
//		UpdateWhere(velox.Expr[Doc]("? > ?", velox.Excluded(D.Rev), D.Rev)).Exec(ctx)
//
// They are ANDed with the policy's conditions, which the update branch
// gets whatever UpdateWhere says. An InsertAll that reads values back
// matches the returned rows to its entities by OnConflict's columns, and
// runs in a transaction, as with the policy's conditions. MySQL's ON
// DUPLICATE KEY UPDATE takes no condition: there Exec fails before
// anything is sent.
func (q InsertQuery[E]) UpdateWhere[_ Lazy[E]](ps ...Pred[E]) InsertQuery[E] {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Where = slices.Clip(cf.Where)
	for _, p := range ps {
		cf.Where = append(cf.Where, Predicate(p))
	}
	q.conflict = &cf
	return q
}

// OnConflictWhere adds the conflict target's conditions: the predicate of
// the partial unique index whose conflict the insert resolves, as ent's
// ConflictWhere does. It renders ON CONFLICT (cols) WHERE … on
// PostgreSQL and SQLite, which pick the partial index by it; a condition
// is plain, holding no relation or subquery. MySQL has no partial index,
// and Exec fails there before anything is sent.
func (q InsertQuery[E]) OnConflictWhere[_ Lazy[E]](ps ...Pred[E]) InsertQuery[E] {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.TargetWhere = slices.Clip(cf.TargetWhere)
	for _, p := range ps {
		cf.TargetWhere = append(cf.TargetWhere, Predicate(p))
	}
	q.conflict = &cf
	return q
}

// OnConstraint names the constraint whose conflict the insert resolves,
// on PostgreSQL: ON CONFLICT ON CONSTRAINT name, as ent's
// ConflictConstraint does. OnConflict's columns are still required: velox
// reads the row back, and matches rows to entities, by them. SQLite and
// MySQL fail the insert before anything is sent.
func (q InsertQuery[E]) OnConstraint[_ Lazy[E]](name string) InsertQuery[E] {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Constraint = name
	q.conflict = &cf
	return q
}

// Excluded is column c of the row an upsert proposes to insert, for
// SetExpr in UpdateSet and Expr in UpdateWhere: EXCLUDED.c on PostgreSQL
// and SQLite, new.c on MySQL. Anywhere else it fails the statement. A
// column it names is always sent, never left to the database's default.
func Excluded[E any](c Column[E]) Term[E] {
	return Term[E]{engine.ExcludedTerm(c.column(nil))}
}

// DoNothing keeps the existing row when the insert conflicts. It drops
// Update's columns and UpdateSet's setters.
func (q InsertQuery[E]) DoNothing[_ Lazy[E]]() InsertQuery[E] {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Update, cf.Set, cf.DoNothing = nil, nil, true
	q.conflict, q.all = &cf, false
	return q
}

// Set adds setters. A column setter, a column handle's Set or SetNull,
// stores its value in each entity before the insert, so it wins over the
// field; Add and Sub fail the insert. The column of a column setter is
// always written, as ent writes every field SetX was called for: Set(0)
// stores 0 and SetNull stores NULL, so neither the column's database
// DEFAULT nor a Default replaces them, while a zero or nil field no setter
// names gets the default (ADR 0124, 0136). A SetPtr given a nil pointer is skipped, as ent's
// SetNillableX(nil): the field stays as it is, so a nil one gets its
// default, and a column of an upsert's Update that no other setter sets is
// not updated (with none left, the upsert keeps the existing row, as
// DoNothing). An edge setter, a relation handle's
// Add, Remove or Clear, is written after the insert, with each entity's key:
//
//	err := db.Insert(&post).Set(veloxgen.Posts.Tags.Add(1, 2)).Exec(ctx)
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
//   - Set, of an edge whose foreign key is on the owner's row, checks its
//     key under the target's Read policy before the insert, in its
//     transaction, and stores it in each entity's key field.
//
// Add, Remove and Clear fail an upsert, whose row may be one it did not
// insert, and an InsertAll whose keys the database assigns, as it reads
// none back. An
// edge is written only when the insert affected a row: a hook that does not
// call next writes none.
func (q InsertQuery[E]) Set[_ Lazy[E]](ss ...Setter[E]) InsertQuery[E] {
	q.set = q.set.Add(assignments(ss))
	return q
}

// SetInput adds the setters in.Setters returns, as Set does. When Setters fails,
// the insert keeps its error, and Exec and SQL return it unchanged before
// anything is sent:
//
//	err := db.Insert(&post).SetInput(input).Exec(ctx)
func (q InsertQuery[E]) SetInput[_ Lazy[E]](in Input[E]) InsertQuery[E] {
	ss, err := in.Setters()
	if err != nil {
		if q.err == nil {
			q.err = err
		}
		return q
	}
	return q.Set(ss...)
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
// A column whose database default the schema declares, and every row holds
// the zero value in, is left out, and a nullable column every row holds nil
// in, unless a setter names the column; the database fills them. What it filled is read back into the entity:
// with RETURNING, or a SELECT by key in the same transaction on MySQL (ADR
// 0093).
//
// InsertAll reads every row's key back too, and the columns the database
// filled: on MySQL the keys of one statement are @@auto_increment_increment
// apart from LastInsertId, for one without OnConflict that wrote every row.
// velox reads that step once per DB, before the first multi-row INSERT, and
// refuses the insert unsent when the read fails; a session that sets its own
// step later is not seen. An InsertAll
// with DoNothing reads nothing back. Give every entity its key, or leave
// every key at zero for the database to assign. Rows that leave different
// columns to the database go in different statements. An InsertAll that needs several
// statements is atomic: outside a transaction it runs them in one, and inside
// one it runs them under a savepoint. So is an upsert of several rows whose
// update branch gets the policy's conditions, so that the error for a hidden
// row also undoes the rows written with it. An InsertAll of no entities sends
// nothing and returns nil under any context, a dry run's or another
// transaction's included: its hooks do not run.
func (q InsertQuery[E]) Exec[_ Lazy[E]](ctx context.Context) error {
	cf, err := q.check()
	if err != nil {
		return err
	}
	q.conflict = cf
	if q.one == nil && len(q.many) == 0 {
		return nil
	}
	q.fillDefaults()
	if q.set.Empty() && !q.db.e.HasHooks(q.meta.Info) {
		var mu engine.MutationState
		return q.send(ctx, &q.db.e, &mu)
	}
	return engine.Insert(ctx, &q.db.e, q.meta.Info, q.set, q.mutation(false).Rows, q.conflict != nil, q.one == nil, q.send)
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
	cf, err := q.check()
	if err != nil {
		return "", nil, err
	}
	q.conflict = cf
	if q.one == nil && len(q.many) == 0 {
		return "", nil, nil
	}
	q.fillDefaults()
	if !q.set.Empty() {
		if _, err := engine.PrepareInsert(q.meta.Info, q.set, q.mutation(false).Rows, q.conflict != nil, q.one == nil); err != nil {
			return "", nil, err
		}
	}
	if q.db.e.HasHooks(q.meta.Info) {
		mu := q.mutation(true)
		if err := engine.Mutate(ctx, &q.db.e, q.meta.Info, mu, q.render); err != nil && !engine.DryRefused(mu, err) {
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

// fillDefaults fills the defaults of every row, before Set's setters and the
// hooks, as ent's builder applies its defaults before its hooks: a value a
// setter or a hook writes is the caller's, and is stored over the default,
// zero or not. Only a field the caller left at its zero value, and nothing
// wrote after, gets the Default or DefaultFunc.
//
// A zero field means unset. On a pointer field (velox-gen's Nillable) that is
// nil, so any value can be stored from the struct. On any other field it is
// the zero value itself: a struct's false, 0 or "" gets the Default.
func (q InsertQuery[E]) fillDefaults[_ Lazy[E]]() {
	if q.one != nil {
		engine.FillDefaults(q.meta.Info, unsafe.Pointer(q.one))
		return
	}
	for i := range q.many {
		engine.FillDefaults(q.meta.Info, unsafe.Pointer(&q.many[i]))
	}
}

// mutation is the insert as its entity's hooks see it: its rows.
func (q InsertQuery[E]) mutation[_ Lazy[E]](dry bool) *engine.MutationState {
	mu := &engine.MutationState{Op: OpInsert, Dry: dry, Edges: q.set.Edges}
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
		err = insertOne(ctx, db, q.meta, q.one, q.set.InsertSet(q.meta.Info), q.conflict, &res)
	} else {
		err = insertMany(ctx, db, q.meta, q.many, q.set.InsertSet(q.meta.Info), q.conflict, &res)
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
		if st, _, err = oneStatement(ctx, &q.db.e, q.meta, q.one, q.set.InsertSet(q.meta.Info), q.conflict); err != nil {
			return err
		}
	} else {
		var err error
		if st, err = engine.InsertAllStatement(ctx, &q.db.e, q.meta.Info, unsafe.Pointer(unsafe.SliceData(q.many)), unsafe.Sizeof(q.many[0]), len(q.many), q.set.InsertSet(q.meta.Info), q.conflict); err != nil {
			return err
		}
	}
	var err error
	mu.SQL, mu.Args, err = q.db.e.DryRun(ctx, &st)
	return err
}

// check refuses an insert that cannot be built: invalid metadata, Insert of
// a nil pointer, a column setter that is not Set or SetNull, an upsert
// without OnConflict's columns, and an upsert that sets the key of a
// bidirectional one-to-one edge (EdgeBidi). It returns the conflict clause
// the insert sends, without the columns whose setters were skipped
// (engine.CheckInsertQuery).
func (q InsertQuery[E]) check[_ Lazy[E]]() (*ConflictClause, error) {
	if q.meta.Err != nil {
		return nil, q.meta.Err
	}
	if q.err != nil {
		return nil, q.err
	}
	return engine.CheckInsertQuery(q.meta.Info, q.set, q.conflict, unsafe.Pointer(q.one), q.single && q.one == nil, q.all)
}

// generatedKey is the index of a single integer primary key column holding
// zero in *p, which the database assigns; -1 when there is none.
func generatedKey[E any](m *metaState[E], p *E) int {
	if len(m.Info.PrimaryKey) != 1 {
		return -1
	}
	k := m.Info.PrimaryKey[0]
	if engine.ZeroInt(m.Addr(p, k)) {
		return k
	}
	return -1
}

func rowValues[E any](m *metaState[E], p *E, cols []int) []any {
	vals := make([]any, len(cols))
	for j, c := range cols {
		vals[j] = fieldValue(m, p, c)
	}
	return vals
}

// oneStatement prepares the entity at p, as Insert's Exec does, and returns
// the statement that inserts it and its generated key column, -1 for none,
// writing set, the columns its setters store a value in, as the entity
// holds them. On
// PostgreSQL and SQLite the statement reads that key back with RETURNING.
func oneStatement[E any](ctx context.Context, db *engine.DB, m *metaState[E], p *E, set []int, cf *ConflictClause) (Statement, int, error) {
	gen := generatedKey(m, p)
	cols := engine.InsertColumns(m.Info, gen)
	if len(cols) == 0 {
		return Statement{}, -1, engine.ErrNothingToInsert(m.Info)
	}
	if err := engine.ValidateInsert(m.Info, db.D, unsafe.Pointer(p), cols, set, cf); err != nil {
		return Statement{}, -1, err
	}
	if err := engine.InsertAllowed(ctx, db, m.Info, unsafe.Pointer(p)); err != nil {
		return Statement{}, -1, err
	}
	st := Statement{Op: OpInsert, Entity: m.Info, Columns: cols, Rows: [][]any{rowValues(m, p, cols)}, Conflict: cf, InTx: db.InTx}
	engine.SetExplicit(&st, set)
	if engine.InfoBidi(m.Info) {
		engine.MirrorRows(&st)
	}
	if err := engine.PlanInsert(db.D, &st, gen); err != nil {
		return Statement{}, -1, err
	}
	engine.BidiInTx(&st, unsafe.Pointer(p))
	return st, gen, nil
}

// insertOne inserts the entity at p and sets out.Affected.
func insertOne[E any](ctx context.Context, db *engine.DB, m *metaState[E], p *E, set []int, cf *ConflictClause, out *Result) error {
	st, gen, err := oneStatement(ctx, db, m, p, set, cf)
	if err != nil {
		return err
	}
	return engine.InsertRow(ctx, db, unsafe.Pointer(p), &st, gen, out)
}

// insertMany inserts every entity in xs and sets out.Affected, with the
// untyped API's batch insert (engine.InsertAllRows).
func insertMany[E any](ctx context.Context, db *engine.DB, m *metaState[E], xs []E, set []int, cf *ConflictClause, out *Result) error {
	if len(xs) == 0 {
		return nil
	}
	return engine.InsertAllRows(ctx, db, m.Info, unsafe.Pointer(unsafe.SliceData(xs)), unsafe.Sizeof(xs[0]), len(xs), set, cf, out)
}
