package velox

import (
	"reflect"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/veloxtype"
)

// Aggregate is the function an aggregate read computes over its one column.
type Aggregate = veloxtype.Aggregate

// The aggregate functions.
const (
	AggregateSum = veloxtype.AggregateSum // SUM
	AggregateAvg = veloxtype.AggregateAvg // AVG
	AggregateMin = veloxtype.AggregateMin // MIN
	AggregateMax = veloxtype.AggregateMax // MAX
)

// ColumnDescriptor describes one column of a Descriptor. For generated code.
type ColumnDescriptor = veloxtype.ColumnDescriptor

// Dialect is a SQL dialect.
type Dialect = veloxtype.Dialect

// The dialects velox supports.
const (
	Postgres = veloxtype.Postgres
	MySQL    = veloxtype.MySQL
	SQLite   = veloxtype.SQLite
)

// EntityInfo is what velox knows about an entity, built once on its first use.
// It is the non-generic view that hooks see in a Statement, and the runtime
// view of the generated Descriptor it is built from. For generated code
// and extensions; outside the 1.0 compatibility promise.
//
// One EntityInfo is shared by every query on the entity, in every goroutine,
// so it must be treated as read-only, slices included. A hook that needs
// different metadata for a statement copies it and points Statement.Entity
// at the copy; it never changes the shared one.
//
// Its fields:
//
//	Name string // the Go type, e.g. "billing.Invoice"
//	Table string
//	Schema string // the schema holding Table; empty for the default
//	Columns []ColumnInfo
//	PrimaryKey []int // indexes into Columns
//	Indexes []IndexInfo
//
// Its methods:
//
//	Column (in *EntityInfo) Column(name string) (int, bool)
//	VersionColumn (in *EntityInfo) VersionColumn() (int, bool) // the Version column's index, and whether there is one
type EntityInfo = engine.EntityInfo

// ColumnInfo describes one column. It is the runtime view of the generated
// ColumnDescriptor it is built from. For generated code and extensions;
// outside the 1.0 compatibility promise.
//
// Its fields:
//
//	Field string // the Go field name
//	Name string // the column name
//	SchemaName string // the schema field's name: ColumnDescriptor.SchemaName, or Name when that is ""
//	Type any // the field's Go type, a reflect.Type: read it with GoType
//	Nullable bool // the column holds NULL: the field is a pointer, or its ColumnDescriptor says Nullable
//	Default any
//	HasDefault bool
//	DefaultFunc func() any // nil when none
//	// UpdateDefault is the function whose result every update writes to the
//	// column; an insert does not (Default and DefaultFunc fill an insert's);
//	// nil when none.
//	UpdateDefault func() any
//	Immutable bool // an update may not assign the column
//	Version bool // the optimistic-locking version: every update adds 1
//	Sensitive bool // the column holds a secret (velox logs no values)
//	// RequiredEdge is ColumnDescriptor.RequiredEdge: the Required edge whose
//	// NULL-able key the column holds, which a write may not leave nil; "" when
//	// none.
//	RequiredEdge string
//	// Codec says the column's value is encoded: its ColumnDescriptor gave Encode
//	// and Decode, as velox-gen does for every json field. The package
//	// documentation's JSON fields section says how such a column binds and reads.
//	Codec bool
type ColumnInfo = engine.ColumnInfo

// GoType returns the Go type of column c's field. ColumnInfo.Type holds it as
// any, and this is a function rather than a method, so that no velox type
// reaches reflect.Type: every package that reaches it compiles about 134 KiB of
// wrappers for reflect's iterator types.
func GoType(c ColumnInfo) reflect.Type { return engine.GoType(c) }

// IndexInfo describes one index.
type IndexInfo = veloxtype.IndexInfo

// OrderTerm is an ordering by one column of a Statement's entity.
type OrderTerm = veloxtype.OrderTerm

// ColumnEQ returns the condition column = v, for hooks that add conditions;
// column is an index into the statement's EntityInfo.Columns.
func ColumnEQ(column int, v any) Predicate { return engine.ColumnEQ(column, v) }

