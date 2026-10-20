# GraphQL Plan 2: The Runtime — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A gqlgen server built on plan 1's SDL answers queries:
- `node`/`nodes`;
- root connections and edge connections, with `first`/`after`/`last`/`before`, `orderBy`, `where` and `totalCount`;
- every statement goes through velox, so tenants, policy, guards and routing apply.

**Architecture:**
- **`contrib/graphql/veloxgql`:** a new runtime package with opaque typed IDs, cursors, scalars, limits, the paginator, the where-input interpreter and the handler.
- **The plugin writes Go too:** one `gql_<label>.go` per entity in the `gen` package. These files hold:
  - the connection and edge types;
  - order and where types;
  - `IDGQL`/`IsNode`;
  - the edge methods;
  - the root connection functions;
  - the where table;
  - plus one `gql_node.go`.
- **gqlgen autobinds** to the `gen` package.
- **Mutations and batched loading come later:** mutations are plan 3, and batched loading of nested edges is plan 4. In this plan an edge connection runs one query per parent that asks for it.

**Tech Stack:** Go 1.27; `github.com/99designs/gqlgen` v0.17.86 (in `contrib/graphql` and `example` only); `github.com/vektah/gqlparser/v2`.

**Spec:** `docs/specs/2026-10-19-graphql-design.md`:
- §3: IDs, connections, ordering, filtering, scalars;
- §3: Transactions and the DB;
- §5: what the application writes;
- its "Rulings made in plan 1".

The follow-ups list "For GraphQL plan 2" in `docs/plans/2026-10-14-core-1-followups.md` is binding input.

## Global Constraints

- **Core rules.**
  - Go 1.27 generic methods: every method of a generic velox type is generic.
  - Nothing runs at program start. This applies to the generated `gen` code too: no package-level variable may be initialised by a function call.
  - No reflect in generic code.
  - The core's allocation budgets (`go test -run Budget -v .`) and the escape check are unchanged. The escape check is `go build -gcflags=-m . 2>&1 | grep "db.go:$(grep -n 'func (db \*DB) plain' db.go | cut -d: -f1):"`, which prints `leaking param content: st` and `read does not escape`.
- **Dependencies.**
  - gqlgen and gqlparser are allowed only in the `contrib/graphql` module and the `example` module.
  - The root module, `cmd/velox-gen` and the annotations package stay free of both.
- **Generated code.**
  - The plugin's Go files compile with velox-gen's.
  - `velox-gen verify` and `-check` cover them.
  - They are deterministic, and they are named `gql_*.go`.
  - Generated code calls velox only through its public API.
- **IDs (spec §3).**
  - An ID is base64url without padding of `<Type>:<key>`, where `<Type>` is the GraphQL type name and `<key>` is the key's canonical text.
  - Decoding an ID of another type fails with `veloxgql.ErrWrongIDType`. The error message names the expected type and never echoes the decoded key.
  - A malformed ID fails with `veloxgql.ErrBadID`.
- **Cursors.**
  - A cursor is base64url without padding of a versioned JSON document that holds the order values and the key.
  - A cursor that does not decode, or whose order does not match the query's order, fails with `veloxgql.ErrBadCursor`.
- **Limits.**
  - `veloxgql.MaxPage` is 100 and `veloxgql.MaxFilterDepth` is 5. Both are package variables with constant initialisers, documented as settable at start-up before serving.
  - `first` or `last` above `MaxPage`, or below 0, fails with `veloxgql.ErrPageTooLarge` or `ErrBadPagination`. Both `first` and `last` given also fails with `ErrBadPagination`.
- **No N+1 is promised in this plan.** Plan 4 adds batching. Tests may assert the per-parent queries.
- **CI.**
  - `scripts/ci-local.sh` and `.github/workflows/ci.yml` match line for line.
  - The example module's gqlgen output is checked to be up to date. The check reruns `go tool gqlgen generate` and diffs the result.
