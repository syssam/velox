# Writing SQL in velox

The handles say most of what an application needs. When they do not, velox
takes SQL text in nine places. Five of them keep the entity's Read and
Write policies, its scope and its interceptors: the text is one part of a
statement velox builds. One, `ExprRaw`, is a raw condition: the statement
keeps its policies, and no policy applies inside the text. Three of them
are raw: the whole statement is yours, velox applies no policy, and each
takes a reason that says why.

Pick the smallest tool that says what you need. The first five cannot leak
another tenant's rows; `ExprRaw` and the last three can.

| Need | Tool | Policies, scope, interceptors | Entity hooks |
| --- | --- | --- | --- |
| A condition | `velox.Expr` | kept | n/a (a read) or kept (in a write's Where) |
| A value: an ordering, a result column, a group key | `velox.Calc`, `Query.SelectRaw` | kept | n/a |
| A column set to an expression | `velox.SetExpr` | kept | run; `Mutation.Value` reports false |
| A condition on another table | `HasWith`, `InSelect`, `FieldInSelect` | kept, the other table's too | n/a |
| A condition with a subquery velox cannot guard | `velox.ExprRaw(reason, …)` | kept on the statement's rows, **none inside the text** | n/a (a read) or kept (in a write's Where) |
| Grouped results | `Select`, `GroupBy`, `Having` with `Calc` | kept | n/a |
| Rows of an entity, by any SELECT | `db.Raw[E](reason, …)` | **bypassed** | not run |
| Rows of no entity | `db.Rows(ctx, reason, …)` | **bypassed** | not run |
| A statement with no rows | `db.Exec(ctx, reason, …)` | **bypassed** | not run |

Every tool goes through the transaction check, the Around hooks, routing
and the SQL comment. The raw ones show their reason to the Around
hooks as `Statement.PolicyBypass`; `ExprRaw`'s reasons are
`Statement.RawReasons()`.

## The text rules

`Expr`, `Calc`, `SelectRaw` and `SetExpr` take the same text. A `?` is filled
by the next argument:

- a column handle of the entity, written as the column;
- a `velox.Term` of the entity (an aggregate, another `Calc`), written as it is;
- any other value, bound as a parameter.

The text is written in parentheses. It may hold functions, operators, casts,
`CASE`, and plain string literals such as `'$.id'`. It may not name another
table or end the statement. A semicolon, a comment, a backslash, a `$`, an
unbalanced parenthesis, a quote that is not a plain literal, and the words
`SELECT`, `WITH`, `TABLE`, `VALUES`, `UNION`, `INTERSECT`, `EXCEPT`, `INTO`,
`RETURNING`, `PRAGMA` and `ATTACH` are refused before anything is sent. So
are the functions that read a table, a file or server state without a
subquery: names starting `pg_`, `lo_`, `dblink`, `query_to_`, `table_to_`,
`schema_to_` or `database_to_`, the sequence functions, `set_config`,
`current_setting`, MySQL's `LOAD_FILE`, `SLEEP`, `BENCHMARK` and locks, and
SQLite's `load_extension`, `readfile`, `writefile` and `edit`. A
wrong number of arguments, another entity's column, and an encoded (JSON)
column are refused too. Pass values as arguments, never in the text.

These rules guard against a policy bypass by mistake. They are not a
defence against hostile input: the text comes from your program, never from
a user. See [ADR 0069](decisions/0069-expr.md).

`ExprRaw` keeps the rules on structure (the semicolon, comments, backslash,
`$`, `#`, parentheses and literals) and drops the ones on words, so its text
may hold `SELECT`, `EXISTS`, `WITH` and any function, and a closed quoted
identifier (`"order"`, `` `order` ``). Its column handles are written
qualified by the table, or by velox's `vx1`… inside a relation's subquery,
so a correlated subquery can name the outer row. No policy applies inside
its text ([ADR 0155](decisions/0155-expr-raw.md)).

The examples below use `I := veloxgen.Invoices`, with `ctx` carrying the
scope.

## A condition: Expr

```go
db.Query[veloxgen.Invoice]().
	Where(velox.Expr[veloxgen.Invoice]("LOWER(?) = LOWER(?)", I.Customer, name)).
	Limit(20).All(ctx)
```

```sql
SELECT … FROM "invoices" WHERE (LOWER("customer") = LOWER($1)) AND "org_id" = $2 LIMIT 20
```

It is one more condition, ANDed with the policy, the scope and the
interceptors. In an update's or a delete's `Where` it narrows the rows the
write may touch, which the Write policy narrows again.

## A value: Calc and SelectRaw

`Calc` makes a `Term`. A term orders with `Asc` and `Desc`, is a result
column of `Select`, a key of `GroupBy`, and an operand of `Expr`:

```go
db.Query[veloxgen.Invoice]().
	OrderBy(velox.Calc[veloxgen.Invoice]("similarity(?, ?)", I.Customer, q).Desc()).
	Limit(10).All(ctx)
```

```sql
SELECT … FROM "invoices" WHERE "org_id" = $1 ORDER BY (similarity("customer", $2)) DESC LIMIT 10
```

`SelectRaw(text, args...)` is `Select(Calc(text, args...))`:

```go
for row, err := range db.Query[veloxgen.Invoice]().
	SelectRaw("EXTRACT(MONTH FROM ?)", I.CreatedAt).
	Select(I.TotalCents.Sum()).
	GroupBy(velox.Calc[veloxgen.Invoice]("EXTRACT(MONTH FROM ?)", I.CreatedAt)).
	AllowUnbounded().Rows(ctx) {
	…
}
```

A projection is read with `Rows`, by position. An entity query ordered by
an expression cannot be paged with `After` or a cursor. See
[ADR 0074](decisions/0074-projection.md).

