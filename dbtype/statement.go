package dbtype

import "time"

// Op is the kind of a velox.Statement.
type Op uint8

// The kinds of statement.
const (
	OpSelect Op = iota + 1
	OpInsert
	OpUpdate
	OpDelete
	// OpRawQuery is raw SQL run by velox.DB.Raw, which velox treats as a read:
	// its SQL and Args are the caller's.
	OpRawQuery
	// OpRawExec is raw SQL run by velox.DB.Exec, which velox treats as a write:
	// its SQL and Args are the caller's.
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

// Reads reports whether o reads: OpSelect, or velox.DB.Raw's query (OpRawQuery).
// Every other Op writes, velox.DB.Exec's raw SQL included.
func (o Op) Reads() bool { return o == OpSelect || o == OpRawQuery }

// Aggregate is the function an aggregate read computes over its one column:
// what Sum, Avg, Min and Max set.
type Aggregate uint8

// The aggregate functions.
const (
	AggregateSum Aggregate = iota + 1 // SUM
	AggregateAvg                      // AVG
	AggregateMin                      // MIN
	AggregateMax                      // MAX
)

// Lock is the lock a locking read takes on the rows it reads.
type Lock uint8

// The locks.
const (
	LockForUpdate Lock = iota + 1 // FOR UPDATE
	LockForShare                  // FOR SHARE
)

// LockWait is what a locking read does about rows another transaction has
// locked. The zero value waits for them.
type LockWait uint8

// The ways of not waiting.
const (
	LockSkipLocked LockWait = iota + 1 // SKIP LOCKED: leave them out
	LockNoWait                         // NOWAIT: fail at once
)

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
	Column int      // index into velox.EntityInfo.Columns
	Op     AssignOp // how the column is assigned
	Value  any      // the value assigned, added or subtracted
}

// OrderTerm is an ordering by one column of a velox.Statement's entity. It is
// the untyped form of velox.Order, as velox.Predicate is of velox.Pred.
type OrderTerm struct {
	Column int  // index into velox.EntityInfo.Columns
	Desc   bool // descending; ascending when false
}

// Result is what executing a statement did: a velox.Statement's, which an Around
// hook reads. An entity hook reads the rows its whole write affected from
// velox.Mutation.Affected instead.
type Result struct {
	// Rows counts the rows read, an INSERT … RETURNING's and Count's single
	// row included. Rows are counted only when the DB has an Around hook or
	// the statement a row cap: otherwise nothing can read it, and it is 0.
	Rows int64
	// Affected is the rows a write affected: the driver's count for one sent
	// without RETURNING, the rows returned for one sent with it. It is -1
	// for a read, for a failed statement, and for a write whose driver could
	// not report its count.
	Affected int64
	// LoopBody is the time Iter's caller spent in the loop body, summed over
	// the rows it yielded, the last one included when the loop broke or the
	// body panicked on it. It grows while next runs and is final once next
	// returns or panics. It is measured only when the DB has an Around hook,
	// and is 0 otherwise and for every method but Iter.
	LoopBody time.Duration
}

// Route is where a statement runs. The route stage sets velox.Statement.Route
// after the Rewrite hooks and before the Around hooks run. TxInfo.Route is a
// transaction's.
type Route struct {
	Shard   string // "" on an unsharded DB
	Replica int    // index into the replicas; -1 for the primary
	// Fallback is set when a replica failed and the read was retried on the
	// primary. It stays set for the statement's later executions, an Around
	// hook's retry, which run on the primary.
	Fallback bool
}

// RowScope is what a context scopes statements by: velox.WithTenant's id,
// the reasons of velox.WithAllTenants and velox.WithoutPolicy, and
// velox.WithShard's shard, which holds other rows than another shard. A batcher
// that reads for several callers in one statement compares their scopes, so
// that callers of different scopes never share a read.
//
// RowScopes are compared with ==, which panics when Tenant holds a value
// that cannot be compared. A tenant column's type is comparable (its
// metadata is refused otherwise), but velox.WithTenant takes any id, and an id
// of another type, which a statement refuses, may not be: a caller that
// compares guards against it with reflect.ValueOf(Tenant).Comparable(),
// which checks the value, since a struct or an array of a comparable type
// may hold an interface whose value is a slice or a map.
type RowScope struct {
	Tenant       any    // velox.WithTenant's id; nil when none
	TenantBypass string // velox.WithAllTenants's reason; "" when none
	PolicyBypass string // velox.WithoutPolicy's reason; "" when none
	// Shard is velox.WithShard's name, and HasShard whether velox.WithShard
	// was called: a shard named "" is told from none.
	Shard    string
	HasShard bool
}

// Tag is a string a context carries to every statement run with it, for the
// hooks (velox.Statement.Tags) and the SQL comment. velox.WithTags sets it.
type Tag struct{ Key, Value string }

// Caller is a statement's call site: the first stack frame outside velox and
// the packages velox.WithCallerSkip names. Caller is zero when none of the 64
// frames above velox's call is outside velox and those packages.
type Caller struct {
	File string // relative to the module root when it can be determined, else the base name
	Line int    // the line in File
	Func string // the function's full name, as runtime.Frame.Function gives it
}
