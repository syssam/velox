package migrate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/syssam/velox"
)

// maxLockWaitTimeout is the most MySQL's lock_wait_timeout takes: a year.
const maxLockWaitTimeout = 31536000

// applyStepwise runs a file statement by statement, as every MySQL file and
// a PostgreSQL no-transaction file run. It inserts the history row dirty,
// with no statements (row, a dirty row to resume, is kept instead), sets
// the lock timeout, and runs the file a unit at a time, from the statement
// after those the row counts, which is the first of a unit unless
// Resolve's ResolveStatements said otherwise, recording after each unit the
// statements of the completed ones. On success the row is clean, with the
// duration and applied_at now. The row is written though ctx is gone, and
// ctx is checked before each unit, so a cancel keeps the count exact; a
// count or clean row the session cannot write because its connection is
// lost is never written through another, and the error says so. A
// failing statement, a cancel, or a file that leaves a transaction open
// leaves the row dirty, with the error, and is a *DirtyError; a
// transaction the file opened is rolled back first. On MySQL a statement
// the server refused with nothing changed is less: the first of a file not
// yet recorded takes its row away, an *Error of Code "retry", and a lock's
// at the head of a unit leaves a row Up goes on from by itself.
func (s *session) applyStepwise(ctx context.Context, f File, row *Applied, o options) (Applied, error) {
	n := len(f.Statements)
	units := f.Units // ParseFile gives every file with statements a unit at 0
	from := 0
	if row != nil {
		from = min(row.Statements, n)
	}
	if err := s.checkRecreates(ctx, f, from); err != nil {
		return Applied{}, err
	}
	if row == nil {
		a := Applied{Version: f.Version, Name: f.Name, Sum: f.Sum, AppliedAt: time.Now().UTC().Truncate(time.Microsecond), Dirty: true}
		if err := s.record(ctx, a); err != nil {
			return Applied{}, wrap(ctx, fmt.Sprintf("%s: recording version %d in %s", f.Path, f.Version, s.cfg.Table), err)
		}
	}
	start := time.Now()
	restore, err := s.stepTimeout(ctx, f)
	if err != nil {
		return Applied{}, s.failed(ctx, f, from, fmt.Errorf("setting the lock timeout: %w", err), o)
	}
	defer restore()
	timeouts := s.statementTimeouts(f)
	table := s.historyTable()
	last := from // the first statement of the last unit run
	for u, first := range units {
		end := n
		if u+1 < len(units) {
			end = units[u+1]
		}
		if end <= from {
			continue
		}
		first = max(first, from)
		if err := ctx.Err(); err != nil {
			return Applied{}, s.failed(ctx, f, first, err, o)
		}
		for i := first; i < end; i++ {
			if err := timeouts.before(ctx, f.Statements[i]); err != nil {
				return Applied{}, s.failed(ctx, f, i, fmt.Errorf("setting the lock timeout: %w", err), o)
			}
			if _, err := s.conn.ExecContext(ctx, f.Statements[i]); err != nil {
				if s.d == velox.Postgres && ctx.Err() == nil && i == first && pgLockFailure(err) {
					// A statement that failed on a lock changed nothing; at a unit's
					// head the whole unit is still to run.
					if open, oerr := s.inTransaction(ctx); !open && oerr == nil {
						err = &retryError{err}
					}
				}
				if s.d == velox.MySQL && ctx.Err() == nil {
					if row == nil && i == 0 && nothingChanged(f.Statements[0], err) {
						if refused := s.notRecorded(ctx, f, err); refused != nil {
							return Applied{}, refused
						}
					}
					if open, oerr := s.inTransaction(ctx); i == first && lockFailure(err) && !open && oerr == nil {
						err = &retryError{err}
					}
				}
				return Applied{}, s.failed(ctx, f, i, err, o)
			}
		}
		if o.unitDone != nil {
			o.unitDone(f.Version, end)
		}
		q := fmt.Sprintf("UPDATE %s SET %s = %s WHERE %s = %s", table, s.q("statements"), s.ph(1), s.q("version"), s.ph(2))
		if err := s.write(ctx, q, end, int64(f.Version)); err != nil {
			if s.lostConn(ctx) {
				return Applied{}, s.lost(ctx, f, first, end, o)
			}
			err = fmt.Errorf("migrate: %s: statements 1 to %d ran, but recording them in %s: %w", f.Path, end, s.cfg.Table, err)
			s.recordError(ctx, f, err, o)
			return Applied{}, err
		}
		last = first
	}
	if open, err := s.inTransaction(ctx); open {
		if err == nil {
			err = errors.New("the file leaves a transaction open")
		}
		s.undo(ctx, "ROLLBACK")
		return Applied{}, s.failed(ctx, f, s.completed(ctx, f.Version, from), err, o)
	}
	a := Applied{
		Version:    f.Version,
		Name:       f.Name,
		Sum:        f.Sum,
		AppliedAt:  time.Now().UTC().Truncate(time.Microsecond),
		Duration:   time.Since(start),
		Statements: n,
	}
	q := fmt.Sprintf("UPDATE %s SET %s = %s, %s = %s, %s = %s, %s = %s, %s = %s WHERE %s = %s", table,
		s.q("applied_at"), s.ph(1), s.q("duration_ms"), s.ph(2), s.q("dirty"), s.ph(3), s.q("statements"), s.ph(4), s.q("error"), s.ph(5),
		s.q("version"), s.ph(6))
	if err := s.write(ctx, q, s.at(a.AppliedAt), a.Duration.Milliseconds(), false, n, "", int64(f.Version)); err != nil {
		if s.lostConn(ctx) {
			return Applied{}, s.lost(ctx, f, last, n, o)
		}
		return Applied{}, fmt.Errorf("migrate: %s: every statement ran, but recording version %d clean in %s; it is dirty still: %w", f.Path, f.Version, s.cfg.Table, err)
	}
	if o.logger != nil {
		o.logger.InfoContext(ctx, "migrate: applied", "version", a.Version, "path", f.Path, "duration", a.Duration)
	}
	return a, nil
}

