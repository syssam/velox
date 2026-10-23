package migrate

import (
	"context"
	"fmt"
	"time"

	"github.com/syssam/velox2"
)

// maxLockWaitTimeout is the most MySQL's lock_wait_timeout takes: a year.
const maxLockWaitTimeout = 31536000

// applyStepwise runs a file statement by statement, as every MySQL file and
// a PostgreSQL no-transaction file run. It inserts the history row dirty,
// with no statements (row, a dirty row to resume, is kept instead), sets
// the lock timeout, and runs the file a unit at a time, from the first
// statement of the first incomplete unit, recording after each unit the
// statements of the completed ones. On success the row is clean, with the
// duration and applied_at now. A failing statement leaves the row dirty,
// with the driver's error, and is a *DirtyError.
func (s *session) applyStepwise(ctx context.Context, f File, row *Applied, o options) (Applied, error) {
	n := len(f.Statements)
	units := f.Units
	if len(units) == 0 && n > 0 {
		units = []int{0}
	}
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
	table := s.q(s.cfg.Table)
	for u, first := range units {
		if first < from {
			continue
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
		q := fmt.Sprintf("UPDATE %s SET %s = %s WHERE %s = %s", table, s.q("statements"), s.ph(1), s.q("version"), s.ph(2))
		if _, err := s.conn.ExecContext(ctx, q, end, int64(f.Version)); err != nil {
			err = wrap(ctx, fmt.Sprintf("%s: statements 1 to %d ran, but recording them in %s", f.Path, end, s.cfg.Table), err)
			s.recordError(ctx, f, err, o)
			return Applied{}, err
		}
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
	if _, err := s.conn.ExecContext(ctx, q, s.at(a.AppliedAt), a.Duration.Milliseconds(), false, n, "", int64(f.Version)); err != nil {
		return Applied{}, wrap(ctx, fmt.Sprintf("%s: every statement ran, but recording version %d clean in %s; it is dirty still", f.Path, f.Version, s.cfg.Table), err)
	}
	if o.logger != nil {
		o.logger.InfoContext(ctx, "migrate: applied", "version", a.Version, "path", f.Path, "duration", a.Duration)
	}
	return a, nil
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
			q = fmt.Sprintf("SET lock_timeout = '%dms'", waitMillis(f.Directives.LockTimeout))
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

// failed records err on f's dirty row, at statement i (from 0), and is the
// *DirtyError.
func (s *session) failed(ctx context.Context, f File, i int, err error, o options) error {
	s.recordError(ctx, f, err, o)
	return &DirtyError{Version: f.Version, Path: f.Path, Statement: i + 1, Statements: len(f.Statements), Err: err}
}

// recordError writes err to f's dirty row, though ctx is gone, and through a
// new connection from the pool when the session's no longer answers. A
// failure to write it goes to o's logger.
func (s *session) recordError(ctx context.Context, f File, err error, o options) {
	c, cancel := cleanup(ctx)
	defer cancel()
	q := fmt.Sprintf("UPDATE %s SET %s = %s WHERE %s = %s", s.q(s.cfg.Table), s.q("error"), s.ph(1), s.q("version"), s.ph(2))
	if _, werr := s.conn.ExecContext(c, q, err.Error(), int64(f.Version)); werr == nil {
		return
	}
	s.broken = true
	if _, werr := s.pool.ExecContext(c, q, err.Error(), int64(f.Version)); werr != nil && o.logger != nil {
		o.logger.ErrorContext(c, "migrate: recording the error of a dirty version", "version", f.Version, "path", f.Path, "error", werr)
	}
}
