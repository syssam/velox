package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
)

// The untyped writes: velox's Insert, InsertAll, Update and Delete with the
// entity's type erased, on a Table, by column index. They build the
// Statements the typed writes build and send them through the same core
// (Mutate, Write, InsertRow, ExecWrite), so the hooks, the policies, a
// scope and the errors are the same; internal/apitest's parity tests hold
// them to it.

// NewRows returns n new zero rows of the entity, one after another in
// memory, as an InsertRows takes them.
func (t Table) NewRows(n int) TableRows {
	s := t.state()
	if s.err != nil || n <= 0 {
		return TableRows{}
	}
	v := reflect.MakeSlice(s.slice.(reflect.Type), n, n)
	return TableRows{p: v.UnsafePointer(), size: s.size, n: n, info: s.info}
}

// SetField sets column col's field of row to v, converted as Convert
// converts; for a nullable column a nil v is NULL, and another v is the value
// its pointer points to.
func (t Table) SetField(row any, col int, v any) error {
	s := t.state()
	p, err := s.rowOf(row)
	if err != nil {
		return err
	}
	if col < 0 || col >= len(s.info.Columns) {
		return fmt.Errorf("velox: %s: a field of column %d, of %d", s.info.Name, col, len(s.info.Columns))
	}
	f := reflect.ValueOf(s.addr(p, col)).Elem()
	if v == nil {
		if !s.info.Columns[col].Nullable {
			return fmt.Errorf("velox: %s: %s is not nullable", s.info.Name, s.info.Columns[col].Field)
		}
		f.SetZero()
		return nil
	}
	c, err := t.Convert(col, v)
	if err != nil {
		return err
	}
	if f.Kind() == reflect.Pointer && s.info.Columns[col].Nullable {
		n := reflect.New(f.Type().Elem())
		n.Elem().Set(reflect.ValueOf(c))
		f.Set(n)
		return nil
	}
	f.Set(reflect.ValueOf(c))
	return nil
}

// Assign is the assignment of v to column col, as a column handle's Set
// makes it: v of the column's Go type (Pred's), or nil for SetNull on a
// nullable column. Another value is an error.
func (t Table) Assign(col int, v any) (Assignment, error) {
	s := t.state()
	if s.err != nil {
		return Assignment{}, s.err
	}
	if col < 0 || col >= len(s.info.Columns) {
		return Assignment{}, fmt.Errorf("velox: %s: an assignment to column %d, of %d", s.info.Name, col, len(s.info.Columns))
	}
	c := &s.info.Columns[col]
	if v == nil {
		if !c.Nullable {
			return Assignment{}, fmt.Errorf("velox: %s: %s is not nullable", s.info.Name, c.Field)
		}
		return Assignment{Column: col, Op: AssignSet}, nil
	}
	want, _ := s.vals[col].(reflect.Type)
	if reflect.TypeOf(v) != want {
		return Assignment{}, fmt.Errorf("velox: %s: an assignment to %s takes a %v, not a %T", s.info.Name, c.Field, want, v)
	}
	return Assignment{Column: col, Op: AssignSet, Value: v}, nil
}

// Validate runs the validators of the columns cols of row on the values it
// holds now: velox's Validate, untyped.
func (t Table) Validate(row any, cols ...int) error {
	s := t.state()
	p, err := s.rowOf(row)
	if err != nil {
		return err
	}
	return ValidateRow(s.info, p, cols)
}

// ValidateSet runs the validators of the Set values of set, and refuses an
// Immutable column among them: velox's ValidateSet, untyped.
func (t Table) ValidateSet(set ...Assignment) error {
	s := t.state()
	if s.err != nil {
		return s.err
	}
	return ValidateSetters(s.info, set)
}

// TableInsert is an INSERT of one row or of many of a Table's entity:
// velox's InsertQuery, untyped. Like it, it is a value.
type TableInsert struct {
	e        *DB
	t        *table
	one      unsafe.Pointer
	single   bool
	err      error // the row's: not one of t's entity
	many     TableRows
	conflict *ConflictClause
}

