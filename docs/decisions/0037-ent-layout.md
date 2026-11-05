# 37. ent's layout: gen plus gen/runtime

Status: accepted, 2026-11-04. Supersedes 0036's layout: gen/entity, the
facade of re-exports and its plugin re-exports are gone. 0036's typed
binds by name, `NotLinkedError` and MetaOnce's retry stay.
Revised by 0038: the descriptor has no `Scope`. Revised by 0039: every
entity stays in gen, always; gen/runtime, its blank import and the runtime
warning are gone; gen imports the schema; `NotLinkedError` and the retry
are gone.

## Context

Under 0036, a schema with `Policy` or `Hooks` split the output: the
entities went in `gen/entity`, which the schema imported, and `gen` was a
facade, `type User = entity.User` and `var Users = entity.Users` for every
name, plus a `<plugin>_velox.go` of re-exports per plugin. Programs imported
`gen` alone, with no blank import.

The facade had costs of its own. gen held aliases where a reader looked for
types: gopls, `%T` and reflect named `entity.User`, so the descriptor
carried a `Name` to say `gen.User` in errors, and gqlimpl read it back. A
method of the user's on an entity had to move to `gen/entity`, as an alias
takes none, and velox-gen refused one in gen. The schema typed its methods
with `entity.User` while programs wrote `gen.User`. And `gen/entity`
appeared only when a schema had behaviour, so the layout of gen changed with
the first Policy. The user asked for ent's layout: all generated code in
gen, always.

## Decision

- **Every entity is in gen, always.** There is no `gen/entity` and no alias.
  The schema imports gen when it needs the entities' types:
  `func (Comment) Policy() velox.Policy[gen.Comment]`.
- **A schema that does not import gen** is read by gen itself, for its
  closure defaults and validators, as before. Nothing else is written.
