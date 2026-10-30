package velox

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/syssam/velox2/internal/conn"
)

// A JSON column's destination must stay unwrapped: conn wraps no sql.Scanner.
var _ sql.Scanner = (*codecScanner)(nil)

// FakeExec is an executor that returns Rows for every query and records what
// it was sent. Queries may run on it concurrently; read its fields only when
// none is running.
type FakeExec struct {
	mu   sync.Mutex
	Rows [][]any
	// Cols are the column names every query's rows report, which Raw reads.
	Cols    []string
	Results [][][]any // the rows of successive queries; Rows once they run out
	SQL     string
	Args    []any
	Calls   int
	// Closed counts the rows values returned by query that have been closed.
	Closed int
	// Scanned counts the rows scanned, across queries.
	Scanned int
	// Err, when set, is returned by every query.
	Err error
	// RowsErr, when set, is what the rows of every query report from Err,
	// after their last row. RowsErrs are the rows errors of successive
	// queries, one each, before RowsErr.
	RowsErr  error
	RowsErrs []error
	// Block makes query and exec wait until their context ends, then return
	// its error.
	Block bool
	// Deadline is the deadline of the last query's or exec's context; zero
	// when it had none.
	Deadline time.Time

	Affected int64 // what exec reports
	// AffectedBy, when set, is what exec reports for each statement, by its
	// text; Affected otherwise.
	AffectedBy func(sql string) int64
	LastID     int64
	HasLastID  bool
	Log        []string // every statement, and BEGIN, COMMIT, ROLLBACK
	// ArgLog holds the arguments of each entry of Log: nil for BEGIN,
	// COMMIT and ROLLBACK.
	ArgLog    [][]any
	Errs      []error // returned by the next calls, one each, before Err
	CommitErr error
	BeginErr  error // what BEGIN returns
	// RollbackErr is what ROLLBACK returns, as pgx does once it has closed a
	// connection whose statement a deadline cut off.
	RollbackErr error
	Isolation   sql.IsolationLevel // the last transaction's options
	ReadOnly    bool
	// RollbackCtxErr and SavepointCtxErr are the first non-nil ctx.Err() that
	// a ROLLBACK, and a ROLLBACK TO or RELEASE SAVEPOINT, ran with; for a
	// ROLLBACK that BlockRollback held, the error its context ended with.
	RollbackCtxErr  error
	SavepointCtxErr error
	// BlockRollback makes ROLLBACK and ROLLBACK TO wait until their context
	// ends, then return its error.
	BlockRollback bool
	// RollbackDeadline is the deadline of the last ROLLBACK's or ROLLBACK
	// TO's context; zero when none.
	RollbackDeadline time.Time
}

// SetRollbackTimeout sets rollbackTimeout to d until restore is called.
func SetRollbackTimeout(d time.Duration) (restore func()) {
	old := rollbackTimeout
	rollbackTimeout = d
	return func() { rollbackTimeout = old }
}

