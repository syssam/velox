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
// than a package that imports velox and declares a non-generic velox variable
// (velox.Dialect), so each figure is the instantiation's own cost.
var genericObjects = []struct {
	name, decl string
	max        int64
}{
	// Measured on 2026-10-14, darwin/arm64, Go 1.27.1: each max is the
	// measured growth plus the larger of 4096 and a quarter of it, rounded up
	// to the next KB, with the measurement beside it. One ordinary method adds
	// about 18 KB to a handle, which exceeds that margin. The types holding
	// executor and schema state are large because their own instantiation
	// reaches the types of their fields.
	{"Col", "velox.Col[E, bool]", 22528},                        // measured 17338
	{"OrderedCol", "velox.OrderedCol[E, int64]", 25600},         // measured 19932
	{"NumberCol", "velox.NumberCol[E, int64]", 26624},           // measured 20772
	{"StringCol", "velox.StringCol[E, string]", 28672},          // measured 22650
	{"NullCol", "velox.NullCol[E, bool]", 24576},                // measured 18924
	{"NullOrderedCol", "velox.NullOrderedCol[E, int64]", 27648}, // measured 21656
	{"NullNumberCol", "velox.NullNumberCol[E, int64]", 28672},   // measured 22398
	{"NullStringCol", "velox.NullStringCol[E, string]", 30720},  // measured 24148
	{"Pred", "velox.Pred[E]", 4096},                             // measured -196
	{"Order", "velox.Order[E]", 4096},                           // measured -234
	{"Setter", "velox.Setter[E]", 6144},                         // measured 1978
	{"Query", "velox.Query[E]", 320512},                         // measured 256034
	{"UpdateQuery", "velox.UpdateQuery[E]", 319488},             // measured 255346
	{"DeleteQuery", "velox.DeleteQuery[E]", 319488},             // measured 255396
	{"InsertQuery", "velox.InsertQuery[E]", 320512},             // measured 256212
	{"Rel", "velox.Rel[E, E]", 60416},                           // measured 48240
	{"One", "velox.One[E]", 20480},                              // measured 15626
	{"Many", "velox.Many[E]", 20480},                            // measured 15706
	{"MetaOnce", "velox.MetaOnce[E]", 234496},                   // measured 187506
	{"Shape", "velox.Shape[E]", 216064},                         // measured 172688
	{"Meta", "velox.Meta[E]", 215040},                           // measured 171906
	{"ColumnDef", "velox.ColumnDef[int64]", 234496},             // measured 187024
	{"RelDef", "velox.RelDef[E]", 234496},                       // measured 187142
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
	files := map[string]string{"base/base.go": "package base\n\nimport \"github.com/syssam/velox2\"\n\ntype E struct{ ID int64 }\n\nvar X velox.Dialect\n"}
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
