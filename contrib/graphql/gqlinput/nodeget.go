package gqlinput

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/orm"
)

// NodeGet returns the Get of a type of node, from the entity's variable of
// the generated root package: the node with an id of any type, as N, the
// interface of the nodes. An id that is not of the entity's id type (IDAs) is
// the id of no node of it, and the answer is the entity's velox.ErrNotFound.
//
// A generated tree lists it per type of node, in NodeTypes.
func NodeGet[N any, PT any, E orm.Predicate, ID any](c *orm.ClientOf[PT, E, ID]) func(ctx context.Context, ex velox.Executor, id any) (N, error) {
	return func(ctx context.Context, ex velox.Executor, id any) (N, error) {
		var none N
		v, ok := IDAs[ID](id)
		if !ok {
			return none, c.Meta.NotFound()
		}
		e, err := c.Get(ctx, ex, v)
		if err != nil {
			return none, err
		}
		n, ok := any(e).(N)
		if !ok {
			return none, c.Meta.NotFound()
		}
		return n, nil
	}
}