// InsertTable starts an INSERT of row, a *E of t's entity, through e:
// untyped.Insert. Another row fails the insert.
func InsertTable(e *DB, t Table, row any) TableInsert {
	s := t.state()
	p, err := s.rowOf(row)
	if s.err != nil {
		err = nil // the insert reports the Table's error
	}
	return TableInsert{e: e, t: s, one: p, single: true, err: err}
}

// InsertRows starts an INSERT of every row of rows, of t's entity, through
// e: untyped.InsertRows.
func InsertRows(e *DB, t Table, rows TableRows) TableInsert {
	return TableInsert{e: e, t: t.state(), many: rows}
}

// OnConflict is InsertQuery.OnConflict, of column indexes.
func (q TableInsert) OnConflict(cols ...int) TableInsert {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Columns = slices.Clone(cols)
	q.conflict = &cf
	return q
}

// Update is InsertQuery.Update, of column indexes.
func (q TableInsert) Update(cols ...int) TableInsert {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Update, cf.DoNothing = slices.Clone(cols), false
	q.conflict = &cf
	return q
}

// DoNothing is InsertQuery.DoNothing.
func (q TableInsert) DoNothing() TableInsert {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Update, cf.DoNothing = nil, true
	q.conflict = &cf
	return q
}

// Exec runs the insert, as InsertQuery.Exec does.
func (q TableInsert) Exec(ctx context.Context) error {
	if err := q.check(); err != nil {
		return err
	}
	if q.one == nil && q.many.n == 0 {
		return nil
	}
	q.fillDefaults()
	if q.e.HasHooks(q.t.info) {
		return Mutate(ctx, q.e, q.t.info, q.mutation(false), q.send)
	}
	var mu MutationState
	return q.send(ctx, &mu)
}

// SQL is InsertQuery.SQL.
func (q TableInsert) SQL(ctx context.Context) (string, []any, error) {
	ctx = DryRunContext(ctx)
	if err := q.check(); err != nil {
		return "", nil, err
	}
	if q.one == nil && q.many.n == 0 {
		return "", nil, nil
	}
	q.fillDefaults()
	if q.e.HasHooks(q.t.info) {
		mu := q.mutation(true)
		if err := Mutate(ctx, q.e, q.t.info, mu, q.render); err != nil && !DryRefused(mu, err) {
			return "", nil, err
		}
		return mu.SQL, mu.Args, nil
	}
	mu := MutationState{Dry: true}
	if err := q.render(ctx, &mu); err != nil {
		return "", nil, err
	}
	return mu.SQL, mu.Args, nil
}

// fillDefaults fills the defaults of every row before the hooks run, as
// InsertQuery's does: a value a hook writes is stored over the default.
func (q TableInsert) fillDefaults() {
	if q.one != nil {
		q.t.fillDefaults(q.one)
		return
	}
	for i := range q.many.n {
		q.t.fillDefaults(q.many.at(i))
	}
}

func (q TableInsert) rows() []unsafe.Pointer {
	if q.one != nil {
		return []unsafe.Pointer{q.one}
	}
	out := make([]unsafe.Pointer, q.many.n)
	for i := range out {
		out[i] = q.many.at(i)
	}
	return out
}

func (q TableInsert) mutation(dry bool) *MutationState {
	return &MutationState{Op: OpInsert, Dry: dry, Rows: q.rows()}
}

func (q TableInsert) send(ctx context.Context, mu *MutationState) error {
	var res Result
	var err error
	if q.one != nil {
		err = q.t.insertOne(ctx, q.e, q.one, q.conflict, &res)
	} else {
		err = q.t.insertMany(ctx, q.e, q.many, q.conflict, &res)
	}
	if err == nil {
		mu.Affected = res.Affected
	}
	return err
}

