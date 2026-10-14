package velox

import (
	"context"
	"fmt"
	"reflect"
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
// Exec changes *x: it fills defaults and reads the generated key back, and it
// leaves those changes in place even when the insert then fails.
func (db *DB) Insert[E any, PE Entity[E]](x PE) InsertQuery[E] {
	return InsertQuery[E]{db: db, meta: x.VeloxMeta(), one: (*E)(x), single: true}
}

// InsertAll starts an INSERT of every entity in xs, in as few statements as
// the dialect's parameter limit allows. Exec fills defaults into xs[i], the
// caller's own slice elements, and leaves them filled even when the insert
// then fails.
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
// or DefaultFunc, and every column with OnUpdate. An integer primary key left
// at zero is not sent: the database assigns it. Insert reads that key back
// into *x: with RETURNING on PostgreSQL and SQLite, from LastInsertId on MySQL
// for an insert without OnConflict. After a conflict that kept the existing
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
	gen := generatedKey(m, p)
	cols := insertColumns(m.info, gen)
	if len(cols) == 0 {
		return errNothingToInsert(m.info)
	}
	st := Statement{Op: OpInsert, Entity: m.info, Columns: cols, Rows: [][]any{rowValues(m, p, cols)}, Conflict: cf, InTx: db.inTx}
	if gen < 0 {
		_, err := execWrite(ctx, db, &st)
		return err
	}
	if db.d != MySQL {
		st.Returning = []int{gen}
		read := func(r rows) error {
			// A hook may have dropped or redirected Returning: then the
			// rows do not hold the key.
			if len(st.Returning) == 1 && st.Returning[0] == gen && r.Next() {
				return r.Scan(m.addr(p, gen))
			}
			return nil
		}
		if db.hooked() {
			return db.run(ctx, &st, read, nil)
		}
		return db.plain(ctx, &st, read, nil)
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
			if _, err := execWrite(ctx, db, &st); err != nil {
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
