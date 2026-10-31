package engine_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/fakeconn"
	"github.com/syssam/velox2/internal/testmodel"
)

// connectError returns a *pgconn.ConnectError as pgx makes one: its fields
// are unexported, so it is made by connecting to a Unix socket in a
// directory that does not exist, which uses no network and no port.
func connectError(t *testing.T) error {
	t.Helper()
	cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pgconn.Connect(cctx, "host=/nonexistent-velox-dir user=u dbname=db sslmode=disable")
	var ce *pgconn.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("connecting to a missing socket returned %T %v, want a *pgconn.ConnectError", err, err)
	}
	return fmt.Errorf("acquire: %w", err)
}

// refusedConnectError returns the *pgconn.ConnectError pgx makes when the
// server answers its startup message with an error, code: a server on a Unix
// socket in a temporary directory that refuses every connection so.
func refusedConnectError(t *testing.T, code string) error {
	t.Helper()
	// A Unix socket's path is limited to about 104 bytes, and a long TMPDIR
	// (macOS's is already long) would push it over, so use /tmp, short, where
	// the OS has one.
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "vx")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	l, err := net.Listen("unix", dir+"/.s.PGSQL.5432")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		be := pgproto3.NewBackend(c, c)
		if _, err := be.ReceiveStartupMessage(); err != nil {
			return
		}
		be.Send(&pgproto3.ErrorResponse{Severity: "FATAL", Code: code, Message: "sorry, too many clients already"})
		_ = be.Flush()
	}()
	cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = pgconn.Connect(cctx, "host="+dir+" user=u dbname=db sslmode=disable")
	var ce *pgconn.ConnectError
	var pg *pgconn.PgError
	if !errors.As(err, &ce) || !errors.As(err, &pg) || pg.Code != code {
		t.Fatalf("connecting to a refusing server returned %T %v, want a *pgconn.ConnectError wrapping PgError %s", err, err, code)
	}
	return fmt.Errorf("acquire: %w", err)
}

// safeToRetryError is an error pgx marks as raised before anything was sent.
type safeToRetryError struct{}

func (safeToRetryError) Error() string { return "failed to write startup message" }

func (safeToRetryError) SafeToRetry() bool { return true }

// netTimeoutError is a net.Error that is a timeout, not from a dial.
type netTimeoutError struct{}

func (netTimeoutError) Error() string { return "read: i/o timeout" }

func (netTimeoutError) Timeout() bool { return true }

func (netTimeoutError) Temporary() bool { return true }

// mysqlErr returns a class that is a real driver error with number n.
func mysqlErr(n uint16, msg string) func(*testing.T) error {
	return func(*testing.T) error {
		return &mysql.MySQLError{Number: n, SQLState: [5]byte{'H', 'Y', '0', '0', '0'}, Message: msg}
	}
}