func (q TableInsert) render(ctx context.Context, mu *MutationState) error {
	var st Statement
	if q.one != nil {
		var err error
		if st, _, err = q.t.oneStatement(ctx, q.e, q.one, q.conflict); err != nil {
			return err
		}
	} else {
		var err error
		if st, err = q.t.firstBatch(ctx, q.e, q.many, q.conflict); err != nil {
			return err
		}
	}
	var err error
	mu.SQL, mu.Args, err = q.e.DryRun(ctx, &st)
	return err
}

func (q TableInsert) check() error {
	if q.t.err != nil {
		return q.t.err
	}
	if q.err != nil {
		return fmt.Errorf("velox: %s: Insert: %w", q.t.info.Name, q.err)
	}
	if err := q.t.checkRows(q.many); err != nil {
		return err
	}
	if q.conflict != nil {
		for _, c := range append(slices.Clip(q.conflict.Columns), q.conflict.Update...) {
			if err := checkCol(q.t.info, c); err != nil {
				return err
			}
		}
		return CheckConflict(q.t.info, q.conflict, q.one)
	}
	return nil
}

func (t *table) fillDefaults(p unsafe.Pointer) {
	for i := range t.info.Columns {
		FillDefault(&t.info.Columns[i], t.addr(p, i))
	}
}

func (t *table) generatedKey(p unsafe.Pointer) int {
	if len(t.info.PrimaryKey) != 1 {
		return -1
	}
	k := t.info.PrimaryKey[0]
	if ZeroInt(t.addr(p, k)) {
		return k
	}
	return -1
}

func (t *table) rowValues(p unsafe.Pointer, cols []int) []any {
	vals := make([]any, len(cols))
	for j, c := range cols {
		vals[j] = Elem(t.addr(p, c))
	}
	return vals
}

func (t *table) oneStatement(ctx context.Context, db *DB, p unsafe.Pointer, cf *ConflictClause) (Statement, int, error) {
	gen := t.generatedKey(p)
	cols := InsertColumns(t.info, gen)
	if len(cols) == 0 {
		return Statement{}, -1, ErrNothingToInsert(t.info)
	}
	if err := ValidateRow(t.info, p, cols); err != nil {
		return Statement{}, -1, err
	}
	if err := InsertAllowed(ctx, db, t.info, p); err != nil {
		return Statement{}, -1, err
	}
	st := Statement{Op: OpInsert, Entity: t.info, Columns: cols, Rows: [][]any{t.rowValues(p, cols)}, Conflict: cf, InTx: db.InTx}
	if InfoBidi(t.info) {
		MirrorRows(&st)
	}
	if err := PlanInsert(db.D, &st, gen); err != nil {
		return Statement{}, -1, err
	}
	BidiInTx(&st, p)
	return st, gen, nil
}

func (t *table) insertOne(ctx context.Context, db *DB, p unsafe.Pointer, cf *ConflictClause, out *Result) error {
	st, gen, err := t.oneStatement(ctx, db, p, cf)
	if err != nil {
		return err
	}
	return InsertRow(ctx, db, p, &st, gen, out)
}

func (t *table) prepareMany(ctx context.Context, db *DB, xs TableRows) (cols []int, per int, err error) {
	if err := RefuseBidiRows(t.info, xs.p, xs.size, xs.n); err != nil {
		return nil, 0, err
	}
	gen := t.generatedKey(xs.at(0))
	for i := range xs.n {
		if (t.generatedKey(xs.at(i)) >= 0) != (gen >= 0) {
			return nil, 0, fmt.Errorf("velox: %s: InsertAll needs every entity's key set, or every key left at zero", t.info.Name)
		}
	}
	cols = InsertColumns(t.info, gen)
	if len(cols) == 0 {
		return nil, 0, ErrNothingToInsert(t.info)
	}
	for i := range xs.n {
		if err := ValidateRow(t.info, xs.at(i), cols); err != nil {
			return nil, 0, err
		}
	}
	for i := range xs.n {
		if err := InsertAllowed(ctx, db, t.info, xs.at(i)); err != nil {
			return nil, 0, err
		}
	}
	return cols, PerStatementOf(db, t.info, cols, xs.n, func(i, c int) any { return t.addr(xs.at(i), c) }), nil
}

