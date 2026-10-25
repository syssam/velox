package velox_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// seeded opens a replicaSet whose primary and replicas each hold an invoice.
func seeded(hooked bool, opts ...velox.Option) *replicaSet {
	s := openReplicas(hooked, opts...)
	s.p.Rows = [][]any{invoiceRow(1)}
	s.r0.Rows = [][]any{invoiceRow(1)}
	s.r1.Rows = [][]any{invoiceRow(1)}
	return s
}

// writeOne makes a write that returns no rows.
func writeOne(t *testing.T, ctx context.Context, db *velox.DB) {
	t.Helper()
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func readOne(t *testing.T, ctx context.Context, db *velox.DB) {
	t.Helper()
	if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPinAfterWrite(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := seeded(hooked, velox.WithPinWindow(50*time.Millisecond))
		sctx := velox.WithSession(ctx)
		// The insert reads its key back; then two reads on the primary.
		s.p.Results = [][][]any{{{int64(42)}}, {invoiceRow(1)}, {invoiceRow(1)}}
		readOne(t, sctx, s.db) // before any write: a replica
		inv := testmodel.Invoice{OrgID: 1, Number: "N"}
		if err := s.db.Insert(&inv).Exec(sctx); err != nil {
			t.Fatal(err)
		}
		readOne(t, sctx, s.db)
		readOne(t, sctx, s.db)
		// replica 0, then the write, then two pinned reads
		s.check(t, hooked, [3]int{3, 1, 0}, onR0, onPrimary, onPrimary, onPrimary)

		time.Sleep(60 * time.Millisecond)
		readOne(t, sctx, s.db)
		s.check(t, hooked, [3]int{3, 1, 1}, onR0, onPrimary, onPrimary, onPrimary, onR1)
	})
}

func TestDefaultPinWindow(t *testing.T) {
	s := seeded(false)
	sctx := velox.WithSession(ctx)
	before := time.Now()
	writeOne(t, sctx, s.db)
	until := velox.PinnedUntil(sctx)
	if d := until.Sub(before); d < 2*time.Second || d > 3*time.Second {
		t.Errorf("a write pins for %v, want the default 2s", d)
	}
}

func TestFailedWriteDoesNotPin(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := seeded(hooked)
		sctx := velox.WithSession(ctx)
		s.p.Err = errors.New("boom")
		inv := testmodel.Invoice{OrgID: 1, Number: "N"}
		if err := s.db.Insert(&inv).Exec(sctx); err == nil {
			t.Fatal("the insert should fail")
		}
		s.p.Err = nil
		readOne(t, sctx, s.db)
		s.check(t, hooked, [3]int{1, 1, 0}, onPrimary, onR0)
		if u := velox.PinnedUntil(sctx); !u.IsZero() {
			t.Errorf("PinnedUntil %v after a failed write, want zero", u)
		}
	})
}

func TestNoSessionNoPin(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := seeded(hooked)
		writeOne(t, ctx, s.db)
		readOne(t, ctx, s.db)
		s.check(t, hooked, [3]int{1, 1, 0}, onPrimary, onR0)
	})
}

// A session pins only its own context's reads: another session, and a context
// without one, still read replicas.
func TestSessionsAreIndependent(t *testing.T) {
	s := seeded(true)
	a, b := velox.WithSession(ctx), velox.WithSession(ctx)
	writeOne(t, a, s.db)
	readOne(t, a, s.db)
	readOne(t, b, s.db)
	readOne(t, ctx, s.db)
	s.check(t, true, [3]int{2, 1, 1}, onPrimary, onPrimary, onR0, onR1)
}

// Primary and a session share one context key: each keeps the other's mark.
func TestSessionAndPrimaryMarksCompose(t *testing.T) {
	s := seeded(true)
	sctx := velox.WithPrimaryReads(velox.WithSession(ctx))
	writeOne(t, sctx, s.db)
	if u := velox.PinnedUntil(sctx); u.IsZero() {
		t.Error("the session under Primary is lost")
	}
	pctx := velox.WithSession(velox.WithPrimaryReads(ctx))
	readOne(t, pctx, s.db)
	s.check(t, true, [3]int{2, 0, 0}, onPrimary, onPrimary)
}

