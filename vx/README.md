# vx

A code-generated ORM for large schemas. It reads velox schemas unchanged and
generates far less code for the Go compiler to chew on: on a 450-entity schema
the generated packages compile in about a tenth of the CPU time and a quarter
of the memory velox's do. The measurements behind each decision are in
`benchmarks/fixtures/realistic/README.md`.

Status: experimental. Query, create, update, delete, bulk create,
transactions, every edge kind with eager loading, defaults, validators, hooks,
policies, interceptors, pagination, table migration and the GraphQL layer
(schema, connections, order and where inputs, edge resolvers) work on SQLite
and Postgres. Not there yet: upsert, MySQL, composite ids, external
`ValueScanner`; the generator refuses the last two with an error that says so.

## Generate

```sh
go run github.com/syssam/velox/vx/cmd/vxgen \
    -schema ./schema -target ./db -package example.com/app/db
```

This writes, under `db`:

| package | what is in it |
|---|---|
| `db` | where every query and mutation starts: `db.User.Query(conn)` |
| `db/entity` | the entities and their typed builders |
| `db/user`, ... | one small package per entity: its columns, enum values and edges |
| `db/migrate` | the tables, and `migrate.Create(ctx, conn)` |
| `db/predicate` | the types that keep one entity's conditions out of another's query |
| `db/schemainit` | the schema's defaults, validators, hooks and policies; `db` links it |
| `db/filter` | with `-graphql`: the where inputs of the GraphQL schema |

The root package is named after the directory, so the application picks the
name (`db`, `store`, `data`) by picking the directory. A generation removes the
generated files of an entity that left the schema.

