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
- [Recording changes](#recording-changes)
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
	"github.com/syssam/velox/dialect/sqlschema"
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
		index.Fields("org_id", "number").Unique().Annotations(sqlschema.IndexWhere("deleted_at IS NULL")),
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
  `NOT NULL`, and an unset field stores its zero value;
  an `Optional` enum, whose zero is none of its values, needs a `Default` or
  `Nillable` ([ADR 0090](decisions/0090-veloxe-defaults.md)).
- `Nillable()` makes the column nullable. The Go field becomes a pointer, and
  its handle gains `IsNull`, `NotNull` and `SetNull`.

An insert takes a struct, so in the struct a field's zero value and "not set"
are the same thing: an insert fills the `Default` into a zero field. It does
so first, before the insert's setters and the hooks, as ent fills its
defaults before its hooks, so a value a setter or a hook writes is stored
over the default, zero or not ([ADR 0082](decisions/0082-defaults-before-setters.md)):

```go
g := veloxgen.Group{Name: "a"}             // field.Bool("open").Default(true)
db.Insert(&g).Exec(ctx)                    // open = true
db.Insert(&g).Set(veloxgen.Groups.Open.Set(false)).Exec(ctx) // open = false
```

| Field | Unset (zero) in the struct stores | To store the zero value |
| --- | --- | --- |
| `field.Bool("active")` | `false` | the struct |
| `field.Bool("active").Default(true)` | `true` | a setter: `Set(Xs.Active.Set(false))` |
| `field.Bool("active").Nillable().Default(true)` | `true`, when the `*bool` is nil | the struct: `new(false)`; NULL: a setter, `Set(Xs.Active.SetNull())` |
| `field.Int("seats").Default(100)` | `100` | a setter: `Set(Xs.Seats.Set(0))` |
| `field.String("plan").Default("free")` | `"free"` | a setter: `Set(Xs.Plan.Set(""))` |
| `field.String("plan").Optional().Nillable()` | `NULL` | the struct |

A nil pointer field is unset, as in ent, where a create builder fills a
default unless `SetX` was called; so is a field whose only setter is a nil
`SetPtr`, ent's `SetNillableX(nil)`. `SetNull()` in the insert is an
explicit NULL: the column is written as `NULL`, so neither the `Default`, a
default function nor the column's `DEFAULT` replaces it
([ADR 0124](decisions/0124-explicit-null-in-inserts.md)).

A GraphQL create input leaves such a field optional (`active: Boolean`): left
out it takes the `Default`, and `false` is stored. A constant `Default` is
also the column's `DEFAULT`. `Annotations(sqlschema.Default("100"))` sets a
database `DEFAULT` of one SQL text for every dialect, and
`sqlschema.DefaultExprs` one for each. An
insert of the zero value leaves such a column out, so the database fills it,
and reads the value back into the struct
([ADR 0093](decisions/0093-lean-inserts.md)):

```go
// field.Time("created_at").Annotations(sqlschema.DefaultExpr("CURRENT_TIMESTAMP"))
// field.Enum("status").Values("new", "paid").Annotations(sqlschema.DefaultExprs(map[string]string{dialect.Postgres: "'new'"}))
inv := veloxgen.Invoice{Number: "A-1"}
db.Insert(&inv).Exec(ctx)
// INSERT INTO "invoices" ("number") VALUES ($1) RETURNING "id", "created_at", "status"
// inv.ID, inv.CreatedAt and inv.Status now hold what the database stored.
```

A zero left to the database skips the field's validators, so an enum whose
zero, `""`, is none of its values inserts and stores its default. A value
given is validated, and so is a column an upsert's conflict clause writes.

A setter is always written, as ent's `SetX` is: its column is in the
INSERT with the value it gives, the zero included, so neither a database
default nor a `Default` replaces it. Only a field no setter names is left
to its default
([ADR 0136](decisions/0136-explicit-zero-and-empty-patch.md)):

```go
// field.Other("exchange_rate", decimal.Decimal{}).Annotations(sqlschema.Default("1"))
db.Insert(&o).Exec(ctx)                                       // exchange_rate = 1
db.Insert(&o).Set(veloxgen.Orders.ExchangeRate.Set(decimal.Decimal{})).Exec(ctx) // exchange_rate = 0
```

The zero a struct leaves to the default is the Go zero value, as
`reflect.Value.IsZero` sees it. So a struct's `decimal.Decimal{}` is left to
the default, while a decimal zero that holds a non-nil `*big.Int`, such as
shopspring's `decimal.Zero`, is not zero to velox, and is written as 0.

`field.Enum("status").Values(...)` generates a Go type, `veloxgen.InvoiceStatus`,
with a constant per value, such as `veloxgen.InvoiceStatusPaid`. The column
is stored as ent stores it: `character varying` on PostgreSQL,
`enum(...)` on MySQL and `text` on SQLite, with no CHECK. A Postgres
`SchemaType` that names a type, `map[string]string{dialect.Postgres: "mood"}`,
makes a PostgreSQL enum type of that name instead.

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
  `post_tags` table), or uses the edge schema `Through` names
  ([Edge schemas](#edge-schemas)).
- `OnDelete(edge.Cascade)` asks the database to delete children with their
  parent.

The [relations example](../examples/relations) shows all three shapes.

### Edge schemas

A many-to-many edge can name an edge schema with `Through`, as in ent. Its
rows are the links, with an ID and columns of their own:

```go
func (User) Edges() []velox.Edge {
	return []velox.Edge{edge.To("groups", Group.Type).Through("memberships", Membership.Type)}
}

func (Group) Edges() []velox.Edge {
	return []velox.Edge{edge.From("users", User.Type).Ref("groups").Through("memberships", Membership.Type)}
}

func (Membership) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("role").Values("owner", "member").Default("member"),
		field.Int64("user_id"),
		field.Int64("group_id"),
	}
}

func (Membership) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("user", User.Type).Unique().Required().Field("user_id").OnDelete(edge.Cascade),
		edge.To("group", Group.Type).Unique().Required().Field("group_id").OnDelete(edge.Cascade),
	}
}
```

- Membership is an entity like any other: `db.Query[veloxgen.Membership]()`
  reads its rows, and `db.Insert` writes one.
- velox-gen writes no join entity. It adds the edge `memberships`, from
  User to the rows, and a unique index over `(user_id, group_id)`.
- `Groups.Add` inserts a row per group, with its defaults, and keeps a link
  that exists. `Remove` and `Clear` delete rows.
- Membership's hooks run on those inserts and deletes. A link its Write
  policy hides stays through `Clear`.
- `Add` fails before any statement when a required column has no default.
  Insert the rows yourself then.
- An edge schema cannot have a Read or Query policy, Interceptors or
  `softdelete.Mixin`: `Has` and `With` through `groups` would not apply
  them.
- velox-gen warns when neither key edge declares `OnDelete`.

[ADR 0109](decisions/0109-edge-schemas.md) records the design. The
[relations example](../examples/relations/README.md#many-to-many-through-an-edge-schema)
runs it.

### Indexes

`index.Fields("a", "b")` makes an index, `.Unique()` makes it unique, and
`.Annotations(sqlschema.IndexWhere("..."))` makes it partial on PostgreSQL and
SQLite, as in ent. Options only a SQL
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

An index is named `<table>_<name>`, its name being its `StorageKey`, else
`<entity>_<columns>` as in ent, unless that name starts with `<table>_`
already.
PostgreSQL wants an index name unique in the database schema, and a mixin's
StorageKey is one name on every table that lists the mixin. A name over the
dialect's limit is shortened with an FNV-1 128 suffix where ent uses MD5,
so a database velox's predecessor migrated keeps its names
([ADR 0083](decisions/0083-package-index-names.md)).

Also on PostgreSQL, `sqlschema.DescColumns("created_at")` (or `Desc()` for every
key) sorts a key descending, which an `ORDER BY name, created_at DESC` needs (a
single column is read backwards as it is), and `sqlschema.OpClassColumn("title",
"gin_trgm_ops")` (or `OpClass(...)` for every key) sets a key's operator class:

```go
index.Fields("title").Annotations(sqlschema.IndexType("GIN"), sqlschema.OpClass("gin_trgm_ops"))
index.Fields("name", "created_at").Annotations(sqlschema.DescColumns("created_at"))
```

A schema's table is `sqlschema.Table("users")`, or
`sqlschema.Annotation{Table: "users"}`, as ent's `entsql` spells it;
`SchemaTable`, `Collation` and `Checks` name its database schema, its MySQL
collation and its CHECK constraints. The old velox's options velox does not
hold, such as `StorageParams`, `Prefix`, `Size` or `ColumnType`, are not
there: [coming from ent](coming-from-ent.md#removed-schema-options)
names each one's replacement.

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

A Go program can run the same generate with package
[`generate`](../generate), as ent's `compiler.Generate` is run from a
`generate.go`. Its `Config` has a field for each flag: `Schema`, `Out`,
`Package`, `Plugins` and `Check`. It also takes what the command cannot: a
plugin called in the process, such as `graphqlgen.Plugin(opts)` from
`contrib/graphql/graphqlgen`, with no tool to build. Such a program is the
check too: have CI run it with `Check: true`, as `velox-gen -check` does not
know a plugin's `Run`.

```go
//go:build ignore

package main

import (
	"context"
	"log"

	"github.com/syssam/velox/contrib/graphql/graphqlgen"
	"github.com/syssam/velox/generate"
)

func main() {
	cfg := generate.Config{
		Out:     "./veloxgen",
		Plugins: []generate.Plugin{graphqlgen.Plugin(nil)},
	}
	if _, err := generate.Run(context.Background(), "", cfg); err != nil {
		log.Fatal(err)
	}
}
```

A `//go:generate go run generate.go` line in another file of the package
runs it.

Generated code carries a version constant (today
`velox.GeneratedCodeIsVersion31`). If you update velox and the shape of
generated code has changed, the old `veloxgen` stops compiling until you run
velox-gen again. [stability.md](stability.md) explains the rule.

### The entities of a package

`velox.Entities[E]()` returns the metadata of every entity of E's generated
package, sorted by Go type name: each one's table, its database schema, its
columns (name, Go field, nullability) and its primary key, as indexes into
the columns. Any one entity of the package names it. It is what ent's
generated `migrate.Tables` gives code that works from table names, such as
a report layer that needs each table's key columns:

```go
infos, err := velox.Entities[veloxgen.User]()
if err != nil {
	return err
}
keys := map[string][]string{}
for _, in := range infos {
	for _, k := range in.PrimaryKey {
		keys[in.Table] = append(keys[in.Table], in.Columns[k].Name)
	}
}
```

Each call returns new copies, which the caller may change. velox-gen writes
the package's list only when its schema declares a Policy, Hooks or
Interceptors (its own or a mixin's); elsewhere `Entities` returns an error.
An entity whose metadata fails to build fails the call, with its error.

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

velox declares no index of a foreign key on PostgreSQL or SQLite, as ent
does not. `diff` writes a `review: unindexed-fk` line above a statement
that adds a key no index leads, or drops the last index that did: `With`,
`Has`, joins and deletes of the referenced row scan the table without one.
Declare `index.Fields("author_id")` in the schema, or pass
`-allow unindexed-fk=posts.author_id` when the scan is meant
([ADR 0114](decisions/0114-unindexed-foreign-keys.md)). MySQL indexes
every key itself.

**Without foreign keys.** velox declares a FOREIGN KEY for every edge. A
database that has never had them, as ent's
`migrate.WithForeignKeys(false)` makes, starts its directory with
`migrate diff -dialect postgres -foreign-keys=false` (or `migrate baseline
… -foreign-keys=false`). velox.json then records `"foreign_keys": false`,
and every file and schema.json of the directory holds no foreign key, on
every dialect. `diff -check` expects none, and `migrate drift` reports a
key the database has as `drop-foreign-key`. The indexes are the same as
with keys, and no `unindexed-fk` line is written, as no key is added. To
change the setting later, edit `"foreign_keys"` in velox.json and run
`migrate hash`; the next `diff` writes the migration that drops or adds
the keys. Relations work as before, since velox reads no key at run time,
but what the database did for one is yours: an edge's `OnDelete` does
nothing, and a key may name a row that does not exist
([ADR 0167](decisions/0167-no-foreign-keys.md)).

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

**In tests and on a development database** you need no directory.
`veloxtest.Open` connects, makes the schema's tables with
`migrate.Create`, and closes the database when the test ends, as ent's
`enttest.Open` does. The tables are the ones `migrate diff` would plan,
built in the test's process from the schema values you list
([ADR 0113](decisions/0113-test-schema-create.md)):

```go
var schemas = []schema.Interface{veloxschema.User{}, veloxschema.Post{}}

func TestPosts(t *testing.T) {
	db := veloxtest.Open(t, "sqlite", "", schemas) // a new file in t.TempDir()
	// ...
}
```

On SQLite an empty DSN, or one of a database in memory such as enttest's
`file:ent?mode=memory&cache=shared&_fk=1`, gives each call a file of its
own in `t.TempDir()`: a copy of the tables `migrate.Create` made once for
the test binary, so an Open costs a file copy, not the schema's DDL
([ADR 0147](decisions/0147-sqlite-memory-and-fast-test-databases.md)).

`migrate.Create(ctx, velox.Pool(db), s)` is the same step for a
development server: it makes the tables in an empty database, does
nothing when the database already holds them as the schema says, and
refuses anything else. It never alters a table and writes no history, so
production keeps the versioned files and `migrate.Up`.

For a production directory without foreign keys, pass
`veloxtest.WithoutForeignKeys()` to `veloxtest.Open`, or
`s.WithoutForeignKeys()` to `migrate.Create`, so the test's tables match
it. `veloxtest.WithOptions(...)` passes `velox.Option`s to the DB.

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
`velox.And` and `velox.Not`. A field's handle is named by what its column
holds, and has the methods that make sense for it:

| Field | Handle | Beyond `EQ`, `NEQ`, `In`, `NotIn`, `Asc`, `Desc`, `Set`, `Count` |
|---|---|---|
| bool, enum, bytes | `Col` | nothing more |
| time | `TimeCol` | `GT`, `GTE`, `LT`, `LTE`, `Between`, `Min`, `Max` |
| number, and an Other field in a `numeric` or `decimal` column (a `decimal.Decimal`) | `NumberCol` | those, and `Sum`, `Avg`, `Add`, `Sub` |
| string | `StringCol` | the comparisons, and `HasPrefix`, `HasSuffix`, `Contains`, `EqualFold`, `ContainsFold`, `Like` |
| any other Other field (a range, a point) | `OtherCol` | `GT`, `GTE`, `LT`, `LTE`, `Between`, `Min`, `Max`, compared by its SQL type |
| json | `JSONCol` | none of those but `Set`: `Text`, `Number`, `Bool`, `HasKey`, `Has`… test inside the document |

A nullable field's handle is the `Null` form, `NullTimeCol` and so on, which
adds `IsNull`, `NotNull` and `SetNull`. An Other field is a number when its
`SchemaType` names `numeric` or `decimal` (MySQL also `dec` and `fixed`) for
every dialect it lists.

An Other field needs a `SchemaType` for every dialect. A decimal, one whose
`SchemaType` names `numeric` or `decimal` for MySQL and PostgreSQL, may name
none for SQLite: its column is `numeric` there, so it orders and compares as
a number (ADR 0148).

SQLite has no decimal type. Its NUMERIC column stores a whole number as an
INTEGER and any other decimal as the nearest double, which keeps 15
significant digits. velox makes the rest behave as on PostgreSQL and MySQL:

- `Sum`, `Avg`, `SumBy`, `AvgBy` and a decimal column's `Sum()` and `Avg()`
  terms are rounded to 15 significant digits. So 0.1 + 0.2 is 0.3, not
  0.30000000000000004. A sum of whole numbers stays an exact integer. A sum
  that needs more than 15 digits is not exact.
- In an `Expr`, `Calc` or `SetExpr`, a value of a decimal column's Go type,
  such as a `decimal.Decimal`, is bound as a number. It compares with an
  aggregate or an arithmetic expression as a number, so
  `velox.Expr[E]("? > ?", X.Amount.Sum(), decimal.NewFromInt(1))` holds for
  the groups it should. The type must be that of a decimal column of the
  query's entity.

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

### Reading one column: Values and IDs

`Values` reads one column of the rows a query reads, and `IDs` reads their
primary keys. Both end the query's chain, as `All` does. `Values` takes the
column's handle, and the result's type is the column's: a pointer for a
nullable column, nil for NULL. A JSON column cannot be read this way, and
`Values` refuses its handle when the program compiles. `IDs` returns the
keys, typed as the key field is:

```go
numbers, err := db.Query[veloxgen.Invoice]().Where(I.Status.EQ(veloxgen.InvoiceStatusPaid)).Limit(100).Values(ctx, I.Number)
ids, err := db.Query[veloxgen.Invoice]().Where(I.Status.EQ(veloxgen.InvoiceStatusPaid)).AllowUnbounded().IDs(ctx)
```

They read as `All` does: the policies, the scope, the filters, `Limit`,
`Offset` and the order apply, and they need a `Limit` or `AllowUnbounded`.

### Choosing columns

`Columns(I.ID, I.Number)` reads only the named columns, and `Omit(...)` reads
all but the named ones. The other fields keep their zero values.

### Ordering and NULL

`OrderBy` takes a handle's `Asc()` or `Desc()`, a Term's, or a joined
column's `rel.By(...)`. NULL sorts after every value ascending and before
them descending, as PostgreSQL sorts it, on every dialect. `NullsFirst()`
and `NullsLast()` put it before or after every value, in either direction:

```go
T := veloxgen.Tasks
db.Query[veloxgen.Task]().OrderBy(T.DueAt.Desc().NullsLast(), T.ID.Asc()).Limit(20)
```

PostgreSQL and SQLite write `"due_at" DESC NULLS LAST`. MySQL has no NULLS
clause, so velox writes `` `due_at` IS NULL, `due_at` DESC ``; an
expression with bound values is written, and bound, twice. The rows come
back in the same order on all three, and `After` pages that order. A column
that cannot be NULL is ordered as if neither were called.

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

A row holds no value of an expression, so `After` refuses an ordering by
one (a relation's aggregate excepted). A computed key, such as a number
parsed from a JSON member, pages through `Paginate`, the GraphQL
connection: order the query by the expression, and velox selects its
value beside each row (`AS velox_order_0`), puts it in the cursor and
compares the same expression with it on the next page, the key breaking
ties, after any `orderBy` the client gives. NULL goes where the ordering
puts it, `NullsFirst`, `NullsLast` or the default:

```go
// The last digit of the votes, NULL for a multiple of 10.
key := velox.Calc[veloxgen.Post]("NULLIF(? % ?, ?)", P.Votes, int64(10), int64(0))
conn, err := db.Query[veloxgen.Post]().OrderBy(key.Desc().NullsLast()).
	Paginate(ctx, after, first, before, last, veloxgen.WithPostOrder(orderBy))
```

Outside GraphQL, `untyped.FromQuery(q).OrderValues()` reads the values
(`rows.OrderValues(i)`) and `AfterValues(row, vals)` continues after a
row. The expression must give the same value for a row each time it runs,
and compare in SQL as the value read back compares: velox binds that value,
an integer, a float, text, a time, a bool or NULL, as the driver read it.
A decimal or a time the driver reads as text (MySQL's DECIMAL, or its
DATETIME without parseTime) is bound cast to its type, so it compares
exactly. The cursor's term hashes the values the expression binds: an
expression bound to a value that changes per request, such as the time now,
makes earlier cursors invalid. A window function or an aggregate is refused.

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

### Counting a relation: Count, Sum, Avg, Min and Max

A relation's `Count`, and `Sum`, `Avg`, `Min` and `Max` of a column of its
target, are terms of each row: a subquery correlated with the row. Order by
one, select it, or compare it in an `Expr`:

```go
db.Query[veloxgen.Author]().OrderBy(A.Posts.Count().Desc(), A.ID.Asc()).Limit(20)     // most posts first
db.Query[veloxgen.Author]().Select(A.Name, A.Posts.Sum(P.Votes)).Limit(20)            // votes per author
db.Query[veloxgen.Author]().Where(velox.Expr[veloxgen.Author]("? >= ?", A.Posts.Count(), 3)) // three posts or more
A.Posts.Where(P.Votes.GT(10)).Count()                                                 // only the popular ones
```

The target's interceptors, filters and Read policy apply inside, as in `Has`:
a post the reader may not see, or a soft-deleted one, is not counted. A
count is 0 for an author with no posts; the other aggregates are NULL, which
orders last ascending. `After` pages over these orderings, the key breaking
ties. A relation's aggregate is a value of each row: it cannot be grouped
by, aggregated, or selected beside `GroupBy`.

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

A string at a path also has `Contains`, `HasPrefix` and `HasSuffix`, which
take the string literally and keep case on every dialect. A number or a
string at a path orders a query with `Asc` and `Desc`, and its `Term()` is a
result column of `Select`:

```go
db.Query[veloxgen.Account]().Where(A.Attrs.Text("name").HasPrefix("Acme")).
	OrderBy(A.Attrs.Number("plan", "seats").Desc()).Limit(20)
```

See [ADR 0115](decisions/0115-json-append-len-patterns-order.md).

A string at a path can be read as a number or as a time. `Number()` reads a
JSON number or a string that spells one, so `{"seats": 10}` and
`{"seats": "10"}` both read 10. `Time()` reads an RFC 3339 string as an
instant, so `"2026-03-01T10:00:00+08:00"` equals `"2026-03-01T02:00:00Z"`
and sorts before `"2026-03-01T03:00:00Z"`:

```go
seats, due := A.Attrs.Text("plan", "seats").Number(), A.Attrs.Text("due").Time()
db.Query[veloxgen.Account]().Where(seats.GTE(10), due.LT(time.Now())).
	OrderBy(due.Asc()).Limit(20)
```

Anything else is NULL, and never fails the statement: `"abc"`, `"01"`,
`" 7"`, a boolean or a missing key for `Number`; a date alone, a time with
no offset, `2026-02-30` or a number for `Time`. A number is JSON's grammar,
with an exponent of at most two digits. A time has an upper-case `T` and
`Z`, a fraction of up to nine digits, an offset within ±14:59 and a year
from 1000 to 9999. PostgreSQL and MySQL compare times to the microsecond.
`Time().Term()` scans into a `time.Time`. See
[ADR 0165](decisions/0165-json-text-number-and-time.md).

### Inside a JSON array: Has, HasSome, HasEvery and IsEmpty

A column that is itself an array (`field.Strings("tags")`, or any
`field.JSON` of a slice) tests its elements:

```go
U := veloxgen.Users
db.Query[veloxgen.User]().Where(U.Tags.Has("admin"))                  // holds "admin"
db.Query[veloxgen.User]().Where(U.Tags.HasSome("admin", "staff"))     // holds one of them
db.Query[veloxgen.User]().Where(U.Tags.HasEvery("admin", "staff"))    // holds both
db.Query[veloxgen.User]().Where(U.Tags.IsEmpty())                     // is []
db.Query[veloxgen.User]().Where(U.Tags.Len().GT(3))                   // more than 3 elements
```

`Len(keys...)` is the length of the array at a path, or of the column's own
array with no key. It is NULL for anything that is not an array. Being a
`JSONNumber`, it has the comparisons, `Asc` and `Desc`.

An element is a string, a number or a boolean. `true` is not `1` and `"1"` is
not `1`, and a document that is not an array holds nothing and is not empty;
a NULL column matches neither the condition nor its `Not`. It is the same on
PostgreSQL, MySQL and SQLite. On PostgreSQL the column must be jsonb, and a GIN
index serves `Has` and `HasEvery`. velox-graphql offers them as
`tagsHas`, `tagsHasSome`, `tagsHasEvery` and `tagsIsEmpty` for `WhereOps()` or
`WhereOps(OpsJSONArray)` on a slice of strings, numbers or booleans. See
[ADR 0080](decisions/0080-json-array-elements.md).

An array inside an object is reached by `Array(keys...)`, which has the same
four conditions, and `HasField` and `HasFieldKey`:

```go
L := veloxgen.LandedCosts
db.Query[veloxgen.LandedCost]().Where(velox.Or(
	L.AllocationDetails.Array("receiptIds").Has(receiptID), // ent: sqljson.ValueContains(f, id, sqljson.Path("receiptIds"))
	L.ItemReceiptID.EQ(receiptID)))
```

Where the path is missing, or holds a scalar or an object, the array holds
nothing and is not empty, so `Not` matches the row. A NULL column matches
neither. `Array()` with no key is the column's own array. On PostgreSQL the
condition keeps `column @> '{"receiptIds":["…"]}'`, which a GIN index on the
column serves. See [ADR 0141](decisions/0141-json-array-at-a-path.md).

An array of objects is searched by one member with `HasField(key, v)`:

```go
T := veloxgen.Tasks // assignees: [{"employee_id": 7, "role": "owner"}, …]
db.Query[veloxgen.Task]().Where(T.Assignees.Array().HasField("employee_id", 7))
db.Query[veloxgen.Task]().Where(T.Meta.Array("review", "by").HasField("role", "owner"))
```

The member compares as `Has` compares an element: 7 matches 7.0, `"7"` is not
7, and `true` is not 1. A scalar element, or an object without the member,
does not match; `Not` matches an empty array and a missing path, and a NULL
column matches neither. It reads alike on the three dialects, but MySQL's
`JSON_CONTAINS` also finds `v` in a member that is an array, such as
`{"employee_id": [7]}`. On PostgreSQL the condition is
`column @> '[{"employee_id":7}]'`, which a GIN index on the column serves.
See [ADR 0159](decisions/0159-json-array-has-field.md).

`HasFieldKey(key)` asks only that the member is there with a value that is not
a JSON null, whatever else it holds:

```go
db.Query[veloxgen.Task]().Where(T.Assignees.Array().HasFieldKey("employee_id"))
```

`[{"employee_id": null}]` does not match, and `0`, `""` and `false` do, so it
differs from `HasKey`, which counts a null member as present. A scalar
element, an object without the member, an empty array and a missing path do
not match (`Not` matches them), and a NULL column matches neither. On
PostgreSQL it is `jsonb_path_exists` of a strict path, which no GIN index
serves; MySQL and SQLite read the member of each element. See
[ADR 0160](decisions/0160-json-array-has-field-key.md).

### A document that contains another: Contains

`Contains(doc)` is true where the column's JSON document contains `doc`, as
the database's JSON containment defines it. An object contains an object
whose every key and value it contains, recursively; a scalar contains an
equal scalar; arrays follow the database's rule.

```go
A := veloxgen.Accounts
db.Query[veloxgen.Account]().Where(A.Attrs.Contains(map[string]any{"plan": "pro"}))
db.Query[veloxgen.Account]().Where(velox.Or(
	A.Attrs.Contains(map[string]any{"beta": true}),
	A.Attrs.Contains(map[string]any{"plan": map[string]any{"tier": "gold"}})))
```

`doc` is encoded with `encoding/json` when `Contains` is called, and bound as
one argument; a `doc` that does not encode fails the statement. PostgreSQL
writes `"attrs" @> $1::jsonb`, with the bare column, so a GIN index on the
jsonb column serves it; MySQL writes `JSON_CONTAINS(attrs, ?)`. SQLite has no
JSON containment, so there the statement fails with an error naming SQLite.
A NULL column matches neither `Contains` nor its `Not`. See
[ADR 0154](decisions/0154-json-contains.md).

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
	return velox.Policy{Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
		// a document is visible when its org_id is that of an organisation the viewer reads
		return velox.FieldInSelect("org_id", "Org", "id", velox.FieldEQ("active", true)), nil
	}}
}
```

It is the same `EXISTS`, so `Org`'s own Read policy applies inside it, and it
is resolved when the statement is prepared: an unknown column or entity, or
two columns of different types, is an error and never a dropped condition.
One mismatch is allowed: an integer and a text column, such as an
approval's `entity_id` that holds the ids of several entities, match as the
integer's text would. The key column stays bare either way, so its index
serves each outer row. An integer column against a text key is cast to text
(`CAST(id AS TEXT)`, on MySQL `CONCAT(id)`). A text column against an
integer key is cast to the integer it spells, behind a guard, so a text
such as `"07"`, `" 7"`, `"+7"`, `""` or one past int64 matches nothing and
never fails the statement:

```sql
-- PostgreSQL; MySQL is alike with REGEXP and CAST(… AS SIGNED)
"vx1"."id" = CASE WHEN "activity"."entity_id" COLLATE "C" ~ '^(0|-{0,1}[1-9]…)$'
	THEN CAST("activity"."entity_id" AS BIGINT) END
-- SQLite, which has no regular expressions: only an integer's own text
-- comes back from the round trip
"vx1"."id" = CASE WHEN CAST(CAST("activity"."entity_id" AS INTEGER) AS TEXT) = "activity"."entity_id"
	THEN CAST("activity"."entity_id" AS INTEGER) END
```

An unsigned 64-bit key, whose values pass int64's, is cast to text instead
([ADR 0161](decisions/0161-text-column-integer-key.md)).
A rule that reaches its own entity again, as a participant's "a conversation
I take part in" read through the participants' own policy does, is cut where
it loops: that inner subquery gets none of the entity's own conditions, while
the outer statement gets them all ([ADR 0091](decisions/0091-policy-cycles-and-text-keys.md)).
`entity` is the entity's name in the schema or its table. velox finds it, and
the target of a `HasEdge`, in the generated package's entity table when the
statement runs, so a rule in a shared package can take its names from
configuration. velox-gen checks every name it can read as a constant and warns
about the others. Prefer `HasEdgeWith` where a relation exists: velox-gen then
checks the whole rule ([ADR 0079](decisions/0079-field-in-select.md),
[ADR 0081](decisions/0081-entity-table.md)).

`velox.FieldInSelectRaw(reason, column, entity, key, conditions...)` is the
same subquery with nothing but `conditions` inside it: no Read policy,
`WithFilter` filter or interceptor of `entity` applies there, soft delete
included. It is for a trail that must outlive the rows it names, such as an
activity log whose text `entity_id` names a lead that was deleted later:

```go
func (ActivityLog) Policy() velox.Policy {
	return velox.Policy{Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
		orgs, err := orgsOf(ctx)
		if err != nil {
			return velox.Predicate{}, err
		}
		// entity_id IN (SELECT lead_id FROM lead WHERE org_id IN (…)), entity_id
		// cast to lead_id's type when it spells an integer
		return velox.FieldInSelectRaw("activity trail: a deleted lead keeps its history",
			"entity_id", "Lead", "lead_id", velox.FieldIn("org_id", orgs...)), nil
	}}
}
```

The names resolve and the columns compare as `FieldInSelect`'s do. A
relation or `FieldInSelect` among the conditions keeps its own target's
policies. The statement around it keeps its own policies on its rows. The
reason is required, and an Around hook reads it from
`Statement.RawReasons()`, as it reads `ExprRaw`'s
([ADR 0157](decisions/0157-field-in-select-raw.md)).

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

### A raw condition: ExprRaw

Some conditions need a subquery velox cannot guard: a text key against an
integer key made text, a table no entity maps, or a table read on purpose
without its policy, such as an audit trail that keeps rows after an
assignment is removed. `ExprRaw` takes the same text as `Expr`, a reason
first, and allows `SELECT`, `EXISTS` and any function:

```go
db.Query[veloxgen.Bill]().Where(velox.ExprRaw[veloxgen.Bill](
	"audit trail: a removed assignment keeps its rows",
	"NOT EXISTS (SELECT 1 FROM audit_log m WHERE m.workspace_id = ? "+
		"AND m.entity_id = CAST(? AS TEXT))", B.WorkspaceID, B.ID))
```

No policy, filter or interceptor applies inside the text: it is `Raw`'s
trade, for one condition. The statement around it keeps its policies on its
own rows, in a read and in an update's or delete's `Where`. A column handle
is written qualified, `"bills"."workspace_id"`, or by velox's alias `vx1`
inside a relation's subquery, so a correlated subquery names the outer row;
alias the text's own tables, never with the outer table's name or `vx` and
a number. The text must be a constant, with values as arguments; a
semicolon, a comment, `$` and an unbalanced parenthesis or quote are still
refused. An Around hook reads each reason from `Statement.RawReasons()`
([ADR 0155](decisions/0155-expr-raw.md)).

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

A projection joins too. `velox.Joined(rel, x)` is the joined row's column, or a
`Calc` or `DateTrunc` of it, as a term of the query's entity, so a child's
rows are grouped by its parent's columns in one statement:

```go
status := velox.Joined(Items.Claim, C.Status)
rows, err := velox.Scan[byStatus](ctx, db.Query[veloxgen.ExpenseClaimItem]().
	Join(Items.Claim).
	Where(Items.Claim.Is(C.DeletedAt.IsNull())).
	Select(status, Items.BaseAmount.Sum().As("total")).
	GroupBy(status).Limit(100))
```

The parent's Read policy is in the join's ON clause, so an inner join leaves
out a row whose parent it hides, and a `LeftJoin` groups it under NULL.
Aggregate a joined term (`velox.Joined(rel, x).Sum()`); an aggregate inside `Joined`
is refused ([ADR 0143](decisions/0143-joined-terms-in-projections.md)).

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
named by its handle, as `Values` names it. Groups can be as many as the
rows, so the query says `AllowUnbounded()`:

```go
totals, err := velox.SumBy(ctx,
	db.Query[veloxgen.Invoice]().AllowUnbounded(),
	I.OrgID, I.TotalCents)
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

`Rows` reads by position: the caller scans each row. `velox.Scan` reads the
rows into a struct instead, as ent's `Scan(ctx, &v)` does. A column handle's
result column is named by its column, and `As` names any other term. A field
takes the column named by its `velox:"name"` tag, or the one matching its own
name with case and underscores ignored. A JSON column is decoded by its
codec. A NULL goes into a pointer field as nil:

```go
type orgSum struct {
	OrgID int64
	Sum   int64 `velox:"sum"`
	N     int
}
rows, err := velox.Scan[orgSum](ctx, db.Query[veloxgen.Invoice]().
	Select(I.OrgID, I.TotalCents.Sum().As("sum"), velox.CountAll[veloxgen.Invoice]().As("n")).
	GroupBy(I.OrgID).Limit(20))

n, err := velox.ScanOne[int64](ctx, db.Query[veloxgen.Invoice]().Select(velox.CountAll[veloxgen.Invoice]()))
```

These fail before anything is sent: a result column with no field, a term
with no name, and a field whose type cannot hold the term's value
([ADR 0116](decisions/0116-typed-projection-scan.md)).
 It is a read of the
entity, so `Where`, the Read policy, the scope and the interceptors apply and
the groups count only rows the caller may see, which `Raw` does not give.
`SelectRaw` and `Calc` take guarded text like `Expr`, so they keep the
policies too. A projection needs a `Limit` or `AllowUnbounded()`. It cannot be
read with `All`, `Count` and the rest, nor combined with `Columns`, `With`,
`Join`, `After` or a lock, and an aggregate of an aggregate is refused, as
is an aggregate in `GroupBy`. A
value that is not a column handle or a term of the entity (a string, another
entity's handle) fails the statement before anything is sent. An ordering by
an expression cannot be a keyset cursor. `UNION` and CTEs are not in the
builder; use [raw SQL](#raw-sql) for those
([ADR 0074](decisions/0074-projection.md), [ADR 0022](decisions/0022-group-by.md)).

A `Calc` that computes one value of each group, such as
`Calc("PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY ?)", I.TotalCents)` or
a user's aggregate, is marked with `velox.Agg`. velox then places it as an
aggregate term: beside `GroupBy` without being a key, and on MySQL in an
ordering as written. velox does not read the text to find an aggregate:
`Calc("SUM(?)", x)` needs `velox.Agg` too, and SQLite's two-argument
`MAX(?, ?)`, a value of each row, does not. `velox.Agg` of a window
function, of a term that is not an expression or of an expression that
holds an aggregate Term fails the statement
([ADR 0151](decisions/0151-explicit-aggregates-filter-entity-policy-db.md)).

A `Calc` or `SelectRaw` may be a window function, such as `COUNT(*) OVER ()`
beside a `GroupBy` and `Limit` for a page of groups and their total. It is
a result column or an ordering, never a condition
([sql.md](sql.md#a-value-calc-and-selectraw),
[ADR 0101](decisions/0101-window-terms-soft-delete-options.md)).

### Date buckets, subtotals and columns by name

`velox.DateTrunc` truncates a time to the start of its year, quarter,
month, ISO week (from Monday), day or hour. One term is the result column,
the group and the ordering:

```go
month := velox.DateTrunc[veloxgen.Invoice](velox.UnitMonth, I.CreatedAt,
	velox.InZone("Europe/London"))
for r, err := range db.Query[veloxgen.Invoice]().
	Select(month, I.TotalCents.Sum()).
	GroupBy(month).OrderBy(month.Asc()).
	Limit(24).Rows(ctx) {
	if err != nil {
		return err
	}
	var start time.Time
	var sum int64
	if err := r.Scan(&start, &sum); err != nil {
		return err
	}
}
```

The value is the bucket's first instant, which scans into a `time.Time` on
every dialect; format the label in Go. Buckets are UTC unless `InZone`
names a zone of the IANA database or an offset such as `+08:00`. MySQL
needs its time zone tables for a name; SQLite takes an offset only.
`FiscalYear(time.April)` starts the years and quarters in April. The unit,
the zone and the month are written as literals, so the text is the same in
`Select`, `GroupBy` and `OrderBy`. A part of a date, such as the month of
the year or the hour of the day, is a `Calc`: `EXTRACT(MONTH FROM ?)` on
PostgreSQL and MySQL.

`velox.Rollup` adds subtotal rows to a `GroupBy`, and a grand total; in
them the rolled-up keys are NULL, and `GROUPING(key)` is 1:

```go
grouping := velox.Calc[veloxgen.Invoice]("GROUPING(?)", I.OrgID)
q := db.Query[veloxgen.Invoice]().
	Select(I.OrgID, I.TotalCents.Sum(), grouping).
	GroupBy(velox.Rollup[veloxgen.Invoice](I.OrgID)).
	OrderBy(grouping.Asc(), I.OrgID.Asc()).Limit(100)
```

PostgreSQL takes `Rollup` and `Cube` among other keys. MySQL has only
`WITH ROLLUP`, so there a `Rollup` is the only key, and `Cube` fails.
SQLite has neither and fails the statement.

Code that is given a field's name, such as a report built from a GraphQL
request, gets the column's term with `velox.ColumnByName`. It selects,
groups and orders as the handle does, and is false for an unknown name.
`Order.Term()` and `Order.Descending()` read an ordering back,
`Term.Column()` names the column a term is, and `RawRow.Columns()` names a
row's columns:

```go
col, ok := velox.ColumnByName[veloxgen.Invoice]("org_id")
if !ok {
	return fmt.Errorf("unknown field")
}
q := db.Query[veloxgen.Invoice]().Select(col, I.TotalCents.Sum()).GroupBy(col).Limit(100)
```

([ADR 0127](decisions/0127-report-terms.md))

The term's aggregates, `col.Sum()` and the rest, write what the handle's
write. `velox.Agg(Calc("SUM(?)", col))` works too, but on SQLite only the
term's `Sum` rounds a decimal column. A conditional distinct count is a `Calc` with
`CountDistinct`:

```go
nonZero := velox.Calc[veloxgen.Invoice]("CASE WHEN ? <> 0 THEN ? END", I.TotalCents, I.TotalCents).CountDistinct()
```

A column computed beside full entity rows, such as a sort key for a cursor,
is a projection of every column plus the `Calc`. Read it with `Scan` into a
struct that embeds the entity:

```go
type keyed struct {
	veloxgen.Record
	SortKey string `velox:"sort_key"`
}
key := velox.Calc[veloxgen.Record]("COALESCE(?, '')", R.Name)
var cols []velox.Selectable[veloxgen.Record]
for _, c := range untyped.TableOf[veloxgen.Record]().Info().Columns {
	t, _ := velox.ColumnByName[veloxgen.Record](c.Name)
	cols = append(cols, t)
}
q := db.Query[veloxgen.Record]().Select(cols...).Select(key.As("sort_key")).
	OrderBy(key.Asc(), R.ID.Asc()).Limit(20)
// the next page: q.Where(velox.Expr[veloxgen.Record]("? > ? OR (? = ? AND ? > ?)", key, k, key, k, R.ID, id))
rows, err := velox.Scan[keyed](ctx, q)
```

A group that comes from a dated table no edge describes, such as the
membership a store had on an order's date, is a `CASE` term over the row's
own columns, built from the memberships read first under their own policy
([ADR 0142](decisions/0142-nextapp-projection-recipes.md)).

### Distinct

`Distinct()` reads each distinct row once, `SELECT DISTINCT`, for a
projection and for `Values`:

```go
orgs, err := db.Query[veloxgen.Invoice]().Distinct().Limit(100).Values(ctx, I.OrgID)
```

A read of entities is by key, so its rows are distinct already: `All`,
`Count`, `Exists` and an aggregate with `Distinct` fail before anything is
sent. Count distinct values with a handle's `CountDistinct`. A `Distinct`
read orders only by what it reads, a projection's terms or `Values`'
column; any other ordering fails on every dialect, as PostgreSQL and MySQL
refuse it
([ADR 0102](decisions/0102-distinct-explain-update-all-privacy-trace.md)).
ent's `GroupBy(a, b).Scan` with no aggregate is `Select(a, b).Distinct()`,
or `Select(a, b).GroupBy(a, b)`.

## Writing

### Insert

`Insert` writes one entity and reads the generated key back into it, with
the columns it left to declared database defaults. `InsertAll` writes
many, in as few statements as it can, and reads the same back into each
row; a multi-row upsert on MySQL, which cannot tell its rows apart, reads
nothing back:

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
// lines[0].ID and lines[1].ID are set now.
```

Both fill defaults and run hooks and policies first.

`InsertAll` takes values, so a zero field is unset and gets its default.
`InsertBulk`, ent's `CreateBulk`, takes one `Insert` a row, each with its
own setters, and stores each row as that `Insert` alone would: a setter's
`false`, `0` or `""` is kept, and a column no setter names gets its
default
([ADR 0153](decisions/0153-insert-bulk.md)):

```go
L := veloxgen.Lines
lines := make([]veloxgen.Line, len(amounts))
qs := make([]velox.InsertQuery[veloxgen.Line], len(amounts))
for i, cents := range amounts {
	qs[i] = db.Insert(&lines[i]).Set(L.InvoiceID.Set(inv.ID), L.AmountCents.Set(cents)) // 0 is stored as 0
}
err := db.InsertBulk(qs...).Exec(ctx)
// each lines[i].ID is set now.
```

Each row's hooks and Insert policy run as for its `Insert`. Rows whose
setters name the same columns share one multi-row INSERT, chunked as
`InsertAll`'s; rows of another column set go in another statement, and
several statements run in one transaction. An upsert option goes on the
`InsertBulk` and applies to every row. Generic code over `E` names both
type arguments, `db.InsertBulk[E, PE](qs...)`, since `PE` is not inferred
there.

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

`UpdateAll()`, ent's `UpdateNewValues`, writes every column of the inserted
row instead. It leaves out the key, the `OnConflict` columns, `Immutable`
columns, the `Version` column (which an upsert bumps) and a column whose
`SetPtr` was skipped. `updated_at` takes the inserted row's value, its
`Default`, since its `UpdateDefault` still does not fire
([ADR 0102](decisions/0102-distinct-explain-update-all-privacy-trace.md)):

```go
err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).UpdateAll().Exec(ctx)
```

`UpdateSet(setters...)` gives the update branch the setters an update
takes, as ent's `AddX` and `ResolveWith` do. `Set` writes a value, `Add`
adds to the stored value, and `SetExpr` writes an expression, in which a
handle is the existing row's column and `velox.Excluded(c)` the inserted
row's:

```go
err := db.Insert(&stock).OnConflict(S.SKU).UpdateSet(
	S.Qty.Add(stock.Qty), // qty = "stocks"."qty" + $n
	velox.SetExpr(S.Peak, "GREATEST(?, ?)", S.Peak, velox.Excluded(S.Peak)),
).Exec(ctx)
```

The entity gets back what the database stored in those columns.
`UpdateWhere(preds...)` conditions the update branch: a conflicting row it
rejects is kept as it is, and the policy's conditions still apply.
`OnConflictWhere(preds...)` names a partial unique index by its predicate,
and `OnConstraint(name)` a PostgreSQL constraint. MySQL's `ON DUPLICATE KEY
UPDATE` can say none of the three, so there they fail before anything is
sent ([ADR 0111](decisions/0111-upsert-setters-hook-conditions-tx-hooks.md)).

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
A JSON array's handle has `Append`, ent's `AppendX`:
`Set(U.Tags.Append([]string{"new"}))`. A NULL column becomes the new
elements ([ADR 0115](decisions/0115-json-append-len-patterns-order.md)).
`velox.SetExpr` sets a column to an SQL expression over the row's own
columns, written as `Expr`'s text is, and keeps the policies:
`Set(velox.SetExpr(I.Customer, "TRIM(?)", I.Customer))`
([ADR 0095](decisions/0095-set-expr.md)).
`UpdateQuery.One(ctx)` updates exactly one row and returns it, read back by
its key ([ADR 0048](decisions/0048-update-one.md)).

### One row by key

A row is named by its key column's handle, `I.ID.EQ(id)`, whether the key is
an int64 or a UUID. `One` on each builder then works on exactly one row:

```go
inv, err := db.Query[veloxgen.Invoice]().Where(I.ID.EQ(id)).One(ctx)            // ent: Get(ctx, id)
inv, err = db.Update[veloxgen.Invoice]().Where(I.ID.EQ(id)).
	Set(I.Customer.Set("Initech"), I.Status.Set(veloxgen.InvoiceStatusSent)).One(ctx) // ent: UpdateOneID(id).SetX().Save(ctx)
err = db.Delete[veloxgen.Invoice]().Where(I.ID.EQ(id)).One(ctx)                     // ent: DeleteOneID(id).Exec(ctx)
```

ent's `ClearX()` is `X.SetNull()` on a Nillable field's handle.

Each fails with an error that matches `velox.ErrNotFound`
(`velox.IsNotFound`) when no row has the key. A row the policies or an
interceptor hide is not found either, a soft-deleted one included: each
runs the statement `Where(I.ID.EQ(id))` would run with `All` or `Exec`.

- `Query.One` reads with `LIMIT 2`, so a condition that matches more rows
  fails with `ErrNotSingular`.
- `UpdateQuery.One` by key sends the UPDATE and reads the row back, in one
  transaction ([ADR 0092](decisions/0092-update-one-by-key.md)). It returns
  the row as written.
- `DeleteQuery.One` by key is one DELETE. When its conditions do not pin the
  key, it runs in a transaction, a savepoint inside one, and rolls back with
  `ErrNotSingular` when more than one row matches. A hook that skips the
  delete without `SetAffected` makes it not found
  ([ADR 0122](decisions/0122-one-row-by-key.md)).

For an entity you already hold, `Save` writes the columns you name by its
key, and is also `ErrNotFound` when the row is gone
([Save](#save)). ent's `UpdateOneID(id).SetA(a).ClearB().Save(ctx)` is
either `Update…One` above, or, with the entity in hand:

```go
inv.Customer, inv.Status = "Initech", veloxgen.InvoiceStatusSent
err := db.Save(&inv, I.Customer, I.Status).Exec(ctx)
```

`Save` writes the struct's values, so a nil pointer field writes NULL: that
is ent's `ClearX()`. It returns no row; the struct holds what was written.

### Optional values and UpdateDefault

Every handle with `Set` has `SetPtr(p)`, ent's `SetNillableX`. A nil `p`
writes nothing; a non-nil one sets `*p`. A patch from an API then needs no
`if` per field:

```go
_, err := db.Update[veloxgen.Invoice]().
	Set(I.Customer.SetPtr(in.Customer), I.TotalCents.SetPtr(in.TotalCents)).
	Where(I.ID.EQ(id)).
	Exec(ctx)
```

On a nullable column a nil `p` still writes nothing: `SetNull()` writes
NULL. An update whose every setter was skipped, or whose `SetInput` gives
nothing, is an empty patch: as in ent, its hooks run and its
`UpdateDefault`s fire, so `updated_at` moves. An update of only edge
setters does the same. When they assign nothing
either, nothing is sent: `Exec` returns 0, `SQL` returns "" and `One`
returns the row
([ADR 0136](decisions/0136-explicit-zero-and-empty-patch.md)). An update
never given a `Set` still fails. In an insert, a skipped setter leaves the
field as it is, so a zero field still gets its declared database default.
In an upsert, a column `Update` names is not updated when its only setter
was skipped. With no column left, the update branch assigns the first
conflict column its own value (`ConflictClause.Ignore`, ent's `Ignore()`),
so the existing row is kept and its key still read back.

`KeepDefaults(cols...)`, on an update and on `Save`, stops the named
columns' `UpdateDefault` from firing: a
system write that must not move `updated_at`. Nothing is written to them.
Setting the column yourself already wins over its `UpdateDefault`, but
writes a value. A column without an `UpdateDefault` fails the statement
([ADR 0099](decisions/0099-keep-defaults-and-set-ptr.md)):

```go
err := db.Save(&inv, I.Status).KeepDefaults(I.UpdatedAt).Exec(ctx)
```

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
catches a common bug. Work that must not join the transaction, such as an
audit row that must survive a rollback, runs on `db` under
`velox.WithoutTx(ctx)`. It needs a second connection, so on SQLite's pool
of one it fails rather than wait for the transaction.

A nested `tx.Tx(...)` is a savepoint: its error undoes only its own work,
and the outer transaction goes on. This is how you recover from a failed
insert on PostgreSQL, where a failed statement otherwise aborts the whole
transaction.

A helper that is handed `db` and the caller's `ctx`, rather than `tx`, joins
the caller's transaction with `velox.TxFrom(ctx, db)`. The DB it returns runs
in that transaction and has `db`'s own `WithoutPolicy` reason, or none, never
the outer transaction's. It is false outside a transaction, after one ended,
and for another DB's
([ADR 0137](decisions/0137-nextapp-port-helpers.md)):

```go
func WithTx(ctx context.Context, db *velox.DB, fn func(ctx context.Context, tx *velox.DB) error) error {
	if tx, ok := velox.TxFrom(ctx, db); ok {
		return fn(ctx, tx) // or tx.Tx(ctx, fn) for a savepoint
	}
	return db.Tx(ctx, fn)
}
```

Options go after the function:

- `velox.TxRetrySerializable(n)` reruns the function after a serialization
  failure or deadlock, up to n more times. The function must be safe to run
  again.
- `velox.TxIsolation(level)`, `velox.TxReadOnly()` and
  `velox.TxTimeout(d)`.

`velox.WithTxDefaults(opts...)` at `Connect` gives every transaction on the
DB those options first, velox's own included; a call's options come after
and override them. A nested `Tx` takes none and does not retry on its own:
the outermost transaction's retry runs it again.

```go
db, err := velox.Connect("postgres", dsn, velox.WithTxDefaults(
	velox.TxRetrySerializable(3), velox.TxTimeout(30*time.Second)))
```

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

### After the commit

`velox.AfterCommit(tx, fn)` queues work that must not happen for a
transaction that rolls back, such as publishing an event:

```go
err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
	if err := tx.Insert(&order).Exec(ctx); err != nil {
		return err
	}
	return velox.AfterCommit(tx, func(ctx context.Context) { publish(ctx, order.ID) })
})
```

The functions run in the order queued, once the outermost transaction has
committed and before `Tx` returns, on the `Tx` context without its
cancellation. A rollback runs none. A nested `tx.Tx` that rolls back drops
the ones queued while it ran. A hook inside `db.Tx` queues with
`velox.AfterCommit(m.DB(), fn)`. Call it from the goroutine that runs the
transaction.
Outside a transaction, and on a `UseTx` DB, whose commit velox never sees,
`AfterCommit` returns an error. One function's panic does not stop the
others: `Tx` panics again once all have run
([ADR 0100](decisions/0100-after-commit-values-and-log.md)).

`velox.BeforeCommit(tx, fn)` runs `fn(ctx) error` once the outermost `fn`
has returned nil, before COMMIT, and its statements on `tx` join the
transaction. An error rolls it back. `velox.AfterRollback(tx, fn)` runs `fn`
once the work done through `tx` is undone: when the outermost transaction
does not commit, or when `tx` is a nested `Tx`'s and its savepoint rolls
back. A transaction that commits runs, in order: each recorded write's sink,
before its savepoint ends; the `BeforeCommit` functions; COMMIT; the
`AfterRollback` functions of savepoints that rolled back; the `AfterCommit`
functions ([ADR 0111](decisions/0111-upsert-setters-hook-conditions-tx-hooks.md)).

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

A run-time hook can name the column handles, so it reads and writes columns
by type rather than by name:

```go
T := veloxgen.Tasks
title, ok := T.Title.Value(m)   // the update's title, a string
err := T.Slug.Assign(m, slug(title)) // m.Set("slug", …)
err = T.CreatedBy.Fill(m, user)      // m.Fill("created_by", user)
for r := range m.Rows() {
	err = T.Slug.Put(r, slug(T.Title.Get(r))) // r.Set and r.Value
}
```

A value of the wrong type does not compile. A nullable column's handle adds
`ValuePtr`, `GetPtr`, `AssignNull` and `PutNull`, which tell NULL apart. A
handle of another entity is a misuse, which fails the write. The schema
package cannot import `veloxgen`, so its hooks keep the names.

A hook asks what a write does with `m.Columns()`, the columns it assigns,
`m.Cleared(name)`, a column it sets to NULL, and `m.Edges()`, its edge
setters. `velox.If(h, cond)` and `velox.Unless` apply a hook only to some
writes, with `velox.HasOp`, `velox.HasColumns`, `velox.HasCleared` or any
function of the `Mutation`. `velox.Reject(ops...)` refuses kinds of write.
They work in the schema and in `WithHooks` alike:

```go
func (Task) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.If(notifyAssignee, velox.HasColumns("assignee_id")),
		velox.Reject(velox.OpDelete),
	}
}
```

A change log does not need a hook: a recording handle reports every row a
write changes, with its old and new values. See
[Recording changes](#recording-changes).

### Policies

A policy decides what the caller may see and change, from the context. It
has three parts:

- `Read` returns a condition ANDed into every statement that reads the
  entity: queries, counts, `With` loads, `Has`, updates and deletes.
- `Write` returns a condition ANDed into updates, deletes and `Save`.
- `Insert` checks each new row.

Each function is given the statement's ctx and `db`, the DB the statement
runs on (the transaction inside `db.Tx`), for a policy that reads; one
that reads nothing names it `_`.

```go
func (Invoice) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
			switch role.FromContext(ctx) {
			case role.Accountant:
				return velox.Allow(), nil
			case role.Viewer:
				return velox.FieldNEQ("status", "draft"), nil
			}
			return velox.Predicate{}, velox.Deny("no billing role")
		},
		Write: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
			if err := role.RequireAccountant(ctx); err != nil {
				return velox.Predicate{}, err
			}
			return velox.Allow(), nil
		},
		Insert: func(ctx context.Context, _ *velox.DB, _ velox.Row) error { return role.RequireAccountant(ctx) },
	}
}
```

Read, Write and Insert fail closed. Returning the zero `velox.Predicate{}`
fails the statement instead of matching everything, and
`velox.Deny(reason)` returns an error that matches `velox.ErrDenied`. A
mixin's policy is ANDed before the entity's own, so an entity cannot widen
what its mixin allows. The Query and Mutation rule lists of package
`privacy` follow ent's privacy instead: a list whose rules all skip allows,
and `privacy.DecisionContext` lifts them. To deny by default, end a list
with `privacy.AlwaysDenyRule()`.

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

Prefer a condition that reads nothing itself, such as `velox.HasEdgeWith`,
which runs inside the statement. A policy function that must read to
decide, such as a lookup of an organization's subtree, reads through the
`db` it is given, with its ctx. `db` is the DB the statement runs on,
never nil: inside `db.Tx` it is the transaction, as `m.DB()` is for a hook
and the `db` a filter gets, so the policy gets the same answer inside a
transaction and outside. A privacy rule reads through `q.DB()` or
`m.DB()` of the query or mutation it judges. A schema cannot name the
generated types, so it reads with `Rows` or package `untyped`:

```go
func (Org) Policy() velox.Policy {
	return velox.Policy{Read: func(ctx context.Context, db *velox.DB) (velox.Predicate, error) {
		var orgs []any
		for row, err := range db.Rows(ctx, "org subtree",
			"SELECT id FROM orgs WHERE path LIKE ?", orgPath(ctx)+"%") {
			if err != nil {
				return velox.Predicate{}, err
			}
			var id int64
			if err := row.Scan(&id); err != nil {
				return velox.Predicate{}, err
			}
			orgs = append(orgs, id)
		}
		return velox.FieldIn("org_id", orgs...), nil
	}}
}
```

A statement made under a policy function's or an interceptor's ctx is not
judged by that function, whatever DB it runs on, so a policy that reads
its own entity ends; every other policy, interceptor and filter applies to
it ([ADR 0135](decisions/0135-policies-read-through-the-transaction.md),
[ADR 0151](decisions/0151-explicit-aggregates-filter-entity-policy-db.md)).

A helper that builds a condition and may have none to add returns the zero
`velox.Predicate{}` (or `Pred[E]{}`), and its caller tests it with
`velox.IsZero(p)` before using it, since a statement holding the zero
condition fails.

To assert a helper's SQL in a test, render a query that holds it on a DB
with no connection, `velox.Open(dialect, nil)`, on any dialect. The `SQL`
methods of a query, an update and a delete send nothing; a statement or
`Tx` run on that DB fails with an error, and so does `SQL` when a policy
reads the database. `WithoutPolicy` keeps the Read policy out of the text,
while an interceptor's condition stays in it:

```go
db := velox.Open(velox.Postgres, nil).WithoutPolicy("render a condition")
text, args, err := db.Query[veloxgen.Invoice]().
	Where(velox.Pred[veloxgen.Invoice](unpaid("A-1", "A-2"))).
	Limit(1).SQL(ctx)
