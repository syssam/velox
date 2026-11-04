package repocheck

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/tempmod"
)

// repoRoot is the repository's root, two directories above this file.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// entityDescSrc is the minimal entity velox-gen writes, with no Policy or
// Hooks method.
const entityDescSrc = `package p

import "github.com/syssam/velox"

type E struct{ ID int64 }

var eMeta velox.MetaOnce[E]

func (*E) VeloxMeta() *velox.Meta[E] { return eMeta.Load(eDescriptor) }

func eDescriptor() velox.Descriptor[E] {
	return velox.Descriptor[E]{
		Table:      "e",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		FieldPtr:       func(x *E, i int) any { return &x.ID },
	}
}
`

// entityDescUseSrc is an entity with a default and two relations, from a Descriptor
// literal, used by a function that queries with relations, inserts, saves,
// updates and deletes it: the generic bodies velox compiles into such a
// package must not reach reflect either.
const entityDescUseSrc = `package p

import (
	"context"

	"github.com/syssam/velox"
)

type E struct {
	ID       int64
	Name     string
	ParentID *int64
	Parent   velox.One[E]
	Kids     velox.Many[E]
}

var eMeta velox.MetaOnce[E]

func (*E) VeloxMeta() *velox.Meta[E] { return eMeta.Load(eDescriptor) }

func eDescriptor() velox.Descriptor[E] {
	return velox.Descriptor[E]{
		Table: "e",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"},
			{Field: "Name", Name: "name", Default: "none"},
			{Field: "ParentID", Name: "parent_id"},
		},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[E]("Parent", 2, 0), velox.EdgeTo[E]("Kids", 0, 2)},
		FieldPtr: func(x *E, i int) any {
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
		EdgeFieldPtr: func(x *E, i int) any {
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
	if _, err := db.Query[E]().Where(eKids.Has()).With(eParent).With(eKids.With(eParent)).Limit(1).All(ctx); err != nil {
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
// declared as methods and bound through the Descriptor's Policy and Hooks fields as
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

// entityDescHooksBound is entityDescHooksSrc with its Descriptor binding the
// methods.
var entityDescHooksBound = strings.Replace(entityDescHooksSrc, `		PrimaryKey: []int{0},
		Edges:`, `		PrimaryKey: []int{0},
		Policy:     []func() velox.BoundPolicy{func() velox.BoundPolicy { return velox.BindPolicy((*E)(nil).Policy()) }},
		Hooks:      []func() velox.BoundHooks{func() velox.BoundHooks { return velox.BindHooks((*E)(nil).Hooks()) }},
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
	// Measured on darwin/arm64, Go 1.27.x; linux/amd64 and linux/arm64 measure
	// within 3%. Each max is the measured growth plus the larger of 4096 and a
	// quarter of it, the quarter capped at 65536, rounded up to the next KiB, with
	// the measurement beside it. One ordinary method adds about 20 KiB to a handle,
	// which exceeds that margin; a velox type reaching reflect.Type adds about 134
	// KiB, which the cap keeps above the margin of the largest rows.
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
	{"Query", "velox.Query[E]", "", 10240},                          // measured 5974
	{"UpdateQuery", "velox.UpdateQuery[E]", "", 9216},               // measured 5250
	{"DeleteQuery", "velox.DeleteQuery[E]", "", 9216},               // measured 5250
	{"InsertQuery", "velox.InsertQuery[E]", "", 10240},              // measured 6118
	{"Rel", "velox.Rel[E, E]", "", 9216},                            // measured 4924
	{"One", "velox.One[E]", "", 20480},                              // measured 15972
	{"Many", "velox.Many[E]", "", 20480},                            // measured 16044
	{"MetaOnce", "velox.MetaOnce[E]", "", 10240},                    // measured 6316
	{"Meta", "velox.Meta[E]", "", 7168},                             // measured 3162
	{"Statement", "velox.Statement", "", 5120},                      // measured 1124
	{"EntityInfo", "*velox.EntityInfo", "", 5120},                   // measured 1140
	{"ColumnInfo", "velox.ColumnInfo", "", 5120},                    // measured 20
	// The entity packages. entityDescriptor is the minimal entity velox-gen writes: Load
	// takes no Policy or Hook generic, and the Descriptor's Policy and Hooks fields hold
	// E-erased BoundPolicy and BoundHooks that BindPolicy and BindHooks make in the
	// entity's own generated code, so that only an entity that declares the methods
	// instantiates those generics. entityDescUse adds a default, two relations and a
	// Use function that queries, inserts, saves, updates and deletes. entityDescHooks
	// is that with a Policy and three hooks bound through the fields; the generics it
	// instantiates are the cost of having a policy and hooks (policyGrowthMax).
	{"entityDescriptor", "", entityDescSrc, 153600}, // measured 132904
	// entityDescUse and entityDescHooks are held tighter than the rule above, which
	// would let them grow by 64 KiB unseen: each bound is the measurement plus about
	// 1.2%, rounded up to the next KiB.
	//
	// Sum, Avg, Min and Max stay functions: a method of Query whose signature names
	// NumberCol[E, V] puts that in the export data of every package that uses a
	// Query, called or not. Query.With and Rel.With stay methods: with both called,
	// functions would save under 1%.
	//
	// velox's own compiled code (not its generic bodies) must not inline what
	// the generic bodies call, such as fmt.Errorf or engine.StmtRan: an inlined
	// callee's body is carried in velox's export data, and then every
	// package's instantiations inline it too, about 20 KB more here. The
	// untyped surface (Table, TableQuery) is engine code for that reason.
	//
	// Each field of Descriptor costs an entity's package about 0.9 KB, in
	// type descriptors and relocations: the lists of Policy and Hooks and the
	// field Interceptors (GeneratedCodeIsVersion13) took entityDescUse from
	// 766634 to 770270 bytes.
	{"entityDescUse", "", entityDescUseSrc, 780288},        // measured 770270
	{"entityDescHooks", "", entityDescHooksBound, 1049600}, // measured 1038290
	{"Policy", "velox.Policy[E]", "", 8192},                // measured 4050
	{"Mutation", "velox.Mutation[E]", "", 7168},            // measured 2226
}

// policyGrowthMax bounds the bytes a Policy and three hooks add to an entity's
// package (entityDescHooks over entityDescUse): the measured growth plus
// genericObjects' margin. Bodies barely count; the cost is
// the generics velox instantiates for E (BindPolicy, BindHooks, On, the
// Mutator and Mutation methods) and the export data of the types the
// functions mention.
const policyGrowthMax = 348160 // measured 289120

// TestGenericTypesCompileSmall enforces the compile-cost rules for one
// package: it measures each row's own object cost, the bytes its package
// takes beyond a package that already imports velox, so an ordinary method on
// a generic type, a method table, or a velox type or generic body reaching
// reflect shows as growth. It does not catch a rule-2 regression through
// relations: E has none, so a relation type reaching its target's types is
// not measured here.
func TestGenericTypesCompileSmall(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds packages; run without -short")
	}
	dir := t.TempDir()
	files := map[string]string{"base/base.go": "package base\n\nimport \"github.com/syssam/velox\"\n\ntype E struct{ ID int64 }\n\nvar x velox.Dialect\n"}
	for _, g := range genericObjects {
		src := g.src
		if src == "" {
			src = fmt.Sprintf("package p\n\nimport \"github.com/syssam/velox\"\n\ntype E struct{ ID int64 }\n\nvar x %s\n", g.decl)
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
