package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// stat is the runs of one measurement, their median and their worst run.
type stat struct {
	Median float64   `json:"median"`
	Worst  float64   `json:"worst"`
	Runs   []float64 `json:"runs"`
}

// newStat summarizes runs, which it keeps in the order given. The median of an
// even count is the mean of the middle two. Worst is the largest run when
// lower is better, and the smallest otherwise.
func newStat(runs []float64, lowerIsBetter bool) stat {
	s := stat{Runs: slices.Clone(runs)}
	if len(runs) == 0 {
		return s
	}
	sorted := slices.Sorted(slices.Values(runs))
	n := len(sorted)
	if n%2 == 1 {
		s.Median = sorted[n/2]
	} else {
		s.Median = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	if lowerIsBetter {
		s.Worst = sorted[n-1]
	} else {
		s.Worst = sorted[0]
	}
	return s
}

// metric is one build metric: its key and its direction. A metric with a
// Derive is computed from the build JSON keys in From; any other is a key of
// the build JSON. Doc, if set, is the one line the report prints under the
// metric's table.
type metric struct {
	Key           string
	LowerIsBetter bool
	From          []string
	Derive        func(raw map[string]float64) float64
	Doc           string
}

// buildMetrics are the build metrics, in table order. orm, n, services, run
// and ref_* are not here: they identify a run or measure the machine's load.
//
// The build JSON also holds rebuild_cached, the count of actions the rebuild
// served from the cache. It is not a metric: it counts every package the edit
// did not touch, the standard library included, so an ORM that generates more
// packages "caches more" without a faster rebuild. rebuild_rerun_share, the
// fraction that reran, replaces it; rebuild_cpu_s and edit_x are the cost.
var buildMetrics = []metric{
	{Key: "codegen_x", LowerIsBetter: true}, {Key: "cold_x", LowerIsBetter: true}, {Key: "edit_x", LowerIsBetter: true},
	{Key: "codegen_cpu_s", LowerIsBetter: true}, {Key: "codegen_wall_s", LowerIsBetter: true},
	{Key: "cold_cpu_s", LowerIsBetter: true}, {Key: "cold_wall_s", LowerIsBetter: true},
	{Key: "cold_app_action_s", LowerIsBetter: true}, {Key: "cold_dep_action_s", LowerIsBetter: true},
	{Key: "regen_cpu_s", LowerIsBetter: true}, {Key: "regen_wall_s", LowerIsBetter: true},
	{Key: "rebuild_cpu_s", LowerIsBetter: true}, {Key: "rebuild_wall_s", LowerIsBetter: true},
	{Key: "rebuild_rerun", LowerIsBetter: true},
	{
		Key: "rebuild_rerun_share", LowerIsBetter: true,
		From:   []string{"rebuild_rerun", "rebuild_cached"},
		Derive: rerunShare,
		Doc:    "rebuild_rerun ÷ (rebuild_rerun + rebuild_cached): the fraction of the build's actions that the edit reran. The count of cached actions is not a metric, because it grows with the number of packages, not with a faster rebuild.",
	},
	{Key: "rebuild_relinks", LowerIsBetter: true},
	{Key: "gen_lines", LowerIsBetter: true}, {Key: "gen_bytes", LowerIsBetter: true},
	{Key: "service_binary_mb", LowerIsBetter: true}, {Key: "link_peak_mb", LowerIsBetter: true},
}

// rerunShare is rerun ÷ (rerun + cached), or 0 when there were no actions.
func rerunShare(raw map[string]float64) float64 {
	rerun, cached := raw["rebuild_rerun"], raw["rebuild_cached"]
	if rerun+cached == 0 {
		return 0
	}
	return rerun / (rerun + cached)
}

// rawKeys are the keys of a build JSON that summarize reads: the reference
// build's, and those of every metric.
func rawKeys() []string {
	keys := []string{refMetric}
	for _, m := range buildMetrics {
		if m.Derive != nil {
			keys = append(keys, m.From...)
		} else {
			keys = append(keys, m.Key)
		}
	}
	return slices.Compact(slices.Sorted(slices.Values(keys)))
}

// refMetric is the key of the reference build's CPU seconds in the build JSON.
const refMetric = "ref_cpu_s"

// runtimeUnits are the units of a benchmark line that the summary keeps. All
// are lower-is-better.
var runtimeUnits = []string{"sec/op", "B/op", "allocs/op"}

// benchNames are the benchmarks, in the order the tables list them.
var benchNames = []string{"InsertOne", "InsertBulk100", "GetByID", "Query100", "EagerO2M100", "UpdateByID", "Count"}

var benchSuffixRE = regexp.MustCompile(`-\d+$`)

// parseBench reads `go test -bench -benchmem` output. It maps each
// benchmark's name, without the Benchmark prefix and the -N suffix, to the
// units sec/op (from ns/op), B/op and allocs/op, and each unit to its
// samples, one per line. Other lines and other units are skipped.
func parseBench(r io.Reader) (map[string]map[string][]float64, error) {
	out := map[string]map[string][]float64{}
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || !strings.HasPrefix(f[0], "Benchmark") {
			continue
		}
		if _, err := strconv.Atoi(f[1]); err != nil {
			continue // not a result line
		}
		name := benchSuffixRE.ReplaceAllString(strings.TrimPrefix(f[0], "Benchmark"), "")
		for i := 2; i+1 < len(f); i += 2 {
			unit := f[i+1]
			switch unit {
			case "ns/op", "B/op", "allocs/op":
			default:
				continue
			}
			v, err := strconv.ParseFloat(f[i], 64)
			if err != nil {
				return nil, fmt.Errorf("benchmark %s: %s %q: %w", name, unit, f[i], err)
			}
			if unit == "ns/op" {
				unit, v = "sec/op", v/1e9
			}
			if out[name] == nil {
				out[name] = map[string][]float64{}
			}
			out[name][unit] = append(out[name][unit], v)
		}
	}
	return out, sc.Err()
}

