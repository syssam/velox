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
	}
	return u
}

// All declares that the update has no Where on purpose and changes every
// row. The guardrails refuse an update with neither (sub-project 2).
func (u UpdateQuery[E]) All[_ Lazy[E]]() UpdateQuery[E] {
	u.st.AllRows = true
	return u
}

// statement is the UPDATE Exec and SQL run: Set's assignments, then one for
// every column with OnUpdate that Set did not name.
func (u UpdateQuery[E]) statement[_ Lazy[E]]() (Statement, error) {
	if u.meta.err != nil {
		return Statement{}, u.meta.err
	}
	if len(u.st.Set) == 0 {
		return Statement{}, fmt.Errorf("velox: %s: Update has no Set", u.meta.info.Name)
	}
	st := cloneStatement(&u.st)
	st.Set = withOnUpdate(u.meta.info, st.Set, nil)
	return st, nil
}

// Exec runs the update and returns the number of rows it changed.
func (u UpdateQuery[E]) Exec[_ Lazy[E]](ctx context.Context) (int64, error) {
	st, err := u.statement()
	if err != nil {
		return 0, err
	}
	res, err := execWrite(ctx, u.db, &st)
	return res.affected, err
}

// SQL returns the statement and arguments Exec would send, without a
// database and before rewrite hooks; "" when the update cannot run.
func (u UpdateQuery[E]) SQL[_ Lazy[E]]() (string, []any) {
	st, err := u.statement()
	if err != nil {
		return "", nil
	}
	return render(u.db.d, &st)
}

// DeleteQuery is a DELETE of entity E, built by chaining and run by Exec.
type DeleteQuery[E any] struct {
	db   *DB
	meta *Meta[E]
	st   Statement
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
	}
	return d
}

// All declares that the delete has no Where on purpose and removes every
// row. The guardrails refuse a delete with neither (sub-project 2).
func (d DeleteQuery[E]) All[_ Lazy[E]]() DeleteQuery[E] {
	d.st.AllRows = true
	return d
}

// Exec runs the delete and returns the number of rows it removed.
func (d DeleteQuery[E]) Exec[_ Lazy[E]](ctx context.Context) (int64, error) {
	if d.meta.err != nil {
		return 0, d.meta.err
	}
	st := cloneStatement(&d.st)
	res, err := execWrite(ctx, d.db, &st)
	return res.affected, err
}

// SQL returns the statement and arguments Exec would send, without a
// database and before rewrite hooks.
func (d DeleteQuery[E]) SQL[_ Lazy[E]]() (string, []any) {
	if d.meta.err != nil {
		return "", nil
	}
	st := cloneStatement(&d.st)
	return render(d.db.d, &st)
}

// Save writes the named columns of *x to the row with x's primary key, and
// every column with OnUpdate, whose new value it also stores in *x. Its error
// wraps ErrNotFound when no row has that key. On MySQL, open the database with
// clientFoundRows=true: otherwise a row whose values did not change counts as
// not found.
func (db *DB) Save[E any, PE Entity[E]](ctx context.Context, x PE, cols ...Column[E]) error {
	m := x.VeloxMeta()
	if m.err != nil {
		return m.err
	}
	in := m.info
	if len(cols) == 0 {
		return fmt.Errorf("velox: %s: Save names no columns", in.Name)
	}
	p := (*E)(x)
	st := Statement{Op: OpUpdate, Entity: in, InTx: db.inTx}
	for _, c := range cols {
		i := int(c.column(nil))
		st.Set = append(st.Set, Assignment{Column: i, Op: AssignSet, Value: fieldValue(m, p, i)})
	}
	st.Set = withOnUpdate(in, st.Set, func(i int, v any) { setField(m, p, i, v) })
	for _, k := range in.PrimaryKey {
		st.Where = append(st.Where, ColumnEQ(k, fieldValue(m, p, k)))
	}
	res, err := execWrite(ctx, db, &st)
	if err != nil {
		return err
	}
	if res.affected == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, in.Name)
	}
	return nil
}

// withOnUpdate appends an assignment for every column with OnUpdate that set
// does not assign, passing each value to apply when it is not nil.
func withOnUpdate(info *EntityInfo, set []Assignment, apply func(col int, v any)) []Assignment {
	named := make(map[int]bool, len(set))
	for _, a := range set {
		named[a.Column] = true
	}
	out := slices.Clip(set)
	for i := range info.Columns {
		if f := info.Columns[i].OnUpdate; f != nil && !named[i] {
			v := f()
			if apply != nil {
				apply(i, v)
			}
			out = append(out, Assignment{Column: i, Op: AssignSet, Value: v})
		}
	}
	return out
}

// fieldValue is the value of column i's field in *p.
func fieldValue[E any](m *Meta[E], p *E, i int) any {
	return reflect.ValueOf(m.addr(p, i)).Elem().Interface()
}

// setField stores v in column i's field in *p.
func setField[E any](m *Meta[E], p *E, i int, v any) {
	reflect.ValueOf(m.addr(p, i)).Elem().Set(reflect.ValueOf(v))
}
