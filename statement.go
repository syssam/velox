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

// String returns the operation's SQL verb in lower case: "select", "insert",
// "update" or "delete".
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
	}
	return "unknown"
}

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
// is rendered, around hooks while it runs (design §5).
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
	MaxRows      int             // SELECT: the most rows it may return; 0: the DB's WithMaxRows
	Set          []Assignment    // UPDATE: the assignments
	Rows         [][]any         // INSERT: the rows, one value per Columns entry
	Conflict     *ConflictClause // INSERT: the upsert clause, if any
	Returning    []int           // INSERT: columns read back (PostgreSQL and SQLite)
	AllRows      bool            // UPDATE and DELETE: the statement said All
	Tenant       any             // the tenant scope applied; nil when unscoped
	TenantBypass string          // the AllTenants reason, when it applied
	PolicyBypass string          // the SkipPolicy reason, when it applied
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
	// Result is set by each execution, before next returns to the Around
	// hooks; a hook that retries sees the last attempt's.
	Result Result
}

// Result is what executing a statement did.
type Result struct {
	// Rows counts the rows read, an INSERT … RETURNING's and Count's single
	// row included. Rows are counted only when the DB has an Around hook or
	// the statement a row cap: otherwise nothing can read it, and it is 0.
	Rows int64
	// Affected is the rows a write affected: the driver's count for one sent
	// without RETURNING, the rows returned for one sent with it. It is -1
	// for a read, for a failed statement, and when the driver does not say.
	Affected int64
}

// Rewrite is a hook that may change a statement before it is rendered, or
// refuse it by returning an error. Rewrite hooks run in the order given to
// Open. They run for the SQL(ctx) methods too, with the statement's DryRun
// set, so a hook must change nothing but the statement.
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
