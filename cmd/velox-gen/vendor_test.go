package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/tempmod"
)

// A module that vendors velox-gen as a tool generates under -mod=vendor:
// the loader's program imports schema/load, which go mod vendor copies only
// because velox-gen imports it.
func TestGenerateVendored(t *testing.T) {
	dir := genModuleSerial(t, nil)
	mod := filepath.Join(dir, "go.mod")
	writeFile(t, mod, read(t, mod)+"\ntool github.com/syssam/velox/cmd/velox-gen\n")
	for _, args := range [][]string{{"mod", "tidy"}, {"mod", "vendor"}} {
		cmd := exec.Command("go", args...)
		cmd.Dir, cmd.Env = dir, offlineEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s%s", args, err, out, tempmod.Hint(string(out)))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor", "github.com", "syssam", "velox", "schema", "load")); err != nil {
		t.Fatalf("go mod vendor left out schema/load: %v", err)
	}
	env := append(os.Environ(), "GOFLAGS=-mod=vendor", "GOPROXY=off", "GOTOOLCHAIN=local")
	var out, errs strings.Builder
	if code := run([]string{"./veloxschema"}, dir, env, &out, &errs); code != 0 {
		t.Fatalf("generate under -mod=vendor: exit %d\n%s%s", code, out.String(), errs.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen", "user.go")); err != nil {
		t.Fatal(err)
	}
}