- **No machine-specific paths** in committed files. Set `GOCACHE=<a scratch directory>` and `GOFLAGS=-p=2` only in your shell.
- **Commits** end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.

## Rulings this plan makes on the spec

1. **No core `Before`.** The spec has the core gain `Before` for `last`/`before`. Instead, the paginator reverses each order term's direction, keysets with `After` on the reversed order, reads `n+1` rows, and reverses the page in memory. The core's API is unchanged. The spec gets a dated note.
2. **`totalCount` and the page are lazy.**
   - A connection is a value holding the prepared query.
   - `TotalCount(ctx)` runs `Count` when gqlgen calls it, which is only when the field is selected.
   - `Edges(ctx)` and `PageInfo(ctx)` run the page query once, shared through a `sync.Once` inside the connection.

   So a count-only selection runs no page query, and nothing walks the selection set for it.
3. **`Node` is bound to a velox interface,** `veloxgql.Noder` with `IsNode()` and `IDGQL() string`. Every Node entity gets both methods.
4. **IDs are `string` in Go.**
   - Generated code decodes them with `veloxgql.DecodeID[K](s, "<Type>")`, which returns the typed key.
   - `IDGQL()` encodes the key.
   - No custom ID scalar is needed: gqlgen's built-in `ID` stays `string`.
5. **Scalars.**
   - `Time`, `UUID`, `Bytes`, `JSON`, `Uint64` and `Cursor` are `veloxgql` types implementing `graphql.Marshaler`/`Unmarshaler`.
   - `Uint64` is a decimal string on the wire, both ways.
   - `JSON` binds to `json.RawMessage`, and the generated getter converts per field. One scalar cannot autobind every Go type (follow-up note).
   - `uint8`/`uint16`/`uint32` fields under `Int` get generated conversions.
6. **The DB comes from the context.**
   - `veloxgql.Handler(db)` stores the `*velox.DB` with `veloxgql.WithDB(ctx, db)`.
   - Generated code reads it with `veloxgql.DBFrom(ctx)`. A missing DB is an error naming the handler.
   - For a `mutation` operation, the handler opens `db.Tx` and stores the transaction's DB. It commits when the response has no errors, and rolls back otherwise or on a panic. Plan 3 exercises it.
7. **Where inputs are generated Go structs.** gqlgen binds input objects to Go structs, so structs are needed. Turning one into a `velox.Pred` goes through:
   - a generated method `(w *XWhereInput) Pred(ctx, depth int) (velox.Pred[X], error)`;
   - which calls generic helpers in `veloxgql`: `EQ`, `In`, `Contains`, `And`/`Or`/`Not` assembly and the depth check.

   The per-field code is one helper call per set predicate, with no logic. This replaces the spec's "table" with the smallest per-field code that gqlgen's binding allows. The spec gets a dated note.

## Review Focus

1. **A typed ID used in the wrong place.** Each of these fails with `ErrWrongIDType` and runs no query:
   - a `Post` ID passed to `node` where the stored key happens to exist as a User;
   - a `Post` ID in a `where` ID predicate of `User`;
   - a `Post` ID as a cursor key.

   Task 1's and Task 6's tests pin it.
2. **Paging edges.** Each of these returns no duplicates and no gaps, and sets `hasNextPage`/`hasPreviousPage` correctly:
   - `last: 2, before: <cursor of the first row>`;
   - `first: 0`;
   - a page exactly the size of the table;
   - an `orderBy` on a non-unique field with ties;
   - a cursor taken under `orderBy: NAME` reused with `orderBy: AGE`, which fails with `ErrBadCursor`.

   Task 3's table test pins them.
3. **Tenant and policy in GraphQL.** A tenant-scoped entity read through `node`, through a connection and through an edge, under a context with tenant A, never returns tenant B's rows. Without a tenant it fails with `ErrNoTenant`. Task 6's e2e test pins it.
4. **A `where` nested deeper than `MaxFilterDepth`** fails with `ErrFilterTooDeep` before any query runs. So do `and`/`or` lists with more than 100 entries (`veloxgql.MaxFilterWidth`). Task 4 pins it.
5. **Generated code that does not compile under gqlgen.** The e2e module runs real `gqlgen generate` against plan 1's SDL, plus `go build` and `go vet`. CI fails if the checked-in gqlgen output is stale. Task 6 pins it.

