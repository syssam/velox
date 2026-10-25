package gen

import (
	"go/ast"
	gobuild "go/build"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"slices"
	"strings"
)

// Systems says on which systems go build builds a .go file.
type Systems int

// The Systems GoFileSystems reports.
const (
	EverySystem Systems = iota // every system, with any toolchain velox-gen's meets
	NoSystem                   // none: //go:build ignore, a tag no build sets, a _x.go or .x.go name
	SomeSystems                // some only: a GOOS or GOARCH in its name or constraint, cgo, a compiler
)

// GoFileSystems says on which systems go build builds the .go file name that
// holds src, whatever system velox-gen itself runs on, and gives the line of
// src that decides, or 0 when the name decides or nothing does.
//
// A name that starts with _ or ., or a _test.go file, is no system's. A
// _GOOS, _GOARCH or _GOOS_GOARCH suffix is some systems'. The build
// constraint, //go:build or else the // +build lines, decides next: each GOOS,
// GOARCH, unix, cgo, gc and gccgo tag in it is free, a go1.N tag holds when
// the toolchain meets it, and every other tag, such as ignore or tools, is
// unset. A constraint that holds for every choice of the free tags builds
// everywhere, one that holds for none nowhere, and any other on some systems.
// An import of "C" needs cgo, so it builds on some systems. A src that go/parser
// cannot read up to its imports is the compiler's to refuse: every system's.
func GoFileSystems(name string, src []byte) (Systems, int) {
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
		if s := systemsOf(x); s != EverySystem {
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

// systemTag reports whether a build tag holds on some systems and not on
// others: a GOOS or GOARCH value, unix, cgo, or a compiler.
func systemTag(tag string) bool {
	switch tag {
	case "unix", "cgo", "gc", "gccgo":
		return true
	}
	return !strings.ContainsAny(tag, "_.") && !nameBuilds("x_"+tag+".go")
}

// buildConstraint is f's //go:build constraint, or else the conjunction of
// its // +build lines, with the line it starts on; nil when it has none. As
// for go/build, only the comments before the package clause count, and a
// // +build line in the package's doc does not.
func buildConstraint(fset *token.FileSet, f *ast.File) (constraint.Expr, int) {
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
			case constraint.IsPlusBuild(c.Text) && g != f.Doc:
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

// systemsOf says on which systems x holds: it tries every choice of its
// system tags.
func systemsOf(x constraint.Expr) Systems {
	var free []string
	var walk func(constraint.Expr)
	walk = func(x constraint.Expr) {
		switch x := x.(type) {
		case *constraint.TagExpr:
			if systemTag(x.Tag) && !slices.Contains(free, x.Tag) {
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
		return SomeSystems // too many to try; some system is left out in practice
	}
	held, failed := false, false
	for set := range 1 << len(free) {
		ok := x.Eval(func(tag string) bool {
			if i := slices.Index(free, tag); i >= 0 {
				return set&(1<<i) != 0
			}
			return slices.Contains(gobuild.Default.ReleaseTags, tag)
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
