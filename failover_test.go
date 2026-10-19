package velox_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// openFailover opens a replicaSet whose primary and replicas each hold an
// invoice. Hooked, it records each statement's Route after next returns, as
// failover leaves it.
func openFailover(hooked bool, extra ...velox.Option) *replicaSet {
	s := &replicaSet{}
	if hooked {
		extra = append(extra, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			err := next(ctx)
			s.mu.Lock()
			s.routes = append(s.routes, st.Route)
			s.mu.Unlock()
			return err
		}))
	}
	o := openReplicas(false, extra...)
	s.p, s.r0, s.r1, s.db = o.p, o.r0, o.r1, o.db
	for _, e := range []*velox.FakeExec{s.p, s.r0, s.r1} {
		e.Rows = [][]any{invoiceRow(1)}
	}
	return s
}

var fellOver = velox.Route{Replica: -1, Fallback: true}

// connectError returns a *pgconn.ConnectError as pgx makes one: its fields
// are unexported, so it is made by connecting to a port nothing listens on.
func connectError(t *testing.T) error {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // the port is now closed: the dial is refused
	host, port, _ := strings.Cut(addr, ":")
	cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = pgconn.Connect(cctx, fmt.Sprintf("postgres://u@%s:%s/db?sslmode=disable&connect_timeout=5", host, port))
	var ce *pgconn.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("connecting to a closed port returned %T %v, want a *pgconn.ConnectError", err, err)
	}
	return fmt.Errorf("acquire: %w", err)
}

// TestFailoverClasses: a read whose replica fails with a connection error is
// retried on the primary, and the replica is skipped until its back-off ends.
func TestFailoverClasses(t *testing.T) {
	classes := map[string]func(t *testing.T) error{
		"driver.ErrBadConn": func(*testing.T) error { return driver.ErrBadConn },
		"a *net.OpError": func(*testing.T) error {
			return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		},
		"a pgconn.ConnectError": connectError,
		"sql.ErrConnDone":       func(*testing.T) error { return fmt.Errorf("query: %w", sql.ErrConnDone) },
		"net.ErrClosed":         func(*testing.T) error { return net.ErrClosed },
		"pgconn.ErrConnClosed":  func(*testing.T) error { return pgconn.ErrConnClosed },
	}
	for name, mk := range classes {
		t.Run(name, func(t *testing.T) {
			err := mk(t)
			bothPaths(t, func(t *testing.T, hooked bool) {
				s := openFailover(hooked)
				s.r0.Errs = []error{err}
				for range 3 {
					readOne(t, s.db, ctx)
				}
				// replica 0 fails and the primary serves the read; then
				// replica 1, and replica 1 again in replica 0's turn.
				s.check(t, hooked, [3]int{1, 1, 2}, fellOver, onR1, onR1)
				if velox.ReplicaDownUntil(s.db, 0).Before(time.Now().Add(4 * time.Second)) {
					t.Errorf("replica 0 is in back-off until %v, want about 5s from now", velox.ReplicaDownUntil(s.db, 0))
				}
			})
		})
	}
}

// TestNoFailoverOnQueryErrors: an error the database reports about the
// statement is returned; the primary is not tried and the replica stays.
func TestNoFailoverOnQueryErrors(t *testing.T) {
	for _, qerr := range []error{
		&pgconn.PgError{Code: "42601", Message: "syntax error"},
		&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'a' for key 'p.p_code_uq'"},
		errors.New("something else"),
	} {
		t.Run(qerr.Error(), func(t *testing.T) {
			bothPaths(t, func(t *testing.T, hooked bool) {
				s := openFailover(hooked)
				s.r0.Errs = []error{qerr}
				_, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
				if !errors.Is(err, qerr) {
					t.Fatalf("got %v, want %v", err, qerr)
				}
				readOne(t, s.db, ctx)
				readOne(t, s.db, ctx)
				s.check(t, hooked, [3]int{0, 2, 1}, onR0, onR1, onR0)
				if d := velox.ReplicaDownUntil(s.db, 0); !d.IsZero() {
					t.Errorf("replica 0 is in back-off until %v", d)
				}
			})
		})
	}
}

