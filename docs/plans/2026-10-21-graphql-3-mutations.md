# GraphQL Plan 3: Mutations — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Generate working GraphQL mutations:
- `createX`, `updateX` and `deleteX`, opt-in with `graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete())`;
- their edges: a unique edge set by its ID, and the IDs of a to-many edge added, removed or cleared;
- each one atomic, inside the operation's transaction or in its own;
- `gen.CreateUser(ctx, input)` and friends, which take the DB from the context, so the application's resolver is one line.

**Architecture:** velox-graphql already writes the mutation inputs and the root `Mutation` in `velox.graphqls` (plan 1). This plan writes the Go behind them. `sdl.Build` exposes the inputs in the `Surface`, so the Go and the SDL take every name from one place. `gogen` writes, in each entity's `gql_<label>.go`, the Go structs `CreateXInput` and `UpdateXInput` and the functions `CreateX`, `UpdateX` and `DeleteX`. Those functions call a few new helpers in `gqlimpl`: decoding IDs, checking that the rows an input names exist for the caller, coded `NOT_FOUND` errors, validation errors that name the input field, and `Atomic`, which runs a mutation in the operation's transaction or in one of its own. Every write goes through velox's write path, so hooks, the tenant scope, policies, validators and guards apply unchanged. The core does not change.

**Tech Stack:** Go 1.27; gqlgen v0.17.86 and gqlparser v2.5.59, unchanged in every `go.mod`. The e2e app module gains the MySQL and pgx drivers, which the root module already requires.

**Spec:** `docs/specs/2026-10-19-graphql-design.md`: §3 "Mutations" and "Transactions and the DB", §5, §6, "Rulings made in plan 1" (Inputs and Refusals) and every dated note. Also `docs/specs/2026-10-20-api-cleanup-design.md` §3.1 and §6a–6c. Follow-ups: "For GraphQL plan 3", "Left by GraphQL plan 2" and "Left by API plan 3" in `docs/plans/2026-10-14-core-1-followups.md`. Where the spec is silent, old velox's `contrib/graphql` (`graphqlgen/mutation_input.go`) is the semantic reference. GraphQL plan 4's items stay with it: collection, `LimitEach`, the request cost limit and caps on `With` loads.

## Global Constraints

- **No aliases.** No renamed or removed API keeps an alias or a deprecated copy. Every caller moves in the same commit: `contrib/graphql` (goldens, e2e tests), `example/graphql` (`gen/`, `graph/`, `main.go`, tests, README) and every doc snippet.
- **The core does not change.** `GeneratedCodeIsVersion4` stays 4. velox-gen's output for a schema without GraphQL is unchanged.
- **`veloxgql.GeneratedCodeIsVersion1` stays 1.** The generated code gains calls to new `gqlimpl` names; code generated before this plan still compiles against the new `veloxgql`. Bump it only if a task would break old generated code, and stop and report if one does.
- **`veloxgql`'s public API does not change.** `TestPublicAPI`'s list stays as it is. Only `CodeNotFound`'s doc changes, since something now returns it. New names go in `gqlimpl`, for generated code only.
- **Generated code imports** the standard library, `velox`, `veloxgql`, `gqlimpl` and the application's own type packages, and nothing else (no gqlgen, no gqlparser).
- **The binding rules for inputs** (§3 Inputs, plan 1's rulings, the API cleanup spec §3.1):
  - A create input's field is optional when the schema field is `Optional`, `Nillable` (`Nullable` in the model), or has a `Default` or `DefaultFunc`. Otherwise it is required.
  - An update input's field is always optional; `nil` means unchanged. It gets `clear<Field>` only when the column is NULL-able (`plugin.Field.Nullable`).
  - A non-`Nillable` bool or number with a `Default` is refused (velox-gen refuses it; velox-graphql refuses it again for a hand-written model).
  - Immutable fields are absent from update inputs. UpdateDefault fields follow plan 1's ruling.
  - Sensitive fields are accepted as input and never output. A generated input's `String()` leaves them out.
  - The tenant is in no input. The tenant scope fills it on insert and scopes every other statement.
  - Validators run through the generated `Check`, inside velox's `Insert` and `Update`.
- **Errors.** Every input error is coded before the presenter sees it, or is one the presenter already maps:
  - a malformed or wrongly typed ID, a conflicting input, too many IDs: `BAD_USER_INPUT`, with the input field's path;
  - a validator's refusal: `BAD_USER_INPUT`, naming the input field;
  - an ID that names no row the caller may see or change: `NOT_FOUND`;
  - `velox.ConstraintError`, `ErrNoTenant` and `ErrDenied`: as `ErrorPresenter` maps them today.
- **Budgets and escape check unchanged.**
  - `go test -run Budget -v .`: one-row 6, tenant 7, hundred rows 105, tx 9/6, observed 7/6, metadata 19, Insert 13, replica one-row 6.
  - `go build -gcflags=-m . 2>&1 | grep "db.go:$(grep -n 'func (db \*DB) plain' db.go | cut -d: -f1):"` prints `leaking param content: st` and `read does not escape`.
  - A task that moves a number stops and reports it.
- **The full gate passes at every task's commit:** `<scratchpad>/fix1.sh`, run on the committed tree, with Docker. If golangci-lint's lock is held, run it with `--allow-parallel-runners`. Also run `scripts/gqlgen-latest.sh` in every task that changes `example/graphql`.
- **CI.** `scripts/ci-local.sh` and `.github/workflows/ci.yml` stay identical in what they run. Task 6 changes both together.
- **No machine-specific paths** in committed files. Committed text says `<scratchpad>`.
- **Shell only:** `GOCACHE=<tmp>/velox2-gocache GOFLAGS=-p=2`, the shared cache the gate uses. The machine is short of memory and loaded: one heavy command at a time. Do not kill other processes.
- **Regenerating the example** is `(cd example && go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema && go -C graphql tool gqlgen generate)`. Commit `gen/` and `graph/` together.
- **Commits** end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.

## Review Focus

1. **An ID that names another tenant's row, or one a policy hides.** `createPost(input: {authorID: …, tagIDs: […]})`, `addPostIDs` and `deletePost(id)` with an ID of a row in another tenant, or one the Read policy hides. Join rows are not tenant-scoped (`velox.EdgeThrough`'s doc), so velox does not check them. The mutation must fail with `NOT_FOUND` and write nothing; `deletePost` of another tenant's post must leave that post's join rows in place. Tasks 3, 4 and 5 pin this in `TestEdgeScope`, and Task 7 in `TestDeleteOtherTenant`.
2. **A conflicting update input.** `{publishedAt: …, clearPublishedAt: true}`, `{authorID: …, clearAuthor: true}`, and an ID in both `addTagIDs` and `removeTagIDs`. Each is refused with `BAD_USER_INPUT` before any statement is sent: there is no reading that both parts can have. Task 4 pins this in `TestUpdateConflicts`.
3. **Repeated and already-linked IDs.** `tagIDs: [a, a]` links once. `addTagIDs: [a]` when `a` is already linked succeeds and changes nothing. `addPostIDs: [p, p]` does not fail with a false `NOT_FOUND` because one row matched twice. Tasks 3 and 4 pin this in `TestRepeatedIDs`.
4. **A failure after the first write.** `createPost` whose second tag ID names no row has already inserted the post. Nothing may be kept, both inside the extension's transaction and when the mutation runs outside one: called from a job with `Extension.Context`, or in an operation `WithTxFilter` leaves out. Tasks 2 and 3 pin this in `TestAtomic`.
5. **A huge ID list.** `addTagIDs` with 10,000 IDs. It is refused with `BAD_USER_INPUT` (`ErrPageTooLarge`) before any statement, as `nodes(ids:)` is: the cap is the `MaxPage` of `veloxgql.LimitsFrom(ctx)`. Task 2 pins this in `TestDecodeIDsCap`, and Task 3 through the generated code.

