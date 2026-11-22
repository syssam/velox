package engine

import (
	"testing"
	"unsafe"
)

// TestStatementSize pins a Statement inside the heap's 640-byte size class.
// An update allocates one, and an object over 512 bytes carries an 8-byte
// header besides: 632 bytes is the most that fits. A field that pushes it
// over costs each such statement 64 bytes; group a new one-byte field with
// the others.
func TestStatementSize(t *testing.T) {
	if n := unsafe.Sizeof(Statement{}); n > 632 {
		t.Errorf("a Statement is %d bytes, over 632: with its heap header it leaves the 640-byte size class", n)
	}
}
