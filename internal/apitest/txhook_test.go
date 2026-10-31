package apitest

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

type hookKey struct{}

// TestTxAroundWrapsTheCall: the first hook is the outermost; statements,
// a savepoint's included, see the call's TxInfo, and on fn's ctx what the
// innermost hook passed.
func TestTxAroundWrapsTheCall(t *testing.T) {
	var log []string
	hook := func(name string) velox.Option {
		return velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
			log = append(log, name+" in")
			err := next(context.WithValue(ctx, hookKey{}, name))
			log = append(log, name+" out")
			return err
		})
	}
	var seen []*velox.TxInfo
	var from []any
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, hook("a"), hook("b"),
		velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			seen = append(seen, st.Tx)
			from = append(from, ctx.Value(hookKey{}))
			return next(ctx)
		}))
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if err := touch(ctx, tx); err != nil {
			return err
		}
		return tx.Tx(ctx, touch) // a savepoint: no hook runs again
	}, velox.TxReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a in", "b in", "b out", "a out"}; !reflect.DeepEqual(log, want) {
		t.Errorf("hooks ran %q", log)
	}
	if want := []string{"BEGIN", touchSQL, "SAVEPOINT velox_1", touchSQL, "RELEASE SAVEPOINT velox_1", "COMMIT"}; !reflect.DeepEqual(e.Log, want) {
		t.Errorf("sent %q", e.Log)
	}
	if len(seen) != 2 || seen[0] != seen[1] || seen[0] == nil || !reflect.DeepEqual(from, []any{"b", "b"}) {
		t.Fatalf("statements saw %v with contexts from %v", seen, from)
	}
	if info := seen[0]; info.ID == 0 || info.Attempt != 1 || !info.ReadOnly || info.Outcome != velox.TxCommit || len(info.Retries) != 0 {
		t.Errorf("TxInfo %+v", info)
	}
}

func TestTxAroundSeesEveryAttempt(t *testing.T) {
	serialization := &pgconn.PgError{Code: "40001"}
	e := &fakeconn.Exec{Errs: []error{serialization}}
	calls := 0
	var info *velox.TxInfo
	var attempts []int
	db := openFake(velox.Postgres, e,
		velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
			calls++
			info = tx
			return next(ctx)
		}),
		velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			attempts = append(attempts, st.Tx.Attempt)
			return next(ctx)
		}))
	if err := db.Tx(ctx, touch, velox.TxRetrySerializable(2)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !reflect.DeepEqual(attempts, []int{1, 2}) || info.Attempt != 2 || info.Outcome != velox.TxCommit {
		t.Fatalf("hook calls %d, attempts %v, info %+v", calls, attempts, info)
	}
	if len(info.Retries) != 1 || !errors.Is(info.Retries[0].Err, serialization) || info.Retries[0].At.IsZero() {
		t.Errorf("retries %+v", info.Retries)
	}
}

// TestRetryExhaustedWithTxAround: when every attempt fails, the hook is
// still called once, and sees the last attempt, its rollback and the retries
// before it.
func TestRetryExhaustedWithTxAround(t *testing.T) {
	ser := &pgconn.PgError{Code: "40001"}
	e := &fakeconn.Exec{Errs: []error{ser, ser, ser}}
	calls := 0
	var info *velox.TxInfo
	db := openFake(velox.Postgres, e, velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
		calls++
		info = tx
		return next(ctx)
	}))
	err := db.Tx(ctx, touch, velox.TxRetrySerializable(2))
	if !errors.Is(err, ser) {
		t.Errorf("got %v, want the serialization failure", err)
	}
	if calls != 1 || info == nil {
		t.Fatalf("hook called %d times", calls)
	}
	if info.Attempt != 3 || info.Outcome != velox.TxRollback || len(info.Retries) != 2 {
		t.Errorf("TxInfo %+v, want attempt 3, a rollback and 2 retries", info)
	}
	var want []string
	for range 3 {
		want = append(want, "BEGIN", touchSQL, "ROLLBACK")
	}
	if !reflect.DeepEqual(e.Log, want) {
		t.Errorf("sent %q", e.Log)
	}
}