// EdgeDescriptor is one relation field of a generated Desc: its name and the columns
// that key it. For generated code. EdgeTo, EdgeBidi and EdgeThrough make it. It carries
// no target entity: the use site supplies that (see EdgeTo).
type EdgeDescriptor = engine.EdgeDescriptor

// EntityLink is one entity of an EntityTable: its Name in the schema, its
// Table, its Go Type's package path and name, and Get, which returns its
// metadata. For generated code. LinkEntity makes one.
type EntityLink = engine.EntityLink

// EntityTable is the entities of one generated package, in which a condition
// by name, HasEdge, HasEdgeWith and FieldInSelect, finds its target when its
// statement runs: by the entity's name in the schema or, failing that, by its
// table. For generated code. Its zero value is ready; Load builds it once,
// from build, and returns it:
//
//	var veloxEntities velox.EntityTable
//
//	func veloxEntityTable() *velox.EntityTable {
//		return veloxEntities.Load(func() []velox.EntityLink {
//			return []velox.EntityLink{velox.LinkEntity[Doc]("Doc", "docs")}
//		})
//	}
type EntityTable = engine.EntityTable

// Op is the kind of a Statement.
type Op = veloxtype.Op

// The kinds of statement.
const (
	OpSelect = veloxtype.OpSelect
	OpInsert = veloxtype.OpInsert
	OpUpdate = veloxtype.OpUpdate
	OpDelete = veloxtype.OpDelete
	// OpRawQuery is raw SQL run by DB.Raw, which velox treats as a read: its
	// SQL and Args are the caller's.
	OpRawQuery = veloxtype.OpRawQuery
	// OpRawExec is raw SQL run by DB.Exec, which velox treats as a write: its
	// SQL and Args are the caller's.
	OpRawExec = veloxtype.OpRawExec
)

// Lock is the lock a locking read takes on the rows it reads.
type Lock = veloxtype.Lock

// The locks.
const (
	LockForUpdate = veloxtype.LockForUpdate // FOR UPDATE
	LockForShare  = veloxtype.LockForShare  // FOR SHARE
)

// LockWait is what a locking read does about rows another transaction has
// locked.
type LockWait = veloxtype.LockWait

// The ways of not waiting.
const (
	LockSkipLocked = veloxtype.LockSkipLocked // SKIP LOCKED: leave them out
	LockNoWait     = veloxtype.LockNoWait     // NOWAIT: fail at once
)

// AssignOp is how an UPDATE assigns a column.
type AssignOp = veloxtype.AssignOp

// The assignment operators.
const (
	AssignSet  = veloxtype.AssignSet  // column = value
	AssignAdd  = veloxtype.AssignAdd  // column = column + value
	AssignSub  = veloxtype.AssignSub  // column = column - value
	AssignExpr = veloxtype.AssignExpr // column = expression (SetExpr)
	// AssignAppend appends the elements of a JSON array to the column's
	// array (a JSON handle's Append).
	AssignAppend = veloxtype.AssignAppend
)

// Assignment is one column an UPDATE sets.
type Assignment = veloxtype.Assignment

// ConflictClause is an INSERT's upsert clause. For generated code and
// extensions; outside the 1.0 compatibility promise.
//
// Its fields:
//
//	Columns []int // the unique key whose conflict the insert resolves
//	Update []int // columns overwritten with the inserted row's values
//	DoNothing bool // keep the existing row; also what an empty Update means
//	// Where is the condition of the update branch, on the existing row: a
//	// conflicting row it rejects is neither updated nor replaced. PostgreSQL
//	// and SQLite render it; velox refuses it on MySQL.
//	Where []Predicate
//	// Ignore, with an empty Update and no DoNothing, keeps the existing row
//	// through an update branch that assigns the first conflict column its
//	// own value (ent's Ignore): the row is returned and its key read back as
//	// after an update. velox sets it when every Update column's setter was
//	// skipped. A Rewrite or Around hook that rebuilds the clause must copy
//	// Ignore.
//	Ignore bool
//	// Set holds the update branch's other assignments (UpdateSet): Set,
//	// Add, Sub and expressions, in which Excluded names the inserted row.
//	Set []Assignment
//	// TargetWhere is the conflict target's condition, a partial index's
//	// predicate (OnConflictWhere); refused on MySQL.
//	TargetWhere []Predicate
//	// Constraint names the constraint of ON CONFLICT ON CONSTRAINT
//	// (OnConstraint), PostgreSQL only.
//	Constraint string
type ConflictClause = engine.ConflictClause

