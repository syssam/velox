package graphql

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWhereInputOutputSubdir asserts the generator writes WhereInput files
// into a subdirectory named "filter", not "gqlfilter".
func TestWhereInputOutputSubdir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "filter-output-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	g := mockGraph()
	gen := NewGenerator(g, Config{
		OutDir:          tmpDir,
		Package:         "graphql",
		RelayConnection: true,
		WhereInputs:     true,
		ORMPackage:      "example/ent",
	})
	require.NoError(t, gen.Generate(context.Background()))

	// filter/filter.go must exist; gqlfilter/ must not.
	assert.FileExists(t, filepath.Join(tmpDir, "filter", "filter.go"))
	assert.NoDirExists(t, filepath.Join(tmpDir, "gqlfilter"))
}