`db`, `db/entity` and `db/schemainit` have **one file per entity**
(`db/entity/user.go`): the definition an editor jumps to is in a file of a
thousand lines, not one of 285,000. The compiler takes the same time either
way; the language server does not (the outline of the single 8 MB file: 3-5 s,
43,000 symbols, 1.3 GB, and no semantic highlighting at that size; of one
entity's file: 1.4-2 s, 0.3 GB). `db/internal/slots` is what lets `entity` and
`schemainit` compile at the same time after a schema edit instead of one after
the other.

## Use

The snippets of this section are `tests/example_test.go`, which `go test`
runs: they compile and print what is shown.

```go
conn, err := orm.Open("postgres", dsn)
err = migrate.Create(ctx, conn) // create or update the tables

// Create, with setters.
alice, err := db.User.Create(conn).
    Set(user.Name.Set("alice"), user.Email.Set("alice@example.com"), user.Age.Set(30)).
    Save(ctx)

// Query: conditions, order, an eager-loaded edge with conditions of its own.
users, err := db.User.Query(conn).
    Where(user.Name.EQ("alice"), user.Age.GT(18)).
    OrderBy(user.CreatedAt.Desc()).
    WithPosts(db.Post.Query(nil).Where(post.Title.NEQ("draft")).OrderBy(post.Title.Asc()).Limit(10)).
    Limit(20).
    All(ctx)

// The neighbors of a row, as a query: conditions, order, a count.
count, err := alice.QueryPosts(conn).Where(post.Title.EQ("first")).Count(ctx)

// Update one row and get it back; update many and get the count.
alice, err = db.User.UpdateByID(conn, alice.ID).Set(user.Age.Add(1)).SaveOne(ctx)
n, err := db.Post.Update(conn).Set(post.ViewCount.Add(1)).Where(post.Author.HasWith(user.ID.EQ(alice.ID))).Exec(ctx)

// By id; a missing row is orm.ErrNotFound.
u, err := db.User.Get(ctx, conn, id)
err = db.User.DeleteByID(ctx, conn, id)
```

- **What a query or mutation runs on is given where it starts and travels
  with the builder**: a database handle or a transaction. A function can build
  a query and another run it. A builder made from `nil` or from a nil
  `*orm.Tx` returns `orm.ErrNoExecutor` when run; the zero value of a builder
  returns `orm.ErrNoEntity`.
- **An entity holds no connection.** `u.QueryPosts(tx)` queries its
  neighbors on what it is given, `WithPosts(...)` loads them with the query
  into `u.Edges.Posts`.
- `db.User` is a package-level variable, not a field of a client struct. A
  struct with a field per entity makes every package that names it read every
  builder of every entity: when it was tried on the 450-entity fixture, the
  312 services that named one took about two and a half times the CPU (556
  CPU-s against 210-234; `benchmarks/fixtures/realistic/README.md`).
- **A column is named like the field of the struct**: `u.Name` is the value,
  `user.Name` the column (`.EQ`, `.Set`, `.Asc`), and `user.ColumnName` the
  string `"name"` for the places that take a name. An edge is `user.Posts`
  (`.Has()`, `.HasWith(...)`, `.Add(ids...)`, `.Remove(ids...)`, `.Clear()`;
  `.Set(id)` on a to-one edge). The type of an enum field is the field's name
  with `Enum`, its values as in velox: `post.StatusEnum`, `post.StatusDraft`;
  `post.StatusValues()` lists them.
  The columns get their names first, so a name that would be taken twice
  goes to the column and the other takes a suffix: an edge called like a
  column is `user.OwnerEdge`, an enum value `user.RoleAdminValue`. Six names
  belong to the package itself (`And`, `Or`, `Not`, `Table`, `ModelName`,
  `EdgeMetas`): a field called `table` is the column `x.TableField`. On a
  real schema of 454 entities, 15,010 columns and 1,245 edges, none of these
  happens once.
- A predicate, setter or order term of one entity does not compile in a query
  or mutation of another, nor does `WithPosts` on a query of an entity without
  that edge.
- **Eager loading is `With<Edge>`**: one more query per edge, whatever the
  number of rows. A query of the target describes the load: its conditions,
  order, nested `With<Edge>` and its limit, which applies per parent. That
  query is not run, so it needs no handle (`db.Post.Query(nil)`).
- **`Exec` on an update or delete touches every matching row and returns the
  count; it is not an error when that is zero.** `SaveOne` is for exactly one
  row (`orm.ErrNotFound`, `orm.ErrNotSingular`); `DeleteByID` reports a
  missing row. There is no `Update().Save`: in velox and Ent that spelling is
  the bulk update.
- `SaveOne` with further conditions is the optimistic lock:
  `UpdateByID(tx, id).Where(x.Version.EQ(v))` is one `UPDATE ... WHERE
  id = ? AND version = ?`, and a row that no longer matches is `ErrNotFound`,
  not written.
- `Columns(user.ColumnName, ...)` on a query reads only those columns into
  the entities, for a list that shows a few columns of a wide table. The
  other fields stay zero, so such an entity is for reading. The id, the
  columns the query is ordered by and the keys of the edges it loads are
  read too. `Select(...)` is the other thing: columns and aggregates scanned
  into a struct of the caller's.
- `SetNillable(p)` sets `*p`, or nothing when `p` is nil.
- `Keep()`, on a column with an update default, leaves it as it is:
  `Set(order.Total.Set(t), order.UpdatedAt.Keep())` is a change that is not
  the user's and does not move `updated_at`.
- `orm.One(q.Exec(ctx))` is for an update that must reach its row and has
  no use for the row: `orm.ErrNotFound` when no row matched, without the
  read `SaveOne` makes. An update with nothing to write (a patch that
  changed nothing) runs no `UPDATE`: it counts the rows its conditions match
  (one `SELECT COUNT`), so the row that is there is still found.
- A time is stored without its monotonic clock reading and to the
  microsecond, and the entity a create returns holds it that way: a guard
  such as `Where(x.UpdatedAt.EQ(u.UpdatedAt))` matches the row it was read
  from on every database.
- The zero predicate, `predicate.User{}`, is no condition: what a function
  returns when it has nothing to add (`orm.P(p).IsZero()` tells). An update
  or a delete refuses it, so that a forgotten condition is not a statement
  over every row.
- There is no `SaveX`, `GetX` or `ExecX`. In a test, `orm.Must(q.Save(ctx))`
  and `orm.Check(q.Exec(ctx))` panic on an error. As methods they would be on
  every builder, and a package pays for the methods of every type it can
  reach.
- `And()` of nothing holds for every row, `Or()` and `Not()` of nothing for
  none: a condition built from a list of filters means the same when the
  list is empty, and a delete of "any of these" over no filter removes
  nothing.
- A create that leaves out a required field, a required edge or an id the
  caller must supply is an `*orm.ValidationError` naming it, before any SQL.
  What the database refuses is an `*orm.ConstraintError` that says which kind
  (`errors.Is(err, orm.ErrUnique)`, `ErrForeignKey`, `ErrCheck`) and what the
  database named as violated. `orm.IsRetryable(err)` is true for a
  serialization failure or a deadlock: the errors for which the transaction,
  as a whole, may be run again.
- The zero predicate (`var p predicate.User`) is no condition in a query,
  which is what a filter built only under some circumstance wants. In an
  update or a delete it is an error: a forgotten assignment must not become a
  statement over every row. To change every row, leave `Where` out.
- Terminals of a query: `All`, `First`, `Only`, `Count`, `Exists`, `IDs`,
  `Iter`, `Paginate`, and `Select(...)` for columns and aggregates.

### Transactions

```go
err := conn.RunTx(ctx, func(tx *orm.Tx) error {
    u, err := db.User.Create(tx).
        Set(user.Name.Set("bob"), user.Email.Set("bob@example.com"), user.Age.Set(40)).
        Save(ctx)
    if err != nil {
        return err
    }
    locked, err := db.User.Query(tx).Where(user.ID.EQ(u.ID)).ForUpdate().Only(ctx)
    if err != nil {
        return err
    }
    _, err = db.User.UpdateByID(tx, locked.ID).Set(user.Age.Set(locked.Age + 1)).SaveOne(ctx)
    return err
})
```

`RunTx` commits when the function returns nil and rolls back when it returns
an error or panics. A mutation of several statements (a row and its join
rows) is wrapped in a savepoint inside a transaction: when it fails, all of
it is undone and the transaction goes on, so an error may be handled and the
rest committed. A query built from
`conn` inside the function runs outside the transaction, as a `*sql.DB` method
would. `RunTxOptions` and `BeginTx` take `*sql.TxOptions` (isolation level,
read-only); with `Begin`, `defer tx.Rollback()` is safe after `Commit`.

### Writing from a struct: `Fill`

A create or an update can take its values from a struct whose fields are named
like the entity's: the request type of an endpoint, a GraphQL input, a row of
an import. Each field says by its type whether it was given:

```go
type createUser struct {
    Name     string
    Email    string
    Age      *int            // nil: not given
    Nickname orm.Opt[string] // absent, null, or a value
    Source   string          `orm:"-"` // not for the row
}
type updateUser struct {
    Name     orm.Opt[string] `json:"name"`
    Age      orm.Opt[int]    `json:"age"`
    Nickname orm.Opt[string] `json:"nickname"`
}

u, err := db.User.Create(conn).Fill(createUser{Name: "carol", Email: "carol@example.com", Age: &age, Nickname: orm.Some("caz")}).Save(ctx)

// A PATCH body: a missing key leaves the column alone, null clears it.
var patch updateUser
err = json.Unmarshal([]byte(`{"age": 31, "nickname": null}`), &patch)
u, err = db.User.UpdateByID(conn, u.ID).Fill(patch).SaveOne(ctx)
```

- A nil pointer, slice or map gives nothing. Anything else is a value and is
  written, zero included.
- An optional gives nothing when absent. Present and null, it sets a column
  that can be NULL to NULL on an update, and gives nothing otherwise. An
  optional is any type with a method `ValueOK() (T, bool)`: `orm.Opt[T]`
  (`orm.Some(v)`, `orm.Null[T]()`; it decodes from JSON), or the one an API
  layer already has.
- A bool field `Clear<Name>` that is true sets the column to NULL.
- **Every field of the struct must name a field of the entity.** A field that
  silently went nowhere would be a write that silently did not happen; this
  way it is an error the first time the code runs. A field that is not meant
  for the row says so in the struct (`orm:"-"`), or is listed after the struct
  by a caller who does not own it: `Fill(in, "Source")`. `orm:"column_name"`
  gives a field named otherwise its column. A value that does not fit the
  field's type, and a field an update may not write, are errors too.
- A later `Set` wins, so a field with a rule of its own (trimmed, normalized,
  "not zero") is skipped in `Fill` and set after it.
- Validators, defaults, hooks and the policy see these writes as they see
  setters.

**An entity is not an input**, and `Fill` refuses one. Its fields cannot say
"not given": `Active: false` is both "store false" and "I did not set it,
store the schema's default". Tried on a real schema, a row created from an
entity stored `is_active = true` where `false` was asked for.

Measured on a create of four fields: 76 allocations with setters, 84 with a
struct of pointers, 86 with a struct of optionals; the time is the statement's
in all three. `Fill` adds two one-line methods per entity to the generated
code and nothing measurable to its compile.

### Large results, pages, aggregates, raw SQL

```go
// One row at a time, without holding the result. Every loop runs the query.
for u, err := range db.User.Query(conn).Where(user.Age.GTE(22)).OrderBy(user.Age.Asc()).Iter(ctx) {
    ...
}

// A page at a time, with a cursor per row: keyset pagination in the shape of
// a Relay connection (after, first, before, last).
page, err := db.User.Query(conn).OrderBy(user.Age.Desc()).Paginate(ctx, nil, &size, nil, nil)
// page.Edges[i].Node, page.Edges[i].Cursor, page.PageInfo.HasNextPage, page.TotalCount
page, err = db.User.Query(conn).OrderBy(user.Age.Desc()).Paginate(ctx, page.PageInfo.EndCursor, &size, nil, nil)

// Columns and aggregates instead of entities; a column that does not exist is an error.
total, err := db.User.Query(conn).Select().Aggregate(orm.Sum(user.ColumnAge)).Int(ctx)
var rows []struct{ Role string `json:"role"`; N int `json:"n"` }
err = db.User.Query(conn).Select().GroupBy(user.ColumnRole).Aggregate(orm.As(orm.Count(), "n")).Scan(ctx, &rows)

// The statement a query would run, and its arguments, without running it.
query, args, err := db.User.Query(conn).Where(user.Age.GT(18)).Select().SQL(ctx)

// A condition written against the SQL builder, and one that arrives at run time.
p := predicate.User(orm.Raw(func(s *sql.Selector) { ... }))
q, err := orm.FieldPredicate(&entity.UserMeta, "age", orm.OpGT, 18)
// A typed predicate as a selector function (for a privacy filter or interceptor).
fn := orm.Compile(orm.P(user.Age.GT(18)))
// Plain statements, on the transaction when given one.
_, err = orm.ExecSQL(ctx, tx, "SELECT pg_advisory_xact_lock($1)", key)
```

### One way to say each thing

A column is one value that does everything: `user.Name.EQ("a")` is a
condition, `user.Name.Set("a")` a setter, `user.Name.Asc()` an order.
Its methods are generated once per column *type* of an entity (about ten per
entity), not once per column and builder. There is no `SetName(v)`: the
per-field methods of velox and Ent were an option here once, and one spelling
was preferred to two. They also cost: with `Set<Field>` alone the `entity`
package of the 450-entity fixture went from 18,958 functions, 14-18 CPU-s and
1.6 GB to 46,348 functions, 26-31 CPU-s and 2.6 GB, on every schema change.

An edge is queried from the row, `u.QueryPosts(tx)`, and only there. That
spelling has a price too, and it was chosen knowing it. A Go package compiles
against everything the types it names expose, method results included:
`(*User).QueryPosts` returns a `*PostQuery`, `User.Edges` names every entity
`User` is connected to, each with such methods, so a package that names one
entity reads the query builder of every entity. With the neighbors queried
from the root package instead (`db.User.QueryPosts(tx, u)`), the 312 services
of the fixture compiled in 106-116 CPU-s; as generated, 141-147. What is paid
is in proportion to the methods of a query type, so that type is kept small.

## GraphQL

```sh
go run github.com/syssam/velox/vx/cmd/vxgen \
    -schema ./schema -target ./db -package example.com/app/db \
    -graphql ./graphql/velox.graphql
```

writes the GraphQL schema of the entities that carry velox's `graphql`
annotations, one file per entity beside `velox.graphql`, and the Go types an
engine binds it to. The schema text is velox's, byte for byte apart from the
Go types it names: on a real 454-entity application the 439 schema files that
had not changed since were identical. The Go is derived from that text, so the
two cannot disagree, and it imports no engine.

| in the schema | in Go |
|---|---|
| `type User`, its scalar fields | `entity.User` and its fields |
| `author: User`, `tags: [Tag!]!` | a method of the entity: `(*Post).Author(ctx)` |
| `posts(after, first, before, last, orderBy, where): PostConnection!` | `(*User).Posts(ctx, after, first, before, last, orderBy, where)` |
| `PostConnection`, `PostEdge`, `PageInfo`, `Cursor` | `entity.PostConnection`, `entity.PostEdge`, `orm.PageInfo`, `orm.Cursor`: what `Paginate` returns |
| `input PostOrder` | `entity.PostOrder` |
| `enum PostOrderField` | `post.OrderField` and its constants, `post.OrderFieldCreatedAt` |
| `input PostWhereInput` | `filter.PostWhereInput` |
| `enum PostStatus` | `post.StatusEnum` and its constants |
| `interface Node` | `db.Noder`; each entity has `IsNode()`, and `db.NodeTypes()` lists them |

A list resolver is a query like any other; the inputs turn into what `Where`
and `OrderBy` take:

```go
func (s *service) List(ctx context.Context, after *orm.Cursor, first *int, before *orm.Cursor, last *int,
    orderBy *entity.PostOrder, where *filter.PostWhereInput,
) (*entity.PostConnection, error) {
    return db.Post.Query(s.db).
        Where(where.P()).
        OrderBy(orderBy.Term()).
        Paginate(ctx, after, first, before, last)
}
```

- `where.P()` of a nil or an empty input is no condition. An input that
  cannot be one (an empty input inside `not`, `and`, `or` or `has...With`;
  nesting deeper than `orm.MaxWhereDepth`) gives a predicate that fails the
  query. Conditions the application adds go in `where.Predicates`, or in
  another `Where`.
- `orderBy.Term()` of a nil input orders by nothing; an order without a
  `Field` orders by the id, in its direction.
- **An edge method takes its filter as an interface**:
  `(*User).Posts(ctx, ..., where interface{ P() predicate.Post })`. What is
  passed is a `*filter.PostWhereInput` (or nil), and another entity's filter
  does not compile. It is not declared as that type because a Go package
  compiles against every type in the signatures of the methods of every type
  it can reach: with the where input there, each package that returns an
  entity read every where input of the schema (a quarter of its compile time
  on a 454-entity schema). An engine has to bind an argument to a parameter
  that is an interface the argument's type implements; gqlc does from the
  change on graphql-go's branch `gqlc-interface-params`.
- The fields a list can be ordered by are constants of the entity's own
  package, `post.OrderFieldCreatedAt`, for the same kind of reason: a package
  pays for every exported name of the packages it imports, and nearly every
  package imports `entity`.
- **The edge methods take the handle from the context.** An engine calls
  `post.Author(ctx)` with the request's context and nothing else, and an
  entity holds no connection, so a server puts the request's handle there
  once: `ctx = orm.WithExecutor(ctx, conn)` (a transaction for a mutation's
  response). Without it they return `orm.ErrNoExecutor`. A to-one edge the
  query already loaded is returned without a query.
- `Paginate` counts the rows of the whole query for `TotalCount`.
  `orm.WithoutTotal(ctx)` skips the count (it is then -1): for a request that
  does not select `totalCount`.
- `db.NodeTypes()` is what a `node(id:)` field goes by when an id does not
  say its type: each type's name, table, and a `Get(ctx, ex, id)` that takes
  an id of any type (an id of another type than the entity's is
  `orm.ErrNotFound`, like an id no row has; an `int64` from an engine finds
  an `int` id). Ask each in turn and take the first that is not
  `orm.ErrNotFound`. That is a query per type, so an application with many
  types keeps what it learns.
- A where input's nesting is limited (`orm.MaxWhereDepth`), counted through
  `has...With` as well as `not`, `and` and `or`.
- `scalar Bytes`, which the schema has when an entity has a bytes field, is
  left for the engine to bind, as `Time` is (velox binds it to a type of its
  gqlgen runtime).
- Not there yet, and refused at generation with a message that says so: an
  entity whose GraphQL type is renamed (`graphql.Type` on the entity),
  ordering by the number of neighbors on an edge, filters on the elements of
  a JSON array, and whatever else the schema would bind to Go this generator
  does not write (the interface of `graphql.InterfaceField`, the scalar of a
  typed JSON field, the accessor of a nillable bytes field). Every `@goModel`
  that names a package this generator writes, and every `@goField(name:)` of
  an entity's type, is checked against what was generated; a package of the
  application's own under the same directory is not this check's business. An entity with no field a list could be ordered by is
  refused by the schema generator itself (an order enum without values):
  give it one, or leave it out of the schema. Mutation inputs are the application's own types: move them to
  a builder with `Set` or `Fill`. Loading edges by what a request selects is
  not done: each edge is a query when it is asked for.

What it costs to compile is in `benchmarks/fixtures/realistic/README.md`.

## Hooks, policies, interceptors

The schema's `Hooks()` and `Policy()` (and its mixins') are used as they are.
A hook receives `*orm.Mutation`, one type for every entity, through the
`velox.Mutation` interface: `m.Field("workspace_id")`,
`m.SetField("workspace_id", id)`, `m.OldField(ctx, "status")`.

