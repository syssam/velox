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

// An Option changes how Up, Status and Resolve run. Each says which of them
// it applies to; the others ignore it.
type Option struct{ apply func(*options) }

type options struct {
	target   uint64        // 0: every file
	lockWait time.Duration // default 5m
	logger   *slog.Logger  // nil: none
	resume   bool

	noTxCheck bool // the tests: skip the check of transaction files' BEGIN, COMMIT and ROLLBACK
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

// WithResume makes Up continue a dirty version before the pending files,
// from the first statement of its first incomplete unit. Its file must be
// unchanged: a changed or missing one is a *ChecksumError; a dirty version
// beside versions above the directory's last file is not resumed, an *Error
// of Code "history". A run killed while a statement ran may have applied
// it, and on MySQL a cancelled statement may still have finished on the
// server: check the failed statement before resuming or undoing.
func WithResume() Option {
	return Option{func(o *options) { o.resume = true }}
}

// WithLockWait bounds how long Up and Resolve wait for the migration lock,
// which another runner holds while it applies, and how long Status waits on
// SQLite for a writer to finish; 5 minutes by default. A wait of 0 or less
// does not wait: the lock is tried once. A wait past what the database
// takes (about 24 days) is cut to it.
func WithLockWait(d time.Duration) Option {
	return Option{func(o *options) { o.lockWait = d }}
}

// WithLogger makes Up log each file it applies, and Resolve the version it
// resolves, to l.
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
	Applied []Applied // in version order: the clean versions up to the directory's last file
	Pending []File
	Ahead   []Applied // above the directory's last file
	Dirty   *Applied
}

// errAppliedMeanwhile is applyTx's word that another runner applied the
// file after this one read the history.
var errAppliedMeanwhile = errors.New("migrate: applied by another runner")

// Up applies dir's pending files to db, in version order. It reads and
// checks the directory, takes one connection and checks that it answers as
// velox.json's dialect at its minimum version, takes the migration lock,
// creates the history table when it is missing, and reads the history
// against the directory: an applied file that was edited or is gone is a
// *ChecksumError, a pending file below the highest applied version an
// *Error of Code "history", and a dirty version a *DirtyError, unless
// WithResume. Versions above the directory's last file are a newer
// directory's: Up applies nothing and sets Report.Ahead. On MySQL, a session
// whose sql_mode holds NO_BACKSLASH_ESCAPES or ANSI_QUOTES is refused,
// before any file runs, when a file to apply holds a literal the mode would
// read otherwise. A file run in a transaction with a statement led by
// BEGIN, START, COMMIT, ROLLBACK or END (or ABORT, on PostgreSQL) is
// refused too, as LintFile refuses it: an *Error of Code "file" at the
// statement's line.
//
// A file runs in a transaction with its history row on PostgreSQL and
// SQLite; one that fails is rolled back, and Up stops with an error naming
// the file and the statement that wraps the driver's. A MySQL file, or a
// PostgreSQL no-transaction one, runs statement by statement: one that fails
// leaves its version dirty and is a *DirtyError, which stops every later run
// until WithResume continues it or Resolve ends it. Either way the Report
// holds the files applied before it.
//
// Up uses one connection of db for its run and closes it after, so nothing
// a file sets on its session reaches db's other connections. Files run with
// the session in UTC on PostgreSQL and MySQL, so a time literal in a file
// means one instant whatever the server's zone. Each file starts with the
// session as Up opened it: after a file, PostgreSQL's settings are RESET
// ALL and MySQL's sql_mode is set back, and the zone is UTC again. Other
// MySQL session state a file sets, such as a user variable, reaches the
// run's later files.
func Up(ctx context.Context, db *sql.DB, dir fs.FS, opts ...Option) (*Report, error) {
	o := optionsOf(opts)
	d, err := ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, &Error{Code: "nil", Reason: "Up needs a database"}
	}
	s, err := open(ctx, db, d.Config, o.lockWait)
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
	var resumed *File
	if de := (*DirtyError)(nil); errors.As(err, &de) && o.resume {
		resumed, err = s.resuming(d, st)
	}
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
	if resumed != nil {
		todo = append(todo, *resumed)
	}
	for _, f := range st.Pending {
		if o.target != 0 && f.Version > o.target {
			break
		}
		todo = append(todo, f)
	}
	if err := s.checkModes(todo); err != nil {
		return nil, err
	}
	for _, f := range todo {
		if o.noTxCheck || s.stepwise(f) {
			continue
		}
		if err := checkTxControl(s.d, f); err != nil {
			return nil, err
		}
	}
	for i, f := range todo {
		if i > 0 {
			if err := s.reset(ctx, todo[i:]); err != nil {
				return r, wrap(ctx, "resetting the session after "+todo[i-1].Path, err)
			}
		}
		var a Applied
		var err error
		switch {
		case resumed != nil && f.Version == resumed.Version:
			a, err = s.applyStepwise(ctx, f, st.Dirty, o)
		case s.stepwise(f):
			a, err = s.applyStepwise(ctx, f, nil, o)
		default:
			a, err = s.applyTx(ctx, f, o)
		}
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
// State.Applied holds the clean versions up to the directory's last file,
// Ahead the clean versions above it, and Dirty the dirty version alone,
// wherever it is. On SQLite, the read waits as long as WithLockWait says
// for a writer to finish; Status takes no other option. Like Up, it uses
// one connection of db and closes it after.
func Status(ctx context.Context, db *sql.DB, dir fs.FS, opts ...Option) (*State, error) {
	o := optionsOf(opts)
	d, err := ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, &Error{Code: "nil", Reason: "Status needs a database"}
	}
	s, err := open(ctx, db, d.Config, o.lockWait)
	if err != nil {
		return nil, err
	}
	defer s.close()
	if s.d == velox.SQLite {
		restore, err := s.lock(ctx, o.lockWait) // busy_timeout alone: SQLite's lock is each file's BEGIN IMMEDIATE
		if err != nil {
			return nil, err
		}
		defer restore()
	}
	rows, _, err := s.history(ctx)
	if err != nil {
		return nil, err
	}
	return reading(d, rows)
}

