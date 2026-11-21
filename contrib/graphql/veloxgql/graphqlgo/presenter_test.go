package graphqlgo_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/syssam/graphql-go"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/contrib/graphql/veloxgql/graphqlgo"
)

// TestErrorPresenter: velox's errors are shown as veloxgql.Present gives
// them, an internal error is logged and shown as "internal error",
// graphql-go's and the application's GraphQL errors pass as graphql-go's
// default presenter shows them, and the cancellation of the request is not
// logged.
func TestErrorPresenter(t *testing.T) {
	var logged []error
	p := graphqlgo.ErrorPresenter(func(_ context.Context, err error) { logged = append(logged, err) })
	driver := errors.New("pq: relation \"secret\" does not exist")
	for _, c := range []struct {
		name      string
		err       error
		code, msg string
		logged    bool
	}{
		{"coded", gqlgraph.InputErrorf("first must not be negative"), "BAD_USER_INPUT", "first must not be negative", false},
		{"denied", fmt.Errorf("read: %w", velox.ErrDenied), "FORBIDDEN", "forbidden", false},
		{"internal", fmt.Errorf("select: %w", driver), "INTERNAL_SERVER_ERROR", "internal error", true},
		{"engine's", graphql.Errorf("Cannot query field \"x\"").WithCode(graphql.CodeValidationFailed), graphql.CodeValidationFailed, "Cannot query field \"x\"", false},
		{"engine's wrapping", &graphql.Error{Message: "operation exceeded its timeout", Err: driver, Extensions: map[string]any{"code": graphql.CodeOperationTimeout}}, graphql.CodeOperationTimeout, "operation exceeded its timeout", false},
		{"gqlparser's", gqlerror.Errorf("Syntax Error"), "", "Syntax Error", false},
	} {
		logged = nil
		out := p(context.Background(), c.err)
		code, _ := out.Extensions["code"].(string)
		if out.Message != c.msg || code != c.code {
			t.Errorf("%s: %q %q, want %q %q", c.name, out.Message, code, c.msg, c.code)
		}
		if got := len(logged) == 1; got != c.logged {
			t.Errorf("%s: logged %v, want %v", c.name, logged, c.logged)
		}
	}
	if p(context.Background(), nil) != nil {
		t.Error("a nil error: not nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logged = nil
	if out := p(ctx, fmt.Errorf("select: %w", context.Canceled)); out.Message != "internal error" || len(logged) != 0 {
		t.Errorf("a canceled request: %q, logged %v", out.Message, logged)
	}
	// The default log, slog's, takes both.
	quiet(t)
	d := graphqlgo.ErrorPresenter(nil)
	if out := d(context.Background(), driver); out.Message != "internal error" {
		t.Errorf("the default log: %q", out.Message)
	}
	if out := d(ctx, context.Canceled); out.Message != "internal error" {
		t.Errorf("the default log, canceled: %q", out.Message)
	}
}

// scalarSDL declares velox's scalars, and fields that echo them.
const scalarSDL = `
scalar Time
scalar UUID
scalar Bytes
scalar Uint64
type Query { echo(t: Time!, u: UUID!, b: Bytes!, n: Uint64!): Vals! none: Vals! }
type Vals { t: Time! u: UUID b: Bytes! n: Uint64! }
`

// vals holds the Go types velox entities hold.
type vals struct {
	T time.Time
	U *uuid.UUID
	B []byte
	N uint64
}

// TestScalars: Scalars binds Time, UUID, Bytes and Uint64 to time.Time,
// uuid.UUID (and a pointer to one), []byte and uint64, written and read as
// veloxgql's scalar types do; a value veloxgql refuses is the client's
// error.
func TestScalars(t *testing.T) {
	type echoArgs struct {
		T time.Time
		U uuid.UUID
		B []byte
		N uint64
	}
	s, err := graphql.NewSchema(graphql.SDL(scalarSDL), graphqlgo.Scalars(),
		graphql.Query(
			graphql.ResolveArgs("echo", func(_ context.Context, _ graphql.Root, a echoArgs) (*vals, error) {
				return &vals{a.T, &a.U, a.B, a.N}, nil
			}),
			graphql.Resolve("none", func(context.Context, graphql.Root) (*vals, error) { return &vals{}, nil }),
		),
		graphql.Object[vals]("Vals",
			graphql.Field("t", func(v *vals) time.Time { return v.T }),
			graphql.Field("u", func(v *vals) *uuid.UUID { return v.U }),
			graphql.Field("b", func(v *vals) []byte { return v.B }),
			graphql.Field("n", func(v *vals) uint64 { return v.N }),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	srv := executorServer(newExecutor(s, graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(nil))))
	ctx := context.Background()
	const q = `query($t: Time!, $u: UUID!, $b: Bytes!, $n: Uint64!) { echo(t: $t, u: $u, b: $b, n: $n) { t u b n } }`
	vars := map[string]any{"t": "2026-11-22T10:00:00.5+08:00", "u": "0F8FAD5B-D9CB-469F-A165-70867728950E", "b": "aGk=", "n": "18446744"}
	if out, want := srv(t, ctx, q, vars), `{"data":{"echo":{"t":"2026-11-22T10:00:00.5+08:00","u":"0f8fad5b-d9cb-469f-a165-70867728950e","b":"aGk=","n":"18446744"}}}`; out != want {
		t.Errorf("echo:\n got %s\nwant %s", out, want)
	}
	if out, want := srv(t, ctx, `{ none { t u b n } }`, nil), `{"data":{"none":{"t":"0001-01-01T00:00:00Z","u":null,"b":"","n":"0"}}}`; out != want {
		t.Errorf("zero values:\n got %s\nwant %s", out, want)
	}
	for name, v := range map[string]map[string]any{
		"a UUID":     {"u": "nope"},
		"Bytes":      {"b": "%%"},
		"a Uint64":   {"n": 7},
		"over int64": {"n": "18446744073709551615"},
	} {
		in := map[string]any{"t": vars["t"], "u": vars["u"], "b": vars["b"], "n": vars["n"]}
		maps.Copy(in, v)
		if out := srv(t, ctx, q, in); !contains([]string{out}, `"errors"`) || contains([]string{out}, `"echo"`) {
			t.Errorf("%s refused: %s", name, out)
		}
	}
}

// TestPresenterPanic: a resolver's panic, which graphql-go recovers and
// logs with its stack, is shown as an internal error and not logged again.
func TestPresenterPanic(t *testing.T) {
	quiet(t)
	var logged []error
	s, err := graphql.NewSchema(graphql.SDL(`type Query { boom: Int }`),
		graphql.Query(graphql.Resolve("boom", func(context.Context, graphql.Root) (int, error) { panic("boom") })))
	if err != nil {
		t.Fatal(err)
	}
	p := graphqlgo.ErrorPresenter(func(_ context.Context, err error) { logged = append(logged, err) })
	out := executorServer(newExecutor(s, graphql.WithErrorPresenter(p)))(t, context.Background(), `{ boom }`, nil)
	if !strings.Contains(out, `"message":"internal error"`) || !strings.Contains(out, "INTERNAL_SERVER_ERROR") || len(logged) != 0 {
		t.Errorf("%s, logged %v; want an internal error, not logged", out, logged)
	}
	// A wrapped one too, and one joined with others.
	rp := recoveredPanic(t)
	for _, err := range []error{fmt.Errorf("field: %w", rp), errors.Join(errors.New("other"), rp)} {
		if out := p(context.Background(), err); out.Message != "internal error" || len(logged) != 0 {
			t.Errorf("%v: %q, logged %v", err, out.Message, logged)
		}
	}
}

// recoveredPanic is the error graphql-go gives the presenter for a panic
// it recovered.
func recoveredPanic(t *testing.T) error {
	t.Helper()
	var got error
	s, err := graphql.NewSchema(graphql.SDL(`type Query { boom: Int }`),
		graphql.Query(graphql.Resolve("boom", func(context.Context, graphql.Root) (int, error) { panic("boom") })))
	if err != nil {
		t.Fatal(err)
	}
	grab := func(ctx context.Context, err error) *graphql.Error {
		got = err
		return graphql.DefaultErrorPresenter(ctx, err)
	}
	executorServer(newExecutor(s, graphql.WithErrorPresenter(grab)))(t, context.Background(), `{ boom }`, nil)
	if got == nil {
		t.Fatal("no error presented")
	}
	return got
}

// quiet sends slog's default logger to nowhere until the test ends: the
// test logs errors on purpose.
func quiet(t *testing.T) {
	old := slog.Default()
	slog.SetDefault(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { slog.SetDefault(old) })
}
