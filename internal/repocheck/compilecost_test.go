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
// declared as methods that name columns by name.
var entityDescHooksSrc = strings.Replace(entityDescUseSrc, `var (
	eID `, `func (*E) Policy() velox.Policy {
	return velox.Policy{
		Read:   func(context.Context) (velox.Predicate, error) { return velox.FieldEQ("name", "r"), nil },
		Write:  func(context.Context) (velox.Predicate, error) { return velox.Allow(), nil },
		Insert: func(_ context.Context, r velox.Row) error { _ = r.Value("name"); return nil },
	}
}

func (*E) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(func(_ context.Context, m *velox.Mutation) error { return m.Fill("name", "i") }, velox.OpInsert),
		velox.On(func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				_ = m.DB()
				if err := m.Set("name", "u"); err != nil {
					return err
				}
				return next.Mutate(ctx, m)
			})
		}, velox.OpUpdate),
		velox.Before(func(_ context.Context, m *velox.Mutation) error {
			m.Where(velox.FieldEQ("id", 1))
			return nil
		}, velox.OpDelete),
	}
}

var (
	eID `, 1)

// entityDescHooksBound is entityDescHooksSrc with its Descriptor naming the
// value that declares the methods, as a hand-written entity names itself.
var entityDescHooksBound = strings.Replace(entityDescHooksSrc, `		PrimaryKey: []int{0},
		Edges:`, `		PrimaryKey: []int{0},
		Schema:     (*E)(nil),
		Edges:`, 1)

