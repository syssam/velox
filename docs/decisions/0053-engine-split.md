# 53. Drivers' errors are package dberr; the rest of the engine stays one

Status: accepted, 2026-11-08. Amends 0020.

## Context

`internal/engine` had 50 files and about 14,700 lines of code. A review
asked whether its name fits, and whether parts of it should be packages of
their own, as ent splits `sqlgraph`, `privacy` and `entql`. It named three
candidates: the drivers' errors, the rendering of SQL, and routing.

## Decision

The name stays. The package is internal, users meet it only in the root's
aliases, and it is what every statement runs through. `runtime` would
shadow the standard package, and `core` says nothing.

`errclass.go` is package `internal/dberr`. It reads the errors of pgx,
go-sql-driver/mysql and modernc.org/sqlite without importing them: a
violated constraint, a lock not taken, a retryable failure, a lost
connection, and `DriverCode`. It uses no engine type, only `veloxtype`'s
`ConstraintError`. `dbErr`, which names the entity, stays in the engine.
Its tests, the pgx mirror and `FuzzConstraint` moved with it.

Rendering stays. It reads `Statement`, `Node` and `EntityInfo`, and those
hold the engine's relations, hooks, filters and bidi steps: `EntityInfo`'s
`rels`, `kit`, `hooks` and `filters`, `Statement`'s `bidi`, and a `Node`'s
`RelRef`. A render package would need each type split in two first.

Routing stays. It is a `DB` option and a step of the pipeline: it reads
`config`, the executor and the statement's pin.

## Consequences

The engine has 49 files and about 350 fewer lines. `dberr` has a coverage floor.
A future split starts from the types, not the files: the statement model
leaves the engine only when `EntityInfo` and `Statement` no longer hold
engine state.