// machine is what the run was measured on. A probe that fails leaves its
// field empty.
type machine struct {
	CPU      string  `json:"cpu"`
	CPUs     int     `json:"cpus"`
	MemoryGB float64 `json:"memory_gb"`
	OS       string  `json:"os"`
	Go       string  `json:"go"`
}

// runEnv is what run.sh recorded about the run's environment in env.txt, and
// the CPU count of the machine that summarized it. The GOFLAGS of the run
// say, among other things, which -p the app builds ran at.
type runEnv struct {
	GoFlags string `json:"goflags"`
	NumCPU  int    `json:"num_cpu"`
}

// readEnv reads <results>/env.txt, whose lines are KEY=value. It returns nil
// if the file is absent or has no GOFLAGS line: the summary then records
// nothing.
func readEnv(results string) *runEnv {
	b, err := os.ReadFile(filepath.Join(results, "env.txt"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "GOFLAGS="); ok {
			return &runEnv{GoFlags: v, NumCPU: runtime.NumCPU()}
		}
	}
	return nil
}

// probeMachine reads the machine. Tests replace it, so that they do not
// depend on the host.
var probeMachine = func() machine {
	m := machine{CPUs: runtime.NumCPU()}
	if m.CPU = output("sysctl", "-n", "machdep.cpu.brand_string"); m.CPU == "" {
		m.CPU = parseCPUInfo(readFile("/proc/cpuinfo"))
	}
	if b, err := strconv.ParseFloat(output("sysctl", "-n", "hw.memsize"), 64); err == nil {
		m.MemoryGB = b / (1 << 30)
	} else {
		m.MemoryGB = parseMemInfo(readFile("/proc/meminfo"))
	}
	m.MemoryGB = float64(int(m.MemoryGB*10+0.5)) / 10
	m.OS = output("uname", "-sr")
	m.Go = output("go", "env", "GOVERSION")
	return m
}