func TestTxIDsAreUnique(t *testing.T) {
	var ids []uint64
	db := openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
		ids = append(ids, tx.ID)
		return next(ctx)
	}))
	for range 2 {
		if err := db.Tx(ctx, func(context.Context, *velox.DB) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if len(ids) != 2 || ids[0] == 0 || ids[0] == ids[1] {
		t.Errorf("ids %v", ids)
	}
}

// TestTxInfoRoute: a transaction runs on its shard's primary, which
// TxInfo.Route names: Replica -1 with replicas too, and the shard on a
// sharded DB.
func TestTxInfoRoute(t *testing.T) {
	var got velox.Route
	keep := velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
		got = tx.Route
		return next(ctx)
	})
	none := func(context.Context, *velox.DB) error { return nil }
	cases := []struct {
		name string
		db   *velox.DB
		ctx  context.Context
		want velox.Route
	}{
		{"unsharded", openFake(velox.Postgres, &fakeconn.Exec{}, keep), ctx, velox.Route{Replica: -1}},
		{"replicas", openFake(velox.Postgres, &fakeconn.Exec{}, keep,
			velox.WithReplicas(fakeConn(&fakeconn.Exec{}))), ctx, velox.Route{Replica: -1}},
		{"sharded", velox.OpenShards(velox.Postgres, map[string]velox.Shard{
			"a": {Primary: fakeConn(&fakeconn.Exec{})},
			"b": {Primary: fakeConn(&fakeconn.Exec{})},
		}, keep, velox.WithShardFor(func(context.Context, any) (string, error) { return "b", nil })),
			velox.WithShard(ctx, "a"), velox.Route{Shard: "a", Replica: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got = velox.Route{Replica: 7}
			if err := tc.db.Tx(tc.ctx, none); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("TxInfo.Route %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestStatementTx: with any hook, a statement in a transaction has its
// TxInfo, without a TxAround hook too; one outside a transaction has none.
func TestStatementTx(t *testing.T) {
	var seen []*velox.TxInfo
	db := openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		seen = append(seen, st.Tx)
		return nil
	}))
	if err := touch(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(ctx, touch, velox.TxRetrySerializable(1)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != nil || seen[1] == nil || seen[1].ID == 0 || seen[1].Attempt != 1 || seen[1].Outcome != velox.TxCommit {
		t.Errorf("statements saw %v", seen)
	}
}

// TestTxAroundRules: what velox does with a hook that breaks the contract,
// and the outcome a hook reads after next.
func TestTxAroundRules(t *testing.T) {
	boom := errors.New("boom")
	own := errors.New("the hook's own")
	var outcome velox.TxOutcome
	observe := func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
		err := next(ctx)
		outcome = tx.Outcome
		return err
	}
	// dropDeadline passes on a context without the deadline, and records it
	// in deadline; untilDeadline returns once it has passed, with err.
	var deadline time.Time
	dropDeadline := func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
		deadline, _ = ctx.Deadline()
		return observe(context.WithoutCancel(ctx), tx, next)
	}
	untilDeadline := func(err error) func(context.Context, *velox.DB) error {
		return func(context.Context, *velox.DB) error {
			for time.Now().Before(deadline) {
				time.Sleep(time.Until(deadline))
			}
			return err
		}
	}
	last := func(e *fakeconn.Exec) string {
		if len(e.Log) == 0 {
			return ""
		}
		return e.Log[len(e.Log)-1]
	}
	var second error
	runs := 0
	for _, tc := range []struct {
		name      string
		hook      velox.TxAround
		fn        func(context.Context, *velox.DB) error // nil: touch
		opts      []velox.TxOption
		beginErr  error
		commitErr error
		check     func(err error, e *fakeconn.Exec) bool
		outcome   velox.TxOutcome // what observe saw; 0: not checked
	}{
		{
			name:  "own error",
			hook:  func(context.Context, *velox.TxInfo, func(context.Context) error) error { return own },
			check: func(err error, e *fakeconn.Exec) bool { return err == own && len(e.Log) == 0 },
		},
		{
			name: "no next",
			hook: func(context.Context, *velox.TxInfo, func(context.Context) error) error { return nil },
			check: func(err error, e *fakeconn.Exec) bool {
				return err != nil && err.Error() == "velox: transaction: a TxAround hook returned without calling next" && len(e.Log) == 0
			},
		},
		{
			name: "discarded",
			hook: func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
				_ = next(ctx)
				return nil
			},
			fn: func(context.Context, *velox.DB) error { return boom },
			check: func(err error, _ *fakeconn.Exec) bool {
				return err != nil && err.Error() == "velox: transaction: a TxAround hook discarded the error from next"
			},
		},
		{
			name: "next twice",
			hook: func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
				err := next(ctx)
				second = next(ctx)
				return err
			},
			fn: func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) },
			check: func(err error, _ *fakeconn.Exec) bool {
				return err == nil && second != nil && strings.Contains(second.Error(), "called next twice") && runs == 1
			},
		},
		{
			name:    "rollback",
			hook:    observe,
			fn:      func(context.Context, *velox.DB) error { return boom },
			check:   func(err error, _ *fakeconn.Exec) bool { return err == boom },
			outcome: velox.TxRollback,
		},
		{
			name:      "commit fails",
			hook:      observe,
			commitErr: boom,
			check:     func(err error, _ *fakeconn.Exec) bool { return errors.Is(err, boom) },
			outcome:   velox.TxError,
		},
		{
			name:    "deadline",
			hook:    observe,
			fn:      func(context.Context, *velox.DB) error { time.Sleep(20 * time.Millisecond); return nil },
			opts:    []velox.TxOption{velox.TxTimeout(time.Millisecond)},
			check:   func(err error, _ *fakeconn.Exec) bool { return errors.Is(err, velox.ErrTimeout) },
			outcome: velox.TxError,
		},
		{
			name:     "begin fails",
			hook:     observe,
			beginErr: boom,
			check: func(err error, e *fakeconn.Exec) bool {
				return errors.Is(err, boom) && reflect.DeepEqual(e.Log, []string{"BEGIN"})
			},
			outcome: velox.TxError,
		},
		{
			// A hook that drops the deadline from the context still gets
			// no commit past it, and the error says velox's deadline passed
			// as soon as it has, not once its timer has fired.
			name: "deadline dropped",
			hook: dropDeadline,
			fn:   untilDeadline(nil),
			opts: []velox.TxOption{velox.TxTimeout(time.Millisecond)},
			check: func(err error, e *fakeconn.Exec) bool {
				return errors.Is(err, velox.ErrTimeout) && last(e) == "ROLLBACK"
			},
			outcome: velox.TxError,
		},
		{
			name: "deadline dropped, fn fails",
			hook: dropDeadline,
			fn:   untilDeadline(boom),
			opts: []velox.TxOption{velox.TxTimeout(time.Millisecond)},
			check: func(err error, e *fakeconn.Exec) bool {
				return errors.Is(err, velox.ErrTimeout) && errors.Is(err, boom) && last(e) == "ROLLBACK"
			},
			outcome: velox.TxError,
		},
		{
			// A hook that recovers a panic from next: velox rolled back,
			// so Tx must not succeed.
			name: "recovered panic",
			hook: func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) (err error) {
				defer func() {
					outcome = tx.Outcome
					_ = recover()
				}()
				return next(ctx)
			},
			fn: func(context.Context, *velox.DB) error { panic("oops") },
			check: func(err error, e *fakeconn.Exec) bool {
				return err != nil && err.Error() == "velox: transaction: a TxAround hook recovered a panic from next" &&
					reflect.DeepEqual(e.Log, []string{"BEGIN", "ROLLBACK"})
			},
			outcome: velox.TxRollback,
		},
		{
			name:    "commit",
			hook:    observe,
			check:   func(err error, _ *fakeconn.Exec) bool { return err == nil },
			outcome: velox.TxCommit,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome, second, runs = 0, nil, 0
			e := &fakeconn.Exec{BeginErr: tc.beginErr, CommitErr: tc.commitErr}
			db := openFake(velox.Postgres, e, velox.WithTxAround(tc.hook))
			fn := tc.fn
			if fn == nil {
				fn = touch
			}
			err := db.Tx(ctx, fn, tc.opts...)
			if !tc.check(err, e) {
				t.Errorf("error %v, second next %v, fn ran %d times, sent %q", err, second, runs, e.Log)
			}
			if tc.outcome != 0 && outcome != tc.outcome {
				t.Errorf("outcome %v, want %v", outcome, tc.outcome)
			}
		})
	}

	// Review Focus 3: a panic in fn rolls back and goes on unchanged, through
	// the hook, whose deferred code runs.
	t.Run("panic", func(t *testing.T) {
		e := &fakeconn.Exec{}
		deferred := false
		var info *velox.TxInfo
		db := openFake(velox.Postgres, e, velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
			info = tx
			defer func() { deferred = true }()
			return next(ctx)
		}))
		defer func() {
			if p := recover(); p != "oops" || !deferred || info.Outcome != velox.TxRollback || last(e) != "ROLLBACK" {
				t.Errorf("recovered %v, hook's defer ran %v, outcome %v, sent %q", p, deferred, info.Outcome, e.Log)
			}
		}()
		_ = db.Tx(ctx, func(context.Context, *velox.DB) error { panic("oops") })
		t.Error("Tx did not re-raise the panic")
	})

	// runtime.Goexit in fn, as t.FailNow does, rolls back and goes on
	// through the hook, which reads the outcome in its deferred code.
	t.Run("Goexit", func(t *testing.T) {
		e := &fakeconn.Exec{}
		var got velox.TxOutcome
		db := openFake(velox.Postgres, e, velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
			defer func() { got = tx.Outcome }()
			return next(ctx)
		}))
		returned := false
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = db.Tx(ctx, func(context.Context, *velox.DB) error { runtime.Goexit(); return nil })
			returned = true
		}()
		<-done
		if returned || got != velox.TxRollback || !reflect.DeepEqual(e.Log, []string{"BEGIN", "ROLLBACK"}) {
			t.Errorf("Tx returned %v, outcome %v, sent %q", returned, got, e.Log)
		}
	})

	t.Run("outcome names", func(t *testing.T) {
		for o, want := range map[velox.TxOutcome]string{
			velox.TxCommit: "commit", velox.TxRollback: "rollback", velox.TxError: "error", 0: "unknown",
		} {
			if got := o.String(); got != want {
				t.Errorf("%d: %q, want %q", o, got, want)
			}
		}
	})

	t.Run("nil hook", func(t *testing.T) {
		defer func() {
			if p := recover(); p != "velox: WithTxAround(nil)" {
				t.Errorf("recovered %v", p)
			}
		}()
		velox.WithTxAround(nil)
		t.Error("WithTxAround(nil) did not panic")
	})
}
