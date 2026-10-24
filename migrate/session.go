package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/syssam/velox2"
)

// A session is the one connection a run holds, checked against the
// directory's Config.
type session struct {
	conn     *sql.Conn
	pool     *sql.DB // the connection's pool, for recording a failure when the connection is gone
	d        velox.Dialect
	cfg      Config
	db       string        // MySQL: DATABASE()
	sqlMode  string        // MySQL: @@SESSION.sql_mode
	lockWait int64         // MySQL: @@SESSION.lock_wait_timeout when the run began
	wait     time.Duration // SQLite: how long BEGIN IMMEDIATE waits, and busy_timeout
	busyWas  int64         // SQLite: the connection's busy_timeout before open set it
}

// wrap is err, from what the runner was doing, as "migrate: <what>: <err>".
// ctx's own error, ErrLockWait and this package's error types are returned
// as they are.
func wrap(ctx context.Context, what string, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var (
		e  *Error
		ce *ChecksumError
		de *DirtyError
	)
	if errors.Is(err, ErrLockWait) || errors.As(err, &e) || errors.As(err, &ce) || errors.As(err, &de) {
		return err
	}
	return fmt.Errorf("migrate: %s: %w", what, err)
}

// refuseSession is a refusal of the connection.
func refuseSession(format string, args ...any) error {
	return &Error{Code: "session", Reason: fmt.Sprintf(format, args...)}
}

// notAnswering is the refusal of a connection whose database answers err
// to a query of d's, with err as its cause.
func notAnswering(d velox.Dialect, err error) error {
	e := refuseSession("the database does not answer as %s, velox.json's dialect: %v", dialectName(d), err).(*Error)
	e.cause = err
	return e
}

// dialectName is d as its documentation names it.
func dialectName(d velox.Dialect) string {
	switch d {
	case velox.Postgres:
		return "PostgreSQL"
	case velox.MySQL:
		return "MySQL"
	case velox.SQLite:
		return "SQLite"
	}
	return d.String()
}

// open takes one connection and checks it: it answers as cfg.Dialect, at its
// minimum version, and on PostgreSQL with standard_conforming_strings on.
// It then puts the session in UTC. On SQLite it first sets busy_timeout to
// wait, before any statement that reads the database: a writer committing
// keeps every reader out, the check's query too.
// Its refusals are *Error with Code "session".
func open(ctx context.Context, db *sql.DB, cfg Config, wait time.Duration) (*session, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, wrap(ctx, "taking a connection", err)
	}
	s := &session{conn: conn, pool: db, d: cfg.Dialect, cfg: cfg}
	if s.d == velox.SQLite {
		if err := s.busyWait(ctx, wait); err != nil {
			s.close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, notAnswering(s.d, err)
		}
	}
	if err := s.check(ctx); err != nil {
		s.close()
		return nil, err
	}
	if err := s.utc(ctx); err != nil {
		s.close()
		return nil, wrap(ctx, "setting the session's time zone", err)
	}
	return s, nil
}

// utc puts the session in UTC, so a file's time literals mean one instant
// whatever the server's zone, the zone Inspect reads them in. The session
// is discarded at close, so the zone never reaches the pool.
func (s *session) utc(ctx context.Context) error {
	var q string
	switch s.d {
	case velox.Postgres:
		q = `SET TimeZone = 'UTC'`
	case velox.MySQL:
		q = `SET time_zone = '+00:00'`
	default:
		return nil // SQLite has no session zone
	}
	_, err := s.conn.ExecContext(ctx, q)
	return err
}

// check is open's checks of the connection.
func (s *session) check(ctx context.Context) error {
	var version string
	var err error
	switch s.d {
	case velox.Postgres:
		var scs string
		err = s.conn.QueryRowContext(ctx, `SELECT current_setting('server_version_num'), current_setting('standard_conforming_strings')`).Scan(&version, &scs)
		if err == nil && scs != "on" {
			return refuseSession("standard_conforming_strings is %s; turn it on for migrating, or a literal's backslashes would be stored otherwise", scs)
		}
	case velox.MySQL:
		var db sql.NullString
		err = s.conn.QueryRowContext(ctx, `SELECT @@version, DATABASE(), @@SESSION.sql_mode, @@SESSION.lock_wait_timeout`).Scan(&version, &db, &s.sqlMode, &s.lockWait)
		if err == nil && (!db.Valid || db.String == "") {
			return refuseSession("the connection has no database; name one in the DSN")
		}
		s.db = db.String
	case velox.SQLite:
		err = s.conn.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&version)
	default:
		return refuseSession("no dialect %s", s.d)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return notAnswering(s.d, err)
	}
	return checkVersion(s.d, version)
}

