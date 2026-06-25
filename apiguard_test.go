package velox_test

import (
	"flag"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	// Blank-import every guarded package. This test renders the API surface from
	// source at runtime (via go/packages), so without a compile-time dependency
	// `go test`'s result cache would NOT invalidate when a guarded package's API
	// changes — the guard could cache-pass straight through a breaking change in
	// CI. These imports tie the test binary's build inputs to the guarded
	// packages, forcing a re-run whenever any of them changes. Keep this list in
	// sync with guardedPackages below.
	_ "github.com/syssam/velox"
	_ "github.com/syssam/velox/dialect/sql"
	_ "github.com/syssam/velox/privacy"
	_ "github.com/syssam/velox/runtime"
	_ "github.com/syssam/velox/schema/edge"
	_ "github.com/syssam/velox/schema/field"
	_ "github.com/syssam/velox/schema/index"
	_ "github.com/syssam/velox/schema/mixin"
)

// updateAPI rewrites the API-guard golden files instead of comparing against
// them. Run `go test . -run TestPublicAPIGuard -update-api` after an
// intentional public-API change.
var updateAPI = flag.Bool("update-api", false, "update public API guard golden files")

// guardedPackages are the consumer-facing packages whose exported surface every
// velox user's schema and wiring code depends on. COMPATIBILITY.md promises
// these are stable within a major version.
//
// This list mirrors the package set checked by the advisory `apidiff` job in
// .github/workflows/ci.yml. The two layers are complementary, not redundant:
//   - apidiff: semantic Go-compatibility diff vs the PR base; PR-only; advisory
//     (warns, never blocks) — good for "is this change breaking?" review.
//   - this guard: golden snapshot; runs in `go test ./...` on every push
//     (including direct pushes to main, which the PR-only job misses); BLOCKS.
//     The forcing function that makes a breaking change deliberate (you must run
//     -update-api and the diff shows up in review).
//
// See COMPATIBILITY.md § Enforcement.
var guardedPackages = []string{
	".",              // root: Schema, Mixin, Field/Edge/Index, Hook, Interceptor, Querier, ...
	"./privacy",      // policy primitives (Allow/Deny/Skip, QueryRule/MutationRule)
	"./schema/field", // field builders (every schema imports these)
	"./schema/edge",  // edge builders
	"./schema/index", // index builders
	"./schema/mixin", // mixin helpers
	"./dialect/sql",  // driver construction + wrappers (Open, OpenDB, Stats/Log drivers)
	"./runtime",      // runtime helpers exported for generated code + advanced users
}

func TestPublicAPIGuard(t *testing.T) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports | packages.NeedDeps,
	}
	pkgs, err := packages.Load(cfg, guardedPackages...)
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	var loadErrs bool
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			t.Errorf("loading %s: %v", p.PkgPath, e)
			loadErrs = true
		}
	})
	require.False(t, loadErrs, "package load errors; cannot evaluate API surface")

	dir := filepath.Join("testdata", "apiguard")
	if *updateAPI {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	for _, p := range pkgs {
		rendered := renderAPI(p)
		golden := filepath.Join(dir, sanitizePkgPath(p.PkgPath)+".txt")

		if *updateAPI {
			require.NoError(t, os.WriteFile(golden, []byte(rendered), 0o644))
			continue
		}

		want, err := os.ReadFile(golden)
		if os.IsNotExist(err) {
			t.Errorf("%s: missing API golden %q — run: go test . -run TestPublicAPIGuard -update-api", p.PkgPath, golden)
			continue
		}
		require.NoError(t, err)

		if string(want) != rendered {
			assert.Equal(t, string(want), rendered,
				"PUBLIC API CHANGED for %s — this is a breaking change per COMPATIBILITY.md.\n"+
					"If the change is intentional, regenerate the golden:\n"+
					"    go test . -run TestPublicAPIGuard -update-api\n"+
					"and call it out in the changelog.", p.PkgPath)
		}
	}
}

