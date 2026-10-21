# API Plan 3: The GraphQL Runtime — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the GraphQL runtime ready for 1.0:
- a version guard between generated code and `veloxgql`, and a `gqlimpl` package for what only generated code calls;
- limits that come from the context, set on an extension with options;
- errors that are safe to show a client, with a code, and an `ErrorPresenter`;
- composable root connections (`XConnectionOf`);
- field directives that also guard the where inputs;
- `Pred(ctx)`;
- a CI job against `gqlgen@latest`.

**Architecture:** Each task changes one part of the runtime and moves every caller in the same commit: `veloxgql`, `gqlimpl`, the code generator (`internal/gogen`, `internal/sdl`), its goldens, the e2e tests, `example/graphql` (its `gen/` and gqlgen's `graph/`), and the docs. Generated code changes in several tasks; each task regenerates it. The repository builds and passes the full gate after every task.

**Tech Stack:** Go 1.27; gqlgen v0.17.86 and gqlparser v2.5.59, unchanged in every `go.mod`. No new dependency.

**Spec:** `docs/specs/2026-10-20-api-cleanup-design.md`, §2.3 and §4, with the rulings of §6a and §6b. The evidence is `docs/reviews/2026-10-20-api-review-contrib.md` (findings 1–9), the GraphQL design `docs/specs/2026-10-19-graphql-design.md`, and the "For GraphQL plan 3" and "Before 1.0 (veloxgql)" lists of `docs/plans/2026-10-14-core-1-followups.md`. Mutations are not in this plan: they are GraphQL plan 3, which builds on it.

## Global Constraints

- **No aliases.** No renamed or removed API keeps an alias or a deprecated copy. Every caller moves in the same commit: `contrib/graphql` (with its goldens and e2e tests), `example/graphql` (`gen/`, `graph/`, `main.go`, tests, README) and every doc snippet.
- **The core's `GeneratedCodeIsVersion4` stays 4.** Core-generated code does not change. The core gains one additive function in Task 3 (`velox.TxRetries`), and nothing else.
- **GraphQL generated code is version 1.** `veloxgql.GeneratedCodeIsVersion1` is new in Task 1 and stays 1 through this plan: no version of it has been released.
- **Generated code imports** the standard library, `velox`, `veloxgql`, `gqlimpl` and the application's own type packages, and nothing else (no gqlgen, no gqlparser).
- **Budgets and escape check unchanged.**
  - `go test -run Budget -v .`: one-row 6, tenant 7, hundred rows 105, tx 9/6, observed 7/6, metadata 19, Insert 13, replica one-row 6.
  - `go build -gcflags=-m . 2>&1 | grep "db.go:$(grep -n 'func (db \*DB) plain' db.go | cut -d: -f1):"` prints `leaking param content: st` and `read does not escape`.
  - A task that moves a number stops and reports it.
- **The full gate passes at every task's commit:** `<scratchpad>/fix1.sh`, with Docker. If golangci-lint's lock is held, run it with `--allow-parallel-runners`.
- **CI.** `scripts/ci-local.sh` and `.github/workflows/ci.yml` stay identical in what they run. Task 8 adds the `gqlgen@latest` job to both.
- **No machine-specific paths** in committed files. Committed text says `<scratchpad>`.
- **Shell only:** `GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2`. The machine is short of memory and loaded: one heavy command at a time. Do not kill other processes.
- **Regenerating the example** is `(cd example && go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema && go -C graphql tool gqlgen generate)`. Commit `gen/` and `graph/` together.
- **Commits** end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.

## Review Focus

1. **Internal text inside a wrapper.** gqlgen hands the presenter a `*gqlerror.Error` whose `Err` is a driver or core error (`velox: gen.Post: no tenant…`, `sqlite: no such table`). The client must see the mapped message and code, never the wrapped text, and the error keeps its `path`. An `errors.Join` of a coded error and a driver error shows the coded message only. Task 5 pins this in `TestErrorPresenter`.
2. **Limits absent from the context.** Generated code runs outside a gqlgen server: in a job, or in a test. `LimitsFrom` must then give the defaults, not zeros, since a zero `MaxPage` would refuse every page. Task 2 pins this: a connection under `context.Background()` pages 100 rows.
3. **A retry hidden among the transaction options.** `WithTxOptions(velox.TxTimeout(t), velox.TxRetrySerializable(3))` must be refused, naming `TxRetrySerializable`. `TxRetrySerializable(3), TxRetrySerializable(0)` must pass, since the last one wins and asks for no retry. Task 3 pins this.
4. **`Pred(ctx)` called twice on one context.** An application calls `w.Pred(ctx)` from its own resolver, twice. Each call starts at depth 0 with a full node budget: the first call must not spend the second's. Task 4 pins this.
5. **A base query with a Limit or an Offset.** `PostsConnectionOf(ctx, q.Limit(1).Offset(1), …)`: the page is the connection's. Its Offset is cleared and its Limit replaced, so paging through the whole list neither skips nor repeats a row. Task 6 pins this.

---

### Task 1: `gqlimpl` and the version guard

**Files:**
- Create: `contrib/graphql/veloxgql/gqlimpl/doc.go`, `where.go`, `paginate.go`, `cursor.go`, and their tests, moved from `veloxgql` (`where_test.go`, `paginate_test.go`, `export_test.go` for the unexported `paginate`).
- Create: `contrib/graphql/veloxgql/internal/shared/value.go`, holding today's `veloxgql/value.go` and the conversion behind `CursorValue`. Both `veloxgql` (IDs) and `gqlimpl` (cursors) import it.
- Delete: `veloxgql/where.go`, `veloxgql/paginate.go`, `veloxgql/value.go`, and the moved tests.
- Modify: `veloxgql/cursor.go` (loses `Check`, `CursorOrder` and `CursorValue`), `veloxgql/doc.go`, `veloxgql/id.go`.
- Create: `contrib/graphql/veloxgql/api_test.go`.
- Modify: `contrib/graphql/internal/gogen/render.go`, `conn.go`, `where.go`, `render_test.go`, and the goldens in `internal/gogen/testdata/golden`.
- Modify: `contrib/graphql/cmd/velox-graphql/e2e_test.go`, `contrib/graphql/doc.go`.
- Regenerate: `example/graphql`.

**Interfaces:**
- In `veloxgql/doc.go`:

```go
// For generated code. GeneratedCodeIsVersion1 is referenced by every Go file
// velox-graphql writes, so that generated code and this module fail to
// compile together when they disagree. It is replaced by
// GeneratedCodeIsVersion2 when generated code must change.
const GeneratedCodeIsVersion1 = true
```

- Every `gql_*.go` has `const _ = veloxgql.GeneratedCodeIsVersion1` after its imports, as core files have `const _ = velox.GeneratedCodeIsVersion4`.
- Package `gqlimpl` (`github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl`). Its doc opens "Package gqlimpl is for generated code." and says compatibility is by `veloxgql.GeneratedCodeIsVersion1`, not semantic versioning, as protobuf's `protoimpl` is. It exports, unchanged in signature for now:
  - `Filter`, `NewFilter`, `Input`, `Not`, `And`, `Or`, `HasWith`, `Is`, `Value`, `ValueAs`, `List`, `ListAs`, `ID`, `IDAs`, `IDList`, `IDListAs`;
  - `AsString`, `AsInt`, `AsFloat`, `AsBool`, `AsUUID`, and `type Integer interface{ ~int | … | ~uint64 }` (today's unexported `integer`, which `AsInt` names);
  - `Args`, `OrderTerm`, `Term`, `Page`, `Connection`, `NewConnection`, `LoadedConnection`.
- Unexported in `gqlimpl`: `paginate` (today's `Paginate`), `checkDepth`, `checkWidth`, `cursorOrder`, `checkCursor(c veloxgql.Cursor, typ string, order []cursorOrder) error` and `cursorValue[T]`.
- `veloxgql` keeps: `Cursor`, `CursorTerm`, `EncodeCursor`, `DecodeCursor`, `PageInfo`, `OrderDirection` and its constants, `Noder`, `EncodeID`, `DecodeID`, `IDType`, `WithDB`, `DBFrom`, `Handler`, the limits, the sentinels, the scalars and their function marshalers, and `GeneratedCodeIsVersion1`. Tasks 2–5 change this list.
- The generated code calls `gqlimpl.*` wherever it called the moved names. Its doc comments name them as `gqlimpl.NewConnection` and so on.

- [ ] **Step 1: Write the failing tests.**
  - `TestPublicAPI` in `veloxgql/api_test.go`: parse the package with `go/doc` and compare the sorted exported names (types, functions, methods, constants, variables) with a list in the test. The list is the set above. It holds no `Paginate`, `CheckDepth`, `CheckWidth`, `CursorOrder`, `CursorValue`, `NewFilter`, `Term` or any other moved name. Every later task that changes the API edits this list.
  - `TestGqlimplIsForGeneratedCode`: `gqlimpl`'s package doc starts `Package gqlimpl is for generated code.` and names `veloxgql.GeneratedCodeIsVersion1`. `GeneratedCodeIsVersion1`'s doc starts `For generated code.`
  - `TestGeneratedFilesCarryTheGuard` in `render_test.go`: every rendered `gql_*.go` contains `const _ = veloxgql.GeneratedCodeIsVersion1` exactly once.
- [ ] **Step 2: Run them.** Run `(cd contrib/graphql && go test ./veloxgql/... ./internal/gogen)`. Expected: FAIL (`gqlimpl` does not exist).
- [ ] **Step 3: Move the code and update the generator.** Move the bodies unchanged. Add `f.gqlimpl()` beside `f.veloxgql()` in `render.go`, and the guard line to every file. Regenerate the goldens with the gogen test's update flag and read every diff: only imports, qualifiers, doc names and the guard change.
- [ ] **Step 4: Regenerate the example and run the module tests.** Run the regeneration command, then `(cd contrib/graphql && go test ./...)` and `(cd example && go test ./graphql/...)`. Expected: PASS. `git diff --stat example/graphql/graph` shows only what the moved names change.
- [ ] **Step 5: Run the full gate.** Expected: rc=0 for every step.
- [ ] **Step 6: Commit** `GraphQL: gqlimpl for generated code, and GeneratedCodeIsVersion1`.

### Task 2: Limits from the context, and `NewExtension`

**Files:**
- Modify: `veloxgql/limits.go`, `veloxgql/db.go`, `veloxgql/errors.go`, `veloxgql/doc.go`, `veloxgql/api_test.go`.
- Delete: `veloxgql/handler.go`, `handler_test.go`. Create: `veloxgql/extension.go`, `extension_test.go`, holding the same behaviour under the new names.
- Modify: `veloxgql/limits_test.go`, `gqlimpl/where.go`, `gqlimpl/paginate.go` and their tests.
- Modify: `internal/gogen/render.go` (`Nodes`), `conn.go` (list edges, `LoadedConnection`, `NewConnection`), the goldens, `e2e_test.go`.
- Modify: `example/graphql/main.go`, `main_test.go`, `README.md`, `graph/resolver.go` (its doc). Regenerate the example.

**Interfaces:**
- In `veloxgql`:

```go
// Limits caps what a client may ask for. Each bounds one argument or one
// edge. A zero field means the default.
type Limits struct {
	MaxPage, MaxList, MaxFilterDepth, MaxFilterWidth, MaxFilterNodes, MaxOrderTerms int
}

// DefaultLimits returns MaxPage 100, MaxList 1000, MaxFilterDepth 5,
// MaxFilterWidth 100, MaxFilterNodes 1000 and MaxOrderTerms 5.
func DefaultLimits() Limits

// LimitsFrom returns the limits the extension put in ctx, or
// DefaultLimits() when there are none.
func LimitsFrom(ctx context.Context) Limits

type Extension struct{ /* unexported */ }
type Option func(*Extension)

func NewExtension(db *velox.DB, opts ...Option) *Extension
func WithLimits(l Limits) Option
// Context returns ctx with the extension's DB and limits, as a query
// operation gets them: for generated code called outside a gqlgen server.
// It panics with the error Validate would return.
func (e *Extension) Context(ctx context.Context) context.Context
```

- `*Extension` implements `graphql.HandlerExtension`, `OperationInterceptor`, `ResponseInterceptor` and `FieldInterceptor`, with today's `dbHandler` behaviour. `ExtensionName()` stays `"VeloxDB"`.
- The package variables `MaxPage`, `MaxList`, `MaxFilterDepth`, `MaxFilterWidth`, `MaxFilterNodes` and `MaxOrderTerms` are removed. So are `Handler` and `WithDB`. `DBFrom` stays, and its error says `serve the schema through veloxgql.NewExtension`.
- `WithLimits` fills each zero field from `DefaultLimits()`. Given twice, the last wins.
- `Validate` returns, for a nil DB, `veloxgql.NewExtension: nil DB`, and for a negative limit, `veloxgql.NewExtension: Limits.MaxPage is -1, and a limit must be positive`.
- The operation's context holds the DB and the limits as one value. A mutation's transaction replaces the DB and keeps the limits.
- New sentinel `ErrFilterTooLarge = errors.New("filter is too large")`: a where argument over `MaxFilterNodes`. `ErrFilterTooWide` keeps lists over `MaxFilterWidth`.
- In `gqlimpl`:
  - `func NewConnection[E any](ctx context.Context, q velox.Query[E], typ string, args Args, order, key []OrderTerm[E]) (*Connection[E], error)`;
  - `func LoadedConnection[E any](ctx context.Context, rows []E, typ string, key []OrderTerm[E]) *Connection[E]`;
  - `NewFilter` reads `LimitsFrom(ctx)` at the top of a where argument, and nested inputs use the same limits.
- Generated code reads `veloxgql.LimitsFrom(ctx).MaxList` for a list edge and `.MaxPage` in `Nodes`. Its doc comments say "the MaxPage of veloxgql.LimitsFrom(ctx)".
- `example/graphql/main.go` calls `srv.Use(veloxgql.NewExtension(db))`.

- [ ] **Step 1: Write the failing tests.**
  - `TestDefaultLimits`: `DefaultLimits()` is `{100, 1000, 5, 100, 1000, 5}`, and `LimitsFrom(context.Background())` equals it.
  - `TestConnectionWithoutExtension` (Review Focus 2): `gqlimpl.NewConnection(context.Background(), q, …)` with no `first` over 150 rows gives a page of 100 with `hasNextPage` true. `LoadedConnection(context.Background(), rows, …)` over 150 rows pages 100.
  - `TestWithLimitsZeroIsDefault`: `NewExtension(db, WithLimits(Limits{MaxPage: 2})).Context(ctx)` gives `LimitsFrom` `{2, 1000, 5, 100, 1000, 5}`.
  - `TestExtensionRefusesNegativeLimits`: each field at -1 fails `Validate` with the message above, naming that field. `Context` panics with the same text.
  - `TestTwoServersDiffer`: two servers in one process, `MaxPage` 2 and 3, run in parallel. `first: 3` fails with `ErrPageTooLarge` on the first and passes on the second.
  - `TestFilterBudgetError`: a where argument over `MaxFilterNodes` fails with `ErrFilterTooLarge` and the text `filter is too large`. A list over `MaxFilterWidth` still fails with `ErrFilterTooWide`.
  - In `e2e_test.go`, the `MaxList` case sets the limit through `NewExtension(db, WithLimits(Limits{MaxList: 1})).Context(ctx)`. It no longer assigns a global.
  - `TestPublicAPI`'s list gains `Limits`, `DefaultLimits`, `LimitsFrom`, `Extension`, `Option`, `NewExtension`, `WithLimits`, `ErrFilterTooLarge`, and loses the six variables, `Handler` and `WithDB`.
- [ ] **Step 2: Run them.** Run `(cd contrib/graphql && go test ./veloxgql/...)`. Expected: FAIL (undefined names).
- [ ] **Step 3: Implement, and move every caller.** The handler tests move to `extension_test.go` and keep every case. Regenerate the goldens and the example.
- [ ] **Step 4: Run the module tests.** Run `(cd contrib/graphql && go test -race ./...)` and `(cd example && go test ./graphql/...)`. Expected: PASS. `grep -rn 'veloxgql\.Max\|veloxgql\.Handler\|veloxgql\.WithDB' --include='*.go' --include='*.md' contrib example` prints nothing.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `GraphQL: limits come from the context, set by NewExtension`.

### Task 3: Transaction options on the extension

**Files:**
- Modify: `tx.go`, `tx_test.go` (the core's one addition).
- Modify: `veloxgql/extension.go`, `extension_test.go`, `api_test.go`, `doc.go`.

**Interfaces:**
- In the core:

```go
// TxRetries returns the n of the last TxRetrySerializable in opts, or 0
// when there is none. It is for code that runs a transaction's fn on an
// application's behalf and cannot run it again.
func TxRetries(opts ...TxOption) int
```

- In `veloxgql`:

```go
// WithTxOptions sets the options of a mutation's transaction. Validate
// refuses TxRetrySerializable: gqlgen makes the response once, so the
// operation cannot run again.
func WithTxOptions(opts ...velox.TxOption) Option

// WithTxFilter decides, for each mutation operation, whether it runs in a
// transaction. f gets the operation's context and its definition. A nil f
// means every mutation does.
func WithTxFilter(f func(ctx context.Context, op *ast.OperationDefinition) bool) Option
```

- `WithTxOptions` given twice: the last wins.
- `Validate` refuses `velox.TxRetries(opts...) > 0` with `veloxgql.WithTxOptions: TxRetrySerializable(3) would run the mutation again, and gqlgen makes its response once`.
- When `f` returns false, the mutation runs on the extension's DB with no transaction. Its field resolvers are not serialized, since no transaction's DB is shared.
- `f` runs in `InterceptOperation`, once per mutation operation.

- [ ] **Step 1: Write the failing tests.**
  - `TestTxRetries` in the core: no options gives 0; `TxTimeout(t), TxRetrySerializable(3)` gives 3; `TxRetrySerializable(3), TxRetrySerializable(0)` gives 0.
  - `TestTxOptionsRefuseRetry` (Review Focus 3): `WithTxOptions(velox.TxTimeout(time.Second), velox.TxRetrySerializable(3))` fails `Validate` with the message above. `TxRetrySerializable(3), TxRetrySerializable(0)` passes. `TxIsolation(sql.LevelSerializable)` passes.
  - `TestTxOptionsApply`: with `TxIsolation(sql.LevelSerializable)`, the test's `TxAround` hook sees `TxInfo.Isolation == sql.LevelSerializable` for a mutation. With `TxTimeout(50*time.Millisecond)`, the mutation resolver's context has a deadline at most 50 ms away.
  - `TestTxFilter`: a filter returning false for the operation named `Login` gives no BEGIN in the hook's log, and the resolver's statements still run. Another mutation runs in one transaction. The filter gets the operation's name.
- [ ] **Step 2: Run them.** Run `go test -run TestTxRetries .` and `(cd contrib/graphql && go test ./veloxgql)`. Expected: FAIL (undefined names).
- [ ] **Step 3: Implement.** `TxRetries` applies each option to a zero `txOptions`. Document both options in `veloxgql`'s package doc, under "The DB".
- [ ] **Step 4: Check the budgets.** Run `go test -run Budget -v .`. Expected: the numbers in Global Constraints, unchanged.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `GraphQL: transaction options and a transaction filter on the extension`.

### Task 4: `Pred(ctx)`

**Files:**
- Modify: `gqlimpl/where.go`, `gqlimpl/where_test.go`.
- Modify: `internal/gogen/where.go`, `conn.go` (the root connection's `where.Pred` call), the goldens, `e2e_test.go`.
- Regenerate: `example/graphql`.

**Interfaces:**
- `type Input[E, T any] interface{ *T; Pred(ctx context.Context) (velox.Pred[E], error) }`.
- `func NewFilter[E any](ctx context.Context, input string) *Filter[E]`.
  - The depth and the node budget travel in the context, under one unexported key.
  - A context without them starts a where argument: depth 0, a full budget, the limits of `LimitsFrom(ctx)`.
  - A nested input's `Pred` gets a context with depth + 1 and the same budget. That context never leaves the helpers.
- Generated: `func (w *UserWhereInput) Pred(ctx context.Context) (velox.Pred[User], error)`. Its doc drops "depth". The root connection calls `where.Pred(ctx)`.

- [ ] **Step 1: Write the failing tests.**
  - `TestPredDepthFromContext`: an input nested `MaxFilterDepth` deep passes; one level deeper fails with `ErrFilterTooDeep` and today's message, `it is nested 6 deep, and the most is 5`.
  - `TestPredTwiceOnOneContext` (Review Focus 4): an input that costs 600 nodes, under a budget of 1000, succeeds twice on the same context. Calling a nested input's `Pred` directly starts at depth 0.
  - `TestPredErrorPath`: today's nested error text, `veloxgql: OwnerWhereInput.hasEventsWith[0]: EventWhereInput.ownerID: …`, is unchanged.
- [ ] **Step 2: Run them.** Run `(cd contrib/graphql && go test ./veloxgql/gqlimpl)`. Expected: FAIL (wrong number of arguments).
- [ ] **Step 3: Implement, regenerate the goldens and the example, and move the e2e calls to `Pred(ctx)`.**
- [ ] **Step 4: Run the full gate.** Expected: rc=0.
- [ ] **Step 5: Commit** `GraphQL: where inputs take Pred(ctx); the depth travels in the context`.

### Task 5: Errors safe for clients, and `ErrorPresenter`

**Files:**
- Create: `veloxgql/presenter.go`, `presenter_test.go`.
- Modify: `veloxgql/internal/shared` (the coded error), `veloxgql/errors.go`, `id.go`, `cursor.go`, `scalar.go`, `marshal.go`, `extension.go`, `api_test.go`, `doc.go`.
- Modify: `gqlimpl/where.go`, `paginate.go`, `cursor.go`; create `gqlimpl/errors.go`.
- Modify: `internal/gogen/render.go` (enum `UnmarshalGQL`, `Node`, `Nodes`), `conn.go` (the order term's error), the goldens.
- Modify: `example/graphql/main.go`, `main_test.go`, `README.md`. Regenerate the example.

**Interfaces:**
- In `veloxgql`:

```go
// The codes ErrorPresenter writes as extensions.code.
const (
	CodeBadUserInput    = "BAD_USER_INPUT"
	CodeForbidden       = "FORBIDDEN"
	CodeUnauthenticated = "UNAUTHENTICATED"
	CodeNotFound        = "NOT_FOUND" // for GraphQL plan 3's mutations
	CodeInternal        = "INTERNAL_SERVER_ERROR"
)

// ErrorPresenter is a gqlgen error presenter for srv.SetErrorPresenter. It
// shows a client only what is safe, with a code; it logs everything else
// with log and shows "internal error". A nil log logs with slog.ErrorContext.
func ErrorPresenter(log func(ctx context.Context, err error)) graphql.ErrorPresenterFunc
```

- **Coded errors.** `shared.Error{Code, Msg string; Err error}`:
  - `Error()` is `"veloxgql: " + Msg`, so Go-side texts are unchanged;
  - `Unwrap()` returns `Err`, so `errors.Is` finds the sentinels;
  - `Extensions() map[string]any` returns `{"code": Code}`, the shape of gqlgen's former `ExtendedError`.
- Every error `veloxgql` and `gqlimpl` return for client input is coded `BAD_USER_INPUT`: the cursor, ID, pagination and filter errors, and the scalars' `Unmarshal*`. `ErrNoDB` stays uncoded: it is a programming error.
- `gqlimpl` exports `func InputErrorf(format string, args ...any) error`, a `BAD_USER_INPUT` error whose text is `"veloxgql: " + fmt.Sprintf(…)`, `%w` included. Generated code uses it:
  - for the order term's error;
  - for `Node`'s and `Nodes`' wrong-type and page errors;
  - for an enum's `UnmarshalGQL`, which reads `veloxgql: UserRole must be ADMIN or MEMBER`. That closes the follow-up on the missing prefix.
- **What the presenter shows**, in this order:

  | The error | Code | Message |
  |---|---|---|
  | nil | — | nil |
  | holds a coded error (`errors.As` to `interface{ Extensions() map[string]any }` whose map has a string `code`) | its code | the coded error's own text without `veloxgql: ` |
  | `*gqlerror.Error` with `Err == nil` (gqlgen's own, or the application's) | unchanged | unchanged |
  | `velox.ErrNoTenant` | `UNAUTHENTICATED` | `unauthenticated` |
  | `velox.ErrDenied` or `*velox.PolicyError` | `FORBIDDEN` | `forbidden` |
  | `*velox.ValidationError` | `BAD_USER_INPUT` | `invalid value: ` + the validator's text |
  | `*velox.ConstraintError` | `BAD_USER_INPUT` | `<kind> constraint violated`, with the kind's `String()` |
  | anything else | `INTERNAL_SERVER_ERROR` | `internal error`, after `log(ctx, err)` |

  - A `*gqlerror.Error` with an `Err` is classified by its `Err`. Its path and locations are kept.
  - Every other error gets `graphql.GetPath(ctx)` as its path, as gqlgen's default presenter does.
  - The commit error the extension adds goes through the presenter like any other.
- `example/graphql/main.go` calls `srv.SetErrorPresenter(veloxgql.ErrorPresenter(nil))`, and its README says why.

- [ ] **Step 1: Write the failing tests.**
  - `TestErrorsAreCoded`: a table over each input error path: `DecodeID` malformed and of the wrong type, `DecodeCursor`, `NewConnection` over `MaxPage`, `first` with `last`, a filter too deep, too wide and too large, and `UnmarshalUUID`. Each error has code `BAD_USER_INPUT`, and `errors.Is` still finds its sentinel.
  - `TestErrorPresenter` (Review Focus 1), a table:
    - a wrapped `ErrBadID` gives `BAD_USER_INPUT`, with a message that does not start `veloxgql: `;
    - `fmt.Errorf("velox: gen.Post: %w", velox.ErrNoTenant)` gives `unauthenticated`, and the message lacks `gen.Post`;
    - a `*velox.PolicyError` gives `FORBIDDEN`;
    - `&velox.ConstraintError{Kind: velox.ConstraintUnique, Constraint: "users_email_key", Err: driverErr}` gives `unique constraint violated`, without `users_email_key`;
    - `errors.New("sqlite: no such table: posts")` gives `internal error` and `INTERNAL_SERVER_ERROR`, and `log` got that error once;
    - a `*gqlerror.Error` with a path and that driver error as `Err` gives `internal error` with the path kept;
    - `errors.Join(codedBadID, driverErr)` shows the coded message only;
    - an application's `*gqlerror.Error` with `Extensions{"code": "X"}` and no `Err` passes unchanged.
  - `TestCommitErrorIsInternal`: a server with the presenter whose commit fails answers `internal error`, and `log` got the commit error.
  - In `example/graphql/main_test.go`:
    - `TestTenantIsolation`'s query without `X-Org-ID` answers `unauthenticated` with code `UNAUTHENTICATED`, and the body contains no `velox:`;
    - `TestBadIDs` expects code `BAD_USER_INPUT` and messages without `veloxgql: `.
  - `TestPublicAPI` gains the five codes and `ErrorPresenter`.
- [ ] **Step 2: Run them.** Run `(cd contrib/graphql && go test ./veloxgql/...)`. Expected: FAIL.
- [ ] **Step 3: Implement.** Move every error construction site to the coded error. Regenerate the goldens and the example.
- [ ] **Step 4: Run the full gate.** Expected: rc=0.
- [ ] **Step 5: Commit** `GraphQL: coded errors, and an ErrorPresenter safe for clients`.

### Task 6: Composable connections

**Files:**
- Modify: `internal/gogen/conn.go`, `render_test.go`, the goldens.
- Modify: `gqlimpl/paginate.go`, `paginate_test.go`.
- Modify: `e2e_test.go`.
- Create: `example/graphql/graph/app.graphqls`.
- Modify: `example/graphql/graph/schema.resolvers.go`, `main_test.go`, `README.md`. Regenerate the example.

**Interfaces:**
- For every Node entity, the generator writes:

```go
// PostsConnectionOf is the connection to the Post nodes q returns, filtered
// by where, ordered by orderBy and then the key, and paged by the other
// arguments. q keeps its filters and With loads; its Offset is cleared and
// its Limit replaced. q must have no OrderBy: the connection orders it. It
// runs no query.
func PostsConnectionOf(ctx context.Context, q velox.Query[Post], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error)
```

  - The name is the GraphQL type's plural, as for the root function (`LabelsConnectionOf` for `Tag`).
  - The parameters are the root function's, after `q`. An entity without an order or a where input has no such parameter.
- The root function `PostsConnection` and every connection edge method call `<Plural>ConnectionOf`. The unexported `<x>ConnectionGQL` functions are removed.
- `gqlimpl.NewConnection` clears the query's Offset (`q.Offset(0)`) before it pages.
- The example's `graph/app.graphqls` declares `extend type Query { publishedPosts(after: Cursor, first: Int, before: Cursor, last: Int, orderBy: [PostOrder!], where: PostWhereInput): PostConnection! }`. Its resolver gets the DB with `veloxgql.DBFrom(ctx)` and calls `gen.PostsConnectionOf(ctx, db.Query[gen.Post]().Where(gen.Posts.Status.EQ(gen.PostStatusPublished)), …)`.

- [ ] **Step 1: Write the failing tests.**
  - `TestConnectionOfEveryNode` in `render_test.go`: each Node entity's file declares `func <Plural>ConnectionOf(`, and no file declares `ConnectionGQL(`.
  - In `e2e_test.go`, `TestConnectionOf`: `gen.PostsConnectionOf(ctx, db.Query[gen.Post]().Where(gen.Posts.Title.EQ("a")), …)` gives posts 2 and 4, and `totalCount` 2.
  - `TestConnectionOfLimitOffset` (Review Focus 5): with the base query `.Limit(1).Offset(1)`, paging `first: 2` and then `after` the end cursor gives every post once, in key order.
  - `TestPublishedPosts` in `example/graphql/main_test.go`: `publishedPosts` under tenant 1 gives Alpha, Gamma and Delta, and `totalCount` 3.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Regenerate the goldens and the example. Run `go -C graphql tool gqlgen generate` and write the new resolver's one line.
- [ ] **Step 4: Run the full gate.** Expected: rc=0.
- [ ] **Step 5: Commit** `GraphQL: XConnectionOf composes a connection from a query`.

### Task 7: Directives reach the where inputs

**Files:**
- Modify: `contrib/graphql/annotation.go`, `annotation_test.go`, `doc.go`.
- Modify: `internal/sdl/where.go`, `rules.go`, `where_test.go`, `rules_test.go`, and the sdl goldens in `internal/sdl/testdata`.
- Modify: `example/graphql/schema/schema.go`, `graph/app.graphqls`, `main.go`, `main_test.go`, `README.md`. Regenerate the example.

**Interfaces:**
- `func OrderUnguarded() Annotation` sets `OrderUnguarded bool` (`json:"order_unguarded,omitempty"`). `Merge` ORs it. It applies to fields only.
- **What is copied.** A where input's fields carry the directives of what they filter, after any `@goField`:
  - a field's predicates (`salary`, `salaryGT`, `salaryIsNil`…) carry the field's `graphql.Directives`;
  - an edge's `has<Edge>` and `has<Edge>With` carry the edge's;
  - a foreign key's `<edge>ID` and `<edge>IDIn` carry the key field's, then the edge's. A directive name already there is not repeated, since GraphQL allows a directive once per location.
- **An application directive** is any directive other than GraphQL's own (sdl's `prelude`: `deprecated`, `include`, `skip`, `specifiedBy`, `defer`, `oneOf`) and `goField`.
- **New refusals:**
  - `OrderField` on a field with an application directive, without `OrderUnguarded`: `schema User, field salary: graphql.OrderField on a field with the directive @admin: its values would be in the cursors and its order would reveal them, which no directive guards; add graphql.OrderUnguarded() to allow it`;
  - `OrderUnguarded` without `OrderField`: `schema User, field salary: graphql.OrderUnguarded without graphql.OrderField says nothing`.
- `contrib/graphql`'s doc says the application declares such a directive `on FIELD_DEFINITION | INPUT_FIELD_DEFINITION`, or gqlgen refuses the schema. Row-level access is velox's policy; field-level access is a directive or `srv.AroundFields`.
- **The example:**
  - `User.email` gets `graphql.WhereOps()` and `graphql.Directives(graphql.Directive{Name: "admin"})`.
  - `graph/app.graphqls` declares `directive @admin on FIELD_DEFINITION | INPUT_FIELD_DEFINITION`.
  - `main.go` implements it: it passes when the request's `X-Role` header is `admin`. Otherwise it fails with an application `*gqlerror.Error` with code `FORBIDDEN`. Like `X-Org-ID`, the header is for this demo only, and the README says so.

- [ ] **Step 1: Write the failing tests.**
  - `TestWhereCarriesDirectives` in `where_test.go`:
    - a field with `@admin` and `WhereOps(OpsAll)` gives `@admin` on each of its predicate fields;
    - an edge with `@admin` and `WhereEdge` gives it on `has<Edge>` and `has<Edge>With`;
    - a foreign key with `@admin`, whose edge has `@admin` and `@audit`, gives `@admin @audit` once each on `<edge>ID`.
  - `TestOrderFieldUnderDirective` in `rules_test.go`: `@admin` with `OrderField` fails with the message above. Adding `OrderUnguarded` passes. `@deprecated` with `OrderField` passes. `OrderUnguarded` alone fails.
  - `TestFieldDirective` in `example/graphql/main_test.go`:
    - without `X-Role`, `users(where: {emailHasPrefix: "a"}) { totalCount }` and `users { edges { node { email } } }` each answer `FORBIDDEN`;
    - with `X-Role: admin`, both succeed, and the filter gives 1.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Regenerate the sdl goldens with their update flag and read the diff. Regenerate the example, then run `go -C graphql tool gqlgen generate`, which writes the directive's root into `graph/generated.go`.
- [ ] **Step 4: Run the full gate.** Expected: rc=0.
- [ ] **Step 5: Commit** `GraphQL: field directives guard the where inputs; OrderField under a directive needs OrderUnguarded`.

### Task 8: A CI job against `gqlgen@latest`

**Files:**
- Create: `scripts/gqlgen-latest.sh`.
- Modify: `.github/workflows/ci.yml`, `scripts/ci-local.sh`.
- Modify: `contrib/graphql/doc.go`, `example/graphql/README.md`.

**Interfaces:**
- `scripts/gqlgen-latest.sh`, with `set -euo pipefail`, run from any directory:
  1. Copy the working tree, tracked and untracked files but not ignored ones, into a temporary directory: `git ls-files -co --exclude-standard -z | tar --null -T - -cf - | tar -xf - -C "$tmp"`. A trap removes the directory, after `chmod -R u+w`.
  2. In the copy's `contrib/graphql`: `go get github.com/99designs/gqlgen@latest`, `go mod tidy`, `go vet ./...`, `go test -count=1 ./...`.
  3. In the copy's `example`: the same `go get` and `go mod tidy`, then `go -C graphql tool gqlgen generate`, `go vet ./graphql/...`, `go test -count=1 ./graphql/...`. gqlgen's new output is built and tested, not compared.
  4. Print `gqlgen <version>: ok`, the version as `go list -m` reads it in the copy's `example`.
- The repository's `go.mod` files stay at v0.17.86. The script never writes outside its temporary directory.
- `ci.yml` gains a second job, `gqlgen-latest`, with the same checkout and Go 1.27 setup as `test`, and one step: `run: scripts/gqlgen-latest.sh`. No database, and no `continue-on-error`: a red job says gqlgen moved.
- `ci-local.sh` runs `scripts/gqlgen-latest.sh` as its last command, after the scale check.
- `contrib/graphql`'s doc says: velox-graphql supports gqlgen v0.17.86 and later; CI tests v0.17.86 and the latest release. The `@goField`/`@goModel` declarations and the mutation lock (gqlgen calls field interceptors around the resolver alone) are what a new gqlgen could break.

- [ ] **Step 1: Write the script and the job.**
- [ ] **Step 2: Run the script.** Run `scripts/gqlgen-latest.sh`. Expected: the last line is `gqlgen v0.17.<n>: ok`, and `git status --short` shows only this task's files. If it fails, stop and report what the new gqlgen changed. Do not adapt the code in this task.
- [ ] **Step 3: Check the two CI files agree.** The steps of the `test` job and the commands of `ci-local.sh` are unchanged, and each gained only the script.
- [ ] **Step 4: Run the full gate.** Expected: rc=0.
- [ ] **Step 5: Commit** `CI: a job against gqlgen@latest, with a local mirror`.

### Task 9: Documents

**Files:**
- `docs/specs/2026-10-20-api-cleanup-design.md`: Status becomes "API plans 1–3 implemented". A §6c lists this plan's rulings and those found while executing it.
- `docs/specs/2026-10-19-graphql-design.md`: dated notes in §3 "Transactions and the DB" (`NewExtension`, its options, `Extension.Context`), in "Rulings made in plan 2" (limits from the context, `Pred(ctx)`, `gqlimpl`), and in §3 "Connections" (`XConnectionOf`).
- `docs/plans/2026-10-14-core-1-followups.md`:
  - mark resolved: "For GraphQL plan 3" (the presenter) and, in "Before 1.0 (veloxgql)", the globals, `Handler`'s name, the public helpers, `ErrFilterTooWide`'s text, the introspection wording and the enum prefix;
  - the "API plan 3 (dialects)" item becomes "API plan 5";
  - the citation item is resolved for `contrib/graphql`, and stays open for `contrib/otel` (API plan 4).
- `example/graphql/README.md`: only `__schema` is free under the complexity limit; `__type(name:)` costs a few points. `main.go`'s comment on `complexityLimit` says the same.
- Create: `contrib/graphql/godoc_test.go`, `TestGodocCitesNoSpec` over `graphql`, `veloxgql` and `gqlimpl`, as the root module's test is.
- Modify: the package docs of `veloxgql` and `gqlimpl`, and `contrib/graphql/doc.go`, checked against the code.

- [ ] **Step 1:** Write `TestGodocCitesNoSpec` and run it. Expected: FAIL only on the comments that cite a spec section. Fix those.
- [ ] **Step 2:** Edit the documents. Check every snippet against the code: `go doc` for each named symbol.
- [ ] **Step 3:** Run the full gate. Expected: rc=0.
- [ ] **Step 4:** Commit `Documents: the GraphQL runtime before 1.0`.

---

## Rulings this plan makes

Each is a call made while writing this plan. An implementer who finds one wrong stops and reports it instead of changing it.

1. **(call)** The guard is `veloxgql.GeneratedCodeIsVersion1`, as §2.3 says, not a `gqlimpl` constant, as the review proposed. Generated files reference it as `const _ = …`, the core's form. One module holds both packages, so one guard covers `gqlimpl` too.
2. **(call)** `gqlimpl` holds every name only generated code calls (Task 1's list). `Paginate`, `CheckDepth` and `CheckWidth` become unexported there, as §2.3 says. So do `Cursor.Check`, `CursorOrder` and `CursorValue`, which only the paginator uses. `Cursor`, `CursorTerm`, `EncodeCursor` and `DecodeCursor` stay in `veloxgql`: the cursor is a scalar the application sees. The conversions both packages need live in `veloxgql/internal/shared`.
3. **(call)** `WithDB` is removed. `(*Extension).Context(ctx)` replaces it: one way to put the DB in a context, which also carries the limits and is validated.
4. **(call)** A zero `Limits` field means the default. A negative one is refused by `Validate`, and `Context` panics with the same error. `DefaultLimits()` is a function, so no mutable package state is left.
5. **(call)** gqlgen v0.17.86 has no `ExtendedError` interface. veloxgql's coded errors have an `Extensions() map[string]any` method in its shape, and `ErrorPresenter` reads it. gqlgen's default presenter does not, so an application gets codes only with `ErrorPresenter`.
6. **(call)** The codes are Apollo's (`BAD_USER_INPUT`, `FORBIDDEN`, `UNAUTHENTICATED`, `INTERNAL_SERVER_ERROR`) and `NOT_FOUND`. `CodeNotFound` is defined now because GraphQL plan 3 adds no public API to `veloxgql`. The messages for core errors are fixed texts (Task 5's table), so no entity, constraint or driver name reaches a client.
7. **(call)** A nil `log` logs with `slog.ErrorContext`, so an internal error is never silent.
8. **(call)** A list edge over `MaxList`, a timeout and `ErrNoDB` are internal errors: the client cannot fix them, and the log names the edge for the operator.
9. **(call)** The node budget gets its own sentinel, `ErrFilterTooLarge` ("filter is too large"). `ErrFilterTooWide` keeps lists over `MaxFilterWidth`.
10. **(call)** The core gains `velox.TxRetries(opts ...TxOption) int`, because a `TxOption` is an opaque function and veloxgql cannot see a retry otherwise. It is additive, generated code does not change, and the core's version stays 4.
11. **(call)** `WithTxFilter`'s function takes the context and the operation definition, as the review proposed: the spec's `func(op) bool` has no way to see the request's caller. A nil function means every mutation runs in a transaction. A mutation the filter leaves out runs on the DB, with its resolvers not serialized.
12. **(call)** `<Plural>ConnectionOf` is written for every Node entity, not only those with a `QueryField`, so an edge-only entity can be composed too. Root functions and edge methods call it. The connection clears the base query's Offset and replaces its Limit. An `OrderBy` on the base query cannot be detected without a core accessor, so it is documented as not allowed instead.
13. **(call)** Directives are copied onto every where-input field that filters on the element: a field's predicates, an edge's `has`/`hasWith`, and a foreign key's ID predicates (the key field's, then the edge's, each name once).
14. **(call)** The `OrderField` refusal counts application directives only. GraphQL's own (`@deprecated`…) guard nothing. The opt-in is `graphql.OrderUnguarded()`, a name that says what it allows.
15. **(call)** The `gqlgen@latest` job is a script, `scripts/gqlgen-latest.sh`, that works in a copy of the tree. CI runs it as its own job, and `ci-local.sh` runs it last, so the local mirror runs what CI runs. It fails red rather than `continue-on-error`. The supported range is "v0.17.86 and later".
16. **(call)** Left out:
    - mutations (GraphQL plan 3);
    - the first/last-aware cost limit, and `LoadedConnection`'s argument rule (GraphQL plan 4);
    - the dialect sweep of `SQL(ctx)` with a policy, which moves to API plan 5: §2.3 and §4 do not cover it;
    - renaming the module to `contrib/veloxgql` (review finding 15 says "consider"; the spec does not ask for it).
