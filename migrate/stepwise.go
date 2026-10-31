package migrate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/syssam/velox"
)

// maxLockWaitTimeout is the most MySQL's lock_wait_timeout takes: a year.
const maxLockWaitTimeout = 31536000

// applyStepwise runs a file statement by statement, as every MySQL file and
// a PostgreSQL no-transaction file run. It inserts the history row dirty,
// with no statements (row, a dirty row to resume, is kept instead), sets
// the lock timeout, and runs the file a unit at a time, from the first
// statement of the first incomplete unit, recording after each unit the
// statements of the completed ones. On success the row is clean, with the
// duration and applied_at now. The row is written though ctx is gone, and
// ctx is checked before each unit, so a cancel keeps the count exact; a
// count or clean row the session cannot write because its connection is
// lost is never written through another, and the error says so. A
// failing statement, a cancel, or a file that leaves a transaction open
// leaves the row dirty, with the error, and is a *DirtyError; a
// transaction the file opened is rolled back first.
func (s *session) applyStepwise(ctx context.Context, f File, row *Applied, o options) (Applied, error) {
	n := len(f.Statements)
	units := f.Units // ParseFile gives every file with statements a unit at 0
	from := 0
	if row == nil {
		a := Applied{Version: f.Version, Name: f.Name, Sum: f.Sum, AppliedAt: time.Now().UTC().Truncate(time.Microsecond), Dirty: true}
		if err := s.record(ctx, a); err != nil {
			return Applied{}, wrap(ctx, fmt.Sprintf("%s: recording version %d in %s", f.Path, f.Version, s.cfg.Table), err)
		}
	} else {
		for _, u := range units {
			if u <= row.Statements {
				from = u
			}
		}
		if row.Statements >= n {
			from = n
		}
	}
	start := time.Now()
	restore, err := s.stepTimeout(ctx, f)
	if err != nil {
		return Applied{}, s.failed(ctx, f, from, fmt.Errorf("setting the lock timeout: %w", err), o)
	}
	defer restore()
	table := s.historyTable()
	last := from // the first statement of the last unit run
	for u, first := range units {
		if first < from {
			continue
		}
		if err := ctx.Err(); err != nil {
			return Applied{}, s.failed(ctx, f, first, err, o)
		}
		end := n
		if u+1 < len(units) {
			end = units[u+1]
		}
		for i := first; i < end; i++ {
			if _, err := s.conn.ExecContext(ctx, f.Statements[i]); err != nil {
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

// stepTimeout sets the lock timeout for a file run statement by statement:
// the file's directive, else on MySQL the Config's, else on PostgreSQL none.
// On MySQL it is whole seconds, rounded up, and 0 sets back the session's
// own. restore resets it.
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