// batch is the statement that inserts xs's rows from to to, prepared.
func (t *table) batch(xs TableRows, from, to int, cols []int, cf *ConflictClause, inTx bool) Statement {
	st := Statement{Op: OpInsert, Entity: t.info, Columns: slices.Clone(cols), Conflict: cf, InTx: inTx, Rows: make([][]any, 0, to-from)}
	for i := from; i < to; i++ {
		st.Rows = append(st.Rows, t.rowValues(xs.at(i), cols))
	}
	if InfoBidi(t.info) {
		MirrorRows(&st)
	}
	return st
}

func (t *table) insertMany(ctx context.Context, db *DB, xs TableRows, cf *ConflictClause, out *Result) error {
	if xs.n == 0 {
		return nil
	}
	if IsDryRun(ctx) {
		return fmt.Errorf("velox: %s: %w", t.info.Name, ErrDryRun)
	}
	if db.Foreign(ctx) {
		return fmt.Errorf("velox: %s: %w", t.info.Name, ErrOutsideTx)
	}
	cols, per, err := t.prepareMany(ctx, db, xs)
	if err != nil {
		return err
	}
	ends := t.defaultRuns(db, xs, cols)
	if db.recording(ctx) != nil {
		return t.recordMany(ctx, db, xs, cols, per, ends, cf, out)
	}
	if len(ends) == 1 && xs.n <= per && !ConditionedUpsert(ctx, db, t.info, cf, xs.n) {
		n, err := t.insertBatches(ctx, db, xs, cols, per, ends, cf, SendBatch)
		if err == nil {
			out.Affected = n
		}
		return err
	}
	var n int64
	err = db.Tx(ctx, func(ctx context.Context, db *DB) error {
		var err error
		n, err = t.insertBatches(ctx, db, xs, cols, per, ends, cf, SendBatch)
		return err
	})
	if err == nil {
		out.Affected = n
	}
	return err
}

// recordMany is insertMany on a recording handle: the same statements, and
// a Change per row, in the order of xs, across the statements (an
// upsert's as conflictRead.paired orders them).
func (t *table) recordMany(ctx context.Context, db *DB, xs TableRows, cols []int, per int, ends []int, cf *ConflictClause, out *Result) error {
	if cf != nil && db.D == MySQL {
		rows := make([]unsafe.Pointer, xs.n)
		for i := range rows {
			rows[i] = xs.at(i)
		}
		if err := refuseUnfindable(db.D, t.info, cf, t.generatedKey(xs.at(0)), rows); err != nil {
			return err
		}
	}
	// On PostgreSQL 18 an upsert reads no row first: one statement is single.
	old := cf != nil && returnsOld(ctx, db)
	single := len(ends) == 1 && xs.n <= per && (cf == nil || old && !ConditionedUpsert(ctx, db, t.info, cf, xs.n))
	var n int64
	var oldp *bool
	if cf != nil {
		oldp = &old
	}
	err := recordedInsert(ctx, db, single, oldp, func(ctx context.Context, tx *DB) ([]ChangeRow, error) {
		var changes []ChangeRow
		var err error
		n, err = t.insertBatches(ctx, tx, xs, cols, per, ends, cf, func(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer, gen int) (int64, error) {
			st.returnOld = old
			cs, k, err := sendRecorded(ctx, tx, st, rows, gen, func(ctx context.Context, tx *DB, st *Statement) (int64, error) {
				return SendBatch(ctx, tx, st, rows, gen)
			})
			changes = append(changes, cs...)
			return k, err
		})
		return changes, err
	})
	if err == nil {
		out.Affected = n
	}
	return err
}

