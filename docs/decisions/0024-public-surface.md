# 0024: the public API is pinned, and migrate's change types are not in it

Date: 2026-11-01

- Status: accepted, 2026-11-01.

## Context

A name exported at 1.0 stays until 2.0. Nothing failed when a name was exported by accident:
`apidiff` needs a first tag to compare with. `migrate` exported a struct and three methods for
each of its 22 kinds of change, and only one test outside the package read their fields.
`internal/engine` exported 111 names that nothing outside it used, so there an export
said nothing about what crosses the package.

## Decision

- `internal/repocheck/testdata/api.txt` lists the exported API of every public package,
  with the members of an engine alias under the alias. `TestPublicAPISnapshot` fails on any
  difference. A deliberate change regenerates the file, so it is a diff a reviewer reads.
- `migrate.Change` is opaque. `Kind`, `Target` and `String` say what a change is; the structs
  behind it are unexported. Exporting one later breaks nobody, and un-exporting one after 1.0
  would break everyone who used it.
- `internal/engine` exports what another package uses, its external tests included, and
  revive's `exported` rule now applies to it. The aliases of `veloxtype` stay exported: they
  are the package's vocabulary.

`Statement`, `ColumnInfo`, `EntityInfo`, `ConflictClause`, `EachClause` and `Shard` stay
aliases of engine types (decision 0020), since each holds an unexported field or an engine
type. Their docs list their members, and the snapshot pins them.

## Consequences

A program that inspects a plan reads `Kind`, `Target` and `String`, not the fields of a
change. If that proves too little, the type that is needed gets exported then.
