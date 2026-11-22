package engine

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
	"unsafe"
)

// sliceOrMap reports whether t, a reflect.Type, is a slice or map type. It is
// not generic, so that prepare holds no reflect.Type.
func sliceOrMap(t any) bool {
	k := rtype(t).Kind()
	return k == reflect.Slice || k == reflect.Map
}

// AddAfter adds to st, a statement on entity in, After's condition for the
// row at after: its rows past that row in st's OrderBy. It is not generic, so
// that it is compiled once; the row's values are read through in's kit. A term
// of OrderBy on a joined relation reads its value from the joined entity stored
// in the row's relation field, so the row must have been read with that join.
// Of a left join, a row with no joined entity pages from NULL, as the join
// read it.
func AddAfter(st *Statement, in *EntityInfo, after unsafe.Pointer) error {
	if len(st.OrderBy) == 0 {
		return fmt.Errorf("velox: %s: After needs an OrderBy", in.Name)
	}
	for _, o := range st.OrderBy {
		if o.Expr != nil {
			return errAfterExpr(in.Name)
		}
	}
	srcs, ordered, err := afterSources(st, in, after)
	if err != nil {
		return err
	}
	if !orderIsTotal(in, ordered) {
		return fmt.Errorf("velox: %s: After needs an OrderBy that includes the primary key or a unique index", in.Name)
	}
	st.Where = append(st.Where, NewPredicate(afterCondition(st, srcs)))
	return nil
}

// afterSources returns each OrderBy term's entity and the row to read its
// value from: the statement's own, or a joined one. It also returns the
// set of the statement's own columns that the order names, and refuses a
// term that After cannot page over.
func afterSources(st *Statement, in *EntityInfo, after unsafe.Pointer) ([]afterSource, map[int]bool, error) {
	srcs := make([]afterSource, len(st.OrderBy))
	ordered := make(map[int]bool, len(st.OrderBy))
	for i, o := range st.OrderBy {
		src := afterSource{info: in, row: after}
		if o.Rel != 0 {
			var err error
			if src, err = joinedSource(st, in, after, o); err != nil {
				return nil, nil, err
			}
		}
		srcs[i] = src
		if o.Column < 0 || o.Column >= len(src.info.Columns) {
			return nil, nil, fmt.Errorf("velox: %s: an ordering names column %d of %s, which has %d", in.Name, o.Column, src.info.Name, len(src.info.Columns))
		}
		col := src.info.Columns[o.Column]
		if col.Codec {
			return nil, nil, errCodecOrder(src.info, o.Column)
		}
		// A nil slice or map is stored as NULL, which no comparison matches.
		if sliceOrMap(col.Type) {
			return nil, nil, fmt.Errorf("velox: %s: After cannot page over the column %s, which can be NULL", src.info.Name, col.Name)
		}
		if o.Rel != 0 {
			continue // a joined row is read whole
		}
		// A row this query returns holds the zero value of a column it did not
		// select, which the comparison would page from.
		if st.Columns != nil && !slices.Contains(st.Columns, o.Column) {
			return nil, nil, fmt.Errorf("velox: %s: After needs the order column %s selected; add it to Columns", in.Name, col.Name)
		}
		ordered[o.Column] = true
	}
	return srcs, ordered, nil
}

// orderIsTotal reports whether the columns of in that ordered holds cover
// its primary key or a unique index of non-nullable columns. Ties are broken
// by the statement's own key: a joined column is not unique for the rows
// that share its row.
func orderIsTotal(in *EntityInfo, ordered map[int]bool) bool {
	covers := func(cols []int) bool {
		for _, c := range cols {
			if !ordered[c] {
				return false
			}
		}
		return len(cols) > 0
	}
	if covers(in.PrimaryKey) {
		return true
	}
	for _, ix := range in.Indexes {
		// NULLs do not collide, so a unique index of a nullable column does
		// not make the order total.
		if ix.Unique && covers(ix.Columns) && !slices.ContainsFunc(ix.Columns, func(c int) bool { return in.Columns[c].Nullable }) {
			return true
		}
	}
	return false
}

// onOrderRow returns n, the condition on o's column, on the joined row when
// o is on a joined relation.
func onOrderRow(o OrderTerm, n Node) Node {
	if o.Rel != 0 {
		return JoinedNode(o.Rel-1, []Node{n})
	}
	return n
}

// orderTerm returns the condition op on o's column against v, on the joined
// row when o is.
func orderTerm(o OrderTerm, op op, v any) Node {
	return onOrderRow(o, Node{Op: op, Col: int32(o.Column), V: v})
}

// afterCondition returns the condition that holds for the rows past the
// row srcs read from in st's OrderBy.
//
// It is (o1 > v1) OR (o1 = v1 AND o2 > v2) OR …, with < for descending
// columns. NULL sorts after every value (the order puts it there, orderBy),
// so past a value ascending is > v OR IS NULL, past NULL ascending is
// nothing, and past NULL descending is IS NOT NULL; NULL equals NULL. A
// column of a left join is NULL where the join found no row, as a nullable
// column is.
func afterCondition(st *Statement, srcs []afterSource) Node {
	value := func(i int) any {
		if srcs[i].row == nil {
			return nil // a left join found no row
		}
		return srcs[i].info.kit.Value(srcs[i].row, st.OrderBy[i].Column)
	}
	nullable := func(i int) bool { return srcs[i].left || srcs[i].info.Columns[st.OrderBy[i].Column].Nullable }
	var or []Node
	for i, o := range st.OrderBy {
		var and []Node
		for k, prev := range st.OrderBy[:i] {
			v := value(k)
			if nullable(k) && nullValue(v) {
				and = append(and, orderTerm(prev, OpIsNull, nil))
			} else {
				and = append(and, orderTerm(prev, OpEQ, v))
			}
		}
		past, ok := pastValue(o, value(i), nullable(i))
		if !ok {
			continue // no row is past NULL
		}
		switch {
		case len(and) > 0:
			or = append(or, Group(OpAnd, append(and, past)))
		case past.Op == OpOr: // flattened: the outer group is an OR too
			or = append(or, past.Kids...)
		default:
			or = append(or, past)
		}
	}
	return Group(OpOr, or)
}

