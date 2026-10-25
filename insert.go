package velox

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"unsafe"
)

// maxParams is each dialect's limit on the parameters of one statement.
var maxParams = map[Dialect]int{Postgres: 65535, MySQL: 65535, SQLite: 32766}

// InsertQuery is an INSERT of one entity or of many, built by chaining and
// run by Exec. Like Query it is a value: extending it leaves it as it was.
type InsertQuery[E any] struct {
	db       *DB
	meta     *Meta[E]
	one      *E
	single   bool // made by Insert, so a nil one is a mistake, not an empty InsertAll
	many     []E
	conflict *ConflictClause
}

// Insert starts an INSERT of the entity x points to:
//
//	db.Insert(&inv).Exec(ctx)
//
// Exec changes *x: the entity's hooks (Hooks) may change it, Exec fills
// defaults and the tenant and reads the generated key back, and it leaves those changes in place even when the
// insert then fails.
func (db *DB) Insert[E any, PE Entity[E]](x PE) InsertQuery[E] {
	return InsertQuery[E]{db: db, meta: x.VeloxMeta(), one: (*E)(x), single: true}
}

// InsertAll starts an INSERT of every entity in xs, in as few statements as
// the dialect's parameter limit allows. The entity's hooks (Hooks) run once,
// over all of xs. Exec fills defaults and the tenant into xs[i], the caller's
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
// Update or DoNothing already chained stays. On MySQL, ON DUPLICATE KEY UPDATE resolves a conflict on any
// unique key.
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

// Exec runs the insert.
//
// The entity's hooks (Hooks) run first, around all that follows; their next
// does the rest. An InsertAll of nothing runs nothing.
//
// Exec then fills, in each entity, every zero field whose column has Default or
// DefaultFunc (an UpdateDefault fills on update only). On a tenant-scoped
// entity it then sets a zero tenant field to the context's tenant, and refuses
// a field set to another one (ErrTenantMismatch) or, under WithAllTenants, left
// at zero (ErrNoTenant). Then the validators run and the policy's Insert is
// asked; every entity is prepared before anything is sent.
//
// Under WithTenant, an upsert that updates must name the tenant column in
// OnConflict (ErrTenantUpsert); on MySQL, where any unique key can conflict,
// every unique key must hold it. Under WithAllTenants, which reaches every
// tenant's rows anyway, neither is required. Its update branch gets the
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
	if len(q.meta.info.hooks) > 0 {
		return mutate(ctx, q.db, q.meta.info, q.mutation(false), func(ctx context.Context, mu *mutation) error { return q.send(ctx, mu) })
	}
	var mu mutation
	return q.send(ctx, &mu)
}

// SQL returns the first statement and arguments Exec would send for ctx, or
// the error Exec would fail with before sending it; it sends nothing, and
// returns "" for an InsertAll of nothing. It runs every stage Exec runs
// before execution: like Exec, it runs the entity's hooks, fills defaults and
// the tenant into the caller's structs (the *E or the []E you passed, which
// it changes although it sends nothing; the generated key is the one field it
// leaves), then runs the validators, asks the policy's Insert and runs the
// Rewrite hooks, the scope and rendering. So the
// hooks and the policy run, and can tell from IsDryRun(ctx), or a Rewrite
// hook from Statement.DryRun, that the statement will not be sent. Around
// hooks, which wrap execution, do not run. A write a hook makes under ctx is
// refused with ErrDryRun, as in UpdateQuery.SQL. An InsertAll too large for one
// statement gives the first. InTx is set when Exec sends the statement in a
// transaction: inside one, for an InsertAll too large for one statement, and
// for one whose update branch gets the policy's conditions.
func (q InsertQuery[E]) SQL[_ Lazy[E]](ctx context.Context) (string, []any, error) {
	ctx = dryRunContext(ctx) // first, so that every hook sees it
	if err := q.check(); err != nil {
		return "", nil, err
	}
	if q.one == nil && len(q.many) == 0 {
		return "", nil, nil
	}
	if len(q.meta.info.hooks) > 0 {
		mu := q.mutation(true)
		if err := mutate(ctx, q.db, q.meta.info, mu, func(ctx context.Context, mu *mutation) error { return q.render(ctx, mu) }); err != nil && !dryRefused(mu, err) {
			return "", nil, err
		}
		return mu.sql, mu.args, nil
	}
	mu := mutation{dry: true}
	if err := q.render(ctx, &mu); err != nil {
		return "", nil, err
	}
	return mu.sql, mu.args, nil
}

