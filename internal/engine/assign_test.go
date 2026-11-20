package engine_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
)

// setExprStmt is an UPDATE of invoices that assigns total the term t, as an
// expression, and number the value "n".
func setExprStmt(t engine.Term) *velox.Statement {
	return &velox.Statement{Op: velox.OpUpdate, Entity: invInfo, Set: []velox.Assignment{
		engine.ExprAssignment(3, t),
		{Column: 2, Op: velox.AssignSet, Value: "n"},
	}, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 1, V: int64(7)})}}
}

// TestRenderSetExpr: an expression assignment writes the column, =, and the
// expression in parentheses, its columns named and its values bound in
// order, in each dialect.
func TestRenderSetExpr(t *testing.T) {
	x := engine.ExprTerm("COALESCE(?, 0) * ?", []engine.ExprSlot{{Col: 3}, {Col: -1}}, []any{2.5})
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: `UPDATE "invoices" SET "total" = (COALESCE("total", 0) * $1), "number" = $2 WHERE "org_id" = $3`,
		velox.MySQL:    "UPDATE `invoices` SET `total` = (COALESCE(`total`, 0) * ?), `number` = ? WHERE `org_id` = ?",
		velox.SQLite:   `UPDATE "invoices" SET "total" = (COALESCE("total", 0) * ?), "number" = ? WHERE "org_id" = ?`,
	} {
		got, args := render(d, setExprStmt(x))
		if got != want {
			t.Errorf("%v:\n got %s\nwant %s", d, got, want)
		}
		if len(args) != 3 || args[0] != 2.5 || args[1] != "n" || args[2] != int64(7) {
			t.Errorf("%v: args %v", d, args)
		}
	}
}

// TestSetExprChecked: what a Rewrite hook may leave in an expression
// assignment is checked with the statement: a value that is no term, an
// aggregate, a term that is no expression, a column out of range and an
// encoded column fail it, and rendering refuses the last two as well.
func TestSetExprChecked(t *testing.T) {
	codec := *invInfo
	codec.Columns = append([]velox.ColumnInfo(nil), invInfo.Columns...)
	codec.Columns[3].Codec = true
	sum := engine.AggTerm(engine.TermSum, engine.ColumnTerm(3))
	for name, c := range map[string]struct {
		st   func() *velox.Statement
		want string
	}{
		"not a term": {func() *velox.Statement {
			st := setExprStmt(engine.Term{})
			st.Set[0].Value = "1"
			return st
		}, "not a term"},
		"aggregate": {func() *velox.Statement {
			return setExprStmt(engine.ExprTerm("?", []engine.ExprSlot{{Col: -1, Term: &sum}}, nil))
		}, "aggregate"},
		"column term": {func() *velox.Statement { return setExprStmt(engine.ColumnTerm(2)) }, "not an expression"},
		"column out of range": {func() *velox.Statement {
			return setExprStmt(engine.ExprTerm("?", []engine.ExprSlot{{Col: 9}}, nil))
		}, "column 9"},
		"encoded column": {func() *velox.Statement {
			st := setExprStmt(engine.ExprTerm("1", nil, nil))
			st.Entity = &codec
			return st
		}, "encoded column"},
		"bad text": {func() *velox.Statement {
			return setExprStmt(engine.ExprTerm("1; x", nil, nil))
		}, `';'`},
	} {
		if err := engine.CheckColumns(c.st()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: CheckColumns: %v, want %q", name, err, c.want)
		}
	}
	st := setExprStmt(engine.ExprTerm("1", nil, nil))
	st.Entity = &codec
	if _, _, err := engine.RenderStmt(velox.Postgres, st); err == nil || !strings.Contains(err.Error(), "encoded column") {
		t.Errorf("render of an encoded column: %v", err)
	}
	if _, _, err := engine.RenderStmt(velox.Postgres, setExprStmt(engine.ColumnTerm(2))); err == nil {
		t.Error("render of a column term as an expression: no error")
	}
}

// TestSetExprCacheKey: the expression's text shapes the cached SQL.
func TestSetExprCacheKey(t *testing.T) {
	var c engine.RenderCache
	one := func(text string) string {
		got, _ := cachedRender(&c, velox.Postgres, setExprStmt(engine.ExprTerm(text, nil, nil)))
		return got
	}
	if a, b := one("1"), one("2"); a == b {
		t.Errorf("two texts, one SQL: %s", a)
	}
}
