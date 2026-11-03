package veloxgql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
	"github.com/syssam/velox/scope"
)

// codeOf is the code err carries for ErrorPresenter, "" for none.
func codeOf(err error) string {
	var c interface{ Extensions() map[string]any }
	if !errors.As(err, &c) {
		return ""
	}
	s, _ := c.Extensions()["code"].(string)
	return s
}

// personName is a where input of person with a name list, and and not, for
// the filter errors.
type personName struct {
	Not    *personName
	And    []*personName
	NameIn []string
}

func (w *personName) Pred(ctx context.Context) (velox.Pred[person], error) {
	p, err := w.PredicateGQL(ctx)
	return velox.Pred[person](p), err
}

func (w *personName) PredicateGQL(ctx context.Context) (velox.Predicate, error) {
	f := gqlimpl.NewFilter(ctx, "PersonWhereInput", velox.TableOf[person]())
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.List(f, "nameIn", w.NameIn, 1, velox.PredIn)
	return f.Pred()
}

// leaky is a where input whose nested input fails with a coded error that
// an uncoded error wraps, as an application's Pred might.
type leaky struct {
	Not  *leaky
	Leak bool
}

func (w *leaky) Pred(ctx context.Context) (velox.Pred[person], error) {
	p, err := w.PredicateGQL(ctx)
	return velox.Pred[person](p), err
}

func (w *leaky) PredicateGQL(ctx context.Context) (velox.Predicate, error) {
	if w != nil && w.Leak {
		return velox.Predicate{}, fmt.Errorf("sqlite: secret: %w", gqlimpl.InputErrorf("bad name"))
	}
	f := gqlimpl.NewFilter(ctx, "LeakyWhereInput", velox.TableOf[person]())
	if w != nil {
		gqlimpl.Not(f, "not", w.Not)
	}
	return f.Pred()
}

