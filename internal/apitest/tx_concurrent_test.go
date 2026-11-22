package apitest

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/conn"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
)

// gateExec is a fake executor whose Exec of gated text calls gate first,
// outside the fake's lock, so that a test can hold one statement mid-send.
type gateExec struct {
	f    *fakeconn.Exec
	gate func(sql string)
}

func (g gateExec) Query(ctx context.Context, q string, args []any) (conn.Rows, error) {
	return g.f.Query(ctx, q, args)
}

func (g gateExec) Exec(ctx context.Context, q string, args []any) (conn.Result, error) {
	g.gate(q)
	return g.f.Exec(ctx, q, args)
}

func (g gateExec) Begin(ctx context.Context, o sql.TxOptions) (conn.Executor, conn.Tx, error) {
	ex, tx, err := g.f.Begin(ctx, o)
	if err != nil {
		return nil, nil, err
	}
	return gateExec{ex.(*fakeconn.Exec), g.gate}, tx, nil
}

// TestTxConcurrentUseIsRefused: a statement on a Tx while another goroutine's
// statement on it is being sent fails, naming the misuse, and the Tx rolls back
// even when fn swallows that error. Unrefused, both ran and the Tx committed,
// racing on the transaction's state. Statements from several goroutines one
// after another pass.
func TestTxConcurrentUseIsRefused(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	e := &fakeconn.Exec{Affected: 1}
	g := gateExec{e, func(q string) {
		if strings.HasPrefix(q, "UPDATE hold") {
			close(entered)
			<-release
		}
	}}
	db := velox.OpenConn(velox.Postgres, engine.DriverConn(velox.Postgres, g))
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		var wg sync.WaitGroup
		var held error
		wg.Go(func() { _, held = tx.Exec(ctx, "test", "UPDATE hold") })
		<-entered
		_, err := tx.Exec(ctx, "test", "UPDATE other")
		close(release)
		wg.Wait()
		if held != nil {
			t.Errorf("the held statement: %v", held)
		}
		if err == nil || !strings.Contains(err.Error(), "not safe for concurrent use") {
			t.Errorf("the concurrent statement: %v", err)
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "not safe for concurrent use") {
		t.Errorf("the Tx: %v", err)
	}
	for _, q := range e.Log {
		if q == "UPDATE other" || q == "COMMIT" {
			t.Errorf("sent %q: %q", q, e.Log)
		}
	}

	e = &fakeconn.Exec{Affected: 1}
	err = openFake(velox.Postgres, e).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		for range 3 {
			var wg sync.WaitGroup
			var err error
			wg.Go(func() { _, err = tx.Exec(ctx, "test", "UPDATE t SET x = 1") })
			wg.Wait()
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Errorf("one goroutine at a time: %v", err)
	}
}
