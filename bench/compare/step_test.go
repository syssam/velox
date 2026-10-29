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
	if res.TimedOut {
		t.Fatalf("got %+v, want no timeout", res)
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

func TestStepTimeout(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	logPath := filepath.Join(dir, "steps.jsonl")
	argv := []string{"sh", "-c", "sleep 5 & echo $! > " + pidFile + "; wait"}
	start := time.Now()
	rec, err := runStep(context.Background(), "timeout", nil, 50*time.Millisecond, logPath, argv)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Exit != -1 {
		t.Fatalf("Exit = %d, want -1", rec.Exit)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the step took %v", d)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
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
