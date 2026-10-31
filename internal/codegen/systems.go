package codegen

import (
	"go/ast"
	gobuild "go/build"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"go/version"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Systems says on which systems go build builds a .go file.
type Systems int

// The Systems GoFileSystems reports.
const (
	EverySystem Systems = iota // every build, with any toolchain the module's go version allows
	NoSystem                   // none: //go:build ignore, a constraint no build meets, a _x.go or .x.go name
	SomeSystems                // some only: a GOOS or GOARCH in its name, a constraint on any other tag, cgo
)

// GoFileSystems says on which builds go build builds the .go file name that
// holds src, whatever system velox-gen itself runs on and whatever tags a
// build sets, and gives the line of src that decides, or 0 when the name
// decides or nothing does. goVersion is the go version of the go.mod of the
// module the file is in, such as "1.27.0" (ModuleGoVersion); "" stands for
// velox-gen's own toolchain.
//
// A name that starts with _ or ., or a _test.go file, is no build's. A
// _GOOS, _GOARCH or _GOOS_GOARCH suffix is some systems'. The build
// constraint, //go:build or else the // +build lines, decides next: ignore is
// unset, a release tag go1.N (go1.1 or later, as go writes it) holds when
// goVersion is go1.N or later and is free otherwise, and every other tag is
// free, go1.0 and go1.027 as much as a GOOS, GOARCH, unix, cgo, compiler,
// goexperiment or architecture-level tag as much as a custom one such as prod
// or tools, since a build may set it or not. A constraint that holds for every
// choice of the free tags builds everywhere, one that holds for none nowhere,
// and any other on some builds. An import of "C" needs cgo, so it builds on
// some systems. A src that go/parser cannot read up to its imports is the
// compiler's to refuse: every system's.
func GoFileSystems(name string, src []byte, goVersion string) (Systems, int) {
	switch {
	case strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") || strings.HasSuffix(name, "_test.go"):
		return NoSystem, 0
	case !nameBuilds(name):
		return SomeSystems, 0
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly|parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return EverySystem, 0
	}
	x, line := buildConstraint(fset, f)
	if x != nil {
		if s := systemsOf(x, goVersion); s != EverySystem {
			return s, line
		}
	}
	for _, im := range f.Imports {
		if im.Path.Value == `"C"` {
			return SomeSystems, fset.Position(im.Pos()).Line
		}
	}
	return EverySystem, 0
}

// noSystem is a build context of no real system, whose files hold only a
// package clause: MatchFile then decides by a file's name alone, by go/build's
// own lists of GOOS and GOARCH values.
var noSystem = gobuild.Context{
	GOOS: "velox", GOARCH: "velox", Compiler: "gc",
	OpenFile: func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("package p\n")), nil },
}

// nameBuilds reports whether a file's name leaves no system out: it has no
// _GOOS, _GOARCH or _GOOS_GOARCH suffix.
func nameBuilds(name string) bool {
	ok, err := noSystem.MatchFile("/", name)
	return err == nil && ok
}

// goTag matches a go1.N release tag.
var goTag = regexp.MustCompile(`^go1\.[0-9]+$`)

// ModuleGoVersion is the go version that the go.mod of the module holding
// dir names, such as "1.27.0", or "" when no go.mod above dir names one. dir
// need not exist.
func ModuleGoVersion(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if src, err := os.ReadFile(filepath.Join(d, "go.mod")); err == nil {
			for line := range strings.Lines(string(src)) {
				if f := strings.Fields(line); len(f) >= 2 && f[0] == "go" {
					return f[1]
				}
			}
			return ""
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// buildConstraint is f's //go:build constraint, or else the conjunction of
// its // +build lines, with the line it starts on; nil when it has none. As
// for go/build, only the comments before the package clause count, a // +build
// line counts only in a group of line comments that a blank line ends before
// the package's doc and before the first /* */ comment, and a //go:build line
// counts in any line comment there.
func buildConstraint(fset *token.FileSet, f *ast.File) (constraint.Expr, int) {
	plusEnd := f.Package // a // +build line's group must end before it
	for _, g := range f.Comments {
		if g.Pos() >= f.Package {
			break
		}
		if g == f.Doc && g.Pos() < plusEnd {
			plusEnd = g.Pos()
		}
		for _, c := range g.List {
			if strings.HasPrefix(c.Text, "/*") && c.Pos() < plusEnd {
				plusEnd = c.Pos()
			}
		}
	}
	var plus constraint.Expr
	plusLine := 0
	for _, g := range f.Comments {
		if g.Pos() >= f.Package {
			break
		}
		for _, c := range g.List {
			switch {
			case constraint.IsGoBuild(c.Text):
				if x, err := constraint.Parse(c.Text); err == nil {
					return x, fset.Position(c.Pos()).Line
				}
			case constraint.IsPlusBuild(c.Text) && g.End() < plusEnd:
				if x, err := constraint.Parse(c.Text); err == nil {
					if plus == nil {
						plus, plusLine = x, fset.Position(c.Pos()).Line
					} else {
						plus = &constraint.AndExpr{X: plus, Y: x}
					}
				}
			}
		}
	}
	return plus, plusLine
}

// systemsOf says on which builds x holds: ignore is unset, a release tag go1.N
// (go1.1 and later, written as go writes it) that goVersion ("" for
// velox-gen's toolchain) meets is set, and it tries every choice of the other
// tags, go1.0, go1.00 and go1.027 among them, which no release sets.
func systemsOf(x constraint.Expr, goVersion string) Systems {
	lang := "go" + goVersion
	if goVersion == "" {
		lang = gobuild.Default.ReleaseTags[len(gobuild.Default.ReleaseTags)-1]
	}
	lang = version.Lang(lang)
	fixed := func(tag string) (value, ok bool) {
		switch {
		case tag == "ignore":
			return false, true
		case goTag.MatchString(tag) && version.IsValid(tag) && version.Compare(tag, "go1.1") >= 0 && lang != "" && version.Compare(tag, lang) <= 0:
			return true, true
		}
		return false, false
	}
	var free []string
	var walk func(constraint.Expr)
	walk = func(x constraint.Expr) {
		switch x := x.(type) {
		case *constraint.TagExpr:
			if _, ok := fixed(x.Tag); !ok && !slices.Contains(free, x.Tag) {
				free = append(free, x.Tag)
			}
		case *constraint.NotExpr:
			walk(x.X)
		case *constraint.AndExpr:
			walk(x.X)
			walk(x.Y)
		case *constraint.OrExpr:
			walk(x.X)
			walk(x.Y)
		}
	}
	walk(x)
	if len(free) > 16 {
		return SomeSystems // too many to try; some build is left out in practice
	}
	held, failed := false, false
	for set := range 1 << len(free) {
		ok := x.Eval(func(tag string) bool {
			if v, ok := fixed(tag); ok {
				return v
			}
			return set&(1<<slices.Index(free, tag)) != 0
		})
		held, failed = held || ok, failed || !ok
	}
	switch {
	case !failed:
		return EverySystem
	case !held:
		return NoSystem
	}
	return SomeSystems
}
