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
	many     []E
	conflict *ConflictClause
}

// Insert starts an INSERT of the entity x points to:
//
//	db.Insert(&inv).Exec(ctx)
func (db *DB) Insert[E any, PE Entity[E]](x PE) InsertQuery[E] {
	return InsertQuery[E]{db: db, meta: x.VeloxMeta(), one: (*E)(x)}
}

// InsertAll starts an INSERT of every entity in xs, in as few statements as
// the dialect's parameter limit allows.
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
// resolves; follow it with Update or DoNothing (without either it does
// nothing). On MySQL, ON DUPLICATE KEY UPDATE resolves a conflict on any
// unique key.
func (q InsertQuery[E]) OnConflict[_ Lazy[E]](cols ...Column[E]) InsertQuery[E] {
	q.conflict = &ConflictClause{Columns: columnIndexes(cols)}
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
// row, the key stays zero.
//
// InsertAll reads nothing back. Give every entity its key, or leave every key
// at zero for the database to assign. Outside a transaction, an InsertAll that
// needs several statements runs them in one transaction.
func (q InsertQuery[E]) Exec[_ Lazy[E]](ctx context.Context) error {
	if q.meta.err != nil {
		return q.meta.err
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
		c := &m.info.Columns[i]
		v := reflect.ValueOf(m.addr(p, i)).Elem()
		switch {
		case c.OnUpdate != nil:
			v.Set(reflect.ValueOf(c.OnUpdate()))
		case c.HasDefault && v.IsZero():
			v.Set(reflect.ValueOf(c.Default))
		case c.DefaultFunc != nil && v.IsZero():
			v.Set(reflect.ValueOf(c.DefaultFunc()))
		}
	}
}

// generatedKey is the index of a single integer primary key column holding
// zero in *p, which the database assigns; -1 when there is none.
func generatedKey[E any](m *Meta[E], p *E) int {
	if len(m.info.PrimaryKey) != 1 {
		return -1
	}
	k := m.info.PrimaryKey[0]
	v := reflect.ValueOf(m.addr(p, k)).Elem()
	if (v.CanInt() || v.CanUint()) && v.IsZero() {
		return k
	}
	return -1
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
	st := Statement{Op: OpInsert, Entity: m.info, Columns: cols, Rows: [][]any{rowValues(m, p, cols)}, Conflict: cf, InTx: db.inTx}
	if gen < 0 {
		_, err := execWrite(ctx, db, &st)
		return err
	}
	if db.d != MySQL {
		st.Returning = []int{gen}
		read := func(r rows) error {
			if r.Next() {
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
	if cf == nil && res.hasLastID && res.affected == 1 {
		v := reflect.ValueOf(m.addr(p, gen)).Elem()
		if v.CanInt() {
			v.SetInt(res.lastID)
		} else {
			v.SetUint(uint64(res.lastID))
		}
	}
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
	if len(xs) <= per || db.inTx {
		return batches(db)
	}
	return db.Tx(ctx, batches)
}
