package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
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
	// rebuild_cached counts packages, not a faster rebuild: it is not a metric,
	// and the share that reran, rerun ÷ (rerun + cached), replaces it.
	if got := s.Build["oldvelox"]["100"]["rebuild_rerun_share"]; math.Abs(got.Median-40.0/130) > 1e-9 || math.Abs(got.Worst-40.0/130) > 1e-9 {
		t.Errorf("oldvelox rebuild_rerun_share %+v, want 40/130", got)
	}
	if got := s.Build["oldvelox"]["100"]["rebuild_rerun"]; got.Median != 40 {
		t.Errorf("oldvelox rebuild_rerun %+v, want 40", got)
	}
	if _, ok := s.Build["oldvelox"]["100"]["rebuild_cached"]; ok {
		t.Error("rebuild_cached is a metric")
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
	t.Run("an input of a derived metric missing", func(t *testing.T) {
		dir := copyFixture(t)
		p := filepath.Join(dir, "raw", "build-ent-n100-r1.json")
		b, _ := os.ReadFile(p)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		delete(m, "rebuild_cached")
		b, _ = json.Marshal(m)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := summarize(dir, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "rebuild_cached") {
			t.Errorf("got %v, want an error naming rebuild_cached", err)
		}
	})
	t.Run("services differ from the config", func(t *testing.T) {
		// The fixture was made with 2 services.
		_, err := summarize(copyFixture(t), map[string]any{"services": 20})
		if err == nil || !strings.Contains(err.Error(), "services") || !strings.Contains(err.Error(), "build-") {
			t.Errorf("got %v, want an error about services that names a build file", err)
		}
		if _, err := summarize(copyFixture(t), map[string]any{"services": 2}); err != nil {
			t.Errorf("matching services: %v", err)
		}
		if _, err := summarize(copyFixture(t), map[string]any{}); err != nil {
			t.Errorf("services left out of the config: %v", err)
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

// copyFixture copies testdata/summary into a temporary directory.
func copyFixture(t *testing.T) string {
	t.Helper()
	return copyFixtureOf(t, "summary")
}

// copyFixtureOf copies testdata/<name> into a temporary directory.
func copyFixtureOf(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("testdata", name))); err != nil {
		t.Fatal(err)
	}
	return dir
}

// variantConfig is the fixture's config with velox2pgx as a variant.
func variantConfig() map[string]any {
	return map[string]any{
		"runs": 2, "services": 2, "bench_count": 2, "benchtime": "10x", "sizes": "100", "variants": "velox2pgx",
	}
}

// TestSummarizeVariant checks that a configured variant's postgres rounds are
// summarized and required, and that a variant's rounds without the config are
// shown but not required.
func TestSummarizeVariant(t *testing.T) {
	fixtureSummary(t) // replaces the probes
	dir := copyFixtureOf(t, "summary-variant")
	s, err := summarize(dir, variantConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Runtime["postgres"]["velox2pgx"]["Query100"]["sec/op"]; len(got.Runs) != 2 || got.Median <= 0 {
		t.Errorf("postgres velox2pgx Query100 sec/op %+v, want 2 runs", got)
	}
	if _, ok := s.Runtime["sqlite"]["velox2pgx"]; ok {
		t.Error("velox2pgx has sqlite results")
	}
	if !slices.Equal(s.Config.Variants, []string{"velox2pgx"}) {
		t.Errorf("config variants %v", s.Config.Variants)
	}

	cfg := variantConfig()
	delete(cfg, "variants")
	s, err = summarize(dir, cfg)
	if err != nil {
		t.Fatalf("an unconfigured variant's rounds: %v", err)
	}
	if got := s.Runtime["postgres"]["velox2pgx"]["Query100"]["sec/op"]; len(got.Runs) != 2 {
		t.Errorf("an unconfigured variant is not shown: %+v", got)
	}

	if err := os.Remove(filepath.Join(dir, "raw", "bench-postgres-velox2pgx-2.txt")); err != nil {
		t.Fatal(err)
	}
	_, err = summarize(dir, variantConfig())
	if err == nil || !strings.Contains(err.Error(), "bench postgres velox2pgx round 2") {
		t.Errorf("got %v, want an error naming bench postgres velox2pgx round 2", err)
	}
	if _, err := summarize(dir, cfg); err != nil {
		t.Errorf("an unconfigured variant is required: %v", err)
	}
	if _, err := newConfig(map[string]any{"variants": "nope"}); err == nil {
		t.Error("an unknown variant must fail")
	}
}

// TestLossesIgnoreVariant checks that a variant of velox2 that beats velox2 is
// no loss: it is velox2 too.
func TestLossesIgnoreVariant(t *testing.T) {
	fixtureSummary(t) // replaces the probes
	s, err := summarize(copyFixtureOf(t, "summary-variant"), variantConfig())
	if err != nil {
		t.Fatal(err)
	}
	for name, units := range s.Runtime["postgres"]["velox2"] {
		for u, st := range units {
			s.Runtime["postgres"]["velox2pgx"][name][u] = newStat([]float64{st.Median / 2}, true)
		}
	}
	for _, l := range losses(s) {
		if l.Winner == "velox2pgx" {
			t.Errorf("a loss to the variant: %+v", l)
		}
	}
}

// TestSummarizeRuntimeOnly checks that steps=runtime needs no build results.
func TestSummarizeRuntimeOnly(t *testing.T) {
	fixtureSummary(t) // replaces the probes
	dir := copyFixtureOf(t, "summary-runtime")
	s, err := summarize(dir, map[string]any{"bench_count": 2, "benchtime": "10x", "steps": "runtime", "variants": "velox2pgx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Build) != 0 || len(s.Missing) != 0 || s.Config.Steps != "runtime" {
		t.Errorf("build %v, missing %v, config %+v", s.Build, s.Missing, s.Config)
	}
	if _, err := summarize(dir, map[string]any{"bench_count": 2}); err == nil || !strings.Contains(err.Error(), "no build-") {
		t.Errorf("without steps: got %v, want an error naming no build-", err)
	}
	if _, err := newConfig(map[string]any{"steps": "build"}); err == nil {
		t.Error("steps=build must fail")
	}
}

func TestSummarizeCompleteness(t *testing.T) {
	fixtureSummary(t) // replaces the probes
	cfg := map[string]any{"runs": 2, "bench_count": 2, "sizes": "100"}
	dir := copyFixture(t)
	for _, f := range []string{"build-ent-n100-r2.json", "bench-sqlite-ent-2.txt", "bench-postgres-velox2-1.txt", "bench-postgres-velox2-2.txt"} {
		if err := os.Remove(filepath.Join(dir, "raw", f)); err != nil {
			t.Fatal(err)
		}
	}
	// One round file without one benchmark.
	p := filepath.Join(dir, "raw", "bench-sqlite-oldvelox-1.txt")
	b, _ := os.ReadFile(p)
	var kept []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(l, "BenchmarkCount-") {
			kept = append(kept, l)
		}
	}
	if err := os.WriteFile(p, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"build ent n=100 run 2",
		"bench sqlite ent round 2",
		"bench postgres velox2 round 1",
		"bench postgres velox2 round 2",
		"bench sqlite oldvelox round 1: Count sec/op",
	}
	_, err := summarize(dir, cfg)
	if err == nil {
		t.Fatal("incomplete results must be an error")
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w+"\n") && !strings.HasSuffix(err.Error(), w) {
			t.Errorf("the error does not name %q:\n%v", w, err)
		}
	}

	s, err := summarizePartial(dir, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range want {
		if !slices.Contains(s.Missing, w) {
			t.Errorf("Missing lacks %q: %v", w, s.Missing)
		}
	}
	if len(s.Build["ent"]["100"]["cold_cpu_s"].Runs) != 1 {
		t.Error("the partial summary should hold the one run that exists")
	}
	if full := fixtureSummary(t); len(full.Missing) != 0 {
		t.Errorf("the complete fixture has missing items: %v", full.Missing)
	}
	// The settings that config leaves out are the highest found.
	if _, err := summarize(dir, nil); err == nil {
		t.Error("derived runs and rounds must still find the missing items")
	}
}

func TestSummarizeCommitFile(t *testing.T) {
	fixtureSummary(t)
	dir := copyFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "commit.txt"), []byte("deadbee\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := summarize(dir, map[string]any{"sizes": "100"})
	if err != nil || s.Versions["velox2"] != "deadbee" {
		t.Errorf("got %q, %v; want deadbee from commit.txt", s.Versions["velox2"], err)
	}
	if err := os.WriteFile(filepath.Join(dir, "commit.txt"), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err = summarize(dir, nil); err != nil || s.Versions["velox2"] != "abc1234" {
		t.Errorf("an empty commit.txt: got %q, %v; want the probe's abc1234", s.Versions["velox2"], err)
	}
}

func TestSummarizeEnv(t *testing.T) {
	fixtureSummary(t)
	cfg := map[string]any{"sizes": "100"}
	dir := copyFixture(t)
	s, err := summarize(dir, cfg)
	if err != nil || s.Env != nil {
		t.Fatalf("no env.txt: got env %+v, %v; want nothing recorded", s.Env, err)
	}
	if b, _ := json.Marshal(s); strings.Contains(string(b), `"env"`) {
		t.Errorf("no env.txt, but the summary has an env: %s", b)
	}

	if err := os.WriteFile(filepath.Join(dir, "env.txt"), []byte("GOFLAGS=-p=2 -mod=mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err = summarize(dir, cfg); err != nil || s.Env == nil || s.Env.GoFlags != "-p=2 -mod=mod" || s.Env.NumCPU != runtime.NumCPU() {
		t.Errorf("env.txt: got %+v, %v; want GOFLAGS and runtime.NumCPU()", s.Env, err)
	}

	// An empty GOFLAGS was recorded: that is a fact, not an absence.
	if err := os.WriteFile(filepath.Join(dir, "env.txt"), []byte("GOFLAGS=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err = summarize(dir, cfg); err != nil || s.Env == nil || s.Env.GoFlags != "" {
		t.Errorf("empty GOFLAGS: got %+v, %v", s.Env, err)
	}

	// A file without a GOFLAGS line records nothing.
	if err := os.WriteFile(filepath.Join(dir, "env.txt"), []byte("OTHER=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err = summarize(dir, cfg); err != nil || s.Env != nil {
		t.Errorf("no GOFLAGS line: got %+v, %v", s.Env, err)
	}
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
	if c, err := newConfig(map[string]any{"sizes": "100 100,300"}); err != nil || !slices.Equal(c.Sizes, []int{100, 300}) {
		t.Errorf("duplicate sizes: got %+v, %v", c, err)
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

	// ent rerunning a smaller share of the rebuild is a loss; more packages
	// cached (rebuild_cached) is not a metric, so it is none.
	s.Build["ent"]["100"]["rebuild_rerun_share"] = newStat([]float64{0.1}, true)
	got = losses(s)
	if len(got) != 2 || got[1].Metric != "rebuild_rerun_share" || got[1].Winner != "ent" || math.Abs(got[1].Ratio-0.6) > 1e-9 {
		t.Errorf("rebuild_rerun_share case: got %+v", got)
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

// TestLossesPerCompetitor checks that every ORM that beats velox2 has its own
// loss, in the order of orms, so that a reader never learns of only the best.
func TestLossesPerCompetitor(t *testing.T) {
	s := fixtureSummary(t)
	s.Build["oldvelox"]["100"]["cold_cpu_s"] = newStat([]float64{11}, true) // velox2 12, ent 10
	var got []loss
	for _, l := range losses(s) {
		if l.Metric == "cold_cpu_s" {
			got = append(got, l)
		}
	}
	if len(got) != 2 || got[0].Winner != "oldvelox" || got[0].Best != 11 || got[1].Winner != "ent" || got[1].Best != 10 {
		t.Fatalf("got %+v, want a loss to old velox (11) and one to ent (10)", got)
	}
	if math.Abs(got[0].Ratio-11.0/12) > 1e-9 || math.Abs(got[1].Ratio-10.0/12) > 1e-9 {
		t.Errorf("ratios %v and %v", got[0].Ratio, got[1].Ratio)
	}

	// A runtime unit too, and only the ORM that beats velox2 has a loss.
	s.Runtime["sqlite"]["oldvelox"]["Count"]["sec/op"] = newStat([]float64{1e-9}, true)
	s.Runtime["sqlite"]["ent"]["Count"]["sec/op"] = newStat([]float64{1e-9}, true)
	s.Runtime["sqlite"]["velox2"]["Count"]["sec/op"] = newStat([]float64{1e-6}, true)
	var count []string
	for _, l := range losses(s) {
		if l.Section == "runtime" && l.Key == "sqlite" && l.Metric == "Count" && l.Unit == "sec/op" {
			count = append(count, l.Winner)
		}
	}
	if !slices.Equal(count, []string{"oldvelox", "ent"}) {
		t.Errorf("Count sec/op losses to %v, want old velox and ent", count)
	}
}

func TestBeaters(t *testing.T) {
	st := func(v float64) stat { return newStat([]float64{v}, true) }
	stats := map[string]stat{"velox2": st(5), "oldvelox": st(5), "ent": st(7)}
	if got := beaters(stats, true); len(got) != 0 {
		t.Errorf("lower is better: a tie and a worse ORM beat velox2: %v", got)
	}
	// The same numbers read the other way: ent is higher, and higher is better.
	if got := beaters(stats, false); !slices.Equal(got, []string{"ent"}) {
		t.Errorf("higher is better: got %v, want ent", got)
	}
	if got := beaters(map[string]stat{"ent": st(1)}, true); got != nil {
		t.Errorf("without velox2: got %v", got)
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
