package veloxgql_test

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/untyped"
)

// TestVariablesWeighWhatTheyTake: 13000 points of a GeoJSON, 39001 plain values, pass; the
// objects of an input type weigh more, and as many of them do not.
func TestVariablesWeighWhatTheyTake(t *testing.T) {
	e := gqlgenx.RequestLimit(nil).(graphql.OperationParameterMutator)
	points := make([]any, 13000)
	for i := range points {
		points[i] = []any{114.17, 22.32}
	}
	if err := e.MutateOperationParameters(context.Background(), &graphql.RawParams{Query: `{ a }`, Variables: map[string]any{"geo": points}}); err != nil {
		t.Errorf("a GeoJSON of 13000 points: %v", err)
	}
}

func TestDefaultLimits(t *testing.T) {
	want := veloxgql.Limits{MaxPage: 100, MaxList: 1000, MaxFilterDepth: 5, MaxFilterWidth: 100, MaxFilterNodes: 1000, MaxOrderTerms: 5, MaxCost: 150000, MaxIntrospectionCost: 250000, MaxTokens: 15000, MaxFields: 5000, MaxFragments: 500, MaxDepth: 64, MaxNameBytes: 128, MaxLiteralBytes: 64 << 10, MaxVariableNodes: 100000, MaxErrors: 100, MaxBodyBytes: 1 << 20, MaxResponseBytes: 32 << 20, Timeout: 30 * time.Second}
	if got := veloxgql.DefaultLimits(); got != want {
		t.Errorf("DefaultLimits() = %+v, want %+v", got, want)
	}
	if got := veloxgql.LimitsOf(nil); got != want {
		t.Errorf("LimitsOf(nil) = %+v, want %+v", got, want)
	}
	// DefaultLimits is a function: changing what it returned changes no one
	// else's.
	d := veloxgql.DefaultLimits()
	d.MaxPage = 1
	if got := veloxgql.DefaultLimits(); got != want {
		t.Errorf("after a change to a copy, DefaultLimits() = %+v", got)
	}
}

// openDB opens an in-memory SQLite DB with opts and one table, items.
func openDB(t *testing.T, opts ...velox.Option) *velox.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1) // an in-memory database lives as long as its one connection
	if _, err := sqlDB.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY); INSERT INTO items (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	return velox.Open(velox.SQLite, sqlDB, opts...)
}

// item is a hand-written entity, as velox-gen would write it.
type item struct {
	ID     int64
	source velox.Source
}

var itemMeta velox.MetaOnce[item]

func (*item) VeloxMeta() *velox.Meta[item] { return itemMeta.Load(itemDescriptor) }

func itemDescriptor() velox.Descriptor[item] {
	return velox.Descriptor[item]{
		Table:      "items",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		SourcePtr:  func(x *item) *velox.Source { return &x.source },
		FieldPtr: func(x *item, i int) any {
			if i == 0 {
				return &x.ID
			}
			return nil
		},
	}
}

// TestMaxCostDefault: MaxCost is 150000 by default, and a zero MaxCost takes
// it.
func TestMaxCostDefault(t *testing.T) {
	if got := veloxgql.DefaultLimits().MaxCost; got != 150000 {
		t.Errorf("DefaultLimits().MaxCost = %d, want 150000", got)
	}
	if got := veloxgql.LimitsOf(openDB(t, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 2}))).MaxCost; got != 150000 {
		t.Errorf("a zero MaxCost became %d, want 150000", got)
	}
	if got := veloxgql.LimitsOf(openDB(t, veloxgql.WithLimits(veloxgql.Limits{MaxCost: 5}))).MaxCost; got != 5 {
		t.Errorf("MaxCost 5 became %d", got)
	}
}

