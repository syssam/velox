# 0016. WithTenant converts the tenant id

- Status: superseded by [0029](0029-scope-replaces-tenant.md), 2026-11-02; accepted 2026-10-29

## Context

`WithTenant(ctx, id any)` takes any value, and a tenant id often comes from a
claim as an `int` or a `string` while the column is an `int64` or a named
type. A strict type match made `WithTenant(ctx, 1)` fail on an `int64` column
(C51).

## Decision

`WithTenant` keeps its signature. It converts a predeclared integer to the
tenant column's Go type when that is an integer type whose range holds it, and
a string to a column of string kind, predeclared or named. A named id is never
converted, so a `UserID` never serves an `OrgID` column. Signs are checked,
and zero is checked after the conversion. The conversion is per entity, per
statement.

## Consequences

An untyped constant works on any integer tenant column. Any other id fails
the statement with `ErrTenantMismatch`; a zero or nil id scopes nothing and
fails with `ErrNoTenant`.

## Evidence

- `WithTenant`'s doc, tenant.go.
- `TestTenantConversion` (tenant_test.go:698).
- Commit b04ac97; docs/history/reviews/2026-10-25-triage-core.md, C51.
- Ruling 12 and builder ruling 8,
  docs/history/specs/2026-10-25-pre-1.0-hardening-design.md:50, :163.
