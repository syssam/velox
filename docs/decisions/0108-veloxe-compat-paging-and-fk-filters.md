# 108. Under compat=veloxe, cursors, ties, page flags and foreign keys are veloxe's

Status: accepted, 2026-11-22. Extends 0088. Amends 0027 and 0103 for
compat=veloxe only: a foreign key's predicates are the column's alone.

## Context

The parity audit (`docs/superpowers/specs/2026-11-22-veloxe-behaviour-parity.md`)
probed veloxe's `examples/fullgql` and velox's paginator with the same
arguments. Four differences reach a client that was written against
veloxe, even under compat=veloxe:

- A cursor veloxe wrote is refused (§1 row 1.18). veloxe's cursor is
  standard base64 without padding of the msgpack map `{"i": key, "v":
  value}`, with `"vs": [values…]` beside a nil `v` in hand-written ones, a
  multi-term order's values as an array in `v`, and a time as msgpack
  extension 1. A client keeps the cursor in the URL, so every list past its
  first page fails after the deploy.
- Ties sort the other way under DESC (row 1.23). veloxe broke ties by the
  key in the one order term's direction; velox breaks them by the key ASC.
- A page after a cursor says `hasPreviousPage: false`, and one before a
  cursor `hasNextPage: false` (rows 1.2 and 1.4). veloxe said true.
- A foreign key's predicates are scoped to what the reader may see of the
  target (§2 row 2.16; 0027, 0103). veloxe's were the column's.

## Decision

All four hold when `gqlgraph.Type.Veloxe` is set, which compat=veloxe
writes into each type literal (0088). Without it nothing changes. Generated
code does not change.

- **Legacy cursors are read, never written.**
  - `gqlcore.DecodeLegacyCursor` is a hand decoder for exactly what veloxe
    wrote: canonical standard base64 without padding of a map of the keys
    `i`, `v` and `vs`. Values are integers, strings of valid UTF-8, nil,
    booleans, floats, 16-byte binaries (a UUID) and extension 1 (a time:
    the array of `MarshalBinary` and the zone's name). `v` and `vs` may be
    arrays of those. Every length is checked against what is left before
    anything is allocated. Anything else, data after the map included, is
    not a legacy cursor. There is no msgpack dependency.
  - It gives the key and values as the JSON values `DecodeCursor` gives: a
    `json.Number`, a string, a bool or nil; a time is its RFC 3339 string.
    A connection then converts them as it converts a velox cursor's.
  - `veloxgql.DecodeCursor` is unchanged and refuses them.
    `Cursor.UnmarshalGQL` and `UnmarshalGQLContext` try velox's format
    first. When it fails and the text is a legacy cursor, they return no
    error. The Cursor's `Key` then holds the `*gqlcore.LegacyCursor`,
    which keeps `DecodeCursor`'s error; `Type` and `Terms` are zero. Any
    other text fails there as before.
  - The connection decides (`Connection.row`). Without `Veloxe` it returns
    the kept error, unwrapped: the same error, `ErrBadCursor` with the same
    text. With `Veloxe` it reads the cursor as veloxe did: its key as the
    key, and its values as the current order's, whatever order it was
    taken under.
  - A nil `v` with no `vs` is the values of an order of no terms, or of one
    term whose value is NULL, as veloxe wrote both. A term on the key's own
    column (`orderBy: ID`) whose value is nil takes the key: veloxe wrote
    `Cursor{ID}` alone for it.
  - A cursor whose number of values is not the order's number of terms is
    refused as "taken under another order", the existing bad-cursor error.
    So is any legacy cursor on a type whose key has more than one column,
    which veloxe's cursors never had. A value of another type fails as a
    velox cursor's does.
  - The connection's own cursors stay velox's. There is no option to emit
    veloxe's.
- **Ties.** With `Veloxe` and an order of exactly one term, the key takes
  that term's direction. With two or more terms the key stays ASC, as it
  does without `Veloxe`. A velox cursor needs no change: the key's
  direction is the Type's and the order's, and a composite key's columns
  carry theirs in their `key:<column>` terms.
- **Page flags.** With `Veloxe`, a forward page with `after` has
  `hasPreviousPage` true, and a backward page with `before` has
  `hasNextPage` true. The other flag is still the extra row's presence.
  Relay allows either answer.
- **Foreign keys.** With `Veloxe`, `EdgeIDField`'s predicates (`xID`,
  `xIDNEQ`, `xIDIn`, …, `xIDIsNil`, `xIDNotNil`) are the column's alone:
  no `Table.ScopedKey`, so no EXISTS. The generated code's path,
  `gqlgraph.Predicate`'s `WhereEdgeID`, shares the helper. 0088's compat
  rules still apply (a false IsNil adds nothing).

