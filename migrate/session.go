package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/syssam/velox2"
)

// A session is the one connection a run holds, checked against the
// directory's Config.
type session struct {
	conn    *sql.Conn
	d       velox.Dialect
	cfg     Config
	db      string // MySQL: DATABASE()
	sqlMode string // MySQL: @@SESSION.sql_mode
}

// refuseSession is a refusal of the connection.
func refuseSession(format string, args ...any) error {
	return &Error{Code: "session", Reason: fmt.Sprintf(format, args...)}
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
// Its refusals are *Error with Code "session".
func open(ctx context.Context, db *sql.DB, cfg Config) (*session, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	s := &session{conn: conn, d: cfg.Dialect, cfg: cfg}
	if err := s.check(ctx); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
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
		err = s.conn.QueryRowContext(ctx, `SELECT @@version, DATABASE(), @@SESSION.sql_mode`).Scan(&version, &db, &s.sqlMode)
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
			return err
		}
		return refuseSession("the database does not answer as %s, velox.json's dialect: %v", dialectName(s.d), err)
	}
	return checkVersion(s.d, version)
}

func (s *session) close() { _ = s.conn.Close() }

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
			return refuseSession("the server is PostgreSQL %d.%d; migrate needs 12 or later", n/10000, n%10000)
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
// a timeout is ErrLockWait. A wait of 0 or less does not wait. On SQLite it
// sets busy_timeout to wait for the run, and each file's BEGIN IMMEDIATE is
// the lock.
func (s *session) lock(ctx context.Context, wait time.Duration) (unlock func(), err error) {
	wait = max(wait, 0)
	switch s.d {
	case velox.Postgres:
		key := lockKey(s.cfg.Table)
		var ok bool
		if wait == 0 {
			err = s.conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok)
		} else {
			if _, err = s.conn.ExecContext(ctx, fmt.Sprintf("SET lock_timeout = '%dms'", ceilDiv(wait, time.Millisecond))); err != nil {
				return nil, err
			}
			_, err = s.conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key)
			ok = err == nil
			if code, _ := velox.DriverCode(err); code == "55P03" {
				err = nil
			}
			c, cancel := cleanup(ctx)
			_, rerr := s.conn.ExecContext(c, `RESET lock_timeout`)
			cancel()
			if err == nil && rerr != nil {
				err = rerr
			}
		}
		switch {
		case err != nil:
			return nil, err
		case !ok:
			return nil, ErrLockWait
		}
		return func() {
			c, cancel := cleanup(ctx)
			defer cancel()
			_, _ = s.conn.ExecContext(c, `SELECT pg_advisory_unlock($1)`, key)
		}, nil
	case velox.MySQL:
		name := lockName(s.db, s.cfg.Table)
		var got sql.NullInt64
		if err := s.conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, ?)`, name, ceilDiv(wait, time.Second)).Scan(&got); err != nil {
			return nil, err
		}
		switch {
		case !got.Valid:
			return nil, fmt.Errorf("migrate: GET_LOCK(%q) failed", name)
		case got.Int64 != 1:
			return nil, ErrLockWait
		}
		return func() {
			c, cancel := cleanup(ctx)
			defer cancel()
			_, _ = s.conn.ExecContext(c, `SELECT RELEASE_LOCK(?)`, name)
		}, nil
	case velox.SQLite:
		var was int64
		if err := s.conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&was); err != nil {
			return nil, err
		}
		if _, err := s.conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", ceilDiv(wait, time.Millisecond))); err != nil {
			return nil, err
		}
		return func() {
			c, cancel := cleanup(ctx)
			defer cancel()
			_, _ = s.conn.ExecContext(c, fmt.Sprintf("PRAGMA busy_timeout = %d", was))
		}, nil
	}
	return nil, refuseSession("no dialect %s", s.d)
}

// ceilDiv is d in units, rounded up.
func ceilDiv(d, unit time.Duration) int64 {
	return int64((d + unit - 1) / unit)
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

// begin starts a file's transaction: BEGIN IMMEDIATE on SQLite, where a busy
// database is ErrLockWait.
func (s *session) begin(ctx context.Context) error {
	q := "BEGIN"
	if s.d == velox.SQLite {
		q = "BEGIN IMMEDIATE"
	}
	_, err := s.conn.ExecContext(ctx, q)
	if s.d == velox.SQLite && sqliteBusy(err) {
		return ErrLockWait
	}
	return err
}

// rollback ends a failed transaction, though ctx is gone.
func (s *session) rollback(ctx context.Context) {
	c, cancel := cleanup(ctx)
	defer cancel()
	_, _ = s.conn.ExecContext(c, "ROLLBACK")
}
