package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestScaleHarnessSmall is Review Focus 5: the harness generates, builds and
// measures a small application, and -check fails when a threshold is exceeded.
func TestScaleHarnessSmall(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an application; run without -short")
	}
	r, err := run(config{n: 20, services: 4, dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if r.ColdBuildS <= 0 || r.LeafRebuildS <= 0 || r.RootRebuildS <= 0 || r.ServiceBinaryMB <= 0 || r.LinkPeakMB <= 0 {
		t.Fatalf("a measurement is missing: %+v", r)
	}
	if r.RootRebuildS < r.LeafRebuildS/4 {
		t.Logf("note: root rebuild %.2fs vs leaf %.2fs", r.RootRebuildS, r.LeafRebuildS)
	}
	th := filepath.Join(t.TempDir(), "th.json")
	tight := map[string]map[string]any{"20": {"cold_build_s": r.ColdBuildS / 1000}}
	b, _ := json.Marshal(tight)
	if err := os.WriteFile(th, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if problems := check(r, 20, th); len(problems) == 0 {
		t.Error("check passed a measurement 1000 times over its threshold")
	}
}