// prepare creates the history table when it is missing and reads it. On
// SQLite both run in a BEGIN IMMEDIATE, so they wait on the lock too.
func (s *session) prepare(ctx context.Context) ([]Applied, error) {
	what := "creating the history table " + s.cfg.Table
	if s.d == velox.SQLite {
		if err := s.begin(ctx); err != nil {
			return nil, wrap(ctx, what, err)
		}
	}
	err := s.createHistory(ctx)
	var rows []Applied
	if err == nil {
		rows, _, err = s.history(ctx)
	}
	if s.d == velox.SQLite {
		if err == nil {
			if _, cerr := s.conn.ExecContext(ctx, "COMMIT"); cerr != nil {
				err = wrap(ctx, what+": COMMIT", cerr)
			}
		}
		if err != nil {
			s.rollback(ctx)
		}
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
		case r.Dirty:
		case inRange(r.Version):
			st.Applied = append(st.Applied, r)
		default:
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
		} else {
			e.missing = true
		}
		if r.Error != "" {
			e.Err = errors.New(r.Error)
		}
		return st, e
	}
	return st, st.check(dir)
}

// check is reading's refusals of the clean rows: an applied file edited or
// gone, and a pending file below the highest applied version.
func (st *State) check(dir *Dir) error {
	for _, r := range st.Applied {
		i := slices.IndexFunc(dir.Files, func(f File) bool { return f.Version == r.Version })
		if i < 0 {
			return &ChecksumError{Version: r.Version, Path: pathOf(dir, r.Version, r.Name), Applied: r.Sum}
		}
		if f := dir.Files[i]; f.Sum != r.Sum {
			return &ChecksumError{Version: r.Version, Path: f.Path, Applied: r.Sum, Now: f.Sum}
		}
	}
	var highest uint64
	for _, rs := range [][]Applied{st.Applied, st.Ahead} {
		if n := len(rs); n > 0 {
			highest = max(highest, rs[n-1].Version)
		}
	}
	if st.Dirty != nil {
		highest = max(highest, st.Dirty.Version)
	}
	for _, f := range st.Pending {
		if f.Version < highest {
			return &Error{Code: "history", Reason: fmt.Sprintf("%s is pending below version %d, the highest applied; the history is linear", f.Path, highest)}
		}
	}
	return nil
}

// stepwise reports whether f runs statement by statement: every MySQL file,
// and a PostgreSQL no-transaction one.
func (s *session) stepwise(f File) bool {
	return s.d == velox.MySQL || (s.d == velox.Postgres && f.Directives.NoTransaction)
}

