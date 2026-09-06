// Package load is the interface for loading a velox/schema package into a Go program.
package load

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/syssam/velox"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

type (
	// A SchemaSpec holds a serializable version of a velox.Schema
	// and its Go package and module information.
	SchemaSpec struct {
		// Schemas defines the loaded schema descriptors.
		Schemas []*Schema

		// PkgPath is the package path of the loaded
		// velox.Schema package.
		PkgPath string

		// Module defines the module information for
		// the user schema package if exists.
		Module *packages.Module
	}

	// Config holds the configuration for loading a velox/schema package.
	Config struct {
		// Path is the path for the schema package.
		Path string
		// Names are the schema names to load. Empty means all schemas in the directory.
		Names []string
		// BuildFlags are forwarded to the package.Config when
		// loading the schema package.
		BuildFlags []string
	}
)

// Load loads the schemas package and build the Go plugin with this info.
func (c *Config) Load() (*SchemaSpec, error) {
	spec, pos, err := c.load()
	if err != nil {
		return nil, fmt.Errorf("velox/load: parse schema dir: %w", err)
	}
	if len(c.Names) == 0 {
		return nil, fmt.Errorf("velox/load: no schema found in: %s", c.Path)
	}
	var b bytes.Buffer
	err = buildTmpl.ExecuteTemplate(&b, "main", struct {
		*Config
		Package string
	}{
		Config:  c,
		Package: spec.PkgPath,
	})
	if err != nil {
		return nil, fmt.Errorf("velox/load: execute template: %w", err)
	}
	buf, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("velox/load: format template: %w", err)
	}
	// The loader lives in a cache dir inside the working directory (not
	// /tmp/) so that Go's internal/ package visibility rules are satisfied:
	// packages under internal/ can only be imported by code rooted at
	// internal/'s parent, and a loader outside the module tree could not
	// import internal/* schema packages. Ent does the same with .entc/.
	//
	// Unlike Ent, the directory is KEPT between runs. Two costs disappear
	// when the previous binary is still there: `go build -o` compares the
	// build ID of an existing output and skips the link when it matches
	// (~0.5s), and macOS validates a freshly written executable on its
	// first exec (~0.5s, measured 0.4-0.7s) — a binary that was not
	// rewritten pays neither. On an unchanged schema that turns a ~1.4s
	// load into ~0.3s. The directory ignores itself via .gitignore.
	if err = prepareCacheDir(cacheDir); err != nil {
		return nil, err
	}
	base := filename(spec.PkgPath, buf)
	target := filepath.Join(cacheDir, base+".go")
	if err = writeIfChanged(target, buf); err != nil {
		return nil, fmt.Errorf("velox/load: write file %s: %w", target, err)
	}
	pruneStaleLoaders(cacheDir, spec.PkgPath, base)
	out, err := gobuild(target, c.BuildFlags)
	if err != nil {
		return nil, err
	}
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		schema, err := UnmarshalSchema([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("velox/load: unmarshal schema %s: %w", line, err)
		}
		spec.Schemas = append(spec.Schemas, schema)
	}
	for _, s := range spec.Schemas {
		s.Pos = pos[s.Name]
	}
	return spec, nil
}

// entInterface holds the reflect.Type of velox.Interface.
var entInterface = reflect.TypeFor[struct{ velox.Interface }]().Field(0).Type

