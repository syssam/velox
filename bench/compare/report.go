package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// display is how the report names each ORM and runtime variant.
var display = map[string]string{"velox": "velox", "veloxe": "veloxe", "ent": "ent", "veloxpgx": "velox (veloxpgx)"}

// otherORMs are the ORMs other than velox, in the order of their columns.
var otherORMs = []string{"veloxe", "ent"}

// versionOrder is the order of the versions table.
var versionOrder = []string{"velox", "veloxe", "ent", "sqlite", "pgx", "x/tools"}

// fmtNum prints v with three significant digits; a whole number stays whole.
func fmtNum(v float64) string {
	switch {
	case math.IsInf(v, 0) || math.IsNaN(v):
		return "n/a"
	case v == math.Trunc(v) && math.Abs(v) < 1e15, math.Abs(v) >= 1000:
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'g', 3, 64)
}

// fmtPair prints a and b with as few significant digits as tell them apart
// (at least three), so that two values that differ never read as equal.
func fmtPair(a, b float64) (string, string) {
	x, y := fmtNum(a), fmtNum(b)
	for p := 4; x == y && a != b && p <= 17; p++ {
		x, y = strconv.FormatFloat(a, 'g', p, 64), strconv.FormatFloat(b, 'g', p, 64)
	}
	return x, y
}

// cell is "median (worst)", or "-" for a measurement that was not made.
func cell(st stat, ok bool) string {
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%s (%s)", fmtNum(st.Median), fmtNum(st.Worst))
}

// ratioCell is the median of st over velox's, to three significant digits.
func ratioCell(st stat, ok bool, v2 stat, v2ok bool) string {
	if !ok || !v2ok || v2.Median == 0 && st.Median != 0 {
		return "-"
	}
	return fmtNum(ratio(st.Median, v2.Median))
}