**A hook that type-asserts the mutation to an interface of typed accessors
(`SetWorkspaceID(int64)`, `WorkspaceID() (int64, bool)`) is refused at
generation.** That is how Ent's documentation writes a hook many entities
share, and here no mutation has such methods: the assertion would fail for
every entity and the hook would do nothing, with no error (the tenant of a
new row not written). `vxgen` reads the source of the schema package and of
the packages of its module it uses, and names each assertion on a
`velox.Mutation`, `velox.Query` or `privacy.Filter` that can never hold.
Rewrite it with `Field`/`SetField`, which work with velox's generated code
too, so it can be done before moving.

The check follows an assertion through another name for the same value
(`any(m)`, an interface that embeds `velox.Mutation`) and through a generic
helper (`as[T](m)`, whether its parameter is a `velox.Mutation` or `any`,
judged where `T` is given). It reads these spellings; it is not a proof. A
mutation put in a variable of type `any` first, or handed through two
helpers, is not followed, and such an assertion still fails silently. A case
of several types is fine when one of them can hold. Code that tries the typed
accessors first and goes by name when the assertion fails is correct under
both generators, and only its author knows that it is that: mark the
assertion with `//vx:nocheck` at the end of its line, or alone on the line
above. A schema package that does not type-check is an error, not a pass.

