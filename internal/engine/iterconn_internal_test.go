package engine

import (
	"strconv"
	"testing"

	"github.com/syssam/velox/internal/fakeconn"
)

// TestGoid: goid is nonzero, stable on a goroutine and differs between two.
func TestGoid(t *testing.T) {
	me := goid()
	if me == 0 || goid() != me {
		t.Errorf("goid %d, then %d", me, goid())
	}
	other := make(chan uint64)
	go func() { other <- goid() }()
	if g := <-other; g == 0 || g == me {
		t.Errorf("another goroutine's goid is %d; this one's %d", g, me)
	}
}

// BenchmarkGoid is goid at a depth of stack: runtime.Stack walks all of it, whatever
// the size of the buffer it is given.
func BenchmarkGoid(b *testing.B) {
	for _, depth := range []int{0, 20, 60} {
		b.Run(strconv.Itoa(depth)+"frames", func(b *testing.B) {
			deep(depth, func() {
				for b.Loop() {
					_ = goid()
				}
			})
		})
	}
}

// deep calls f under n more frames.
//
//go:noinline
func deep(n int, f func()) {
	if n == 0 {
		f()
		return
	}
	deep(n-1, f)
}

// TestHoldConnOnlyOnAPoolOfOne: a DB on any other pool notes nothing, and releasing
// that note leaves another goroutine's alone.
func TestHoldConnOnlyOnAPoolOfOne(t *testing.T) {
	db := newDB(SQLite, &fakeconn.Exec{}, nil)
	db.Cfg.connHolder.Store(7)
	h := holdConn(&db, 1)
	if h != 0 || db.Cfg.connHolder.Load() != 7 {
		t.Errorf("note %d; holder %d", h, db.Cfg.connHolder.Load())
	}
	releaseConn(&db, h)
	if db.Cfg.connHolder.Load() != 7 {
		t.Errorf("holder %d", db.Cfg.connHolder.Load())
	}
}
