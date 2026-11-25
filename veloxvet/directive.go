package veloxvet

import (
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
)

// line is a line of a file.
type line struct {
	file string
	n    int
}

// directives are where a package's comments of one directive sit: the
// lines that hold one, and the lines that hold one alone.
type directives struct {
	on    map[line]bool
	alone map[line]bool
}

// findDirectives finds the package's comments that are text, or text, a
// space and a reason.
func findDirectives(pass *analysis.Pass, text string) directives {
	d := directives{on: map[line]bool{}, alone: map[line]bool{}}
	for _, f := range pass.Files {
		var src []byte
		for _, g := range f.Comments {
			for _, cm := range g.List {
				if cm.Text != text && !strings.HasPrefix(cm.Text, text+" ") {
					continue
				}
				posn := pass.Fset.Position(cm.Slash)
				if src == nil {
					src, _ = pass.ReadFile(posn.Filename)
				}
				l := line{posn.Filename, posn.Line}
				d.on[l] = true
				if alone(src, posn.Offset) {
					d.alone[l] = true
				}
			}
		}
	}
	return d
}

// alone reports whether only blanks precede offset on its line of src.
func alone(src []byte, offset int) bool {
	if offset > len(src) {
		return false
	}
	for i := offset - 1; i >= 0 && src[i] != '\n'; i-- {
		if src[i] != ' ' && src[i] != '\t' {
			return false
		}
	}
	return true
}

// waives reports whether a directive waives the report at pos: one on a
// line of the statement that holds pos, or alone on the line before the
// statement. Of an if, for or switch statement, or a case, whose header
// holds pos, only the header counts: a statement in a header, such as an
// if's init, is a statement of its own. Outside a function, the
// declaration's spec stands for the statement. The lines inside the body
// of a function literal in the statement, which does not hold pos, belong
// to the literal's statements, not to this one.
func (d directives) waives(pass *analysis.Pass, pos token.Pos) bool {
	if len(d.on) == 0 {
		return false
	}
	from, to := pos, pos
	var stmt ast.Node
	if f := fileOf(pass, pos); f != nil {
		stmt, from, to = enclosing(f, pos)
	}
	start, end := pass.Fset.Position(from), pass.Fset.Position(to)
	if d.alone[line{start.Filename, start.Line - 1}] {
		return true
	}
	inner := literalLines(pass.Fset, stmt, pos)
	for n := start.Line; n <= end.Line; n++ {
		if d.on[line{start.Filename, n}] && !inner[n] {
			return true
		}
	}
	return false
}

// literalLines are the lines of n that lie wholly inside the body of a
// function literal that does not hold pos: those after its opening brace's
// line and before its closing brace's.
func literalLines(fset *token.FileSet, n ast.Node, pos token.Pos) map[int]bool {
	if n == nil {
		return nil
	}
	var out map[int]bool
	ast.Inspect(n, func(n ast.Node) bool {
		lit, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		if lit.Body.Pos() <= pos && pos < lit.Body.End() {
			return true
		}
		from, to := fset.Position(lit.Body.Lbrace).Line, fset.Position(lit.Body.Rbrace).Line
		for l := from + 1; l < to; l++ {
			if out == nil {
				out = map[int]bool{}
			}
			out[l] = true
		}
		return false
	})
	return out
}

// fileOf is the package's file that holds pos.
func fileOf(pass *analysis.Pass, pos token.Pos) *ast.File {
	for _, f := range pass.Files {
		if f.FileStart <= pos && pos < f.FileEnd {
			return f
		}
	}
	return nil
}

// enclosing returns the innermost statement of f that holds pos, and its
// start and end as waives describes them, or nil and pos when there is
// none.
func enclosing(f *ast.File, pos token.Pos) (ast.Node, token.Pos, token.Pos) {
	path, _ := astutil.PathEnclosingInterval(f, pos, pos)
	for _, n := range path {
		switch n := n.(type) {
		case *ast.BlockStmt:
		case ast.Stmt:
			return n, n.Pos(), header(n, pos)
		case ast.Spec:
			return n, n.Pos(), n.End()
		}
	}
	return nil, pos, pos
}

// header is the end of s, or of its header when pos is in the header.
func header(s ast.Stmt, pos token.Pos) token.Pos {
	var open token.Pos
	switch s := s.(type) {
	case *ast.IfStmt:
		open = s.Body.Lbrace
	case *ast.ForStmt:
		open = s.Body.Lbrace
	case *ast.RangeStmt:
		open = s.Body.Lbrace
	case *ast.SwitchStmt:
		open = s.Body.Lbrace
	case *ast.CaseClause:
		open = s.Colon
	}
	if open.IsValid() && pos < open {
		return open
	}
	return s.End()
}
