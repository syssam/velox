package gqlgenx_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// TestTransactionerExtensionName: the name gqlgen's server knows it by.
func TestTransactionerExtensionName(t *testing.T) {
	if name := (gqlgenx.Transactioner{}).ExtensionName(); name != "VeloxTransactioner" {
		t.Errorf("ExtensionName() = %q, want VeloxTransactioner", name)
	}
}

// TestRootDBJoinsTheMutationsTx: a mutation's resolver holds only the root
// DB, and a statement it runs on it under the resolver's context joins the
// mutation's transaction (decision 0171). A query has no transaction: its
// statements run on the DB alone.
func TestRootDBJoinsTheMutationsTx(t *testing.T) {
	db, l := handlerDB(t)
	srv := handlerServer(t, db, nil)
	if got, want := post(t, context.Background(), srv, `{ count }`), `{"data":{"count":0}}`; got != want {
		t.Errorf("query: %s, want %s", got, want)
	}
	if stmts, txs := l.take(); !slices.Equal(stmts, []string{"select"}) || len(txs) != 0 {
		t.Errorf("query: statements %q, transactions %q; want one select outside a transaction", stmts, txs)
	}
	if got, want := post(t, context.Background(), srv, `mutation { add(id: 1) }`), `{"data":{"add":1}}`; got != want {
		t.Errorf("mutation: %s, want %s", got, want)
	}
	if stmts, txs := l.take(); !slices.Equal(stmts, []string{"insert in tx"}) || !slices.Equal(txs, []string{"commit"}) {
		t.Errorf("mutation: statements %q, transactions %q; want one insert in one committed transaction", stmts, txs)
	}
	// The insert is the transaction's: a later failure takes it back.
	_ = post(t, context.Background(), srv, `mutation { a: add(id: 2) b: fail(id: 3) }`)
	if _, txs := l.take(); !slices.Equal(txs, []string{"rollback"}) {
		t.Errorf("failing mutation: transactions %q, want one rolled back", txs)
	}
	if ids := peopleIDs(t, db, l); !slices.Equal(ids, []int64{1}) {
		t.Errorf("people %v, want [1]: the failed mutation's inserts rolled back", ids)
	}
}

// TestTransactionerValidate: gqlgen's server refuses a Transactioner with no
// DB, and one whose options retry, since gqlgen makes the response once and
// the operation cannot run again.
func TestTransactionerValidate(t *testing.T) {
	db, _ := handlerDB(t)
	const nilDB = "gqlgenx.Transactioner: nil DB"
	if err := (gqlgenx.Transactioner{}).Validate(nil); err == nil || err.Error() != nilDB {
		t.Errorf("Validate with no DB: %v, want %q", err, nilDB)
	}
	func() {
		defer func() {
			if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), nilDB) {
				t.Errorf("Use with a nil DB panicked with %v, want %q", p, nilDB)
			}
		}()
		handler.New(testSchema(nil)).Use(gqlgenx.Transactioner{})
	}()

	retry := gqlgenx.Transactioner{DB: db, Options: []velox.TxOption{velox.TxTimeout(time.Second), velox.TxRetrySerializable(3)}}
	want := "gqlgenx.Transactioner: TxRetrySerializable(3) would run the mutation again, and gqlgen makes its response once"
	if err := retry.Validate(nil); err == nil || err.Error() != want {
		t.Errorf("Validate = %v, want %q", err, want)
	}
	func() {
		defer func() {
			if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), want) {
				t.Errorf("Use with a retry panicked with %v, want %q", p, want)
			}
		}()
		handler.New(testSchema(nil)).Use(retry)
	}()
	for name, opts := range map[string][]velox.TxOption{
		"no options":                   nil,
		"the last retry asks for none": {velox.TxRetrySerializable(3), velox.TxRetrySerializable(0)},
		"isolation":                    {velox.TxIsolation(sql.LevelSerializable)},
	} {
		if err := (gqlgenx.Transactioner{DB: db, Options: opts}).Validate(nil); err != nil {
			t.Errorf("%s: Validate = %v, want nil", name, err)
		}
	}
}

