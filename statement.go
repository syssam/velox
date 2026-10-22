package velox

import (
	"context"
	"time"
)

// Op is the kind of a Statement.
type Op uint8

// The kinds of statement.
const (
	OpSelect Op = iota + 1
	OpInsert
	OpUpdate
	OpDelete
	// OpRawQuery is raw SQL run by DB.Raw, which velox treats as a read: its
	// SQL and Args are the caller's.
	OpRawQuery
	// OpRawExec is raw SQL run by DB.Exec, which velox treats as a write: its
	// SQL and Args are the caller's.
	OpRawExec
)

// String returns the operation's SQL verb in lower case: "select", "insert",
// "update" or "delete"; "raw query" and "raw exec" for raw SQL.
func (o Op) String() string {
	switch o {
	case OpSelect:
		return "select"
	case OpInsert:
		return "insert"
	case OpUpdate:
		return "update"
	case OpDelete:
		return "delete"
	case OpRawQuery:
		return "raw query"
	case OpRawExec:
		return "raw exec"
	}
	return "unknown"
}

// Raw reports whether o is raw SQL: OpRawQuery or OpRawExec.
func (o Op) Raw() bool { return o == OpRawQuery || o == OpRawExec }

// Reads reports whether o reads: OpSelect, or Raw's query (OpRawQuery).
// Every other Op writes, Exec's raw SQL included.
func (o Op) Reads() bool { return o == OpSelect || o == OpRawQuery }

// AssignOp is how an UPDATE assigns a column.
type AssignOp uint8

// The assignment operators.
const (
	AssignSet AssignOp = iota + 1 // column = value
	AssignAdd                     // column = column + value
	AssignSub                     // column = column - value
)

// Assignment is one column an UPDATE sets.
type Assignment struct {
	Column int // index into EntityInfo.Columns
	Op     AssignOp
	Value  any
}

// ConflictClause is an INSERT's upsert clause.
type ConflictClause struct {
	Columns   []int // the unique key whose conflict the insert resolves
	Update    []int // columns overwritten with the inserted row's values
	DoNothing bool  // keep the existing row; also what an empty Update means
	// Where is the condition of the update branch, on the existing row: a
	// conflicting row it rejects is neither updated nor replaced. PostgreSQL
	// and SQLite render it; velox refuses it on MySQL.
	Where []Predicate

	policy bool // the scope added the policy's conditions to Where
}

