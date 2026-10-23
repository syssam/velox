package migrate

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"time"

	"github.com/syssam/velox2"
)

// An Option changes how Up and Status run.
type Option struct{ apply func(*options) }

type options struct {
	target   uint64        // 0: every file
	lockWait time.Duration // default 5m
	logger   *slog.Logger  // nil: none
}

// defaultLockWait is how long Up waits for the migration lock by default.
const defaultLockWait = 5 * time.Minute

func optionsOf(opts []Option) options {
	o := options{lockWait: defaultLockWait}
	for _, opt := range opts {
		if opt.apply != nil {
			opt.apply(&o)
		}
	}
	return o
}

// WithTarget makes Up apply the pending files up to version, and none
// above it.
func WithTarget(version uint64) Option {
	return Option{func(o *options) { o.target = version }}
}

// WithLockWait bounds how long Up waits for the migration lock, which
// another runner holds while it applies; 5 minutes by default. A wait of 0
// does not wait.
func WithLockWait(d time.Duration) Option {
	return Option{func(o *options) { o.lockWait = d }}
}

// WithLogger makes Up log each file it applies to l.
func WithLogger(l *slog.Logger) Option {
	return Option{func(o *options) { o.logger = l }}
}

// Applied is a version of the history: a file the runner applied.
type Applied struct {
	Version    uint64
	Name       string
	Sum        string
	AppliedAt  time.Time
	Duration   time.Duration
	Dirty      bool
	Statements int    // statements of completed units, of a file run statement by statement
	Error      string // the failure that left it dirty
}

// A Report is what Up did.
type Report struct {
	Applied   []Applied // by this call
	Baselined []uint64
	Ahead     bool // the database has versions above the directory's last
}

// A State is a directory read against a database's history.
type State struct {
	Applied []Applied // in version order: the history up to the directory's last file
	Pending []File
	Ahead   []Applied // above the directory's last file
	Dirty   *Applied
}

// errAppliedMeanwhile is applyTx's word that another runner applied the
// file after this one read the history.
var errAppliedMeanwhile = errors.New("migrate: applied by another runner")

// Up applies dir's pending files to db, in version order, each in a
// transaction with its history row. It reads and checks the directory,
// takes one connection and checks that it answers as velox.json's dialect
// at its minimum version, takes the migration lock, creates the history
// table when it is missing, and reads the history against the directory:
// an applied file that was edited or is gone is a *ChecksumError, a pending
// file below the highest applied version an *Error of Code "history", and a
// dirty version a *DirtyError. Versions above the directory's last file are
// a newer directory's: Up applies nothing and sets Report.Ahead.
//
// A file that fails is rolled back, and Up stops with an error naming the
// file and the statement that wraps the driver's; the Report holds the
// files applied before it.
func Up(ctx context.Context, db *sql.DB, dir fs.FS, opts ...Option) (*Report, error) {
	o := optionsOf(opts)
	d, err := ReadDir(dir)
	if err != nil {
		return nil, err
	}
	s, err := open(ctx, db, d.Config)
	if err != nil {
		return nil, err
	}
	defer s.close()
	unlock, err := s.lock(ctx, o.lockWait)
	if err != nil {
		return nil, err
	}
	defer unlock()
	rows, err := s.prepare(ctx)
	if err != nil {
		return nil, err
	}
	st, err := reading(d, rows)
	if err != nil {
		return nil, err
	}
	r := &Report{}
	if len(st.Ahead) > 0 {
		r.Ahead = true
		if o.logger != nil {
			o.logger.InfoContext(ctx, "migrate: the database has versions above the directory's last; nothing applied", "ahead", st.Ahead[len(st.Ahead)-1].Version)
		}
		return r, nil
	}
	var todo []File
	for _, f := range st.Pending {
		if o.target != 0 && f.Version > o.target {
			break
		}
		if s.d == velox.MySQL || f.Directives.NoTransaction {
			return nil, &Error{Code: "session", Reason: "files run statement by statement are not built yet"}
		}
		todo = append(todo, f)
	}
	for _, f := range todo {
		a, err := s.applyTx(ctx, f, o)
		if errors.Is(err, errAppliedMeanwhile) {
			continue
		}
		if err != nil {
			return r, err
		}
		r.Applied = append(r.Applied, a)
	}
	return r, nil
}

