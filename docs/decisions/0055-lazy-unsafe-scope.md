# 55. Lazy, unsafe and routing stay in the core, measured

Status: accepted, 2026-11-08.

## Context

A senior review challenged three choices the core makes on purpose: the
`[_ Lazy[E]]` type parameter on every method of a generic type, `unsafe` in
the root and the engine, and routing (replicas, shards) and migrate in the
module. This record gives the numbers behind each.

## Decision

**Lazy stays.** Go compiles every ordinary method of a generic type into
each package that names an instance of it, called or not; a generic method
is compiled only where it is called. velox has 206 such methods. A copy of
the repository with every `[_ Lazy[…]]` removed was measured against this
one (`TestGenericTypesCompileSmall`, `go build -trimpath`, Go 1.27, Apple M3
Max), in bytes added to a package by one variable of each type:

| type | with Lazy | without | × |
|---|---:|---:|---:|
| `Query[E]` | 6,342 | 483,080 | 76 |
| `Rel[E, T]` | 6,848 | 394,626 | 58 |
| `InsertQuery[E]` | 8,102 | 287,060 | 35 |
| `StringCol[E]` | 14,796 | 289,378 | 20 |
| `UpdateQuery[E]` | 7,822 | 135,554 | 17 |
| `DeleteQuery[E]` | 6,966 | 77,162 | 11 |
| an entity's package | 770,312 | 2,377,868 | 3.1 |

The tracker example's `gen` package, eight entities, compiles in 0.16 s with
Lazy and 0.45 s without. The cost is a parameter that callers never write
and that godoc shows; `Lazy`'s doc says so. The guard test keeps it.

**unsafe stays, confined.** It is used in 35 files of the root, the engine
and `untyped`: a column is read and written at its offset in the entity, so
the engine handles every entity without generics or reflection per row,
and a root `*DB` is the `*engine.DB` it holds. An offset is not written by
hand: the engine takes it from a pointer the generated code gives into a
zero value of the entity (`fieldOf`), and its type from reflect, at first
use, so it cannot drift from the struct. Every test run of the root,
examples and veloxotel in CI is `-race`, which turns on `checkptr`; the
`FuzzRender` and `FuzzScope` targets run in CI on every push and pull request. The untyped
surface takes and returns `any`, not `unsafe.Pointer` (0051), so no user
code holds one.

**Routing and migrate stay in the module, at no cost to those who do not
use them.** `route.go` is 637 lines; a DB without replicas or shards pays
one nil check per statement (`db.Rt == nil` in `route`). They are in the core because
a statement's scope, its transaction's pin and its policy must agree on the
connection, which a wrapper outside the engine cannot see. migrate is
velox's own (0006) and an Atlas provider ships with it; a team on Atlas
uses Atlas. Neither is imported by a program that does not call it: the
root does not import `migrate`.

## Consequences

- A new method of a generic type is generic (`methods_test.go`).
- A new use of `unsafe` gets its offset from `fieldOf` or a size check, as
  `DB`'s `[unsafe.Sizeof(DB{}) - unsafe.Sizeof(engine.DB{})]` does.
- Routing that cannot be kept free for a DB without replicas is refused.