// output is the trimmed stdout of a command, or "" if it fails.
func output(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// parseCPUInfo is the first model name of a /proc/cpuinfo.
func parseCPUInfo(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// parseMemInfo is the MemTotal of a /proc/meminfo, in GiB.
func parseMemInfo(s string) float64 {
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && k == "MemTotal" {
			if f := strings.Fields(v); len(f) > 0 {
				if kb, err := strconv.ParseFloat(f[0], 64); err == nil {
					return kb / (1 << 20)
				}
			}
		}
	}
	return 0
}

// probeCommit is velox's version: the short commit of root's HEAD, plus
// -dirty when a tracked file differs. Untracked files do not count: an
// untracked .DS_Store would mark every run dirty. It is "" if git fails.
var probeCommit = func(root string) string {
	rev := output("git", "-C", root, "rev-parse", "--short", "HEAD")
	if rev == "" {
		return ""
	}
	if output("git", "-C", root, "status", "--porcelain", "--untracked-files=no") != "" {
		rev += "-dirty"
	}
	return rev
}

// harnessModules are the summary's versions that come from a harness go.mod:
// the key → the module path and the directory of the go.mod that pins it.
var harnessModules = []struct{ Key, Path, ModDir string }{
	{"veloxe", "github.com/syssam/velox", veloxeORM.ModDir},
	{"ent", "entgo.io/ent", ""},
	{"sqlite", "modernc.org/sqlite", ""},
	{"pgx", "github.com/jackc/pgx/v5", ""},
	{"x/tools", "golang.org/x/tools", ""},
}

// readVersions is velox's commit and the versions the harness go.mod pins.
// The commit is the one in results/commit.txt, which run.sh writes when the
// run starts, if that file exists and is not empty; otherwise it is probed now.
func readVersions(results string) (map[string]string, error) {
	dir, err := harnessDir()
	if err != nil {
		return nil, err
	}
	v := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(results, "commit.txt")); err == nil && strings.TrimSpace(string(b)) != "" {
		v["velox"] = strings.TrimSpace(string(b))
	} else {
		v["velox"] = probeCommit(filepath.Join(dir, "..", ".."))
	}
	for _, m := range harnessModules {
		required, err := requirements(filepath.Join(dir, m.ModDir, "go.mod"))
		if err != nil {
			return nil, err
		}
		if required[m.Path] == "" {
			return nil, fmt.Errorf("%s does not require %s", filepath.Join(m.ModDir, "go.mod"), m.Path)
		}
		v[m.Key] = required[m.Path]
	}
	return v, nil
}

// requirements is what the go.mod at path requires: the module path → the version.
func requirements(path string) (map[string]string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := modfile.ParseLax("go.mod", src, nil)
	if err != nil {
		return nil, err
	}
	required := map[string]string{}
	for _, r := range f.Require {
		required[r.Mod.Path] = r.Mod.Version
	}
	return required, nil
}

// config is the run's settings, as the summary records them.
type config struct {
	Runs       int    `json:"runs"`
	Services   int    `json:"services"`
	BenchCount int    `json:"bench_count"`
	Benchtime  string `json:"benchtime"`
	Sizes      []int  `json:"sizes"`
	// Variants are the runtime variants (see runtimeVariants) whose
	// PostgreSQL rounds the run made, and which are therefore required.
	Variants []string `json:"variants,omitempty"`
	// Steps is what the run measured: all (build and runtime), or runtime.
	Steps string `json:"steps,omitempty"`
}

// runtimeOnly reports whether the run skipped the build steps.
func (c config) runtimeOnly() bool { return c.Steps == "runtime" }

// parseConfig turns the -config k=v arguments into a map.
func parseConfig(kvs []string) (map[string]any, error) {
	m := map[string]any{}
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("config %q: want key=value", kv)
		}
		m[k] = v
	}
	return m, nil
}

// newConfig reads cfg: runs, services and bench_count are numbers (or their
// decimal text), benchtime is a string, sizes is a list of numbers or text
// that separates them with spaces or commas, variants is a list of runtime
// variants in the same way, and steps is all (the default) or runtime. Any
// other key is an error.
func newConfig(cfg map[string]any) (config, error) {
	var c config
	for k, v := range cfg {
		var err error
		switch k {
		case "runs":
			c.Runs, err = toInt(v)
		case "services":
			c.Services, err = toInt(v)
		case "bench_count":
			c.BenchCount, err = toInt(v)
		case "benchtime":
			s, ok := v.(string)
			if !ok {
				err = fmt.Errorf("%v is not a string", v)
			}
			c.Benchtime = s
		case "sizes":
			c.Sizes, err = toInts(v)
		case "variants":
			c.Variants, err = toVariants(v)
		case "steps":
			s, ok := v.(string)
			switch {
			case !ok:
				err = fmt.Errorf("%v is not a string", v)
			case s == "all" || s == "runtime":
				c.Steps = s
			default:
				err = fmt.Errorf("%q: want all or runtime", s)
			}
		default:
			err = errors.New("unknown key (have runs, services, bench_count, benchtime, sizes, variants, steps)")
		}
		if err != nil {
			return config{}, fmt.Errorf("config %s: %w", k, err)
		}
	}
	c.Sizes = slices.Compact(slices.Sorted(slices.Values(c.Sizes)))
	c.Variants = slices.Compact(slices.Sorted(slices.Values(c.Variants)))
	if c.Steps == "" {
		c.Steps = "all"
	}
	return c, nil
}