// EachClause makes a SELECT read, or count, the rows of each owner apart: the
// rows whose owner column holds one of Keys. velox sets it for a With load
// with Rel.LimitEach, for LoadEach and for CountEach; a Rewrite hook may not
// change it. With Count, the statement reads each owner's key and its count,
// one row per owner that has rows. For generated code and extensions;
// outside the 1.0 compatibility promise.
//
// Its fields:
//
//	Keys []any // the owners' keys, distinct
//	Column int // without Join: the column of Entity that holds an owner's key
//	// Join is the join entity of a many-to-many relation; nil otherwise. The
//	// statement joins it on Join.Columns[JoinTarget] = Entity.Columns[Target]
//	// and partitions by Join.Columns[JoinOwner].
//	Join *EntityInfo
//	JoinOwner, JoinTarget int
//	Target int
//	// Limit is the most rows per owner, in the statement's OrderBy; 0 is no
//	// limit, and is always 0 on a Count.
//	Limit int
type EachClause = engine.EachClause

// Projection is what a SELECT reads in place of its entity's columns, set by
// Query.Project: terms, one result column each, grouped by terms and filtered
// by conditions of the same entity. The rows are read by position, with
// Query.Rows, and the Read policy, scope and interceptors filter them as
// they filter every read. A Rewrite hook may not change it. For generated
// code and extensions; outside the 1.0 compatibility promise.
//
// Its fields:
//
//	Terms []Term // the result columns, in order
//	GroupBy []Term // GROUP BY, when not empty
//	Having []Predicate // HAVING: all must hold
type Projection = engine.Projection

// JoinClause is a to-one relation joined into a SELECT, by Query.Join or LeftJoin: the
// target's columns follow the statement's own in every row, and the relation's
// conditions and orderings (Rel.Is and Rel.By) are on them. A Rewrite hook may not
// change it. For generated code and extensions; outside the 1.0
// compatibility promise.
//
// Its fields:
//
//	Ref RelRef // the relation, as the query named it
//	Left bool // LEFT JOIN: the row stays, with no target, when there is none
//	// Link is Ref resolved, set before anything else reads the statement.
//	Link Link
//	// On holds the target's Read conditions, added by the scope to the join's ON
//	// clause: a target the policy hides is no match, so an inner join drops the
//	// row and a left join keeps it without a target.
//	On []Predicate
type JoinClause = engine.JoinClause