// TestFailoverClasses: a read whose replica fails with a connection error is
// retried on the primary, and the replica is skipped until its back-off ends.
func TestFailoverClasses(t *testing.T) {
	classes := map[string]func(t *testing.T) error{
		"driver.ErrBadConn": func(*testing.T) error { return driver.ErrBadConn },
		"a *net.OpError": func(*testing.T) error {
			return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		},
		"a reset connection": func(*testing.T) error {
			return &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
		},
		"a dial that timed out": func(*testing.T) error {
			return &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}
		},
		"io.ErrUnexpectedEOF": func(*testing.T) error {
			return fmt.Errorf("failed to receive message: %w", io.ErrUnexpectedEOF)
		},
		"io.EOF":                func(*testing.T) error { return io.EOF },
		"PgError 57P01":         func(*testing.T) error { return &pgconn.PgError{Severity: "FATAL", Code: "57P01"} },
		"PgError 57P02":         func(*testing.T) error { return &pgconn.PgError{Severity: "FATAL", Code: "57P02"} },
		"PgError 57P03":         func(*testing.T) error { return &pgconn.PgError{Severity: "FATAL", Code: "57P03"} },
		"mysql.ErrInvalidConn":  func(*testing.T) error { return fmt.Errorf("query: %w", mysql.ErrInvalidConn) },
		"pgconn.SafeToRetry":    func(*testing.T) error { return fmt.Errorf("send: %w", safeToRetryError{}) },
		"a pgconn.ConnectError": connectError,
		"a pgconn.ConnectError wrapping PgError 53300": func(t *testing.T) error {
			return refusedConnectError(t, "53300")
		},
		"MySQL 1040 too many connections":  mysqlErr(1040, "Too many connections"),
		"MySQL 1203 user connection limit": mysqlErr(1203, "User velox already has more than 'max_user_connections' active connections"),
		"MySQL 1226 user resource limit":   mysqlErr(1226, "User 'velox' has exceeded the 'max_user_connections' resource"),
		"MySQL 1045 access denied":         mysqlErr(1045, "Access denied for user 'velox'@'10.0.0.1' (using password: YES)"),
		"MySQL 1129 host blocked":          mysqlErr(1129, "Host '10.0.0.1' is blocked because of many connection errors"),
		"MySQL 1130 host not allowed":      mysqlErr(1130, "Host '10.0.0.1' is not allowed to connect to this MySQL server"),
		"MySQL 1053 shutdown in progress":  mysqlErr(1053, "Server shutdown in progress"),
		"MySQL 1927 connection killed":     mysqlErr(1927, "Connection was killed"),
		"a wrapped MySQL 1040": func(*testing.T) error {
			return fmt.Errorf("query: %w", &mysql.MySQLError{Number: 1040, Message: "Too many connections"})
		},
		"sql.ErrConnDone":      func(*testing.T) error { return fmt.Errorf("query: %w", sql.ErrConnDone) },
		"net.ErrClosed":        func(*testing.T) error { return net.ErrClosed },
		"pgconn.ErrConnClosed": func(*testing.T) error { return pgconn.ErrConnClosed },
	}
	for name, mk := range classes {
		t.Run(name, func(t *testing.T) {
			err := mk(t)
			bothPaths(t, func(t *testing.T, hooked bool) {
				s := openFailover(hooked)
				s.r0.Errs = []error{err}
				for range 3 {
					readOne(t, ctx, s.db)
				}
				// replica 0 fails and the primary serves the read; then
				// replica 1, and replica 1 again in replica 0's turn.
				s.check(t, hooked, [3]int{1, 1, 2}, fellOver, onR1, onR1)
				if replicaDownUntil(s.db, 0).Before(time.Now().Add(4 * time.Second)) {
					t.Errorf("replica 0 is in back-off until %v, want about 5s from now", replicaDownUntil(s.db, 0))
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
		// too many connections, but from a statement, not a connect
		&pgconn.PgError{Severity: "FATAL", Code: "53300", Message: "too many connections"},
		&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'a' for key 'p.p_code_uq'"},
		&mysql.MySQLError{Number: 1146, Message: "Table 'velox.invoices' doesn't exist"},
		&mysql.MySQLError{Number: 1064, Message: "You have an error in your SQL syntax"},
		errors.New("something else"),
		// a net timeout that is not a dial's: maybe a slow query
		&net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded},
		fmt.Errorf("read: %w", netTimeoutError{}),
	} {
		t.Run(qerr.Error(), func(t *testing.T) {
			bothPaths(t, func(t *testing.T, hooked bool) {
				s := openFailover(hooked)
				s.r0.Errs = []error{qerr}
				_, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
				if !errors.Is(err, qerr) {
					t.Fatalf("got %v, want %v", err, qerr)
				}
				readOne(t, ctx, s.db)
				readOne(t, ctx, s.db)
				s.check(t, hooked, [3]int{0, 2, 1}, onR0, onR1, onR0)
				if d := replicaDownUntil(s.db, 0); !d.IsZero() {
					t.Errorf("replica 0 is in back-off until %v", d)
				}
			})
		})
	}
}

// TestCancelledReadIsNotAFailover pins that a read whose
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
				if d := replicaDownUntil(s.db, 0); !d.IsZero() {
					t.Errorf("replica 0 is in back-off until %v", d)
				}
				if hooked && (len(s.routes) != 1 || s.routes[0] != onR0) {
					t.Errorf("routes %+v, want replica 0's", s.routes)
				}
			})
		})
	}
}

