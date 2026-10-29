package buildcost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPeakMB: the largest number of the file, in MB; a line that is not a number
// is skipped; a missing file is an error that says there were no measurements.
func TestPeakMB(t *testing.T) {
	f := filepath.Join(t.TempDir(), "linkmem.txt")
	if err := os.WriteFile(f, []byte("1048576\n3145728\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := PeakMB(f)
	if err != nil || got != 3 {
		t.Fatalf("PeakMB = %v, %v; want 3", got, err)
	}
	if _, err := PeakMB(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "no linker measurements") {
		t.Fatalf("PeakMB of a missing file: %v", err)
	}
}

// TestReferenceBuild: the wrapper builds, the reference builds cold under it,
// and both its CPU time and the linker's peak memory are measured.
func TestReferenceBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds net/http cold")
	}
	tmp := t.TempDir()
	wrapper := filepath.Join(tmp, "linkmem")
	if err := BuildWrapper(wrapper); err != nil {
		t.Fatal(err)
	}
	ref := filepath.Join(tmp, "ref")
	if err := WriteReference(ref); err != nil {
		t.Fatal(err)
	}
	cpu, wall, err := BuildReference(ref, filepath.Join(tmp, "cache"), wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if cpu <= 0 || wall <= 0 {
		t.Fatalf("cpu = %v, wall = %v; want both positive", cpu, wall)
	}
	mb, err := PeakMB(filepath.Join(ref, "linkmem.txt"))
	if err != nil || mb <= 0 {
		t.Fatalf("PeakMB = %v, %v; want positive", mb, err)
	}
}