// toVariants reads a list of runtime variants: a list of strings, or text
// that separates them with spaces or commas. An empty text is no variant.
func toVariants(v any) ([]string, error) {
	var out []string
	switch v := v.(type) {
	case []string:
		out = slices.Clone(v)
	case []any:
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%v is not a string", e)
			}
			out = append(out, s)
		}
	case string:
		out = strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' })
	default:
		return nil, fmt.Errorf("%v is not a list of variants", v)
	}
	for _, name := range out {
		if _, ok := runtimeVariants[name]; !ok {
			return nil, fmt.Errorf("unknown variant %q (have %s)", name, strings.Join(slices.Sorted(maps.Keys(runtimeVariants)), ", "))
		}
	}
	return out, nil
}

func toInt(v any) (int, error) {
	switch v := v.(type) {
	case int:
		return v, nil
	case float64:
		if v == float64(int(v)) {
			return int(v), nil
		}
	case string:
		return strconv.Atoi(strings.TrimSpace(v))
	}
	return 0, fmt.Errorf("%v is not a whole number", v)
}

func toInts(v any) ([]int, error) {
	switch v := v.(type) {
	case []int:
		return slices.Clone(v), nil
	case []any:
		var out []int
		for _, e := range v {
			n, err := toInt(e)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, nil
	case string:
		var out []int
		for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' }) {
			n, err := strconv.Atoi(f)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%v is not a list of sizes", v)
}

// summary is summary.json.
type summary struct {
	Machine   machine                                          `json:"machine"`
	Versions  map[string]string                                `json:"versions"`
	Steps     []stepRecord                                     `json:"steps"`
	Build     map[string]map[string]map[string]stat            `json:"build"`     // orm → n → metric
	Reference map[string]map[string]stat                       `json:"reference"` // orm → n
	Runtime   map[string]map[string]map[string]map[string]stat `json:"runtime"`   // dialect → orm → benchmark → unit
	Config    config                                           `json:"config"`
	// Env is absent when the results directory has no env.txt.
	Env *runEnv `json:"env,omitempty"`
	// Missing lists the results that were absent when -partial let them be.
	Missing []string `json:"missing"`
}

var (
	buildFileRE = regexp.MustCompile(`^build-([a-z0-9]+)-n(\d+)-r(\d+)\.json$`)
	benchFileRE = regexp.MustCompile(`^bench-([a-z0-9]+)-([a-z0-9]+)-(\d+)\.txt$`)
)

// numbered is a file of a numbered run.
type numbered struct {
	path string
	i    int
}

// benchDialects are the dialects every run measures.
var benchDialects = []string{"sqlite", "postgres"}

// summarize reads the results directory: raw/build-<orm>-n<N>-r<run>.json,
// raw/bench-<dialect>-<orm>-<i>.txt and steps.jsonl. The runs of a build
// metric, and the samples of a benchmark, are in run order. It fails unless
// the results are complete (see summarizePartial).
func summarize(results string, cfg map[string]any) (summary, error) {
	return summarizePartial(results, cfg, false)
}

// summarizePartial is summarize with the completeness check made optional.
// The results are complete when every ORM has a build JSON for every size and
// every run 1..config.runs (none under steps=runtime), and a round file
// holding all seven benchmarks, in every unit, for every dialect and every
// round 1..bench_count; each configured variant needs the same on postgres.
// A variant's round files are summarized whether or not config lists it. A
// setting that config leaves out is the highest number found. Without
// partial, anything missing is an error that names it; with partial, the
// summary lists it in Missing.
func summarizePartial(results string, cfg map[string]any, partial bool) (summary, error) {
	c, err := newConfig(cfg)
	if err != nil {
		return summary{}, err
	}
	s := summary{
		Machine:   probeMachine(),
		Env:       readEnv(results),
		Build:     map[string]map[string]map[string]stat{},
		Reference: map[string]map[string]stat{},
		Runtime:   map[string]map[string]map[string]map[string]stat{},
		Missing:   []string{},
	}
	if s.Versions, err = readVersions(results); err != nil {
		return s, err
	}
	if s.Steps, err = readSteps(filepath.Join(results, "steps.jsonl")); err != nil {
		return s, err
	}
	raw := filepath.Join(results, "raw")
	sizes, haveBuild, err := summarizeBuild(&s, raw, c.Services, c.runtimeOnly())
	if err != nil {
		return s, err
	}
	if len(c.Sizes) == 0 {
		c.Sizes = sizes
	}
	for _, n := range sizes {
		if !slices.Contains(c.Sizes, n) {
			return s, fmt.Errorf("build results for n=%d, which config sizes %v do not list", n, c.Sizes)
		}
	}
	haveRounds, err := summarizeRuntime(&s, raw)
	if err != nil {
		return s, err
	}
	s.Missing = missingResults(&c, haveBuild, haveRounds)
	s.Config = c
	if len(s.Missing) > 0 && !partial {
		list := s.Missing
		more := ""
		if len(list) > 20 {
			list, more = list[:20], fmt.Sprintf("\n  and %d more", len(list)-20)
		}
		return s, fmt.Errorf("incomplete results (-partial summarizes them anyway), missing:\n  %s%s", strings.Join(list, "\n  "), more)
	}
	return s, nil
}

// buildKey is one build JSON; roundKey is one bench round file.
type buildKey struct {
	orm    string
	n, run int
}
type roundKey struct {
	dialect, orm string
	round        int
}

// missingResults lists what the run should have produced and did not. It
// fills in c.Runs and c.BenchCount when they are 0, as the highest number found.
func missingResults(c *config, haveBuild map[buildKey]bool, haveRounds map[roundKey]map[string]map[string]bool) []string {
	missing := []string{}
	if c.Runs == 0 {
		for k := range haveBuild {
			c.Runs = max(c.Runs, k.run)
		}
	}
	if c.BenchCount == 0 {
		for k := range haveRounds {
			c.BenchCount = max(c.BenchCount, k.round)
		}
	}
	if !c.runtimeOnly() {
		for _, n := range c.Sizes {
			for _, o := range orms {
				for r := 1; r <= c.Runs; r++ {
					if !haveBuild[buildKey{o.Name, n, r}] {
						missing = append(missing, fmt.Sprintf("build %s n=%d run %d", o.Name, n, r))
					}
				}
			}
		}
	}
	for _, d := range benchDialects {
		var names []string
		for _, o := range orms {
			names = append(names, o.Name)
		}
		if d == "postgres" {
			names = append(names, c.Variants...)
		}
		for _, o := range names {
			for i := 1; i <= max(c.BenchCount, 1); i++ {
				got, ok := haveRounds[roundKey{d, o, i}]
				if !ok {
					missing = append(missing, fmt.Sprintf("bench %s %s round %d", d, o, i))
					continue
				}
				for _, name := range benchNames {
					for _, u := range runtimeUnits {
						if !got[name][u] {
							missing = append(missing, fmt.Sprintf("bench %s %s round %d: %s %s", d, o, i, name, u))
						}
					}
				}
			}
		}
	}
	return missing
}

// readSteps reads the step log, one stepRecord per line.
func readSteps(path string) ([]stepRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	steps := []stepRecord{}
	for i, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var r stepRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		steps = append(steps, r)
	}
	return steps, nil
}

// summarizeBuild fills s.Build and s.Reference from raw/build-*.json and
// returns the sizes found, in numeric order, and which builds exist. If
// services is not 0, every build JSON must have been made with that many.
// Finding no build JSON is an error unless runtimeOnly.
func summarizeBuild(s *summary, raw string, services int, runtimeOnly bool) ([]int, map[buildKey]bool, error) {
	files, err := filepath.Glob(filepath.Join(raw, "build-*.json"))
	if err != nil {
		return nil, nil, err
	}
	type key struct {
		orm string
		n   int
	}
	type sample struct {
		run  int
		vals map[string]float64
	}
	groups := map[key][]sample{}
	have := map[buildKey]bool{}
	for _, p := range files {
		m := buildFileRE.FindStringSubmatch(filepath.Base(p))
		if m == nil {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		var r map[string]any
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", p, err)
		}
		n, _ := strconv.Atoi(m[2])
		run, _ := strconv.Atoi(m[3])
		if _, err := ormByName(m[1]); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", p, err)
		}
		if r["orm"] != m[1] || r["n"] != float64(n) || r["run"] != float64(run) {
			return nil, nil, fmt.Errorf("%s: holds orm %v, n %v, run %v, which its name does not say", p, r["orm"], r["n"], r["run"])
		}
		if services != 0 && r["services"] != float64(services) {
			return nil, nil, fmt.Errorf("%s: made with services %v, but config services is %d", p, r["services"], services)
		}
		vals := map[string]float64{}
		for _, k := range rawKeys() {
			v, ok := r[k].(float64)
			if !ok {
				return nil, nil, fmt.Errorf("%s: no number for %s", p, k)
			}
			vals[k] = v
		}
		k := key{orm: m[1], n: n}
		groups[k] = append(groups[k], sample{run, vals})
		have[buildKey{m[1], n, run}] = true
	}
	if len(groups) == 0 && !runtimeOnly {
		return nil, nil, fmt.Errorf("no build-<orm>-n<N>-r<run>.json in %s", raw)
	}
	var sizes []int
	for k, ss := range groups {
		slices.SortFunc(ss, func(a, b sample) int { return a.run - b.run })
		n := strconv.Itoa(k.n)
		if s.Build[k.orm] == nil {
			s.Build[k.orm], s.Reference[k.orm] = map[string]map[string]stat{}, map[string]stat{}
		}
		s.Build[k.orm][n] = map[string]stat{}
		for _, mt := range buildMetrics {
			s.Build[k.orm][n][mt.Key] = newStat(column(ss, func(x sample) float64 {
				if mt.Derive != nil {
					return mt.Derive(x.vals)
				}
				return x.vals[mt.Key]
			}), mt.LowerIsBetter)
		}
		s.Reference[k.orm][n] = newStat(column(ss, func(x sample) float64 { return x.vals[refMetric] }), true)
		if !slices.Contains(sizes, k.n) {
			sizes = append(sizes, k.n)
		}
	}
	slices.Sort(sizes)
	return sizes, have, nil
}