// load the velox/schema info.
func (c *Config) load() (*SchemaSpec, map[string]string, error) {
	pkgs, err := packages.Load(&packages.Config{
		BuildFlags: c.BuildFlags,
		Mode:       packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule,
	}, c.Path, entInterface.PkgPath())
	if err != nil {
		return nil, nil, fmt.Errorf("loading package: %w", err)
	}
	if len(pkgs) < 2 {
		// Check if the package loading failed due to Go-related
		// errors, such as 'missing go.sum entry'.
		if err := golist(c.Path, c.BuildFlags); err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("missing package information for: %s", c.Path)
	}
	// Find packages by PkgPath rather than assuming positional ordering.
	var entPkg, pkg *packages.Package
	for _, p := range pkgs {
		switch p.PkgPath {
		case entInterface.PkgPath():
			entPkg = p
		default:
			pkg = p
		}
	}
	if entPkg == nil || pkg == nil {
		if err := golist(c.Path, c.BuildFlags); err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("missing package information for: %s", c.Path)
	}
	if len(pkg.Errors) != 0 {
		return nil, nil, c.loadError(pkg.Errors[0])
	}
	if len(entPkg.Errors) != 0 {
		return nil, nil, fmt.Errorf("velox/load: framework package error: %w", entPkg.Errors[0])
	}
	names := make(map[string]string)
	obj := entPkg.Types.Scope().Lookup(entInterface.Name())
	if obj == nil {
		return nil, nil, fmt.Errorf("velox/load: cannot find %s in package %s", entInterface.Name(), entPkg.PkgPath)
	}
	iface := obj.Type().Underlying().(*types.Interface)
	for k, v := range pkg.TypesInfo.Defs {
		typ, ok := v.(*types.TypeName)
		if !ok || !k.IsExported() || !types.Implements(typ.Type(), iface) {
			continue
		}
		spec, ok := k.Obj.Decl.(*ast.TypeSpec)
		if !ok {
			return nil, nil, fmt.Errorf("invalid declaration %T for %s", k.Obj.Decl, k.Name)
		}
		if _, ok := spec.Type.(*ast.StructType); !ok {
			return nil, nil, fmt.Errorf("invalid spec type %T for %s", spec.Type, k.Name)
		}
		p := pkg.Fset.Position(spec.Pos())
		names[k.Name] = fmt.Sprintf("%s:%d", p.Filename, p.Line)
	}
	if len(c.Names) == 0 {
		// Populate discovered names without mutating the original Config.
		c.Names = slices.Sorted(maps.Keys(names))
	} else {
		// Work on a copy to avoid mutating caller's slice.
		sorted := make([]string, len(c.Names))
		copy(sorted, c.Names)
		slices.Sort(sorted)
		c.Names = sorted
	}
	return &SchemaSpec{PkgPath: pkg.PkgPath, Module: pkg.Module}, names, nil
}

func (c *Config) loadError(perr packages.Error) (err error) {
	if strings.Contains(perr.Msg, "import cycle not allowed") {
		if cause := c.cycleCause(); cause != "" {
			perr.Msg += "\n" + cause
		}
	}
	err = perr
	if perr.Pos == "" {
		// Strip "-:" prefix in case of empty position.
		err = errors.New(perr.Msg)
	}
	return err
}

func (c *Config) cycleCause() (cause string) {
	dir, err := parser.ParseDir(token.NewFileSet(), c.Path, nil, 0)
	// Ignore reporting in case of parsing
	// error, or there no packages to parse.
	if err != nil || len(dir) == 0 {
		return
	}
	// Find the package that contains the schema, or
	// extract the first package if there is only one.
	pkg := dir[filepath.Base(c.Path)]
	if pkg == nil {
		for _, v := range dir {
			pkg = v
			break
		}
	}
	// Package local declarations used by schema fields.
	locals := make(map[string]bool)
	for _, f := range pkg.Files {
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.TYPE {
				continue
			}
			for _, s := range g.Specs {
				ts, ok := s.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				// Non-struct types such as "type Role int".
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					locals[ts.Name.Name] = true
					continue
				}
				var embedSchema bool
				astutil.Apply(st.Fields, func(c *astutil.Cursor) bool {
					f, ok := c.Node().(*ast.Field)
					if ok {
						switch x := f.Type.(type) {
						case *ast.SelectorExpr:
							if x.Sel.Name == "Schema" || x.Sel.Name == "Mixin" {
								embedSchema = true
							}
						case *ast.Ident:
							// A common pattern is to create local base schema to be embedded by other schemas.
							if name := strings.ToLower(x.Name); name == "schema" || name == "mixin" {
								embedSchema = true
							}
						}
					}
					// Stop traversing the AST in case an ~velox.Schema is embedded.
					return !embedSchema
				}, nil)
				if !embedSchema {
					locals[ts.Name.Name] = true
				}
			}
		}
	}
	// No local declarations to report.
	if len(locals) == 0 {
		return
	}
	// Usage of local declarations by schema fields.
	goTypes := make(map[string]bool)
	for _, f := range pkg.Files {
		for _, d := range f.Decls {
			f, ok := d.(*ast.FuncDecl)
			if !ok || f.Name.Name != "Fields" || f.Type.Params.NumFields() != 0 || f.Type.Results.NumFields() != 1 {
				continue
			}
			astutil.Apply(f.Body, func(cursor *astutil.Cursor) bool {
				i, ok := cursor.Node().(*ast.Ident)
				if ok && locals[i.Name] {
					goTypes[i.Name] = true
				}
				return true
			}, nil)
		}
	}
	names := make([]string, 0, len(goTypes))
	for k := range goTypes {
		names = append(names, strconv.Quote(k))
	}
	slices.Sort(names)
	if len(names) > 0 {
		cause = fmt.Sprintf("To resolve this issue, move the custom types used by the generated code to a separate package: %s", strings.Join(names, ", "))
	}
	return
}

