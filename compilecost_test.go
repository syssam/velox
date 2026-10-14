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

// entitySrc is a package shaped like a generated one: an entity with a Schema
// method, its MetaOnce, VeloxMeta and shape function.
const entitySrc = `package p

import "github.com/syssam/velox2"

type E struct{ ID int64 }

func (*E) Schema(*velox.Schema) {}

var eMeta velox.MetaOnce[E]

func (*E) VeloxMeta() *velox.Meta[E] { return eMeta.Get(eShape) }

func eShape() velox.Shape[E] {
	return velox.Shape[E]{Columns: []string{"ID"}, Addr: func(x *E, i int) any { return &x.ID }}
}
`

// entityUseSrc is an entity whose Schema declares a column option and two
// relations, used by a function that queries with relations, inserts, saves,
// updates and deletes it: the generic bodies velox compiles into such a
// package must not reach reflect either.
const entityUseSrc = `package p

import (
	"context"

	"github.com/syssam/velox2"
)

type E struct {
	ID       int64
	Name     string
	ParentID *int64
	Parent   velox.One[E]
	Kids     velox.Many[E]
}

func (x *E) Schema(s *velox.Schema) {
	s.Column(&x.Name).Default("none")
	s.One(&x.Parent).References(&x.ParentID)
	s.Many(&x.Kids).ForeignKey(func(k *E) **int64 { return &k.ParentID })
}

var eMeta velox.MetaOnce[E]

func (*E) VeloxMeta() *velox.Meta[E] { return eMeta.Get(eShape) }

func eShape() velox.Shape[E] {
	return velox.Shape[E]{
		Columns:   []string{"ID", "Name", "ParentID"},
		Relations: []velox.Relation{velox.RelationTo[E]("Parent"), velox.RelationTo[E]("Kids")},
		Addr: func(x *E, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Name
			case 2:
				return &x.ParentID
			}
			return nil
		},
	}
}

var (
	eID     = velox.NumberCol[E, int64]{I: 0}
	eName   = velox.StringCol[E, string]{I: 1}
	eParent = velox.Rel[E, E]{I: 0}
	eKids   = velox.Rel[E, E]{I: 1}
)

func Use(ctx context.Context, db *velox.DB, e *E) error {
	if _, err := db.Query[E]().Where(eKids.Has()).With(eParent).Limit(1).All(ctx); err != nil {
		return err
	}
	if err := db.Insert(e).Exec(ctx); err != nil {
		return err
	}
	if err := db.Save(ctx, e); err != nil {
		return err
	}
	if _, err := db.Update[E]().Set(eName.Set("x")).Where(eID.EQ(1)).Exec(ctx); err != nil {
		return err
	}
	_, err := db.Delete[E]().Where(eID.EQ(1)).Exec(ctx)
	return err
}
`