// firstBatch prepares xs as insertMany does and returns the first statement
// it would send, for SQL(ctx).
func (t *table) firstBatch(ctx context.Context, db *DB, xs TableRows, cf *ConflictClause) (Statement, error) {
	cols, per, err := t.prepareMany(ctx, db, xs)
	if err != nil {
		return Statement{}, err
	}
	ends := t.defaultRuns(db, xs, cols)
	inTx := db.InTx || len(ends) > 1 || xs.n > per || ConditionedUpsert(ctx, db, t.info, cf, xs.n)
	st := t.batch(xs, 0, min(per, ends[0]), cols, cf, inTx)
	if err := PlanInsert(db.D, &st, t.generatedKey(xs.at(0))); err != nil {
		return Statement{}, err
	}
	return st, nil
}

// InsertAllRows is InsertAll's Exec for the n entities of in that start at
// p, size bytes apart: the untyped API's InsertAll, which the typed one
// shares, so that no entity compiles a batch insert of its own.
func InsertAllRows(ctx context.Context, db *DB, in *EntityInfo, p unsafe.Pointer, size uintptr, n int, cf *ConflictClause, out *Result) error {
	return InfoTable(in).state().insertMany(ctx, db, TableRows{p: p, size: size, n: n, info: in}, cf, out)
}

// InsertAllStatement is the first statement InsertAllRows would send, for
// SQL(ctx).
func InsertAllStatement(ctx context.Context, db *DB, in *EntityInfo, p unsafe.Pointer, size uintptr, n int, cf *ConflictClause) (Statement, error) {
	return InfoTable(in).state().firstBatch(ctx, db, TableRows{p: p, size: size, n: n, info: in}, cf)
}

// defaultRuns is DefaultRuns over xs.
func (t *table) defaultRuns(db *DB, xs TableRows, cols []int) []int {
	return DefaultRuns(t.info, db.D, cols, xs.n, func(i, c int) any { return Elem(t.addr(xs.at(i), c)) })
}

// insertBatches sends xs's rows, a statement of at most per rows within
// each run of ends, each with send (SendBatch, or recordMany's).
func (t *table) insertBatches(ctx context.Context, db *DB, xs TableRows, cols []int, per int, ends []int, cf *ConflictClause,
	send func(ctx context.Context, db *DB, st *Statement, rows []unsafe.Pointer, gen int) (int64, error)) (int64, error) {
	gen := t.generatedKey(xs.at(0))
	var affected int64
	unknown := false
	from := 0
	for _, end := range ends {
		for start := from; start < end; start += per {
			stop := min(start+per, end)
			st := t.batch(xs, start, stop, cols, cf, db.InTx)
			if err := PlanInsert(db.D, &st, gen); err != nil {
				return 0, err
			}
			rows := make([]unsafe.Pointer, stop-start)
			for i := range rows {
				rows[i] = xs.at(start + i)
			}
			n, err := send(ctx, db, &st, rows, gen)
			if err != nil {
				return 0, err
			}
			if n < 0 {
				unknown = true
			}
			affected += n
		}
		from = end
	}
	if unknown {
		return -1, nil
	}
	return affected, nil
}

// TableUpdate is an UPDATE of a Table's entity: velox's UpdateQuery,
// untyped, with setters by column index. Like it, it is a value.
type TableUpdate struct {
	e   *DB
	t   *table
	st  Statement
	err error // an assignment's or a condition's error
}

// UpdateTable starts an UPDATE of t's entity through e:
// untyped.Update.
func UpdateTable(e *DB, t Table) TableUpdate {
	s := t.state()
	return TableUpdate{e: e, t: s, st: Statement{Op: OpUpdate, Entity: s.info, InTx: e.InTx}}
}

// Set adds the assignment of v to column col, checked as Table.Assign
// checks it: a value of another type fails the update.
func (u TableUpdate) Set(col int, v any) TableUpdate {
	a, err := Table{u.t}.Assign(col, v)
	if err != nil {
		if u.err == nil {
			u.err = err
		}
		return u
	}
	return u.Assign(a)
}

// SetNull adds the assignment of NULL to column col, a nullable column.
func (u TableUpdate) SetNull(col int) TableUpdate { return u.Set(col, nil) }

// Assign adds assignments made elsewhere, as by a column handle's Set.
func (u TableUpdate) Assign(as ...Assignment) TableUpdate {
	u.st.Set = append(slices.Clip(u.st.Set), as...)
	return u
}