// esc keeps a text from breaking a Markdown table cell.
func esc(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// writeReport writes the summary as Markdown tables.
func writeReport(w io.Writer, s summary) error {
	var b strings.Builder
	sizes := s.Config.Sizes
	if len(sizes) == 0 {
		for n := range s.Build["velox"] {
			x, _ := strconv.Atoi(n)
			sizes = append(sizes, x)
		}
		slices.Sort(sizes)
	}

	b.WriteString("# velox performance comparison: tables\n\n")
	b.WriteString("How to read the tables: a cell is `median (worst)` of the runs, where worst is the largest run, ")
	b.WriteString("or the smallest for a metric where higher is better (the tables say which). ")
	b.WriteString("The ratio columns are the other ORM ÷ velox, from the medians: for a lower-is-better metric, a ratio above 1 means velox is better; ")
	b.WriteString("for a higher-is-better one, a ratio below 1 does. ")
	b.WriteString("A `velox (veloxpgx)` column, where there is one, is velox's app on `veloxpgx.Open` (a pgx pool) instead of database/sql; it is velox too, so it is never a loss.\n\n")
	if len(s.Missing) > 0 {
		b.WriteString("## Incomplete results\n\n")
		b.WriteString("These results were not in the results directory when the summary was made, so the medians below may rest on fewer runs than planned, and the losses may be incomplete:\n\n")
		for _, m := range s.Missing {
			fmt.Fprintf(&b, "- %s\n", m)
		}
		b.WriteString("\n")
	}
	writeMachine(&b, s, sizes)

	if s.Config.runtimeOnly() {
		b.WriteString("Runtime only: the build steps were not run.\n\n")
	} else {
		writeBuild(&b, s, sizes)
	}

	last := lastSteps(s.Steps)
	for _, d := range slices.Sorted(maps.Keys(s.Runtime)) {
		fmt.Fprintf(&b, "## Runtime: %s\n\n", d)
		names := benchRows(s.Runtime[d])
		cols := runtimeColumns(s.Runtime[d])
		for _, u := range runtimeUnits {
			fmt.Fprintf(&b, "### %s (%s)\n\nLower is better.\n\n", u, d)
			writeTableHead(&b, "benchmark", cols)
			noted := false
			for _, name := range names {
				get := func(orm string) (stat, bool) { st, ok := s.Runtime[d][orm][name][u]; return st, ok }
				label := name
				if name == insertBulk {
					label, noted = name+" ¹", true
				}
				writeRow(&b, label, cols, get)
			}
			b.WriteString("\n")
			if noted {
				b.WriteString(insertBulkNote + "\n\n")
			}
		}
		if late := timedOutRounds(last, d); len(late) > 0 {
			fmt.Fprintf(&b, "**Timed-out load gate.** These %s benchmark rounds started after their load gate timed out, so their samples in the tables above were taken under load: %s.\n\n", d, strings.Join(late, "; "))
		}
	}

	b.WriteString("## Where velox loses\n\n")
	b.WriteString("Every metric in which another ORM's median beats velox's. There is no noise threshold: a difference of any size is listed.\n\n")
	ls := losses(s)
	switch {
	case len(ls) == 0 && len(s.Missing) > 0:
		b.WriteString("No loss among the results present, but the results are incomplete (see Incomplete results), so this does not show that velox is best.\n")
	case len(ls) == 0:
		b.WriteString("velox is best on every metric measured.\n")
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
		best, v2 := fmtPair(l.Best, l.Velox2)
		r := "n/a"
		if !math.IsInf(l.Ratio, 0) {
			r, _ = fmtPair(l.Ratio, 1)
		}
		fmt.Fprintf(&b, "- %s: %s %s vs velox %s (%s ÷ velox = %s, %s)\n",
			where, display[l.Winner], best, v2, display[l.Winner], r, dir)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeBuild writes the build tables, one per build metric.
func writeBuild(b *strings.Builder, s summary, sizes []int) {
	b.WriteString("## Build cost\n\n")
	b.WriteString("The `_x` metrics are CPU seconds over the reference build's CPU seconds.\n\n")
	for _, m := range buildMetrics {
		dir := "lower is better"
		if !m.LowerIsBetter {
			dir = "higher is better"
		}
		fmt.Fprintf(b, "### %s\n\n%s.\n\n", m.Key, dir)
		if m.Doc != "" {
			fmt.Fprintf(b, "%s\n\n", m.Doc)
		}
		writeTableHead(b, "size", otherORMs)
		for _, n := range sizes {
			k := strconv.Itoa(n)
			get := func(orm string) (stat, bool) { st, ok := s.Build[orm][k][m.Key]; return st, ok }
			writeRow(b, "n="+k, otherORMs, get)
		}
		b.WriteString("\n")
	}
	if late := timedOutBuilds(lastSteps(s.Steps)); len(late) > 0 {
		fmt.Fprintf(b, "**Timed-out load gate.** These builds started after their load gate timed out, so their numbers in every table above were taken under load: %s.\n\n", strings.Join(late, "; "))
	}
}

// runtimeColumns are the columns of a dialect's runtime tables after
// velox's: each runtime variant with results, then the other ORMs.
func runtimeColumns(byORM map[string]map[string]map[string]stat) []string {
	var cols []string
	for _, v := range slices.Sorted(maps.Keys(runtimeVariants)) {
		if _, ok := byORM[v]; ok {
			cols = append(cols, v)
		}
	}
	return append(cols, otherORMs...)
}

// insertBulk is the benchmark that does not do the same work in every ORM.
const insertBulk = "InsertBulk100"

// insertBulkNote is the footnote under every runtime table that has the
// InsertBulk100 row.
const insertBulkNote = "¹ InsertBulk100 is not the same work in every ORM: velox's `InsertAll` reads no keys back, " +
	"while ent and veloxe send `INSERT ... RETURNING id`, scan the 100 ids and return 100 entities. " +
	"The difference is by design and cannot be removed: ent has no bulk insert without the read-back. " +
	"A velox advantage in this row is partly the advantage of doing less."

var (
	buildStepRE = regexp.MustCompile(`^build-([a-z0-9]+)-n(\d+)-r(\d+)$`)
	gateStepRE  = regexp.MustCompile(`^gate-([a-z0-9]+)-(\d+)$`)
)

// gateNote says how long a timed-out gate waited and the load it gave up at.
func gateNote(r stepRecord) string {
	return fmt.Sprintf("gate waited %s s, load %s before", fmtNum(r.Gate.WaitedS), fmtNum(r.LoadBefore.One))
}

// lastSteps keeps the last record of each step name, in the order of those
// records. After a resume the log holds the failed or interrupted attempt of
// a step and the attempt that replaced it; only the last one counts.
func lastSteps(steps []stepRecord) []stepRecord {
	at := map[string]int{}
	for i, r := range steps {
		at[r.Name] = i
	}
	var out []stepRecord
	for i, r := range steps {
		if at[r.Name] == i {
			out = append(out, r)
		}
	}
	return out
}

// failedSteps names the steps whose last record has a non-zero exit.
func failedSteps(steps []stepRecord) []string {
	var out []string
	for _, r := range lastSteps(steps) {
		if r.Exit != 0 {
			out = append(out, fmt.Sprintf("%s (exit %d)", r.Name, r.Exit))
		}
	}
	return out
}

// timedOutBuilds names the builds whose load gate timed out.
func timedOutBuilds(steps []stepRecord) []string {
	var out []string
	for _, r := range steps {
		if m := buildStepRE.FindStringSubmatch(r.Name); m != nil && r.Gate != nil && r.Gate.TimedOut {
			out = append(out, fmt.Sprintf("%s n=%s run %s (%s)", display[m[1]], m[2], m[3], gateNote(r)))
		}
	}
	return out
}

// timedOutRounds names the rounds of dialect d whose load gate timed out.
func timedOutRounds(steps []stepRecord, d string) []string {
	var out []string
	for _, r := range steps {
		if m := gateStepRE.FindStringSubmatch(r.Name); m != nil && m[1] == d && r.Gate != nil && r.Gate.TimedOut {
			out = append(out, fmt.Sprintf("round %s (%s)", m[2], gateNote(r)))
		}
	}
	return out
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
	return append(rows, slices.Sorted(maps.Keys(seen))...)
}

// writeTableHead writes the head of a metric table: first, velox, then
// cols, then each of cols ÷ velox.
func writeTableHead(b *strings.Builder, first string, cols []string) {
	fmt.Fprintf(b, "| %s | velox |", first)
	for _, c := range cols {
		fmt.Fprintf(b, " %s |", display[c])
	}
	for _, c := range cols {
		fmt.Fprintf(b, " %s ÷ velox |", display[c])
	}
	b.WriteString("\n|---|---|" + strings.Repeat("---|", 2*len(cols)) + "\n")
}

// writeRow writes one row of a metric table, with the columns of
// writeTableHead; get finds an ORM's stat.
func writeRow(b *strings.Builder, label string, cols []string, get func(orm string) (stat, bool)) {
	v2, v2ok := get("velox")
	fmt.Fprintf(b, "| %s | %s |", label, cell(v2, v2ok))
	for _, c := range cols {
		st, ok := get(c)
		fmt.Fprintf(b, " %s |", cell(st, ok))
	}
	for _, c := range cols {
		st, ok := get(c)
		fmt.Fprintf(b, " %s |", ratioCell(st, ok, v2, v2ok))
	}
	b.WriteString("\n")
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
	if e := s.Env; e != nil {
		fmt.Fprintf(b, "- GOFLAGS of the run: `%s` (go env GOFLAGS when run.sh started); CPUs counted by Go: %d\n", e.GoFlags, e.NumCPU)
	}
	if s.Config.runtimeOnly() {
		fmt.Fprintf(b, "- Benchmark rounds: %d, benchtime: %s (runtime only)\n\n", s.Config.BenchCount, or(s.Config.Benchtime))
	} else {
		fmt.Fprintf(b, "- Runs: %d, services: %d, benchmark rounds: %d, benchtime: %s\n\n",
			s.Config.Runs, s.Config.Services, s.Config.BenchCount, or(s.Config.Benchtime))
	}

	b.WriteString("### Versions\n\n| component | version |\n|---|---|\n")
	keys := slices.Clone(versionOrder)
	for _, k := range slices.Sorted(maps.Keys(s.Versions)) {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		if v, ok := s.Versions[k]; ok {
			fmt.Fprintf(b, "| %s | %s |\n", k, or(v))
		}
	}

	if !s.Config.runtimeOnly() {
		writeReference(b, s, sizes)
	}

	b.WriteString("\n### Steps\n\n")
	b.WriteString("Load is the 1m, 5m and 15m load average. A step that waited at its load gate shows how long, and whether the wait timed out: it then ran under the load shown. After a resume the table lists every attempt of a step; the notes about timed-out gates and failed steps use only the last attempt.\n\n")
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
	if failed := failedSteps(s.Steps); len(failed) > 0 {
		fmt.Fprintf(b, "**Failed steps.** The last record of these steps has a non-zero exit: %s.\n\n", strings.Join(failed, "; "))
	}
}

// writeReference writes the reference build's table.
func writeReference(b *strings.Builder, s summary, sizes []int) {
	b.WriteString("\n### Reference build\n\n")
	b.WriteString("`ref_cpu_s`: the CPU seconds of a fixed reference build, taken around each measurement. It measures the machine's load, not an ORM; the `_x` metrics divide by it. Each cell is `median (worst)`.\n\n")
	b.WriteString("| size | velox | veloxe | ent |\n|---|---|---|---|\n")
	for _, n := range sizes {
		k := strconv.Itoa(n)
		v2, ok1 := s.Reference["velox"][k]
		old, ok2 := s.Reference["veloxe"][k]
		ent, ok3 := s.Reference["ent"][k]
		fmt.Fprintf(b, "| n=%s | %s | %s | %s |\n", k, cell(v2, ok1), cell(old, ok2), cell(ent, ok3))
	}
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
