// Package buildcost measures what a build costs: the CPU time of a command and
// the children it reaped, the peak memory of the linker (through a -toolexec
// wrapper), and a reference build of a small module to compare both with.
// bench/scale and bench/compare share it.
package buildcost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LinkMemEnv is the environment variable that names the file the wrapper
// appends each link's peak memory to.
const LinkMemEnv = "VELOX_LINKMEM"

// CPUSeconds is the user plus system CPU time of cmd, which has been waited
// for. The rusage behind it counts the children cmd reaped: under go build,
// the compiler, the linker and the -toolexec wrapper that runs them.
func CPUSeconds(cmd *exec.Cmd) float64 {
	return (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Seconds()
}

// referenceSrc is the reference module's program: a small binary whose cold
// build compiles the standard library's net/http and what it imports.
const referenceSrc = `package main

import (
	"fmt"
	"net/http"
)

func main() { fmt.Println(http.StatusOK) }
`

// WriteReference writes the reference module, ref, with its own go.mod, in dir.
func WriteReference(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module ref\n\ngo 1.27.0\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "main.go"), []byte(referenceSrc), 0o644)
}

// BuildReference builds the reference module in dir cold, with the fresh build
// cache cache, under the same -toolexec wrapper as the application, and
// returns its CPU and wall seconds. Its linker's peak memory goes to a file of
// its own, not the application's.
func BuildReference(dir, cache, wrapper string) (cpu, wall float64, err error) {
	cmd := exec.Command("go", "build", "-toolexec", wrapper, "-o", filepath.Join(dir, "ref"), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOCACHE="+cache, "GOFLAGS=", "GOPROXY=off", "GOWORK=off", LinkMemEnv+"="+filepath.Join(dir, "linkmem.txt"))
	start := time.Now()
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, 0, fmt.Errorf("building the reference: %v\n%s", err, out)
	}
	return CPUSeconds(cmd), time.Since(start).Seconds(), nil
}

// BuildWrapper builds the -toolexec program that runs each tool and, for the
// linker, appends its peak memory in bytes to $VELOX_LINKMEM.
func BuildWrapper(path string) error {
	dir, err := os.MkdirTemp("", "velox-linkmem-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	src := `package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func main() {
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	if cmd.ProcessState != nil && strings.TrimSuffix(filepath.Base(os.Args[1]), ".exe") == "link" {
		if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			peak := int64(ru.Maxrss)
			if runtime.GOOS == "linux" {
				peak *= 1024 // kilobytes on Linux, bytes on macOS
			}
			if f, ferr := os.OpenFile(os.Getenv("VELOX_LINKMEM"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); ferr == nil {
				fmt.Fprintln(f, peak)
				f.Close()
			}
		}
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "linkmem:", err) // the tool did not start
		os.Exit(1)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module linkmem\n\ngo 1.27.0\n"), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-o", path, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("building the toolexec wrapper: %v\n%s", err, out)
	}
	return nil
}

// PeakMB is the largest peak memory in file, in MB: the wrapper appends one line
// of bytes per link, and a line that is not a number is skipped.
func PeakMB(file string) (float64, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return 0, fmt.Errorf("no linker measurements: %w", err)
	}
	var peak int64
	for _, line := range strings.Fields(string(b)) {
		if v, err := strconv.ParseInt(line, 10, 64); err == nil && v > peak {
			peak = v
		}
	}
	return float64(peak) / (1 << 20), nil
}
