package conn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// sqliteTimeLayout writes every time as the same width of UTC. SQLite has no time
// type and compares the text it holds, so that text sorts by the instant only when
// it has one zone and one precision. SQLite's drivers read this form back.
const sqliteTimeLayout = "2006-01-02 15:04:05.000000000-07:00"

// SQLiteTimes returns ex with each time argument written in sqliteTimeLayout, so
// that a statement stores and compares times by the instant whatever their zone.
func SQLiteTimes(ex Executor) Executor { return sqliteTimes{ex} }

type sqliteTimes struct{ Executor }

// Query runs a statement that returns rows.
func (e sqliteTimes) Query(ctx context.Context, s string, args []any) (Rows, error) {
	return e.Executor.Query(ctx, s, sqliteTimeArgs(args))
}

// Exec runs a statement that returns no rows.
func (e sqliteTimes) Exec(ctx context.Context, s string, args []any) (Result, error) {
	return e.Executor.Exec(ctx, s, sqliteTimeArgs(args))
}

// Begin starts a transaction, whose statements the returned Executor runs.
//
// Drivers ignore a transaction's options on SQLite, so velox keeps them. Every
// SQLite transaction is serializable, so that level is the default and another
// is refused. A read-only one runs under PRAGMA query_only, a setting of the
// connection, and not of the transaction: it is off again before the
// connection serves another statement.
func (e sqliteTimes) Begin(ctx context.Context, o sql.TxOptions) (Executor, Tx, error) {
	if o.Isolation != sql.LevelDefault && o.Isolation != sql.LevelSerializable {
		return nil, nil, fmt.Errorf("SQLite's transactions are serializable; it has no isolation level %s", o.Isolation)
	}
	o.Isolation = sql.LevelDefault
	var (
		ex  Executor
		tx  Tx
		err error
	)
	if s, ok := e.Executor.(SQL); ok && o.ReadOnly {
		ex, tx, err = s.BeginOnConn(ctx, sql.TxOptions{}, "PRAGMA query_only = ON", "PRAGMA query_only = OFF")
	} else {
		ex, tx, err = e.Executor.Begin(ctx, o)
	}
	if err != nil {
		return nil, nil, err
	}
	return sqliteTimes{ex}, tx, nil
}

// sqliteTimeArgs is args with its times as text. It copies args only when one is a
// time, as the caller's slice is a statement's, which hooks read.
func sqliteTimeArgs(args []any) []any {
	copied := false
	for i, a := range args {
		t, ok := timeOf(a)
		if !ok {
			continue
		}
		if !copied {
			args, copied = slices.Clone(args), true
		}
		args[i] = t.UTC().Format(sqliteTimeLayout)
	}
	return args
}

// timeOf is the time a is, points to or, as a driver.Valuer, gives. A nil pointer
// and a Valuer that fails are not times: database/sql handles them.
func timeOf(a any) (time.Time, bool) {
	switch x := a.(type) {
	case time.Time:
		return x, true
	case *time.Time:
		if x != nil {
			return *x, true
		}
	case driver.Valuer:
		if v := reflect.ValueOf(a); v.Kind() == reflect.Pointer && v.IsNil() {
			return time.Time{}, false
		}
		if v, err := x.Value(); err == nil {
			t, ok := v.(time.Time)
			return t, ok
		}
	}
	return time.Time{}, false
}