A filter rule shared by many entities asks the filter whether the table has
the column it filters on: `f.(interface{ HasColumn(string) bool })`, on
queries and mutations alike.

`conn.Use(hooks...)` and `conn.Intercept(interceptors...)` register on the
handle, at start-up. An interceptor is handed `*orm.Query`, one type for
every entity (`Type()`, `HasColumn`, `WhereP`, `Limit`); `orm.TraverseFunc`
and `orm.InterceptFunc` make one of a function of it. A policy covers every read (terminals, eager loads,
traversals, edge predicates) and every create, update and delete of its own
entity; an interceptor covers reads only, so rules about which rows a caller
may touch belong in the policy. As in Ent, changing an edge writes the other
side's key or a join row without asking the other entity's policy or hooks:
`user.Pets.Add(id)` rewrites `pets.owner_id` under User's policy only.

The root package links `schemainit`, so a program that starts its queries at
`db.<Entity>` has all of this. One that uses `db/entity` alone must import
`db/schemainit` itself; until it does, an entity whose schema has defaults,
validators, hooks or a policy returns an error from every query and mutation.

## Code written once for several entities

The generated code has no generic types. A function that builds predicates
for whichever entity it is used with is generic over the predicate type,
under the constraint `orm.Predicate`, and sees a column as an interface of
the methods the column types share: `orm.Column[T, P]`,
`orm.OrderedColumn[T, P]`, `orm.TextColumn[P]`, `orm.NullableColumn[P]`.

