package velox

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

// FakeExec is an executor that returns Rows for every query and records what
// it was sent. Queries may run on it concurrently; read its fields only when
// none is running.
type FakeExec struct {
	mu    sync.Mutex
	Rows  [][]any
	SQL   string
	Args  []any
	Calls int
	// Closed counts the rows values returned by query that have been closed.
	Closed int
	// Scanned counts the rows scanned, across queries.
	Scanned int
	// Err, when set, is returned by every query.
	Err error

	Affected  int64 // what exec reports
	LastID    int64
	HasLastID bool
	Log       []string // every statement, and BEGIN, COMMIT, ROLLBACK
	Errs      []error  // returned by the next calls, one each, before Err
	CommitErr error
	Isolation sql.IsolationLevel // the last transaction's options
	ReadOnly  bool
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

func (e *FakeExec) query(_ context.Context, sql string, args []any) (rows, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SQL, e.Args = sql, args
	e.Calls++
	e.Log = append(e.Log, sql)
	if err := e.next(); err != nil {
		return nil, err
	}
	return &fakeRows{data: e.Rows, e: e}, nil
}

func (e *FakeExec) exec(ctx context.Context, sql string, args []any) (result, error) {
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
	return nil
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
		return fmt.Errorf("fake: cannot scan into %T", dst)
	}
	return nil
}