func column[T any](xs []T, f func(T) float64) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = f(x)
	}
	return out
}

// summarizeRuntime fills s.Runtime from the round files raw/bench-<dialect>-<orm>-<i>.txt,
// where <orm> is an ORM or a runtime variant.
// The per-ORM files that run.sh concatenates carry no round number and are not
// read. It returns, for each round file, which benchmarks and units it holds.
func summarizeRuntime(s *summary, raw string) (map[roundKey]map[string]map[string]bool, error) {
	files, err := filepath.Glob(filepath.Join(raw, "bench-*.txt"))
	if err != nil {
		return nil, err
	}
	rounds := map[[2]string][]numbered{} // dialect, orm → files
	have := map[roundKey]map[string]map[string]bool{}
	for _, p := range files {
		m := benchFileRE.FindStringSubmatch(filepath.Base(p))
		if m == nil {
			continue
		}
		if _, err := runtimeORMByName(m[2]); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		i, _ := strconv.Atoi(m[3])
		k := [2]string{m[1], m[2]}
		rounds[k] = append(rounds[k], numbered{p, i})
	}
	for k, fs := range rounds {
		slices.SortFunc(fs, func(a, b numbered) int { return a.i - b.i })
		samples := map[string]map[string][]float64{}
		for _, f := range fs {
			fh, err := os.Open(f.path)
			if err != nil {
				return nil, err
			}
			got, err := parseBench(fh)
			_ = fh.Close()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.path, err)
			}
			if len(got) == 0 {
				return nil, fmt.Errorf("%s: no benchmark results", f.path)
			}
			rk := roundKey{k[0], k[1], f.i}
			have[rk] = map[string]map[string]bool{}
			for name, units := range got {
				have[rk][name] = map[string]bool{}
				if samples[name] == nil {
					samples[name] = map[string][]float64{}
				}
				for u, vs := range units {
					have[rk][name][u] = true
					samples[name][u] = append(samples[name][u], vs...)
				}
			}
		}
		d, o := k[0], k[1]
		if s.Runtime[d] == nil {
			s.Runtime[d] = map[string]map[string]map[string]stat{}
		}
		s.Runtime[d][o] = map[string]map[string]stat{}
		for name, units := range samples {
			s.Runtime[d][o][name] = map[string]stat{}
			for u, vs := range units {
				s.Runtime[d][o][name][u] = newStat(vs, true)
			}
		}
	}
	return have, nil
}

