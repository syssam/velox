package graphqlgen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file the extension wrote on one run and not the next is deleted: an
// entity that stops being filterable, or a connection, leaves nothing behind
// that compiles against code that no longer exists. A file the extension
// never wrote is left alone, whatever its name.
func TestGenerateDeletesWhatItNoLongerWrites(t *testing.T) {
	dir := t.TempDir()
	run := func(whereInputs bool) {
		t.Helper()
		g := NewGenerator(mockGraph(), Config{
			OutDir: dir, Package: "graphql", ORMPackage: "example/ent",
			RelayConnection: true, WhereInputs: whereInputs,
		})
		require.NoError(t, g.Generate(context.Background()))
	}
	filterFile := filepath.Join(dir, "filter", "filter.go")
	mine := filepath.Join(dir, "filter", "custom.go")

	run(true)
	require.FileExists(t, filterFile)
	manifest, err := os.ReadFile(filepath.Join(dir, manifestFile))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "filter/filter.go\n", "paths use forward slashes on every OS")
	require.NoError(t, os.WriteFile(mine, []byte("package filter\n"), 0o644))

	run(false)
	assert.NoFileExists(t, filterFile, "the extension stopped writing it")
	assert.FileExists(t, mine, "the extension never wrote it")
	manifest, err = os.ReadFile(filepath.Join(dir, manifestFile))
	require.NoError(t, err)
	assert.NotContains(t, string(manifest), "filter/")
	assert.True(t, strings.HasSuffix(string(manifest), "\n"))

	// A second identical run leaves the manifest as it was.
	before, _ := os.Stat(filepath.Join(dir, manifestFile))
	run(false)
	after, _ := os.Stat(filepath.Join(dir, manifestFile))
	assert.Equal(t, before.ModTime(), after.ModTime(), "no-op regeneration rewrites nothing")
}
