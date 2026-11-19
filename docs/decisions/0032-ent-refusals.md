# 32. What ent users write is generated, or refused with the way out

Status: accepted, 2026-11-02. Its explicit-null bullet is reversed by
[0045](0045-graphql-input-setters.md): a null is a field not given, as in entgql.
Its refusal of a non-zero Default on a bool or number, and the 2026-11-18
amendment's `ZeroInvalid`, are withdrawn by
[0082](0082-defaults-before-setters.md): defaults are filled before setters
and hooks, so a setter stores false or 0 over them.

## Context

ent and entgql users found four refusals that turned away schemas ent accepts:
`Default(false)` on a bool, an `OrderField` on a nullable field, a
`field.Strings` served as the `JSON` scalar, and a Uint64 over 2^63-1 with no
reason given. velox inserts structs, and a zero field there means unset. Each of
the decisions below keeps that rule and removes the refusals it does not need.

## Decision

- **A zero Default.** A Default equal to the Go zero value on a bool or number
  that is not Nillable is allowed. velox-gen (rule 36), the GraphQL plugin's sdl
  check and the engine's Descriptor check all allow it. The zero it stores is what
  unset stores, so nothing is lost. The plugin's create mutation no longer
  refuses a zero given for such a field.
- **A non-zero Default stays refused.** ent fills `Default(true)` only when the
  builder never set the field. velox cannot tell a struct's `false` from unset:
  accepting the Default would silently store true where the caller wrote false.
  That is the unsafe choice, so it stays refused. The message now gives the ways
  out: make the field Nillable, name it so that its zero is the default, or drop
  the Default. For GraphQL, `graphql.CreateDefault(v)` renders `active: Boolean! = true`
  in the create input. gqlgen fills it when a client leaves the field out, and
  the type stays `Boolean!`. The Go field and the column are as they were. The
  value must match the field's type and range. It is refused on a field that is
  never input (tenant, foreign key, primary key), on a skipped or Nillable field,
  on one with a Default, and on an entity with no create mutation.
- **NULL sorts as the largest value.** This holds on every dialect: an ascending
  order puts NULL last, a descending one first. PostgreSQL does this natively.
  On MySQL and SQLite, ORDER BY adds `col IS NULL` (`DESC` for a descending
  order) before a nullable column. `After` builds null-aware keyset conditions:
  past a value ascending, `> v OR col IS NULL`; past NULL, `IS NULL` for the tie
  and `IS NOT NULL` descending. A unique index over a nullable column is not a
  total order, as NULLs repeat, so the primary key still closes such an order.
  `gqlimpl.NullTerm` carries NULL in the cursor as a JSON null, and the plugin
  uses it for an `OrderField` on a pointer field. Grouped aggregates' NULL key
  sorts last too, which supersedes the 0022 bullet.
- **JSON lists.** A JSON field of `[]string`, `[]int`, `[]int64`, `[]float64` or
  `[]bool` is a GraphQL list of the non-null scalar (`[String!]`) in the type
  and in the inputs. It binds the Go slice, with no getter. Filters do not
  change. Other JSON stays the `JSON` scalar.
- **A Uint64 over 2^63-1** is still refused, since database/sql cannot send a
  uint64 with the high bit set. The message now says so.
- **A required field the create input leaves out is the resolver's to set.**
  entgql users skip a field with `Skip(SkipMutationCreateInput)` and set it in
  the resolver. `Create<T>` inserted its zero value silently, "" in a NOT NULL
  column, and a validator's error on it named `Create<T>Input.<field>`, a field
  the SDL has not. Now, when the create input leaves out a field that is not
  Optional, not nullable and has no Default (the key and the tenant aside),
  `Create<T>` takes `set func(*T)`, which it calls after the input's values and
  before the validators. Three designs were weighed. A variadic option on every
  `Create<T>` keeps resolvers compiling, which is what let the zero through. A
  generation-time refusal cannot see a `velox.Hook` that fills the field, and
  needs an annotation to lift it. The parameter only where the schema needs it
  makes the resolver gqlgen stubbed fail to compile at the call, which is the
  refusal with its way out, and leaves every other `Create<T>` as it was; nil
  says a Hook fills it. Each input names its own fields in errors
  (`<entity>CreateFieldGQL`, `<entity>UpdateFieldGQL`), so a field set by the
  resolver names none. An update has nothing to mirror: a field its input leaves
  out keeps its value.