// TestWithLimitsPanicsOnNegative: a negative limit panics, naming the
// field; zero is the default.
func TestWithLimitsPanicsOnNegative(t *testing.T) {
	const tail = ", and a limit must not be negative (zero is the default)"
	for _, c := range []struct {
		l    veloxgql.Limits
		want string
	}{
		{veloxgql.Limits{MaxCost: -1}, "veloxgql.WithLimits: Limits.MaxCost is -1" + tail},
		{veloxgql.Limits{MaxPage: -3}, "veloxgql.WithLimits: Limits.MaxPage is -3" + tail},
		{veloxgql.Limits{Timeout: -time.Second}, "veloxgql.WithLimits: Limits.Timeout is -1s" + tail},
	} {
		func() {
			defer func() {
				if got := recover(); got != c.want {
					t.Errorf("WithLimits(%+v) panicked with %v, want %q", c.l, got, c.want)
				}
			}()
			veloxgql.WithLimits(c.l)
		}()
	}
}

// TestWithLimitsZeroIsDefault: a zero field of WithLimits' Limits is the
// default, and the last WithLimits wins.
func TestWithLimitsZeroIsDefault(t *testing.T) {
	got := veloxgql.LimitsOf(openDB(t, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 2})))
	want := veloxgql.DefaultLimits()
	want.MaxPage = 2
	if got != want {
		t.Errorf("WithLimits(Limits{MaxPage: 2}): %+v, want %+v", got, want)
	}
	if got := veloxgql.LimitsOf(openDB(t)); got != veloxgql.DefaultLimits() {
		t.Errorf("no WithLimits: %+v, want the defaults", got)
	}
	got = veloxgql.LimitsOf(openDB(t,
		veloxgql.WithLimits(veloxgql.Limits{MaxPage: 2, MaxList: 3}),
		veloxgql.WithLimits(veloxgql.Limits{MaxOrderTerms: 4, MaxCost: 150000}),
	))
	want = veloxgql.DefaultLimits()
	want.MaxOrderTerms = 4
	if got != want {
		t.Errorf("WithLimits twice: %+v, want the last's, %+v", got, want)
	}
}

// TestLimitsReachDerivedDBs: the limits of a DB are those of the DB of a
// transaction begun on it, and of the DB a row was read through.
func TestLimitsReachDerivedDBs(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 7}))
	err := db.Tx(ctx, func(_ context.Context, tx *velox.DB) error {
		if got := veloxgql.LimitsOf(tx).MaxPage; got != 7 {
			t.Errorf("a transaction's MaxPage = %d, want 7", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := db.Query[item]().AllowUnbounded().First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	src := untyped.SourceDB(untyped.TableOf[item](), row)
	if got := veloxgql.LimitsOf(src).MaxPage; got != 7 {
		t.Errorf("a row's source DB has MaxPage %d, want 7", got)
	}
}

// TestNothingRunsAtStart pins the rule that nothing runs at program start, in
// the core or in gqlgenx: no file of either has an init function but an
// adapter's gqlcore.Register of its source (registers), and no package-level variable
// is initialised by a call or by a map literal (the compiler builds a map in
// init). Two calls are allowed, because the compiler initialises them
// statically (go build -gcflags=-S shows veloxgql's init empty, with the
// values in the data section):
//   - errors.New of a string literal, errors being the standard library's,
//     as velox's own sentinel errors are;
//   - the conversion of nil to a pointer type, (*T)(nil), for the
//     compile-time interface assertions.
func TestNothingRunsAtStart(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// gqlgen's adapter, which a gqlgen server links with the core.
	adapter, err := filepath.Glob("gqlgenx/*.go")
	if err != nil {
		t.Fatal(err)
	}
	names = append(names, adapter...)
	var srcs []source
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, source{name, string(src)})
	}
	if len(srcs) == 0 {
		t.Fatal("found no source files")
	}
	for _, bad := range startWork(t, srcs) {
		t.Error(bad)
	}
}

// The check finds what it is for.
func TestStartWorkFindsIt(t *testing.T) {
	const a = `package p

import ("errors"; "fmt"; "regexp"; "github.com/99designs/gqlgen/graphql")

type T struct{}
type M map[string]int

var ok = errors.New("fine")
var _ graphql.Marshaler = (*T)(nil)
var (
	re   = regexp.MustCompile("a")
	n, m = 1, len("ab")
	e    = fmt.Errorf("x")
	f    = func() int { return 1 }
	s    = []error{errors.New(fmt.Sprint(1))}
	lit  = map[string]int{"a": 1}
	nm   = &M{}
	conv = T(struct{}{})
	ptr  = (*T)(&T{})
)

func init() {}
`
	const b = `package p

import errors "github.com/pkg/errors"

var notStd = errors.New("not static")
`
	got := startWork(t, []source{{"a.go", a}, {"b.go", b}})
	want := []string{"re", "m", "e", "s", "lit", "nm", "conv", "ptr", "init", "notStd"}
	if len(got) != len(want) {
		t.Fatalf("found %q, want one each for %q", got, want)
	}
	for i, w := range want {
		if !strings.Contains(got[i], " "+w+" ") && !strings.HasSuffix(got[i], " "+w) {
			t.Errorf("finding %d = %q, want one naming %s", i, got[i], w)
		}
	}
}

type source struct{ name, src string }

// startWork returns a line for each init function and each package-level
// variable whose initialiser runs code, in the files srcs of one package.
func startWork(t *testing.T, srcs []source) []string {
	t.Helper()
	fset := token.NewFileSet()
	files := make([]*ast.File, len(srcs))
	maps := map[string]bool{} // the package's map types
	for i, s := range srcs {
		f, err := parser.ParseFile(fset, s.name, s.src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[i] = f
		for _, d := range f.Decls {
			if d, ok := d.(*ast.GenDecl); ok && d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					ts := spec.(*ast.TypeSpec)
					if _, ok := ts.Type.(*ast.MapType); ok {
						maps[ts.Name.Name] = true
					}
				}
			}
		}
	}
	var out []string
	for _, f := range files {
		imports := map[string]string{} // the name a file refers to an import by → its path
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			name := path.Base(p)
			if im.Name != nil {
				name = im.Name.Name
			}
			imports[name] = p
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name == "init" && !registers(d, imports) {
					out = append(out, fset.Position(d.Pos()).String()+": func init")
				}
			case *ast.GenDecl:
				if d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, v := range vs.Values {
						if why := runs(v, imports, maps); why != "" {
							n := vs.Names[min(i, len(vs.Names)-1)].Name
							out = append(out, fset.Position(v.Pos()).String()+": var "+n+" "+why)
						}
					}
				}
			}
		}
	}
	return out
}

