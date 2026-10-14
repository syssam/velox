package velox

import "context"

// Op is the kind of a Statement.
type Op uint8

// The kinds of statement.
const (
	OpSelect Op = iota + 1
	OpInsert
	OpUpdate
	OpDelete
)

// Statement is one operation on its way to the database. Every query becomes
// one, and the pipeline's hooks see and may change it: rewrite hooks before it
// is rendered, around hooks while it runs (design §5).
//
// The EntityInfo that Entity points to is shared by every query on the
// entity, in every goroutine: treat it as read-only. A rewrite hook may point
// Entity at an EntityInfo of its own (a copy with another Table, say), but
// must never change the shared one.
type Statement struct {
	Op        Op
	Entity    *EntityInfo // shared and read-only: replace it, never edit it
	Count     bool        // SELECT COUNT(*) instead of columns
	Columns   []int       // the selected columns; nil selects all
	Where     []Predicate // all must hold
	OrderBy   []OrderTerm
	Limit     int // 0: no limit
	Offset    int
	Unbounded bool // the query said Unbounded
	InTx      bool
	SQL       string // set by rendering, after the rewrite hooks
	Args      []any  // set by rendering
}

// Rewrite is a hook that may change a statement before it is rendered, or
// refuse it by returning an error. Rewrite hooks run in the order given to
// Open.
type Rewrite func(ctx context.Context, st *Statement) error

// Around is a hook that wraps a statement's execution. next runs the
// statement and reads all its rows, so the hook wraps everything from sending
// it to its last row; with Iter, that includes the body of the caller's loop.
// The context passed to next must stay valid until next returns: a hook may
// set a deadline and cancel it once next has returned.
//
// A hook must call next, and return its error, unless it returns an error of
// its own instead. It may call next again to retry after an error; running
// the statement again after Iter has yielded rows is an error. The first
// Around given to Open is the outermost.
type Around func(ctx context.Context, st *Statement, next func(context.Context) error) error