---

### Task 1: `veloxgql`: IDs, cursors, scalars, limits, errors

**Files:**
- Create `contrib/graphql/veloxgql/`:
  - `doc.go`;
  - `id.go`: `EncodeID`, `DecodeID[K]`, `Noder`;
  - `cursor.go`: `Cursor`, encode and decode;
  - `scalar.go`: `Time`, `UUID`, `Bytes`, `JSON`, `Uint64`;
  - `limits.go`: `MaxPage`, `MaxFilterDepth`, `MaxFilterWidth`;
  - `errors.go`.
- Test: `contrib/graphql/veloxgql/*_test.go`.
- Modify: `contrib/graphql/go.mod`, to add gqlgen v0.17.86.

**Interfaces:**
- Produces:
  ```go
  type Noder interface{ IsNode(); IDGQL() string }
  func EncodeID(typ string, key any) string // key: int64, string, uuid (16-byte array or string form), …
  func DecodeID[K any](id, typ string) (K, error)
  var (
      ErrBadID, ErrWrongIDType, ErrBadCursor, ErrPageTooLarge,
      ErrBadPagination, ErrFilterTooDeep, ErrNoDB error
  )
  type Cursor struct{ Values []any; Key any } // MarshalGQL/UnmarshalGQL
  func EncodeCursor(c Cursor) string
  func DecodeCursor(s string) (Cursor, error)
  type Time time.Time; type UUID [16]byte; type Bytes []byte; type JSON json.RawMessage; type Uint64 uint64 // each with MarshalGQL/UnmarshalGQL
  var MaxPage, MaxFilterDepth, MaxFilterWidth = 100, 5, 100
  type OrderDirection string // ASC, DESC; MarshalGQL/UnmarshalGQL
  type PageInfo struct{ HasNextPage, HasPreviousPage bool; StartCursor, EndCursor *Cursor }
  ```
  - The cursor JSON is `{"v":1,"o":[…values…],"k":<key>}`.
  - Times are RFC 3339 with nanoseconds.
  - A key's JSON keeps its type: a number for integers, a string otherwise.

- [ ] **Step 1: Write the failing tests.**
  - `TestIDRoundTrip`: covers `int64`, string and UUID keys.
  - `TestIDWrongType`: decoding a `Post:1` ID as `User` gives `ErrWrongIDType`, and the message does not contain `1`.
  - `TestIDMalformed`: bad base64, no colon, an empty type, or a key that does not parse as `K` each give `ErrBadID`.
  - `TestCursorRoundTrip`: times keep nanoseconds and their zone offset, and an `int64` above 2^53 keeps its exact value.
  - `TestCursorMalformed`.
  - `TestScalars`: each scalar marshals and unmarshals, invalid input is an error, and `Uint64` is a decimal string both ways.
  - `TestLimitsAreConstants`: `go vet` shows no package-level function call. In practice the test checks that the values exist and are documented.
