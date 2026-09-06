package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
)

// driftError reports paths where regenerated output diverges from what is
// currently committed on disk. It is returned by [runCheck] so main() can
// exit with a non-zero status, and its message summarizes the drift for CI
// logs.
type driftError struct {
	paths []string
}

func (e *driftError) Error() string {
	preview := e.paths
	if len(preview) > 20 {
		preview = append(preview[:20:20], fmt.Sprintf("... (+%d more)", len(e.paths)-20))
	}
	return fmt.Sprintf("generated code is out of date — %d path(s) differ:\n  %s\nrun `velox generate` and commit the result",
		len(e.paths), strings.Join(preview, "\n  "))
}

// runCheck generates into a temporary directory and compares against the
// resolved target. It reports drift (a file velox would write that is
// missing or different on disk) but ignores extra files that exist in the
// target but are not velox output — those belong to the user and are out
// of scope.
//
// Returned *driftError means the check executed successfully but found
// drift; any other error means the check itself failed.
func runCheck(ctx context.Context, schemaPath string, opts []gen.Option) error {
	realTarget, err := resolveTarget(schemaPath, opts)
	if err != nil {
		return fmt.Errorf("resolving target: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "velox-check-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	tempOpts := append([]gen.Option{}, opts...)
	tempOpts = append(tempOpts, gen.WithTarget(tempDir))
	tempCfg, err := gen.NewConfig(tempOpts...)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	if genErr := compiler.GenerateContext(ctx, schemaPath, tempCfg); genErr != nil {
		return fmt.Errorf("code generation failed: %w", genErr)
	}

	diffs, err := diffAgainst(tempDir, realTarget)
	if err != nil {
		return fmt.Errorf("diffing output: %w", err)
	}
	if len(diffs) == 0 {
		return nil
	}
	return &driftError{paths: diffs}
}

// resolveTarget mirrors the target-resolution logic in compiler.Generate
// (uses configured Target if set, else the parent of the schema directory).
// We need to know the final target up-front so --check can compare against
// the right place without actually running the real generate.
func resolveTarget(schemaPath string, opts []gen.Option) (string, error) {
	cfg, err := gen.NewConfig(opts...)
	if err != nil {
		return "", err
	}
	if cfg.Target != "" {
		return filepath.Abs(cfg.Target)
	}
	abs, err := filepath.Abs(schemaPath)
	if err != nil {
		return "", err
	}
	return filepath.Dir(abs), nil
}

// diffAgainst walks the generated output in `generated` and returns the
// relative paths that are missing from or differ in `onDisk`. Paths that
// exist only in `onDisk` are ignored — velox only asserts what it would
// write, not what the user may have added.
func diffAgainst(generated, onDisk string) ([]string, error) {
	var diffs []string
	err := filepath.WalkDir(generated, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(generated, path)
		if err != nil {
			return err
		}
		want, err := os.ReadFile(path) //#nosec G304,G122 -- path is inside our own temp dir
		if err != nil {
			return err
		}
		have, err := os.ReadFile(filepath.Join(onDisk, rel)) //#nosec G304 -- onDisk is the user's own target
		switch {
		case errors.Is(err, fs.ErrNotExist):
			diffs = append(diffs, filepath.ToSlash(rel)+" (missing)")
			return nil
		case err != nil:
			return err
		case !bytes.Equal(want, have):
			diffs = append(diffs, filepath.ToSlash(rel)+" (differs)")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(diffs)
	return diffs, nil
}
