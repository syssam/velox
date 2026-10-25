package velox

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// execInfo is the EntityInfo of Exec's statements: the name their errors and
// the Around hooks see. Exec scans nothing, so it has no table or columns.
var execInfo = EntityInfo{Name: "Exec", Tenant: -1}

// errNoReason is the error of raw SQL run without a reason.
func errNoReason(name string) error {
	return fmt.Errorf("velox: %s: raw SQL needs a reason, as it applies neither the tenant scope nor the policy", name)
}

// Exec runs query, raw SQL that returns no rows, with args, and returns the
// number of rows it affected, as the driver counts them. reason says why the
// statement may skip the tenant scope and the policy; if it is empty or only
// white space, Exec fails before anything runs. The hooks see reason as
// given, untrimmed. See Raw for what raw SQL goes through, and what it skips.
//
// velox treats Exec as a write (OpRawExec), whatever its SQL: it goes to the
// primary, pins the session (WithSession), and fails with ErrDryRun under a
// dry run (IsDryRun). Its errors, and the Around hooks, name its statement
// "Exec".
//
//	n, err := db.Exec(ctx, "nightly cleanup", "DELETE FROM sessions WHERE expires_at < $1", now)
func (db *DB) Exec(ctx context.Context, reason, query string, args ...any) (int64, error) {
	if noReason(reason) {
		return 0, errNoReason(execInfo.Name)
	}
	st := &Statement{Op: OpRawExec, Entity: &execInfo, SQL: query, Args: args, TenantBypass: reason, PolicyBypass: reason, InTx: db.inTx}
	var res result
	if err := db.run(ctx, st, nil, &res); err != nil {
		return 0, err
	}
	return res.affected, nil
}

// RawQuery is raw SQL whose rows scan into entity E, made by Raw and run by
// All, One or Iter. Like a Query it is a value, and running it does not
// change it.
type RawQuery[E any] struct {
	db      *DB
	meta    *Meta[E]
	reason  string
	query   string
	args    []any
	maxRows int
}

// Raw returns query, raw SQL with args, as a query whose rows scan into
// entity E. reason says why it may skip the tenant scope and the policy; if
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
//	totals, err := db.Raw[gen.Invoice]("revenue report",
//		"SELECT org_id, SUM(total_cents) AS total_cents FROM invoices GROUP BY org_id").All(ctx)
//
// Raw SQL is the escape hatch for row locks, CTEs and anything else the
// builder cannot express. velox applies neither the tenant scope nor any
// policy to it: tenant scoping is the caller's, written into the SQL, and the
// Around hooks see reason as Statement.TenantBypass and PolicyBypass. It goes
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
// WithPrimaryReads, and never under a dry run.
//
// On a DB opened with OpenShards, Raw and Exec run on the shard the context
// names, as every statement does: WithShard's, else the one WithShardFor gives
// for the context's tenant (WithTenant), though the tenant scopes nothing.
// With neither they fail with ErrNoShard, before anything is sent.
func (db *DB) Raw[E any, PE Entity[E]](reason, query string, args ...any) RawQuery[E] {
	return RawQuery[E]{db: db, meta: PE(nil).VeloxMeta(), reason: reason, query: query, args: args}
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
	if q.meta.err != nil {
		return Statement{}, q.meta.err
	}
	if noReason(q.reason) {
		return Statement{}, errNoReason(q.meta.info.Name)
	}
	return Statement{Op: OpRawQuery, Entity: q.meta.info, SQL: q.query, Args: q.args, MaxRows: q.maxRows,
		TenantBypass: q.reason, PolicyBypass: q.reason, InTx: q.db.inTx}, nil
}

// rawRun is the state of one run of a RawQuery: All's and One's rows go to
// out, Iter's to yield.
type rawRun[E any] struct {
	st      Statement
	m       *Meta[E]
	limit   int // One's: stop after this many rows; 0: read them all
	out     []E
	yield   func(E, error) bool // Iter's; nil for All and One
	timed   bool                // Iter's, on a DB with an Around hook: time the loop body
	yielded bool                // a row was yielded
	stopped bool                // the loop over Iter ended early
}

// readRaw reads r's rows by column name, anew should an Around hook retry.
func readRaw[E any](a *rawRun[E], r rows) error {
	if a.yield != nil && (a.yielded || a.stopped) {
		return errIterAgain
	}
	cols, err := rawColumns(a.m.info, r)
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
			if len(a.out) == a.limit {
				return nil
			}
			continue
		}
		var e E
		if err := scanRow(r, a.m, cols, dst, &e); err != nil {
			return err
		}
		a.yielded, a.st.handed = true, true
		if !yieldRow(a.yield, e, a.timed, &a.st.Result.LoopBody) {
			a.stopped = true
			return nil
		}
	}
	return nil
}