func TestTxCommitPins(t *testing.T) {
	update := func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx)
		return err
	}
	t.Run("a committed write pins", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := seeded(hooked)
			sctx := velox.WithSession(ctx)
			if err := s.db.Tx(sctx, update); err != nil {
				t.Fatal(err)
			}
			readOne(t, sctx, s.db)
			s.check(t, hooked, [3]int{2, 0, 0}, onPrimary, onPrimary)
		})
	})
	t.Run("a rolled back write does not", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := seeded(hooked)
			sctx := velox.WithSession(ctx)
			boom := errors.New("boom")
			err := s.db.Tx(sctx, func(ctx context.Context, tx *velox.DB) error {
				if err := update(ctx, tx); err != nil {
					return err
				}
				return boom
			})
			if !errors.Is(err, boom) {
				t.Fatalf("Tx returned %v", err)
			}
			readOne(t, sctx, s.db)
			s.check(t, hooked, [3]int{1, 1, 0}, onPrimary, onR0)
		})
	})
	t.Run("a failed commit does not", func(t *testing.T) {
		s := seeded(false)
		sctx := velox.WithSession(ctx)
		s.p.CommitErr = errors.New("commit failed")
		if err := s.db.Tx(sctx, update); err == nil {
			t.Fatal("the commit should fail")
		}
		readOne(t, sctx, s.db)
		if s.r0.Calls != 1 {
			t.Errorf("the read after a failed commit went to %v, want replica 0", s.calls())
		}
	})
	t.Run("a transaction of reads does not", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := seeded(hooked)
			sctx := velox.WithSession(ctx)
			err := s.db.Tx(sctx, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			readOne(t, sctx, s.db)
			s.check(t, hooked, [3]int{1, 1, 0}, onPrimary, onR0)
		})
	})
	t.Run("a write in a savepoint that rolls back still pins", func(t *testing.T) {
		s := seeded(false)
		sctx := velox.WithSession(ctx)
		err := s.db.Tx(sctx, func(ctx context.Context, tx *velox.DB) error {
			_ = tx.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				if err := update(ctx, tx); err != nil {
					return err
				}
				return errors.New("undo")
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		readOne(t, sctx, s.db)
		if s.r0.Calls != 0 || s.r1.Calls != 0 {
			t.Errorf("the read went to %v, want the primary", s.calls())
		}
	})
	t.Run("a write in a savepoint that commits pins", func(t *testing.T) {
		s := seeded(false)
		sctx := velox.WithSession(ctx)
		err := s.db.Tx(sctx, func(ctx context.Context, tx *velox.DB) error { return tx.Tx(ctx, update) })
		if err != nil {
			t.Fatal(err)
		}
		readOne(t, sctx, s.db)
		if s.r0.Calls != 0 || s.r1.Calls != 0 {
			t.Errorf("the read went to %v, want the primary", s.calls())
		}
	})
	t.Run("a retried transaction forgets the failed attempt's write", func(t *testing.T) {
		s := seeded(false)
		sctx := velox.WithSession(ctx)
		attempt := 0
		err := s.db.Tx(sctx, func(ctx context.Context, tx *velox.DB) error {
			attempt++
			if attempt == 1 {
				if err := update(ctx, tx); err != nil {
					return err
				}
				return &pgconn.PgError{Code: "40001"}
			}
			return nil
		}, velox.TxRetrySerializable(1))
		if err != nil || attempt != 2 {
			t.Fatalf("attempt %d, err %v; want the second attempt to commit", attempt, err)
		}
		readOne(t, sctx, s.db)
		if s.r0.Calls != 1 {
			t.Errorf("the read went to %v, want replica 0", s.calls())
		}
	})
}

// A read-only transaction runs on the primary, like every transaction.
func TestReadOnlyTxRunsOnThePrimary(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := seeded(hooked)
		err := s.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
			return err
		}, velox.TxReadOnly())
		if err != nil {
			t.Fatal(err)
		}
		s.check(t, hooked, [3]int{1, 0, 0}, onPrimary)
		if !s.p.ReadOnly || s.p.Log[0] != "BEGIN" {
			t.Errorf("the read-only transaction did not begin on the primary: readOnly %v, log %q", s.p.ReadOnly, s.p.Log)
		}
	})
}

