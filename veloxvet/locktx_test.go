package veloxvet_test

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/syssam/velox/veloxvet"
)

// TestLockTx runs the analyzer, enabled, on testdata/l, which uses velox's
// real packages and its test model, and on testdata/lhelp, whose facts
// testdata/l imports.
func TestLockTx(t *testing.T) {
	enableLockTx(t, "true")
	analysistest.Run(t, testModule(t), veloxvet.LockTx, "./lhelp", "./l")
}

// TestLockTxOff checks that the analyzer reports nothing and exports no
// fact without its flag, on the same packages.
func TestLockTxOff(t *testing.T) {
	enableLockTx(t, "false")
	var rec recorder
	for _, r := range analysistest.Run(&rec, testModule(t), veloxvet.LockTx, "./lhelp", "./l") {
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		if len(r.Diagnostics) > 0 {
			t.Errorf("%s: %d diagnostics with the check off, the first: %s", r.Pass.Pkg.Path(), len(r.Diagnostics), r.Diagnostics[0].Message)
		}
	}
	if len(rec.errs) == 0 {
		t.Error("analysistest found no unmet expectation: the check ran")
	}
	for _, e := range rec.errs {
		if !isUnmet(e) {
			t.Errorf("unexpected error: %s", e)
		}
	}
}

func enableLockTx(t *testing.T, v string) {
	t.Helper()
	if err := veloxvet.LockTx.Flags.Set("enable", v); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = veloxvet.LockTx.Flags.Set("enable", "false") })
}

// recorder is an analysistest.Testing that keeps its errors.
type recorder struct{ errs []string }

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// isUnmet reports whether e is analysistest's error for an expectation
// that nothing met: with the check off, every one of testdata/l's.
func isUnmet(e string) bool {
	for _, s := range []string{"no diagnostic was reported", "no fact was reported"} {
		if strings.Contains(e, s) {
			return true
		}
	}
	return false
}
