package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
	if code := runProgram(ctx, dir, src, os.Environ(), &out, &errs); code != 1 || !strings.Contains(errs.String(), "velox-gen verify: interrupted\n") {
		t.Errorf("%d %q %q", code, out.String(), errs.String())
	}
	noVerifyDirs(t, dir)
}
