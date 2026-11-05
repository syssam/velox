# 39. Behaviour is untyped, by column name; gen imports the schema

Status: accepted, 2026-11-05. Supersedes 0034's typing and package runtime,
0037's gen/runtime half and 0036's typed binds. Revises 0038, 0029 and 0007.
Spec: `docs/superpowers/specs/2026-11-05-untyped-schema-behaviour-design.md`.

## Context

Under 0034 and 0037 an entity's `Policy`, `Hooks` and `Interceptors` were
typed with gen (`velox.Policy[gen.Issue]`), so the schema package imported
gen, which is generated from it. A new project's schema did not compile
before gen existed. The cycle was the reason for `gen/runtime`, its blank
import, the loader's prune overlay and `NotLinkedError`.

A mixin's methods were generic over the entity, and tenancy read
`func (InOrg) Policy[E any]() velox.Policy[E] { return scope.By[E](OrgKey,
"org_id").Policy() }`: three layers deep. ent's mixin methods are not
generic.

## Decision

Behaviour declared in the schema is untyped and names columns by name, as
ent's `ent.Policy`, `ent.Hook` and generic `ent.Mutation` do.

- `velox.Cond` is an instance of `Pred`, so `And`, `Or` and `Not` serve both
  forms. `velox.C("org_id")` gives a column's operators; `velox.Edge("doc")`
  gives `Has` and `HasWhere`. `velox.Allow()` matches every row. A name is
  resolved when the statement runs: a name the entity has no column of, a
  value of another kind, a text operator on a number or the zero `Cond`
  fails the statement.
- `velox.Policy{Read, Write, Insert}`: `Read` and `Write` return a `Cond`;
  `Insert` takes a `velox.Row` (`Value`, `IsZero`, `Entity`). A `Row.Set` in a
  policy fails the insert.
- `type Hook func(next Mutator) Mutator` over `*velox.Mutation`: `Op`, `Set`,
  `Fill`, `Value`, `Rows`, `Where`, `Conds`, `DB`, `Table`, `Affected`,
  `SetAffected`. `velox.Before(f, ops...)` runs `f`, then next.
- `velox.Interceptor{Filter}` returns a `Cond`.
- A mixin's methods are plain: `func (InOrg) Policy() velox.Policy`.
  velox-gen refuses a generic one, and the typed shape, with the shape it
  binds.
- gen imports the schema package as `sch` and lists method values in each
  descriptor: `Policy: []func() velox.Policy{sch.InOrg{}.Policy,
  sch.Issue{}.Policy}`, mixins first, in `Mixin()` order. There is one mode.
  velox-gen refuses a schema package that imports gen, directly (at the
  import) or through other packages (with the chain).
- A relation a behaviour method names with `velox.Edge`, by a constant,
  reached from the method through same-package helpers, is wrapped in
  `velox.Linked[T]` in its descriptor, so that its EXISTS applies the
  target's Read policy. velox-gen finds them with go/types. A name that is
  not a constant is refused at its line; a relation not linked fails the
  statement.
- `EdgeTo`, `EdgeBidi` and `EdgeThrough` take the schema's edge name.
- A hook that must use gen's typed API is a run-time hook:
  `velox.WithHooks[gen.Task](hooks...)`, a DB option, which runs after the
  schema's hooks, for that DB, its transactions and shards.
- Package `scope` is `Key` with `With`, `From`, `Require` and `Match`;
  `scope.By` and `Scope` are gone. `scopetest.Isolates` checks the column is
  immutable, NOT NULL and of the key's type.
- Removed: `Policy[E]`, `Hook[E]`, `Mutator[E]`, `MutateFunc[E]`,
  `Mutation[E]`, `Interceptor[E]`, `Named[E, V]`, `velox.Field`, `Bind*`,
  `Bound*`, `Allow[E]` and the handles' `ValueIn`. `NotLinkedError` and
  MetaOnce's retry go in Phase 4 of the spec.
- `GeneratedCodeIsVersion16`.

## Consequences

- The schema compiles before gen exists. gen/runtime, its blank import, the
  runtime warning and `veloxRuntime()` are gone; velox-gen's sweep removes an
  older gen/runtime and names a user's file left there.
- A condition's names are checked when its statement runs, not at compile
  time. Phase 3 of the spec adds velox-gen's check of constant names.
- Typed handles (`gen.Issues.AuthorID.EQ`) stay for application code.
- Compile cost (`TestGenericTypesCompileSmall`, darwin/arm64): a Policy and
  three hooks add 88788 bytes to an entity's package, down from 287980;
  entityDescHooks is 861366 bytes, down from 1055932, and entityDescMixin
  871618, down from 1130512. The Descriptor's fields name `velox.Hook`,
  whose `Mutation.DB` names `*DB`, so the export data of an entity package
  that used no DB grows: entityDescriptor is 284684 bytes, up from 143458.
  A package that uses a DB already pays it: entityDescUse grew 0.6%.
- Run time: a by-name policy resolves its names against the entity's index
  when the statement is prepared. BenchmarkPolicyByName and
  BenchmarkPolicyByIndex (`internal/engine`, SQLite in memory) allocate the
  same, 36 allocations and 1296 bytes per SELECT; their fastest of ten
  interleaved runs were 14.7 and 14.1 µs.

## Evidence

- `internal/apitest/untyped_test.go` and the renamed policy, hook and
  interceptor tests; `scope`'s `TestKeyRequire` and `TestKeyMatch`;
  `scopetest`'s `TestIsolatesRefusesMutableColumn` and
  `TestIsolatesRefusesNullableColumn`.
- `internal/codegen`: goldens `blog`, `scoped`, `mixinbehaviour` and
  `mixinplain` with no runtime files; `TestSchemaMethodsRefusesTypedShape`,
  `TestMixinMethodRefusesGeneric`, `TestBehaviourRefusesSchemaImportingGen`,
  `TestRenderBindsMethodValues`, `TestEdgeLinkedWhenBehaviourNamesIt`,
  `TestEdgeNameMustBeConstant`.
- `cmd/velox-gen`: `TestGenerateWithBehaviourWithoutGen`,
  `TestSweepRemovesGenRuntime`, `TestGenerateRefusesSchemaImportingGen`.
- The examples, regenerated, with no gen/runtime.
