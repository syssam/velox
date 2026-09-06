package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
)

// testSchemaPath returns the absolute path to the canonical testschema
// directory, or skips the test if it is not reachable from cmd/velox.
func testSchemaPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testschema"))
	require.NoError(t, err)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("testschema not accessible at %s: %v", p, err)
	}
	return p
}

func TestRunCheck_NoDriftAfterGenerate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode (runs full codegen pipeline twice)")
	}

	schemaPath := testSchemaPath(t)
	target := t.TempDir()

	opts := []gen.Option{
		gen.WithTarget(target),
		gen.WithPackage("example.com/out"),
	}

	cfg, err := gen.NewConfig(opts...)
	require.NoError(t, err)
	require.NoError(t, compiler.GenerateContext(context.Background(), schemaPath, cfg))

	// A second generate into the same target with identical config should
	// produce zero drift.
	err = runCheck(context.Background(), schemaPath, opts)
	assert.NoError(t, err)
}

func TestRunCheck_ReportsDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode (runs full codegen pipeline)")
	}

	schemaPath := testSchemaPath(t)
	target := t.TempDir()

	opts := []gen.Option{
		gen.WithTarget(target),
		gen.WithPackage("example.com/out"),
	}

	cfg, err := gen.NewConfig(opts...)
	require.NoError(t, err)
	require.NoError(t, compiler.GenerateContext(context.Background(), schemaPath, cfg))

	// Walk the generated tree and corrupt the first regular file we find —
	// this simulates a forgot-to-regenerate scenario.
	var victim string
	_ = filepath.WalkDir(target, func(path string, d os.DirEntry, _ error) error {
		if victim != "" || d.IsDir() {
			return nil
		}
		victim = path
		return nil
	})
	require.NotEmpty(t, victim, "expected at least one generated file")
	require.NoError(t, os.WriteFile(victim, []byte("// tampered\n"), 0o644))

	err = runCheck(context.Background(), schemaPath, opts)
	require.Error(t, err)
	var drift *driftError
	require.True(t, errors.As(err, &drift), "expected driftError, got %T: %v", err, err)
	assert.NotEmpty(t, drift.paths)
}

func TestRunCheck_ReportsMissingFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode (runs full codegen pipeline)")
	}

	schemaPath := testSchemaPath(t)
	target := t.TempDir()

	opts := []gen.Option{
		gen.WithTarget(target),
		gen.WithPackage("example.com/out"),
	}

	// No prior generate — target is empty. Every file velox would produce
	// should be reported as missing.
	err := runCheck(context.Background(), schemaPath, opts)
	require.Error(t, err)
	var drift *driftError
	require.True(t, errors.As(err, &drift), "expected driftError, got %T: %v", err, err)
	assert.NotEmpty(t, drift.paths)
}

func TestGenerateCmd_DryRunAndCheckMutuallyExclusive(t *testing.T) {
	err := generateCmd([]string{"--dry-run", "--check", "/nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}
