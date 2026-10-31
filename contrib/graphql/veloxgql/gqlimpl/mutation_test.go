package gqlimpl_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// post is a hand-written tenant-scoped entity, as velox-gen would write it,
// for CheckExists: a key, a tenant and a title. Its Read policy hides the posts
// titled "hidden".
type post struct {
	ID     int64
	Tenant string
	Title  string
}

var posts = struct {
	ID    velox.NumberCol[post, int64]
	Title velox.StringCol[post, string]
}{
	ID:    velox.NumberCol[post, int64]{I: 0},
	Title: velox.StringCol[post, string]{I: 2},
}

func (*post) Policy() velox.Policy[post] {
	return velox.Policy[post]{
		Read: func(context.Context) (velox.Pred[post], error) { return posts.Title.NEQ("hidden"), nil },
	}
}

var postMeta velox.MetaOnce[post]

func (*post) VeloxMeta() *velox.Meta[post] { return postMeta.Load(postDesc) }

func postDesc() velox.Desc[post] {
	return velox.Desc[post]{
		Table:      "posts",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Tenant", Name: "tenant"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Tenant:     1,
		Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*post)(nil).Policy()) },
		Addr: func(x *post, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Tenant
			case 2:
				return &x.Title
			}
			return nil
		},
	}
}

// txRecord records how each transaction of a DB ended, and its statements:
// "insert" or "select", with " in tx" in a transaction.
type txRecord struct {
	mu    sync.Mutex
	txs   []string
	stmts []string
}

func (r *txRecord) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	s := st.Op.String()
	if st.InTx {
		s += " in tx"
	}
	r.mu.Lock()
	r.stmts = append(r.stmts, s)
	r.mu.Unlock()
	return next(ctx)
}

func (r *txRecord) txAround(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
	defer func() {
		r.mu.Lock()
		r.txs = append(r.txs, tx.Outcome.String())
		r.mu.Unlock()
	}()
	return next(ctx)
}

func (r *txRecord) take() (txs, stmts []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	txs, stmts = r.txs, r.stmts
	r.txs, r.stmts = nil, nil
	return txs, stmts
}

// mutationDB opens an empty in-memory database with the people and posts
// tables, with hooks that record into the returned record.
func mutationDB(t *testing.T) (*velox.DB, *txRecord) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	return mutationDBOn(t, sqlDB)
}

// savepointDB is mutationDB through a savepointConn, whose record it returns
// too.
func savepointDB(t *testing.T) (*velox.DB, *txRecord, *savepointRec) {
	t.Helper()
	probe, err := sql.Open("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	drv := probe.Driver()
	_ = probe.Close()
	sp := &savepointRec{}
	db, r := mutationDBOn(t, sql.OpenDB(savepointConnector{drv, sp}))
	return db, r, sp
}

// mutationDBOn is mutationDB on sqlDB, an in-memory database it closes when
// the test ends.
func mutationDBOn(t *testing.T, sqlDB *sql.DB) (*velox.DB, *txRecord) {
	t.Helper()
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	for _, ddl := range []string{
		`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, tenant TEXT NOT NULL, title TEXT NOT NULL)`,
	} {
		if _, err := sqlDB.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	r := &txRecord{}
	return velox.Open(velox.SQLite, sqlDB, velox.WithAround(r.around), velox.WithTxAround(r.txAround)), r
}

// savepointRec records the savepoint statements of a savepointConn: SAVEPOINT,
// RELEASE and ROLLBACK TO, in the order they were sent. While failRollback is
// set, a ROLLBACK TO fails with errRollbackTo and is not sent.
type savepointRec struct {
	mu           sync.Mutex
	stmts        []string
	failRollback bool
}

// take returns the statements recorded and forgets them.
func (r *savepointRec) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stmts
	r.stmts = nil
	return s
}

var errRollbackTo = errors.New("savepointConn: ROLLBACK TO refused")

// savepointConnector opens in-memory SQLite connections of drv as
// savepointConns that record into rec.
type savepointConnector struct {
	drv driver.Driver
	rec *savepointRec
}

func (c savepointConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.drv.Open("file::memory:")
	if err != nil {
		return nil, err
	}
	return savepointConn{conn.(savepointInner), c.rec}, nil
}

func (c savepointConnector) Driver() driver.Driver { return c.drv }

// savepointInner is what the wrapped SQLite connection implements, and
// savepointConn passes on.
type savepointInner interface {
	driver.Conn
	driver.ExecerContext
	driver.QueryerContext
	driver.ConnBeginTx
	driver.ConnPrepareContext
}

// savepointConn is a SQLite connection that records its savepoint statements
// in rec.
type savepointConn struct {
	savepointInner
	rec *savepointRec
}

func (c savepointConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if strings.HasPrefix(q, "SAVEPOINT") || strings.HasPrefix(q, "RELEASE") || strings.HasPrefix(q, "ROLLBACK TO") {
		c.rec.mu.Lock()
		c.rec.stmts = append(c.rec.stmts, q)
		fail := c.rec.failRollback && strings.HasPrefix(q, "ROLLBACK TO")
		c.rec.mu.Unlock()
		if fail {
			return nil, errRollbackTo
		}
	}
	return c.savepointInner.ExecContext(ctx, q, args)
}

// countPeople counts the people outside any transaction.
func countPeople(t *testing.T, db *velox.DB) int64 {
	t.Helper()
	n, err := db.Query[person]().Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

var errAfterWrite = errors.New("failed after the first write")

// insertThenFail is a mutation's body that writes and then fails.
func insertThenFail(ctx context.Context, db *velox.DB) error {
	if err := db.Insert(&person{ID: 1, Name: "p", Age: 1}).Exec(ctx); err != nil {
		return err
	}
	return errAfterWrite
}

// mutationSDL is the schema of the served mutation: one field, run.
const mutationSDL = `
type Query { x: Int }
type Mutation { run: Int }
`

// serveRun serves mutationSDL through ext; its run field calls run with the
// operation's context, and fails with run's error.
func serveRun(ext *veloxgql.Extension, run func(ctx context.Context) error) http.Handler {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: mutationSDL})
	srv := handler.New(&graphql.ExecutableSchemaMock{
		SchemaFunc: func() *ast.Schema { return schema },
		ComplexityFunc: func(context.Context, string, string, int, map[string]any) (int, bool) {
			return 0, false
		},
		ExecFunc: func(context.Context) graphql.ResponseHandler {
			done := false
			return func(ctx context.Context) *graphql.Response {
				if done {
					return nil
				}
				done = true
				if err := run(ctx); err != nil {
					return &graphql.Response{Data: []byte(`{"run":null}`), Errors: gqlerror.List{gqlerror.Wrap(err)}}
				}
				return &graphql.Response{Data: []byte(`{"run":1}`)}
			}
		},
	})
	srv.AddTransport(transport.POST{})
	srv.Use(ext)
	return srv
}

