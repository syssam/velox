package velox_test

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	// Tie the test binary to the guarded packages so `go test`'s result cache
	// invalidates when any of them changes (see the matching comment in
	// apiguard_test.go). The guard itself reads source via go/packages.
	_ "github.com/syssam/velox/compiler/gen"
	_ "github.com/syssam/velox/contrib/graphql"
	_ "github.com/syssam/velox/runtime"
)

// The dead-API guard catches velox's most repeated defect: an identifier that
// is declared (and often assigned at init) but that nothing in production code
// ever reads, so the feature it stands for silently does nothing while tests
// assert only that the value was stored. See CONTRIBUTING.md § "Dead-API
// Guard" for how to respond when it fails.
//
// "Reader" means a use in a non-test file anywhere in the root module —
// including the generated code of tests/integration and examples/realworld,
// which is how generator output that references runtime.X counts as a reader.
// Rules:
//
//	(a) every gen.Feature var must be consulted by a generator (a use of the var
//	    outside feature.go, or its Name passed as a literal to
//	    FeatureEnabled/HasFeature) unless its doc comment says "Deprecated:".
//	(b) every field of contrib/graphql.Annotation must be read outside
//	    annotation.go, or by an accessor in annotation.go that itself has a
//	    live caller. Writes (composite-literal keys, assignment targets) and
//	    reads inside Merge do not count.
//	(c) every package-level registry var that a runtime function writes
//	    (RegisterX, SetX, ...) must be read by a function that has a caller.
//	(d) every exported top-level func, type, var and const in runtime (which
//	    exists to serve generated code) must be reachable: used from another
//	    package, or from the declaration of a live runtime identifier.
//	(e) no field of a struct in generated code (tests/integration,
//	    examples/realworld) may be written only inside clone(): such a field
//	    is copied between queries but never populated.
//
// Exceptions live in testdata/deadapi/allowlist.txt, one "<rule> <pkg>.<Name>"
// per line with a reason. Keep it short; an allowlist that grows is a guard
// that has stopped working.

const (
	modulePath       = "github.com/syssam/velox"
	runtimePkgPath   = modulePath + "/runtime"
	genPkgPath       = modulePath + "/compiler/gen"
	graphqlPkgPath   = modulePath + "/contrib/graphql"
	deadAPIAllowlist = "testdata/deadapi/allowlist.txt"
)

// generatedFixtures must exist for the guard to see generator output as
// readers. Both are gitignored; CI generates them before `go test ./...`.
var generatedFixtures = []string{
	"tests/integration/entity",
	"examples/realworld/velox/entity",
}

func TestDeadAPIGuard(t *testing.T) {
	for _, dir := range generatedFixtures {
		if _, err := os.Stat(dir); err != nil {
			msg := fmt.Sprintf("dead-API guard needs generated fixtures (%s missing); run:\n"+
				"    go run tests/integration/generate.go && (cd examples/realworld && go run generate.go)", dir)
			if os.Getenv("CI") != "" {
				t.Fatal(msg)
			}
			t.Skip(msg)
		}
	}

	idx := loadDeadAPIIndex(t)
	allow := readDeadAPIAllowlist(t)

	var dead []string
	report := func(rule, id string, pos token.Position, why string) {
		if allow[rule+" "+id] {
			allow[rule+" "+id] = false // mark as used
			return
		}
		dead = append(dead, fmt.Sprintf("  (%s) %s — %s\n        declared at %s", rule, id, why, relPos(pos)))
	}

	idx.checkFeatures(t, report)
	idx.checkAnnotationFields(t, report)
	// A reader allowlisted as user-facing API under rule (d) keeps the
	// registry it reads alive.
	idx.checkRegistries(t, report, func(name string) bool { _, ok := allow["d runtime."+name]; return ok })
	idx.checkRuntimeExports(t, report)
	idx.checkClonedOnlyFields(report)

	for key, unused := range allow {
		if unused {
			dead = append(dead, fmt.Sprintf("  stale allowlist entry %q in %s — the identifier is read now (or gone); delete the line", key, deadAPIAllowlist))
		}
	}

	if len(dead) > 0 {
		sort.Strings(dead)
		t.Errorf("DEAD API: %d identifier(s) have no production reader:\n%s\n\n"+
			"Each one is declared but nothing outside tests reads it, so whatever it stands\n"+
			"for silently does nothing. Fix by, in order of preference:\n"+
			"  1. deleting it (add a CHANGELOG [Unreleased] Removed entry; run\n"+
			"     `go test . -run TestPublicAPIGuard -update-api` if it was public API);\n"+
			"  2. wiring it up, with a test that asserts on behavior, not on storage;\n"+
			"  3. for a gen.Feature kept only for config compatibility, a \"Deprecated:\" doc line;\n"+
			"  4. as a last resort, a line in %s: \"<rule> <pkg>.<Name>  # reason\".",
			len(dead), strings.Join(dead, "\n"), deadAPIAllowlist)
	}
}