// TestErrorsAreCoded: every error veloxgql and gqlimpl return for a client's
// input is coded BAD_USER_INPUT, and errors.Is still finds its sentinel.
func TestErrorsAreCoded(t *testing.T) {
	db, _ := handlerDB(t)
	ctx := context.Background()
	key := &gqlimpl.Conn{Type: "Person", Key: []gqlimpl.Col{{Field: "id", Col: int(people.ID.I)}}, Columns: func([]string) []int { return nil }}
	conn := func(args gqlimpl.Args) error {
		_, err := gqlimpl.NewConnection(ctx, db.QueryTable(velox.TableOf[person]()), key, args, nil)
		return err
	}
	pred := func(w *personName) error {
		_, err := w.Pred(ctx)
		return err
	}
	deep := &personName{}
	for range veloxgql.DefaultLimits().MaxFilterDepth + 1 {
		deep = &personName{Not: deep}
	}
	wide := &personName{NameIn: make([]string, veloxgql.DefaultLimits().MaxFilterWidth+1)}
	large := &personName{}
	for range 30 {
		large.And = append(large.And, &personName{NameIn: make([]string, 40)})
	}
	one, two := 1, 2
	over := veloxgql.DefaultLimits().MaxPage + 1
	badKey := veloxgql.Cursor{Type: "Person", Key: "abc"}
	for _, c := range []struct {
		name string
		err  error
		is   error
	}{
		{"DecodeID malformed", func() error { _, err := veloxgql.DecodeID[int64]("!", "Person"); return err }(), veloxgql.ErrBadID},
		{"DecodeID of another type", func() error {
			_, err := veloxgql.DecodeID[int64](veloxgql.EncodeID("Post", 1), "Person")
			return err
		}(), veloxgql.ErrWrongIDType},
		{"IDType", func() error { _, err := veloxgql.IDType("!"); return err }(), veloxgql.ErrBadID},
		{"DecodeCursor", func() error { _, err := veloxgql.DecodeCursor("!"); return err }(), veloxgql.ErrBadCursor},
		{"Cursor.UnmarshalGQLContext", new(veloxgql.Cursor).UnmarshalGQLContext(context.Background(), 1), veloxgql.ErrBadCursor},
		{"cursor of another type", conn(gqlimpl.Args{After: &veloxgql.Cursor{Type: "Post", Key: int64(1)}}), veloxgql.ErrWrongIDType},
		{"cursor value", conn(gqlimpl.Args{After: &badKey}), veloxgql.ErrBadCursor},
		{"over MaxPage", conn(gqlimpl.Args{First: &over}), veloxgql.ErrPageTooLarge},
		{"first with last", conn(gqlimpl.Args{First: &one, Last: &two}), veloxgql.ErrBadPagination},
		{"filter too deep", pred(deep), veloxgql.ErrFilterTooDeep},
		{"filter too wide", pred(wide), veloxgql.ErrFilterTooWide},
		{"filter too large", pred(large), veloxgql.ErrFilterTooLarge},
		{"UnmarshalUUID", func() error { _, err := veloxgql.UnmarshalUUID("x"); return err }(), nil},
		{"UnmarshalTime", func() error { _, err := veloxgql.UnmarshalTime(1); return err }(), nil},
		{"UnmarshalUint", func() error { _, err := veloxgql.UnmarshalUint64("-1"); return err }(), nil},
		{"UnmarshalFloat32", func() error { _, err := veloxgql.UnmarshalFloat32("x"); return err }(), nil},
		{"Bytes", new(veloxgql.Bytes).UnmarshalGQL(1), nil},
		{"JSON", new(veloxgql.JSON).UnmarshalGQL(func() {}), nil},
		{"OrderDirection", new(veloxgql.OrderDirection).UnmarshalGQL("UP"), nil},
		{"InputErrorf", gqlimpl.InputErrorf("X must be %s: %w", "Y", veloxgql.ErrBadID), veloxgql.ErrBadID},
	} {
		switch {
		case c.err == nil:
			t.Errorf("%s: no error", c.name)
		case codeOf(c.err) != veloxgql.CodeBadUserInput:
			t.Errorf("%s: code %q, want BAD_USER_INPUT: %v", c.name, codeOf(c.err), c.err)
		case c.is != nil && !errors.Is(c.err, c.is):
			t.Errorf("%s: %v is not %v", c.name, c.err, c.is)
		case !strings.HasPrefix(c.err.Error(), "veloxgql: ") || strings.Count(c.err.Error(), "veloxgql: ") != 1:
			t.Errorf("%s: the text %q does not start veloxgql: once", c.name, c.err)
		}
	}
	if got := gqlimpl.InputErrorf("X must be %s: %w", "Y", veloxgql.ErrBadID).Error(); got != "veloxgql: X must be Y: malformed ID" {
		t.Errorf("InputErrorf's text is %q", got)
	}
	if err := veloxgql.ErrNoDB; codeOf(err) != "" {
		t.Errorf("ErrNoDB has the code %q", codeOf(err))
	}
	if _, err := veloxgql.DBFromContext(ctx); codeOf(err) != "" {
		t.Errorf("DBFromContext's error has the code %q", codeOf(err))
	}
}

// presented is the JSON of an error the presenter made.
type presented struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path"`
	Extensions map[string]any `json:"extensions"`
}

// appCodedError is an application's coded error, with the code APP, which may
// wrap an error the client is not shown.
type appCodedError struct {
	msg string
	err error
}

func (e *appCodedError) Error() string              { return e.msg }
func (e *appCodedError) Unwrap() error              { return e.err }
func (e *appCodedError) Extensions() map[string]any { return map[string]any{"code": "APP"} }

// logged records what an ErrorPresenter logs.
type logged struct {
	mu   sync.Mutex
	errs []error
}

func (l *logged) log(_ context.Context, err error) {
	l.mu.Lock()
	l.errs = append(l.errs, err)
	l.mu.Unlock()
}

func (l *logged) take() []error {
	l.mu.Lock()
	defer l.mu.Unlock()
	errs := l.errs
	l.errs = nil
	return errs
}

