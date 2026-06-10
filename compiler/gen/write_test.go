package gen

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

func TestWriteFileIfChanged_NewFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.go")
	wrote, err := WriteFileIfChanged(p, []byte("package x\n"), 0o644)
	require.NoError(t, err)
	assert.True(t, wrote, "first write must report wrote=true")

	data, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, "package x\n", string(data))

	fi, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), fi.Mode().Perm(),
		"fresh files must carry the requested mode, not CreateTemp's 0600")
}

func TestWriteFileIfChanged_IdenticalSkips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.go")
	content := []byte("package x\n")
	_, err := WriteFileIfChanged(p, content, 0o644)
	require.NoError(t, err)

	// Pin the mtime to a sentinel so any rewrite is detectable without sleeping.
	sentinel := time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, os.Chtimes(p, sentinel, sentinel))

	wrote, err := WriteFileIfChanged(p, content, 0o644)
	require.NoError(t, err)
	assert.False(t, wrote, "identical content must be skipped")

	fi, err := os.Stat(p)
	require.NoError(t, err)
	assert.True(t, fi.ModTime().Equal(sentinel),
		"skipped write must leave mtime untouched — mtime-based toolchains depend on it")
}

func TestWriteFileIfChanged_ChangedRewrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.go")
	_, err := WriteFileIfChanged(p, []byte("package x\n"), 0o644)
	require.NoError(t, err)

	wrote, err := WriteFileIfChanged(p, []byte("package y\n"), 0o644)
	require.NoError(t, err)
	assert.True(t, wrote)

	data, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, "package y\n", string(data))
}

// TestOptionalFeatureSpecs_UniqueOutputs pins the ONE-writer-per-file rule at
// the dispatch level: no two optional-feature specs may target the same output
// path, and no spec may target a path owned by a static graph-level writer.
// Two writers on one path race inside the shared errgroup — whichever rename
// lands last wins and the loser's content is silently discarded. This is
// exactly how FeatureVersionedMigration's output was lost while it pointed at
// migrate/migrate.go (owned by generateMigrations).
func TestOptionalFeatureSpecs_UniqueOutputs(t *testing.T) {
	// Paths written unconditionally by generateShared/generateMigrations/
	// generateFeatures outside the optionalFeatureSpecs table.
	staticOwned := map[string]string{
		"client.go":          "generateShared",
		"velox.go":           "generateShared",
		"tx.go":              "generateShared",
		"runtime.go":         "generateShared",
		"entity/hooks.go":    "generateShared",
		"hook/hook.go":       "generateFeatures(core)",
		"migrate/schema.go":  "generateMigrations",
		"migrate/migrate.go": "generateMigrations",
	}
	seen := map[string]string{}
	for _, spec := range optionalFeatureSpecs {
		target := filepath.Join(spec.dir, spec.file)
		if owner, ok := staticOwned[filepath.ToSlash(target)]; ok {
			t.Errorf("feature %s targets %s, which is owned by %s — two writers race and one output is silently lost",
				spec.feature.Name, target, owner)
		}
		if prev, ok := seen[target]; ok {
			t.Errorf("features %s and %s both target %s — two writers race and one output is silently lost",
				prev, spec.feature.Name, target)
		}
		seen[target] = spec.feature.Name
	}
}

// TestGen_NoopRegen_PreservesMtimes is the end-to-end guard for the
// write-if-changed contract: running the full generator twice over an
// unchanged schema must leave every emitted file byte-identical AND
// mtime-identical. Without the mtime half, a no-op `make generate` cascades
// through every mtime-based consumer (make rules, file watchers, editor
// indexers) even though Go's content-addressed build cache stays warm.
func TestGen_NoopRegen_PreservesMtimes(t *testing.T) {
	schemas := []*load.Schema{
		{
			Name: "User",
			Fields: []*load.Field{
				{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
				{Name: "age", Info: &field.TypeInfo{Type: field.TypeInt}, Optional: true, Nillable: true},
			},
		},
	}
	target := t.TempDir()
	newGraph := func() *Graph {
		graph, err := NewGraph(&Config{
			Package:   "test/gen",
			Target:    target,
			Storage:   drivers["sql"],
			IDType:    &field.TypeInfo{Type: field.TypeInt},
			Generator: testGenerator(),
		}, schemas...)
		require.NoError(t, err)
		return graph
	}

	require.NoError(t, newGraph().Gen())

	// Pin every emitted file (including the manifest) to a sentinel mtime.
	sentinel := time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC)
	var pinned int
	require.NoError(t, filepath.WalkDir(target, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		pinned++
		return os.Chtimes(path, sentinel, sentinel)
	}))
	require.NotZero(t, pinned, "first Gen must emit files")

	// Second, schema-unchanged run: nothing may be rewritten.
	require.NoError(t, newGraph().Gen())

	var rewritten []string
	require.NoError(t, filepath.WalkDir(target, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		if !fi.ModTime().Equal(sentinel) {
			rel, _ := filepath.Rel(target, path)
			rewritten = append(rewritten, rel)
		}
		return nil
	}))
	assert.Empty(t, rewritten,
		"no-op regen must not rewrite any file — these were touched despite identical schema")
}