- [ ] **Step 2: Run** `go -C contrib/graphql test ./veloxgql/`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go -C contrib/graphql test -race ./veloxgql/`, vet and lint. Expected: PASS.
- [ ] **Step 5: Commit** `veloxgql: typed IDs, cursors, scalars and limits`.

### Task 2: The plugin writes the Go types

**Files:**
- Create: `contrib/graphql/internal/gogen/`, which renders `gql_<label>.go` per Node or connection entity, and `gql_node.go`.
- Modify:
  - `contrib/graphql/cmd/velox-graphql/main.go`, to return the Go files with the SDL;
  - `contrib/graphql/internal/sdl` (if needed), to share the naming it already computes so the Go and the SDL cannot drift.
- Test:
  - `contrib/graphql/internal/gogen/render_test.go`, with goldens;
  - the e2e test through velox-gen, now compiling the result.

**Interfaces:**
- Consumes: plan 1's `plugin.Model` and the SDL naming; Task 1's `veloxgql`.
- Produces, in the `gen` package, per Node entity `X` with key type `K`:
  ```go
  func (*X) IsNode()
  func (x *X) IDGQL() string                    // veloxgql.EncodeID("<GraphQL type>", x.<KeyField>)
  type XConnection struct{ /* unexported: the query, args, once, page, err */ }
  func (c *XConnection) Edges(ctx context.Context) ([]*XEdge, error)
  func (c *XConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error)
  func (c *XConnection) TotalCount(ctx context.Context) (int, error)
  type XEdge struct{ Node *X; Cursor veloxgql.Cursor }
  type XOrder struct{ Direction veloxgql.OrderDirection; Field XOrderField }
  type XOrderField string // the enum values; MarshalGQL/UnmarshalGQL
  type XWhereInput struct{ Not *XWhereInput; And, Or []*XWhereInput; /* one pointer field per predicate, named as gqlgen derives from the SDL name */ }
  ```
  - All of these use the GraphQL type names, so `Type("Account")` gives `AccountConnection`.
  - `gql_node.go` holds `func Node(ctx context.Context, id string) (veloxgql.Noder, error)` and `func Nodes(ctx context.Context, ids []string) ([]veloxgql.Noder, error)`. They decode the type from the ID and query that entity once. `Nodes` sends one query per type present and keeps the input order, with nil for a missing ID.
  - Methods, connection functions and where `Pred` come in Tasks 3 and 4. This task writes types and stubs only where those tasks need hooks.

- [ ] **Step 1: Write the failing tests.**
  - `TestGoGolden`: the plan 1 testdata model renders the golden Go.
  - `TestGoCompiles` (e2e): velox-gen with `-plugin velox-graphql` writes `gen`, then `go build ./gen` and `go vet ./gen` pass.
  - `TestNamesMatchSDL`: every Go type the plugin writes has the name of the SDL type it binds to, and every SDL object, input and enum without a Go type is listed and justified. This is the check that gqlgen autobind will find each one.
- [ ] **Step 2: Run** `go -C contrib/graphql test ./...`. Expected: FAIL.
- [ ] **Step 3: Implement.** The generated code reads like hand-written Go: gofmt'd, with doc comments on every exported name.
- [ ] **Step 4: Run** `go -C contrib/graphql test -race ./...`, vet and lint. Expected: PASS.
- [ ] **Step 5: Commit** `contrib/graphql: the plugin writes the Go types gqlgen binds to`.

### Task 3: Connections and the paginator

**Files:**
- Create: `contrib/graphql/veloxgql/paginate.go`, `veloxgql/db.go` (`WithDB`, `DBFrom`).
- Modify: `contrib/graphql/internal/gogen/`. It adds the root connection functions, the `<Edge>GQL` methods (connection and list and unique edges) and the order mapping.
- Test:
  - `contrib/graphql/veloxgql/paginate_test.go`, on SQLite through velox with a small hand-written `Desc` entity;
  - gogen goldens.

**Interfaces:**
- Produces:
  ```go
  type Args struct{ After, Before *Cursor; First, Last *int }
  type OrderTerm[E any] struct{ /* a velox column handle and the direction */ }
  func Paginate[E any](ctx context.Context, q velox.Query[E], args Args, order []OrderTerm[E], key func(*E) any, values func(*E) []any) (*Page[E], error)
  type Page[E any] struct{ Rows []*E; Info PageInfo; Cursors []Cursor }
  func WithDB(ctx context.Context, db *velox.DB) context.Context
  func DBFrom(ctx context.Context) (*velox.DB, error)
  ```
  - The exact generic shape is the builder's choice, within the core rules (velox's `Query` methods are generic, so `Paginate` is a function, not a method).
  - Generated per entity:
    - `func XsConnection(ctx, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy …, where *XWhereInput) (*XConnection, error)`. The parameters are in the SDL's argument order, and `orderBy`/`where` are present only when the SDL has them.
    - `func (x *X) <Edge>GQL(ctx, …same arguments…) (*TConnection, error)`.
    - `(*T, error)` for a unique edge, and `([]*T, error)` for `Connection(false)`.
    - These query the target filtered by the relation (`Rel.Has`/the key) and use the loaded relation when it was loaded with `With` and no arguments. Plan 4 extends that.
  - Paging (Ruling 1):
    - the order always ends with the key;
    - `before`/`last` reverse every term's direction and use `After`, then reverse the rows;
    - `n+1` rows give `hasNextPage` (forward) or `hasPreviousPage` (backward);
    - with neither `first` nor `last`, the page size is `MaxPage`.

- [ ] **Step 1: Write the failing tests.** `TestPaginate` is a table over 10 rows with names that tie. It covers:
  - `first` 3, then `after` each cursor;
  - `last` 3, then `before` each cursor;
  - `first` 0;
  - `first` 10 and 11;
  - order by name ASC and DESC with ties;
  - a reused cursor under another order, which gives `ErrBadCursor`;
  - `first` with `last`, `first: 101` and `first: -1`, which give the errors.

  Each case asserts the exact rows, `hasNextPage`/`hasPreviousPage`, start and end cursors, and that the statements run (seen through an Around hook) are one page query, plus one `COUNT` only when `TotalCount` was called. These are Review Focus 2's cases.
  - `TestDBFrom`: no DB gives `ErrNoDB`.
  - gogen goldens for the connection functions and edge methods.
- [ ] **Step 2: Run** the tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go -C contrib/graphql test -race ./...`, vet and lint. Expected: PASS.
- [ ] **Step 5: Commit** `veloxgql: keyset connections, lazy totalCount, edge methods`.

