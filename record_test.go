package velox_test

import (
	"context"
	"sync"
	"testing"

	"github.com/syssam/velox"
)

// TestRecorderConcurrent: Record is safe from many goroutines at once.
func TestRecorderConcurrent(t *testing.T) {
	var rec velox.Recorder
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if err := rec.Record(t.Context(), velox.Change{Entity: "X"}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if n := rec.Len(); n != 800 {
		t.Errorf("Len = %d, want 800", n)
	}
	rec.Reset()
	if n := rec.Len(); n != 0 {
		t.Errorf("Len after Reset = %d", n)
	}
}

// TestRecorderAllIsASnapshot: All yields what was recorded when it was
// called, in order; a Record during the iteration does not join it.
func TestRecorderAllIsASnapshot(t *testing.T) {
	var rec velox.Recorder
	ctx := context.Background()
	_ = rec.Record(ctx, velox.Change{Key: 1})
	_ = rec.Record(ctx, velox.Change{Key: 2})
	var keys []any
	for c := range rec.All() {
		keys = append(keys, c.Key)
		_ = rec.Record(ctx, velox.Change{Key: 3})
	}
	if len(keys) != 2 || keys[0] != 1 || keys[1] != 2 {
		t.Errorf("All yielded %v, want [1 2]", keys)
	}
	if rec.Len() != 4 {
		t.Errorf("Len = %d, want 4", rec.Len())
	}
}

// TestRecordFuncIsASink: a RecordFunc is a Sink that calls the function.
func TestRecordFuncIsASink(t *testing.T) {
	var got []velox.Change
	var s velox.Sink = velox.RecordFunc(func(_ context.Context, c velox.Change) error {
		got = append(got, c)
		return nil
	})
	_ = s.Record(t.Context(), velox.Change{Entity: "X"})
	if len(got) != 1 || got[0].Entity != "X" {
		t.Errorf("got %v", got)
	}
}
