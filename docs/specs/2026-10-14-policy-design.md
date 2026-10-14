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
except execute: guard, entity hooks, rewrite, scope, render. It prints exactly
what would be sent for that context, and returns the error the statement
would fail with. Entity hooks and `Rewrite` hooks therefore run for `SQL(ctx)`
too; they must have no side effects beyond the struct or statement they are
given (documented on each hook type). A hook that needs to know can read
`Statement.DryRun`.

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
generated keys back. A tenant value that is the zero value of its type cannot
be told apart from "not set" and is filled; applications must not use a zero
tenant ID.

**Upserts.** A conflict can land on another tenant's row and update it:

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
  subquery on the entity. Rows a reader cannot see do not exist.
- **Write** and **Read** are both ANDed into every UPDATE, DELETE and `Save`:
  a writer cannot change what it cannot see. A `Save` whose row is hidden
  returns `ErrNotFound`, as for a row that does not exist.
- **Insert** checks every inserted or upserted row after `BeforeInsert` hooks
  and the tenant fill.
- **Upsert update branch:** PostgreSQL and SQLite render the Read and Write
  predicates as `ON CONFLICT … DO UPDATE … WHERE`. When that `WHERE` is false
  the database neither inserts nor updates the row and reports no error.
  velox sees fewer affected rows than it sent and returns a `*PolicyError`. MySQL has no such clause: an
  upsert that updates on an entity with a Read or Write policy is refused
  with `ErrPolicyUpsert`.
- A zero `Pred[E]{}` means no restriction. Any error denies the statement.
  There is no allow-on-error. Denials are `*PolicyError{Entity, Op, Err}`;
  `velox.Deny(reason)` returns an error that makes one, and
  `errors.Is(err, velox.ErrDenied)` holds for every denial.
- **Bypass:** `velox.SkipPolicy(ctx, reason)`; an empty reason is ignored.
  It is separate from `AllTenants`: a cross-tenant job usually still wants
  the policy, and a support tool may want the reverse.
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

## 3. Entity hooks

Timestamps need no hooks: Core's `OnUpdate` column option already sets them on
every insert and update path. Hooks are for validation and derived fields.

```go
s.BeforeInsert(func(ctx context.Context, x *Invoice) error { … })
s.BeforeUpdate(func(ctx context.Context, m *velox.Mutation[Invoice]) error { … })
s.BeforeDelete(func(ctx context.Context, m *velox.Mutation[Invoice]) error { … })
```

- Each may be declared more than once; hooks of a kind run in the order
  declared. Mixins may declare hooks, typed on the mixin's own struct and
  given the embedded value; a mixin's hooks run before the entity's. A hook
  typed on any other struct is a `SchemaError`.
- **BeforeInsert** runs once per row, for `Insert`, `InsertAll` and upserts,
  on the struct: Core defaults are filled, then the tenant, then the hooks run;
  the tenant check and the policy's Insert check run on the result, so a hook
  cannot swap the tenant. Then the row's values are read from the struct.
- **BeforeUpdate** runs for set-based `Update` and for `Save`. `Mutation[E]`:
  - `Value(c Column[E]) (any, bool)` reads an assignment;
  - `Set(ss ...Setter[E])` adds or replaces assignments;
  - `Where(ps ...Pred[E])` narrows the rows.

  For `Save`, an assignment a hook adds is also stored into the caller's
  struct, so the struct matches the row.
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
  always has its key.

Set at `Open`; both off by default:

- `velox.WithMaxLimit(n)`: a `Limit` above n fails with `ErrLimitTooLarge`.
  `WithMaxLimit(n)` panics for n < 2, because `One` limits itself to 2.
- `velox.WithMaxRows(n)`: a read that returns more than n rows fails with
  `ErrTooManyRows` when row n+1 arrives. This applies to every row-returning
  SELECT, including `Unbounded`, `Iter`, and each `With` load statement; it
  does not apply to `Count` or to an insert's `RETURNING`. The cap counts per
  statement. With `Iter` the caller has already handled n rows when the error
  is yielded (documented). `Query.MaxRows(n)` overrides the cap for one query
  and its `With` loads.

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
  applies. A transaction's deadline bounds every statement inside it, whatever
  context the statement is given, and statement deadlines still apply inside a
  transaction. A transaction past its deadline is rolled back, never
  committed, on every driver.
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
  type and scan plan.
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
- `Policy[E]`, `Mutation[E]` and the new `Schema` methods follow Core §3: every
  method of a generic type is generic (`methods_test.go`), nothing runs at
  init, and no reachable type holds `reflect.Type` (rule 7).
- An entity package declaring a policy and all three hooks grows by at most
  8 KiB of object over one without (`compilecost_test.go`).

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