// rawColumns maps the columns r returns, by name, to in's columns. It refuses
// a column in has no field for, and a column returned twice, which would
// scan into one field twice.
func rawColumns(in *EntityInfo, r rows) ([]int, error) {
	names, err := resultColumns(r)
	if err != nil {
		return nil, err
	}
	cols := make([]int, len(names))
	seen := make(map[int]bool, len(names))
	var unknown, twice []string
	for i, name := range names {
		c, ok := in.Column(name)
		switch {
		case !ok:
			unknown = append(unknown, strconv.Quote(name))
		case seen[c] && !slices.Contains(twice, strconv.Quote(name)):
			twice = append(twice, strconv.Quote(name))
		}
		seen[c], cols[i] = true, c
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("the raw SQL returned %s, which %s has no field for", strings.Join(unknown, ", "), in.Name)
	}
	if len(twice) > 0 {
		return nil, fmt.Errorf("the raw SQL returned %s more than once", strings.Join(twice, ", "))
	}
	return cols, nil
}

// resultColumns returns the names of the columns r returns: database/sql's
// rows and the test executor's report them with Columns, pgx's with
// FieldDescriptions.
func resultColumns(r rows) ([]string, error) {
	if c, ok := r.(*countedRows); ok {
		r = c.rows
	}
	switch r := r.(type) {
	case interface{ Columns() ([]string, error) }:
		return r.Columns()
	case pgx.Rows:
		fds := r.FieldDescriptions()
		names := make([]string, len(fds))
		for i := range fds {
			names[i] = fds[i].Name
		}
		return names, nil
	}
	return nil, fmt.Errorf("the driver's rows (%T) do not name their columns", r)
}

// prepareRaw is prepare's work for raw SQL, which is the caller's: there is
// nothing to resolve, guard, rewrite, scope or render. It sets what the
// Around hooks read (observe), gives them a copy of the arguments to change,
// sets the fingerprint, of the caller's SQL, and appends the SQL comment,
// after trimming trailing semicolons and white space: a comment after a
// statement's terminator would make a second, empty statement, which MySQL
// without multiStatements refuses. When the last line holds "--", the comment
// goes on a line of its own, so that a line comment does not swallow it.
func (db *DB) prepareRaw(ctx context.Context, st *Statement) {
	db.observe(ctx, st)
	if len(db.cfg.arounds) > 0 {
		st.Args = slices.Clone(st.Args)
	}
	st.Fingerprint = fingerprint(st.SQL)
	if db.cfg.comment != nil {
		q := strings.TrimRight(st.SQL, "; \t\r\n")
		if strings.Contains(q[strings.LastIndexByte(q, '\n')+1:], "--") {
			q += "\n"
		}
		st.SQL = q + db.cfg.comment.text(st)
	}
}

// All runs the query and returns every row.
func (q RawQuery[E]) All[_ Lazy[E]](ctx context.Context) ([]E, error) {
	st, err := q.statement()
	if err != nil {
		return nil, err
	}
	a := &rawRun[E]{st: st, m: q.meta}
	if err := q.db.run(ctx, &a.st, func(r rows) error { return readRaw(a, r) }, nil); err != nil {
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
	a := &rawRun[E]{st: st, m: q.meta, limit: 2}
	if err := q.db.run(ctx, &a.st, func(r rows) error { return readRaw(a, r) }, nil); err != nil {
		return nil, err
	}
	switch len(a.out) {
	case 0:
		return nil, fmt.Errorf("velox: %s: %w", q.meta.info.Name, ErrNotFound)
	case 1:
		return &a.out[0], nil
	}
	return nil, fmt.Errorf("velox: %s: %w", q.meta.info.Name, ErrNotSingular)
}

// Iter runs the query and yields one row at a time; only the current row is
// held in memory. As with Query.Iter, the loop body runs inside the Around
// hooks and under the statement's deadline.
func (q RawQuery[E]) Iter[_ Lazy[E]](ctx context.Context) iter.Seq2[E, error] {
	return func(yield func(E, error) bool) {
		st, err := q.statement()
		if err != nil {
			var zero E
			yield(zero, err)
			return
		}
		a := &rawRun[E]{st: st, m: q.meta, yield: yield, timed: len(q.db.cfg.arounds) > 0}
		err = q.db.run(ctx, &a.st, func(r rows) error { return readRaw(a, r) }, nil)
		if err != nil && !a.stopped {
			var zero E
			yield(zero, err)
		}
	}
}