// genericObjects is one unexported variable of each exported generic type,
// and of the non-generic types generated code reaches, as a package would
// declare it (or, with src, a whole package), and the most bytes its compiled
// package may take more than a package that imports velox and declares an
// unexported velox.Dialect variable.
var genericObjects = []struct {
	name, decl string
	src        string // the package's source, instead of one variable of decl
	max        int64
}{
	// Measured on 2026-10-14, darwin/arm64, Go 1.27.1: each max is the
	// measured growth plus the larger of 4096 and a quarter of it, the quarter
	// capped at 65536, rounded up to the next KiB, with the measurement beside
	// it. One ordinary method adds about 20 KiB to a handle (NumberCol: 14846 →
	// 35782), which exceeds that margin; a velox type reaching reflect.Type adds
	// about 134 KiB, which the cap keeps above the margin of the largest rows.
	// linux/amd64 and linux/arm64 measure within 3% of darwin/arm64.
	{"Col", "velox.Col[E, bool]", "", 18432},                        // measured 14312
	{"OrderedCol", "velox.OrderedCol[E, int64]", "", 19456},         // measured 14924
	{"NumberCol", "velox.NumberCol[E, int64]", "", 19456},           // measured 14846
	{"StringCol", "velox.StringCol[E, string]", "", 19456},          // measured 14900
	{"NullCol", "velox.NullCol[E, bool]", "", 19456},                // measured 14658
	{"NullOrderedCol", "velox.NullOrderedCol[E, int64]", "", 19456}, // measured 15270
	{"NullNumberCol", "velox.NullNumberCol[E, int64]", "", 19456},   // measured 15188
	{"NullStringCol", "velox.NullStringCol[E, string]", "", 19456},  // measured 15228
	{"Pred", "velox.Pred[E]", "", 5120},                             // measured 1014
	{"Order", "velox.Order[E]", "", 6144},                           // measured 1114
	{"Setter", "velox.Setter[E]", "", 8192},                         // measured 3168
	{"Query", "velox.Query[E]", "", 10240},                          // measured 5270
	{"UpdateQuery", "velox.UpdateQuery[E]", "", 9216},               // measured 4502
	{"DeleteQuery", "velox.DeleteQuery[E]", "", 9216},               // measured 4502
	{"InsertQuery", "velox.InsertQuery[E]", "", 10240},              // measured 5402
	{"Rel", "velox.Rel[E, E]", "", 8192},                            // measured 3528
	{"One", "velox.One[E]", "", 20480},                              // measured 15972
	{"Many", "velox.Many[E]", "", 20480},                            // measured 16044
	{"MetaOnce", "velox.MetaOnce[E]", "", 10240},                    // measured 5470
	{"Shape", "velox.Shape[E]", "", 7168},                           // measured 2910
	{"Meta", "velox.Meta[E]", "", 7168},                             // measured 2316
	{"ColumnDef", "velox.ColumnDef[int64]", "", 6144},               // measured 1530
	{"RelDef", "velox.RelDef[E]", "", 6144},                         // measured 1680
	{"Statement", "velox.Statement", "", 5120},                      // measured 368
	{"EntityInfo", "*velox.EntityInfo", "", 5120},                   // measured 294
	{"Schema", "*velox.Schema", "", 5120},                           // measured 282
	{"ColumnInfo", "velox.ColumnInfo", "", 5120},                    // measured 20
	{"entity", "", entitySrc, 123904},                               // measured 98878
	{"entityUse", "", entityUseSrc, 740352},                         // measured 673936
}

// TestGenericTypesCompileSmall enforces design §3 rules 1, 3 and 7 for one
// package: it measures each row's own object cost, the bytes its package
// takes beyond a package that already imports velox, so an ordinary method on
// a generic type, a method table, or a velox type or generic body reaching
// reflect shows as growth. It does not catch a rule-2 regression through
// relations: E has none, so a relation type reaching its target's types is
// not measured here.
func TestGenericTypesCompileSmall(t *testing.T) {
	if testing.Short() {
		t.Skip("builds packages; run without -short")
	}
	dir := t.TempDir()
	files := map[string]string{"base/base.go": "package base\n\nimport \"github.com/syssam/velox2\"\n\ntype E struct{ ID int64 }\n\nvar x velox.Dialect\n"}
	for _, g := range genericObjects {
		src := g.src
		if src == "" {
			src = fmt.Sprintf("package p\n\nimport \"github.com/syssam/velox2\"\n\ntype E struct{ ID int64 }\n\nvar x %s\n", g.decl)
		}
		files[g.name+"/p.go"] = src
	}
	if err := tempmod.Write(dir, repoRoot(), files); err != nil {
		t.Fatal(err)
	}
	size := func(t *testing.T, pkg string) int64 {
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
	base := size(t, "base")
	for _, g := range genericObjects {
		t.Run(g.name, func(t *testing.T) {
			t.Parallel()
			what := "one variable of " + g.decl
			if g.src != "" {
				what = "the " + g.name + " package"
			}
			got := size(t, g.name) - base
			t.Logf("%s: +%d bytes", g.name, got)
			if got > g.max {
				t.Errorf("%s adds %d bytes to its package, over the recorded %d (design §3)", what, got, g.max)
			}
		})
	}
}