// mutation is the insert as its entity's hooks see it: its rows.
func (q InsertQuery[E]) mutation[_ Lazy[E]](dry bool) *mutation {
	mu := &mutation{op: OpInsert, dry: dry}
	if q.one != nil {
		mu.rows = []unsafe.Pointer{unsafe.Pointer(q.one)}
		return mu
	}
	mu.rows = make([]unsafe.Pointer, len(q.many))
	for i := range q.many {
		mu.rows[i] = unsafe.Pointer(&q.many[i])
	}
	return mu
}

// send is Exec's work inside the hooks, their next: it prepares every row
// (defaults, tenant, the validators and the
// policy's Insert) and sends the statements. It sets mu.affected only when
// they all succeeded, so that Mutation.Affected is 0 after an error whatever
// order insertOne sets Affected and checks an upsert in.
func (q InsertQuery[E]) send[_ Lazy[E]](ctx context.Context, mu *mutation) error {
	var res Result
	var err error
	if q.one != nil {
		err = insertOne(ctx, q.db, q.meta, q.one, q.conflict, &res)
	} else {
		err = insertMany(ctx, q.db, q.meta, q.many, q.conflict, &res)
	}
	if err == nil {
		mu.affected = res.Affected
	}
	return err
}

// render is SQL's work inside the hooks: it prepares the rows as send does,
// and renders the first statement into mu.sql and mu.args.
func (q InsertQuery[E]) render[_ Lazy[E]](ctx context.Context, mu *mutation) error {
	var st Statement
	if q.one != nil {
		var err error
		if st, _, err = oneStatement(ctx, q.db, q.meta, q.one, q.conflict); err != nil {
			return err
		}
	} else {
		cols, per, err := prepareMany(ctx, q.db, q.meta, q.many)
		if err != nil {
			return err
		}
		inTx := q.db.inTx || len(q.many) > per || conditionedUpsert(ctx, q.db, q.meta.info, q.conflict, len(q.many))
		st = batch(q.meta, q.many[:min(per, len(q.many))], cols, q.conflict, inTx)
	}
	var err error
	mu.sql, mu.args, err = q.db.dryRun(ctx, &st)
	return err
}

