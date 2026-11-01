# 0008. Write policies filter

- Status: accepted, 2026-10-29

## Context

A `Write` policy is ANDed into every UPDATE, DELETE and Save. A caller who
updates a row the policy hides could get an error or a count of 0 (C56).

## Decision

A row Write hides is left alone, and that is not an error: an Update or Delete
that matches only such rows affects 0 rows, as with SQL row-level security.
Write decides which existing rows may change; it does not check the new
values.

## Consequences

An application that must tell "forbidden" from "missing" checks the count, or
reads the row first. A caller learns nothing about a row it may not change
beyond that the write changed nothing.

## Evidence

- `Policy.Write`'s doc, policy.go.
- `TestWritePolicyFiltersToZeroRows` (policy_test.go:773): an update and a
  delete as a caller the policy refuses affect 0 rows with no error; the
  owner's delete affects 1. On a pnote, whose Read allows the row and whose
  Write hides it, the update and the delete affect 0 rows, the row is
  unchanged, and a query still finds it. With Write ignored, that update
  affects 1 row and the test fails.
- docs/history/reviews/2026-10-25-triage-core.md, C56; docs/history/specs/2026-10-25-pre-1.0-hardening-design.md:94.
