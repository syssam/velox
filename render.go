package velox

import "strings"

// writer renders a statement. With argsOnly it collects the arguments and
// writes no text: the text is cached (render_cache.go), and one code path for
// both keeps the arguments in the order of the placeholders.
type writer struct {
	d        Dialect
	in       *EntityInfo
	b        strings.Builder
	args     []any
	argsOnly bool
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
	w.ident(w.in.Table)
	for i, p := range st.Where {
		if i == 0 {
			w.str(" WHERE ")
		} else {
			w.str(" AND ")
		}
		w.pred(p.n)
	}
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

func (w *writer) pred(n node) {
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
	}
}

// render returns a statement's SQL and arguments. Only SELECT exists until
// the writes plan.
func render(d Dialect, st *Statement) (string, []any) {
	if st.Op != OpSelect {
		panic("velox: render: only SELECT is implemented")
	}
	w := writer{d: d, in: st.Entity}
	w.selectStmt(st)
	return w.b.String(), w.args
}
