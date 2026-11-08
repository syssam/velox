# Coming from ent

velox's schema DSL follows ent's: `schema.Schema`, `Fields`, `Edges`,
`Indexes`, `Mixin`, and `field.*`, `edge.*` and `index.*` builders with ent's
names. Most ent schemas port by changing imports. A few rules differ, because
velox inserts and updates Go structs, where a zero field means unset. This page
lists the differences that change what a schema means or what a call does.
[Decision 0032](decisions/0032-ent-refusals.md) says which ent schemas velox-gen
refuses, and why.

## Finding schemas

velox-gen finds schemas by ent's rule. A schema is an exported, non-generic
struct type of the schema package that implements `schema.Interface`. It may
implement it by value or through a pointer, so every method may have a
pointer receiver. It may declare the methods itself or have them from an
embedded struct, at any depth: embedding `schema.Schema` is the usual way.
Only the files of the package's build are read, as by ent: a `_test.go` file
is not, and nor is a file no build includes (`//go:build ignore`).

velox-gen refuses, at the type's line, what looks like a schema and is not
one, where ent would skip it or fail later:

- **A schema that embeds another schema.** ent makes both entities, so
  `type Release struct{ Base }` with an exported `Base` gives a `bases` table
  too. velox-gen refuses it. A shared base is unexported, which is never a
  schema: `type base struct{ schema.Schema }`, with the methods all its
  schemas share, such as a `Mixin`. To share fields, a mixin is the usual way.
- **An unexported struct that embeds `schema.Schema` and that no type
  embeds.** It is likely a schema with a lower-case name.
- **`*schema.Schema` embedded**, at any depth: its methods panic on the nil
  pointer.
- **A generic type, an alias or a non-struct type** that implements
  `schema.Interface` or embeds `schema.Schema`.
- **A method of another signature**, such as `Fields() []ent.Field`, or a
  field named like a method, which hides `schema.Schema`'s.
- **A file of the schema package that only some builds include**, behind a
  tag or named for a system: the model would depend on the build.

A schema's methods may be in any file of the package. A many-to-many join
entity has no schema; its `Policy` or `Hooks` go on an empty struct of its
name (`type UserFriend struct{}`), which is not a schema.

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

**`field.Strings` is a JSON column, as in ent.** `field.Strings`, `field.Ints`
and `field.Floats` are JSON fields of `[]string`, `[]int` and `[]float64`, and
`field.JSON(name, T)` takes any other type. The column is `jsonb` on
PostgreSQL, `json` on MySQL and `text` on SQLite, and `migrate diff` writes it
on each. As said above, an `Optional` one is NULL-able, and a nil slice stores
NULL. A required one is NOT NULL: a nil slice stores the JSON `null` and reads
back nil, where ent's create refuses the field unset. Its handle, a
`velox.JSONCol`, sets it and reads its assigned value in a hook; velox
neither orders by a JSON column nor compares one.

**The implicit ID is int64.** The implicit `id` is `field.Int64`, where
ent's is `int`. A foreign-key field must have its target's ID type: declare it
`field.Int64`, or declare the target's ID with `field.Int("id")`. A schema may
declare its own `id` of any key type, as in ent: `field.UUID("id", uuid.UUID{})`
with a `Default`, or `field.String("id")` for a key the application chooses.
Relations, many-to-many links, upserts and `After` work on such keys on all
three dialects (`TestNonIntegerKeys`).

**Defaults on a NOT NULL column.** A `Default` on a field that is not
`Nillable` is accepted for a string, an enum, a time (`Default(time.Now)`), a
JSON field such as `field.Strings` and a bool or number whose `Default` is its
zero value. An unset field takes the `Default` on insert, as in ent, and the
literal `Default` of a string, enum, bool or number is the column's `DEFAULT`
too. One case is refused: a non-zero `Default`, or any `DefaultFunc`, on a
bool or number that is not `Nillable`. A struct's `false` or `0` cannot be
told from unset, and ent fills
`Default(true)` only when the builder never set the field, so velox would
store `true` where your struct said `false`. A refusal is safer than that
silent overwrite. For `field.Bool("active").Default(true)` the message offers,
spelled with the field's own name and value:

| You want | Write |
| --- | --- |
| unset stores `true` | `field.Bool("active").Nillable().Default(true)`: a `*bool`, and the column holds NULL |
| ent's column, `NOT NULL DEFAULT true` | `field.Bool("active").SchemaDefault(map[string]string{field.Postgres: "true", field.MySQL: "1", field.SQLite: "1"})`, and set `Active: true` in Go |
| a GraphQL create input that defaults | `graphql.CreateDefault(true)` on the field: `active: Boolean! = true` |
| no default | a name whose zero is the default (`disabled` for `enabled`), or drop the `Default` |

`Default(false)` or `Default(0)` is allowed, as it stores what unset stores.
`SchemaDefault` is the column's default only: velox's inserts write every
column, so only rows written outside velox take it; the column is an ent
database's `NOT NULL DEFAULT true`. `CreateDefault` is filled in by gqlgen
when a client leaves the field out.

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