// pastValue returns the condition on o's column for the rows past the
// value v, and false when no row is past it: v is NULL and o ascends.
// nullable says whether the column can be NULL.
func pastValue(o OrderTerm, v any, nullable bool) (Node, bool) {
	past := orderTerm(o, OpGT, v)
	if o.Desc {
		past = orderTerm(o, OpLT, v)
	}
	if !nullable {
		return past, true
	}
	switch null := nullValue(v); {
	case null && !o.Desc:
		return Node{}, false
	case null:
		past = orderTerm(o, OpNotNull, nil)
	case !o.Desc:
		past = onOrderRow(o, Group(OpOr, []Node{{Op: OpGT, Col: int32(o.Column), V: v}, {Op: OpIsNull, Col: int32(o.Column)}}))
	}
	return past, true
}

// afterSource is the row After reads one ordered column from, and the entity it is
// a row of. Of a left join, row is nil when the join found none.
type afterSource struct {
	info *EntityInfo
	row  unsafe.Pointer
	left bool // of a left join: the column is NULL where the join found no row
}

// joinedSource is the joined entity that o, an ordering by a joined relation, reads
// from the row at after: the one stored in the row's relation field. It fails when
// the statement does not join the relation, or, of an inner join, the row has no
// joined entity: it was not read with the join, and has no value to page from. Of a
// left join, a row with none pages from NULL, as the join reads a missing row.
func joinedSource(st *Statement, in *EntityInfo, after unsafe.Pointer, o OrderTerm) (afterSource, error) {
	rel := o.Rel - 1
	i, ok := joinOf(st.Joins, rel)
	if !ok {
		return afterSource{}, fmt.Errorf("velox: %s: an ordering by a joined relation needs Join or LeftJoin of it in the same query", in.Name)
	}
	l, err := st.Joins[i].Ref.Link()
	if err != nil {
		return afterSource{}, err
	}
	off, err := RelOffset(in, rel)
	if err != nil {
		return afterSource{}, err
	}
	h := (*Holder)(unsafe.Add(after, off))
	left := st.Joins[i].Left
	if h.V == nil && left {
		return afterSource{info: l.Target, left: true}, nil
	}
	if h.V == nil {
		return afterSource{}, fmt.Errorf("velox: %s: After cannot page over a column of %s: the row has none joined; read it with the same Join", in.Name, l.Name)
	}
	// V holds a *T, which an interface keeps in its data word.
	return afterSource{info: l.Target, row: dataWord(h.V), left: left}, nil
}

// nullValue reports whether v, a nullable column's value, is stored as NULL:
// a nil pointer, or a driver.Valuer whose value is nil.
func nullValue(v any) bool {
	if isNil(v) {
		return true
	}
	if dv, ok := v.(driver.Valuer); ok {
		x, err := dv.Value()
		return err == nil && x == nil
	}
	return false
}

// CloneStatement copies a query's statement for one run, with its slices
// capped so that anything velox itself appends to them (a rewrite hook's
// condition, say) cannot reach the query's slices. It does not copy their
// elements: DB.Run does that before a hook, which may edit them, sees the
// statement.
func CloneStatement(st *Statement) Statement {
	c := *st
	c.Where = slices.Clip(c.Where)
	c.OrderBy = slices.Clip(c.OrderBy)
	c.Columns = slices.Clip(c.Columns)
	c.Set = slices.Clip(c.Set)
	c.Returning = slices.Clip(c.Returning)
	return c
}

// ColumnCount is how many columns st reads: its Columns, or all of its entity's.
func ColumnCount(st *Statement) int {
	if st.Columns != nil {
		return len(st.Columns)
	}
	return len(st.Entity.Columns)
}

// ErrIterAgain is what Iter's reading returns when an around hook runs the
// statement again after rows were yielded, which cannot be taken back.
var ErrIterAgain = errors.New("an Around hook called next again after Iter had yielded rows")

// AddSince adds the time since start to *d.
func AddSince(d *time.Duration, start time.Time) { *d += time.Since(start) }

// CountRun is the state of one Count.
type CountRun struct {
	St Statement
	N  int64
}

// ReadCount reads a count's one row from r into c.N, which stays 0 when r has none.
func ReadCount(c *CountRun, r Rows) error { return scanCount(&c.N, r) }

// scanCount reads a count's one row from r into *n, which stays 0 when r
// has none.
func scanCount(n *int64, r Rows) error {
	*n = 0
	if r.Next() {
		return r.Scan(n)
	}
	return nil
}

// ExistsRun is the state of one Exists.
type ExistsRun struct {
	St    Statement
	Found bool
}

// ReadExists reads no column: a row is the answer.
func ReadExists(x *ExistsRun, r Rows) error {
	x.Found = r.Next()
	return nil
}
