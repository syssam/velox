# 0005. Deprecation

- Status: accepted, 2026-10-29

## Context

Before 1.0 the API still changes, and no release has been made. Keeping a
deprecated identifier beside its replacement doubles what a reader must learn
and what the tests must hold, for users who do not exist yet.

## Decision

Before 1.0, nothing is deprecated: it changes or goes, and CHANGELOG.md says
so. After 1.0, a deprecated identifier carries a `// Deprecated:` comment until
the next major version. The paragraph names its replacement.

## Consequences

An upgrade before 1.0 may not compile, and the CHANGELOG's "Changed" and
"Removed" lists say what to change. After 1.0, a deprecated identifier
stays through every release of its major version.

## Evidence

- `git grep -nP '//\s*Deprecated:' -- '*.go'` prints nothing.
- CHANGELOG.md, its opening rules.
- Controller ruling 7, docs/history/plans/2026-10-29-hardening-6-docs.md.
