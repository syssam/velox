package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNewStat(t *testing.T) {
	s := newStat([]float64{3, 1, 2}, true)
	if s.Median != 2 || s.Worst != 3 || !slices.Equal(s.Runs, []float64{3, 1, 2}) {
		t.Errorf("lower is better: got %+v, want median 2, worst 3, runs in order", s)
	}
	if s := newStat([]float64{3, 1, 2}, false); s.Median != 2 || s.Worst != 1 {
		t.Errorf("higher is better: got %+v, want median 2, worst 1", s)
	}
	if s := newStat([]float64{1, 2, 3, 4}, true); s.Median != 2.5 {
		t.Errorf("even count: median %v, want 2.5", s.Median)
	}
}

func TestParseBench(t *testing.T) {
	in := "goos: darwin\ngoarch: arm64\npkg: example.com/tmp\n" +
		"BenchmarkCount-14   \t    1000\t      1234 ns/op\t      56 B/op\t       3 allocs/op\n" +
		"BenchmarkCount-14   \t    1000\t      2000 ns/op\t      60 B/op\t       4 allocs/op\n" +
		"PASS\nok  \texample.com/tmp\t1.0s\n"
	got, err := parseBench(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got benchmarks %v, want only Count", got)
	}
	c := got["Count"]
	if want := []float64{1.234e-06, 2e-06}; !slices.Equal(c["sec/op"], want) {
		t.Errorf("sec/op %v, want %v", c["sec/op"], want)
	}
	if !slices.Equal(c["B/op"], []float64{56, 60}) || !slices.Equal(c["allocs/op"], []float64{3, 4}) {
		t.Errorf("B/op %v, allocs/op %v", c["B/op"], c["allocs/op"])
	}
	if _, err := parseBench(strings.NewReader("BenchmarkX-4 10 abc ns/op\n")); err == nil {
		t.Error("a sample that is not a number must be an error")
	}
}

