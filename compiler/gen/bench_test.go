package gen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

func BenchmarkGraph_Gen(b *testing.B) {
	target := filepath.Join(os.TempDir(), "velox")
	require.NoError(b, os.MkdirAll(target, os.ModePerm), "creating tmpdir")
	defer os.RemoveAll(target)
	storage, err := gen.NewStorage("sql")
	require.NoError(b, err)
	graph, err := compiler.LoadGraph("../integration/privacy/velox/schema", &gen.Config{
		Storage: storage,
		IDType:  &field.TypeInfo{Type: field.TypeInt},
		Target:  target,
		Package: "github.com/syssam/velox/compiler/integration/privacy/velox",
	})
	require.NoError(b, err)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := graph.Gen()
		require.NoError(b, err)
	}
}
