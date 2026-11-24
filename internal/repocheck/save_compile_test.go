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
// type-checked from source once. The importer is not safe for concurrent
// use: srcMu holds it for one type check at a time.
var (
	srcMu   sync.Mutex
	srcFset = token.NewFileSet()
	srcImp  = sync.OnceValue(func() types.Importer {
		return srcImporter{repoRoot(), importer.ForCompiler(srcFset, "source", nil).(types.ImporterFrom)}
	})
)

// typeCheck type-checks the single file src as a package in the module and
// returns the errors the checker reports.
func typeCheck(t *testing.T, src string) []string {
	t.Helper()
	srcMu.Lock()
	defer srcMu.Unlock()
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
	t.Parallel()
	if testing.Short() {
		t.Skip("type-checks the module from source; run without -short")
	}
	const head = `package snippet

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
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

// TestGroupKeyAtCompileTime: a grouped aggregate infers K from the bare key
// handle and refuses, when it compiles, a JSON column, another entity's
// column, a handle's pointer and a value column of another entity.
func TestGroupKeyAtCompileTime(t *testing.T) {
	t.Parallel()
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
		a, _ = velox.CountBy(ctx, q, S.ID)
		b, _ = velox.CountBy(ctx, q, S.Status)
		c, _ = velox.CountBy(ctx, q, S.Prev)
		d, _ = velox.CountBy(ctx, q, S.Flag)
		e, _ = velox.CountBy(ctx, q, S.MaybeFlag)
		f, _ = velox.CountBy(ctx, q, S.When)
		g, _ = velox.CountBy(ctx, q, S.MaybeWhen)
		h, _ = velox.CountBy(ctx, q, S.Ratio)
		i, _ = velox.SumBy(ctx, q, S.Status, S.Price)
		k, _ = velox.AvgBy(ctx, q, S.Status, S.Price)
		l, _ = velox.MinBy(ctx, q, S.Prev, S.Price)
		m, _ = velox.MaxBy(ctx, q, S.Prev, S.Price)
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
		"a JSON column":          `velox.CountBy(ctx, j, fixture.JDocs.Tags)`,
		"a nullable JSON column": `velox.CountBy(ctx, j, fixture.JDocs.Maybe)`,
		"another entity's key":   `velox.CountBy(ctx, q, I.OrgID)`,
		"a handle's pointer":     `velox.CountBy(ctx, q, &S.Status)`,
		"another entity's value": `velox.SumBy(ctx, q, S.Status, I.Total)`,
		"a text value":           `velox.SumBy(ctx, q, S.Status, S.Name)`,
	} {
		if errs := typeCheck(t, head+"\t_, _ = "+bad+"\n}\n"); len(errs) == 0 {
			t.Errorf("%s compiles: %s", name, bad)
		}
	}
}

// TestValuesAtCompileTime: Query.Values infers V from the handle of every
// readable kind, a pointer for a nullable column, and refuses, when it
// compiles, a JSON column, another entity's column, a handle's pointer, a
// Key and a result of another type.
func TestValuesAtCompileTime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("type-checks the module from source; run without -short")
	}
	const head = `package snippet

import (
	"context"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/internal/testmodel/kind"
)

var (
	_ fixture.JDoc
	_ kind.Status
	_ time.Time
)

func values(ctx context.Context, q velox.Query[testmodel.Sample], j velox.Query[fixture.JDoc]) {
	S, I := testmodel.Samples, testmodel.Invoices
	_, _ = S, I
`
	good := head + `
	var (
		a, _ = q.Values(ctx, S.ID)
		b, _ = q.Values(ctx, S.Status)
		c, _ = q.Values(ctx, S.Prev)
		d, _ = q.Values(ctx, S.Flag)
		e, _ = q.Values(ctx, S.MaybeFlag)
		f, _ = q.Values(ctx, S.When)
		g, _ = q.Values(ctx, S.MaybeWhen)
		h, _ = q.Values(ctx, S.Name)
		i, _ = q.Values(ctx, S.Price)
		k, _ = q.IDs[int64](ctx)
	)
	var _ []int64 = a
	var _ []kind.Status = b
	var _ []*kind.Status = c
	var _ []bool = d
	var _ []*bool = e
	var _ []time.Time = f
	var _ []*time.Time = g
	var _ []string = h
	var _ []kind.Cents = i
	var _ []int64 = k
}
`
	if errs := typeCheck(t, good); len(errs) != 0 {
		t.Fatalf("the control does not compile: %q", errs)
	}
	for name, bad := range map[string]string{
		"a JSON column":            `j.Values(ctx, fixture.JDocs.Tags)`,
		"a nullable JSON column":   `j.Values(ctx, fixture.JDocs.Maybe)`,
		"another entity's column":  `q.Values(ctx, I.OrgID)`,
		"a handle's pointer":       `q.Values(ctx, &S.Status)`,
		"a result of another type": `func() (_ []string, err error) { return q.Values(ctx, S.ID) }()`,
	} {
		if errs := typeCheck(t, head+"\t_, _ = "+bad+"\n}\n"); len(errs) == 0 {
			t.Errorf("%s compiles: %s", name, bad)
		}
	}
}

// TestTypedHookAccessorsAtCompileTime: a hook's typed accessors take and
// return the column's type, so a value of another type does not compile.
func TestTypedHookAccessorsAtCompileTime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("type-checks the module from source; run without -short")
	}
	const head = `package snippet

import (
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

var I = testmodel.Invoices

func hook(m *velox.Mutation, r velox.Row) {
`
	good := head + `	var total float64
	total, _ = I.Total.Value(m)
	_ = I.Total.Assign(m, total)
	_ = I.Note.Fill(m, "x")
	var n string = I.Number.Get(r)
	_ = I.Number.Put(r, n)
	var p *time.Time
	p, _ = I.PaidAt.ValuePtr(m)
	p = I.PaidAt.GetPtr(r)
	_ = p
	_ = I.PaidAt.AssignNull(m)
	_ = I.PaidAt.PutNull(r)
}
`
	if errs := typeCheck(t, good); len(errs) != 0 {
		t.Fatalf("the control does not compile: %q", errs)
	}
	for _, bad := range []string{
		`_ = I.Total.Assign(m, "x")`,
		`_ = I.Note.Fill(m, 1)`,
		`_ = I.Number.Put(r, 5)`,
		`var s string; s, _ = I.Total.Value(m); _ = s`,
		`var i int = I.Number.Get(r); _ = i`,
		`_ = I.PaidAt.Assign(m, "x")`,
	} {
		if errs := typeCheck(t, head+"\t"+bad+"\n}\n"); len(errs) == 0 {
			t.Errorf("%s compiles", bad)
		}
	}
}
