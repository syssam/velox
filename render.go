package velox

import (
	"fmt"
	"strconv"
	"strings"
)

// writer renders a statement. With argsOnly it collects the arguments and
// writes no text: the text is cached (render_cache.go), and one code path for
// both keeps the arguments in the order of the placeholders.
type writer struct {
	d        Dialect
	in       *EntityInfo
	b        strings.Builder
	args     []any
	argsOnly bool
	alias    string // the alias of the subquery being written, and its nesting
	depth    int
}

func (w *writer) str(s string) {
	if !w.argsOnly {
		w.b.WriteString(s)
	}
}

func (w *writer) ident(s string) {
	if !w.argsOnly {
		w.d.quote(&w.b, s)
	}
}

func (w *writer) col(i int32) { w.ident(w.in.Columns[i].Name) }

func (w *writer) arg(v any) {
	w.args = append(w.args, v)
	if !w.argsOnly {
		w.d.placeholder(&w.b, len(w.args))
	}
}

var cmpText = [...]string{opEQ: " = ", opNEQ: " <> ", opGT: " > ", opGTE: " >= ", opLT: " < ", opLTE: " <= "}

func (w *writer) selectStmt(st *Statement) {
	w.str("SELECT ")
	switch {
	case st.Count:
		w.str("COUNT(*)")
	case st.Columns == nil:
		for i := range w.in.Columns {
			if i > 0 {
				w.str(", ")
			}
			w.col(int32(i))
		}
	default:
		for i, c := range st.Columns {
			if i > 0 {
				w.str(", ")
			}
			w.col(int32(c))
		}
	}
	w.str(" FROM ")
	w.table()
	w.where(st.Where)
	for i, o := range st.OrderBy {
		if i == 0 {
			w.str(" ORDER BY ")
		} else {
			w.str(", ")
		}
		w.col(int32(o.Column))
		if o.Desc {
			w.str(" DESC")
		}
	}
	if st.Limit > 0 {
		w.str(" LIMIT ")
		w.arg(st.Limit)
	}
	if st.Offset > 0 {
		if st.Limit == 0 && w.d != Postgres {
			w.str(" LIMIT ")
			w.str(w.d.noLimit())
		}
		w.str(" OFFSET ")
		w.arg(st.Offset)
	}
}

// isNilArg reports a value that SQL compares with IS NULL, not =. It
// recognizes an untyped nil and a nil []byte only: a nil value of another type
// (a nil *int64 in an any, say) is compared with =, which matches nothing.
func isNilArg(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case []byte:
		return x == nil
	}
	return false
}

func (w *writer) pred(n node) {
	if (n.op == opEQ || n.op == opNEQ) && isNilArg(n.v) {
		w.col(n.col)
		if n.op == opEQ {
			w.str(" IS NULL")
		} else {
			w.str(" IS NOT NULL")
		}
		return
	}
	switch n.op {
	case opEQ, opNEQ, opGT, opGTE, opLT, opLTE:
		w.col(n.col)
		w.str(cmpText[n.op])
		w.arg(n.v)
	case opIn, opNotIn:
		vs := n.v.([]any)
		if len(vs) == 0 {
			if n.op == opIn {
				w.str("1 = 0")
			} else {
				w.str("1 = 1")
			}
			return
		}
		w.col(n.col)
		if n.op == opNotIn {
			w.str(" NOT IN (")
		} else {
			w.str(" IN (")
		}
		for i, v := range vs {
			if i > 0 {
				w.str(", ")
			}
			w.arg(v)
		}
		w.str(")")
	case opBetween:
		w.col(n.col)
		w.str(" BETWEEN ")
		w.arg(n.v)
		w.str(" AND ")
		w.arg(n.w)
	case opLike:
		w.col(n.col)
		w.str(" LIKE ")
		w.arg(n.v)
		w.str(w.d.likeEscape())
	case opFold:
		w.str("LOWER(")
		w.col(n.col)
		w.str(") = LOWER(")
		w.arg(n.v)
		w.str(")")
	case opIsNull:
		w.col(n.col)
		w.str(" IS NULL")
	case opNotNull:
		w.col(n.col)
		w.str(" IS NOT NULL")
	case opAnd, opOr:
		if len(n.kids) == 0 {
			if n.op == opAnd {
				w.str("1 = 1")
			} else {
				w.str("1 = 0")
			}
			return
		}
		w.str("(")
		for i, k := range n.kids {
			if i > 0 {
				if n.op == opAnd {
					w.str(" AND ")
				} else {
					w.str(" OR ")
				}
			}
			w.pred(k)
		}
		w.str(")")
	case opNot:
		w.str("NOT (")
		w.pred(n.kids[0])
		w.str(")")
	case opExists:
		w.exists(n)
	}
}