// retryMark begins a dirty row's error when a lock kept the statement after those it
// counts from running: Up goes on from there unasked.
const retryMark = "retry: "

// A retryError is a failure of a statement to get a lock, to retry.
type retryError struct{ err error }

func (e *retryError) Error() string { return retryMark + e.err.Error() }
func (e *retryError) Unwrap() error { return e.err }

// lockFailure reports whether err is MySQL's lock wait timeout or deadlock: the
// statement had changed nothing, and InnoDB took back any row it wrote.
func lockFailure(err error) bool {
	code, _ := velox.DriverCode(err)
	return code == "1205" || code == "1213"
}

// nothingChanged reports whether MySQL's err means q changed nothing: a lock's, or the
// server's refusal of a DDL statement, which InnoDB runs atomically. A DML statement
// on MyISAM can stop part-way, and a server stopping or a statement killed says
// nothing of how far it went.
func nothingChanged(q string, err error) bool {
	if lockFailure(err) {
		return true
	}
	code, _ := velox.DriverCode(err)
	if code == "" || slices.Contains([]string{"1053", "1317", "1927", "3024"}, code) {
		return false
	}
	return slices.Contains([]string{"ALTER", "CREATE", "DROP", "RENAME", "TRUNCATE"}, (&fileLinter{d: velox.MySQL, ws: lex(velox.MySQL, q)}).upper(0))
}

// notRecorded takes back the row of f, whose first statement the server refused with
// err and nothing changed, and is the refusal. It is nil when the row stays.
func (s *session) notRecorded(ctx context.Context, f File, err error) error {
	q := fmt.Sprintf("DELETE FROM %s WHERE %s = %s", s.historyTable(), s.q("version"), s.ph(1))
	if s.persist(ctx, q, int64(f.Version)) == nil {
		return &Error{Code: "retry", Pos: f.Path, cause: err, Reason: fmt.Sprintf("statement 1 of %d: %v\n  the server changed nothing, and version %d is not recorded; fix the cause, if it is not a lock, and run migrate apply again",
			len(f.Statements), err, f.Version)}
	}
	return nil
}

// write runs a write to the history on the session alone, though ctx is
// gone. A unit's count and the clean row are written so: one written
// through another connection could count statements the server rolled
// back with the lost one.
func (s *session) write(ctx context.Context, q string, args ...any) error {
	c, cancel := cleanup(ctx)
	defer cancel()
	_, err := s.conn.ExecContext(c, q, args...)
	return err
}

// lostConn reports whether the session's connection no longer answers.
func (s *session) lostConn(ctx context.Context) bool {
	c, cancel := cleanup(ctx)
	defer cancel()
	return s.conn.PingContext(c) != nil
}

// lost is the *DirtyError of a connection lost after statements first to
// end (from 0, end exclusive) ran and before their count was written: the
// row keeps the count the session last wrote, and the error, written
// through the pool, says their outcome is unknown.
func (s *session) lost(ctx context.Context, f File, first, end int, o options) error {
	return s.failed(ctx, f, first, errors.New(lostMessage(first, end)), o)
}

