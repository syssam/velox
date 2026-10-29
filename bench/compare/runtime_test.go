package main

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// appMethods are the methods of *app that every ORM's ops_test.go defines.
var appMethods = []string{"count", "eager", "get", "insertBulk", "insertOne", "query", "rows", "seed", "seededID", "update"}

// benchmarks are the Benchmark functions of common/bench_test.go.
var benchmarks = []string{
	"BenchmarkCount", "BenchmarkEagerO2M100", "BenchmarkGetByID", "BenchmarkInsertBulk100",
	"BenchmarkInsertOne", "BenchmarkQuery100", "BenchmarkUpdateByID",
}

// parseRuntime parses one file of testdata/runtime.
func parseRuntime(t *testing.T, rel string) *ast.File {
	t.Helper()
	src, err := runtimeFS.ReadFile("testdata/runtime/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestRuntimeFilesAgree checks that each ORM's ops file defines ormName as its
// directory's name and the same methods of *app, and that the shared file
// holds exactly the seven benchmarks.
func TestRuntimeFilesAgree(t *testing.T) {
	for _, o := range orms {
		f := parseRuntime(t, o.Name+"/ops_test.go")
		if f.Name.Name != "bench" {
			t.Errorf("%s: package %s, want bench", o.Name, f.Name.Name)
		}
		var name string
		var methods []string
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, s := range d.Specs {
					if v, ok := s.(*ast.ValueSpec); ok && d.Tok == token.CONST {
						for i, id := range v.Names {
							if id.Name == "ormName" && i < len(v.Values) {
								if lit, ok := v.Values[i].(*ast.BasicLit); ok {
									name = strings.Trim(lit.Value, `"`)
								}
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) != 1 {
					continue
				}
				if star, ok := d.Recv.List[0].Type.(*ast.StarExpr); ok {
					if id, ok := star.X.(*ast.Ident); ok && id.Name == "app" {
						methods = append(methods, d.Name.Name)
					}
				}
			}
		}
		if name != o.Name {
			t.Errorf("%s: ormName is %q", o.Name, name)
		}
		slices.Sort(methods)
		if !slices.Equal(methods, appMethods) {
			t.Errorf("%s: the methods of *app are %v, want %v", o.Name, methods, appMethods)
		}
	}
	var got []string
	for _, d := range parseRuntime(t, "common/bench_test.go").Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Benchmark") {
			got = append(got, fn.Name.Name)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, benchmarks) {
		t.Errorf("common/bench_test.go has the benchmarks %v, want %v", got, benchmarks)
	}
}

// runGuard writes o's runtime app and runs its TestGuard, with extra in the environment.
func runGuard(t *testing.T, o orm, extra ...string) {
	t.Helper()
	tools := testTools(t)
	app := t.TempDir()
	if err := writeRuntimeApp(app, o, tools); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-race", "-count=1", "-run", "^TestGuard$", ".")
	cmd.Dir, cmd.Env = app, goEnv(extra...)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok  \texample.com/tmp") {
		t.Fatalf("%s: TestGuard: %v\n%s", o.Name, err, out)
	}
}

func TestGuardSQLite(t *testing.T) {
	if testing.Short() {
		t.Skip("three code generations and test builds")
	}
	for _, o := range orms {
		t.Run(o.Name, func(t *testing.T) { runGuard(t, o, "COMPARE_DIALECT=sqlite") })
	}
}

// TestGuardPostgres runs the guard of each ORM in turn against one database
// (Review Focus 4): each ORM touches only its own schema; the velox2pgx run
// reuses and reseeds velox2's. Every schema keeps its rows after the later
// guards. The velox2pgx run passes only if newApp's probe found its
// operations on the pgx pool.
func TestGuardPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("four code generations and test builds")
	}
	dsn := os.Getenv("VELOX_COMPARE_POSTGRES")
	if dsn == "" {
		t.Skip("VELOX_COMPARE_POSTGRES is unset")
	}
	runGuard(t, entORM, "COMPARE_DIALECT=postgres")
	runGuard(t, velox2ORM, "COMPARE_DIALECT=postgres")
	runGuard(t, velox2ORM, "COMPARE_DIALECT=postgres", "COMPARE_VELOX2_PGX=1")
	runGuard(t, oldVeloxORM, "COMPARE_DIALECT=postgres")
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	for _, name := range []string{"ent", "velox2", "oldvelox"} {
		var e1 int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_`+name+`.e1s`).Scan(&e1); err != nil {
			t.Fatal(err)
		}
		if e1 != 500 {
			t.Errorf("cmp_%s.e1s has %d rows after the four guards, want 500", name, e1)
		}
	}
}

func TestRuntimeORMByName(t *testing.T) {
	if got, err := runtimeORMByName("velox2pgx"); err != nil || got != "velox2" {
		t.Errorf("velox2pgx: got %q, %v; want velox2", got, err)
	}
	for _, o := range orms {
		if got, err := runtimeORMByName(o.Name); err != nil || got != o.Name {
			t.Errorf("%s: got %q, %v", o.Name, got, err)
		}
	}
	_, err := runtimeORMByName("nope")
	if err == nil {
		t.Fatal("nope: no error")
	}
	for _, name := range []string{"velox2", "oldvelox", "ent", "velox2pgx"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error %q does not name %s", err, name)
		}
	}
}
