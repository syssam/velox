# 0166. Keyset paging over an ordering by an expression

- Status: accepted, 2026-11-24. Extends
  [0059](0059-query-paginate.md): `Query.Paginate` takes a query ordered
  by expressions. Amends 0142's recipe for a computed sort key and 0163's
  "connections never meet a Nulls term". Amended 2026-11-24: a cursor's
  value of another kind than its term gives is refused (see the
  amendment below).

## Context

An application sorts custom-object records by keys computed in SQL: a
guarded number or time parsed from a JSON member, a lowered text. It must
keyset-page them through `Query.Paginate`, the GraphQL connection
(`aggregate-shapes.md` (d)). `After` and every cursor refused an ordering
by an expression (`errAfterExpr`): After reads each ordering's value from
the row, and a row holds no value of an expression. 0142's recipe was a
projection with the key aliased beside the columns and a hand-written
`Expr` keyset, which loses `With`, the field collection and the
connection type the resolver returns.

## Decision

- **The read selects the values.** `Statement.OrderValues` (a bool in the
  bools' padding; the Statement stays 632 bytes or less) makes a SELECT of
  rows also read, after its columns and joined columns, each ordering
  that is an expression, `<term> AS velox_order_<n>`. Each copy binds its
  own values, as an expression's `IS NULL` term in ORDER BY already does.
  The values are scanned into `any`, a `[]byte` copied as a string, and
  kept beside the rows, not in the entity. The render cache keys on the
  flag (the eighth bit of the flags byte), and a Rewrite hook may not
  change it or the number of such orderings.
- **After takes the values.** `AddAfterValues(st, in, row, vals)`
  compares each such ordering with its value: equal is `e = v`, or
  `e IS NULL` for NULL; past is `e > v` where NULL sorts first, `(e > v OR
  e IS NULL)` where it sorts last, `e IS NOT NULL` past NULL where NULL
  sorts first, and nothing past NULL where it sorts last (`<` descending),
  as 0163 pages a nullable column. Every expression is taken to be
  nullable. The other orderings read the row as before, and the primary
  key or a unique index must still end the order. `AddAfter` is
  `AddAfterValues` with nil, so `After` still refuses an expression.
- **The untyped API carries it.** `TableQuery.OrderValues()`,
  `TableRows.OrderValues(i)`, `TableQuery.AfterValues(row, vals)`,
  `TableQuery.Order()`, `TableQuery.Unordered()`, `Table.OrderSig(o)` and
  `untyped.OrderValueTerm(o)`, `EncodeOrderValue`, `DecodeOrderValue`,
  `DecimalText` and `TimeText`. `velox.Query` gains nothing: a method in
  `Query[E]`'s method set costs every entity package its declaration, and
  `entityDescMixin` was 62 bytes under its bound.
- **A connection's query may be ordered by expressions.** gqlgraph puts
  them first, then the `orderBy` argument, then the key. An ordering of
  the query by a column or by a relation's aggregate is still refused
  with the same internal error. A backward page turns each expression's
  direction and its NullsFirst or NullsLast; the default turns with the
  direction, as 0163 has it.
- **The cursor.** The format does not change: `{"v":1,"t":…,"o":[…],
  "k":…}`. An expression's term is named `expr:<signature>`, the FNV-64
  hash of the ordering as SQLite writes it in ORDER BY (direction and NULL
  placement included) and of each bound value with its Go type. A cursor
  taken under another expression, other bound values, another placement,
  or no expression, is "taken under another order" (`ErrBadCursor`).
  Because the bound values are hashed, an expression bound to a value that
  changes per request (the time now, a search term) makes every earlier
  cursor another order's: bind such a value in a filter, or accept cursors
  that last one request. A msgpack (entgql) cursor is refused on such a
  connection; entgql never paged one.
- **Values keep their kind.** `untyped.EncodeOrderValue` and
  `DecodeOrderValue` (root, so the integration tests cover the round trip
  on every dialect) write a value so that it reads back as the same Go
  value: null, a bool, an integer as a JSON integer (`"u:<digits>"` past
  int64: MySQL's BIGINT UNSIGNED, PostgreSQL's numeric(20,0)), a finite float as a JSON number with a
  fraction or exponent (`3.0`), NaN and the infinities as `"f:NaN"`, text
  as `"s:<text>"`, a time as `"t:<RFC 3339 nano>"`, a decimal read as text
  as `"d:<text>"` and a time read as text as `"dt:<text>"`. A cursor is
  the client's, so decoding takes only what the encoder writes: a decimal
  as the drivers print it (digits, sign, point, no exponent) or exactly
  PostgreSQL's `NaN`, `Infinity` or `-Infinity`, a time as MySQL's
  `2006-01-02[ 15:04:05[.ffffff]]` that is a real date or exactly its zero
  date (`0000-00-00`, with zero time and zero fraction digits as MySQL
  prints them), `u:` digits past int64 with no sign or leading zero, `f:`
  exactly NaN, +Inf or -Inf, and a JSON number as JSON spells one.
  Anything else (`d:nan`, `d:+NaN`, `dt:0000-00-00 99:00:00`) fails with
  `ErrBadCursor`, before the database could cast it to 0 or NULL. Stored
  data always pages: every edge carries a cursor, so an encoder that
  refused a value would fail the whole page. PostgreSQL's NaN binds as
  `CAST(? AS NUMERIC)` and sorts after every number. MySQL's default
  sql_mode (NO_ZERO_DATE) casts a zero-date literal to NULL, so the keyset
  binds none: the zero date (text, or the zero `time.Time` parseTime reads
  it as) sorts first, and its conditions are `YEAR(e) = 0 AND MONTH(e) = 0
  AND DAY(e) = 0` and its negation. MySQL compares no zero date as less
  than a date either, so descending past any other date the keyset admits
  the zero date by that condition. A real date 0001-01-01 read with
  parseTime is the zero `time.Time` too, so MySQL pages it as the zero
  date. Decoding has no dialect, so `AfterValues` refuses with
  `untyped.ErrOrderValue` (a GraphQL connection's `ErrBadCursor`) a value
  no read on the statement's dialect gives, before the database sees it:
  a NaN or infinite decimal off PostgreSQL (MySQL casts `'NaN'` to 0), the
  zero date off MySQL (PostgreSQL fails to read it as a time, SQLite
  compares it as text), a NaN float off PostgreSQL, an infinite float on
  MySQL, an unsigned integer past int64 on SQLite, text holding a NUL
  byte on PostgreSQL (amendment below). `FuzzOrderValue` holds
  the round trip.
- **A decimal or a time read as text is bound cast to its type.** The
  read takes each value column's database type when the rows tell it
  (`sql.ColumnType.DatabaseTypeName`): text in a DECIMAL or NUMERIC column
  is an `untyped.DecimalText`, and in a DATE, DATETIME or TIMESTAMP column
  a `TimeText`. pgx's own rows tell no types; its `pgtype.Numeric` (read
  into an `any`) is a DecimalText by its `NumericValue` method. The keyset
  binds a DecimalText as `CAST(? AS DECIMAL(65,30))` on MySQL and
  `CAST(? AS NUMERIC)` elsewhere, and a TimeText or a `time.Time` as
  `CAST(? AS DATETIME(6))` on MySQL. MySQL compares a decimal with text as
  a double, so without the cast rows that differ past a double's digits
  tie, and the page repeats or skips them. What the drivers give, as read
  in their source: go-sql-driver/mysql 1.10 parses integers (uint64 when
  unsigned) and floats even on the text protocol, and returns `[]byte` for
  DECIMAL and, without parseTime, for DATE, DATETIME and TIMESTAMP; pgx's
  database/sql driver returns numeric as a string, and pgx sends a string
  argument in text format, which PostgreSQL reads as the type the context
  gives it. `TestOrderValuesWire` pages an integer, a fraction, a decimal
  finer than a double, a text and a time (on MySQL and PostgreSQL an
  unsigned value past int64, and on MySQL a time without parseTime)
  through the cursor's JSON, both
  ways, asserting the exact rows.
- **Any expression may page.** Any expression that is not an aggregate
  or a window function may page; terms have no Go type. The expression
  must give a row the same value each time, and compare in SQL as its
  value read back compares. A type the cast list does not name (MySQL's
  TIME, JSON) is bound as the driver read it.

## Rejected

- **Requiring typed terms.** velox's `Term` has no Go type, and a typed
  variant would add generic types to every entity package. The driver's
  value, written with its type into the cursor, round-trips on all three
  dialects.
- **Reusing 0128's placeholders on PostgreSQL.** 0128 exists because
  PostgreSQL matches a grouped expression by its text. An ungrouped ORDER
  BY needs no match, and two equal orderings with equal values would share
  placeholders that the render cache key does not describe.
- **Computing the cursor row's value with a subquery by its key**, as a
  relation's aggregate does. A row deleted or changed between pages would
  move the page.
- **A second statement for the page's keys (0142's option B).** The two
  statements can disagree under concurrent writes.
