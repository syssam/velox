package engine

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sync/atomic"
	"time"
)

type config struct {
	Rewrites  []Rewrite
	Arounds   []Around
	Cache     RenderCache
	MaxLimit  int        // WithMaxLimit; 0: none
	MaxRows   int        // WithMaxRows; 0: none
	Deadlines Deadlines  // WithDeadlines
	Callers   *callers   // WithCallers; nil: off
	Comment   *commenter // WithSQLComment; nil: off
	TxArounds []TxAround
	// TxDefaults are WithTxDefaults's: the options every outermost Tx
	// applies before its own.
	TxDefaults []TxOption
	// SQLiteLocks is WithSQLiteLockingReads': a locking read on SQLite runs,
	// without its lock clause, where it is refused otherwise.
	SQLiteLocks bool
	// MaxParams is WithMaxParams': the most parameters a statement may
	// have; 0: the dialect's limit.
	MaxParams int
	// RollbackTimeout is WithRollbackTimeout's; 0: DefaultRollbackTimeout.
	RollbackTimeout time.Duration
	// connHold is the *connHold of the Iter or Tx that holds a pool's one
	// connection; nil: none (iterconn.go). A Value: an atomic.Pointer would put
	// connHold in the export data of every package that uses a DB
	// (TestGenericTypesCompileSmall).
	connHold atomic.Value
	Replicas []Conn // WithReplicas
	// pinWindow is WithPinWindow's: how long a write pins its session.
	PinWindow time.Duration
	// backoff is WithReplicaBackoff's: how long a failed replica is skipped.
	Backoff time.Duration
	// shardFor is WithShardFor's; nil: none.
	ShardFor func(ctx context.Context) (string, error)
	// Hooks are WithEntityHooks's, by entity; nil: none.
	Hooks map[*EntityInfo][]HookFn
	// AllHooks are WithAllHooks's, for every entity, outermost first.
	AllHooks []HookFn
	// Filters are WithFilter's, for every entity, in order.
	Filters []EntityFilterFn
	// noReturningOld is WithoutReturningOld's.
	noReturningOld bool
	// version is the PostgreSQL server_version_num a recording handle read
	// (serverVersion): 0 unknown, versionLoading while being read.
	version atomic.Int64
	// autoInc is MySQL's @@auto_increment_increment, read for a multi-row
	// insert's keys (autoIncrementStep): 0 unknown.
	autoInc atomic.Int64
	// values are WithValue's, a []keyValue in the order given; an any, so
	// that no type of it is in the export data of every package that uses a
	// DB (TestGenericTypesCompileSmall).
	values any
}

// keyValue is one WithValue.
type keyValue struct{ key, value any }

// Option configures a DB's pipeline. The With functions make them; the zero
// Option is not valid, and Connect, Open, OpenConn and OpenShards panic on it.
type Option struct{ apply func(*config) }

// WithRewrite adds a rewrite hook. Rewrite hooks run in the order given. A
// nil hook panics.
func WithRewrite(r Rewrite) Option {
	if r == nil {
		panic("velox: WithRewrite(nil)")
	}
	return Option{func(c *config) { c.Rewrites = append(c.Rewrites, r) }}
}

// WithValue attaches value to the DB under key, which Value reads. A nil
// key, or one that is not comparable, panics.
func WithValue(key, value any) Option {
	if key == nil {
		panic("velox: WithValue with a nil key")
	}
	if !reflect.TypeOf(key).Comparable() {
		panic(fmt.Sprintf("velox: WithValue with a key of %T, which is not comparable", key))
	}
	return Option{func(c *config) {
		vs, _ := c.values.([]keyValue)
		c.values = append(vs, keyValue{key, value})
	}}
}

// Value returns the value WithValue attached to db under key, the last one
// given; nil when there is none.
func (db *DB) Value(key any) any {
	if db.Cfg == nil {
		return nil // the zero DB
	}
	vs, _ := db.Cfg.values.([]keyValue)
	for _, kv := range slices.Backward(vs) {
		if kv.key == key {
			return kv.value
		}
	}
	return nil
}

// WithMaxLimit refuses, with ErrLimitTooLarge, a query whose Limit is over n.
// n must be at least 2, the limit One uses; a smaller n panics.
func WithMaxLimit(n int) Option {
	if n < 2 {
		panic(fmt.Sprintf("velox: WithMaxLimit(%d): the maximum must be at least 2, the limit One uses", n))
	}
	return Option{func(c *config) { c.MaxLimit = n }}
}

// WithSQLiteLockingReads lets a locking read run on SQLite, rendered
// without its lock clause; the root's documents what that guarantees.
func WithSQLiteLockingReads() Option { return Option{func(c *config) { c.SQLiteLocks = true }} }

// WithMaxParams lowers the most parameters a statement may have below the
// dialect's limit, so that tests split statements at a small size. The root
// does not offer it.
func WithMaxParams(n int) Option { return Option{func(c *config) { c.MaxParams = max(n, 0) }} }

