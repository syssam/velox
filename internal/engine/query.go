package engine

import (
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
		if col.Nullable {
			return fmt.Errorf("velox: %s: After cannot page over the nullable column %s", in.Name, col.Name)
		}
		// A nil slice or map is stored as NULL, which no comparison matches.
		if sliceOrMap(col.Type) {
			return fmt.Errorf("velox: %s: After cannot page over the column %s, which can be NULL", in.Name, col.Name)
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
		unique = unique || (ix.Unique && covers(ix.Columns))
	}
	if !unique {
		return fmt.Errorf("velox: %s: After needs an OrderBy that includes the primary key or a unique index", in.Name)
	}
	// (o1 > v1) OR (o1 = v1 AND o2 > v2) OR …, with < for descending columns.
	var or []Node
	for i, o := range st.OrderBy {
		var and []Node
		for _, prev := range st.OrderBy[:i] {
			and = append(and, Node{Op: OpEQ, Col: int32(prev.Column), V: in.kit.Value(after, prev.Column)})
		}
		cmp := OpGT
		if o.Desc {
			cmp = OpLT
		}
		and = append(and, Node{Op: cmp, Col: int32(o.Column), V: in.kit.Value(after, o.Column)})
		if len(and) == 1 {
			or = append(or, and[0])
		} else {
			or = append(or, Group(OpAnd, and))
		}
	}
	st.Where = append(st.Where, NewPredicate(Group(OpOr, or)))
	return nil
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