// lostMessage is lost's error for statements first to end (from 0, end
// exclusive).
func lostMessage(first, end int) string {
	which, they := fmt.Sprintf("statements %d to %d", first+1, end), "they"
	if end <= first+1 {
		which, they = fmt.Sprintf("statement %d", first+1), "it"
	}
	return fmt.Sprintf("the connection was lost during %s; check whether %s ran before resuming", which, they)
}

// persist runs a write of a dirty row's error, though ctx is gone, and
// through a new connection from the pool when the session's no longer
// answers.
func (s *session) persist(ctx context.Context, q string, args ...any) error {
	c, cancel := cleanup(ctx)
	defer cancel()
	if _, err := s.conn.ExecContext(c, q, args...); err == nil {
		return nil
	}
	_, err := s.pool.ExecContext(c, q, args...)
	return err
}

// inTransaction reports whether the session is inside a transaction a file
// opened, which a file run statement by statement must not leave open. A
// session that cannot say is reported as inside one, with the error.
func (s *session) inTransaction(ctx context.Context) (bool, error) {
	c, cancel := cleanup(ctx)
	defer cancel()
	switch s.d {
	case velox.MySQL:
		// Outside a transaction a savepoint ends with its statement, so
		// rolling back to it is 1305; inside one (autocommit off too) it
		// is there.
		_, err := s.conn.ExecContext(c, "SAVEPOINT velox_migrate_check")
		if err == nil {
			_, err = s.conn.ExecContext(c, "ROLLBACK TO SAVEPOINT velox_migrate_check")
			if code, _ := velox.DriverCode(err); code == "1305" {
				return false, nil
			}
		}
		if err != nil {
			return true, fmt.Errorf("checking that the file leaves no transaction open: %w", err)
		}
		return true, nil
	case velox.Postgres:
		// A savepoint outside a transaction block is 25P01, inside one it is
		// taken; an aborted block refuses it.
		_, err := s.conn.ExecContext(c, "SAVEPOINT velox_migrate_check")
		if code, _ := velox.DriverCode(err); code == "25P01" {
			return false, nil
		}
		if err != nil {
			if code, _ := velox.DriverCode(err); code != "25P02" {
				return true, fmt.Errorf("checking that the file leaves no transaction open: %w", err)
			}
		}
		return true, nil
	}
	return false, nil
}

// completed is the statements the row of version v records, as committed,
// or from when it cannot be read.
func (s *session) completed(ctx context.Context, v uint64, from int) int {
	c, cancel := cleanup(ctx)
	defer cancel()
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s = %s", s.q("statements"), s.historyTable(), s.q("version"), s.ph(1))
	var k int
	if err := s.conn.QueryRowContext(c, q, int64(v)).Scan(&k); err == nil {
		return k
	}
	if err := s.pool.QueryRowContext(c, q, int64(v)).Scan(&k); err == nil {
		return k
	}
	return from
}

// pgLockFailure reports whether err is PostgreSQL's lock timeout or deadlock:
// the statement failed waiting, and changed nothing.
func pgLockFailure(err error) bool {
	code, _ := velox.DriverCode(err)
	return code == "55P03" || code == "40P01"
}

// stmtTimeouts sets PostgreSQL's lock timeout before each statement of a file
// run statement by statement with no lock-timeout directive: none for a
// statement that waits for older transactions by design, which blocks no
// query of the table while it waits, and the Config's for every other, as a
// statement waiting for a table's lock queues each query of the table behind
// it. cur is the value set, stepTimeout's 0 at first.
type stmtTimeouts struct {
	s       *session
	on      bool
	timeout string
	cur     string
}

// statementTimeouts is the stmtTimeouts of f, which sets nothing unless f is
// a PostgreSQL file without a lock-timeout directive.
func (s *session) statementTimeouts(f File) *stmtTimeouts {
	return &stmtTimeouts{s: s, on: s.d == velox.Postgres && !f.Directives.LockTimeoutSet, timeout: pgLockTimeout(s.cfg.LockTimeout), cur: "0"}
}

func (t *stmtTimeouts) before(ctx context.Context, q string) error {
	if !t.on {
		return nil
	}
	want := t.timeout
	if waitsForOthers(q) {
		want = "0"
	}
	if want == t.cur {
		return nil
	}
	if _, err := t.s.conn.ExecContext(ctx, "SET lock_timeout = "+want); err != nil {
		return err
	}
	t.cur = want
	return nil
}

