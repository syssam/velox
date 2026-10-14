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
// Exec changes *x: it fills defaults and the tenant, runs the BeforeInsert
// hooks and reads the generated key back, and it leaves those changes in
// place even when the insert then fails.
func (db *DB) Insert[E any, PE Entity[E]](x PE) InsertQuery[E] {
	return InsertQuery[E]{db: db, meta: x.VeloxMeta(), one: (*E)(x), single: true}
}

// InsertAll starts an INSERT of every entity in xs, in as few statements as
// the dialect's parameter limit allows. Exec fills defaults and the tenant
// into xs[i], the caller's own slice elements, runs the BeforeInsert hooks on
// them, and leaves those changes in place even when the insert then fails.
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
// Exec first fills, in each entity, every zero field whose column has Default
// or DefaultFunc, and every column with OnUpdate. On a tenant-scoped entity
// it then sets a zero tenant field to the context's tenant, and refuses a
// field set to another one (ErrTenantMismatch) or, under AllTenants, left at
// zero (ErrNoTenant). Then the BeforeInsert hooks run, the tenant is checked
// again, and the policy's Insert is asked; every entity is prepared before
// anything is sent.
//
// An upsert that updates must name the tenant column in OnConflict
// (ErrTenantUpsert); on MySQL, where any unique key can conflict, every
// unique key must hold it. Its update branch gets the policy's Read and Write
// conditions, on the existing row; an upsert that changed fewer rows than it
// sent, because the conditions hid a conflicting row, fails with a
// *PolicyError. MySQL cannot apply them, so there such an upsert of an entity
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
// one it runs them under a savepoint.
func (q InsertQuery[E]) Exec[_ Lazy[E]](ctx context.Context) error {
	if q.meta.err != nil {
		return q.meta.err
	}
	if q.single && q.one == nil {
		return fmt.Errorf("velox: %s: Insert of a nil pointer", q.meta.info.Name)
	}
	if q.conflict != nil && len(q.conflict.Columns) == 0 {
		return fmt.Errorf("velox: %s: an upsert needs OnConflict with the columns of a unique key", q.meta.info.Name)
	}
	if q.one != nil {
		return insertOne(ctx, q.db, q.meta, q.one, q.conflict)
	}
	return insertMany(ctx, q.db, q.meta, q.many, q.conflict)
}

// fillDefaults sets, in *p, every zero field whose column has Default or
// DefaultFunc, and every column with OnUpdate.
func fillDefaults[E any](m *Meta[E], p *E) {
	for i := range m.info.Columns {
		fillDefault(&m.info.Columns[i], m.addr(p, i))
	}
}