// resuming is the file of st's dirty version, to resume, after reading's
// refusals of the clean rows. The database must have no version above the
// directory's last file, whose files the directory lacks (else an *Error of
// Code "history"). Its file must be in dir with the row's checksum (else a
// *ChecksumError), and be one that runs statement by statement, since a
// file run in a transaction never leaves its version dirty (else an *Error
// of Code "history").
func (s *session) resuming(dir *Dir, st *State) (*File, error) {
	r := st.Dirty
	if n := len(st.Ahead); n > 0 {
		return nil, &Error{Code: "history", Reason: fmt.Sprintf("version %d is dirty, and the database has versions above the directory's last file, up to %d; restore the missing files to the directory and resume with it, or %s",
			r.Version, max(st.Ahead[n-1].Version, r.Version), undoHint(r.Version))}
	}
	i := slices.IndexFunc(dir.Files, func(f File) bool { return f.Version == r.Version })
	if i < 0 {
		return nil, &ChecksumError{Version: r.Version, Path: pathOf(dir, r.Version, r.Name), Applied: r.Sum, dirty: true}
	}
	f := dir.Files[i]
	if f.Sum != r.Sum {
		return nil, &ChecksumError{Version: r.Version, Path: f.Path, Applied: r.Sum, Now: f.Sum}
	}
	if !s.stepwise(f) {
		return nil, &Error{Code: "history", Reason: fmt.Sprintf("%s: version %d is dirty, but its file runs in a transaction, which cannot be resumed; resolve it", f.Path, r.Version)}
	}
	if err := st.check(dir); err != nil {
		return nil, err
	}
	return &f, nil
}

// applyTx runs a file in one transaction, its history row included: BEGIN
// (BEGIN IMMEDIATE on SQLite), the lock timeout on PostgreSQL, the
// statements, a check that the file left the transaction open, PRAGMA
// foreign_key_check on SQLite with foreign keys off, the row, COMMIT, then
// a line to o's logger. On SQLite, foreign keys are turned off before BEGIN
// when the file asks and set back after, and a file another runner applied
// since the history was read is skipped with errAppliedMeanwhile. That runner's row is
// matched by version alone: its checksum was checked against its own
// directory, and the next run checks it against this one.
func (s *session) applyTx(ctx context.Context, f File, o options) (Applied, error) {
	rolledBack := func(format string, args ...any) error {
		return fmt.Errorf("migrate: %s: "+format+"; the file was rolled back", append([]any{f.Path}, args...)...)
	}
	fkOff := s.d == velox.SQLite && f.Directives.ForeignKeysOff
	if fkOff {
		var was int
		if err := s.conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&was); err != nil {
			return Applied{}, wrap(ctx, f.Path+": reading foreign_keys", err)
		}
		defer s.undo(ctx, fmt.Sprintf("PRAGMA foreign_keys = %d", was))
		if _, err := s.conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return Applied{}, wrap(ctx, f.Path+": turning foreign keys off", err)
		}
	}
	if err := s.begin(ctx); err != nil {
		return Applied{}, wrap(ctx, f.Path+": BEGIN", err)
	}
	done := false
	defer func() {
		if !done {
			s.rollback(ctx)
		}
	}()
	if s.d == velox.SQLite {
		rows, _, err := s.readHistory(ctx)
		if err != nil {
			return Applied{}, rolledBack("reading the history table %s: %w", s.cfg.Table, err)
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
		if _, err := s.conn.ExecContext(ctx, "SET LOCAL lock_timeout = "+pgLockTimeout(timeout)); err != nil {
			return Applied{}, rolledBack("setting the lock timeout: %w", err)
		}
	}
	start := time.Now()
	for i, q := range f.Statements {
		if _, err := s.conn.ExecContext(ctx, q); err != nil {
			return Applied{}, rolledBack("statement %d of %d: %w", i+1, len(f.Statements), err)
		}
	}
	if !s.ownsTx(ctx) {
		return Applied{}, fmt.Errorf("migrate: %s: the file ended the runner's transaction, so what it ran is committed in part; version %d is not recorded", f.Path, f.Version)
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
		return Applied{}, rolledBack("recording version %d in %s: %w", f.Version, s.cfg.Table, err)
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

// ownsTx reports whether the transaction applyTx began is open still, after
// the file's statements: a file that ended it ran its later statements on
// their own. On PostgreSQL a savepoint outside a transaction block is
// 25P01; on SQLite a BEGIN outside a transaction succeeds, and is rolled
// back. A session that cannot say is taken as owning it, and the history
// write that follows fails on its own.
func (s *session) ownsTx(ctx context.Context) bool {
	c, cancel := cleanup(ctx)
	defer cancel()
	switch s.d {
	case velox.Postgres:
		_, err := s.conn.ExecContext(c, "SAVEPOINT velox_migrate_check")
		if code, _ := velox.DriverCode(err); code == "25P01" {
			return false
		}
		if err == nil {
			s.undo(ctx, "RELEASE SAVEPOINT velox_migrate_check")
		}
	case velox.SQLite:
		if _, err := s.conn.ExecContext(c, "BEGIN"); err == nil {
			s.rollback(ctx)
			return false
		}
	}
	return true
}
