package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
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
	if r.ColdBuildS <= 0 || r.SchemaEditRebuildS <= 0 || r.ServiceBinaryMB <= 0 || r.LinkPeakMB <= 0 {
		t.Fatalf("a measurement is missing: %+v", r)
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
	r := report{N: 20, ColdBuildS: 10, SchemaEditRebuildS: 5, ServiceBinaryMB: 5, LinkPeakMB: 100}
	generous := map[string]map[string]any{"20": {
		"_comment": "ignored", "cold_build_s": 20.0, "schema_edit_rebuild_s": 10.0,
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

// TestCheckRefusesAMissingOrUnknownKey: the file and the code agree on the keys.
func TestCheckRefusesAMissingOrUnknownKey(t *testing.T) {
	r := report{N: 20}
	write := func(th map[string]any) string {
		p := filepath.Join(t.TempDir(), "th.json")
		b, _ := json.Marshal(map[string]map[string]any{"20": th})
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	all := map[string]any{"cold_build_s": 1.0, "schema_edit_rebuild_s": 1.0, "service_binary_mb": 1.0, "link_peak_mb": 1.0}
	if problems := check(r, 20, write(all)); len(problems) != 0 {
		t.Errorf("a complete file: %v", problems)
	}
	missing := maps.Clone(all)
	delete(missing, "link_peak_mb")
	if problems := check(r, 20, write(missing)); len(problems) != 1 || !strings.Contains(problems[0], "missing threshold link_peak_mb") {
		t.Errorf("a missing key: %v", problems)
	}
	unknown := maps.Clone(all)
	unknown["leaf_rebuild_s"] = 1.0
	if problems := check(r, 20, write(unknown)); len(problems) != 1 || !strings.Contains(problems[0], "unknown measurement leaf_rebuild_s") {
		t.Errorf("an unknown key: %v", problems)
	}
}

// TestGenerateShape: n schemas and s services, and every inverse edge has the
// To edge it names on its target, which Build requires.
func TestGenerateShape(t *testing.T) {
	const n, s = 60, 7
	schemas, services := generate(n, s)
	if len(schemas) != n || len(services) != s {
		t.Fatalf("got %d schemas and %d services, want %d and %d", len(schemas), len(services), n, s)
	}
	from := regexp.MustCompile(`edge\.From\("\w+", E(\d+)\.Type\)\.Ref\("(\w+)"\)`)
	inverses := 0
	for i := range n {
		src := schemas[filepath.Join("schema", fmt.Sprintf("e%d.go", i))]
		for _, m := range from.FindAllStringSubmatch(src, -1) {
			inverses++
			target := schemas[filepath.Join("schema", fmt.Sprintf("e%s.go", m[1]))]
			if want := fmt.Sprintf(`edge.To(%q, E%d.Type)`, m[2], i); !strings.Contains(target, want) {
				t.Errorf("e%d: no %s on e%s", i, want, m[1])
			}
		}
	}
	// Every schema but the first of its group belongs to the previous one, and
	// every group head but the first belongs to another head.
	if want := (n - n/10) + (n/10 - 1); inverses != want {
		t.Errorf("%d inverse edges, want %d", inverses, want)
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
	known := []string{"cold_build_s", "schema_edit_rebuild_s", "service_binary_mb", "link_peak_mb"}
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
		case "schema_edit_rebuild_s":
			r.SchemaEditRebuildS = f
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