- **An explicit null on an update's nullable field clears it.** entgql treats
  it as not given. This stays (0027): a client writing `{field: null}` means
  NULL, and `clear<Field>` stays for clients that think otherwise. It is listed
  first under "Differences from entgql" in contrib/graphql's documentation.

## Consequences

- `veloxgql.GeneratedCodeIsVersion8`: generated GraphQL code must be
  regenerated. The core guard stays at 7.
- SDL breaking change: a `field.Strings` (and the others above) was `JSON`, and
  is now `[String!]`. Clients that sent a JSON text must send a list.
- On MySQL and SQLite, an ORDER BY on a nullable column puts NULL last ascending
  where it was first, and an index on the column cannot serve the added
  `IS NULL` term. Grouped NULL keys move the same way.
- A non-zero Default on a non-Nillable bool or number is still refused outside
  GraphQL. Ordering by bytes or JSON is still refused.
- `Create<T>` of an entity whose create input leaves out a required field takes
  a third argument: its resolver must be changed when the code is regenerated.
  The runtime is unchanged, so `GeneratedCodeIsVersion8` stays.

## Evidence

`TestBuildZeroDefaultAllowed`, `TestDescriptorDefaultRules`,
`TestZeroDefaultNotRefused`, `TestCreateDefault`, `TestCreateDefaultServed`;
`TestAfterNullable`, `TestAfterNullableUniqueIndex`, `TestAfterNullsExact`
(SQLite, PostgreSQL through pgx and database/sql, MySQL), `TestPaginateNullable`,
`TestNullableOrderTerm`, `TestNullableOrderServed`; `TestJSONLists`,
`TestJSONListsServed`, `TestJSONInputExact`; `TestUint64OverSaysWhy`;
`TestCreateSetsSkipped`, `TestSkippedFieldSetByResolver`; `TestUpdateNullKeeps`
(once `TestUpdateNullClears`, before 0045).

## Review

2026-11-05, the non-zero Default re-examined for ent users. The refusal
stays. Two ways to accept `field.Bool("active").Default(true)` were weighed.
Writing the struct as it is, with the Default as the column's only, stores
false where a ported ent create left the field unset: a silent difference.
A pointer Go field over a NOT NULL column keeps ent's column and its create,
but its type says NULL where the column holds none, it breaks 0003's rule
that a pointer is NULL, and the engine, the handles and the GraphQL runtime
all read a pointer field as nullable. The message now spells the ways out
with the field's own builder, name and value, `SchemaDefault` included, which
keeps an ent database's `NOT NULL DEFAULT true`. `TestBuildDefaultWaysOut`
checks the columns of the lines it suggests.

The same day, an `OrderVia` of a nullable field was allowed, and an
ordering by a joined nullable column sorts NULL last on MySQL and SQLite
too: see 0031's Review.

`field.Strings` was never refused by velox-gen: it is a JSON column, as in
ent, and 0032's JSON lists serve it in GraphQL. The binding model now holds
a required `field.Strings` and `field.Ints` beside the Optional one, so
`TestBindingRoundTrip` applies the planner's migration of a NOT NULL JSON
list and round-trips it on SQLite, PostgreSQL and MySQL.

## Amendment, 2026-11-18: a number a check refuses 0 for

The refusal of a non-zero Default exists because reading 0 as unset would
overwrite an explicit 0. A number whose built-in checks refuse 0 (`Positive`,
`Negative`, a `Min` above 0, a `Max` below 0, a `Range` that excludes it) never
stores 0, so nothing is overwritten that could have been stored. velox-gen
(rule 36) and the plugin's sdl check now allow a non-zero Default or
DefaultFunc on such a number, and old velox's
`field.Int("seats").Positive().Default(100)` ports as it is. A bool is still
refused, whatever its checks: false and true are both values. The engine's
Descriptor check cannot see checks, so velox-gen writes
`ColumnDescriptor.ZeroInvalid` for a literal non-zero Default of such a number
and the engine reads it in place of refusing (not for a DefaultFunc, which the
engine never checked, and not on a bool). An explicit 0 on such a field takes
the Default instead of failing the check, as unset does. `GeneratedCodeIsVersion20`.
Evidence: `TestBuildZeroInvalidDefault`, `TestRenderZeroInvalid`,
`TestZeroInvalidDefault` (engine), `TestZeroInvalidInsert`,
`TestDefaultOnNumberThatIsNeverZero` (sdl).