// TestTransactionerIgnoresDBRetry: a retry among the DB's transaction
// defaults does not run a mutation again.
func TestTransactionerIgnoresDBRetry(t *testing.T) {
	db, l := handlerDB(t, velox.WithTxDefaults(velox.TxRetrySerializable(3)))
	var runs atomic.Int64
	resolvers := handlerResolvers(db, nil)
	fail := resolvers["fail"]
	resolvers["fail"] = func(ctx context.Context, args map[string]any) (any, error) {
		runs.Add(1)
		return fail(ctx, args)
	}
	srv := serve(resolvers, gqlgenx.Transactioner{DB: db})
	_ = post(t, context.Background(), srv, `mutation { fail(id: 1) }`)
	if n := runs.Load(); n != 1 {
		t.Errorf("the resolver ran %d times, want 1", n)
	}
	if _, txs := l.take(); !slices.Equal(txs, []string{"rollback"}) {
		t.Errorf("transactions %q, want one rolled back", txs)
	}
}

func TestMutationCommits(t *testing.T) {
	db, l := handlerDB(t)
	srv := handlerServer(t, db, nil)
	got := post(t, context.Background(), srv, `mutation { a: add(id: 1) b: add(id: 2) }`)
	if want := `{"data":{"a":1,"b":2}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// Both root fields ran in the one transaction, which committed.
	stmts, txs := l.take()
	if !slices.Equal(stmts, []string{"insert in tx", "insert in tx"}) || !slices.Equal(txs, []string{"commit"}) {
		t.Errorf("statements %q, transactions %q; want two inserts in one committed transaction", stmts, txs)
	}
	if ids := peopleIDs(t, db, l); !slices.Equal(ids, []int64{1, 2}) {
		t.Errorf("people %v, want [1 2]", ids)
	}
}

// TestMutationGetsTheTxContext: a mutation's resolvers get the transaction's
// context. It has the transaction's deadline. Under it a statement on the DB
// given to Transactioner, which began the transaction, runs in it, on the
// connection the transaction holds; one on another DB fails with
// ErrOutsideTx, before it waits for that connection.
func TestMutationGetsTheTxContext(t *testing.T) {
	db, _ := handlerDB(t, velox.WithDeadlines(velox.Deadlines{Tx: 30 * time.Second}))
	var deadline bool
	var inside, outside error
	srv := serve(map[string]resolver{
		"add": func(ctx context.Context, args map[string]any) (any, error) {
			_, deadline = ctx.Deadline()
			_, inside = db.Query[person]().Count(ctx)
			_, outside = db.WithoutPolicy("another DB").Query[person]().Count(ctx)
			return args["id"], nil
		},
	}, gqlgenx.Transactioner{DB: db})
	if got, want := post(t, context.Background(), srv, `mutation { add(id: 1) }`), `{"data":{"add":1}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if !deadline {
		t.Error("the resolver's context has no deadline")
	}
	if inside != nil {
		t.Errorf("a statement on the extension's DB: %v", inside)
	}
	if !errors.Is(outside, velox.ErrOutsideTx) {
		t.Errorf("a statement on another DB: %v, want ErrOutsideTx", outside)
	}
}

// TestMutationWritesOnItsDB: a mutation's resolver that writes on the DB
// given to Transactioner, as a resolver holding only that DB does, writes in
// the mutation's transaction, and its write rolls back with the mutation.
func TestMutationWritesOnItsDB(t *testing.T) {
	db, _ := handlerDB(t)
	var seen int64
	srv := serve(map[string]resolver{
		"fail": func(ctx context.Context, args map[string]any) (any, error) {
			if err := db.Insert(&person{ID: args["id"].(int64), Name: "p", Age: 1}).Exec(ctx); err != nil {
				return nil, err
			}
			n, err := db.Query[person]().Count(ctx)
			if err != nil {
				return nil, err
			}
			seen = n
			return nil, errFail
		},
	}, gqlgenx.Transactioner{DB: db})
	if got := post(t, context.Background(), srv, `mutation { fail(id: 1) }`); !strings.Contains(got, errFail.Error()) {
		t.Fatalf("got %s, want the resolver's error", got)
	}
	if seen != 1 {
		t.Errorf("in the mutation the DB counted %d people, want its own insert", seen)
	}
	if n, err := db.Query[person]().Count(context.Background()); err != nil || n != 0 {
		t.Errorf("after the mutation: %d people, %v; want 0: the insert rolled back", n, err)
	}
}

// TestMutationWithoutTx: a mutation's resolver holds only the transaction's
// context. Under velox.WithoutTx it, its statement on the DB given to
// Transactioner runs outside the transaction: the audit row survives the
// mutation's rollback. Once the mutation has ended, the context the resolver kept marks
// nothing, and a statement on the DB runs under it.
func TestMutationWithoutTx(t *testing.T) {
	// A file, so that a second connection sees the same database.
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "people.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(2) // the transaction's, and the audit row's
	if _, err := sqlDB.Exec(`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, sqlDB)
	var kept context.Context
	srv := serve(map[string]resolver{
		"fail": func(ctx context.Context, args map[string]any) (any, error) {
			kept = ctx
			id := args["id"].(int64)
			// The audit row first: SQLite's transaction takes its write
			// lock at its first write, and would make this one wait.
			if err := db.Insert(&person{ID: 100 + id, Name: "audit", Age: 1}).Exec(velox.WithoutTx(ctx)); err != nil {
				return nil, err
			}
			if err := db.Insert(&person{ID: id, Name: "p", Age: 1}).Exec(ctx); err != nil {
				return nil, err
			}
			return nil, errFail
		},
	}, gqlgenx.Transactioner{DB: db})
	if got := post(t, context.Background(), srv, `mutation { fail(id: 1) }`); !strings.Contains(got, errFail.Error()) {
		t.Fatalf("got %s, want the resolver's error", got)
	}
	if ids := peopleIDs(t, db, &txLog{}); !slices.Equal(ids, []int64{101}) {
		t.Errorf("people %v, want [101]: the audit row alone", ids)
	}
	if n, err := db.Query[person]().Count(context.WithoutCancel(kept)); err != nil || n != 1 {
		t.Errorf("after the mutation, on the resolver's context: %d, %v; want 1 and no error", n, err)
	}
}

func TestMutationRollsBack(t *testing.T) {
	for _, c := range []struct {
		name, query, want string
	}{
		{
			"error",
			`mutation { fail(id: 1) }`,
			`{"errors":[{"message":"fail failed","path":["fail"]}],"data":null}`,
		},
		{
			"panic",
			`mutation { boom(id: 1) }`,
			`{"errors":[{"message":"internal system error","path":["boom"]}],"data":null}`,
		},
		{
			// A field that succeeds is rolled back with the one that fails,
			// and its data is not reported.
			"partial",
			`mutation { a: add(id: 1) b: fail(id: 2) c: add(id: 3) }`,
			`{"errors":[{"message":"fail failed","path":["b"]}],"data":null}`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, l := handlerDB(t)
			srv := handlerServer(t, db, nil)
			if got := post(t, context.Background(), srv, c.query); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
			if _, txs := l.take(); !slices.Equal(txs, []string{"rollback"}) {
				t.Errorf("transactions %q, want one rolled back", txs)
			}
			if ids := peopleIDs(t, db, l); len(ids) != 0 {
				t.Errorf("people %v, want none", ids)
			}
		})
	}
}

// TestMutationCommitFails: a mutation whose resolvers succeed but whose
// transaction does not commit, here because the request was canceled, keeps
// nothing and says so.
func TestMutationCommitFails(t *testing.T) {
	db, l := handlerDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := handlerServer(t, db, &cancel)
	got := post(t, ctx, srv, `mutation { cancel(id: 1) }`)
	if want := `{"errors":[{"message":"velox: transaction: context canceled"}],"data":null}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if _, txs := l.take(); !slices.Equal(txs, []string{"error"}) {
		t.Errorf("transactions %q, want one that failed", txs)
	}
	if ids := peopleIDs(t, db, l); len(ids) != 0 {
		t.Errorf("people %v, want none", ids)
	}
}

// TestMutationPanicRollsBack: a panic gqlgen does not recover, here in a
// response interceptor inside the extension's, rolls the transaction back and
// carries on to the server, which reports it.
func TestMutationPanicRollsBack(t *testing.T) {
	db, l := handlerDB(t)
	srv := handlerServer(t, db, nil)
	srv.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
		resp := next(ctx)
		if resp != nil {
			panic("after the response")
		}
		return resp
	})
	got := post(t, context.Background(), srv, `mutation { add(id: 1) }`)
	if want := `{"errors":[{"message":"internal system error"}],"data":null}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if _, txs := l.take(); !slices.Equal(txs, []string{"rollback"}) {
		t.Errorf("transactions %q, want one rolled back", txs)
	}
	if ids := peopleIDs(t, db, l); len(ids) != 0 {
		t.Errorf("people %v, want none", ids)
	}
}

func TestQueryHasNoTx(t *testing.T) {
	db, l := handlerDB(t)
	srv := handlerServer(t, db, nil)
	if got, want := post(t, context.Background(), srv, `{ count }`), `{"data":{"count":0}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if stmts, txs := l.take(); !slices.Equal(stmts, []string{"select"}) || len(txs) != 0 {
		t.Errorf("statements %q, transactions %q; want one select outside a transaction", stmts, txs)
	}
}

// TestStreamedOperations: over SSE, which asks for responses until there are
// none, a mutation still runs in one transaction, and a subscription gets
// the DB with no transaction: each event's statements run on db.
func TestStreamedOperations(t *testing.T) {
	db, l := handlerDB(t)
	srv := handlerServer(t, db, nil)
	sse := func(query string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"query": query})
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "text/event-stream")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Body.String()
	}
	got := sse(`mutation { add(id: 1) }`)
	if want := "event: next\ndata: {\"data\":{\"add\":1}}\n\n"; !strings.Contains(got, want) {
		t.Errorf("mutation: %q, want it to hold %q", got, want)
	}
	if stmts, txs := l.take(); !slices.Equal(stmts, []string{"insert in tx"}) || !slices.Equal(txs, []string{"commit"}) {
		t.Errorf("mutation: statements %q, transactions %q; want one insert in one committed transaction", stmts, txs)
	}
	got = sse(`subscription { count }`)
	if want := "event: next\ndata: {\"data\":{\"count\":1}}\n\n"; strings.Count(got, want) != 2 {
		t.Errorf("subscription: %q, want two events %q", got, want)
	}
	if stmts, txs := l.take(); !slices.Equal(stmts, []string{"select", "select"}) || len(txs) != 0 {
		t.Errorf("subscription: statements %q, transactions %q; want two selects outside a transaction", stmts, txs)
	}
}

