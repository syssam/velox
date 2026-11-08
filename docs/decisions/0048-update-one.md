# 48. UpdateQuery.One writes one row and returns it

Status: accepted, 2026-11-07. Moves 0045's read-back from the application
into the core. 0062 adds `SetInput`, which keeps the error of `Setters()`
for `Exec` or `One`.

## Context

After 0045 a GraphQL update resolver wrote with `UpdateQuery.Exec` and read
the row back itself. examples/graphql's `update` helper was 30 lines: a
count of 0 was NOT_FOUND, a count the driver did not report was an error,
the read-back went without the Read policy, and an input that set nothing
read under it. On MySQL the count needed `clientFoundRows=true`. Every
application had to copy it. ent has `UpdateOne(...).Save(ctx)`, which
returns the row. Each resolver also spent three lines on
`veloxgql.DBFromContext`, which fails only when the schema is served
without the extension.

## Decision

- `UpdateQuery.One(ctx) (*E, error)` writes the one row the conditions
  match and returns it as written. In one transaction it reads the row's
  key under the Read and Write policies, locked where the dialect can;
  no row is `ErrNotFound`, more than one `ErrNotSingular`, and nothing is
  written. The UPDATE's only condition is that key, so the hooks see it.
  The edge setters' edges follow, and the row is read back by its key
  without the Read policy. An update with no setter returns the row.
- Inside a transaction, a One that writes no edge takes no savepoint, as
  Exec takes none: it is one UPDATE.
- One sends one statement more than Exec, the read of the key. veloxgql's
  cost counts two statements for every mutation input, the read of the
  key and the read-back.
- `veloxgql.DB(ctx) *velox.DB` is `DBFromContext` for a resolver: it
  panics with DBFromContext's error, a program served without
  `NewExtension`, which gqlgen's recover reports as an internal error.
  `DBFromContext` stays for code that wants the error.
- `Setters()` keeps its error return. An ID that does not decode is the
  client's mistake, and Go returns it where it happens; ent's `SetInput`
  needs none only because ent's IDs are typed.

## Consequences

- An update resolver is the update and a NOT_FOUND mapping; the example's
  `update` helper is gone. MySQL no longer needs `clientFoundRows` for it.
- Its NOT_FOUND now also covers a row the Read policy hides, as the edge
  setters' read of the rows already did.
- A row a hook declines to write (it does not call next) is still read
  back and returned.
