# 0012. Drift never hides a real difference, with one exception

- Status: accepted, 2026-10-29

## Context

`migrate.Drift` compares a database with its snapshot. A database prints a
predicate or check its own way, so Drift compares parsed forms. A normaliser
that matched too much would hide a hand-edited constraint; a typed one needs
column types Drift does not have (M9).

## Decision

A difference in meaning is a change: Drift never hides a real difference. A
cast the database adds itself without changing the value matches no cast, and
what the comparison cannot settle is a Warning. The one documented exception
is a cast whose meaning depends on the column's type: on PostgreSQL,
`d >= '2020-01-01 10:00'::timestamp` matches `d >= '2020-01-01 10:00'` on a
date column, and `qty::text = '05'` matches `qty = '05'`. The typed
normaliser is deferred whole; a conservative fix would report false drift on
common predicates.

## Consequences

A hand-edited check is found. A change that only adds or removes such a cast
is not, and the package doc says so.

## Evidence

- migrate/doc.go, the paragraph on Drift: "The exception is a cast whose
  meaning depends on the column's type".
- `TestSamePredicateKnownLimits` (migrate/predicate_internal_test.go:14) pins
  both pairs as matching, and an interval cast as not.
- Builder ruling 6, docs/specs/2026-10-25-pre-1.0-hardening-design.md:161.