// fixtureSummary summarizes testdata/summary with the machine and the commit fixed.
func fixtureSummary(t *testing.T) summary {
	t.Helper()
	oldM, oldC := probeMachine, probeCommit
	t.Cleanup(func() { probeMachine, probeCommit = oldM, oldC })
	probeMachine = func() machine {
		return machine{CPU: "Fixture CPU", CPUs: 8, MemoryGB: 16, OS: "Fixture 1.0", Go: "go1.27.0"}
	}
	probeCommit = func(string) string { return "abc1234" }
	s, err := summarize(filepath.Join("testdata", "summary"), map[string]any{
		"runs": 2, "services": 2, "bench_count": 2, "benchtime": "10x", "sizes": "100",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSummarizeFixture(t *testing.T) {
	s := fixtureSummary(t)
	if got := s.Build["ent"]["100"]["cold_cpu_s"]; got.Median != 10 || got.Worst != 11.25 || len(got.Runs) != 2 {
		t.Errorf("ent cold_cpu_s %+v, want median 10, worst 11.25, 2 runs", got)
	}
	// Higher is better: the worst of rebuild_cached is the minimum.
	if got := s.Build["oldvelox"]["100"]["rebuild_cached"]; got.Median != 90 || got.Worst != 90 {
		t.Errorf("oldvelox rebuild_cached %+v", got)
	}
	for _, k := range []string{"ref_cpu_s", "orm", "n", "services", "run", "ref_cpu_before_s"} {
		if _, ok := s.Build["ent"]["100"][k]; ok {
			t.Errorf("build metrics hold %q, which is not a metric", k)
		}
	}
	if len(s.Build["ent"]["100"]) != len(buildMetrics) {
		t.Errorf("%d metrics, want %d", len(s.Build["ent"]["100"]), len(buildMetrics))
	}
	if got := s.Reference["ent"]["100"]; math.Abs(got.Median-0.8) > 1e-9 {
		t.Errorf("ent reference %+v, want median 0.8", got)
	}
	if s.Versions["ent"] != "v0.14.6" {
		t.Errorf("ent version %q, want v0.14.6", s.Versions["ent"])
	}
	for _, k := range []string{"velox2", "oldvelox", "sqlite", "pgx", "x/tools"} {
		if s.Versions[k] == "" {
			t.Errorf("no version for %s: %v", k, s.Versions)
		}
	}
	if s.Versions["velox2"] != "abc1234" {
		t.Errorf("velox2 version %q", s.Versions["velox2"])
	}
	if len(s.Steps) != 3 {
		t.Fatalf("%d steps, want 3", len(s.Steps))
	}
	timedOut := 0
	for _, r := range s.Steps {
		if r.Gate != nil && r.Gate.TimedOut {
			timedOut++
		}
	}
	if timedOut != 1 {
		t.Errorf("%d steps timed out, want 1", timedOut)
	}
	got := s.Runtime["postgres"]["oldvelox"]["Count"]["sec/op"]
	if len(got.Runs) != 2 || got.Median <= 0 {
		t.Errorf("postgres oldvelox Count sec/op %+v", got)
	}
	if len(s.Runtime) != 2 || len(s.Runtime["sqlite"]) != 3 || len(s.Runtime["sqlite"]["ent"]) != 7 || len(s.Runtime["sqlite"]["ent"]["Count"]) != 3 {
		t.Errorf("runtime has the wrong shape: %d dialects", len(s.Runtime))
	}
	if !slices.Equal(s.Config.Sizes, []int{100}) || s.Config.Runs != 2 || s.Config.Benchtime != "10x" || s.Config.BenchCount != 2 || s.Config.Services != 2 {
		t.Errorf("config %+v", s.Config)
	}
	if s.Machine.CPU != "Fixture CPU" {
		t.Errorf("machine %+v", s.Machine)
	}
}

func TestSummarizeErrors(t *testing.T) {
	fixtureSummary(t) // replaces the probes
	copyFixture := func(t *testing.T) string {
		dir := t.TempDir()
		if err := os.CopyFS(dir, os.DirFS(filepath.Join("testdata", "summary"))); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	t.Run("size not in config", func(t *testing.T) {
		_, err := summarize(copyFixture(t), map[string]any{"sizes": "300"})
		if err == nil || !strings.Contains(err.Error(), "100") {
			t.Errorf("got %v, want an error naming n=100", err)
		}
	})
	t.Run("size without results", func(t *testing.T) {
		_, err := summarize(copyFixture(t), map[string]any{"sizes": "100 300"})
		if err == nil || !strings.Contains(err.Error(), "300") {
			t.Errorf("got %v, want an error naming n=300", err)
		}
	})
	t.Run("a metric missing", func(t *testing.T) {
		dir := copyFixture(t)
		p := filepath.Join(dir, "raw", "build-ent-n100-r1.json")
		b, _ := os.ReadFile(p)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		delete(m, "cold_cpu_s")
		b, _ = json.Marshal(m)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := summarize(dir, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "cold_cpu_s") {
			t.Errorf("got %v, want an error naming cold_cpu_s", err)
		}
	})
	t.Run("no results", func(t *testing.T) {
		if _, err := summarize(t.TempDir(), nil); err == nil {
			t.Error("an empty directory must be an error")
		}
	})
	t.Run("sizes from the results", func(t *testing.T) {
		s, err := summarize(copyFixture(t), nil)
		if err != nil || !slices.Equal(s.Config.Sizes, []int{100}) {
			t.Errorf("got %+v, %v", s.Config, err)
		}
	})
}

func TestParseConfig(t *testing.T) {
	got, err := parseConfig([]string{"runs=3", "sizes=100,300", "benchtime=1s"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := newConfig(got)
	if err != nil || c.Runs != 3 || c.Benchtime != "1s" || !slices.Equal(c.Sizes, []int{100, 300}) {
		t.Errorf("got %+v, %v", c, err)
	}
	c, err = newConfig(map[string]any{"sizes": "300 100", "bench_count": 6.0})
	if err != nil || !slices.Equal(c.Sizes, []int{100, 300}) || c.BenchCount != 6 {
		t.Errorf("got %+v, %v", c, err)
	}
	for _, bad := range []map[string]any{{"runs": "x"}, {"colour": "red"}, {"sizes": "10 x"}} {
		if _, err := newConfig(bad); err == nil {
			t.Errorf("newConfig(%v) must fail", bad)
		}
	}
	if _, err := parseConfig([]string{"runs"}); err == nil {
		t.Error("a config without = must fail")
	}
}

func TestSummaryHasNoSecret(t *testing.T) {
	t.Setenv("VELOX_COMPARE_POSTGRES", "postgres://u:hunter2@h/db")
	s := fixtureSummary(t)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var rep strings.Builder
	if err := writeReport(&rep, s); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hunter2") || strings.Contains(rep.String(), "hunter2") {
		t.Error("the DSN's password is in the summary or the report")
	}
}

func TestLosses(t *testing.T) {
	s := fixtureSummary(t)
	// The fixture: ent's cold_cpu_s median is 10 and velox2's 12; ent's
	// ref_cpu_s is lower than velox2's, and that is no loss.
	got := losses(s)
	if len(got) != 1 {
		t.Fatalf("got %d losses %+v, want 1", len(got), got)
	}
	l := got[0]
	if l.Section != "build" || l.Key != "100" || l.Metric != "cold_cpu_s" || l.Winner != "ent" ||
		l.Velox2 != 12 || l.Best != 10 || math.Abs(l.Ratio-10.0/12) > 1e-9 {
		t.Errorf("got %+v", l)
	}

	// Higher is better: ent caching more of the rebuild is a loss.
	e := s.Build["ent"]["100"]
	e["rebuild_cached"] = newStat([]float64{120}, false)
	got = losses(s)
	if len(got) != 2 || got[1].Metric != "rebuild_cached" || got[1].Winner != "ent" || got[1].Ratio != 1.2 {
		t.Errorf("rebuild_cached case: got %+v", got)
	}

	// A runtime loss: sec/op, B/op and allocs/op are lower-is-better.
	s.Runtime["postgres"]["oldvelox"]["GetByID"]["B/op"] = newStat([]float64{1}, true)
	got = losses(s)
	var rt []loss
	for _, l := range got {
		if l.Section == "runtime" {
			rt = append(rt, l)
		}
	}
	if len(rt) != 1 || rt[0].Key != "postgres" || rt[0].Metric != "GetByID" || rt[0].Unit != "B/op" || rt[0].Winner != "oldvelox" {
		t.Errorf("runtime losses %+v", rt)
	}
	if !slices.IsSortedFunc(got, func(a, b loss) int {
		return strings.Compare(a.Section+"\x00"+a.Key+"\x00"+a.Metric+"\x00"+a.Unit, b.Section+"\x00"+b.Key+"\x00"+b.Metric+"\x00"+b.Unit)
	}) {
		t.Errorf("losses are not sorted: %+v", got)
	}

	// A tie is no loss.
	s.Build["ent"]["100"]["cold_cpu_s"] = s.Build["velox2"]["100"]["cold_cpu_s"]
	for _, l := range losses(s) {
		if l.Metric == "cold_cpu_s" {
			t.Errorf("a tie is a loss: %+v", l)
		}
	}
}

func TestProbeParsers(t *testing.T) {
	cpu := "processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Xeon(R) CPU @ 2.20GHz\nmodel name\t: other\n"
	if got := parseCPUInfo(cpu); got != "Intel(R) Xeon(R) CPU @ 2.20GHz" {
		t.Errorf("cpu %q", got)
	}
	if got := parseCPUInfo("nothing\n"); got != "" {
		t.Errorf("cpu %q", got)
	}
	if got := parseMemInfo("MemFree: 1 kB\nMemTotal:       16777216 kB\n"); got != 16 {
		t.Errorf("mem %v", got)
	}
	if got := parseMemInfo("x\n"); got != 0 {
		t.Errorf("mem %v", got)
	}
}
