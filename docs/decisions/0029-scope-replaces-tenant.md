# 29. A scope is a Policy and a hook; the core has no tenant

Status: accepted, 2026-11-02. Supersedes 0016, and the tenant-reference and join-row
parts of 0028. Revised by 0038: a scope is applied by a tenancy mixin. Revised
by 0039: package `scope` is `Key` with `Require` and `Match`; a scope is the
application's mixin.

## Context

Tenancy was a second row filter beside `Policy`, built into the engine: a
`Descriptor.Tenant` column, `WithTenant`, `WithAllTenants`, `TenantRefs` on join
entities, foreign-key checks, and its own errors. Soft delete, per-owner rows and
region filters needed the same filter and got none of it. Ent, Django, Rails, Hibernate
and Prisma keep the core general and let the application, or a small helper, express
tenancy through the general mechanism.

The full comparison and the design are in
`docs/superpowers/specs/2026-11-02-remove-tenant-design.md`.

## Decision

The core knows no tenant. Row scoping is a `Policy` plus a hook, made by the package
`velox/scope`:

- `scope.NewKey[V](name)` is a typed context key; `Key.With(ctx, v)` stores a value
  (and records it for `RowScope` through `velox.WithScopeValue`), `Key.From` reads it
  and reports false for the zero value.
- `scope.By(key, eq, fieldPtr)` gives `Policy()`, `Wrap(p)`, which ANDs the scope into a
  policy's Read and Write and checks an insert's column against the context, and
  `Fill()`, an insert hook that fills a zero column. (Revised by 0038: `scope.By[E](key,
  column)` finds the column by name, refuses a mutable or nullable one, and has no
  `Wrap`; a tenancy mixin returns its `Policy()` and `Fill()`, and velox ANDs the
  policies.)
- No value in the context is `scope.ErrNoScope`; a row for another value is
  `scope.ErrMismatch`. Both are policy errors, so a statement fails closed.
- `WithoutPolicy(ctx, reason)` lifts every policy, a scope included, so there is one
  bypass, with one reason that hooks see as `PolicyBypass`.
- The scope column is `Immutable()` in the schema, and the engine refuses to change it
  (`ErrImmutable`), which replaces `ErrTenantImmutable`.
- `WithShardFor` takes `func(ctx) (string, error)` and reads the context itself.

Removed with it: `field.Tenant()`, `Descriptor.Tenant` and `TenantRefs`, the
foreign-key checks (`ErrTenantRef`), the scoping of join rows, the id conversion of
0016, and `RowScope.Tenant`.

## Consequences

- One mechanism, tested once. A scope composes with any other policy rule.
- Three capabilities are smaller, and the CHANGELOG says so:
  - On MySQL an upsert that updates an entity with a Read or Write policy fails with
    `ErrPolicyUpsert`, as any unique key can conflict there.
  - A Rewrite hook that edits a row after the policy's `Insert` check is not checked
    against the scope again.
  - A join entity may not have its own Read policy; it is read through its two sides.
    A bare many-to-many join table is unscoped, and a foreign key that names another
    org's row is the application's to check, in a hook or a policy.
- `plugin.ModelVersion` is 3 and the model JSON version 4; generated code needs no new
  `GeneratedCodeIsVersionN`, as the descriptor lost fields and gained none.
- The generated code is smaller: no `Tenant: -1` line in each descriptor.

## Evidence

- `scope/`, with its tests; `examples/tenant`.
- `internal/apitest/scope_test.go`, `insert_scope_test.go`, `internal/engine` `TestInsertHookCannotMoveTheScope`.
- `internal/integration/scope_test.go` on PostgreSQL, MySQL and SQLite.
