package gen

import (
	"runtime/debug"
	"testing"
)

// TestRelaxGC pins the generation-time GC contract: GOGC is raised only
// while the returned restore function has not run, the previous value
// comes back exactly, and an explicit GOGC in the environment is honored
// by leaving the runtime setting alone.
func TestRelaxGC(t *testing.T) {
	t.Setenv("GOGC", "")
	before := debug.SetGCPercent(100)
	debug.SetGCPercent(before)

	restore := relaxGC()
	if got := debug.SetGCPercent(-1); got != generationGCPercent {
		t.Fatalf("during generation GOGC=%d, want %d", got, generationGCPercent)
	}
	debug.SetGCPercent(generationGCPercent)
	restore()
	if got := debug.SetGCPercent(before); got != before {
		t.Fatalf("after restore GOGC=%d, want %d", got, before)
	}

	t.Setenv("GOGC", "50")
	prev := debug.SetGCPercent(50)
	relaxGC()()
	if got := debug.SetGCPercent(prev); got != 50 {
		t.Fatalf("explicit GOGC must be left alone, got %d", got)
	}
}
