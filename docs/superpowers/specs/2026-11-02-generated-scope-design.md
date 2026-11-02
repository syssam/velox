# Generated scope: the scope column in the schema, no hand-written scope.go

- Status: implemented, 2026-11-02 (recorded in `docs/decisions/0030-generated-scope.md`).
- Builds on: `docs/decisions/0029-scope-replaces-tenant.md` (the `scope` package).
- Breaking: the examples only (pre-1.0; the `scope` package keeps its API).

## Why

`scope` confines an entity's rows to a value the context carries. To use it today a
user writes, in the generated package, a file like `examples/routing/gen/scope.go`:

```go
var Org = scope.NewKey[int64]("org")
var noteScope = scope.By(Org, Notes.OrgID.EQ, func(x *Note) *int64 { return &x.OrgID })
func (*Note) Policy() velox.Policy[Note] { return noteScope.Policy() }
func (*Note) Hooks() []velox.Hook[Note]  { return []velox.Hook[Note]{noteScope.Fill()} }
```

That file says nothing the schema does not already say: `org_id` is the scope column.
It sits in `gen/` only because `Policy` and `Hooks` are methods and Go puts a method
in its type's package. Five examples carry such a file (graphql, routing, tenant,
tour, tracker). Each is boilerplate that a user can get wrong in a way that does not
fail: forget `Fill()`, or call `Policy()` and not `Wrap` in a Policy with its own rules
(tracker's roles), and rows are silently unscoped.

ent keeps its output directory fully generated. velox cannot move hooks and policies
into `schema/` (they are typed on generated handles, and `schema/` must compile before
`gen/` exists), so the way to a cleaner `gen/` is to generate what is mechanical.

## Goals

- A scoped entity is declared once, in the schema, on its scope column.
- No hand-written file is needed for the common case; `gen/` then holds only
  generated files plus code that is really the user's (role rules, other hooks).
- A user cannot forget the scope: it is applied to the entity whether or not the
  entity has a Policy or Hooks of its own.
- The engine and the `scope` package do not change. The generator only writes the
  `scope.By` wiring the user writes now.

## Non-goals

- More than one scope column per entity (org and region): refused for now, written by
  hand with `scope.By` as today.
- A scope that is not "column equals the context's value" (soft delete, published
  only). Those stay Policy and Hooks written by hand.
- Generating role or ownership rules (tracker's `policy.go`).
- Changing `scope.Key`, `By`, `Wrap` or `Fill`.

## Anti-goals

- The engine or `Descriptor` learning a "tenant" again (ADR 0029).
- A scoped entity that reads unscoped because a user's Policy replaced the scope.

## Design

### The annotation

```go
field.Int64("org_id").Immutable().Annotations(schema.Scoped("org"))
```

`schema.Scoped(key)` is a field annotation in package `schema`, beside `Table` and
`Check`. `key` names the scope; the same key on several entities is one key. The
column's Go type is the key's value type (`int64`, `string`, a UUID: any comparable
type).

Why in the generator and not a plugin (my call, from the code): the descriptor's
`Policy` and `Hooks` binding is decided by core from the methods found in the user's
files (`codegen.Methods`, `Bound`), which skips files with velox-gen's header. A
plugin's output would not be seen, so its `Policy` and `Hooks` would never be bound.

### What is generated

For each key, once, in the package: `var OrgKey = scope.NewKey[int64]("org")`. The
variable is `<GoName(key)>Key` always (my call: `tracker` has an `Org` entity, so a
bare `Org` clashes there; one rule beats a per-example exception). It is generated
into `velox.go`'s neighbour `scope_velox.go`, which exists only when some entity is
scoped.

For each scoped entity: `var noteScope = scope.By(OrgKey, Notes.OrgID.EQ, func(x *Note) *int64 { return &x.OrgID })`,
unexported, also in `scope_velox.go`.

### How it is applied

The generated descriptor binds the user's methods with `velox.BindPolicy((*Note)(nil).Policy())`.
For a scoped entity it binds the scope around them:

```go
Policy: func() velox.BoundPolicy { return velox.BindPolicy(noteScope.Wrap((*Note)(nil).Policy())) }
Hooks:  func() velox.BoundHooks  { return velox.BindHooks(append([]velox.Hook[Note]{noteScope.Fill()}, (*Note)(nil).Hooks()...)) }
```

with the user's method left out when they have none (`velox.Policy[Note]{}` and no
hooks). So the user's `Policy` holds only their own rules and never calls `Wrap`; the
scope is outermost and cannot be dropped. `Fill` runs first, so a later hook sees the
filled column. This also deletes the `projectScope.Wrap(...)` calls from tracker.

### Checks (`velox-gen verify` and generation)

Refused with a message naming the entity and field:

- the scoped column is not `Immutable` (an update could move a row between scopes);
- it is `Optional`, `Nillable` or a pointer (a missing value must not mean "every
  scope");
- two scoped columns on one entity;
- the same key on columns of different Go types;
- a user file declares `<entity>Scope` or `<Key>Key` (would clash).

### The examples after the change

`scope.go` is deleted from graphql, routing, tenant, tour and tracker, and each
schema's `org_id` gains `schema.Scoped("org")`. Call sites change `gen.Org.With` to
`gen.OrgKey.With`. Tracker keeps `policy.go` (roles) minus the `Wrap`. Where a
`gen/` then has no handwritten file (routing, tenant, graphql), its package comment
is the generated one again (`Config.UserDoc` false).

## Constraints

- Plugin model: `plugin.ModelVersion` is unchanged; the annotation reaches plugins
  as an ordinary raw field annotation.
- GraphQL: `contrib/graphql` reads the same descriptor; nothing there changes. The
  batcher already keys callers by `velox.WithScopeValue`, which `Key.With` sets.
- The `scope` package stays importable by hand-written code for the cases above.
- Determinism: `velox-gen -check` must be stable (sorted keys and entities).

## Left to the builder

- The annotation's exact name and file name (`Scoped`, `scope_velox.go`).
- Whether `OrgKey`'s doc comment is generated from the field's comment.
- Golden tests: extend the `blog` golden with a scoped entity and a scoped entity
  that also has Policy and Hooks.
- Updating ADR 0029 or adding 0030; CHANGELOG entry; README/doc snippets in `scope/`.

## Open question for the owner

Wrapping is automatic, so a user can no longer opt one scoped entity out of the scope
inside its own Policy. The only lift is `velox.WithoutPolicy(ctx, reason)`, as for
every policy. I believe that is right; say if some entity needs "scoped, except for
this rule".
