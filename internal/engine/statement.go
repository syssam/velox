package engine

import (
	"context"
)

// lockSQL and lockWaitSQL are the clauses, after LIMIT and OFFSET.
var (
	lockSQL     = [...]string{LockForUpdate: " FOR UPDATE", LockForShare: " FOR SHARE"}
	lockWaitSQL = [...]string{LockSkipLocked: " SKIP LOCKED", LockNoWait: " NOWAIT"}
)

// ConflictClause is an INSERT's upsert clause.
type ConflictClause struct {
	Columns   []int // the unique key whose conflict the insert resolves
	Update    []int // columns overwritten with the inserted row's values
	DoNothing bool  // keep the existing row; also what an empty Update means
	// Where is the condition of the update branch, on the existing row: a
	// conflicting row it rejects is neither updated nor replaced. PostgreSQL
	// and SQLite render it; velox refuses it on MySQL.
	Where []Predicate
}

// EachClause makes a SELECT read, or count, the rows of each owner apart: the
// rows whose owner column holds one of Keys. velox sets it for a With load
// with Rel.LimitEach, for LoadEach and for CountEach; a Rewrite hook may not
// change it. With Count, the statement reads each owner's key and its count,
// one row per owner that has rows.
type EachClause struct {
	Keys   []any // the owners' keys, distinct
	Column int   // without Join: the column of Entity that holds an owner's key
	// Join is the join entity of a many-to-many relation; nil otherwise. The
	// statement joins it on Join.Columns[JoinTarget] = Entity.Columns[Target]
	// and partitions by Join.Columns[JoinOwner].
	Join                  *EntityInfo
	JoinOwner, JoinTarget int
	Target                int
	// Limit is the most rows per owner, in the statement's OrderBy; 0 is no
	// limit, and is always 0 on a Count.
	Limit int
}

// keyed reports whether, on entity in, c reads rows by in's one-column
// primary key: each owner then has at most one row, and needs no window.
func (c *EachClause) keyed(in *EntityInfo) bool {
	return c.Join == nil && len(in.PrimaryKey) == 1 && in.PrimaryKey[0] == c.Column
}

// windowed reports whether c's statement, on entity in, ranks each owner's
// rows with ROW_NUMBER() and keeps the first Limit of them.
func (c *EachClause) windowed(in *EntityInfo) bool { return c.Limit > 0 && !c.keyed(in) }

