# 0014. Query.With and Rel.With stay methods

- Status: accepted, 2026-10-29

## Context

velox's rule is a method when it needs no type parameter beyond `E`, and a
function when it does, because a `Query` method with its own type parameter is
paid for in the export data of every entity package. `Query.With[T]` and
`Rel.With[U]` break the rule (G18). The spec set the test: measure, and keep
the methods only if they cost under 1% of the entity-package bound.

## Decision

They stay methods. On 2026-10-26, with both called, `entityDescUse` measured
769036 bytes with the methods and 767398 with functions, a difference of 1638
bytes, under the 1% (7670 bytes).

## Consequences

`q.With(rel)` reads as the other query builders do. `Sum`, `Avg`, `Min` and
`Max` stay functions, since as methods they cost about 2.3%.

## Evidence

- Commit a21f54a: the measurement, and compilecost_test.go's `entityDescUse`
  row, whose comment says "Query.With and Rel.With stay methods".
- docs/history/specs/2026-10-20-api-cleanup-design.md, ruling 21 (:282).
- Ruling 11 and builder ruling 10,
  docs/history/specs/2026-10-25-pre-1.0-hardening-design.md:49, :165.