// SELECT ... FROM "invoices" WHERE (NOT "paid" AND "number" = ANY($1)) LIMIT 1
```

A condition by name may take its column from configuration, such as
`velox.FieldIn(cfg.OrgColumn, ids...)`. velox-gen checks a constant name
and warns on any other. The statement checks every name when it runs, and
an unknown one fails it, naming the entity and the column.

### Interceptors

An interceptor is a filter that applies to every statement on an entity,
like a `Read` policy, but `WithoutPolicy` does not lift it. It is meant for
application rules that even admin code should respect, such as hiding
soft-deleted rows. Declare `Interceptors() []velox.Interceptor` on a schema
or mixin; each has a `Filter func(ctx, db) (velox.Predicate, error)`.
`db` is the DB the statement runs on, the transaction's inside `Tx`, as a
policy function's is: a Filter that reads, reads through it. A statement
made under the Filter's ctx is not narrowed by that Filter.

### Hooks and filters for every entity

Some rules cover many entities and live in the application, not the
schema: a permission matrix loaded per request, say. Two DB options add
them for every entity, as ent's `client.Use` and `client.Intercept` do
([ADR 0098](decisions/0098-hooks-and-filters-for-every-entity.md)).

`velox.WithHooksAll(hooks...)` runs hooks on every entity's writes, before
the schema's hooks and `WithHooks[E]`'s. `m.Entity()` names the entity,
`"Invoice"`, so a hook acts on some and passes the others on:

```go
refund := velox.On(func(next velox.Mutator) velox.Mutator {
	return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
		if err := next.Mutate(ctx, m); err != nil || m.Entity() != "CreditNote" {
			return err
		}
		return syncRefunds(ctx, m.DB())
	})
}, velox.OpInsert, velox.OpUpdate)
```

`velox.WithFilter(f)` adds a row filter for every entity. velox calls it
with the statement's context, the DB it runs on, the entity
(`velox.FilterEntity`) and an op, at most once per
statement for each entity and op, except where a relation loops back
([ADR 0091](decisions/0091-policy-cycles-and-text-keys.md)). The op is the
statement's own for its own entity (`OpSelect`, `OpUpdate`, `OpDelete`, or
`OpInsert` for an upsert's update branch; `OpUpdate` for a `Writable` read,
as for an update; `OpDelete` for a soft delete, which sends an UPDATE) and
`OpSelect` for each entity a
`With`, `Join`, `Has` or `HasWith` reaches. It reaches what an interceptor
reaches, and `WithoutPolicy` does not lift it.

This soft-delete filter narrows reads only:

```go
db, err := velox.Connect("postgres", dsn, velox.WithFilter(
	func(ctx context.Context, _ *velox.DB, e velox.FilterEntity, op velox.Op) (velox.Predicate, error) {
		if op != velox.OpSelect || !e.HasColumn("deleted_at") || includeDeleted(ctx) {
			return velox.Allow(), nil
		}
		return velox.FieldIsNull("deleted_at"), nil
	}))