```go
func recent[P orm.Predicate](created orm.OrderedColumn[time.Time, P], since time.Time, more ...P) P {
    return orm.And(append(more, created.GTE(since))...)
}

posts, err := db.Post.Query(conn).Where(recent(&post.CreatedAt, since)).All(ctx)
```

A column is passed by its address (the methods of a column type have
pointer receivers), and the predicate type is inferred from it, so the
result is a `predicate.Post` that a query of users does not accept.
`orm.And`, `orm.Or` and `orm.Not` are the entity packages' own, for any
entity; under the constraint a predicate converts to `orm.P` and back, which
is how such a function wraps a condition on the SQL builder
(`P(orm.Raw(f))`) or returns none (`*new(P)`).

## Moving an application from velox

`cmd/vxmigrate` rewrites an application written against velox's generated
code. Run it in the application's module while the old generated code is
still there (it reads the application with types, and needs them), then
generate with `vxgen` and build:

```sh
go run github.com/syssam/velox/vx/cmd/vxmigrate -generated example.com/app/velox -write -report todo.txt
```

It does not compile anything, so a large application is read in seconds. On
one of 455 entities and 9,400 files it rewrote 90,400 references and left
some 425 lines for a person, each with the reason (170 of them the warnings
in the table below; the rest what it cannot rewrite): queries
changed through `Modify`, the application's own plumbing around the client
(a client in a context, its runtime configuration), cursors built by hand,
and how the application opens its database. An edge
query from an entity that was handed to a function is given the handle of
the context, and reported, since only a person knows whether every caller's
context carries one.
Its package comment lists what it rewrites.

