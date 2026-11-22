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
type Vals { t: Time! u: UUID b: Bytes e: Bytes! n: Uint64! }
`

// vals holds the Go types velox entities hold.
type vals struct {
	T time.Time
	U *uuid.UUID
	B []byte
	E []byte
	N uint64
}

// TestScalars: Scalars binds Time, UUID, Bytes and Uint64 to time.Time,
// uuid.UUID (and a pointer to one), []byte and uint64, written and read as
// veloxgql's scalar types do; a value veloxgql refuses is the client's
// error. A nil []byte, a NULL column, is null, and an empty one "".
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
				return &vals{T: a.T, U: &a.U, B: a.B, N: a.N}, nil
			}),
			graphql.Resolve("none", func(context.Context, graphql.Root) (*vals, error) { return &vals{E: []byte{}}, nil }),
		),
		graphql.Object[vals]("Vals",
			graphql.Field("t", func(v *vals) time.Time { return v.T }),
			graphql.Field("u", func(v *vals) *uuid.UUID { return v.U }),
			graphql.Field("b", func(v *vals) []byte { return v.B }),
			graphql.Field("e", func(v *vals) []byte { return v.E }),
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
	if out, want := srv(t, ctx, `{ none { t u b e n } }`, nil), `{"data":{"none":{"t":"0001-01-01T00:00:00Z","u":null,"b":null,"e":"","n":"0"}}}`; out != want {
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

// ints holds the integer Go types velox entities hold beside graphql-go's
// own int, int32 and int64, as inputs gqlc binds take them.
type ints struct {
	I8  *int8
	I16 int16
	U8  uint8
	U16 uint16
	U32 uint32
	U   uint
}

// TestScalarsIntegers: Scalars binds Int to int8, int16, uint8, uint16 and
// uint32, and Uint64 to uint, both ways and as inputs' fields: a value out
// of the Go type's range, or of Int's, is the client's error, and so is a
// fraction; a uint32 over Int's 32 bits is an error of its field.
func TestScalarsIntegers(t *testing.T) {
	const sdl = `