A `Calc` whose text aggregates, such as `SUM(?)` or a user's aggregate, is
marked with `velox.Agg`, and velox then places it as an aggregate term. velox
does not read the text to find one (ADR 0151).

A `Calc` or `SelectRaw` may hold a window function: `COUNT(*) OVER ()`,
`ROW_NUMBER() OVER (PARTITION BY ? ORDER BY ?)`, `RANK`, `DENSE_RANK`, and
`SUM`, `AVG`, `MIN`, `MAX` with `OVER`. Beside `GroupBy` and `Limit`,
`COUNT(*) OVER ()` counts every group, so one statement reads a page of a
report and its total:

```go
db.Query[veloxgen.Invoice]().
	Select(I.OrgID, I.TotalCents.Sum(), velox.Calc[veloxgen.Invoice]("COUNT(*) OVER ()")).
	GroupBy(I.OrgID).OrderBy(I.OrgID.Asc()).Limit(20).Offset(40).Rows(ctx)
```

Inside `OVER (...)` the text is checked as the rest is: a subquery and the
refused functions stay refused. A window function is a result column or an
ordering only. velox refuses one in `Expr` (a `Where` or a `Having`), in
`GroupBy`, under an aggregate and in `SetExpr`, as SQL computes windows
after all of those (ADR 0101). MySQL 8 and SQLite 3.25 have windows.

## A column set to an expression: SetExpr

```go
db.Update[veloxgen.Post]().
	Where(P.ID.EQ(id)).
	Set(velox.SetExpr(P.Views, "? + 1", P.Views),
		velox.SetExpr(P.Search, "to_tsvector('english', ? || ' ' || ?)", P.Title, P.Body)).
	Exec(ctx)
```

```sql
UPDATE "posts" SET "views" = ("views" + 1),
  "search" = (to_tsvector('english', "title" || ' ' || "body")),
  "updated_at" = $1 WHERE "id" = $2 AND "org_id" = $3
```

It is a setter beside `Set`, `Add` and `Sub`, in `UpdateQuery.Set`.
`UpdateQuery.One` returns the row with the value the database computed.

velox cannot read the value before the database computes it, so:

- the column's validators do not run on it, as they do not on `Add` and `Sub`;
- a hook's `Mutation.Value` reports false for the column;
- an `Immutable` or `Version` column, an encoded (JSON) column, a primary
  key that `One` must read back, and an insert refuse it.

On MySQL an expression reads the values that earlier assignments of the same
UPDATE wrote, the `updated_at` default included. PostgreSQL and SQLite read
the row as it was. See [ADR 0095](decisions/0095-set-expr.md).

## Another table: HasWith and InSelect

A condition on another table is a subquery that velox writes, so that table's
Read policy and interceptors apply inside it:

```go
db.Query[veloxgen.Invoice]().Where(I.Lines.HasWith(L.AmountCents.GT(10000)))
db.Query[veloxgen.Author]().Where(A.ID.InSelect(P.AuthorID.Select(P.Votes.GT(20))))
```

```sql
… WHERE EXISTS (SELECT 1 FROM "lines" AS "vx1" WHERE "vx1"."invoice_id" = "invoices"."id" AND "amount_cents" > $1) …
```

A schema's Policy names the other entity with `velox.FieldInSelect`. See
[ADR 0057](decisions/0057-column-subqueries.md).

## Raw: Raw, Rows and Exec

When the statement itself is beyond the builders (a CTE, a window function,
a lateral join, a lock the builders do not take), write all of it. velox
then applies no policy and no scope: filter by tenant in the SQL. Each call
takes a reason, which the Around hooks see and an audit log can record.
The SQL uses the dialect's placeholders (`$1` on PostgreSQL, `?` elsewhere).

Rows of an entity, scanned by column name (alias computed columns):

```go
top, err := db.Raw[veloxgen.Invoice]("monthly report",
	`SELECT org_id, SUM(total_cents) AS total_cents FROM invoices
	 WHERE org_id = $1 GROUP BY org_id`, org).All(ctx)
```

Rows of no entity, scanned by position:

```go
for row, err := range db.Rows(ctx, "rank customers",
	`SELECT customer, RANK() OVER (ORDER BY SUM(total_cents) DESC)
	 FROM invoices WHERE org_id = $1 GROUP BY customer`, org) {
	if err != nil {
		return err
	}
	var name string
	var rank int64
	if err := row.Scan(&name, &rank); err != nil {
		return err
	}
}
```

A statement with no rows:

```go
n, err := db.Exec(ctx, "nightly cleanup", "DELETE FROM sessions WHERE expires_at < $1", now)
```

`Raw` counts as a read and goes to a replica; `Exec` counts as a write. In a
transaction both see its writes.

## Coming from ent

| ent | velox |
| --- | --- |
| `Where(sql.FieldEQ…)`, `predicate.X(func(s *sql.Selector){…})` | `velox.Expr` |
| `predicate.X(func(s *sql.Selector){…})` with a subquery on another table | `HasWith` or `InSelect`; `velox.ExprRaw(reason, …)` when no policy may apply inside |
| `Order(func(s *sql.Selector){ s.OrderExpr(…) })` | `velox.Calc(…).Desc()` |
| `Modify(func(u *sql.UpdateBuilder){ u.Set(c, sql.Expr(…)) })` | `velox.SetExpr` |
| `Modify(func(s *sql.Selector){ s.Select(…) })` + `Scan` | `Select` / `SelectRaw` + `Rows` |
| `client.QueryContext` / `ExecContext` | `db.Rows` / `db.Exec`, with a reason |

ent's selector functions see the whole statement, so one can drop a privacy
filter by mistake. velox's text is one guarded part of a statement velox
builds, and the policies stay outside it.
