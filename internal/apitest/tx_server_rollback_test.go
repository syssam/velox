package apitest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

const rollbackOnTimeout = "SELECT @@innodb_rollback_on_timeout"

// TestMySQLDeadlockBreaksTheTransaction: MySQL's 1213 rolled the whole
// transaction back and left the connection in autocommit, so an fn that
// swallows it must not go on as if it were still in one: later statements fail
// unsent, and fn's nil rolls back with the deadlock's error.
func TestMySQLDeadlockBreaksTheTransaction(t *testing.T) {
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	e := &fakeconn.Exec{Errs: []error{dead}}
	db := openFake(velox.MySQL, e)
	var first, second error
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, first = tx.Exec(ctx, "test", "UPDATE t SET a = 1")
		_, second = tx.Exec(ctx, "test", "UPDATE t SET a = 2")
		return nil
	})
	if !errors.Is(first, dead) || !errors.Is(second, dead) || !errors.Is(err, dead) {
		t.Errorf("first %v; second %v; Tx %v", first, second, err)
	}
	if want := []string{"BEGIN", "UPDATE t SET a = 1", "ROLLBACK"}; !slices.Equal(e.Log, want) {
		t.Errorf("sent %q, want %q", e.Log, want)
	}
}

// TestMySQLDeadlockFromRowsBreaksTheTransaction: the deadlock can come as the
// rows' error.
func TestMySQLDeadlockFromRowsBreaksTheTransaction(t *testing.T) {
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	e := &fakeconn.Exec{RowsErrs: []error{dead}}
	db := openFake(velox.MySQL, e)
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, _ = tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
		return nil
	})
	if !errors.Is(err, dead) || slices.Contains(e.Log, "COMMIT") {
		t.Errorf("Tx %v; sent %q", err, e.Log)
	}
}

// TestMySQLDeadlockSwallowedIsRetried: the commit's refusal carries the
// deadlock, which TxRetryRolledBack retries.
func TestMySQLDeadlockSwallowedIsRetried(t *testing.T) {
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	e := &fakeconn.Exec{Errs: []error{dead}}
	db := openFake(velox.MySQL, e)
	attempts := 0
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		attempts++
		_, _ = tx.Exec(ctx, "test", "UPDATE t SET a = 1")
		return nil
	}, velox.TxRetryRolledBack(1))
	if err != nil || attempts != 2 {
		t.Errorf("%v after %d attempts", err, attempts)
	}
}

// TestMySQLLockTimeout: 1205 rolls back the statement, and the transaction
// only under innodb_rollback_on_timeout, which is asked. No answer counts as
// on.
func TestMySQLLockTimeout(t *testing.T) {
	timeout := &mysql.MySQLError{Number: 1205, Message: "Lock wait timeout exceeded"}
	for name, c := range map[string]struct {
		e      *fakeconn.Exec
		broken bool
	}{
		"off":       {&fakeconn.Exec{Errs: []error{timeout}, Rows: [][]any{{int64(0)}}}, false},
		"on":        {&fakeconn.Exec{Errs: []error{timeout}, Rows: [][]any{{int64(1)}}}, true},
		"no answer": {&fakeconn.Exec{Errs: []error{timeout, errors.New("gone")}}, true},
		"no row":    {&fakeconn.Exec{Errs: []error{timeout}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			err := openFake(velox.MySQL, c.e).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				_, _ = tx.Exec(ctx, "test", "UPDATE t SET a = 1")
				return nil
			})
			if !slices.Contains(c.e.Log, rollbackOnTimeout) {
				t.Errorf("the server was not asked: %q", c.e.Log)
			}
			if c.broken != errors.Is(err, timeout) || c.broken == slices.Contains(c.e.Log, "COMMIT") {
				t.Errorf("Tx %v; sent %q", err, c.e.Log)
			}
		})
	}
}

// TestOtherDatabasesKeepTheirTransaction: PostgreSQL aborts a transaction and
// refuses its COMMIT itself, and an error of another number leaves MySQL's.
func TestOtherDatabasesKeepTheirTransaction(t *testing.T) {
	for name, c := range map[string]struct {
		d   velox.Dialect
		err error
	}{
		"postgres": {velox.Postgres, &pgconn.PgError{Code: "40P01"}},
		"mysql":    {velox.MySQL, &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}},
	} {
		t.Run(name, func(t *testing.T) {
			e := &fakeconn.Exec{Errs: []error{c.err}}
			err := openFake(c.d, e).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				_, _ = tx.Exec(ctx, "test", "UPDATE t SET a = 1")
				return nil
			})
			if err != nil || !slices.Contains(e.Log, "COMMIT") {
				t.Errorf("Tx %v; sent %q", err, e.Log)
			}
		})
	}
}

// TestBrokenTransactionSendsNothing: the check is where a statement is sent, so
// an Around hook that retries next after a 1213 cannot run it in autocommit, and
// the statement fails rather than return the retry's nil.
func TestBrokenTransactionSendsNothing(t *testing.T) {
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	e := &fakeconn.Exec{Errs: []error{dead}}
	db := openFake(velox.MySQL, e, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		if err := next(ctx); err == nil {
			return nil
		}
		return next(ctx)
	}))
	var stmt error
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, stmt = tx.Exec(ctx, "test", "UPDATE t SET a = 1")
		return nil
	})
	if !errors.Is(stmt, dead) || !errors.Is(err, dead) {
		t.Errorf("the statement %v; Tx %v", stmt, err)
	}
	if want := []string{"BEGIN", "UPDATE t SET a = 1", "ROLLBACK"}; !slices.Equal(e.Log, want) {
		t.Errorf("sent %q, want %q", e.Log, want)
	}
}
