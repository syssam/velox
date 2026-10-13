package ormgql_test

import (
	"bytes"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox/compiler"
	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql/graphqlgen"
	"github.com/syssam/velox/contrib/graphql/ormgql"
	"github.com/syssam/velox/orm/ormgen"
)

// The generated code the tests of package orm run against, with its GraphQL
// layer and the schema files, is what the generator produces now. Without this, a change to the generator that was
// not followed by a regeneration would be tested against the old output, and
// pass.
func TestGeneratedCodeIsCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("loads and generates a schema")
	}
	for _, c := range []struct {
		name, schema, dir string
		cfg               ormgen.Config
		// sdl is the directory of the tree's GraphQL schema, for a tree
		// that is generated with one.
		sdl string
	}{
		{"testschema", "github.com/syssam/velox/testschema", "../../../tests/orm/velox", ormgen.Config{Package: "github.com/syssam/velox/tests/orm/velox"}, "../../../tests/orm/graphql"},
		{"gqlshapes", "github.com/syssam/velox/tests/orm/gqlshapes/schema", "../../../tests/orm/gqlshapes/velox", ormgen.Config{Package: "github.com/syssam/velox/tests/orm/gqlshapes/velox"}, "../../../tests/orm/gqlshapes/graphql"},
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
			sdlOut := t.TempDir()
			if c.sdl != "" {
				// As velox-gen -graphql generates it.
				c.cfg.Extensions = []ormgen.Extension{ormgql.New(ormgql.Config{Dir: sdlOut, Schema: graphqlgen.Config{
					SchemaFilename: "velox.graphql", SchemaSplitMode: graphqlgen.SchemaSplitPerEntity, WhereInputs: true,
				}})}
			}
			if err := ormgen.Generate(graph, c.cfg); err != nil {
				t.Fatal(err)
			}
			if c.sdl != "" {
				wantSDL, gotSDL := files(t, sdlOut, ".graphql"), files(t, c.sdl, ".graphql")
				for rel, src := range wantSDL {
					if !bytes.Equal(gotSDL[rel], src) {
						t.Errorf("%s in %s is not what the generator writes now: regenerate", rel, c.sdl)
					}
				}
				for rel := range gotSDL {
					if _, ok := wantSDL[rel]; !ok {
						t.Errorf("%s in %s is no longer generated: regenerate", rel, c.sdl)
					}
				}
				if len(wantSDL) < 4 {
					t.Errorf("only %d schema files were generated", len(wantSDL))
				}
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
			if err := ormgen.Generate(graph, c.cfg); err != nil {
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

// An extension adds files and changes none: the tree generated with the
// GraphQL layer is the tree generated without it, and the layer's own files
// beside it. An application that takes the layer out gets the tree it would
// have had, and reading a file tells which of the two it belongs to.
func TestGraphQLLayerIsInFilesOfItsOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("loads and generates a schema")
	}
	for _, c := range []struct {
		name, schema, pkg, tracked string
		// kinds are the kinds of file of the layer the tree must have, for
		// the test to cover them.
		kinds []string
	}{
		{"testschema", "github.com/syssam/velox/testschema", "github.com/syssam/velox/tests/orm/velox", "../../../tests/orm/velox",
			[]string{"filter", "entity", "nodes", "leaf"}},
		// The shapes that need more: an interface of the schema, and the
		// scalar of a JSON field's Go type.
		{"gqlshapes", "github.com/syssam/velox/tests/orm/gqlshapes/schema", "github.com/syssam/velox/tests/orm/gqlshapes/velox", "../../../tests/orm/gqlshapes/velox",
			[]string{"entity", "interfaces", "nodes", "scalars", "leaf"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := t.TempDir()
			vcfg, err := vgen.NewConfig(vgen.WithTarget(out), vgen.WithPackage(c.pkg))
			if err != nil {
				t.Fatal(err)
			}
			graph, err := compiler.LoadGraph(c.schema, vcfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := ormgen.Generate(graph, ormgen.Config{Target: out, Package: c.pkg}); err != nil {
				t.Fatal(err)
			}
			plain, with := files(t, out, ".go"), files(t, c.tracked, ".go")
			if len(plain) < 10 {
				t.Fatalf("only %d files were generated", len(plain))
			}
			for rel, src := range plain {
				if !bytes.Equal(with[rel], src) {
					t.Errorf("%s is not the same file with the GraphQL layer as without it", rel)
				}
			}
			kinds := map[string]int{}
			for rel := range with {
				if _, ok := plain[rel]; ok {
					continue
				}
				dir, base := filepath.Split(filepath.ToSlash(rel))
				switch {
				case dir == "filter/":
					kinds["filter"]++
				case dir == "entity/" && base == "entity_gql.go":
					kinds["interfaces"]++
				case dir == "entity/" && strings.HasSuffix(base, "_gql.go"):
					kinds["entity"]++
				case dir == "" && base == "node_gql.go":
					kinds["nodes"]++
				case dir == "" && base == "scalars_gql.go":
					kinds["scalars"]++
				case base == "gql.go":
					kinds["leaf"]++
				default:
					t.Errorf("%s is written with the GraphQL layer only, and is not named as a file of it", rel)
				}
			}
			for _, kind := range c.kinds {
				if kinds[kind] == 0 {
					t.Errorf("the tracked tree has no file of the layer of the kind %q: the test no longer covers it", kind)
				}
			}
		})
	}
}