// checkModes refuses, on MySQL, a session whose sql_mode would read one of
// files' literals otherwise: NO_BACKSLASH_ESCAPES, a literal holding a
// backslash; ANSI_QUOTES, a "…" literal. Every file is checked before the
// first runs.
func (s *session) checkModes(files []File) error {
	if s.d != velox.MySQL {
		return nil
	}
	modes := strings.Split(strings.ToUpper(s.sqlMode), ",")
	noBackslash := slices.Contains(modes, "NO_BACKSLASH_ESCAPES")
	ansiQuotes := slices.Contains(modes, "ANSI_QUOTES")
	for _, f := range files {
		switch {
		case noBackslash && f.backslash:
			return refuseSession("%s holds a backslash in a literal, which NO_BACKSLASH_ESCAPES would store as two; turn the mode off for migrating", f.Path)
		case ansiQuotes && f.dquote:
			return refuseSession("%s holds a \"…\" literal, which ANSI_QUOTES would read as a name; turn the mode off for migrating", f.Path)
		}
	}
	return nil
}

// close closes the connection; it never returns to the pool. A file may
// change any setting of the session, and a failed clean-up may leave the
// lock or the run's settings on it, so none of it reaches the application's
// other connections.
func (s *session) close() { discard(s.conn) }

// discard closes conn so that it never returns to the pool.
func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

// undo runs a clean-up statement, though ctx is gone. A failure is left to
// close, which discards the connection with whatever it holds.
func (s *session) undo(ctx context.Context, q string, args ...any) {
	c, cancel := cleanup(ctx)
	defer cancel()
	_, _ = s.conn.ExecContext(c, q, args...)
}

// The minimum versions.
var (
	minPostgres = 120000
	minMySQL    = [3]int{8, 0, 19}
	minSQLite   = [3]int{3, 35, 0}
)

// checkVersion refuses a server below PostgreSQL 12 (server_version_num),
// MySQL 8.0.19 (@@version; MariaDB refused) or SQLite 3.35 (sqlite_version()).
func checkVersion(d velox.Dialect, v string) error {
	switch d {
	case velox.Postgres:
		n, err := strconv.Atoi(v)
		if err != nil {
			return refuseSession("PostgreSQL's server_version_num %q is not a number", v)
		}
		if n < minPostgres {
			name := strconv.Itoa(n / 10000) // 10 and later: the major version alone
			if n < 100000 {
				name += "." + strconv.Itoa(n/100%100)
			}
			return refuseSession("the server is PostgreSQL %s; migrate needs 12 or later", name)
		}
	case velox.MySQL:
		if strings.Contains(strings.ToLower(v), "mariadb") {
			return refuseSession("the server is MariaDB %s; migrate needs MySQL 8.0.19 or later", v)
		}
		n, ok := triple(v)
		if !ok {
			return refuseSession("MySQL's version %q is not <major>.<minor>.<patch>", v)
		}
		if less(n, minMySQL) {
			return refuseSession("the server is MySQL %s; migrate needs 8.0.19 or later", v)
		}
	case velox.SQLite:
		n, ok := triple(v)
		if !ok {
			return refuseSession("SQLite's version %q is not <major>.<minor>.<patch>", v)
		}
		if less(n, minSQLite) {
			return refuseSession("the library is SQLite %s; migrate needs 3.35 or later", v)
		}
	default:
		return refuseSession("no dialect %s", d)
	}
	return nil
}

