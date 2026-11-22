# Coming from ent

velox's schema DSL follows ent's: `velox.Schema`, `Fields`, `Edges`,
`Indexes`, `Mixin`, and `field.*`, `edge.*` and `index.*` builders with ent's
names. Most ent schemas port by changing imports. A few rules differ, because
velox inserts and updates Go structs, where a zero field means unset. This page
lists the differences that change what a schema means or what a call does.
[Decision 0032](decisions/0032-ent-refusals.md) says which ent schemas velox-gen
refuses, and why. A veloxe application reads this page first, then
[coming-from-veloxe.md](coming-from-veloxe.md).

## Finding schemas

velox-gen finds schemas by ent's rule. A schema is an exported, non-generic
struct type of the schema package that implements `schema.Interface`. It may
implement it by value or through a pointer, so every method may have a
pointer receiver. It may declare the methods itself or have them from an
embedded struct, at any depth: embedding `velox.Schema` is the usual way.
Only the files of the package's build are read, as by ent: a `_test.go` file
is not, and nor is a file no build includes (`//go:build ignore`).

velox-gen refuses, at the type's line, what looks like a schema and is not
one, where ent would skip it or fail later:

- **A schema that embeds another schema.** ent makes both entities, so
  `type Release struct{ Base }` with an exported `Base` gives a `bases` table
  too. velox-gen refuses it. A shared base is unexported, which is never a
  schema: `type base struct{ velox.Schema }`, with the methods all its
  schemas share, such as a `Mixin`. To share fields, a mixin is the usual way.
- **An unexported struct that embeds `velox.Schema` and that no type
  embeds.** It is likely a schema with a lower-case name.
- **`*velox.Schema` embedded**, at any depth: its methods panic on the nil
  pointer.
- **A generic type, an alias or a non-struct type** that implements
  `schema.Interface` or embeds `velox.Schema`.
- **A method of another signature**, such as `Fields() []ent.Field`, or a
  field named like a method, which hides `velox.Schema`'s.
- **A file of the schema package that only some builds include**, behind a
  tag or named for a system: the model would depend on the build.

A schema's methods may be in any file of the package. A plain many-to-many
join entity has no schema; its `Policy` or `Hooks` go on an empty struct of
its name (`type UserFriend struct{}`), which is not a schema. An edge schema,
which `Through` names, is a schema, and its behaviour is its own (see Edges).

## Fields

**`Optional` is NOT NULL.** In ent, `Optional()` makes the column NULL-able. In
velox, as in veloxe, it means only that the field need not be given on create:
the column stays NOT NULL and the Go field is a value. `Nillable()` is what
makes a column NULL-able, a pointer in Go with `Null*` handles (`IsNull`,
`SetNull`), and `Default` is what an unset field takes. An unset `Optional`
field with neither is written as its Go zero value, `""`, `0`, `false` or a
zero time, as veloxe wrote it ([0003](decisions/0003-nullability.md),
[0090](decisions/0090-veloxe-defaults.md)). An `Optional` enum is the
exception, whose zero is none of its values: velox-gen asks it for a `Default`
or `Nillable`. An `Optional` JSON field's column is NULL-able; unset, a slice
or a map is written empty, `[]` or `{}`, a struct as its zero value's JSON,
and a pointer as its element's zero value's JSON.
An `Optional` `Unique` field needs `Nillable` or a `DefaultFunc`, or every
unset row would hold the same value.

A string, bool or number column that is NOT NULL and has no `Default` gets
its zero as its column `DEFAULT`, as veloxe's `FeatureAutoDefault` gave it, so
`migrate diff` can add it to a table that has rows. Any other NOT NULL column
with no default fails on those rows, and `-allow add-required-column=...` only
helps on an empty table.

