# API review: contrib/otel and contrib/graphql (2026-10-20)

### Summary verdict
Both modules are carefully built and thoroughly documented. The otel adapter is close to 1.0. What it still needs is one way to configure it, scope/schema metadata, and a fix to a metric that is skewed under `Iter`. GraphQL is not ready to freeze yet. The `veloxgql` package mixes the API applications call with the functions generated code calls. Users' `gen/` already reads globals (`veloxgql.MaxList`, `MaxPage`), and nothing checks that generated code and runtime versions match. Client-safe errors, handler options and a composable root connection are missing. Fix how generated code is coupled to the runtime first: once users have generated code, everything else gets more expensive to change.

### Findings

**1. Must · graphql/maintain · No version guard between generated code and veloxgql.**
Evidence: core has `velox.GeneratedCodeIsVersion3` (doc.go:13-18); `gql_*.go` (golden `gql_user.go`, `gql_node.go`) reference no equivalent, and `grep Version veloxgql` finds only `cursorVersion`.
Change: `const GeneratedCodeIsVersion1 = true` in the generated-code package (see 2), referenced as `var _ = gqlimpl.GeneratedCodeIsVersion1` in every `gql_*.go`. Breaking: no (additive, but it only protects users if it ships before the first release).

**2. Should (I'd treat it as Must) · graphql/clean+maintain · The application API and the generated-code functions share one package.**
Evidence: `go doc veloxgql` lists about 60 symbols. About 30 of them exist only for `gen/`: `Value/ValueAs/List/ListAs/ID/IDAs/IDList/IDListAs/Is/Not/And/Or/HasWith/Input/NewFilter/Filter`, `As*`, `Term/OrderTerm/NewConnection/LoadedConnection/Connection/Page/Paginate`, `CheckDepth/CheckWidth`, `CursorValue/CursorOrder/CursorTerm`. `veloxgql.ID` as a *function* reads like an ID type. This also settles the follow-up "decide which helpers stay public" on a principle instead of case by case.
Change: follow protobuf's `protoimpl` and move them to `contrib/graphql/veloxgql/gqlimpl`, documented as "for generated code; compatibility is by GeneratedCodeIsVersionN, not semver". Keep only these in `veloxgql`: the extension, limits, errors and presenter, the scalars, `Cursor`, `PageInfo`, `OrderDirection`, `Noder`, `EncodeID/DecodeID/IDType`, `WithDB/DBFrom`. Drop `Paginate`, `CheckDepth` and `CheckWidth` outright. Breaking: yes (users regenerate).

**3. Must (agree with the follow-up; widen the scope) · graphql/correct · Limits are globals, and generated code reads them.**
The follow-up covers the globals. It doesn't mention that generated code reads them directly: `gql_user.go` `MaxRows(veloxgql.MaxList)`, `gql_node.go` `len(ids) > veloxgql.MaxPage`. `LoadedConnection(rows, typ, key)` (paginate.go:147) has no `ctx`, so it cannot read a per-request limit. The fix therefore changes the code generator, not only the runtime.
Change:
```go
type Limits struct{ MaxPage, MaxList, MaxFilterDepth, MaxFilterWidth, MaxFilterNodes, MaxOrderTerms int }
func DefaultLimits() Limits
func LimitsFrom(ctx context.Context) Limits   // defaults when absent
// gqlimpl.LoadedConnection(ctx, rows, typ, key); gen code: veloxgql.LimitsFrom(ctx).MaxList
```
Breaking: yes.

**4. Must · graphql/clean · Rename `Handler` and give it options.**
`veloxgql.Handler(db)` is not an `http.Handler`. It sits next to gqlgen's `handler.New` and returns an anonymous 4-interface type (handler.go:66-71). It has no way to set limits, `velox.TxOption`s (isolation, `Timeout`), or which mutations run in a transaction.
Change:
```go
func NewExtension(db *velox.DB, opts ...Option) *Extension
func WithLimits(Limits) Option
func WithTxOptions(...velox.TxOption) Option   // refuses RetrySerializable in Validate: the response is built once, fn cannot rerun
func WithTxFilter(func(ctx context.Context, op *ast.OperationDefinition) bool) Option
```
Today every mutation holds a pooled connection for the whole operation, including mutations that call external services such as `login` or `sendEmail`. Document that, or begin the transaction lazily. Breaking: yes.

**5. Must before 1.0 (I'd move it ahead of plan 3) · graphql/clean+correct · Errors reach clients unfiltered and without a code.**
The follow-up schedules an ErrorPresenter recipe for plan 3, but queries already leak today:
- core and driver text (`velox: gen.Post: no tenant…`, `velox.ErrTooManyRows` wrapped as `veloxgql: Account.groups: …`, timeouts, SQLite/pgx messages);
- the commit error, which `InterceptResponse` adds (handler.go:165-167).

veloxgql's own messages also carry a library prefix (`veloxgql: AccountWhereInput…`) that clients should not see.
Change: veloxgql errors implement gqlgen's `ExtendedError` (`Extensions() map[string]any{"code": "BAD_USER_INPUT"}`). Add
```go
func ErrorPresenter(log func(ctx context.Context, err error)) graphql.ErrorPresenterFunc
```
It passes veloxgql's sentinels, `PolicyError`/`ErrDenied` (FORBIDDEN), `ErrNoTenant` (UNAUTHENTICATED), `ValidationError` and `ConstraintError` (kind only) through, logs everything else and returns "internal error". Use it in example/graphql. Breaking: no.

**6. Should · graphql/complete · Root connections cannot be composed.**
`AccountsConnection(ctx, after, first, before, last, where)` always starts from `db.Query[User]()`. The function that takes a base query, `userConnectionGQL(ctx, q, …)`, is unexported (golden gql_user.go). An application cannot write `myPosts` or `publishedPosts`. entgql's `client.Post.Query().Where(…).Paginate(…)` can.
Change: export `PostsConnectionOf(ctx, q velox.Query[Post], after, first, before, last, orderBy, where)` and make the root function call it. Breaking: no.

**7. Should · graphql/correct · Field-level auth can be bypassed through filters and ordering.**
`graphql.Directives` on a field land only on the type's `FIELD_DEFINITION` (render.go:359-409, 746). The field's `where` predicates and `orderBy` value carry none. An `@hasRole` on `salary` therefore still lets a client bisect it with `salaryGT`, the "value oracle" that plan 2's ruling refuses for `Skip(SkipType)` fields.
Change: copy a field's directives onto its where-input fields (gqlgen runs `INPUT_FIELD_DEFINITION` directives), and refuse `OrderField` on a directive-carrying field unless opted in. Document the auth recipe: row-level is velox policy, field-level is a directive or `srv.AroundFields`. Breaking: no (it is a new refusal).

**8. Should · graphql/clean · `Pred(ctx, depth int)` exposes an internal counter.**
Every where input gets an exported `Pred(ctx context.Context, depth int)` (golden gql_user.go). An application calling it from a custom resolver must pass `0`.
Change: carry depth in the context, as the node budget already is (where.go NewFilter), giving `Pred(ctx) (velox.Pred[E], error)`. Breaking: yes (generated code).

**9. Should · graphql/maintain · gqlgen coupling is untested beyond v0.17.86.**
- velox.graphqls declares `@goField`/`@goModel` "as gqlgen v0.17.86 documents them" (render.go:746), and the app must not redeclare them.
- Mutation serialization relies on gqlgen calling field interceptors around the resolver alone (handler.go:172-177). That is gqlgen behaviour, not documented API.

gqlgen is pre-1.0 and breaks in minor releases.
Change: add a CI job against `gqlgen@latest` and document the supported range. Generated code imports only veloxgql and velox, which is good: keep it that way.

**10. Must for plan 3 · graphql/complete · What mutations need to look like.**
- One-liners: `gen.CreateUser(ctx, in CreateUserInput) (*User, error)`, `UpdateUser(ctx, id string, in UpdateUserInput)`, `DeleteUser(ctx, id string) (string, error)`.
- Composable pieces, because apps set server-owned fields (author = the authenticated user): `(in *CreateUserInput) Entity(ctx) (User, error)` (IDs decoded and typed), `(in *UpdateUserInput) Apply(ctx, u velox.Update[User]) (velox.Update[User], error)`, `EdgeChanges(ctx, tx, id) error`.
- Input errors name their path as filter errors do (`CreateUserInput.groupIDs[3]`) and carry codes (finding 5). Delete of a missing or invisible row returns NOT_FOUND.
- All of it lives in `gen`. veloxgql gains no new public API.

**11. Should · otel/clean · There are two ways to configure the adapter.**
The only reason to use `Around(cfg)`/`TxAround(cfg)` (config.go:43-50) is to avoid `WithCallers`, and doing so builds two adapters. Use one entry point:
```go
type Config struct{ …; Callers CallerMode /* default on */ }
func Options(cfg Config) []velox.Option
```
Drop `Around` and `TxAround`. Also align the bool names (`OmitQueryText`, `RecordTenant`, `MetricFingerprint`) to one style. Breaking: yes.

**12. Should · otel/modern · No instrumentation-scope version or semconv schema URL.**
`tp.Tracer(scope)` and `mp.Meter(scope)` (config.go:76-77) leave both out. Because the semconv names are hand-written constants (spec §2.4), the schema URL is the only machine-readable record of which semconv release they follow. Without it, backends cannot translate them when DB semconv renames attributes.
Change: `trace.WithInstrumentationVersion(version)` and `trace.WithSchemaURL(schemaURL)` (the same for the meter), with `schemaURL` a constant in attrs.go next to the names. Breaking: no.

**13. Should · otel/correct · `db.client.operation.duration` includes application code under `Iter`.**
statement.go:42-44 times `next`, and `next` includes the `Iter` loop body (doc.go:20-22). Statement latency histograms and SLOs then mix database time with application time.
Change: record the metric from the start of the call to the first row, or to the end of reading rows. Or add `velox.iter=true` to the metric attributes and describe it. The span may keep covering the loop. Breaking: no.

**14. Should · otel/complete · Semconv attributes are missing.**
There is no `db.namespace`, `server.address`/`server.port`, or `db.response.status_code` (SQLSTATE or MySQL error number). The DB semconv list these for both spans and `db.client.operation.duration`. Retried serialization failures are `_OTHER` (known); a status code fixes that too.
Change: add `velox.DriverCode(err) (string, bool)` to core and `Config.Namespace`/`Server` to the adapter, or have velox.DB expose them. Breaking: no.

**15. Should · otel+graphql/clean · Import path and package name differ.**
`contrib/otel` is package `veloxotel`, so every import needs an alias (example/otel/main.go:21).
Change: rename the directory and module to `contrib/veloxotel`. For symmetry, consider `contrib/graphql` becoming `contrib/veloxgql` (holding the annotations `graphql`, `veloxgql` and `velox-graphql`). Breaking: yes (import path).

**16. Could · otel/complete · Add a statement filter and an extra-attributes hook (otelsql's `SpanFilter`, otelgorm's `WithAttributes`):**
`Config.Filter func(*velox.Statement) bool`, `Config.Attributes func(ctx, *velox.Statement) []attribute.KeyValue`. Breaking: no.

**17. Could · graphql/correct · Renames invalidate client data.** Document that `graphql.Type` renames change every ID and cursor clients hold, because both encode the type name.

**18. Could · maintain · Test-only dependencies leak into users' module graphs.** `modernc.org/sqlite` (contrib/graphql go.mod) and `otel/sdk` (contrib/otel) become minimum versions for every user. Move the e2e tests to a nested test module if this ever matters.

### Keep as is
- otel: the API-only dependency with written-out semconv names, plus the deps test; bounded metric attributes with the fingerprint opt-in; exemplar-friendly recording on the span's context; parenting statements under the `velox.tx` span; never recovering panics; client/internal span kinds; and the sampling and pool-metrics non-goals.
- graphql: the annotations package compiles only `schema`; typed opaque IDs that are never looked up when they are of the wrong type; versioned keyset cursors bound to type and order; lazy `totalCount`/page; error messages that never echo keys; the mutation-per-transaction rule with `data: null` on rollback (same as entgql's `Transactioner`); subscriptions running without a transaction (and out of scope); the plugin protocol's version checks (main.go:48-55).
- Batched loading (plan 4) should stay behind generated code and `gqlimpl`. A public DataLoader is not needed for 1.0, and none may run inside a mutation (known).

### Proposed order of work
1. Coupling first: findings 1, 2, 3, 8 (generated code changes once: version guard, `gqlimpl`, `LimitsFrom(ctx)`, `Pred(ctx)`).
2. Findings 4 and 5: `NewExtension` with options, coded errors and `ErrorPresenter`, used in the example.
3. Findings 6 and 7: composable root connection, directives on where inputs.
4. otel: findings 11, 12, 13, then 14 (needs core) and 15 (paths, together with any graphql rename).
5. Plan 3 against finding 10's shape, then plan 4 with the first/last-aware cost limit (known, Must before 1.0), and finding 9's gqlgen@latest CI job.
6. Could items as time allows.