// triple reads the leading "<major>.<minor>.<patch>" of v, which may go on
// with a suffix, as "8.0.36-log".
func triple(v string) ([3]int, bool) {
	var n [3]int
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return n, false
	}
	if i := strings.IndexFunc(parts[2], func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		parts[2] = parts[2][:i]
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// lockName is MySQL's lock: "velox_migrate_" and the first 16 hex digits of
// the SHA-256 of "<database>.<table>". GET_LOCK is server-wide, so the name
// holds the database; it is hashed to stay within MySQL's 64 characters.
func lockName(database, table string) string {
	h := sha256.Sum256([]byte(database + "." + table))
	return "velox_migrate_" + hex.EncodeToString(h[:8])
}

// lockKey is PostgreSQL's advisory lock: the first 8 bytes of the SHA-256 of
// the history table's name, big-endian. Advisory locks are per database.
func lockKey(table string) int64 {
	h := sha256.Sum256([]byte(table))
	return int64(binary.BigEndian.Uint64(h[:8]))
}

// cleanup is a context for undoing what a run did, though its own context
// is gone.
func cleanup(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

// lock takes the migration lock, waiting at most wait and while ctx lives;
// a timeout is ErrLockWait. A wait of 0 or less does not wait: the lock is
// tried once. On SQLite it sets busy_timeout to wait for the run, and each
// file's BEGIN IMMEDIATE is the lock.
func (s *session) lock(ctx context.Context, wait time.Duration) (unlock func(), err error) {
	const what = "taking the migration lock"
	wait = max(wait, 0)
	switch s.d {
	case velox.Postgres:
		key := lockKey(s.cfg.Table)
		var ok bool
		if wait == 0 {
			err = s.conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok)
		} else {
			if _, err = s.conn.ExecContext(ctx, fmt.Sprintf("SET lock_timeout = '%dms'", waitMillis(wait))); err != nil {
				return nil, wrap(ctx, what, err)
			}
			_, err = s.conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key)
			ok = err == nil
			if code, _ := velox.DriverCode(err); code == "55P03" {
				err = nil
			}
			s.undo(ctx, `RESET lock_timeout`)
		}
		switch {
		case err != nil:
			return nil, wrap(ctx, what, err)
		case !ok:
			return nil, ErrLockWait
		}
		return func() { s.undo(ctx, `SELECT pg_advisory_unlock($1)`, key) }, nil
	case velox.MySQL:
		name := lockName(s.db, s.cfg.Table)
		var got sql.NullInt64
		if err := s.conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, ?)`, name, waitSeconds(wait)).Scan(&got); err != nil {
			return nil, wrap(ctx, what, err)
		}
		switch {
		case !got.Valid:
			return nil, fmt.Errorf("migrate: %s: GET_LOCK(%q) returned NULL", what, name)
		case got.Int64 != 1:
			return nil, ErrLockWait
		}
		return func() { s.undo(ctx, `SELECT RELEASE_LOCK(?)`, name) }, nil
	case velox.SQLite:
		if wait != s.wait {
			if _, err := s.conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", waitMillis(wait))); err != nil {
				return nil, wrap(ctx, what, err)
			}
			s.wait = wait
		}
		return func() { s.undo(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", s.busyWas)) }, nil
	}
	return nil, refuseSession("no dialect %s", s.d)
}

// busyWait notes the connection's busy_timeout and sets it to wait: SQLite
// then waits that long for a writer, from the session's first query on.
// Neither statement reads the database, so neither is busy.
func (s *session) busyWait(ctx context.Context, wait time.Duration) error {
	wait = max(wait, 0)
	if err := s.conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&s.busyWas); err != nil {
		return err
	}
	if _, err := s.conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", waitMillis(wait))); err != nil {
		return err
	}
	s.wait = wait
	return nil
}

// waitMillis is a lock wait in milliseconds, rounded up, and at most
// 2147483647, the most PostgreSQL's lock_timeout and SQLite's busy_timeout
// take.
func waitMillis(d time.Duration) int64 {
	return min(ceilDiv(max(d, 0), time.Millisecond), math.MaxInt32)
}

// waitSeconds is a lock wait in whole seconds, rounded up, and at most
// 2147483647, for MySQL's GET_LOCK.
func waitSeconds(d time.Duration) int64 { return min(ceilDiv(max(d, 0), time.Second), math.MaxInt32) }

// ceilDiv is d in units, rounded up, for d of 0 or more.
func ceilDiv(d, unit time.Duration) int64 {
	n := d / unit
	if d%unit != 0 {
		n++
	}
	return int64(n)
}

// sqliteBusy reports SQLITE_BUSY, or one of its extended codes.
func sqliteBusy(err error) bool {
	code, ok := velox.DriverCode(err)
	if !ok {
		return false
	}
	n, cerr := strconv.Atoi(code)
	return cerr == nil && n&0xff == 5
}

// begin starts a file's transaction. On SQLite it is BEGIN IMMEDIATE, tried
// again until the lock wait has passed, so that a cancelled ctx ends the
// wait at once (SQLite's own busy wait does not see it); a database still
// busy is ErrLockWait.
func (s *session) begin(ctx context.Context) error {
	if s.d != velox.SQLite {
		_, err := s.conn.ExecContext(ctx, "BEGIN")
		return err
	}
	if _, err := s.conn.ExecContext(ctx, "PRAGMA busy_timeout = 0"); err != nil {
		return err
	}
	defer s.undo(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", waitMillis(s.wait)))
	deadline := time.Now().Add(s.wait)
	for {
		_, err := s.conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		if !sqliteBusy(err) {
			return err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return ErrLockWait
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(left, 10*time.Millisecond)):
		}
	}
}

// rollback ends a failed transaction, though ctx is gone.
func (s *session) rollback(ctx context.Context) { s.undo(ctx, "ROLLBACK") }
