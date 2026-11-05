// Package fakeconn is an executor for velox's tests: it answers from what the test
// gave it and records what it was sent. No database is involved.
package fakeconn

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/syssam/velox/internal/conn"
)

var _ conn.Executor = (*Exec)(nil)

// Exec is an executor that returns Rows for every query and records what
// it was sent. Queries may run on it concurrently; read its fields only when
// none is running.
type Exec struct {
	mu   sync.Mutex
	Rows [][]any
	// Cols are the column names every query's rows report, which Raw reads.
	Cols    []string
	Results [][][]any // the rows of successive queries; Rows once they run out
	// Answer, when set, gives the rows of a query it reports true for, ahead of
	// Results.
	Answer func(sql string, args []any) ([][]any, bool)
	SQL    string
	Args   []any
	Calls  int
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
	// its error, or BlockErr when that is set.
	Block bool
	// BlockErr, when set, is what a call that Block held returns once its
	// context ends: a driver error that does not say the context ended, as
	// pgx's database/sql driver returns driver.ErrBadConn.
	BlockErr error
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
	// BlockCommit makes COMMIT wait until its context ends, then return
	// CommitErr.
	BlockCommit bool
	BeginErr    error // what BEGIN returns
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

// rollbackWait records the deadline of a ROLLBACK's or ROLLBACK TO's ctx and,
// when BlockRollback is set, waits for ctx to end.
func (e *Exec) rollbackWait(ctx context.Context) error {
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
func (e *Exec) next() error {
	if len(e.Errs) > 0 {
		err := e.Errs[0]
		e.Errs = e.Errs[1:]
		return err
	}
	return e.Err
}

// wait records ctx's deadline and, when Block is set, waits for ctx to end.
func (e *Exec) wait(ctx context.Context) error {
	e.mu.Lock()
	e.Deadline, _ = ctx.Deadline()
	block, berr := e.Block, e.BlockErr
	e.mu.Unlock()
	if !block {
		return nil
	}
	<-ctx.Done()
	if berr != nil {
		return berr
	}
	return ctx.Err()
}

// Query answers from Results, then Rows, and records the statement.
func (e *Exec) Query(ctx context.Context, sql string, args []any) (conn.Rows, error) {
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
	answered := false
	if e.Answer != nil {
		var rows [][]any
		if rows, answered = e.Answer(sql, args); answered {
			data = rows
		}
	}
	if !answered && len(e.Results) > 0 {
		data, e.Results = e.Results[0], e.Results[1:]
	}
	rerr := e.RowsErr
	if len(e.RowsErrs) > 0 {
		rerr, e.RowsErrs = e.RowsErrs[0], e.RowsErrs[1:]
	}
	return &fakeRows{data: data, cols: e.Cols, e: e, err: rerr}, nil
}

// Exec reports Affected, or AffectedBy's answer, and records the statement.
func (e *Exec) Exec(ctx context.Context, sql string, args []any) (conn.Result, error) {
	if err := e.wait(ctx); err != nil {
		return conn.Result{}, err
	}
	if strings.HasPrefix(sql, "ROLLBACK TO") {
		if err := e.rollbackWait(ctx); err != nil {
			return conn.Result{}, err
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
		return conn.Result{}, err
	}
	affected := e.Affected
	if e.AffectedBy != nil {
		affected = e.AffectedBy(sql)
	}
	return conn.Result{Affected: affected, LastID: e.LastID, HasLastID: e.HasLastID}, nil
}

// Begin records BEGIN and returns e itself as the transaction's executor.
func (e *Exec) Begin(_ context.Context, o sql.TxOptions) (conn.Executor, conn.Tx, error) {
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

type fakeTx struct{ e *Exec }

func (t fakeTx) Commit(ctx context.Context) error {
	t.e.mu.Lock()
	block := t.e.BlockCommit
	t.e.mu.Unlock()
	if block {
		<-ctx.Done()
	}
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

type fakeRows struct {
	data [][]any
	cols []string
	i    int
	e    *Exec
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
