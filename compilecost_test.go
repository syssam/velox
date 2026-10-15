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

// entityPolicySrc is entityUseSrc with a Policy and all three entity hooks
// declared in its Schema: what they add to an entity's package is measured
// against entityUseSrc (policy design §7).
var entityPolicySrc = strings.Replace(entityUseSrc, `	s.Many(&x.Kids).ForeignKey(func(k *E) **int64 { return &k.ParentID })
`, `	s.Many(&x.Kids).ForeignKey(func(k *E) **int64 { return &k.ParentID })
	s.Policy(velox.Policy[E]{
		Read:   func(context.Context) (velox.Pred[E], error) { return eName.EQ("r"), nil },
		Write:  func(context.Context) (velox.Pred[E], error) { return velox.Pred[E]{}, nil },
		Insert: func(_ context.Context, x *E) error { return nil },
	})
	s.BeforeInsert(func(_ context.Context, x *E) error { x.Name = "i"; return nil })
	s.BeforeUpdate(func(_ context.Context, m *velox.Mutation[E]) error { m.Set(eName.Set("u")); return nil })
	s.BeforeDelete(func(_ context.Context, m *velox.Mutation[E]) error { m.Where(eID.EQ(1)); return nil })
`, 1)

// entityDescSrc is entitySrc on the Desc path: the minimal entity velox-gen
// writes, with no Policy or Hooks method.
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

// entityDescUseSrc is entityUseSrc on the Desc path: the same columns,
// default, relations and Use function, from a Desc literal.
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
		Relations:  []velox.Relation{velox.EdgeTo[E]("Parent", 2, 0), velox.EdgeTo[E]("Kids", 0, 2)},
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
		RelAddr: func(x *E, i int) any {
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

// entityDescHooksSrc is entityDescUseSrc with a Policy and three hooks,
// declared as methods and bound through the Desc's Policy and Hooks fields as
// velox-gen binds them.
var entityDescHooksSrc = strings.Replace(entityDescUseSrc, `var (
	eID `, `func (*E) Policy() velox.Policy[E] {
	return velox.Policy[E]{
		Read:   func(context.Context) (velox.Pred[E], error) { return eName.EQ("r"), nil },
		Write:  func(context.Context) (velox.Pred[E], error) { return velox.Pred[E]{}, nil },
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
		Relations:`, `		Tenant:     -1,
		Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*E)(nil).Policy()) },
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*E)(nil).Hooks()) },
		Relations:`, 1)

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
	// handles, 3.3 KiB for entity and 7.75 KiB (7,938 bytes) for entityUse. Nothing in
	// observability is generic (TestObservabilityIsNotGeneric), so this is
	// export data only; some rows had also drifted since 2026-10-14.
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
	{"Query", "velox.Query[E]", "", 10240},                          // measured 5718
	{"UpdateQuery", "velox.UpdateQuery[E]", "", 9216},               // measured 4950
	{"DeleteQuery", "velox.DeleteQuery[E]", "", 9216},               // measured 4950
	{"InsertQuery", "velox.InsertQuery[E]", "", 10240},              // measured 5850
	{"Rel", "velox.Rel[E, E]", "", 8192},                            // measured 3528
	{"One", "velox.One[E]", "", 20480},                              // measured 15972
	{"Many", "velox.Many[E]", "", 20480},                            // measured 16044
	{"MetaOnce", "velox.MetaOnce[E]", "", 10240},                    // measured 6134
	{"Shape", "velox.Shape[E]", "", 8192},                           // measured 3572
	{"Meta", "velox.Meta[E]", "", 7168},                             // measured 2978
	{"ColumnDef", "velox.ColumnDef[int64]", "", 7168},               // measured 2194
	{"RelDef", "velox.RelDef[E]", "", 7168},                         // measured 2344
	{"Statement", "velox.Statement", "", 5120},                      // measured 940
	{"EntityInfo", "*velox.EntityInfo", "", 5120},                   // measured 956
	{"Schema", "*velox.Schema", "", 5120},                           // measured 946
	{"ColumnInfo", "velox.ColumnInfo", "", 5120},                    // measured 20
	// The entity packages, remeasured on 2026-10-15 after policy 2. An
	// entity's package carries the export data of every velox type its API
	// reaches, and Schema's Policy, BeforeInsert, BeforeUpdate and
	// BeforeDelete take functions of a context.Context (whose Deadline brings
	// time.Time and its methods, about 9.5 KiB) and a Mutation[E] (which holds
	// a *Statement, about 2.3 KiB); those four methods with Policy[E] and
	// Mutation[E] add about 8 KiB more. entityUse also compiles the insert
	// path's new generic steps (oneStatement, prepareMany, batch, check).
	// Remeasured on 2026-10-15 after schema plan 2's typed hooks: EntityInfo
	// reaches the hooks' mutation, and entityUse compiles the hooks branch of
	// each write and insertBatches.
	{"entity", "", entitySrc, 154624},       // measured 132168 (134630 when last recorded, 127904 before typed hooks, 123318 before that, 119972 before observability, 98878 before policy 2)
	{"entityUse", "", entityUseSrc, 784384}, // measured 759910 (761664 when last recorded, 731746 before typed hooks, 717868 before that, 709930 before observability, 673936 before policy 2)
	// The Desc path, remeasured on 2026-10-15 after Desc gained Policy and
	// Hooks. entityDesc is entity on the Desc path: with nothing bound it is
	// 8 KiB under entity (Load takes no Policy or Hook generic). It was
	// 237706 when Load checked E for Policy and Hooks methods through
	// interfaces, and 157092 with an unsafe conversion of method expressions:
	// 80 KiB of Policy and Hook instantiations in every entity's package,
	// which an entity without the methods never used. The fields now hold
	// E-erased BoundPolicy and BoundHooks, made by BindPolicy and BindHooks in
	// the entity's own generated code, so that only an entity that declares
	// the methods instantiates those generics. entityDescUse is entityUse on
	// Desc (854600 with the interface check). entityDescHooks is that with a
	// Policy and three hooks bound through the fields (990872 with the
	// interface check, from the same source without the bindings); the
	// generics it instantiates are the cost of having a policy and hooks.
	// Their maxima follow the rule above.
	{"entityDesc", "", entityDescSrc, 155648},              // measured 124438
	{"entityDescUse", "", entityDescUseSrc, 787456},        // measured 721136
	{"entityDescHooks", "", entityDescHooksBound, 1065984}, // measured 999852
	{"Policy", "velox.Policy[E]", "", 8192},                // measured 4050
	{"Mutation", "velox.Mutation[E]", "", 9216},            // measured 3572
}

