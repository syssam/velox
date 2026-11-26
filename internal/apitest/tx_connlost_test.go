package apitest

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"reflect"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestTxRetryRolledBackLostConnection: a statement that fails on a lost or
// refused connection rolled the transaction back with the connection, so
// TxRetryRolledBack runs fn again; a failed login, a violated constraint and an
// error of fn's own do not.
func TestTxRetryRolledBackLostConnection(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	for _, c := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"a bad connection", driver.ErrBadConn, true},
		{"an unexpected EOF", io.ErrUnexpectedEOF, true},
		{"a reset", reset, true},
		{"admin shutdown", &pgconn.PgError{Code: "57P01"}, true},
		{"cannot connect now", &pgconn.PgError{Code: "57P03"}, true},
		{"a failed login", &pgconn.PgError{Code: "28P01"}, false},
		{"a unique violation", &pgconn.PgError{Code: "23505"}, false},
		{"a timeout", &net.OpError{Op: "read", Net: "tcp", Err: timeoutError{}}, false},
		{"another error", errors.New("boom"), false},
	} {
		e := &fakeconn.Exec{Errs: []error{c.err}}
		db := openFake(velox.Postgres, e)
		runs := 0
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) }, velox.TxRetryRolledBack(2))
		if c.retry {
			want := []string{"BEGIN", touchSQL, "ROLLBACK", "BEGIN", touchSQL, "COMMIT"}
			if err != nil || runs != 2 || !reflect.DeepEqual(e.Log, want) {
				t.Errorf("%s: err %v, runs %d, log %q", c.name, err, runs, e.Log)
			}
		} else if err == nil || runs != 1 {
			t.Errorf("%s: err %v, runs %d; want one run and an error", c.name, err, runs)
		}

		// Without the option nothing runs again.
		e = &fakeconn.Exec{Errs: []error{c.err}}
		db = openFake(velox.Postgres, e)
		runs = 0
		if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) }); err == nil || runs != 1 {
			t.Errorf("%s without the option: err %v, runs %d", c.name, err, runs)
		}
	}
}

// timeoutError is a net.Error that is a timeout: a read that timed out may be a
// slow query, not a lost connection.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// An error of fn's own, io.EOF for one, is not a lost connection unless a
// statement lost it.
func TestTxRetryRolledBackIgnoresFnsOwnEOF(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	runs := 0
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		runs++
		if err := touch(ctx, tx); err != nil {
			return err
		}
		return io.EOF // reading a file, say
	}, velox.TxRetryRolledBack(3))
	if !errors.Is(err, io.EOF) || runs != 1 {
		t.Errorf("err %v, runs %d; want io.EOF and one run", err, runs)
	}
}

// A connection refused at BEGIN is retried, and a lost connection at COMMIT is
// not: its outcome is unknown.
func TestTxRetryRolledBackBeginAndCommit(t *testing.T) {
	e := &fakeconn.Exec{BeginErr: &pgconn.PgError{Code: "57P03"}}
	db := openFake(velox.Postgres, e)
	runs := 0
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return nil }, velox.TxRetryRolledBack(2))
	if err == nil || runs != 0 {
		t.Errorf("BEGIN refused: err %v, runs %d", err, runs)
	}
	begins := 0
	for _, q := range e.Log {
		if q == "BEGIN" {
			begins++
		}
	}
	if begins != 3 {
		t.Errorf("BEGIN refused, retried twice: %d BEGINs in %q", begins, e.Log)
	}

	e = &fakeconn.Exec{CommitErr: driver.ErrBadConn}
	db = openFake(velox.Postgres, e)
	runs = 0
	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) }, velox.TxRetryRolledBack(3))
	if !errors.Is(err, velox.ErrCommitUnknown) || runs != 1 {
		t.Errorf("COMMIT lost: err %v, runs %d; want ErrCommitUnknown and one run", err, runs)
	}
}
