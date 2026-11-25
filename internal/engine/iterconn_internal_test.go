package engine

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox/internal/fakeconn"
)

// TestHoldConnOnlyOnAPoolOfOne: a DB on any other pool notes nothing, and
// releasing that note leaves another holder's alone.
func TestHoldConnOnlyOnAPoolOfOne(t *testing.T) {
	db := newDB(SQLite, &fakeconn.Exec{}, nil)
	other := &connHold{caller: context.Background()}
	db.Cfg.connHold.Store(other)
	h := holdConn(context.Background(), &db, nil)
	if h != nil || holder(&db) != other {
		t.Errorf("note %v; holder %v", h, holder(&db))
	}
	releaseConn(&db, h)
	if holder(&db) != other {
		t.Errorf("holder %v", holder(&db))
	}
}

// uncomparableCtx is a context whose dynamic type == cannot compare.
type uncomparableCtx struct {
	context.Context
	tags []string
}

// TestConnHoldOwns: the holder owns the context it was given and WithoutTx
// of its fn's, and no other; an uncomparable context does not panic.
func TestConnHoldOwns(t *testing.T) {
	given := t.Context()
	child, cancelChild := context.WithCancel(given)
	defer cancelChild()
	f := &txFrame{}
	f.ctx = txCtx{Context: given, root: f}
	h := &connHold{caller: given, tx: f}
	for name, c := range map[string]struct {
		ctx  context.Context
		want bool
	}{
		"given":        {given, true},
		"WithoutTx":    {noTxCtx{&f.ctx}, true},
		"child":        {child, false},
		"background":   {context.Background(), false},
		"uncomparable": {uncomparableCtx{given, nil}, false},
	} {
		if got := h.owns(c.ctx); got != c.want {
			t.Errorf("%s: owns %v, want %v", name, got, c.want)
		}
	}
	iter := &connHold{caller: uncomparableCtx{given, nil}}
	if iter.owns(uncomparableCtx{given, nil}) {
		t.Error("an uncomparable context is owned")
	}
}

// nopConnector opens no connection; a pool on it is only counted.
type nopConnector struct{}

func (nopConnector) Connect(context.Context) (driver.Conn, error) { return nil, driver.ErrBadConn }
func (nopConnector) Driver() driver.Driver                        { return nil }

// TestWaitsForConn: a statement under the context that holds the pool's one
// connection fails at once with the holder's error; once the statement's
// context, or the context of the Tx that holds the connection, has ended,
// the error says so and matches the context's error; any other context, a
// transaction's statement and a pool of more than one wait.
func TestWaitsForConn(t *testing.T) {
	pool := sql.OpenDB(nopConnector{})
	t.Cleanup(func() { _ = pool.Close() })
	pool.SetMaxOpenConns(1)
	db := Open(SQLite, pool, nil)

	given := t.Context()
	txCtxOf := func(c context.Context) *txFrame {
		f := &txFrame{}
		f.ctx = txCtx{Context: c, root: f}
		return f
	}
	hold := func(h *connHold) { db.Cfg.connHold.Store(h) }

	hold(&connHold{caller: given})
	if err := waitsForConn(given, &db); !errors.Is(err, errIterConn) {
		t.Errorf("under an Iter's context: %v", err)
	}
	if err := waitsForConn(context.Background(), &db); err != nil {
		t.Errorf("under another context: %v", err)
	}
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	hold(&connHold{caller: ended})
	if err := waitsForConn(ended, &db); !errors.Is(err, context.Canceled) || errors.Is(err, errIterConn) {
		t.Errorf("under an Iter's ended context: %v", err)
	}

	hold(&connHold{caller: given, tx: txCtxOf(given)})
	if err := waitsForConn(given, &db); !errors.Is(err, errTxConn) {
		t.Errorf("under a Tx's given context: %v", err)
	}
	// The Tx's own context, under a deadline of its own, has ended while the
	// context it was given has not.
	txEnded, cancelTx := context.WithCancel(given)
	cancelTx()
	hold(&connHold{caller: given, tx: txCtxOf(txEnded)})
	err := waitsForConn(given, &db)
	if !errors.Is(err, context.Canceled) || errors.Is(err, errTxConn) {
		t.Errorf("after the Tx's context ended: %v", err)
	}
	if want := "the context of the Tx whose fn this is ended: context canceled"; err == nil || err.Error() != want {
		t.Errorf("after the Tx's context ended: %v, want %q", err, want)
	}

	hold(&connHold{caller: given, tx: txCtxOf(given)})
	inTx := db
	inTx.InTx = true
	if err := waitsForConn(given, &inTx); err != nil {
		t.Errorf("a transaction's statement: %v", err)
	}
	pool.SetMaxOpenConns(2)
	if err := waitsForConn(given, &db); err != nil {
		t.Errorf("on a pool of two: %v", err)
	}
}

// TestBumpOnce: an update of an entity with a Version column gets one
// increment, and one that already assigns it gets none.
func TestBumpOnce(t *testing.T) {
	in := &EntityInfo{Name: "V", Columns: []ColumnInfo{{Name: "id"}, {Name: "version"}}, version: 2}
	st := &Statement{Op: OpUpdate, Entity: in}
	bumpOnce(st)
	bumpOnce(st)
	if len(st.Set) != 1 || st.Set[0] != (Assignment{Column: 1, Op: AssignAdd, Value: int64(1)}) {
		t.Errorf("set %+v", st.Set)
	}
	plain := &Statement{Op: OpUpdate, Entity: &EntityInfo{Name: "P"}}
	if bumpOnce(plain); len(plain.Set) != 0 {
		t.Errorf("an entity without a version: %+v", plain.Set)
	}
}

// TestWatchHeldWarns: a statement on a pool of one, under a context its Tx
// was not given (one derived from it, here), waits for the connection the
// Tx holds; after heldWarnAfter velox logs one warning that says so, and
// the statement fails at its deadline with errHeld. A BEGIN under such a
// context is watched too. With nothing held, nothing is logged.
func TestWatchHeldWarns(t *testing.T) {
	old := heldWarnAfter
	heldWarnAfter = 20 * time.Millisecond
	t.Cleanup(func() { heldWarnAfter = old })
	var buf syncBuffer
	oldLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLog) })

	db, err := Connect("sqlite", "file:watchheld?mode=memory", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	if _, err := db.RawExec(ctx, "test", "SELECT 1", nil); err != nil || buf.Len() != 0 {
		t.Fatalf("nothing held: %v, logged %q", err, buf.String())
	}
	type key struct{}
	err = db.Tx(ctx, func(context.Context, *DB) error {
		derived, cancel := context.WithTimeout(context.WithValue(ctx, key{}, 1), 200*time.Millisecond)
		defer cancel()
		_, err := db.RawExec(derived, "test", "SELECT 1", nil)
		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, errHeld) {
			t.Errorf("the statement that waited: %v", err)
		}
		begin, cancelBegin := context.WithTimeout(context.WithValue(ctx, key{}, 2), 200*time.Millisecond)
		defer cancelBegin()
		if err := db.Tx(begin, func(context.Context, *DB) error { return nil }); err == nil {
			t.Error("a BEGIN that waited: nil error")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "waited for the pool's one connection"); n != 2 {
		t.Errorf("warnings = %d, want 2: %s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "holder=Tx") || !strings.Contains(buf.String(), "entity=transaction") {
		t.Errorf("the warnings name no Tx holder or BEGIN: %s", buf.String())
	}
}

// syncBuffer is a bytes.Buffer safe for a timer's goroutine and the test's.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}