// deadAPIIndex holds every non-test package of the root module with syntax
// and type information, plus a reverse index of identifier uses.
type deadAPIIndex struct {
	fset *token.FileSet
	pkgs map[string]*packages.Package
	// uses maps an object to the positions (in non-test files) that use it.
	uses map[types.Object][]useSite
}

type useSite struct {
	pos   token.Pos
	file  string        // absolute file name
	write bool          // composite-literal key or assignment target
	fn    *ast.FuncDecl // enclosing top-level function, nil outside one
}

func loadDeadAPIIndex(t *testing.T) *deadAPIIndex {
	t.Helper()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
	}
	pkgs, err := packages.Load(cfg, "./...")
	require.NoError(t, err)

	idx := &deadAPIIndex{
		fset: cfg.Fset,
		pkgs: map[string]*packages.Package{},
		uses: map[types.Object][]useSite{},
	}
	if idx.fset == nil && len(pkgs) > 0 {
		idx.fset = pkgs[0].Fset
	}
	var loadErrs bool
	for _, p := range pkgs {
		for _, e := range p.Errors {
			t.Errorf("loading %s: %v", p.PkgPath, e)
			loadErrs = true
		}
		idx.pkgs[p.PkgPath] = p
	}
	require.False(t, loadErrs, "package load errors; cannot evaluate readers")

	for _, p := range pkgs {
		for _, f := range p.Syntax {
			name := p.Fset.File(f.Pos()).Name()
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			idx.indexFile(p, f, name)
		}
	}
	return idx
}

// indexFile records every identifier use in f, noting whether it is a write.
func (idx *deadAPIIndex) indexFile(p *packages.Package, f *ast.File, name string) {
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj := p.TypesInfo.Uses[id]
		if obj == nil {
			return true
		}
		obj = canonical(obj)
		idx.uses[obj] = append(idx.uses[obj], useSite{pos: id.Pos(), file: name, write: isWrite(stack), fn: enclosingFunc(stack)})
		return true
	})
}

// canonical maps instantiated generic objects back to their origin.
func canonical(obj types.Object) types.Object {
	switch o := obj.(type) {
	case *types.Func:
		return o.Origin()
	case *types.Var:
		return o.Origin()
	}
	return obj
}

// isWrite reports whether the identifier at the end of path is a
// composite-literal key or the target of an assignment.
func isWrite(path []ast.Node) bool {
	id := path[len(path)-1]
	child := id
	for i := len(path) - 2; i >= 0; i-- {
		switch p := path[i].(type) {
		case *ast.KeyValueExpr:
			return p.Key == child
		case *ast.SelectorExpr:
			if p.Sel != child {
				return false
			}
			child = p
			continue
		case *ast.AssignStmt:
			for _, l := range p.Lhs {
				if l == child {
					return true
				}
			}
			return false
		case *ast.IndexExpr:
			if p.X != child {
				return false
			}
			child = p
			continue
		}
		return false
	}
	return false
}

// enclosingFunc returns the function declaration on the path to a use.
func enclosingFunc(path []ast.Node) *ast.FuncDecl {
	for i := len(path) - 1; i >= 0; i-- {
		if fd, ok := path[i].(*ast.FuncDecl); ok {
			return fd
		}
	}
	return nil
}

func (idx *deadAPIIndex) pkg(t *testing.T, path string) *packages.Package {
	t.Helper()
	p := idx.pkgs[path]
	require.NotNil(t, p, "guarded package %s not loaded", path)
	return p
}

func (idx *deadAPIIndex) position(pos token.Pos) token.Position { return idx.fset.Position(pos) }

// ---------------------------------------------------------------------------
// (a) gen.Feature vars
// ---------------------------------------------------------------------------