```

`e.HasColumn(column)` reports whether the entity has a column of that
name, as `FieldEQ` names one, so the filter needs no list of soft-deleted
entities. It reads the entity's own metadata: nothing global, no lock
([ADR 0151](decisions/0151-explicit-aggregates-filter-entity-policy-db.md)).

It leaves writes alone for two reasons. ent's interceptors
wrap queries only, so a port keeps their behaviour. And a restore must
reach a deleted row: code that reads it with the deleted included and then
clears `deleted_at` under the plain context would otherwise update 0 rows
and say nothing.

`e.Name()` is the entity's name, which the generated package names in a
constant: `veloxgen.TypeOrder` is `"Order"`
([ADR 0139](decisions/0139-entity-type-constants.md)).

A filter fails closed. It returns `velox.Allow()` for an entity it does
not restrict, the join entities of many-to-many edges included: restricting
one makes `With` loads and `Has` disagree. The zero `velox.Predicate{}`, an
error, or a column the entity lacks fails the statement, and an error is
not a denial. `velox.None()` matches no row, for a caller who may see
none. Several filters AND, in the order given.

A filter that reads the database to make its condition, such as a team's
members or an organization's subtree, reads through the `db` parameter,
the DB the statement runs on, with the `ctx` it was given. It is never nil,
as `m.DB()` is not for a hook. Inside `Tx` it is the transaction, so the
statement gets the same condition inside a transaction as outside; a read
on a DB captured at `Connect` would fail there with `ErrOutsideTx`. The filter's reads are not narrowed by the filter itself,
so it does not call itself without end; the other filters, interceptors
and policies apply to them
([ADR 0132](decisions/0132-filter-reads-through-the-statements-db.md)).

```go
velox.WithFilter(func(ctx context.Context, db *velox.DB, e velox.FilterEntity, op velox.Op) (velox.Predicate, error) {
	if e.Name() != veloxgen.TypeOrder || op != velox.OpSelect {
		return velox.Allow(), nil
	}
	teams, err := db.Query[veloxgen.Member]().
		Where(veloxgen.Members.UserID.EQ(userID(ctx))).Limit(100).All(ctx)
	if err != nil {
		return velox.Predicate{}, err
	}
	ids := make([]any, len(teams))
	for i, m := range teams {
		ids[i] = m.TeamID
	}
	return velox.FieldIn("team_id", ids...), nil
})
```

A schema written as ent's are keeps its `privacy` rules
([ADR 0089](decisions/0089-veloxe-privacy-shape.md)).

## Recording changes

An audit log needs the values a write replaced. `db.Record(sink)` returns a
handle like `db` whose writes report each row they change to the sink: one
`velox.Change` per row, bulk updates and deletes included, with the row
before and after the write. velox reads the old row in the write's own
transaction, locked, so it is the row the write replaced.

The sink gets all of a write's changes in one call, inside the write's
transaction, before it commits, with `tx`, a handle on that transaction
that does not record. Here a sink set up once, at startup, turns every
write's changes into audit rows in the write's own transaction.
`veloxgen.AuditLog` is your own audit entity, not part of the tour
(`I := veloxgen.Invoices`):

```go
audit := velox.RecordFunc(func(ctx context.Context, tx *velox.DB, changes []velox.Change) error {
	rows := make([]veloxgen.AuditLog, len(changes))
	for i, c := range changes {
		old, err := json.Marshal(c.OldRow()) // null for an insert
		if err != nil {
			return err
		}
		cur, err := json.Marshal(c.NewRow()) // null for a delete
		if err != nil {
			return err
		}
		var changed []string
		for _, f := range c.Diff() {
			changed = append(changed, f.Name)
		}
		rows[i] = veloxgen.AuditLog{
			Entity: c.Entity, Key: fmt.Sprint(c.Key), Op: c.Op.String(),
			Old: old, New: cur, Changed: changed,
		}
	}
	return tx.InsertAll(rows).Exec(ctx) // in the write's transaction, not recorded
})
rdb := db.Record(audit) // once, at startup

