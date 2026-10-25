package velox

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/syssam/velox2/internal/tempmod"
)

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

// entityDescSrc is the minimal entity velox-gen writes, with no Policy or
// Hooks method.
const entityDescSrc = `package p

import "github.com/syssam/velox2"

type E struct{ ID int64 }

var eMeta velox.MetaOnce[E]

func (*E) VeloxMeta() *velox.Meta[E] { return eMeta.Load(eDesc) }

func eDesc() velox.Desc[E] {
	return velox.Desc[E]{
		Table:      "e",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr:       func(x *E, i int) any { return &x.ID },
	}
}
`

// entityDescUseSrc is an entity with a default and two relations, from a Desc
// literal, used by a function that queries with relations, inserts, saves,
// updates and deletes it: the generic bodies velox compiles into such a
// package must not reach reflect either.
const entityDescUseSrc = `package p

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

var eMeta velox.MetaOnce[E]

func (*E) VeloxMeta() *velox.Meta[E] { return eMeta.Load(eDesc) }

func eDesc() velox.Desc[E] {
	return velox.Desc[E]{
		Table: "e",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "Name", Name: "name", Default: "none"},
			{Field: "ParentID", Name: "parent_id"},
		},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[E]("Parent", 2, 0), velox.EdgeTo[E]("Kids", 0, 2)},
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
		EdgeAddr: func(x *E, i int) any {
			switch i {
			case 0:
				return &x.Parent
			case 1:
				return &x.Kids
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
	if err := db.Save(e, eName).Exec(ctx); err != nil {
		return err
	}
	if _, err := db.Update[E]().Set(eName.Set("x")).Where(eID.EQ(1)).Exec(ctx); err != nil {
		return err
	}
	_, err := db.Delete[E]().Where(eID.EQ(1)).Exec(ctx)
	return err
}
`

// entityDescHooksSrc is entityDescUseSrc with a Policy and three hooks,
// declared as methods and bound through the Desc's Policy and Hooks fields as
// velox-gen binds them.
var entityDescHooksSrc = strings.Replace(entityDescUseSrc, `var (
	eID `, `func (*E) Policy() velox.Policy[E] {
	return velox.Policy[E]{
		Read:   func(context.Context) (velox.Pred[E], error) { return eName.EQ("r"), nil },
		Write:  func(context.Context) (velox.Pred[E], error) { return velox.Allow[E](), nil },
		Insert: func(_ context.Context, x *E) error { return nil },
	}
}

func (*E) Hooks() []velox.Hook[E] {
	return []velox.Hook[E]{
		velox.On(func(next velox.Mutator[E]) velox.Mutator[E] {
			return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
				for _, x := range m.Rows() {
					x.Name = "i"
				}
				return next.Mutate(ctx, m)
			})
		}, velox.OpInsert),
		velox.On(func(next velox.Mutator[E]) velox.Mutator[E] {
			return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
				m.Set(eName.Set("u"))
				_ = m.DB()
				return next.Mutate(ctx, m)
			})
		}, velox.OpUpdate),
		velox.On(func(next velox.Mutator[E]) velox.Mutator[E] {
			return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
				m.Where(eID.EQ(1))
				return next.Mutate(ctx, m)
			})
		}, velox.OpDelete),
	}
}

var (
	eID `, 1)