func (idx *deadAPIIndex) checkFeatures(t *testing.T, report func(rule, id string, pos token.Position, why string)) {
	p := idx.pkg(t, genPkgPath)
	featureType := p.Types.Scope().Lookup("Feature")
	require.NotNil(t, featureType, "gen.Feature type not found")

	// Names passed as string literals to a feature check.
	literalChecks := map[string]bool{}
	for _, lp := range idx.pkgs {
		for _, f := range lp.Syntax {
			if strings.HasSuffix(lp.Fset.File(f.Pos()).Name(), "_test.go") {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "FeatureEnabled" && sel.Sel.Name != "HasFeature") {
					return true
				}
				if tv, ok := lp.TypesInfo.Types[call.Args[0]]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
					literalChecks[constant.StringVal(tv.Value)] = true
				}
				return true
			})
		}
	}

	for _, f := range p.Syntax {
		ast.Inspect(f, func(n ast.Node) bool {
			gd, ok := n.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				return true
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					obj := p.TypesInfo.Defs[name]
					if obj == nil || !types.Identical(obj.Type(), featureType.Type()) {
						continue
					}
					if vs.Doc != nil && strings.Contains(vs.Doc.Text(), "Deprecated:") {
						continue
					}
					declFile := idx.position(name.Pos()).Filename
					live := false
					for _, u := range idx.uses[obj] {
						if u.file != declFile {
							live = true
							break
						}
					}
					if !live && i < len(vs.Values) {
						if fname := featureName(p, vs.Values[i]); fname != "" && literalChecks[fname] {
							live = true
						}
					}
					if !live {
						report("a", "gen."+name.Name, idx.position(name.Pos()),
							"feature flag is never consulted by a generator (no FeatureEnabled/HasFeature/spec use)")
					}
				}
			}
			return false
		})
	}
}