scalar Time
scalar UUID
scalar Bytes
scalar Uint64
input In { i8: Int, i16: Int!, u8: Int!, u16: Int!, u32: Int!, u: Uint64! }
type Query { echo(in: In!): Out! big: Out! }
type Out { i8: Int, i16: Int!, u8: Int!, u16: Int!, u32: Int!, u: Uint64! }
`
	type echoArgs struct{ In ints }
	s, err := graphql.NewSchema(graphql.SDL(sdl), graphqlgo.Scalars(),
		graphql.Input[ints]("In"),
		graphql.Query(
			graphql.ResolveArgs("echo", func(_ context.Context, _ graphql.Root, a echoArgs) (*ints, error) { return &a.In, nil }),
			graphql.Resolve("big", func(context.Context, graphql.Root) (*ints, error) { return &ints{U32: 1 << 31}, nil }),
		),
		graphql.Object[ints]("Out",
			graphql.Field("i8", func(v *ints) *int8 { return v.I8 }),
			graphql.Field("i16", func(v *ints) int16 { return v.I16 }),
			graphql.Field("u8", func(v *ints) uint8 { return v.U8 }),
			graphql.Field("u16", func(v *ints) uint16 { return v.U16 }),
			graphql.Field("u32", func(v *ints) uint32 { return v.U32 }),
			graphql.Field("u", func(v *ints) uint { return v.U }),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	srv := executorServer(newExecutor(s, graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(nil))))
	ctx := context.Background()
	const q = `query($in: In!) { echo(in: $in) { i8 i16 u8 u16 u32 u } }`
	in := map[string]any{"i8": -128, "i16": -32768, "u8": 255, "u16": 65535, "u32": 2147483647, "u": "18446744"}
	if out, want := srv(t, ctx, q, map[string]any{"in": in}), `{"data":{"echo":{"i8":-128,"i16":-32768,"u8":255,"u16":65535,"u32":2147483647,"u":"18446744"}}}`; out != want {
		t.Errorf("echo:\n got %s\nwant %s", out, want)
	}
	if out, want := srv(t, ctx, `{ echo(in: {i8: null, i16: 1, u8: 2, u16: 3, u32: 4, u: "5"}) { i8 i16 u8 u16 u32 u } }`, nil), `{"data":{"echo":{"i8":null,"i16":1,"u8":2,"u16":3,"u32":4,"u":"5"}}}`; out != want {
		t.Errorf("literal:\n got %s\nwant %s", out, want)
	}
	if out := srv(t, ctx, `{ big { u32 } }`, nil); !contains([]string{out}, `"path":["big","u32"]`) || contains([]string{out}, `"u32":`) {
		t.Errorf("a uint32 over Int written: %s", out)
	}
	for name, v := range map[string]map[string]any{
		"an int8 over":    {"i8": 128},
		"an int16 under":  {"i16": -32769},
		"a uint8 under":   {"u8": -1},
		"a uint16 over":   {"u16": 65536},
		"a uint32 over":   {"u32": int64(2147483648)},
		"a fraction":      {"u8": 1.5},
		"a uint negative": {"u": "-1"},
	} {
		bad := maps.Clone(in)
		maps.Copy(bad, v)
		if out := srv(t, ctx, q, map[string]any{"in": bad}); !contains([]string{out}, `"code":"BAD_USER_INPUT"`) || contains([]string{out}, `"echo":{`) {
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

// TestPanicErrorTypeName: the error graphql-go presents for a recovered
// panic is of the Go type the presenter knows it by, *graphql.panicError.
// graphql-go exports no way to tell it, so the presenter compares the type's
// name: when graphql-go renames the type, this fails, and the presenter's
// name must follow.
func TestPanicErrorTypeName(t *testing.T) {
	if got := fmt.Sprintf("%T", recoveredPanic(t)); got != "*graphql.panicError" {
		t.Errorf("graphql-go's recovered panic is a %s; recoveredPanic in presenter.go looks for *graphql.panicError", got)
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

// money is an application's Go type no scalar of velox's binds.
type money int64

// TestUnboundInputFails: an input's field of a Go type no scalar binds, as
// a generated input's field of a graphql.Type the application has not bound
// to the field's own Go type, or has bound to another, fails NewSchema at start-up, naming the input
// field, and is not left for a request to find. velox-gen does not refuse
// it, as whether the application binds the type is the application's.
func TestUnboundInputFails(t *testing.T) {
	const src = "scalar Time scalar UUID scalar Bytes scalar Uint64 scalar Money\ninput In { price: Money }\ntype Query { x: String }"
	q := graphql.Query(graphql.Resolve("x", func(context.Context, graphql.Root) (*string, error) { return nil, nil }))
	other := graphql.Scalar("Money", func(w *graphql.Writer, n int64) error { w.Int64(n); return nil }, decodeMoney)
	for name, opt := range map[string]graphql.SchemaOption{"unbound": graphql.Options(), "bound to another Go type": other} {
		err := graphql.Validate(graphql.SDL(src), graphqlgo.Scalars(), opt, graphql.Input[struct{ Price *money }]("In"), q)
		if err == nil || !strings.Contains(err.Error(), "In.price") {
			t.Errorf("%s: %v, want an error naming In.price", name, err)
		}
	}
	bound := graphql.Scalar("Money", func(w *graphql.Writer, m money) error { w.Int64(int64(m)); return nil },
		func(v any) (money, error) { n, err := decodeMoney(v); return money(n), err })
	if err := graphql.Validate(graphql.SDL(src), graphqlgo.Scalars(), bound, graphql.Input[struct{ Price *money }]("In"), q); err != nil {
		t.Errorf("bound by the application: %v", err)
	}
}

// decodeMoney reads a Money literal or variable.
func decodeMoney(v any) (int64, error) {
	if n, ok := v.(int64); ok {
		return n, nil
	}
	return 0, errors.New("not a Money")
}

// TestSubscribeSourceError pins what a client sees of a subscription
// source function's error under ErrorPresenter: its text, with no code, as
// graphql-go drops the error before the presenter sees it (ADR 0106; ask
// GA6). Once graphql-go keeps the error, the presenter needs a rule that
// sends a code-less *graphql.Error with a foreign Err through
// veloxgql.Present, and this test changes with it.
func TestSubscribeSourceError(t *testing.T) {
	quiet(t)
	db, _ := modelDB(t)
	s, err := graphql.NewSchema(graphql.SDL(`type Query { n: Int } type Subscription { tick: Int }`),
		graphql.Query(graphql.Field("n", func(graphql.Root) int { return 1 })),
		graphql.Subscription(graphql.Subscribe("tick", func(context.Context) (<-chan int, error) {
			return nil, errors.New(`pq: relation "secret" does not exist`)
		})))
	if err != nil {
		t.Fatal(err)
	}
	var logged []error
	e := newExecutor(s, graphqlgo.With(db), graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(func(_ context.Context, err error) { logged = append(logged, err) })))
	_, err = e.Subscribe(context.Background(), &graphql.Request{Query: `subscription { tick }`})
	var se *graphql.SubscribeError
	if !errors.As(err, &se) || len(se.Response.Errors) != 1 {
		t.Fatalf("Subscribe = %v, want one *SubscribeError", err)
	}
	got := se.Response.Errors[0]
	if got.Message != `pq: relation "secret" does not exist` || got.Extensions["code"] != nil || len(logged) != 0 {
		t.Errorf("%q %v, logged %v; want the source's text verbatim, no code, nothing logged", got.Message, got.Extensions, logged)
	}
}

// TestCodedErrorDefaultPresenter: a velox coded error gives its code to
// graphql.DefaultErrorPresenter too, through GraphQLExtensions, so a server
// without ErrorPresenter still sends the code.
func TestCodedErrorDefaultPresenter(t *testing.T) {
	for _, err := range []error{gqlgraph.InputErrorf("bad %d", 1), fmt.Errorf("wrapped: %w", gqlgraph.InputErrorf("bad"))} {
		got := graphql.DefaultErrorPresenter(context.Background(), err)
		if got.Extensions["code"] != "BAD_USER_INPUT" {
			t.Errorf("DefaultErrorPresenter(%v) extensions = %v, want code BAD_USER_INPUT", err, got.Extensions)
		}
	}
}