// postMutation sends `mutation { run }` to srv and returns the body.
func postMutation(t *testing.T, srv http.Handler) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": "mutation { run }"})
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w.Body.String()
}

// TestAtomic is Review Focus 4: a mutation's writes are kept or dropped
// together, in the extension's transaction when there is one and in one of
// Atomic's own otherwise, and Atomic never opens a transaction inside the
// extension's.
func TestAtomic(t *testing.T) {
	t.Run("outside a server", func(t *testing.T) {
		db, r := mutationDB(t)
		ctx := veloxgql.NewExtension(db).NewContext(context.Background())
		var got *velox.DB
		err := gqlimpl.Atomic(ctx, func(ctx context.Context, tx *velox.DB) error {
			got = tx
			return insertThenFail(ctx, tx)
		})
		if !errors.Is(err, errAfterWrite) {
			t.Fatalf("Atomic = %v, want fn's error", err)
		}
		if got == db {
			t.Error("fn got the extension's DB, not a transaction's")
		}
		if txs, stmts := r.take(); !slices.Equal(txs, []string{"rollback"}) || !slices.Equal(stmts, []string{"insert in tx"}) {
			t.Errorf("transactions %q, statements %q; want one insert in one rolled back transaction", txs, stmts)
		}
		if n := countPeople(t, db); n != 0 {
			t.Errorf("%d people kept, want 0", n)
		}

		// A fn that succeeds commits.
		if err := gqlimpl.Atomic(ctx, func(ctx context.Context, tx *velox.DB) error {
			return tx.Insert(&person{ID: 2, Name: "q", Age: 1}).Exec(ctx)
		}); err != nil {
			t.Fatal(err)
		}
		if n := countPeople(t, db); n != 1 {
			t.Errorf("%d people kept, want 1", n)
		}
	})

	t.Run("in the extension's transaction", func(t *testing.T) {
		db, r := mutationDB(t)
		var opDB, fnDB *velox.DB
		srv := serveRun(veloxgql.NewExtension(db), func(ctx context.Context) error {
			var err error
			if opDB, err = veloxgql.DBFromContext(ctx); err != nil {
				return err
			}
			return gqlimpl.Atomic(ctx, func(ctx context.Context, tx *velox.DB) error {
				fnDB = tx
				return tx.Insert(&person{ID: 1, Name: "p", Age: 1}).Exec(ctx)
			})
		})
		if got, want := postMutation(t, srv), `{"data":{"run":1}}`; got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
		if fnDB == nil || fnDB == opDB {
			t.Error("fn did not get a savepoint's DB: it got the operation's, or none")
		}
		if txs, stmts := r.take(); !slices.Equal(txs, []string{"commit"}) || !slices.Equal(stmts, []string{"insert in tx"}) {
			t.Errorf("transactions %q, statements %q; want one insert in the operation's one transaction", txs, stmts)
		}

		// fn's failure fails the operation, whose transaction drops the write.
		srv = serveRun(veloxgql.NewExtension(db), func(ctx context.Context) error {
			return gqlimpl.Atomic(ctx, func(ctx context.Context, tx *velox.DB) error {
				if err := tx.Insert(&person{ID: 2, Name: "q", Age: 1}).Exec(ctx); err != nil {
					return err
				}
				return errAfterWrite
			})
		})
		if got := postMutation(t, srv); !strings.Contains(got, `"data":null`) {
			t.Errorf("got %s, want data null", got)
		}
		if txs, _ := r.take(); !slices.Equal(txs, []string{"rollback"}) {
			t.Errorf("transactions %q, want one rolled back", txs)
		}
		if n := countPeople(t, db); n != 1 {
			t.Errorf("%d people kept, want 1", n)
		}
	})

	t.Run("left out by WithTxFilter", func(t *testing.T) {
		db, r := mutationDB(t)
		ext := veloxgql.NewExtension(db, veloxgql.WithTxFilter(func(context.Context, *ast.OperationDefinition) bool { return false }))
		var fnDB *velox.DB
		srv := serveRun(ext, func(ctx context.Context) error {
			return gqlimpl.Atomic(ctx, func(ctx context.Context, tx *velox.DB) error {
				fnDB = tx
				return insertThenFail(ctx, tx)
			})
		})
		if got := postMutation(t, srv); !strings.Contains(got, errAfterWrite.Error()) {
			t.Errorf("got %s, want fn's error", got)
		}
		if fnDB == nil || fnDB == db {
			t.Error("fn did not get a transaction's DB")
		}
		if txs, stmts := r.take(); !slices.Equal(txs, []string{"rollback"}) || !slices.Equal(stmts, []string{"insert in tx"}) {
			t.Errorf("transactions %q, statements %q; want one insert in Atomic's rolled back transaction", txs, stmts)
		}
		if n := countPeople(t, db); n != 0 {
			t.Errorf("%d people kept, want 0", n)
		}
	})

	t.Run("no DB", func(t *testing.T) {
		err := gqlimpl.Atomic(context.Background(), func(context.Context, *velox.DB) error {
			t.Error("fn ran with no DB")
			return nil
		})
		if !errors.Is(err, veloxgql.ErrNoDB) {
			t.Errorf("Atomic = %v, want ErrNoDB", err)
		}
	})
}

