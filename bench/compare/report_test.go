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

func TestReportTables(t *testing.T) {
	s := fixtureSummary(t)
	rep := fixtureReport(t, s)
	for _, want := range []string{
		"## Machine and load",
		"Fixture CPU",
		"v0.14.6",
		"| n=100 | 12 (13.5) | 24 (27) | 10 (11.25) | 2.00 | 0.83 |", // cold_cpu_s
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
	if !strings.Contains(rep, "| n=100 | 12 (13.5) | 24 (27) | - | 2.00 | - |") {
		t.Errorf("a missing ORM must show as -:\n%s", rep)
	}
}