// summarizeMain is compare summarize -results DIR -out summary.json [-config k=v…].
func summarizeMain(args []string) int {
	fs := flag.NewFlagSet("summarize", flag.ContinueOnError)
	results := fs.String("results", "", "the results directory")
	out := fs.String("out", "", "the summary's JSON file")
	partial := fs.Bool("partial", false, "summarize incomplete results too, and list what is missing")
	var kvs configFlags
	fs.Var(&kvs, "config", "a setting of the run, key=value (runs, services, bench_count, benchtime, sizes, variants, steps); repeatable")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *results == "" || *out == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "compare summarize: -results and -out are required, and nothing else")
		return 2
	}
	cfg, err := parseConfig(kvs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compare summarize:", err)
		return 2
	}
	s, err := summarizePartial(*results, cfg, *partial)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compare summarize:", err)
		return 1
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err == nil {
		tmp := *out + ".tmp"
		if err = os.WriteFile(tmp, append(b, '\n'), 0o644); err == nil {
			if err = os.Rename(tmp, *out); err != nil {
				err = errors.Join(err, os.Remove(tmp))
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "compare summarize:", err)
		return 1
	}
	return 0
}

// configFlags collects the repeated -config flag.
type configFlags []string

func (c *configFlags) String() string     { return strings.Join(*c, " ") }
func (c *configFlags) Set(v string) error { *c = append(*c, v); return nil }