// entityDescHooksBound is entityDescHooksSrc with its Desc binding the
// methods.
var entityDescHooksBound = strings.Replace(entityDescHooksSrc, `		Tenant:     -1,
		Edges:`, `		Tenant:     -1,
		Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*E)(nil).Policy()) },
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*E)(nil).Hooks()) },
		Edges:`, 1)

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
	// linux/amd64 and linux/arm64 measure within 3% of darwin/arm64. The rows
	// that moved were remeasured on 2026-10-15 after observability: Statement
	// gained Dialect, Tags, Caller, Tx, Fingerprint and Result, and their
	// types (Caller, Tag, Result, TxInfo, TxRetry) are export data in every
	// package that reaches a Statement or a DB: +242 bytes for Statement
	// (566 → 808 against the commit before the plan), up to +240 for the
	// handles, and the same export data for the entity packages, whose rows
	// were then the old struct-scanning path's (removed 2026-10-19; the
	// entity rows below replace them). Nothing in observability is generic
	// (TestObservabilityIsNotGeneric), so this is export data only; some rows
	// had also drifted since 2026-10-14. Every "measured" figure in this table
	// was remeasured on 2026-10-22, darwin/arm64, Go 1.27.1. The bounds were
	// kept: UpdateQuery, DeleteQuery, MetaOnce, Statement, EntityInfo and
	// entityDesc now sit a little under what the rule above would give
	// (about 4 KiB of headroom each, 23 KiB for entityDesc), which is
	// tighter, not looser.
	{"Col", "velox.Col[E, bool]", "", 18432},                        // measured 14312
	{"OrderedCol", "velox.OrderedCol[E, int64]", "", 19456},         // measured 14924
	{"NumberCol", "velox.NumberCol[E, int64]", "", 19456},           // measured 14846
	{"StringCol", "velox.StringCol[E, string]", "", 19456},          // measured 14900
	{"NullCol", "velox.NullCol[E, bool]", "", 19456},                // measured 14658
	{"NullOrderedCol", "velox.NullOrderedCol[E, int64]", "", 19456}, // measured 15270
	{"NullNumberCol", "velox.NullNumberCol[E, int64]", "", 19456},   // measured 15188
	{"NullStringCol", "velox.NullStringCol[E, string]", "", 19456},  // measured 15228
	{"JSONCol", "velox.JSONCol[E, []string]", "", 19456},            // measured 14814
	{"NullJSONCol", "velox.NullJSONCol[E, []string]", "", 19456},    // measured 15142
	{"Pred", "velox.Pred[E]", "", 5120},                             // measured 1014
	{"Order", "velox.Order[E]", "", 6144},                           // measured 1114
	{"Setter", "velox.Setter[E]", "", 8192},                         // measured 3168
	{"Query", "velox.Query[E]", "", 10240},                          // measured 5932
	{"UpdateQuery", "velox.UpdateQuery[E]", "", 9216},               // measured 5208
	{"DeleteQuery", "velox.DeleteQuery[E]", "", 9216},               // measured 5208
	{"InsertQuery", "velox.InsertQuery[E]", "", 10240},              // measured 6076
	{"Rel", "velox.Rel[E, E]", "", 9216},                            // measured 4880 (4556 before LimitEach and MaxRows; 3528 before the use site named the target)
	{"One", "velox.One[E]", "", 20480},                              // measured 15972
	{"Many", "velox.Many[E]", "", 20480},                            // measured 16044
	{"MetaOnce", "velox.MetaOnce[E]", "", 10240},                    // measured 6274
	{"Meta", "velox.Meta[E]", "", 7168},                             // measured 3118
	{"Statement", "velox.Statement", "", 5120},                      // measured 1080
	{"EntityInfo", "*velox.EntityInfo", "", 5120},                   // measured 1096
	{"ColumnInfo", "velox.ColumnInfo", "", 5120},                    // measured 20
	// The entity packages, remeasured on 2026-10-19 when the old path went,
	// on darwin/arm64, Go 1.27.1. entityDesc is the
	// minimal entity velox-gen writes: Load takes no Policy or Hook generic,
	// and the Desc's Policy and Hooks fields hold E-erased BoundPolicy and
	// BoundHooks that BindPolicy and BindHooks make in the entity's own
	// generated code, so that only an entity that declares the methods
	// instantiates those generics. entityDescUse adds a default, two
	// relations and a Use function that queries, inserts, saves, updates and
	// deletes. entityDescHooks is that with a Policy and three hooks bound
	// through the fields; the generics it instantiates are the cost of having
	// a policy and hooks (policyGrowthMax). The rows of the old struct-scanning
	// path (entity, entityUse, Shape, ColumnDef, RelDef, Schema) are gone with
	// it. entityDesc was remeasured on 2026-10-22 after Rel.LimitEach and
	// Rel.MaxRows (+518 bytes: 130782 before); its bound was kept. It was
	// remeasured on 2026-10-23, after SQLType left ColumnDesc (-38 bytes), and
	// its bound was kept again.
	{"entityDesc", "", entityDescSrc, 153600}, // measured 131266
	// entityDescUse is held like entityDescHooks: its bound is the
	// measurement plus about 1.2%, rounded up to the next KiB; remeasured on
	// 2026-10-22, darwin/arm64, Go 1.27.1, after the locking reads (about
	// +8 KiB here and in entityDescHooks), after Mutation's Conds, Affected
	// and SetAffected and the handles' ValueIn (+350 bytes; the bound held),
	// and again after the final review's fixes (782738 before), and after
	// Rel.LimitEach and Rel.MaxRows (+1404 bytes: 782716 under 792576
	// before), and after LoadEach and CountEach, whose After condition moved
	// out of Query's generic prepare into addAfter, compiled once (-26846
	// bytes: 784120 under 793600 before), and after SQLType left ColumnDesc
	// (-88 bytes; the bound held). Sum, Avg, Min and Max stay
	// functions: as Query methods they measured 800636 (before addAfter),
	// since a method of Query whose signature names NumberCol[E, V] puts that
	// in the export data of every package that uses a Query, called or not.
	{"entityDescUse", "", entityDescUseSrc, 766976}, // measured 757436
	// entityDescHooks is held tighter than the rule above: a quarter of it
	// would let it grow by 64 KiB unseen. Its bound is the measurement plus
	// about 1.2%, rounded up to the next KiB, the headroom it had before
	// (1046660 under 1058816); remeasured on 2026-10-22, darwin/arm64, Go
	// 1.27.1, after Mutation's Conds, Affected and SetAffected and the
	// handles' ValueIn (+5022 bytes: 1068930 under 1082368 before them),
	// and again when Mutation became a struct holding the mutation (-2708
	// bytes: 1073952 under 1087488 before), and after Rel.LimitEach and
	// Rel.MaxRows (+1398 bytes: 1071244 under 1084416 before), and after
	// addAfter (-26882 bytes: 1072642 under 1086464 before), and after
	// SQLType left ColumnDesc (-90 bytes; the bound held).
	{"entityDescHooks", "", entityDescHooksBound, 1058816}, // measured 1045926
	{"Policy", "velox.Policy[E]", "", 8192},                // measured 4050
	// Mutation was remeasured on 2026-10-22, when it became a struct
	// holding the mutation instead of a defined type of it (4692 before),
	// and its bound moved down by the rule above.
	{"Mutation", "velox.Mutation[E]", "", 7168}, // measured 2182
}

