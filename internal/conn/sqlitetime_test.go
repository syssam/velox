package conn_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/syssam/velox/internal/conn"
)

// argsExec records the arguments of its last statement.
type argsExec struct {
	args     []any
	beginErr error
}

func (e *argsExec) Query(_ context.Context, _ string, args []any) (conn.Rows, error) {
	e.args = args
	return nil, nil
}

func (e *argsExec) Exec(_ context.Context, _ string, args []any) (conn.Result, error) {
	e.args = args
	return conn.Result{}, nil
}

func (e *argsExec) Begin(context.Context, sql.TxOptions) (conn.Executor, conn.Tx, error) {
	return e, nil, e.beginErr
}

type goodValuer struct{ t time.Time }

func (v goodValuer) Value() (driver.Value, error) { return v.t, nil }

type textValuer struct{}

func (textValuer) Value() (driver.Value, error) { return "text", nil }

type badValuer struct{}

func (badValuer) Value() (driver.Value, error) { return nil, errors.New("bad") }

type ptrValuer struct{ t time.Time }

func (v *ptrValuer) Value() (driver.Value, error) { return v.t, nil }

func TestSQLiteTimesWritesOneForm(t *testing.T) {
	at := time.Date(2026, 10, 31, 8, 0, 0, 5, time.FixedZone("HK", 8*3600))
	const want = "2026-10-31 00:00:00.000000005+00:00"
	var nilTime *time.Time
	var nilValuer *ptrValuer
	in := []any{at, &at, goodValuer{at}, &ptrValuer{at}, nilTime, nilValuer, textValuer{}, badValuer{}, 7}
	wantArgs := []any{want, want, want, want, nilTime, nilValuer, textValuer{}, badValuer{}, 7}
	caller := append([]any(nil), in...)

	rec := &argsExec{}
	ex := conn.SQLiteTimes(rec)
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "x", in); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.args, wantArgs) {
		t.Errorf("Exec's args = %v, want %v", rec.args, wantArgs)
	}
	if !reflect.DeepEqual(in, caller) {
		t.Errorf("the caller's slice changed: %v", in)
	}
	rec.args = nil
	if _, err := ex.Query(ctx, "x", in); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.args, wantArgs) {
		t.Errorf("Query's args = %v, want %v", rec.args, wantArgs)
	}

	inTx, _, err := ex.Begin(ctx, sql.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inTx.Exec(ctx, "x", []any{at}); err != nil {
		t.Fatal(err)
	}
	if rec.args[0] != want {
		t.Errorf("in a transaction, the arg = %v", rec.args[0])
	}

	boom := errors.New("boom")
	if _, _, err := conn.SQLiteTimes(&argsExec{beginErr: boom}).Begin(ctx, sql.TxOptions{}); !errors.Is(err, boom) {
		t.Errorf("Begin's error = %v", err)
	}
}

// TestSQLiteTimesFormSortsByTheInstant: the form has one width, so text order is
// time order across fractions of a second.
func TestSQLiteTimesFormSortsByTheInstant(t *testing.T) {
	rec := &argsExec{}
	ex := conn.SQLiteTimes(rec)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := ex.Exec(context.Background(), "x", []any{base, base.Add(time.Nanosecond), base.Add(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	a, b, c := rec.args[0].(string), rec.args[1].(string), rec.args[2].(string)
	if a >= b || b >= c {
		t.Errorf("not in order: %s %s %s", a, b, c)
	}
}
