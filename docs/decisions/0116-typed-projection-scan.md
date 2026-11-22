# 116. Scan reads a projection into a struct

Status: accepted, 2026-11-22.

## Context

`Query.Select` reads terms, and `Rows` yields them by position (0074).
`RawRow.Scan` is the driver's: an encoded column comes back as its stored
bytes, and a JSON column could not be selected at all. ent scans a
`GroupBy`, `Aggregate` or `Select` into a struct with `Scan(ctx, &v)`, by
column name. ADR 0074 left scanning into a struct out.

## Decision

- `velox.Scan[T](ctx, q) ([]T, error)` and `velox.ScanOne[T](ctx, q) (T,
  error)` are generic functions, not methods: T is not E, and a function is
  compiled only where it is called (0055), as `Values` and `IDs` are.
  `ScanOne` sets `Limit(2)` and fails with `ErrNotFound` or
  `ErrNotSingular`, as `One` does.
- **Names.** A result column's name is a column's own, or the one
  `Term.As(name)` gives. `As` writes `AS name` in the select list only; in
  GroupBy, Having, an Expr and an ordering the term is written as it is,
  and Distinct's ordering check ignores the name. A name is an identifier
  of at most 63 bytes, the same on every dialect.
- **Fields.** A struct field takes the result column its tag `velox:"name"`
  names, else the one whose name equals the field's with case and
  underscores ignored, or whose column's Go field it is (`OrgID` takes
  `org_id`). A tag beats a name; `velox:"-"` takes none. Exported fields,
  promoted ones included; a nil embedded pointer gets a new struct.
- **The value itself.** A T that is no struct, a `time.Time`, a
  `sql.Scanner`, or a struct with no field for the one JSON column a
  projection reads, takes the one term.
- **Decoding.** A JSON column is decoded into its field by the column's
  codec, as an entity's is. A JSON column may now be a result column of a
  projection, read whole; it is still refused in GroupBy, an ordering, an
  Expr, an aggregate and a DISTINCT read. Everything else goes to the
  driver's Scan: a pointer field takes NULL as nil, a number converts to
  any number type, text to a named string type (an enum), and on SQLite a
  time from the text an aggregate yields, as the connection reads it.
- **Refusals before anything is sent:** no Select, a term with no name, a
  result column with no field, two fields for one column, one field for
  two, and a field whose type cannot hold the term's: a column's Go type,
  a JSON path's kind, a count's int64, a sum's or average's number, Min's
  and Max's column type. Calc is not checked; the driver's error names it.
  A row that fails to scan names the entity and T.
- The plan is built by `engine.NewScanPlan` once per call, outside the
  generic function, so the generic body is a few lines.

## Rejected

- A `Scan(ctx, &v)` method on Query: a method of a generic type cannot take
  a type parameter of its own, and `any` would move the type check to run
  time without a gain.
- Matching by the database's column names (`Rows.Columns`): velox knows each
  term, so it maps by position and refuses before sending.
- `velox:"…"` falling back to `json` tags: ent uses `sql` tags; a struct
  tag of velox's own is explicit.

## Not done

- `untyped.TableQuery` and `db.Rows` have no Scan: the first can be added
  beside it when a caller needs one; the second has no terms to decode by.
- Codec decoding of a JSON column under an aggregate or Calc: such terms
  are refused already.

## Evidence

- `internal/apitest/scan_test.go`: struct and whole-value scans, codec
  decoding, NULL, `ScanOne`'s errors, every refusal and the decode error.
- `internal/integration/scan_test.go`: a grouped report (sum, count,
  MIN of a time into `time.Time`, MAX into a pointer), an enum, a nullable
  enum, a time column, JSON columns and JSON path terms, on SQLite and, in
  the gate, PostgreSQL and MySQL.
