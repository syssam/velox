# Coming from ent

velox's schema DSL follows ent's: `schema.Schema`, `Fields`, `Edges`,
`Indexes`, `Mixin`, and `field.*`, `edge.*` and `index.*` builders with ent's
names. Most ent schemas port by changing imports. A few rules differ, because
velox inserts and updates Go structs, where a zero field means unset. This page
lists the differences that change what a schema means or what a call does.
[Decision 0029](decisions/0029-ent-refusals.md) says which ent schemas velox-gen
refuses, and why.

## Fields

**`Optional` is NOT NULL.** In ent, `Optional()` makes the column NULL-able. In
velox it means only that the field need not be given on create: the column
stays NOT NULL, the Go field is a value, and an unset field writes its `Default`
or its zero value. For a NULL-able column write `Optional().Nillable()`, which
makes the Go field a pointer and adds `Null*` handles (`IsNull`, `SetNull`).
This is deliberate ([0003](decisions/0003-nullability.md)). An `Optional` JSON
field is the one exception: its column is NULL-able. velox-gen refuses an
`Optional` field whose zero value cannot be stored or means nothing:

- an `Optional` time, enum or bytes field needs `Nillable` or a `Default` (for
  time and bytes, a `DefaultFunc` such as `time.Now` counts);
- an `Optional` `Unique` field needs `Nillable` or a `DefaultFunc`, or every
  unset row would hold the same value.

When `migrate diff` adds a column to a table that has rows, give the field a
`Default` or make it `Optional`: a NOT NULL column with no default fails on
those rows, and `-allow add-required-column=...` only helps on an empty table.

**IDs are int64.** The implicit `id` is `field.Int64`, where ent's is `int`. A
foreign-key field must have its target's ID type: declare it `field.Int64`, or
declare the target's ID with `field.Int("id")`.

**Defaults on bool and number.** A struct's `false` or `0` cannot be told from
unset, so:

- `Default(false)` or `Default(0)` on a field that is not `Nillable` is allowed.
  It stores what unset stores.
- A non-zero `Default`, or any `DefaultFunc`, on a bool or number that is not
  `Nillable` is refused. ent fills `Default(true)` only when the builder never
  set the field. velox would store `true` where your struct said `false`. The
  ways out: make the field `Nillable`, name it so that its zero is the default
  (`disabled` rather than `enabled`), or drop the `Default`.
- For GraphQL, `graphql.CreateDefault(true)` renders `active: Boolean! = true`
  in the create input. gqlgen fills it in when a client leaves the field out,
  and the column keeps no default.

The same holds for a string: with `Default("x")`, an insert can never store `""`.

## Edges

**Name the foreign key with `Field`.** ent adds a hidden foreign-key column
when an edge has no `Field`. velox-gen does too, and names its Go field after
the column, such as `UserPosts` for `user_posts`. Prefer to declare the key and
bind it:

```go
// Post
field.Int64("author_id"),
edge.From("author", User.Type).Ref("posts").Unique().Required().Field("author_id"),
```

**An optional edge's field is `Nillable`.** When the edge is not `Required`,
its field must be `Optional().Nillable()`: an unset foreign key is NULL, and a
value field would write 0, which no row's key is. A `Required` edge's field is
not `Optional`.

**Many-to-many links are join rows.** An `edge.To` whose inverse is not
`Unique` either is many-to-many. velox-gen writes the join entity (`PostTag`
for `post_tags`), and you link and unlink by inserting and deleting its rows.
The core API has no `AddTagIDs` or `RemoveTagIDs`; the GraphQL update input
has `add<Edge>IDs` and `remove<Edge>IDs`.
[examples/relations](../examples/relations/README.md#many-to-many) shows how.

**Relations load only when asked.** `With(P.Author)` loads one with one more
query, and `Get()` reports whether it was loaded. Nothing loads lazily, and
there is no `QueryPosts()`: read the posts with `db.Query[gen.Post]()` and a
condition, or use `A.Posts.Has()` and `HasWhere` in a `Where`.

## Queries and writes

| ent | velox |
| --- | --- |
| `client.User.Create().SetName("a").Save(ctx)` | `db.Insert(&gen.User{Name: "a"}).Exec(ctx)` |
| `client.User.Query().All(ctx)` | `db.Query[gen.User]().Unbounded().All(ctx)`, or with a `Limit` |
| `client.User.Update().Where(...).SetAge(3).Save(ctx)` | `db.Update[gen.User]().Where(...).Set(U.Age.Set(3)).Exec(ctx)` |
| `client.User.Delete().Exec(ctx)` (every row) | `db.Delete[gen.User]().All().Exec(ctx)` |

A `Query` with neither a `Limit` nor `Unbounded()` fails with `ErrUnbounded`.
An `Update` or `Delete` with neither a `Where` nor `All()` fails with
`ErrNoWhere`. The names differ because they mean different things: `Unbounded`
lifts a read's row limit, and `All` confirms a write to every row.

Policies and hooks are methods you write on the generated entity, in your own
file in `gen`, not schema methods (see the package documentation).

On SQLite, `velox.Connect` turns foreign keys on for every connection. A
`*sql.DB` you pass to `velox.Open` is used as it is, so put
`_pragma=foreign_keys(1)` in its DSN.

## GraphQL (contrib/graphql, compared with entgql)

- **A null in an update input clears the field.** entgql ignores a null for an
  optional field, so only `clear<Field>: true` sets NULL. In velox, a null given
  to a nullable field sets it to NULL, as `clear<Field>` does, and a field left
  out is left as it is. A field that cannot be NULL has no `clear<Field>`, and
  a null given to it counts as left out. A null given to an edge's `<edge>ID`
  is refused, with a message naming `clear<Edge>`.
- **JSON lists are GraphQL lists.** A JSON field of `[]string`, `[]int`,
  `[]int64`, `[]float64` or `[]bool` (`field.Strings` and the like) is
  `[String!]` and so on, as in entgql, in the type and in the inputs. Other JSON
  is the `JSON` scalar.
- `CreateDefault` stands in for a non-zero `Default` on a bool or number, as
  shown above.
