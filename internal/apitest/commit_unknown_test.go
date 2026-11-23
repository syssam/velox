package apitest

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestCommitUnknown: a COMMIT that fails with an error that does not say the
// transaction rolled back matches ErrCommitUnknown, so a caller that retries
// on its own can tell "may have committed" from "did not". The driver's error
// and the message are unchanged. A deferred constraint and a serialization
// failure at COMMIT are rollbacks, and an error before COMMIT never reached
// it: none of them match.
func TestCommitUnknown(t *testing.T) {
	boom := errors.New("connection reset by peer")
	for _, tc := range []struct {
		name    string
		exec    func() *fakeconn.Exec
		fn      func(context.Context, *velox.DB) error
		unknown bool
	}{
		{name: "driver error at commit", exec: func() *fakeconn.Exec { return &fakeconn.Exec{CommitErr: boom} }, unknown: true},
		{name: "bad connection at commit", exec: func() *fakeconn.Exec { return &fakeconn.Exec{CommitErr: driver.ErrBadConn} }, unknown: true},
		{name: "serialization failure at commit", exec: func() *fakeconn.Exec { return &fakeconn.Exec{CommitErr: &pgconn.PgError{Code: "40001"}} }},
		{name: "deferred constraint at commit", exec: func() *fakeconn.Exec {
			return &fakeconn.Exec{CommitErr: &pgconn.PgError{Code: "23503", ConstraintName: "items_invoice_fk"}}
		}},
		{name: "begin fails", exec: func() *fakeconn.Exec { return &fakeconn.Exec{BeginErr: boom} }},
		{name: "fn fails", exec: func() *fakeconn.Exec { return &fakeconn.Exec{} }, fn: func(context.Context, *velox.DB) error { return boom }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := tc.fn
			if fn == nil {
				fn = func(context.Context, *velox.DB) error { return nil }
			}
			err := openFake(velox.Postgres, tc.exec()).Tx(context.Background(), fn)
			if err == nil {
				t.Fatal("Tx succeeded")
			}
			if got := errors.Is(err, velox.ErrCommitUnknown); got != tc.unknown {
				t.Errorf("errors.Is(%v, ErrCommitUnknown) = %v, want %v", err, got, tc.unknown)
			}
			if tc.unknown && (!errors.Is(err, tc.exec().CommitErr) || !strings.HasPrefix(err.Error(), "velox: commit: ")) {
				t.Errorf("%v: the driver's error or the message changed", err)
			}
		})
	}
}

// TestCommitUnknownWhenCanceledDuringCommit: a context that ends while COMMIT
// is on the wire leaves the outcome unknown too: the server may have
// committed before the cancel reached it. The error matches both.
func TestCommitUnknownWhenCanceledDuringCommit(t *testing.T) {
	e := &fakeconn.Exec{CommitErr: driver.ErrBadConn}
	db := openFake(velox.Postgres, e)
	cctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := db.Tx(cctx, func(context.Context, *velox.DB) error {
		e.BlockCommit = true
		time.AfterFunc(20*time.Millisecond, cancel)
		return nil
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, velox.ErrCommitUnknown) {
		t.Errorf("%v: want context.Canceled and ErrCommitUnknown", err)
	}
}

// TestCommitUnknownIsNotRetried: TxRetrySerializable never runs fn again after
// a COMMIT whose outcome is unknown, so a committed transaction is not
// applied twice.
func TestCommitUnknownIsNotRetried(t *testing.T) {
	e := &fakeconn.Exec{CommitErr: driver.ErrBadConn}
	runs := 0
	err := openFake(velox.Postgres, e).Tx(context.Background(), func(context.Context, *velox.DB) error {
		runs++
		return nil
	}, velox.TxRetrySerializable(3))
	if !errors.Is(err, velox.ErrCommitUnknown) || runs != 1 {
		t.Errorf("%v, runs %d: want ErrCommitUnknown after one run", err, runs)
	}
}
