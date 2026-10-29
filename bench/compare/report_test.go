package main

import (
	"strings"
	"testing"
)

func fixtureReport(t *testing.T, s summary) string {
	t.Helper()
	var b strings.Builder
	if err := writeReport(&b, s); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestReportShowsTimedOutGate(t *testing.T) {
	rep := fixtureReport(t, fixtureSummary(t))
	var timed, other int
	for _, line := range strings.Split(rep, "\n") {
		switch {
		case strings.Contains(line, "build-ent-n100-r1"):
			timed++
			if !strings.Contains(line, "timed out") || !strings.Contains(line, "3600") {
				t.Errorf("the timed-out step's row lacks the gate: %q", line)
			}
		case strings.Contains(line, "build-velox2-n100-r1"), strings.Contains(line, "bench-sqlite-velox2-1"):
			other++
			if strings.Contains(line, "timed out") {
				t.Errorf("a step that did not time out says so: %q", line)
			}
		}
	}
	if timed != 1 || other != 2 {
		t.Errorf("steps table rows: %d timed out, %d others; want 1 and 2\n%s", timed, other, rep)
	}
}

// section is the report from heading to the next "## " heading.
func section(rep, heading string) string {
	i := strings.Index(rep, heading)
	if i < 0 {
		return ""
	}
	rest := rep[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return rest[:j]
	}
	return rest
}

func TestReportMarksTimedOutGates(t *testing.T) {
	s := fixtureSummary(t)
	s.Steps = append(s.Steps, stepRecord{
		Name: "gate-sqlite-2", LoadBefore: load{One: 31.5},
		Gate: &gateResult{Max: 12, WaitedS: 600, TimedOut: true},
	}, stepRecord{
		Name: "gate-postgres-1", Gate: &gateResult{Max: 12, WaitedS: 5},
	})
	rep := fixtureReport(t, s)
	sqlite, postgres := section(rep, "## Runtime: sqlite"), section(rep, "## Runtime: postgres")
	if !strings.Contains(sqlite, "round 2 (gate waited 600 s, load 31.5 before)") || strings.Contains(postgres, "Timed-out") {
		t.Errorf("a timed-out bench gate is not marked under its dialect's tables:\nsqlite: %s\npostgres: %s", sqlite, postgres)
	}
	build := section(rep, "## Build cost")
	if !strings.Contains(build, "ent n=100 run 1 (gate waited 3600 s, load 29.6 before)") {
		t.Errorf("a timed-out build gate is not marked under the build tables:\n%s", build)
	}
	if strings.Count(build, "Timed-out load gate") != 1 {
		t.Error("the build gate note must appear once")
	}
}

func TestReportLegendAndPrecision(t *testing.T) {
	rep := fixtureReport(t, fixtureSummary(t))
	if i, j := strings.Index(rep, "other ORM ÷ velox2"), strings.Index(rep, "## Machine and load"); i < 0 || i > j {
		t.Error("the legend on the ratio columns is missing or comes after the first table")
	}
	// A small loss does not read as a tie.
	s := fixtureSummary(t)
	s.Build["ent"]["100"]["cold_cpu_s"] = newStat([]float64{11.97}, true)
	rep = fixtureReport(t, s)
	if !strings.Contains(rep, "ent 11.97 vs velox2 12 (ent ÷ velox2 = 0.998, lower is better)") {
		t.Errorf("unexpected loss line:\n%s", section(rep, "## Where velox2 loses"))
	}
	// Three digits would print both as 12 and the ratio as 1: more are shown.
	s.Build["ent"]["100"]["cold_cpu_s"] = newStat([]float64{11.9999}, true)
	rep = fixtureReport(t, s)
	if !strings.Contains(rep, "ent 11.9999 vs velox2 12 (ent ÷ velox2 = 0.99999, lower is better)") {
		t.Errorf("the loss reads as a tie:\n%s", section(rep, "## Where velox2 loses"))
	}
	for in, want := range map[float64]string{12: "12", 11.25: "11.2", 0.8333333: "0.833", 1.234e-06: "1.23e-06", 123456.7: "123457"} {
		if got := fmtNum(in); got != want {
			t.Errorf("fmtNum(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestReportIncomplete(t *testing.T) {
	s := fixtureSummary(t)
	s.Build["ent"]["100"]["cold_cpu_s"] = newStat([]float64{12, 14}, true) // no loss left
	s.Missing = []string{"build ent n=100 run 3", "bench sqlite ent round 2"}
	rep := fixtureReport(t, s)
	if !strings.HasPrefix(rep[strings.Index(rep, "## "):], "## Incomplete results") {
		t.Error("the report must start with the incomplete results")
	}
	for _, m := range s.Missing {
		if !strings.Contains(rep, "- "+m+"\n") {
			t.Errorf("the report does not list %q", m)
		}
	}
	if strings.Contains(rep, "velox2 is best on every metric measured.") {
		t.Error("an incomplete run claims velox2 is best on every metric")
	}
	if !strings.Contains(rep, "results are incomplete") {
		t.Error("the loss section does not say the results are incomplete")
	}
}

func TestReportTables(t *testing.T) {
	s := fixtureSummary(t)
	rep := fixtureReport(t, s)
	for _, want := range []string{
		"## Machine and load",
		"Fixture CPU",
		"v0.14.6",
		"| n=100 | 12 (13.5) | 24 (27) | 10 (11.2) | 2 | 0.833 |", // cold_cpu_s
		"## Where velox2 loses",
		"cold_cpu_s",
		"no noise threshold",
		"sec/op",
		"InsertBulk100",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("the report lacks %q", want)
		}
	}
	// The tables come in buildMetrics order: the CPU _x tables first.
	last := -1
	for _, m := range buildMetrics {
		i := strings.Index(rep, "### "+m.Key)
		if i < 0 || i < last {
			t.Fatalf("table %s is missing or out of order (at %d, after %d)", m.Key, i, last)
		}
		last = i
	}
	// The reference CPU is in the machine section only, never a metric table.
	if strings.Contains(rep, "### ref_cpu_s") {
		t.Error("ref_cpu_s has a metric table")
	}
	if i, j := strings.Index(rep, "ref_cpu_s"), strings.Index(rep, "### codegen_x"); i < 0 || i > j {
		t.Error("the reference CPU is not shown in the machine and load section")
	}
	// The report names cold_cpu_s as the one loss, with ent as the winner.
	tail := rep[strings.Index(rep, "## Where velox2 loses"):]
	if n := strings.Count(tail, "\n- "); n != 1 || !strings.Contains(tail, "ent 10 vs velox2 12") {
		t.Errorf("losses section: %q", tail)
	}
}

func TestReportBestOnEveryMetric(t *testing.T) {
	s := fixtureSummary(t)
	s.Build["ent"]["100"]["cold_cpu_s"] = newStat([]float64{12, 14}, true)
	rep := fixtureReport(t, s)
	if !strings.Contains(rep, "velox2 is best on every metric measured.") {
		t.Errorf("no best-everywhere line:\n%s", rep[strings.Index(rep, "## Where velox2 loses"):])
	}
}

func TestReportMissingORM(t *testing.T) {
	s := fixtureSummary(t)
	delete(s.Build, "ent")
	delete(s.Reference, "ent")
	rep := fixtureReport(t, s)
	if !strings.Contains(rep, "| n=100 | 12 (13.5) | 24 (27) | - | 2 | - |") {
		t.Errorf("a missing ORM must show as -:\n%s", rep)
	}
}

func TestReportInsertBulkFootnote(t *testing.T) {
	rep := fixtureReport(t, fixtureSummary(t))
	for _, d := range []string{"sqlite", "postgres"} {
		sec := section(rep, "## Runtime: "+d)
		if n := strings.Count(sec, "| InsertBulk100 ¹ |"); n != len(runtimeUnits) {
			t.Errorf("%s: %d InsertBulk100 rows are marked, want %d", d, n, len(runtimeUnits))
		}
		if n := strings.Count(sec, insertBulkNote); n != len(runtimeUnits) {
			t.Errorf("%s: %d footnotes, want one under each of %d unit tables", d, n, len(runtimeUnits))
		}
		for _, other := range []string{"InsertOne", "GetByID", "Count"} {
			if strings.Contains(sec, "| "+other+" ¹") {
				t.Errorf("%s: %s carries the footnote", d, other)
			}
		}
	}
	for _, want := range []string{"reads no keys back", "RETURNING id", "100 entities"} {
		if !strings.Contains(insertBulkNote, want) {
			t.Errorf("the footnote lacks %q", want)
		}
	}
	// Each footnote follows its own table, before the next heading.
	for _, sec := range strings.Split(section(rep, "## Runtime: sqlite"), "### ")[1:] {
		if !strings.Contains(sec, "| InsertBulk100 ¹ |") || !strings.Contains(sec, insertBulkNote) {
			t.Errorf("a unit table lacks the marker or its footnote:\n%s", sec)
		}
	}
}

func TestReportUsesLastRecordPerStep(t *testing.T) {
	s := fixtureSummary(t)
	// The fixture's ent build timed out its gate. After a resume, the step ran
	// again and the gate passed; a bench step failed once, then succeeded; and
	// one step's last attempt failed.
	s.Steps = append(s.Steps,
		stepRecord{Name: "build-ent-n100-r1", Gate: &gateResult{Max: 12, WaitedS: 1}},
		stepRecord{Name: "gate-sqlite-1", Exit: 1, Gate: &gateResult{Max: 12, WaitedS: 600, TimedOut: true}, LoadBefore: load{One: 31.5}},
		stepRecord{Name: "gate-sqlite-1", Gate: &gateResult{Max: 12, WaitedS: 2}},
		stepRecord{Name: "bench-sqlite-ent-1", Exit: 2},
		stepRecord{Name: "bench-sqlite-ent-1"},
		stepRecord{Name: "guard-postgres-ent", Exit: 3},
		stepRecord{Name: "gate-postgres-1", Gate: &gateResult{Max: 12, WaitedS: 600, TimedOut: true}, LoadBefore: load{One: 30}},
	)
	rep := fixtureReport(t, s)
	if strings.Contains(section(rep, "## Build cost"), "Timed-out load gate") {
		t.Errorf("a superseded timed-out build gate is reported:\n%s", section(rep, "## Build cost"))
	}
	if sec := section(rep, "## Runtime: sqlite"); strings.Contains(sec, "Timed-out") {
		t.Errorf("a superseded timed-out bench gate is reported:\n%s", sec)
	}
	if sec := section(rep, "## Runtime: postgres"); !strings.Contains(sec, "round 1 (gate waited 600 s, load 30 before)") {
		t.Errorf("the last record's timed-out gate is not reported:\n%s", sec)
	}
	if !strings.Contains(rep, "**Failed steps.** The last record of these steps has a non-zero exit: guard-postgres-ent (exit 3).") {
		t.Errorf("a step whose last exit is non-zero is not called out:\n%s", section(rep, "## Machine and load"))
	}
	if strings.Contains(rep, "bench-sqlite-ent-1 (exit") || strings.Contains(rep, "gate-sqlite-1 (exit") {
		t.Error("a superseded failure is called out")
	}
	// The steps table still lists every attempt.
	if n := strings.Count(rep, "| gate-sqlite-1 |"); n != 2 {
		t.Errorf("%d rows for gate-sqlite-1, want both attempts", n)
	}
	// A clean log has no failure note.
	if strings.Contains(fixtureReport(t, fixtureSummary(t)), "Failed steps") {
		t.Error("the fixture has no failed step")
	}
}

func TestReportShowsEnv(t *testing.T) {
	s := fixtureSummary(t)
	if strings.Contains(fixtureReport(t, s), "GOFLAGS") {
		t.Error("without env, the report names GOFLAGS")
	}
	s.Env = &runEnv{GoFlags: "-p=2", NumCPU: 14}
	rep := section(fixtureReport(t, s), "## Machine and load")
	if !strings.Contains(rep, "GOFLAGS of the run: `-p=2`") || !strings.Contains(rep, "CPUs counted by Go: 14") {
		t.Errorf("the machine section lacks the env:\n%s", rep)
	}
}

func TestReportRerunShare(t *testing.T) {
	rep := fixtureReport(t, fixtureSummary(t))
	sec := section(rep, "### rebuild_rerun_share")
	if !strings.HasPrefix(sec, "\n\nlower is better.") || !strings.Contains(sec, "not a metric") {
		t.Errorf("rebuild_rerun_share has no direction or explanation:\n%s", sec)
	}
	if strings.Contains(rep, "### rebuild_cached") || strings.Contains(rep, "higher is better.") {
		t.Error("rebuild_cached has a table, or a table is higher-is-better")
	}
}

// TestReportRuntimeOnly checks that a runtime-only report has no build tables,
// says so, and shows the velox2pgx column on postgres.
func TestReportRuntimeOnly(t *testing.T) {
	fixtureSummary(t) // replaces the probes
	s, err := summarize(copyFixtureOf(t, "summary-runtime"), map[string]any{"bench_count": 2, "steps": "runtime", "variants": "velox2pgx"})
	if err != nil {
		t.Fatal(err)
	}
	rep := fixtureReport(t, s)
	if strings.Contains(rep, "## Build cost") || strings.Contains(rep, "### Reference build") {
		t.Error("a runtime-only report has build tables")
	}
	if !strings.Contains(rep, "Runtime only: the build steps were not run.") {
		t.Error("the report does not say it is runtime only")
	}
	head := "| benchmark | velox2 | velox2 (OpenPgx) | old velox | ent | velox2 (OpenPgx) ÷ velox2 | old velox ÷ velox2 | ent ÷ velox2 |"
	pg := section(rep, "### sec/op (postgres)")
	if !strings.Contains(pg, head) {
		t.Errorf("the postgres sec/op table lacks the velox2 (OpenPgx) columns:\n%s", pg)
	}
	// The fixture's velox2pgx is 0.8 of velox2's sec/op.
	if !strings.Contains(pg, "| 0.8 | ") {
		t.Errorf("no 0.8 ratio in the postgres sec/op table:\n%s", pg)
	}
	if strings.Contains(section(rep, "## Runtime: sqlite"), "OpenPgx") {
		t.Error("the sqlite tables have the velox2 (OpenPgx) column")
	}
	// A timed-out round gate is still reported without the build tables.
	s.Steps = append(s.Steps, stepRecord{
		Name: "gate-postgres-1", LoadBefore: load{One: 30},
		Gate: &gateResult{Max: 12, WaitedS: 600, TimedOut: true},
	})
	if sec := section(fixtureReport(t, s), "## Runtime: postgres"); !strings.Contains(sec, "round 1 (gate waited 600 s, load 30 before)") {
		t.Errorf("a runtime-only report drops a timed-out round gate:\n%s", sec)
	}
}