// TestMutationResolversRunOneAtATime: below a mutation's root, gqlgen
// resolves an object's fields concurrently, but the transaction's DB is not
// safe for concurrent use, so Transactioner runs a mutation's resolvers one
// at a time. Pair.a and Pair.b each wait for the other to be in flight, and then
// count the people on the DB in their context. In a query they overlap; in a
// mutation they never do, and both run in its transaction. A query's wait
// has a bound only to fail a test that would hang, and a long one, so that
// load cannot make the two miss each other; a mutation's is short, since
// there the wait is expected to run out.
func TestMutationResolversRunOneAtATime(t *testing.T) {
	db, l := handlerDB(t)
	var inFlight, most atomic.Int64
	// arrived counts the resolvers that have started, and never goes down: a
	// resolver waits for the other to start, not to be in flight still, which
	// the other may no longer be by the time this one looks.
	var arrived atomic.Int64
	var wait atomic.Int64 // how long a resolver waits for the other, in ns
	wait.Store(int64(30 * time.Second))
	seen := func() {
		for n := inFlight.Load(); ; {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				return
			}
		}
	}
	side := func(ctx context.Context, args map[string]any) (any, error) {
		inFlight.Add(1)
		defer inFlight.Add(-1)
		seen()
		arrived.Add(1)
		for end := time.Now().Add(time.Duration(wait.Load())); arrived.Load() < 2 && time.Now().Before(end); {
			time.Sleep(time.Millisecond)
		}
		seen()
		return db.Query[person]().Count(ctx)
	}
	resolvers := handlerResolvers(db, nil)
	resolvers["pair"] = func(context.Context, map[string]any) (any, error) { return struct{}{}, nil }
	resolvers["Pair.a"] = side
	resolvers["Pair.b"] = side
	srv := serve(resolvers, gqlgenx.Transactioner{DB: db})

	if got, want := post(t, context.Background(), srv, `{ pair { a b } }`), `{"data":{"pair":{"a":0,"b":0}}}`; got != want {
		t.Errorf("query: %s, want %s", got, want)
	}
	if n := most.Swap(0); n != 2 {
		t.Errorf("query: at most %d resolvers in flight, want 2: the test schema does not run them concurrently", n)
	}
	l.take()

	wait.Store(int64(100 * time.Millisecond))
	arrived.Store(0)
	if got, want := post(t, context.Background(), srv, `mutation { pair { a b } }`), `{"data":{"pair":{"a":0,"b":0}}}`; got != want {
		t.Errorf("mutation: %s, want %s", got, want)
	}
	if n := most.Load(); n != 1 {
		t.Errorf("mutation: at most %d resolvers in flight, want 1", n)
	}
	if stmts, txs := l.take(); !slices.Equal(stmts, []string{"select in tx", "select in tx"}) || !slices.Equal(txs, []string{"commit"}) {
		t.Errorf("mutation: statements %q, transactions %q; want two selects in one committed transaction", stmts, txs)
	}
}