// TestAtomicSavepoint is Review Focus 1 and 7: in the extension's
// transaction, Atomic runs fn in a savepoint, so a resolver that swallows fn's
// error drops fn's writes only, and the operation commits its other writes;
// a savepoint that does not roll back fails the operation, which keeps
// nothing; fn's panic undoes the savepoint and carries on, and the operation
// keeps nothing. Outside a server no savepoint is made.
func TestAtomicSavepoint(t *testing.T) {
	t.Run("in the extension's transaction, fn succeeds", func(t *testing.T) {
		db, r, sp := savepointDB(t)
		var opDB, fnDB *velox.DB
		srv := serveRun(veloxgql.NewExtension(db), func(ctx context.Context) error {
			var err error
			if opDB, err = veloxgql.DBFromContext(ctx); err != nil {
				return err
			}
			return gqlimpl.Atomic(ctx, func(ctx context.Context, tx *velox.DB) error {
				fnDB = tx
				return tx.Insert(&person{ID: 1, Name: "p", Age: 1}).Exec(ctx)
			})
		})
		if got, want := postMutation(t, srv), `{"data":{"run":1}}`; got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
		if got, want := sp.take(), []string{"SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1"}; !slices.Equal(got, want) {
			t.Errorf("savepoint statements %q, want %q", got, want)
		}
		if txs, _ := r.take(); !slices.Equal(txs, []string{"commit"}) {
			t.Errorf("transactions %q, want one committed", txs)
		}
		if fnDB == nil || fnDB == opDB {
			t.Error("fn got the operation's DB, not a savepoint's")
		}
		if n := countPeople(t, db); n != 1 {
			t.Errorf("%d people kept, want 1", n)
		}
	})

	t.Run("two mutations in one operation", func(t *testing.T) {
		db, r, sp := savepointDB(t)
		srv := serveRun(veloxgql.NewExtension(db), func(ctx context.Context) error {
			for id := int64(1); id <= 2; id++ {
				if err := gqlimpl.Atomic(ctx, func(ctx context.Context, tx *velox.DB) error {
					return tx.Insert(&person{ID: id, Name: "p", Age: 1}).Exec(ctx)
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if got, want := postMutation(t, srv), `{"data":{"run":1}}`; got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
		want := []string{"SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1", "SAVEPOINT velox_2", "RELEASE SAVEPOINT velox_2"}
		if got := sp.take(); !slices.Equal(got, want) {
			t.Errorf("savepoint statements %q, want %q: one name per savepoint of the transaction", got, want)
		}
		if txs, _ := r.take(); !slices.Equal(txs, []string{"commit"}) {
			t.Errorf("transactions %q, want one committed", txs)
		}
	})

	// swallow is a resolver that swallows the error of a mutation that
	// writes person 1 and fails, then writes person 2 and returns nil; ignore
	// says whether it swallows the second write's error too.
	swallow := func(ignore bool) func(ctx context.Context) error {
		return func(ctx context.Context) error {
			_ = gqlimpl.Atomic(ctx, insertThenFail)
			db, err := veloxgql.DBFromContext(ctx)
			if err != nil {
				return err
			}
			if err := db.Insert(&person{ID: 2, Name: "q", Age: 1}).Exec(ctx); err != nil && !ignore {
				return err
			}
			return nil
		}
	}

	t.Run("swallowing", func(t *testing.T) {
		db, r, sp := savepointDB(t)
		srv := serveRun(veloxgql.NewExtension(db), swallow(false))
		if got, want := postMutation(t, srv), `{"data":{"run":1}}`; got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
		want := []string{"SAVEPOINT velox_1", "ROLLBACK TO SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1"}
		if got := sp.take(); !slices.Equal(got, want) {
			t.Errorf("savepoint statements %q, want %q", got, want)
		}
		if txs, _ := r.take(); !slices.Equal(txs, []string{"commit"}) {
			t.Errorf("transactions %q, want one committed", txs)
		}
		if n := countPeople(t, db); n != 1 {
			t.Errorf("%d people kept, want 1", n)
		}
		if ok, err := db.Query[person]().Where(people.ID.EQ(1)).Exists(context.Background()); err != nil || ok {
			t.Errorf("person 1 kept: %v, %v; want the swallowed mutation's write gone", ok, err)
		}
	})

	t.Run("swallowing, and ROLLBACK TO fails", func(t *testing.T) {
		db, r, sp := savepointDB(t)
		sp.failRollback = true
		srv := serveRun(veloxgql.NewExtension(db), swallow(true))
		if got := postMutation(t, srv); !strings.Contains(got, `"data":null`) || !strings.Contains(got, "a savepoint did not roll back") {
			t.Errorf("got %s, want data null and the broken transaction's error: it must not commit", got)
		}
		want := []string{"SAVEPOINT velox_1", "ROLLBACK TO SAVEPOINT velox_1"}
		if got := sp.take(); !slices.Equal(got, want) {
			t.Errorf("savepoint statements %q, want %q", got, want)
		}
		// The commit is refused and the transaction rolled back: TxInfo
		// reports a refused commit as TxError.
		if txs, _ := r.take(); !slices.Equal(txs, []string{"error"}) {
			t.Errorf("transactions %q, want one whose commit was refused", txs)
		}
		sp.failRollback = false
		if n := countPeople(t, db); n != 0 {
			t.Errorf("%d people kept, want 0", n)
		}
	})

	t.Run("fn panics", func(t *testing.T) {
		db, r, sp := savepointDB(t)
		var carried any
		srv := serveRun(veloxgql.NewExtension(db), func(ctx context.Context) error {
			defer func() {
				carried = recover()
				panic(carried)
			}()
			return gqlimpl.Atomic(ctx, func(ctx context.Context, tx *velox.DB) error {
				if err := tx.Insert(&person{ID: 1, Name: "p", Age: 1}).Exec(ctx); err != nil {
					return err
				}
				panic("boom")
			})
		})
		srv.(*handler.Server).SetRecoverFunc(func(context.Context, any) error { return gqlerror.Errorf("internal system error") })
		if got := postMutation(t, srv); !strings.Contains(got, `"data":null`) || !strings.Contains(got, "internal system error") {
			t.Errorf("got %s, want data null and the recovered panic's error", got)
		}
		if carried != "boom" {
			t.Errorf("Atomic's panic %v, want fn's panic carried on", carried)
		}
		want := []string{"SAVEPOINT velox_1", "ROLLBACK TO SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1"}
		if got := sp.take(); !slices.Equal(got, want) {
			t.Errorf("savepoint statements %q, want %q: the savepoint undone", got, want)
		}
		if txs, _ := r.take(); !slices.Equal(txs, []string{"rollback"}) {
			t.Errorf("transactions %q, want one rolled back", txs)
		}
		if n := countPeople(t, db); n != 0 {
			t.Errorf("%d people kept, want 0", n)
		}
	})

	t.Run("outside a server", func(t *testing.T) {
		db, r, sp := savepointDB(t)
		ctx := veloxgql.NewExtension(db).NewContext(context.Background())
		if err := gqlimpl.Atomic(ctx, insertThenFail); !errors.Is(err, errAfterWrite) {
			t.Fatalf("Atomic = %v, want fn's error", err)
		}
		if got := sp.take(); len(got) != 0 {
			t.Errorf("savepoint statements %q, want none", got)
		}
		if txs, _ := r.take(); !slices.Equal(txs, []string{"rollback"}) {
			t.Errorf("transactions %q, want one rolled back", txs)
		}
		if n := countPeople(t, db); n != 0 {
			t.Errorf("%d people kept, want 0", n)
		}
	})
}

// codeOf is the code err carries for veloxgql.ErrorPresenter, "" for none.
func codeOf(err error) string {
	var c interface{ Extensions() map[string]any }
	if !errors.As(err, &c) {
		return ""
	}
	s, _ := c.Extensions()["code"].(string)
	return s
}

func TestDecodeID(t *testing.T) {
	const at = "CreatePostInput.authorID"
	user := veloxgql.EncodeID("User", int64(7))
	if k, err := gqlimpl.DecodeID[int64](at, user, "User"); err != nil || k != 7 {
		t.Errorf("DecodeID = %v, %v; want 7", k, err)
	}
	type userID int64
	if k, err := gqlimpl.DecodeIDAs(at, user, "User", func(k int64) userID { return userID(k) }); err != nil || k != 7 {
		t.Errorf("DecodeIDAs = %v, %v; want 7", k, err)
	}
	for _, c := range []struct {
		name, id string
		want     error
	}{
		{"malformed", "!", veloxgql.ErrBadID},
		{"another type", veloxgql.EncodeID("Post", int64(7)), veloxgql.ErrWrongIDType},
	} {
		_, err := gqlimpl.DecodeID[int64](at, c.id, "User")
		_, errAs := gqlimpl.DecodeIDAs(at, c.id, "User", func(k int64) userID { return userID(k) })
		for _, err := range []error{err, errAs} {
			if !errors.Is(err, c.want) {
				t.Errorf("%s: %v, want %v", c.name, err, c.want)
			}
			if code := codeOf(err); code != veloxgql.CodeBadUserInput {
				t.Errorf("%s: code %q, want BAD_USER_INPUT", c.name, code)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "veloxgql: "+at+": ") {
				t.Errorf("%s: %q does not start with the input field", c.name, err)
			}
		}
	}
}

// encodeUsers returns the IDs of the Users 1 to n.
func encodeUsers(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = veloxgql.EncodeID("User", int64(i+1))
	}
	return ids
}

// TestDecodeIDsCap is Review Focus 5: an ID list is capped by MaxPage, as
// nodes(ids:) is, before any ID is decoded.
func TestDecodeIDsCap(t *testing.T) {
	const at = "UpdatePostInput.addTagIDs"
	ctx := context.Background()
	if k, err := gqlimpl.DecodeIDs[int64](ctx, at, encodeUsers(100), "User"); err != nil || len(k) != 100 {
		t.Errorf("100 IDs: %d keys, %v; want 100", len(k), err)
	}
	// 101 IDs, the last malformed: the cap is checked first.
	over := append(encodeUsers(100), "!")
	for name, err := range map[string]error{
		"DecodeIDs": func() error { _, err := gqlimpl.DecodeIDs[int64](ctx, at, over, "User"); return err }(),
		"DecodeIDsAs": func() error {
			_, err := gqlimpl.DecodeIDsAs(ctx, at, over, "User", func(k int64) int64 { return k })
			return err
		}(),
	} {
		if !errors.Is(err, veloxgql.ErrPageTooLarge) || errors.Is(err, veloxgql.ErrBadID) {
			t.Errorf("%s of 101 IDs: %v, want ErrPageTooLarge alone", name, err)
		}
		if code := codeOf(err); code != veloxgql.CodeBadUserInput {
			t.Errorf("%s of 101 IDs: code %q, want BAD_USER_INPUT", name, code)
		}
		if err != nil && !strings.HasPrefix(err.Error(), "veloxgql: "+at+": ") {
			t.Errorf("%s: %q does not start with the input field", name, err)
		}
	}

	db, _ := mutationDB(t)
	small := veloxgql.NewExtension(db, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 2})).NewContext(ctx)
	if _, err := gqlimpl.DecodeIDs[int64](small, at, encodeUsers(2), "User"); err != nil {
		t.Errorf("2 IDs under MaxPage 2: %v", err)
	}
	if _, err := gqlimpl.DecodeIDs[int64](small, at, encodeUsers(3), "User"); !errors.Is(err, veloxgql.ErrPageTooLarge) {
		t.Errorf("3 IDs under MaxPage 2: %v, want ErrPageTooLarge", err)
	}

	// A bad ID in a list names the list.
	_, err := gqlimpl.DecodeIDs[int64](ctx, at, []string{encodeUsers(1)[0], "!"}, "User")
	if !errors.Is(err, veloxgql.ErrBadID) || codeOf(err) != veloxgql.CodeBadUserInput || !strings.HasPrefix(err.Error(), "veloxgql: "+at) {
		t.Errorf("a malformed ID in the list: %v, want BAD_USER_INPUT ErrBadID naming %s", err, at)
	}
}

func TestDecodeIDsRepeats(t *testing.T) {
	a, b := veloxgql.EncodeID("User", int64(1)), veloxgql.EncodeID("User", int64(2))
	ctx := context.Background()
	k, err := gqlimpl.DecodeIDs[int64](ctx, "x", []string{a, b, a}, "User")
	if err != nil || !slices.Equal(k, []int64{1, 2}) {
		t.Errorf("DecodeIDs = %v, %v; want [1 2]", k, err)
	}
	type userID int64
	v, err := gqlimpl.DecodeIDsAs(ctx, "x", []string{b, a, b, b}, "User", func(k int64) userID { return userID(k) })
	if err != nil || !slices.Equal(v, []userID{2, 1}) {
		t.Errorf("DecodeIDsAs = %v, %v; want [2 1]", v, err)
	}
}

func TestExist(t *testing.T) {
	db, _ := mutationDB(t)
	ctx := veloxgql.NewExtension(db).NewContext(context.Background())
	ann, bob := velox.WithTenant(ctx, "ann"), velox.WithTenant(ctx, "bob")
	for _, p := range []struct {
		ctx context.Context
		p   post
	}{{ann, post{ID: 1, Title: "a"}}, {bob, post{ID: 2, Title: "b"}}, {ann, post{ID: 4, Title: "hidden"}}} {
		if err := db.Insert(&p.p).Exec(p.ctx); err != nil {
			t.Fatal(err)
		}
	}
	const at = "CreateUserInput.postIDs"
	if err := gqlimpl.CheckExists(ann, db.Query[post]().Where(posts.ID.In(1)), 1, at, "Post"); err != nil {
		t.Errorf("one ID of ann's post: %v", err)
	}
	for name, c := range map[string]struct {
		ctx context.Context
		ids []int64
	}{
		"missing":          {ann, []int64{1, 3}},
		"another tenant's": {ann, []int64{1, 2}},
		"hidden by policy": {ann, []int64{1, 4}},
	} {
		err := gqlimpl.CheckExists(c.ctx, db.Query[post]().Where(posts.ID.In(c.ids...)), len(c.ids), at, "Post")
		if codeOf(err) != veloxgql.CodeNotFound {
			t.Errorf("%s: %v, want NOT_FOUND", name, err)
		}
		if want := "veloxgql: " + at + ": 1 of the 2 IDs name no Post"; err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
	}
	// One ID that names no row reads as NotFound does.
	for name, id := range map[string]int64{"missing": 3, "another tenant's": 2, "hidden by policy": 4} {
		err := gqlimpl.CheckExists(ann, db.Query[post]().Where(posts.ID.In(id)), 1, at, "Post")
		if codeOf(err) != veloxgql.CodeNotFound {
			t.Errorf("one ID, %s: %v, want NOT_FOUND", name, err)
		}
		if want := "veloxgql: " + at + ": no Post has this ID"; err == nil || err.Error() != want {
			t.Errorf("one ID, %s: %v, want %q", name, err, want)
		}
	}
	// The query's own error is returned as it is.
	if err := gqlimpl.CheckExists(ctx, db.Query[post]().Where(posts.ID.In(1)), 1, at, "Post"); !errors.Is(err, velox.ErrNoTenant) {
		t.Errorf("no tenant: %v, want ErrNoTenant", err)
	}
}

func TestNotFound(t *testing.T) {
	err := gqlimpl.NotFound("updatePost", "Post")
	if codeOf(err) != veloxgql.CodeNotFound || err.Error() != "veloxgql: updatePost: no Post has this ID" {
		t.Errorf("NotFound = %v (code %q)", err, codeOf(err))
	}
}

// TestReadable: Readable returns the keys of the rows its query selects
// under the tenant and the Read policy, and runs no query for no keys.
func TestReadable(t *testing.T) {
	db, r := mutationDB(t)
	ctx := veloxgql.NewExtension(db).NewContext(context.Background())
	ann, bob := velox.WithTenant(ctx, "ann"), velox.WithTenant(ctx, "bob")
	for _, p := range []struct {
		ctx context.Context
		p   post
	}{{ann, post{ID: 1, Title: "a"}}, {bob, post{ID: 2, Title: "b"}}, {ann, post{ID: 3, Title: "c"}}, {ann, post{ID: 4, Title: "hidden"}}} {
		if err := db.Insert(&p.p).Exec(p.ctx); err != nil {
			t.Fatal(err)
		}
	}
	id := func(x *post) int64 { return x.ID }
	ids := []int64{1, 2, 3, 4, 5}
	got, err := gqlimpl.Readable(ann, db.Query[post]().Where(posts.ID.In(ids...)).Columns(posts.ID).OrderBy(posts.ID.Asc()), len(ids), id)
	if err != nil || !slices.Equal(got, []int64{1, 3}) {
		t.Errorf("Readable = %v, %v: want 1 and 3, without bob's and the hidden one", got, err)
	}
	r.take()
	if got, err := gqlimpl.Readable(ann, db.Query[post]().Where(posts.ID.In()), 0, id); err != nil || len(got) != 0 {
		t.Errorf("Readable of no keys = %v, %v", got, err)
	}
	if _, stmts := r.take(); len(stmts) != 0 {
		t.Errorf("Readable of no keys sent %v", stmts)
	}
	if _, err := gqlimpl.Readable(ctx, db.Query[post]().Where(posts.ID.In(1)), 1, id); !errors.Is(err, velox.ErrNoTenant) {
		t.Errorf("no tenant: %v, want ErrNoTenant", err)
	}
}

// TestUnlinkAll: UnlinkAll pages through its links MaxPage rows at a time,
// after the last row of each page, so that a link unlink leaves is not read
// again, and stops at a short page; unlink's error stops it.
func TestUnlinkAll(t *testing.T) {
	db, _ := mutationDB(t)
	ctx := veloxgql.NewExtension(db, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 2})).NewContext(context.Background())
	for i := int64(1); i <= 5; i++ {
		if err := db.Insert(&person{ID: i, Name: "p", Age: i}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	links := db.Query[person]().OrderBy(people.ID.Asc())
	age := func(x *person) int64 { return x.Age }
	var pages [][]int64
	// unlink deletes the odd ones, and leaves the even ones.
	unlink := func(ks []int64) error {
		pages = append(pages, ks)
		for _, k := range ks {
			if k%2 == 1 {
				if _, err := db.Delete[person]().Where(people.ID.EQ(k)).Exec(ctx); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := gqlimpl.UnlinkAll(ctx, links, age, unlink); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(pages) != "[[1 2] [3 4] [5]]" {
		t.Errorf("pages %v, want [[1 2] [3 4] [5]]", pages)
	}
	if n := countPeople(t, db); n != 2 {
		t.Errorf("%d people left, want 2", n)
	}
	// A full last page reads one more, empty, page.
	pages = nil
	if err := gqlimpl.UnlinkAll(ctx, links, age, unlink); err != nil || fmt.Sprint(pages) != "[[2 4]]" {
		t.Errorf("pages %v, %v: want [[2 4]]", pages, err)
	}
	errStop := errors.New("stop")
	if err := gqlimpl.UnlinkAll(ctx, links, age, func([]int64) error { return errStop }); !errors.Is(err, errStop) {
		t.Errorf("unlink's error: %v", err)
	}
}

// TestRepointed: an UPDATE that re-pointed fewer children than its IDs fails
// with BAD_USER_INPUT when linked counts a child another owner holds, and as
// CheckExists does otherwise; one that re-pointed them all runs no query. A
// count the driver did not report (-1) is an internal error.
func TestRepointed(t *testing.T) {
	db, _ := mutationDB(t)
	ctx := veloxgql.NewExtension(db).NewContext(context.Background())
	ann := velox.WithTenant(ctx, "ann")
	if err := db.Insert(&post{ID: 1, Title: "linked"}).Exec(ann); err != nil {
		t.Fatal(err)
	}
	const at = "CreateAccountInput.postIDs"
	linked, none := db.Query[post]().Where(posts.ID.In(1)), db.Query[post]().Where(posts.ID.In(2))
	// No tenant: the query would fail, so none runs.
	if err := gqlimpl.Repointed(ctx, 2, 2, linked, at, "Post", "Account"); err != nil {
		t.Errorf("all re-pointed: %v", err)
	}
	for _, c := range []struct {
		got        int64
		n          int
		q          velox.Query[post]
		typ, owner string
		code, want string
	}{
		{0, 2, linked, "Post", "Account", veloxgql.CodeBadUserInput, "veloxgql: " + at + ": a Post is already linked to another Account"},
		{0, 1, linked, "Article", "Writer", veloxgql.CodeBadUserInput, "veloxgql: " + at + ": an Article is already linked to another Writer"},
		{0, 1, linked, "User", "Writer", veloxgql.CodeBadUserInput, "veloxgql: " + at + ": a User is already linked to another Writer"},
		{0, 1, linked, "Umbrella", "Writer", veloxgql.CodeBadUserInput, "veloxgql: " + at + ": an Umbrella is already linked to another Writer"},
		{1, 2, none, "Post", "Account", veloxgql.CodeNotFound, "veloxgql: " + at + ": 1 of the 2 IDs name no Post"},
		{0, 1, none, "Post", "Account", veloxgql.CodeNotFound, "veloxgql: " + at + ": no Post has this ID"},
		// A count the driver did not report is not a short one: an internal
		// error, not NOT_FOUND.
		{-1, 1, none, "Post", "Account", "", "veloxgql: " + at + ": row count unknown"},
		{-1, 2, linked, "Post", "Account", "", "veloxgql: " + at + ": row count unknown"},
	} {
		err := gqlimpl.Repointed(ann, c.got, c.n, c.q, at, c.typ, c.owner)
		if err == nil || err.Error() != c.want || codeOf(err) != c.code {
			t.Errorf("Repointed(%d, %d) = %v (code %q), want %q, %s", c.got, c.n, err, codeOf(err), c.want, c.code)
		}
	}
	if err := gqlimpl.Repointed(ctx, 0, 1, linked, at, "Post", "Account"); !errors.Is(err, velox.ErrNoTenant) {
		t.Errorf("the count's error: %v, want ErrNoTenant", err)
	}
}

// TestStillLinked: a row q finds, though the caller's tenant scope hides it,
// fails with BAD_USER_INPUT naming only the two types; no row is not an error.
func TestStillLinked(t *testing.T) {
	db, _ := mutationDB(t)
	ctx := veloxgql.NewExtension(db).NewContext(context.Background())
	if err := db.Insert(&post{ID: 1, Title: "held"}).Exec(velox.WithTenant(ctx, "b")); err != nil {
		t.Fatal(err)
	}
	a := velox.WithTenant(ctx, "a")
	const at = "UpdateXInput.yID"
	held, none := db.Query[post]().Where(posts.ID.EQ(1)), db.Query[post]().Where(posts.ID.EQ(2))
	for _, c := range []struct{ typ, want string }{
		{"Y", "veloxgql: " + at + ": the X is already linked to a Y"},
		{"Account", "veloxgql: " + at + ": the X is already linked to an Account"},
	} {
		err := gqlimpl.StillLinked(a, held, at, c.typ, "X")
		if err == nil || err.Error() != c.want || codeOf(err) != veloxgql.CodeBadUserInput {
			t.Errorf("StillLinked(%s) = %v (code %q), want %q", c.typ, err, codeOf(err), c.want)
		}
	}
	if err := gqlimpl.StillLinked(a, none, at, "Y", "X"); err != nil {
		t.Errorf("no row: %v", err)
	}
}

// TestDisjoint: a key in both lists is BAD_USER_INPUT naming the input and
// both fields, and echoes no ID; lists with no key in common, empty ones
// included, are not an error.
func TestDisjoint(t *testing.T) {
	for _, c := range []struct{ add, remove []int64 }{{nil, nil}, {[]int64{1}, nil}, {nil, []int64{1}}, {[]int64{1, 2}, []int64{3}}} {
		if err := gqlimpl.Disjoint("UpdatePostInput", "addTagIDs", "removeTagIDs", c.add, c.remove); err != nil {
			t.Errorf("Disjoint(%v, %v) = %v, want nil", c.add, c.remove, err)
		}
	}
	err := gqlimpl.Disjoint("UpdatePostInput", "addTagIDs", "removeTagIDs", []int64{1, 2, 3}, []int64{4, 3})
	if codeOf(err) != veloxgql.CodeBadUserInput || err.Error() != "veloxgql: UpdatePostInput: an ID is in both addTagIDs and removeTagIDs" {
		t.Errorf("Disjoint = %v (code %q)", err, codeOf(err))
	}
}

func TestWriteError(t *testing.T) {
	name := func(goField string) string {
		if goField == "Title" {
			return "title"
		}
		return ""
	}
	ve := &velox.ValidationError{Entity: "gen.Post", Field: "Title", Err: errors.New("must not be empty")}
	err := gqlimpl.WriteError(ve, "CreatePostInput", "gen.Post", name)
	if codeOf(err) != veloxgql.CodeBadUserInput {
		t.Errorf("code %q, want BAD_USER_INPUT", codeOf(err))
	}
	if want := "veloxgql: CreatePostInput.title: invalid value: must not be empty"; err == nil || err.Error() != want {
		t.Errorf("WriteError = %v, want %q", err, want)
	}
	var got *velox.ValidationError
	if !errors.As(err, &got) || got != ve {
		t.Error("errors.As does not find the *velox.ValidationError")
	}

	// Wrapped directly, as by fmt.Errorf("...: %w", ve).
	if err := gqlimpl.WriteError(fmt.Errorf("insert: %w", ve), "CreatePostInput", "gen.Post", name); codeOf(err) != veloxgql.CodeBadUserInput {
		t.Errorf("a wrapped validation error: %v, want BAD_USER_INPUT", err)
	}
	// Joined with an internal error, it is returned as it is, so that the
	// presenter still logs the internal part.
	joined := errors.Join(errors.New("driver: connection reset"), ve)
	if got := gqlimpl.WriteError(joined, "CreatePostInput", "gen.Post", name); got != joined {
		t.Errorf("a validation error joined with an internal one: %v, want it unchanged", got)
	}

	for label, e := range map[string]error{
		"another entity": &velox.ValidationError{Entity: "gen.User", Field: "Title", Err: errors.New("must not be empty")},
		"unnamed field":  &velox.ValidationError{Entity: "gen.Post", Field: "Secret", Err: errors.New("must not be empty")},
		"constraint":     &velox.ConstraintError{Entity: "gen.Post", Kind: velox.ConstraintUnique, Constraint: "posts_title_key"},
	} {
		if got := gqlimpl.WriteError(e, "CreatePostInput", "gen.Post", name); got != e {
			t.Errorf("%s: %v, want the error unchanged", label, got)
		}
	}
	if gqlimpl.WriteError(nil, "CreatePostInput", "gen.Post", name) != nil {
		t.Error("WriteError(nil) is not nil")
	}
}

func TestFromJSON(t *testing.T) {
	type meta struct{ Tags []string }
	got, err := gqlimpl.FromJSON[meta]("CreatePostInput.meta", veloxgql.JSON(`{"Tags":["a"]}`))
	if err != nil || !slices.Equal(got.Tags, []string{"a"}) {
		t.Errorf("FromJSON = %v, %v", got, err)
	}
	_, err = gqlimpl.FromJSON[meta]("CreatePostInput.meta", veloxgql.JSON(`{"Tags":1}`))
	if codeOf(err) != veloxgql.CodeBadUserInput || !strings.HasPrefix(err.Error(), "veloxgql: CreatePostInput.meta: ") {
		t.Errorf("a JSON of another shape: %v, want BAD_USER_INPUT naming the field", err)
	}
	if strings.Contains(err.Error(), "gqlimpl_test") || strings.Contains(err.Error(), "meta.Tags") {
		t.Errorf("%q names a Go type", err)
	}
}
