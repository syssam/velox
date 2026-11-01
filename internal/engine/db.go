package engine

import (
	"context"
	"fmt"
	"math"
	"slices"
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
	Replicas  []Conn // WithReplicas
	// pinWindow is WithPinWindow's: how long a write pins its session.
	PinWindow time.Duration
	// backoff is WithReplicaBackoff's: how long a failed replica is skipped.
	Backoff time.Duration
	// shardFor is WithShardFor's; nil: none.
	ShardFor func(ctx context.Context, tenant any) (string, error)
}

// Option configures a DB's pipeline. The With functions make them; the zero
// Option is not valid, and Open, OpenConn and OpenShards panic on it.
type Option struct{ apply func(*config) }

// WithRewrite adds a rewrite hook. Rewrite hooks run in the order given. A
// nil hook panics.
func WithRewrite(r Rewrite) Option {
	if r == nil {
		panic("velox: WithRewrite(nil)")
	}
	return Option{func(c *config) { c.Rewrites = append(c.Rewrites, r) }}
}

// WithMaxLimit refuses, with ErrLimitTooLarge, a query whose Limit is over n.
// n must be at least 2, the limit One uses; a smaller n panics.
func WithMaxLimit(n int) Option {
	if n < 2 {
		panic(fmt.Sprintf("velox: WithMaxLimit(%d): the maximum must be at least 2, the limit One uses", n))
	}
	return Option{func(c *config) { c.MaxLimit = n }}
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
func CheckColumns(st *Statement) error {
	n := len(st.Entity.Columns)
	bad := func(c int) error {
		if c < 0 || c >= n {
			return fmt.Errorf("velox: %s: a Rewrite hook set column %d, the entity has %d columns", st.Entity.Name, c, n)
		}
		return nil
	}
	for _, c := range st.Columns {
		if err := bad(c); err != nil {
			return err
		}
	}
	for _, o := range st.OrderBy {
		if err := bad(o.Column); err != nil {
			return err
		}
	}
	for _, a := range st.Set {
		if err := bad(a.Column); err != nil {
			return err
		}
	}
	for _, c := range st.Returning {
		if err := bad(c); err != nil {
			return err
		}
	}
	if cf := st.Conflict; cf != nil {
		if len(cf.Columns) == 0 {
			return fmt.Errorf("velox: %s: a Rewrite hook left an upsert with no conflict columns", st.Entity.Name)
		}
		for _, c := range append(slices.Clip(cf.Columns), cf.Update...) {
			if err := bad(c); err != nil {
				return err
			}
		}
	}
	for _, r := range st.Rows {
		if len(r) != len(st.Columns) {
			return fmt.Errorf("velox: %s: a Rewrite hook made a row of %d values for %d columns", st.Entity.Name, len(r), len(st.Columns))
		}
	}
	var walk func(nd Node) error
	walk = func(nd Node) error {
		switch nd.Op {
		case OpAnd, OpOr, OpNot:
			for _, k := range nd.Kids {
				if err := walk(k); err != nil {
					return err
				}
			}
			return nil
		case OpExists:
			// A backstop: a hook can put another entity's relation here
			// through a Predicate conversion, and an owner column out of this
			// entity's range would make rendering panic. Only the owner's
			// column is the statement's entity's; the kids are conditions on
			// the target, made by HasWhere. A Rewrite hook may have added one
			// whose relation is not resolved yet.
			l, err := existsLink(nd)
			if err != nil {
				return err
			}
			return bad(l.OwnerCol)
		case OpInvalid:
			return nd.V.(error)
		case OpAllow:
			return nil // it names no column
		case OpThrough:
			// col is the statement's entity's; the join column and the
			// kids' columns are the join entity's.
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
		}
		return bad(int(nd.Col))
	}
	conds := func(ps []Predicate) error {
		for _, p := range ps {
			// walk does not enter an EXISTS's kids, so look for the zero
			// Predicate first.
			if hasZero(p.n) {
				return errZeroPredicate(st.Entity.Name)
			}
			if err := walk(p.n); err != nil {
				return err
			}
		}
		return nil
	}
	if err := conds(st.Where); err != nil {
		return err
	}
	if st.Conflict != nil {
		return conds(st.Conflict.Where)
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
// dialect d allows, with a *paramsError.
func checkParams(d Dialect, st *Statement) error {
	if lim := MaxParams[d]; len(st.Args) > lim {
		return &paramsError{entity: st.Entity.Name, d: d, N: len(st.Args), Max: lim}
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
