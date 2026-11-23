package veloxtest

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
)

// errRolledBack is what RolledBack's function returns to db.Tx, so the
// transaction rolls back.
var errRolledBack = errors.New("veloxtest: rolled back")

// RolledBack runs fn in a transaction of db and rolls it back, whatever fn
// does: a test's writes are seen inside fn and by no one after it. It fails
// t when the transaction cannot begin or roll back. fn reports through t;
// a t.Fatal in fn rolls the transaction back too.
//
//	veloxtest.RolledBack(t, ctx, db, func(ctx context.Context, tx *velox.DB) {
//		if err := tx.Insert(&post).Exec(ctx); err != nil {
//			t.Fatal(err)
//		}
//	})
func RolledBack(t testing.TB, ctx context.Context, db *velox.DB, fn func(ctx context.Context, tx *velox.DB)) {
	t.Helper()
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		fn(ctx, tx)
		return errRolledBack
	})
	if !errors.Is(err, errRolledBack) {
		t.Fatalf("veloxtest: RolledBack: %v", err)
	}
}
