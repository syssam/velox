package engine

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/syssam/velox/internal/conn"
)

type lastArg struct{ arg any }

func (e *lastArg) Query(_ context.Context, _ string, args []any) (conn.Rows, error) {
	e.arg = args[0]
	return nil, nil
}

func (e *lastArg) Exec(_ context.Context, _ string, args []any) (conn.Result, error) {
	e.arg = args[0]
	return conn.Result{}, nil
}

func (e *lastArg) Begin(context.Context, sql.TxOptions) (conn.Executor, conn.Tx, error) {
	return e, nil, nil
}

// TestEveryConnectionOfADialectIsWrapped: a primary, a replica and an unsharded
// DB's executor write a time as their dialect needs: text on SQLite, unchanged on
// the others.
func TestEveryConnectionOfADialectIsWrapped(t *testing.T) {
	at := time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)
	for _, d := range []Dialect{SQLite, Postgres, MySQL} {
		primary, replica, plain := &lastArg{}, &lastArg{}, &lastArg{}
		sh := newShard(d, primary, []Conn{{ex: replica}}, "test:")
		db := newDB(d, plain, nil)
		for name, ex := range map[string]struct {
			run executor
			rec *lastArg
		}{
			"primary": {sh.Primary, primary},
			"replica": {sh.Replicas[0].ex, replica},
			"plain":   {db.Ex, plain},
		} {
			if _, err := ex.run.Exec(context.Background(), "x", []any{at}); err != nil {
				t.Fatal(err)
			}
			_, isText := ex.rec.arg.(string)
			if isText != (d == SQLite) {
				t.Errorf("%v %s: the arg is %T", d, name, ex.rec.arg)
			}
		}
	}
}
