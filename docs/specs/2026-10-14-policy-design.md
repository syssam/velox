# velox2 Policy and guardrails — design

Status: draft for review · 2026-10-14 · Go 1.27+

Sub-project 2 of five (Core design, `2026-10-14-core-design.md`, table at the
top). It plugs into Core's pipeline (§5 there) and nothing else, and adds:
tenant scoping, row-level policy, typed entity hooks, required limits and row
caps, and deadlines. It also settles the Core follow-ups filed under
"Sub-project 2" in `docs/plans/2026-10-14-core-1-followups.md`.

## Decisions

Made by the author during design (2026-10-14):

- **Built in, not optional.** Scoping, policy, entity hooks, guards and
  deadlines are fixed stages of the pipeline. They run on the hook-free path
  too. A service cannot forget to turn them on.
- **Declared in `Schema`.** An entity's tenant column, policy and hooks are
  declared in its own `Schema` method, so the team that owns the struct owns
  them.
- **Hooks** means typed per-entity hooks (`BeforeInsert`, `BeforeUpdate`,
  `BeforeDelete`). There are no after-hooks and no after-commit hooks.
- **Policy is filter and decide.** A read policy is a predicate added to every
  read; a write policy is a predicate added to every update and delete; an
  insert policy checks each inserted row. Any error denies.
- **The tenant comes from the context**, fail closed. Bypass is explicit,
  carries a reason, and is separate from the policy bypass.
- **Guards are always on.** `ErrUnbounded` and `ErrNoWhere` cannot be turned
  off; the escape is per query (`Unbounded()`, `All()`). A maximum `Limit` and
  a row cap are set at `Open`.
- **Deadlines** are a default and a cap, for statements and transactions.
- **`SQL()` becomes `SQL(ctx)`** and prints exactly what would be sent.

## Success criteria

1. A statement on a tenant-scoped entity never reads or writes another
   tenant's rows unless its context says `AllTenants` with a reason.
2. A statement on an entity with a policy never reads or writes a row the
   policy hides unless its context says `SkipPolicy` with a reason.
3. No read runs without a limit, and no update or delete runs without a
   `Where`, unless the code that built it says so.
4. Every rule holds on all three dialects, or is refused on the dialect that
   cannot enforce it, with an error that says why.
5. Core's criteria still hold: `SQL(ctx)` prints what is sent; compile cost
   follows use (Core §3, including rule 7); nothing runs at program start;
   per-query overhead stays within §7's budget.

## 1. Pipeline

Core's pipeline gains three stages and a wrapper around execution:

```
build ─▶ guard ─▶ entity hooks ─▶ rewrite ─▶ scope ─▶ render ─▶ execute ─▶ scan
                                                                (deadline ▸ around ▸ row cap)
```

| Stage | What it does | Sees |
|---|---|---|
| guard | `ErrUnbounded`, `ErrNoWhere`, maximum `Limit` (§4) | The statement as the caller built it |
| entity hooks | `BeforeUpdate`, `BeforeDelete` (§3); inserts run `BeforeInsert` during build, on the struct | The statement or struct, typed |
| rewrite | Core's user `Rewrite` hooks, unchanged | |
| scope | Tenant predicate and checks (§2.1), policy predicates and checks (§2.2), inside `EXISTS` subqueries too | The statement after the user's hooks |
| execute | Deadline outermost (§5), then Core's `Around` hooks, then the row cap counting rows as they are scanned (§4) | |

Order matters:

- **Guard runs first**, so the tenant and policy predicates velox adds never
  count as the caller's `Where`.
- **Scope runs after the user's `Rewrite` hooks**, so a hook cannot remove what
  it adds. `Rewrite` hooks are trusted code configured at `Open`; they may
  still add predicates and refuse statements.
- **The deadline wraps the `Around` hooks**, so it bounds their retries; with
  `Iter` it also covers the caller's loop body, as Core's execute stage does.

The stages run on both of Core's paths (`plain` with no hooks, `run` with
hooks). On an entity with no tenant column, no policy and no hooks, guard and
scope are a few branches.

