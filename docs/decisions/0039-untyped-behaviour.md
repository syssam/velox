# 39. Behaviour is untyped, by column name; gen imports the schema

Status: accepted, 2026-11-05. Supersedes 0034's typing and package runtime,
0037's gen/runtime half and 0036's typed binds. Revises 0038, 0029 and 0007.
Spec: `docs/superpowers/specs/2026-11-05-untyped-schema-behaviour-design.md`.
Revised by 0040: a descriptor names the schema's value, `Schema:
schema.Issue{}`, in place of the method lists, gen imports the schema by its
own name, and `velox.C`, `velox.Edge` and `Cond` are ent's `velox.FieldEQ`,
`velox.HasEdge` and `velox.Predicate`.

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
- velox-gen checks, on the same scan, each constant name a behaviour
  method reaches: `velox.C`'s, `velox.Edge`'s, a `Mutation`'s `Set`,
  `Fill` and `Value`, and a `Row`'s `Value`, `IsZero` and `Set`. The
  name must be a column (a relation, for `Edge`) of every entity the
  method serves: an entity's own, and each entity that lists the mixin.
  The conditions of `HasWhere` name the target's columns. A value of known
  type must be of the column's kind class, a constant must fit the
  column's type, nil fits only a nullable column's `Set` or `Fill`, a text
  operator needs a text column and `IsNull` a nullable one. Each refusal
  is a `file:line` error that names the entity and the method; a name
  velox-gen cannot read is a warning. A call in another package is left
  to run time.
- `EdgeTo`, `EdgeBidi` and `EdgeThrough` take the schema's edge name.
- A hook that must use gen's typed API is a run-time hook:
  `velox.WithHooks[gen.Task](hooks...)`, a DB option, which runs after the
  schema's hooks, for that DB, its transactions and shards.
- Package `scope` is `Key` with `With`, `From`, `Require` and `Match`;
  `scope.By` and `Scope` are gone. `scopetest.Isolates` checks the column is
  immutable, NOT NULL and of the key's type.
- Removed: `Policy[E]`, `Hook[E]`, `Mutator[E]`, `MutateFunc[E]`,
  `Mutation[E]`, `Interceptor[E]`, `Named[E, V]`, `velox.Field`, `Bind*`,
  `Bound*`, `Allow[E]` and the handles' `ValueIn`; then `NotLinkedError`,
  MetaOnce's retry of a build that panicked with one, and the loader's
  prune overlay and `listCycle`. A failed build is kept, as any other.
- `GeneratedCodeIsVersion16`.

## Consequences

- The schema compiles before gen exists. gen/runtime, its blank import, the
  runtime warning and `veloxRuntime()` are gone; velox-gen's sweep removes an
  older gen/runtime and names a user's file left there.
- A condition's names are not checked at compile time. velox-gen checks the
  constant ones; the statement checks every name when it runs.
- Typed handles (`gen.Issues.AuthorID.EQ`) stay for application code.
- Compile cost (`TestGenericTypesCompileSmall`, darwin/arm64): a Policy and
  three hooks add 88776 bytes to an entity's package, down from 287980;
  entityDescHooks is 858964 bytes, down from 1055932, and entityDescMixin
  869212, down from 1130512. The Descriptor's fields name `velox.Hook`,
  whose `Mutation.DB` names `*DB`, so the export data of an entity package
  that used no DB grows: entityDescriptor is 282970 bytes, up from 143458.
  A package that uses a DB already pays it: entityDescUse grew 0.3%. At
  N=100 with a Policy per entity, the build loses a package and 42% of
  its archive bytes and 30% of its CPU (Review).
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
  `TestEdgeNameMustBeConstant`; the column checks' `TestCheckColumns*` in
  `behaviourcheck_test.go`.
- `cmd/velox-gen`: `TestGenerateWithBehaviourWithoutGen`,
  `TestSweepRemovesGenRuntime`, `TestGenerateRefusesSchemaImportingGen`,
  `TestGenerateRefusesWrongColumns` (golden `testdata/wrongcolumn`).
- The examples, regenerated, with no gen/runtime.
- Phase 4: `TestMetaOnceKeepsFailure`, `TestMarshalLeavesOwnBehaviour`,
  `TestBehaviourRefusesSchemaImportingGen`'s `cycle`, and
  `TestGenerateNamesTheChainOfACycle`, which passes without `listCycle`.

## Review

2026-11-05: 0037's Review variant, bench/scale's shape at N=100 with a
`Policy` per entity, building `./gen/...` and `./schema` cold with every
dependency cached (darwin/arm64, three runs each, back to back, each tree
at a path of the same length):

| tree | packages | lines in gen | user CPU | max RSS | archives |
| --- | --- | --- | --- | --- | --- |
| d5ac7a1b, typed, gen/runtime | gen, gen/runtime, schema | 13457 | 5.03 to 5.12 s | 260 to 285 MB | 50.69 MB |
| 7eac9b9d, untyped (Phase 3) | gen, schema | 13084 | 3.05 to 3.52 s | 270 to 288 MB | 29.68 MB |
| Phase 4 | gen, schema | 13084 | 3.43 to 3.58 s | 267 to 276 MB | 29.44 MB |

The archives are gen 24375300, gen/runtime 10465702 and schema 15846552
bytes typed; gen 24699782 and schema 4980680 at Phase 3; gen 24463186 and
schema 4980680 at Phase 4. The schema package loses its `Policy[gen.X]`
instances, 69% of its bytes. gen grows 0.4% from typed to Phase 4: each
descriptor lists method values in place of `rt.XPolicy`. Wall time fell
from 1.8 s to 1.2 s: the critical path is schema → gen, not
gen → schema → gen/runtime.

`MetaOnce.Load` is `//go:noinline`. Without the retry, Load became
inlinable, and inlined with its closure into each entity's `VeloxMeta` it
took gen to 25806666 bytes and 3.6 to 3.9 s of CPU. Not inlined, gen is
1% smaller than at Phase 3. `TestGenericTypesCompileSmall` in one
worktree, Phase 4 against Phase 3's bounds: entityDescriptor 282970
bytes, entityDescUse 770188, entityDescHooks 858964, entityDescMixin
869212, policy and hooks 88776. The entity rows' bounds are each
measurement plus 1.2%, rounded up to the next KiB.
