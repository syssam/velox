# 0020: the root package is the generic API, internal/engine is the rest

Date: 2026-10-30. Amended by 0053: the drivers' errors are internal/dberr.

## Context

velox's root package held the whole runtime: 34 code files, 13,348 lines, and 82 test
files beside them. ent's root holds 3 files of shared types. velox cannot be ent here:
ent generates its query builders into the application, and velox keeps them generic
in the root so that a schema's build cost stays flat. Go requires a generic type's
methods in its package, so the generic API has to stay.

The rest did not have to. Measured with the type checker: `Statement`, `Predicate` and
`EntityInfo` reach 39 types, none generic and none naming `DB`.

## Decision

- `internal/engine` holds what is not generic: statements, conditions, metadata,
  rendering and its cache, scoping, loading, routing, transactions, errors, caller
  capture, and `engine.DB`, the pipeline.
- The root keeps `DB`, the generic API, and the files `types.go`, `options.go`, `errors.go`, `routing.go`, `tx.go` and `context.go`: an alias, constant, variable or
  thin function for each public name the engine holds. `velox.Statement` is the same
  type as it was; generated code and applications do not change.
- The root's `DB` holds an `engine.DB` by value, in its one unexported field: not
  embedded, which would promote the engine's methods into the public API. Holding it by
  value keeps a statement's path free of another pointer and a transaction's frame one
  allocation. `Tx` gives its fn the `*DB` of the frame's `*engine.DB`, the same memory,
  through a closure over fn, which costs a transaction with a `TxAround` hook one
  allocation. An unsafe cast of the function value would save it, but package unsafe
  does not allow that.
- `Pred[E]` moved beside `Predicate`, as a generic alias, because `velox.Predicate(p)`
  is a public conversion, and two structs with an unexported field are identical only
  in one package.
- Private fields of public types stay private. The root reaches them through functions
  of the engine, so `Statement` and `EntityInfo` gain no public field.
- Each test lives by the code it tests. A test of the engine's behaviour is in
  `internal/engine`, in `package engine_test` when it goes through the root or
  generated entities, which that package may import, so `go test -cover
  ./internal/engine` means something on its own. A test of the rest of the public
  API is in `internal/apitest`, one of the repository in `internal/repocheck`, and
  only one that needs the root's private fields, or a Go example, in the root.

## Cost

- `go doc velox.Statement` and pkg.go.dev show an alias, not a struct. The comment of
  an alias of an engine type therefore lists its exported fields and methods, and
  `TestAliasDocsListMembers` fails when they drift apart. It is still less than a real
  declaration: no links per field. Decision 0021 moved the plain data types to the
  public package `veloxtype`, whose aliases need no list.
- The engine's exports are wide, 375 top-level names, 53 of them aliases of `veloxtype`,
  because the root needs them. Only this module can import them.
- Caller capture and the check for `One` and `Many` knew one package, and now know both.

`DB`'s pipeline was the last step, and is done: `run`, `prepare`, `exec`, `route`,
`scope`, the guards, deadlines, transactions and their hooks, and the non-generic work
of the writes, the bidirectional edges, the loads, `LoadEach` and `CountEach`, are
methods and functions of `engine.DB`. The root went from 26 files and 7,454 lines of
code to 17 files and 4,866 lines, and the engine from 7,271 to 9,888 lines.
What is left in the root besides thin forwarding names `Meta[E]` or a generic API type:
`oneStatement`, `prepareMany`, `insertMany` and `insertBatches`, the scanning, and the
query builders.

`MutationState.DB` stays an `any`, as `EntityInfo` reaches it: a `*DB` field made an
entity's package 14 KiB larger (`entityDesc`, 130,162 to 144,268 bytes).