// WithRollbackTimeout replaces DefaultRollbackTimeout, so that tests bound a
// rollback tightly. The root does not offer it.
func WithRollbackTimeout(d time.Duration) Option {
	return Option{func(c *config) { c.RollbackTimeout = max(d, 0) }}
}

// WithMaxRows fails, with ErrTooManyRows, a query that returns more than n
// rows: a relation load and Iter included, Count and an insert's RETURNING
// not. velox sends such a query with LIMIT n+1 (or the query's own Limit,
// when that is lower), so the database stops early: the extra row is the one
// that trips the cap. The cap counts per statement, so a With load that is
// split into chunks to stay under the parameter limit is counted chunk by
// chunk. A query's MaxRows overrides it, for the query and its With loads; a
// load with Rel.LimitEach is bounded by its limit instead, and Rel.MaxRows
// gives a load a cap of its own.
// n <= 0 sets no cap; n must otherwise be at least 2, the rows One reads, and
// n = 1 panics.
func WithMaxRows(n int) Option {
	if n == 1 {
		panic("velox: WithMaxRows(1): the cap must be at least 2, the rows One reads")
	}
	return Option{func(c *config) { c.MaxRows = max(n, 0) }}
}

// WithAround adds an around hook. The first one given is the outermost. A nil
// hook panics.
func WithAround(a Around) Option {
	if a == nil {
		panic("velox: WithAround(nil)")
	}
	return Option{func(c *config) { c.Arounds = append(c.Arounds, a) }}
}

// defaultPinWindow is how long a write pins its session unless WithPinWindow
// says otherwise, and defaultBackoff how long a failed replica is skipped
// unless WithReplicaBackoff does.
const (
	defaultPinWindow = 2 * time.Second
	defaultBackoff   = 5 * time.Second
)

// newConfig returns the configuration opts make.
func newConfig(opts []Option) *config {
	cfg := &config{PinWindow: defaultPinWindow, Backoff: defaultBackoff}
	for _, o := range opts {
		o.apply(cfg)
	}
	return cfg
}

// CheckColumns reports a column index in st, set by a rewrite hook, that
// st's entity does not have; rendering would panic on it. The generic API
// sets only valid ones.
func CheckColumns(st *Statement) error { return checkColumns(st, "a Rewrite hook set") }

// checkColumns is CheckColumns, whose errors say who names a column: "a
// Rewrite hook set".
func checkColumns(st *Statement, who string) error {
	if err := checkClauseColumns(st, who); err != nil {
		return err
	}
	if err := checkConflict(st, who); err != nil {
		return err
	}
	for _, r := range st.Rows {
		if len(r) != len(st.Columns) {
			return fmt.Errorf("velox: %s: a Rewrite hook made a row of %d values for %d columns", st.Entity.Name, len(r), len(st.Columns))
		}
	}
	if err := checkConds(st, who, st.Where); err != nil {
		return err
	}
	if err := checkProject(st, who); err != nil {
		return err
	}
	if err := checkOrderExprs(st, who); err != nil {
		return err
	}
	if err := checkSetExprs(st, who); err != nil {
		return err
	}
	if st.Conflict != nil {
		return checkConds(st, who, st.Conflict.Where)
	}
	return nil
}

// badColumn returns an error when c is not a column index of st's entity.
func badColumn(st *Statement, who string, c int) error {
	if n := len(st.Entity.Columns); c < 0 || c >= n {
		return fmt.Errorf("velox: %s: %s column %d, the entity has %d columns", st.Entity.Name, who, c, n)
	}
	return nil
}

// badColumns returns badColumn's error for the first bad index in cs.
func badColumns(st *Statement, who string, cs []int) error {
	for _, c := range cs {
		if err := badColumn(st, who, c); err != nil {
			return err
		}
	}
	return nil
}

// checkClauseColumns checks the column indexes of st's column list, order,
// assignments and RETURNING list.
func checkClauseColumns(st *Statement, who string) error {
	if err := badColumns(st, who, st.Columns); err != nil {
		return err
	}
	for _, o := range st.OrderBy {
		if o.Rel != 0 {
			continue // a column of the joined target: render checks it
		}
		if err := badColumn(st, who, o.Column); err != nil {
			return err
		}
	}
	for _, a := range st.Set {
		if err := badColumn(st, who, a.Column); err != nil {
			return err
		}
	}
	return badColumns(st, who, st.Returning)
}

// checkConflict checks the conflict and update columns of st's upsert.
func checkConflict(st *Statement, who string) error {
	cf := st.Conflict
	if cf == nil {
		return nil
	}
	if len(cf.Columns) == 0 {
		return fmt.Errorf("velox: %s: a Rewrite hook left an upsert with no conflict columns", st.Entity.Name)
	}
	if err := badColumns(st, who, cf.Columns); err != nil {
		return err
	}
	for _, a := range cf.Set {
		if err := badColumn(st, who, a.Column); err != nil {
			return err
		}
	}
	if err := checkConds(st, who, cf.TargetWhere); err != nil {
		return err
	}
	return badColumns(st, who, cf.Update)
}