// fillDefault sets the field that ptr points to as column c's Default,
// DefaultFunc or OnUpdate says. It is not generic, so that the reflect.Value
// it uses is not compiled into every package that inserts (design §3 rule 7).
func fillDefault(c *ColumnInfo, ptr any) {
	v := reflect.ValueOf(ptr).Elem()
	switch {
	case c.OnUpdate != nil:
		v.Set(reflect.ValueOf(c.OnUpdate()))
	case c.HasDefault && v.IsZero():
		v.Set(defaultValue(c.Default))
	case c.DefaultFunc != nil && v.IsZero():
		v.Set(reflect.ValueOf(c.DefaultFunc()))
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

func insertOne[E any](ctx context.Context, db *DB, m *Meta[E], p *E, cf *ConflictClause) error {
	fillDefaults(m, p)
	if err := prepareRow(ctx, m.info, unsafe.Pointer(p)); err != nil {
		return err
	}
	gen := generatedKey(m, p)
	cols := insertColumns(m.info, gen)
	if len(cols) == 0 {
		return errNothingToInsert(m.info)
	}
	st := Statement{Op: OpInsert, Entity: m.info, Columns: cols, Rows: [][]any{rowValues(m, p, cols)}, Conflict: cf, InTx: db.inTx}
	if gen < 0 {
		res, err := execWrite(ctx, db, &st)
		if err != nil {
			return err
		}
		return checkUpserted(&st, res.affected)
	}
	if db.d != MySQL {
		st.Returning = []int{gen}
		var back int64 // the rows that came back
		read := func(r rows) error {
			if !r.Next() {
				return nil
			}
			back = 1
			// A hook may have redirected Returning: then the row does not
			// hold the key.
			if len(st.Returning) == 1 && st.Returning[0] == gen {
				return r.Scan(m.addr(p, gen))
			}
			return nil
		}
		var err error
		if db.hooked() {
			err = db.run(ctx, &st, read, nil)
		} else {
			err = db.plain(ctx, &st, read, nil)
		}
		if err != nil || len(st.Returning) == 0 {
			// Without RETURNING (a hook dropped it), no row tells nothing.
			return err
		}
		return checkUpserted(&st, back)
	}
	res, err := execWrite(ctx, db, &st)
	if err != nil {
		return err
	}
	// st.Conflict, not cf: a rewrite hook may have added or removed the clause.
	if st.Conflict == nil && res.hasLastID && res.affected == 1 {
		return setGeneratedKey(m, p, gen, res.lastID)
	}
	return nil
}

// prepareRow readies the row at p, of entity in, to be inserted, once its
// defaults are filled: it fills or checks the tenant column, runs the
// BeforeInsert hooks, checks the tenant again on what they left, and asks the
// policy's Insert. It changes the caller's entity, and the changes stay when
// the insert then fails.
func prepareRow(ctx context.Context, in *EntityInfo, p unsafe.Pointer) error {
	var id any
	var all string
	if in.Tenant >= 0 {
		id, all = tenantOf(ctx)
		if err := fillTenant(in, p, id, all); err != nil {
			return err
		}
	}
	for _, h := range in.beforeInsert {
		if err := h(ctx, p); err != nil {
			return fmt.Errorf("velox: %s: before insert: %w", in.Name, err)
		}
	}
	if in.Tenant >= 0 && len(in.beforeInsert) > 0 {
		if err := checkRowTenant(in, p, id, all, "a BeforeInsert hook set"); err != nil {
			return err
		}
	}
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
// checks one already set. Under AllTenants (all) the row must name its
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

// checkRowTenant checks the row's tenant field against the context's tenant
// id, whose type fillTenant checked: under WithTenant the field must hold id,
// under AllTenants (all) it must not be zero, and with neither the row has no
// tenant. who says what put the value in the field.
func checkRowTenant(in *EntityInfo, p unsafe.Pointer, id any, all, who string) error {
	col := in.Columns[in.Tenant].Name
	v := tenantField(in, p)
	switch {
	case all != "":
		if v.IsZero() {
			return fmt.Errorf("velox: %s: %w: under AllTenants the row names its tenant, and its %s is zero", in.Name, ErrNoTenant, col)
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

func insertMany[E any](ctx context.Context, db *DB, m *Meta[E], xs []E, cf *ConflictClause) error {
	if len(xs) == 0 {
		return nil
	}
	for i := range xs {
		fillDefaults(m, &xs[i])
		if err := prepareRow(ctx, m.info, unsafe.Pointer(&xs[i])); err != nil {
			return err
		}
	}
	gen := generatedKey(m, &xs[0])
	for i := range xs {
		if (generatedKey(m, &xs[i]) >= 0) != (gen >= 0) {
			return fmt.Errorf("velox: %s: InsertAll needs every entity's key set, or every key left at zero", m.info.Name)
		}
	}
	cols := insertColumns(m.info, gen)
	if len(cols) == 0 {
		return errNothingToInsert(m.info)
	}
	per := max(1, maxParams[db.d]/len(cols))
	batches := func(db *DB) error {
		for start := 0; start < len(xs); start += per {
			end := min(start+per, len(xs))
			st := Statement{Op: OpInsert, Entity: m.info, Columns: cols, Conflict: cf, InTx: db.inTx, Rows: make([][]any, 0, end-start)}
			for i := start; i < end; i++ {
				st.Rows = append(st.Rows, rowValues(m, &xs[i], cols))
			}
			res, err := execWrite(ctx, db, &st)
			if err != nil {
				return err
			}
			if err := checkUpserted(&st, res.affected); err != nil {
				return err
			}
		}
		return nil
	}
	if len(xs) <= per {
		return batches(db)
	}
	return db.Tx(ctx, batches)
}
