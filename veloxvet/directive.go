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
// statement. Of an if, for, switch or select statement, or a case, whose
// header holds pos, only the header counts. Outside a function, the
// declaration's spec stands for the statement.
func (d directives) waives(pass *analysis.Pass, pos token.Pos) bool {
	if len(d.on) == 0 {
		return false
	}
	from, to := pos, pos
	if f := fileOf(pass, pos); f != nil {
		from, to = enclosing(f, pos)
	}
	start, end := pass.Fset.Position(from), pass.Fset.Position(to)
	if d.alone[line{start.Filename, start.Line - 1}] {
		return true
	}
	for n := start.Line; n <= end.Line; n++ {
		if d.on[line{start.Filename, n}] {
			return true
		}
	}
	return false
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

// enclosing returns the start and end of the innermost statement of f that
// holds pos, as waives describes it, or pos when there is none.
func enclosing(f *ast.File, pos token.Pos) (token.Pos, token.Pos) {
	path, _ := astutil.PathEnclosingInterval(f, pos, pos)
	for _, n := range path {
		switch n := n.(type) {
		case *ast.BlockStmt:
		case ast.Stmt:
			return n.Pos(), header(n, pos)
		case ast.Spec:
			return n.Pos(), n.End()
		}
	}
	return pos, pos
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
	case *ast.TypeSwitchStmt:
		open = s.Body.Lbrace
	case *ast.SelectStmt:
		open = s.Body.Lbrace
	case *ast.CaseClause:
		open = s.Colon
	case *ast.CommClause:
		open = s.Colon
	}
	if open.IsValid() && pos < open {
		return open
	}
	return s.End()
}