// loss is one metric in which one other ORM's median beats velox's: Winner
// is that ORM, and an ORM that also beats velox has a loss of its own. Section
// is "build" (Key the size, Metric the build metric, Unit empty) or "runtime"
// (Key the dialect, Metric the benchmark, Unit its unit). Best is Winner's
// median, and Ratio is Best over Velox2.
type loss struct {
	Section, Key, Metric, Unit, Winner string
	Velox2, Best, Ratio                float64
}

// beaters lists the ORMs other than velox whose median in stats beats
// velox's in the metric's direction, in the order of orms. A tie does not
// beat. stats without velox has no beaters. A runtime variant is velox's
// own and never beats it.
func beaters(stats map[string]stat, lowerIsBetter bool) []string {
	v2, ok := stats["velox"]
	if !ok {
		return nil
	}
	var out []string
	for _, o := range orms[1:] {
		st, ok := stats[o.Name]
		if !ok {
			continue
		}
		if lowerIsBetter && st.Median < v2.Median || !lowerIsBetter && st.Median > v2.Median {
			out = append(out, o.Name)
		}
	}
	return out
}

// losses lists every build metric per size, and every runtime unit per dialect
// and benchmark, where another ORM's median beats velox's in the metric's
// direction: one loss for each ORM that beats velox. There is no noise
// threshold. The result is sorted by Section, Key, Metric and Unit, and, for
// the same metric, in the order of orms.
func losses(s summary) []loss {
	var out []loss
	consider := func(section, key, metric, unit string, lowerIsBetter bool, stats map[string]stat) {
		for _, w := range beaters(stats, lowerIsBetter) {
			out = append(out, loss{section, key, metric, unit, w, stats["velox"].Median, stats[w].Median, ratio(stats[w].Median, stats["velox"].Median)})
		}
	}
	for n := range s.Build["velox"] {
		for _, m := range buildMetrics {
			stats := map[string]stat{}
			for orm, bySize := range s.Build {
				if st, ok := bySize[n][m.Key]; ok {
					stats[orm] = st
				}
			}
			consider("build", n, m.Key, "", m.LowerIsBetter, stats)
		}
	}
	for d, byORM := range s.Runtime {
		for name, units := range byORM["velox"] {
			for u := range units {
				stats := map[string]stat{}
				for orm, byName := range byORM {
					if st, ok := byName[name][u]; ok {
						stats[orm] = st
					}
				}
				consider("runtime", d, name, u, true, stats)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b loss) int {
		return cmp.Or(strings.Compare(a.Section, b.Section), compareKeys(a.Key, b.Key),
			strings.Compare(a.Metric, b.Metric), strings.Compare(a.Unit, b.Unit))
	})
	return out
}

// compareKeys orders sizes numerically and anything else as text.
func compareKeys(a, b string) int {
	x, errX := strconv.Atoi(a)
	y, errY := strconv.Atoi(b)
	if errX == nil && errY == nil {
		return cmp.Compare(x, y)
	}
	return strings.Compare(a, b)
}

// ratio is a ÷ b; it is 1 when both are 0, and +Inf when only b is.
func ratio(a, b float64) float64 {
	if a == 0 && b == 0 {
		return 1
	}
	return a / b
}
