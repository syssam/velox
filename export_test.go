package velox

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

// FakeExec is an executor that returns Rows for every query and records what
// it was sent. Queries may run on it concurrently; read its fields only when
// none is running.
type FakeExec struct {
	mu      sync.Mutex
	Rows    [][]any
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
	// Block makes query and exec wait until their context ends, then return
	// its error.
	Block bool
	// Deadline is the deadline of the last query's or exec's context; zero
	// when it had none.
	Deadline time.Time

	Affected  int64 // what exec reports
	LastID    int64
	HasLastID bool
	Log       []string // every statement, and BEGIN, COMMIT, ROLLBACK
	Errs      []error  // returned by the next calls, one each, before Err
	CommitErr error
	BeginErr  error // what BEGIN returns
	// RollbackErr is what ROLLBACK returns, as pgx does once it has closed a
	// connection whose statement a deadline cut off.
	RollbackErr error
	Isolation   sql.IsolationLevel // the last transaction's options
	ReadOnly    bool
	// RollbackCtxErr and SavepointCtxErr are the first non-nil ctx.Err() that
	// a ROLLBACK, and a ROLLBACK TO or RELEASE SAVEPOINT, ran with.
	RollbackCtxErr  error
	SavepointCtxErr error
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

func (e *FakeExec) query(ctx context.Context, sql string, args []any) (rows, error) {
	if err := e.wait(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SQL, e.Args = sql, args
	e.Calls++
	e.Log = append(e.Log, sql)
	if err := e.next(); err != nil {
		return nil, err
	}
	data := e.Rows
	if len(e.Results) > 0 {
		data, e.Results = e.Results[0], e.Results[1:]
	}
	return &fakeRows{data: data, e: e}, nil
}

func (e *FakeExec) exec(ctx context.Context, sql string, args []any) (result, error) {
	if err := e.wait(ctx); err != nil {
		return result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if (strings.HasPrefix(sql, "ROLLBACK TO") || strings.HasPrefix(sql, "RELEASE")) && e.SavepointCtxErr == nil {
		e.SavepointCtxErr = ctx.Err()
	}
	e.SQL, e.Args = sql, args
	e.Calls++
	e.Log = append(e.Log, sql)
	if err := e.next(); err != nil {
		return result{}, err
	}
	return result{affected: e.Affected, lastID: e.LastID, hasLastID: e.HasLastID}, nil
}

func (e *FakeExec) begin(_ context.Context, o txOptions) (executor, txControl, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Log = append(e.Log, "BEGIN")
	if e.BeginErr != nil {
		return nil, nil, e.BeginErr
	}
	e.Isolation, e.ReadOnly = o.isolation, o.readOnly
	return e, fakeTx{e}, nil
}

type fakeTx struct{ e *FakeExec }

func (t fakeTx) commit(context.Context) error {
	t.e.mu.Lock()
	defer t.e.mu.Unlock()
	t.e.Log = append(t.e.Log, "COMMIT")
	return t.e.CommitErr
}

func (t fakeTx) rollback(ctx context.Context) error {
	t.e.mu.Lock()
	defer t.e.mu.Unlock()
	if t.e.RollbackCtxErr == nil {
		t.e.RollbackCtxErr = ctx.Err()
	}
	t.e.Log = append(t.e.Log, "ROLLBACK")
	return t.e.RollbackErr
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
	return db.ex.query(context.Background(), sql, args)
}

type fakeRows struct {
	data [][]any
	i    int
	e    *FakeExec
}

func (r *fakeRows) Next() bool { r.i++; return r.i <= len(r.data) }
func (r *fakeRows) Err() error { return nil }
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
	switch d := dst.(type) {
	case *int64:
		*d = src.(int64)
	case *string:
		*d = src.(string)
	case *float64:
		*d = src.(float64)
	case *bool:
		*d = src.(bool)
	case *[]byte:
		*d = src.([]byte)
	case *time.Time:
		*d = src.(time.Time)
	case **time.Time:
		if src == nil {
			*d = nil
		} else {
			v := src.(time.Time)
			*d = &v
		}
	case **string:
		if src == nil {
			*d = nil
		} else {
			v := src.(string)
			*d = &v
		}
	default:
		return assignReflect(dst, src)
	}
	return nil
}

// assignReflect scans src into any pointer dst: a named type, or a pointer
// field (nil src sets it to nil). Like the typed cases it panics on a source
// of another kind.
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
		panic(fmt.Sprintf("fake: cannot scan nil into %T", dst))
	}
	sv := reflect.ValueOf(src)
	target := dv.Type()
	if dv.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if sv.Kind() != target.Kind() {
		panic(fmt.Sprintf("fake: cannot scan %T into %T", src, dst))
	}
	v := reflect.New(target).Elem()
	v.Set(sv.Convert(target))
	if dv.Kind() == reflect.Pointer {
		dv.Set(v.Addr())
	} else {
		dv.Set(v)
	}
	return nil
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

// PolicyBypass returns the context's SkipPolicy reason.
func PolicyBypass(ctx context.Context) string { return policyBypass(ctx) }

// InfoOf returns entity E's metadata.
func InfoOf[E any, PE Entity[E]]() *EntityInfo { return PE(nil).VeloxMeta().info }

// Prepare runs db's stages before execution on st, as run does: guard,
// entity hooks, Rewrite hooks, scope, row cap and rendering.
func Prepare(db *DB, ctx context.Context, st *Statement) error { return db.prepare(ctx, st) }

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
	return p.n.v.(link).target
}

// LoadDesc builds entity E's metadata from d, as MetaOnce.Load does on the
// entity's first use, then resolves its relations, as CheckEntity does.
func LoadDesc[E any](d Desc[E]) (*EntityInfo, error) {
	var o MetaOnce[E]
	m := o.Load(func() Desc[E] { return d })
	if m.err != nil {
		return nil, m.err
	}
	for _, r := range m.info.rels {
		if _, err := r.resolve(m.info); err != nil {
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
