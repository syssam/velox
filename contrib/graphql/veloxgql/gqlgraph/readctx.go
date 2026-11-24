package gqlgraph

import (
	"context"

	"github.com/syssam/velox"
)

// readContext is the context a Connection reads under (decision 0172):
// the context of the read, which gives its Deadline, Done and Err, with
// the values of the context Paginate was given first.
//
// The read's own transaction mark is dropped (velox.WithoutTx): a
// transaction is a value, and so it comes from Paginate's context alone.
// A Paginate under velox.WithoutTx then reads outside the transaction
// whatever the reading context carries.
type readContext struct {
	context.Context                 // the reading context, without its transaction mark
	vals            context.Context // Paginate's context, without its cancellation
}

// Value returns vals' value of key, or, when it has none, the reading
// context's.
func (c readContext) Value(key any) any {
	if v := c.vals.Value(key); v != nil {
		return v
	}
	return c.Context.Value(key)
}

// readIn returns the context c reads under when read under ctx: ctx's
// cancellation and deadline, with the values of the context c was made
// in first. A connection made in no context reads under ctx.
//
// The cancellation lookup of the context package (its cancelCtxKey) finds
// nothing in vals, which context.WithoutCancel made, and so finds ctx's: a
// context derived from the result is a child of ctx's cancellation, with
// no goroutine of its own.
func (c *Connection) readIn(ctx context.Context) context.Context {
	if c.from == nil {
		return ctx
	}
	return readContext{Context: velox.WithoutTx(ctx), vals: c.from}
}