func TestPinUntil(t *testing.T) {
	t.Run("pins reads", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := seeded(hooked)
			sctx := velox.WithSession(ctx)
			until := time.Now().Add(time.Hour)
			velox.PinUntil(sctx, until)
			if got := velox.PinnedUntil(sctx); !got.Equal(until) {
				t.Errorf("PinnedUntil %v, want %v", got, until)
			}
			readOne(t, sctx, s.db)
			s.check(t, hooked, [3]int{1, 0, 0}, onPrimary)
		})
	})
	t.Run("a time in the past pins nothing", func(t *testing.T) {
		s := seeded(true)
		sctx := velox.WithSession(ctx)
		velox.PinUntil(sctx, time.Now().Add(-time.Hour))
		readOne(t, sctx, s.db)
		s.check(t, true, [3]int{0, 1, 0}, onR0)
	})
	t.Run("never lowers a later pin", func(t *testing.T) {
		sctx := velox.WithSession(ctx)
		late := time.Now().Add(2 * time.Hour)
		velox.PinUntil(sctx, late)
		velox.PinUntil(sctx, time.Now().Add(time.Hour))
		if got := velox.PinnedUntil(sctx); !got.Equal(late) {
			t.Errorf("PinnedUntil %v after an earlier PinUntil, want %v", got, late)
		}
		// nor does a write
		s := seeded(false)
		writeOne(t, sctx, s.db)
		if got := velox.PinnedUntil(sctx); !got.Equal(late) {
			t.Errorf("PinnedUntil %v after a write, want %v", got, late)
		}
	})
	t.Run("raises an earlier one", func(t *testing.T) {
		s := seeded(false)
		sctx := velox.WithSession(ctx)
		writeOne(t, sctx, s.db)
		first := velox.PinnedUntil(sctx)
		later := first.Add(time.Hour)
		velox.PinUntil(sctx, later)
		if got := velox.PinnedUntil(sctx); !got.Equal(later) {
			t.Errorf("PinnedUntil %v, want %v", got, later)
		}
	})
	t.Run("no session, no effect", func(t *testing.T) {
		s := seeded(true)
		velox.PinUntil(ctx, time.Now().Add(time.Hour))
		if got := velox.PinnedUntil(ctx); !got.IsZero() {
			t.Errorf("PinnedUntil %v on a context without a session, want zero", got)
		}
		readOne(t, ctx, s.db)
		s.check(t, true, [3]int{0, 1, 0}, onR0)
	})
	t.Run("a time past int64's nanoseconds pins, not wraps", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			for _, far := range []time.Time{time.Date(2262, 4, 12, 0, 0, 0, 0, time.UTC), time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)} {
				s := seeded(hooked)
				sctx := velox.WithSession(ctx)
				velox.PinUntil(sctx, far)
				if got := velox.PinnedUntil(sctx); got.Before(time.Date(2262, 1, 1, 0, 0, 0, 0, time.UTC)) {
					t.Errorf("PinUntil(%v): PinnedUntil %v", far, got)
				}
				readOne(t, sctx, s.db)
				s.check(t, hooked, [3]int{1, 0, 0}, onPrimary)
			}
		})
	})
	t.Run("a time before int64's nanoseconds pins nothing", func(t *testing.T) {
		for _, early := range []time.Time{{}, time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)} {
			sctx := velox.WithSession(ctx)
			velox.PinUntil(sctx, early)
			if got := velox.PinnedUntil(sctx); !got.IsZero() {
				t.Errorf("PinUntil(%v): PinnedUntil %v, want zero", early, got)
			}
		}
	})
	t.Run("a session with no write has a zero pin", func(t *testing.T) {
		if got := velox.PinnedUntil(velox.WithSession(ctx)); !got.IsZero() {
			t.Errorf("PinnedUntil %v, want zero", got)
		}
	})
}

// TestSessionConcurrency: goroutines share one session, writing and reading.
// Before any write, reads go to the replicas; a read issued after some
// goroutine's write has returned goes to the primary.
func TestSessionConcurrency(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		type holderKey struct{}
		opts := []velox.Option{velox.WithPinWindow(time.Hour)}
		if hooked {
			opts = append(opts, velox.WithAround(
				func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
					if h, ok := ctx.Value(holderKey{}).(*velox.Route); ok {
						*h = st.Route
					}
					return next(ctx)
				}))
		}
		s := seeded(false, opts...)
		sctx := velox.WithSession(ctx)
		const goroutines = 8
		errs := make(chan string, 1000)
		read := func() (velox.Route, bool) {
			var got velox.Route
			rctx := context.WithValue(sctx, holderKey{}, &got)
			if _, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(rctx); err != nil {
				errs <- err.Error()
				return got, false
			}
			return got, true
		}

		// No write yet: every read goes to a replica.
		var wg sync.WaitGroup
		for range goroutines {
			wg.Go(func() {
				for range 10 {
					if got, ok := read(); ok && hooked && got.Replica == -1 {
						errs <- "a read before any write ran on the primary"
					}
				}
			})
		}
		wg.Wait()
		if got := s.calls(); got != [3]int{0, goroutines * 10 / 2, goroutines * 10 / 2} {
			t.Errorf("before any write, calls (primary, replica 0, replica 1) %v, want the replicas only", got)
		}

		var wrote atomic.Bool
		var before atomic.Int64 // reads begun before any write returned
		replicaCalls := s.r0.Calls + s.r1.Calls
		for g := range goroutines {
			wg.Go(func() {
				for i := range 50 {
					if (g+i)%7 == 0 {
						if _, err := s.db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(sctx); err != nil {
							errs <- err.Error()
							return
						}
						wrote.Store(true)
						continue
					}
					after := wrote.Load() // a write returned before this read began
					if !after {
						before.Add(1)
					}
					got, ok := read()
					if !ok {
						return
					}
					if hooked && after && got.Replica != -1 {
						errs <- "a read after a write ran on replica " + strconv.Itoa(got.Replica)
					}
				}
			})
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Error(e)
		}
		if n := s.r0.Calls + s.r1.Calls - replicaCalls; int64(n) > before.Load() {
			t.Errorf("%d reads went to a replica once writes began, but only %d began before a write returned", n, before.Load())
		}
		if velox.PinnedUntil(sctx).IsZero() {
			t.Error("the session was never pinned")
		}
	})
}

