package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox2/internal/buildcost"
)

func TestCountActions(t *testing.T) {
	for _, tc := range []struct {
		file string
		want actionCounts
	}{
		// cold.json: app actions of 0.25s and 0.5s, a dependency of 1s, all run.
		{"cold.json", actionCounts{Build: 3, BuildRerun: 3, Link: 1, LinkRerun: 1, AppS: 0.75, DepS: 1}},
		// edit.json: only the generated package reran; the other two were cached.
		{"edit.json", actionCounts{Build: 3, BuildRerun: 1, Link: 1, LinkRerun: 1, AppS: 0.625, DepS: 0.25}},
	} {
		got, err := countActions(filepath.Join("testdata", "actiongraph", tc.file), "example.com/tmp")
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.file, got, tc.want)
		}
	}
	reran, err := reranUnder(filepath.Join("testdata", "actiongraph", "edit.json"), "example.com/tmp/gen")
	if err != nil || reran != 1 {
		t.Errorf("reranUnder(edit.json, gen) = %d, %v; want 1", reran, err)
	}
	// example.com/tmp/s0 is not under example.com/tmp/s.
	if reran, err := reranUnder(filepath.Join("testdata", "actiongraph", "cold.json"), "example.com/tmp/s"); err != nil || reran != 0 {
		t.Errorf("reranUnder(cold.json, s) = %d, %v; want 0", reran, err)
	}
	if _, err := countActions(filepath.Join("testdata", "actiongraph", "missing.json"), "example.com/tmp"); err == nil {
		t.Error("countActions of a missing file: no error")
	}
}

var (
	toolsOnce sync.Once
	toolsDir  string
	errTools  error
)

// testTools builds the three generators once, into a directory that the
// tests of this package share. It is left behind in the temporary directory.
func testTools(t *testing.T) string {
	t.Helper()
	toolsOnce.Do(func() {
		hd, err := harnessDir()
		if err != nil {
			errTools = err
			return
		}
		if toolsDir, errTools = os.MkdirTemp("", "compare-tools-"); errTools != nil {
			return
		}
		args := []string{"build", "-o", toolsDir + string(filepath.Separator)}
		for _, o := range orms {
			args = append(args, toolPkgs[o.Name])
		}
		cmd := exec.Command("go", args...)
		cmd.Dir, cmd.Env = hd, goEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			errTools = err
			t.Logf("building the generators:\n%s", out)
		}
	})
	if errTools != nil {
		t.Fatalf("building the generators: %v", errTools)
	}
	return toolsDir
}

// TestColdMustBeCold checks that a cold build served from a warm cache is an
// error (Review Focus 1).
func TestColdMustBeCold(t *testing.T) {
	if testing.Short() {
		t.Skip("a code generation and builds")
	}
	tools := testTools(t)
	dir := t.TempDir()
	wrapper, ref, cache := filepath.Join(dir, "linkmem"), filepath.Join(dir, "ref"), filepath.Join(dir, "cache")
	if err := buildcost.BuildWrapper(wrapper); err != nil {
		t.Fatal(err)
	}
	if err := buildcost.WriteReference(ref); err != nil {
		t.Fatal(err)
	}
	// One build warms the cache with the standard library.
	if _, _, err := buildcost.BuildReference(ref, cache, wrapper); err != nil {
		t.Fatal(err)
	}
	coldCache = cache
	t.Cleanup(func() { coldCache = "" })
	_, err := measureBuild(buildConfig{ORM: velox2ORM, N: 10, Services: 2, Run: 1, Tools: tools, Raw: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "cold build reused") {
		t.Fatalf("measureBuild with a warm cache: %v; want an error that contains %q", err, "cold build reused")
	}
}

// TestEditMustRebuild checks that a schema edit that changes no generated
// file is an error (Review Focus 2). velox2's schema package is not compiled
// by the build, so the comment changes nothing that is.
func TestEditMustRebuild(t *testing.T) {
	if testing.Short() {
		t.Skip("a code generation and builds")
	}
	tools := testTools(t)
	old := editLine
	editLine = "// extra"
	t.Cleanup(func() { editLine = old })
	_, err := measureBuild(buildConfig{ORM: velox2ORM, N: 10, Services: 2, Run: 1, Tools: tools, Raw: t.TempDir()})
	const want = "no action of the generated package reran"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("measureBuild with a comment edit: %v; want an error that contains %q", err, want)
	}
}

func TestMeasureBuildSmall(t *testing.T) {
	if testing.Short() {
		t.Skip("three code generations and builds")
	}
	tools := testTools(t)
	for _, o := range orms {
		t.Run(o.Name, func(t *testing.T) {
			raw := t.TempDir()
			r, err := measureBuild(buildConfig{ORM: o, N: 10, Services: 2, Run: 1, Tools: tools, Raw: raw})
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(b, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != 27 {
				t.Errorf("the report has %d keys, want 27: %s", len(fields), b)
			}
			for k, v := range fields {
				switch v := v.(type) {
				case string:
					if v == "" {
						t.Errorf("%s is empty", k)
					}
				case float64:
					if v <= 0 && k != "rebuild_cached" {
						t.Errorf("%s = %v, want > 0", k, v)
					}
				default:
					t.Errorf("%s is a %T", k, v)
				}
			}
			if r.RebuildRelinks != 2 {
				t.Errorf("rebuild_relinks = %d, want 2", r.RebuildRelinks)
			}
			if r.ColdX != r.ColdCPUS/r.RefCPUS {
				t.Errorf("cold_x = %v, want %v / %v", r.ColdX, r.ColdCPUS, r.RefCPUS)
			}
			for _, name := range []string{"cold", "edit"} {
				if _, err := os.Stat(filepath.Join(raw, "ag-"+o.Name+"-n10-r1-"+name+".json")); err != nil {
					t.Error(err)
				}
			}
			t.Logf("%s", b)
		})
	}
}
