package velox

import (
	"context"
	"fmt"
	"time"
)

// FakeExec is an executor that returns Rows for every query and records what
// it was sent.
type FakeExec struct {
	Rows  [][]any
	SQL   string
	Args  []any
	Calls int
}

func (e *FakeExec) query(_ context.Context, sql string, args []any) (rows, error) {
	e.SQL, e.Args = sql, args
	e.Calls++
	return &fakeRows{data: e.Rows}, nil
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
}

func (r *fakeRows) Next() bool { r.i++; return r.i <= len(r.data) }
func (r *fakeRows) Err() error { return nil }
func (r *fakeRows) Close()     {}

func (r *fakeRows) Scan(dst ...any) error {
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