// policyGrowthMax bounds the bytes a Policy and the three entity hooks add to
// an entity's package (entityPolicySrc over entityUseSrc): the measured growth
// plus genericObjects' margin (policy design §7). On 2026-10-15, darwin/arm64,
// Go 1.27.1, they added 98984 when the maximum was set, and 104764 when last
// measured (it had drifted before the Desc path's fields), still inside it. Measured in packages with a shorter import path
// they add 92266, of which the six functions alone, assigned to variables
// instead of declared, take 41948, and velox's generic Policy, BeforeInsert,
// BeforeUpdate, BeforeDelete and mutationHook, instantiated for E with their
// closures, the other 50318. Bodies barely count (a Read returning Pred[E]{}
// costs as much as one returning a condition); every instantiated symbol's
// name spells out E's shape and package path.
const policyGrowthMax = 123904 // measured 104764 (98984 when set)

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
	if entityPolicySrc == entityUseSrc {
		t.Fatal("entityPolicySrc adds nothing to entityUseSrc: its anchor, the Many line of E's Schema, is not in entityUseSrc")
	}
	files["entityPolicy/p.go"] = entityPolicySrc
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
				t.Errorf("%s adds %d bytes to its package, over the recorded %d (design §3)", what, got, g.max)
			}
		})
	}
	t.Run("policy and hooks", func(t *testing.T) {
		t.Parallel()
		got := size(t, "entityPolicy") - size(t, "entityUse")
		t.Logf("policy and hooks: +%d bytes", got)
		if got > policyGrowthMax {
			t.Errorf("a Policy and the three entity hooks add %d bytes to an entity's package, over the recorded %d (policy design §7)", got, policyGrowthMax)
		}
	})
}

// TestObservabilityIsNotGeneric pins observability design success criterion
// 5: nothing in Core's observability files is instantiated per entity.
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
