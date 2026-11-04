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
  instead; a generic one is refused, since it cannot be re-exported. (The
  second Review, point 2, puts every re-export in `gen/velox.go`.)
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
  links the schema, in the program or test". The failure is not kept: an
  entity touched before gen's `init` runs, from a package-level var or an
  `init` of the schema or a helper, which Go initializes first, fails then
  and works once gen has bound the schema. The panic is a `velox.NotLinkedError`,
  and MetaOnce does not keep a build that panics with one (second Review,
  point 1).
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
  written. A file of the user's there stays, and velox-gen warns, naming
  it: a plain gen does not build it.
- **Plugins write into gen/entity.** In split mode velox-gen gives a plugin
  `out_package` `entity` and `out_path` `…/gen/entity`, so velox-graphql's Go
  files sit beside the entities whose methods they declare. velox-gen
  re-exports each Go file the plugin writes directly in `gen/entity` (in
  one file per plugin since the second Review, point 2).
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
  the Go type's `entity.Doc`, and so does the refusal of an unbound method.
  `%T` and reflect still say `entity.Doc`, as does an error raised while
  the descriptor is built, before it names the entity: the not-linked
  panic, and a relation whose target's type is wrong.
- A file of the user's in `gen/entity` is not re-exported; one in `gen` may not
  declare a name gen re-exports, nor a method on one: `gen.User` is an alias,
  and Go takes no method on another package's type. velox-gen refuses such a
  method at its line, with "move it to gen/entity", rather than write a gen
  that does not build. A method written in plain mode moves when the first
  Policy or Hooks splits gen.
- A Go type of a field still cannot live in the schema package: `gen/entity`
  would import it, and it imports `gen/entity`.
- Build cost at 100 entities, each with a `Policy` (darwin/arm64, a cache warm
  with every dependency, three runs): compiling gen and the schema took 4.8 s
  of CPU before and 5.2 s after (+8%); building a program that uses one entity
  5.3 s and 5.7 s (+7%). The binary grows from 5.22 MB to 5.60 MB (+7%): the
  schema values are held as interfaces, so the linker keeps their types and
  methods. gen has 13 366 lines before and 14 685 after. The repocheck
  compile-cost tests measure the generic runtime, which does not change.

## Review

2026-11-04: the generated descriptor read every default, validator and
method off the schema by position, `rt.Comment.Mixin()[0].Fields()[1]`, and
the methods by an interface assertion per entity. It is now written as by
hand. The rulings, all applied (`GeneratedCodeIsVersion9`):

1. **A named function is called by its name.** The loader names each
   default, update default and validator function with
   `runtime.FuncForPC`. An exported function declared at package level is
   called as `pkg.Name`, in the entity's file, when the entities' package
   can import its package: go list says it loads and is not package main,
   Go's internal rule allows it, and it imports neither gen nor gen/entity
   (nor, in split mode, the schema package). `mixin.Time` is
   `func() any { return time.Now() }`. A closure, a method value, a method,
   an instance of a generic function, an unexported function, one of
   package main or a vendored package, or one whose type is not the one
   the column calls, is not (`TestFuncRef`, `TestSplitFuncName`,
   `TestInternalOK`).
2. **The rest is bound by name and typed.** `entity.VeloxRuntime` has a
   field per value gen gives an entity: `CommentPolicy func()
   velox.Policy[Comment]`, `GroupTokenDefault func() string`,
   `PostSlugValidator func(string) error`. `gen/velox.go` sets each by
   name: a method value, `sch.Comment{}.Policy` (or `(&sch.Comment{}).Policy`
   for a pointer receiver); the schema package's own exported function,
   `sch.NewSlug`; else a read of the field's descriptor by its name,
   `schema.FieldDescriptor(&sch.Group{}, "token").DefaultFunc.(func()
   string)`. `schema.FieldDescriptor` is new; it finds a mixin's field too,
   and panics, naming the schema and the field, when the generated code is
   older than the schema. A wrong Policy signature fails to compile at its
   line of `gen/velox.go` (`TestWrongPolicySignatureDoesNotCompile`).
   `veloxPolicy` and `veloxHooks`, a generic instance per entity, are gone,
   and so are the compile-check vars they needed.
3. In plain mode the entity's file calls the function by name as well, and
   reads the others with `schema.FieldDescriptor` in its Descriptor
   function (`TestFunctionDefaults`, both modes).
4. Fail-closed is kept: `veloxRuntime()` still runs first in every
   Descriptor, with the same message, and an entity used before gen's init
   works once gen has run.
5. Cost: gen of examples/tracker went from 1 345 lines to 1 309,
   internal/testmodel's from 1 006 to 967. The repocheck compile-cost
   tests measure the generic runtime, which does not change.

2026-11-04, second: a review of the generated code. The rulings, all
applied (`GeneratedCodeIsVersion10`):

1. **The not-linked failure is the runtime's.** `veloxRuntime()` panics
   with `velox.NotLinkedError`, a string type. `engine.BuildDescriptor` reports
   such a panic, and `MetaOnce.Load` returns its error Meta without keeping
   it, so the next use builds again (`TestMetaOnceRetriesNotLinked`). Any
   other failure is kept, as before. VeloxMeta is the one line it is in
   plain mode, `return projectMeta.Load(projectDescriptor)`. The closed e2e
   and `TestEntityUsedBeforeGenInit` still pass.
2. **gen is one file.** In split mode gen holds `velox.go` alone: the
   package comment, the init, and the re-exports of every file velox-gen
   writes in gen/entity, as one `type (…)`, one `const (…)` and one
   `var (…)` declaration in the order of the files. A plugin's Go files in
   gen/entity are re-exported the same way in one file per plugin,
   `<plugin>_velox.go`: velox-graphql's is `velox-graphql_velox.go`.
   gqlgen's autobind of gen binds through the aliases wherever they are
   (`TestCollectRun`). The sweep removes the per-file re-exports an older
   velox-gen wrote (`TestSplitFacadeIsOneFile`). examples/tracker's gen
   went from 10 files of 139 lines to 1 of 96.
