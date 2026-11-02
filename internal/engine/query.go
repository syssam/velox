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
// that it is compiled once; the row's values are read through in's kit.
func AddAfter(st *Statement, in *EntityInfo, after unsafe.Pointer) error {
	if len(st.OrderBy) == 0 {
		return fmt.Errorf("velox: %s: After needs an OrderBy", in.Name)
	}
	ordered := make(map[int]bool, len(st.OrderBy))
	for _, o := range st.OrderBy {
		col := in.Columns[o.Column]
		if col.Codec {
			return errCodecOrder(in, o.Column)
		}
		// A nil slice or map is stored as NULL, which no comparison matches.
		if sliceOrMap(col.Type) {
			return fmt.Errorf("velox: %s: After cannot page over the column %s, which can be NULL", in.Name, col.Name)
		}
		// A row this query returns holds the zero value of a column it did not
		// select, which the comparison would page from.
		if st.Columns != nil && !slices.Contains(st.Columns, o.Column) {
			return fmt.Errorf("velox: %s: After needs the order column %s selected; add it to Columns", in.Name, col.Name)
		}
		ordered[o.Column] = true
	}
	covers := func(cols []int) bool {
		for _, c := range cols {
			if !ordered[c] {
				return false
			}
		}
		return len(cols) > 0
	}
	unique := covers(in.PrimaryKey)
	for _, ix := range in.Indexes {
		// NULLs do not collide, so a unique index of a nullable column does
		// not make the order total.
		unique = unique || (ix.Unique && covers(ix.Columns) && !slices.ContainsFunc(ix.Columns, func(c int) bool { return in.Columns[c].Nullable }))
	}
	if !unique {
		return fmt.Errorf("velox: %s: After needs an OrderBy that includes the primary key or a unique index", in.Name)
	}
	// (o1 > v1) OR (o1 = v1 AND o2 > v2) OR …, with < for descending columns.
	// NULL sorts after every value (the order puts it there, orderBy), so
	// past a value ascending is > v OR IS NULL, past NULL ascending is
	// nothing, and past NULL descending is IS NOT NULL; NULL equals NULL.
	var or []Node
	for i, o := range st.OrderBy {
		var and []Node
		for _, prev := range st.OrderBy[:i] {
			v := in.kit.Value(after, prev.Column)
			if in.Columns[prev.Column].Nullable && nullValue(v) {
				and = append(and, Node{Op: OpIsNull, Col: int32(prev.Column)})
			} else {
				and = append(and, Node{Op: OpEQ, Col: int32(prev.Column), V: v})
			}
		}
		v, col := in.kit.Value(after, o.Column), int32(o.Column)
		past := Node{Op: OpGT, Col: col, V: v}
		if o.Desc {
			past.Op = OpLT
		}
		if in.Columns[o.Column].Nullable {
			switch null := nullValue(v); {
			case null && !o.Desc:
				continue // no row is past NULL
			case null:
				past = Node{Op: OpNotNull, Col: col}
			case !o.Desc:
				past = Group(OpOr, []Node{past, {Op: OpIsNull, Col: col}})
			}
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
	st.Where = append(st.Where, NewPredicate(Group(OpOr, or)))
	return nil
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
func ReadCount(c *CountRun, r Rows) error {
	c.N = 0
	if r.Next() {
		return r.Scan(&c.N)
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
