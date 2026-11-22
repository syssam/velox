package main

import (
	"bytes"
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"

	"github.com/syssam/velox/internal/tempmod"
)

// runtimeFS holds the runtime apps' test files: common/ is the same in every
// app, and <orm>/ holds that ORM's ops_test.go.
//
//go:embed testdata/runtime
var runtimeFS embed.FS

// writeRuntimeApp writes o's runtime app in dir: RuntimeSchema, its generated
// code, for velox the DDL of both dialects, and the test files of common/ and
// of o, all at the app root.
func writeRuntimeApp(dir string, o orm, tools string) error {
	if err := writeApp(o, dir, o.RuntimeSchema()); err != nil {
		return err
	}
	if _, _, err := codegen(dir, o, tools); err != nil {
		return err
	}
	if o.Name == veloxORM.Name {
		for _, d := range []string{"sqlite", "postgres"} {
			cmd := exec.CommandContext(context.Background(), filepath.Join(tools, "velox-gen"), "migrate", "sql", "-dialect", d, "./schema")
			var stderr bytes.Buffer
			cmd.Dir, cmd.Env, cmd.Stderr = dir, goEnv(), &stderr
			out, err := cmd.Output()
			if err != nil {
				return fmt.Errorf("velox-gen migrate sql -dialect %s: %w\n%s%s", d, err, stderr.Bytes(), tempmod.Hint(stderr.String()))
			}
			if err := os.WriteFile(filepath.Join(dir, "ddl_"+d+".sql"), out, 0o644); err != nil {
				return err
			}
		}
	}
	for _, sub := range []string{"common", o.Name} {
		root := path.Join("testdata/runtime", sub)
		entries, err := fs.ReadDir(runtimeFS, root)
		if err != nil {
			return err
		}
		for _, e := range entries {
			src, err := runtimeFS.ReadFile(path.Join(root, e.Name()))
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, e.Name()), src, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// runtimeAppMain is compare runtime-app: it writes one ORM's runtime app.
func runtimeAppMain(args []string) int {
	fs := flag.NewFlagSet("runtime-app", flag.ContinueOnError)
	name := fs.String("orm", "", "the ORM: velox, veloxe or ent")
	tools := fs.String("tools", "", "the directory of the generator binaries")
	dir := fs.String("dir", "", "the app's directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || *tools == "" || *dir == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "compare runtime-app: -orm, -tools and -dir are required, and nothing else")
		return 2
	}
	o, err := ormByName(*name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compare runtime-app:", err)
		return 2
	}
	if err := writeRuntimeApp(*dir, o, *tools); err != nil {
		fmt.Fprintln(os.Stderr, "compare runtime-app:", err)
		return 1
	}
	return 0
}