// Status reads dir against db's history, as Up would, without the lock and
// without creating the history table. A dirty version gives the state, with
// Dirty set, and a *DirtyError together; Up's other refusals give no state.
// State.Applied holds the history up to the directory's last file, Ahead the
// clean versions above it, and Dirty the dirty version, wherever it is.
// It takes Up's options and uses none of them.
func Status(ctx context.Context, db *sql.DB, dir fs.FS, opts ...Option) (*State, error) {
	d, err := ReadDir(dir)
	if err != nil {
		return nil, err
	}
	s, err := open(ctx, db, d.Config)
	if err != nil {
		return nil, err
	}
	defer s.close()
	rows, _, err := s.history(ctx)
	if err != nil {
		return nil, err
	}
	return reading(d, rows)
}

// prepare creates the history table when it is missing and reads it. On
// SQLite both run in a BEGIN IMMEDIATE, so they wait on the lock too.
func (s *session) prepare(ctx context.Context) ([]Applied, error) {
	if s.d == velox.SQLite {
		if err := s.begin(ctx); err != nil {
			return nil, err
		}
	}
	err := s.createHistory(ctx)
	var rows []Applied
	if err == nil {
		rows, _, err = s.history(ctx)
	}
	if s.d == velox.SQLite {
		if err != nil {
			s.rollback(ctx)
			return nil, err
		}
		_, err = s.conn.ExecContext(ctx, "COMMIT")
	}
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// pathOf is the path of version v's file in dir, or, when the directory has
// none, the path Dir.Add would have given it.
func pathOf(dir *Dir, v uint64, name string) string {
	if i := slices.IndexFunc(dir.Files, func(f File) bool { return f.Version == v }); i >= 0 {
		return dir.Files[i].Path
	}
	return fmt.Sprintf("%04d_%s.sql", v, name)
}

// reading is dir against the history rows: a dirty row first, as the state
// with Dirty set and a *DirtyError, together; else the state, or the first
// refusal (*ChecksumError, or *Error with Code "history"), with a nil state.
// Checksums are compared for clean rows only; clean rows above the last file
// are Ahead.
func reading(dir *Dir, rows []Applied) (*State, error) {
	rows = slices.Clone(rows)
	slices.SortFunc(rows, func(a, b Applied) int { return cmp.Compare(a.Version, b.Version) })
	var last uint64
	inRange := func(uint64) bool { return false }
	if n := len(dir.Files); n > 0 {
		last = dir.Files[n-1].Version
		inRange = func(v uint64) bool { return v <= last }
	}
	st := &State{}
	applied := map[uint64]bool{}
	for i, r := range rows {
		applied[r.Version] = true
		if r.Dirty && st.Dirty == nil {
			st.Dirty = &rows[i]
		}
		switch {
		case inRange(r.Version):
			st.Applied = append(st.Applied, r)
		case !r.Dirty:
			st.Ahead = append(st.Ahead, r)
		}
	}
	for _, f := range dir.Files {
		if !applied[f.Version] {
			st.Pending = append(st.Pending, f)
		}
	}
	if r := st.Dirty; r != nil {
		e := &DirtyError{Version: r.Version, Path: pathOf(dir, r.Version, r.Name)}
		if i := slices.IndexFunc(dir.Files, func(f File) bool { return f.Version == r.Version }); i >= 0 {
			e.Statements = len(dir.Files[i].Statements)
		}
		if r.Error != "" {
			e.Err = errors.New(r.Error)
		}
		return st, e
	}
	for _, r := range st.Applied {
		i := slices.IndexFunc(dir.Files, func(f File) bool { return f.Version == r.Version })
		if i < 0 {
			return nil, &ChecksumError{Version: r.Version, Path: pathOf(dir, r.Version, r.Name), Applied: r.Sum}
		}
		if f := dir.Files[i]; f.Sum != r.Sum {
			return nil, &ChecksumError{Version: r.Version, Path: f.Path, Applied: r.Sum, Now: f.Sum}
		}
	}
	if len(rows) > 0 {
		highest := rows[len(rows)-1].Version
		for _, f := range st.Pending {
			if f.Version < highest {
				return nil, &Error{Code: "history", Reason: fmt.Sprintf("%s is pending below version %d, the highest applied; the history is linear", f.Path, highest)}
			}
		}
	}
	return st, nil
}

// applyTx runs a file in one transaction, its history row included: BEGIN
// (BEGIN IMMEDIATE on SQLite), the lock timeout on PostgreSQL, the
// statements, PRAGMA foreign_key_check on SQLite with foreign keys off, the
// row, COMMIT, then a line to o's logger. On SQLite, foreign keys are turned off before BEGIN when the
// file asks and set back after, and a file another runner applied since the
// history was read is skipped with errAppliedMeanwhile.
func (s *session) applyTx(ctx context.Context, f File, o options) (Applied, error) {
	rolledBack := func(format string, args ...any) error {
		return fmt.Errorf("migrate: %s: "+format+"; the file was rolled back", append([]any{f.Path}, args...)...)
	}
	fkOff := s.d == velox.SQLite && f.Directives.ForeignKeysOff
	if fkOff {
		var was int
		if err := s.conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&was); err != nil {
			return Applied{}, err
		}
		if _, err := s.conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return Applied{}, err
		}
		defer func() {
			c, cancel := cleanup(ctx)
			defer cancel()
			_, _ = s.conn.ExecContext(c, fmt.Sprintf("PRAGMA foreign_keys = %d", was))
		}()
	}
	if err := s.begin(ctx); err != nil {
		return Applied{}, err
	}
	done := false
	defer func() {
		if !done {
			s.rollback(ctx)
		}
	}()
	if s.d == velox.SQLite {
		rows, _, err := s.history(ctx)
		if err != nil {
			return Applied{}, rolledBack("reading the history: %w", err)
		}
		if slices.ContainsFunc(rows, func(a Applied) bool { return a.Version == f.Version }) {
			return Applied{}, errAppliedMeanwhile
		}
	}
	if s.d == velox.Postgres {
		timeout := s.cfg.LockTimeout
		if f.Directives.LockTimeoutSet {
			timeout = f.Directives.LockTimeout
		}
		if _, err := s.conn.ExecContext(ctx, fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", ceilDiv(timeout, time.Millisecond))); err != nil {
			return Applied{}, rolledBack("setting the lock timeout: %w", err)
		}
	}
	start := time.Now()
	for i, q := range f.Statements {
		if _, err := s.conn.ExecContext(ctx, q); err != nil {
			return Applied{}, rolledBack("statement %d of %d: %w", i+1, len(f.Statements), err)
		}
	}
	if fkOff {
		if err := s.foreignKeyCheck(ctx); err != nil {
			return Applied{}, rolledBack("%w", err)
		}
	}
	a := Applied{
		Version:    f.Version,
		Name:       f.Name,
		Sum:        f.Sum,
		AppliedAt:  time.Now().UTC().Truncate(time.Microsecond),
		Duration:   time.Since(start),
		Statements: len(f.Statements),
	}
	if err := s.record(ctx, a); err != nil {
		return Applied{}, rolledBack("recording the version: %w", err)
	}
	if _, err := s.conn.ExecContext(ctx, "COMMIT"); err != nil {
		return Applied{}, rolledBack("COMMIT: %w", err)
	}
	done = true
	if o.logger != nil {
		o.logger.InfoContext(ctx, "migrate: applied", "version", a.Version, "path", f.Path, "duration", a.Duration)
	}
	return a, nil
}

// foreignKeyCheck is PRAGMA foreign_key_check's first row, as an error.
func (s *session) foreignKeyCheck(ctx context.Context) error {
	rows, err := s.conn.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("PRAGMA foreign_key_check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return rows.Err()
	}
	var table, parent string
	var rowid sql.NullInt64
	var fk int
	if err := rows.Scan(&table, &rowid, &parent, &fk); err != nil {
		return fmt.Errorf("PRAGMA foreign_key_check: %w", err)
	}
	return fmt.Errorf("PRAGMA foreign_key_check reports row %d of %s, whose foreign key %d names no row of %s", rowid.Int64, table, fk, parent)
}
