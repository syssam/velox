package velox

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/syssam/velox2/internal/tempmod"
)

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

// genericObjects is one variable of each exported generic type, as a package
// would declare it, and the most bytes its compiled package may take more
// than a package declaring an int64. Measured on 2026-10-14 and doubled.
var genericObjects = []struct {
	name, decl string
	max        int64
}{
	// Measured on 2026-10-14, darwin/arm64, Go 1.27.1: each max is twice the
	// measured growth rounded up to the next KB, the measurement beside it.
	// The figures include the export data the variable's type reaches, so
	// the types holding executor and schema state are larger than the handles.
	{"Col", "velox.Col[E, bool]", 67584},                        // measured 33710
	{"OrderedCol", "velox.OrderedCol[E, int64]", 72704},         // measured 36304
	{"NumberCol", "velox.NumberCol[E, int64]", 74752},           // measured 37144
	{"StringCol", "velox.StringCol[E, string]", 78848},          // measured 39022
	{"NullCol", "velox.NullCol[E, bool]", 70656},                // measured 35296
	{"NullOrderedCol", "velox.NullOrderedCol[E, int64]", 76800}, // measured 38028
	{"NullNumberCol", "velox.NullNumberCol[E, int64]", 77824},   // measured 38770
	{"NullStringCol", "velox.NullStringCol[E, string]", 81920},  // measured 40520
	{"Pred", "velox.Pred[E]", 32768},                            // measured 16176
	{"Order", "velox.Order[E]", 32768},                          // measured 16138
	{"Setter", "velox.Setter[E]", 36864},                        // measured 18350
	{"Query", "velox.Query[E]", 545792},                         // measured 272406
	{"UpdateQuery", "velox.UpdateQuery[E]", 543744},             // measured 271718
	{"DeleteQuery", "velox.DeleteQuery[E]", 543744},             // measured 271768
	{"InsertQuery", "velox.InsertQuery[E]", 545792},             // measured 272584
	{"Rel", "velox.Rel[E, E]", 130048},                          // measured 64612
	{"One", "velox.One[E]", 64512},                              // measured 31998
	{"Many", "velox.Many[E]", 64512},                            // measured 32078
	{"MetaOnce", "velox.MetaOnce[E]", 408576},                   // measured 203878
	{"Shape", "velox.Shape[E]", 378880},                         // measured 189060
	{"Meta", "velox.Meta[E]", 376832},                           // measured 188278
	{"ColumnDef", "velox.ColumnDef[int64]", 407552},             // measured 203396
	{"RelDef", "velox.RelDef[E]", 407552},                       // measured 203514
}

// TestGenericTypesCompileSmall enforces design §3 rules 1–3: declaring one
// variable of a generic velox type adds little to the declaring package's
// object, because no method body is compiled until called and no type
// descriptor reaches beyond the type.
func TestGenericTypesCompileSmall(t *testing.T) {
	if testing.Short() {
		t.Skip("builds packages; run without -short")
	}
	dir := t.TempDir()
	files := map[string]string{"base/base.go": "package base\n\ntype E struct{ ID int64 }\n\nvar X int64\n"}
	for _, g := range genericObjects {
		files[g.name+"/p.go"] = fmt.Sprintf("package p\n\nimport \"github.com/syssam/velox2\"\n\ntype E struct{ ID int64 }\n\nvar X %s\n", g.decl)
	}
	if err := tempmod.Write(dir, repoRoot(), files); err != nil {
		t.Fatal(err)
	}
	size := func(pkg string) int64 {
		out := filepath.Join(dir, pkg+".a")
		if _, err := tempmod.Go(dir, "build", "-o", out, "./"+pkg); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	base := size("base")
	for _, g := range genericObjects {
		got := size(g.name) - base
		t.Logf("%s: +%d bytes", g.name, got)
		if got > g.max {
			t.Errorf("one variable of %s adds %d bytes to its package, over the recorded %d (design §3)", g.decl, got, g.max)
		}
	}
}