// renderAPI produces a deterministic, unexported-noise-free text snapshot of a
// package's exported surface: top-level funcs/consts/vars/types, plus the
// exported method set, exported struct fields, and interface methods of each
// exported type. Parameter names are omitted (renaming a param is not breaking);
// types are not (changing a type is).
func renderAPI(pkg *packages.Package) string {
	qual := types.RelativeTo(pkg.Types)
	scope := pkg.Types.Scope()
	var lines []string

	for _, name := range scope.Names() {
		if !token.IsExported(name) {
			continue
		}
		switch o := scope.Lookup(name).(type) {
		case *types.Func:
			sig := o.Type().(*types.Signature)
			lines = append(lines, "func "+name+typeParamsString(sig.TypeParams(), qual)+sigString(sig, qual))
		case *types.Const:
			lines = append(lines, "const "+name+" "+types.TypeString(o.Type(), qual))
		case *types.Var:
			lines = append(lines, "var "+name+" "+types.TypeString(o.Type(), qual))
		case *types.TypeName:
			lines = append(lines, renderTypeName(name, o, qual)...)
		}
	}

	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

func renderTypeName(name string, o *types.TypeName, qual types.Qualifier) []string {
	if o.IsAlias() {
		return []string{"type " + name + " = " + types.TypeString(o.Type(), qual)}
	}
	named, ok := o.Type().(*types.Named)
	if !ok {
		return []string{"type " + name + " " + types.TypeString(o.Type(), qual)}
	}
	underlying := named.Underlying()
	out := []string{"type " + name + typeParamsString(named.TypeParams(), qual) + " " + kindWord(underlying, qual)}

	switch u := underlying.(type) {
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			if f := u.Field(i); f.Exported() {
				out = append(out, "  field "+name+"."+f.Name()+" "+types.TypeString(f.Type(), qual))
			}
		}
	case *types.Interface:
		for i := 0; i < u.NumMethods(); i++ {
			if m := u.Method(i); m.Exported() {
				out = append(out, "  method "+name+"."+m.Name()+sigString(m.Type().(*types.Signature), qual))
			}
		}
	}

	// Concrete (non-interface) named types expose a method set on the value
	// and/or pointer receiver. Dedup across both so a pointer method isn't
	// listed twice.
	if _, isIface := underlying.(*types.Interface); !isIface {
		seen := make(map[string]bool)
		for _, recv := range []types.Type{named, types.NewPointer(named)} {
			ms := types.NewMethodSet(recv)
			for i := 0; i < ms.Len(); i++ {
				m := ms.At(i).Obj()
				if !m.Exported() || seen[m.Name()] {
					continue
				}
				seen[m.Name()] = true
				out = append(out, "  method "+name+"."+m.Name()+sigString(m.Type().(*types.Signature), qual))
			}
		}
	}
	return out
}

func kindWord(u types.Type, qual types.Qualifier) string {
	switch u.(type) {
	case *types.Struct:
		return "struct"
	case *types.Interface:
		return "interface"
	default:
		return types.TypeString(u, qual)
	}
}

// sigString renders a signature as "(paramTypes) resultTypes", names omitted,
// variadic-aware.
func sigString(sig *types.Signature, qual types.Qualifier) string {
	var b strings.Builder
	b.WriteByte('(')
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		if sig.Variadic() && i == params.Len()-1 {
			b.WriteString("...")
			b.WriteString(types.TypeString(params.At(i).Type().(*types.Slice).Elem(), qual))
		} else {
			b.WriteString(types.TypeString(params.At(i).Type(), qual))
		}
	}
	b.WriteByte(')')

	switch res := sig.Results(); res.Len() {
	case 0:
	case 1:
		b.WriteByte(' ')
		b.WriteString(types.TypeString(res.At(0).Type(), qual))
	default:
		b.WriteString(" (")
		for i := 0; i < res.Len(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(types.TypeString(res.At(i).Type(), qual))
		}
		b.WriteByte(')')
	}
	return b.String()
}

// typeParamsString renders a type-parameter list as "[T constraint, ...]" (empty
// for non-generic symbols), so a change to a generic constraint
// (e.g. [T any] -> [T comparable]) is captured as the breaking change it is.
func typeParamsString(tp *types.TypeParamList, qual types.Qualifier) string {
	if tp == nil || tp.Len() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < tp.Len(); i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		p := tp.At(i)
		b.WriteString(p.Obj().Name())
		b.WriteByte(' ')
		b.WriteString(types.TypeString(p.Constraint(), qual))
	}
	b.WriteByte(']')
	return b.String()
}

func sanitizePkgPath(p string) string {
	return strings.NewReplacer("/", "_", ".", "_").Replace(p)
}
