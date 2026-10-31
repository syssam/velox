package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// TestDryRunIsRouted pins that SQL(ctx) refuses a
// statement with no shard as a run would, calling WithShardFor once.
func TestDryRunIsRouted(t *testing.T) {
	s := openShards(true)
	q := s.db.Query[testmodel.Item]().Limit(1)
	for _, c := range []struct {
		ctx     context.Context
		lookups int32
	}{{bg, 0}, {velox.WithTenant(bg, int64(3)), 1}, {velox.WithTenant(bg, int64(4)), 1}} {
		s.lookups.Store(0)
		if _, _, err := q.SQL(c.ctx); !errors.Is(err, velox.ErrNoShard) {
			t.Errorf("Query.SQL: %v, want ErrNoShard", err)
		}
		if _, _, err := s.db.Update[testmodel.Item]().Set(It.Qty.Set(2)).Where(It.ID.EQ(1)).SQL(c.ctx); !errors.Is(err, velox.ErrNoShard) {
			t.Errorf("Update.SQL: %v, want ErrNoShard", err)
		}
		if _, _, err := s.db.Insert(&testmodel.Item{ID: 1}).SQL(c.ctx); !errors.Is(err, velox.ErrNoShard) {
			t.Errorf("Insert.SQL: %v, want ErrNoShard", err)
		}
		if n := s.lookups.Load(); n != 3*c.lookups {
			t.Errorf("WithShardFor called %d times for 3 dry runs, want %d", n, 3*c.lookups)
		}
	}

	tctx := velox.WithTenant(bg, int64(1))
	text, _, err := q.SQL(tctx)
	if err != nil || !strings.Contains(text, `FROM "items"`) {
		t.Fatalf("SQL with a tenant: %q, %v", text, err)
	}
	st := velox.Statement{Op: velox.OpSelect, Entity: infoOf[testmodel.Item](), Limit: 1}
	if _, _, err := dryRun(tctx, s.db, &st); err != nil {
		t.Fatal(err)
	}
	s.euR.Rows = [][]any{itemRow(1, 1, "A", 1)}
	if _, err := q.All(tctx); err != nil {
		t.Fatal(err)
	}
	s.check(t, true, [3]int{0, 1, 0}, euR0)
	if st.Route != euR0 {
		t.Errorf("the dry run's Route %+v, want %+v as the run's", st.Route, euR0)
	}
}