// featureName extracts the Name string from a Feature{...} literal.
func featureName(p *packages.Package, e ast.Expr) string {
	cl, ok := e.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Name" {
			if tv, ok := p.TypesInfo.Types[kv.Value]; ok && tv.Value != nil {
				return constant.StringVal(tv.Value)
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// (b) contrib/graphql.Annotation fields
// ---------------------------------------------------------------------------

func (idx *deadAPIIndex) checkAnnotationFields(t *testing.T, report func(rule, id string, pos token.Position, why string)) {
	p := idx.pkg(t, graphqlPkgPath)
	obj := p.Types.Scope().Lookup("Annotation")
	require.NotNil(t, obj, "graphql.Annotation not found")
	st, ok := obj.Type().Underlying().(*types.Struct)
	require.True(t, ok)
	declFile := idx.position(obj.Pos()).Filename

	// Functions declared in annotation.go that are live: called from outside
	// annotation.go, or from another live function in it. Merge copies every
	// field and so proves nothing about a reader.
	liveInDecl := map[*ast.FuncDecl]bool{}
	var funcs []*ast.FuncDecl
	for _, f := range p.Syntax {
		if p.Fset.File(f.Pos()).Name() != declFile {
			continue
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name != "Merge" {
				funcs = append(funcs, fd)
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, fd := range funcs {
			if liveInDecl[fd] {
				continue
			}
			fobj := p.TypesInfo.Defs[fd.Name]
			for _, u := range idx.uses[fobj] {
				caller := u.fn
				if u.file != declFile || (caller != nil && caller != fd && liveInDecl[caller]) {
					liveInDecl[fd] = true
					changed = true
					break
				}
			}
		}
	}

	for i := range st.NumFields() {
		field := st.Field(i)
		if !field.Exported() {
			continue
		}
		live := false
		for _, u := range idx.uses[field] {
			if u.write {
				continue
			}
			if u.file != declFile {
				live = true
				break
			}
			if fd := u.fn; fd != nil && liveInDecl[fd] {
				live = true
				break
			}
		}
		if !live {
			report("b", "graphql.Annotation."+field.Name(), idx.position(field.Pos()),
				"annotation field is stored but no generator reads it (accessors without callers do not count)")
		}
	}
}

// ---------------------------------------------------------------------------
// (c) runtime registries
// ---------------------------------------------------------------------------

func (idx *deadAPIIndex) checkRegistries(t *testing.T, report func(rule, id string, pos token.Position, why string), userFacing func(name string) bool) {
	p := idx.pkg(t, runtimePkgPath)
	scope := p.Types.Scope()

	// Package-level vars written by each exported top-level function.
	type registrar struct {
		decl *ast.FuncDecl
		obj  types.Object
	}
	writers := map[types.Object][]registrar{} // registry var -> functions writing it
	funcDecls := map[types.Object]*ast.FuncDecl{}
	for _, f := range p.Syntax {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			fobj := p.TypesInfo.Defs[fd.Name]
			funcDecls[fobj] = fd
			if fd.Recv != nil || !fd.Name.IsExported() || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, l := range as.Lhs {
					root := l
					for {
						switch x := root.(type) {
						case *ast.IndexExpr:
							root = x.X
							continue
						case *ast.SelectorExpr:
							root = x.X
							continue
						}
						break
					}
					id, ok := root.(*ast.Ident)
					if !ok {
						continue
					}
					v, ok := p.TypesInfo.Uses[id].(*types.Var)
					if ok && v.Parent() == scope {
						writers[v] = append(writers[v], registrar{fd, fobj})
					}
				}
				return true
			})
		}
	}

	hasCaller := func(fobj types.Object) bool {
		fd := funcDecls[fobj]
		for _, u := range idx.uses[fobj] {
			if u.fn != fd {
				return true
			}
		}
		return false
	}

	for v, ws := range writers {
		isWriter := map[*ast.FuncDecl]bool{}
		for _, w := range ws {
			isWriter[w.decl] = true
		}
		live := false
		for _, u := range idx.uses[v] {
			fd := u.fn
			if fd == nil || isWriter[fd] {
				continue
			}
			rd := p.TypesInfo.Defs[fd.Name]
			if fd.Recv != nil || hasCaller(rd) || userFacing(fd.Name.Name) {
				live = true
				break
			}
		}
		if !live {
			names := make([]string, 0, len(ws))
			seen := map[string]bool{}
			for _, w := range ws {
				if !seen[w.obj.Name()] {
					seen[w.obj.Name()] = true
					names = append(names, w.obj.Name())
				}
			}
			sort.Strings(names)
			report("c", "runtime."+v.Name(), idx.position(v.Pos()),
				fmt.Sprintf("registry written by %s but read by no function that has a caller", strings.Join(names, ", ")))
		}
	}
}

// ---------------------------------------------------------------------------
// (d) runtime exports
// ---------------------------------------------------------------------------

func (idx *deadAPIIndex) checkRuntimeExports(t *testing.T, report func(rule, id string, pos token.Position, why string)) {
	p := idx.pkg(t, runtimePkgPath)
	scope := p.Types.Scope()

	// The span of each object's own declaration: a func's FuncDecl, a type's
	// TypeSpec plus every method declared on it, a var/const's ValueSpec.
	// Package initializers own no span: their code always runs, so whatever
	// they use is live.
	spans := map[types.Object][]ast.Node{}
	for _, f := range p.Syntax {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name == "init" {
					continue
				}
				if d.Recv == nil {
					spans[p.TypesInfo.Defs[d.Name]] = append(spans[p.TypesInfo.Defs[d.Name]], d)
					continue
				}
				if tn := recvTypeName(p, d); tn != nil {
					spans[tn] = append(spans[tn], d)
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						o := p.TypesInfo.Defs[s.Name]
						spans[o] = append(spans[o], s)
					case *ast.ValueSpec:
						for _, n := range s.Names {
							o := p.TypesInfo.Defs[n]
							spans[o] = append(spans[o], s)
						}
					}
				}
			}
		}
	}

	within := func(pos token.Pos, nodes []ast.Node) bool {
		for _, n := range nodes {
			if n.Pos() <= pos && pos < n.End() {
				return true
			}
		}
		return false
	}

	// Reachability: an object is live if it is used from another package, or
	// from inside the declaration of a live object of this package, or from
	// code that belongs to no object (init functions, blank declarations).
	// This finds dead clusters — an exported constructor whose only caller is
	// another dead export — that a one-level "has any use" check misses.
	owner := func(pos token.Pos) types.Object {
		for o, nodes := range spans {
			if within(pos, nodes) {
				return o
			}
		}
		return nil
	}
	ownFiles := map[string]bool{}
	for _, f := range p.CompiledGoFiles {
		ownFiles[f] = true
	}
	refs := map[types.Object][]types.Object{} // owner -> objects its declaration uses
	live := map[types.Object]bool{}
	var queue []types.Object
	mark := func(o types.Object) {
		if !live[o] {
			live[o] = true
			queue = append(queue, o)
		}
	}
	for obj, sites := range idx.uses {
		if obj.Pkg() != p.Types || obj.Parent() != scope {
			continue
		}
		for _, u := range sites {
			var from types.Object
			if ownFiles[u.file] {
				from = owner(u.pos)
				if from == nil {
					mark(obj)
					continue
				}
				if from != obj {
					refs[from] = append(refs[from], obj)
				}
				continue
			}
			mark(obj)
		}
	}
	for len(queue) > 0 {
		o := queue[0]
		queue = queue[1:]
		for _, r := range refs[o] {
			mark(r)
		}
	}

	for _, name := range scope.Names() {
		if !token.IsExported(name) {
			continue
		}
		obj := scope.Lookup(name)
		if !live[obj] {
			report("d", "runtime."+name, idx.position(obj.Pos()),
				"exported runtime identifier is reachable from no generated code and no other non-test code")
		}
	}
}

