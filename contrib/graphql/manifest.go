package graphql

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/syssam/velox/compiler/gen"
)

// manifestFile records, in OutDir, every file the GraphQL extension wrote,
// so the next run can delete the ones it no longer writes. The core
// generator keeps .velox-manifest for its own output, but the extension
// writes after that is final: an entity that stopped being a connection left
// query/gql_pagination_<entity>.go behind, still compiling against a
// Paginate that no longer existed.
const manifestFile = ".velox-graphql-manifest"

// writtenFiles is the set of files one run wrote, safe for the parallel
// writers.
type writtenFiles struct {
	mu    sync.Mutex
	paths []string
}

// track records a file the extension wrote, or would have written had its
// content changed: either way it is part of this run's output.
func (g *Generator) track(path string) {
	rel, err := filepath.Rel(g.config.OutDir, path)
	if err != nil || g.written == nil {
		return
	}
	g.written.mu.Lock()
	g.written.paths = append(g.written.paths, filepath.ToSlash(rel))
	g.written.mu.Unlock()
}

// pruneStale deletes the files the previous run recorded and this one did
// not write, then records this run's. Paths are stored with forward
// slashes, so a manifest committed from one OS is valid on another.
func (g *Generator) pruneStale() error {
	if g.written == nil {
		return nil
	}
	manifest := filepath.Join(g.config.OutDir, manifestFile)
	current := map[string]bool{}
	for _, rel := range g.written.paths {
		current[rel] = true
	}
	var errs []error
	if data, err := os.ReadFile(manifest); err == nil {
		for rel := range strings.SplitSeq(string(data), "\n") {
			if rel == "" || current[rel] {
				continue
			}
			p := filepath.Join(g.config.OutDir, filepath.FromSlash(rel))
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("remove stale %s: %w", p, err))
			}
		}
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for rel := range current {
			if !yield(rel) {
				return
			}
		}
	})
	if _, err := gen.WriteFileIfChanged(manifest, []byte(strings.Join(names, "\n")+"\n"), 0o644); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
