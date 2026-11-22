package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox/examples/internal/golden"
)

// TestOutput runs the example and compares what it prints with
// testdata/output.golden; go test -update rewrites the file.
func TestOutput(t *testing.T) {
	var out strings.Builder
	if err := run(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	golden.Check(t, out.String())
}

// failAfter is a context that is cancelled once Done has been asked n times.
type failAfter struct {
	context.Context
	mu   sync.Mutex
	left int
	done chan struct{}
}

func (c *failAfter) Done() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.left == 0 {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
	} else {
		c.left--
	}
	return c.done
}

func (c *failAfter) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

// TestRunStops: the example returns the error of whichever statement fails
// and prints no more, which is how a real application would meet a database
// that goes away part-way. The context is cancelled after n checks, for
// each n until the example finishes.
func TestRunStops(t *testing.T) {
	for n := 0; ; n += 97 {
		ctx := &failAfter{Context: t.Context(), left: n, done: make(chan struct{})}
		err := run(ctx, io.Discard)
		if err == nil {
			t.Logf("finished with %d checks", n)
			if n == 0 {
				t.Fatal("a cancelled context did not stop the example")
			}
			return
		}
		if n > 5000 {
			t.Fatalf("still failing after %d checks: %v", n, err)
		}
	}
}