// Statement is one operation on its way to the database. Every query becomes
// one, and the pipeline's hooks see and may change it: rewrite hooks before it
// is rendered, around hooks while it runs. For generated code and
// extensions; outside the 1.0 compatibility promise.
//
// Raw SQL (OpRawQuery and OpRawExec) is not rendered: Rewrite hooks do not see
// it, and its SQL, Args and Fingerprint are set when it is made. Its Entity is
// the entity Raw scans into, or for Exec an EntityInfo named "Exec" with no
// table or columns. Its PolicyBypass holds the caller's reason, as given,
// untrimmed (so does a WithoutPolicy reason).
//
// The EntityInfo that Entity points to is shared by every query on the
// entity, in every goroutine: treat it as read-only. A rewrite hook may point
// Entity at an EntityInfo of its own (a copy with another Table, say), but
// must never change the shared one. Pointing it at another table does not
// change the tables inside an EXISTS (see Rewrite).
//
// Its method RawReasons returns the reasons of the ExprRaw conditions it
// holds, which read past the policies inside their text only; they do not
// set PolicyBypass.
//
// Its fields:
//
//	Op Op
//	Entity *EntityInfo // shared and read-only: replace it, never edit it
//	Count bool // SELECT COUNT(*): Count clears Columns, OrderBy, Limit and Offset; a Rewrite hook may not change it, or set Columns, and one that sets the others gets them rendered
//	Exists bool // SELECT 1 … LIMIT 1: Exists clears Columns, OrderBy and Offset and sets Limit to 1; a Rewrite hook may not change it, or set Columns
//	Distinct bool // SELECT DISTINCT: Query.Distinct, of a projection or a read of one column (Values) only; a Rewrite hook may not change it
//	Aggregate Aggregate // SELECT SUM(c) …: Columns holds c alone; like Count, it clears OrderBy, Limit and Offset; a Rewrite hook may not change it, or Columns
//	GroupBy int // SELECT with Count or Aggregate: the key column's index plus one, 0 for none: one row per key, the key first; velox orders by the key, and a Rewrite hook may not change it
//	Joins []JoinClause // SELECT: to-one relations joined in, each by Join or LeftJoin; a Rewrite hook may not change them
//	Each *EachClause // SELECT: a relation read or counted per owner; a Rewrite hook may not change it
//	Project *Projection // SELECT: terms read in place of the entity's columns, grouped and filtered; read by Query.Rows; a Rewrite hook may not change it
//	Lock Lock // SELECT: ForUpdate or ForShare; 0 for a read that locks nothing; a Rewrite hook may not change it
//	LockWait LockWait // SELECT: SkipLocked or NoWait; a Rewrite hook may not change it
//	Columns []int // SELECT: the selected columns, nil selects all; INSERT: the columns written
//	Where []Predicate // all must hold
//	OrderBy []OrderTerm
//	Limit int // 0: no limit; after the Rewrite hooks, velox lowers it to the row cap + 1
//	Offset int
//	Unbounded bool // the query said AllowUnbounded
//	Writable bool // SELECT: the query said Writable; the scope adds the Write policy, and a Rewrite hook may not change it
//	MaxRows int // SELECT and OpRawQuery: the most rows it may return; 0: the DB's WithMaxRows, or, with Each, its bound by construction (Rel.MaxRows sets it)
//	Set []Assignment // UPDATE: the assignments
//	Rows [][]any // INSERT: the rows, one value per Columns entry
//	Conflict *ConflictClause // INSERT: the upsert clause, if any
//	Returning []int // INSERT: columns read back (PostgreSQL and SQLite)
//	AllRows bool // UPDATE and DELETE: the statement said All
//	PolicyBypass string // the WithoutPolicy reason, which lifts every policy, a scope's included; raw SQL's reason
//	DryRun bool // built for SQL(ctx), not sent
//	InTx bool // set by velox: the statement runs in a transaction, inside db.Tx or one velox opens for it (a split InsertAll, a policy-conditioned upsert, an EdgeBidi write); a Rewrite hook may not change it
//	Tx *TxInfo // the transaction the statement runs in, when the DB has hooks; nil otherwise
//	Dialect Dialect // the DB's: an Around hook has only the statement
//	// Tags are the context's WithTags tags, set before the hooks run. A
//	// Rewrite hook gets its own copy; it may change them, and the SQL
//	// comment is rendered from what it leaves.
//	Tags []Tag
//	// Caller is the statement's call site, with WithCallers; zero
//	// otherwise. A Rewrite hook may change it, as Tags.
//	Caller Caller
//	SQL string // set by rendering, after the rewrite hooks: the text sent, WithSQLComment's comment included
//	Fingerprint string // set by rendering: the shape's ID, the hash of SQL without the comment
//	Args []any // set by rendering
//	// Route is where the statement runs: set by the route stage, after
//	// rendering. Rewrite hooks run before it and see it unset (zero, not
//	// Replica -1); Around hooks, and everything after them, see it set. An
//	// Around hook reads it, with one exception: setting Route.Fallback to true
//	// sends the hook's retry to the primary. Changing WithPrimaryReads,
//	// WithShard or WithSession in the context passed to next does not re-route
//	// the statement, as routing happens before the Around hooks. A fallback
//	// keeps Route.Fallback set for the hook's later calls to next; a hook
//	// that counts failovers compares it with its value before next.
//	Route Route
//	// Result is reset before the Around hooks run, and set by each
//	// execution before next returns to them; a hook that retries sees the
//	// last attempt's.
//	Result Result
type Statement = engine.Statement

// Result is what executing a statement did, which an Around hook reads.
type Result = veloxtype.Result

// Pred is a condition on entity E, made by a column handle or by And, Or and
// Not. It is the typed form of Predicate, as Order is of OrderTerm.
type Pred[E any] = engine.Pred[E]
