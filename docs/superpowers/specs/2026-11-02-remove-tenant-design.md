# Remove Tenant: row scoping through Policy and hooks

- Status: implemented, 2026-11-02 (recorded in `docs/decisions/0029-scope-replaces-tenant.md`)
- Breaking: yes (pre-1.0, so allowed; see CHANGELOG "Unreleased")

## Why

`Tenant: -1` in every generated descriptor is the visible symptom. The cause is that
tenancy is a second, special row-filter beside the general one that already exists:

| | General (`Policy`) | Tenant (built in) |
|---|---|---|
| filter on reads | `Policy.Read(ctx) (Pred, error)` | `Descriptor.Tenant` + `WithTenant` |
| filter on update/delete | `Policy.Write` | the same |
| check on insert | `Policy.Insert(ctx, *E)` | tenant fill and check |
| bypass | `WithoutPolicy(ctx, reason)` | `WithAllTenants(ctx, reason)` |
| errors | `ErrDenied`-style | `ErrNoTenant`, `ErrTenantMismatch`, `ErrTenantRef` |

Soft delete, per-owner rows, region filters and "published only" all need the same
thing and get none of the tenant machinery. About 700 non-test lines (engine `scope.go`,
`tenantref.go`, `insert.go`, `policy.go`, `route.go`, codegen, GraphQL), 99 test files,
the `field.Tenant()` DSL, `TenantRefs`, `Statement.Tenant` and `RowScope.Tenant` exist
for one case.

## What the industry does

- **ent, Go:** no tenancy in the ORM. Privacy policies filter, hooks fill. (ADR 0007
  already says velox follows "privacy rules decide, hooks query".)
- **EF Core:** global query filter per entity, `IgnoreQueryFilters()` to bypass. Inserts
  are not filled.
- **MikroORM, Hibernate `@Filter`:** named filters, on by default, parameterised from
  the request, switched off by name.
- **Hibernate `@TenantId`, Rails `acts_as_tenant`:** the only ones that fill the column
  on insert, through a hook on save.
- **Django, SQLAlchemy, Prisma, GORM:** an event, scope or extension, or PostgreSQL
  row-level security.

Nobody keeps a tenant index in the schema descriptor. A filter, a fill hook and a
bypass are the standard parts.

## Decision

Delete tenancy from the core. Express it, and every similar rule, with what is already
there:

- **Filter reads and writes:** `Policy.Read` and `Policy.Write`.
- **Check inserts:** `Policy.Insert`.
- **Fill on insert:** an insert hook that sets the column from the context.
- **Bypass:** `WithoutPolicy(ctx, reason)`, which already exists.
- **Sharding:** `WithShardFor(func(ctx) (string, error))`. The function reads its own
  context value, not a tenant velox stores.
- **Immutable key:** `field.Immutable()`, which already exists.

A small package, `velox/scope`, gives the common case in one call so that no user
writes the filter, fill and check by hand and forgets one:

```go
// In the schema:
func (Issue) Policy() velox.Policy[gen.Issue] { return scope.By(gen.Issues.OrgID, orgFrom).Policy() }
func (Issue) Hooks()  velox.Hooks[gen.Issue]  { return scope.By(gen.Issues.OrgID, orgFrom).Hooks() }
```

`scope.By(col, from)` takes a column handle and `from func(ctx) (V, bool)`. It returns:
`Read` and `Write` as `col == v`, `Insert` that refuses a row whose column differs, a
hook that fills an unset column, and a refusal (`scope.ErrNoScope`) when `from` finds
nothing. The value's type is the column's type `V`, which removes the conversion rules
of ADR 0016 (`WithTenant` converts).

## Goals

1. No `Tenant` in `Descriptor`, `EntityInfo`, `Statement`, `RowScope`, `ColumnDescriptor`,
   the schema DSL, `schema.json`, or the generated code.
2. The tenant behaviour the tests pin today is reproducible with `scope.By` plus
   `WithoutPolicy`, and each such test moves to the new API, not deleted.
3. One filter mechanism, so a soft-delete or ownership rule is written the same way.

## Non-goals

- Row-level security in the database. `scope` stays in the ORM.
- A second named-filter system beside `Policy` (MikroORM style). `Policy` is the filter.

## Capabilities that go, and their replacement

| Today | After | Cost |
|---|---|---|
| `TenantRefs`: a write that sets an FK to a scoped row first checks the row is the tenant's, one SELECT per column | removed. Use a composite FK `(org_id, project_id) -> (org_id, id)`, which the doc already calls "the stronger guarantee", or a hook using `Mutation.DB` | one fewer round trip per write; the guarantee moves to the database |
| Join rows of a many-to-many edge scoped through the sides' `TenantRefs` | removed. A join entity may have Insert and Write policies, but not a Read policy: it is read through its two sides, whose Read policies apply | a bare many-to-many join table is unscoped; docs say so |
| `ErrNoTenant`, `ErrTenantMismatch`, `ErrTenantRef`, `Statement.Tenant`, `Statement.TenantBypass`, GraphQL "unauthenticated" mapping | `scope.ErrNoScope`; GraphQL maps it to the same code; hooks read `Statement.PolicyBypass` | renames |
| `WithTenant` / `WithAllTenants` | context key and `scope.With(ctx, v)` for the helper; `WithoutPolicy` for bypass | none |
| `field.Tenant()` | removed; use `Immutable()` plus `scope.By` | none |

## Anti-goals

- Do not keep `field.Tenant()` as an alias that secretly generates policy code. That is
  the same special case in a new place.
- Do not remove the isolation guarantee silently: the `examples/tenant` example and the
  docs must show the isolation test (tenant A cannot read, update, delete or reference
  tenant B's rows) passing on the new API.

## Order of work (each step ends with the whole suite green)

1. Add `velox/scope` with `By`, `With`, `ErrNoScope`, tests. Nothing removed yet.
2. Move `examples/tenant` and `examples/tracker` to `scope.By`; the tenant tests there
   pass on the new API.
3. Move the engine and apitest tests that use tenancy (99 files) to `scope.By` or delete
   those that test only the removed internals (`TenantRefs`, conversion, join scoping).
4. Remove `Tenant`/`TenantRefs` from `Descriptor`, `ColumnDescriptor`, `EntityInfo`,
   `Statement`, `RowScope`, the engine (`scope.go`, `tenantref.go`, the insert fill),
   codegen, the schema DSL, `schema.json`, GraphQL, `WithShardFor` (now a ctx function).
5. Docs: ADR 0016 superseded, new ADR, CHANGELOG, `docs/migrations-operations.md`,
   `SECURITY.md`, READMEs. Regenerate every generated file and the API snapshot.

## Left to the builder

The exact shape of `scope.By`'s return value (one struct with `Policy()` and `Hooks()`,
or two functions), the name `scope` versus `rowscope`, and the GraphQL annotation that
hides the scoped column from inputs.

## Open points I need a ruling on

1. **Drop `TenantRefs` and join-row scoping from the engine** (my default), or keep them
   as an opt-in `scope.CheckRefs` hook. Keeping them costs a hook that issues a SELECT
   per FK and a scoped join-entity helper.
2. **Sharding** (`WithShardFor(ctx, tenant)`): change the callback to `func(ctx)` (my
   default), a breaking signature change.
