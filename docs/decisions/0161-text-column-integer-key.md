# 0161. A text column meets an integer key with the key bare

- Status: accepted, 2026-11-24. Amends [0157](0157-field-in-select-raw.md)
  and [0079](0079-field-in-select.md).

## Context

0157 compares a text column with an integer key, in `FieldInSelect` and
`FieldInSelectRaw`, by casting the key to text inside the `EXISTS`:

```sql
EXISTS (SELECT 1 FROM "sales_order" AS "vx1"
        WHERE CAST("vx1"."sales_order_id" AS TEXT) = "activity"."entity_id" …)
```

The key is inside a function, so neither PostgreSQL nor MySQL can probe its
primary-key index with the outer row's value: each outer row scans the
target. An activity trail of many rows over a large table is slow for no
reason but the side the cast is on.

## Decision

The key stays bare. The text column is cast to an integer when it spells
one, and is NULL otherwise, so the rows that match are the rows that text
equality matched: `"7"` meets 7; `"07"`, `" 7"`, `"+7"`, `"7.0"`, `"-0"`,
`""` and a number past int64 meet nothing. A guard keeps every other text
from the cast, in a `CASE`, so no text fails the statement.

- PostgreSQL: `"vx1"."k" = CASE WHEN c COLLATE "C" ~ '<int64Text>' THEN
  CAST(c AS BIGINT) END`. `COLLATE "C"` because a nondeterministic collation
  refuses a regular expression. An `int2` or `int4` key meets the `bigint`
  through PostgreSQL's integer operator family, which its btree index
  serves, so one cast fits every signed width and an out-of-range text
  never reaches a narrower cast.
- MySQL: `CASE WHEN c REGEXP '<int64Text>' AND c NOT REGEXP '[^-0-9]' THEN
  CAST(c AS SIGNED) END`. A cast of a bad text is a warning in a read and an
  error in a write's `Where` under strict mode, so the guard is needed for
  both. ICU's `$` may match before a final line break, which the second
  test refuses.
- SQLite: `CASE WHEN CAST(CAST(c AS INTEGER) AS TEXT) = c THEN CAST(c AS
  INTEGER) END`. SQLite has no regular expressions and its cast never
  fails: it reads a text's leading integer, 0 when there is none, clamped to
  int64's range. Only an int64's own text comes back unchanged, and the
  round trip compares under the column's collation, as the cast key did, so
  the rows are exactly 0157's. `EXPLAIN QUERY PLAN` shows the key's index
  searched (`SEARCH vx1 … (id=?)`), where 0157's form built an automatic
  index or scanned.

`int64Text` (`internal/engine/render.go`) matches exactly the texts
`strconv.FormatInt` writes: `0`, or an optional `-` and up to 18 digits with
no leading zero, or 19 digits up to 9223372036854775807 spelled digit by
digit, or `-9223372036854775808`. It has no `?`, which MySQL's driver counts
as a placeholder under `interpolateParams`, and no backslash, which
`NO_BACKSLASH_ESCAPES` reads differently; `-{0,1}` stands for `-?`.

An unsigned 64-bit key (`uint64`, `uint`) keeps 0157's form, the key cast to
text: its values pass int64's, and on PostgreSQL it is `numeric(20,0)` or
`bigint` by whether it is an identity, so no one integer cast fits. Unsigned
keys of 32 bits or fewer take the integer form.

The reverse, an integer column against a text key (0091), is unchanged: its
cast is already on the outer column and the text key is bare.

The form is the default, with no option: it matches the same rows, and the
point is speed. FieldInSelect renders only as the correlated `EXISTS`; there
is no `IN` form to change, and a relation's aggregate term never carries a
FieldInSelect's link.

### Rendering with no database

`velox.Open(d, nil)` is the supported way to render a statement's SQL per
dialect without a database: the `SQL` methods of `Query`, `UpdateQuery`,
`DeleteQuery` and the rest run the guard, the Rewrite hooks, the policy
scope and rendering, and send nothing. A statement or `Tx` run on that DB
now fails with an error naming the nil `*sql.DB`, where it was a nil-pointer
panic. A policy that reads the database reads through the same DB, so
`SQL` fails with that error. No helper is added: the Examples
and the guide already used the form, and a helper in `veloxtest` would pull
the test package into tools that only print SQL.

## Rejected

- Casting the column with no guard: PostgreSQL fails the statement on the
  first non-numeric text, MySQL fails a write's `Where`, and SQLite's cast
  of `"abc"` is 0, which would match a key 0.
- A length and lexicographic check for the range: it depends on the
  column's collation; the regular expression does not.
- A numeric cast in a nested `CASE` for the 19-digit range: longer, and two
  casts per row.
- `pg_input_is_valid`: PostgreSQL 16 and later, and it accepts `"07"`,
  `" 7"` and `"+7"`, which text equality does not.
- An option to keep 0157's form: the rows are the same.

## Consequences

- Under a MySQL `PAD SPACE` collation (`utf8mb4_general_ci`), or one that
  folds full-width digits, 0157's text compare also matched `"7 "` or
  `"７"`; the integer form does not. The default `utf8mb4_0900_ai_ci` is
  `NO PAD`. The change can only narrow such a match.
- The SQL is longer; the render cache holds it once per statement shape.

## Evidence

`internal/engine/named_sub_test.go`: `TestFieldInSelectTextColumn` (the SQL
per dialect, FieldInSelect and FieldInSelectRaw, in `Not`).
`internal/engine/int64text_internal_test.go`: `TestInt64Text` (the regular
expression against `strconv` at both ends of int64, every digit at every
place), `TestInt64Key`. `internal/integration/fieldinselectraw_test.go`:
`TestFieldInSelectTextColumnForms` (the forms and boundaries, a read, an
UPDATE and a DELETE, the SQLite plan; PostgreSQL and MySQL when their
variables are set). `internal/apitest/offline_test.go`: `TestOfflineSQL`.
