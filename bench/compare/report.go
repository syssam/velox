package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
)

// display is how the report names each ORM.
var display = map[string]string{"velox2": "velox2", "oldvelox": "old velox", "ent": "ent"}

// versionOrder is the order of the versions table.
var versionOrder = []string{"velox2", "oldvelox", "ent", "sqlite", "pgx", "x/tools"}

// fmtNum prints v with four significant digits, or as a whole number.
func fmtNum(v float64) string {
	switch {
	case math.IsInf(v, 0) || math.IsNaN(v):
		return "n/a"
	case v == math.Trunc(v) && math.Abs(v) < 1e15, math.Abs(v) >= 1000:
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'g', 4, 64)
}

// cell is "median (worst)", or "-" for a measurement that was not made.
func cell(st stat, ok bool) string {
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%s (%s)", fmtNum(st.Median), fmtNum(st.Worst))
}

// ratioCell is the median of st over velox2's, to two decimals.
func ratioCell(st stat, ok bool, v2 stat, v2ok bool) string {
	if !ok || !v2ok || v2.Median == 0 && st.Median != 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", ratio(st.Median, v2.Median))
}

// esc keeps a text from breaking a Markdown table cell.
func esc(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// writeReport writes the summary as Markdown tables.
func writeReport(w io.Writer, s summary) error {
	var b strings.Builder
	sizes := s.Config.Sizes
	if len(sizes) == 0 {
		for n := range s.Build["velox2"] {
			x, _ := strconv.Atoi(n)
			sizes = append(sizes, x)
		}
		slices.Sort(sizes)
	}

	b.WriteString("# velox2 performance comparison: tables\n\n")
	writeMachine(&b, s, sizes)

	b.WriteString("## Build cost\n\n")
	b.WriteString("Each cell is `median (worst)` over the runs; the ratios divide the medians. ")
	b.WriteString("The `_x` metrics are CPU seconds over the reference build's CPU seconds.\n\n")
	for _, m := range buildMetrics {
		dir := "lower is better"
		if !m.LowerIsBetter {
			dir = "higher is better"
		}
		fmt.Fprintf(&b, "### %s\n\n%s.\n\n", m.Key, dir)
		writeTableHead(&b, "size")
		for _, n := range sizes {
			k := strconv.Itoa(n)
			get := func(orm string) (stat, bool) { st, ok := s.Build[orm][k][m.Key]; return st, ok }
			writeRow(&b, "n="+k, get)
		}
		b.WriteString("\n")
	}

	for _, d := range slices.Sorted(mapKeys(s.Runtime)) {
		fmt.Fprintf(&b, "## Runtime: %s\n\n", d)
		names := benchRows(s.Runtime[d])
		for _, u := range runtimeUnits {
			fmt.Fprintf(&b, "### %s (%s)\n\nLower is better.\n\n", u, d)
			writeTableHead(&b, "benchmark")
			for _, name := range names {
				get := func(orm string) (stat, bool) { st, ok := s.Runtime[d][orm][name][u]; return st, ok }
				writeRow(&b, name, get)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Where velox2 loses\n\n")
	b.WriteString("Every metric in which another ORM's median beats velox2's. There is no noise threshold: a difference of any size is listed.\n\n")
	ls := losses(s)
	if len(ls) == 0 {
		b.WriteString("velox2 is best on every metric measured.\n")
	}
	for _, l := range ls {
		dir := "lower is better"
		where := fmt.Sprintf("build, n=%s, %s", l.Key, l.Metric)
		if l.Section == "runtime" {
			where = fmt.Sprintf("runtime, %s, %s, %s", l.Key, l.Metric, l.Unit)
		} else {
			for _, m := range buildMetrics {
				if m.Key == l.Metric && !m.LowerIsBetter {
					dir = "higher is better"
				}
			}
		}
		fmt.Fprintf(&b, "- %s: %s %s vs velox2 %s (%s ÷ velox2 = %s, %s)\n",
			where, display[l.Winner], fmtNum(l.Best), fmtNum(l.Velox2), display[l.Winner], fmtRatio(l.Ratio), dir)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func fmtRatio(r float64) string {
	if math.IsInf(r, 0) {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", r)
}

func mapKeys[K comparable, V any](m map[K]V) func(yield func(K) bool) {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// benchRows are the benchmarks of any ORM in the dialect's results: the known
// ones in their order, then any other by name.
func benchRows(byORM map[string]map[string]map[string]stat) []string {
	seen := map[string]bool{}
	for _, byName := range byORM {
		for name := range byName {
			seen[name] = true
		}
	}
	var rows []string
	for _, name := range benchNames {
		if seen[name] {
			rows = append(rows, name)
			delete(seen, name)
		}
	}
	return append(rows, slices.Sorted(mapKeys(seen))...)
}

func writeTableHead(b *strings.Builder, first string) {
	fmt.Fprintf(b, "| %s | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |\n|---|---|---|---|---|---|\n", first)
}

// writeRow writes one row of a metric table; get finds an ORM's stat.
func writeRow(b *strings.Builder, label string, get func(orm string) (stat, bool)) {
	v2, v2ok := get("velox2")
	old, oldok := get("oldvelox")
	ent, entok := get("ent")
	fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n", label,
		cell(v2, v2ok), cell(old, oldok), cell(ent, entok),
		ratioCell(old, oldok, v2, v2ok), ratioCell(ent, entok, v2, v2ok))
}

// writeMachine writes the machine and load section.
func writeMachine(b *strings.Builder, s summary, sizes []int) {
	or := func(v string) string {
		if v == "" {
			return "unknown"
		}
		return v
	}
	m := s.Machine
	b.WriteString("## Machine and load\n\n")
	fmt.Fprintf(b, "- CPU: %s (%d CPUs)\n- Memory: %s GB\n- OS: %s\n- Go: %s\n",
		or(m.CPU), m.CPUs, fmtNum(m.MemoryGB), or(m.OS), or(m.Go))
	fmt.Fprintf(b, "- Runs: %d, services: %d, benchmark rounds: %d, benchtime: %s\n\n",
		s.Config.Runs, s.Config.Services, s.Config.BenchCount, or(s.Config.Benchtime))

	b.WriteString("### Versions\n\n| component | version |\n|---|---|\n")
	keys := slices.Clone(versionOrder)
	for _, k := range slices.Sorted(mapKeys(s.Versions)) {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		if v, ok := s.Versions[k]; ok {
			fmt.Fprintf(b, "| %s | %s |\n", k, or(v))
		}
	}

	b.WriteString("\n### Reference build\n\n")
	b.WriteString("`ref_cpu_s`: the CPU seconds of a fixed reference build, taken around each measurement. It measures the machine's load, not an ORM; the `_x` metrics divide by it. Each cell is `median (worst)`.\n\n")
	b.WriteString("| size | velox2 | old velox | ent |\n|---|---|---|---|\n")
	for _, n := range sizes {
		k := strconv.Itoa(n)
		v2, ok1 := s.Reference["velox2"][k]
		old, ok2 := s.Reference["oldvelox"][k]
		ent, ok3 := s.Reference["ent"][k]
		fmt.Fprintf(b, "| n=%s | %s | %s | %s |\n", k, cell(v2, ok1), cell(old, ok2), cell(ent, ok3))
	}

	b.WriteString("\n### Steps\n\n")
	b.WriteString("Load is the 1m, 5m and 15m load average. A step that waited at its load gate shows how long, and whether the wait timed out: it then ran under the load shown.\n\n")
	b.WriteString("| step | start | wall (s) | exit | load before | load after | gate waited (s) | gate |\n|---|---|---|---|---|---|---|---|\n")
	for _, r := range s.Steps {
		waited, gate := "-", "-"
		if g := r.Gate; g != nil {
			waited = fmtNum(g.WaitedS)
			gate = fmt.Sprintf("passed (max %s)", fmtNum(g.Max))
			if g.TimedOut {
				gate = fmt.Sprintf("**timed out** (max %s)", fmtNum(g.Max))
			}
		}
		fmt.Fprintf(b, "| %s | %s | %s | %d | %s | %s | %s | %s |\n", esc(r.Name), r.Start, fmtNum(r.WallS), r.Exit,
			fmtLoad(r.LoadBefore), fmtLoad(r.LoadAfter), waited, gate)
	}
	b.WriteString("\n")
}

func fmtLoad(l load) string {
	return fmt.Sprintf("%.2f / %.2f / %.2f", l.One, l.Five, l.Fifteen)
}

// reportMain is compare report -summary summary.json; the report goes to stdout.
func reportMain(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	path := fs.String("summary", "", "the summary's JSON file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "compare report: -summary is required, and nothing else")
		return 2
	}
	b, err := os.ReadFile(*path)
	var s summary
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	if err == nil {
		err = writeReport(os.Stdout, s)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "compare report:", err)
		return 1
	}
	return 0
}
