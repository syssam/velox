# 38. Tenancy is a mixin; schema.Scoped is gone

Status: accepted, 2026-11-05. Supersedes 0030. Revises 0029, 0034 and 0037.
Revised by 0039: `InOrg`'s methods are plain and untyped; `scope.By` is gone;
the column check moves to `scopetest.Isolates`; velox-gen checks the column
names a behaviour method gives.
Revised by [0054](0054-fail-closed-runtime.md).

## Context

0030 marked a scoped column with `schema.Scoped("org")`. velox-gen wrote
`scope_velox.go`, with the key and a `scope.By` per entity, and the descriptor
named the scope in `Descriptor.Scope`, which velox composed outside the entity's
policy and hooks through `velox.Scoper` and `velox.ScopeKit`.

Since 0034 an entity's behaviour is declared on its schema, and since the mixin
work (spec `docs/history/superpowers/specs/2026-11-04-mixin-behaviour-design.md`) a
mixin may declare `Policy[E any]()`, `Hooks[E any]()` and `Interceptors[E any]()`,
generic methods that velox-gen binds for every entity that lists the mixin. That
is ent's shape for tenancy and soft delete (ent's privacytenant example). With
it, `Scoped` was a second way to say one thing, with its own codegen, its own
descriptor field, its own refusals and a generated file of package variables.

## Decision

Tenancy is a mixin the application writes over package `scope`:

```go
var OrgKey = scope.NewKey[int64]("org")

type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []schema.Field { return []schema.Field{field.Int64("org_id").Immutable()} }

func (InOrg) Policy[E any]() velox.Policy[E] { return scope.By[E](OrgKey, "org_id").Policy() }

func (InOrg) Hooks[E any]() []velox.Hook[E] {
	return []velox.Hook[E]{scope.By[E](OrgKey, "org_id").Fill()}
}
```

- `scope.By[E](key, column)` finds the column by name with `velox.Field`. It
  refuses a mutable or nullable column, and a column the entity lacks or types
  differently, as the entity's `*SchemaError` when velox builds its metadata.
- The mixin's policy and hook come first: velox-gen binds mixins in `Mixin()`
  order, then the entity's own. The policies are ANDed and none lifts another,
  so an entity's own `Policy` cannot drop the scope. The scope's `Fill` runs
  before the entity's hooks, which see `org_id`.
- `velox.WithoutPolicy(ctx, reason)` lifts it, as every policy.
- `scope/scopetest.Isolates` checks that an entity is confined; a test calls it
  per scoped entity.
- `schema.Scoped`, `ScopedAnnotation`, `scope_velox.go`, the generated key and
  scope variables, `Descriptor.Scope`, `velox.Scoper`, `velox.ScopeKit`,
  `scope.Scope.Wrap` and the codegen refusals of 0030 (rules 57 to 59) are
  removed. velox-gen's sweep removes a stale `scope_velox.go`.
- The key lives in the schema package: programs write `schema.OrgKey`, not
  `gen.OrgKey`.
- velox-graphql's Create doc named the fields an insert requires that the
  create input leaves out, except the `Scoped` column. It now leaves out any
  field the schema skips explicitly, `Skip(SkipMutationCreateInput)` or
  `CreateInput(false)`: the application or a hook sets it.

## Consequences

- One mechanism for behaviour on a schema: mixins and the entity's own
  methods. Soft delete is a mixin too (`schema/mixin/softdelete`).
- What `Scoped` checked at generate time is checked when velox builds the
  metadata, at the first statement or at `velox.CheckEntity`: a mutable or
  nullable column, or a wrong column name or type. velox-gen checks a literal
  name passed to `velox.Field` in a mixin's method; `scope.By`'s is checked at
  run time.
- An entity that leaves the mixin out of `Mixin()` is unscoped, and nothing
  refuses it. `scopetest.Isolates` in a test is the guard.
- A descriptor names the bound lists alone; an entity without behaviour
  compiles nothing for it, as before.
- `GeneratedCodeIsVersion15`: a scoped entity's descriptor has no `Scope:` line
  and gen has no `scope_velox.go`.

## Evidence

- `scope/scope_test.go` (`TestScopeANDsWithTheEntitysPolicy`; `scope.By` and `TestByRefuses` are gone (0039):
  scopetest's `TestIsolatesRefusesMutableColumn` and `TestIsolatesRefusesNullableColumn`,
  and `TestScopeKeyOfAnotherTypeFailsClosed` and `TestScopeMissingColumnFailsClosed`, cover its cases),
  `scope/scopetest`, `schema/mixin/softdelete`.
- `internal/codegen` golden `scoped`, now mixins, and `TestRenderNamesSchema` (once `TestRenderScopedBinding`).
- `cmd/velox-gen` `TestGenerateTenancyMixin`, `TestSweepRemovesScopeVelox`.
- `internal/apitest` `TestComposeScopePolicyHooks`.
- `examples/tenant` `TestInOrgIsolatesProject`; tracker, tour, routing and
  graphql list `InOrg`.