err := rdb.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
	_, err := tx.Update[veloxgen.Invoice]().Where(I.TotalCents.LT(10000)).Set(I.TotalCents.Add(500)).Exec(ctx)
	return err
})
```

On PostgreSQL below 18, for two matching invoices, the transaction sends:

```sql
BEGIN
SAVEPOINT velox_1
SHOW server_version_num  -- once per pool, at the first recorded write
SELECT "id", "created_at", "updated_at", "org_id", "deleted_at", "number", "customer", "status", "total_cents"
  FROM "invoices" WHERE "total_cents" < $1 AND "deleted_at" IS NULL AND "org_id" = $2
  ORDER BY "id" FOR UPDATE
UPDATE "invoices" SET "total_cents" = "total_cents" + $1, "updated_at" = $2
  WHERE "total_cents" < $3 AND "id" = ANY($4) AND "deleted_at" IS NULL AND "org_id" = $5
  RETURNING "id", "created_at", "updated_at", "org_id", "deleted_at", "number", "customer", "status", "total_cents"
INSERT INTO "audit_logs" … VALUES (…), (…)  -- the sink, once for the write
RELEASE SAVEPOINT velox_1
COMMIT
```

Outside `Tx` the same write sends the same statements in a transaction of
velox's own, with no SAVEPOINT: the SHOW, BEGIN, the read, the UPDATE, the
sink's INSERT, COMMIT. A sink error rolls the write back in both.

SQLite sends the same without `FOR UPDATE` and the SHOW, with the keys
as an IN list (`"id" IN (?, ?)`), and with its guard against integer
overflow in the Add: `"total_cents" = CASE WHEN
typeof("total_cents" + ?) = 'real' THEN abs(-9223372036854775808) ELSE
"total_cents" + ? END`. MySQL has no
RETURNING, so after the UPDATE it reads the new rows with
``SELECT `id`, … FROM `invoices` WHERE `id` IN (?, ?)``. On PostgreSQL 18
and later the read and the write are one statement:

```sql
UPDATE "invoices" SET "total_cents" = "total_cents" + $1, "updated_at" = $2
  WHERE "total_cents" < $3 AND "deleted_at" IS NULL AND "org_id" = $4
  RETURNING old."id", …, old."total_cents", new."id", …, new."total_cents"
