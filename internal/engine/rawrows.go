package engine

import (
	"context"
	"time"
)

// RawRows is a sequence of rows to range over, as an iter.Seq2[RawRow, error]
// is: velox's RawRows, which DB.Rows, Query.Rows and TableQuery.Rows return.
type RawRows func(yield func(RawRow, error) bool)

// RawRow is a row that RawRows yields: velox's RawRow. It is valid only in
// the loop body that receives it.
type RawRow struct{ r Rows }

// NewRawRow is r as a RawRow.
func NewRawRow(r Rows) RawRow { return RawRow{r} }

// Scan copies the row's columns, in the order the SQL returns them, into
// dst, as the driver's Rows.Scan does. No codec decodes them.
func (r RawRow) Scan(dst ...any) error { return r.r.Scan(dst...) }

// Columns returns the names of the row's columns, in the order the SQL
// returns them: a term named with As by its name, a column by its own, and
// any other term by the name the database gives it, which differs by
// dialect.
func (r RawRow) Columns() ([]string, error) { return resultColumns(r.r) }

// rowsRun is the state of one run of ProjectRows.
type rowsRun struct {
	st      Statement
	yield   func(RawRow, error) bool
	timed   bool // the DB has an Around hook: time the loop body
	yielded bool // a row was yielded
	stopped bool // the loop over the rows ended early
}

// readProjected yields each row until the loop over the rows ends.
func readProjected(it *rowsRun, r Rows) error {
	if it.yielded || it.stopped {
		return ErrIterAgain
	}
	for r.Next() {
		it.yielded = true
		it.st.handed = true
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
	defer AddSince(body, time.Now())
	return yield(row, nil)
}

// ProjectRows runs the statement prep returns, a projection's, as an
// iterator does, and yields its rows one at a time. prep runs each time the
// sequence is ranged over, and its error is yielded. The loop body runs
// inside the around hooks, and the statement's connection is held until the
// loop ends.
func ProjectRows(ctx context.Context, db *DB, prep func() (Statement, error)) RawRows {
	return func(yield func(RawRow, error) bool) {
		st, err := prep()
		if err != nil {
			yield(RawRow{}, err)
			return
		}
		st.iter = true
		var stopped bool
		if db.Hooked() {
			it := &rowsRun{st: st, yield: yield, timed: len(db.Cfg.Arounds) > 0}
			err = db.Run(ctx, &it.st, func(r Rows) error { return readProjected(it, r) }, nil)
			stopped = it.stopped
		} else {
			it := rowsRun{st: st, yield: yield}
			err = db.Plain(ctx, &it.st, func(r Rows) error { return readProjected(&it, r) }, nil)
			stopped = it.stopped
		}
		if err != nil && !stopped {
			yield(RawRow{}, err)
		}
	}
}
