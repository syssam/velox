package engine

import "testing"

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

func BenchmarkGoid(b *testing.B) {
	for b.Loop() {
		_ = goid()
	}
}