// TestErrorPresenter: a client sees a coded error's message and code, a
// core error's fixed message, and "internal error" for everything else,
// which is logged; never the text of a driver, an entity or a constraint.
func TestErrorPresenter(t *testing.T) {
	driverErr := errors.New(`sqlite: no such table: posts (users_email_key, tenant 7, /srv/app/db.go)`)
	_, codedBadID := veloxgql.DecodeID[int64]("!", "User")
	path := ast.Path{ast.PathName("posts"), ast.PathIndex(0), ast.PathName("author")}
	fieldCtx := graphql.WithFieldContext(context.Background(), &graphql.FieldContext{
		Field: graphql.CollectedField{Field: &ast.Field{Alias: "node"}},
	})
	app := &gqlerror.Error{Message: "app says no", Path: path, Extensions: map[string]any{"code": "X"}}
	_, int8Err := graphql.UnmarshalInt8(300)
	_, uint16Err := graphql.UnmarshalUint16(70000)
	_, uintSignErr := graphql.UnmarshalUint32(-1)
	// A PostgreSQL error's text holds its SQLSTATE, which velox.DriverCode
	// reads for traces only.
	pgErr := &pgconn.PgError{Severity: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "users_email_key"`}
	secret := []string{"sqlite", "posts (", "users_email_key", "tenant 7", "/srv/app", "gen.Post", "gen.User", "Email", "velox: ", "veloxgql: ", "SQLSTATE", "23505"}
	for _, c := range []struct {
		name   string
		err    error
		code   string
		msg    string
		path   string // the path's JSON
		logged bool
	}{
		{"wrapped ErrBadID", fmt.Errorf("resolver: %w", codedBadID), "BAD_USER_INPUT", "User ID: malformed ID: not canonical base64url without padding", `["node"]`, false},
		{"no scope", fmt.Errorf("velox: gen.Post: %w", scope.ErrNoScope), "UNAUTHENTICATED", "unauthenticated", `["node"]`, false},
		{"denied", fmt.Errorf("velox: gen.Post: %w", velox.ErrDenied), "FORBIDDEN", "forbidden", `["node"]`, false},
		{"scope mismatch", fmt.Errorf("velox: gen.Post: %w: a row's org_id is 2, the context's is 1", scope.ErrMismatch), "FORBIDDEN", "forbidden", `["node"]`, false},
		// A driver's typed nil, whose Unwrap panics, is passed over.
		{"denied, past a typed nil", errors.Join((*pgconn.ConnectError)(nil), fmt.Errorf("velox: gen.Post: %w", velox.ErrDenied)), "FORBIDDEN", "forbidden", `["node"]`, false},
		{"constraint, past a typed nil", errors.Join((*pgconn.ConnectError)(nil), &velox.ConstraintError{Entity: "gen.User", Kind: velox.ConstraintUnique, Err: driverErr}), "BAD_USER_INPUT", "unique constraint violated", `["node"]`, false},
		{"a typed nil alone", fmt.Errorf("x: %w", (*pgconn.ConnectError)(nil)), "INTERNAL_SERVER_ERROR", "internal error", `["node"]`, true},
		{"policy", &velox.PolicyError{Entity: "gen.Post", Op: velox.OpSelect, Err: errors.New("tenant 7 may not")}, "FORBIDDEN", "forbidden", `["node"]`, false},
		{"validation", &velox.ValidationError{Entity: "gen.User", Field: "Email", Err: errors.New("must hold an @")}, "BAD_USER_INPUT", "invalid value: must hold an @", `["node"]`, false},
		{"constraint", &velox.ConstraintError{Entity: "gen.User", Kind: velox.ConstraintUnique, Constraint: "users_email_key", Err: driverErr}, "BAD_USER_INPUT", "unique constraint violated", `["node"]`, false},
		{"constraint with a SQLSTATE", &velox.ConstraintError{Entity: "gen.User", Kind: velox.ConstraintUnique, Constraint: "users_email_key", Err: pgErr}, "BAD_USER_INPUT", "unique constraint violated", `["node"]`, false},
		{"driver", driverErr, "INTERNAL_SERVER_ERROR", "internal error", `["node"]`, true},
		{"driver with a SQLSTATE", pgErr, "INTERNAL_SERVER_ERROR", "internal error", `["node"]`, true},
		{"no DB", fmt.Errorf("veloxgql: %w", veloxgql.ErrNoDB), "INTERNAL_SERVER_ERROR", "internal error", `["node"]`, true},
		{"too many rows", fmt.Errorf("veloxgql: Post.likes: %w", velox.ErrTooManyRows), "INTERNAL_SERVER_ERROR", "internal error", `["node"]`, true},
		{"gqlerror over a driver error", &gqlerror.Error{Message: driverErr.Error(), Path: path, Err: driverErr}, "INTERNAL_SERVER_ERROR", "internal error", `["posts",0,"author"]`, true},
		{"gqlerror over a coded error", gqlerror.WrapPath(path, codedBadID), "BAD_USER_INPUT", "User ID: malformed ID: not canonical base64url without padding", `["posts",0,"author"]`, false},
		{"join", errors.Join(codedBadID, driverErr), "BAD_USER_INPUT", "User ID: malformed ID: not canonical base64url without padding", `["node"]`, true},
		{"join, driver first", errors.Join(driverErr, codedBadID), "BAD_USER_INPUT", "User ID: malformed ID: not canonical base64url without padding", `["node"]`, true},
		{"an application's coded error over a driver error", gqlerror.WrapPath(path, &appCodedError{msg: "app says no", err: driverErr}), "APP", "app says no", `["posts",0,"author"]`, true},
		{"a gqlerror with an application code over a driver error", &gqlerror.Error{Message: "app says no", Path: path, Extensions: map[string]any{"code": "APP"}, Err: driverErr}, "INTERNAL_SERVER_ERROR", "internal error", `["posts",0,"author"]`, true},
		{"an application's coded error alone", &appCodedError{msg: "app says no"}, "APP", "app says no", `["node"]`, false},
		{"coded over two sentinels", gqlerror.WrapPath(path, gqlimpl.InputErrorf("User cursor: %w: the key is an %w", veloxgql.ErrBadCursor, veloxgql.ErrWrongIDType)), "BAD_USER_INPUT", "User cursor: malformed cursor: the key is an ID of another type", `["posts",0,"author"]`, false},
		{"int8 overflow", gqlerror.WrapPath(path, int8Err), "BAD_USER_INPUT", "300 overflows signed 8-bit integer", `["posts",0,"author"]`, false},
		{"uint16 overflow", uint16Err, "BAD_USER_INPUT", "70000 overflows unsigned 16-bit integer", `["node"]`, false},
		{"negative uint32", fmt.Errorf("input: %w", uintSignErr), "BAD_USER_INPUT", "-1 is an invalid unsigned integer: includes sign", `["node"]`, false},
	} {
		l := &logged{}
		got := veloxgql.ErrorPresenter(l.log)(fieldCtx, c.err)
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var p presented
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		pathJSON, _ := json.Marshal(p.Path)
		if p.Message != c.msg || p.Extensions["code"] != c.code || string(pathJSON) != c.path || len(p.Extensions) != 1 {
			t.Errorf("%s: got %s, want the message %q, the code %s and the path %s", c.name, b, c.msg, c.code, c.path)
		}
		for _, s := range secret {
			if strings.Contains(string(b), s) {
				t.Errorf("%s: the client sees %q: %s", c.name, s, b)
			}
		}
		errs := l.take()
		switch {
		case c.logged && (len(errs) != 1 || !errors.Is(errs[0], c.err) && !errors.Is(c.err, errs[0])):
			t.Errorf("%s: logged %v, want the error once", c.name, errs)
		case !c.logged && len(errs) != 0:
			t.Errorf("%s: logged %v, want nothing", c.name, errs)
		}
	}

	l := &logged{}
	present := veloxgql.ErrorPresenter(l.log)
	if got := present(fieldCtx, nil); got != nil {
		t.Errorf("nil: got %v", got)
	}
	if got := present(fieldCtx, app); got != app || got.Message != "app says no" || got.Extensions["code"] != "X" || len(got.Path) != 3 {
		t.Errorf("an application's gqlerror: got %#v, want it unchanged", got)
	}
	if got := present(fieldCtx, fmt.Errorf("wrapped: %w", app)); got != app {
		t.Errorf("a wrapped application gqlerror: got %#v, want it unchanged", got)
	}
	if errs := l.take(); len(errs) != 0 {
		t.Errorf("logged %v, want nothing", errs)
	}
	// An application's gqlerror joined with a driver's error is shown as it
	// is, and the driver's error is logged.
	joined := errors.Join(driverErr, app)
	if got := present(fieldCtx, joined); got != app {
		t.Errorf("an application gqlerror joined with a driver error: got %#v, want it unchanged", got)
	}
	if errs := l.take(); len(errs) != 1 || !errors.Is(errs[0], driverErr) {
		t.Errorf("logged %v, want the joined error once", errs)
	}
	// The input error presented keeps its path through the filter's fields.
	_, err := (&personName{Not: &personName{NameIn: make([]string, veloxgql.DefaultLimits().MaxFilterWidth+1)}}).Pred(context.Background())
	if got := present(fieldCtx, err); got.Message != "PersonWhereInput.not: PersonWhereInput.nameIn: filter list is too long: it has 101 entries, and the most is 100" {
		t.Errorf("a filter's error: got %q", got.Message)
	}

	// The path of a filter's fields is not shown over an uncoded error, whose
	// text is not known to be safe: the coded error's own text is.
	_, err = (&leaky{Not: &leaky{Leak: true}}).Pred(context.Background())
	if got := present(fieldCtx, err); got.Message != "bad name" || got.Extensions["code"] != "BAD_USER_INPUT" {
		t.Errorf("a filter's error over an uncoded one: got %q %v", got.Message, got.Extensions)
	}

	// A nil log logs with slog, and still presents.
	if got := veloxgql.ErrorPresenter(nil)(fieldCtx, driverErr); got.Message != "internal error" {
		t.Errorf("a nil log: got %q", got.Message)
	}
}

// TestNotFoundPresented: a mutation's NOT_FOUND reaches the client with its
// code and its text, and is not logged.
func TestNotFoundPresented(t *testing.T) {
	l := &logged{}
	got := veloxgql.ErrorPresenter(l.log)(context.Background(), fmt.Errorf("resolver: %w", gqlimpl.NotFound("updatePost", "Post")))
	if got.Message != "updatePost: no Post has this ID" || got.Extensions["code"] != veloxgql.CodeNotFound {
		t.Errorf("got %q %v, want NOT_FOUND and the message", got.Message, got.Extensions)
	}
	if errs := l.take(); len(errs) != 0 {
		t.Errorf("logged %v, want nothing", errs)
	}
}

// TestTooCostlyPresented: the extension's cost error reaches the client with
// its code COST_LIMIT_EXCEEDED and its text, wraps ErrTooCostly, and is not
// logged.
func TestTooCostlyPresented(t *testing.T) {
	l := &logged{}
	ext := veloxgql.NewExtension(&velox.DB{}, veloxgql.WithLimits(veloxgql.Limits{MaxCost: 2}))
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: costSDL})
	if err := ext.Validate(&graphql.ExecutableSchemaMock{SchemaFunc: func() *ast.Schema { return schema }}); err != nil {
		t.Fatal(err)
	}
	doc, errs := gqlparser.LoadQueryWithRules(schema, `{ users(first: 2) { totalCount } }`, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	gerr := ext.MutateOperationContext(context.Background(), &graphql.OperationContext{Operation: doc.Operations[0], Doc: doc})
	if gerr == nil {
		t.Fatal("no error for cost 202 over MaxCost 2")
	}
	if !errors.Is(gerr, veloxgql.ErrTooCostly) || codeOf(gerr) != veloxgql.CodeTooCostly {
		t.Errorf("error %v: is ErrTooCostly %v, code %q", gerr, errors.Is(gerr, veloxgql.ErrTooCostly), codeOf(gerr))
	}
	got := veloxgql.ErrorPresenter(l.log)(context.Background(), gerr)
	if want := "request is too costly: it costs 202, and the most is 2"; got.Message != want || got.Extensions["code"] != "COST_LIMIT_EXCEEDED" {
		t.Errorf("got %q %v, want %q and COST_LIMIT_EXCEEDED", got.Message, got.Extensions, want)
	}
	if errs := l.take(); len(errs) != 0 {
		t.Errorf("logged %v, want nothing", errs)
	}
}

// TestCommitErrorIsInternal: the commit's error the extension adds goes
// through the server's presenter, which shows "internal error". The test's
// commit fails because the request is canceled, which is not logged.
func TestCommitErrorIsInternal(t *testing.T) {
	db, _ := handlerDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := handlerServer(t, db, &cancel)
	l := &logged{}
	srv.SetErrorPresenter(veloxgql.ErrorPresenter(l.log))
	got := post(t, ctx, srv, `mutation { cancel(id: 1) }`)
	if want := `{"errors":[{"message":"internal error","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// The request's context is canceled, so the commit's error is the
	// cancellation's, and not logged.
	if errs := l.take(); len(errs) != 0 {
		t.Errorf("logged %v, want nothing for a canceled request", errs)
	}
}

// TestCommitErrorOnLiveRequest: a transaction that fails to commit on a
// request still alive (here a TxAround hook fails it after the commit, as a
// lost connection would) is an internal error, and logged once.
func TestCommitErrorOnLiveRequest(t *testing.T) {
	commitErr := errors.New("sqlite: disk I/O error")
	db, _ := handlerDB(t, velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
		if err := next(ctx); err != nil {
			return err
		}
		return commitErr
	}))
	srv := handlerServer(t, db, nil)
	l := &logged{}
	srv.SetErrorPresenter(veloxgql.ErrorPresenter(l.log))
	got := post(t, context.Background(), srv, `mutation { add(id: 1) }`)
	if want := `{"errors":[{"message":"internal error","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if errs := l.take(); len(errs) != 1 || !errors.Is(errs[0], commitErr) {
		t.Errorf("logged %v, want the commit's error once", errs)
	}
}

// TestErrorPresenterCanceled: an error that is only the cancellation of
// the request, the client gone, is not logged as an internal error: log
// does not get it, and a nil log records it at level debug. Anything else on
// a canceled request (a driver's error, a panic, either joined with the
// cancellation) is logged, as a timeout is. The client sees what it would
// otherwise.
func TestErrorPresenterCanceled(t *testing.T) {
	driverErr := errors.New("sqlite: interrupted")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	canceled = graphql.WithFieldContext(canceled, &graphql.FieldContext{Field: graphql.CollectedField{Field: &ast.Field{Alias: "posts"}}})
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	// The recover function's error for a panic, as an application writes
	// one (serve's returns a gqlerror without Err, which is not logged).
	panicErr := fmt.Errorf("panic: %v", "boom")
	coded := gqlimpl.InputErrorf("User cursor: %w", veloxgql.ErrBadCursor)
	l := &logged{}
	for _, c := range []struct {
		name   string
		err    error
		logged bool
	}{
		{"bare cancellation", context.Canceled, false},
		{"wrapped cancellation", fmt.Errorf("velox: transaction: %w", context.Canceled), false},
		{"wrapped in a gqlerror", gqlerror.WrapPath(ast.Path{ast.PathName("posts")}, fmt.Errorf("sqlite: %w", context.Canceled)), false},
		{"cancellation joined with a coded error", errors.Join(coded, context.Canceled), false},
		{"driver error", driverErr, true},
		{"panic", panicErr, true},
		{"cancellation joined with a driver error", errors.Join(context.Canceled, driverErr), true},
		{"cancellation joined with a panic", errors.Join(fmt.Errorf("velox: %w", context.Canceled), panicErr), true},
	} {
		got := veloxgql.ErrorPresenter(l.log)(canceled, c.err)
		if code := got.Extensions["code"]; code != "INTERNAL_SERVER_ERROR" && code != "BAD_USER_INPUT" {
			t.Errorf("%s: presented %v", c.name, got)
		}
		errs := l.take()
		switch {
		case c.logged && (len(errs) != 1 || errs[0] != c.err):
			t.Errorf("%s: logged %v, want the error once", c.name, errs)
		case !c.logged && len(errs) != 0:
			t.Errorf("%s: logged %v, want nothing: it is only the cancellation", c.name, errs)
		}
	}
	// On a live request a cancellation of the application's own is logged.
	if got := veloxgql.ErrorPresenter(l.log)(context.Background(), context.Canceled); got.Message != "internal error" || len(l.take()) != 1 {
		t.Errorf("a live request's cancellation: presented %q, want it logged", got.Message)
	}
	if got := veloxgql.ErrorPresenter(l.log)(expired, context.DeadlineExceeded); got.Message != "internal error" {
		t.Errorf("timed out: presented %q", got.Message)
	}
	if errs := l.take(); len(errs) != 1 {
		t.Errorf("timed out: logged %v, want the error", errs)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	if got := veloxgql.ErrorPresenter(nil)(canceled, context.Canceled); got.Message != "internal error" {
		t.Errorf("a nil log: presented %q", got.Message)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("the log is not one JSON record: %v\n%s", err, buf.Bytes())
	}
	if rec["level"] != "DEBUG" || rec["msg"] != "veloxgql: request canceled" || rec["path"] != "posts" || rec["err"] != "context canceled" {
		t.Errorf("logged %v, want a debug record", rec)
	}
	// A driver's error on a canceled request is logged at level error.
	buf.Reset()
	if got := veloxgql.ErrorPresenter(nil)(canceled, errors.Join(context.Canceled, driverErr)); got.Message != "internal error" {
		t.Errorf("a driver error on a canceled request: presented %q", got.Message)
	}
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil || rec["level"] != "ERROR" || rec["msg"] != "veloxgql: internal error" {
		t.Errorf("a driver error on a canceled request: logged %v (%v), want an error record", rec, err)
	}
}

// TestErrorPresenterOnCanceled: OnCanceled sends an error that is only the
// cancellation of the request to its function, in place of log's default
// (nothing, or a debug record when log is nil); nil drops it. Anything else
// on a canceled request still goes to log.
func TestErrorPresenterOnCanceled(t *testing.T) {
	driverErr := errors.New("sqlite: interrupted")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	canceled = graphql.WithFieldContext(canceled, &graphql.FieldContext{Field: graphql.CollectedField{Field: &ast.Field{Alias: "posts"}}})

	l, c := &logged{}, &logged{}
	present := veloxgql.ErrorPresenter(l.log, veloxgql.OnCanceled(c.log))
	got := present(canceled, context.Canceled)
	if got.Message != "internal error" || got.Extensions["code"] != "INTERNAL_SERVER_ERROR" {
		t.Errorf("a bare cancellation: presented %v", got)
	}
	if errs := c.take(); len(errs) != 1 || errs[0] != context.Canceled {
		t.Errorf("OnCanceled got %v, want the cancellation once", errs)
	}
	if errs := l.take(); len(errs) != 0 {
		t.Errorf("log got %v, want nothing: it is only the cancellation", errs)
	}
	joined := errors.Join(context.Canceled, driverErr)
	_ = present(canceled, joined)
	if errs := l.take(); len(errs) != 1 || errs[0] != joined {
		t.Errorf("log got %v, want the joined error once", errs)
	}
	if errs := c.take(); len(errs) != 0 {
		t.Errorf("OnCanceled got %v, want nothing: the error is more than the cancellation", errs)
	}
	// On a live request a cancellation is the application's own: log's.
	_ = present(context.Background(), context.Canceled)
	if len(l.take()) != 1 || len(c.take()) != 0 {
		t.Error("a live request's cancellation must go to log")
	}
	// The last OnCanceled wins.
	first := &logged{}
	_ = veloxgql.ErrorPresenter(l.log, veloxgql.OnCanceled(first.log), veloxgql.OnCanceled(c.log))(canceled, context.Canceled)
	if len(first.take()) != 0 || len(c.take()) != 1 || len(l.take()) != 0 {
		t.Error("two OnCanceled: the cancellation must go to the last one's function only")
	}

	// With a nil log, OnCanceled replaces the debug record; OnCanceled(nil)
	// drops the error without one.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	if got := veloxgql.ErrorPresenter(nil, veloxgql.OnCanceled(nil))(canceled, context.Canceled); got.Message != "internal error" {
		t.Errorf("OnCanceled(nil): presented %q", got.Message)
	}
	if buf.Len() != 0 {
		t.Errorf("OnCanceled(nil) logged %s, want nothing", buf.Bytes())
	}
	_ = veloxgql.ErrorPresenter(nil, veloxgql.OnCanceled(c.log))(canceled, context.Canceled)
	if buf.Len() != 0 || len(c.take()) != 1 {
		t.Errorf("a nil log with OnCanceled: slog got %s, want nothing, and OnCanceled the error", buf.Bytes())
	}
	// A driver's error is still logged at level error.
	_ = veloxgql.ErrorPresenter(nil, veloxgql.OnCanceled(nil))(canceled, driverErr)
	if !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Errorf("a driver's error on a canceled request: logged %s", buf.Bytes())
	}

	// The zero option is not valid.
	defer func() {
		if r := recover(); r != "veloxgql.ErrorPresenter: zero PresenterOption" {
			t.Errorf("zero PresenterOption: recovered %v", r)
		}
	}()
	veloxgql.ErrorPresenter(nil, veloxgql.PresenterOption{})
	t.Error("zero PresenterOption: no panic")
}

// TestErrorPresenterSlog: with no log, an internal error is logged with
// slog, with the field's path, the operation's name and the error.
func TestErrorPresenterSlog(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	driverErr := errors.New("sqlite: no such table: posts")
	ctx := graphql.WithOperationContext(context.Background(), &graphql.OperationContext{OperationName: "Feed"})
	ctx = graphql.WithFieldContext(ctx, &graphql.FieldContext{Field: graphql.CollectedField{Field: &ast.Field{Alias: "posts"}}})
	present := veloxgql.ErrorPresenter(nil)
	for _, c := range []struct {
		name string
		ctx  context.Context
		err  error
		want map[string]any
	}{
		{"the field's path", ctx, driverErr, map[string]any{"path": "posts", "operation": "Feed"}},
		{"a gqlerror's path", ctx, gqlerror.WrapPath(ast.Path{ast.PathName("users"), ast.PathIndex(2), ast.PathName("posts")}, driverErr),
			map[string]any{"path": "users[2].posts", "operation": "Feed"}},
		{"no operation", context.Background(), driverErr, map[string]any{"path": ""}},
	} {
		buf.Reset()
		if got := present(c.ctx, c.err); got.Message != "internal error" {
			t.Errorf("%s: presented %q", c.name, got.Message)
		}
		var rec map[string]any
		if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
			t.Fatalf("%s: the log is not one JSON record: %v\n%s", c.name, err, buf.Bytes())
		}
		if rec["level"] != "ERROR" || rec["msg"] != "veloxgql: internal error" || !strings.Contains(fmt.Sprint(rec["err"]), "sqlite: no such table: posts") {
			t.Errorf("%s: logged %v", c.name, rec)
		}
		for k, v := range c.want {
			if rec[k] != v {
				t.Errorf("%s: logged %s=%v, want %v", c.name, k, rec[k], v)
			}
		}
		if _, ok := c.want["operation"]; !ok && rec["operation"] != nil {
			t.Errorf("%s: logged operation=%v, want none", c.name, rec["operation"])
		}
	}
}