```

To act after the commit, collect the changes with a `velox.Recorder`
instead, and range over `rec.All()` after `Tx` returns. A Recorder drops
what a transaction, a savepoint or a retried attempt rolled back, so once
`Tx` returns it holds what committed. A Recorder shared by concurrent
transactions also holds, in `All` and `Len`, the rows of those still running,
until they commit or roll back. [ExampleDB_Record](../example_record_test.go) builds
an audit batch this way, and ExampleRecordFunc there writes audit rows
through the sink's `tx`.

### What a Change holds

- `c.Entity` is the entity's name ("Invoice"), `c.Op` is `OpInsert`,
  `OpUpdate` or `OpDelete`, and `c.Key` is the row's primary key: after the
  write for an insert or an update, before it for a delete, a `[]any` for a
  composite key.
- `I.TotalCents.Old(c)` and `I.TotalCents.New(c)` read one column, typed. A `Null*`
  handle gives the element, the zero when NULL; `I.PaidAt.OldPtr(c)` and
  `NewPtr(c)` give a fresh pointer to it, nil for NULL. `c.Changed(I.Status)`
  reports whether an update changed any of the columns given.
  `velox.RowsOf[veloxgen.Invoice](c)` gives both rows as `*Invoice`.
- For any entity, `c.OldRow()` and `c.NewRow()` are the rows as `*E`, and
  `c.Diff()` lists an update's columns that differ, as
  `FieldChange{Name, Old, New}`. An insert's and a delete's Diff is nil.
  A decimal column compares by number, so `"1.50"` and `"1.5"` of a
  decimal type held as text are the same value.
- An insert's new row is the row as stored. PostgreSQL and SQLite return
  every column with the INSERT; MySQL reads the rows by key after it, in
  the write's transaction. A value the column rounds or pads (a decimal's
  scale, a time's microseconds, a `CHAR`) is as the database holds it, so
  the row equals a later update's old row. Your entity still holds what
  you sent, with the key and the declared defaults read back.
- Times in the rows are in UTC, so their JSON compares.
- An update that changes no column is not reported. A column an
  `UpdateDefault` fills, such as `updated_at`, does not count: every update
  changes it. `velox.RecordIgnore(I.Customer)` names more columns that do
  not count, and `velox.RecordKeep(I.UpdatedAt)` takes an UpdateDefault
  column back in; pass one of each per entity. Diff leaves ignored columns
  out, but the getters and the rows still hold them.
- A `Sensitive` column keeps ent's meaning: it is not printed or
  serialized. The getters and `RowsOf` give its real value, `OldRow` and
  `NewRow` marshal without it, and Diff leaves it out. Nothing is masked.
  An update that changes only a Sensitive column is reported with an empty
  Diff; `c.Changed(col)` tells that the secret changed.

### When the sink runs

The sink runs once per write (an `Exec`, a `One`, an `Insert`, an
`InsertAll`, a `Save`, the edge writes of a `Rel.Add`), after its
statements and inside its transaction, before COMMIT, with every Change of
the write in order. A write that changed nothing does not call it. Its
error is the write's error, and rolls the write back.

- In a transaction, the write and the sink run under a savepoint. An error
  from either rolls back to it, so a recorded write that returns an error
  took no effect. A recorded write inside it that fails, a hook's included,
  fails the whole write even when the hook swallows the error; to tolerate
  that failure, run that write in `tx.Tx`.
- Outside a transaction, velox opens one for the write, and the sink runs
  before its COMMIT.
- `tx` is a handle on the write's transaction that does not record: what
  the sink writes through it commits or rolls back with the change, and is
  not recorded itself. Write through `tx` alone: a write through another
  handle runs outside the write's transaction (and on a pool of one
  connection waits for it forever), and one through the recording handle
  is recorded again, calling the sink from inside itself.
- A hook runs before its write's transaction or savepoint opens: what it
  writes through `m.DB()` is a recorded write of its own, with its own sink
  call, before or after the write's as the hook orders them. Outside `Tx`
  it is also its own transaction; for one, write in `Tx`. A write with
  edge setters (`Insert(&p).Set(P.Tags.Add(1))`) runs its hooks inside its
  transaction or savepoint, so there the hook's write joins its sink call.
- A `RecordFunc` sees each write's changes before the commit, including
  those a later rollback of your transaction undoes. For side effects
  outside the database, use a Recorder and act once `Tx` returns.
- velox never sees a transaction you began yourself (`velox.UseTx`) end:
  every change goes to the sink, and a Recorder used through it keeps the
  changes of a transaction that rolls back. What the sink writes through
  `tx` there rolls back with your transaction.

### Which writes are recorded

Every write through the recording handle, its transactions and savepoints,
its `WithoutPolicy`, and a hook's `m.DB()`. Raw SQL and dry runs are not.
`Record` on a transaction's handle replaces the sink for that handle: its
writes go to the new sink, and a rollback still drops a Recorder's rows.

- **Upsert.** A row that conflicted is an update with its real old values,
  an inserted row is an insert, and a row `DoNothing` kept is not reported.
  On MySQL a recorded upsert refuses a row with NULL in a conflict column or
  a generated key left zero in the conflict target, and fails when a row
  conflicts on another unique key, or two rows of one InsertAll share a
  key. Insert such rows without `OnConflict`.
- **Soft delete.** softdelete's update is reported as `OpDelete` with
  `c.Soft` true and both rows. `Changed` is false and Diff nil. Another
  soft-delete mixin's update is reported as an update.
- **Edges.** Re-pointing a bidirectional one-to-one edge reports each row it
  changes. A many-to-many `Add` or `Remove` reports the join rows, both rows
  of a bidirectional link.
- A recorded write that cannot account for every row it touched fails and
  is rolled back. A Rewrite hook may not change its `RETURNING`.
- A delete reports only the rows the database deleted: below PostgreSQL 18
  and on SQLite it returns their keys, so a row a hook or a trigger kept is
  not reported; on MySQL a count short of the rows read fails the write.

### What it costs

A handle that does not record sends what it always sent. A recording one
adds:

- below PostgreSQL 18, one locked read per update or delete;
- for an insert, `RETURNING` of every column on PostgreSQL and SQLite,
  and on MySQL one read of every column by key per statement, in place of
  the read of the declared defaults;
- outside a transaction, a BEGIN and a COMMIT, for a single statement too;
- in a transaction, one SAVEPOINT and RELEASE per write, an edge write
  of several statements too;
- on PostgreSQL, one `SHOW server_version_num` per pool, at the first
  recorded write.

Below PostgreSQL 18 an upsert's first read cannot lock a row that is not
there yet: a row another session inserts under the same key at that moment
is reported as an insert. On MySQL two recorded upserts of new keys can
deadlock; `TxRetrySerializable` retries them. An upsert's old rows are read
without the scope or the Read policy, so a sink can see values the caller
cannot. [ADR 0094](decisions/0094-change-recording.md) has the details.

`velox.Diff(before, after)` compares two rows you already hold.

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
		Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
			org, err := OrgKey.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ("org_id", org), nil
		},
		Insert: func(ctx context.Context, _ *velox.DB, r velox.Row) error {
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
ctx = veloxschema.OrgKey.With(ctx, orgID)
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

A soft delete is a delete to a `WithFilter` filter, which is given
`OpDelete`, and an update to the entity's policies. A `WithHooksAll` hook,
which runs outside the mixin, sees it twice: as the delete (`OpDelete`),
then as the mixin's update inside it (`OpUpdate`), whose
`m.Value(softdelete.Column)` is a time. A restore's is nil. The entity's own
delete hooks run inside the mixin and see only hard deletes.

Two options change it. `Assign` sets more columns in the same UPDATE, read
from the delete's context, such as who deleted the row; its error fails the
delete. `Reads: softdelete.Unfiltered` installs no filter: reads and updates
see deleted rows, a restore is a plain update, and a delete stays soft and
marks live rows only:

```go
softdelete.Mixin{
	Assign: func(ctx context.Context) ([]softdelete.Set, error) {
		u, err := auth.User(ctx)
		if err != nil {
			return nil, err
		}
		return []softdelete.Set{{Column: "deleted_by_id", Value: u.ID}}, nil
	},
	Reads: softdelete.Unfiltered,
}
```

`softdelete.By(column, key)` is an `Assign` that sets a column to a
`scope.Key`'s value in the delete's context, and nothing when there is
none. `softdelete.Assigns` joins several:

```go
softdelete.Mixin{Assign: softdelete.Assigns(
	softdelete.By("deleted_by_user_id", auth.User),
	softdelete.By("deleted_by_member_id", auth.Member),
)}
```

## Raw SQL

[sql.md](sql.md) lists every way to write SQL in velox, from a guarded
condition to a raw statement, and which keep the policies. Reach for raw SQL
only when `Expr`, `Calc` and `SetExpr` cannot say it. A single condition
that must read past the policies is `ExprRaw`, or `FieldInSelectRaw` for
another entity named in a schema, which keep them on the rest of the
statement.

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
PostgreSQL and MySQL, and on SQLite under the option below. A job queue, for a `Job` entity with a `state` enum,
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

SQLite has no row locks, and a locking read there fails by default. The
option `velox.WithSQLiteLockingReads()` runs it without the lock clause, as
ent does, so code written for PostgreSQL runs on SQLite in tests. Every
other check still holds: the read needs a `Tx`, and `Count` cannot lock.
`SkipLocked` then skips nothing, and `NoWait` does not stop a wait.

What stands in for the lock is SQLite's one writer at a time
([ADR 0123](decisions/0123-sqlite-locking-reads-opt-in.md)):

- On `velox.Connect`'s one connection, transactions in the process take
  turns, so a read-modify-write is never interleaved.
- On several connections, or several processes, a `Tx` begins DEFERRED, as
  the driver's plain `BEGIN` does: it takes the write lock at its first
  write. Two transactions can read the same row. When one has written it,
  the other's write fails with SQLITE_BUSY. No update is lost, and
  `velox.TxRetrySerializable(n)` runs the loser again.
- With `_txlock=immediate` in the DSN, every `Tx` takes the write lock at
  its `BEGIN`. A second one waits, up to the busy timeout, and then reads
  what the first wrote, as it would behind a row lock.

```go
db, err := velox.Connect("sqlite", "app.db?_txlock=immediate&_pragma=busy_timeout(5000)",
	velox.WithSQLiteLockingReads())