// TestTxOptionsApply: the options reach the mutation's transaction.
func TestTxOptionsApply(t *testing.T) {
	// post returns when the response is written, after the resolvers and the
	// hooks have returned, so these are read from the test's goroutine only
	// once the server's are done; the mutex states it for the race detector.
	var mu sync.Mutex
	var iso sql.IsolationLevel
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	db := velox.Open(velox.SQLite, sqlDB, velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
		mu.Lock()
		iso = tx.Isolation
		mu.Unlock()
		return next(ctx)
	}))
	var left time.Duration
	var has bool
	resolvers := map[string]resolver{
		"add": func(ctx context.Context, args map[string]any) (any, error) {
			d, ok := ctx.Deadline()
			mu.Lock()
			has, left = ok, time.Until(d)
			mu.Unlock()
			return args["id"], nil
		},
	}
	srv := serve(resolvers, gqlgenx.Transactioner{DB: db, Options: []velox.TxOption{velox.TxIsolation(sql.LevelSerializable)}})
	_ = post(t, context.Background(), srv, `mutation { add(id: 1) }`)
	mu.Lock()
	defer mu.Unlock()
	if iso != sql.LevelSerializable {
		t.Errorf("isolation %v, want serializable", iso)
	}
	srv = serve(resolvers, gqlgenx.Transactioner{DB: db, Options: []velox.TxOption{velox.TxTimeout(50 * time.Millisecond)}})
	mu.Unlock()
	_ = post(t, context.Background(), srv, `mutation { add(id: 1) }`)
	mu.Lock()
	if !has || left > 50*time.Millisecond {
		t.Errorf("deadline %v (set %v), want at most 50ms away", left, has)
	}
}
