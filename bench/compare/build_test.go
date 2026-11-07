package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox/internal/buildcost"
)

func TestCountActions(t *testing.T) {
	for _, tc := range []struct {
		file string
		want actionCounts
	}{
		// cold.json: all ran; the app's actions took 0.25 and 0.5 CPU seconds,
		// the dependency 1.
		{"cold.json", actionCounts{Build: 3, BuildRerun: 3, Link: 1, LinkRerun: 1, AppS: 0.75, DepS: 1}},
		// edit.json: only the generated package reran, 0.375 CPU seconds; the
		// cached actions add none of their wall time.
		{"edit.json", actionCounts{Build: 3, BuildRerun: 1, Link: 1, LinkRerun: 1, AppS: 0.375, DepS: 0}},
	} {
		got, err := countActions(filepath.Join("testdata", "actiongraph", tc.file))
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.file, got, tc.want)
		}
	}
	for _, tc := range []struct {
		file, prefix, exclude string
		want                  int
	}{
		{"edit.json", "example.com/tmp/gen", "", 1},
		{"cold.json", "example.com/tmp", "", 2},
		// The excluded package, as ent's schema under its generated directory.
		{"cold.json", "example.com/tmp", "example.com/tmp/gen", 1},
		// example.com/tmp/s0 is not under example.com/tmp/s.
		{"cold.json", "example.com/tmp/s", "", 0},
	} {
		reran, err := reranUnder(filepath.Join("testdata", "actiongraph", tc.file), tc.prefix, tc.exclude)
		if err != nil || reran != tc.want {
			t.Errorf("reranUnder(%s, %q, %q) = %d, %v; want %d", tc.file, tc.prefix, tc.exclude, reran, err, tc.want)
		}
	}
	if _, err := countActions(filepath.Join("testdata", "actiongraph", "missing.json")); err == nil {
		t.Error("countActions of a missing file: no error")
	}
}

// TestMain removes the generators that testTools built.
func TestMain(m *testing.M) {
	code := m.Run()
	if toolsDir != "" {
		_ = os.RemoveAll(toolsDir)
	}
	os.Exit(code)
}

var (
	toolsOnce sync.Once
	toolsDir  string
	errTools  error
)

// testTools builds the three generators once, into a directory that the
// tests of this package share and TestMain removes.
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
		for _, o := range orms {
			cmd := exec.Command("go", "build", "-o", toolsDir+string(filepath.Separator), toolPkgs[o.Name])
			cmd.Dir, cmd.Env = filepath.Join(hd, o.ModDir), goEnv()
			out, err := cmd.CombinedOutput()
			if err == nil {
				continue
			}
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
	_, err := measureBuild(buildConfig{ORM: veloxORM, N: 10, Services: 2, Run: 1, Tools: tools, Raw: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "cold build reused") {
		t.Fatalf("measureBuild with a warm cache: %v; want an error that contains %q", err, "cold build reused")
	}
}

// TestEditMustRebuild checks that a schema edit that changes no generated
// file is an error (Review Focus 2). The comment edit leaves velox-gen's output
// byte-identical, so no action under the generated package reruns. The build
// does compile the schema package (go build ./... compiles every package of
// the app), but reranUnder looks only under the generated package.
func TestEditMustRebuild(t *testing.T) {
	if testing.Short() {
		t.Skip("a code generation and builds")
	}
	tools := testTools(t)
	old := editLine
	editLine = "// extra"
	t.Cleanup(func() { editLine = old })
	_, err := measureBuild(buildConfig{ORM: veloxORM, N: 10, Services: 2, Run: 1, Tools: tools, Raw: t.TempDir()})
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