## Why

- **How the flag reaches decoding.** The scalar cannot know it.
  graphql-go calls `UnmarshalGQL` with no context, and gqlgen's context
  does not name the type. So decoding is lazy for legacy cursors only:
  velox's format first, then the legacy check, and the connection, which
  knows `Type.Veloxe`, decides. Any text that is neither format fails in
  the scalar as it did.
- **Why the Key holds it.** The core may have no `init` and no exported
  name that only gqlgraph calls (`TestNothingRunsAtStart`, the API list).
  An unexported field would need one or the other. `Key` is `any` already,
  and its type is internal, so only gqlgraph reads it.
- **Why reject a mismatched count.** veloxe read a cursor's value as the
  new order's, unchecked. Guessing which value goes with which term would
  give a wrong page silently. With the same count the reading is veloxe's,
  so one order term behaves exactly as before the deploy.
- **Why the tie rule needs one term.** It is what veloxe's single-order
  path did (`gql_pagination_*.go`: the id term in `order.Direction`), and
  nextapp's clients send one order. veloxe's MultiOrder, a list of
  orders, tied by the key in the first term's direction. nextapp has no
  MultiOrder schema, and the ruling for this task keeps velox's key ASC
  for several terms rather than carry that rule over untested.
- **Why plain FK predicates.** Compat shows the foreign key in the type as
  an `ID` (0087), so scoping its predicates hides nothing. It only changes
  results (a soft-deleted or hidden target reads as null) and adds an
  EXISTS per predicate on every policy-bearing target.

## Consequences

- Without `Veloxe`, a text that is a legacy cursor now fails when the
  connection is built, not when the argument is decoded. The error is the
  same value with the same text and code. gqlgen's response is byte for
  byte what it was: it reported the argument error on the field. graphql-go
  reported a variable error with no data. Now it reports the field's error
  with `data: {field: null}`, as gqlgen does (the parity suite's "a veloxe
  cursor"). No velox server ever wrote such a text.
- A server that reads `Cursor.Key` itself sees an opaque value for a
  legacy cursor, where before the argument failed.
- Under compat, a list sorted DESC on a non-unique column orders its ties
  by the key DESC. velox-graphql's veloxe e2e `TestVeloxeNames` changed
  with it.
- veloxe on SQLite put NULLs first in ASC; velox puts NULL last on every
  dialect (0032). A legacy cursor from a NULL row on SQLite pages from
  velox's NULL position. On PostgreSQL, nextapp's dialect, both agree.
- Parity items left out: `hasX` reaching soft-deleted rows (item 4) stays
  velox's behaviour; `first`+`before` and the others (item 6) stay refused.

## Evidence

- `contrib/graphql/veloxgql/internal/gqlcore/legacy_test.go`:
  `TestDecodeLegacyCursor` (the probe's eight cursors and fourteen written
  by veloxe's `Cursor` through msgpack v5.4.1: times, arrays, `vs`,
  floats, a UUID key), `TestDecodeLegacyCursorRefuses`,
  `TestDecodeLegacyCursorEncodings`, and `FuzzLegacyCursor` (CI's fuzz
  step).
- `contrib/graphql/veloxgql/cursor_test.go`: `TestCursorLegacy`.
- `contrib/graphql/veloxgql/gqlgraph/compat_test.go`:
  `TestVeloxeLegacyCursor` (VP:P2, P4, P11b, P12 replayed on the probe's
  rows, and `orderBy: ID` both ways), `TestVeloxeTieOrder` (VP-ties T1,
  T2), `TestVeloxePageFlags`, `TestVeloxeForeignKeyPlain` and
  `TestVeloxeForeignKeyGenerated` (through `gqlgraph.Predicate`), each
  against the same case without compat.
- `contrib/graphql/veloxgql/graphqlgo/parity_test.go`: "a veloxe cursor",
  without compat, equal through gqlgenx and graphqlgo.
- `contrib/graphql/cmd/velox-graphql/veloxe_e2e_test.go`: generated code
  under compat=veloxe pages from the probe's cursors through gqlgen, and
  filters by a customer a `WithFilter` filter hides.
