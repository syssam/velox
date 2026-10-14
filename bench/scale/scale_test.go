package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// TestCheckPassesAGenerousThreshold: check reports nothing when every
// measurement is under its threshold, and ignores comment keys.
func TestCheckPassesAGenerousThreshold(t *testing.T) {
	r := report{N: 20, ColdBuildS: 10, LeafRebuildS: 1, RootRebuildS: 5, ServiceBinaryMB: 5, LinkPeakMB: 100}
	generous := map[string]map[string]any{"20": {
		"_comment": "ignored", "cold_build_s": 20.0, "leaf_rebuild_s": 2.0, "root_rebuild_s": 10.0,
		"service_binary_mb": 10.0, "link_peak_mb": 200.0,
	}}
	th := filepath.Join(t.TempDir(), "th.json")
	b, _ := json.Marshal(generous)
	if err := os.WriteFile(th, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if problems := check(r, 20, th); len(problems) != 0 {
		t.Errorf("a generous threshold produced problems: %v", problems)
	}
	if problems := check(r, 30, th); len(problems) != 1 || !strings.Contains(problems[0], "no thresholds for n=30") {
		t.Errorf("an n with no thresholds: %v", problems)
	}
}

// TestCommittedThresholds reads the real thresholds.json (no build, so it runs
// under -short too): it must parse, and every key that is not a comment must
// name a measurement that check knows.
func TestCommittedThresholds(t *testing.T) {
	b, err := os.ReadFile("thresholds.json")
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]map[string]any
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatalf("thresholds.json does not parse: %v", err)
	}
	if _, ok := all["500"]; !ok {
		t.Error("no thresholds for n=500, the size CI checks")
	}
	known := []string{"cold_build_s", "leaf_rebuild_s", "root_rebuild_s", "service_binary_mb", "link_peak_mb"}
	for n, th := range all {
		for k, v := range th {
			if strings.HasPrefix(k, "_") {
				continue
			}
			if !slices.Contains(known, k) {
				t.Errorf("n=%s: %q is not a measurement", n, k)
			}
			if _, ok := v.(float64); !ok {
				t.Errorf("n=%s: %s is not a number: %v", n, k, v)
			}
		}
	}
	// A run that meets every recorded threshold exactly passes check.
	r := report{N: 500}
	for k, v := range all["500"] {
		f, _ := v.(float64)
		switch k {
		case "cold_build_s":
			r.ColdBuildS = f
		case "leaf_rebuild_s":
			r.LeafRebuildS = f
		case "root_rebuild_s":
			r.RootRebuildS = f
		case "service_binary_mb":
			r.ServiceBinaryMB = f
		case "link_peak_mb":
			r.LinkPeakMB = f
		}
	}
	if problems := check(r, 500, "thresholds.json"); len(problems) != 0 {
		t.Errorf("a run exactly at the thresholds: %v", problems)
	}
}

// TestRunRefusesAnUsedDirectory: a reused directory holds a warm build cache,
// which would make the cold build not cold.
func TestRunRefusesAnUsedDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "leftover"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(config{n: 20, services: 4, dir: dir}); err == nil || !strings.Contains(err.Error(), "is not empty") {
		t.Fatalf("got %v, want a refusal", err)
	}
}