The report is also where the tool says that code which still compiles would
do something else, when it cannot keep what the code did. Read those lines
first:

| Reported | Why |
|---|---|
| a value asserted to the query or mutation type of one entity | every entity's is `*orm.Query` and `*orm.Mutation` now: the assertion holds for all of them, or for none; test `Type()` |
| `Or` or `Not` of a list that may be empty, or taken as a value | velox's matched every row, `orm.Or()` matches none |
| `p, err := where.P()` | an input with no condition was an error and is no condition now |
| `Limit` in the option of an eager load | it was over all neighbors read, and is for each parent now |
| a method value (`b.SetName`, `c.User.Get`) whose receiver is not a variable assigned once, unless it is handed straight to a call from a variable | the function in its place evaluates the receiver when called |
| a field or a constructor of velox's error types | the runtime's are `orm.ErrNotFound`, `orm.ErrNotSingular`, `*orm.ValidationError` (`Name` is `Field`, rewritten), `*orm.ConstraintError` |
| a package of the generated tree the new one does not have (`hook`, something of the application's own kept there) | once, at its first reference; the summary of the run counts the rest |
| several orders or filters given to `Paginate` | velox used the last of each |
| `tx.Client()` handed on as a value | it is the transaction; what takes it must take `orm.Executor` |
| a GraphQL edge method called on an entity (`u.Posts(ctx, ...)`) | it reads on the handle of the context |
| a deferred call that came to be wrapped (`defer q.ExecX(ctx)`) | it is put in a function so that it still runs later; its arguments are evaluated then |

What it keeps without a word: `IsNotFound`, `IsNotSingular`,
`IsConstraintError`, `IsValidationError` and `IsNotLoaded`, and the values
`ErrNotFound` and `ErrNotSingular`, of the generated package or of the
library, become the runtime's (velox's never hold for an error of the new
code). An edge query is given the handle its entity was read on only when
the text settles it: the entity's variable is given its value in one place,
before the query, nothing is written through it and `Unwrap` is not called
on it, and the handle's variable is not given another value. Otherwise it
gets the context's handle and a line in the report.

