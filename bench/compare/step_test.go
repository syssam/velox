package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseUptime(t *testing.T) {
	got, err := parseUptime("12:00  up 3 days,  2:11, 4 users,  load averages: 29.60 26.68 25.90")
	if err != nil || got != (load{29.60, 26.68, 25.90}) {
		t.Fatalf("macOS: got %+v, %v", got, err)
	}
	got, err = parseUptime(" 12:00:00 up 3 days,  1 user,  load average: 1.00, 2.00, 3.00")
	if err != nil || got != (load{1, 2, 3}) {
		t.Fatalf("Linux: got %+v, %v", got, err)
	}
	if _, err := parseUptime("up 3 days"); err == nil {
		t.Fatal("a line without load averages must be an error")
	}
}

func TestWaitLoadTimesOut(t *testing.T) {
	read := func() (load, error) { return load{One: 20}, nil }
	res, err := waitLoad(context.Background(), gate{Max: 12, Wait: 30 * time.Millisecond, Poll: 5 * time.Millisecond}, read)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.WaitedS < 0.03 {
		t.Fatalf("got %+v, want TimedOut after at least 30ms", res)
	}
}

func TestWaitLoadPasses(t *testing.T) {
	loads := []float64{20, 5}
	read := func() (load, error) {
		l := load{One: loads[0]}
		if len(loads) > 1 {
			loads = loads[1:]
		}
		return l, nil
	}
	res, err := waitLoad(context.Background(), gate{Max: 12, Wait: time.Second, Poll: 5 * time.Millisecond}, read)
	if err != nil {
		t.Fatal(err)
	}
	if res.TimedOut || res.WaitedS <= 0 {
		t.Fatalf("got %+v, want a pass after a second poll", res)
	}
}

func TestStepLogsNoSecret(t *testing.T) {
	t.Setenv("VELOX_COMPARE_POSTGRES", "postgres://u:hunter2@h/db")
	logPath := filepath.Join(t.TempDir(), "steps.jsonl")
	rec, err := runStep(context.Background(), "exit3", nil, time.Minute, logPath, []string{"sh", "-c", "exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Exit != 3 {
		t.Fatalf("Exit = %d, want 3", rec.Exit)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("log has %d lines, want 1", len(lines))
	}
	var parsed stepRecord
	if err := json.Unmarshal(lines[0], &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Name != "exit3" || parsed.Exit != 3 {
		t.Fatalf("parsed %+v", parsed)
	}
	if bytes.Contains(data, []byte("hunter2")) {
		t.Fatal("the log holds the secret")
	}
}

// spawnScript is a command that backgrounds a sleep, writes its pid to
// pidFile and waits: the sleep is a child that only a group kill reaches.
func spawnScript(pidFile string) []string {
	return []string{"sh", "-c", "sleep 5 & echo $! > " + pidFile + "; wait"}
}

// readPid waits for pidFile to hold a pid.
func readPid(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		raw, err := os.ReadFile(pidFile)
		if pid, aerr := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && aerr == nil {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pid in %s", pidFile)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// requireGone fails unless the process is gone within a second.
func requireGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the backgrounded sleep (pid %d) outlived the step", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStepTimeout(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	start := time.Now()
	rec, err := runStep(context.Background(), "timeout", nil, 500*time.Millisecond, filepath.Join(dir, "steps.jsonl"), spawnScript(pidFile))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Exit != -1 {
		t.Fatalf("Exit = %d, want -1", rec.Exit)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the step took %v", d)
	}
	requireGone(t, readPid(t, pidFile))
}

func TestStepCancel(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	logPath := filepath.Join(dir, "steps.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// Cancel when the pid file appears, or after 2s, whichever is first.
		for range 200 {
			if raw, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(raw), "\n") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	rec, err := runStep(ctx, "cancel", nil, time.Minute, logPath, spawnScript(pidFile))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Exit != -1 {
		t.Fatalf("Exit = %d, want -1", rec.Exit)
	}
	requireGone(t, readPid(t, pidFile))
	data, err := os.ReadFile(logPath)
	if err != nil || bytes.Count(data, []byte("\n")) != 1 {
		t.Fatalf("want one logged line, got %q (%v)", data, err)
	}
}

func TestStepNormalExitKillsNothing(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	argv := []string{"sh", "-c", "sleep 2 >/dev/null 2>&1 & echo $! > " + pidFile}
	rec, err := runStep(context.Background(), "bg", nil, time.Minute, filepath.Join(dir, "steps.jsonl"), argv)
	if err != nil || rec.Exit != 0 {
		t.Fatalf("exit %d, %v", rec.Exit, err)
	}
	pid := readPid(t, pidFile)
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("a normal exit killed the command's background child: %v", err)
	}
}

func TestStepBadLogFailsBeforeRunning(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	_, err := runStep(context.Background(), "x", nil, time.Minute, filepath.Join(dir, "no", "such", "steps.jsonl"), []string{"touch", marker})
	if err == nil {
		t.Fatal("want an error for an unwritable log")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran although the log could not be opened")
	}
}

func TestStepGateKey(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "steps.jsonl")
	g := &gate{Max: 1e9, Wait: time.Minute, Poll: time.Millisecond}
	if _, err := runStep(context.Background(), "gated", g, time.Minute, logPath, []string{"true"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var rec stepRecord
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Gate == nil || rec.Gate.Max != 1e9 || rec.Gate.TimedOut {
		t.Fatalf("gate = %+v", rec.Gate)
	}
}

func TestStepMainExitCodes(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "steps.jsonl")
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"-name", "a", "-log", logPath, "--", "sh", "-c", "exit 3"}, 3},
		{[]string{"-name", "b", "-log", logPath, "--", "true"}, 0},
		{[]string{"-name", "c", "-log", logPath, "-timeout", "50ms", "--", "sleep", "5"}, 1},
		{[]string{"-name", "d", "-log", logPath}, 2},
		{[]string{"-log", logPath, "--", "true"}, 2},
	} {
		if got := stepMain(tc.args); got != tc.want {
			t.Errorf("stepMain(%q) = %d, want %d", tc.args, got, tc.want)
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"gate"`)) {
		t.Fatal("-gate 0 must mean no gate")
	}
}

func TestStepNoGate(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "steps.jsonl")
	if _, err := runStep(context.Background(), "true", nil, time.Minute, logPath, []string{"true"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(data), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["gate"]; ok {
		t.Fatalf("the line has a gate key: %s", data)
	}
}