- **Root `After` with values.** A method of `Query[E]` costs every entity
  package; `untyped.FromQuery(q)` reaches the untyped API.

## Consequences

- `customobject/sort.go` maps to `q.OrderBy(nullsLast, key.Desc(), …)`
  and `Paginate`, with no hand-written keyset and the generated connection
  type.
- No generated-code change: the generated `PaginateQuery` passes q to
  gqlgraph as before. Its comment still says q must not be ordered; the
  runtime's godoc says what is allowed.
- Existing cursors encode and decode byte for byte as before
  (`TestOldCursorBytes`).
- Compile cost: no change to a generic type or the generated code.

## Amendment, 2026-11-24: a value must be of its term's kind

A downstream application found that a cursor value which decodes but has
the wrong kind for its term was bound as it was. A client that changed
the value of a `Text(k).Number()` ordering to `"s:abc"` failed the whole
statement on PostgreSQL, a 500, and read a wrong page on SQLite. Text
holding a NUL byte failed it on PostgreSQL too. The decision promises
that a forged cursor is `ErrBadCursor`, never a wrong page.

- **A static check, not error mapping.** `AfterValues` checks each value
  against the kind its term gives, where velox knows it without the
  database (`termValueKind`). A mismatch is `untyped.ErrOrderValue`,
  which a connection reports as `ErrBadCursor`. PostgreSQL's 22xxx errors
  are not mapped: row data raises the same codes, such as `22P02` from a
  `Text(k).Number()`-like Calc over a non-numeric row.