// rollbackWait records the deadline of a ROLLBACK's or ROLLBACK TO's ctx and,
// when BlockRollback is set, waits for ctx to end.
func (e *FakeExec) rollbackWait(ctx context.Context) error {
	e.mu.Lock()
	e.RollbackDeadline, _ = ctx.Deadline()
	block := e.BlockRollback
	e.mu.Unlock()
	if !block {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

// next is the error the next call returns: the first of Errs, then Err.
func (e *FakeExec) next() error {
	if len(e.Errs) > 0 {
		err := e.Errs[0]
		e.Errs = e.Errs[1:]
		return err
	}
	return e.Err
}

// wait records ctx's deadline and, when Block is set, waits for ctx to end.
func (e *FakeExec) wait(ctx context.Context) error {
	e.mu.Lock()
	e.Deadline, _ = ctx.Deadline()
	block := e.Block
	e.mu.Unlock()
	if !block {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func (e *FakeExec) Query(ctx context.Context, sql string, args []any) (rows, error) {
	if err := e.wait(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SQL, e.Args = sql, args
	e.Calls++
	e.Log = append(e.Log, sql)
	e.ArgLog = append(e.ArgLog, args)
	if err := e.next(); err != nil {
		return nil, err
	}
	data := e.Rows
	if len(e.Results) > 0 {
		data, e.Results = e.Results[0], e.Results[1:]
	}
	rerr := e.RowsErr
	if len(e.RowsErrs) > 0 {
		rerr, e.RowsErrs = e.RowsErrs[0], e.RowsErrs[1:]
	}
	return &fakeRows{data: data, cols: e.Cols, e: e, err: rerr}, nil
}

func (e *FakeExec) Exec(ctx context.Context, sql string, args []any) (result, error) {
	if err := e.wait(ctx); err != nil {
		return result{}, err
	}
	if strings.HasPrefix(sql, "ROLLBACK TO") {
		if err := e.rollbackWait(ctx); err != nil {
			return result{}, err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if (strings.HasPrefix(sql, "ROLLBACK TO") || strings.HasPrefix(sql, "RELEASE")) && e.SavepointCtxErr == nil {
		e.SavepointCtxErr = ctx.Err()
	}
	e.SQL, e.Args = sql, args
	e.Calls++
	e.Log = append(e.Log, sql)
	e.ArgLog = append(e.ArgLog, args)
	if err := e.next(); err != nil {
		return result{}, err
	}
	affected := e.Affected
	if e.AffectedBy != nil {
		affected = e.AffectedBy(sql)
	}
	return result{Affected: affected, LastID: e.LastID, HasLastID: e.HasLastID}, nil
}

func (e *FakeExec) Begin(_ context.Context, o sql.TxOptions) (executor, txControl, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Log = append(e.Log, "BEGIN")
	e.ArgLog = append(e.ArgLog, nil)
	if e.BeginErr != nil {
		return nil, nil, e.BeginErr
	}
	e.Isolation, e.ReadOnly = o.Isolation, o.ReadOnly
	return e, fakeTx{e}, nil
}

type fakeTx struct{ e *FakeExec }

func (t fakeTx) Commit(context.Context) error {
	t.e.mu.Lock()
	defer t.e.mu.Unlock()
	t.e.Log = append(t.e.Log, "COMMIT")
	t.e.ArgLog = append(t.e.ArgLog, nil)
	return t.e.CommitErr
}

func (t fakeTx) Rollback(ctx context.Context) error {
	werr := t.e.rollbackWait(ctx)
	t.e.mu.Lock()
	defer t.e.mu.Unlock()
	if t.e.RollbackCtxErr == nil {
		t.e.RollbackCtxErr = ctx.Err()
	}
	if werr != nil {
		return werr
	}
	t.e.Log = append(t.e.Log, "ROLLBACK")
	t.e.ArgLog = append(t.e.ArgLog, nil)
	return t.e.RollbackErr
}

// render is renderStmt for the tests that render statements that cannot fail.
func render(d Dialect, st *Statement) (string, []any) {
	text, args, err := renderStmt(d, st)
	if err != nil {
		panic(err)
	}
	return text, args
}

// render is get for the tests that look only at the text.
func (c *renderCache) render(d Dialect, st *Statement) (string, []any) {
	e, args, err := c.get(d, st)
	if err != nil {
		panic(err)
	}
	return e.text, args
}

// FakeConn returns a Conn that runs statements on e.
func FakeConn(e *FakeExec) Conn { return Conn{ex: e} }

// SetReplicaDown puts db's replica i in back-off until until.
func SetReplicaDown(db *DB, i int, until time.Time) {
	db.rt.one.replicas[i].downUntil.Store(until.UnixNano())
}

// ReplicaDownUntil returns the end of db's replica i's back-off; zero when
// it has none.
func ReplicaDownUntil(db *DB, i int) time.Time {
	if u := db.rt.one.replicas[i].downUntil.Load(); u != 0 {
		return time.Unix(0, u)
	}
	return time.Time{}
}

// DryRun prepares and routes st as the SQL(ctx) methods do.
func DryRun(ctx context.Context, db *DB, st *Statement) (string, []any, error) {
	return db.dryRun(ctx, st)
}

// OpenFake returns a DB of dialect d that runs statements on e.
func OpenFake(d Dialect, e *FakeExec, opts ...Option) *DB { return newDB(d, e, opts) }

// RawRows runs sql on db's executor, bypassing the pipeline: the baseline of
// hand-written code.
func RawRows(db *DB, sql string, args []any) (interface {
	Next() bool
	Scan(...any) error
	Close()
}, error) {
	return db.ex.Query(context.Background(), sql, args)
}

type fakeRows struct {
	data [][]any
	cols []string
	i    int
	e    *FakeExec
	err  error // what Err reports
}

func (r *fakeRows) Next() bool { r.i++; return r.i <= len(r.data) }
func (r *fakeRows) Err() error { return r.err }

func (r *fakeRows) Columns() ([]string, error) { return r.cols, nil }
func (r *fakeRows) Close() {
	r.e.mu.Lock()
	r.e.Closed++
	r.e.mu.Unlock()
}

func (r *fakeRows) Scan(dst ...any) error {
	r.e.mu.Lock()
	r.e.Scanned++
	r.e.mu.Unlock()
	row := r.data[r.i-1]
	if len(dst) != len(row) {
		return fmt.Errorf("fake: %d destinations for %d columns", len(dst), len(row))
	}
	for k, d := range dst {
		if err := assign(d, row[k]); err != nil {
			return err
		}
	}
	return nil
}

func assign(dst, src any) error {
	// A typed case takes a src of exactly its kind, as the driver's values are;
	// any other is an error, as database/sql's Scan returns one.
	bad := func() error { return fmt.Errorf("fake: cannot scan %T into %T", src, dst) }
	switch d := dst.(type) {
	case sql.Scanner:
		return d.Scan(src)
	case *int64:
		v, ok := src.(int64)
		if !ok {
			return bad()
		}
		*d = v
	case *string:
		v, ok := src.(string)
		if !ok {
			return bad()
		}
		*d = v
	case *float64:
		v, ok := src.(float64)
		if !ok {
			return bad()
		}
		*d = v
	case *bool:
		v, ok := src.(bool)
		if !ok {
			return bad()
		}
		*d = v
	case *[]byte:
		v, ok := src.([]byte)
		if !ok {
			return bad()
		}
		*d = v
	case *time.Time:
		v, ok := src.(time.Time)
		if !ok {
			return bad()
		}
		*d = v
	case **time.Time:
		if src == nil {
			*d = nil
			return nil
		}
		v, ok := src.(time.Time)
		if !ok {
			return bad()
		}
		*d = &v
	case **string:
		if src == nil {
			*d = nil
			return nil
		}
		v, ok := src.(string)
		if !ok {
			return bad()
		}
		*d = &v
	default:
		return assignReflect(dst, src)
	}
	return nil
}

// assignReflect scans src into any pointer dst: a named type, or a pointer
// field (nil src sets it to nil). Like the typed cases it returns an error for
// a source of another kind, except that an int64 converts into any integer
// kind, as database/sql does, and is an error when it does not fit.
func assignReflect(dst, src any) error {
	dv := reflect.ValueOf(dst)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("fake: cannot scan into %T", dst)
	}
	dv = dv.Elem()
	if src == nil {
		switch dv.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
			dv.SetZero()
			return nil
		}
		return fmt.Errorf("fake: cannot scan %T into %T", src, dst)
	}
	sv := reflect.ValueOf(src)
	target := dv.Type()
	if dv.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	v := reflect.New(target).Elem()
	switch {
	case sv.Kind() == target.Kind():
		v.Set(sv.Convert(target))
	case sv.Kind() == reflect.Int64 && isIntKind(target.Kind()):
		n := sv.Int()
		var fits bool
		switch target.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
			fits = !v.OverflowInt(n)
			if fits {
				v.SetInt(n)
			}
		default:
			fits = n >= 0 && !v.OverflowUint(uint64(n))
			if fits {
				v.SetUint(uint64(n))
			}
		}
		if !fits {
			return fmt.Errorf("fake: %d overflows %T", n, dst)
		}
	default:
		return fmt.Errorf("fake: cannot scan %T into %T", src, dst)
	}
	if dv.Kind() == reflect.Pointer {
		dv.Set(v.Addr())
	} else {
		dv.Set(v)
	}
	return nil
}

// isIntKind reports whether k is a signed or unsigned integer kind.
func isIntKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return false
}

// SetMaxParams lowers a dialect's parameter limit for a test.
func SetMaxParams(d Dialect, n int) (restore func()) {
	old := maxParams[d]
	maxParams[d] = n
	return func() { maxParams[d] = old }
}

// CacheSize returns the number of shapes db's render cache holds.
func CacheSize(db *DB) int { return db.cfg.cache.size() }

// TenantOf returns the context's tenant state, as scope reads it.
func TenantOf(ctx context.Context) (id any, all string) { return tenantOf(ctx) }

// TagsOf returns the context's WithTags tags, as observe reads them.
func TagsOf(ctx context.Context) []Tag { return tagsOf(ctx) }

// PolicyBypass returns the context's WithoutPolicy reason.
func PolicyBypass(ctx context.Context) string { return policyBypass(ctx) }

// InfoOf returns entity E's metadata.
func InfoOf[E any, PE Entity[E]]() *EntityInfo { return PE(nil).VeloxMeta().info }

// Prepare runs db's stages before execution on st, as run does: guard,
// entity hooks, Rewrite hooks, scope, row cap and rendering.
func Prepare(ctx context.Context, db *DB, st *Statement) error { return db.prepare(ctx, st) }

// Kids returns p's sub-conditions: the operands of an And, Or or Not, or the
// conditions an EXISTS puts on its target.
func Kids(p Predicate) []Predicate {
	out := make([]Predicate, len(p.n.kids))
	for i, k := range p.n.kids {
		out[i] = Predicate{k}
	}
	return out
}

// ExistsOn returns the target of an EXISTS; nil for any other condition.
func ExistsOn(p Predicate) *EntityInfo {
	if p.n.op != opExists {
		return nil
	}
	l, ok := p.n.v.(link)
	if !ok {
		return nil
	}
	return l.target
}

// TargetOf is the target of a relation to T as a use site names it: what Rel
// puts in a load or a condition.
func TargetOf[T any]() UseTarget { return targetOf[T]() }

// UseTarget is a relation's target as a use site names it.
type UseTarget = useTarget

// LoadDesc builds entity E's metadata from d, as MetaOnce.Load does on the
// entity's first use, then resolves its relations, as CheckRelation does:
// relation i of an EdgeTo takes the target targets[i], which a use site would
// name. A relation with no target there is left unresolved.
func LoadDesc[E any](d Desc[E], targets ...UseTarget) (*EntityInfo, error) {
	var o MetaOnce[E]
	m := o.Load(func() Desc[E] { return d })
	if m.err != nil {
		return nil, m.err
	}
	for i, r := range m.info.rels {
		var use useTarget
		if i < len(targets) {
			use = targets[i]
		}
		if use.get == nil {
			continue
		}
		if _, err := r.resolve(m.info, use); err != nil {
			return m.info, err
		}
	}
	return m.info, nil
}

// SamePreds reports whether a and b are the same conditions. A relation not
// yet resolved is a relRef, which holds a function: reflect.DeepEqual never
// finds two equal, so relRefs are compared by their relation index.
func SamePreds(a, b []Predicate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameNode(a[i].n, b[i].n) {
			return false
		}
	}
	return true
}

func sameNode(a, b node) bool {
	if a.op != b.op || a.col != b.col || len(a.kids) != len(b.kids) || !reflect.DeepEqual(a.w, b.w) {
		return false
	}
	ra, aok := a.v.(relRef)
	rb, bok := b.v.(relRef)
	switch {
	case aok != bok || aok && ra.i != rb.i:
		return false
	case !aok && !reflect.DeepEqual(a.v, b.v):
		return false
	}
	for i := range a.kids {
		if !sameNode(a.kids[i], b.kids[i]) {
			return false
		}
	}
	return true
}

// OpenColumnScan returns a DB on db whose rows are colRows when wrap is set,
// whatever db's driver.
func OpenColumnScan(d Dialect, db *sql.DB, wrap bool) *DB {
	return newDB(d, conn.NewSQLCols(db, wrap), nil)
}

// PgxStdlib reports whether db's driver is pgx's stdlib driver.
func PgxStdlib(db *sql.DB) bool { return conn.PgxStdlib(db) }

// RowsAreColumnScanning runs SELECT 1 on db, a DB made by Open, and reports
// whether its rows are colRows.
func RowsAreColumnScanning(db *DB) bool {
	r, err := db.ex.Query(context.Background(), "SELECT 1", nil)
	if err != nil {
		panic(err)
	}
	defer r.Close()
	return conn.RowsColumnScanning(r)
}

// ConnColumnScanning reports whether c's executor makes colRows.
func ConnColumnScanning(c Conn) bool { return c.ex.(conn.SQL).ColumnScanning() }