**`field.Strings` is a JSON column, as in ent.** `field.Strings`, `field.Ints`
and `field.Floats` are JSON fields of `[]string`, `[]int` and `[]float64`, and
`field.JSON(name, T)` takes any other type. The column is `jsonb` on
PostgreSQL, `json` on MySQL and `text` on SQLite, and `migrate diff` writes it
on each. As said above, an `Optional` one is NULL-able, and a nil slice stores
NULL. A required one is NOT NULL: a nil slice stores the JSON `null` and reads
back nil, where ent's create refuses the field unset. Its handle, a
`velox.JSONCol`, sets it and reads its assigned value in a hook. velox
neither orders by a whole JSON document nor compares one. Where ent writes
`sqljson.ValueEQ(user.FieldAttrs, "pro", sqljson.Path("plan", "tier"))`,
velox writes `veloxgen.Users.Attrs.Text("plan", "tier").EQ("pro")`; `Number`
and `Bool` read a number or a boolean, and `HasKey` tests that a key is there
([0058](decisions/0058-json-paths.md)). `Has`, `HasSome`, `HasEvery` and
`IsEmpty` test the elements of a column that is an array, where ent writes
`sqljson.ValueContains`: `veloxgen.Users.Tags.Has("admin")`
([0080](decisions/0080-json-array-elements.md)).

**The implicit ID is int64.** The implicit `id` is `field.Int64`, where
ent's is `int`. A foreign-key field must have its target's ID type: declare it
`field.Int64`, or declare the target's ID with `field.Int("id")`. A schema may
declare its own `id` of any key type, as in ent: `field.UUID("id", uuid.UUID{})`
with a `Default`, or `field.String("id")` for a key the application chooses.
Relations, many-to-many links, upserts and `After` work on such keys on all
three dialects (`TestNonIntegerKeys`).

