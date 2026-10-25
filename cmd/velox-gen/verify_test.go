package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestVerifySweepsOldDirs: verify removes the directory a killed verify left
// behind an hour ago or more, and leaves a younger one, which may be another
// verify's.
func TestVerifySweepsOldDirs(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	old, young := filepath.Join(dir, ".velox-verify-old"), filepath.Join(dir, ".velox-verify-new")
	for _, d := range []string{old, young} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	then := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, then, then); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := velox(t, dir, "verify", "-out", "gen", "./schema"); code != 0 {
		t.Fatalf("verify: %d %q %q", code, out, errs)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the old directory is still there: %v", err)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("the young directory is gone: %v", err)
	}
}

// TestVerifyExplainsBuildFailure: when the check program does not build,
// verify says after go's errors that the generated package may be stale or
// edited.
func TestVerifyExplainsBuildFailure(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	p := filepath.Join(dir, "gen", "velox.go")
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(src, "\nvar _ = undefinedName\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	const why = "velox-gen verify: the check program did not build; the generated package may be stale or edited by hand: run velox-gen, then verify again\n"
	code, _, errs := velox(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || !strings.Contains(errs, "undefinedName") || !strings.HasSuffix(errs, why) {
		t.Errorf("%d %q", code, errs)
	}
	noVerifyDirs(t, dir)
}

// TestRunProgramCanceled: a canceled context, as on an interrupt, stops the
// program and removes its directory.
func TestRunProgramCanceled(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errs bytes.Buffer
	src := []byte("package main\n\nfunc main() {}\n")
	if code := runProgram(ctx, dir, src, os.Environ(), &out, &errs); code != interruptedCode || !strings.Contains(errs.String(), "velox-gen verify: interrupted\n") {
		t.Errorf("%d %q %q", code, out.String(), errs.String())
	}
	noVerifyDirs(t, dir)
}

// cancelOn is a writer that cancels when what it is written holds word. Its
// buffer is a field, not embedded, so that io.Copy calls Write, not the
// buffer's ReadFrom.
type cancelOn struct {
	buf    bytes.Buffer
	word   string
	cancel func()
}

func (w *cancelOn) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), w.word) {
		w.cancel()
	}
	return n, err
}

func (w *cancelOn) String() string { return w.buf.String() }

// TestRunProgramInterrupts: an interrupt reaches the program as os.Interrupt,
// not a kill, so it can clean up, and verify exits 130.
func TestRunProgramInterrupts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot send os.Interrupt")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module m\n\ngo 1.27.0\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"os/signal\"\n)\n\nfunc main() {\n" +
		"\tc := make(chan os.Signal, 1)\n\tsignal.Notify(c, os.Interrupt)\n\tfmt.Println(\"ready\")\n\t<-c\n\tfmt.Println(\"cleaned up\")\n}\n")
	out := &cancelOn{word: "ready", cancel: cancel}
	var errs bytes.Buffer
	if code := runProgram(ctx, dir, src, os.Environ(), out, &errs); code != interruptedCode || !strings.Contains(out.String(), "cleaned up") || errs.String() != "velox-gen verify: interrupted\n" {
		t.Errorf("%d %q %q", code, out.String(), errs.String())
	}
	noVerifyDirs(t, dir)
}

// TestRunProgramKilledBySignal: a program that dies of a signal, with no
// interrupt, is not interrupted: verify says how it died.
func TestRunProgramKilledBySignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no signals on Windows")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module m\n\ngo 1.27.0\n")
	src := []byte("package main\n\nimport \"syscall\"\n\nfunc main() { _ = syscall.Kill(syscall.Getpid(), syscall.SIGKILL); select {} }\n")
	defer func(d time.Duration) { interruptGrace = d }(interruptGrace)
	interruptGrace = 100 * time.Millisecond
	var out, errs bytes.Buffer
	if code := runProgram(context.Background(), dir, src, os.Environ(), &out, &errs); code != 1 || errs.String() != "velox-gen verify: signal: killed\n" {
		t.Errorf("%d %q", code, errs.String())
	}
	noVerifyDirs(t, dir)
}

// TestInterrupted: a command is interrupted when ctx is done, or when it died
// of a signal and ctx is done within the grace, as when a terminal's Ctrl-C
// reaches it before velox-gen's handler; an exit of its own is not.
func TestInterrupted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no signals on Windows")
	}
	killed := exec.Command("sh", "-c", "kill -9 $$").Run()
	failed := exec.Command("sh", "-c", "exit 1").Run()
	if killed == nil || failed == nil {
		t.Fatalf("%v %v", killed, failed)
	}
	defer func(d time.Duration) { interruptGrace = d }(interruptGrace)
	interruptGrace = 100 * time.Millisecond
	done, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"done", done, nil, true},
		{"done, failed", done, failed, true},
		{"killed", context.Background(), killed, false},
		{"failed", context.Background(), failed, false},
		{"ok", context.Background(), nil, false},
	} {
		if got := interrupted(c.ctx, c.err); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
	interruptGrace = time.Minute
	soon, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	if !interrupted(soon, killed) {
		t.Error("killed, then done: not interrupted")
	}
}