// entityDescMixinSrc is entityDescUseSrc with entityDescHooksSrc's Policy
// and three hooks declared by a mixin M that a schema S lists, and S named
// as velox-gen names a schema. S and M live in the entity's package here,
// where velox-gen's live in the schema package.
var entityDescMixinSrc = strings.Replace(strings.Replace(strings.Replace(entityDescUseSrc, `var (
	eID `, `type S struct{}

func (S) Mixin() []schema.Mixin { return []schema.Mixin{M{}} }

type M struct{ mixin.Schema }

func (M) Policy() velox.Policy {
	return velox.Policy{
		Read:   func(context.Context) (velox.Predicate, error) { return velox.FieldEQ("name", "r"), nil },
		Write:  func(context.Context) (velox.Predicate, error) { return velox.Allow(), nil },
		Insert: func(_ context.Context, r velox.Row) error { _ = r.Value("name"); return nil },
	}
}

func (M) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(func(_ context.Context, m *velox.Mutation) error { return m.Fill("name", "i") }, velox.OpInsert),
		velox.On(func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				_ = m.DB()
				if err := m.Set("name", "u"); err != nil {
					return err
				}
				return next.Mutate(ctx, m)
			})
		}, velox.OpUpdate),
		velox.Before(func(_ context.Context, m *velox.Mutation) error {
			m.Where(velox.FieldEQ("id", 1))
			return nil
		}, velox.OpDelete),
	}
}

var (
	eID `, 1), `		PrimaryKey: []int{0},
		Edges:`, `		PrimaryKey: []int{0},
		Schema:     S{},
		Edges:`, 1), `	"github.com/syssam/velox"
`, `	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/mixin"
`, 1)

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
	{"Col", "velox.Col[E, bool]", "", 18432},                        // measured 14208
	{"OrderedCol", "velox.OrderedCol[E, int64]", "", 19456},         // measured 14820
	{"NumberCol", "velox.NumberCol[E, int64]", "", 19456},           // measured 14756
	{"StringCol", "velox.StringCol[E, string]", "", 19456},          // measured 14796
	{"NullCol", "velox.NullCol[E, bool]", "", 19456},                // measured 14554
	{"NullOrderedCol", "velox.NullOrderedCol[E, int64]", "", 19456}, // measured 15166
	{"NullNumberCol", "velox.NullNumberCol[E, int64]", "", 19456},   // measured 15084
	{"NullStringCol", "velox.NullStringCol[E, string]", "", 19456},  // measured 15124
	{"JSONCol", "velox.JSONCol[E, []string]", "", 19456},            // measured 14710
	{"NullJSONCol", "velox.NullJSONCol[E, []string]", "", 19456},    // measured 15038
	{"Pred", "velox.Pred[E]", "", 5120},                             // measured 1136
	{"Order", "velox.Order[E]", "", 6144},                           // measured 2964
	{"Setter", "velox.Setter[E]", "", 8192},                         // measured 3194
	{"Query", "velox.Query[E]", "", 10240},                          // measured 6342
	{"UpdateQuery", "velox.UpdateQuery[E]", "", 9216},               // measured 7822
	{"DeleteQuery", "velox.DeleteQuery[E]", "", 9216},               // measured 6966
	{"InsertQuery", "velox.InsertQuery[E]", "", 10240},              // measured 8102
	{"Rel", "velox.Rel[E, E]", "", 9216},                            // measured 6848
	{"One", "velox.One[E]", "", 20480},                              // measured 15856
	{"Many", "velox.Many[E]", "", 20480},                            // measured 15930
	{"MetaOnce", "velox.MetaOnce[E]", "", 10240},                    // measured 8074
	{"Meta", "velox.Meta[E]", "", 7168},                             // measured 4906
	{"Statement", "velox.Statement", "", 5120},                      // measured 1412
	{"EntityInfo", "*velox.EntityInfo", "", 5120},                   // measured 1428
	{"ColumnInfo", "velox.ColumnInfo", "", 5120},                    // measured 80
	// The entity packages. entityDescriptor is the minimal entity velox-gen writes.
	// The Descriptor names the value that declares the behaviour in its field
	// Schema, an any (GeneratedCodeIsVersion17), and velox reads the untyped
	// velox.Policy, []velox.Hook and []velox.Interceptor from it, so an entity's
	// behaviour instantiates no generic for it. entityDescUse adds a default, two
	// relations and a Use function that queries, inserts, saves, updates and
	// deletes. entityDescHooks is that with a Policy and three hooks named through
	// the field (policyGrowthMax).
	//
	// The entity packages are held tighter than the rule above, which would let
	// them grow by 64 KiB unseen: each bound is the measurement plus about 1.2%,
	// rounded up to the next KiB.
	//
	// The untyped fields name velox.Hook, whose Mutation's DB method names *DB,
	// so the export data of every entity package describes DB's methods:
	// entityDescriptor, which used no DB, took 284684 bytes, up from 143458
	// (GeneratedCodeIsVersion15). A package that uses a DB, as entityDescUse and
	// every program's do, already describes them: entityDescUse grew from 767952
	// to 772578 bytes, 0.6%. A Policy and three hooks, which instantiated
	// Policy[E], Hook[E], Mutator[E] and Mutation[E] with their methods, now add
	// 88788 bytes over entityDescUse, down from 287980: entityDescHooks took
	// 861366 bytes, down from 1055932, and entityDescMixin, whose mixin's methods
	// are no longer generic, 871618, down from 1130512.
	//
	// MetaOnce.Load no longer retries a build that panics with a
	// NotLinkedError (ADR 0039), and is not inlined: entityDescriptor took
	// 282970 bytes, entityDescUse 770188, entityDescHooks 858964 and
	// entityDescMixin 869212, each about 2.3 KB less. Inlined, Load took
	// entityDescriptor to 143080 bytes but gen of 100 entities with a Policy
	// each from 24.5 to 25.8 MB, as its closure was compiled into every
	// entity's VeloxMeta.
	//
	// The field Schema is an any, where the lists named velox.Hook, so an
	// entity package that uses no DB no longer describes DB's methods
	// (GeneratedCodeIsVersion17): entityDescriptor took 136078 bytes, down from
	// 287036 in the same worktree, entityDescUse 763846, down from 774634, and
	// entityDescHooks 847422, down from 863410.
	{"entityDescriptor", "", entityDescSrc, 141312}, // measured 139274 (Descriptor.Links, GeneratedCodeIsVersion21; was 136404)
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
	//
	// BindPolicy, BindHooks and BindInterceptors bind a list of functions
	// (GeneratedCodeIsVersion14): each instance holds a loop and a closure,
	// which took entityDescHooks from 1037930 to 1058404 bytes, 20 KB, with
	// them not inlined (inlined, 1071690). velox-gen writes that binding in
	// package runtime, which exports nothing, and gen no longer holds the
	// closures that bound each method, nor a second struct of the bound
	// fields.
	//
	// Removing Descriptor.Scope and velox.Scoper (GeneratedCodeIsVersion15)
	// took entityDescriptor from 142268 to 141368 bytes, entityDescUse from
	// 770560 to 769648, entityDescHooks from 1058404 to 1057508 and
	// entityDescMixin from 1132944 to 1132078; the bounds were lowered.
	//
	// A column handle's Select, InSelect and NotInSelect (ADR 0057) took
	// entityDescUse from 773116 to 773212 bytes: a generic method costs a
	// package that names its type only its declaration.
	//
	// Query.Paginate (ADR 0059) and DB.Rows (ADR 0060) took entityDescHooks
	// from 856672 to 858774 bytes and entityDescMixin from 901726 to 903830:
	// a package that names DB or Query in its exports carries their method
	// declarations. Rows returns its own type, RawRows: an iter.Seq2[RawRow,
	// error] cost each package holding a *DB its instantiation, 3382 bytes
	// more for one variable of UpdateQuery, DeleteQuery or InsertQuery.
	//
	// Projections (Query.Select, SelectRaw, GroupBy, Having and Rows, Term and
	// the aggregate methods of the handles) took entityDescUse from 781242 to
	// 811916 bytes, entityDescHooks from 858774 to 895406 and entityDescMixin
	// from 903830 to 940492, about 4% each. A handle variable's row did not
	// move: a method on every handle type, as a typed Select would need, cost
	// about 10 KB in each.
	// The Query and Statement rows grew by 124 bytes each. About 12 KB of
	// the growth is in the engine's types and bodies that the entity's
	// package reaches, and the rest in the new methods of Query.
	{"entityDescUse", "", entityDescUseSrc, 818176},       // measured 811916
	{"entityDescHooks", "", entityDescHooksBound, 903168}, // measured 895406
	// entityDescMixin is entityDescHooks with the Policy and hooks a mixin's,
	// which a schema S lists. S, M and the schema and mixin types they name
	// compile into this package: 45232 bytes more than an entity's own
	// methods, up from 10248 when the Descriptor listed M's method values.
	// velox-gen's S and M live in the schema package, so gen does not pay
	// it: at N=100 with a Policy each, gen's archive took 23896614 bytes,
	// down from 24455668, and the schema's 5008904, up from 4959122.
	{"entityDescMixin", "", entityDescMixinSrc, 948224}, // measured 940492
}

// policyGrowthMax bounds the bytes a Policy and three hooks add to an entity's
// package (entityDescHooks over entityDescUse): the measured growth plus
// about 1.2%, rounded up to the next KiB. velox instantiates no generic for
// them; the cost is the methods' bodies and closures, and the export data of
// the types they mention.
const policyGrowthMax = 84992 // measured 83576

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
		// -trimpath: without it the archive holds the checkout's path, so a
		// size depends on where the repository is.
		if _, err := tempmod.Go(dir, "build", "-trimpath", "-o", out, "./"+pkg); err != nil {
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