// TestTxAroundPins: the commit pins the session of the context given to Tx,
// whatever context a TxAround hook passes on, and only once a commit has
// succeeded.
func TestTxAroundPins(t *testing.T) {
	update := func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx)
		return err
	}
	t.Run("a hook that drops the session", func(t *testing.T) {
		s := seeded(false, velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
			return next(context.WithoutCancel(velox.WithSession(ctx))) // a new session: the one given to Tx is not in it
		}))
		sctx := velox.WithSession(ctx)
		if err := s.db.Tx(sctx, update); err != nil {
			t.Fatal(err)
		}
		if velox.PinnedUntil(sctx).IsZero() {
			t.Error("the session of the context given to Tx was not pinned")
		}
		readOne(t, sctx, s.db)
		if s.r0.Calls != 0 || s.r1.Calls != 0 {
			t.Errorf("the read went to %v, want the primary", s.calls())
		}
	})
	retry := velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
		err := next(ctx)
		if err != nil {
			err = next(ctx) // velox refuses a second call
		}
		return err
	})
	t.Run("a hook that retries a rolled back write", func(t *testing.T) {
		s := seeded(false, retry)
		sctx := velox.WithSession(ctx)
		calls := 0
		err := s.db.Tx(sctx, func(ctx context.Context, tx *velox.DB) error {
			calls++
			if err := update(ctx, tx); err != nil {
				return err
			}
			return errors.New("undo")
		})
		if err == nil || !strings.Contains(err.Error(), "called next twice") || calls != 1 {
			t.Fatalf("fn ran %d times, Tx returned %v; want one run and velox's refusal", calls, err)
		}
		if got := velox.PinnedUntil(sctx); !got.IsZero() {
			t.Errorf("a rolled back write pinned the session until %v", got)
		}
	})
	t.Run("a hook that retries after a failed commit", func(t *testing.T) {
		s := seeded(false, retry)
		s.p.CommitErr = errors.New("commit failed")
		sctx := velox.WithSession(ctx)
		if err := s.db.Tx(sctx, update); err == nil {
			t.Fatal("Tx should fail")
		}
		if got := velox.PinnedUntil(sctx); !got.IsZero() {
			t.Errorf("a failed commit pinned the session until %v", got)
		}
	})
	t.Run("a hook that calls next again after a commit", func(t *testing.T) {
		s := seeded(false, velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
			if err := next(ctx); err != nil {
				return err
			}
			return next(ctx)
		}))
		sctx := velox.WithSession(ctx)
		calls := 0
		err := s.db.Tx(sctx, func(ctx context.Context, tx *velox.DB) error { calls++; return update(ctx, tx) })
		if err == nil || calls != 1 {
			t.Fatalf("fn ran %d times, Tx returned %v; want one run and velox's refusal", calls, err)
		}
		if velox.PinnedUntil(sctx).IsZero() {
			t.Error("the committed write did not pin the session")
		}
		if n := strings.Count(strings.Join(s.p.Log, "\n"), "COMMIT"); n != 1 {
			t.Errorf("%d commits, want 1", n)
		}
	})
}

func TestPinWindowPanics(t *testing.T) {
	defer func() {
		r := recover()
		if s, ok := r.(string); !ok || !strings.HasPrefix(s, "velox: ") || !strings.Contains(s, "WithPinWindow") {
			t.Errorf("recovered %v, want a velox panic naming WithPinWindow", r)
		}
	}()
	velox.WithPinWindow(-1)
}
