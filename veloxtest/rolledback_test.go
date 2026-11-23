package veloxtest_test

import (
	"context"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/veloxtest"
)

// TestRolledBack: RolledBack's writes are seen inside it and gone after,
// also when fn ends the test with t.SkipNow (runtime.Goexit, as t.Fatal
// does), and the pool of one is free again afterwards.
func TestRolledBack(t *testing.T) {
	ctx := context.Background()
	db := veloxtest.Open(t, "sqlite", "", schemas)
	insert := func(ctx context.Context, tx *velox.DB) {
		t.Helper()
		if err := tx.Insert(&testmodel.Sample{Name: "a", Status: "new", When: time.Now(), Blob: []byte("b")}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n, err := tx.Query[testmodel.Sample]().Count(ctx); err != nil || n != 1 {
			t.Fatalf("inside: %d, %v", n, err)
		}
	}
	veloxtest.RolledBack(t, ctx, db, insert)
	t.Run("goexit", func(t *testing.T) {
		veloxtest.RolledBack(t, ctx, db, func(ctx context.Context, tx *velox.DB) {
			insert(ctx, tx)
			t.SkipNow()
		})
	})
	if n, err := db.Query[testmodel.Sample]().Count(ctx); err != nil || n != 0 {
		t.Errorf("after: %d samples, %v; want 0", n, err)
	}
}