// Where adds conditions; all of them must hold.
func (u TableUpdate) Where(ps ...Predicate) TableUpdate {
	u.st.Where, u.err = addWhere(u.st.Where, u.err, ps)
	return u
}

// All is UpdateQuery.All.
func (u TableUpdate) All() TableUpdate {
	u.st.AllRows = true
	return u
}

// MarkSoftDelete returns u marked as a soft delete, the update package
// softdelete makes of a delete: a recording handle reports each row it
// changes as a delete, Soft, with the row before and after it. It changes
// nothing that is sent.
func MarkSoftDelete(u TableUpdate) TableUpdate {
	u.st.softDelete = true
	return u
}

func (u TableUpdate) statement() (Statement, error) {
	if u.t.err != nil {
		return Statement{}, u.t.err
	}
	if u.err != nil {
		return Statement{}, u.err
	}
	if len(u.st.Set) == 0 {
		return Statement{}, fmt.Errorf("velox: %s: Update has no Set", u.t.info.Name)
	}
	st := CloneStatement(&u.st)
	if err := u.t.checkStatement(&st); err != nil {
		return Statement{}, err
	}
	return st, nil
}

// Exec is UpdateQuery.Exec.
func (u TableUpdate) Exec(ctx context.Context) (int64, error) {
	st, err := u.statement()
	if err != nil {
		return 0, err
	}
	n, _, _, err := Write(ctx, u.e, &st, false, false)
	return n, err
}

// SQL is UpdateQuery.SQL.
func (u TableUpdate) SQL(ctx context.Context) (string, []any, error) {
	ctx = DryRunContext(ctx)
	st, err := u.statement()
	if err != nil {
		return "", nil, err
	}
	_, q, args, err := Write(ctx, u.e, &st, true, false)
	return q, args, err
}

// TableDelete is a DELETE of a Table's entity: velox's DeleteQuery,
// untyped. Like it, it is a value.
type TableDelete struct {
	e   *DB
	t   *table
	st  Statement
	err error
}

// DeleteTable starts a DELETE of t's entity through e:
// untyped.Delete.
func DeleteTable(e *DB, t Table) TableDelete {
	s := t.state()
	return TableDelete{e: e, t: s, st: Statement{Op: OpDelete, Entity: s.info, InTx: e.InTx}}
}

// Where adds conditions; all of them must hold.
func (d TableDelete) Where(ps ...Predicate) TableDelete {
	d.st.Where, d.err = addWhere(d.st.Where, d.err, ps)
	return d
}

// All is DeleteQuery.All.
func (d TableDelete) All() TableDelete {
	d.st.AllRows = true
	return d
}

// Exec is DeleteQuery.Exec.
func (d TableDelete) Exec(ctx context.Context) (int64, error) {
	if d.t.err != nil {
		return 0, d.t.err
	}
	if d.err != nil {
		return 0, d.err
	}
	st := CloneStatement(&d.st)
	if err := d.t.checkStatement(&st); err != nil {
		return 0, err
	}
	n, _, _, err := Write(ctx, d.e, &st, false, false)
	return n, err
}

// SQL is DeleteQuery.SQL.
func (d TableDelete) SQL(ctx context.Context) (string, []any, error) {
	ctx = DryRunContext(ctx)
	if d.t.err != nil {
		return "", nil, d.t.err
	}
	if d.err != nil {
		return "", nil, d.err
	}
	st := CloneStatement(&d.st)
	if err := d.t.checkStatement(&st); err != nil {
		return "", nil, err
	}
	_, q, args, err := Write(ctx, d.e, &st, true, false)
	return q, args, err
}

// addWhere appends ps to where, and the first condition's error to err,
// as the typed writes' Where do.
func addWhere(where []Predicate, err error, ps []Predicate) ([]Predicate, error) {
	where = slices.Clip(where)
	for _, p := range ps {
		where = append(where, p)
		if err == nil {
			err = PredErr(PredNode(p))
		}
	}
	return where, err
}