**Defaults on a NOT NULL column.** Every ent `Default` and `DefaultFunc` is
accepted as it is, `field.Bool("active").Default(true)` and
`field.Int("seats").Default(100)` included, and the column is ent's, `NOT
NULL DEFAULT true`. ent fills a default when the builder never set the
field; velox inserts a struct, so a field left at its zero value takes it.
The insert fills the defaults before its setters and the hooks, as ent's
`Save` fills them before its hooks, so ent's `SetActive(false)` is velox's
setter and stores `false`:

```go
db.Insert(&u).Set(veloxgen.Users.Active.Set(false)).Exec(ctx)
```

A struct's `Active: false` with no setter stores `true`: in the struct,
`false` is unset. A GraphQL create input leaves the field optional, so
`active: false` is stored and a missing `active` takes the default. A hook
sees the defaults already filled, as in ent, so its `Mutation.Fill` of such a
column does nothing; its `Mutation.Set` stores the value
([ADR 0082](decisions/0082-defaults-before-setters.md)).

A constant `Default` is also the column's `DEFAULT`, as in ent: `Default("")`
gives `DEFAULT ''` and `Default(0)` gives `DEFAULT 0` (a bool, number, string
or enum; a `DefaultFunc` gives none). `SchemaDefault` is for a column default
that `Default` cannot say, such as `now()`, or for one without a Go default.
An insert leaves such a column out when the row holds the field's zero
value, so the database fills it, and velox reads the value back into the
struct: `RETURNING` on PostgreSQL and SQLite, a `SELECT` by key on MySQL
([ADR 0093](decisions/0093-lean-inserts.md)). ent leaves out any field the
builder did not set; velox, which has no "set" bit on a struct field, reads
the zero value as unset for such a column only, as GORM's `default` tag
does. A nil Nillable field is left out too, as in ent.
`CreateDefault` is filled in by gqlgen when a client leaves the field out.

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
for `post_tags`). ent's `AddTagIDs(ids...)`, `RemoveTagIDs(ids...)` and
`ClearTags()` are the setters `P.Tags.Add(ids...)`, `P.Tags.Remove(ids...)`
and `P.Tags.Clear()`, given to `Insert`'s or `Update`'s `Set`; the GraphQL
update input has `add<Edge>IDs` and `remove<Edge>IDs`.
[examples/relations](../examples/relations/README.md#many-to-many) shows how.
With `Through`, the links are an edge schema's rows, as in ent, and
velox-gen writes no join entity
([examples/relations](../examples/relations/README.md#many-to-many-through-an-edge-schema),
ADR 0109).

**An edge schema's hooks and policies run on Add and Remove.** ent fills
only defaults when `AddGroupIDs` inserts an edge schema's rows; its hooks
and privacy do not run (veloxe's `compiler/gen/sql/helper.go:96`). velox
inserts and deletes them as Membership rows, so Membership's hooks run, and
a link its Write policy hides stays through `Clear`. velox is stricter on
reads too. An edge schema cannot have a Read or Query policy, Interceptors
or `softdelete.Mixin`, its own or a mixin's: `Has` and `With` through the
many-to-many edge would not apply them. velox-gen refuses each. `Add` fails
before any statement when a column it would insert is required and has no
default; ent leaves that to the database.

**An edge schema is keyed by its own `id`.** ent's
`field.ID("user_id", "group_id")` annotation, a composite key, is not in
velox, so a port that uses it fails to compile. Remove it: velox keys the
rows by `id` and adds a unique index over the two keys. Composite keys are
left to ADR 0110.

**A bidirectional edge cannot go through an edge schema.** ent's
`edge.To("friends", User.Type).Through("friendships", Friendship.Type)`,
with no inverse, is refused (rule 69). Declare the inverse,
`edge.From("friended", User.Type).Ref("friends")`, and velox stores each
link once, in one direction.

**A tenant column on an edge schema is a plain column.** A scope mixin's
Policy sets Read, which an edge schema cannot have. Keep the column, and
make it Optional and set it in a hook, or give it a Default: a required
field with no default makes `Add` fail before any hook runs. A `WithFilter`
filter that restricts the edge schema fails `Has` and `With` through the
many-to-many edge (ADR 0098's amendment). A filter that exempts the edge
schema leaves `db.Query[veloxgen.Membership]()` unfiltered too. So read the
links through the edge velox-gen adds, with the tenant's condition written
out: `U.Memberships.HasWith(M.OrgID.EQ(org))`.

**On MySQL, a link that collides only on another unique key is dropped.**
`Add`'s `ON DUPLICATE KEY UPDATE` applies to every unique key of the edge
schema, so a new link whose row collides with another on a key other than
the pair is not written, and there is no error. PostgreSQL and SQLite fail
the write. velox-gen warns of an edge schema with such a key.

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
there is no `QueryPosts()`: read the posts with `db.Query[veloxgen.Post]()` and a
condition, or use `A.Posts.Has()` and `HasWith` in a `Where`.

## Queries and writes

| ent | velox |
| --- | --- |
| `client.User.Create().SetName("a").Save(ctx)` | `db.Insert(&veloxgen.User{Name: "a"}).Exec(ctx)` |
| `client.User.Query().All(ctx)` | `db.Query[veloxgen.User]().AllowUnbounded().All(ctx)`, or with a `Limit` |
| `client.User.Update().Where(...).SetAge(3).Save(ctx)` | `db.Update[veloxgen.User]().Where(...).Set(U.Age.Set(3)).Exec(ctx)` |
| `client.User.UpdateOneID(id).SetAge(3).Save(ctx)` | `db.Update[veloxgen.User]().Where(U.ID.EQ(id)).Set(U.Age.Set(3)).One(ctx)`, the row as written |
| `client.User.Delete().Exec(ctx)` (every row) | `db.Delete[veloxgen.User]().All().Exec(ctx)` |
| `client.User.Create().OnConflict(...).UpdateNewValues()` | `db.Insert(&u).OnConflict(U.Email).UpdateAll().Exec(ctx)` |
| `client.User.Query().Select(user.FieldName, user.FieldAge).All(ctx)` | `db.Query[veloxgen.User]().Columns(U.Name, U.Age).Limit(n).All(ctx)`: the other fields stay zero. velox's `Select` is a projection: terms read by position with `Rows`, not entities |
| `client.User.Query().Unique(true).Select(user.FieldAge)` | `velox.Values(ctx, db.Query[veloxgen.User]().Distinct().Limit(n), U.Age.Key())` |
| none: `EXPLAIN` by hand on what `client.Debug()` logs | `q.SQL(ctx)`, then `EXPLAIN` through `db.Rows` |
| `client.User.Update().SetNillableNickname(p)` | `Set(U.Nickname.SetPtr(p))`: a nil `p` sets nothing (decision 0099) |
| `client.User.Update().SkipDefault(user.FieldUpdatedAt)` (veloxe) | `db.Update[veloxgen.User]().KeepDefaults(U.UpdatedAt)` |
| `client.User.Query().IDs(ctx)` | `velox.IDs[int64](ctx, db.Query[veloxgen.User]().Limit(n))` |
| `tx.OnCommit(...)` | `velox.AfterCommit(tx, fn)`: `fn` runs once the outermost transaction has committed (decision 0100) |
| `client.Debug()` | the option `velox.WithLog(logger)`; `velox.LogArgs()` logs arguments too |
| `ent.IsConstraintError(err)`, `IsValidationError`, `IsNotSingular` | `velox.IsConstraintError(err)`, `IsValidationError`, `IsNotSingular` |

`Count` returns an `int64`, where ent's returns an `int`.

Inside `db.Tx`, a statement on the outer `db` fails with `ErrOutsideTx`
rather than running outside the transaction. To mix raw SQL and velox in one
transaction, begin it on the `*sql.DB` and run velox on it with
`velox.UseTx(db, tx)`.

A `Query` with neither a `Limit` nor `AllowUnbounded()` fails with `ErrUnbounded`.
An `Update` or `Delete` with neither a `Where` nor `All()` fails with
`ErrNoWhere`. The names differ because they mean different things: `AllowUnbounded`
allows a read with no row limit, and `All` confirms a write to every row.

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
| `sql.FieldContains`, `FieldHasPrefix`, `FieldHasSuffix`, `FieldEqualFold`, `FieldContainsFold` | `velox.FieldContains`, `FieldHasPrefix`, `FieldHasSuffix`, `FieldEqualFold`, `FieldContainsFold` |
| `sqlgraph.HasNeighbors` (`user.HasPets()`) | `velox.HasEdge("pets")` |
| `sqlgraph.HasNeighborsWith` (`user.HasPetsWith(...)`) | `velox.HasEdgeWith("pets", ps...)` |
| `sql.AndPredicates`, `OrPredicates`, `NotPredicates` | `velox.And`, `velox.Or`, `velox.Not` |
| `privacy.Allow` | `privacy.Allow`; `velox.Allow()` is the condition that matches every row |

A `velox.Predicate` is a `Pred` of no
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
lift it. A hook that must use the typed API of `veloxgen` (another entity, say)
is a run-time hook, `velox.WithHooks[veloxgen.Task](...)`, a DB option.
ent's `client.Use(hooks...)` is `velox.WithHooksAll(hooks...)`, and
`m.Type()` is `m.Entity()`. ent's `client.Intercept(...)` that adds a
condition is `velox.WithFilter(func(ctx, entity string, op velox.Op) (velox.Predicate,
error))`, a row filter for every entity with an interceptor's reach
(decision 0098). ent's interceptor wraps queries only; a filter that
returns `velox.Allow()` when `op != velox.OpSelect` does the same.

ent's `m.OldTitle(ctx)` reads one row's old value, on a one-row update,
with a query outside the update's lock. In velox the writes through a
recording handle, `db.Record(sink)`, report every row they change, bulk
updates and deletes included, with the row before and after the write, read
locked in the write's transaction. `T.Title.Old(c)` is ent's `OldTitle` for
a recorded `Change` (see the guide's "Recording changes").

A mixin declares behaviour as ent's does, with the same plain methods.
velox reads them for every entity that lists the mixin, mixins first,
in `Mixin()` order, then the entity's own. The policies are ANDed, so none
lifts another. velox reads mixins one level deep, as ent does, but where
ent ignores a mixin's own `Mixin()` method, velox-gen refuses a mixin whose
`Mixin()` lists any: list them all in the schema's `Mixin()`. Tenancy is such a mixin, over package `scope` (decisions
0038 and 0039), as in ent's privacytenant example:

```go
var OrgKey = scope.NewKey[int64]("org")