// waitsForOthers reports whether the PostgreSQL statement q waits for older
// transactions by design, under a lock that blocks no read or write of the
// table: CONCURRENTLY, and ALTER TABLE … VALIDATE CONSTRAINT. A short lock
// timeout would make it fail routinely.
func waitsForOthers(q string) bool {
	l := &fileLinter{d: velox.Postgres, ws: lex(velox.Postgres, q)}
	if l.concurrently() {
		return true
	}
	return l.upper(0) == "ALTER" && l.upper(1) == "TABLE" && (l.has("VALIDATE", "CONSTRAINT") || l.has("DETACH", "PARTITION") && l.has("CONCURRENTLY"))
}

// stepTimeout sets the lock timeout for a file run statement by statement:
// the file's directive, else on MySQL the Config's, else on PostgreSQL none,
// which statementTimeouts then sets per statement. On MySQL it is whole
// seconds, rounded up, and 0 sets back the session's own. restore resets it.
func (s *session) stepTimeout(ctx context.Context, f File) (restore func(), err error) {
	switch s.d {
	case velox.Postgres:
		q := "SET lock_timeout = 0"
		if f.Directives.LockTimeoutSet {
			q = "SET lock_timeout = " + pgLockTimeout(f.Directives.LockTimeout)
		}
		if _, err := s.conn.ExecContext(ctx, q); err != nil {
			return nil, err
		}
		return func() { s.undo(ctx, "RESET lock_timeout") }, nil
	case velox.MySQL:
		t := s.cfg.LockTimeout
		if f.Directives.LockTimeoutSet {
			t = f.Directives.LockTimeout
		}
		secs := s.lockWait
		if t > 0 {
			secs = min(ceilDiv(t, time.Second), maxLockWaitTimeout)
		}
		if _, err := s.conn.ExecContext(ctx, fmt.Sprintf("SET SESSION lock_wait_timeout = %d", secs)); err != nil {
			return nil, err
		}
		return func() { s.undo(ctx, fmt.Sprintf("SET SESSION lock_wait_timeout = %d", s.lockWait)) }, nil
	}
	return func() {}, nil
}

// failed rolls back a transaction the file opened, records err on f's
// dirty row, and is the *DirtyError at statement i (from 0, at most the
// file's last). When a transaction was rolled back, the undo hint names the
// statements the row counts as committed, not those before i.
func (s *session) failed(ctx context.Context, f File, i int, err error, o options) error {
	open, perr := s.inTransaction(ctx)
	s.recordError(ctx, f, err, o)
	n := len(f.Statements)
	e := &DirtyError{Version: f.Version, Path: f.Path, Statement: max(min(i+1, n), 1), Statements: n, Err: err}
	if open && perr == nil {
		k := s.completed(ctx, f.Version, i)
		e.applied = &k
	}
	var re *retryError
	e.retry = errors.As(err, &re)
	if s.d == velox.MySQL && !e.retry {
		e.guide = mysqlGuide(f, s.completed(ctx, f.Version, i))
	}
	return e
}

// recordError rolls back a transaction the file opened (a ROLLBACK outside
// one does nothing) and, on MySQL, turns autocommit back on, so the row's
// error commits; then it writes err to f's dirty row through persist. A
// failure to write it goes to o's logger.
func (s *session) recordError(ctx context.Context, f File, err error, o options) {
	s.undo(ctx, "ROLLBACK")
	if s.d == velox.MySQL {
		s.undo(ctx, "SET autocommit = 1")
	}
	q := fmt.Sprintf("UPDATE %s SET %s = %s WHERE %s = %s", s.historyTable(), s.q("error"), s.ph(1), s.q("version"), s.ph(2))
	if werr := s.persist(ctx, q, err.Error(), int64(f.Version)); werr != nil && o.logger != nil {
		c, cancel := cleanup(ctx)
		defer cancel()
		o.logger.ErrorContext(c, "migrate: recording the error of a dirty version", "version", f.Version, "path", f.Path, "error", werr)
	}
}

// mysqlGuide is what a dirty MySQL version needs said, whose row counts k of f's
// statements: MySQL undoes no statement, and a run that died may have left one running.
func mysqlGuide(f File, k int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  the history counts %d of %d statements as run. A statement still on the server when the run died may have finished since: see SHOW FULL PROCESSLIST, and SHOW CREATE TABLE against these:", k, len(f.Statements))
	for i, q := range f.Statements {
		if i == 20 {
			fmt.Fprintf(&b, "\n    and %d more", len(f.Statements)-i)
			break
		}
		q = strings.Join(strings.Fields(q), " ")
		if len(q) > 100 {
			q = q[:100] + "..."
		}
		fmt.Fprintf(&b, "\n    %d. %s", i+1, q)
	}
	fmt.Fprintf(&b, "\n  if the first K ran and the history counts otherwise, run migrate resolve -version %d -statements K, then migrate apply -resume", f.Version)
	return b.String()
}
