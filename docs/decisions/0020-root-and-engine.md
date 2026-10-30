# 0020: the root package is the generic API, internal/engine is the rest

Date: 2026-10-30

## Context

velox2's root package held the whole runtime: 34 code files, 13,348 lines, and 82 test
files beside them. ent's root holds 3 files of shared types. velox2 cannot be ent here:
ent generates its query builders into the application, and velox2 keeps them generic
in the root so that a schema's build cost stays flat. Go requires a generic type's
methods in its package, so the generic API has to stay.

The rest did not have to. Measured with the type checker: `Statement`, `Predicate` and
`EntityInfo` reach 39 types, none generic and none naming `DB`.

## Decision

- `internal/engine` holds what is not generic and does not name `DB`: statements,
  conditions, metadata, rendering and its cache, most of scoping, loading, routing and
  transactions' parts, errors, caller capture.
- The root keeps `DB`, the generic API, and `api.go`: an alias, constant, variable or
  thin function for each public name the engine holds. `velox.Statement` is the same
  type as it was; generated code and applications do not change.
- `Pred[E]` moved beside `Predicate`, as a generic alias, because `velox.Predicate(p)`
  is a public conversion, and two structs with an unexported field are identical only
  in one package.
- Private fields of public types stay private. The root reaches them through functions
  of the engine, so `Statement` and `EntityInfo` gain no public field.
- The tests that need no internals are in `internal/apitest`, those that need the
  engine's alone in `internal/engine`, and those that need the root's in the root.

## Cost

- `go doc velox.Statement` and pkg.go.dev show an alias, not a struct. The alias's
  comment therefore lists its exported fields and methods, and
  `TestAliasDocsListMembers` fails when they drift apart. It is still less than a real
  declaration: no links per field.
- The engine's exports are wide, about 240 names, because the root needs them. Only
  this module can import them.
- Caller capture and the check for `One` and `Many` knew one package, and now know both.

`DB`'s pipeline (`run`, `prepare`, `exec`, `route`, `scope`) is still in the root, about
1,500 lines, as it is written on `DB`. Moving it would need an engine type that `DB`
wraps. That is the one further step left, if the root is to shrink more.
