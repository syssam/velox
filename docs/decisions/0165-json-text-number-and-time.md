# 165. A string in a JSON column reads as a number or an instant

- Status: accepted, 2026-11-24. Extends [0058](0058-json-paths.md).

## Context

An app keeps custom objects as JSON documents whose fields its users define.
A field of type number may hold `10` or `"10"`, by whichever client wrote it,
and a field of type date-time holds an RFC 3339 string in the writer's
offset. The app filters and sorts on them. 0058's `Number` is NULL for the
string, and `Text` compares `"9"` above `"10"` and
`"2026-03-01T10:00:00+08:00"` above `"2026-03-01T03:00:00Z"`, an earlier
instant. The app wrote the casts in PostgreSQL-only raw SQL, which fails the
statement on the first value the cast cannot read.

## Decision

`JSONText` gets two methods. Both read at the text's path, nested keys
included, and give NULL, never an error, for anything else.

- `Text(keys...).Number()` is a `JSONNumber`: the number at the path, or
  the number a string there spells. It has `JSONNumber`'s comparisons,
  `Between`, `IsNull`, `NotNull`, `Asc`, `Desc` and `Term`.
- `Text(keys...).Time()` is a new `JSONTime`: the instant an RFC 3339
  string spells. It has `EQ`, `NEQ`, `GT`, `GTE`, `LT`, `LTE` and
  `Between` of a `time.Time`, bound in UTC, `IsNull`, `NotNull`, `Asc`,
  `Desc`, and `Term`, which `Scan` reads into a `time.Time`.

They are two new `JSONKind`s, so a statement's cache key holds them as it
holds 0058's kinds, and they work wherever a JSON path's value does: Where,
Not, Or, a write's Where, a result column and an ordering.

### What a string must spell

A number is JSON's grammar (`jsonNumberText`): an optional `-`, `0` or digits
with no leading zero, an optional fraction and exponent. The exponent has at
most two digits and the text at most 200 characters, so that every value is
within float64's range and no cast overflows. `"10"`, `"-2.5"`, `"1e3"` and
`"1E+05"` do; `"+1"`, `"01"`, `".5"`, `"1."`, `" 1"`, `"1e100"`, `"1,000"`
and `"NaN"` do not. JSON's grammar rather than a looser one so that the
dialects agree: SQLite tests it with `json_valid`.

A time (`jsonTimeText`) is `YYYY-MM-DDTHH:MM:SS`, a fraction of one to nine
digits or none, then `Z` or an offset within ±14:59. The T and the Z are
upper case. The date is real, leap years included, of a year from 1000 to
9999. A leap second does not match, as Go's `time.Parse` refuses it.

- The years are MySQL DATETIME's range.
- 9999-12-31 is refused: an offset west of UTC moves its instant past 9999,
  which neither MySQL's DATETIME nor SQLite's date functions hold.
- ±14:59 is SQLite's limit on an offset, and within PostgreSQL's.

### SQL

A guard in a `CASE` keeps every other text from the cast. A cast of a bad
text fails a PostgreSQL statement. On MySQL it warns, and strict mode makes
that warning an error in a write's Where (0161).

- PostgreSQL: Number is `CASE jsonb_typeof(v) WHEN 'number' THEN
  (t)::numeric WHEN 'string' THEN CASE WHEN length(t) <= 200 AND t COLLATE
  "C" ~ '<jsonNumberText>' THEN CAST(t AS numeric) END END`, which compares
  with a float64 as 0058's Number does. Time is `CASE WHEN
  jsonb_typeof(v) = 'string' AND t COLLATE "C" ~ '<jsonTimeText>' AND
  left(t, 10) <> '9999-12-31' THEN CAST(t AS timestamptz) END`, which reads
  the offset. The regular expression checks the date, so no `pg_input_is_valid`
  is needed, which PostgreSQL 14 lacks.
- MySQL: Number casts a JSON number as 0058 does, and a string that passes
  `REGEXP_LIKE(u, '<jsonNumberText>', 'c')`, `NOT REGEXP_LIKE(u,
  '[^-+.0-9eE]', 'c')` and the length to `DOUBLE`, as 0058's Number reads a
  number. Time takes the text that passes `REGEXP_LIKE(u,
  '<jsonTimeText>', 'c')`, `NOT REGEXP_LIKE(u, '[^-+.:0-9TZ]', 'c')` and the
  last-date test. It casts the text before the zone to `DATETIME(6)` and
  subtracts the offset in minutes, read from the text, giving a DATETIME of
  UTC, as velox stores a time. `'c'` keeps case whatever the collation. The
  second test refuses a final line break, before which ICU's `$` can also
  match (0161).
- SQLite has no regular expressions. Number takes the text that is at most
  200 characters, `json_valid` (RFC 8259's grammar), and two GLOBs: only
  number characters, and no three-digit exponent. It casts that text `AS
  REAL`; the guard matters here, as `CAST('abc' AS REAL)` is 0. Time is
  guarded by GLOBs of the shape and the zone, a round trip through strftime
  (which moves 2026-02-30 to March), a test of the hour (strftime keeps
  24:00 as written), and `julianday(t) IS NOT NULL`, which refuses anything
  but a fraction between the seconds and the zone. The value is velox's
  SQLite text of a time, `2006-01-02 15:04:05.000000000+00:00` in UTC. It
  compares as velox binds a time there and velox's SQLite rows parse it into
  a `time.Time`. strftime converts the time to UTC with its offset but
  without its fraction, which it would round to the millisecond; the
  fraction is copied, padded to nine digits.

### Rejected

- MySQL `CONVERT_TZ(local, '+08:00', '+00:00')`: it leaves a time outside
  TIMESTAMP's range (1970 to 2038) unconverted.
- MySQL's offset in a datetime literal (8.0.19): it converts to the
  session's time zone, which velox does not set.
- SQLite `julianday` or `unixepoch` as the value: they keep milliseconds,
  and give no `time.Time` to Scan.
- One subquery per value that extracts the text once: MySQL before 8.0.14
  refuses an outer reference in a derived table, and the other dialects
  gain nothing a plan cannot.
- `DECIMAL(65,30)` on MySQL: 35 integer digits, and an overflow warns. A
  float64 argument compares as a double in any case.

## Consequences

- Each value at the path binds the path again: Number binds it up to 10
  times on PostgreSQL, Time 18 times on SQLite. No index serves the
  expressions.
- PostgreSQL and MySQL compare a time to the microsecond, rounding finer
  digits; SQLite to the nanosecond.
- A number past 2^53 loses precision, as 0058's does.
- `TestJSONParsedText` (`internal/engine`) checks both regular expressions
  against Go's parsers over texts one character from valid ones.
  `TestJSONTextParsedParity` (`internal/integration`) checks that every
  dialect reads those texts as Go does. `TestJSONTextNumber` and
  `TestJSONTextTime` run the conditions, a write's Where, the orders and
  the result columns. `TestJSONTextParsedSQL` (`internal/apitest`) holds
  the SQL of each dialect.
- No generated-code change: the methods are on `JSONText`, which the
  handles already return.
