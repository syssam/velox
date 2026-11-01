// Command tx shows transactions: commit and rollback with db.Tx, a nested Tx
// as a savepoint that recovers from a unique-key violation, retrying on
// serialization failures, and deadlines.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tx/gen"
	"github.com/syssam/velox/migrate"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// The tables come from migrations/, made, in the example module, by
//
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./tx/migrations ./tx/schema
//
// and applied with migrate.Up. The temporary SQLite database is this demo's;
// a real application opens its own.
//
//go:embed migrations
var migrations embed.FS

var errFunds = errors.New("insufficient funds")

// transfer moves amount between two accounts. It takes a *velox.DB, so it
// runs unchanged on db or on a transaction's tx.
func transfer(ctx context.Context, db *velox.DB, from, to string, amount int64) error {
	A := gen.Accounts
	n, err := db.Update[gen.Account]().
		Set(A.Balance.Sub(amount)).
		Where(A.Name.EQ(from), A.Balance.GTE(amount)).
		Exec(ctx)
	if err != nil {
		return err
	}
	if n != 1 { // the name is unique: one row, or none
		return fmt.Errorf("%s: %w", from, errFunds)
	}
	n, err = db.Update[gen.Account]().Set(A.Balance.Add(amount)).Where(A.Name.EQ(to)).Exec(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("no account %q", to)
	}
	return nil
}

func run(ctx context.Context, w io.Writer) error {
	dir, err := os.MkdirTemp("", "velox-tx-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	db, err := velox.Connect("sqlite", filepath.Join(dir, "tx.db"), velox.WithDeadlines(velox.Deadlines{
		Statement: 2 * time.Second, MaxStatement: 10 * time.Second,
		Tx: 5 * time.Second, MaxTx: 30 * time.Second,
	}))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	if _, err := migrate.Up(ctx, velox.Pool(db), sub); err != nil {
		return err
	}
	// Every statement and transaction gets a deadline when its context has
	// none, and none may run longer than the maximum.
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }
	balances := func() string {
		as, err := db.Query[gen.Account]().OrderBy(gen.Accounts.Name.Asc()).Limit(10).All(ctx)
		if err != nil {
			return err.Error()
		}
		var s []string
		for _, a := range as {
			s = append(s, fmt.Sprintf("%s=%d", a.Name, a.Balance))
		}
		return strings.Join(s, " ")
	}

	err = db.InsertAll([]gen.Account{{Name: "alice", Balance: 100}, {Name: "bob", Balance: 50}}).Exec(ctx)
	if err != nil {
		return err
	}
	say("start:    %s", balances())

	// fn returning nil commits. tx is a *velox.DB like db, and ctx is the
	// transaction's: Tx's, with its deadline. Under it, a statement on db
	// instead of tx fails with velox.ErrOutsideTx.
	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return transfer(ctx, tx, "alice", "bob", 30) })
	say("commit:   %s (err=%v)", balances(), err)

	// fn's error rolls back everything fn did: here the first transfer.
	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if err := transfer(ctx, tx, "bob", "alice", 10); err != nil {
			return err
		}
		return transfer(ctx, tx, "alice", "carol", 10)
	})
	say("rollback: %s (err=%v)", balances(), err)
	err = transfer(ctx, db, "bob", "alice", 500)
	say("no tx:    %s (errFunds=%t)", balances(), errors.Is(err, errFunds))

	// A nested Tx is a savepoint: its error undoes only its own work, and
	// the outer transaction goes on. A unique violation is a
	// *velox.ConstraintError on every dialect.
	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		acct := gen.Account{Name: "bob", Balance: 5}
		err := tx.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return tx.Insert(&acct).Exec(ctx) })
		var ce *velox.ConstraintError
		if errors.As(err, &ce) && ce.Kind == velox.ConstraintUnique {
			say("savepoint: %s constraint on %s; opening bob2 instead", ce.Kind, ce.Constraint)
			acct.Name = "bob2"
			err = tx.Insert(&acct).Exec(ctx)
		}
		return err
	})
	say("nested:   %s (err=%v)", balances(), err)

	// TxRetrySerializable runs fn again, up to n more times, after a
	// serialization failure or deadlock (SQLite: BUSY, LOCKED). fn must be
	// safe to run again. This database has one connection, so no conflict
	// happens and fn runs once.
	attempts := 0
	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		attempts++
		return transfer(ctx, tx, "bob2", "alice", 5)
	}, velox.TxRetrySerializable(3))
	say("retry:    %s (attempts=%d, err=%v)", balances(), attempts, err)

	// TxTimeout bounds one transaction, within WithDeadlines' MaxTx. Every
	// statement in it gets the transaction's deadline, and a transaction
	// past it is rolled back, never committed.
	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		time.Sleep(50 * time.Millisecond) // slow work while the transaction is open
		return transfer(ctx, tx, "alice", "bob", 1)
	}, velox.TxTimeout(10*time.Millisecond))
	say("timeout:  %s (ErrTimeout=%t, DeadlineExceeded=%t)",
		balances(), errors.Is(err, velox.ErrTimeout), errors.Is(err, context.DeadlineExceeded))
	return nil
}