---

### Task 1: The inputs in the Surface, with their directives

**Files:**
- Modify: `contrib/graphql/internal/sdl/surface.go`, `input.go`, `render.go` (the input rendering), `rules.go`.
- Modify: `contrib/graphql/internal/sdl/input_test.go`, `surface_test.go`, `rules_test.go`, and the goldens `internal/sdl/testdata/velox.graphqls` (regenerated with the test's `-update` flag).
- Modify: `contrib/graphql/cmd/velox-graphql/e2e_test.go` (expected SDL lines).
- Modify: `contrib/graphql/doc.go` (directives reach the inputs).

**Interfaces:**
- In `sdl`, `Type` gains:

```go
	// Create and Update are the fields of CreateXInput and UpdateXInput, in
	// the SDL's order; nil when the type has no createX or no updateX.
	Create, Update []InputField
	// Delete reports whether the type has deleteX.
	Delete bool
```

```go
// InputKind is what a field of a mutation input does.
type InputKind int

const (
	InputValue     InputKind = iota // <field>: the field's value
	InputClear                      // clear<Field>: the field's column to NULL
	InputEdgeID                     // <edge>ID: a unique edge's node
	InputEdgeIDs                    // <edge>IDs, on create: a to-many edge's nodes
	InputAddIDs                     // add<Edge>IDs
	InputRemoveIDs                  // remove<Edge>IDs
	InputClearEdge                  // clear<Edge>
)

// InputField is a field of a mutation input.
type InputField struct {
	Name     string    // the GraphQL name, as "publishedAt"
	GoName   string    // the Go struct field gqlgen binds it to, ignoring case: goOf's rule
	Type     string    // the GraphQL type, as "Time" or "[ID!]"
	Kind     InputKind
	Field    int       // InputValue, InputClear: the index in Entity.Fields; else -1
	Edge     int       // the edge kinds: the index in Entity.Edges; else -1
	Target   string    // the edge kinds: the GraphQL type of the IDs; else ""
	GoType   string    // InputValue: the Go type of the value, as WhereField.GoType gives it; "veloxgql.JSON" for JSON
	GoPkg    string    // the import path GoType names; "" for none
	Required bool      // non-null in GraphQL: a value in Go, not a pointer
}
```

- `Type.Create` is set when the entity's `Mutations` lists create; `Type.Update` when it lists update and the update input has fields (plan 1 writes no `updateX` for an empty one); `Type.Delete` when it lists delete.
- **Directives on inputs.** A field's `graphql.Directives` go on its `InputValue` and `InputClear` fields. An edge's go on its edge input fields. `@goField` is never added to an input field. Use `directives` and `unite` from `where.go`.
- **A defensive refusal** in `rules.go`, for a hand-written model: a field of type bool or a number type that has `HasDefault` and is not `Nullable` fails with `schema X, field y: a Default on a bool or number that is not Nillable cannot be told from unset; velox-gen refuses it too`.

- [ ] **Step 1: Write the failing tests.**
  - `TestSurfaceInputs` in `surface_test.go`, over the testdata model:
    - a type with create, update and delete has `Create`, `Update` and `Delete` set, and a type without mutations has none;
    - each `InputField` has the name, type and kind of the SDL's line, in the SDL's order, and `Required` matches a trailing `!`;
    - a nullable field gives an `InputValue` and an `InputClear` with the same `Field`;
    - an M2M edge gives `InputEdgeIDs` on create and `InputAddIDs`, `InputRemoveIDs` and `InputClearEdge` on update, all with `Target` set.
  - `TestInputsCarryDirectives` in `input_test.go`: a field with `@admin` gives `@admin` on `name`, and on `clearName` when it is nullable; an edge with `@admin` gives it on `<edge>ID`, `add<Edge>IDs`, `remove<Edge>IDs` and `clear<Edge>`.
  - `TestDefaultOnNonNillableNumber` in `rules_test.go`: a hand-written model with an `int` field that has `HasDefault` and no `Nullable` fails with the message above. A `Nullable` one passes.
  - `TestSurfaceMatchesSDL`: every input object that `velox.graphqls` declares has its fields, by name and type, in `Type.Create` or `Type.Update`.
- [ ] **Step 2: Run them.** Run `(cd contrib/graphql && go test ./internal/sdl)`. Expected: FAIL (`Type` has no field `Create`).
- [ ] **Step 3: Implement.** `inputFields` and `edgeInputs` already compute each field; record its kind, field or edge index and target in the `object`, and copy them into `Type` in `Build`. Take `GoType` and `GoPkg` from `goValue`, as the where input does. Regenerate the SDL golden with `-update` and read the diff: only directives on input fields change.
- [ ] **Step 4: Run the module tests.** Run `(cd contrib/graphql && go test ./...)`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0 for every step.
- [ ] **Step 6: Commit** `GraphQL: the mutation inputs in the Surface; field directives reach them`.

### Task 2: The runtime of mutations in `gqlimpl`

**Files:**
- Create: `contrib/graphql/veloxgql/gqlimpl/mutation.go`, `mutation_test.go`.
- Modify: `contrib/graphql/veloxgql/internal/shared/error.go` (a `NOT_FOUND` constructor), and create `internal/shared/tx.go` (the operation's transaction mark).
- Modify: `contrib/graphql/veloxgql/extension.go` (it marks the transaction it opens), `extension_test.go`.
- Modify: `contrib/graphql/veloxgql/presenter.go` (`CodeNotFound`'s doc only), `gqlimpl/doc.go`.

**Interfaces:**
- In `shared`:
  - `const CodeNotFound = "NOT_FOUND"`, which `veloxgql.CodeNotFound` becomes equal to (`veloxgql.CodeNotFound = shared.CodeNotFound`, as `CodeBadUserInput` is).
  - `func NotFoundf(format string, args ...any) error`: a `*Error` with `CodeNotFound`, built as `InputErrorf` builds its error.
  - `func WithOperationTx(ctx context.Context, tx *velox.DB) context.Context` and `func OperationTx(ctx context.Context) *velox.DB`. The extension calls the first in `InterceptResponse`, with the transaction's DB, beside `withState`.
- In `gqlimpl`, for generated code only:

```go
// Atomic runs fn with the DB veloxgql.DBFrom(ctx) reads. When that DB is the
// transaction the Extension opened for a mutation operation, fn runs in it;
// otherwise fn runs in db.Tx(ctx, fn), so a mutation's writes are kept or
// dropped together outside a server too.
func Atomic(ctx context.Context, fn func(ctx context.Context, db *velox.DB) error) error

// DecodeID decodes id, an ID of the GraphQL type typ, for the input field
// at ("CreatePostInput.authorID", "updatePost.id"). Its error is
// BAD_USER_INPUT, names at, and wraps veloxgql.ErrBadID or ErrWrongIDType.
func DecodeID[K any](at, id, typ string) (K, error)
func DecodeIDAs[K, V any](at, id, typ string, conv func(K) V) (V, error)

// DecodeIDs decodes ids as DecodeID does, keeps the first of each repeated
// key, and refuses more than the MaxPage of veloxgql.LimitsFrom(ctx) IDs
// with BAD_USER_INPUT wrapping veloxgql.ErrPageTooLarge, before decoding any.
func DecodeIDs[K comparable](ctx context.Context, at string, ids []string, typ string) ([]K, error)
func DecodeIDsAs[K comparable, V any](ctx context.Context, at string, ids []string, typ string, conv func(K) V) ([]V, error)

// Exist fails with NOT_FOUND, naming at and typ, unless q counts n rows:
// q selects the rows the IDs of at name, under the caller's tenant and
// Read policy.
func Exist[E any](ctx context.Context, q velox.Query[E], n int, at, typ string) error

// NotFound is the NOT_FOUND error of a mutation whose ID names no row of
// typ the caller may change: "veloxgql: <at>: no <typ> has this ID".
func NotFound(at, typ string) error

// WriteError is err from a write of entity (the name velox reports, as
// "gen.Post") made from the input named input. A *velox.ValidationError of
// entity on a field name gives a GraphQL name for becomes BAD_USER_INPUT
// "<input>.<name>: invalid value: <the validator's text>", wrapping err.
// Every other error is returned as it is.
func WriteError(err error, input, entity string, name func(goField string) string) error

// FromJSON decodes j into a T for the input field at; on failure, a
// BAD_USER_INPUT error naming at.
func FromJSON[T any](at string, j veloxgql.JSON) (T, error)
```

- `Exist`'s message is `veloxgql: <at>: <missing> of the <n> IDs name no <typ>`; `NotFound`'s is above. Neither echoes an ID or a key.
- `veloxgql.CodeNotFound`'s doc says: the generated mutations return it for an ID that names no row the caller may see or change.

- [ ] **Step 1: Write the failing tests** in `mutation_test.go`, on SQLite, with a small hand-written entity as `veloxgql`'s `person_test.go` has:
  - `TestAtomic` (Review Focus 4):
    - under `NewExtension(db).Context(ctx)`, a fn that inserts a row and then returns an error leaves no row: `Atomic` opened its own transaction;
    - inside a mutation served by the extension, `Atomic`'s fn gets the operation's transaction DB, and the test's `TxAround` hook sees one transaction for the operation, not two;
    - with `WithTxFilter` returning false, the fn runs in a transaction of its own, and a failing fn leaves no row.
  - `TestDecodeID`: a malformed ID and an ID of another type each fail with `BAD_USER_INPUT`, a text that starts `veloxgql: CreatePostInput.authorID: `, and `errors.Is` finds `ErrBadID` or `ErrWrongIDType`.
  - `TestDecodeIDsCap` (Review Focus 5): 101 IDs under the default limits fail with `ErrPageTooLarge` and code `BAD_USER_INPUT`; 100 pass. Under `WithLimits(Limits{MaxPage: 2})`, 3 fail.
  - `TestDecodeIDsRepeats`: `[a, b, a]` gives the keys of `[a, b]`.
  - `TestExist`: two IDs of which one names no row fail with `NOT_FOUND` and `1 of the 2 IDs name no Post`; a row of another tenant counts as none.
  - `TestWriteError`:
    - a `*velox.ValidationError{Entity: "gen.Post", Field: "Title", Err: errors.New("must not be empty")}` gives `BAD_USER_INPUT` and `veloxgql: CreatePostInput.title: invalid value: must not be empty`, and `errors.As` still finds the `*velox.ValidationError`;
    - the same error of another entity, or of a field the name function returns `""` for, is returned unchanged;
    - a `*velox.ConstraintError` is returned unchanged.
  - `TestNotFoundPresented` in `veloxgql`'s `presenter_test.go`: `ErrorPresenter` shows `gqlimpl.NotFound("updatePost", "Post")` as code `NOT_FOUND` and message `updatePost: no Post has this ID`, and does not log it.
  - `TestPublicAPI` runs unchanged.
- [ ] **Step 2: Run them.** Run `(cd contrib/graphql && go test ./veloxgql/...)`. Expected: FAIL (undefined names).
- [ ] **Step 3: Implement.** `Atomic` compares `shared.OperationTx(ctx)` with the DB `DBFrom` returns. `Exist` uses `q.Count(ctx)`. `WriteError` uses `errors.As`.
- [ ] **Step 4: Run the module tests.** Run `(cd contrib/graphql && go test -race ./...)`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `GraphQL: gqlimpl's runtime for mutations: Atomic, IDs, NOT_FOUND and named validation errors`.

### Task 3: `CreateXInput` and `CreateX`

**Files:**
- Create: `contrib/graphql/internal/gogen/mutation.go` (the writer of inputs and mutations; Tasks 4 and 5 extend it).
- Modify: `contrib/graphql/internal/gogen/render.go` (`typeFile` calls it), `render_test.go`, and the goldens in `internal/gogen/testdata/golden`.
- Create: `contrib/graphql/cmd/velox-graphql/mutation_e2e_test.go`.

**Interfaces:**
- For each type with `Create`, in its `gql_<label>.go`:

```go
// CreatePostInput is the input of createPost.
type CreatePostInput struct {
	Title       string     // a required field: its value
	Status      *PostStatus // an optional one: nil means not given
	PublishedAt *time.Time
	AuthorID    string     // a required unique edge
	TagIDs      []string   // a to-many edge
}

// String returns the input's fields except its sensitive ones.
func (in CreatePostInput) String() string
func (in CreatePostInput) GoString() string

// CreatePost creates one Post from input, with its edges, and returns it.
func CreatePost(ctx context.Context, input CreatePostInput) (*Post, error)
```

- The names follow the GraphQL type: `CreateLabelInput` and `CreateLabel` for `Tag` shown as `Label`. The renderer's `declare` refuses a clash. Two fields of one input whose Go names are equal ignoring case are refused: gqlgen could not tell them apart.
- A field's Go type is `InputField.GoType`, a pointer when not `Required`. An ID is `string`, an ID list `[]string`.
- **What `CreateX` does,** in this order:
  1. Decode every ID of the input with `DecodeID`/`DecodeIDs` (`…As` when the key field's Go type is not the target's key type, with the `As*` conversion `idCall` picks), and every JSON field with `FromJSON`. Any failure returns before a statement.
  2. In `gqlimpl.Atomic`:
     - for each unique edge that `OwnsKey` and is given: `Exist(ctx, db.Query[T]().Where(T.ID.In(k)), 1, …)`, then set the key on the entity;
     - for each M2M edge given: `Exist` over its IDs;
     - build the entity: a required field from its value; an optional one only when not nil (a nil leaves the zero value, which `Insert` fills from a `Default`); a `Nillable` field takes the pointer;
     - `db.Insert(&x).Exec(ctx)`, its error through `WriteError(err, "CreatePostInput", "<pkg>.Post", postFieldGQL)`;
     - an O2O edge whose key the target holds: `db.Update[T]().Set(T.Key.Set(x.ID)).Where(T.ID.EQ(t)).Exec(ctx)`, and `NotFound` when it matched 0 rows;
     - an O2M edge: `db.Update[T]().Set(T.Key.Set(x.ID)).Where(T.ID.In(ids...)).Exec(ctx)`, and `Exist`'s error when it matched fewer rows than `len(ids)`;
     - an M2M edge: `db.InsertAll(rows).OnConflict(J.Owner, J.Target).DoNothing().Exec(ctx)`, one join row per ID.
  3. Return `&x`.
- **The join columns.** The owner's column in the join entity is its `Fields[0]` for an edge with `Inverse` false and `Fields[1]` for one with `Inverse` true; the target's is the other. This is the order velox-gen's `EdgeThrough` uses (`internal/gen/lower.go`). Task 3's `TestCreateM2MBothSides` pins it.
- `<label>FieldGQL(goField string) string` is an unexported function of the entity's file: a switch from a field's Go name to its GraphQL name, `""` for a field in no input. Task 4's update uses it too.
- A key whose Go type differs from the owner's (an `ids.UserID` key field holding an `int64` key) converts with a Go conversion, checked by `r.converts`.

- [ ] **Step 1: Write the failing tests.**
  - In `render_test.go`:
    - `TestCreateInputs`: each type with `Create` has `type Create<T>Input struct` with one Go field for each `InputField`, a pointer exactly when not `Required`, and `func Create<T>(ctx context.Context, input Create<T>Input) (*<Entity>, error)`;
    - `TestInputStringHidesSensitive`: a sensitive field's Go name is not in the body of the input's `String`.
  - In `mutation_e2e_test.go`: `e2eMutSchema`, a schema of its own whose tables share no name with `internal/itest`'s:
    - `Writer`: tenant `org_id`; `name` with `NotEmpty()`; `bio`, `Optional().Nillable()`; `nick`, `Optional().Default("anon")`; `score`, an `Int` with `Optional()`; `password`, `Sensitive()`; `meta`, an `Optional` JSON field; edges `articles` (O2M), `circles` (M2M), `profile` (O2O, its key on `Profile`); `Mutations(Create(), Update(), Delete())`.
    - `Article`: `title`; edge `writer` (M2O, `writer_id` `Optional().Nillable()`); `Mutations(Create(), Update(), Delete())`. A hand-written `gen/article_policy.go` declares `Policy()`: `Insert` refuses the title `forbidden`, and `Write` is `Articles.Title.NEQ("locked")`.
    - `Circle`: `name` with `Unique()`; edge `writers`, the inverse of `Writer.circles`; `Mutations(Create(), Update(), Delete())`.
    - `Profile`: `handle`; edge `writer`, the inverse of `Writer.profile`, `Unique()` with `Field("writer_id")`, `Optional().Nillable()`; `Mutations(Create())`.
  - `e2eMutRun`, the app's test file, with a `targets(t)` that gives SQLite only for now and a DDL per target. Each target's DB records the statements it sends with a `velox.WithAround` hook, as `example/graphql`'s test server does. Its tests:
    - `TestCreate`: `CreateWriter` under tenant 7 with `nick` nil, `score` nil, `password` set, two circle IDs and one article ID gives `nick` `anon`, `score` 0, `org_id` 7, the article re-pointed, two join rows; `fmt.Sprint(input)` does not contain the password;
    - `TestCreateM2MBothSides`: `CreateCircle` with `writerIDs` and `CreateWriter` with `circleIDs` each write join rows that `Writer.circles` loads;
    - `TestRepeatedIDs` (Review Focus 3): `circleIDs: [a, a]` writes one join row; `articleIDs: [p, p]` succeeds;
    - `TestEdgeScope` (Review Focus 1): `CreateArticle` with the `writerID` of tenant 8's writer, under tenant 7, fails with `NOT_FOUND` and inserts no article;
    - `TestAtomic` (Review Focus 4): `CreateWriter` whose second circle ID names no row fails with `NOT_FOUND`, and no writer exists afterwards, run under `NewExtension(db).Context(ctx)`;
    - `TestCreateErrors`: an empty `name` fails with `BAD_USER_INPUT` and `veloxgql: CreateWriterInput.name: invalid value: …`; the title `forbidden` fails with `errors.Is(err, velox.ErrDenied)`; a second circle of the same name fails with a `*velox.ConstraintError`; a malformed `meta` JSON fails with `BAD_USER_INPUT` naming `CreateWriterInput.meta`; a writer ID given where a circle ID is expected fails with `ErrWrongIDType`.
  - `TestMutationsRun` builds the app with `app(…)`, generates, and runs `go test -count=1 -v .`. It requires `--- PASS:` for each test above.
- [ ] **Step 2: Run them.** Run `(cd contrib/graphql && go test ./internal/gogen ./cmd/velox-graphql)`. Expected: FAIL.
- [ ] **Step 3: Implement.** Regenerate the gogen goldens with `-update` and read every diff.
- [ ] **Step 4: Run the module tests.** Run `(cd contrib/graphql && go test ./...)`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `GraphQL: CreateXInput and CreateX, with edges`.

### Task 4: `UpdateXInput` and `UpdateX`

**Files:**
- Modify: `internal/gogen/mutation.go`, `render_test.go`, the goldens.
- Modify: `render_test.go`'s `unbound`: the case for the mutation inputs goes, since every input now has a Go type.
- Modify: `cmd/velox-graphql/mutation_e2e_test.go`.

**Interfaces:**

```go
// UpdatePostInput is the input of updatePost: a field not given is left as
// it is.
type UpdatePostInput struct {
	Title            *string
	PublishedAt      *time.Time
	ClearPublishedAt bool
	AuthorID         *string
	AddTagIDs        []string
	RemoveTagIDs     []string
	ClearTags        bool
}

func (in UpdatePostInput) String() string
func (in UpdatePostInput) GoString() string

// UpdatePost updates the Post with the ID id: only the fields input gives
// change. It returns the Post as it is afterwards.
func UpdatePost(ctx context.Context, id string, input UpdatePostInput) (*Post, error)
```

- **What `UpdateX` does,** in this order:
  1. Decode `id` with `DecodeID(…, "updatePost.id", …)`, and every ID and JSON value of the input, as `CreateX` does.
  2. Refuse a conflicting input with `gqlimpl.InputErrorf`, before any statement:
     - `<field>` with `clear<Field>`: `veloxgql: UpdatePostInput: publishedAt and clearPublishedAt together`;
     - `<edge>ID` with `clear<Edge>`: the same form;
     - a key in both `add<Edge>IDs` and `remove<Edge>IDs`: `veloxgql: UpdatePostInput: an ID is in both addTagIDs and removeTagIDs`.
  3. In `gqlimpl.Atomic`:
     - collect the setters: `<H>.<Field>.Set(*v)` for a given field, `<H>.<Field>.SetNull()` for a clear; for a unique edge that `OwnsKey`, `Exist` on the new ID and then set the key, or `SetNull` for a clear;
     - with setters: `db.Update[E]().Set(set...).Where(E.ID.EQ(k)).Exec(ctx)`, its error through `WriteError(err, "UpdatePostInput", …)`, and `NotFound("updatePost", "Post")` when it matched 0 rows (missing, another tenant's, or excluded by the Write policy);
     - without setters: `db.Query[E]().Where(E.ID.EQ(k)).Count(ctx)`, and `NotFound` when 0 (GraphQL plan 4 makes this a locking `Writable().ForUpdate().Exists` read, which closes the time-of-check race);
     - an O2O edge whose key the target holds: with a NULL-able key, first `SetNull` on the target row that holds `k` now and is not the new one, then set the new one; a clear sets NULL where the key is `k`;
     - an O2M edge: clear, then remove, then add. Clear sets the key NULL where it is `k`. Remove sets it NULL where it is `k` and the ID is among the removed; an ID not on the edge is ignored. Add sets it to `k` where the ID is among the added, and `Exist`'s error when fewer rows matched;
     - an M2M edge: clear, then remove, then add. Clear and remove delete join rows; add checks the targets with `Exist`, then inserts with `DoNothing`;
     - read the row back with `db.Query[E]().Where(E.ID.EQ(k)).One(ctx)`; `velox.ErrNotFound` becomes `NotFound`.
- An empty input sends no write: it reads the row and returns it, or `NOT_FOUND`.

- [ ] **Step 1: Write the failing tests.**
  - `TestUpdateInputs` in `render_test.go`: every field of `UpdateXInput` is a pointer, an ID list or a `bool` (for each `InputClear` and `InputClearEdge`), and `func Update<T>(ctx context.Context, id string, input Update<T>Input) (*<Entity>, error)` exists.
  - `TestEveryInputBound`: the existing `unbound` check passes with no case for the mutation inputs.
  - In `e2eMutRun`:
    - `TestUpdate`: `bio` set then cleared reads NULL; `nick` nil stays `anon`; `password` changes and is not in the result's `String()`; `addArticleIDs`, `removeArticleIDs` and `clearArticles` move the articles' `writer_id`; `addCircleIDs` and `removeCircleIDs` change the join rows; `clearProfile` sets the profile's `writer_id` NULL; `profileID` on a writer moves the profile and clears the one before;
    - `TestUpdateConflicts` (Review Focus 2): each of the three conflicts fails with `BAD_USER_INPUT` and the message above, and the statement hook saw no statement;
    - `TestRepeatedIDs` gains: `addCircleIDs` of a circle already linked succeeds and leaves one join row;
    - `TestEdgeScope` gains: `UpdateWriter` of tenant 8's writer under tenant 7 fails with `NOT_FOUND`; `addArticleIDs` with tenant 8's article fails with `NOT_FOUND` and moves nothing;
    - `TestUpdatePolicy`: `UpdateArticle` of an article titled `locked` fails with `NOT_FOUND`; of another, it changes the title;
    - `TestUpdateEmpty`: an empty input returns the row unchanged and sends one `SELECT COUNT`, then the read; an unknown ID gives `NOT_FOUND`;
    - `TestUpdateErrors`: `name: ""` fails with `veloxgql: UpdateWriterInput.name: invalid value: …`.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Regenerate the goldens and read the diff.
- [ ] **Step 4: Run the module tests.** Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `GraphQL: UpdateXInput and UpdateX: nil leaves a field, clear sets NULL, edges add, remove and clear`.

### Task 5: `DeleteX`

**Files:**
- Modify: `internal/gogen/mutation.go`, `render_test.go`, the goldens.
- Modify: `cmd/velox-graphql/mutation_e2e_test.go`.

**Interfaces:**

```go
// DeletePost deletes the Post with the ID id, and its rows in the join
// tables of its many-to-many edges, and returns the ID.
func DeletePost(ctx context.Context, id string) (string, error)
```

- **What `DeleteX` does:**
  1. Decode `id` with `DecodeID(…, "deletePost.id", …)`.
  2. In `gqlimpl.Atomic`:
     - when the entity has an M2M edge (either side of a pair): `db.Query[E]().Where(E.ID.EQ(k)).Count(ctx)`, and `NotFound` when 0, before any write; then delete its join rows, one `DELETE` per join entity;
     - `db.Delete[E]().Where(E.ID.EQ(k)).Exec(ctx)`, and `NotFound` when it removed 0 rows. The join rows' deletion then rolls back with it.
  3. Return the canonical ID, `veloxgql.EncodeID("<Type>", k)`.
- Rows of other entities whose key points at the row (O2M, O2O) are not touched: the database's foreign key decides, and a violation is a `*velox.ConstraintError`, which the presenter shows as `foreign key constraint violated`.

- [ ] **Step 1: Write the failing tests.**
  - `TestDeleteFunc` in `render_test.go`: each type with `Delete` has `func Delete<T>(ctx context.Context, id string) (string, error)`; a type without it has none.
  - In `e2eMutRun`:
    - `TestDelete`: `DeleteWriter` returns the ID and removes the writer and its join rows; a second call gives `NOT_FOUND`;
    - `TestEdgeScope` gains: `DeleteWriter` of tenant 8's writer under tenant 7 gives `NOT_FOUND`, and that writer's join rows are all still there;
    - `TestDeletePolicy`: `DeleteArticle` of a `locked` article gives `NOT_FOUND` and keeps it;
    - `TestDeleteConstraint`: on a target with foreign keys enforced, deleting a writer that articles point at fails with a `*velox.ConstraintError` of kind `ConstraintForeignKey`, and its join rows are back. (On SQLite, the DSN turns foreign keys on with `_pragma=foreign_keys(1)`.)
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Regenerate the goldens and read the diff.
- [ ] **Step 4: Run the module tests.** Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `GraphQL: DeleteX, with its join rows`.

### Task 6: The mutation itest on PostgreSQL and MySQL

**Files:**
- Modify: `contrib/graphql/cmd/velox-graphql/mutation_e2e_test.go` (`targets` and the DDL of PostgreSQL and MySQL in `e2eMutRun`).
- Modify: `scripts/gqlgen-latest.sh`, `scripts/ci-local.sh`, `.github/workflows/ci.yml`.
- Modify (not committed): `<scratchpad>/fix1.sh`.

**Interfaces:**
- `e2eMutRun`'s `targets(t)` gives SQLite always, PostgreSQL through `database/sql` with pgx's `stdlib` when `VELOX_TEST_POSTGRES` is set, and MySQL when `VELOX_TEST_MYSQL` is set. Each test runs as a subtest per target, named `sqlite`, `postgres` and `mysql`. Each target drops and creates its tables at the start of the test.
- A `TestMain` in the app fails the run when `CI=true` and either variable is unset, as `internal/itest`'s does.
- The app's `go.mod` gains `github.com/go-sql-driver/mysql` and `github.com/jackc/pgx/v5`. `app(…)` already concatenates the root `go.sum`, which pins both.
- `TestMutationsRun` passes the environment through, as it does today. When both variables are set, it requires a `--- PASS:` line for each test's `postgres` and `mysql` subtests too.
- `scripts/gqlgen-latest.sh` runs `contrib/graphql`'s tests with `CI` unset (`env -u CI go test …`): that job has no databases, and the generated mutations import no gqlgen.
- `ci.yml`'s `contrib/graphql` step already has the job's database variables; `ci-local.sh` exports them before it. Neither changes in what it runs, only the comment above the step, which now says the step runs the mutation itest.
- `fix1.sh` runs `contrib/graphql`'s tests before Docker starts. Add, after the `export CI=true` line, `(cd contrib/graphql && step go test -count=1 -run TestMutationsRun -v ./cmd/velox-graphql)`, so the gate runs the itest on both databases.

- [ ] **Step 1: Write the DDL and the targets.** The DDL of each dialect matches the gen package's tables, the join table's name included, which `gen/<join>_velox.go` gives. MySQL needs `clientFoundRows=true`, which the variable's DSN has.
- [ ] **Step 2: Run it on the databases.** Start the containers as `fix1.sh` does, export its variables, and run `(cd contrib/graphql && go test -count=1 -run TestMutationsRun -v ./cmd/velox-graphql)`. Expected: PASS, with `--- PASS: TestCreate/postgres` and `--- PASS: TestCreate/mysql` among the lines.
- [ ] **Step 3: Check the dialect differences.** If a test fails on one dialect only, fix the generated code, not the test, and say what differed in the commit message.
- [ ] **Step 4: Run `scripts/gqlgen-latest.sh`.** Expected: `gqlgen v0.17.<n>: ok`.
- [ ] **Step 5: Run the full gate,** with the new `fix1.sh` line. Expected: rc=0 for every step.
- [ ] **Step 6: Commit** `GraphQL: the mutation itest on PostgreSQL and MySQL`.

### Task 7: Mutations in `example/graphql`

**Files:**
- Modify: `example/graphql/schema/schema.go`, `main.go` (the DDL), `main_test.go`, `README.md`.
- Regenerate: `example/graphql/gen`, `example/graphql/graph` (`schema.resolvers.go` gets the mutation resolvers).

**Interfaces:**
- The schema:
  - `Post`: `graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete())`; `title` gains `NotEmpty()`.
  - `Tag` (`Label`): `graphql.Mutations(graphql.Create(), graphql.Update())`; `name` gains `Unique()`.
  - `User`: `graphql.Mutations(graphql.Create(), graphql.Update())`. `email`'s `@admin` now guards its input fields too, and `passwordHash` is an input only.
- The DDL: `tags.name` is `UNIQUE`.
- Each resolver is one line: `return gen.CreatePost(ctx, input)`, `return gen.UpdatePost(ctx, id, input)`, `return gen.DeletePost(ctx, id)`, and so on.

- [ ] **Step 1: Write the failing tests** in `main_test.go`, each with its response and its statements, as the file's other tests are:
  - `TestCreatePost`, under acme: `createPost(input: {title: "Epsilon", authorID: user2, tagIDs: [label1, label2, label1]}) { title status author { name } tags { totalCount } }` gives `DRAFT`, `Brian` and 2. The statements are one `SELECT COUNT` on users, one on tags, the `INSERT` into posts, the `INSERT` into post_tags with two rows, and the reads of the selection.
  - `TestUpdatePost`: `updatePost(id: post1, input: {clearPublishedAt: true, removeTagIDs: [label1]})` gives `publishedAt` null and one tag left. `title: ""` answers `BAD_USER_INPUT` with `UpdatePostInput.title: invalid value: …`.
  - `TestDeleteOtherTenant` (Review Focus 1): under acme, `deletePost(id: post5)` answers `NOT_FOUND`; under globex, post 5 still has its tag.
  - `TestDeletePost`: under acme, `deletePost(id: post1)` returns `post1`, and `node(id: post1)` is then null.
  - `TestMutationTransaction`: `mutation { a: createLabel(input: {name: "rust", rank: 4}) { id } b: createLabel(input: {name: "go", rank: 5}) { id } }` answers `data` null and one error, `BAD_USER_INPUT` `unique constraint violated`; `labels(where: {name: "rust"}) { totalCount }` then gives 0.
  - `TestCreateUserDirective`: `createUser` that sets `email`, without `X-Role`, answers `FORBIDDEN` and creates nothing; with `X-Role: admin`, it creates the user, and a query of `passwordHash` fails gqlgen's validation.
  - `TestMutationInputShape`: `createPost` with the variable `input.title` set to the number 3 answers gqlgen's own error. The test pins the error's `path`, which names the variable and the field (`["variable", "input", "title"]`), and no `code`.
- [ ] **Step 2: Regenerate the example,** run `go -C graphql tool gqlgen generate`, and write each resolver's one line. Run `(cd example && go test ./graphql/...)`. Expected: the new tests PASS. If `TestMutationInputShape`'s path does not name the field, stop and report it: the ruling on gqlgen's messages then needs revisiting.
- [ ] **Step 3: Update the README:** the mutations, one transaction per operation, the codes a client sees, and the `@admin` directive on inputs. Remove "has no mutations yet".
- [ ] **Step 4: Run `scripts/gqlgen-latest.sh`.** Expected: `gqlgen v0.17.<n>: ok`.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Example: GraphQL mutations, one transaction per operation`.

### Task 8: Documents

**Files:**
- `docs/specs/2026-10-19-graphql-design.md`: a section "Rulings made in plan 3", after plan 2's, listing this plan's rulings and those found while executing it. Dated notes in §3 "Mutations" (the Go signatures, `Atomic`, the edge order) and §6 (where the mutation itest lives).
- `docs/plans/2026-10-14-core-1-followups.md`:
  - mark resolved: "`example/graphql` declares no mutations", "`NOT_FOUND` (`CodeNotFound`) is defined and nothing returns it yet", and "GraphQL validation messages … do not name the field" (velox's validation errors now name the input field; gqlgen's own name it through their `path`);
  - add "Left by GraphQL plan 3" with what this plan found and left.
- `contrib/graphql/doc.go`: the section on mutations replaces "The Go of mutations is not written yet": the generated functions, the inputs' Go types, `Atomic`, the codes, and directives on inputs.
- `contrib/graphql/veloxgql/gqlimpl/doc.go`: the mutation helpers.

- [ ] **Step 1:** Edit the documents. Check every snippet against the code: `go doc` for each named symbol.
- [ ] **Step 2:** Run `(cd contrib/graphql && go test -run TestGodocCitesNoSpec ./...)`. Expected: PASS.
- [ ] **Step 3:** Run the full gate. Expected: rc=0.
- [ ] **Step 4:** Commit `Documents: GraphQL mutations`.

---

## Rulings this plan makes

Each is a call made while writing this plan. An implementer who finds one wrong stops and reports it instead of changing it.

1. **(call)** The Go of the inputs and the mutations goes in each entity's `gql_<label>.go`, written by a new `internal/gogen/mutation.go`. No new generated file kind, so the stale-file sweep needs nothing new.
2. **(call)** Input Go types: a required field is a value; an optional one a pointer (`nil` means not given), bytes and JSON included; an ID is a `string`; an ID list a `[]string`; a `clear…` field a `bool`, as old velox and entgql have it. Go field names follow `goOf`, which gqlgen matches ignoring case, so input fields need no `@goField`.
3. **(call)** Each generated mutation is atomic. `gqlimpl.Atomic` runs it in the extension's transaction when there is one, and in a `db.Tx` of its own otherwise: outside a server (`Extension.Context`) and in an operation `WithTxFilter` leaves out. This follows ent, whose writes open a transaction only when they are not in one. It needs no core change: the extension marks the transaction it opened (`shared.WithOperationTx`), so no savepoint is made inside it.
4. **(call)** Every ID an input names is checked against what the caller may read, before it is written. A unique key on the row and an M2M target are checked with one `COUNT` each; an O2M add is checked by the `UPDATE`'s row count, which the tenant and the Write policy scope. `velox.EdgeThrough` leaves both endpoints of a link to the application, and the GraphQL anti-goals forbid a request that bypasses the tenant scope.
5. **(call)** An ID that names no row the caller may see or change is `NOT_FOUND`, whether it is missing, another tenant's, or excluded by a policy's predicate. Telling these apart would tell a client that a row exists. A policy's `Insert` refusal stays `FORBIDDEN`.
6. **(call)** Update semantics, from old velox: `nil` leaves a field as it is. Edges apply clear, then remove, then add, so `clearTags` with `addTagIDs` replaces the set. Three inputs that have no single meaning are refused with `BAD_USER_INPUT` before any statement: a field with its `clear…`, a unique edge's ID with its `clear…`, and an ID in both `add…IDs` and `remove…IDs`. Old velox applied clear then set and left the outcome to the builder.
7. **(call)** Repeated IDs count once. Adding an M2M link that exists is not an error (`OnConflict(…).DoNothing()`). Removing an ID that is not on the edge is ignored, as in ent.
8. **(call)** An ID list is capped by the `MaxPage` of `veloxgql.LimitsFrom(ctx)`, as `nodes(ids:)` is, with `ErrPageTooLarge`. No new limit is added, so `veloxgql`'s API does not change.
9. **(call)** An update that sets no column sends no `UPDATE`: it checks the row with a `COUNT`, applies the edges, and returns the row. velox's `Update` needs a `Set`, and an empty `UPDATE` would only fire `UpdateDefault`s.
10. **(call)** `deleteX` returns the canonical ID, re-encoded from the key. It deletes the row's join rows first, after a `COUNT` that checks the row is the caller's, so another tenant's join rows are never touched. Rows of other entities that point at the row are left to the database's foreign keys, as in ent.
11. **(call)** The owner's join column is the join entity's `Fields[0]` for an edge declared with `edge.To` and `Fields[1]` for its inverse, as velox-gen's `EdgeThrough` reads them. Task 3 pins it from both sides, so a change in velox-gen fails a test.
12. **(call)** A field's `graphql.Directives` reach its mutation input fields, and an edge's reach its edge inputs, as they reach the where inputs (§6c ruling 12). A directive guards a field's value, and an input sets it. An application directive on an input field must be declared `on INPUT_FIELD_DEFINITION`: **breaking** for an application whose directive is on a field that has no where predicates, which until now needed only `FIELD_DEFINITION`.
13. **(call)** Validation errors name the input field: `UpdatePostInput.title: invalid value: must not be empty`, coded `BAD_USER_INPUT` by `gqlimpl.WriteError`. gqlgen's own messages, for a variable or argument of the wrong shape, are left as gqlgen writes them: they name the field through their `path`, and the example pins that. This closes the follow-up "GraphQL validation messages don't name the field".
14. **(call)** A mutation returns the errors of velox unchanged except for validation errors, so `errors.Is` and `errors.As` keep working in the application, and `ErrorPresenter` maps them as today: constraint errors by kind, `ErrNoTenant` to `UNAUTHENTICATED`, `ErrDenied` to `FORBIDDEN`.
15. **(call)** A non-`Nillable` bool or number with a `Default` is refused by velox-graphql too, for a hand-written model; velox-gen already refuses it. An `Optional` string with a `Default` keeps the core's rule: an empty value is unset, so `Insert` writes the `Default`.
16. **(call)** The mutation itest lives in `contrib/graphql`'s e2e app module, not in `internal/itest`: the root module may not depend on `contrib/graphql`. It runs on SQLite always and on PostgreSQL and MySQL when their variables are set. The `gqlgen@latest` job, which has no databases, runs it with `CI` unset.
17. **(call)** `veloxgql`'s public API does not change. `CodeNotFound` was defined in API plan 3 for this plan. Everything new is in `gqlimpl`, under `GeneratedCodeIsVersion1`, which stays 1 because the additions break no code generated before.
18. **(call)** Left out:
    - a raw-key field in create inputs for an entity whose key the caller supplies (plan 1's refusal stands);
    - JSON append and other partial updates of a JSON field (old velox's `Append…`);
    - `@goModel` binding of application enums (still open from plan 2);
    - GraphQL plan 4's collection, `LimitEach`, the request cost limit and caps on `With` loads.
