# Policy 2: tenant scoping, policy, entity hooks — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make velox scope every statement on a tenant-scoped entity to the context's tenant, apply each entity's row-level policy, run typed entity hooks, and print exactly what is sent with `SQL(ctx)`.

**Architecture:**
- New declarations live in `Schema`. They are a typed `Policy[E]` and typed `BeforeInsert`/`BeforeUpdate`/`BeforeDelete` hooks, stored on `EntityInfo` as non-generic functions.
- A new **scope** stage (`scope.go`) runs in both execution paths, after the `Rewrite` hooks. It does four things:
  - adds the tenant predicate and the policy predicates;
  - adds them inside `EXISTS` subqueries on scoped targets, copying nodes rather than editing shared ones;
  - checks assignments to the tenant column;
  - enforces the upsert rules.
- Inserts prepare each row on its struct before its values are read: tenant fill, `BeforeInsert`, tenant check, then the policy's `Insert` check.
- `BeforeUpdate`/`BeforeDelete` run on the statement through `Mutation[E]`. Statements with such hooks always take `DB.run`, so `DB.plain` stays non-escaping.
- `SQL(ctx)` runs every stage except execute.

**Tech Stack:** Go 1.27 (generic methods), the in-repo `FakeExec`, `internal/itest` on SQLite, PostgreSQL (pgx and database/sql) and MySQL 8.4, and `velox-gen` for the test model.

**Spec:** `docs/history/specs/2026-10-14-policy-design.md` §1.1, §2, §3, §7, §8. Plan 1 (`docs/history/plans/2026-10-14-policy-1-guards.md`, merged) built the guard, the row cap, the deadlines and the error convention that this plan builds on.

## Global Constraints

- Go 1.27. Every method of a generic velox type is generic (`[_ Lazy[E]]` or its own type parameter); `methods_test.go` enforces it. `Schema`'s new methods are generic methods of a non-generic type with their own type parameter.
- Nothing runs at program start. No velox type reachable from generated code holds `reflect.Type` (Core §3 rule 7): keep reflect types as `any` and use the existing `rtype`/`elemType`/`GoType` helpers.
- Errors:
  - Every error starts `velox: <Entity>: `. Sentinels are bare lower-case phrases, returned wrapped with `%w`.
  - Entity-hook errors read `velox: <Entity>: before insert: <err>` (likewise `before update`, `before delete`).
  - Policy denials are `*PolicyError`, whose text is `velox: <Entity>: <op> denied by policy: <err>`.
- New sentinels, exact text:
  - `ErrNoTenant` = `"no tenant in the context"`
  - `ErrTenantMismatch` = `"tenant does not match"`
  - `ErrTenantImmutable` = `"tenant column cannot change"`
  - `ErrTenantUpsert` = `"upsert could cross tenants"`
  - `ErrDenied` = `"denied by policy"`
  - `ErrPolicyUpsert` = `"upsert cannot apply the policy"`
- `DB.plain`'s `st` and `read` must not escape. Never pass `st` to a function value inside `plain` or anything it calls. Policy functions take only `ctx`.
- Core's budget tests keep passing. A tenant-scoped one-row read may cost at most one more allocation than Core's one-row budget (spec §7).
- `gofmt`, `go vet` and `golangci-lint run` are clean; `go test -race ./...` passes in the root module and in `cmd/velox-gen`.
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.

## Rulings this plan makes on the spec

- **The tenant column must be NOT NULL and of a comparable type.** Otherwise `Schema` reports a `SchemaError`. A nullable or slice tenant cannot be compared or filled safely.
- **Mixins may declare `BeforeInsert` only.** A mixin's hook is typed on the mixin's struct and given the embedded value. A mixin has no column handles, so a `Mutation[M]` would be useless; `BeforeUpdate`/`BeforeDelete` in a `Mixin` is a `SchemaError`.
- **`Mutation[E]` records its errors.** `Set` on a delete, or a zero/invalid predicate given to `Where`, makes the hook stage fail with that error after the hook returns.
- **`SQL(ctx)` and multi-batch inserts.** `SQL(ctx)` on an `InsertAll` larger than one statement returns the first batch's statement. A `SQL(ctx)` call runs the row preparation and the hooks: tenant fill, defaults and `BeforeInsert` change the caller's structs, as `Exec` does.
- **`Save` writes back hook assignments.** After a successful `Save`, every `AssignSet` assignment in the statement as sent is stored into `*x` when its value is assignable to the field; a nil value stores the zero value. This includes Core's `OnUpdate` values and the assignments a `BeforeUpdate` hook adds.
- **`ConflictClause` gains `Where []Predicate`,** the condition of the upsert's update branch. PostgreSQL and SQLite render it, qualified so that it names the existing row. MySQL never gets one: velox refuses there first.
- **The test model gains `Ticket`.** It is tenant-scoped, with a policy and every hook kind, plus a `Stamp` mixin with a `BeforeInsert`. The existing tests run under `velox.AllTenants(context.Background(), "velox tests")`, because `Invoice` is tenant-scoped.