### Task 4: Where inputs

**Files:**
- Create: `contrib/graphql/veloxgql/where.go`, with the generic predicate helpers and the depth and width checks.
- Modify: `contrib/graphql/internal/gogen/`, which writes `(w *XWhereInput) Pred(ctx context.Context, depth int) (velox.Pred[X], error)`.
- Test:
  - `contrib/graphql/veloxgql/where_test.go`;
  - an SQLite test of every operation through a generated where input (in gogen's e2e).

**Interfaces:**
- Consumes: Task 2's `XWhereInput` and Task 1's `DecodeID`.
- Produces:
  - `func (w *XWhereInput) Pred(ctx, depth) (velox.Pred[X], error)`. It handles `not`/`and`/`or`, every predicate from plan 1's SDL, `has<Edge>` (`Rel.Has`) and `has<Edge>With` (`Rel.HasWhere` over the target's `Pred`).
  - An ID predicate decodes with `DecodeID`. A foreign-key `<edge>ID…` predicate decodes as the target's ID.
  - Helpers in `veloxgql`, with names the builder chooses: `CheckDepth(depth int) error`, `CheckWidth(n int) error`, and the assembly of `And`/`Or`/`Not` that turns a nil or empty input into "no condition".

- [ ] **Step 1: Write the failing tests.**
  - `TestWhereOps`: every operation of every field type in the plan 1 testdata, on rows that make each predicate select a known subset.
  - `TestWhereLogic`: `and`, `or`, `not`, nesting, and an empty input meaning all rows.
  - `TestWhereDepthAndWidth`: depth 6 gives `ErrFilterTooDeep`, and an `or` of 101 gives an error. In both, no statement runs.
  - `TestWhereIDs`: a wrong-type ID gives `ErrWrongIDType` and no statement runs.
  - `TestHasEdge`: `hasPosts: true/false` and `hasPostsWith: [{titleContains: "x"}]`.
