package velox

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/syssam/velox/internal/engine"
)

// projection returns q's Projection to extend: a copy of it, whose slices
// may be appended to, or a new one. A Query is a value, so a chaining method
// never changes the one it was called on.
func (q Query[E]) projection[_ Lazy[E]]() *engine.Projection {
	p := engine.Projection{}
	if q.s.St.Project != nil {
		p = *q.s.St.Project
	}
	p.Terms, p.GroupBy, p.Having = slices.Clip(p.Terms), slices.Clip(p.GroupBy), slices.Clip(p.Having)
	return &p
}

// needProject records the error of a GroupBy or Having on a query that has
// no Select.
func (q Query[E]) needProject[_ Lazy[E]](what string) Query[E] {
	if q.s.St.Project == nil && q.s.Err == nil {
		q.s.Err = fmt.Errorf("velox: %s: %s needs Select: it groups or filters the terms Select reads", q.meta.Info.Name, what)
	}
	return q
}

// Select returns the query reading terms in place of E's columns: a column
// handle, an expression (Calc, SelectRaw) or an aggregate of either (a
// handle's Sum, Avg, Min, Max, Count and CountDistinct, and CountAll), one
// result column each, in the order given. Select again adds more terms. Read
// the rows with Rows, which scans them by position; All, One, Count and the
// other readers of entities refuse a projection.
//
//	rows := db.Query[veloxgen.Invoice]().
//		Where(I.Paid.EQ(true)).
//		Select(I.OrgID, I.Total.Sum(), velox.CountAll[veloxgen.Invoice]()).
//		GroupBy(I.OrgID).
//		Having(velox.Expr[veloxgen.Invoice]("? > ?", I.Total.Sum(), 100)).
//		OrderBy(I.Total.Sum().Desc()).
//		Limit(10).
//		Rows(ctx)
//
// A projection is a read of E like any other: Where, the Read policy, the
// scope, the interceptors, Limit and the row cap apply to it, and it needs
// a Limit or AllowUnbounded. It cannot be combined with Columns, Omit, With,
// Join, After, a lock or a per-owner read.
func (q Query[E]) Select[_ Lazy[E]](ts ...Selectable[E]) Query[E] {
	p := q.projection()
	for _, t := range ts {
		p.Terms = append(p.Terms, termOf[E](t))
	}
	q.s.St.Project = p
	return q
}

// SelectRaw is Select(Calc(text, args...)): one expression of SQL text as a
// result column, written as Expr's text is, so a ? is a column handle, a Term
// or a bound value. Unlike Raw, it is guarded and keeps the Read policy, the
// scope and the interceptors: the text can hold a function, an operator and
// CASE, and not a subquery or the end of the statement.
//
//	db.Query[veloxgen.Invoice]().Limit(10).
//		SelectRaw("EXTRACT(MONTH FROM ?)", I.CreatedAt).Rows(ctx)
func (q Query[E]) SelectRaw[_ Lazy[E]](text string, args ...any) Query[E] {
	return q.Select(Calc[E](text, args...))
}

// GroupBy returns the query grouped by terms: the rows that agree on all of
// them are one result row. It needs Select, and each plain term Select
// reads must be among them, or the database refuses the statement.
func (q Query[E]) GroupBy[_ Lazy[E]](ts ...Selectable[E]) Query[E] {
	if q = q.needProject("GroupBy"); q.s.St.Project == nil {
		return q
	}
	p := q.projection()
	for _, t := range ts {
		p.GroupBy = append(p.GroupBy, termOf[E](t))
	}
	q.s.St.Project = p
	return q
}

// Having returns the query keeping the groups where all of ps hold: an Expr
// of aggregate terms. It needs Select.
func (q Query[E]) Having[_ Lazy[E]](ps ...Pred[E]) Query[E] {
	if q = q.needProject("Having"); q.s.St.Project == nil {
		return q
	}
	p := q.projection()
	for _, h := range ps {
		p.Having = append(p.Having, Predicate(h))
		if q.s.Err == nil {
			if err := engine.PredErr(engine.PredNode(Predicate(h))); err != nil {
				q.s.Err = err
			}
		}
	}
	q.s.St.Project = p
	return q
}

// errProjectRows is the refusal of a reader of entities on a projection.
func errProjectRows(entity string) error {
	return fmt.Errorf("velox: %s: a projection reads terms, not entities: read it with Rows", entity)
}

// rowsRun is the state of one Rows.
type rowsRun struct {
	st      Statement
	yield   func(RawRow, error) bool
	timed   bool // the DB has an Around hook: time the loop body
	yielded bool // a row was yielded
	stopped bool // the loop over Rows ended early
}

// readRows yields each row until the loop over Rows ends.
func readRows(it *rowsRun, r engine.Rows) error {
	if it.yielded || it.stopped {
		return engine.ErrIterAgain
	}
	for r.Next() {
		it.yielded = true
		engine.SetStmtHanded(&it.st)
		if !yieldRaw(it.yield, RawRow{r}, it.timed, &it.st.Result.LoopBody) {
			it.stopped = true
			return nil
		}
	}
	return nil
}

// yieldRaw yields row and, when timed, adds the time the loop body took to
// *body, also when the loop body panics.
func yieldRaw(yield func(RawRow, error) bool, row RawRow, timed bool, body *time.Duration) bool {
	if !timed {
		return yield(row, nil)
	}
	defer engine.AddSince(body, time.Now())
	return yield(row, nil)
}

// Rows runs the query and yields the rows of its Select one at a time,
// scanned by position with RawRow.Scan: the terms in the order Select gave
// them. Only the current row is held. A query with no Select fails with an
// error.
//
// It runs as Iter does: the loop body runs inside the around hooks, the
// statement's connection is held until the loop ends, and on a pool of one
// connection a statement in the body fails. Rewrite hooks see the
// statement's Project and may not change it. SQL shows the statement.
func (q Query[E]) Rows[_ Lazy[E]](ctx context.Context) RawRows {
	return func(yield func(RawRow, error) bool) {
		st, err := q.projectStmt()
		if err != nil {
			yield(RawRow{}, err)
			return
		}
		engine.SetStmtIter(&st)
		var stopped bool
		if q.s.DB.Hooked() {
			it := &rowsRun{st: st, yield: yield, timed: len(q.s.DB.Cfg.Arounds) > 0}
			err = q.s.DB.Run(ctx, &it.st, func(r engine.Rows) error { return readRows(it, r) }, nil)
			stopped = it.stopped
		} else {
			it := rowsRun{st: st, yield: yield}
			err = q.s.DB.Plain(ctx, &it.st, func(r engine.Rows) error { return readRows(&it, r) }, nil)
			stopped = it.stopped
		}
		if err != nil && !stopped {
			yield(RawRow{}, err)
		}
	}
}

// projectStmt is the statement Rows sends: the query's, which must be a
// projection and no more than one.
func (q Query[E]) projectStmt[_ Lazy[E]]() (Statement, error) {
	name := q.meta.Info.Name
	switch {
	case q.meta.Err != nil:
		return Statement{}, q.meta.Err
	case q.s.Err != nil:
		return Statement{}, q.s.Err
	case q.s.St.Project == nil:
		return Statement{}, fmt.Errorf("velox: %s: Rows reads a projection: add Select", name)
	case len(q.s.Loads) > 0:
		return Statement{}, fmt.Errorf("velox: %s: a projection cannot be read with With", name)
	case q.s.After != nil:
		return Statement{}, fmt.Errorf("velox: %s: a projection cannot be read After a row", name)
	}
	return engine.PrepareStmt(&q.s.St, q.meta.Info, nil)
}