// checkConds checks the conditions ps of st: none is the zero Predicate and
// each names only columns its entity has.
func checkConds(st *Statement, who string, ps []Predicate) error {
	for _, p := range ps {
		// checkNode does not enter an EXISTS's kids, so look for the zero
		// Predicate first.
		if hasZero(p.n) {
			return errZeroPredicate(st.Entity.Name)
		}
		if err := checkNode(st, who, p.n); err != nil {
			return err
		}
	}
	return nil
}

// checkNode checks the column indexes of the condition nd and its kids.
func checkNode(st *Statement, who string, nd Node) error {
	switch nd.Op {
	case OpAnd, OpOr, OpNot:
		for _, k := range nd.Kids {
			if err := checkNode(st, who, k); err != nil {
				return err
			}
		}
		return nil
	case OpExists:
		// A backstop: a hook can put another entity's relation here
		// through a Predicate conversion, and an owner column out of this
		// entity's range would make rendering panic. Only the owner's
		// column is the statement's entity's; the kids are conditions on
		// the target, made by HasWith. A Rewrite hook may have added one
		// whose relation is not resolved yet.
		l, err := existsLink(nd)
		if err != nil {
			return err
		}
		return badColumn(st, who, l.OwnerCol)
	case OpInvalid:
		return nd.V.(error)
	case opNamed, opNamedEdge, opNamedSub:
		return nil // resolved against the statement's entity when it runs
	case OpAllow:
		return nil // it names no column
	case OpExpr:
		return checkExprCols(nd.V.(*exprData), func(c int) error { return badColumn(st, who, c) })
	case OpThrough:
		if err := checkThrough(st, nd); err != nil {
			return err
		}
	}
	return badColumn(st, who, int(nd.Col))
}

// checkThrough checks the join entity's columns that the OpThrough nd
// names: its join column and its kids' columns. nd's own column is the
// statement's entity's, which checkNode checks.
func checkThrough(st *Statement, nd Node) error {
	j := nd.V.(*EntityInfo)
	bad := func(c int) error {
		if c < 0 || c >= len(j.Columns) {
			return fmt.Errorf("velox: %s: a condition names column %d of %s, which has %d", st.Entity.Name, c, j.Name, len(j.Columns))
		}
		return nil
	}
	if err := bad(nd.W.(int)); err != nil {
		return err
	}
	for _, k := range nd.Kids {
		if err := bad(int(k.Col)); err != nil {
			return err
		}
	}
	return nil
}

// sameEach reports whether a hook's Each, a, holds what velox's, b, does. A
// key velox set is comparable, so comparing it with any value cannot panic.
func sameEach(a, b *EachClause) bool {
	if a.Column != b.Column || a.Join != b.Join || a.JoinOwner != b.JoinOwner || a.JoinTarget != b.JoinTarget ||
		a.Target != b.Target || a.Limit != b.Limit || len(a.Keys) != len(b.Keys) {
		return false
	}
	for i, k := range b.Keys {
		if a.Keys[i] != k {
			return false
		}
	}
	return true
}

// eachBound is the most rows a per-owner read can return by construction, and
// whether it has such a bound: Limit for each owner of a windowed read, one
// for each key of a count or of a read by the target's one-column primary
// key.
func eachBound(st *Statement) (int, bool) {
	c := st.Each
	switch {
	case c == nil:
		return 0, false
	case st.Count || c.keyed(st.Entity):
		return max(len(c.Keys), 1), true
	case c.Limit > 0:
		if c.Limit > math.MaxInt/max(len(c.Keys), 1) {
			return math.MaxInt, true
		}
		return max(c.Limit*len(c.Keys), 1), true
	}
	return 0, false
}

// countedRows counts the rows read and, when capped, ends them after
// left of them and records that there were more.
type countedRows struct {
	Rows
	N      int64
	Left   int
	Capped bool
	Over   bool
}

func (c *countedRows) Next() bool {
	if !c.Rows.Next() {
		return false
	}
	if c.Capped {
		if c.Left == 0 {
			c.Over = true // the row that trips the cap is not counted
			return false
		}
		c.Left--
	}
	c.N++
	return true
}

// readRows runs read on r and closes r, and returns read's error and the
// error r reports once closed: pgx reports some errors only then. r is
// closed even when read panics, as the loop body of an Iter, which runs
// inside read, may.
func readRows(r Rows, read func(Rows) error) (readErr, rowsErr error) {
	defer func() {
		r.Close()
		rowsErr = r.Err()
	}()
	return read(r), nil
}

// checkParams refuses a rendered statement with more parameters than
// db's dialect allows, with a *paramsError.
func checkParams(db *DB, st *Statement) error {
	if lim := db.maxParams(); len(st.Args) > lim {
		return &paramsError{entity: st.Entity.Name, d: db.D, N: len(st.Args), Max: lim}
	}
	return nil
}

// paramsError is a rendered statement with more parameters than its dialect
// allows; nothing was sent. Its text is checkParams's.
type paramsError struct {
	entity string
	d      Dialect
	N, Max int
}

func (e *paramsError) Error() string {
	return fmt.Sprintf("velox: %s: the statement has %d parameters; %s allows %d", e.entity, e.N, e.d, e.Max)
}