var (
	//go:embed template/main.tmpl schema.go
	files     embed.FS
	buildTmpl = templates()
)

func templates() *template.Template {
	tmpls, err := schemaTemplates()
	if err != nil {
		panic(err)
	}
	tmpl := template.Must(template.New("templates").
		ParseFS(files, "template/main.tmpl"))
	for _, t := range tmpls {
		tmpl = template.Must(tmpl.Parse(t))
	}
	return tmpl
}

// schemaTemplates turns the schema.go file and its import block into templates.
func schemaTemplates() ([]string, error) {
	src, err := files.ReadFile("schema.go")
	if err != nil {
		return nil, fmt.Errorf("read embedded schema.go: %w", err)
	}
	var (
		imports []string
		code    bytes.Buffer
		fset    = token.NewFileSet()
	)
	f, err := parser.ParseFile(fset, "schema.go", src, parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("parse schema file: %w", err)
	}
	for _, decl := range f.Decls {
		if decl, ok := decl.(*ast.GenDecl); ok && decl.Tok == token.IMPORT {
			for _, spec := range decl.Specs {
				imports = append(imports, spec.(*ast.ImportSpec).Path.Value)
			}
			continue
		}
		if err := format.Node(&code, fset, decl); err != nil {
			return nil, fmt.Errorf("format node: %w", err)
		}
		code.WriteByte('\n')
	}
	return []string{
		fmt.Sprintf(`{{ define "schema" }} %s {{ end }}`, code.String()),
		fmt.Sprintf(`{{ define "imports" }} %s {{ end }}`, groupImports(imports)),
	}, nil
}

// cacheDir is where the loader source and binary live, relative to the
// working directory. See Load for why it is inside the module and why it
// persists across runs.
const cacheDir = ".velox"

// prepareCacheDir creates the loader cache dir and makes it ignore itself,
// so a persisted .velox/ never shows up as untracked files in the user's
// repository.
func prepareCacheDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("velox/load: create cache dir: %w", err)
	}
	if err := writeIfChanged(filepath.Join(dir, ".gitignore"), []byte("*\n")); err != nil {
		return fmt.Errorf("velox/load: write %s/.gitignore: %w", dir, err)
	}
	return nil
}

