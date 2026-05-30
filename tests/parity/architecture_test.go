package parity_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBrainHasNoORMImports pins that the A2 packages (op/model/compare/runner)
// stay free of velox and ent imports — the reference oracle must be independent
// of the implementations it judges. A3 will add runVelox/runEnt in runner/ that
// DO import them; this guard excludes files whose names start with "run_v"/"run_e".
func TestBrainHasNoORMImports(t *testing.T) {
	roots := []string{"op", "model", "compare", "runner"}
	fset := token.NewFileSet()
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		require.NoError(t, err)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			if strings.HasPrefix(e.Name(), "run_velox") || strings.HasPrefix(e.Name(), "run_ent") {
				continue // A3 executor files are allowed to import the ORMs
			}
			f, err := parser.ParseFile(fset, filepath.Join(root, e.Name()), nil, parser.ImportsOnly)
			require.NoError(t, err)
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				require.False(t,
					strings.Contains(p, "syssam/velox") || strings.Contains(p, "velox.test/parity/velox") ||
						strings.Contains(p, "entgo.io/ent") || strings.Contains(p, "velox.test/parity/ent"),
					"%s/%s imports ORM package %q — the reference brain must stay independent", root, e.Name(), p)
			}
		}
	}
}
