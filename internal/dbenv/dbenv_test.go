package dbenv_test

import (
	"fmt"
	"testing"

	"github.com/syssam/velox/internal/dbenv"
)

// fakeT records what dbenv does to a test.
type fakeT struct{ fatal, skip string }

func (*fakeT) Helper()                             {}
func (f *fakeT) Fatalf(format string, args ...any) { f.fatal = fmt.Sprintf(format, args...) }
func (f *fakeT) Skipf(format string, args ...any)  { f.skip = fmt.Sprintf(format, args...) }

// TestNeedAndOptional: a set variable is returned; an unset one skips Need's
// test and leaves Optional's database out, or fails either test under
// VELOX_ITEST_REQUIRE=1.
func TestNeedAndOptional(t *testing.T) {
	const name = "VELOX_DBENV_TEST_DSN"
	for _, c := range []struct {
		value, require string
		want           string
		fatal, skip    bool // Need's; Optional never skips
	}{
		{"dsn", "", "dsn", false, false},
		{"dsn", "1", "dsn", false, false},
		{"", "", "", false, true},
		{"", "0", "", false, true},
		{"", "1", "", true, false},
	} {
		t.Run(fmt.Sprintf("value=%q,require=%q", c.value, c.require), func(t *testing.T) {
			t.Setenv(name, c.value)
			t.Setenv(dbenv.Required, c.require)
			var need, opt fakeT
			if got := dbenv.Need(&need, name); got != c.want {
				t.Errorf("Need: %q, want %q", got, c.want)
			}
			if got := dbenv.Optional(&opt, name); got != c.want {
				t.Errorf("Optional: %q, want %q", got, c.want)
			}
			if (need.fatal != "") != c.fatal || (opt.fatal != "") != c.fatal {
				t.Errorf("failed Need %q, Optional %q; want failed %v", need.fatal, opt.fatal, c.fatal)
			}
			// Need skips only when it did not fail: a real Fatalf stops the test.
			if (need.skip != "") != (c.skip || c.fatal) || opt.skip != "" {
				t.Errorf("skipped Need %q, Optional %q", need.skip, opt.skip)
			}
		})
	}
}
