# 30. A scoped column is declared in the schema; velox-gen writes the scope

Status: accepted, 2026-11-02. Builds on 0029.

## Context

0029 made a scope a `Policy` plus a hook, built by the package `scope`. To use it an
entity needed a hand-written file in the generated package, with the key, the
`scope.By` of its column, and a `Policy` and `Hooks` method that returned them. The
file said only what the schema already said: this column is the scope. It sat in
`gen/` because a method must be in its type's package, so the generated directory was
mixed, unlike ent's. It could also be got wrong without an error: leave out `Fill()`,
or write a `Policy` of one's own and not wrap it, and rows were unscoped.

The design is in `docs/superpowers/specs/2026-11-02-generated-scope-design.md`.

## Decision

A column is marked `schema.Scoped("org")`. velox-gen then writes `scope_velox.go`:

- one variable per key, `OrgKey` (`<GoName(key)>Key`, always: a bare `Org` clashed with
  tracker's `Org` entity), a `scope.NewKey[V]` of the column's Go type;
- one variable per scoped entity, `noteScope`, the `scope.By` of its column.

The entity's descriptor binds the scope around whatever the user declares, so the scope
is outermost and cannot be dropped: `BindPolicy(noteScope.Wrap(user's Policy))`, or
`noteScope.Policy()` when there is none, and `Fill()` first in the hooks. A user's
`Policy` holds only its own rules and never calls `Wrap`. (Since 0037's Review of
2026-11-04 the descriptor names the scope in `Descriptor.Scope`, and velox composes it
in the same order.)

velox-gen refuses, with the entity and field: a scoped column that is not `Immutable`,
is `Optional` or `Nillable`, or is not an integer, string or UUID; two scoped columns on
one entity; one key on columns of two Go types; a key that makes no Go name. It also
refuses a user file that declares `<Key>Key` or `<entity>Scope`.

The generator writes this itself and not a plugin: whether `Policy` and `Hooks` are
bound is decided by core from the user's files, which skip files with velox-gen's
header, so a plugin's methods would never be bound.

## Consequences

- The five examples that had a `scope.go` have none; routing, tenant and graphql have
  a fully generated `gen/`. Hooks and policies of an entity's own stay hand-written in
  `gen/`, because they are methods on the generated type. (Superseded by 0034: the
  schema declares them, and every example's `gen/` is fully generated.)
- The engine and the `scope` package are unchanged. The hand-written form remains for
  a rule the annotation does not cover: a scope that is not "column equals the
  context's value", or two scoped columns.
- A scoped entity cannot opt out of its scope inside its own `Policy`. The only way to
  lift it is `velox.WithoutPolicy(ctx, reason)`, as for every policy.
- The key variable is `OrgKey`, not `Org`, in the examples.
- `GeneratedCodeIsVersion` stays 7: generated code of an unscoped entity is byte for
  byte as before, and a scoped one needs only the `scope` package, which exists.
