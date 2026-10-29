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

// metric is one build metric: its key in the build JSON and its direction.
type metric struct {
	Key           string
	LowerIsBetter bool
}

// buildMetrics are the build metrics, in table order. orm, n, services, run
// and ref_* are not here: they identify a run or measure the machine's load.
var buildMetrics = []metric{
	{"codegen_x", true}, {"cold_x", true}, {"edit_x", true},
	{"codegen_cpu_s", true}, {"codegen_wall_s", true},
	{"cold_cpu_s", true}, {"cold_wall_s", true}, {"cold_app_action_s", true}, {"cold_dep_action_s", true},
	{"regen_cpu_s", true}, {"regen_wall_s", true},
	{"rebuild_cpu_s", true}, {"rebuild_wall_s", true},
	{"rebuild_rerun", true}, {"rebuild_cached", false}, {"rebuild_relinks", true},
	{"gen_lines", true}, {"gen_bytes", true},
	{"service_binary_mb", true}, {"link_peak_mb", true},
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

// probeCommit is velox2's version: the short commit of root's HEAD, plus
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

// harnessModules are the summary's versions that come from the harness's
// go.mod: the key → the module path.
var harnessModules = []struct{ Key, Path string }{
	{"oldvelox", "github.com/syssam/velox"},
	{"ent", "entgo.io/ent"},
	{"sqlite", "modernc.org/sqlite"},
	{"pgx", "github.com/jackc/pgx/v5"},
	{"x/tools", "golang.org/x/tools"},
}

// readVersions is velox2's commit and the versions the harness go.mod pins.
func readVersions() (map[string]string, error) {
	dir, err := harnessDir()
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(filepath.Join(dir, "go.mod"))
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
	v := map[string]string{"velox2": probeCommit(filepath.Join(dir, "..", ".."))}
	for _, m := range harnessModules {
		if required[m.Path] == "" {
			return nil, fmt.Errorf("the harness go.mod does not require %s", m.Path)
		}
		v[m.Key] = required[m.Path]
	}
	return v, nil
}

// config is the run's settings, as the summary records them.
type config struct {
	Runs       int    `json:"runs"`
	Services   int    `json:"services"`
	BenchCount int    `json:"bench_count"`
	Benchtime  string `json:"benchtime"`
	Sizes      []int  `json:"sizes"`
}

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
// decimal text), benchtime is a string, and sizes is a list of numbers or text
// that separates them with spaces or commas. Any other key is an error.
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
		default:
			err = errors.New("unknown key (have runs, services, bench_count, benchtime, sizes)")
		}
		if err != nil {
			return config{}, fmt.Errorf("config %s: %w", k, err)
		}
	}
	slices.Sort(c.Sizes)
	return c, nil
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

// summarize reads the results directory: raw/build-<orm>-n<N>-r<run>.json,
// raw/bench-<dialect>-<orm>-<i>.txt and steps.jsonl. The runs of a build
// metric, and the samples of a benchmark, are in run order.
func summarize(results string, cfg map[string]any) (summary, error) {
	c, err := newConfig(cfg)
	if err != nil {
		return summary{}, err
	}
	s := summary{
		Machine:   probeMachine(),
		Build:     map[string]map[string]map[string]stat{},
		Reference: map[string]map[string]stat{},
		Runtime:   map[string]map[string]map[string]map[string]stat{},
	}
	if s.Versions, err = readVersions(); err != nil {
		return s, err
	}
	if s.Steps, err = readSteps(filepath.Join(results, "steps.jsonl")); err != nil {
		return s, err
	}
	sizes, err := summarizeBuild(&s, filepath.Join(results, "raw"))
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
	for _, n := range c.Sizes {
		if !slices.Contains(sizes, n) {
			return s, fmt.Errorf("config sizes list n=%d, which has no build results", n)
		}
	}
	s.Config = c
	return s, summarizeRuntime(&s, filepath.Join(results, "raw"))
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

// summarizeBuild fills s.Build and s.Ref from raw/build-*.json and returns the
// sizes found, in numeric order.
func summarizeBuild(s *summary, raw string) ([]int, error) {
	files, err := filepath.Glob(filepath.Join(raw, "build-*.json"))
	if err != nil {
		return nil, err
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
	for _, p := range files {
		m := buildFileRE.FindStringSubmatch(filepath.Base(p))
		if m == nil {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var r map[string]any
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		n, _ := strconv.Atoi(m[2])
		run, _ := strconv.Atoi(m[3])
		if _, err := ormByName(m[1]); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if r["orm"] != m[1] || r["n"] != float64(n) || r["run"] != float64(run) {
			return nil, fmt.Errorf("%s: holds orm %v, n %v, run %v, which its name does not say", p, r["orm"], r["n"], r["run"])
		}
		vals := map[string]float64{}
		for _, k := range append([]string{refMetric}, metricKeys()...) {
			v, ok := r[k].(float64)
			if !ok {
				return nil, fmt.Errorf("%s: no number for %s", p, k)
			}
			vals[k] = v
		}
		k := key{orm: m[1], n: n}
		groups[k] = append(groups[k], sample{run, vals})
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("no build-<orm>-n<N>-r<run>.json in %s", raw)
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
			s.Build[k.orm][n][mt.Key] = newStat(column(ss, func(x sample) float64 { return x.vals[mt.Key] }), mt.LowerIsBetter)
		}
		s.Reference[k.orm][n] = newStat(column(ss, func(x sample) float64 { return x.vals[refMetric] }), true)
		if !slices.Contains(sizes, k.n) {
			sizes = append(sizes, k.n)
		}
	}
	slices.Sort(sizes)
	return sizes, nil
}

func column[T any](xs []T, f func(T) float64) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = f(x)
	}
	return out
}

