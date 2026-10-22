// Package tempmod writes temporary Go modules that build against this
// repository, offline, for velox's build-cost tests and bench/scale.
package tempmod

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Write creates a module example.com/tmp in dir that requires velox2 from
// repo (by replace), with the given files (path → content), and resolves its
// requirements from the module cache only. files is not changed.
func Write(dir, repo string, files map[string]string) error {
	mod := fmt.Sprintf("module example.com/tmp\n\ngo 1.27.0\n\nrequire github.com/syssam/velox2 v0.0.0\n\nreplace github.com/syssam/velox2 => %s\n", repo)
	sum, err := os.ReadFile(filepath.Join(repo, "go.sum"))
	if err != nil {
		return err
	}
	all := maps.Clone(files)
	if all == nil {
		all = map[string]string{}
	}
	all["go.mod"], all["go.sum"] = mod, string(sum)
	for name, content := range all {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	_, err = Go(dir, "mod", "tidy")
	return err
}

// Go runs the go command in dir, offline, and returns its combined output.
// It runs "go" from PATH: go test, go run, go generate and go tool put their
// own GOROOT/bin first on a child's PATH (go help test), so this is the
// toolchain that runs the caller.
func Go(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("go %v: %w\n%s%s", args, err, out.String(), Hint(out.String()))
	}
	return out.String(), err
}

// offlineMessage is what the go command prints when a module is missing from
// the module cache and GOPROXY=off forbids fetching it.
const offlineMessage = "module lookup disabled by GOPROXY=off"

// Hint explains the output of a go command that ran with GOPROXY=off: when it
// failed on a module the cache lacks, it says how to fill the cache, and
// otherwise it is empty. The test modules take their dependencies from the
// cache and fetch nothing, so a pruned cache fails them with a bare lookup
// error.
func Hint(out string) string {
	if !strings.Contains(out, offlineMessage) {
		return ""
	}
	return "\nthe module cache lacks a dependency of the test modules (it is read offline): run go mod download in the repository's root module and in contrib/graphql\n"
}
