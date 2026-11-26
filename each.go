package velox

import (
	"context"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// LoadEach returns, for each of owners in order, its rows of q related
// through r: q's conditions, After, Columns and scope, r's Where, and the
// order q's OrderBy, then r's, then the target's key. r's LimitEach is the
// most rows per owner, in that order, and r's With loads apply to the rows.
// One statement per chunk of owners under the dialect's parameter limit.
// q's Columns gain the relation's key columns when it leaves them out. A q
// with a Limit, an Offset or a lock is refused; a nil owner fails, naming
// the entity; an owner whose key is NULL gets no rows.
//
// q's own With loads apply to the rows too. Each owner gets a slice of its
// own, empty when it has no rows; owners with one key get equal slices. The
// slices share the arrays the rows were read into, and one array of the rows
// copied out of them, each capped at its length, so a row kept from one keeps
// the others' rows alive too: copy it to keep it alone. The
// statements are q's entity's, so its Read policy, a scope included, and hooks see
// them, and a row they hide takes no place in a page. Their cap is r's
// MaxRows, else q's, else LimitEach's bound (LimitEach rows per owner), else
// the DB's. A many-to-many relation is read in the same one statement, joined
// to its join table, as Rel.LimitEach reads it.
func LoadEach[E, T any](ctx context.Context, q Query[T], r Rel[E, T], owners []*E) ([][]*T, error) {
	owner, err := ownerOf[E]()
	// A []*E has the layout of a []unsafe.Pointer, as in Rel.Of: the work is
	// not generic, so that it is compiled once.
	ps := unsafe.Slice((*unsafe.Pointer)(unsafe.Pointer(unsafe.SliceData(owners))), len(owners))
	parts, err := engine.LoadEach(ctx, eachOf(q), owner, err, r.spec(), ps)
	if err != nil {
		return nil, err
	}
	n := 0
	for _, p := range parts {
		n += len(p.([]T))
	}
	buf := make([]*T, n) // one array for every owner's pointers
	out := make([][]*T, len(parts))
	for i, p := range parts {
		rows := p.([]T)
		out[i] = buf[:len(rows):len(rows)]
		for k := range rows {
			out[i][k] = &rows[k]
		}
		buf = buf[len(rows):]
	}
	return out, nil
}

// CountEach returns, for each of owners in order, the number of q's rows
// related to it through r, as LoadEach would read them without a limit:
// SELECT <owner key>, COUNT(*) … GROUP BY <owner key>, one statement per
// chunk of owners. An owner with none gets 0, and so does an owner whose key
// is NULL. A nil owner fails the call, with an error naming the entity.
//
// r's LimitEach, OrderBy and With do not apply, nor q's order and columns.
// q's Limit, Offset or a lock is refused, as by LoadEach, unlike Query.Count,
// which ignores Limit and Offset. A many-to-many relation counts each target
// once per owner, as LoadEach reads it.
func CountEach[E, T any](ctx context.Context, q Query[T], r Rel[E, T], owners []*E) ([]int64, error) {
	owner, err := ownerOf[E]()
	ps := unsafe.Slice((*unsafe.Pointer)(unsafe.Pointer(unsafe.SliceData(owners))), len(owners))
	return engine.CountEach(ctx, eachOf(q), owner, err, r.spec(), ps)
}

// eachOf is what LoadEach and CountEach read of q, without its type.
func eachOf[T any](q Query[T]) engine.EachQuery {
	e := engine.EachQuery{St: q.s.St, After: q.s.After, Loads: q.s.Loads, Err: q.s.Err, Joins: q.s.Joins.Scanner()}
	if pub(q.s.DB) != nil {
		e.DB = q.s.DB
	}
	if q.meta != nil {
		e.In = q.meta.Info
		if q.meta.Err != nil {
			e.Err = q.meta.Err
		}
	}
	return e
}
