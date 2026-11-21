# 101. Window terms, soft-delete options, no back-references

Status: accepted, 2026-11-21.

## Context

nextapp's generic aggregate service pages a grouped report and reads its
total in one statement with `COUNT(*) OVER ()` (veloxe gap report §2.1
item 11). velox's `Calc` and `SelectRaw` text (0069, 0074) already passed
`OVER`: no word of a window clause is in the refused list. Nothing kept a
window function out of the places SQL refuses it, so a `Where` holding one
reached the database and failed there.

nextapp's soft delete sets `deleted_by_*` from the context and leaves reads
unfiltered (UF N-V11, item 12). `softdelete.Mixin` could do neither.

veloxe fills a loaded child's inverse edge with its parent (item 13). velox
does not, and whether nextapp reads such a back-reference was unknown.

## Decision

- A window function is allowed in `Calc` and `SelectRaw` text. `newExpr`
  marks an expression whose text holds the word `OVER` outside a literal.
  What `OVER (...)` holds is checked as the rest of the text is, so a
  subquery and the refused functions stay refused inside it.
- A window function is a result column or an ordering only. velox refuses
  one, before anything is sent, where SQL computes windows after the
  clause:
  - an `Expr`, in its text or a `Term` among its slots (`ExprNode`), which
    covers `Where`, `Having`, interceptors and policies;
  - `GroupBy` (`checkProject`);
  - an aggregate of it (`AggTerm`);
  - `SetExpr` (`checkSetExpr`).
- Beside `GroupBy` and `Limit`, `COUNT(*) OVER ()` counts every group. It
  is tested on SQLite in `internal/apitest`, and on PostgreSQL, MySQL 8 and
  SQLite in `internal/integration`.
- `softdelete.Mixin` gets two fields. Its zero value behaves as before.
  - `Assign func(ctx) ([]softdelete.Set, error)` returns more columns for
    the soft delete's one UPDATE. A `Set` names a column as
    `velox.FieldEQ` does, and its value is converted as `Mutation.Set`
    converts it (`engine.NamedAssignment`). The delete fails, and nothing
    is sent, on an error from `Assign`, an unknown column, a value that
    does not convert, or `deleted_at` or a primary-key column among the
    columns. It does not run under `Skip`, whose delete removes the rows.
  - `Reads: softdelete.Unfiltered` installs no interceptor. Reads, updates
    and Saves see deleted rows, and a restore is a plain update. The
    delete hook ANDs `deleted_at IS NULL` itself, so a second delete
    affects 0 rows and keeps the first one's time and `Assign` values.
    This is nextapp's own mixin's behaviour.
  - The type is a by-name list, not `[]velox.Assignment`. A schema
    package cannot name a column by index, and a list keeps the UPDATE's
    text, and so its render-cache entry, the same from call to call, which
    a map would not.
- Back-references are not built. nextapp-graphql's hand-written code was
  searched, excluding the generated `velox` package and every file headed
  `Code generated … DO NOT EDIT`, for `.Edges.<Name>` reads. Each read of a
  to-one parent edge (`Edges.Bill`, `Edges.PurchaseOrder`,
  `Edges.ItemReceipt`, `Edges.Review`, `Edges.PosTransaction`, and the
  rest) follows a `With<Name>()` of that edge on the child's own query in
  the same package. None reads an inverse edge filled by a parent's load.
  `docs/coming-from-ent.md` records it under "Refused by design".

## Consequences

- No generated code changes, and the root's generic types do not change.
- A window function's text in a condition now fails in velox with an error
  that says where to put it, not in the database.
- A program that sets `deleted_by` in a hook of its own can drop the hook for
  `Assign`. An `Unfiltered` entity's reads must test `deleted_at` where they
  mean live rows.