## Review Focus

1. **Building a scope never changes a query's shared predicate trees.** A base query extended from several goroutines must not see another call's tenant predicates. `EXISTS` scoping copies the nodes it changes (Task 3).
2. **A tenant value of the wrong Go type** (`int` versus the column's `int64`) is refused with `ErrTenantMismatch`. It never matches nothing silently (Task 2).
3. **An upsert's update branch never touches another tenant's row** on any dialect: PostgreSQL and SQLite through the conflict-column rule, MySQL by refusal (Task 4).
4. **A `BeforeInsert` hook that changes the tenant field** is caught by the tenant check that runs after it (Task 4).
5. **`SQL(ctx)` and execution send identical text and arguments** for the same context (Task 6).

---

### Task 1: Declarations, test model and the context helpers

**Files:**
- Create: `scope_api.go`, which holds:
  - the context helpers `WithTenant`, `AllTenants`, `SkipPolicy`, and internal readers `tenantOf`, `policyBypass`;
  - `Policy[E]`, `PolicyError`, `Deny`, `Mutation[E]`.
- Modify:
  - `schema.go`: `Policy`, `BeforeInsert`, `BeforeUpdate`, `BeforeDelete`, and mixin tracking;
  - `meta.go`: `EntityInfo`'s unexported hook and policy fields; the tenant column checks in `buildInfo`/`validate`;
  - `statement.go`: the new fields, `Op.String`, `ConflictClause.Where`;
  - `errors.go`: the sentinels.
- Create: `internal/testmodel/ticket.go` (with the `Stamp` mixin). Generate `internal/testmodel/ticket_velox.go` with velox-gen; mixins get no generated file.
- Test: `scope_api_test.go` (package `velox_test`), `schema_policy_internal_test.go` (package `velox`).

**Interfaces (produced; later tasks use exactly these):**

```go
// context helpers (scope_api.go)
func WithTenant(ctx context.Context, id any) context.Context
func AllTenants(ctx context.Context, reason string) context.Context // empty reason: ctx unchanged
func SkipPolicy(ctx context.Context, reason string) context.Context // empty reason: ctx unchanged
// tenantOf returns the context's tenant state: id (nil when none), the AllTenants reason ("" when none).
// The later of WithTenant and AllTenants on a context wins (one key holds both).
func tenantOf(ctx context.Context) (id any, all string)
func policyBypass(ctx context.Context) string

type Policy[E any] struct {
	Read   func(ctx context.Context) (Pred[E], error)
	Write  func(ctx context.Context) (Pred[E], error)
	Insert func(ctx context.Context, x *E) error
}

type PolicyError struct {
	Entity string
	Op     Op
	Err    error
}
func (e *PolicyError) Error() string        // "velox: " + Entity + ": " + Op.String() + " denied by policy: " + Err.Error()
func (e *PolicyError) Unwrap() error        // Err
func (e *PolicyError) Is(target error) bool // target == ErrDenied
func Deny(reason string) error              // an error whose text is reason; errors.Is(…, ErrDenied) holds

type Mutation[E any] struct { st *Statement; err error }
func (m *Mutation[E]) Op[_ Lazy[E]]() Op
func (m *Mutation[E]) Value[_ Lazy[E]](c Column[E]) (any, bool) // the AssignSet value of c, if assigned
func (m *Mutation[E]) Set[_ Lazy[E]](ss ...Setter[E])           // replaces an assignment of the same column, else appends; on a delete records an error
func (m *Mutation[E]) Where[_ Lazy[E]](ps ...Pred[E])           // appends; a zero or invalid predicate records an error

// Schema (schema.go)
func (s *Schema) Policy[E any](p Policy[E])
func (s *Schema) BeforeInsert[E any](f func(ctx context.Context, x *E) error)
func (s *Schema) BeforeUpdate[E any](f func(ctx context.Context, m *Mutation[E]) error)
func (s *Schema) BeforeDelete[E any](f func(ctx context.Context, m *Mutation[E]) error)

// EntityInfo (meta.go), unexported, filled by buildInfo:
//   policy       *policy // nil when none
//   beforeInsert []func(ctx context.Context, p unsafe.Pointer) error // mixins' first, in declaration order
//   beforeUpdate []func(ctx context.Context, st *Statement) error
//   beforeDelete []func(ctx context.Context, st *Statement) error
// type policy struct {
//   read, write func(ctx context.Context) (Predicate, error) // nil when not declared
//   insert      func(ctx context.Context, p unsafe.Pointer) error
// }

// Statement (statement.go), after AllRows:
//   Tenant       any    // the tenant scope applied; nil when unscoped
//   TenantBypass string // the AllTenants reason, when it applied
//   PolicyBypass string // the SkipPolicy reason, when it applied
//   DryRun       bool   // built for SQL(ctx), not sent
// ConflictClause gains: Where []Predicate // UPDATE branch condition (PostgreSQL, SQLite)
func (o Op) String() string // "select", "insert", "update", "delete"
```

**Rules for the `Schema` methods:**
- The type check. `E` (or the mixin's type) is compared as `elemType((*E)(nil))` against `s.typ` (the entity) or the mixin currently being applied.
  - `applyMixins` records the mixin's `reflect.Type` (as `any`) and its offset in new `Schema` fields while it calls `Mixin`, and clears them afterwards.
  - A hook typed on any other struct is a `SchemaError` naming the type.
- A mixin's `BeforeInsert` wraps `f` as `func(ctx, p) error { return f(ctx, (*M)(unsafe.Add(p, off))) }`. The entity's own uses offset 0. Mixin hooks are kept in a separate slice and placed before the entity's.
- `Policy` called in a `Mixin`, or called twice, is a `SchemaError`. `BeforeUpdate`/`BeforeDelete` in a `Mixin` is a `SchemaError`.
- `Policy[E]`'s `Read` and `Write` are wrapped as `func(ctx) (Predicate, error) { p, err := f(ctx); return Predicate(p), err }`. `Insert` is wrapped over `unsafe.Pointer`.
- The generic methods' bodies must stay tiny. Each wraps once and calls a non-generic helper that does the checks and stores the result. That keeps every instantiation's object small.

**The tenant column checks (in `buildInfo`, after `Schema` returns):**
- A `Tenant` field whose type is a pointer, or not comparable (`!rtype(c.Type).Comparable()`), is a `SchemaError`: "the tenant column must be NOT NULL and comparable".

**The test model:** `internal/testmodel/ticket.go`:

```go
package testmodel

import (
	"context"
	"errors"

	"github.com/syssam/velox2"
)

// userKey is the context key of the user the tests act as.
type userKey struct{}

// AsUser returns ctx acting as user; "admin" sees and changes every ticket.
func AsUser(ctx context.Context, user string) context.Context { return context.WithValue(ctx, userKey{}, user) }

func userOf(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }

// Stamp is a mixin whose BeforeInsert records who created the row.
type Stamp struct {
	CreatedBy string
}

// Mixin fills CreatedBy from the context's user when it is empty.
func (st *Stamp) Mixin(s *velox.Schema) {
	s.BeforeInsert(func(ctx context.Context, x *Stamp) error {
		if x.CreatedBy == "" {
			x.CreatedBy = userOf(ctx)
		}
		return nil
	})
}

// Ticket is tenant-scoped and has a policy and every kind of hook.
type Ticket struct {
	Stamp
	ID        int64
	OrgID     int64
	Owner     string
	Title     string
	Done      bool
	InvoiceID int64
	ParentID  *int64
	Invoice   velox.One[Invoice]
	Parent    velox.One[Ticket]
}

// Schema scopes tickets to OrgID and lets a user see and change only their own.
func (x *Ticket) Schema(s *velox.Schema) {
	s.Table("tickets")
	s.Tenant(&x.OrgID)
	s.Unique(&x.OrgID, &x.Title)
	own := func(ctx context.Context) (velox.Pred[Ticket], error) {
		switch u := userOf(ctx); u {
		case "":
			return velox.Pred[Ticket]{}, velox.Deny("no user")
		case "admin":
			return velox.Pred[Ticket]{}, nil
		default:
			return Tickets.Owner.EQ(u), nil
		}
	}
	s.Policy(velox.Policy[Ticket]{
		Read:  own,
		Write: own,
		Insert: func(ctx context.Context, x *Ticket) error {
			if u := userOf(ctx); u != "admin" && u != x.Owner {
				return velox.Deny("a ticket must be created by its owner")
			}
			return nil
		},
	})
	s.BeforeInsert(func(_ context.Context, x *Ticket) error {
		if x.Title == "" {
			return errors.New("a ticket needs a title")
		}
		return nil
	})
	s.BeforeUpdate(func(_ context.Context, m *velox.Mutation[Ticket]) error {
		if v, ok := m.Value(Tickets.Title); ok && v == "" {
			return errors.New("a ticket needs a title")
		}
		return nil
	})
	s.BeforeDelete(func(_ context.Context, m *velox.Mutation[Ticket]) error {
		m.Where(Tickets.Done.EQ(true)) // only finished tickets are deleted
		return nil
	})
}
```

Generate its handles. From the repository root: `go -C cmd/velox-gen build -o "$PWD/bin/velox-gen" . && bin/velox-gen ./...`. Then `bin/velox-gen -check ./...` must pass. If velox-gen's golden test in `cmd/velox-gen` compares the test model's files, the new file must match what it writes. Do not hand-edit generated files.

- [ ] **Step 1: Write the failing tests**
  - `scope_api_test.go`:
    - `WithTenant` then `AllTenants` → `AllTenants` wins, and the reverse.
    - `AllTenants(ctx, "")` leaves ctx unchanged.
    - `SkipPolicy(ctx, "")` likewise.
    - A `*PolicyError`'s text, `errors.Is(err, ErrDenied)` and `errors.As`.
    - `Deny("x")` matches `ErrDenied`, and its text is `x`.
    - `Op.String` for all four ops.
    - `TestTicketMetadata`:
      - `velox.CheckEntity[testmodel.Ticket]()` returns nil;
      - `Tenant` is OrgID's index.
  - `schema_policy_internal_test.go` builds small fixture entities with `buildInfo`, as `meta_test.go` does, and checks the `SchemaError`s:
    - a nullable tenant;
    - a slice tenant;
    - `Policy` twice;
    - `Policy` in a mixin;
    - `BeforeUpdate` in a mixin;
    - a hook typed on another struct.

    It also checks that a mixin's `BeforeInsert` receives the embedded struct, by calling `info.beforeInsert[0]` on an entity pointer and observing the mixin field change, and that mixin hooks come before the entity's.
- [ ] **Step 2: Run them to see them fail.** Command: `go test -run 'Tenant|Policy|Deny|OpString|Ticket|SchemaPolicy' .` Expected: they fail to compile, because the new API is undefined.
- [ ] **Step 3: Implement** the interfaces and rules above, write `ticket.go`, and generate `ticket_velox.go`.
- [ ] **Step 4: Run the tests.** Commands: `go test ./... && (cd cmd/velox-gen && go test ./...) && bin/velox-gen -check ./...`. Expected: PASS. `methods_test.go` passes, because `Mutation`'s methods are generic.
- [ ] **Step 5: Commit**: "Declare tenant context, policies and entity hooks; the Ticket test model".

---

### Task 2: Tenant scope for reads, updates and deletes

**Files:**
- Create: `scope.go`.
- Modify: `db.go`:
  - `plain` calls `db.scope(ctx, st)` after the guard;
  - `run` calls it after the `Rewrite` hooks and their checks, before `capLimit`.
- Modify the tests: `query_test.go` (`ctx = velox.AllTenants(context.Background(), "velox tests")`), `internal/itest/itest_test.go` (the same). Fix any test that inserts an `Invoice` with `OrgID` 0.
- Test: `tenant_test.go` (package `velox_test`).

**Interfaces:**
- Produces:
  - `func (db *DB) scope(ctx context.Context, st *Statement) error`. Task 3 adds policy to it and Task 4 adds the insert rules.
  - `func tenantPredicate(in *EntityInfo, id any) Predicate`.
  - `func checkTenantValue(in *EntityInfo, id any) error`, which returns `ErrTenantMismatch` (wrapped) when `reflect.TypeOf(id) != GoType(in.Columns[in.Tenant])`.

**Behaviour** (spec §2.1 table). For `st.Entity.Tenant >= 0` and op SELECT (Count included), UPDATE or DELETE:
- With `WithTenant(id)`:
  - check the type;
  - append `ColumnEQ(in.Tenant, id)` to `st.Where`;
  - set `st.Tenant = id`.
- With `AllTenants(reason)`: set `st.TenantBypass = reason` and add no predicate.
- With neither: return `fmt.Errorf("velox: %s: %w", name, ErrNoTenant)`.

For UPDATE, each assignment to the tenant column is checked:
- `AssignSet` to a value `== id` is allowed;
- any other value, or `AssignAdd`/`AssignSub`, is `ErrTenantImmutable`;
- under `AllTenants`, all are allowed.

`EXISTS` nodes (from `Has`/`HasWhere`) whose link target has a tenant column get the target's tenant predicate added to their kids, recursively.
- **Copy, never edit.** Build a new `node` with a new `kids` slice for every node on the path to a changed `EXISTS`. Leave unchanged subtrees shared. Assign the new top-level `Predicate` into `st.Where[i]`. `st.Where` itself is already a per-run slice, so element assignment is safe.
- **Tenant-type check inside `EXISTS`.** The target's tenant column may have a different Go type from the owner's. Check the type against the target and report `ErrTenantMismatch` naming the target.

The scope applies to relation loads automatically, because `fetch` runs statements through `plain`/`run`. `Statement.Tenant` and `TenantBypass` are set only when the entity (or an `EXISTS` target) is scoped.

**Cost:** on an entity with `Tenant < 0` and no `EXISTS` in `Where`, `scope` is a few branches. On a scoped read it is one `ctx.Value` lookup and one append. Add `TestTenantScopedOneRowBudget` to `budget_test.go`:
- set up as `TestOneRowAllocationBudget`, with `velox.WithTenant(context.Background(), int64(1))`;
- allow `vx <= raw + 9`, which is Core's +8 plus the one append;
- add `BenchmarkTenantScopedOneRow`.

- [ ] **Step 1: Write the failing tests** (`tenant_test.go`, `FakeExec`, Postgres dialect):
  - **The tenant predicate is added.** `db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5).All(velox.WithTenant(bg, int64(7)))` sends `SELECT … FROM "invoices" WHERE "paid" = $1 AND "org_id" = $2 LIMIT $3` with args `[true 7 5]`.
    - Use the real column list.
    - `bg` is `context.Background()`.
  - **Count, Update, Delete and Save** get the same predicate.
  - **No tenant in the context.** Every operation returns `ErrNoTenant`, with nothing sent.
  - **`AllTenants`** adds nothing. A hook sees `st.TenantBypass == "reason"`.
  - **Review Focus 2.** `WithTenant(bg, 7)` (an untyped constant, so `int`) returns `ErrTenantMismatch`, with nothing sent.
  - **Assignments to the tenant column:**
    - `Update…Set(I.OrgID.Set(7))` under tenant 7 is allowed;
    - `Set(I.OrgID.Set(8))` is `ErrTenantImmutable`;
    - `I.OrgID.Add(1)`, if `NumberCol` has `Add`, is `ErrTenantImmutable`;
    - under `AllTenants`, `Set(8)` is allowed.
  - **`Has` on a scoped target.** `db.Query[testmodel.Ticket]().Where(testmodel.Tickets.Invoice.Has()).Limit(1)` under tenant 7 renders the invoice tenant predicate inside the `EXISTS`, and the ticket's own outside. Policy is Task 3's: use `SkipPolicy` here.
  - **Review Focus 1.** A base query with a `Has` predicate, run 50 times concurrently under different tenants (`-race`), sends each tenant's own argument, and the base query's `SQL` is unchanged afterwards.
    - Until Task 6 replaces `SQL()`, compare `e.Log` entries.
    - Use a `FakeExec` per goroutine, through a fresh `OpenFake` per goroutine sharing nothing else.
  - **`With` loads.** `With(testmodel.Tickets.Parent)` under tenant 7 scopes the parent load statement too.
- [ ] **Step 2: Run them to see them fail.**
- [ ] **Step 3: Implement** `scope.go` and the calls in `plain`/`run`. Change the shared test contexts to `AllTenants`. Run `go test ./...` and fix tests that now fail for tenant reasons only. Typically these insert an Invoice with OrgID 0: give it an `OrgID`.
- [ ] **Step 4: Run the tests.** Commands: `go test -race ./... && go test -run Budget -count=3 .`
- [ ] **Step 5: Commit**: "Scope reads, updates and deletes to the context's tenant".

---

### Task 3: Policy for reads, updates and deletes

**Files:** modify `scope.go`; test `policy_test.go`.

**Behaviour** (spec §2.2). The policy is skipped under `SkipPolicy(reason)`, which sets `st.PolicyBypass = reason` when the entity has a policy. Otherwise:
- SELECT (Count included) ANDs `Read`'s predicate.
- UPDATE and DELETE AND both `Read`'s and `Write`'s.
- A zero `Predicate` adds nothing.
- An error returns `&PolicyError{Entity: in.Name, Op: st.Op, Err: err}` and nothing is sent.
- `EXISTS` nodes on a target with a `Read` policy get the target's `Read` predicate in their kids, with the same copy-on-write as Task 2.

Each policy function is called once per statement. Add the policy predicates after the tenant predicate. A `Save` whose row the policy hides affects 0 rows and returns `ErrNotFound`, unchanged.

- [ ] **Step 1: Write the failing tests** (`policy_test.go`). Contexts are `WithTenant(bg, int64(1))` plus `testmodel.AsUser(…)`.
  - A ticket SELECT as "ann" adds `"owner" = $n`. As "admin" it adds nothing. With no user it returns a `*PolicyError` with `Op == OpSelect`, `errors.Is(err, ErrDenied)` holds, the text is `velox: testmodel.Ticket: select denied by policy: no user`, and nothing is sent.
  - Update and Delete as "ann" get the owner predicate. For Delete, after Task 5's hook there is also a `done` predicate; assert with `strings.Contains`.
  - `Count` gets the read predicate.
  - `Has(Tickets.Parent)` puts the owner predicate inside the `EXISTS`.
  - `With(Tickets.Parent)` scopes the load.
  - `SkipPolicy(…, "support")` adds no owner predicate, and a hook sees `PolicyBypass == "support"`.
  - `Save` of a hidden ticket (`FakeExec.Affected = 0`) returns `ErrNotFound`.
- [ ] **Step 2: Run them to see them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...` and the budget tests.
- [ ] **Step 5: Commit**: "Apply each entity's read and write policy".

---

### Task 4: Inserts: row preparation, tenant and policy checks, upsert rules

**Files:**
- Modify: `insert.go`:
  - a new `prepareRow(ctx context.Context, in *EntityInfo, p unsafe.Pointer) error`, called for every row after `fillDefaults` and before `rowValues`, in `insertOne` and `insertMany`;
  - `insertOne` and `insertMany` take the ctx they already have.
- Modify: `scope.go` (the insert rules), `render.go` (`ConflictClause.Where`).
- Test: `insert_scope_test.go`.

**Behaviour:**

`prepareRow` runs, in order:
1. **Tenant fill.** For a scoped entity under `WithTenant(id)`, after the type check:
   - a zero tenant field is set to `id`, through `reflect.NewAt(GoType(col), unsafe.Add(p, col.off)).Elem()`;
   - a non-zero field different from `id` is `ErrTenantMismatch`.

   Under `AllTenants`, a zero field is `ErrNoTenant`. With neither, `ErrNoTenant`.
2. **`BeforeInsert` hooks.** Errors are wrapped `velox: <Entity>: before insert: %w`.
3. **The tenant check again,** on the result (Review Focus 4). Under `WithTenant(id)` the field must equal `id`.
4. **The policy's `Insert`,** unless `SkipPolicy` applies. Errors become `*PolicyError` with `Op: OpInsert`.

Scope rules for `OpInsert` with a `Conflict` that updates (`!DoNothing && len(Update) > 0`). `DoNothing` is exempt.
- **Tenant, PostgreSQL and SQLite.** Unless `AllTenants`, `Conflict.Columns` must contain `in.Tenant`; otherwise `ErrTenantUpsert`, "name the tenant column in OnConflict".
- **Tenant, MySQL.** Unless `AllTenants`, every unique index in `in.Indexes` and the primary key must contain `in.Tenant`; otherwise `ErrTenantUpsert`, naming the first index (by its columns) that does not.
- **Policy, PostgreSQL and SQLite.** Unless `SkipPolicy`, set `Conflict.Where` to the `Read` and `Write` predicates (zero ones omitted). On a policy error, return the `*PolicyError` (`Op: OpUpdate`).
- **Policy, MySQL.** Unless `SkipPolicy`, if `Read` or `Write` is declared, return `ErrPolicyUpsert`.
- **Copy before editing.** `st.Conflict` is per statement in `run` (it is cloned), but not in `plain`. Copy the clause before editing it.

Render, PostgreSQL and SQLite: `ON CONFLICT (…) DO UPDATE SET … WHERE <preds>`.
- The predicates must name the existing row. On PostgreSQL an unqualified column there can be ambiguous with `EXCLUDED`. Qualify every column in `Conflict.Where` with the table's quoted name, or render `INSERT INTO <table> AS vx0` and qualify with `vx0` if the writer's alias mechanism makes that simpler.
- SQLite resolves unqualified names to the existing row; qualifying is fine too.
- Task 8's real-database test decides. Golden SQL is pinned here.
- The render cache key must include `Conflict.Where`'s shape.

**After execution:** an upsert whose `Conflict.Where` velox set, and which affected fewer rows than it sent, returns `&PolicyError{Op: OpUpdate, Err: errors.New("the existing row is hidden from this upsert")}`.
- The affected count comes from `execWrite`'s result, or for `insertOne` with `RETURNING`, from whether a row came back.
- A `DoNothing` that skipped rows is not an error.

- [ ] **Step 1: Write the failing tests** (`insert_scope_test.go`):
  - **Tenant fill.** An insert of `Ticket{Owner: "ann", Title: "t"}` as ann under tenant 7 sets `OrgID` 7, and the mixin fills `CreatedBy` "ann".
  - **Mismatch.** `OrgID` 8 under tenant 7 is `ErrTenantMismatch`.
  - **`AllTenants`.** A zero `OrgID` is `ErrNoTenant`; `OrgID` 3 is inserted.
  - **The `BeforeInsert` hook.** An empty title fails with `velox: testmodel.Ticket: before insert: a ticket needs a title`.
  - **The policy's `Insert`.** As "bob" inserting `Owner: "ann"`, the result is a `*PolicyError` (`OpInsert`).
  - **Review Focus 4.** Write this case as an internal test (package `velox`) with a fixture entity whose `BeforeInsert` sets the tenant field to 9 under tenant 7. Expect `ErrTenantMismatch`.
  - **Every row is prepared.** `InsertAll` of 3 tickets prepares all 3.
  - **Upsert on PostgreSQL.** `OnConflict(T.ID).Update(T.Title)` is `ErrTenantUpsert`. `OnConflict(T.OrgID, T.Title).Update(T.Done)` renders `… DO UPDATE SET "done" = EXCLUDED."done" WHERE <qualified owner predicate>`, with golden SQL for PostgreSQL and SQLite.
  - **Upsert on MySQL.** `Ticket`'s primary key `ID` lacks `org_id`, so an updating upsert is `ErrTenantUpsert`. Under `AllTenants` it is `ErrPolicyUpsert`, because `Ticket` has a policy. Under `AllTenants` and `SkipPolicy` it is allowed.
  - **`DoNothing`** is allowed on all three dialects.
  - **A hidden existing row.** An upsert with a policy `WHERE` and `FakeExec.Affected` 0 (one row sent) returns a `*PolicyError`.
- [ ] **Step 2: Run them to see them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...`.
- [ ] **Step 5: Commit**: "Inserts: fill and check the tenant, run BeforeInsert and the policy; tenant- and policy-safe upserts".

---

### Task 5: BeforeUpdate, BeforeDelete and Save's write-back

**Files:**
- Modify `db.go`:
  - add an entity-hook stage in `run`, after the guard and before the hook copy and the `Rewrite` hooks: for `OpUpdate`, `st.Entity.beforeUpdate`; for `OpDelete`, `beforeDelete`;
  - `execWrite` uses `run` when `db.hooked() || len(st.Entity.beforeUpdate) > 0 || len(st.Entity.beforeDelete) > 0`;
  - `plain` never calls entity hooks.
- Modify `write.go` (`Save`'s write-back).
- Test: `hooks_test.go`.

**Behaviour:**
- Each hook gets a fresh `&Mutation[E]{st: st}`, made inside the non-generic wrapper stored by `Schema.BeforeUpdate`.
- **Errors.** A hook's error is wrapped `velox: <Entity>: before update: %w` (or `before delete`). If the hook returned nil but `m.err` is set, return `m.err` wrapped the same way.
- **The guard runs again after the hooks** (cheap), so a zero predicate added through `Mutation.Where` is caught.
- **Order:** guard, entity hooks, `Rewrite` hooks, scope.
- **Save's write-back** follows the ruling above. Replace `stored := slices.Clone(st.Set[named:])` with a pass over the final `st.Set` after success.
  - Use the existing `setField` path, guarded by `reflect` assignability.
  - Keep the guarantee that `*x` is unchanged after an error.

- [ ] **Step 1: Write the failing tests** (`hooks_test.go`, as "admin" under tenant 1):
  - **Empty title.** `Update[Ticket]().Set(T.Title.Set("")).Where(T.ID.EQ(1))` returns `velox: testmodel.Ticket: before update: a ticket needs a title`, with nothing sent.
  - **The delete hook adds its condition.** `Delete[Ticket]().Where(T.ID.EQ(1))` sends SQL containing `"done" = `.
  - **The hook sees the tenant-scoped statement before scoping.** It runs before scope, so the tenant predicate is not yet there. Assert the order with a `Rewrite` hook that records `len(st.Where)`.
  - **A `Mutation.Set` replaces and appends.** Add an internal test with a fixture entity whose `BeforeUpdate` calls `m.Set(X.Note.Set("n"))`. `Save(ctx, &x, X.Title)` then stores `Note` "n" into `x` after success. After an error (`FakeExec.Errs`), `x` is unchanged.
  - **`Set` on a delete's mutation** returns the recorded error.
  - **`Mutation.Value`** returns the assigned value and false for an unassigned column.
- [ ] **Step 2: Run them to see them fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...` and the budget tests.
- [ ] **Step 5: Commit**: "Run BeforeUpdate and BeforeDelete on the statement; Save stores what it sent".

---

### Task 6: SQL(ctx)

**Files:**
- Modify:
  - `query.go`, `write.go`, `insert.go`: `SQL(ctx context.Context) (string, []any, error)` on `Query`, `UpdateQuery`, `DeleteQuery`, and a new one on `InsertQuery`;
  - `db.go`: a `prepare` method shared by `run` and `SQL`.
- Modify every caller of the old `SQL()` in tests, docs and examples.
- Test: `sql_test.go`.

**Behaviour:**
- **What `SQL(ctx)` runs:** every stage `run` runs before execution, with `st.DryRun = true`: guard, entity hooks, the hook copy, `Rewrite` hooks with their checks, scope, `capLimit`, render.
- **What it returns:** `st.SQL`, `st.Args`, and the error the statement would fail with. Execution never happens; `Around` hooks are not called.
- **Refactor:** split `run` into `prepare(ctx, st) error` (the stages above) and the execution part. `SQL(ctx)` calls `prepare`.
- **Inserts:** `InsertQuery.SQL(ctx)` runs `fillDefaults` and `prepareRow` on the caller's rows, like `Exec`, and returns the first statement `Exec` would send. Refactor `insertOne`/`insertMany` so that building a statement is shared.
- **Docs:** every `SQL` doc comment says hooks run, and that `DryRun` tells them so.

- [ ] **Step 1: Write the failing tests** (`sql_test.go`):
  - **Review Focus 5.** For a scoped read, update, delete and insert (Ticket as ann under tenant 7), `SQL(ctx)` equals what `FakeExec` then receives from `Exec`/`All`: the same text and the same args.
  - **Refusals.** `SQL(ctx)` of an unbounded query returns `ErrUnbounded`. With no tenant, `ErrNoTenant`.
  - **Hooks see `DryRun`.** A `Rewrite` hook sees `DryRun == true` under `SQL` and false under `All`. `Around` hooks are not called by `SQL`.
- [ ] **Step 2: Run them to see them fail** (a compile error: `SQL` takes no arguments).
- [ ] **Step 3: Implement.** Then update every `SQL()` call in the root module, `internal/itest`, `cmd/velox-gen` (if any), `bench/scale` and doc comments.
- [ ] **Step 4: Run** `go test -race ./... && (cd cmd/velox-gen && go test ./...)`.
- [ ] **Step 5: Commit**: "SQL(ctx) prints exactly what would be sent".

---

### Task 7: Guards for scope: fuzz, compile cost, golden SQL

**Files:**
- Modify: `fuzz_test.go` (`FuzzScope`), `compilecost_test.go`, `render_test.go` (golden SQL), `scripts/ci-local.sh` and `.github/workflows/ci.yml` (a `FuzzScope` smoke run).

**Behaviour:**
- **`FuzzScope`** (package `velox`). From fuzz bytes, build `Ticket` statements:
  - op: SELECT, Count, UPDATE (with one `Set`) or DELETE;
  - a random `Where` tree of depth ≤ 3 over `EQ`/`In`/`And`/`Or`/`Not` and `Has(Parent)`/`HasWhere(Parent, …)`;
  - optionally `Rewrite` hooks.

  Run `db.scope` with `WithTenant(int64(n))` and `AsUser("ann")`. Assert:
  - the top-level `Where` contains, as separate elements, the tenant predicate (opEQ on the tenant column with value n) and the owner predicate;
  - every `EXISTS` node targeting `Ticket` contains both in its kids, at every depth;
  - the input `Where` trees are deep-equal to a copy taken before the run (Review Focus 1).

  Seed corpus: a few hand-made inputs. Smoke run in CI: `go test -run '^$' -fuzz '^FuzzScope$' -fuzztime 10s .`, next to the existing fuzz runs.
- **Compile cost.** In `compilecost_test.go`, add a case following the existing pattern: an entity package declaring a `Policy` and all three hooks grows by at most 8 KiB of object over the same package without them (spec §7). Check that `Policy[E]` and `Mutation[E]` hold no `reflect.Type`: extend the rule-7 check if one exists.
- **Golden SQL.** For `Ticket` as ann under tenant 7, pin the exact SQL on PostgreSQL, MySQL and SQLite for:
  - SELECT with `Has(Parent)`;
  - Count;
  - UPDATE;
  - DELETE;
  - an upsert on PostgreSQL and SQLite.

- [ ] **Step 1: Write the tests.** Check that `FuzzScope` fails when scope skips the `EXISTS` kids (a temporary edit, reverted).
- [ ] **Step 2: Run** `go test -race ./... && go test -run '^$' -fuzz '^FuzzScope$' -fuzztime 20s .`
- [ ] **Step 3: Commit**: "Fuzz the scope; pin its compile cost and SQL".

---

### Task 8: Two tenants on every database; documents; CI

**Files:**
- Modify: `internal/itest/itest_test.go`: a `tickets` table in each dialect's DDL and `setup`.
- Create: `internal/itest/scope_test.go`.
- Modify: `docs/history/specs/2026-10-14-policy-design.md`, `docs/history/specs/2026-10-14-core-design.md`, `docs/history/plans/2026-10-14-core-1-followups.md`.

**Seed data.** Two tenants (org 1 and org 2), each with tickets owned by "ann" and "bob". The `tickets` table has a unique `(org_id, title)` and the primary key `id`.

**Tests**, under `WithTenant(int64(1))` as "ann", unless stated otherwise. Prove that only org 1's rows owned by ann are seen or changed by each of these:
- `All`, `One`, `Iter`, `Count`;
- `With(Parent)`, `Has(Parent)`, `Has(Invoice)`;
- `Update`, `Delete` (only done ones), `Save`;
- `Insert` (the tenant is filled), `InsertAll`;
- an upsert on PostgreSQL and SQLite.

**What every test checks:**
- Each test reads the rows back through the `raw` handle (or `AllTenants` + `SkipPolicy`) and compares them.
- On MySQL, the updating upsert is refused with `ErrTenantUpsert`.
- As "admin", the result covers all of org 1. Under `AllTenants` + `SkipPolicy`, it covers both orgs.
- A hidden existing row in a PostgreSQL/SQLite upsert yields a `*PolicyError`, and the row is unchanged. This is the test that decides Task 4's qualification of the upsert `WHERE`.

**Documents:**
- `policy-design.md`: record this plan's rulings, in §2.1, §2.2, §3, §1.1 and §2.3 (`ConflictClause.Where`).
- `core-design.md` §4/§5: `SQL(ctx)`, and the new `Statement` fields.
- `core-1-followups.md`: a "Left by policy plan 2" section.

**Then:**
- [ ] **Step 1: Write the tests.**
- [ ] **Step 2: Run `scripts/ci-local.sh`.** Expected: exit 0 on every database.
- [ ] **Step 3: Update the documents.**
- [ ] **Step 4: Commit**: "Tenant and policy isolation on every database; spec and records".