// TestCancelledReadIsNotAFailover pins Review Focus 2 (Ruling 5): a read whose
// context ends neither falls over nor puts the replica in back-off.
func TestCancelledReadIsNotAFailover(t *testing.T) {
	for name, c := range map[string]struct {
		opts []velox.Option
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		"cancelled": {ctx: func() (context.Context, context.CancelFunc) {
			cctx, cancel := context.WithCancel(ctx)
			time.AfterFunc(20*time.Millisecond, cancel)
			return cctx, cancel
		}, want: context.Canceled},
		"past the caller's deadline": {ctx: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(ctx, 20*time.Millisecond)
		}, want: context.DeadlineExceeded},
		"past velox's deadline": {
			opts: []velox.Option{velox.WithDeadlines(velox.Deadlines{Statement: 20 * time.Millisecond})},
			ctx:  func() (context.Context, context.CancelFunc) { return ctx, func() {} },
			want: velox.ErrTimeout,
		},
	} {
		t.Run(name, func(t *testing.T) {
			bothPaths(t, func(t *testing.T, hooked bool) {
				s := openFailover(hooked, c.opts...)
				s.r0.Block = true
				rctx, cancel := c.ctx()
				defer cancel()
				_, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(rctx)
				if !errors.Is(err, c.want) {
					t.Fatalf("got %v, want %v", err, c.want)
				}
				if s.p.Calls != 0 {
					t.Errorf("the primary was sent %d statements", s.p.Calls)
				}
				if d := velox.ReplicaDownUntil(s.db, 0); !d.IsZero() {
					t.Errorf("replica 0 is in back-off until %v", d)
				}
				if hooked && (len(s.routes) != 1 || s.routes[0] != onR0) {
					t.Errorf("routes %+v, want replica 0's", s.routes)
				}
			})
		})
	}
}

// TestBackoffExpires: a failed replica is tried again once its back-off ends.
func TestBackoffExpires(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked, velox.WithReplicaBackoff(50*time.Millisecond))
		s.r0.Errs = []error{driver.ErrBadConn}
		readOne(t, s.db, ctx) // replica 0 fails
		readOne(t, s.db, ctx) // replica 1
		readOne(t, s.db, ctx) // replica 0's turn, skipped: replica 1
		time.Sleep(60 * time.Millisecond)
		readOne(t, s.db, ctx) // replica 1's turn
		readOne(t, s.db, ctx) // replica 0's, and it is back
		s.check(t, hooked, [3]int{1, 2, 3}, fellOver, onR1, onR1, onR1, onR0)
		if d := velox.ReplicaDownUntil(s.db, 0); !d.IsZero() {
			t.Errorf("replica 0's ended back-off is still recorded, until %v", d)
		}
	})
}

// TestAllReplicasDown: with every replica in back-off, reads go to the
// primary, and are not fallbacks.
func TestAllReplicasDown(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked)
		s.r0.Err, s.r1.Err = driver.ErrBadConn, driver.ErrBadConn
		for range 4 {
			readOne(t, s.db, ctx)
		}
		s.check(t, hooked, [3]int{4, 1, 1}, fellOver, fellOver, onPrimary, onPrimary)
	})
}

// TestRetryFailsReturnsPrimaryError: when the primary fails the retry too,
// the caller gets the primary's error.
func TestRetryFailsReturnsPrimaryError(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked)
		x := &pgconn.PgError{Code: "57P03", Message: "the database system is starting up"}
		s.r0.Errs, s.p.Err = []error{driver.ErrBadConn}, x
		_, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
		if !errors.Is(err, x) || errors.Is(err, driver.ErrBadConn) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") {
			t.Fatalf("got %v, want the primary's error, named", err)
		}
		s.check(t, hooked, [3]int{1, 1, 0}, fellOver)
	})
}

// TestRowsErrorIsNotRetried pins Ruling 4: an error the rows report after the
// statement was sent is returned, not retried, but a connection error still
// puts the replica in back-off.
func TestRowsErrorIsNotRetried(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked)
		s.r0.RowsErr = driver.ErrBadConn
		_, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
		if !errors.Is(err, driver.ErrBadConn) {
			t.Fatalf("got %v, want driver.ErrBadConn", err)
		}
		s.check(t, hooked, [3]int{0, 1, 0}, onR0)
		if velox.ReplicaDownUntil(s.db, 0).Before(time.Now()) {
			t.Error("replica 0 is not in back-off")
		}
	})
	t.Run("a query error from the rows leaves the replica in rotation", func(t *testing.T) {
		s := openFailover(false)
		s.r0.RowsErr = &pgconn.PgError{Code: "22012", Message: "division by zero"}
		if _, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err == nil {
			t.Fatal("the read should fail")
		}
		if d := velox.ReplicaDownUntil(s.db, 0); !d.IsZero() {
			t.Errorf("replica 0 is in back-off until %v", d)
		}
	})
}

