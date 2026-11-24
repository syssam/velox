package velox

import (
	"context"
	"fmt"
	"iter"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// Exec runs query, raw SQL that returns no rows, with args, and returns the
// number of rows it affected, as the driver counts them, or -1 when the
// driver could not report its count. reason says why the statement may skip
// the policies, a scope included; if it is empty or only white space, Exec
// fails before anything runs. The hooks see reason as given, untrimmed. See
// Raw for what raw SQL goes through, and what it skips.
//
// velox treats Exec as a write (OpRawExec), whatever its SQL: it goes to the
// primary, pins the session (WithSession), and fails with ErrDryRun under a
// dry run (IsDryRun). Its errors, and the Around hooks, name its statement
// "Exec". In a transaction SQL that would end it is refused (ErrTxControl): see
// DB.Tx.
//
//	n, err := db.Exec(ctx, "nightly cleanup", "DELETE FROM sessions WHERE expires_at < $1", now)
func (db *DB) Exec(ctx context.Context, reason, query string, args ...any) (int64, error) {
	return db.e.RawExec(ctx, reason, query, args)
}

// RawRows is what DB.Rows, Query.Rows and untyped.TableQuery.Rows return: a
// sequence of rows to range over, as an iter.Seq2[RawRow, error] is.
type RawRows = engine.RawRows

// RawRow is a row that RawRows yields. It is valid only in the loop body
// that receives it.
//
// Its methods:
//
//	// Scan copies the row's columns, in the order the SQL returns them, into
//	// dst, as the driver's Rows.Scan does. No codec decodes them.
//	Scan(dst ...any) error
//	// Columns returns the names of the row's columns, in the order the SQL
//	// returns them: a term named with As by its name, a column by its own,
//	// and any other term by the name the database gives it, which differs
//	// by dialect.
//	Columns() ([]string, error)
type RawRow = engine.RawRow

// Rows runs query, raw SQL that returns rows, with args, and yields its rows
// one at a time, for SQL whose rows are no entity's: a report, a CTE, a
// window function. reason says why it may skip the policies, as Raw's does;
// if it is empty or only white space, Rows yields that error and runs
// nothing.
//
//	for row, err := range db.Rows(ctx, "revenue report",
//		"SELECT org_id, SUM(total_cents) FROM invoices GROUP BY org_id") {
//		if err != nil {
//			return err
//		}
//		var org, total int64
//		if err := row.Scan(&org, &total); err != nil {
//			return err
//		}
//	}
//
// It runs as Raw's Iter does, on the connection the DB's statements use: in
// a transaction, the transaction's, where velox.Pool is nil; on a DB from
// OpenConn, OpenShards or veloxpgx, that DB's. It goes through the
// transaction check, the Around hooks (as OpRawQuery, its statement named
// "Rows"), routing (as a read), the SQL comment and the row cap. As with
// Query.Iter, the loop body runs inside the Around hooks, and on a pool of
// one connection a statement in the body fails.
func (db *DB) Rows(ctx context.Context, reason, query string, args ...any) RawRows {
	return func(yield func(RawRow, error) bool) {
		stopped, err := db.e.RawRows(ctx, reason, query, args, func(r engine.Rows) bool { return yield(engine.NewRawRow(r), nil) })
		if err != nil && !stopped {
			yield(RawRow{}, err)
		}
	}
}

// RawQuery is raw SQL whose rows scan into entity E, made by Raw and run by
// All, One or Iter. Like a Query it is a value, and running it does not
// change it.
type RawQuery[E any] struct {
	db      *DB
	meta    *metaState[E]
	reason  string
	query   string
	args    []any
	maxRows int
}

// Raw returns query, raw SQL with args, as a query whose rows scan into
// entity E. reason says why it may skip the policies, a scope included; if
// it is empty or only white space, running the query fails before anything
// is sent. The hooks see reason as given, untrimmed.
//
// Each column the SQL returns is scanned, by name, into E's field of that
// column name, through its codec when it has one; a column E has no field
// for is an error naming it, and so is a column returned twice. The names
// must match exactly, case included, so alias a computed column to the
// column name (SUM(total) AS total); PostgreSQL folds unquoted names to lower
// case, while MySQL and SQLite return them as written. The fields whose
// columns the SQL does not return keep their zero values, so an aggregate can
// scan into the entity it sums:
//
//	totals, err := db.Raw[veloxgen.Invoice]("revenue report",
//		"SELECT org_id, SUM(total_cents) AS total_cents FROM invoices GROUP BY org_id").All(ctx)
//
// Raw SQL is the escape hatch for row locks, CTEs and anything else the
// builder cannot express. velox applies no policy to it: scoping is the
// caller's, written into the SQL, and the Around hooks see reason as
// Statement.PolicyBypass. It goes
// through the transaction check, the Around hooks (as OpRawQuery),
// WithDeadlines, routing (as a read), the SQL comment and the row cap;
// Rewrite hooks and the entity's Hooks do not run, and it loads no relations.
// The SQL is sent as written, in the dialect's placeholders ($1 on
// PostgreSQL, ? elsewhere), and args are bound as given: no codec encodes
// them. With WithSQLComment, trailing semicolons and white space are trimmed
// from the SQL before the comment is appended, on a line of its own when the
// SQL's last line holds a -- comment. Raw has no SQL(ctx) method: its SQL is
// the caller's already.
//
// velox treats Raw as a read: it goes to a replica unless WithPrimaryReads or a
// pinned session says otherwise, and runs under a dry run (IsDryRun). Run a
// write that returns rows (UPDATE … RETURNING) in a transaction, or under
// WithPrimaryReads, and never under a dry run. In a transaction Raw counts as a
// write, since velox cannot tell one from a read: its commit pins the session
// (WithSession). Outside one, a write through Raw does not pin it. In a transaction
// SQL that would end it is refused (ErrTxControl): see DB.Tx.
//
// On a DB opened with OpenShards, Raw and Exec run on the shard the context
// names, as every statement does: WithShard's, else the one WithShardFor gives
// for the context. With neither they fail with ErrNoShard, before anything is sent.
func (db *DB) Raw[E any, PE Entity[E]](reason, query string, args ...any) RawQuery[E] {
	return RawQuery[E]{db: db, meta: stateOf(PE(nil).VeloxMeta()), reason: reason, query: query, args: args}
}

// MaxRows returns the query allowed to return up to n rows, in place of the
// DB's WithMaxRows cap; n <= 0 restores the DB's cap. velox cannot add a
// LIMIT to raw SQL, so the cap counts the rows read and fails the query with
// ErrTooManyRows at the row over it; the database may have computed more.
func (q RawQuery[E]) MaxRows[_ Lazy[E]](n int) RawQuery[E] {
	q.maxRows = max(n, 0)
	return q
}

// statement is the statement one run of the query sends, or why it cannot.
func (q RawQuery[E]) statement[_ Lazy[E]]() (Statement, error) {
	if q.meta.Err != nil {
		return Statement{}, q.meta.Err
	}
	if engine.NoReason(q.reason) {
		return Statement{}, engine.ErrNoReason(q.meta.Info.Name)
	}
	return Statement{Op: OpRawQuery, Entity: q.meta.Info, SQL: q.query, Args: q.args, MaxRows: q.maxRows,
		PolicyBypass: q.reason, InTx: q.db.e.InTx}, nil
}

// rawRun is the state of one run of a RawQuery: All's and One's rows go to
// out, Iter's to yield.
type rawRun[E any] struct {
	st      Statement
	m       *metaState[E]
	src     *engine.Source // the rows' Source
	limit   int            // One's: stop after this many rows; 0: read them all
	out     []E
	yield   func(E, error) bool // Iter's; nil for All and One
	timed   bool                // Iter's, on a DB with an Around hook: time the loop body
	yielded bool                // a row was yielded
	stopped bool                // the loop over Iter ended early
}

// readRaw reads r's rows by column name, anew should an Around hook retry.
func readRaw[E any](a *rawRun[E], r engine.Rows) error {
	if a.yield != nil && (a.yielded || a.stopped) {
		return engine.ErrIterAgain
	}
	cols, err := engine.RawColumns(a.m.Info, r)
	if err != nil {
		return err
	}
	dst := make([]any, len(cols))
	a.out = nil
	for r.Next() {
		if a.yield == nil {
			a.out = append(a.out, *new(E))
			if err := scanRow(r, a.m, cols, dst, &a.out[len(a.out)-1]); err != nil {
				return err
			}
			a.src.Put(a.m.Info, unsafe.Pointer(&a.out[len(a.out)-1]))
			if len(a.out) == a.limit {
				return nil
			}
			continue
		}
		var e E
		if err := scanRow(r, a.m, cols, dst, &e); err != nil {
			return err
		}
		a.src.Put(a.m.Info, unsafe.Pointer(&e)) // inlined: e stays on the stack
		a.yielded = true
		engine.SetStmtHanded(&a.st)
		if !yieldRow(a.yield, e, a.timed, &a.st.Result.LoopBody) {
			a.stopped = true
			return nil
		}
	}
	return nil
}

// All runs the query and returns every row.
func (q RawQuery[E]) All[_ Lazy[E]](ctx context.Context) ([]E, error) {
	st, err := q.statement()
	if err != nil {
		return nil, err
	}
	a := &rawRun[E]{st: st, m: q.meta, src: q.db.e.Source()}
	if err := q.db.e.Run(ctx, &a.st, func(r engine.Rows) error { return readRaw(a, r) }, nil); err != nil {
		return nil, err
	}
	return a.out, nil
}

// One runs the query for exactly one row, reading at most two. Its error
// wraps ErrNotFound when the SQL returns no row and ErrNotSingular when it
// returns more than one.
func (q RawQuery[E]) One[_ Lazy[E]](ctx context.Context) (*E, error) {
	st, err := q.statement()
	if err != nil {
		return nil, err
	}
	a := &rawRun[E]{st: st, m: q.meta, src: q.db.e.Source(), limit: 2}
	if err := q.db.e.Run(ctx, &a.st, func(r engine.Rows) error { return readRaw(a, r) }, nil); err != nil {
		return nil, err
	}
	switch len(a.out) {
	case 0:
		return nil, fmt.Errorf("velox: %s: %w", q.meta.Info.Name, ErrNotFound)
	case 1:
		return &a.out[0], nil
	}
	return nil, fmt.Errorf("velox: %s: %w", q.meta.Info.Name, ErrNotSingular)
}

// Iter runs the query and yields one row at a time; only the current row is
// held in memory. As with Query.Iter, the loop body runs inside the Around
// hooks and under the statement's deadline, and on a pool of one connection a
// statement in the body fails, as Query.Iter says.
func (q RawQuery[E]) Iter[_ Lazy[E]](ctx context.Context) iter.Seq2[E, error] {
	return func(yield func(E, error) bool) {
		st, err := q.statement()
		if err != nil {
			var zero E
			yield(zero, err)
			return
		}
		engine.SetStmtIter(&st)
		a := &rawRun[E]{st: st, m: q.meta, src: q.db.e.Source(), yield: yield, timed: len(q.db.e.Cfg.Arounds) > 0}
		err = q.db.e.Run(ctx, &a.st, func(r engine.Rows) error { return readRaw(a, r) }, nil)
		if err != nil && !a.stopped {
			var zero E
			yield(zero, err)
		}
	}
}
