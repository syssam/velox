# 0013. Database version floors

- Status: accepted, 2026-10-29

## Context

PostgreSQL 12 and 13 and MySQL 8.0 are past their end of life (MS1, C32).
Every supported version needs a CI run, and SQLite needs `DROP COLUMN`.

## Decision

migrate refuses a server below PostgreSQL 14, MySQL 8.4 or SQLite 3.35, and
refuses MariaDB. The refusal names the minimum. GitHub CI runs PostgreSQL 14
and 18 and MySQL 8.4, pinned by digest; `scripts/ci-local.sh` runs one image
of each, and `VELOX_CI_MATRIX=1` adds CI's PostgreSQL 14.

## Consequences

A team on PostgreSQL 13 or MySQL 8.0 cannot use `velox-gen migrate` until it
upgrades. The runtime itself makes no such check.

## Evidence

- `checkVersion` (migrate/session.go) and migrate/doc.go's list of refusals.
- `TestCheckVersion` (migrate/session_test.go:13) and `TestCheckVersionFloors`
  (migrate/session_test.go:58).
- `git grep -nP 'checkVersion|CheckVersion' -- '*.go' ':!migrate/**'` prints
  nothing: no code outside migrate checks the version.
- .github/workflows/ci.yml, the postgres:14 and postgres:18 matrix and
  mysql:8.4.
- Builder ruling 16, docs/history/specs/2026-10-25-pre-1.0-hardening-design.md:171;
  migrate spec ruling 41, docs/history/specs/2026-10-23-migrate-design.md:1323.