func metricKeys() []string {
	keys := make([]string, len(buildMetrics))
	for i, m := range buildMetrics {
		keys[i] = m.Key
	}
	return keys
}

// summarizeRuntime fills s.Runtime from the round files raw/bench-<dialect>-<orm>-<i>.txt.
// The per-ORM files that run.sh concatenates carry no round number and are not read.
func summarizeRuntime(s *summary, raw string) error {
	files, err := filepath.Glob(filepath.Join(raw, "bench-*.txt"))
	if err != nil {
		return err
	}
	rounds := map[[2]string][]numbered{} // dialect, orm → files
	for _, p := range files {
		m := benchFileRE.FindStringSubmatch(filepath.Base(p))
		if m == nil {
			continue
		}
		if _, err := ormByName(m[2]); err != nil {
			return fmt.Errorf("%s: %w", p, err)
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
				return err
			}
			got, err := parseBench(fh)
			_ = fh.Close()
			if err != nil {
				return fmt.Errorf("%s: %w", f.path, err)
			}
			if len(got) == 0 {
				return fmt.Errorf("%s: no benchmark results", f.path)
			}
			for name, units := range got {
				if samples[name] == nil {
					samples[name] = map[string][]float64{}
				}
				for u, vs := range units {
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
	return nil
}

// summarizeMain is compare summarize -results DIR -out summary.json [-config k=v…].
func summarizeMain(args []string) int {
	fs := flag.NewFlagSet("summarize", flag.ContinueOnError)
	results := fs.String("results", "", "the results directory")
	out := fs.String("out", "", "the summary's JSON file")
	var kvs configFlags
	fs.Var(&kvs, "config", "a setting of the run, key=value (runs, services, bench_count, benchtime, sizes); repeatable")
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
	s, err := summarize(*results, cfg)
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

// loss is one metric in which another ORM's median beats velox2's. Section is
// "build" (Key the size, Metric the build metric, Unit empty) or "runtime"
// (Key the dialect, Metric the benchmark, Unit its unit). Ratio is Best over
// Velox2.
type loss struct {
	Section, Key, Metric, Unit, Winner string
	Velox2, Best, Ratio                float64
}

// losses lists every build metric per size, and every runtime unit per dialect
// and benchmark, where another ORM's median beats velox2's in the metric's
// direction. There is no noise threshold. The result is sorted by Section,
// Key, Metric and Unit.
func losses(s summary) []loss {
	var out []loss
	consider := func(section, key, metric, unit string, lowerIsBetter bool, stats map[string]stat) {
		v2, ok := stats["velox2"]
		if !ok {
			return
		}
		var best *stat
		var winner string
		for _, o := range orms[1:] {
			st, ok := stats[o.Name]
			if !ok {
				continue
			}
			beats := st.Median < v2.Median
			if !lowerIsBetter {
				beats = st.Median > v2.Median
			}
			if !beats {
				continue
			}
			if best == nil || (lowerIsBetter && st.Median < best.Median) || (!lowerIsBetter && st.Median > best.Median) {
				best, winner = &st, o.Name
			}
		}
		if best == nil {
			return
		}
		out = append(out, loss{section, key, metric, unit, winner, v2.Median, best.Median, ratio(best.Median, v2.Median)})
	}
	for n := range s.Build["velox2"] {
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
		for name, units := range byORM["velox2"] {
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
	slices.SortFunc(out, func(a, b loss) int {
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