```

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

if ce, ok := errors.AsType[*velox.ConstraintError](err); ok && ce.Kind == velox.ConstraintUnique {
	// ce.Constraint names the violated constraint
}
```

`velox.IsConstraintError`, `IsValidationError` and `IsNotSingular` are the
same tests by ent's names. Like `IsNotFound`, a driver's typed-nil error
cannot make them panic.

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
| `*ValidationError` | a value failed a field's validator; `Name` is the schema field's name, `Field` its Go name |
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
conditions included. To read the database's plan for a query, take
`q.SQL(ctx)` and run `EXPLAIN` on it through `db.Rows`, with the arguments
`SQL` returned.

`velox.WithLog(slog.Default(), velox.LogArgs())` logs every statement at
Debug: its SQL, duration, rows and error. With `LogArgs`, it logs the
arguments too. A Sensitive column's argument is always `"<sensitive>"`.
An argument velox cannot tie to a column, such as an `Expr`'s or raw
SQL's, is `"<hidden>"`. A driver's error message can echo a value, so it
is printed only with `LogArgs` and when every argument was shown;
otherwise the record names the constraint, velox's own error (such as
`not found`, or a field's `validation failed`), or the error's type, and
the driver's code.

### Counting statements: N+1 in tests

A loop that reads a relation once per row sends one statement per row: an
N+1. `velox.WithStatementCounting()` counts each statement in the
`StatementCounter` that `velox.CountStatements(ctx)` puts in a context, by
shape, the statement's text without its arguments. `Check` reports a read
of one shape run more often than a `StatementBudget` allows, naming it
([ADR 0117](decisions/0117-statement-counting.md)):

