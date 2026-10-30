package apitest

import (
	"testing"

	"github.com/syssam/velox2"
)

// TestOpRawAndReads pins which Ops are raw SQL and which read.
func TestOpRawAndReads(t *testing.T) {
	cases := []struct {
		op         velox.Op
		raw, reads bool
	}{
		{velox.OpSelect, false, true},
		{velox.OpInsert, false, false},
		{velox.OpUpdate, false, false},
		{velox.OpDelete, false, false},
		{velox.OpRawQuery, true, true},
		{velox.OpRawExec, true, false},
		{velox.Op(0), false, false},
		{velox.Op(99), false, false},
	}
	for _, c := range cases {
		if got := c.op.Raw(); got != c.raw {
			t.Errorf("%v.Raw() = %v, want %v", c.op, got, c.raw)
		}
		if got := c.op.Reads(); got != c.reads {
			t.Errorf("%v.Reads() = %v, want %v", c.op, got, c.reads)
		}
	}
	if got := velox.Op(99).String(); got != "unknown" {
		t.Errorf("Op(99).String() = %q, want unknown", got)
	}
}