**Relations are in an Edges struct, as in ent.** A post's author is
`post.Edges.Author`, of `PostEdges`, tagged `json:"edges,omitzero"`. Each
edge is a `velox.One[T]` or `velox.Many[T]` whose `Get()` returns the value
and whether it was loaded, so there is no `AuthorOrErr`.

**A required field is always in JSON.** ent tags every field
`json:"name,omitempty"`, so a stored 0, false or "" is left out. velox leaves
out only what can be absent: a required scalar is `json:"name"`; a pointer,
an Optional field, a slice or a map is `omitempty`; a time, UUID or other
struct is `omitzero`. A field's `StructTag` replaces the tag, as in ent.

**Relations load only when asked.** `With(P.Author)` loads one with one more
query, and `Get()` reports whether it was loaded. Nothing loads lazily, and
there is no `QueryPosts()`: read the posts with `db.Query[gen.Post]()` and a
condition, or use `A.Posts.Has()` and `HasWith` in a `Where`.

## Queries and writes

| ent | velox |
| --- | --- |
| `client.User.Create().SetName("a").Save(ctx)` | `db.Insert(&gen.User{Name: "a"}).Exec(ctx)` |
| `client.User.Query().All(ctx)` | `db.Query[gen.User]().Unbounded().All(ctx)`, or with a `Limit` |
| `client.User.Update().Where(...).SetAge(3).Save(ctx)` | `db.Update[gen.User]().Where(...).Set(U.Age.Set(3)).Exec(ctx)` |
| `client.User.UpdateOneID(id).SetAge(3).Save(ctx)` | `db.Update[gen.User]().Where(U.ID.EQ(id)).Set(U.Age.Set(3)).One(ctx)`, the row as written |
| `client.User.Delete().Exec(ctx)` (every row) | `db.Delete[gen.User]().All().Exec(ctx)` |

A `Query` with neither a `Limit` nor `Unbounded()` fails with `ErrUnbounded`.
An `Update` or `Delete` with neither a `Where` nor `All()` fails with
`ErrNoWhere`. The names differ because they mean different things: `Unbounded`
lifts a read's row limit, and `All` confirms a write to every row.

Policies, hooks and interceptors are schema methods, as in ent, and name
columns by name, as ent's `ent.Policy`, `ent.Hook` and generic
`ent.Mutation` (`Field`, `SetField`) do: `func (User) Policy() velox.Policy`,
`func (User) Hooks() []velox.Hook` and
`func (User) Interceptors() []velox.Interceptor` (decisions 0039 and 0040).
A condition is a `velox.Predicate`, built by ent's names:

| ent (generic `sql` predicates) | velox |
| --- | --- |
| `sql.FieldEQ("org_id", v)` | `velox.FieldEQ("org_id", v)` |
| `sql.FieldNEQ`, `FieldGT`, `FieldGTE`, `FieldLT`, `FieldLTE` | `velox.FieldNEQ`, `FieldGT`, `FieldGTE`, `FieldLT`, `FieldLTE` |
| `sql.FieldIn(name, vs...)`, `FieldNotIn` | `velox.FieldIn(name, vs...)`, `FieldNotIn` |
| `sql.FieldIsNull(name)`, `FieldNotNull` | `velox.FieldIsNull(name)`, `FieldNotNull` |
| `sql.FieldContains`, `FieldHasPrefix`, `FieldHasSuffix`, `FieldEqualFold` | `velox.FieldContains`, `FieldHasPrefix`, `FieldHasSuffix`, `FieldEqualFold` |
| `sqlgraph.HasNeighbors` (`user.HasPets()`) | `velox.HasEdge("pets")` |
| `sqlgraph.HasNeighborsWith` (`user.HasPetsWith(...)`) | `velox.HasEdgeWith("pets", ps...)` |
| `sql.AndPredicates`, `OrPredicates`, `NotPredicates` | `velox.And`, `velox.Or`, `velox.Not` |
| `privacy.Allow` | `velox.Allow()` |

ent's `sql.FieldContainsFold` has no velox form: the engine has no
case-insensitive substring match. A `velox.Predicate` is a `Pred` of no
entity, so `And`, `Or` and `Not` take it, and `velox.Predicate(p)` turns a
typed handle's `Pred[E]` into one. Its zero value fails the statement: it
is a struct, not an interface, so it cannot be nil, and a policy that
returns `velox.Predicate{}` with its error is refused, never read as
"no condition". A hook reads and writes a row with `m.Value`, `m.Set` and `m.Fill`, and a
policy's `Insert` reads it with `r.Value`. velox-gen refuses a constant name
the entity has no column of, at its line, and a constant value that does
not fit the column; any other name the entity has no column of fails the
statement. A velox interceptor is a row filter, not ent's query
middleware: its condition reaches every read and write of the entity, a
relation's included, before the Read policy, and `WithoutPolicy` does not
lift it. A hook that must use the typed API of `gen` (another entity, say)
is a run-time hook, `velox.WithHooks[gen.Task](...)`, a DB option.