### 1.1 `SQL(ctx)`

`Query.SQL`, `InsertQuery.SQL`, `UpdateQuery.SQL` and `DeleteQuery.SQL` take a
context: `SQL(ctx context.Context) (string, []any, error)`. It runs every stage
except execute: guard, entity hooks, rewrite, scope, row cap, render. It prints
exactly what would be sent for that context, and returns the error the
statement would fail with before it is sent, including:

- a statement with more parameters than the dialect allows;
- a query whose `Columns`/`Omit` leave out a `With` relation's key column,
  checked before and after the `Rewrite` hooks, as `All` checks it.

Entity hooks and `Rewrite` hooks therefore run for `SQL(ctx)` too; they must
have no side effects beyond the struct or statement they are given
(documented on each hook type). `Around` hooks, which wrap execution, do not
run. Each `SQL(ctx)` method marks its context first, before any hook or
policy function runs, so `velox.IsDryRun(ctx context.Context) bool` is true in
every `BeforeInsert`, `BeforeUpdate` and `BeforeDelete` hook, `Rewrite` hook
and policy function that runs for it, and false when the statement is sent.
An entity hook, which is given no `Statement`, reads `IsDryRun`; a `Rewrite`
hook may read it or `Statement.DryRun`, which `SQL(ctx)` also sets.

For an insert, `SQL(ctx)` prepares the rows as `Exec` does: Core defaults, the
tenant fill and `BeforeInsert` change the caller's structs, and the policy's
Insert check runs. An `InsertAll` too large for one statement returns the
first batch's statement. `InTx` is set whenever `Exec` would send the
statement in a transaction: inside one, for an `InsertAll` too large for one
statement, and for an `InsertAll` whose update branch gets the policy (§2.2).
An `InsertAll` of nothing returns `""`.

This replaces Core's `SQL() (string, []any)`. Nothing is released, so the
signature changes now rather than after 1.0.

## 2. Scope

### 2.1 Tenant

An entity declares its tenant column in `Schema`, as Core already allows:

```go
func (x *Invoice) Schema(s *velox.Schema) {
    s.Tenant(&x.OrgID)
}
```

The context carries the tenant:

```go
ctx = velox.WithTenant(ctx, orgID)               // set by middleware
ctx = velox.AllTenants(ctx, "nightly billing")   // bypass; the reason is required
```

- The tenant column must be `NOT NULL` (not a pointer field) and of a
  comparable type; otherwise `Schema` reports a `SchemaError`. A nullable or
  slice tenant could be neither compared nor filled safely.
- The tenant column may not have `Default`, `DefaultFunc` or `OnUpdate`; any
  of them is a `SchemaError`. The context's tenant fills the column: a default
  would put a row in a tenant no context named, and `OnUpdate` would move rows
  between tenants.
- The later of the two on a context wins. `AllTenants` with an empty reason
  is ignored, so the statement fails closed.
- The tenant's dynamic type must be the tenant column's Go type exactly (a
  named `OrgID` does not match `int64`). A mismatch fails the statement with
  `ErrTenantMismatch`.

On a statement whose entity has a tenant column:

