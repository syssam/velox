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
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	_ "github.com/syssam/velox/internal/testmodel/runtime" // binds the schema's policies and hooks
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

// TestGroupKeyAtCompileTime: a grouped aggregate infers K from the key's handle
// and refuses, when it compiles, a JSON column, another entity's column, a bare
// handle and a value column of another entity.
func TestGroupKeyAtCompileTime(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the module from source; run without -short")
	}
	const head = `package snippet

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/internal/testmodel/kind"
)

var (
	_ fixture.JDoc
	_ kind.Status
)

func group(ctx context.Context, q velox.Query[testmodel.Sample], j velox.Query[fixture.JDoc]) {
	S, I := testmodel.Samples, testmodel.Invoices
	_, _ = S, I
`
	good := head + `
	var (
		_ []velox.Group[int64, int64]
		a, _ = velox.CountBy(ctx, q, S.ID.Key())
		b, _ = velox.CountBy(ctx, q, S.Status.Key())
		c, _ = velox.CountBy(ctx, q, S.Prev.Key())
		d, _ = velox.CountBy(ctx, q, S.Flag.Key())
		e, _ = velox.CountBy(ctx, q, S.MaybeFlag.Key())
		f, _ = velox.CountBy(ctx, q, S.When.Key())
		g, _ = velox.CountBy(ctx, q, S.MaybeWhen.Key())
		h, _ = velox.CountBy(ctx, q, S.Ratio.Key())
		i, _ = velox.SumBy(ctx, q, S.Status.Key(), S.Price)
		k, _ = velox.AvgBy(ctx, q, S.Status.Key(), S.Price)
		l, _ = velox.MinBy(ctx, q, S.Prev.Key(), S.Price)
		m, _ = velox.MaxBy(ctx, q, S.Prev.Key(), S.Price)
	)
	var _ []velox.Group[int64, int64] = a
	var _ []velox.Group[kind.Status, int64] = b
	var _ []velox.Group[*kind.Status, int64] = c
	_, _, _, _, _ = d, e, f, g, h
	var _ []velox.Group[kind.Status, kind.Cents] = i
	var _ []velox.Group[kind.Status, float64] = k
	var _ []velox.Group[*kind.Status, kind.Cents] = l
	var _ []velox.Group[*kind.Status, kind.Cents] = m
}
`
	if errs := typeCheck(t, good); len(errs) != 0 {
		t.Fatalf("the control does not compile: %q", errs)
	}
	for name, bad := range map[string]string{
		"a JSON column":          `velox.CountBy(ctx, j, fixture.JDocs.Tags.Key())`,
		"a nullable JSON column": `velox.CountBy(ctx, j, fixture.JDocs.Maybe.Key())`,
		"another entity's key":   `velox.CountBy(ctx, q, I.OrgID.Key())`,
		"a bare handle":          `velox.CountBy(ctx, q, S.Status)`,
		"another entity's value": `velox.SumBy(ctx, q, S.Status.Key(), I.Total)`,
		"a text value":           `velox.SumBy(ctx, q, S.Status.Key(), S.Name)`,
	} {
		if errs := typeCheck(t, head+"\t_, _ = "+bad+"\n}\n"); len(errs) == 0 {
			t.Errorf("%s compiles: %s", name, bad)
		}
	}
}