- [ ] **Step 2: Run** the tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go -C contrib/graphql test -race ./...`, vet and lint. Expected: PASS.
- [ ] **Step 5: Commit** `veloxgql: where inputs become velox predicates`.

### Task 5: The handler

**Files:**
- Create: `contrib/graphql/veloxgql/handler.go`, with `Handler(db *velox.DB) graphql.HandlerExtension`.
- Test: `contrib/graphql/veloxgql/handler_test.go`, using gqlgen's `handler.New` with a tiny executable schema built in the test (or the e2e module's).

**Interfaces:**
- Produces: `func Handler(db *velox.DB) interface{ graphql.HandlerExtension; graphql.OperationInterceptor; graphql.ResponseInterceptor }` (Ruling 6), or the gqlgen interfaces it needs. It does two things:
  - every operation's context gets the DB;
  - a `mutation` operation runs in one `db.Tx`. It commits when the response has no errors, and rolls back when there are errors or on a panic. Mutations execute serially in gqlgen, so a single transaction is safe.

- [ ] **Step 1: Write the failing tests.**
  - `TestHandlerPutsTheDB`.
  - `TestMutationCommits`: a mutation resolver that inserts, then succeeds, leaves the row.
  - `TestMutationRollsBack`: a resolver that inserts and then returns an error leaves no row. So does a resolver that panics.
  - `TestQueryHasNoTx`: a query's statements have `InTx` false, seen through an Around hook.
- [ ] **Step 2: Run** the tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go -C contrib/graphql test -race ./...`, vet and lint. Expected: PASS.
- [ ] **Step 5: Commit** `veloxgql: the handler puts the DB in the context and a mutation in a transaction`.

### Task 6: `example/graphql` end to end

**Files:**
- Create `example/graphql/`, with:
  - `schema/` (User, Post with tenant, Tag M2M, an enum, a time, a sensitive field);
  - `gen/` (velox-gen with the plugin);
  - `gqlgen.yml`;
  - `graph/` (gqlgen output, checked in, plus `resolver.go`/`schema.resolvers.go` with the one-line root resolvers);
  - `main.go`;
  - `main_test.go`;
  - `README.md`.
- Modify:
  - `example/go.mod`: gqlgen as a tool and dependency; the `velox-graphql` tool;
  - `scripts/ci-local.sh` and `.github/workflows/ci.yml`: the example loop gains `graphql` with `-plugin velox-graphql`, and a step reruns `gqlgen generate` in `example/graphql` and fails on a diff;
  - `example/README.md`;
  - `docs/specs/2026-10-19-graphql-design.md`: dated notes for Rulings 1 and 7;
  - `docs/plans/2026-10-14-core-1-followups.md`: tick the "For GraphQL plan 2" items done, and keep the rest.

**Interfaces:**
- Consumes: everything above.
- `gqlgen.yml`:
  - `schema: [gen/*.graphqls, graph/*.graphqls]`;
  - `autobind: [<module>/graphql/gen]`;
  - `models` for `Node` → `veloxgql.Noder`, and for `Time`, `UUID`, `Bytes`, `JSON`, `Uint64`, `Cursor`, `OrderDirection` and `PageInfo` → `veloxgql`.

- [ ] **Step 1: Write the tests** in `main_test.go`. They run against an `httptest` server on SQLite, sending real GraphQL documents:
  - `node` for each type;
  - `nodes` with a missing ID (null in place);
  - a wrong-type ID, which gives an error;
  - a root connection paged forward and backward;
  - `orderBy` and `where`;
  - a nested edge connection;
  - `totalCount` only;
  - tenant isolation (Review Focus 3), with the tenant set by a test middleware;
  - a sensitive field absent from the schema (introspection).
  - Each test asserts the JSON response exactly, and asserts the statements sent, through an Around hook.
- [ ] **Step 2: Generate.** Run `go -C example tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema`, then `go -C example/graphql tool gqlgen generate`. Write the resolvers. Run the tests. Expected: PASS.
- [ ] **Step 3: Run the full gate** (`fix1.sh`, updated for the example and the gqlgen check). Expected: every step rc=0.
- [ ] **Step 4: Commit** `example/graphql: a gqlgen server on the generated schema`.