// TestBackoffExpires: a failed replica is skipped for WithReplicaBackoff's
// duration, and tried again once its back-off ends.
func TestBackoffExpires(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked, velox.WithReplicaBackoff(time.Minute))
		s.r0.Errs = []error{driver.ErrBadConn}
		before := time.Now()
		readOne(t, ctx, s.db) // replica 0 fails
		after := time.Now()
		if d := replicaDownUntil(s.db, 0); d.Before(before.Add(time.Minute)) || d.After(after.Add(time.Minute)) {
			t.Errorf("replica 0 is in back-off until %v, want a minute from the read", d)
		}
		readOne(t, ctx, s.db) // replica 1
		readOne(t, ctx, s.db) // replica 0's turn, skipped: replica 1
		// End the back-off, rather than wait for it.
		setReplicaDown(s.db, 0, time.Now().Add(-time.Millisecond))
		readOne(t, ctx, s.db) // replica 1's turn
		readOne(t, ctx, s.db) // replica 0's, and it is back
		s.check(t, hooked, [3]int{1, 2, 3}, fellOver, onR1, onR1, onR1, onR0)
		if d := replicaDownUntil(s.db, 0); !d.IsZero() {
			t.Errorf("replica 0's ended back-off is still recorded, until %v", d)
		}
	})
}

// TestRowsErrorFallsOver: a connection error the replica's rows report,
// before any row reached the caller, falls over to the primary like one from
// the query call; the read starts again there.
func TestRowsErrorFallsOver(t *testing.T) {
	t.Run("All", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := openFailover(hooked)
			s.r0.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
			s.r0.RowsErr = driver.ErrBadConn
			s.p.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
			invs, err := s.db.Query[testmodel.Invoice]().Limit(10).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(invs) != 2 || invs[0].ID != 1 || invs[1].ID != 2 {
				t.Errorf("read %+v, want the primary's two invoices once each", invs)
			}
			s.check(t, hooked, [3]int{1, 1, 0}, fellOver)
			if replicaDownUntil(s.db, 0).Before(time.Now()) {
				t.Error("replica 0 is not in back-off")
			}
		})
	})
	t.Run("Count", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := openFailover(hooked)
			s.r0.Rows = [][]any{{int64(3)}}
			s.r0.RowsErr = io.ErrUnexpectedEOF
			s.p.Rows = [][]any{{int64(5)}}
			n, err := s.db.Query[testmodel.Invoice]().Count(ctx)
			if err != nil || n != 5 {
				t.Fatalf("Count %d, %v; want the primary's 5", n, err)
			}
			s.check(t, hooked, [3]int{1, 1, 0}, fellOver)
		})
	})
	t.Run("Iter before its first row", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := openFailover(hooked)
			s.r0.Rows = nil
			s.r0.RowsErr = driver.ErrBadConn
			s.p.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
			var ids []int64
			for inv, err := range s.db.Query[testmodel.Invoice]().Limit(10).Iter(ctx) {
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, inv.ID)
			}
			if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
				t.Errorf("yielded %v, want [1 2]", ids)
			}
			s.check(t, hooked, [3]int{1, 1, 0}, fellOver)
		})
	})
	t.Run("Iter after a row", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := openFailover(hooked)
			s.r0.Rows = [][]any{invoiceRow(1)}
			s.r0.RowsErr = driver.ErrBadConn
			var ids []int64
			var errs []error
			for inv, err := range s.db.Query[testmodel.Invoice]().Limit(10).Iter(ctx) {
				if err != nil {
					errs = append(errs, err)
					continue
				}
				ids = append(ids, inv.ID)
			}
			if len(ids) != 1 || len(errs) != 1 || !errors.Is(errs[0], driver.ErrBadConn) || !strings.HasPrefix(errs[0].Error(), "velox: testmodel.Invoice: ") {
				t.Fatalf("yielded %v and errors %v; want row 1, then the replica's error", ids, errs)
			}
			s.check(t, hooked, [3]int{0, 1, 0}, onR0)
			if replicaDownUntil(s.db, 0).Before(time.Now()) {
				t.Error("replica 0 is not in back-off")
			}
		})
	})
	t.Run("a pinned With load", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := openFailover(hooked)
			s.r0.Results = [][][]any{{invoiceRow(1), invoiceRow(2)}, {itemRow(10, 1, "A", 1)}}
			s.r0.RowsErrs = []error{nil, driver.ErrBadConn} // the query reads; the load's rows break
			s.p.Rows = [][]any{itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1)}
			invs, err := s.db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := invs[0].Items.Get()
			b, _ := invs[1].Items.Get()
			if len(a) != 1 || len(b) != 1 || b[0].SKU != "B" {
				t.Fatalf("items %v and %v, want the primary's one each", a, b)
			}
			s.check(t, hooked, [3]int{1, 2, 0}, onR0, fellOver)
		})
	})
	t.Run("a reader's own error is not a failover", func(t *testing.T) {
		// pgx reports a failed Scan again through rows.Err(), wrapping its
		// cause; a Scanner that fails with io.ErrUnexpectedEOF must not
		// look like a broken connection. The fake's Scan fails on a row
		// with the wrong column count.
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := openFailover(hooked)
			s.r0.Rows = [][]any{{int64(1)}}
			s.r0.RowsErr = fmt.Errorf("can't scan into dest[0]: %w", io.ErrUnexpectedEOF)
			_, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
			if err == nil || !strings.Contains(err.Error(), "destinations") || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") {
				t.Fatalf("got %v, want the reader's scan error, named", err)
			}
			if d := replicaDownUntil(s.db, 0); !d.IsZero() {
				t.Errorf("replica 0 is in back-off until %v", d)
			}
			s.check(t, hooked, [3]int{0, 1, 0}, onR0)
			// Replica 0 is still in rotation: it serves its next turn.
			s.r0.Rows, s.r0.RowsErr = [][]any{invoiceRow(1)}, nil
			readOne(t, ctx, s.db) // replica 1
			readOne(t, ctx, s.db) // replica 0
			s.check(t, hooked, [3]int{0, 2, 1}, onR0, onR1, onR0)
		})
	})
	t.Run("a query error from the rows leaves the replica in rotation", func(t *testing.T) {
		s := openFailover(false)
		s.r0.RowsErr = &pgconn.PgError{Code: "22012", Message: "division by zero"}
		if _, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err == nil {
			t.Fatal("the read should fail")
		}
		if d := replicaDownUntil(s.db, 0); !d.IsZero() {
			t.Errorf("replica 0 is in back-off until %v", d)
		}
		s.check(t, false, [3]int{0, 1, 0})
	})
}

