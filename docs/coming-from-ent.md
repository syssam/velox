# Coming from ent

velox's schema DSL follows ent's: `velox.Schema`, `Fields`, `Edges`,
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
velox it means only that the field need not be given on create:
the column stays NOT NULL and the Go field is a value. `Nillable()` is what
makes a column NULL-able, a pointer in Go with `Null*` handles (`IsNull`,
`SetNull`), and `Default` is what an unset field takes. An unset `Optional`
field with neither is written as its Go zero value, `""`, `0`, `false` or a
zero time ([0003](decisions/0003-nullability.md),
[0090](decisions/0090-veloxe-defaults.md)). An `Optional` enum is the
exception, whose zero is none of its values: velox-gen asks it for a `Default`
or `Nillable`. An `Optional` JSON field's column is NULL-able; unset, a slice
or a map is written empty, `[]` or `{}`, a struct as its zero value's JSON,
and a pointer as its element's zero value's JSON.
An `Optional` `Unique` field needs `Nillable` or a `DefaultFunc`, or every
unset row would hold the same value.

A string, bool or number column that is NOT NULL and has no `Default` gets
its zero as its column `DEFAULT`, so `migrate diff` can add it to a table that has rows. Any other NOT NULL column
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
([0080](decisions/0080-json-array-elements.md)). The other `sqljson` helpers
map as follows ([0115](decisions/0115-json-append-len-patterns-order.md)):

| ent | velox |
| --- | --- |
| `client.User.Update().AppendTags([]string{"x"})` (`sqljson.Append`) | `Set(U.Tags.Append([]string{"x"}))`; a NULL column becomes the elements, as in ent |
| `sqljson.ValueContains(f, v, sqljson.Path("a", "ids"))` | `U.Attrs.Array("a", "ids").Has(v)`; also `HasSome`, `HasEvery`, `IsEmpty`: false where the path holds no array ([0141](decisions/0141-json-array-at-a-path.md)) |
| `sqljson.ValueContains(f, doc)` with an object `doc` and no path | `U.Attrs.Contains(doc)`: PostgreSQL's `@>`, MySQL's `JSON_CONTAINS`, and a refusal on SQLite, where ent's form compares `json_each` values ([0154](decisions/0154-json-contains.md)) |
| `sqljson.ValueContains(f, map[string]any{"employee_id": 7}, sqljson.Path(…))` on an array of objects | `U.Attrs.Array(…).HasField("employee_id", 7)`, or `Array()` for the column's own array: alike on PostgreSQL (`@>`, GIN), MySQL and SQLite, where ent's form compares `json_each` values as text ([0159](decisions/0159-json-array-has-field.md)) |
| `sqljson.LenEQ(user.FieldTags, 2)`, `LenGT`… with `sqljson.Path` | `U.Tags.Len().EQ(2)`, `U.Attrs.Len("a", "b").GT(0)`: NULL where the path holds no array |
| `sqljson.StringContains(f, "x", sqljson.Path("name"))`, `StringHasPrefix`, `StringHasSuffix` | `U.Attrs.Text("name").Contains("x")`, `HasPrefix`, `HasSuffix`: literal and with case on every dialect |
| `sqljson.OrderValue(f, sqljson.Path("n"))` | `OrderBy(U.Attrs.Number("n").Asc())`, or `Text("n").Desc()` |

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
or enum; a `DefaultFunc` gives none). `sqlschema.Default`, `DefaultExpr` and
`DefaultExprs`, as ent's `entsql` has them, are for a column default
that `Default` cannot say, such as `now()`, or for one without a Go default.
An insert leaves such a column out when the row holds the field's zero
value, so the database fills it, and velox reads the value back into the
struct: `RETURNING` on PostgreSQL and SQLite, a `SELECT` by key on MySQL
([ADR 0093](decisions/0093-lean-inserts.md)). ent leaves out any field the
builder did not set; velox, which has no "set" bit on a struct field, reads
the zero value as unset for such a column only, as GORM's `default` tag
does. A nil Nillable field is left out too, as in ent. A setter is ent's
`SetX`: its column is always written, so `Set(U.Rate.Set(0))` stores 0 and
`SetNull()` stores NULL over any default
([ADR 0136](decisions/0136-explicit-zero-and-empty-patch.md)).
`CreateDefault` is filled in by gqlgen when a client leaves the field out.

The same holds for a string: with `Default("x")`, a struct's `""` takes the
default, and `Set(U.Plan.Set(""))` stores `""`.