type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []velox.Field { return []velox.Field{field.Int64("org_id").Immutable()} }

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
delete hard. A deleted row keeps its unique values, so velox-gen warns of
each unique key over every row of such an entity: a `Unique` field, a
one-to-one edge's key, or a unique index with no `Where`. On PostgreSQL and
SQLite, declare the key as
`index.Fields("email").Unique().Where("deleted_at IS NULL")`; a one-to-one
key, or any key on MySQL, is freed by a delete under `softdelete.Skip(ctx)`. `scopetest.Isolates` checks, in a test, that an entity is
confined, and that its column is immutable, NOT NULL and of the key's type.

The layout is ent's, with no runtime package (decisions 0037, 0039 and
0040): `veloxgen` holds every entity, handle and descriptor, and imports the
schema package by its own name for its defaults, validators and
behaviour. A descriptor names the schema's value, `Schema: schema.Issue{}`,
and velox reads the behaviour from it as ent's generated runtime reads
`schema.Issue{}.Policy()`: each `Mixin()` value's, in order, then the
schema's own. An entity with no behaviour, its own or a mixin's, names no
schema. There is no `veloxgen/runtime` and no blank import. The schema package may not
import `veloxgen`, directly or through another package: velox-gen refuses it,
with the chain. A schema compiles before `veloxgen` exists, so no
`schema/snapshot` is needed. Plugins write into `veloxgen`.