// TestPinnedLoadFallsOver: a With load pinned to its query's replica
// falls over like any replica read when that replica fails.
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
		if replicaDownUntil(s.db, 0).Before(time.Now()) {
			t.Error("replica 0 is not in back-off")
		}
	})
}

// TestDownReplicaTurnsSpreadEvenly: while one of three replicas is in
// back-off, its turns are shared evenly by the other two.
func TestDownReplicaTurnsSpreadEvenly(t *testing.T) {
	p := &fakeconn.Exec{}
	rs := []*fakeconn.Exec{{}, {}, {}}
	for _, e := range rs {
		e.Rows = [][]any{invoiceRow(1)}
	}
	db := openFake(velox.Postgres, p, velox.WithReplicas(fakeConn(rs[0]), fakeConn(rs[1]), fakeConn(rs[2])))
	setReplicaDown(db, 1, time.Now().Add(time.Hour))
	for range 60 {
		readOne(t, ctx, db)
	}
	if got := [4]int{p.Calls, rs[0].Calls, rs[1].Calls, rs[2].Calls}; got != [4]int{0, 30, 0, 30} {
		t.Errorf("calls (primary, replicas 0, 1, 2) %v, want [0 30 0 30]", got)
	}
}

// TestEndedBackoffIsCleared: a replica whose back-off has ended is put back
// on the path that does not read the clock.
func TestEndedBackoffIsCleared(t *testing.T) {
	s := openFailover(false)
	setReplicaDown(s.db, 0, time.Now().Add(-time.Second))
	readOne(t, ctx, s.db)
	if d := replicaDownUntil(s.db, 0); !d.IsZero() {
		t.Errorf("replica 0's ended back-off is still recorded, until %v", d)
	}
	s.check(t, false, [3]int{0, 1, 0})
}
