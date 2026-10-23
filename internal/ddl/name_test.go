package ddl

import (
	"fmt"
	"hash/fnv"
	"strings"
	"testing"
	"unicode/utf8"
)

func fnv32a(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

func TestCap(t *testing.T) {
	for _, n := range []int{62, 63} {
		if s := strings.Repeat("a", n); Cap(s) != s {
			t.Errorf("Cap of %d bytes changed it: %q", n, Cap(s))
		}
	}
	a64 := strings.Repeat("a", 64)
	want := strings.Repeat("a", 54) + "_" + fmt.Sprintf("%08x", fnv32a(a64))
	if got := Cap(a64); got != want || len(got) != MaxName {
		t.Errorf("Cap(a64) = %q (%d bytes), want %q", got, len(got), want)
	}
	// The cut falls back before a rune that would cross byte 54.
	s := strings.Repeat("a", 53) + "é" + strings.Repeat("b", 20)
	got := Cap(s)
	if want := strings.Repeat("a", 53) + "_" + fmt.Sprintf("%08x", fnv32a(s)); got != want || len(got) != 62 || !utf8.ValidString(got) {
		t.Errorf("Cap = %q (%d bytes), want %q", got, len(got), want)
	}
}

// A helper is capped as a whole, so it stays within the cap and apart from the
// object it stands beside.
func TestCapHelpers(t *testing.T) {
	base := strings.Repeat("t", MaxName)
	for _, c := range []struct{ got, whole string }{
		{OldEnum(base), base + "_velox_old"},
		{NotNull(base, "c"), base + "_c_not_null"},
		{Rebuild(base), "velox_new_" + base},
	} {
		if len(c.got) > MaxName || c.got == base || c.got == Cap(base) || c.got != Cap(c.whole) {
			t.Errorf("got %q, want Cap(%q)", c.got, c.whole)
		}
	}
	for got, want := range map[string]string{
		OldEnum("users_role"):     "users_role_velox_old",
		NotNull("users", "email"): "users_email_not_null",
		Rebuild("users"):          "velox_new_users",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
