# A tour of velox

This guide walks through velox one task at a time: describing your data,
generating code, creating tables, and then reading and writing rows. Each
section shows the smallest code that works and links to a runnable example
and to the design decision behind it, for when you want the details.

If you have not run the [quick start](../README.md#quick-start) yet, start
there: it takes an empty directory to a working query in six steps. If you
know ent, keep [coming-from-ent.md](coming-from-ent.md) open beside this page;
it lists every place where velox behaves differently.

The snippets below follow the [tour example](../examples/tour), an invoicing
app with organizations. You can run it with `cd examples && go run ./tour`.

## Contents

- [How velox fits together](#how-velox-fits-together)
- [Defining a schema](#defining-a-schema)
- [Generating code](#generating-code)
- [Migrations](#migrations)
- [Connecting](#connecting)
- [Reading](#reading)
- [Relations](#relations)
- [Aggregates and GROUP BY](#aggregates-and-group-by)
- [Writing](#writing)
- [Transactions](#transactions)
- [Hooks, policies and interceptors](#hooks-policies-and-interceptors)
- [Multi-tenancy with a scope](#multi-tenancy-with-a-scope)
- [Soft delete](#soft-delete)
- [Raw SQL](#raw-sql)
- [Row locks](#row-locks)
- [Deadlines](#deadlines)
- [Errors](#errors)
- [Read replicas and shards](#read-replicas-and-shards)
- [Observability](#observability)
- [GraphQL](#graphql)
- [Where to go next](#where-to-go-next)

## How velox fits together

There are three parts:

1. **A schema package** that you write. Each entity is a Go struct with
   methods such as `Fields` and `Edges`, in the same style as ent.
2. **`velox-gen`**, a code generator. It reads the schema and writes a `veloxgen`
   package: a plain struct per entity (`veloxgen.Invoice`) and a set of typed
   column handles (`veloxgen.Invoices`).
3. **The `velox` runtime**, a small generic API. `db.Query[veloxgen.Invoice]()`,
   `db.Insert(&inv)` and friends work for every entity, so the generated code
   stays small.

Because the handles are typed, `veloxgen.Invoices.TotalCents.GT("ten")` is a
compile error rather than a runtime surprise.

## Defining a schema

A schema is a struct that embeds `velox.Schema` (the same type as
`schema.Schema`; the guide uses the root's spelling, which ent and the old velox
share, for `Schema`, `Field`, `Edge`, `Index`, `Mixin` and `Annotation`). Its
methods describe the table. Here is the tour's `Invoice`:

```go
package veloxschema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

type Invoice struct{ velox.Schema }

func (Invoice) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}, InOrg{}, softdelete.Mixin{}}
}

func (Invoice) Fields() []velox.Field {
	return []velox.Field{
		field.String("number"),
		field.String("customer"),
		field.Enum("status").Values("draft", "sent", "paid"),
		field.Int64("total_cents"),
	}
}

func (Invoice) Edges() []velox.Edge {
	return []velox.Edge{edge.To("lines", Line.Type)}
}

func (Invoice) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("org_id", "number").Unique().Where("deleted_at IS NULL"),
	}
}
```

### Fields

Every entity gets an `id` column, an `int64`, unless you declare your own
`id` field (a UUID or a string works too; see
[ADR 0056](decisions/0056-scale-round.md)).

The `field` package has a builder per type: `String`, `Text`, `Bool`,
`Int`, `Int8` to `Int64`, `Uint` to `Uint64`, `Float`, `Float32`, `Time`,
`Bytes`, `UUID`, `JSON` (plus the shortcuts `Strings`, `Ints` and `Floats`),
`Enum` and `Other`. Builders take options such as `Unique()`, `Immutable()`,
`Default(...)`, `Sensitive()` and `Comment(...)`.

Two options work differently from ent, so they are worth learning early
([ADR 0003](decisions/0003-nullability.md)):

- `Optional()` means "may be left out on insert". The column stays
  `NOT NULL`, so an `Optional` field needs a `Default` or `Nillable`:
  `Optional().Default("")` is an empty string. velox stores no zero value
  in their place ([ADR 0063](decisions/0063-optional-needs-a-value.md)).
- `Nillable()` makes the column nullable. The Go field becomes a pointer, and
  its handle gains `IsNull`, `NotNull` and `SetNull`.

An insert takes a struct, so a field's zero value and "not set" are the same
thing. That decides what a `Default` can do:

| Field | Unset (zero) on insert stores | Can store the zero value? |
| --- | --- | --- |
| `field.Bool("active")` | `false` | yes |
| `field.Bool("active").Default(true)` | refused by velox-gen: `false` could not be stored | — |
| `field.Bool("active").Nillable().Default(true)` | `true`, when the `*bool` is nil | yes: `new(false)` |
| `field.Int("seats").Default(0)` | `0` | yes |
| `field.Int("seats").Default(100)` | refused by velox-gen: `0` could not be stored | — |
| `field.Int("seats").Positive().Default(100)` | `100` | `0` is refused by `Positive` anyway |
| `field.String("plan").Default("free")` | `"free"` | no: `""` becomes `"free"` |
| `field.String("plan").Optional().Nillable()` | `NULL` | yes |

For a bool that should default to `true`, use `Nillable().Default(true)`,
or name the field so that its zero is the default (`disabled`, not
`enabled`). `SchemaDefault` sets only the column's `DEFAULT`, for rows written
outside velox, and `Annotations(sqlschema.Default("100"))` is the same with one SQL text for
every dialect (`sqlschema.DefaultExprs` takes one for each). [Coming from ent](coming-from-ent.md) ("Defaults on a NOT NULL column")
has the whole list.

`field.Enum("status").Values(...)` generates a Go type, `veloxgen.InvoiceStatus`,
with a constant per value, such as `veloxgen.InvoiceStatusPaid`.

### Edges

Relations are declared with `edge.To` on one side and `edge.From(...).Ref(...)`
on the other, as in ent. The side that holds the foreign key names it with
`Field`:

```go
func (Line) Fields() []velox.Field {
	return []velox.Field{
		field.Int64("invoice_id"),
		field.String("description"),
		field.Int64("amount_cents"),
	}
}

func (Line) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("invoice", Invoice.Type).Ref("lines").Unique().Required().Field("invoice_id"),
	}
}
```

- `To` with an inverse that is `Unique` is one-to-many.
- `To` with an inverse that is not `Unique` is many-to-many. velox-gen writes
  a join entity for it (in the relations example, `veloxgen.PostTag` for the
  `post_tags` table).
- `OnDelete(edge.Cascade)` asks the database to delete children with their
  parent.

The [relations example](../examples/relations) shows all three shapes.

### Indexes

`index.Fields("a", "b")` makes an index, `.Unique()` makes it unique, and
`.Where("...")` makes it partial on PostgreSQL and SQLite. Options only a SQL
database reads are annotations of package `dialect/sqlschema`, as in the old
velox, so that the same schema can feed GraphQL and other targets. On PostgreSQL, `sqlschema.IndexType("GIN")` picks the
access method (hash, gin, gist, spgist or brin; btree is the default), and a GIN
index is the one that may cover a JSON field:

```go
index.Fields("attrs").Annotations(sqlschema.IndexType("GIN")).StorageKey("ix_attrs_gin")
```

MySQL and SQLite have no choice of method, so velox-gen refuses an `IndexType`
on them, and a unique index must be btree
([ADR 0070](decisions/0070-index-method.md)). A GIN index works on a JSON (jsonb)
column, an array or a tsvector as it is. On a text or number column it needs an
extension that gives GIN an operator class for the type, `btree_gin` or
`pg_trgm`: velox does not create extensions, so add `CREATE EXTENSION` to the
baseline by hand, and without it the index fails when it is applied, not in
velox-gen.

Also on PostgreSQL, `sqlschema.DescColumns("created_at")` (or `Desc()` for every
key) sorts a key descending, which an `ORDER BY name, created_at DESC` needs (a
single column is read backwards as it is), and `sqlschema.OpClassColumn("title",
"gin_trgm_ops")` (or `OpClass(...)` for every key) sets a key's operator class:

```go
index.Fields("title").Annotations(sqlschema.IndexType("GIN"), sqlschema.OpClass("gin_trgm_ops"))
index.Fields("name", "created_at").Annotations(sqlschema.DescColumns("created_at"))
```

`sqlschema.IndexWhere` is `.Where` as an annotation, for a port; give an index one
of them. A schema may also be named the old velox's way, `sqlschema.Annotation{Table:
"users"}` or `sqlschema.Table("users")`, which is `schema.Table`. The old velox's
`StorageParams`, `Prefix`, `IncludeColumns`, `Size`, `ColumnType` and `OnDelete`
compile and are refused by velox-gen, naming velox's own spelling.

Name only an operator class that is not its type's default: PostgreSQL reads a
default one back as none, and the schema would differ from the database for
ever ([ADR 0072](decisions/0072-index-desc-opclass.md)). `Desc` is a btree
index's. An entity's primary key is always
one column; for a natural key, keep `id` and add a unique index.

### Mixins

A mixin is a reusable piece of schema: fields, indexes, and behaviour such as
hooks and policies. velox ships two:

- `mixin.Time{}` adds `created_at` and `updated_at`, kept up to date for you.
- `softdelete.Mixin{}` adds `deleted_at` and turns deletes into updates (see
  [Soft delete](#soft-delete)).

You write your own by embedding `mixin.Schema`. The tour's `InOrg` mixin,
covered under [multi-tenancy](#multi-tenancy-with-a-scope), is a good model.

## Generating code

Add the generator to your module once, then run it after every schema
change:

```sh
go get -tool github.com/syssam/velox/cmd/velox-gen
go tool velox-gen ./veloxschema
```

This writes the `veloxgen` package next to `veloxschema`. Everything in `veloxgen/` is
generated, so never edit it. Commit it, and in CI check that it is up to
date:

```sh
go tool velox-gen -check ./veloxschema   # writes nothing; exits 3 when veloxgen is stale
go tool velox-gen verify ./veloxschema   # builds every entity and reports mistakes
```

The folders are `./veloxschema` and `./veloxgen` unless you say otherwise:
`-out ./path` names the folder of the generated package and `-package name`
its name, which is the folder's base name when you leave it out. Put the
command in one `go:generate` line, and have CI call it, so the folder is named
once.

Generated code carries a version constant (today
`velox.GeneratedCodeIsVersion22`). If you update velox and the shape of
generated code has changed, the old `veloxgen` stops compiling until you run
velox-gen again. [stability.md](stability.md) explains the rule.

## Migrations

velox plans migrations itself by comparing your schema with the last
snapshot in the migrations directory ([ADR 0006](decisions/0006-own-migrate-and-atlas.md)).

```sh
go tool velox-gen migrate diff -dialect postgres   # first diff names the dialect
go tool velox-gen migrate apply -db "$DATABASE_URL" # or set $VELOX_DB
go tool velox-gen migrate status -db "$DATABASE_URL"
go tool velox-gen migrate drift -db "$DATABASE_URL"
```

`diff` writes the next numbered `.sql` file into `./migrations`. Read it and
commit it like any other code. `diff` will not plan a destructive change,
such as dropping a table or a column, until you allow it with `-allow`, for
example `-allow drop-column=users.nickname`. `apply` also checks the database
before a change that could lose data the schema does not describe
([ADR 0026](decisions/0026-migration-safety.md)).
There are no down migrations: to undo a change, change the schema back and
diff forward.

You can also apply migrations from your program, which is handy in tests and
small services. Embed the directory and call `migrate.Up` on the pool:

```go
//go:embed migrations
var migrations embed.FS

sub, err := fs.Sub(migrations, "migrations")
if err != nil {
	return err
}
if _, err := migrate.Up(ctx, velox.Pool(db), sub); err != nil {
	return err
}
```

**Using Atlas instead?** `go tool velox-gen migrate sql -dialect postgres
./veloxschema` prints the schema's `CREATE` statements for Atlas's
`external_schema`. A directory is managed by one tool or the other, never
both. [examples/README.md](../examples/README.md#migrations) has a complete
`atlas.hcl`, and the steps to adopt an existing database (made by ent or by
hand) with `migrate baseline`.

When `migrate apply` refuses something,
[migrations-operations.md](migrations-operations.md) explains what to do.

## Connecting

`velox.Connect` opens a pool from a `database/sql` driver name and a DSN.
Import the driver yourself:

```go
import (
	"github.com/syssam/velox"
	_ "modernc.org/sqlite"
)

db, err := velox.Connect("sqlite", "app.db")
if err != nil {
	return err
}
defer db.Close()
```

The accepted driver names are `"postgres"`, `"pgx"`, `"mysql"`, `"sqlite"`
and `"sqlite3"`. A few things to know:

- **MySQL** DSNs must set `parseTime=true` and `clientFoundRows=true`.
  `Connect` returns an error otherwise.
- **SQLite** gets a pool of one connection, and `Connect` turns on foreign
  keys. Inside a `Tx` or an `Iter` loop, use the transaction's DB or finish
  the loop first: the one connection is busy.
- **Your own pool:** open a `*sql.DB` yourself and wrap it with
  `velox.Open(velox.Postgres, sqlDB)`. Closing it stays your job.
- **PostgreSQL with pgx:** `veloxpgx.Open(pool)` takes a `*pgxpool.Pool` and
  is faster than going through `database/sql`
  ([ADR 0019](decisions/0019-veloxpgx.md)).

```go
pool, err := pgxpool.New(ctx, "postgres://localhost/app")
if err != nil {
	return err
}
defer pool.Close()
db := veloxpgx.Open(pool)
```

`velox.Pool(db)` returns the `*sql.DB` under a DB made by `Connect` or
`Open`, for pool settings and for `migrate.Up`. It is nil for a pgx DB, a
transaction's DB and a sharded DB.

Options go at the end of `Connect`, `Open` or `veloxpgx.Open`, such as
`velox.WithDeadlines(...)`, `velox.WithMaxRows(n)` or
`velox.WithHooks[veloxgen.Task](...)`.

## Reading

A query is a value. You build it with `Where`, `OrderBy` and `Limit`, then
run it with a terminal method. The handles live in the plural variable
(`veloxgen.Invoices`), which is often shortened to one letter:

```go
I := veloxgen.Invoices

unpaid, err := db.Query[veloxgen.Invoice]().
	Where(I.Status.NEQ(veloxgen.InvoiceStatusPaid)).
	OrderBy(I.Number.Asc()).
	Limit(10).
	All(ctx)
```

The terminal methods:

| Method | Returns |
|---|---|
| `All(ctx)` | every matching row, as `[]veloxgen.Invoice` |
| `One(ctx)` | exactly one row; `ErrNotFound` for none, `ErrNotSingular` for several |
| `First(ctx)` | the first row in the query's order |
| `Count(ctx)` | the number of matching rows |
| `Exists(ctx)` | whether any row matches (`SELECT 1 … LIMIT 1`) |
| `Iter(ctx)` | an `iter.Seq2` that holds one row at a time |
| `SQL(ctx)` | the statement and arguments it would send, without sending them |

Conditions passed to one `Where` are ANDed. Combine them with `velox.Or`,
`velox.And` and `velox.Not`. String handles add `HasPrefix`, `Contains`,
`EqualFold`, `ContainsFold` and `Like`; ordered handles add `GT`, `Between`, `In` and the rest.

### Every read needs a Limit

A query that reads rows must say how many it may read. Without `Limit`,
`All`, `Iter` and the other row readers fail with `velox.ErrUnbounded`
before anything is sent. When you really do want every row, say so with
`AllowUnbounded()`:

```go
for x, err := range db.Query[veloxgen.Invoice]().OrderBy(I.ID.Asc()).AllowUnbounded().Iter(ctx) {
	if err != nil {
		return err
	}
	fmt.Println(x.Number)
}
```

`Count` and `Exists` need no `Limit`, as they return one value. A DB can also
cap every read with `velox.WithMaxLimit` and `velox.WithMaxRows`.

### Choosing columns

`Columns(I.ID, I.Number)` reads only the named columns, and `Omit(...)` reads
all but the named ones. The other fields keep their zero values.

### Pagination

`Offset(n)` works, but keyset pagination is better for large tables.
`After(&lastRow)` continues after the previous page's last row. The
`OrderBy` must end in the primary key or a unique column:

```go
P := veloxgen.Posts
page := db.Query[veloxgen.Post]().OrderBy(P.Votes.Desc(), P.ID.Asc()).Limit(20)
for {
	got, err := page.All(ctx)
	if err != nil || len(got) == 0 {
		return err
	}
	// ... use got ...
	page = page.After(&got[len(got)-1])
}
```

## Relations

### Eager loading with With

velox never loads a relation behind your back. `With` loads it in one extra
query for all rows at once (`WHERE invoice_id IN (...)`), and the result is in
the entity's `Edges` field:

```go
first, err := db.Query[veloxgen.Invoice]().
	Where(I.Number.EQ("INV-001")).
	With(I.Lines.OrderBy(veloxgen.Lines.AmountCents.Desc())).
	One(ctx)
if err != nil {
	return err
}
lines, loaded := first.Edges.Lines.Get() // loaded is false when With was not used
```

A relation handle takes its own `Where`, `OrderBy` and nested `With`:

```go
A, P := veloxgen.Authors, veloxgen.Posts
authors, err := db.Query[veloxgen.Author]().
	With(A.Posts.Where(P.Votes.GTE(10)).OrderBy(P.Votes.Desc()).With(P.Comments)).
	Limit(10).
	All(ctx)
```

`LimitEach(n)` loads at most n related rows per owner, in one statement:
`With(A.Posts.OrderBy(P.Votes.Desc()).LimitEach(3))` gives each author their
three best posts.

### Filtering by a relation: Has and HasWith

`Has` and `HasWith` are `EXISTS` subqueries:

```go
db.Query[veloxgen.Author]().Where(A.Posts.Has())                     // authors with posts
db.Query[veloxgen.Author]().Where(A.Posts.HasWith(P.Votes.GT(20)))   // ... with a popular post
db.Query[veloxgen.Author]().Where(velox.Not(A.Posts.Has()))          // authors with none
```

### Inside a JSON column: Text, Number, Bool and HasKey

A JSON field's handle can test a value inside the document, at a path of
object keys. Here `Account` stands for any entity with a
`field.JSON("attrs", map[string]any{})`:

```go
A := veloxgen.Accounts
db.Query[veloxgen.Account]().Where(A.Attrs.Text("plan", "tier").EQ("pro"))  // a string
db.Query[veloxgen.Account]().Where(A.Attrs.Number("plan", "seats").GT(10))  // a number
db.Query[veloxgen.Account]().Where(A.Attrs.Bool("trial").EQ(true))          // a boolean
db.Query[veloxgen.Account]().Where(A.Attrs.HasKey("trial"))                 // a key is there
```

`Text` is NULL where the path is missing or holds something other than a
string, so the number `5` is never the string `"5"`, and strings compare with
case. `Number` and `Bool` work the same way for numbers and booleans. The results are the same on
PostgreSQL, MySQL and SQLite: velox writes each dialect's JSON functions and
passes the keys as arguments. A key can't be empty or contain `"`, `\` or a
control character. See [ADR 0058](decisions/0058-json-paths.md).

### Inside a JSON array: Has, HasSome, HasEvery and IsEmpty

A column that is itself an array (`field.Strings("tags")`, or any
`field.JSON` of a slice) tests its elements:

```go
U := veloxgen.Users
db.Query[veloxgen.User]().Where(U.Tags.Has("admin"))                  // holds "admin"
db.Query[veloxgen.User]().Where(U.Tags.HasSome("admin", "staff"))     // holds one of them
db.Query[veloxgen.User]().Where(U.Tags.HasEvery("admin", "staff"))    // holds both
db.Query[veloxgen.User]().Where(U.Tags.IsEmpty())                     // is []
```

An element is a string, a number or a boolean. `true` is not `1` and `"1"` is
not `1`, and a document that is not an array holds nothing and is not empty;
a NULL column matches neither the condition nor its `Not`. It is the same on
PostgreSQL, MySQL and SQLite. On PostgreSQL the column must be jsonb, and a GIN
index serves `Has` and `HasEvery`. velox-graphql offers them as
`tagsHas`, `tagsHasSome`, `tagsHasEvery` and `tagsIsEmpty` for `WhereOps()` or
`WhereOps(OpsJSONArray)` on a slice of strings, numbers or booleans. See
[ADR 0080](decisions/0080-json-array-elements.md).

### Subqueries on a column: Select and InSelect

When no edge links the two columns, or you want to say which columns are
compared, a column handle's `Select` makes a subquery of that column. Another
handle of the same Go type tests against it with `InSelect` or `NotInSelect`:

```go
popular := P.AuthorID.Select(P.Votes.GT(20))           // SELECT author_id FROM posts WHERE votes > 20
db.Query[veloxgen.Author]().Where(A.ID.InSelect(popular))     // authors of a popular post
db.Query[veloxgen.Author]().Where(A.ID.NotInSelect(popular))  // authors of none
```

The types must match: an `int64` column against an `int64` subquery, an enum
against the same enum. velox writes it as the same `EXISTS` that `HasWith`
uses, so the subquery's entity gets its own Read policy and interceptors (a
soft-deleted post is not counted). `NotInSelect` is `NOT EXISTS`, so a NULL
in the subquery doesn't empty the result the way SQL's `NOT IN` would.
Aggregates, CTEs and window functions in a subquery still go through `Raw`.
See [ADR 0057](decisions/0057-column-subqueries.md).

A schema's Policy, Hooks or Interceptors cannot use handles, which live in the
generated package they are compiled into. They name the other entity instead,
with `velox.FieldInSelect(column, entity, key, conditions...)`: this row's
`column` is among the `key` values of the `entity` rows that meet `conditions`.

```go
func (Doc) Policy() velox.Policy {
	return velox.Policy{Read: func(ctx context.Context) (velox.Predicate, error) {
		// a document is visible when its org_id is that of an organisation the viewer reads
		return velox.FieldInSelect("org_id", "Org", "id", velox.FieldEQ("active", true)), nil
	}}
}
```

It is the same `EXISTS`, so `Org`'s own Read policy applies inside it, and it
is resolved when the statement is prepared: an unknown column or entity, or
two columns of different types, is an error and never a dropped condition.
`entity` is the entity's name in the schema or its table. velox finds it, and
the target of a `HasEdge`, in the generated package's entity table when the
statement runs, so a rule in a shared package can take its names from
configuration. velox-gen checks every name it can read as a constant and warns
about the others. Prefer `HasEdgeWith` where a relation exists: velox-gen then
checks the whole rule ([ADR 0079](decisions/0079-field-in-select.md),
[ADR 0081](decisions/0081-entity-table.md)).

### SQL text in a condition: Expr

For a condition the handles do not say, such as a function or an expression,
`Expr` takes SQL text. A `?` is filled by the next argument: a column handle,
which is written as the column, or a value, which is bound:

```go
db.Query[veloxgen.Invoice]().Where(
	velox.Expr[veloxgen.Invoice]("LOWER(?) = LOWER(?)", I.Customer, name))
db.Query[veloxgen.Invoice]().Where(
	velox.Expr[veloxgen.Invoice]("EXTRACT(YEAR FROM ?) = ?", I.CreatedAt, 2026))
```

It is one more condition in `Where`, so the Read policy, the scope and the
interceptors still apply, which `Raw` does not give. To keep that true, text
with a semicolon, a comment, a backslash, a `$`, an unbalanced parenthesis, a
quote that is not a plain string literal (`''`, `'_'`, `'[]'` are plain; a
backslash, a doubled quote or a `?` inside is not), or a word that starts a
subquery (`SELECT`, `WITH`, `TABLE`, `VALUES`, `UNION`) is refused before
anything is sent. Pass any other value as an argument. Write a condition on
another table with `HasWith` or `InSelect`, which apply that table's
policies. The check guards against a mistake, not
against hostile text; text comes from your program, not from a user
([ADR 0069](decisions/0069-expr.md)).

### Joins

For a to-one relation (a belongs-to, or a one-to-one), `Join` reads the
related row in the same statement and fills the edge. `rel.Is(...)` filters
on the joined row's columns, and `rel.By(...)` orders by them:

```go
posts, err := db.Query[veloxgen.Post]().
	Join(P.Author).
	Where(P.Author.Is(A.Name.EQ("Grace"))).
	OrderBy(P.Author.By(A.Name.Asc()), P.ID.Asc()).
	Limit(20).
	All(ctx)
```

`LeftJoin` keeps rows that have no related row. A to-many relation cannot be
joined, because it would repeat the owner; use `With` or `HasWith` for those
([ADR 0031](decisions/0031-to-one-joins.md)).

### Linking rows

A foreign key is an ordinary field, so a belongs-to link is set like any
other column (`Line{InvoiceID: inv.ID}`). For many-to-many edges and edges
whose key lives on the other table, relation handles provide setters for
`Insert` and `Update`:

```go
db.Update[veloxgen.Post]().Set(veloxgen.Posts.Tags.Add(1, 2)).Where(veloxgen.Posts.ID.EQ(id)).Exec(ctx)
```

`Add`, `Remove` and `Clear` are written after the row, in one transaction
([ADR 0044](decisions/0044-edge-setters.md)). `Set(key)` sets an edge whose
key is a column of the owner's own row. You can also insert
and delete the join entity's rows directly, as the
[relations example](../examples/relations) does.

## Aggregates and GROUP BY

`Sum`, `Avg`, `Min` and `Max` take a query and a number column, and ask the
database for one value:

```go
paid, err := velox.Sum(ctx,
	db.Query[veloxgen.Invoice]().Where(I.Status.EQ(veloxgen.InvoiceStatusPaid)),
	I.TotalCents)
```

`Avg`, `Min` and `Max` also return a `bool` that is false when no row matched.
Nillable columns use `SumNull`, `AvgNull` and so on.

`CountBy`, `SumBy`, `AvgBy`, `MinBy` and `MaxBy` group by one key column,
made with the handle's `Key()` method. Groups can be as many as the rows, so
the query says `AllowUnbounded()`:

```go
totals, err := velox.SumBy(ctx,
	db.Query[veloxgen.Invoice]().AllowUnbounded(),
	I.OrgID.Key(), I.TotalCents)
for _, g := range totals {
	fmt.Println(g.Key, g.Value)
}
```

### Reports: Select, GroupBy and Having

For several keys, several aggregates, `HAVING`, `COUNT(DISTINCT x)` or an
ordering by an aggregate or an expression, `Select` reads **terms** in place
of the entity. A term is a column handle as it is, SQL text over columns
(`velox.Calc`, or `SelectRaw` for one expression) or an aggregate: a handle's
`Sum`, `Avg`, `Min`, `Max`, `Count` and `CountDistinct`, or `velox.CountAll`.
The same term is a result column, a group key, an operand in `Expr` and an
ordering:

```go
for r, err := range db.Query[veloxgen.Invoice]().
	Where(I.Status.EQ(veloxgen.InvoiceStatusPaid)).
	Select(I.OrgID, I.TotalCents.Sum(), velox.CountAll[veloxgen.Invoice]()).
	GroupBy(I.OrgID).
	Having(velox.Expr[veloxgen.Invoice]("? > ?", I.TotalCents.Sum(), 100000)).
	OrderBy(I.TotalCents.Sum().Desc()).
	Limit(20).
	Rows(ctx) {
	if err != nil {
		return err
	}
	var orgID, sum, n int64
	if err := r.Scan(&orgID, &sum, &n); err != nil {
		return err
	}
}

// One expression of SQL text:
db.Query[veloxgen.Invoice]().Limit(10).
	SelectRaw("EXTRACT(MONTH FROM ?)", I.CreatedAt).Rows(ctx)
```

`Rows` reads by position: the caller scans each row. It is a read of the
entity, so `Where`, the Read policy, the scope and the interceptors apply and
the groups count only rows the caller may see, which `Raw` does not give.
`SelectRaw` and `Calc` take guarded text like `Expr`, so they keep the
policies too. A projection needs a `Limit` or `AllowUnbounded()`. It cannot be
read with `All`, `Count` and the rest, nor combined with `Columns`, `With`,
`Join`, `After` or a lock, and an aggregate of an aggregate is refused. A
value that is not a column handle or a term of the entity (a string, another
entity's handle) fails the statement before anything is sent. An ordering by
an expression cannot be a keyset cursor. `UNION`, CTEs and window functions
are not in the builder; use [raw SQL](#raw-sql) for those
([ADR 0074](decisions/0074-projection.md), [ADR 0022](decisions/0022-group-by.md)).

## Writing

### Insert

`Insert` writes one entity and reads the generated key back into it.
`InsertAll` writes many in one statement and reads nothing back:

```go
inv := veloxgen.Invoice{Number: "INV-001", Customer: "Initech", Status: veloxgen.InvoiceStatusSent}
if err := db.Insert(&inv).Exec(ctx); err != nil {
	return err
}
// inv.ID is set now.

lines := []veloxgen.Line{
	{InvoiceID: inv.ID, Description: "Consulting", AmountCents: 120000},
	{InvoiceID: inv.ID, Description: "Travel", AmountCents: 30000},
}
err := db.InsertAll(lines).Exec(ctx)
```

Both fill defaults and run hooks and policies first.

### Upsert

`OnConflict` names the conflicting columns, followed by `Update(cols...)` or
`DoNothing()`:

```go
err := db.Insert(&inv).
	OnConflict(I.OrgID, I.Number).
	Update(I.TotalCents).
	Exec(ctx)
```

The update branch writes only the columns you name; `updated_at` and other
`UpdateDefault` columns do not fire there
([ADR 0017](decisions/0017-upsert-skips-updatedefault.md)).

### Update and Delete

Updates are set-based: you say which columns change and which rows match.
`Exec` returns the number of rows affected:

```go
n, err := db.Update[veloxgen.Invoice]().
	Set(I.Status.Set(veloxgen.InvoiceStatusPaid)).
	Where(I.Number.EQ("INV-001")).
	Exec(ctx)

n, err = db.Delete[veloxgen.Line]().Where(veloxgen.Lines.AmountCents.EQ(0)).Exec(ctx)
```

Number handles also have `Add` and `Sub` (`Set(I.TotalCents.Add(500))`).
`UpdateQuery.One(ctx)` updates exactly one row and returns it, read back by
its key ([ADR 0048](decisions/0048-update-one.md)).

### A write needs a Where

An `Update` or `Delete` without `Where` fails with `velox.ErrNoWhere`. If you
mean every row, say `.All()`:

```go
db.Delete[veloxgen.Line]().All().Exec(ctx)
```

### Save

`Save` writes the named columns of an entity you already hold, by its primary
key, and fails with `ErrNotFound` when the row is gone. There is no
"save the whole struct": you list the columns you changed.

```go
draft.Customer = "Umbrella Corp"
err := db.Save(draft, I.Customer).Exec(ctx)
```

Values that hooks set and `updated_at` are written back into the struct.

### Optimistic locking

Mark one integer field as the version:

```go
field.Int64("version").Version()
```

From then on, every update adds 1 to it, and `Save` only writes when the row
still holds the version you read. If another writer got there first, `Save`
fails with `velox.ErrStale` (which also matches `ErrNotFound`) and changes
nothing. Writes may not assign the version themselves; that fails with
`velox.ErrVersionSet` ([ADR 0056](decisions/0056-scale-round.md)).

For a `Doc` entity with that field, `D := veloxgen.Docs`:

```go
if err := db.Save(&doc, D.Title).Exec(ctx); errors.Is(err, velox.ErrStale) {
	// someone else saved first: reload and retry, or tell the user
}
```

## Transactions

`db.Tx` runs a function in a transaction. Returning nil commits; returning an
error rolls back and `Tx` returns that error. With the
[tx example](../examples/tx)'s `Account` entity, `A := veloxgen.Accounts`:

```go
err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
	if _, err := tx.Update[veloxgen.Account]().Set(A.Balance.Sub(30)).Where(A.Name.EQ("alice")).Exec(ctx); err != nil {
		return err
	}
	_, err := tx.Update[veloxgen.Account]().Set(A.Balance.Add(30)).Where(A.Name.EQ("bob")).Exec(ctx)
	return err
})
```

Inside the function, use `tx` and the `ctx` you were given. A statement on
the outer `db` under that context fails with `velox.ErrOutsideTx`, which
catches a common bug.

A nested `tx.Tx(...)` is a savepoint: its error undoes only its own work,
and the outer transaction goes on. This is how you recover from a failed
insert on PostgreSQL, where a failed statement otherwise aborts the whole
transaction.

Options go after the function:

- `velox.TxRetrySerializable(n)` reruns the function after a serialization
  failure or deadlock, up to n more times. The function must be safe to run
  again.
- `velox.TxIsolation(level)`, `velox.TxReadOnly()` and
  `velox.TxTimeout(d)`.

### A transaction you began

Code that already holds a `*sql.Tx`, with raw SQL beside velox calls, runs
the velox calls on it with `velox.UseTx`:

```go
tx, _ := velox.Pool(db).BeginTx(ctx, nil)
defer tx.Rollback()
vtx, err := velox.UseTx(db, tx)
if err != nil {
	return err
}
if _, err := tx.ExecContext(ctx, "UPDATE accounts SET ..."); err != nil {
	return err
}
if err := vtx.Insert(&entry).Exec(ctx); err != nil {
	return err
}
return tx.Commit()
```

The transaction stays yours: `vtx` never begins, commits or rolls back, and
its statements fail with database/sql's error once you end `tx`. A `vtx.Tx`
is a savepoint. It retries nothing, takes no option, and runs no `TxAround`
hook, which wraps a `Tx` call that this is not. `UseTx` refuses a sharded DB,
a DB that is in a transaction already, and `veloxpgx`, whose connection runs
on pgx's transactions ([ADR 0066](decisions/0066-use-tx.md)).

See the [tx example](../examples/tx).

## Hooks, policies and interceptors

An entity's behaviour is declared in the schema package, next to its fields,
as in ent. The schema package does not import `veloxgen` (gen imports it), so
behaviour names columns by their schema names: `velox.FieldEQ("status", s)`,
`m.Value("title")`. velox-gen checks those names when it generates, and velox
checks them again when the statement runs
([ADR 0040](decisions/0040-schema-value-and-ent-names.md)).

### Hooks

A hook wraps an entity's writes. `velox.Before` runs a function before the
write, for the operations it names:

```go
func (Task) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(deriveSlug, velox.OpInsert),
		velox.Before(renamed, velox.OpUpdate),
	}
}

func deriveSlug(_ context.Context, m *velox.Mutation) error {
	for r := range m.Rows() {
		title, _ := r.Value("title").(string)
		if title == "" {
			return errors.New("a task needs a title")
		}
		if err := r.Set("slug", slug(title)); err != nil {
			return err
		}
	}
	return nil
}
```

On an update, `m.Value(name)` tells you whether the update assigns a column
and `m.Set(name, v)` adds one. On an update or delete, `m.Where(...)` narrows
the rows. `velox.On(hook, ops...)` wraps the whole write, for work after it.

A hook that needs generated types, such as one that inserts a `veloxgen.Audit`
row, is added at run time with `velox.WithHooks[veloxgen.Task](...)` when opening
the DB. Inside it, `m.DB()` is the DB the write runs on, so inside `db.Tx`
the audit row commits or rolls back with the write. See the
[hooks example](../examples/hooks).

### Policies

A policy decides what the caller may see and change, from the context. It
has three parts:

- `Read` returns a condition ANDed into every statement that reads the
  entity: queries, counts, `With` loads, `Has`, updates and deletes.
- `Write` returns a condition ANDed into updates, deletes and `Save`.
- `Insert` checks each new row.

```go
func (Invoice) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Predicate, error) {
			switch role.FromContext(ctx) {
			case role.Accountant:
				return velox.Allow(), nil
			case role.Viewer:
				return velox.FieldNEQ("status", "draft"), nil
			}
			return velox.Predicate{}, velox.Deny("no billing role")
		},
		Write: func(ctx context.Context) (velox.Predicate, error) {
			if err := role.RequireAccountant(ctx); err != nil {
				return velox.Predicate{}, err
			}
			return velox.Allow(), nil
		},
		Insert: func(ctx context.Context, _ velox.Row) error { return role.RequireAccountant(ctx) },
	}
}
```

Policies fail closed. Returning the zero `velox.Predicate{}` fails the
statement instead of matching everything, and `velox.Deny(reason)` returns
an error that matches `velox.ErrDenied`. A mixin's policy is ANDed before the
entity's own, so an entity cannot widen what its mixin allows.

One behaviour is worth knowing in advance: a `Write` condition filters rather
than errors. An update aimed only at rows the caller may not change affects 0
rows, as with SQL row-level security
([ADR 0008](decisions/0008-write-policies-filter.md)).
Check the count. Where 0 must say "forbidden" or "not found", ask past the
policies with the same condition; interceptors such as soft delete still
apply, so a deleted row stays not found:

```go
n, err := db.Update[veloxgen.Invoice]().Set(I.Status.Set(veloxgen.InvoiceStatusPaid)).
	Where(I.ID.EQ(id)).Exec(ctx)
if err == nil && n == 0 {
	hidden, err := db.WithoutPolicy("tell forbidden from missing").
		Query[veloxgen.Invoice]().Where(I.ID.EQ(id)).Exists(ctx)
	switch {
	case err != nil:
		return err
	case hidden:
		return errForbidden // your own error
	}
	return velox.ErrNotFound
}
```

The second query tells the caller that a row it may not change exists, so
use it only where that is not a secret.

`db.WithoutPolicy("monthly revenue report")` returns a DB that skips every
policy, for admin jobs and reports. It needs a reason, which hooks can see,
and it only affects code that is handed that DB; a context carries no bypass
([ADR 0054](decisions/0054-fail-closed-runtime.md)). See the
[policy example](../examples/policy).

### Interceptors

An interceptor is a filter that applies to every statement on an entity,
like a `Read` policy, but `WithoutPolicy` does not lift it. It is meant for
application rules that even admin code should respect, such as hiding
soft-deleted rows. Declare `Interceptors() []velox.Interceptor` on a schema
or mixin; each has a `Filter func(ctx) (velox.Predicate, error)`.

## Multi-tenancy with a scope

velox has no built-in tenant. Instead, the `scope` package gives you a
typed context key, and a mixin you write uses it in a policy and a hook
([ADR 0029](decisions/0029-scope-replaces-tenant.md),
[ADR 0038](decisions/0038-tenancy-is-a-mixin.md)). This is the tour's
`InOrg` mixin in full:

```go
var OrgKey = scope.NewKey[int64]("org")

type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []velox.Field {
	return []velox.Field{field.Int64("org_id").Immutable()}
}

func (InOrg) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Predicate, error) {
			org, err := OrgKey.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ("org_id", org), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error {
			return OrgKey.Match(ctx, r.Value("org_id"))
		},
	}
}

func (InOrg) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(func(ctx context.Context, m *velox.Mutation) error {
			if org, ok := OrgKey.From(ctx); ok {
				return m.Fill("org_id", org)
			}
			return nil
		}, velox.OpInsert),
	}
}
```

List it in an entity's `Mixin()`, and set the organization once per request,
usually in middleware:

```go
ctx = schema.OrgKey.With(ctx, orgID)
```

From then on, reads, updates and deletes see only that organization's rows,
and inserts get its `org_id`. A request that forgot to set it fails with
`scope.ErrNoScope` instead of seeing every tenant's data. An insert naming a
different organization fails with `scope.ErrMismatch`.

In your tests, `scopetest.Isolates` checks that an entity really is confined
to its scope. See the [tenant example](../examples/tenant).

## Soft delete

List `softdelete.Mixin{}` in an entity's `Mixin()`. A delete then sets
`deleted_at` instead of removing the row, and every read and write skips
deleted rows. `softdelete.Skip(ctx)` turns it off for one context, to see,
restore or purge deleted rows:

```go
// restore
db.Update[veloxgen.Invoice]().
	Where(I.Number.EQ("INV-002")).
	Set(I.DeletedAt.SetNull()).
	Exec(softdelete.Skip(ctx))

// purge for good
db.Delete[veloxgen.Invoice]().
	Where(I.DeletedAt.NotNull()).
	Exec(softdelete.Skip(ctx))
```

The filter is an interceptor, so `WithoutPolicy` still hides deleted rows.
A soft delete does not trigger `ON DELETE CASCADE`.

## Raw SQL

Raw SQL is the escape hatch for what the builder cannot express. Each call
takes a reason, because velox applies no policy or scope to raw SQL; filtering
by tenant is up to you.

```go
n, err := db.Exec(ctx, "nightly cleanup",
	"DELETE FROM sessions WHERE expires_at < $1", now)

totals, err := db.Raw[veloxgen.Invoice]("revenue report",
	"SELECT org_id, SUM(total_cents) AS total_cents FROM invoices GROUP BY org_id").All(ctx)
```

`Raw` scans columns into an entity's fields by name, so alias computed
columns to a column name. For a result that is not an entity, range over
`Rows` and scan each row in column order:

```go
for row, err := range db.Rows(ctx, "revenue report",
	"SELECT org_id, SUM(total_cents) FROM invoices GROUP BY org_id") {
	if err != nil {
		return err
	}
	var org, total int64
	if err := row.Scan(&org, &total); err != nil {
		return err
	}
}
```

All three run on the connection the DB uses, so in a `Tx` they see the
transaction's own writes. `velox.Pool(db)` is nil on a transaction's DB.

## Row locks

`ForUpdate()` and `ForShare()` lock the rows a query reads until the
transaction ends; `SkipLocked()` and `NoWait()` change what happens when
another transaction holds a row. Locking reads run inside `Tx` only, on
PostgreSQL and MySQL. A job queue, for a `Job` entity with a `state` enum,
looks like this:

```go
err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
	jobs, err := tx.Query[veloxgen.Job]().
		Where(J.State.EQ(veloxgen.JobStatePending)).
		OrderBy(J.ID.Asc()).
		Limit(10).
		ForUpdate().
		SkipLocked().
		All(ctx)
	// ... work on jobs, then update them ...
	return err
})
```

On MySQL a locking read locks every row it scans, so give its filter an
index.

## Deadlines

`velox.WithDeadlines` gives every statement and transaction a time limit,
set once when you open the DB:

```go
db, err := velox.Connect("sqlite", "app.db", velox.WithDeadlines(velox.Deadlines{
	Statement:    2 * time.Second,  // when the context has no deadline
	MaxStatement: 10 * time.Second, // never longer than this
	Tx:           5 * time.Second,
	MaxTx:        30 * time.Second,
}))
```

`velox.TxTimeout(d)` bounds one transaction, within `MaxTx`. A transaction
that runs past its deadline is rolled back, never committed, and fails with an
error matching both `velox.ErrTimeout` and `context.DeadlineExceeded`.

## Errors

velox wraps errors with context (`velox: veloxgen.Invoice: ...`) and gives you
sentinels and typed errors to test for:

```go
_, err := db.Query[veloxgen.Invoice]().Where(I.ID.EQ(id)).One(ctx)
switch {
case velox.IsNotFound(err):
	// 404
case errors.Is(err, velox.ErrDenied):
	// 403
}

var ce *velox.ConstraintError
if errors.As(err, &ce) && ce.Kind == velox.ConstraintUnique {
	// ce.Constraint names the violated constraint
}
```

The ones you will meet most:

| Error | When |
|---|---|
| `ErrNotFound` (test with `IsNotFound`) | `One` or `Save` found no row |
| `ErrNotSingular` | `One` found more than one |
| `ErrUnbounded` | a read with no `Limit` or `AllowUnbounded()` |
| `ErrNoWhere` | an update or delete with no `Where` or `All()` |
| `ErrDenied`, `*PolicyError` | a policy refused |
| `ErrStale` | `Save` lost an optimistic-locking race |
| `ErrOutsideTx` | a statement on `db` instead of `tx` inside `Tx` |
| `ErrTimeout` | a deadline passed |
| `*ConstraintError` | a unique, foreign-key, not-null or check constraint failed |
| `*ValidationError` | a value failed a field's validator |
| `scope.ErrNoScope` | a scoped entity with no scope value in the context |

`velox.DriverCode(err)` gives the database's own code (such as a PostgreSQL
SQLSTATE) for logs.

## Read replicas and shards

`velox.WithReplicas(...)` sends reads to replicas, and `velox.WithSession(ctx)`
keeps a request reading its own writes from the primary for a short while.
`velox.OpenShards` runs each statement on one shard, chosen from the context by
`velox.WithShardFor`. Both are optional, and a DB without them pays nothing
for them ([ADR 0055](decisions/0055-lazy-unsafe-scope.md)). See the
[routing example](../examples/routing).

## Observability

`contrib/veloxotel` is an OpenTelemetry adapter in its own module, so velox
itself depends on no OpenTelemetry package. Pass its options when opening the
DB:

```go
db := velox.Open(velox.SQLite, sqlDB, veloxotel.Options(veloxotel.Config{
	TracerProvider: tp,
	MeterProvider:  mp,
	App:            "billing",         // turns on the SQL comment
	CommentTags:    []string{"route"}, // velox.WithTags keys it carries
})...)
```

You get a span per statement (nested under a `velox.tx` span inside a
transaction), a fingerprint of each statement's shape, the call site
in your code, and metrics. See the [otel example](../examples/otel).

For something lighter, `velox.WithAround` runs your function around every
statement, and `Query.SQL(ctx)` shows what a query would send, policy
conditions included.

## GraphQL

`contrib/graphql` is a velox-gen plugin that generates a GraphQL schema and
Go code for [gqlgen](https://gqlgen.com): Relay connections with `orderBy`
and `where` inputs, edges, and create and update inputs. An ID is the
entity's key, so a resolver takes `id int64`; `gqlgen.yml` binds `ID` to
`veloxgql.Int64ID` for int64 keys. You annotate the schema:

```go
field.String("name").Annotations(graphql.OrderField("NAME"), graphql.WhereOps()),
```

```go
func (User) Annotations() []velox.Annotation {
	return []velox.Annotation{
		graphql.QueryField("users"),
		graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate),
	}
}
```

Then generate with the plugin and run gqlgen:

```sh
go get -tool github.com/syssam/velox/contrib/graphql/cmd/velox-graphql
go get -tool github.com/99designs/gqlgen
go tool velox-gen -plugin velox-graphql ./veloxschema
go tool gqlgen generate
```

Most resolvers are one line. `veloxgql.Setup` wraps the gqlgen server with
limits on a request's size, depth and cost, and `veloxgql.ErrorPresenter`
keeps database and velox error text away from clients. GraphQL reads and
writes run through the same velox runtime, so policies and scopes apply to
them too. The
[graphql example](../examples/graphql) is a complete server, and its README
walks through each step.

## Where to go next

- [examples/](../examples/README.md): one runnable program per topic, plus
  [tracker](../examples/tracker), a whole service.
- [coming-from-ent.md](coming-from-ent.md): the differences from ent.
- [stability.md](stability.md): what may change before and after 1.0.
- [decisions/](decisions/README.md): why velox works the way it does.
- The API reference on
  [pkg.go.dev](https://pkg.go.dev/github.com/syssam/velox), with runnable
  examples. Until v1.0.0 is tagged, pkg.go.dev shows an older project; read
  the godoc from a checkout with `go doc github.com/syssam/velox`.