// Statement is one operation on its way to the database. Every query becomes
// one, and the pipeline's hooks see and may change it: rewrite hooks before it
// is rendered, around hooks while it runs.
//
// Raw SQL (OpRawQuery and OpRawExec) is not rendered: Rewrite hooks do not
// see it, and its SQL, Args and Fingerprint are set when it is made. Its
// Entity is the entity Raw scans into, or for Exec an EntityInfo named "Exec"
// with no table or columns. Its TenantBypass and PolicyBypass both hold the
// caller's reason, as given, untrimmed (so does an AllTenants or SkipPolicy
// reason).
//
// The EntityInfo that Entity points to is shared by every query on the
// entity, in every goroutine: treat it as read-only. A rewrite hook may point
// Entity at an EntityInfo of its own (a copy with another Table, say), but
// must never change the shared one.
type Statement struct {
	Op           Op
	Entity       *EntityInfo // shared and read-only: replace it, never edit it
	Count        bool        // SELECT COUNT(*): Count clears Columns, OrderBy, Limit and Offset; a Rewrite hook that sets them gets them rendered
	Columns      []int       // SELECT: the selected columns, nil selects all; INSERT: the columns written
	Where        []Predicate // all must hold
	OrderBy      []OrderTerm
	Limit        int // 0: no limit; after the Rewrite hooks, velox lowers it to the row cap + 1
	Offset       int
	Unbounded    bool            // the query said Unbounded
	Writable     bool            // SELECT: the query said Writable; the scope adds the Write policy, and a Rewrite hook may not change it
	MaxRows      int             // SELECT and OpRawQuery: the most rows it may return; 0: the DB's WithMaxRows
	Set          []Assignment    // UPDATE: the assignments
	Rows         [][]any         // INSERT: the rows, one value per Columns entry
	Conflict     *ConflictClause // INSERT: the upsert clause, if any
	Returning    []int           // INSERT: columns read back (PostgreSQL and SQLite)
	AllRows      bool            // UPDATE and DELETE: the statement said All
	Tenant       any             // the tenant scope applied; nil when unscoped
	TenantBypass string          // the AllTenants reason, when it applied; raw SQL's reason
	PolicyBypass string          // the SkipPolicy reason, when it applied; raw SQL's reason
	DryRun       bool            // built for SQL(ctx), not sent
	InTx         bool
	Tx           *TxInfo // the transaction the statement runs in, when the DB has hooks; nil otherwise
	Dialect      Dialect // the DB's: an Around hook has only the statement
	// Tags are the context's WithTags tags, set before the hooks run. A
	// Rewrite hook gets its own copy; it may change them, and the SQL
	// comment is rendered from what it leaves.
	Tags []Tag
	// Caller is the statement's call site, with WithCallers; zero
	// otherwise. A Rewrite hook may change it, as Tags.
	Caller      Caller
	SQL         string // set by rendering, after the rewrite hooks: the text sent, WithSQLComment's comment included
	Fingerprint string // set by rendering: the shape's ID, the hash of SQL without the comment
	Args        []any  // set by rendering
	// Route is where the statement runs: set by the route stage, after
	// rendering. Rewrite hooks run before it and see it unset (zero, not
	// Replica -1); Around hooks, and everything after them, see it set. An
	// Around hook reads it, with one exception: setting Route.Fallback to true
	// sends the hook's retry to the primary. Changing ReadPrimary, OnShard or
	// WithSession in the context passed to next does not re-route the
	// statement, as routing happens before the Around hooks.
	Route Route
	// Result is reset before the Around hooks run, and set by each
	// execution before next returns to them; a hook that retries sees the
	// last attempt's.
	Result Result

	ran target // where exec sent it; its With loads run there
	// handed is set once Iter has handed the caller a row: a broken
	// connection is then not failed over, as the read cannot start again.
	handed bool
}

// Result is what executing a statement did: a Statement's, which an Around
// hook reads. Mutation.Result gives an entity hook one for the whole write,
// with only Affected set; see there.
type Result struct {
	// Rows counts the rows read, an INSERT … RETURNING's and Count's single
	// row included. Rows are counted only when the DB has an Around hook or
	// the statement a row cap: otherwise nothing can read it, and it is 0.
	Rows int64
	// Affected is the rows a write affected: the driver's count for one sent
	// without RETURNING, the rows returned for one sent with it. It is -1
	// for a read and for a failed statement.
	Affected int64
	// LoopBody is the time Iter's caller spent in the loop body, summed over
	// the rows it yielded, the last one included when the loop broke or the
	// body panicked on it. It grows while next runs and is final once next
	// returns or panics. It is measured only when the DB has an Around hook,
	// and is 0 otherwise and for every method but Iter.
	LoopBody time.Duration
}

// Rewrite is a hook that may change a statement before it is rendered, or
// refuse it by returning an error. Rewrite hooks run in the order given to
// Open. They run for the SQL(ctx) methods too, with the statement's DryRun
// set, so a hook must change nothing but the statement. They do not run for
// raw SQL (OpRawQuery and OpRawExec), which has nothing to rewrite; Around
// hooks see it.
type Rewrite func(ctx context.Context, st *Statement) error

// Around is a hook that wraps a statement's execution. next runs the
// statement and reads all its rows, so the hook wraps everything from sending
// it to its last row; with Iter, that includes the body of the caller's loop,
// and Result.LoopBody says how much of next was the loop body.
// The context passed to next must stay valid until next returns: a hook may
// set a deadline and cancel it once next has returned. A write pins the
// session (WithSession) found in the context the hook passes to next.
//
// A hook must call next, and return its error, unless it returns an error of
// its own instead. It may call next again to retry after an error; running
// the statement again after Iter has yielded rows is an error. A retry is
// sent where the route stage now points: a read that fell over to the
// primary (Route.Fallback), on a connection error from its query or its rows,
// is retried there, not on the replica in back-off. The first Around given
// to Open is the outermost.
//
// With Iter the caller's loop body runs inside the hooks, so a hook that
// recovers a panic and returns normally makes Go report that a range function
// recovered a loop body panic: let panics through.
type Around func(ctx context.Context, st *Statement, next func(context.Context) error) error