- **What a term knows.** A JSON path's `Number`, `Len` and `Text(k).Number`
  give a number; `Text(k).Time` a time; `Text` text; `Bool` a bool. A
  date bucket gives a time, and a Calc marked `Decimal` a number. A
  column, through a joined term, is known by its Go type: an integer or
  float, or a MySQL decimal column, a number; a string text; a bool a
  bool; a time a time; a `[]byte` bytes. A type with Value or Scan, such
  as a decimal type, and an encoded column are not known.
- **What each kind accepts** is what the drivers read for it and the
  encoder writes. NULL is every kind's.
  - A number accepts an integer or a float, or a `DecimalText`
    (PostgreSQL's numeric through pgx, MySQL's decimal).
  - A time accepts a `time.Time` or a `TimeText` (MySQL without
    parseTime). SQLite also accepts a string: its times are velox's text.
  - Text accepts a string.
  - A bool accepts a bool. MySQL and SQLite also accept an integer, since
    their booleans are integers.
  - Bytes accept a string, as `OrderValues` copies them, or a `[]byte`.
- **What stays unchecked.** A Calc that is not marked `Decimal`, and a
  JSON path's raw value, have no known kind. Their values are bound as
  before, so a forged value of another kind can still fail the statement
  on PostgreSQL. A string column whose SQL type is not text (a string
  field mapped to a numeric column by an annotation) is taken as text.
  SQLite takes any string for a time; it compares text and fails nothing.
- **Dialect refusals.** `checkOrderValue` also refuses text holding a NUL
  byte on PostgreSQL: its text cannot hold one, so no PostgreSQL page
  wrote one. A `bytea` value holding NUL cannot page either; it never
  could, since pgx sends the string as text. An unsigned integer past
  int64 is refused only on SQLite. PostgreSQL's numeric(20,0), velox's
  uint64 column there, holds one, as MySQL's BIGINT UNSIGNED does.
- **Size.** The check is type switches and the reflect calls the engine
  already makes. It adds no regexp and no package-level variable.
- **Tests.** `TestCheckValueKind` and `TestTermValueKind` cover each kind
  on each dialect. `TestOrderValuesWire`'s "typed terms" pages JSON
  Number, Time and Text orderings on every dialect, and refuses
  mismatched values and PostgreSQL's NUL. gqlgraph's
  `TestConnectionExpressionValueKind` refuses them at the connection.
