package main

import (
	_ "embed"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// benchInner is the application's benchmarks: queries through gqlgen's handler
// on SQLite, with the generated code and the extension.
//
//go:embed testdata/bench_inner_test.txt
var benchInner string

// TestGeneratedBench generates ten entities' application, as TestLargeSchemaServed
// does, and runs its benchmarks, logging their results: the time and the
// allocations of a list, a nested list, a where filter and lookups by ID. It
// runs only with VELOX_GQL_BENCH=1, as it measures and asserts nothing:
//
//	VELOX_GQL_BENCH=1 go test -run TestGeneratedBench -v ./cmd/velox-graphql
//
// Compare two runs with benchstat; GOMAXPROCS=1 (VELOX_GQL_BENCH_CPU=1) leaves
// out the time gqlgen's goroutines wait for one another.
func TestGeneratedBench(t *testing.T) {
	if os.Getenv("VELOX_GQL_BENCH") == "" {
		t.Skip("set VELOX_GQL_BENCH=1")
	}
	const n = 10
	files := scaleSchemas(n)
	files["gqlgen.yml"] = e2eCollectGqlgen
	dir, run := app(t, files, true)
	for _, step := range [][]string{{"tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"}} {
		if out, err := run(step...); err != nil {
			t.Fatalf("%v: %v\n%s", step, err, out)
		}
	}
	for name, src := range map[string]string{
		"graph/schema.resolvers.go": scaleResolvers(t, dir, n),
		"ddl_test.go":               scaleDDL(t, dir, n),
		"scale_test.go":             strings.ReplaceAll(scaleInner, "__N__", strconv.Itoa(n)),
		"bench_test.go":             benchInner,
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range [][]string{{"tool", "gqlgen", "generate"}, {"mod", "tidy", "-e"}} {
		if out, err := run(step...); err != nil {
			t.Fatalf("%v: %v\n%s", step, err, out)
		}
	}
	args := []string{"test", "-run", "XXX", "-bench", ".", "-benchmem", "-benchtime", "2s", "-count", "1"}
	if cpu := os.Getenv("VELOX_GQL_BENCH_CPU"); cpu != "" {
		args = append(args, "-cpu", cpu)
	}
	out, err := run(append(args, ".")...)
	t.Logf("\n%s", out)
	if err != nil {
		t.Fatalf("go test -bench: %v", err)
	}
}
