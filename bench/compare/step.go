package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// load is the system load average, as uptime prints it.
type load struct {
	One     float64 `json:"1m"`
	Five    float64 `json:"5m"`
	Fifteen float64 `json:"15m"`
}

// loadRE matches both "load averages: 29.60 26.68 25.90" (macOS) and
// "load average: 1.00, 2.00, 3.00" (Linux).
var loadRE = regexp.MustCompile(`load averages?:\s*([0-9.]+),?\s+([0-9.]+),?\s+([0-9.]+)`)

// parseUptime reads the three load averages from an uptime line.
func parseUptime(out string) (load, error) {
	m := loadRE.FindStringSubmatch(out)
	if m == nil {
		return load{}, fmt.Errorf("no load averages in %q", strings.TrimSpace(out))
	}
	var v [3]float64
	for i := range v {
		f, err := strconv.ParseFloat(m[i+1], 64)
		if err != nil {
			return load{}, fmt.Errorf("load average %q: %w", m[i+1], err)
		}
		v[i] = f
	}
	return load{One: v[0], Five: v[1], Fifteen: v[2]}, nil
}

// readUptime runs uptime and returns its raw line and the parsed load. It
// runs in the C locale, so that the decimals have a point.
func readUptime() (string, load, error) {
	cmd := exec.CommandContext(context.Background(), "uptime")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return "", load{}, fmt.Errorf("uptime: %w", err)
	}
	raw := strings.TrimSpace(string(out))
	l, err := parseUptime(raw)
	return raw, l, err
}

// gate holds a step back until the one-minute load is below Max, for at
// most Wait. It polls every Poll.
type gate struct {
	Max        float64
	Wait, Poll time.Duration
}

// gateResult is what the gate did, as the step record shows it.
type gateResult struct {
	Max      float64 `json:"max"`
	WaitedS  float64 `json:"waited_s"`
	TimedOut bool    `json:"timed_out"`
}

// waitLoad returns once read reports One < g.Max, or once g.Wait has passed
// (TimedOut). It polls every g.Poll, and never sleeps past g.Wait.
func waitLoad(ctx context.Context, g gate, read func() (load, error)) (gateResult, error) {
	start := time.Now()
	res := gateResult{Max: g.Max}
	for {
		l, err := read()
		if err != nil {
			return res, err
		}
		elapsed := time.Since(start)
		res.WaitedS = elapsed.Seconds()
		if l.One < g.Max {
			return res, nil
		}
		if elapsed >= g.Wait {
			res.TimedOut = true
			return res, nil
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(min(g.Poll, g.Wait-elapsed)):
		}
	}
}

// stepRecord is one line of the step log.
type stepRecord struct {
	Name         string      `json:"name"`
	Argv         []string    `json:"argv"`
	Start        string      `json:"start"`
	WallS        float64     `json:"wall_s"`
	Exit         int         `json:"exit"`
	LoadBefore   load        `json:"load_before"`
	LoadAfter    load        `json:"load_after"`
	UptimeBefore string      `json:"uptime_before"`
	UptimeAfter  string      `json:"uptime_after"`
	Gate         *gateResult `json:"gate,omitempty"`
}

// runStep runs argv, waiting first at g (nil: no gate), and appends one JSON
// line to logPath. The log is opened before anything runs, so a bad path
// fails at once. The command's stdout and stderr pass through. The record
// holds argv as given and no environment: pass a secret, such as a DSN, in the
// environment, never as an argument.
//
// The command leads its own process group. A timeout, or a cancelled ctx,
// kills the whole group (a go command's compile and link processes too) and
// records exit -1; so does a command that cannot start. After a normal exit
// nothing is killed. The record is written whenever the command ran, even if
// the uptime after it cannot be read.
func runStep(ctx context.Context, name string, g *gate, timeout time.Duration, logPath string, argv []string) (stepRecord, error) {
	rec := stepRecord{Name: name, Argv: argv}
	if len(argv) == 0 {
		return rec, errors.New("step: no command")
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return rec, err
	}
	defer func() { _ = f.Close() }()
	if g != nil {
		res, err := waitLoad(ctx, *g, func() (load, error) {
			_, l, err := readUptime()
			return l, err
		})
		if err != nil {
			return rec, err
		}
		rec.Gate = &res
	}
	if rec.UptimeBefore, rec.LoadBefore, err = readUptime(); err != nil {
		return rec, err
	}
	start := time.Now()
	rec.Start = start.UTC().Format(time.RFC3339)

	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "compare step: %v\n", err)
		rec.Exit = -1
	} else {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case werr := <-done:
			rec.Exit = exitCode(werr)
		case <-timer.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
			rec.Exit = -1
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
			rec.Exit = -1
		}
	}
	rec.WallS = time.Since(start).Seconds()
	if rec.UptimeAfter, rec.LoadAfter, err = readUptime(); err != nil {
		fmt.Fprintf(os.Stderr, "compare step: load after the command: %v\n", err)
		rec.UptimeAfter, rec.LoadAfter = "", load{}
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return rec, err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return rec, err
	}
	return rec, f.Close()
}

// exitCode is the command's exit code; -1 when it died by a signal.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	return -1
}

// stepMain is the step subcommand: compare step -name N -log FILE [-gate 12]
// [-gate-wait 60m] [-timeout 120m] -- argv... It returns the command's exit code.
func stepMain(args []string) int {
	fs := flag.NewFlagSet("step", flag.ContinueOnError)
	name := fs.String("name", "", "the step's name")
	logPath := fs.String("log", "", "the JSON-lines log to append to")
	gateMax := fs.Float64("gate", 0, "wait for the one-minute load to fall below this (0: no gate)")
	gateWait := fs.Duration("gate-wait", 60*time.Minute, "give up waiting at the gate after this long")
	timeout := fs.Duration("timeout", 120*time.Minute, "kill the command's process group after this long")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || *logPath == "" || fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: compare step -name N -log FILE [-gate 12] [-gate-wait 60m] [-timeout 120m] -- argv...")
		return 2
	}
	var g *gate
	if *gateMax > 0 {
		g = &gate{Max: *gateMax, Wait: *gateWait, Poll: 10 * time.Second}
	}
	// The command leads its own process group, so a signal to compare does
	// not reach it: on SIGINT or SIGTERM runStep kills the group itself.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	rec, err := runStep(ctx, *name, g, *timeout, *logPath, fs.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "compare step: %v\n", err)
		if rec.Exit == 0 {
			return 1
		}
	}
	if rec.Exit < 0 {
		return 1
	}
	return rec.Exit
}
