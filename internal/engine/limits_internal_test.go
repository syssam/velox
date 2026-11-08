package engine

import (
	"context"
	"testing"
	"time"

	"github.com/syssam/velox/internal/fakeconn"
)

// TestLimitsDefault: WithMaxParams and WithRollbackTimeout of 0 or less, and
// a RenderCache's bounds of 0, are the defaults; a cache's bound below 0
// holds nothing.
func TestLimitsDefault(t *testing.T) {
	for _, c := range []struct {
		d    Dialect
		opts []Option
		want int
	}{
		{Postgres, nil, 65535},
		{MySQL, nil, 65535},
		{SQLite, nil, 32766},
		{SQLite, []Option{WithMaxParams(0)}, 32766},
		{Postgres, []Option{WithMaxParams(-3)}, 65535},
		{Postgres, []Option{WithMaxParams(10)}, 10},
	} {
		db := newDB(c.d, &fakeconn.Exec{}, c.opts)
		if got := db.maxParams(); got != c.want {
			t.Errorf("%v with %d options: maxParams %d, want %d", c.d, len(c.opts), got, c.want)
		}
	}

	// bounded reports whether the rollback context's deadline is d from a
	// moment within the call.
	bounded := func(d time.Duration, opts ...Option) bool {
		db := newDB(SQLite, &fakeconn.Exec{}, opts)
		start := time.Now()
		rctx, cancel := db.rollbackContext(context.Background(), time.Time{})
		end := time.Now()
		defer cancel()
		dl, ok := rctx.Deadline()
		return ok && !dl.Before(start.Add(d)) && !dl.After(end.Add(d))
	}
	for name, opts := range map[string][]Option{"none": nil, "0": {WithRollbackTimeout(0)}, "-1s": {WithRollbackTimeout(-time.Second)}} {
		if !bounded(DefaultRollbackTimeout, opts...) {
			t.Errorf("rollback timeout %s: the deadline is not DefaultRollbackTimeout from now", name)
		}
	}
	if !bounded(50*time.Millisecond, WithRollbackTimeout(50*time.Millisecond)) {
		t.Error("rollback timeout 50ms: the deadline is not 50ms from now")
	}

	var zero RenderCache
	if zero.maxShapes() != DefaultMaxShapes || zero.maxBytes() != DefaultMaxCacheBytes {
		t.Errorf("a zero cache's bounds: %d shapes, %d bytes", zero.maxShapes(), zero.maxBytes())
	}
	none := RenderCache{MaxBytes: -1}
	st := &Statement{Op: OpSelect, Entity: &EntityInfo{Name: "E", Table: "e", Columns: []ColumnInfo{{Name: "id"}}}, Columns: []int{0}}
	if _, _, err := none.Get(SQLite, st); err != nil {
		t.Fatal(err)
	}
	if none.Size() != 0 || none.Bytes != 0 {
		t.Errorf("a cache of MaxBytes -1 holds %d shapes, %d bytes", none.Size(), none.Bytes)
	}
}
