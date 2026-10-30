# 0019. pgx's native path is the package veloxpgx, and the core links no driver

- Status: accepted, 2026-10-30

## Context

The core package imported pgx for three things: the native executor behind
`OpenPgx` and `PgxConn`, the classification of pgx's errors, and two interfaces
the stdlib scan wrapper checks (decision 0018). So every application linked pgx,
including one that uses only SQLite or MySQL (C53). The core already imported
neither of the other two drivers: it recognizes their errors by type name.

## Decision

- `veloxpgx.Open(pool)` and `veloxpgx.Conn(pool)` replace `velox.OpenPgx` and
  `velox.PgxConn`.
- It is a package in this module, not a module of its own. The module still
  requires pgx, for velox-gen and for tests; Go's module pruning keeps pgx out of
  an application's go.mod unless it imports veloxpgx. A second module would add a
  second tag to release in step with the first, for nothing an application sees.
- The driver interface is `internal/conn`, so it is no API: nothing outside the
  module can implement it. Two functions cross into the core: `velox.OpenConn`,
  which is useful as it stands, and `velox.DriverConn`, whose parameter an
  application cannot name. If third-party drivers are wanted later, `internal/conn`
  can be promoted without breaking anything; the reverse could not be done.
- The core treats pgx as it treats the other two drivers: `*pgconn.PgError` and
  `*pgconn.ConnectError` by type name, PgError's fields by name, `ErrConnClosed`
  and `ErrTxClosed` by type and text, `pgx.Rows`'s columns through a method called
  by name. The core must still do this without veloxpgx, since `velox.Open` with
  pgx's stdlib driver returns the same errors, and a blank import to get them
  classified would be easy to forget and would fail silently.
- `velox.Open` keeps taking a `*sql.DB`.

## Consequences

- Names replace the compiler's checks. `TestPgxTypesMirrored` and the error tests,
  which use pgx's real types, catch a change when pgx is upgraded here. An
  application can select a newer pgx v5 through MVS, as decision 0018 notes for its
  own mirror.
- An error whose `As` method yields a `*pgconn.PgError` is no longer recognized.
  pgx has none, and the MySQL and SQLite paths never honored it.
- Any `errors.New("conn closed")` or `errors.New("tx is closed")` counts as pgx's,
  whichever package made it. Both mean what velox takes them to mean, and MySQL's
  `invalid connection` was already read this way.
- G38 stays open: `Open` cannot see a MySQL DSN, so it cannot refuse one without
  `clientFoundRows`. A `veloxmysql.Open(cfg)` that sets the flags could close it
  after 1.0.
