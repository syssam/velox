# 36. A schema with behaviour splits gen into gen/entity and gen

Status: accepted, 2026-11-04. Revises 0034's package runtime.

## Context

Under 0034, a schema's `Policy` and `Hooks` are typed with gen
(`velox.Policy[gen.Doc]`), so the schema imports gen and gen cannot import the
schema. velox-gen broke the cycle as ent does: a generated `gen/runtime`
imported both and gave gen the schema's values in its `init`. A program
imported it for its effect, `import _ ".../gen/runtime"`.

That blank import was the one line every program and every test package had to
remember. Forgetting it failed closed, but it failed in each new test package
and each new command. `gen/runtime` held a struct with three fields per entity
(the schema, its policy and its hooks), and gen had a second file to bind it.

## Decision

- **The split.** When a schema declares `Policy` or `Hooks`, or imports
  `gen/entity`, velox-gen writes the entities into `gen/entity`, package
  `entity`. It holds every entity, handle, enum, descriptor and scope, and does
  not import the schema package. The schema imports it:
  `func (Doc) Policy() velox.Policy[entity.Doc]`.
- **gen is the facade.** `gen` imports `gen/entity` and the schema. Each file of
  `gen/entity` but `runtime_velox.go`, which only gen calls, has a file of the
  same name in `gen` that re-exports its exported names: a type is an alias (a generic type a generic alias), a const or var is
  `X = entity.X`, a function is a wrapper that passes its parameters and type
  parameters on. A function whose signature names an unexported type is a var
  instead; a generic one is refused, since it cannot be re-exported.
  `entity/entity.go` is re-exported into `gen/velox.go`, which also holds the
  `init`: `entity.SetVeloxRuntime(entity.VeloxRuntime{Doc: &sch.Doc{}, ...})`.
  A program imports gen, as it would without behaviour, and links the schema
  with it. There is no `gen/runtime` and no blank import.
- **One schema value per entity.** `VeloxRuntime` has one field per entity that
  needs its schema: `schema.Interface`, or `any` for a join entity, whose
  schema is an empty struct. The descriptor reads `Policy` and `Hooks` from it
  by an interface assertion (`veloxPolicy[E]`, `veloxHooks[E]`). A value
  without the method panics with the method and its signature, and velox turns
  the panic into the entity's error at its first use. `gen/velox.go` also
  holds a compile check of each method, `(*sch.Doc).Policy`, so a wrong
  signature fails the build of gen, at that line.
- **Fail closed.** A program that links `gen/entity` without `gen` fails at
  the first use of any entity, with "entity: the schema's hooks, policies and
  defaults are not linked: import "…/gen", which re-exports "…/gen/entity" and
  links the schema, in the program or test".
- **The schema may not import gen.** velox-gen refuses a schema package that
  imports gen, at the import: "the schema package imports "…/gen", the
  generated package, which imports the schema package to bind its Hooks and
  Policy: import "…/gen/entity", which holds the entities, and write
  entity.User for gen.User". It refuses a package the schema imports that
  imports gen too, naming the chain. A helper package may import `gen/entity`;
  that alone splits gen.
- **Plain mode is unchanged.** A schema with neither method, which does not
  import `gen/entity`, gets one package gen, as before. The switch either way
  rewrites every file, and the sweep removes `gen/entity` when it is no longer
  written.
- **Plugins write into gen/entity.** In split mode velox-gen gives a plugin
  `out_package` `entity` and `out_path` `…/gen/entity`, so velox-graphql's Go
  files sit beside the entities whose methods they declare. velox-gen
  re-exports each Go file the plugin writes directly in `gen/entity`.
  velox-graphql's `velox.graphql` moves to `gen/entity`; `gen/**/*.graphql` in
  gqlgen.yml finds it in either layout, and gqlgen's autobind of gen binds
  through the aliases.
- **The inner package is named entity**, after what it holds, and its first
  file is `entity.go`: the stale-file sweep skips a directory holding a
  generated `velox.go` as another run's output, and must enter `gen/entity`.
- `GeneratedCodeIsVersion8`.

## Consequences

- No program or test imports anything for its effect. velox's own tests and
  the examples drop `_ ".../gen/runtime"`.
- Errors and telemetry name the entity `gen.Doc` in either layout: the
  descriptor's new `Name` field, which velox-gen sets in split mode, overrides
  the Go type's `entity.Doc`. `%T` and reflect still say `entity.Doc`, as
  does the error of a descriptor that panics before it returns.
- A file of the user's in `gen/entity` is not re-exported; one in `gen` may not
  declare a name gen re-exports.
- A Go type of a field still cannot live in the schema package: `gen/entity`
  would import it, and it imports `gen/entity`.
- Build cost at 100 entities, each with a `Policy` (darwin/arm64, a cache warm
  with every dependency, three runs): compiling gen and the schema took 4.8 s
  of CPU before and 5.2 s after (+8%); building a program that uses one entity
  5.3 s and 5.7 s (+7%). The binary grows from 5.22 MB to 5.60 MB (+7%): the
  schema values are held as interfaces, so the linker keeps their types and
  methods. gen has 13 366 lines before and 14 685 after. The repocheck
  compile-cost tests measure the generic runtime, which does not change.
