# 0155. ExprRaw is one raw condition, with a reason

- Status: accepted, 2026-11-24. Amends 0069.

## Context

An application ported from ent wrote conditions as `func(*sql.Selector)`
with subqueries that velox's typed API cannot say without changing what
they mean:

1. `entity_id IN (SELECT CAST(bill_id AS TEXT) FROM bill WHERE
   organization_id IN (…))`: a text column against an integer key made
   text. `InSelect` refuses two types (0057), and reading the ids into Go
   does not scale.
2. `member_role_id IN (SELECT member_role_id FROM member_role WHERE
   organization_id IN (…))`, on purpose without member_role's policy,
   interceptors or soft delete: a compliance audit trail must not lose rows
   when an assignment is removed later. `HasWith` and `InSelect` apply the
   target's policies (0057), which is right for every other use.
3. A correlated `NOT EXISTS (SELECT 1 FROM audit_log m WHERE
   m.workspace_id = <outer>.workspace_id AND m.entity_id =
   CAST(<outer>.id AS TEXT))`, generic over any entity with a workspace_id,
   on a table no entity maps.

`Expr` refuses a subquery (0069), rightly: velox cannot add the inner
table's policy without parsing SQL. The only other way was `Raw` for the
whole statement, which drops the outer entity's policies too.

## Decision

- `velox.ExprRaw[E](reason, text string, args ...any) Pred[E]`, beside
  `Expr`, named as `SelectRaw` pairs with `Select`, the reason first as in
  `db.Raw`. `untyped.ExprRaw(reason, text, args...)` is its untyped form.
  It is `db.Raw`'s trade made explicit for one condition.
- The text may hold `SELECT`, `EXISTS`, `WITH`, `UNION`, a window inside a
  subquery and any function: the word list of 0069 does not apply. velox
  adds nothing inside the text, so no policy, filter, scope or interceptor
  applies there. The statement around it is built as always: its Read or
  Write policy, scope and interceptors still filter its own rows. It is a
  `Pred[E]`, so it works in `Where`, `Having`, `Or`, `And` and `Not`, and
  in an update's and a delete's `Where`.
- The guard on structure stays: outside a literal, a semicolon, `--`, `/*`,
  `*/`, `#`, a backslash, `$`, a control character, an `@` before a name and
  an unbalanced parenthesis are refused, and a literal is 0069's. A quoted
  identifier, `"x"` or `` `x` ``, is allowed when it is closed and holds no
  doubled quote, `?`, backslash or control character. An empty or blank
  reason fails the statement, as `db.Raw`'s does.
- `?` is the only placeholder, written in the dialect's form. A column
  handle or a Term of E fills a `?` as the outer row's column, always
  qualified: by the table's name (with its schema) at the top of the
  statement, under a join too, since velox never aliases the statement's
  own table; by velox's alias `vx1`, `vx2`… inside a relation's subquery
  (`HasWith`, `InSelect`); by the join's alias in a joined target's
  condition. `Expr` writes a bare column where it can, and a bare name in a
  subquery binds to the subquery's own table, so a raw condition qualifies.
  The text must alias its own tables, never with the outer table's name or
  `vx` and a number.
- Values are arguments, bound as `Expr`'s are. The render cache keys the
  text, what each slot holds and whether the condition is raw (an `Expr` of
  the same text writes bare columns), never the values or the reason.
- `Statement.RawReasons() []string` returns the reasons of every raw
  condition the statement holds, in `Where`, a join's `On`, a projection's
  `Having` and an upsert's conditions, at any depth, in the order written;
  nil for none. A method computed from the conditions, not a field: a
  Rewrite hook that drops a condition drops its reason, and `Statement`
  stays in its size class. `PolicyBypass` is not set: the policies still
  apply to the statement's rows, and a hook that treats `PolicyBypass` as
  "every policy was lifted" stays right.

## Rejected

- Adding the inner table's policy: it needs a SQL parser to find the tables
  (0069's reason, unchanged).
- A `cast` option on `InSelect` for case 1: it covers case 1 alone, and case
  2 asks for the policy to stay off.
- Reasons as a field: a slice field grows `Statement` past its 640-byte size
  class (`TestStatementSize`) and a Rewrite hook could leave it stale.

## Consequences

- A raw condition can leak what its text reads. The reason is required, and
  hooks see it, so an audit hook records "this read used a raw condition
  because <reason>".
- The text is SQL of one dialect when it uses a dialect's cast or function;
  MySQL's `CAST(x AS CHAR)` may not compare with a column of another
  collation (error 1267), where `CONCAT(x)` does, as velox's text keys do.

## Evidence

`internal/apitest/exprraw_test.go`: `TestExprRawKeepsOuterPolicy`,
`TestExprRawQualifies` (dialects, a self-join, `HasWith`'s `vx1`, Or and
Not, untyped), `TestExprRawWrites`, `TestExprRawHaving`,
`TestExprRawRefused`, `TestExprRawReasons` (an Around hook),
`TestExprRawRenderCache`. `internal/integration/exprraw_test.go`:
`TestExprRaw`, cases 1 and 3 on SQLite, and on PostgreSQL and MySQL when
their variables are set.
