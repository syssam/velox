package migrate

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestImports: the package imports the standard library, the root package,
// internal/ddl and internal/buildinfo, and nothing else, so a tool that reads a snapshot links no
// driver and no generator.
func TestImports(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	n := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		n++
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			switch {
			case path == "github.com/syssam/velox", path == "github.com/syssam/velox/internal/ddl", path == "github.com/syssam/velox/internal/buildinfo":
			case !strings.Contains(strings.SplitN(path, "/", 2)[0], "."):
			default:
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
	if n == 0 {
		t.Fatal("no source files")
	}
}