// check refuses an insert that cannot be built: invalid metadata, Insert of
// a nil pointer, an upsert without OnConflict's columns.
func (q InsertQuery[E]) check[_ Lazy[E]]() error {
	if q.meta.err != nil {
		return q.meta.err
	}
	if q.single && q.one == nil {
		return fmt.Errorf("velox: %s: Insert of a nil pointer", q.meta.info.Name)
	}
	if q.conflict != nil && len(q.conflict.Columns) == 0 {
		return fmt.Errorf("velox: %s: an upsert needs OnConflict with the columns of a unique key", q.meta.info.Name)
	}
	if q.conflict != nil && q.meta.info.immutable {
		for _, c := range q.conflict.Update {
			if col := &q.meta.info.Columns[c]; col.Immutable {
				return fmt.Errorf("velox: %s: %s: %w", q.meta.info.Name, col.Name, ErrImmutable)
			}
		}
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
// number that is not Nillable.
func fillDefaults[E any](m *Meta[E], p *E) {
	for i := range m.info.Columns {
		fillDefault(&m.info.Columns[i], m.addr(p, i))
	}
}

// fillDefault sets the field that ptr points to as column c's Default or
// DefaultFunc says. A DefaultFunc returns the element type of a pointer field
// (velox-gen's Nillable), which is stored through a new pointer. It is not
// generic, so that the reflect.Value it uses is not compiled into every
// package that inserts.
func fillDefault(c *ColumnInfo, ptr any) {
	v := reflect.ValueOf(ptr).Elem()
	switch {
	case c.HasDefault && v.IsZero():
		v.Set(defaultValue(c.Default))
	case c.DefaultFunc != nil && v.IsZero():
		d := reflect.ValueOf(c.DefaultFunc())
		if t := v.Type(); t.Kind() == reflect.Pointer && d.Type() != t {
			p := reflect.New(t.Elem())
			p.Elem().Set(d)
			d = p
		}
		v.Set(d)
	}
}

// defaultValue is a column's static default as a reflect.Value. A pointer
// default is copied, so entities never share the pointee.
func defaultValue(d any) reflect.Value {
	v := reflect.ValueOf(d)
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		p := reflect.New(v.Type().Elem())
		p.Elem().Set(v.Elem())
		return p
	}
	return v
}

// generatedKey is the index of a single integer primary key column holding
// zero in *p, which the database assigns; -1 when there is none.
func generatedKey[E any](m *Meta[E], p *E) int {
	if len(m.info.PrimaryKey) != 1 {
		return -1
	}
	k := m.info.PrimaryKey[0]
	if zeroInt(m.addr(p, k)) {
		return k
	}
	return -1
}

// zeroInt reports whether ptr points to an integer holding zero.
func zeroInt(ptr any) bool {
	v := reflect.ValueOf(ptr).Elem()
	return (v.CanInt() || v.CanUint()) && v.IsZero()
}

// insertColumns lists the columns an INSERT writes: every column but skip.
func insertColumns(in *EntityInfo, skip int) []int {
	cols := make([]int, 0, len(in.Columns))
	for i := range in.Columns {
		if i != skip {
			cols = append(cols, i)
		}
	}
	return cols
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
func oneStatement[E any](ctx context.Context, db *DB, m *Meta[E], p *E, cf *ConflictClause) (Statement, int, error) {
	fillDefaults(m, p)
	if err := prepareRow(ctx, m.info, unsafe.Pointer(p)); err != nil {
		return Statement{}, -1, err
	}
	gen := generatedKey(m, p)
	cols := insertColumns(m.info, gen)
	if len(cols) == 0 {
		return Statement{}, -1, errNothingToInsert(m.info)
	}
	if err := validateRow(m.info, unsafe.Pointer(p), cols); err != nil {
		return Statement{}, -1, err
	}
	if err := insertAllowed(ctx, m.info, unsafe.Pointer(p)); err != nil {
		return Statement{}, -1, err
	}
	st := Statement{Op: OpInsert, Entity: m.info, Columns: cols, Rows: [][]any{rowValues(m, p, cols)}, Conflict: cf, InTx: db.inTx}
	if gen >= 0 && db.d != MySQL {
		st.Returning = []int{gen}
	}
	return st, gen, nil
}

// insertOne inserts the entity at p and sets out.Affected.
func insertOne[E any](ctx context.Context, db *DB, m *Meta[E], p *E, cf *ConflictClause, out *Result) error {
	st, gen, err := oneStatement(ctx, db, m, p, cf)
	if err != nil {
		return err
	}
	if gen < 0 {
		res, err := execWrite(ctx, db, &st)
		if err != nil {
			return err
		}
		out.Affected = res.affected
		return checkUpserted(&st, res.affected)
	}
	if db.d != MySQL {
		var back int64 // the rows that came back
		read := func(r rows) error {
			if !r.Next() {
				return nil
			}
			back = 1
			// A hook may have redirected Returning: then the row does not
			// hold the key.
			if len(st.Returning) == 1 && st.Returning[0] == gen {
				return r.Scan(scanDst(m.info, gen, m.addr(p, gen)))
			}
			return nil
		}
		if db.hooked() {
			err = db.run(ctx, &st, read, nil)
		} else {
			err = db.plain(ctx, &st, read, nil)
		}
		if err != nil {
			return err
		}
		out.Affected = back
		if len(st.Returning) == 0 {
			// Without RETURNING (a hook dropped it), no row tells nothing.
			return nil
		}
		return checkUpserted(&st, back)
	}
	res, err := execWrite(ctx, db, &st)
	if err != nil {
		return err
	}
	out.Affected = res.affected
	// st.Conflict, not cf: a rewrite hook may have added or removed the clause.
	if st.Conflict == nil && res.hasLastID && res.affected == 1 {
		return setGeneratedKey(m, p, gen, res.lastID)
	}
	return nil
}

// prepareRow readies the row at p, of entity in, to be inserted, once its
// defaults are filled: it fills or checks the tenant column. The validators,
// then the policy's Insert (insertAllowed), run after it. It changes the
// caller's entity, and the changes stay when the insert then fails.
func prepareRow(ctx context.Context, in *EntityInfo, p unsafe.Pointer) error {
	if in.Tenant >= 0 {
		id, all := tenantOf(ctx)
		return fillTenant(in, p, id, all)
	}
	return nil
}

// insertAllowed asks the policy's Insert about the row of in at p, prepared
// and validated, unless WithoutPolicy.
func insertAllowed(ctx context.Context, in *EntityInfo, p unsafe.Pointer) error {
	if pol := in.policy; pol != nil && pol.insert != nil && policyBypass(ctx) == "" {
		if err := pol.insert(ctx, p); err != nil {
			return &PolicyError{Entity: in.Name, Op: OpInsert, Err: err}
		}
	}
	return nil
}

// tenantField returns the tenant field of the row of in at p.
func tenantField(in *EntityInfo, p unsafe.Pointer) reflect.Value {
	c := &in.Columns[in.Tenant]
	return reflect.NewAt(GoType(*c), unsafe.Add(p, c.off)).Elem()
}

// fillTenant sets the row's zero tenant field to the context's tenant id, and
// checks one already set. Under WithAllTenants (all) the row must name its
// tenant.
func fillTenant(in *EntityInfo, p unsafe.Pointer, id any, all string) error {
	if id != nil {
		if err := checkTenantValue(in, id); err != nil {
			return err
		}
		if v := tenantField(in, p); v.IsZero() {
			v.Set(reflect.ValueOf(id))
			return nil
		}
	}
	return checkRowTenant(in, p, id, all, "the row has")
}

// checkRowTenant checks the row's tenant field against the context's tenant id,
// whose type fillTenant checked: under WithTenant the field must hold id, under
// WithAllTenants (all) it must not be zero, and with neither the row has no
// tenant. who says what put the value in the field.
func checkRowTenant(in *EntityInfo, p unsafe.Pointer, id any, all, who string) error {
	col := in.Columns[in.Tenant].Name
	v := tenantField(in, p)
	switch {
	case all != "":
		if v.IsZero() {
			return fmt.Errorf("velox: %s: %w: under WithAllTenants the row names its tenant, and its %s is zero", in.Name, ErrNoTenant, col)
		}
		return nil
	case id == nil:
		return fmt.Errorf("velox: %s: %w", in.Name, ErrNoTenant)
	case !v.Equal(reflect.ValueOf(id)):
		return fmt.Errorf("velox: %s: %w: %s %s %v, the tenant is %v", in.Name, ErrTenantMismatch, who, col, v, id)
	}
	return nil
}

// errHiddenRow is why an upsert that affected fewer rows than it sent is
// denied: the policy's condition kept a conflicting row from being updated.
var errHiddenRow = errors.New("the existing row is hidden from this upsert")

// checkUpserted denies an upsert whose update branch the scope conditioned on
// the policy, when it affected fewer rows than it sent: a conflicting row the
// policy hides was neither inserted nor updated.
func checkUpserted(st *Statement, affected int64) error {
	if cf := st.Conflict; cf != nil && cf.policy && affected < int64(len(st.Rows)) {
		return &PolicyError{Entity: st.Entity.Name, Op: OpUpdate, Err: errHiddenRow}
	}
	return nil
}

func errNothingToInsert(in *EntityInfo) error {
	return fmt.Errorf("velox: %s: nothing to insert: the only column is the generated key", in.Name)
}

// setGeneratedKey stores id in the integer key field gen, refusing a value
// the field cannot hold.
func setGeneratedKey[E any](m *Meta[E], p *E, gen int, id int64) error {
	return setKey(m.info, gen, m.addr(p, gen), id)
}

// setKey stores id in the integer field ptr points to, column gen of in. It
// is not generic for the reason given on fillDefault.
func setKey(in *EntityInfo, gen int, ptr any, id int64) error {
	v := reflect.ValueOf(ptr).Elem()
	if v.CanInt() {
		if v.OverflowInt(id) {
			return fmt.Errorf("velox: %s: LastInsertId %d overflows the key field %s", in.Name, id, in.Columns[gen].Field)
		}
		v.SetInt(id)
		return nil
	}
	if id < 0 || v.OverflowUint(uint64(id)) {
		return fmt.Errorf("velox: %s: LastInsertId %d overflows the key field %s", in.Name, id, in.Columns[gen].Field)
	}
	v.SetUint(uint64(id))
	return nil
}

// prepareMany prepares every entity in xs, as InsertAll's Exec does, and
// returns the columns to insert and the most rows one statement holds.
func prepareMany[E any](ctx context.Context, db *DB, m *Meta[E], xs []E) (cols []int, per int, err error) {
	for i := range xs {
		fillDefaults(m, &xs[i])
		if err := prepareRow(ctx, m.info, unsafe.Pointer(&xs[i])); err != nil {
			return nil, 0, err
		}
	}
	gen := generatedKey(m, &xs[0])
	for i := range xs {
		if (generatedKey(m, &xs[i]) >= 0) != (gen >= 0) {
			return nil, 0, fmt.Errorf("velox: %s: InsertAll needs every entity's key set, or every key left at zero", m.info.Name)
		}
	}
	cols = insertColumns(m.info, gen)
	if len(cols) == 0 {
		return nil, 0, errNothingToInsert(m.info)
	}
	for i := range xs {
		if err := validateRow(m.info, unsafe.Pointer(&xs[i]), cols); err != nil {
			return nil, 0, err
		}
	}
	for i := range xs {
		if err := insertAllowed(ctx, m.info, unsafe.Pointer(&xs[i])); err != nil {
			return nil, 0, err
		}
	}
	return cols, max(1, maxParams[db.d]/len(cols)), nil
}

// batch is the statement that inserts xs, prepared, in one statement.
func batch[E any](m *Meta[E], xs []E, cols []int, cf *ConflictClause, inTx bool) Statement {
	st := Statement{Op: OpInsert, Entity: m.info, Columns: cols, Conflict: cf, InTx: inTx, Rows: make([][]any, 0, len(xs))}
	for i := range xs {
		st.Rows = append(st.Rows, rowValues(m, &xs[i], cols))
	}
	return st
}

// insertMany inserts every entity in xs and sets out.Affected.
func insertMany[E any](ctx context.Context, db *DB, m *Meta[E], xs []E, cf *ConflictClause, out *Result) error {
	if len(xs) == 0 {
		return nil
	}
	// Before the batches' Tx, which would fail as the transaction's: the
	// error names the entity on every path. The dry run first, as run and
	// plain check it.
	if IsDryRun(ctx) {
		return fmt.Errorf("velox: %s: %w", m.info.Name, ErrDryRun)
	}
	if db.foreign(ctx) {
		return fmt.Errorf("velox: %s: %w", m.info.Name, ErrOutsideTx)
	}
	cols, per, err := prepareMany(ctx, db, m, xs)
	if err != nil {
		return err
	}
	if len(xs) <= per && !conditionedUpsert(ctx, db, m.info, cf, len(xs)) {
		n, err := insertBatches(ctx, db, m, xs, cols, per, cf)
		if err == nil {
			out.Affected = n
		}
		return err
	}
	var n int64 // in the transaction's closure: only this path pays for it
	err = db.Tx(ctx, func(ctx context.Context, db *DB) error {
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
// returns the rows they affected.
func insertBatches[E any](ctx context.Context, db *DB, m *Meta[E], xs []E, cols []int, per int, cf *ConflictClause) (int64, error) {
	var affected int64
	for start := 0; start < len(xs); start += per {
		st := batch(m, xs[start:min(start+per, len(xs))], cols, cf, db.inTx)
		res, err := execWrite(ctx, db, &st)
		if err != nil {
			return 0, err
		}
		if err := checkUpserted(&st, res.affected); err != nil {
			return 0, err
		}
		affected += res.affected
	}
	return affected, nil
}

// conditionedUpsert predicts, before the scope runs, whether it will give
// the update branch of an InsertAll of n rows of entity in the policy's Read
// or Write conditions. A row those conditions hide fails the upsert once the
// statement has written the others, so such an InsertAll of several rows runs
// in a transaction, which the failure rolls back. The scope conditions an
// upsert that updates, on any dialect but MySQL (which refuses it), of an
// entity with a Read or Write policy, unless WithoutPolicy.
func conditionedUpsert(ctx context.Context, db *DB, in *EntityInfo, cf *ConflictClause, n int) bool {
	return n > 1 && cf != nil && !cf.DoNothing && len(cf.Update) > 0 && db.d != MySQL &&
		in.policy != nil && (in.policy.read != nil || in.policy.write != nil) && policyBypass(ctx) == ""
}
