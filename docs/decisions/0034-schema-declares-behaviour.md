# 34. The schema declares an entity's hooks and policy; gen is all generated

Status: accepted, 2026-11-03. Supersedes the hand-written part of 0030. Builds on 0033.

## Context

Until now an entity's `Policy` and `Hooks` were methods written by hand on the
generated type, in a file of one's own in `gen/`: a method must be in its type's
package. `gen/` was therefore half generated, half the user's, unlike ent's, where the
schema declares `Policy()` and `Hooks()` and the generated package is entirely the
generator's. The examples carried `task_hooks.go`, `doc_policy.go`, `policy.go` and
context helpers (`WithUser`, `WithRole`, `Principal`) in `gen/`.

Moving the methods to the schema has two costs, both ent's:

- **An import cycle.** The methods are typed with the generated entities
  (`velox.Policy[gen.Doc]`), so the schema imports gen; gen imported the schema to
  read default functions and validators. ent breaks it with a generated package
  `ent/runtime`, which imports both and registers the schema's pieces into ent.
- **Bootstrapping.** The generator compiles the schema package to load it. When the
  schema imports gen, a stale gen (a field the hook uses that gen lacks yet), a cycle
  with the old gen, or no gen at all makes the schema fail to compile, and the
  generation that would fix gen cannot run. ent users meet this as "entc fails until I
  comment out my hooks", and ent added `--feature schema/snapshot` for it.

And a risk of ent's: a program that does not import `ent/runtime` runs without its hooks
and policies, silently.

## Decision

- **The schema declares behaviour.** `func (Doc) Policy() velox.Policy[gen.Doc]` and
  `func (Task) Hooks() []velox.Hook[gen.Task]`, on the schema type, value or pointer
  receiver. velox-gen reads them from the schema package's source (`SchemaMethods`) and
  refuses another shape at its line, or a method on a type that is no entity's. A
  many-to-many join entity, which has no schema, gets them on an empty struct of its
  name declared in the schema package (`type UserFriend struct{}`).
- **gen is all generated.** velox-gen refuses a `Policy` or `Hooks` method on an
  entity in the generated package, and says where it goes. Other files of the user's
  in `gen/` are still allowed; the examples have none.
- **package runtime.** When the schema declares any, velox-gen writes
  `gen/runtime_velox.go`, the type `VeloxRuntime` with one field per thing gen takes
  from the schema (`Invoice schema.Interface` for the defaults and validators,
  `InvoiceHooks`, `InvoicePolicy`) and `SetVeloxRuntime`, and
  `gen/runtime/runtime_velox.go`, package runtime, whose `init` fills it with the
  schema's values and method values (`(&sch.Invoice{}).Policy`). gen then does not
  import the schema. A method of the wrong type fails to compile there.
- **Fail closed.** In this mode every entity's Descriptor first calls `veloxRuntime()`,
  which panics, naming the import to add, when package runtime is not linked; velox
  turns the panic into the entity's error at its first use. A program that forgot
  `import _ ".../gen/runtime"` fails on its first statement instead of running without
  its policies. Every entity checks, not only those with behaviour, so the mistake
  shows up in any test.
- **Bootstrapping is solved in the loader.** The loader compiles the schema package
  through `go build -overlay`, with the files rewritten in place (`prune`): the `Hooks`
  and `Policy` methods, the declarations only they reach, and the imports only those
  use are blanked, line breaks kept, so positions stay true. What is kept is reached
  from the schema types, their methods, and `init`. The schema then does not import gen
  while loading, and velox-gen runs with gen stale, cyclic or deleted. `go list`'s
  import-cycle error on the schema package is passed over for the same reason
  (`listCycle`).
- **verify** links package runtime in its check program.

## Consequences

- A program, and a test package that uses gen at run time, imports `gen/runtime`. The
  examples do, and so do velox's own test packages that use `internal/testmodel`.
- Adding the first `Hooks` or `Policy` rewrites every entity's file (each checks the
  runtime), and removing the last removes both runtime files.
- An entity's behaviour must not be needed to load the schema: a `Fields` method that
  calls a function a hook also uses keeps that function, and if it uses gen, loading
  needs gen to compile, as before.
- Mixins still carry no behaviour: a hook a mixin's column needs is a generic function
  each entity lists (`auth.CreatedBy` in the hooks example).
- The engine's check for an unbound `Policy` or `Hooks` method stays, for entities
  written by hand, and its message now points to the schema package.
- A Go type of a field (`GoType`, a JSON field's struct) cannot be declared in a schema
  package that declares behaviour: gen imports it, and the schema imports gen. It goes
  in a package of its own, as the graphql e2e's `shapes` does.
- What read the policy from a method on the entity type no longer can: gqlimpl's
  foreign-key filters asked `*T` for `Policy()`, and now ask `Meta.HasReadPolicy`, new
  for generated code. That also fixes a target whose only Read policy is a generated
  scope (0030), which the method check missed.
- `GeneratedCodeVersion` is unchanged: the generated code calls no new velox API.
