package compiler

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

// TestGenerateIsDeterministic runs the full codegen pipeline twice against
// the canonical testschema and asserts byte-for-byte identical output across
// runs. Catches nondeterminism introduced by map iteration, goroutine
// ordering through errgroup, filepath walk order, or non-stable sort keys.
//
// A single nondeterministic map range would otherwise corrupt PR diffs —
// pinning this at the pipeline level is cheaper than debugging a 200K-line
// spurious diff after the fact.
func TestGenerateIsDeterministic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode (runs full codegen pipeline)")
	}

	schemaPath, err := filepath.Abs(filepath.Join("..", "testschema"))
	require.NoError(t, err)
	if _, err := os.Stat(schemaPath); err != nil {
		t.Skipf("testschema not accessible at %s: %v", schemaPath, err)
	}

	run := func(target string) {
		cfg, err := gen.NewConfig(
			gen.WithTarget(target),
			gen.WithPackage("github.com/syssam/velox/tests/integration"),
			gen.WithFeatures(
				gen.FeaturePrivacy,
				gen.FeatureIntercept,
				gen.FeatureEntQL,
				gen.FeatureNamedEdges,
				gen.FeatureBidiEdgeRefs,
				gen.FeatureSnapshot,
				gen.FeatureSchemaConfig,
				gen.FeatureLock,
				gen.FeatureExecQuery,
				gen.FeatureUpsert,
				gen.FeatureVersionedMigration,
				gen.FeatureGlobalID,
				gen.FeatureValidator,
				gen.FeatureAutoDefault,
			),
		)
		require.NoError(t, err)
		require.NoError(t, GenerateContext(context.Background(), schemaPath, cfg))
	}

	dir1 := t.TempDir()
	dir2 := t.TempDir()
	run(dir1)
	run(dir2)

	diffs := diffGeneratedDirs(t, dir1, dir2)
	if len(diffs) > 0 {
		preview := diffs
		if len(preview) > 20 {
			preview = append(preview[:20:20], "... (truncated)")
		}
		t.Fatalf("nondeterministic codegen — %d path(s) differ between runs:\n  %s",
			len(diffs), strings.Join(preview, "\n  "))
	}
}

// diffGeneratedDirs walks two directories and reports relative paths that
// differ — missing on either side, or content mismatch. Paths are returned
// in sorted order for stable failure messages.
func diffGeneratedDirs(t *testing.T, a, b string) []string {
	t.Helper()
	filesA := collectGeneratedFiles(t, a)
	filesB := collectGeneratedFiles(t, b)

	allPaths := make(map[string]struct{}, len(filesA)+len(filesB))
	for p := range filesA {
		allPaths[p] = struct{}{}
	}
	for p := range filesB {
		allPaths[p] = struct{}{}
	}

	var diffs []string
	for p := range allPaths {
		ba, okA := filesA[p]
		bb, okB := filesB[p]
		switch {
		case !okA:
			diffs = append(diffs, p+" (only in run 2)")
		case !okB:
			diffs = append(diffs, p+" (only in run 1)")
		case !bytes.Equal(ba, bb):
			diffs = append(diffs, p+" (content differs)")
		}
	}
	sort.Strings(diffs)
	return diffs
}

// collectGeneratedFiles returns a map of relative-path → file contents for
// every regular file under root. Relative paths use forward slashes so that
// failure messages are stable across platforms.
func collectGeneratedFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	require.NoError(t, err)
	return out
}
