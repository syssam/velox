# 37. ent's layout: gen plus gen/runtime

Status: accepted, 2026-11-04. Supersedes 0036's layout: gen/entity, the
facade of re-exports and its plugin re-exports are gone. 0036's typed
binds by name, `NotLinkedError` and MetaOnce's retry stay.

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
- Build cost at 100 entities, each with a `Policy` (darwin/arm64, a cache
  warm with every dependency, the app's packages built cold, five runs at a
  load average near 30): see the second section of this record's Review
  once measured. The facade's 700-odd lines and one package go.

## Review

2026-11-04: measured with bench/scale's shape at N=100, a `Policy` per
entity, building `./gen/...` and `./schema` with every dependency cached.