What it does not see, and a person must: a query or mutation type of one
entity given to a generic function as a type argument (`as[*UserMutation](m)`
holds for every entity afterwards), and a method value whose receiver a
function it was handed to keeps for later.

Before moving, rewrite the hooks that assert a mutation to typed setters
(see above); `vxgen` refuses them, and the rewritten form works with velox.

## Go conventions

The API was checked against the Go team's guidance (Code Review Comments,
Effective Go, the blog posts on package names, contexts in structs, when to
use generics and module compatibility, the `iter` and `database/sql`
documentation), and the generated code passes `revive`'s `exported`,
`package-comments`, `receiver-naming`, `error-strings`, `context-as-argument`
and `var-naming` rules, the last one for every name the generator chooses: an
entity the schema calls `Http` stays `Http`. Where it departs, on purpose:

- `All(ctx)` returns a slice, as in Ent and velox; the iterator, which Go's
  convention for collections would call `All`, is `Iter`.
- A column descriptor is a named string type whose methods have pointer
  receivers: a method with a value receiver is compiled twice (measured: 1.7
  times the compile of the descriptor packages).
- Conditions are methods of the column (`user.Name.EQ("a")`), although
  functions (`orm.EQ(user.Name, "a")`) would compile ten times faster and
  keep the same checks: the descriptor packages are about 40 CPU-s of a cold
  build (4 as functions) and none of an incremental one. The methods read
  better, are found by typing a period, and give shorter errors. Ten designs
  are compared in `benchmarks/fixtures/realistic/README.md`.
