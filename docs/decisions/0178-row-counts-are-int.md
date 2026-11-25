# 178. A row count is an int, as in ent

Status: accepted, 2026-11-26.

## Context

`Query.Count`, `UpdateQuery.Exec` and `DeleteQuery.Exec` returned an
`int64`, the type `database/sql` gives `RowsAffected`. ent's `Count`,
`Update.Save` and `Delete.Exec` return an `int`: ent converts the
driver's count at its boundary (`sqlgraph.DeleteNodes`). Code moved from
ent, or from veloxe, that stores a count in an `int` failed to compile.

## Decision

The three root methods return an `int`. The engine still counts in an
`int64`, and the root converts.

`DB.Exec`, `Mutation.Affected` and `SetAffected`, the untyped package's
`Count` and `Exec`, `CountBy` and `CountEach` keep the `int64`: ent has no
counterpart for them, and they report what the driver does.

## Consequences

A count past 2^31 overflows an `int` on a 32-bit build. No supported
server target is 32-bit.

Each Count and Exec caller that held the result in an `int64` takes an
`int`. `docs/coming-from-ent.md` lists the types.