A mixin declares behaviour as ent's does, with the same plain methods.
velox reads them for every entity that lists the mixin, mixins first,
in `Mixin()` order, then the entity's own. The policies are ANDed, so none
lifts another. Tenancy is such a mixin, over package `scope` (decisions
0038 and 0039), as in ent's privacytenant example:

```go
var OrgKey = scope.NewKey[int64]("org")

type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []schema.Field { return []schema.Field{field.Int64("org_id").Immutable()} }

func (InOrg) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Predicate, error) {
			org, err := OrgKey.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ("org_id", org), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error { return OrgKey.Match(ctx, r.Value("org_id")) },
	}
}

func (InOrg) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if org, ok := OrgKey.From(ctx); ok {
			return m.Fill("org_id", org)
		}
		return nil
	}, velox.OpInsert)}
}
```

Soft delete is `softdelete.Mixin{}` from `schema/mixin/softdelete`: a delete
sets `deleted_at`, and `softdelete.Skip(ctx)` shows deleted rows and makes a
delete hard. `scopetest.Isolates` checks, in a test, that an entity is
confined, and that its column is immutable, NOT NULL and of the key's type.

The layout is ent's, with no runtime package (decisions 0037, 0039 and
0040): `gen` holds every entity, handle and descriptor, and imports the
schema package by its own name for its defaults, validators and
behaviour. A descriptor names the schema's value, `Schema: schema.Issue{}`,
and velox reads the behaviour from it as ent's generated runtime reads
`schema.Issue{}.Policy()`: each `Mixin()` value's, in order, then the
schema's own. An entity with no behaviour, its own or a mixin's, names no
schema. There is no `gen/runtime` and no blank import. The schema package may not
import `gen`, directly or through another package: velox-gen refuses it,
with the chain. A schema compiles before `gen` exists, so no
`schema/snapshot` is needed. Plugins write into `gen`.

On SQLite, `velox.Connect` turns foreign keys on for every connection. A
`*sql.DB` you pass to `velox.Open` is used as it is, so put
`_pragma=foreign_keys(1)` in its DSN.

On SQLite a time field is a `datetime(9)` column, where ent's is `datetime`.
velox writes every time as UTC text with nine fractional digits and parses it
itself; the driver parses a `datetime` column, slower, and velox reads that
too. `migrate diff` on a database ent made plans the change to `datetime(9)`,
a table rebuild on SQLite.

## GraphQL (contrib/graphql, compared with entgql)

- **Inputs, not mutations, as in entgql.** velox-graphql writes
  `Create<T>Input` and `Update<T>Input`, and no `type Mutation`: the
  application declares its mutations in its own `.graphql` file and writes
  the resolvers. Where entgql's resolver calls
  `client.Todo.Create().SetInput(input).Save(ctx)`, velox's calls
  `input.Setters()`, which returns velox setters, and writes them with
  `db.Insert(x).Set(set...)`, `x` the row the resolver has already set what
  the client must not choose on, or
  `db.Update[T]().Where(...).Set(set...).One(ctx)`, ent's `UpdateOne`.
  As in entgql, a null in an input is a field not given; `clear<Field>:
  true` sets NULL. No delete is generated: a delete is the application's
  `db.Delete[T]()`.
- **A connection is NewXConnection of a query.** Where entgql's resolver
  calls `client.Todo.Query().Paginate(ctx, after, first, before, last,
  ent.WithTodoOrder(orderBy), ent.WithTodoFilter(where.Filter))`, velox's
  calls `gen.NewTodoConnection(ctx, db.Query[gen.Todo](), after, first,
  before, last, orderBy, where)`, with `db` from `veloxgql.DB(ctx)`, as
  entgql's `ent.FromContext(ctx)`. velox has no per-entity query type to
  hang a `Paginate` method on, and the connection is lazy: it sends
  nothing until gqlgen resolves its fields. The query may carry the
  application's own filters; it must not be ordered. A where input's
  `Pred(ctx)` is entgql's `P()`: a `velox.Pred[T]` for any query.
- **JSON lists are GraphQL lists.** A JSON field of `[]string`, `[]int`,
  `[]int64`, `[]float64` or `[]bool` (`field.Strings` and the like) is
  `[String!]` and so on, as in entgql, in the type and in the inputs. Other JSON
  is the `JSON` scalar.
- `CreateDefault` stands in for a non-zero `Default` on a bool or number, as
  shown above.
- **Nullable order fields sort NULL last.** An `OrderField`, or an `OrderVia`
  of a to-one edge's target, may be on a `Nillable` field. NULL sorts after
  every value ascending and before every value descending, on PostgreSQL,
  MySQL and SQLite alike, and a cursor holds it as null, so cursor pages over
  NULL keys are exact. ent leaves the place of NULL to the database, which on
  MySQL and SQLite is first ascending, and offers `sql.OrderNullsFirst()`;
  velox has one place for NULL and no such option. The core's `OrderBy`, and a
  joined column's `rel.By`, sort NULL the same way.