On SQLite, `velox.Connect` turns foreign keys on for every connection. A
`*sql.DB` you pass to `velox.Open` is used as it is, so put
`_pragma=foreign_keys(1)` in its DSN.

On SQLite a time field is a `datetime(9)` column, where ent's is `datetime`.
velox writes every time as UTC text with nine fractional digits and parses it
itself; the driver parses a `datetime` column, slower, and velox reads that
too. `migrate diff` on a database ent made plans the change to `datetime(9)`,
a table rebuild on SQLite.

## Running the generator from Go

ent's `entc.go` calls `entc.Generate` (veloxe's, `compiler.Generate`) with a
`gen.Config`. velox's is `generate.Run(ctx, dir, generate.Config{...})`,
from package `github.com/syssam/velox/generate`, in a `generate.go` behind
`//go:build ignore` ([decision 0097](decisions/0097-generate-package.md)).
It writes what `go tool velox-gen` writes with the same settings.

| ent / veloxe | velox |
|---|---|
| `compiler.Generate("./schema", cfg)`, `entc.Generate` | `generate.Run(ctx, "", generate.Config{Schema: "./veloxschema"})` |
| `gen.NewConfig(opts...)` | a `generate.Config` literal; the zero value is velox-gen with no flags |
| `gen.WithTarget(dir)` | `Out` (default `./veloxgen`) |
| `gen.WithPackage(path)` | not needed: the import path is the module's path joined to `Out`'s; `Package` names the package (default `Out`'s base name) |
| `gen.WithSchema(path)` | `Schema`, a package pattern (default `./veloxschema`) |
| `gen.WithFeatures(FeaturePrivacy, FeatureIntercept, ...)` | always on, so no flag: privacy is `Policy` and package `privacy`, intercept is `Interceptors` as row filters, lock is `ForUpdate`, modifier is `Expr`, `Calc`, `Select` and `SetExpr`, upsert is `OnConflict`, execquery is `db.Exec`, `Rows` and `Raw`, autodefault is the zero `DEFAULT` (decision 0090), entpredicates are the handles and `FieldEQ`, versioned migrations are velox-gen migrate, schemaconfig is `TableIn` and `sqlschema.Schema`; snapshot is not needed, as velox-gen loads a schema whose generated package is stale |
| `gen.WithHooks(...)` | not offered. A hook that sorts each entity's fields has no form: `CREATE TABLE` follows the schema's declaration order, so declare the fields in the order wanted. A prefix hook for index names is the default (decision 0090). |
| `gen.WithTemplates`, `compiler.TemplateDir`… | not offered: the generated package is entirely velox-gen's, reproducible from the schema alone, so `velox-gen -check` proves it current (decision 0034). Code of your own goes in a file of yours beside it. |
| `compiler.Extensions(entgql.NewExtension(...))` | `Plugins`: `generate.Plugin{Tool: "velox-graphql"}` runs the tool; `graphqlgen.Plugin(opts)` from `contrib/graphql/graphqlgen` runs it in the process, with the same output |
| `gen.WithBuildFlags`, `compiler.BuildFlags("-p=4")` | `Env: append(os.Environ(), "GOFLAGS=-p=4")`, the environment of every go command Run starts |
| `gen.WithStorageDriver`, `gen.WithIDType`, `gen.WithHeader` | not offered: the dialect is the migration directory's, IDs are int64 unless the schema declares one, and the header is velox-gen's, which marks the files it owns |

A program with a plugin that runs only in the process is its own check: run
it with `Check: true` in CI, where it returns `generate.ErrStale` when a file
is out of date. `velox-gen -check` does not know such a plugin.

## Refused by design

- **Not built: back-references (no reader in nextapp).** ent's and veloxe's
  load can fill each loaded child's inverse edge with its parent, so that
  `child.Edges.Parent` is set after `parent.With(children)`. velox does not:
  a child's to-one edge is set only when that edge is loaded, with
  `With(C.Parent)` on the child's query. The one application ported from
  veloxe reads no back-reference: each of its `child.Edges.Parent` reads
  follows a `With` of that edge (ADR 0101).

## GraphQL (contrib/graphql, compared with entgql)

- **Inputs, not mutations, as in entgql.** velox-graphql writes
  `Create<T>Input` and `Update<T>Input`, and no `type Mutation`: the
  application declares its mutations in its own `.graphql` file and writes
  the resolvers. Where entgql's resolver calls
  `client.Todo.Create().SetInput(input).Save(ctx)`, velox's calls
  `db.Insert(x).SetInput(input).Exec(ctx)`, `x` the row the resolver has
  already set what the client must not choose on, or
  `db.Update[T]().Where(...).SetInput(input).One(ctx)`, ent's `UpdateOne`.
  The input's `Setters()` returns the velox setters, and an error for an
  input it refuses; `SetInput` keeps that error for `Exec` or `One`.
  As in entgql, a null in an input is a field not given; `clear<Field>:
  true` sets NULL. No delete is generated: a delete is the application's
  `db.Delete[T]()`.
- **A connection is a query's Paginate.** Where entgql's resolver
  calls `client.Todo.Query().Paginate(ctx, after, first, before, last,
  ent.WithTodoOrder(orderBy), ent.WithTodoFilter(where.Filter))`, velox's
  calls `db.Query[veloxgen.Todo]().Paginate(ctx, after, first, before, last,
  veloxgen.WithTodoOrder(orderBy), veloxgen.WithTodoFilter(where))`, with `db` from
  `veloxgql.DB(ctx)`, as entgql's `ent.FromContext(ctx)`. The filter
  option takes the where input itself. `Paginate` is generic and calls
  the entity's generated `PaginateQuery`, so it does not compile for an
  entity with no connection. The connection is lazy: it sends nothing
  until gqlgen resolves its fields. The query may carry the
  application's own filters; it must not be ordered. A where input's
  `Pred(ctx)` is entgql's `P()`: a `velox.Pred[T]` for any query.
- **No Node, and an ID is the key.** velox-graphql writes no `Node`
  interface, no `node` or `nodes` query and no `Noder`. An ID is the
  entity's key in its own Go type, which gqlgen decodes: a resolver takes
  `id int64`, and an input's `authorID` is an `int64`. Every key of a
  schema's GraphQL types has one Go type, since gqlgen binds every ID
  argument to the first model `gqlgen.yml` lists for `ID`: `gqlgenx.Int64ID`
  for int64, `gqlgenx.UUID` for UUID, gqlgen's `graphql.ID` for string.
  velox-graphql refuses a schema whose keys differ. An application that
  needs `node(id:)` declares and resolves it itself (ADR 0061).
- **Where inputs read empty values as SQL does.** entgql adds nothing for
  them; velox-graphql gives each its meaning: `xIsNil: false` is `IS NOT
  NULL`, an empty `xIn` matches no row, `or: []` matches no row, and `not:
  {}` matches none, where entgql refuses an empty input. An empty `and` or
  `hasXWith` adds nothing.
- **OrderVia keeps every row.** An `orderBy` through an edge reads it with a
  LEFT JOIN: a row whose target is missing or hidden sorts as NULL, and the
  connection's rows and `totalCount` are the same as without the order.
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