- **A schema that imports gen** (velox-gen finds it with `go list -deps`, as
  before, and a `Policy` or `Hooks` implies it) is not imported by gen. gen
  holds `runtime_velox.go`: `VeloxRuntime`, a typed field per value gen
  needs from the schema (0036's second ruling, unchanged), and
  `SetVeloxRuntime`. `gen/runtime/runtime.go`, package runtime, imports gen
  and the schema, and its `init` sets them by name:
  `gen.SetVeloxRuntime(gen.VeloxRuntime{CommentPolicy: sch.Comment{}.Policy})`.
  A wrong signature fails to compile at its line there. The application
  imports it once, for its effect, in its main package and in each test
  package, `import _ "<module>/gen/runtime"`, as ent's `ent/runtime`.
- **gen/runtime only when something needs binding.** A schema that imports
  gen, whose defaults are named functions gen can call (`time.Now`) and
  that has no `Policy` or `Hooks`, gets no gen/runtime, and its entities
  check none.
- **Fail closed.** With gen/runtime, every entity's descriptor calls
  `veloxRuntime()` first. Without the import it panics with a
  `velox.NotLinkedError` that names the line to add: "gen: the schema's
  hooks, policies and defaults are not linked: add import _
  "<module>/gen/runtime" to the program's main package, or to the test".
  MetaOnce does not keep the failure, so an entity used before runtime's
  `init` works once it has run.
- **velox-gen warns** when it writes gen/runtime and no `.go` file of the
  module, tests included, imports it. It reads the files' imports only
  (`go/parser`, `ImportsOnly`), skipping what go skips, and the module's
  nested modules; it is a warning, as a program in another module may link
  it.
- **The schema may not import gen/runtime**, which imports it: directly, a
  refusal at the import; through another package, one that names the chain.
- **Plugins write into gen** in either case, with no re-export file.
  gqlgen's autobind of gen binds the types themselves.
- **The sweep removes the old layout.** `gen/entity`'s generated files go,
  and so do the old facade's content and a plugin's re-export file, each
  generated and no longer written. A file of the user's left in
  `gen/entity` stays, and velox-gen names it.
- `Descriptor.Name` is gone: the Go type is `gen.X`. gqlimpl's lookup of the
  name goes with it.
- `GeneratedCodeIsVersion11`.

## Consequences

- The trade-off is ent's: one blank import per program and per test
  package, instead of a facade of re-exports. Forgetting it fails closed,
  at the first statement, with the line to add, and velox-gen warns of a
  module that has none.
- velox's own test packages that use internal/testmodel import
  `internal/testmodel/runtime`, and so do the examples whose schemas have
  behaviour, in `main.go` and in a test package outside main.
- gen's types are what a reader, gopls, `%T` and reflect see; a user's
  method on an entity stays in gen with or without behaviour.
- Build cost does not grow: see the Review. The facade's 700 lines go, and
  so does a package, gen/entity, for gen/runtime's one file.

## Review

2026-11-04: measured with bench/scale's shape at N=100, a `Policy` per
entity, building `./gen/...` and `./schema` cold with every dependency
cached (darwin/arm64, five runs each, back to back, at a load average of
32 to 38, so wall time is noise):

| layout | packages | lines in gen | user CPU | max RSS |
| --- | --- | --- | --- | --- |
| 0036, split | gen, gen/entity | 13832 | 4.81 to 5.30 s, median 4.97 | 258 to 312 MB, median 285 |
| 0037, ent's | gen, gen/runtime | 13133 | 3.69 to 3.80 s, median 3.75 | 277 to 325 MB, median 285 |

The facade's aliases and wrappers cost about a second of CPU to type-check
and compile; memory is the same.

2026-11-04, one shape for behaviour (`GeneratedCodeIsVersion12`): a
descriptor names each part by one line, `Scope: commentScope`,
`Policy: rt.CommentPolicy` and `Hooks: rt.CommentHooks`, and velox
composes them in `Load`, in code that is not generic. The order is as
before: the scope's `Wrap` around the entity's policy, or around an empty
one, and the scope's `Fill` before the entity's hooks. gen's
`SetVeloxRuntime` binds the schema's methods with `BindPolicy` and
`BindHooks`, in closures velox calls once, when it builds the metadata.

The fields keep the erased types, `func() BoundPolicy` and
`func() BoundHooks`. Typed fields (`func() Policy[E]`) and a typed
`PolicyFunc[E]` were measured and refused: every generic function or type
instantiated for an entity carries the go.shape name of its struct, which
at bench/scale's shape is about a kilobyte, in each of its symbols. Typed
fields cost 175 KB per entity in `entityDescHooks` and 42% of gen's object
at N=100; `PolicyFunc[E]` cost 3.7%. The erased form instantiates what the
old generated closures did, `BindPolicy[E]` and `BindHooks[E]`, and a
scope's composition is `ScopeKit[E]`'s methods, so only a scoped entity
compiles it.

Measured in one worktree at 18c9d64d and at the change, so paths are
equal: `TestGenericTypesCompileSmall`'s entityDescriptor 138146 → 138384
bytes, entityDescUse 766412 → 766634, entityDescHooks 1032448 → 1032512,
policy and hooks 266036 → 265878. At N=100 with a `Policy` per entity, gen's
archive is 28697904 → 28701420 bytes and gen/runtime's 3558650 → 3557106,
with the same 17101 go.shape symbols. No budget changed.

2026-11-05, mixins with behaviour (`GeneratedCodeIsVersion14` and 15): the
`Scope:` line, `Descriptor.Scope`, `velox.Scoper` and `ScopeKit` are gone
(0038). A descriptor names `Policy`, `Hooks` and `Interceptors`, each a list
of bindings, and gen/runtime binds each entity's sources there, mixins first:
`velox.BindPolicy[gen.Comment](sch.InOrg{}.Policy, sch.Comment{}.Policy)`.
gen's `veloxBound`, which flagged the methods `SetVeloxRuntime` had bound,
was removed with version 14: gen's `VeloxRuntime` holds the bound lists, and
an entity without behaviour names none. The erased types stay, for the
reason above.
