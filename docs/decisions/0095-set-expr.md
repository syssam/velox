# 95. SQL where the builders end: SetExpr, and Calc as the term

Status: accepted, 2026-11-20.

## Context

The user asked for SQL the builders cannot say to be easy to write, easier
than in ent, with the policies still applying. The design
(`docs/superpowers/specs/2026-11-20-inserts-changes-sql-design.md`, part 4)
asked for three things: an expression as a term, `velox.SQL[E]`; an
expression as an assignment, `Col.SetSQL`; and a guide.

Most of it existed. `velox.Calc[E](text, args...)` is an expression as a
`Term` (0074): it orders with `Asc` and `Desc`, is a result column of
`Select` and a key of `GroupBy`, and its text is `Expr`'s, checked when the
statement runs. The spec's `OrderBy(velox.SQL[E]("similarity(?, ?)", Name,
q).Desc())` is `velox.Calc[E](…).Desc()` today. `SelectRaw` is
`Select(Calc(…))`. Conditions are `Expr` (0069), subqueries `InSelect` and
`HasWith` (0057), raw statements `Raw`, `Rows` and `Exec` (0060).

What was missing was the assignment. An update could set a Go value, or add
or subtract one (`Add`, `Sub`); `SET views = views * 2` or `SET search =
to_tsvector(…)` went to `db.Exec`, which skips the scope. ent writes it with
`Modify(func(u *sql.UpdateBuilder){ u.Set(c, sql.Expr(…)) })`, whose
function sees the whole statement.

## Decision

- No `velox.SQL`. `Calc` is the expression term, and a second name for it
  would be two ways to say one thing. `docs/sql.md` lists every place SQL
  text goes, so the name is found where the need is.
- `velox.SetExpr[E](c Column[E], text string, args ...any) Setter[E]`
  assigns column c an expression: `UPDATE … SET c = (text)`. Its text and
  arguments are `Expr`'s: a column handle or a `Term` of E fills a `?` as it
  is, any other value is bound, and the same text is refused.
- It is named for `Expr`, whose text it takes, as `Calc` is not: `SetExpr`
  says "assign an `Expr`-style text". `SetSQL` would bring a third word for
  one kind of text.
- It is a free function, not a method on the handles. A method on every
  handle kind costs compile time in each entity's package (0050,
  `TestGenericTypesCompileSmall`); the function costs nothing there. E is
  inferred from the handle through `Column[E]`'s method, so the call names
  no type: `velox.SetExpr(P.Views, "? * 2", P.Views)`.
- `veloxtype.AssignExpr` is the new `AssignOp`, public so that a Rewrite
  hook can tell the assignment. Its `Value` is the expression, an
  `untyped.Term` (engine's). The render cache keys the expression's shape.
- velox cannot know the value before the database computes it. So:
  - the column's validators do not run, as on `Add` and `Sub`; refusing
    columns with validators would refuse most string columns and a counter
    with `Min(0)`, which `Add` already updates unvalidated;
  - a hook's `Mutation.Value` reports false for the column, as for `Add`;
  - `SaveQuery` stores only `AssignSet` values back into the entity;
    `UpdateQuery.One` reads the row back, so it returns the computed value.
- Refused, before anything is sent: an `Immutable` column (`ErrImmutable`)
  and a `Version` column (`ErrVersionSet`), by the checks every assignment
  has; an encoded (JSON) column, whose stored bytes are velox's encoding,
  as `Add` and `Expr` refuse it; a primary key that `One` must read back,
  as `Add` is (0092); a bidirectional edge's key, which only `Set` may
  assign; an aggregate in the text, which an UPDATE cannot compute; and an
  insert's setter: an insert's columns are Go values, held in its rows.
- The scope is safe: its column is `Immutable`, and an insert refuses the
  expression. The Write policy, the scope and the interceptors narrow the
  UPDATE as they narrow any other. The text names only the entity's own
  columns, never another table.
- On MySQL an expression reads the values earlier assignments of the same
  UPDATE wrote, the `UpdateDefault` ones included; PostgreSQL and SQLite
  read the row as it was. velox documents it and does not reorder.
- `untyped.SetExpr(col, text, args...)` returns the `velox.Assignment`, for
  `TableUpdate.Assign`, with `untyped.Col` terms. velox-graphql needs no
  change: its inputs set Go values.
- Generated code does not change.

## Consequences

- An application writes `SET views = views + 1`, `SET search =
  to_tsvector(…)` and `SET total = ROUND(total * ?, 2)` in a typed update
  that keeps its policies, where ent needs `Modify` and velox needed `Exec`.
- An insert cannot take an expression yet. `INSERT … VALUES (to_tsvector(?))`
  is a later change, after 0093 reshapes the INSERT's column list.
- `docs/sql.md` is the guide, with a table of which tools keep the policies
  and which bypass them with a reason.
