package repocheck

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"sync"
	"testing"
)

// srcImporter imports packages from source, resolved from dir, the module's
// root.
type srcImporter struct {
	dir string
	imp types.ImporterFrom
}

func (s srcImporter) Import(path string) (*types.Package, error) {
	return s.imp.ImportFrom(path, s.dir, 0)
}

// The file set and the importer are shared, so that the module's packages are
// type-checked from source once.
var (
	srcFset = token.NewFileSet()
	srcImp  = sync.OnceValue(func() types.Importer {
		return srcImporter{repoRoot(), importer.ForCompiler(srcFset, "source", nil).(types.ImporterFrom)}
	})
)

// typeCheck type-checks the single file src as a package in the module and
// returns the errors the checker reports.
func typeCheck(t *testing.T, src string) []string {
	t.Helper()
	fset := srcFset
	f, err := parser.ParseFile(fset, "snippet.go", src, parser.AllErrors)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}
	var errs []string
	cfg := types.Config{
		Importer: srcImp(),
		Error:    func(err error) { errs = append(errs, err.Error()) },
	}
	_, _ = cfg.Check("snippet", fset, []*ast.File{f}, nil)
	return errs
}

// TestSaveNamesAColumnAtCompileTime: db.Save(&x) with no column is a compile
// error, not a runtime one; the same call with a column compiles.
func TestSaveNamesAColumnAtCompileTime(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the module from source; run without -short")
	}
	const head = `package snippet

import (
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func save(db *velox.DB, x *testmodel.Invoice) {
`
	good := head + "\t_ = db.Save(x, testmodel.Invoices.Number)\n\t_ = db.Save(x, testmodel.Invoices.Number, testmodel.Invoices.Total).Where(testmodel.Invoices.Total.EQ(1))\n}\n"
	if errs := typeCheck(t, good); len(errs) != 0 {
		t.Fatalf("the control does not compile: %q", errs)
	}
	bad := head + "\t_ = db.Save(x)\n}\n"
	errs := typeCheck(t, bad)
	if len(errs) != 1 || !strings.Contains(errs[0], "not enough arguments in call to db.Save") {
		t.Errorf("db.Save(x) type-checks as %q, want one \"not enough arguments\" error", errs)
	}
}
