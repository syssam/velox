package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two packages: a has 10 of 20 statements covered (50%), b has 8 of 8 (100%).
// The first profile covers one of a's blocks, the second the other one and a
// block of a that the first also counted: merged by position, not summed.
const profileA = `mode: atomic
example.com/m/a/a.go:1.1,2.2 10 1
example.com/m/a/a.go:3.1,4.2 10 0
example.com/m/b/b.go:1.1,2.2 8 3
`

const profileA2 = `mode: set
example.com/m/a/a.go:1.1,2.2 10 1
example.com/m/a/a.go:3.1,4.2 10 0
`

func TestMergeProfiles(t *testing.T) {
	cov, err := mergeProfiles([]io.Reader{strings.NewReader(profileA), strings.NewReader(profileA2)})
	if err != nil {
		t.Fatal(err)
	}
	if got := cov["example.com/m/a"]; got != (pkgCov{covered: 10, total: 20}) {
		t.Errorf("a = %+v, want 10 of 20 (the same block in two profiles counts once)", got)
	}
	if got := cov["example.com/m/b"]; got != (pkgCov{covered: 8, total: 8}) {
		t.Errorf("b = %+v, want 8 of 8", got)
	}
	// A block covered in either profile is covered.
	other := "mode: set\nexample.com/m/a/a.go:3.1,4.2 10 1\n"
	cov, err = mergeProfiles([]io.Reader{strings.NewReader(profileA), strings.NewReader(other)})
	if err != nil {
		t.Fatal(err)
	}
	if got := cov["example.com/m/a"]; got != (pkgCov{covered: 20, total: 20}) {
		t.Errorf("a = %+v, want 20 of 20 (covered in one profile)", got)
	}
}

func TestMergeProfilesMalformed(t *testing.T) {
	for _, in := range []string{"mode: set\nnot a block\n", "mode: set\nf.go:1.1,2.2 x 1\n"} {
		if _, err := mergeProfiles([]io.Reader{strings.NewReader(in)}); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

func runWith(t *testing.T, floors string, profiles ...string) []string {
	t.Helper()
	dir := t.TempDir()
	ff := filepath.Join(dir, "floors.txt")
	if err := os.WriteFile(ff, []byte(floors), 0o600); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i, p := range profiles {
		pp := filepath.Join(dir, "p"+string(rune('a'+i))+".out")
		if err := os.WriteFile(pp, []byte(p), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, pp)
	}
	problems, err := run(ff, paths, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	return problems
}

func TestRun(t *testing.T) {
	t.Run("passes at and above the floors", func(t *testing.T) {
		got := runWith(t, "# comment\nexample.com/m/a 50.0 # at the floor\nexample.com/m/b 99\n", profileA)
		if len(got) != 0 {
			t.Errorf("problems = %q, want none", got)
		}
	})
	t.Run("under a floor", func(t *testing.T) {
		got := runWith(t, "example.com/m/a 60.0\nexample.com/m/b 100\n", profileA)
		if len(got) != 1 {
			t.Fatalf("problems = %q, want one", got)
		}
		for _, want := range []string{"example.com/m/a", "50.0% < 60.0%", "short by 10.0", "floors.txt", "49.0"} {
			if !strings.Contains(got[0], want) {
				t.Errorf("%q does not contain %q", got[0], want)
			}
		}
	})
	t.Run("a package without a line", func(t *testing.T) {
		got := runWith(t, "example.com/m/a 50\n", profileA)
		if len(got) != 1 || !strings.Contains(got[0], "add a floor for example.com/m/b: measured 100.0%") {
			t.Errorf("problems = %q", got)
		}
	})
	t.Run("a line without a package", func(t *testing.T) {
		got := runWith(t, "example.com/m/a 50\nexample.com/m/b 100\nexample.com/m/gone 10\n", profileA)
		if len(got) != 1 || !strings.Contains(got[0], "example.com/m/gone has a floor but no coverage: remove it") {
			t.Errorf("problems = %q", got)
		}
	})
	t.Run("every problem is listed", func(t *testing.T) {
		got := runWith(t, "example.com/m/a 90\nexample.com/m/gone 10\n", profileA)
		if len(got) != 3 {
			t.Errorf("problems = %q, want three (under, missing, stale)", got)
		}
	})
}

func TestParseFloors(t *testing.T) {
	for _, bad := range []string{"a\n", "a 101\n", "a -1\n", "a x\n", "a 1\na 2\n", "a 1 2\n"} {
		if _, err := parseFloors(strings.NewReader(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	got, err := parseFloors(strings.NewReader("\n# c\n  a  12.5  # x\n"))
	if err != nil || got["a"] != 12.5 || len(got) != 1 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestZeroStatementsPercent(t *testing.T) {
	if got := (pkgCov{}).percent(); got != 0 {
		t.Errorf("percent = %v, want 0", got)
	}
}