// TestPinnedLoadFallsOver: a With load pinned to its query's replica (Ruling
// 3) falls over like any replica read when that replica fails.
func TestPinnedLoadFallsOver(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked)
		s.r0.Results = [][][]any{{invoiceRow(1), invoiceRow(2)}}
		s.r0.Errs = []error{nil, driver.ErrBadConn} // the query reads; the load fails
		s.p.Rows = [][]any{itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1)}
		invs, err := s.db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if items, ok := invs[1].Items.Get(); !ok || len(items) != 1 || items[0].SKU != "B" {
			t.Fatalf("items %v, loaded %v", items, ok)
		}
		// The query reads replica 0; its load fails there and falls over.
		s.check(t, hooked, [3]int{1, 2, 0}, onR0, fellOver)
		if velox.ReplicaDownUntil(s.db, 0).Before(time.Now()) {
			t.Error("replica 0 is not in back-off")
		}
	})
}

// TestLoadsFollowAFallback: a query that fell over to the primary loads its
// With relations there too (Ruling 3).
func TestLoadsFollowAFallback(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked)
		s.r0.Errs = []error{driver.ErrBadConn}
		s.p.Results = [][][]any{{invoiceRow(1), invoiceRow(2)}, {itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1)}}
		invs, err := s.db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if items, ok := invs[1].Items.Get(); !ok || len(items) != 1 || items[0].SKU != "B" {
			t.Fatalf("items %v, loaded %v", items, ok)
		}
		s.check(t, hooked, [3]int{2, 1, 0}, fellOver, onPrimary)
	})
}

// TestAroundRetryAfterFallbackStaysOnThePrimary: an Around hook that retries
// after a fallback whose retry failed sends the statement where its Route
// says, the primary, not back to the failed replica.
func TestAroundRetryAfterFallbackStaysOnThePrimary(t *testing.T) {
	var routes []velox.Route
	s := openFailover(false, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		err := next(ctx)
		if err != nil {
			err = next(ctx)
		}
		routes = append(routes, st.Route)
		return err
	}))
	s.r0.Errs, s.p.Errs = []error{driver.ErrBadConn}, []error{errors.New("primary hiccup")}
	readOne(t, s.db, ctx)
	s.check(t, false, [3]int{2, 1, 0})
	if len(routes) != 1 || routes[0] != fellOver {
		t.Errorf("routes %+v, want one fallback", routes)
	}
}

// TestDownReplicaTurnsSpreadEvenly: while one of three replicas is in
// back-off, its turns are shared evenly by the other two.
func TestDownReplicaTurnsSpreadEvenly(t *testing.T) {
	p := &velox.FakeExec{}
	rs := []*velox.FakeExec{{}, {}, {}}
	for _, e := range rs {
		e.Rows = [][]any{invoiceRow(1)}
	}
	db := velox.OpenFake(velox.Postgres, p, velox.WithReplicas(velox.FakeConn(rs[0]), velox.FakeConn(rs[1]), velox.FakeConn(rs[2])))
	velox.SetReplicaDown(db, 1, time.Now().Add(time.Hour))
	for range 60 {
		readOne(t, db, ctx)
	}
	if got := [4]int{p.Calls, rs[0].Calls, rs[1].Calls, rs[2].Calls}; got != [4]int{0, 30, 0, 30} {
		t.Errorf("calls (primary, replicas 0, 1, 2) %v, want [0 30 0 30]", got)
	}
}

// TestEndedBackoffIsCleared: a replica whose back-off has ended is put back
// on the path that does not read the clock.
func TestEndedBackoffIsCleared(t *testing.T) {
	s := openFailover(false)
	velox.SetReplicaDown(s.db, 0, time.Now().Add(-time.Second))
	readOne(t, s.db, ctx)
	if d := velox.ReplicaDownUntil(s.db, 0); !d.IsZero() {
		t.Errorf("replica 0's ended back-off is still recorded, until %v", d)
	}
	s.check(t, false, [3]int{0, 1, 0})
}