```go
ctx, n := velox.CountStatements(ctx)
listPosts(ctx, db)
if err := n.Check(velox.StatementBudget{Statements: 10, Repeats: 1}); err != nil {
	t.Error(err) // ran 20 times, of 1 allowed: SELECT ... FROM "users" WHERE "id" = $1
}
```

A DB without the option runs none of it. `veloxtest.Open` adds it, and
`veloxtest.NoNPlusOne(t, ctx)` fails the test when it ends if a read of
one shape ran twice; `veloxtest.Budget(t, ctx, b)` checks any budget.
The fix is `With`, which loads a relation for every row in one more
statement. velox's GraphQL resolvers batch each relation per request, so
a query through them passes.

## GraphQL

`contrib/graphql` is a velox-gen plugin that generates a GraphQL schema and
Go code for [gqlgen](https://gqlgen.com): Relay connections with `orderBy`
and `where` inputs, edges, and create and update inputs. An ID is the
entity's key, so a resolver takes `id int64`; `gqlgen.yml` binds `ID` to
`gqlgenx.Int64ID` for int64 keys. You annotate the schema:

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

Most resolvers are one line. The generated code imports the engine-free
runtime `veloxgql` and its package `gqlgraph`; gqlgen's adapter is
`veloxgql/gqlgenx`. `gqlgenx.Setup` wraps the gqlgen server with limits on
a request's size, depth and cost, and `gqlgenx.ErrorPresenter` keeps
database and velox error text away from clients. A connection or an edge
read in a context no adapter made fails with
`veloxgql.ErrNoSelectionSource`. A service that pages outside a request,
in its own list method, a call from another service or a test, asks for
it with `ctx = veloxgql.Service(ctx, db)`: there generated code reads
every column, and each edge one row at a time. GraphQL reads and
writes run through the same velox runtime, so policies and scopes apply to
them too. The
[graphql example](../examples/graphql) is a complete server, and its README
walks through each step.

The same runtime also serves
[github.com/syssam/graphql-go](https://github.com/syssam/graphql-go), whose
generator gqlc binds the SDL. Generate for it with `engine=graphql-go`, and
gqlc binds every field to the generated code except the root fields, JSON
fields and fields of the application's own Go type:

```sh
go tool velox-gen -plugin velox-graphql:engine=graphql-go ./veloxschema
go tool gqlc -config gqlc.yaml
```

The SDL then names each type's Go type with `@goModel`, and `gqlc.yaml`
needs the keys the package documentation of `contrib/graphql` lists under
"graphql-go and gqlc". A JSON field or a field of the application's own
Go type is on the Resolver, whose method returns its getter's value in one
line. gqlc does not read
`@goField(name:)` yet, so velox-gen refuses a field or edge renamed with
`graphql.FieldName`, a non-null bytes field and a key not named ID, naming
each. The adapter is `veloxgql/graphqlgo`,
whose functions are graphql-go options. `velox` and `app` below are the
packages gqlc writes from `velox.graphql` and the application's
`app.graphql`, not velox's root package:

```go
s, err := graph.NewSchema(velox.Bindings(root), app.Bindings(root), graphqlgo.Scalars())
exec := graphql.NewExecutor(s,
	graphqlgo.With(db),
	graphql.WithMaxDepth(12),
	graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(log)))
```

`graphqlgo.With(db)` puts velox's state in each operation, so edges batch
and reads are projected as under gqlgen, and `graphqlgo.Scalars()` binds
velox's scalars in the schema. A schema that declares only some of them
binds those with `graphqlgo.ScalarsOnly("Time", "UUID")`. graphql-go's own options limit a request's
size, depth, complexity and cost; velox keeps only its fixed bounds on a
page (100 rows), a list edge (1000) and a where filter. A mutation runs
in no transaction: a resolver that needs one opens it with `db.Tx` or
`velox.UseTx`.

### velox-graphql's options

velox-graphql takes `key=value` options after a colon, separated by commas.
With none, the API is entgql's: names from the Go name, where inputs that
fail closed, no `Node`, and only the annotated order fields and root fields.
Each option stands alone, and a value it does not know fails the run.

| Option | Default | The other value |
| --- | --- | --- |
| `engine=` | `gqlgen` | `graphql-go`, served by graphql-go and bound by gqlc |
| `where=` | every field with `WhereOps` | `whitelist`: a field other than the key has predicates only with `graphql.WhereInput` |
| `names=` | `go`: `imageURL` | `schema`: `imageUrl`, from the schema name |
| `filters=` | `strict`: an empty `idIn` matches no node | `lenient`: an empty `In`, a false `IsNil` and an empty `or` add nothing, and an empty nested input wraps `veloxgql.ErrEmptyWhereInput` |
| `node=` | `off` | `on`: `Node`, `node(id:)` and `nodes(ids:)`, for IDs unique across types |
| `idops=` | `kind`: equality alone for a foreign key | `equality`: equality alone for the key, any field named like a key and an Other field |
| `orderfields=` | `annotated` | `all`: every orderable field in the order enum |
| `queryfields=` | `annotated` | `all`: a root field for every type |
| `optional=` | `nonnull`, as the column is | `nullable` |
| `requirededges=` | `nullable`, as a policy may hide the node | `nonnull` |
| `inputkeys=` | `edge`: `bestFriendID` | `column`: `userBestFriend` |
| `jsontypes=` | `json`: the `JSON` scalar | `named`: the Go type's name, which the application declares |
| `sdlfiles=` | `one` | `entity`: `velox.graphql` and one `velox_<entity>.graphql` each |
| `entities=A+B` | every entity | the SDL of the entities listed alone |
| `apptypes=A+B` | none | the types and scalars the application declares itself |
| `only=enums` | everything | the enums alone, for an application with its own SDL |

An application that keeps the GraphQL API of velox's predecessor, served by
graphql-go, gives these together
([ADR 0156](decisions/0156-graphql-options-not-compat.md)):

```sh
go tool velox-gen -plugin velox-graphql:names=schema,filters=lenient,node=on,sdlfiles=entity,orderfields=all,queryfields=all,optional=nullable,requirededges=nonnull,jsontypes=named,idops=equality,inputkeys=column,apptypes=JSON+UUID+Uint64,where=whitelist,engine=graphql-go ./veloxschema
```

For gqlgen, leave out `engine=graphql-go`. That option writes what gqlc
needs: `@goModel` on each type, the Resolver for a field a `<Field>GQL`
method converts, and a `graphql` tag on an input field whose name
graphql-go would not derive, such as `IPAddress`'s `ipAddress`.

### Where the SDL goes

The SDL files are written into `-out` beside the Go code, unless velox-gen's
`-files` names another directory of the module. Each `.graphql` file then
goes there at the same path, and the Go files stay in `-out`:

```sh
go tool velox-gen -out veloxgen -files schema -plugin velox-graphql:sdlfiles=entity ./veloxschema
```

writes `schema/velox.graphql` and `schema/velox_<entity>.graphql`, where
gqlgen's `schema:` list reads them. velox-gen owns the files there that
start with its header: `-check` reports them, a run removes those no
longer written, and an application's own `.graphql` files beside them stay.
The directory must be inside the module and beside `-out`, not above or
inside it. Adding `-files` moves the SDL out of `-out`; after dropping or
changing it, delete the old directory's files by hand
([ADR 0169](decisions/0169-plugin-files-directory.md)).

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