// Statement is one operation on its way to the database. Every query becomes
// one, and the pipeline's hooks see and may change it: rewrite hooks before it
// is rendered, around hooks while it runs.
//
// Raw SQL (OpRawQuery and OpRawExec) is not rendered: Rewrite hooks do not see
// it, and its SQL, Args and Fingerprint are set when it is made. Its Entity is
// the entity Raw scans into, or for Exec an EntityInfo named "Exec" with no
// table or columns. Its TenantBypass and PolicyBypass both hold the caller's
// reason, as given, untrimmed (so does a WithAllTenants or WithoutPolicy
// reason).
//
// The EntityInfo that Entity points to is shared by every query on the
// entity, in every goroutine: treat it as read-only. A rewrite hook may point
// Entity at an EntityInfo of its own (a copy with another Table, say), but
// must never change the shared one. Pointing it at another table does not
// change the tables inside an EXISTS (see Rewrite).
type Statement struct {
	Op           Op
	Entity       *EntityInfo // shared and read-only: replace it, never edit it
	Count        bool        // SELECT COUNT(*): Count clears Columns, OrderBy, Limit and Offset; a Rewrite hook may not change it, or set Columns, and one that sets the others gets them rendered
	Exists       bool        // SELECT 1 … LIMIT 1: Exists clears Columns, OrderBy and Offset and sets Limit to 1; a Rewrite hook may not change it, or set Columns
	Aggregate    Aggregate   // SELECT SUM(c) …: Columns holds c alone; like Count, it clears OrderBy, Limit and Offset; a Rewrite hook may not change it, or Columns
	GroupBy      int         // SELECT with Count or Aggregate: the key column's index plus one, 0 for none: one row per key, the key first; velox orders by the key, and a Rewrite hook may not change it
	Each         *EachClause // SELECT: a relation read or counted per owner; a Rewrite hook may not change it
	Lock         Lock        // SELECT: ForUpdate or ForShare; 0 for a read that locks nothing; a Rewrite hook may not change it
	LockWait     LockWait    // SELECT: SkipLocked or NoWait; a Rewrite hook may not change it
	Columns      []int       // SELECT: the selected columns, nil selects all; INSERT: the columns written
	Where        []Predicate // all must hold
	OrderBy      []OrderTerm
	Limit        int // 0: no limit; after the Rewrite hooks, velox lowers it to the row cap + 1
	Offset       int
	Unbounded    bool            // the query said Unbounded
	Writable     bool            // SELECT: the query said Writable; the scope adds the Write policy, and a Rewrite hook may not change it
	MaxRows      int             // SELECT and OpRawQuery: the most rows it may return; 0: the DB's WithMaxRows, or, with Each, its bound by construction (Rel.MaxRows sets it)
	Set          []Assignment    // UPDATE: the assignments
	Rows         [][]any         // INSERT: the rows, one value per Columns entry
	Conflict     *ConflictClause // INSERT: the upsert clause, if any
	Returning    []int           // INSERT: columns read back (PostgreSQL and SQLite)
	AllRows      bool            // UPDATE and DELETE: the statement said All
	Tenant       any             // the tenant scope applied, as the type of the entity's tenant column; nil when unscoped; when the entity is unscoped but the statement reads a scoped entity, the id as WithTenant was given it
	TenantBypass string          // the WithAllTenants reason, when it applied; raw SQL's reason
	PolicyBypass string          // the WithoutPolicy reason, when it applied; raw SQL's reason
	DryRun       bool            // built for SQL(ctx), not sent
	InTx         bool            // set by velox: the statement runs in a transaction, inside db.Tx or one velox opens for it (a split InsertAll, a policy-conditioned upsert, an EdgeBidi write); a Rewrite hook may not change it
	Tx           *TxInfo         // the transaction the statement runs in, when the DB has hooks; nil otherwise
	Dialect      Dialect         // the DB's: an Around hook has only the statement
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
	// sends the hook's retry to the primary. Changing WithPrimaryReads,
	// WithShard or WithSession in the context passed to next does not re-route
	// the statement, as routing happens before the Around hooks. A fallback
	// keeps Route.Fallback set for the hook's later calls to next; a hook
	// that counts failovers compares it with its value before next.
	Route Route
	// Result is reset before the Around hooks run, and set by each
	// execution before next returns to them; a hook that retries sees the
	// last attempt's.
	Result Result

	ran Target // where exec sent it; its With loads run there
	pin Target // a With load's: the connection its query ran on, which its reads go to; zero otherwise
	// handed is set once Iter has handed the caller a row: a broken
	// connection is then not failed over, as the read cannot start again.
	handed bool
	// upsertPolicy is set by the scope when it added the policy's conditions to
	// the upsert's update branch: an upsert that then affected fewer rows than it
	// sent met a row the policy hides.
	upsertPolicy bool
	// upsertRows is the number of rows the statement had when the scope
	// conditioned it: CheckUpserted compares with it, not with Rows, which
	// an Around hook may change.
	upsertRows int
	// bidi is set on a statement of a bidirectional one-to-one write, whose
	// Rewrite hooks' changes CheckBidiRewrite checks; nil otherwise.
	bidi *BidiStep
}

// Rewrite is a hook that may change a statement before it is rendered, or
// refuse it by returning an error. Rewrite hooks run in the order given to
// Open. They run for the SQL(ctx) methods too, with the statement's DryRun
// set, so a hook must change nothing but the statement. They do not run for
// raw SQL (OpRawQuery and OpRawExec), which has nothing to rewrite; Around
// hooks see it.
//
// A hook that points st.Entity at another table changes the statement's own
// table only: the tables in an EXISTS that Has or HasWhere adds keep their
// names. The target's own tenant column and Read policy apply inside the
// EXISTS, but a hook that moves tables cannot reach it. With a table per
// tenant, avoid Has and HasWhere, or load the relation with With, which goes
// through the hooks.
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
// its own instead. A hook must not assign to *st after next returns: velox
// records in st where the statement ran, and the query's With loads read from
// there. It may call next again to retry after an error; running
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

// The functions below reach what Statement keeps private, for the code around
// velox's generic API.
func StmtBidi(st *Statement) *BidiStep { return st.bidi }

func SetStmtBidi(st *Statement, b *BidiStep) { st.bidi = b }

func StmtPin(st *Statement) Target { return st.pin }

func SetStmtPin(st *Statement, t Target) { st.pin = t }

func StmtRan(st *Statement) Target { return st.ran }

func SetStmtRan(st *Statement, t Target) { st.ran = t }

func StmtHanded(st *Statement) bool { return st.handed }

func SetStmtHanded(st *Statement) { st.handed = true }

func StmtUpsertPolicy(st *Statement) bool { return st.upsertPolicy }

func SetStmtUpsertPolicy(st *Statement, on bool) { st.upsertPolicy = on }