// policyGrowthMax bounds the bytes a Policy and three hooks add to an entity's
// package (entityDescHooks over entityDescUse): the measured growth plus
// genericObjects' margin. Bodies barely count; the cost is
// the generics velox instantiates for E (BindPolicy, BindHooks, On, the
// Mutator and Mutation methods) and the export data of the types the
// functions mention.
const policyGrowthMax = 348160 // measured 288528

// TestGenericTypesCompileSmall enforces the compile-cost rules for one
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
		out := filepath.Join(t.TempDir(), pkg+".a") // a package may be built by two subtests at once
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
				t.Errorf("%s adds %d bytes to its package, over the recorded %d", what, got, g.max)
			}
		})
	}
	t.Run("policy and hooks", func(t *testing.T) {
		t.Parallel()
		got := size(t, "entityDescHooks") - size(t, "entityDescUse")
		t.Logf("policy and hooks: +%d bytes", got)
		if got > policyGrowthMax {
			t.Errorf("a Policy and the three entity hooks add %d bytes to an entity's package, over the recorded %d", got, policyGrowthMax)
		}
	})
}

// TestObservabilityIsNotGeneric pins that nothing in Core's observability files
// is instantiated per entity.
func TestObservabilityIsNotGeneric(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range []string{"tags.go", "callers.go", "comment.go", "txhook.go"} {
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.FuncDecl:
				if d.Type.TypeParams != nil {
					t.Errorf("%s: %s is generic", fset.Position(d.Pos()), d.Name.Name)
				}
			case *ast.TypeSpec:
				if d.TypeParams != nil {
					t.Errorf("%s: %s is generic", fset.Position(d.Pos()), d.Name.Name)
				}
			}
			return true
		})
	}
}