- The package of an entity is named after it (`user`, `order`, `time`), which
  takes a name the caller may want for a variable or already has for a
  standard package. Import it under another name where that happens. One
  package per entity is what keeps a change to one entity from recompiling
  the others.
- `entity.<Entity>Meta`, the `New<Entity>...` constructors, a per-entity
  package's `EdgeMetas` and the fields of `orm.QueryCore`, `orm.MutCore` and
  `orm.Meta` are exported for the generated packages, which are separate
  packages; they are not for application code.
- `Fill` takes `any` and uses reflection, as `encoding/json` does: its inputs
  are types this module has never seen.
- The schema's hooks and policies reach the entities through package-level
  variables filled by an `init`, linked by a blank import: the
  `database/sql` driver pattern. They are not changed after start-up.
- `orm.Opt` has value and pointer receivers, as `time.Time` has: decoding
  needs the pointer.

## Building

On the 450-entity fixture, with four compiles at a time on a busy machine
(`benchmarks/compile-bench.sh vx cold app`): the generated packages compile in
88-100 CPU-s, the largest (`entity`) in 1.3-1.5 GB; 312 services on top of
them in 138 CPU-s, 0.42 s and 104-121 MB for the median one. velox on the same
schema: 1,098 and 1,151 CPU-s. The language server checks one service in 3.0
GB (velox: 12.2 GB).

The Go compiler runs with its default GC setting. Measured on an earlier
version of the generator and a 454-entity schema, `GOGC=200 go build ./...`
took a sixth off the CPU time of the ORM packages and a third off the wall
time, `GOGC=400` a quarter and 40%, at the price of memory: the largest
package went from 1.7 GB to 2.0 GB and 2.5 GB. The compile of `entity` spends
a quarter of its CPU in the garbage collector (`go tool compile` takes
`-cpuprofile` and `-traceprofile`; pass them with `-gcflags=<pkg>=...`). Set
`GOGC` in the build environment, not in the program.

## Tests

```sh
go test -race ./...                                  # SQLite
VX_TEST_POSTGRES='postgres://...' go test ./tests/   # Postgres
```

`tests/` compares against velox on the same schema (a randomized
differential test, and the JSON of the same rows) and runs the examples above;
`tests/shapes/` runs 85 schema shapes, what must not compile, and the schemas
the generator refuses; `gen` regenerates both trees in-process and fails when
the checked-in code is not what the generator writes; `tests/bench_test.go`
compares allocations with velox. The suites cover 94% of the statements of
`orm` and 92% of `gen`.
