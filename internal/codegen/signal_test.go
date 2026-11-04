//go:build unix

package codegen

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// TestLoadSignalChild is the process TestLoadSignalRemovesTempDir signals:
// it loads the schema package of the module VELOX_LOAD_SIGNAL_DIR names.
func TestLoadSignalChild(t *testing.T) {
	dir := os.Getenv("VELOX_LOAD_SIGNAL_DIR")
	if dir == "" {
		t.Skip("run by TestLoadSignalRemovesTempDir")
	}
	_, err := Load(dir, "./schema", offline())
	t.Fatalf("Load returned %v; the signal should have ended the process", err)
}

// A SIGTERM or SIGINT while velox-gen loads the schema, as ci-local's
// fail-fast sends, removes Load's .velox-gen- directory, and the process
// dies of the signal. The schema's init holds the load until the test
// releases it.
func TestLoadSignalRemovesTempDir(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			ready, release := filepath.Join(t.TempDir(), "ready"), filepath.Join(t.TempDir(), "release")
			dir := module(t, map[string]string{
				"schema/user.go": userSchema,
				"schema/hold.go": "package schema\n\nimport (\n\t\"os\"\n\t\"time\"\n)\n\nfunc init() {\n" +
					"\t_ = os.WriteFile(" + strconv.Quote(ready) + ", nil, 0o644)\n" +
					"\tfor range 1200 {\n\t\tif _, err := os.Stat(" + strconv.Quote(release) + "); err == nil {\n\t\t\treturn\n\t\t}\n" +
					"\t\ttime.Sleep(50 * time.Millisecond)\n\t}\n}\n",
			})
			// The loader program, which the signal does not reach, ends.
			t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o644) })
			cmd := exec.Command(os.Args[0], "-test.run", "^TestLoadSignalChild$", "-test.count=1")
			cmd.Env = append(os.Environ(), "VELOX_LOAD_SIGNAL_DIR="+dir)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Minute)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatal("the schema's init never ran")
				}
				time.Sleep(50 * time.Millisecond)
			}
			if left, _ := filepath.Glob(filepath.Join(dir, ".velox-gen-*")); len(left) != 1 {
				t.Fatalf("while loading: %v, want one directory", left)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err := cmd.Wait()
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("the child exited with %v", err)
			}
			ws, ok := ee.Sys().(syscall.WaitStatus)
			if died := ok && (ws.Signaled() && ws.Signal() == sig || ws.ExitStatus() == 130); !died {
				t.Errorf("the child ended with %v, want the signal %v", err, sig)
			}
			if left, _ := filepath.Glob(filepath.Join(dir, ".velox-gen-*")); len(left) != 0 {
				t.Errorf("left %v", left)
			}
		})
	}
}