// registers reports whether init is an adapter's registration of its
// selection source (decision 0173), the one init allowed: a single call of
// gqlcore.Register with a composite literal, which takes a lock and stores a
// slice of one element.
func registers(init *ast.FuncDecl, imports map[string]string) bool {
	if init.Body == nil || len(init.Body.List) != 1 {
		return false
	}
	es, ok := init.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Register" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || imports[pkg.Name] != "github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore" {
		return false
	}
	_, lit := call.Args[0].(*ast.CompositeLit)
	return lit
}

// runs says what in e runs at start, outside function literals: a call other
// than the static ones, or a map literal; "" when nothing does.
func runs(e ast.Expr, imports map[string]string, maps map[string]bool) string {
	why := ""
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false // runs when called, not at start
		case *ast.CallExpr:
			if !staticErrorsNew(n, imports) && !nilPointer(n) {
				why = "is initialised by a call"
			}
		case *ast.CompositeLit:
			switch ty := n.Type.(type) {
			case *ast.MapType:
				why = "is initialised by a map literal"
			case *ast.Ident:
				if maps[ty.Name] {
					why = "is initialised by a map literal"
				}
			}
		}
		return why == ""
	})
	return why
}

// staticErrorsNew reports whether c is the standard library's errors.New of
// a string literal.
func staticErrorsNew(c *ast.CallExpr, imports map[string]string) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "New" || len(c.Args) != 1 {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	lit, isLit := c.Args[0].(*ast.BasicLit)
	return ok && imports[pkg.Name] == "errors" && isLit && lit.Kind == token.STRING
}

// nilPointer reports whether c is (*T)(nil).
func nilPointer(c *ast.CallExpr) bool {
	paren, ok := c.Fun.(*ast.ParenExpr)
	if !ok || len(c.Args) != 1 {
		return false
	}
	_, star := paren.X.(*ast.StarExpr)
	arg, isIdent := c.Args[0].(*ast.Ident)
	return star && isIdent && arg.Name == "nil"
}
