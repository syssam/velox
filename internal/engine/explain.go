package engine

import (
	"context"
	"strings"
)

// explainSQL is what each dialect's EXPLAIN puts before a statement. MySQL's
// FORMAT=TREE is one column of text, as PostgreSQL's plan is; SQLite's
// EXPLAIN QUERY PLAN is four columns, the last the step's text.
var explainSQL = [...]string{
	Postgres: "EXPLAIN ",
	MySQL:    "EXPLAIN FORMAT=TREE ",
	SQLite:   "EXPLAIN QUERY PLAN ",
}

// explainRun is the state of one Explain.
type explainRun struct {
	st    Statement
	lines []string
}

// readPlan reads the plan's rows into e.lines, anew should an around hook
// retry. SQLite's rows are (id, parent, notused, detail); the others' one
// column of text.
func readPlan(e *explainRun, d Dialect, r Rows) error {
	e.lines = e.lines[:0]
	for r.Next() {
		var line string
		var err error
		if d == SQLite {
			var id, parent, unused int64
			err = r.Scan(&id, &parent, &unused, &line)
		} else {
			err = r.Scan(&line)
		}
		if err != nil {
			return err
		}
		e.lines = append(e.lines, line)
	}
	return nil
}

// Explain runs the query state q, of entity info, under the dialect's
// EXPLAIN and returns the plan's lines joined by newlines. The statement is
// the one All or Rows would send: the guard, the Rewrite hooks, the scope
// with the policies and the filters, the row cap and rendering all apply,
// and it runs through the Around hooks, so that WithLog masks its
// arguments as any statement's. q's With loads are not explained.
func Explain(ctx context.Context, q *QueryState, info *EntityInfo) (string, error) {
	if q.Err != nil {
		return "", q.Err
	}
	st, err := PrepareStmt(&q.St, info, q.After)
	if err != nil {
		return "", err
	}
	st.explain = true
	e := &explainRun{st: st}
	db := q.DB
	if db.Hooked() {
		err = db.Run(ctx, &e.st, func(r Rows) error { return readPlan(e, db.D, r) }, nil)
	} else {
		err = db.Plain(ctx, &e.st, func(r Rows) error { return readPlan(e, db.D, r) }, nil)
	}
	if err != nil {
		return "", err
	}
	return strings.Join(e.lines, "\n"), nil
}
