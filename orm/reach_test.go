//go:build go1.27

package orm_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplicationTypesDoNotReachReflect pins what a package of an application
// pays for naming a type of this package, by compiling one.
//
// A package compiles something for every type it can reach from the types it
// names: through fields, through pointers, and through the signatures of
// methods, the unexported ones too. For most types that is next to nothing.
// For reflect.Type, reflect.Value and the handles of database/sql it is some
// 160 KB of object code and 0.02 CPU-s, in each package. Meta had a field of
// type reflect.Type once, and every package that named a predicate, which is
// every package of a generated tree and of the application using it, paid
// that: the 450 entity packages of the benchmarks were 194 MB of archives
// where they are now 128, and took a quarter more CPU to compile.
//
// So no type an application names may reach those: such a value is held as
// any (Meta.typ), and a function that takes or returns one is a function and
// not a method (typeOf, goType, runSelection). The test declares one variable
// of each type and compares the object file with that of a variable of
// reflect.Type itself.
func TestApplicationTypesDoNotReachReflect(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a dozen packages")
	}
	dir := t.TempDir()
	list := exec.Command("go", "list", "-export", "-deps", "-f", "{{if .Export}}packagefile {{.ImportPath}}={{.Export}}{{end}}",
		"github.com/syssam/velox/orm", "reflect")
	cfg, err := list.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	cfgPath := filepath.Join(dir, "importcfg")
	if err := os.WriteFile(cfgPath, cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	size := func(name, imports, typ string) int64 {
		src := filepath.Join(dir, name+".go")
		code := fmt.Sprintf("package %s\n\nimport (\n%s)\n\n// C is the one thing the package has.\nvar C %s\n", name, imports, typ)
		if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
		obj := filepath.Join(dir, name+".a")
		out, err := exec.Command("go", "tool", "compile", "-p", name, "-importcfg", cfgPath, "-complete", "-o", obj, src).CombinedOutput()
		if err != nil {
			t.Fatalf("compiling a variable of %s: %v\n%s", typ, err, out)
		}
		st, err := os.Stat(obj)
		if err != nil {
			t.Fatal(err)
		}
		return st.Size()
	}

	const ormImport = "\t\"github.com/syssam/velox\"\n\t\"github.com/syssam/velox/orm\"\n"
	empty := size("empty", "", "int")
	control := size("control", "\t\"reflect\"\n", "reflect.Type")
	if control < 4*empty+50_000 {
		t.Skipf("a variable of reflect.Type costs %d bytes of object (an int: %d): this toolchain no longer charges for it", control, empty)
	}
	for i, typ := range []string{
		"orm.P", "orm.S", "orm.O", "*orm.Meta", "*orm.EdgeMeta", "*orm.FieldMeta", "*orm.EdgeLoad",
		"*orm.QueryCore", "*orm.MutCore", "*orm.Selection", "*orm.Page",
		"orm.StringColumn[string, orm.P]", "orm.NullableAutoNumberColumn[int, orm.P]",
		"*orm.ToOne[orm.P, orm.P, int]", "*orm.ToMany[orm.P, orm.P, int]",
		"*orm.QueryOf[*int, orm.P, int]", "*orm.CreateOf[*int, orm.P]", "*orm.CreateBulkOf[*int, orm.P]",
		"*orm.UpdateOf[*int, orm.P]", "*orm.DeleteOf[orm.P]", "orm.ClientOf[*int, orm.P, int]",
		"*orm.Connection[*int]", "velox.Executor", "*velox.Tx", "velox.Hook", "velox.Policy", "velox.Interceptor",
	} {
		imports := ormImport
		if !strings.Contains(typ, "velox.") {
			imports = "\t\"github.com/syssam/velox/orm\"\n"
		} else if !strings.Contains(typ, "orm.") {
			imports = "\t\"github.com/syssam/velox\"\n"
		}
		if got := size(fmt.Sprintf("probe%d", i), imports, typ); got >= control {
			t.Errorf("a package with one variable of %s is %d bytes of object, as much as one of reflect.Type (%d): the type reaches reflect or database/sql through a field or the signature of a method", typ, got, control)
		}
	}
}