func recvTypeName(p *packages.Package, fd *ast.FuncDecl) types.Object {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return nil
	}
	e := fd.Recv.List[0].Type
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
			continue
		case *ast.IndexExpr:
			e = x.X
			continue
		case *ast.IndexListExpr:
			e = x.X
			continue
		case *ast.ParenExpr:
			e = x.X
			continue
		}
		break
	}
	if id, ok := e.(*ast.Ident); ok {
		return p.TypesInfo.Uses[id]
	}
	return nil
}

// ---------------------------------------------------------------------------
// (e) generated struct fields populated only by clone()
// ---------------------------------------------------------------------------

// generatedPkgPrefixes are the generator-output packages the root module
// compiles (see generatedFixtures).
var generatedPkgPrefixes = []string{
	modulePath + "/tests/integration",
	modulePath + "/examples/realworld/velox",
}

// checkClonedOnlyFields reports a field of a generated struct whose every
// write is inside a clone()/Clone() method: it is copied from query to query
// but never given a value, so whatever reads it always sees the zero value.
// That was loadTotal on every generated query — declared, cloned, ranged
// over in sqlAll, and never appended to. A field with no write at all is
// not reported (zero values, embedded locks and the like are legitimate).
func (idx *deadAPIIndex) checkClonedOnlyFields(report func(rule, id string, pos token.Position, why string)) {
	for path, p := range idx.pkgs {
		if !isGeneratedPkg(path) {
			continue
		}
		for _, f := range p.Syntax {
			if strings.HasSuffix(p.Fset.File(f.Pos()).Name(), "_test.go") {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, fld := range st.Fields.List {
					for _, name := range fld.Names {
						obj := p.TypesInfo.Defs[name]
						if obj == nil {
							continue
						}
						writes, cloneWrites := 0, 0
						for _, u := range idx.uses[obj] {
							if !u.write {
								continue
							}
							writes++
							if u.fn != nil && strings.EqualFold(u.fn.Name.Name, "clone") {
								cloneWrites++
							}
						}
						if writes > 0 && writes == cloneWrites {
							report("e", p.Name+"."+ts.Name.Name+"."+name.Name, idx.position(name.Pos()),
								"generated struct field is written only by clone(); nothing ever gives it a value")
						}
					}
				}
				return false
			})
		}
	}
}

func isGeneratedPkg(path string) bool {
	for _, prefix := range generatedPkgPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// allowlist
// ---------------------------------------------------------------------------

// readDeadAPIAllowlist parses "<rule> <pkg>.<Name>  # reason" lines. Every
// entry must carry a reason.
func readDeadAPIAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	f, err := os.Open(filepath.FromSlash(deadAPIAllowlist))
	if os.IsNotExist(err) {
		return out
	}
	require.NoError(t, err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry, reason, _ := strings.Cut(line, "#")
		fields := strings.Fields(entry)
		if len(fields) != 2 || strings.TrimSpace(reason) == "" {
			t.Errorf("%s:%d: want \"<rule> <pkg>.<Name>  # reason\", got %q", deadAPIAllowlist, n, line)
			continue
		}
		out[fields[0]+" "+fields[1]] = true
	}
	require.NoError(t, sc.Err())
	return out
}

func relPos(pos token.Position) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, pos.Filename); err == nil {
			pos.Filename = rel
		}
	}
	return pos.String()
}
