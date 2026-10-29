package main

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/syssam/velox2/internal/buildcost"
	"github.com/syssam/velox2/internal/tempmod"
)

// harnessDir is the directory of this module, bench/compare, which holds the
// go.mod and go.sum that every app is written with.
func harnessDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate the harness module")
	}
	return filepath.Dir(file), nil
}

// appGoMod turns the harness's go.mod into the go.mod of a temporary app:
// module example.com/tmp, no tool directives, every requirement with its
// version, and the replace of velox2 pointing at root. Nothing is tidied, so
// the three apps resolve the same versions of what they share.
func appGoMod(harnessMod []byte, root string) ([]byte, error) {
	f, err := modfile.Parse("go.mod", harnessMod, nil)
	if err != nil {
		return nil, err
	}
	if err := f.AddModuleStmt("example.com/tmp"); err != nil {
		return nil, err
	}
	for _, t := range slices.Clone(f.Tool) {
		if err := f.DropTool(t.Path); err != nil {
			return nil, err
		}
	}
	const velox2 = "github.com/syssam/velox2"
	if err := f.DropReplace(velox2, ""); err != nil {
		return nil, err
	}
	if err := f.AddReplace(velox2, "", root, ""); err != nil {
		return nil, err
	}
	f.Cleanup()
	return f.Format()
}

// writeApp writes the app in dir: the go.mod that appGoMod makes of the
// harness's, the harness's go.sum as it is, and files (path relative to dir →
// source).
func writeApp(dir string, files map[string]string) error {
	hd, err := harnessDir()
	if err != nil {
		return err
	}
	mod, err := os.ReadFile(filepath.Join(hd, "go.mod"))
	if err != nil {
		return err
	}
	root, err := filepath.Abs(filepath.Join(hd, "..", ".."))
	if err != nil {
		return err
	}
	appMod, err := appGoMod(mod, root)
	if err != nil {
		return err
	}
	sum, err := os.ReadFile(filepath.Join(hd, "go.sum"))
	if err != nil {
		return err
	}
	all := maps.Clone(files)
	if all == nil {
		all = map[string]string{}
	}
	all["go.mod"], all["go.sum"] = string(appMod), string(sum)
	for name, content := range all {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// goEnv is the environment of a go command in an app: the process's, with the
// module cache as the only source (offline, no workspace, -mod=mod as
// tempmod.Go has it, after the user's GOFLAGS), then extra.
func goEnv(extra ...string) []string {
	env := append(os.Environ(), "GOFLAGS="+tempmod.GoFlags("-mod=mod"), "GOPROXY=off", "GOWORK=off")
	return append(env, extra...)
}

// countGenerated counts the lines and bytes of the *.go files under dir,
// leaving out the directory skip, a path relative to dir (ent's schema, which
// is not generated). Empty skip leaves out nothing. dir may be relative or
// absolute. Call it as countGenerated(filepath.Join(app, o.GenDir), o.genSkip()).
func countGenerated(dir, skip string) (lines, bytes int, err error) {
	if skip != "" {
		skip = filepath.Clean(skip)
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel, err := filepath.Rel(dir, p); err == nil && skip != "" && rel == skip {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".go" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		bytes += len(b)
		lines += strings.Count(string(b), "\n")
		if len(b) > 0 && b[len(b)-1] != '\n' {
			lines++
		}
		return nil
	})
	return lines, bytes, err
}

// codegen runs o's code generator, from the tools directory, in the app, and
// returns its CPU seconds (with the go commands it ran) and wall seconds. It
// uses the ordinary build cache: the generators are run warm, as a developer runs them.
func codegen(app string, o orm, tools string) (cpu, wall float64, err error) {
	argv := o.Codegen(tools)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = app, goEnv()
	start := time.Now()
	out, err := cmd.CombinedOutput()
	wall = time.Since(start).Seconds()
	if err != nil {
		return 0, wall, fmt.Errorf("%s code generation: %v\n%s%s", o.Name, err, out, tempmod.Hint(string(out)))
	}
	return buildcost.CPUSeconds(cmd), wall, nil
}