**An update with nothing set.** As in ent, an update whose setters were all
skipped (`SetPtr(nil)`), or whose `SetInput` gives nothing, still runs its
hooks and its `UpdateDefault`s, so `updated_at` moves; so does one of only
edge setters, as when ent's `AddTagIDs` changes a relation. When they assign
nothing either, nothing is sent: `Exec` returns 0 and `One` returns the row
([ADR 0136](decisions/0136-explicit-zero-and-empty-patch.md)). An update
never given a `Set` fails with "Update has no Set", where ent's would run.

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
and privacy do not run. velox
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
and whether it was loaded, so there is no `AuthorOrErr`. Where ent's code
assigns `post.Edges.Author = u`, such as a test's fixture, velox's assigns
`velox.LoadedOne(u)` (`velox.LoadedMany(s)` for a to-many edge).

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
| `client.User.CreateBulk(builders...).Save(ctx)` | `db.InsertBulk(qs...).Exec(ctx)`, each `qs[i]` a `db.Insert(&us[i]).Set(...)`: a setter's zero is stored per row, and the keys come back into `us`; rows naming the same columns share one INSERT (decision 0153). `db.InsertAll(us)` takes values and cannot tell a zero from unset |
| `client.User.Query().All(ctx)` | `db.Query[veloxgen.User]().AllowUnbounded().All(ctx)`, or with a `Limit` |
| `client.User.Update().Where(...).SetAge(3).Save(ctx)` | `db.Update[veloxgen.User]().Where(...).Set(U.Age.Set(3)).Exec(ctx)` |
| `client.User.Get(ctx, id)` | `db.Query[veloxgen.User]().Where(U.ID.EQ(id)).One(ctx)` |
| `client.User.UpdateOneID(id).SetAge(3).ClearNick().Save(ctx)` | `db.Update[veloxgen.User]().Where(U.ID.EQ(id)).Set(U.Age.Set(3), U.Nick.SetNull()).One(ctx)`, the row as written; or, with the user in hand, `db.Save(&u, U.Age, U.Nick).Exec(ctx)` |
| `client.User.DeleteOneID(id).Exec(ctx)` | `db.Delete[veloxgen.User]().Where(U.ID.EQ(id)).One(ctx)`: `ErrNotFound` when no row went (decision 0121) |
| `client.User.Delete().Exec(ctx)` (every row) | `db.Delete[veloxgen.User]().All().Exec(ctx)` |
| `client.User.Create().OnConflict(...).UpdateNewValues()` | `db.Insert(&u).OnConflict(U.Email).UpdateAll().Exec(ctx)` |
| `.OnConflict(...).UpdateAge()`, `.AddAge(1)`, `.SetAge(3)` on the upsert | `.OnConflict(U.Email).Update(U.Age)`, `.UpdateSet(U.Age.Add(1))`, `.UpdateSet(U.Age.Set(3))` |
| `sql.ResolveWith(func(u *sql.UpdateSet){ u.Set(c, sql.Expr(...)) })`, `u.Excluded()` | `.UpdateSet(velox.SetExpr(U.Age, "? + ?", U.Age, velox.Excluded(U.Age)))` |
| `sql.UpdateWhere(p)`, `sql.ConflictWhere(p)`, `sql.ConflictConstraint(name)` | `.UpdateWhere(p)`, `.OnConflictWhere(p)`, `.OnConstraint(name)` (PostgreSQL; `OnConflict`'s columns stay required) (decision 0111) |
| `client.User.Query().Select(user.FieldName, user.FieldAge).All(ctx)` | `db.Query[veloxgen.User]().Columns(U.Name, U.Age).Limit(n).All(ctx)`: the other fields stay zero. velox's `Select` is a projection: terms read by position with `Rows`, or into a struct with `velox.Scan`, not entities |
| `client.User.Query().GroupBy(user.FieldAge).Aggregate(ent.Count()).Scan(ctx, &v)`, `Select(...).Scan` | `velox.Scan[T](ctx, db.Query[veloxgen.User]().Select(U.Age, velox.CountAll[veloxgen.User]().As("count")).GroupBy(U.Age).Limit(n))`: fields by a `velox:"name"` tag or their name, JSON decoded; `velox.ScanOne` for one row (decision 0116) |
| `client.User.Query().Unique(true).Select(user.FieldAge)` | `db.Query[veloxgen.User]().Distinct().Limit(n).Values(ctx, U.Age)` |
| none: `EXPLAIN` by hand on what `client.Debug()` logs | `q.SQL(ctx)`, then `EXPLAIN` through `db.Rows` |
| `client.User.Update().SetNillableNickname(p)` | `Set(U.Nickname.SetPtr(p))`: a nil `p` sets nothing (decision 0099) |
| an update that must not fire `UpdateDefault` on `updated_at` | `db.Update[veloxgen.User]().KeepDefaults(U.UpdatedAt)` |
| `client.User.Query().Order(user.ByPostsCount(sql.OrderDesc()))` | `db.Query[veloxgen.User]().OrderBy(U.Posts.Count().Desc(), U.ID.Asc())`: the posts the reader may not see are not counted (decision 0112) |
| `client.User.Query().Order(user.ByPosts(sql.OrderBySum(post.FieldViews)))` | `OrderBy(U.Posts.Sum(P.Views).Asc(), U.ID.Asc())`; `Avg`, `Min` and `Max` too, NULL for a user with no posts |
| `user.HasPostsWith(...)` with a count, `sql.P` by hand | `velox.Expr[veloxgen.User]("? >= ?", U.Posts.Count(), 3)` in a `Where` |
| a predicate `func(s *sql.Selector)` whose subquery `HasWith` and `InSelect` cannot say (a cast key, a table no entity maps, a table read without its policy) | `velox.ExprRaw[veloxgen.User](reason, "? IN (SELECT CAST(b.id AS TEXT) FROM bill b WHERE b.org_id = ?)", U.EntityID, org)`: no policy inside the text, the outer statement's kept; a handle is the qualified outer column, for a correlated subquery; `Statement.RawReasons()` shows the reason to hooks (decision 0155); in a schema's Policy, by name and with no policy of the other entity inside, `velox.FieldInSelectRaw(reason, "entity_id", "Bill", "id", velox.FieldEQ("org_id", org))`, a text column against an integer key cast (decision 0157) |
| `client.User.Query().IDs(ctx)` | `db.Query[veloxgen.User]().Limit(n).IDs[int64](ctx)` |
| `tx.OnCommit(...)` | `velox.AfterCommit(tx, fn)`: `fn` runs once the outermost transaction has committed (decision 0100) |
| a commit hook (`tx.OnCommit` returning a `Committer` that runs before `next.Commit`) | `velox.BeforeCommit(tx, fn)`: its error rolls back (decision 0111) |
| `tx.OnRollback(...)` | `velox.AfterRollback(tx, fn)`: also runs when the nested `Tx` it was queued in rolls back (decision 0111) |
| `m.Fields()`, `m.FieldCleared(f)`, `m.AddedEdges()`/`RemovedEdges()`/`ClearedEdges()` | `m.Columns()`, `m.Cleared(name)`, `m.Edges()` |
| `hook.If`, `hook.Unless`, `hook.HasOp`, `hook.HasFields`, `hook.HasClearedFields`, `hook.Reject` | `velox.If`, `velox.Unless`, `velox.HasOp`, `velox.HasColumns`, `velox.HasCleared`, `velox.Reject` |
| `client.Debug()` | the option `velox.WithLog(logger)`; `velox.LogArgs()` logs arguments too |
| `ent.IsConstraintError(err)`, `IsValidationError`, `IsNotSingular` | `velox.IsConstraintError(err)`, `IsValidationError`, `IsNotSingular` |
| `ValidationError.Name`, the schema field's name (`"address_city"`) | `velox.ValidationError.Name`, the same; `Field` is the Go name, which `Error()` prints ([ADR 0144](decisions/0144-validation-error-name.md)) |

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
| a privacy rule's `ent.TxFromContext(ctx)`, else the client | a `velox.Policy` function's `db` parameter, a privacy rule's `q.DB()` or `m.DB()`, an Interceptor's Filter's `db`: the statement's DB, the transaction's inside `Tx`; a read under the rule's ctx is not judged by that rule (decisions 0135, 0151) |

A `velox.Predicate` is a `Pred` of no
entity, so `And`, `Or` and `Not` take it, and `velox.Predicate(p)` turns a
typed handle's `Pred[E]` into one. Its zero value fails the statement: it
is a struct, not an interface, so it cannot be nil, and a policy that
returns `velox.Predicate{}` with its error is refused, never read as
"no condition". A hook reads and writes a row with `m.Value`, `m.Set` and `m.Fill`, and a
policy's `Insert` reads it with `r.Value`. A run-time hook, which can import
the generated package, uses the handles instead, as ent's typed
`m.Title()` and `m.SetTitle(v)`: `T.Title.Value(m)`, `T.Title.Assign(m, v)`,
`T.Title.Fill(m, v)`, and `T.Title.Get(r)` and `Put(r, v)` on a row, where a
value of the wrong type does not compile. velox-gen refuses a constant name
the entity has no column of, at its line, and a constant value that does
not fit the column; any other name the entity has no column of fails the
statement. A velox interceptor is a row filter, not ent's query
middleware: its condition reaches every read and write of the entity, a
relation's included, before the Read policy, and `WithoutPolicy` does not
lift it. A hook that must use the typed API of `veloxgen` (another entity, say)
is a run-time hook, `velox.WithHooks[veloxgen.Task](...)`, a DB option.
ent's `client.Use(hooks...)` is `velox.WithHooksAll(hooks...)`, and
`m.Type()` is `m.Entity()`, which compares with the generated
`veloxgen.TypeTask`, as ent's `m.Type()` with `ent.TypeTask`. ent's `client.Intercept(...)` that adds a
condition is `velox.WithFilter(func(ctx, db *velox.DB, e velox.FilterEntity, op velox.Op) (velox.Predicate,
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
		Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
			org, err := OrgKey.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ("org_id", org), nil
		},
		Insert: func(ctx context.Context, _ *velox.DB, r velox.Row) error { return OrgKey.Match(ctx, r.Value("org_id")) },
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
`index.Fields("email").Unique().Annotations(sqlschema.IndexWhere("deleted_at IS NULL"))`; a one-to-one
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

On SQLite, `ForUpdate` and `ForShare` fail, where ent drops the clause. The
option `velox.WithSQLiteLockingReads()` drops it as ent does. SQLite's one
writer stands in for the lock; the guide's
[Row locks](guide.md#row-locks) says what that guarantees under DEFERRED
and IMMEDIATE transactions.

On SQLite a time field is a `datetime(9)` column, where ent's is `datetime`.
velox writes every time as UTC text with nine fractional digits and parses it
itself; the driver parses a `datetime` column, slower, and velox reads that
too. `migrate diff` on a database ent made plans the change to `datetime(9)`,
a table rebuild on SQLite.

### More calls

| ent | velox |
| --- | --- |
| `ent.Open(dialect.Postgres, dsn)` | `velox.Connect("postgres", dsn, opts...)` |
| `Modify(func(s *sql.Selector){…})`, `func(*sql.Selector)` predicates, `WhereP` | `velox.Expr` for a condition, `velox.Calc` for a term or an order, `Select` or `SelectRaw` with `Rows` for a projection, `velox.SetExpr` for an update ([sql.md](sql.md#coming-from-ent)) |
| `COUNT(*) OVER ()` in a `Modify` | `velox.Calc[T]("COUNT(*) OVER ()")` or `SelectRaw`; a window function is refused in `Expr`, `GroupBy`, an aggregate and `SetExpr` ([ADR 0101](decisions/0101-window-terms-soft-delete-options.md)) |
| a report's table from field names to columns, `sql.As(f, name)` by name | `velox.ColumnByName[T](column)`, a Term that selects, groups and orders ([guide](guide.md#date-buckets-subtotals-and-columns-by-name), [ADR 0127](decisions/0127-report-terms.md)) |
| a date bucket in a `Modify`: `TO_CHAR(? AT TIME ZONE 'z', 'YYYY-MM')`, `date_trunc` | `velox.DateTrunc[T](velox.UnitMonth, col, velox.InZone(z))`, the bucket's first instant on every dialect; `FiscalYear(m)` for fiscal years and quarters; a date part (`EXTRACT`) stays a `Calc` |
| `GroupBy(f…).Aggregate(ent.As(ent.Sum(col), n))` with a run-time `col` | `t, _ := velox.ColumnByName[T](col)`; `Select(key, t.Sum().As(n)).GroupBy(key)` ([ADR 0142](decisions/0142-nextapp-projection-recipes.md)) |
| `GroupBy(a, b).Scan` with no aggregate | `Select(a, b).Distinct()` or `Select(a, b).GroupBy(a, b)`, read with `velox.Scan` |
| `COUNT(DISTINCT CASE WHEN c <> 0 THEN c END)` in a `Modify` | `velox.Calc[T]("CASE WHEN ? <> 0 THEN ? END", c, c).CountDistinct()` |
| `AppendSelectExprAs(expr, alias)` beside entity rows, read with `Value(alias)` | `Select` of every column (`ColumnByName`) and the `Calc` `.As(alias)`, scanned into a struct embedding the entity; the keyset an `Expr` over the same Term ([guide](guide.md#date-buckets-subtotals-and-columns-by-name)) |
| a derived table in `Modify` so the parent's columns group the child | `Join(rel)` and `velox.Joined(rel, col)` in `Select` and `GroupBy`, the parent's policy in ON ([ADR 0143](decisions/0143-joined-terms-in-projections.md)) |
| a hand join to a dated table with no edge | read the rows of that table first, then a `CASE` term over the document's own columns; a correlated outer reference is not built ([ADR 0142](decisions/0142-nextapp-projection-recipes.md)) |
| `sqljson.ValueContains(f, v, sqljson.Path(k))` | `T.F.Array(k).Has(v)` ([ADR 0141](decisions/0141-json-array-at-a-path.md)); an object element by one member, `T.F.Array(k).HasField(key, v)` ([ADR 0159](decisions/0159-json-array-has-field.md)) |
| `GROUP BY ROLLUP(…)`, `CUBE(…)` in a `Modify` | `GroupBy(velox.Rollup[T](keys...))`, `velox.Cube`; MySQL `WITH ROLLUP` as the only key, SQLite refuses |
| field names kept beside each `Order` | `o.Term().Column()` and `o.Descending()`; `RawRow.Columns()` for a row's names |
| `client.Intercept(...)`, `intercept.Func` | `velox.WithFilter(func(ctx, db *velox.DB, e velox.FilterEntity, op velox.Op) (velox.Predicate, error))`, which returns `velox.Allow()` for no condition ([guide](guide.md#hooks-and-filters-for-every-entity)) |
| `client.Use(hooks...)`, `m.Type()` | `velox.WithHooksAll(hooks...)`, `m.Entity()` |
| the generated package's `ent.TypeUser` | `veloxgen.TypeUser`, the same value: the `Name()` of the entity a filter receives and `m.Entity()` ([ADR 0139](decisions/0139-entity-type-constants.md)) |
| a privacy rule's `WhereP(func(*sql.Selector))` | `Filter.Where(velox.Predicate)` |
| `ForUpdate()` on SQLite | the read fails, unless the option `velox.WithSQLiteLockingReads()` runs it without the clause ([guide](guide.md#row-locks), [ADR 0123](decisions/0123-sqlite-locking-reads-opt-in.md)) |
| the generated package's `IsConstraintError(err)`, `IsValidationError`, `IsNotSingular`, `IsNotFound` | `velox.IsConstraintError(err)`, `IsValidationError`, `IsNotSingular`, `IsNotFound` |
| `ValidationError.Name`, the schema field's name, from which an error presenter derives the GraphQL input key | `velox.ValidationError.Name`, the same; `Field` is the Go name ([ADR 0144](decisions/0144-validation-error-name.md)) |
| `OpCreate` | `OpInsert` |
| `OpUpdate \| OpUpdateOne` | `OpUpdate` |
| `OpDelete \| OpDeleteOne` | `OpDelete` |
| an op tested as a bit set | an op compared with `==`; `velox.On(h, ops...)` runs `h` for the ops listed |
| a hook's or an interceptor's lookup on the client inside a transaction | a read through the filter's `db` parameter, the statement's DB ([guide](guide.md#hooks-and-filters-for-every-entity), [ADR 0132](decisions/0132-filter-reads-through-the-statements-db.md)) |
| a client read inside a transaction that must not join it | a read on `db` under `velox.WithoutTx(ctx)`; under fn's `ctx` it fails with `ErrOutsideTx` |
| a test's `tx, _ := c.Tx(ctx); defer tx.Rollback()` that never commits | `veloxtest.RolledBack(t, ctx, db, func(ctx context.Context, tx *velox.DB) { … })`: fn's writes roll back, a `t.Fatal` in fn included |
| a failed COMMIT, told apart by its message | `errors.Is(err, velox.ErrCommitUnknown)`: the outcome is unknown, so retry nothing. A COMMIT that says it rolled back does not match, such as PostgreSQL's answer to an aborted transaction, `pgx.ErrTxCommitRollback` ([ADR 0134](decisions/0134-commit-unknown.md)) |
| a helper returning a `nil` predicate for "no condition", checked with `p == nil` | the zero `velox.Predicate{}` or `Pred[E]{}`, checked with `velox.IsZero(p)`: a `Pred` holds a slice, so it has no `nil` and no `==`. A zero `Pred` that reaches a query fails the statement |
| a retry and a timeout in a helper around every transaction | `velox.WithTxDefaults(velox.TxRetrySerializable(3), velox.TxTimeout(30*time.Second))` at `Connect` ([ADR 0133](decisions/0133-tx-defaults-and-none.md)) |
| `Select(f).Strings(ctx)`, `Ints(ctx)` | `q.Values(ctx, T.F)` |
| `e.Unwrap()` | none: an entity is a plain struct, with no client in it |
| a `softdelete` mixin of your own, with `deleted_by_*` | `softdelete.Mixin{Assign: …}`, and `Reads: softdelete.Unfiltered` for unfiltered reads ([guide](guide.md#soft-delete)) |
| `entgql.Cursor` | `veloxgql.Cursor{Type, Terms, Key}`, bound to a type and an order |
| `entgql.PageInfo` | `veloxgql.PageInfo` |
| a where input's `AddPredicates(p)` | none: `q.Where(p)` before `Paginate` |
| `w.UnmarshalGQL(doc)` of a kept filter (an access policy's `{"ownerId": "123"}`) | `veloxgql.DecodeWhere(doc, &w)`, then `w.Pred(ctx)`. An unknown key or a value of the wrong type is an error naming its path, such as `not.idIn[0]` ([ADR 0146](decisions/0146-decode-where.md)) |

## Tests and migrations

ent's `client.Schema.Create` is an auto-migration that may run anywhere.
velox migrates with versioned files: `velox-gen migrate diff` writes them,
and `migrate apply` or `migrate.Up` runs them. For tests and development
databases it has the bootstrap ent's tests use
([decision 0113](decisions/0113-test-schema-create.md)).

| ent | velox |
|---|---|
| `enttest.Open(t, "sqlite3", dsn)` | `veloxtest.Open(t, "sqlite", dsn, schemas)`, with `schemas` every schema value of the package; an empty or in-memory `dsn` is a file of the call's own in `t.TempDir()`, a copy of tables made once per test binary (ADR 0147) |
| `client.Schema.Create(ctx)` in a test or a development server | `migrate.Create(ctx, velox.Pool(db), s)`, with `s` from `veloxtest.Schema(dialect, schemas...)`: it makes the tables in an empty database, does nothing when they are there as the schema says, and refuses any other database |
| `client.Schema.Create(ctx)` at deploy | `migrate diff`, then `migrate apply` or `migrate.Up` |
| `migrate.WithDropColumn`, `WithDropIndex` | `migrate diff -allow drop-column=t.c`; `Create` never alters a table |

ent leaves a foreign key without an index unremarked. velox declares
none either, but `migrate diff` writes an `unindexed-fk` review line for
a key it adds that no index leads, which `-allow unindexed-fk=t.c`
silences ([decision 0114](decisions/0114-unindexed-foreign-keys.md)).

ent has no N+1 check. velox's `veloxtest.NoNPlusOne(t, ctx)` fails a test
whose context ran one shape of read twice, as a loop over rows that reads
a relation per row does; `velox.CountStatements` and `StatementBudget` are
the same check outside veloxtest
([decision 0117](decisions/0117-statement-counting.md)).

### Adopting a database ent migrated

A database `client.Schema.Create` made is adopted once, then each schema
change is one file.
[Adopting an existing database](../examples/README.md#adopting-an-existing-database)
gives the five steps. In short:

1. `go tool velox-gen migrate baseline -dialect postgres -db <dsn>` writes
   `./migrations/0001_baseline.sql`, which builds the tables as that
   database holds them.
2. `go tool velox-gen migrate apply -baseline 1 -db <dsn>` records it as
   applied, running nothing, on each existing database.
3. `go tool velox-gen migrate drift -db <dsn>` checks each one.

Then each change is a file:

| ent | velox |
| --- | --- |
| `client.Schema.Create(ctx)` at deploy | `migrate diff` when the schema changes, then `migrate apply -db <dsn>`, or `migrate.Up(ctx, velox.Pool(db), dir)` in the program |
| `client.Schema.WriteTo(ctx, os.Stdout)` | `migrate diff -dry-run` prints the file it would write; `migrate apply -dry-run` prints the files it would apply |
| `migrate.WithDropIndex(true)` | none: `diff` plans a dropped index, as a finding to review |
| a hand-written table rename before `Create` | `migrate diff -rename-table old=new`, or `-rename-column table.old=new` |
| hand-written SQL around `Create` | statements you add to the file `diff` wrote, then `migrate hash` |

`diff` compares the schema with the directory's `schema.json`, not with a
database. `migrate drift -db <dsn>` compares a database with it.

`migrate diff` refuses a destructive change until `-allow` names it, such
as a dropped table or column. It also refuses a NOT NULL column with no
default, which would fail on a table that has rows. A string, bool or
number column gets its zero as its default, so only another type, such as
a JSON column, meets this. Give the field a `Default` or make it
`Nillable`, or add the statements that fill it to the file by hand.
[A join table becomes an edge schema](migrations-operations.md#a-join-table-becomes-an-edge-schema)
shows the edit and `migrate hash`.

velox declares a foreign key for every edge, and has no option to leave
them out. A database made with `migrate.WithForeignKeys(false)` has none,
and baseline records it as it is, so the first `diff` adds every foreign
key. Each fails on rows whose key matches no row of its target: find and
fix those first.

A field made `Nillable` gets `DROP NOT NULL` from `diff`, so a hand step
that relaxed such columns is not needed. When `apply` refuses or stops,
[migrations-operations.md](migrations-operations.md) says what to do.

## Running the generator from Go

ent's `entc.go` calls `entc.Generate` with a
`gen.Config`. velox's is `generate.Run(ctx, dir, generate.Config{...})`,
from package `github.com/syssam/velox/generate`, in a `generate.go` behind
`//go:build ignore` ([decision 0097](decisions/0097-generate-package.md)).
It writes what `go tool velox-gen` writes with the same settings.

| ent | velox |
|---|---|
| `entc.Generate("./schema", cfg)` | `generate.Run(ctx, "", generate.Config{Schema: "./veloxschema"})` |
| `gen.NewConfig(opts...)` | a `generate.Config` literal; the zero value is velox-gen with no flags |
| `gen.WithTarget(dir)` | `Out` (default `./veloxgen`) |
| `gen.WithPackage(path)` | not needed: the import path is the module's path joined to `Out`'s; `Package` names the package (default `Out`'s base name) |
| `gen.WithSchema(path)` | `Schema`, a package pattern (default `./veloxschema`) |
| `gen.WithFeatures(FeaturePrivacy, FeatureIntercept, ...)` | always on, so no flag: privacy is `Policy` and package `privacy`, intercept is `Interceptors` as row filters, lock is `ForUpdate`, modifier is `Expr`, `Calc`, `Select` and `SetExpr`, upsert is `OnConflict`, execquery is `db.Exec`, `Rows` and `Raw`, autodefault is the zero `DEFAULT` (decision 0090), entpredicates are the handles and `FieldEQ`, versioned migrations are velox-gen migrate, schemaconfig is `TableIn` and `sqlschema.Schema`; snapshot is not needed, as velox-gen loads a schema whose generated package is stale |
| `gen.WithHooks(...)` | not offered. A hook that sorts each entity's fields has no form: `CREATE TABLE` follows the schema's declaration order, so declare the fields in the order wanted. A prefix hook for index names is the default (decision 0090). |
| `gen.WithTemplates`, `entc.TemplateDir`… | not offered: the generated package is entirely velox-gen's, reproducible from the schema alone, so `velox-gen -check` proves it current (decision 0034). Code of your own goes in a file of yours beside it. |
| `entc.Extensions(entgql.NewExtension(...))` | `Plugins`: `generate.Plugin{Tool: "velox-graphql"}` runs the tool; `graphqlgen.Plugin(opts)` from `contrib/graphql/graphqlgen` runs it in the process, with the same output |
| `gen.WithBuildFlags`, `entc.BuildFlags("-p=4")` | `Env: append(os.Environ(), "GOFLAGS=-p=4")`, the environment of every go command Run starts |
| `gen.WithStorageDriver`, `gen.WithIDType`, `gen.WithHeader` | not offered: the dialect is the migration directory's, IDs are int64 unless the schema declares one, and the header is velox-gen's, which marks the files it owns |

A program with a plugin that runs only in the process is its own check: run
it with `Check: true` in CI, where it returns `generate.ErrStale` when a file
is out of date. `velox-gen -check` does not know such a plugin.

## Behaviour a port sees change

**`HasWith` sees soft delete and row filters.** A `WithFilter` filter, an
interceptor and `softdelete.Mixin` reach every `Has` and `HasWith`, the
GraphQL `hasX` and `hasXWith` included. So a parent whose only matching
children are soft-deleted, or hidden by a row filter, does not match.
Check each `hasXWith` a client sends, and each `HasWith` in service code.
To match deleted children too, run the query under the context that shows
deleted rows, such as `softdelete.Skip(ctx)`. That context shows deleted
parents as well.

**A filter must not restrict a join entity.** A `WithFilter` filter that
returns a condition for the join entity of a many-to-many edge, or for an
edge schema, fails `Has`, `With`, `LoadEach` and `CountEach` through it.
Return `velox.Allow()` for those names
([ADR 0098](decisions/0098-hooks-and-filters-for-every-entity.md)).

**A filter is asked about writes.** ent's interceptors wrap queries. A
velox filter is called with the op, `OpSelect` for reads and the write's
op for updates and deletes. A filter that narrows reads only returns
`velox.Allow()` when `op != velox.OpSelect`. Otherwise a restore that
clears `deleted_at` under the plain context updates no row. A filter finds
columns by name: it asks `e.HasColumn("deleted_at")` of the
`velox.FilterEntity` it is given, in place of a generated list of entity
names.

**A read on the client inside a transaction fails.** Inside `db.Tx`, a
statement on `db` under fn's `ctx` fails with `ErrOutsideTx`, where ent's
client runs it outside the transaction. Run it on `tx`, or on `db` under
`velox.WithoutTx(ctx)` when it must not join the transaction; on SQLite's
pool of one connection that fails too, rather than wait for the
transaction. A `WithTx(ctx, db, fn)` helper that kept its own transaction
key in the context, so that a nested call joined the outer transaction,
asks `velox.TxFrom(ctx, db)` instead: it returns the transaction ctx
belongs to, with `db`'s own `WithoutPolicy` reason, and the helper runs fn
on it, or begins `db.Tx` when it is false
([ADR 0137](decisions/0137-nextapp-port-helpers.md)). A lookup on `db`
that swallows its error narrows the statement inside a transaction and
not outside.

**A rule may name columns from configuration.** `velox.FieldIn`,
`FieldEQ` and `FieldInSelect` take any string. velox-gen checks a constant
name and warns on any other, and the statement checks it when it runs.
An unknown name fails the statement, naming the entity and the column.
To check a rule's SQL in a test, render a query that holds it with
`velox.Open(d, nil)` and `Query.SQL`, which needs no database
([guide](guide.md#policies)).

**A transaction retries nothing by default.** A velox `Tx` runs once,
under `WithDeadlines`' `Tx` limit, and panics again after the rollback.
`velox.WithTxDefaults` at `Connect` gives a retry and a timeout to every
`Tx`, and a call's own options override them. A nested `Tx` does not retry
on its own, and `gqlgenx`'s mutation transaction never retries. A panic
stays a panic: recover it in fn to make it an error.

**A zero a setter gives is stored.** A field such as `exchange_rate`, with
a Go `Default` and `sqlschema.Default("1")`, stores 0 from
`Set(X.ExchangeRate.Set(0))`, as ent's `SetExchangeRate(0)` does: every
column a setter names is written
([ADR 0136](decisions/0136-explicit-zero-and-empty-patch.md)). A zero the
struct holds with no setter is unset and takes the default. A
`decimal.Decimal` that holds a non-nil `*big.Int`, such as `decimal.Zero`,
is not the Go zero, and is written from the struct too
([guide](guide.md#fields)).

**An empty patch still runs its hooks.** An update whose setters are all
`SetPtr(nil)` runs its hooks and its `UpdateDefault`s, so `updated_at`
moves. When they assign nothing, no statement is sent and `Exec` returns 0
([ADR 0136](decisions/0136-explicit-zero-and-empty-patch.md)).

**A self one-to-one reads from the right end.** With the key on the assoc
edge, as in `reversed_by`/`reversal_of`, the inverse edge returns the row
whose key holds its ID
([ADR 0085](decisions/0085-self-one-to-one-key-on-assoc.md)).

**A decimal Other field with no SQLite `SchemaType` is a `numeric`
column**, so a `decimal.Decimal` field whose `SchemaType` names MySQL and
PostgreSQL only opens on SQLite and compares as a number. The text `"1.10"`
is stored as 1.1. Any other Other field still needs a `SchemaType` for
SQLite ([ADR 0148](decisions/0148-other-decimal-numeric-on-sqlite.md)).

## Removed schema options

velox keeps one spelling of each schema option, `entsql`'s where ent has
one. A port that names one of these does not compile; write the
replacement.

| Removed | Write instead |
|---|---|
| `schema.Table(name)` | `sqlschema.Table(name)` |
| `schema.TableIn(schema, table)` | `sqlschema.SchemaTable(schema, table)` |
| `schema.Collation(c)` | `sqlschema.Collation(c)` |
| `schema.Check(name, expr)` | `sqlschema.Checks(map[string]string{name: expr})` |
| `index.Fields(...).Where(pred)` | `index.Fields(...).Annotations(sqlschema.IndexWhere(pred))` |
| `field.X(...).SchemaDefault(m)` | `field.X(...).Annotations(sqlschema.DefaultExprs(m))` |
| `field.Postgres`, `field.MySQL`, `field.SQLite` | `dialect.Postgres`, `dialect.MySQL`, `dialect.SQLite` |
| `sqlschema.Check(expr)`, `Annotation.Check` | `sqlschema.Checks`, which names the constraint |
| `sqlschema.Size(n)`, `Annotation.Size` | `field.MaxLen(n)` |
| `sqlschema.ColumnType(t)`, `Annotation.ColumnType` | `field.SchemaType`, by dialect |
| `sqlschema.OnDelete(a)`, `Annotation.OnDelete` | `edge.OnDelete(a)` |
| `sqlschema.CascadeAction`, `Cascade`, `SetNull`, `Restrict`, `NoAction` | `edge.Action`, `edge.Cascade`, `edge.SetNull`, `edge.Restrict`, `edge.NoAction` |
| `sqlschema.SetDefault` | not supported |
| `sqlschema.OnUpdate(a)`, `Annotation.OnUpdate` | not supported: velox writes no `ON UPDATE` |
| `sqlschema.Charset(c)`, `Annotation.Charset` | not supported |
| `sqlschema.Skip()`, `Annotation.Skip` | not supported |
| `sqlschema.IncrementStart(n)`, `Annotation.IncrementStart` | not supported |
| `sqlschema.WithComments(b)`, `Annotation.WithComments` | not needed: velox writes a comment where the dialect has them |
| `sqlschema.StorageParams(p)`, `IndexAnnotation.StorageParams` | not supported |
| `sqlschema.Prefix(n)`, `sqlschema.PrefixColumn(name, n)`, `IndexAnnotation.Prefix`, `PrefixColumns` | not supported: velox does not model a MySQL index prefix |
| `sqlschema.IncludeColumns(...)`, `IndexAnnotation.IncludeColumns` | not supported: velox does not model a covering index |

## Refused by design

- **Not built: back-references.** ent's load can fill each loaded
  child's inverse edge with its parent, so that `child.Edges.Parent` is set
  after `parent.With(children)`. velox does not: a child's to-one edge is
  set only when that edge is loaded, with `With(C.Parent)` on the child's
  query (ADR 0101).
- **Templates, graph hooks, `WithGenerator`, `WithHeader`, feature flags
  and dependency injection.** The generated package is velox-gen's alone,
  so `velox-gen -check` proves it current from the schema
  ([ADR 0034](decisions/0034-schema-declares-behaviour.md),
  [0097](decisions/0097-generate-package.md)).
- **`field.Any`, `field.Deprecated`, `field.ValueScanner`, an edge's
  `StructTag` and `sqlschema.View`.** Not built; nothing asked for them.
- **`FeatureCheckBounds`.** `Min`, `Max` and `Range` make no `CHECK`.
  Declare one with `sqlschema.Checks`.
- **Composite keys and `field.ID`.** An edge schema is keyed by its own
  `id` ([ADR 0109](decisions/0109-edge-schemas.md)).
- **An exported SQL builder.** `sql.Selector` and `sqlgraph` are not
  public, so no code can drop a policy from a statement
  ([ADR 0069](decisions/0069-expr.md)).
- **Named eager loads (`WithNamedX`).** One `With` per relation.
- **`AllX` and the other panicking variants, and `Clone`.** Builders are
  values, and every call returns its error.
- **`querylanguage.P` (entql).** `velox.FieldEQ` and its kin, and package
  `untyped`, name columns by string.
- **A per-client table schema at run time.** `sqlschema.SchemaTable` and
  `sqlschema.Schema` are fixed at generation.
- **`privacy.NewPolicies` and `Policies`.** velox composes a schema's and
  its mixins' policies itself.

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
- **No Node by default, and an ID is the key.** entgql's `WithRelaySpec`,
  on by default, turns on Relay connections and `Node` together.
  velox-graphql always writes connections, and writes `Node` only with
  `node=on`: the `Node` interface, `node` and `nodes` first in the root
  query, and `Noder`, `ResolveNode` and `ResolveNodes`. An ID is the
  entity's key in its own Go type, which gqlgen decodes: a resolver takes
  `id int64`, and an input's `authorID` is an `int64`. Every key of a
  schema's GraphQL types has one Go type, since gqlgen binds every ID
  argument to the first model `gqlgen.yml` lists for `ID`: `gqlgenx.Int64ID`
  for int64, `gqlgenx.UUID` for UUID, gqlgen's `graphql.ID` for string.
  velox-graphql refuses a schema whose keys differ (ADR 0061). Since an ID
  holds no type, `node(id:)` needs IDs unique across types; auto-increment
  keys collide, which is why `node=off` is the default. entgql finds an
  ID's type from ent's universal-ID ranges or from `WithNodeType`; velox
  finds the types that hold an ID with one statement, for any key type, and
  reports an ID two types hold as ambiguous. The generated `WithNodeType`
  and `WithFixedNodeType` are entgql's: with them, only the type given is
  read and no lookup runs (ADR 0152).
- **Where inputs read empty values as SQL does.** entgql adds nothing for
  them; velox-graphql gives each its meaning: `xIsNil: false` is `IS NOT
  NULL`, an empty `xIn` matches no row, `or: []` matches no row, and `not:
  {}` matches none, where entgql refuses an empty input. An empty `and` or
  `hasXWith` adds nothing.
- **OrderVia keeps every row.** An `orderBy` through an edge reads it with a
  LEFT JOIN: a row whose target is missing or hidden sorts as NULL, and the
  connection's rows and `totalCount` are the same as without the order.
- **An edge's count orders as entgql's, and counts what the reader sees.**
  `graphql.OrderField("POSTS_COUNT")` on a to-many edge adds `POSTS_COUNT`
  to the owner's order field, as in entgql. It orders by `Rel.Count`, a
  subquery under the target's Read policy, interceptors and filters, so a
  post the reader may not see is not counted; entgql counts every row. The
  connection's rows and `totalCount` are as without the order. It is refused
  on a to-one edge (use `OrderVia`) and on an edge with an application
  directive. A request that orders by it costs what a `has<Edge>With` costs.
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
- **Limits apply to every request.** A connection with no `first` or
  `last` reads `MaxPage` rows, 100 by default, and a `first` or `last`
  above it fails with `ErrPageTooLarge`; under graphql-go `MaxPage` is
  fixed at 100. A list edge, one with no connection, reads at most
  `MaxList` rows, 1000 by default; more fails with `velox.ErrTooManyRows`.
  A cursor taken under another order is refused, and so is `after: ""`.
  `first` with `before`, `last` with `after`, and `after` with `before`
  are a `BAD_USER_INPUT` error. `totalCount` runs its COUNT only when the
  field is resolved. `xContainsFold` and `xEqualFold` compare `LOWER(x)`
  with `LOWER(v)`. An error from velox carries a code, such as
  `BAD_USER_INPUT`, in `extensions.code`.
- **Options shape the API.** velox-graphql takes `key=value` options after
  a colon, where entgql takes Go options; [the guide](guide.md#velox-graphqls-options)
  lists them. `where=whitelist` stands in for a hook that skips the where
  input of every field without `graphql.WhereInput`; `engine=graphql-go`
  serves [graphql-go](https://github.com/syssam/graphql-go) in place of
  gqlgen; `sdlfiles=entity` writes one SDL file per entity, and
  `entities=A+B` the SDL of some entities alone
  ([ADR 0125](decisions/0125-graphql-sdl-per-entity-and-subset.md)).
- **Not built:** a chosen SDL path (the SDL is in the generated package),
  federation, subscriptions, interfaces and unions, input validation
  annotations (generated inputs run the schema's validators),
  `WhereInputFields`, `Unbind`, `MapsTo`, `SkipField`, the generator's
  hooks (`WithSchemaHook`, `WithTemplates`, `WithGQLGenConfig`) and
  `FeatureWhereInputAll`: where inputs are opt in, field by field, with
  `graphql.WhereOps` or `graphql.WhereInput`.
