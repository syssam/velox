// Package lhelp holds helpers whose facts the locktx analyzer passes to
// the packages that call them.
package lhelp

import (
	"context"

	"github.com/syssam/velox"
	tm "github.com/syssam/velox/internal/testmodel"
)

var I = tm.Invoices

// LockRow locks on its parameter: each caller is checked in its place.
func LockRow(ctx context.Context, tx *velox.DB, id int64) error { // want LockRow:`LocksOnParam\(p1\)`
	_, err := tx.Query[tm.Invoice]().Where(I.ID.EQ(id)).ForUpdate().All(ctx)
	return err
}

// Forward passes its parameter on: it gets the fact too.
func Forward(ctx context.Context, db *velox.DB, id int64) error { // want Forward:`LocksOnParam\(p1\)`
	return LockRow(ctx, db, id)
}

// Load locks only when lock is true.
func Load(ctx context.Context, db *velox.DB, id int64, lock bool) error { // want Load:`LocksOnParam\(p1 when p3==true\)`
	q := db.Query[tm.Invoice]().Where(I.ID.EQ(id))
	if lock {
		q = q.ForUpdate()
	}
	_, err := q.All(ctx)
	return err
}

// WithTx joins the transaction ctx carries, else begins one: fn gets only
// a transaction's handle.
func WithTx(ctx context.Context, db *velox.DB, fn func(ctx context.Context, tx *velox.DB) error) error { // want WithTx:`TxCallback\(p2\)`
	if tx, ok := velox.TxFrom(ctx, db); ok {
		return fn(ctx, tx)
	}
	return db.Tx(ctx, fn)
}

// Base starts a query on its parameter.
func Base(db *velox.DB) velox.Query[tm.Invoice] { // want Base:`QueryFrom\(p0\)`
	return db.Query[tm.Invoice]().Where(I.Paid.EQ(false))
}

// Locked starts a locked query on its parameter.
func Locked(db *velox.DB) velox.Query[tm.Invoice] { // want Locked:`QueryFrom\(p0 locked\)`
	return db.Query[tm.Invoice]().ForShare()
}

// Hold begins a transaction in a goroutine and runs fn in it.
func Hold(ctx context.Context, db *velox.DB, fn func(ctx context.Context, tx *velox.DB) error) chan error { // want Hold:`TxCallback\(p2\)`
	ready := make(chan error, 1)
	go func() {
		_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			err := fn(ctx, tx)
			ready <- err
			return err
		})
	}()
	return ready
}
