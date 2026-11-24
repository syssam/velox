package engine

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
)

// An ordering by an expression pages by its values (decision 0166). A row
// does not hold an expression's value, so a read that pages reads it beside
// the row (Statement.OrderValues), and After takes it back with the row
// (AddAfterValues), bound into the same expression in the keyset
// condition.

// OrderValueTerm reports whether o orders by an expression whose value a
// keyset reads beside the row: an expression term (Calc, a JSON member, a
// joined column's term, a date bucket) that is not a relation's aggregate,
// which After computes from the row's key instead.
func OrderValueTerm(o OrderTerm) bool {
	t, ok := o.Expr.(Term)
	return ok && t.kind != termRel
}

// orderValueCount is the number of the values st reads beside each row:
// one per OrderValueTerm of its OrderBy, or -1 when it reads none.
func orderValueCount(st *Statement) int {
	if !st.OrderValues {
		return -1
	}
	n := 0
	for _, o := range st.OrderBy {
		if OrderValueTerm(o) {
			n++
		}
	}
	return n
}

// OrderSig is the signature of o, an ordering of the Table's rows: see
// OrderSig.
func (t Table) OrderSig(o OrderTerm) string { return OrderSig(t.Info(), o) }

// OrderSig is the signature of o, an ordering of in's rows: a short hex
// hash of the term as SQLite writes it in ORDER BY, its direction and NULL
// placement included, and of each value it binds with its Go type. Two
// orderings with the same signature sort alike; a cursor names its
// expression terms by it, so a cursor taken under another expression, or
// the same one with other bound values, is refused.
func OrderSig(in *EntityInfo, o OrderTerm) string {
	w := writer{d: SQLite, in: in}
	if o.Expr != nil {
		w.orderTerm(o)
	} else {
		w.orderBy([]OrderTerm{o}, false)
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(w.b.String()))
	for _, a := range w.args {
		_, _ = fmt.Fprintf(h, "\x00%T\x00%v", a, a)
	}
	if w.err != nil {
		_, _ = fmt.Fprintf(h, "\x00%v", w.err)
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// checkOrderValueTerm refuses of an OrderValueTerm that a keyset cannot
// page over: an aggregate or a window, whose value is not the row's own.
func checkOrderValueTerm(in *EntityInfo, o OrderTerm) error {
	t := o.Expr.(Term)
	if hasAgg(t) || hasWindow(t) {
		return fmt.Errorf("velox: %s: After cannot page over an ordering by an aggregate or a window function, whose value is not the row's own", in.Name)
	}
	return nil
}

// valueSource is the afterSource of an OrderValueTerm: the row's value of
// it, read beside the row.
func valueSource(in *EntityInfo, o OrderTerm, v any) (afterSource, error) {
	if err := checkOrderValueTerm(in, o); err != nil {
		return afterSource{}, err
	}
	return afterSource{info: in, value: true, v: v}, nil
}

// valueEqual is the condition that the expression o orders by has the value
// v: NULL equals NULL.
func valueEqual(o OrderTerm, v any) Node {
	t := o.Expr.(Term)
	if nullValue(v) {
		return valueExpr("? IS NULL", t)
	}
	return valueExpr("? = ?", t, v)
}

// valuePast is the condition that the expression o orders by is past v,
// and false when no row is: as pastValue's for a nullable column, since an
// expression can be NULL.
func valuePast(o OrderTerm, v any) (Node, bool) {
	t := o.Expr.(Term)
	cmp := " > "
	if o.Desc {
		cmp = " < "
	}
	switch null, first := nullValue(v), nullsFirst(o); {
	case null && !first:
		return Node{}, false
	case null:
		return valueExpr("? IS NOT NULL", t), true
	case first:
		return valueExpr("?"+cmp+"?", t, v), true
	}
	return valueExpr("(?"+cmp+"? OR ? IS NULL)", t, v, t), true
}

// valueExpr is the condition text with each ? filled by the next of xs: a
// Term written as it is, anything else bound.
func valueExpr(text string, xs ...any) Node {
	slots := make([]ExprSlot, len(xs))
	var args []any
	for i, x := range xs {
		if t, ok := x.(Term); ok {
			slots[i] = ExprSlot{Col: -1, Term: &t}
			continue
		}
		slots[i] = ExprSlot{Col: -1}
		args = append(args, x)
	}
	return ExprNode(text, slots, args)
}

// orderValueScan is the destinations of the values a row's read scans
// beside its columns, and where they are kept: n per row, in vals.
type orderValueScan struct {
	n    int
	dst  []any
	cell []any
	vals []any
}

// newOrderValueScan is the scan of st's order values; nil when it reads
// none.
func newOrderValueScan(st *Statement) *orderValueScan {
	n := orderValueCount(st)
	if n <= 0 {
		return nil
	}
	s := &orderValueScan{n: n, dst: make([]any, n), cell: make([]any, n)}
	for i := range s.cell {
		s.dst[i] = &s.cell[i]
	}
	return s
}

// keep appends the values the last row scanned, a []byte copied as a
// string: a driver may reuse its buffer.
func (s *orderValueScan) keep() {
	for i, v := range s.cell {
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		s.vals = append(s.vals, v)
		s.cell[i] = nil
	}
}

// width is the number of values per row, 0 for none.
func (s *orderValueScan) width() int {
	if s == nil {
		return 0
	}
	return s.n
}

// Order returns a copy of the query's orderings, in order.
func (q TableQuery) Order() []OrderTerm { return slices.Clone(q.st.OrderBy) }

// Unordered returns the query with no ordering: a caller that builds its
// own order from Order, such as a paginator, starts from it.
func (q TableQuery) Unordered() TableQuery {
	q.st.OrderBy = nil
	return q
}

// OrderValues returns the query whose All also reads the value of each
// ordering that is an expression (OrderValueTerm), beside each row, as
// AfterValues takes them back: TableRows.OrderValues. They are the values
// the driver reads into an any, a []byte copied as a string. One, First,
// Count and Exists read none.
func (q TableQuery) OrderValues() TableQuery {
	q.orderVals = true
	return q
}

// AfterValues is After for a query ordered by expressions: vals are row's
// values of the orderings that are expressions (OrderValueTerm), in order,
// as OrderValues read them with the row. Each expression is compared with
// its value in the keyset condition, the expression written there again
// with its own bound values; a nil value is NULL, placed as the ordering
// places it. The other orderings read row as After does, so the primary
// key must still end the order. The number of vals must be the number of
// such orderings, or the query fails when it runs.
func (q TableQuery) AfterValues(row any, vals []any) TableQuery {
	q = q.After(row)
	q.afterVals = slices.Clone(vals)
	if q.afterVals == nil {
		q.afterVals = []any{}
	}
	return q
}

// OrderValues returns row i's values of the query's orderings that are
// expressions, as TableQuery.OrderValues read them; nil when the query did
// not read them. The slice is the rows' own.
func (r TableRows) OrderValues(i int) []any {
	if r.nv == 0 {
		return nil
	}
	return r.vals[i*r.nv : (i+1)*r.nv : (i+1)*r.nv]
}

// scanWithValues scans the current row into dst, whose last s.width()
// destinations are the order values', and keeps them.
func scanWithValues(r Rows, dst []any, s *orderValueScan) error {
	if err := r.Scan(dst...); err != nil {
		return err
	}
	if s != nil {
		s.keep()
	}
	return nil
}
