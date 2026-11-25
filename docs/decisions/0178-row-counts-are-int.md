# 178. A row count is an int, as in ent

Status: accepted, 2026-11-26.

## Context

`Query.Count`, `UpdateQuery.Exec` and `DeleteQuery.Exec` returned an
`int64`, the type `database/sql` gives `RowsAffected`. ent's `Count`,
`Update.Save` and `Delete.Exec` return an `int`: ent converts the
driver's count at its boundary (`sqlgraph.DeleteNodes`). Code moved from
ent, or from veloxe, that stores a count in an `int` failed to compile.

## Decision

The three root methods return an `int`, and so do their untyped twins
(`TableQuery.Count`, `TableUpdate.Exec`, `TableDelete.Exec`) and
`Mutation.Affected` and `SetAffected`: a hook that makes its own write
passes an update's `Exec` count to `SetAffected`, and the two must be one
type. The engine's statements still count in an `int64`; the root and the
untyped handles convert.

`DB.Exec`, `CountBy` and `CountEach` keep the `int64`: `DB.Exec` returns
what the driver reports, and the other two return the counts of a group
and of an owner's rows, which have no ent method to match.

## Consequences

A count past 2^31 overflows an `int` on a 32-bit build. No supported
server target is 32-bit.

Each Count and Exec caller that held the result in an `int64` takes an
`int`. `docs/coming-from-ent.md` lists the types.

The conversions add about 1 KB to a package that uses `Count` or `Exec`
(`entityDescUse` 806950 to 808042 bytes, `entityDescHooks` 892988 to
894078); the recorded limits in `compilecost_test.go` rose by 1 KB.