| Statement | With `WithTenant(id)` | With `AllTenants` | With neither |
|---|---|---|---|
| SELECT, Count, `With` loads | `AND tenant = id` | unscoped | `ErrNoTenant` |
| `Has`/`HasWhere` subquery on a scoped target | `AND tenant = id` inside the `EXISTS` | unscoped | `ErrNoTenant` |
| UPDATE, DELETE, `Save` | `AND tenant = id` | unscoped | `ErrNoTenant` |
| INSERT (each row) | zero tenant field: set to `id`; equal: kept; different: `ErrTenantMismatch` | field must be non-zero, else `ErrNoTenant` | `ErrNoTenant` |
| Assigning the tenant column (`Set`, `Save`, an upsert's update list) | to `id`: allowed (it changes nothing); to another value: `ErrTenantImmutable` | allowed | `ErrNoTenant` |

The insert fill writes the tenant into the caller's struct, as Core writes
generated keys back. A tenant field holding the zero value of its type cannot
be told apart from "not set" and is filled. So the zero value is never a
tenant: `WithTenant(ctx, v)` where `v` is its type's zero value fails every
statement on a scoped entity with `ErrNoTenant` ("the tenant is the zero
value"). That fails closed when middleware parses a missing claim as 0.

- The `EXISTS` rule holds inside a policy's own condition too (§2.2).
- An error raised for an `EXISTS` target names the statement's entity first,
  keeping the `velox: <Entity>: ` rule, and the target after it:
  `velox: Invoice: in the EXISTS on Customer: no tenant in the context`.
- A `Rewrite` hook that replaces `Entity` with a copy must keep the original's
  tenant column and policy; a copy that drops or moves either is refused, as
  a copy with other columns is (§6). Otherwise a hook could unscope a
  statement silently.

**Upserts.** A conflict can land on another tenant's row and update it. The
rules below apply under `WithTenant`; under `AllTenants` the statement reaches
every tenant's rows anyway, and they do not apply:

- **PostgreSQL, SQLite:** an upsert that updates (`OnConflict(...).Update(...)`)
  on a scoped entity must include the tenant column in `OnConflict`'s columns,
  else `ErrTenantUpsert`. A collision on another tenant's key then fails with
  a unique violation instead of updating that row.
- **MySQL:** `ON DUPLICATE KEY UPDATE` fires on any unique key. An upsert that
  updates on a scoped entity is refused with `ErrTenantUpsert` unless every
  unique index declared in `Schema`, and the primary key, contain the tenant
  column. The error says which index does not. velox knows only the indexes
  `Schema` declares; a unique index created outside it defeats this check,
  which the documentation says.
- `DoNothing` is allowed on every dialect without these rules: it can skip a
  row but never modify one. (MySQL renders it as an `ON DUPLICATE KEY UPDATE`
  that assigns nothing new.)

### 2.2 Policy

```go
func (x *Invoice) Schema(s *velox.Schema) {
    s.Policy(velox.Policy[Invoice]{
        Read:   func(ctx context.Context) (velox.Pred[Invoice], error) { … },
        Write:  func(ctx context.Context) (velox.Pred[Invoice], error) { … },
        Insert: func(ctx context.Context, x *Invoice) error { … },
    })
}
```

- `Policy` may be declared once per entity, in the entity's own `Schema`
  (not a mixin's); a second call is a `SchemaError`. Any field may be nil.
- **Read** is ANDed into every SELECT, Count, `With` load and `Has`/`HasWhere`
  subquery on the entity. Rows a reader cannot see do not exist: so a
  `Not(Has())` over rows the policy hides is true.
- **Policies inside policies:** a policy's condition may itself use
  `Has`/`HasWhere`; the target's tenant and Read policy are applied inside
  that `EXISTS` too, recursively, so an `Item` whose Read is
  `Items.Invoice.Has()` shows only the items whose invoice the caller may
  see. Each entity's Read is called once per statement and reused for every
  `EXISTS` on it.
- **Cycles fail closed:** a Read policy that reaches its own Read again
  through `EXISTS`, directly or through another entity's, cannot be written
  as finite SQL. The statement fails with `velox: <Entity>: the Read policy
  of <Target> reaches its own Read through an EXISTS; such a policy cannot be
  applied`. That is the schema's bug, not a denial: it is not a
  `*PolicyError`.
- **Write** and **Read** are both ANDed into every UPDATE, DELETE and `Save`:
  a writer cannot change what it cannot see. A `Save` whose row is hidden
  returns `ErrNotFound`, as for a row that does not exist.
- **Write filters, it does not check.** It decides which existing rows may
  change; it does not check the new values (there is no `WITH CHECK`). A
  writer may therefore change a column the policy reads, such as an owner,
  and hand the row to someone else. Only the tenant column is immutable
  (§2.1).
- **Insert** checks every inserted or upserted row after `BeforeInsert` hooks
  and the tenant fill.
- **Upsert update branch:** PostgreSQL and SQLite render the Read and Write
  predicates as `ON CONFLICT … DO UPDATE … WHERE` (`ConflictClause.Where`,
  §2.3). When that `WHERE` is false the database neither inserts nor updates
  the row and reports no error. velox sees fewer affected rows (or returned
  rows) than it sent and returns a `*PolicyError` for `update`: `the existing
  row is hidden from this upsert`. An `InsertAll` of more than one row with
  that condition runs in a transaction (a savepoint inside the caller's), so
  that the error also undoes the rows it did write. Whether it will have the
  condition is decided before the scope runs, from what is known then: an
  upsert that updates (not `DoNothing`, a non-empty `Update`), a dialect other
  than MySQL, an entity that declares Read or Write, and no `SkipPolicy`. MySQL
  has no such clause: an upsert that updates on an entity with a Read or
  Write policy is refused with `ErrPolicyUpsert`.
- A zero `Pred[E]{}` means no restriction. Any error denies the statement.
  There is no allow-on-error. Denials are `*PolicyError{Entity, Op, Err}`,
  whose text is `velox: <Entity>: <op> denied by policy: <err>`;
  `velox.Deny(reason)` returns an error that makes one, and
  `errors.Is(err, velox.ErrDenied)` holds for every denial.
- A denial raised for an `EXISTS` target names the statement's entity first,
  as §2.1's errors do: `velox: Invoice: in the EXISTS on Customer: select
  denied by policy: …`. It is still a `*PolicyError` and `ErrDenied`, and its
  `Op` is `select` whatever the statement is: the `EXISTS` reads its target.
- **Bypass:** `velox.SkipPolicy(ctx, reason)`; an empty reason is ignored.
  It is separate from `AllTenants`: a cross-tenant job usually still wants
  the policy, and a support tool may want the reverse.
- `Statement.PolicyBypass` holds the reason when `SkipPolicy` skipped a policy
  function that would otherwise have run, on the statement's entity or an
  `EXISTS` target. It stays empty on a statement whose entities have no
  policy to skip, so an audit log records what was actually bypassed.
- Policy predicates join the render cache's shape key like any `Where`. A
  policy whose predicate structure varies by caller adds shapes, within the
  cache's existing bounds (4,096 shapes, 4 MiB).
- Policies are called once per statement. A `With` load runs its target's
  Read policy once per chunk.

### 2.3 Statement fields

`Statement` gains, set by scope and read by later hooks and by sub-project 3:

```go
Tenant       any    // the tenant applied; nil when unscoped
TenantBypass string // the AllTenants reason, when it applied
PolicyBypass string // the SkipPolicy reason, when it applied
DryRun       bool   // the statement is being built for SQL(ctx), not sent
MaxRows      int    // SELECT: the most rows it may return; 0: the DB's cap
```

`ConflictClause` gains the condition of an upsert's update branch:

```go
Where []Predicate // on the existing row; a conflicting row it rejects is neither updated nor replaced
```

- Scope appends the policy's Read and Write predicates to a copy of the
  clause, so the query stays reusable by another caller; a `Rewrite` hook may
  set `Where` itself. Every `EXISTS` in it, a hook's included, gets its
  target's tenant and Read policy, as one in a statement's `Where` does.
- A `Rewrite` hook's `Where` on an upsert that has no update branch to apply
  it to (`DoNothing`, or an empty `Update`) is an error, not dropped.
- PostgreSQL and SQLite render it as `DO UPDATE SET … WHERE …`, each column
  qualified with the table's name (`"tickets"."owner"`) so that it names the
  existing row. PostgreSQL rejects the unqualified name as ambiguous with
  `EXCLUDED`'s (SQLSTATE 42702); the real-database tests pin PostgreSQL's
  (SQLite accepts either form).
- MySQL never gets one: velox refuses there first (§2.1, §2.2).

## 3. Entity hooks

Timestamps need no hooks: Core's `OnUpdate` column option already sets them on
every insert and update path. Hooks are for validation and derived fields.

```go
s.BeforeInsert(func(ctx context.Context, x *Invoice) error { … })
s.BeforeUpdate(func(ctx context.Context, m *velox.Mutation[Invoice]) error { … })
s.BeforeDelete(func(ctx context.Context, m *velox.Mutation[Invoice]) error { … })
```

- Each may be declared more than once; hooks of a kind run in the order
  declared. A mixin may declare `BeforeInsert` only, typed on the mixin's own
  struct and given the embedded value; a mixin's hooks run before the
  entity's. A mixin has no column handles, so a `Mutation` of it would be
  useless: `BeforeUpdate` or `BeforeDelete` in a `Mixin` is a `SchemaError`,
  as is a hook typed on any other struct.
- **BeforeInsert** runs once per row, for `Insert`, `InsertAll` and upserts,
  on the struct: Core defaults are filled, then the tenant, then the hooks run;
  the tenant check and the policy's Insert check run on the result, so a hook
  cannot swap the tenant. Then the row's values are read from the struct.
- **BeforeUpdate** runs for set-based `Update` and for `Save`. `Mutation[E]`:
  - `Value(c Column[E]) (any, bool)` reads an assignment;
  - `Set(ss ...Setter[E])` adds or replaces assignments;
  - `Where(ps ...Pred[E])` narrows the rows.

  A `Mutation` records its errors: `Set` on a delete, or a zero or invalid
  predicate given to `Where`, makes the hook stage fail with that error once
  the hook returns, even if the hook returns nil.
- **Save writes back what it sent.** After a successful `Save`, every plain
  assignment (`Set`, not arithmetic) in the statement as sent is stored into
  `*x` when its value is assignable to the field: Core's `OnUpdate` values,
  and those a `BeforeUpdate` or `Rewrite` hook added, included. A nil value
  stores the field's zero value; a nullable column's value is stored through
  a new pointer. So the struct matches the row. After an error, including
  `ErrNotFound`, `*x` is as it was.
- **BeforeDelete** gets the same `Mutation[E]`; it may narrow or refuse.
  `Set` on a delete's mutation is an error.
- An upsert's update branch runs `BeforeInsert` only.
- Any error refuses the statement.
- Hooks are not given a `*DB`. A hook that queries inside a write is a scale
  hazard; a lookup belongs before the write.

Hooks are stored on `EntityInfo` as non-generic functions over the entity's
address: the generic `Schema` method wraps the typed function once, when
metadata is built.

## 4. Guards and row caps

Always on:

- **`ErrUnbounded`:** a SELECT with no `Limit` and no `Unbounded()`. This
  includes `Limit(n)` with n ≤ 0, `Iter`, and keyset `After`. `Count` is exempt.
  `One` sets its own limit. Core's relation loads say `Unbounded`.
- **`ErrNoWhere`:** an UPDATE or DELETE with no `Where` and no `All()`. `Save`
  always has its key. A `Where` whose every condition holds for every row by
  its structure alone (an empty `And`, `NotIn()` of nothing, `Not` of an empty
  `Or`) counts as no `Where`.

Set at `Open`; both off by default:

- `velox.WithMaxLimit(n)`: a `Limit` above n fails with `ErrLimitTooLarge`.
  `WithMaxLimit(n)` panics for n < 2, because `One` limits itself to 2.
- `velox.WithMaxRows(n)`: a read that returns more than n rows fails with
  `ErrTooManyRows` when row n+1 arrives. This applies to every row-returning
  SELECT, including `Unbounded`, `Iter`, and each `With` load statement; it
  does not apply to `Count` or to an insert's `RETURNING`. The cap counts per
  statement. velox renders `LIMIT` cap+1, so the database stops early: when
  the query has no `Limit` or a higher one, velox sets it to n+1 after the
  guard and the `Rewrite` hooks (which see the caller's `Limit`) and before
  rendering (the `Around` hooks see the one sent). With `Iter` the caller has
  already handled n rows when the error is yielded (documented).
  `Query.MaxRows(n)` overrides the cap for one query and its `With` loads.
  `WithMaxRows(1)` panics, because `One` reads 2 rows; n ≤ 0 is no cap.

## 5. Deadlines

```go
velox.WithDeadlines(velox.Deadlines{
    Statement:    2 * time.Second,  // when the context has no deadline
    MaxStatement: 30 * time.Second, // a later deadline is brought forward to this
    Tx:           10 * time.Second,
    MaxTx:        60 * time.Second,
})
```

- **Statements:** with no deadline on the context, `Statement` applies. A
  deadline later than now + `MaxStatement` is brought forward to it. Zero
  fields are off. The deadline wraps the `Around` hooks and, with `Iter`, the
  caller's loop body.
- **Transactions:** `Tx` and `MaxTx` apply the same way to the whole `db.Tx`
  call: the function, every `RetrySerializable` attempt and the commit. The
  `Tx` default applies only to a context with no deadline. The `Tx` option
  `Timeout(d)` is a cap on the transaction: it replaces `Tx` for one call, is
  still subject to `MaxTx`, and an earlier deadline on the context still
  applies. A transaction's effective deadline is velox's or the one on the
  context given to `db.Tx`, whichever is earlier; it bounds every statement
  inside the transaction, nested `Tx` (savepoints) included, whatever context
  the statement is given, and statement deadlines still apply inside a
  transaction. A transaction whose context has ended by commit time (past
  either deadline, or canceled) is rolled back, never committed, on every
  driver; its error is `ErrTimeout` when velox's deadline passed, and
  otherwise matches `context.DeadlineExceeded` or `context.Canceled`. A
  context whose deadline has passed counts as ended: velox waits at most a
  short grace (50ms) for its `Done` to close, so a context that never closes
  it cannot hang a transaction; when it has not closed by then, the error
  matches `context.DeadlineExceeded`. Once the
  context has ended, a failed rollback is not reported: the driver may have
  closed the connection (pgx does), and the server aborts the transaction.
  `Timeout(d)` with d ≤ 0 is ignored, and a nested `Tx` ignores it.
- **Errors:** velox's deadlines carry a context cause. A statement that runs
  out of time returns an error for which `errors.Is(err,
  context.DeadlineExceeded)` holds; `errors.Is(err, velox.ErrTimeout)` holds
  only when velox's own deadline fired, not the caller's.
- **Cost:** a deadline costs `context.WithDeadline`'s allocations, only when
  velox applies one. It is benchmarked on its own, outside §7's budget.
- Server-side timeouts (PostgreSQL `SET LOCAL statement_timeout`) are out of
  scope.

## 6. Errors and hook hardening

**Convention.** Every error velox returns starts with `velox: <Entity>: `.
Sentinels are bare phrases (`ErrUnbounded`: "query has no Limit") and are
always returned wrapped with `%w`, so the prefix never doubles. Errors from
`Rewrite` hooks and entity hooks are wrapped the same way, naming the stage
(`velox: Invoice: rewrite: …`, `velox: Invoice: before insert: …`). Errors
from `Around` hooks are returned unchanged: they normally wrap `next`'s error,
which already carries the prefix. Core's existing sentinels change their text
accordingly.

New sentinels: `ErrNoTenant`, `ErrTenantMismatch`, `ErrTenantImmutable`,
`ErrTenantUpsert`, `ErrDenied`, `ErrPolicyUpsert`, `ErrLimitTooLarge`,
`ErrTooManyRows`, `ErrTimeout`. New typed error: `*PolicyError{Entity string;
Op Op; Err error}`, with `Unwrap` returning `Err` and `Is(ErrDenied)` true.

**Hardening** (the Core follow-ups):

- `WithRewrite(nil)` and `WithAround(nil)` panic when called (in `Open`'s argument list), as
  `http.Handle(nil)` does.
- A `Rewrite` hook that sets `Entity` to nil or changes `Op` fails the
  statement. Replacing `Entity` is allowed only with one backed by the same Go
  type and scan plan, and with the same tenant column and policy (§2.1).
- A zero `Predicate{}` in `Where` fails the statement.
- Hooks cannot edit a predicate's arguments: `Predicate`'s node is
  unexported and `In` copies its values into a new slice. A test pins this and
  the follow-up closes.
- `Statement.Count`'s contract is documented: velox clears `Columns`,
  `OrderBy`, `Limit` and `Offset`, and a hook that sets them gets them
  rendered.
- A non-nil empty `Columns` (for example `Omit` of every column) fails the
  statement instead of rendering `SELECT  FROM`.

## 7. Performance and compile cost

- Statements on entities with no tenant column, policy or hooks keep Core's
  budget (Core §8) unchanged.
- A tenant-scoped one-row read stays within Core's one-row budget plus one
  allocation and 150 ns. A budget test pins it.
- `Policy[E]` and `Mutation[E]` follow Core §3: every
  method of a generic type is generic (`methods_test.go`), nothing runs at
  init, and no reachable type holds `reflect.Type` (rule 7).
- A policy and all three hooks add about 99 KB of object to an entity's
  package (98,988 bytes on darwin/arm64, Go 1.27.1). About 42 KB of that is
  the user's own six closures; the rest is velox's generic `Policy` and hook
  registration, instantiated for the entity. This section first gave 8 KiB,
  an estimate that was never measured. `compilecost_test.go` enforces the
  measured cost plus its usual margin (123,904 bytes), so a regression still
  fails; cutting velox's share is a follow-up. A policy and hooks are opt-in
  per entity.
- An entity package with no policy or hooks grew by about 21 KB during this
  work (98,878 to 119,896 bytes): export data, because its API now reaches
  `context.Context`, `Statement` and the Policy and hook signatures.
  Its object code did not change. The guard is re-baselined to it.

  *Note, 2026-10-19.* The figures above are the old struct-scanning path's,
  which schema plan 4 removed. On the Desc path `compilecost_test.go` measures
  a Policy and three hooks as `entityDescHooks` over `entityDescUse`: 282,308
  bytes on darwin/arm64, Go 1.27.1, with a guard of 348,160. It costs more
  than the old 99 KB because the generics instantiated in the entity's own
  package are now the whole cost (`BindPolicy`, `BindHooks`, `On`, and the
  `Mutator` and `Mutation` methods the hooks call), and the generated Desc
  binds them there. The old figures stay as history.

## 8. Testing

- Unit tests against the fake executor, one per rule above, and golden SQL on
  all three dialects for every scoped operation.
- Real-database tests (`internal/itest`, on PostgreSQL through pgx and
  `database/sql`, MySQL 8.4 and SQLite) seed two tenants and prove isolation on
  `All`, `One`, `Iter`, `Count`, `With`, `Has`, `Update`, `Delete`, `Save`,
  `Insert`, `InsertAll` and both upsert forms, plus policy filtering and
  denial, and deadlines (`pg_sleep`, `SLEEP`).
- `FuzzScope`: for random statements on a scoped entity with a policy, the
  statement after scope has the tenant and policy predicates as top-level
  `AND` terms (checked on the statement's structure, not its text). It runs
  in `scripts/ci-local.sh` with the other fuzz smoke runs.
- Mutation check: removing scope from any one path fails a test.

## 9. Delivery

Two plans:

1. **Guards and deadlines:** the error convention, hook hardening, `ErrUnbounded`
   and `ErrNoWhere`, `WithMaxLimit`, `WithMaxRows`, `WithDeadlines`. No API
   shape changes except the sentinels' text.
2. **Scope and hooks:** tenant scoping, policy, entity hooks, `Statement`'s new
   fields, `SQL(ctx)`.

Core's design document is updated where this one changes it: `SQL(ctx)` (Core
§4), `Statement` (Core §5), the error convention and sentinels (Core §7).

## 10. Out of scope

- After-hooks and after-commit hooks.
- Soft delete.
- Field-level redaction.
- Database-native row-level security (PostgreSQL only).
- Server-side statement timeouts.
