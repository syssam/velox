package gen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	gensql "github.com/syssam/velox/compiler/gen/sql"
	"github.com/syssam/velox/schema/field"
)

// loadBenchGraph loads the privacy integration schema for benchmarking.
func loadBenchGraph(b *testing.B, target string, features ...gen.Feature) *gen.Graph {
	b.Helper()
	storage, err := gen.NewStorage("sql")
	require.NoError(b, err)
	graph, err := compiler.LoadGraph("../integration/privacy/velox/schema", &gen.Config{
		Storage:  storage,
		IDType:   &field.TypeInfo{Type: field.TypeInt},
		Target:   target,
		Package:  "github.com/syssam/velox/compiler/integration/privacy/velox",
		Features: features,
	})
	require.NoError(b, err)
	return graph
}

// BenchmarkGraph_Gen benchmarks full code generation (base, no optional features).
func BenchmarkGraph_Gen(b *testing.B) {
	target := filepath.Join(os.TempDir(), "velox-bench-base")
	require.NoError(b, os.MkdirAll(target, os.ModePerm))
	defer os.RemoveAll(target)
	graph := loadBenchGraph(b, target)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		require.NoError(b, gensql.Generate(graph))
	}
}

// BenchmarkGraph_Gen_WithPrivacy benchmarks generation with the privacy feature enabled.
func BenchmarkGraph_Gen_WithPrivacy(b *testing.B) {
	target := filepath.Join(os.TempDir(), "velox-bench-privacy")
	require.NoError(b, os.MkdirAll(target, os.ModePerm))
	defer os.RemoveAll(target)
	graph := loadBenchGraph(b, target, gen.FeaturePrivacy)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		require.NoError(b, gensql.Generate(graph))
	}
}

// BenchmarkGraph_Gen_WithAllFeatures benchmarks generation with all optional features.
func BenchmarkGraph_Gen_WithAllFeatures(b *testing.B) {
	target := filepath.Join(os.TempDir(), "velox-bench-all")
	require.NoError(b, os.MkdirAll(target, os.ModePerm))
	defer os.RemoveAll(target)
	graph := loadBenchGraph(b, target,
		gen.FeaturePrivacy,
		gen.FeatureIntercept,
		gen.FeatureSnapshot,
	)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		require.NoError(b, gensql.Generate(graph))
	}
}

// BenchmarkGraph_LoadGraph benchmarks schema loading and graph construction.
func BenchmarkGraph_LoadGraph(b *testing.B) {
	target := filepath.Join(os.TempDir(), "velox-bench-load")
	require.NoError(b, os.MkdirAll(target, os.ModePerm))
	defer os.RemoveAll(target)
	storage, err := gen.NewStorage("sql")
	require.NoError(b, err)
	cfg := &gen.Config{
		Storage: storage,
		IDType:  &field.TypeInfo{Type: field.TypeInt},
		Target:  target,
		Package: "github.com/syssam/velox/compiler/integration/privacy/velox",
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := compiler.LoadGraph("../integration/privacy/velox/schema", cfg)
		require.NoError(b, err)
	}
}
