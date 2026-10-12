package ormgen

import (
	"bytes"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/syssam/velox/compiler"
	vgen "github.com/syssam/velox/compiler/gen"
)

// The generated code the tests of this module run against is what the
// generator produces now. Without this, a change to the generator that was
// not followed by a regeneration would be tested against the old output, and
// pass.
func TestGeneratedCodeIsCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("loads and generates a schema")
	}
	for _, c := range []struct {
		name, schema, dir string
		cfg               Config
	}{
		{"shapes", "github.com/syssam/velox/tests/orm/shapes/schema", "../../tests/orm/shapes/velox", Config{Package: "github.com/syssam/velox/tests/orm/shapes/velox"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := t.TempDir()
			vcfg, err := vgen.NewConfig(vgen.WithTarget(out), vgen.WithPackage(c.cfg.Package))
			if err != nil {
				t.Fatal(err)
			}
			graph, err := compiler.LoadGraph(c.schema, vcfg)
			if err != nil {
				t.Fatal(err)
			}
			c.cfg.Target = out
			if err := Generate(graph, c.cfg); err != nil {
				t.Fatal(err)
			}
			want := files(t, out, ".go")
			got := files(t, c.dir, ".go")
			for rel, src := range want {
				// What is written is what gofmt writes: a tree that gofmt
				// would change fails the format check of whoever keeps it.
				if clean, err := format.Source(src); err != nil || !bytes.Equal(clean, src) {
					t.Errorf("%s is not gofmt's formatting of itself (%v)", rel, err)
				}
				switch have, ok := got[rel]; {
				case !ok:
					t.Errorf("%s is generated and missing from %s: regenerate", rel, c.dir)
				case !bytes.Equal(have, src):
					t.Errorf("%s in %s is not what the generator writes now: regenerate (scripts/regen.sh)", rel, c.dir)
				}
			}
			for rel := range got {
				if _, ok := want[rel]; !ok {
					t.Errorf("%s in %s is no longer generated: regenerate", rel, c.dir)
				}
			}
			if len(want) < 10 {
				t.Errorf("only %d files were generated", len(want))
			}
			// A second generation over the same tree writes nothing.
			before := mtimes(t, out)
			if err := Generate(graph, c.cfg); err != nil {
				t.Fatal(err)
			}
			for rel, at := range mtimes(t, out) {
				if !at.Equal(before[rel]) {
					t.Errorf("%s was rewritten by a generation that changed nothing", rel)
				}
			}
		})
	}
}

func files(t *testing.T, dir, ext string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ext {
			return err
		}
		src, err := os.ReadFile(path) //nolint:gosec // a path under a test directory
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[rel] = src
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mtimes(t *testing.T, dir string) map[string]time.Time {
	t.Helper()
	out := map[string]time.Time{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[rel] = info.ModTime()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