// exists writes EXISTS over a relation. The subquery's table is aliased
// vx<depth>, so it is told apart from the outer table even when they are the
// same table; the correlation names the outer side by its alias, or by its
// table at the top; the target's conditions are unqualified and bind to the
// innermost table.
func (w *writer) exists(n node) {
	l := n.v.(link)
	owner, outer := w.in, w.alias
	w.depth++
	alias := "vx" + strconv.Itoa(w.depth)
	w.str("EXISTS (SELECT 1 FROM ")
	w.in = l.target
	w.table()
	w.str(" AS ")
	w.ident(alias)
	w.str(" WHERE ")
	w.ident(alias)
	w.str(".")
	w.col(int32(l.targetCol))
	w.str(" = ")
	if outer != "" {
		w.ident(outer)
	} else {
		w.in = owner
		w.table()
		w.in = l.target
	}
	w.str(".")
	w.ident(owner.Columns[l.ownerCol].Name)
	w.alias = alias
	for _, k := range n.kids {
		w.str(" AND ")
		w.pred(k)
	}
	w.str(")")
	w.in, w.alias = owner, outer
	w.depth--
}

// render returns a statement's SQL and arguments.
func render(d Dialect, st *Statement) (string, []any) {
	w := writer{d: d, in: st.Entity}
	w.statement(st)
	return w.b.String(), w.args
}

// statement writes a statement of any kind.
func (w *writer) statement(st *Statement) {
	switch st.Op {
	case OpSelect:
		w.selectStmt(st)
	case OpInsert:
		w.insertStmt(st)
	case OpUpdate:
		w.updateStmt(st)
	case OpDelete:
		w.deleteStmt(st)
	default:
		panic(fmt.Sprintf("velox: render: unknown statement kind %d", st.Op))
	}
}

// table writes the entity's table, qualified by its schema when it has one.
func (w *writer) table() {
	if w.in.Schema != "" {
		w.ident(w.in.Schema)
		w.str(".")
	}
	w.ident(w.in.Table)
}

func (w *writer) where(ps []Predicate) {
	for i, p := range ps {
		if i == 0 {
			w.str(" WHERE ")
		} else {
			w.str(" AND ")
		}
		w.pred(p.n)
	}
}

func (w *writer) columns(cs []int) {
	for i, c := range cs {
		if i > 0 {
			w.str(", ")
		}
		w.col(int32(c))
	}
}

func (w *writer) insertStmt(st *Statement) {
	w.str("INSERT INTO ")
	w.table()
	w.str(" (")
	w.columns(st.Columns)
	w.str(") VALUES ")
	for r, row := range st.Rows {
		if r > 0 {
			w.str(", ")
		}
		w.str("(")
		for i, v := range row {
			if i > 0 {
				w.str(", ")
			}
			w.arg(v)
		}
		w.str(")")
	}
	if st.Conflict != nil {
		w.conflict(st.Conflict)
	}
	if len(st.Returning) > 0 && w.d != MySQL {
		w.str(" RETURNING ")
		w.columns(st.Returning)
	}
}

// conflict writes the upsert clause. MySQL has neither a conflict target nor
// DO NOTHING: ON DUPLICATE KEY UPDATE applies to any unique key, keeping the
// existing row is a no-op assignment, and the inserted values come from the
// row alias new (MySQL 8.0.19 and later). The alias makes an unqualified column
// name on the right side ambiguous (error 1052), so the no-op assignment
// qualifies it with the table.
func (w *writer) conflict(cf *ConflictClause) {
	keep := cf.DoNothing || len(cf.Update) == 0
	if w.d == MySQL {
		w.str(" AS new ON DUPLICATE KEY UPDATE ")
		if keep {
			w.col(int32(cf.Columns[0]))
			w.str(" = ")
			w.table()
			w.str(".")
			w.col(int32(cf.Columns[0]))
			return
		}
		for i, c := range cf.Update {
			if i > 0 {
				w.str(", ")
			}
			w.col(int32(c))
			w.str(" = new.")
			w.col(int32(c))
		}
		return
	}
	w.str(" ON CONFLICT (")
	w.columns(cf.Columns)
	w.str(")")
	if keep {
		w.str(" DO NOTHING")
		return
	}
	w.str(" DO UPDATE SET ")
	for i, c := range cf.Update {
		if i > 0 {
			w.str(", ")
		}
		w.col(int32(c))
		w.str(" = EXCLUDED.")
		w.col(int32(c))
	}
}

func (w *writer) updateStmt(st *Statement) {
	w.str("UPDATE ")
	w.table()
	w.str(" SET ")
	for i, a := range st.Set {
		if i > 0 {
			w.str(", ")
		}
		w.col(int32(a.Column))
		w.str(" = ")
		switch a.Op {
		case AssignAdd:
			w.col(int32(a.Column))
			w.str(" + ")
		case AssignSub:
			w.col(int32(a.Column))
			w.str(" - ")
		}
		w.arg(a.Value)
	}
	w.where(st.Where)
}

func (w *writer) deleteStmt(st *Statement) {
	w.str("DELETE FROM ")
	w.table()
	w.where(st.Where)
}