// writeIfChanged writes data to path unless the file already holds exactly
// those bytes. Leaving an unchanged file alone keeps its mtime and, for the
// loader source, keeps `go build -o` on its up-to-date fast path.
func writeIfChanged(path string, data []byte) error {
	if prev, err := os.ReadFile(path); err == nil && bytes.Equal(prev, data) {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}

// pruneStaleLoaders removes earlier loader sources and binaries for the same
// schema package (a different content hash, i.e. a schema type was added or
// removed) so the cache dir holds one loader per package rather than
// growing without bound. Only names that match this package's prefix
// exactly followed by a hash are touched — a nested or `_`-suffixed
// sibling package has a different prefix (the prefix carries a hash of the
// import path, so `a/b_c` and `a_b/c` cannot collide) — and in-flight
// temp binaries of concurrent loads are never removed.
func pruneStaleLoaders(dir, pkg, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	re := regexp.MustCompile("^" + regexp.QuoteMeta(loaderPrefix(pkg)) + `[0-9a-f]{12}\.go(\.bin)?$`)
	for _, e := range entries {
		name := e.Name()
		if !re.MatchString(name) || strings.HasPrefix(name, keep+".") {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// loaderPrefix is the filename prefix shared by every loader of one schema
// package: velox_<pkg with / replaced by _>_<8 hex of the import path>_.
// The path hash makes the prefix injective: sanitizing alone maps both
// `a/b_c` and `a_b/c` to `a_b_c`.
func loaderPrefix(pkg string) string {
	sum := sha256.Sum256([]byte(pkg))
	return fmt.Sprintf("velox_%s_%x_", strings.ReplaceAll(pkg, "/", "_"), sum[:4])
}

// filename names the loader source after the schema package and a hash of
// its content. The Go build cache keys a command-line-arguments package on
// the file NAME as well as its bytes, so a per-run timestamp (Ent's scheme)
// made every generation a cache miss: compile plus a full link of a binary
// that pulls in velox and the schema package, ~0.8s on a laptop. The loader
// source only embeds the schema package path and type names, so it is
// byte-identical across runs until a schema type is added or removed —
// with a content-derived name an unchanged schema rebuilds from cache in
// ~0.2s, and after a schema edit only the link reruns.
func filename(pkg string, content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%s%x", loaderPrefix(pkg), sum[:6])
}

// gobuild compiles the target Go file into a binary and executes it.
// Unlike 'go run', the binary is compiled from a single self-contained
// file with no external module dependencies beyond the Go toolchain cache.
//
// The cached binary next to the source is reused whenever the toolchain
// says it is current: `go build -n -o <bin>` reports the actions it would
// run without running them, and prints no link step when the existing
// output's build ID matches (~0.14s, versus ~0.5s for a link). That keeps
// the file untouched, which matters twice over: no relink, and no macOS
// first-exec code-signature validation (0.4-0.7s) on a rewritten file.
// When a build is needed it goes to a per-process temp file (concurrent
// loads in one directory do not share it) and is renamed over the cached
// binary only if the bytes differ.
func gobuild(target string, buildFlags []string) (string, error) {
	binPath := target + ".bin"
	// The loader binary runs once per generation; DWARF and the symbol
	// table are dead weight that make the link ~30% slower. -ldflags is
	// placed before the caller's flags so an explicit -ldflags in
	// BuildFlags wins.
	flags := make([]string, 0, 1+len(buildFlags))
	flags = append(flags, "-ldflags=-s -w")
	flags = append(flags, buildFlags...)

	if _, err := os.Stat(binPath); err == nil && !needsLink(binPath, target, flags) {
		return runLoader(binPath)
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".bin.*.tmp")
	if err != nil {
		return "", fmt.Errorf("velox/load: create temp binary: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	args := make([]string, 0, 1+len(flags)+3)
	args = append(args, "build")
	args = append(args, flags...)
	args = append(args, "-o", tmpPath, target)
	cmd := exec.Command("go", args...)
	stderr := bytes.NewBuffer(nil)
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("velox/load: %s", msg)
		}
		return "", fmt.Errorf("velox/load: build failed: %w", err)
	}
	if err := keepIfIdentical(tmpPath, binPath); err != nil {
		return "", err
	}
	return runLoader(binPath)
}

// needsLink asks the go tool, without running anything, whether building
// target to binPath would link. A current output produces no link step.
// Any failure to answer is treated as "yes" so a real build decides.
func needsLink(binPath, target string, flags []string) bool {
	args := make([]string, 0, 2+len(flags)+3)
	args = append(args, "build", "-n")
	args = append(args, flags...)
	args = append(args, "-o", binPath, target)
	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		return true
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if hasLinkAction(line) {
			return true
		}
	}
	return false
}

// hasLinkAction reports whether a `go build -n` line invokes the linker.
//
// Every field is checked by base name, for two reasons. The tool is printed
// as a path whose separator is platform-specific
// (…/pkg/tool/darwin_arm64/link, …\pkg\tool\windows_amd64\link.exe), so a
// "/link " substring test silently never fires on Windows and the loader
// would reuse a stale binary forever, generating from an outdated schema.
// And the tool is not the first field: the line is prefixed with an
// environment assignment (GOROOT='…' …/link -o …). Comparing base names
// also keeps the unrelated `cat >$WORK/b001/importcfg.link` line from
// counting as a link step.
func hasLinkAction(line string) bool {
	for _, f := range strings.Fields(line) {
		if i := strings.LastIndexAny(f, `/\`); i >= 0 {
			f = f[i+1:]
		}
		if f == "link" || f == "link.exe" {
			return true
		}
	}
	return false
}

// runLoader executes the compiled loader and returns its stdout.
func runLoader(binPath string) (string, error) {
	cmd := exec.Command(binPath)
	stdout := bytes.NewBuffer(nil)
	stderr := bytes.NewBuffer(nil)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("velox/load: %s", msg)
		}
		return "", fmt.Errorf("velox/load: binary execution failed: %w", err)
	}
	return stdout.String(), nil
}

// keepIfIdentical moves the freshly built tmp over dst unless dst already
// holds the same bytes, in which case tmp is discarded and dst — with its
// mtime and, on macOS, its cached code-signature validation — is kept.
func keepIfIdentical(tmp, dst string) error {
	same, err := sameContent(tmp, dst)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if same {
		return os.Remove(tmp)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("velox/load: install loader binary: %w", err)
	}
	return nil
}

// sameContent reports whether both files exist and hold identical bytes.
func sameContent(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		return false, fmt.Errorf("velox/load: stat %s: %w", a, err)
	}
	fb, err := os.Stat(b)
	if err != nil || fa.Size() != fb.Size() {
		return false, nil
	}
	da, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	db, err := os.ReadFile(b)
	if err != nil {
		return false, nil
	}
	return bytes.Equal(da, db), nil
}

// golist checks if 'go list' can be executed on the given target.
func golist(target string, buildFlags []string) error {
	_, err := gocmd("list", target, buildFlags)
	return err
}

// goCmd runs a go command and returns its output.
func gocmd(command, target string, buildFlags []string) (string, error) {
	args := make([]string, 0, 1+len(buildFlags)+1)
	args = append(args, command)
	args = append(args, buildFlags...)
	args = append(args, target)
	cmd := exec.Command("go", args...)
	stderr := bytes.NewBuffer(nil)
	stdout := bytes.NewBuffer(nil)
	cmd.Stderr = stderr
	cmd.Stdout = stdout
	if err := cmd.Run(); err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// groupImports renders quoted import paths as goimports would lay them
// out: standard library first, a blank line, then everything else, each
// group sorted. The loader source persists in .velox/ and consumer lint
// gates that walk dot-directories would otherwise flag it.
//
// compiler/gen.regroupImports applies the same stdlib rule to already
// rendered Go source. The two are deliberately not shared: compiler/gen
// imports this package, so a common helper would need a third package for
// a three-line predicate.
func groupImports(paths []string) string {
	var std, other []string
	for _, p := range paths {
		first, _, _ := strings.Cut(strings.Trim(p, `"`), "/")
		if strings.Contains(first, ".") {
			other = append(other, p)
		} else {
			std = append(std, p)
		}
	}
	sort.Strings(std)
	sort.Strings(other)
	switch {
	case len(std) == 0:
		return strings.Join(other, "\n")
	case len(other) == 0:
		return strings.Join(std, "\n")
	}
	return strings.Join(std, "\n") + "\n\n" + strings.Join(other, "\n")
}
