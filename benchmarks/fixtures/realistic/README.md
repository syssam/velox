# realistic

A 450-entity schema that is shaped like a real large application, for
measuring what an ORM's generated code costs the Go compiler. Nothing in it
comes from that application except counts and proportions.

In these notes `velox` is the generator being replaced (`cmd/velox`, the
`runtime` package) and `orm` the new one (`cmd/velox-gen`, package `orm`),
which was developed under the name vx: a measurement taken then carries the
date it was taken on and is of the code of that day.

The other fixtures in this directory (`stress-100`, `stress-200`, `stress-328`)
are chains of entities with four fields each. They are useful for the
generator's speed and understate the compiler's work: the application this
fixture models has 32 fields per entity, a few hub entities with 15 to 30
edges, tenant policies and hooks on most entities, and its generated code
takes the compiler 20 times longer per entity than a stress entity's.

## Shape

Measured on the reference application (454 entities), and on this fixture
(`go run shape.go`):

| | reference | fixture |
|---|---|---|
| entities | 454 | 450 |
| fields per entity, mean | 32.1 | 30.8 |
| fields per entity p10 / p50 / p90 / p99 | 19 / 30 / 48 / 92 | 17 / 28 / 48 / 73 |
| edges per entity, mean | 2.74 | 2.76 |
| edges per entity p50 / p90 / p99 / max | 2 / 6 / 16 / 30 | 2 / 6 / 18 / 31 |
| optional / nillable / with default | 0.54 / 0.41 / 0.44 | 0.52 / 0.36 / 0.41 |
| enum / JSON / immutable / validated | 0.08 / 0.04 / 0.09 / 0.18 | 0.08 / 0.04 / 0.09 / 0.19 |
| policies, hooks (stitched) | 496, 1165 | 474, 1153 |
| indexes per entity | 7.5 | 6.9 |

Entities have an int64 id with a generated default, three shared mixins
(`TimeMixin`; `WorkspaceMixin` with a tenant policy and two hooks;
`AuditMixin` with a hook; `SoftDeleteMixin` on some), and a minority have a
policy or a hook of their own. Relations are mostly one-to-many with a foreign
key field on the child, some one-way, a few many-to-many and one-to-one, and
the parent of each is drawn with preferential attachment so a handful of
entities become hubs. Every entity carries the GraphQL annotations the
application uses (a query field, a Relay connection, where-input fields).

The hooks in the mixins use only the `velox.Mutation` interface, so the same
schema runs on every ORM that reads velox schemas.

## Regenerate

The schema is a pure function of the seed:

```sh
go run gen_schema.go -n 450 -seed 1      # writes schema/rec*.go
go run shape.go                          # prints the table above
```

`schema/mixins.go` and `types/` are written by hand.

## Measure

```sh
benchmarks/compile-bench.sh velox        # cold, noop, hook, field
benchmarks/compile-bench.sh orm
GOGC=200 benchmarks/compile-bench.sh orm cold
```

It generates the ORM's code into `velox/` or `db/` (not tracked), builds it
with an empty build cache and a timing wrapper (`benchmarks/tools/
compile-timing.sh`), and prints wall time, number of packages compiled, CPU
time, and the largest compile's memory, for a cold build, a repeated build, an
edited hook body and one added field. Run it on an idle machine; it warns if
the load is high.

`REALISTIC_GRAPHQL=0` generates velox's code without its GraphQL extension.

## Results

`-p 4`, empty build cache, Apple M-series, 14 CPUs, 2026-10-08. The machine was
busy (load average 25 to 35 for these runs), which inflates wall time and
somewhat CPU time: the same velox cold build measured 1,324 CPU-s at load 62
and 1,098 CPU-s at load 34. Repeat on an idle machine before quoting.

| | velox (core + GraphQL extension) | orm |
|---|---|---|
| cold: packages compiled | 910 | 454 |
| cold: CPU | 1,098 s | 97 s |
| cold: wall | 659 s | 62 s |
| cold: largest compile | 5.8 GB (`query`) | 1.5 GB (`model`) |
| repeated build | 0 packages | 0 packages |
| hook body edited: packages, CPU | 2, 20 s | 1, 9 s |
| one field added to one entity: packages | 460 | 4 |
| one field added: CPU | 821 s | 34 s |
| one field added: wall | 285 s | 38 s |
| one field added: largest compile | 7.1 GB (`query`) | 1.4 GB (`model`) |

For orm, a field added to one entity rebuilds that entity's package, `model`,
`client` and `schemainit`; every other entity package is untouched because
entity packages import only the `predicate` package. For velox it rebuilds
every package that imports the shared `entity`, `query` and `filter`.

This table is about the generated ORM packages only. What an application's own
packages pay for importing them is a separate cost.

### Mock application: 312 services on top of the ORM

`gen_services.go` writes `services_velox/` and `services_orm/`: 312 packages, each
with about 24 ORM calls drawn from the call mix of the modelled application
(queries 52%, lookups by id 15%, one-row updates 15%, creates 9%, deletes,
bulk creates; a third of the queries with an eager load, a quarter of the
writes in a transaction) and about 1,200 lines in all, written against velox's
API and against orm's from the same plan. `benchmarks/compile-bench.sh <orm> app`
builds the ORM first and times the services alone.

| | velox | orm |
|---|---|---|
| 312 services: CPU | 1,151 s | 190 s |
| per service: median CPU | 3.44 s | 0.65 s |
| per service: median memory | 1,069 MB | 143 MB |
| largest service compile | 1,152 MB | 199 MB |

For calibration: the same measurement on the real services of the modelled
application (velox) gave a median of 3.18 s and 895 MB per service, so the mock
is a fair stand-in on the velox side. Nothing like the real orm number exists
yet; the mock is written to fit orm's API as orm's author sees it.

ORM plus services, cold: velox 2,249 CPU-s, orm 287 CPU-s (7.8x). The
application's GraphQL layer is not in this fixture.

## orm after the package rename (2026-10-08, load 40-80)

The generated tree is now `db` (root, where queries start), `db/entity`,
`db/migrate`, `db/predicate`, `db/schemainit` and a package per entity; the
root package links `schemainit`.

| scenario | packages compiled | CPU | wall | largest compile |
|---|---|---|---|---|
| cold (three runs) | 455 | 92.6 / 97.1 / 99.4 s | 50 – 83 s | 1.5 GB (`entity`) |
| noop | 0 | 0 | 1.2 s | |
| hook body edited | 2 (`schemainit`, root) | 11.6 s | 10.8 s | 0.6 GB |
| one field added | 5 | 33.9 s | 41.6 s | 1.6 GB |
| 312 services (two runs) | 312 | 234 / 250 s | | 0.2 GB |

Linking `schemainit` from the root package added one small package to the
hook and field edits (they were 1 and 4) and nothing else: the root package's
compiled form does not change, so nothing that imports it is rebuilt.

`ORM_FLAGS=-setters ORM_SETTERS=1 benchmarks/compile-bench.sh orm cold app`
measures a `Set<Field>` method per field: the `entity` package goes from
18,958 to 46,348 functions, 14 to 28 CPU-s and 1.6 to 2.6 GB (four alternating
runs each: 13.9/14.2/17.9/14.2 against 26.1/31.4/29.1/27.8 s); the services
that call them are within the noise (249 and 211 CPU-s).

## What a velox-shaped surface costs on the orm engine (2026-10-08, one round, load 30-80)

(The root struct of this section came from an experimental generator option,
`-handle`, that was removed once it had been measured; `-setters` remains.)

`ORM_FLAGS="-setters -handle" ORM_SETTERS=1 ORM_HANDLE=1` generated velox's setter
families (`Set<F>`, `SetNillable<F>`, `Clear<F>`: 76,564 functions in `entity`
against 18,958) and a root struct with a field per entity that the services
name. The run was stopped by the system for low memory before its second
round, so these are single measurements beside two of the plain build:

| | ORM cold CPU | `entity` memory | 312 services CPU | per service (median) |
|---|---|---|---|---|
| plain | 76.8 / 95.7 s | 1.5 GB | 209.6 / 233.6 s | 0.70 / 0.78 s, 157 MB |
| setter families | 148.7 s | 3.45 GB | 313.0 s | 1.03 s, 124 MB |
| setter families + root struct | 177.7 s | 3.64 GB | 556.3 s | 1.72 s, 382 MB |
| velox | 1,098 s | 5.8 GB | 1,151 s | 3.44 s, 1,069 MB |

The root struct is the expensive part: every package that names it reads every
builder's method set. The services number of the setter-only row was taken
while the load was rising and is not trustworthy on its own.

## Column descriptors in ten designs (2026-10-08, load 10-30)

What a column descriptor costs to compile, for the same 450 entities and, for
the users, the 19,355 descriptor expressions of the mock services
(`rec010.NameField.EQ(x)`, `.To`, `.GTE`, `.Desc`) in 311 packages that hold
nothing else. CPU seconds of the compiles, three rounds (two for the rows
marked *, which were taken at a higher load beside orm at 58.5 / 62.0 and
14.6 / 15.3):

| design | value type checked | entity checked | 450 descriptor packages | 311 user packages |
|---|---|---|---|---|
| orm: generated types, methods with pointer receivers | compile | compile | 40.5 / 43.3 / 41.9 | 8.9 / 8.8 / 9.0 |
| the same with value receivers * | compile | compile | 100.3 / 108.4 | 14.3 / 15.1 |
| generic `Column[T]`, value receivers * | compile | no | 96.2 / 100.5 | 35.8 / 37.2 |
| generic `Column[T]`, pointer receivers | compile | no | 32.2 / 31.7 / 32.9 | 16.3 / 16.3 / 17.1 |
| generic `ColumnE[E, T]`, value receivers * | compile | compile | 99.0 / - | 41.2 / 40.9 |
| generic `ColumnE[E, T]`, pointer receivers | compile | compile | 33.3 / 33.0 / 37.6 | 20.0 / 20.3 / 24.1 |
| generic functions, `orm.EQ(col, v)`, `Column[T]` | compile | no | 3.9 / 4.0 / 4.0 | 5.8 / 5.8 / 6.5 |
| generic functions, `ColumnE[E, T]` | compile | compile | 4.1 / 4.1 / 4.0 | 6.7 / 6.8 / 7.1 |
| shared runtime types (`orm.StringCol`), no generics | basic types only | no | 2.8 / 3.0 / 3.1 | 3.6 / 3.6 / 3.6 |
| orm's types, all in one package * | compile | compile | 43.8 / 31.1, 3.1-3.3 GB | 56.4 / 36.8 |

What the table says:

- **The receiver matters more than generic or generated.** A method with a
  value receiver is compiled twice; with pointer receivers a generic
  `Column[T]` costs in total what orm's generated types cost (cheaper to
  declare, dearer to use). An earlier version of this section compared orm with
  value-receiver generics only and concluded that generics cost 1.8 to 3
  times more; that comparison was unfair and is withdrawn.
- **Method syntax is what is paid for.** Any design in which `col.EQ(v)` is a
  method that returns an entity-typed predicate compiles that method in every
  entity's package: 33 to 43 CPU-s here. The same checks with functions,
  `orm.EQ(col, v)`, cost 4, because a generic function is compiled only where
  it is called.
- One package for every entity's columns costs the same CPU serially, 3 GB,
  and makes every user dearer.
- orm's descriptor packages are 91% column methods (162 of 178 functions on
  average); the rest, which the other designs do not have, are edges.

In proportion: the descriptor packages are about 41 of the 290 to 330 CPU-s of
a cold build of the ORM and the services, they compile in parallel, and a
schema edit recompiles one of them.

### The function style in full

The function rows above have one column type and three operators. Built out to
carry every check the method style has, the column's type says what the
column allows, `orm.Col[E, T, N, C, A, S]` (entity, Go type, can be NULL, can
be cleared, can be added to, has an order), and each function asks for what
it needs: `IsNull` for `N = Yes`, `Contains` for `T ~string`. Eight mistakes
the method style rejects were tried and all eight do not compile: a
condition, assignment or order of another entity, a value of the wrong type,
`IsNull` on a NOT NULL column, `Clear` on a column that cannot be cleared,
`Add` on a string, `Contains` on an integer. The messages are longer: "type
fcol.Col[rec010.Entity, int64, fcol.No, fcol.No, fcol.Yes, fcol.Yes] of
rec010.IDField does not match ..." where the method style says
"rec010.IDField.IsNil undefined".

| | 450 descriptor packages | 311 user packages | generated source | language server, descriptors only |
|---|---|---|---|---|
| orm: methods | 39.8 / 41.6 / 44.8 CPU-s | 8.7 / 8.7 / 9.0 | 18.6 MB | 1.7 GB |
| functions, every check | 4.2 / 4.3 / 4.6 | 7.4 / 8.0 / 7.3 | 3.1 MB | 0.6 GB |

## The language server (2026-10-08)

`gopls check` (v0.22.0) of one service file, from an empty gopls cache, in a
workspace that holds one ORM's generated code and its 312 services. Two runs:

| | wall | CPU | peak memory |
|---|---|---|---|
| orm | 9 s / 8 s | 42 s / 43 s | 5.1 / 5.0 GB |
| velox | 43 s / 41 s | 290 s / 286 s | 12.2 / 12.3 GB |

## What an entity's methods name (2026-10-09, load 15-55)

A package compiles against everything the types it names expose: fields, and
the parameters and results of methods. orm had two methods per edge that named
another entity's query builder, `(*User).QueryPosts(ex) *PostQuery` and
`(*UserQuery).WithPosts(...*PostQuery)`. Through them a service that used one
entity read the query builder of every entity.

`cold` is the ORM alone, `app` the 312 services on top of it; CPU-s, three
rounds, and the median service package.

| | cold | app | median service |
|---|---|---|---|
| both methods (before) | 74 / 75 / 91 | 172 / 210 / 211 | 0.58-0.72 s, 144-154 MB |
| `WithPosts` takes an interface, entity method kept | 66 / 91 | 162 / 217 | 0.56-0.74 s, 139-158 MB |
| entity method removed, `WithPosts` takes `*PostQuery` | 67 / 69 / 66 | 176 / 174 / 195 | 0.57-0.66 s, 121-147 MB |
| both: a function `db.QueryUserPosts(ex, u)` | 90 / 66 / 67 | 110 / 80 / 80 | 0.23-0.32 s, 61-78 MB |
| both: a client method `db.User.QueryPosts(ex, u)` (**adopted**) | 94 / 66 / 66 | 113 / 85 / 80 | 0.23-0.33 s, 68-77 MB |

Either change alone gains nothing: the graph stays connected through the
other. Both together halve the services. As a client method the traversal
costs what the function does, because a client is read only by the packages
that use that entity, and the neighbors' queries no longer lead anywhere; the
real schema has at most 30 edges on an entity (median 3).

The language server, cold `gopls check` of one service with the generated
code in the workspace: 4.8-5.0 GB before, 2.8-3.4 GB after.

Pinned by `internal/ormgen/reach_test.go::TestBuildersStayOutOfTheTypeGraph`.

### Tried the same day, without effect

- **The same column methods, cheaper bodies.** `//go:noinline` on each, a
  shared helper for the body, a nullable column type embedding its base type
  (170 -> 128 functions a package), and all three together: 450 descriptor
  packages in 49-78 CPU-s against 54-66 for the methods as they are, on a
  machine at load 25. No variant is outside the noise.
- **The generated code as a module of its own**, a dependency of the
  application's module instead of a directory of it: `gopls check` of a
  service peaks at 4.2-4.4 GB against 4.8-5.0 GB, same wall time.

## The edit loop: schema edit, generate, rebuild (2026-10-09)

Measured on a machine at load 10-30 unless said otherwise; the parts are
serial, so a saving in one is a saving in the loop.

| part | before | after | what changed |
|---|---|---|---|
| compile the schema package (the generator builds a program around it) | 3.6-3.9 s | 1.8 s | velox's schema builders and annotation constructors are `//go:noinline`: 60% of that compile was inlining them into `Fields()` |
| the same, real 454-entity schema | 4.0-4.7 s | 2.2-2.4 s | |
| link and run that program, read the result | 1.3-1.4 s | 1.3-1.4 s | |
| generate the files | 3.9-4.0 s | 0.9-1.2 s | each file formatted on its own, on every CPU; formatting was two thirds of it |
| `entity`, then `schemainit`, then the root package | 5.2-5.8 + 1.8 + 1.5 s | max(5.2-5.8, 1.8) + 1.5 s | `schemainit` no longer imports `entity` (`orm.Slot`) and compiles beside it |
| the 312 services | about 10 s (108 CPU-s on 14 cores) | the same | |

About 28 s for one edited field before, about 21 s after, on a machine that
is otherwise quiet (the sum of the parts; the loop as a whole could not be
measured the same day without other work on the machine doubling every time).

### One file per entity

`entity`, the root package and `schemainit` were one file each: 8.3, 1.2 and
1.8 MB here, 10.1 MB and 285,000 lines for `entity` on the real schema. Now
a file per entity, the largest 44 KB here and 70 KB (1,700 lines) there.

| | one file | a file per entity |
|---|---|---|
| compile `entity`: wall, CPU, memory | 9.9-13.4 s, 19.6-20.9 s, 1.4-1.5 GB (load 50-70) | 10.3-10.9 s, 20.3-21.3 s, 1.4-1.7 GB |
| `gopls symbols` of the file an entity is in | 3.3-4.8 s, 1.3 GB, 43,501 symbols | 1.4-2.0 s, 0.3-0.4 GB, 78 symbols |
| `gopls semtok` of that file | no tokens: the file is too large | 532 tokens |

### What a service's compile is made of, and what did not help

One probe package per step, front end of the compiler and the export data it
writes (which the packages importing it read back):

| the package | front end | export data |
|---|---|---|
| uses `context` only | 7 ms | 12 KB |
| holds a `*velox.DB` (then `*orm.DB`) | 12 ms | 82 KB |
| builds one predicate | 27 ms | 92 KB |
| runs one query, returns a count | 65 ms | 118 KB |
| runs one query, returns the entities | 97 ms | 773 KB |
| a whole mock service, 55 functions | 133 ms | 829 KB |

Tried against the 312 services, each in alternating rounds:

| change | services, CPU-s | |
|---|---|---|
| as generated | 103-109 | |
| entity structs that do not reference each other (untyped `Edges`) | 95-98 | not offered: typed edges are a requirement, and this is what they cost |
| entity structs without their enum types, so they name no entity package | 110-112 against 115-119 | 4%, not worth an enum type that reads `predicate.UserRole` |
| services compiled with inlining off (`-gcflags=-l`) | 101-103 against 117-119 | the upper bound of what `//go:noinline` anywhere could give: 13% |
| the root package's methods `//go:noinline` | 109-119 against 116-121 | within the noise, and it would put every builder on the heap |
| more back-end threads for `entity` (`-c=8`, `-c=14`) | no change | the go command of 1.27 already gives a package that compiles alone all of them |

## `u.QueryPosts(tx)` again: what made it expensive (2026-10-09, load 45-100)

Asked to keep the method on the entity, the cost of reaching every query
builder from every entity was taken apart by generating the method again and
removing parts of the query type. The 312 services, CPU-s:

| the entity has `QueryPosts(tx)`, and the query type... | services |
|---|---|
| is complete | 220 / 256 / 228 |
| has no `Iter` (`iter.Seq2[*User, error]`) | 233 / 216 |
| has no `Paginate` (`*orm.Page[*User]`) | 136 / 139 |
| has neither | 167 / 142 / 143 |
| is complete, and `orm.Page` has no methods | 145 / 145 |
| the same, with 5 of its 17 chain and terminal methods | 132 / 132 |
| no method on the entity (the default) | 127 / 115 / 115, 112 / 113 |

Two thirds of the cost was one type. `orm.Page[T]` had two methods, and a
generic type's methods are compiled by every package that can reach one of
its instantiations, called or not: a service that could reach 450 query
types compiled `Page[*RecN].StartCursor` and `EndCursor` 450 times.
`iter.Seq2` has no methods and costs nothing. `StartCursor` and `EndCursor`
are now fields; `internal/ormgen/reach_test.go` pins which generic types a generated
signature may name, `orm` that `Page` has no methods.

What is left is the size of 450 query types, in proportion to their methods:
with the final generator, 110 / 111 / 114 CPU-s by default and 150 / 148 /
154 with `-entity-queries` (export data of a service 0.80 and 1.83 MB, largest
compile 121-129 and 166-176 MB). The method is an option, off by default.

## One spelling: `u.QueryPosts(tx)` (2026-10-09, load 70-85)

Decided: an edge is queried from the row and only there; the client method
`db.User.QueryPosts(tx, u)`, the `-entity-queries` option and the `-setters`
option are gone. `WithPosts` takes `*PostQuery` again: the interface that kept
the builders out of reach has nothing left to protect.

| | 312 services, CPU-s | a service's export data |
|---|---|---|
| no builder in reach of an entity (the design given up) | 106 / 116, 110 / 111 / 114 | 0.80 MB |
| `u.QueryPosts(tx)`, as generated now | 141 / 147 / 146 | 1.77 MB |

Tried to get the spelling for less: the entity's method returning a small
interface per entity that embeds one generic interface with the seventeen
query methods, so that nothing per entity but its `With<Edge>` methods is
declared. 653 CPU-s against 106-116 and 140-151 in the same rounds, and 366 MB
for the largest compile: a package pays for every instantiation of a generic
type with methods that it can reach, interface or not, far more than for the
same methods declared plainly. What remains is in proportion to the number of
methods on a query type (17 to 5 of them: 145 to 132).

(The -setters and -handle measurements above describe options that no longer
exist; the numbers stand as the reason.)

## The final design, standard benchmark (2026-10-09, load 45-55)

`benchmarks/compile-bench.sh orm cold app`, two runs: one spelling for each
thing (`user.Name.Set(v)`, `u.QueryPosts(tx)`), one file per entity.

| | CPU-s | largest compile | median service |
|---|---|---|---|
| the generated packages, cold | 99.9 / 88.0 | 1,333 / 1,517 MB (`entity`) | |
| the 312 services | 138.6 / 138.2 | 168 / 171 MB | 0.43 / 0.42 s, 104 / 121 MB |

`gopls check` of one service, cold, generated code in the workspace: 3.0 GB
(two runs).

For comparison, on the same machine in the same state earlier that day: with
no builder in reach of an entity, cold 98-102 and services 108; before that
work, cold 75-91 and services 172-211. On a quiet machine the same builds take
about a third less CPU.

## The GraphQL layer (2026-10-09, load 30-60)

orm now writes velox's GraphQL schema and the Go an engine binds it to
(`velox-gen -graphql`). `REALISTIC_LIST=1` gives each mock service a list endpoint
per entity it uses: Relay's four arguments, an order input, a where input, a
connection back.

| `compile-bench.sh orm cold app`, two runs | cold | the 312 services | median service |
|---|---|---|---|
| without the GraphQL layer (above) | 99.9 / 88.0 CPU-s | 138.6 / 138.2 | 0.42 s, 104-121 MB |
| with it as first written, and the list endpoints | 103.7 / 116.4 | 217.2 / 214.3 | 0.70 s, 131-139 MB |
| with it as generated now (the two changes below), and the list endpoints | 91.2 / 93.0 | 150.2 / 158.2 | 0.48-0.50 s, 124-125 MB |
| without, in the same session as the last row | 83.5 | 134.7 | 0.42 s, 108 MB |

What is left over the plain tree is the list endpoints themselves: more
code per service, and the where inputs a service names (in this fixture one
where input leads to most others through its `has...With` fields; the real
schema offers those on 14 entities).

### What the layer costs a package that never uses it

Measured on the real 454-entity schema this fixture models (its where inputs
have 66,495 fields; the fixture's have 21,074, so the table above understates
them): a package that does nothing but return one entity, CPU time of its
compile (`go build -toolexec`, the least of seven), and its export data.

| | CPU | export data |
|---|---|---|
| no GraphQL layer | 0.22 s | 1,547 KB |
| the layer as first written | 0.38 s | 3,336 KB |
| the order-field constants in the entities' own packages | 0.31 s | 3,335 KB |
| and the edge methods taking the where input as an interface (as generated now) | 0.23 s | 1,741 KB |

Two separate costs, about 0.07 s each, found with a profile of the compiler
(`-gcflags=<pkg>=-cpuprofile=`, twelve runs merged, `pprof -diff_base`):

- **Exported names of an imported package.** For each package a file imports,
  the compiler walks every object the package exports (`noder.readPackage`,
  `pkgbits.PeekObj`), used or not. The fields a list can be ordered by were
  12,079 constants in package `entity`, which nearly every package imports.
  They are in the entity's own package now (`user.OrderFieldCreatedAt`),
  where only the code that works with that entity pays for them.
- **Types in the signatures of an entity's methods.** Reading a type reads
  its methods, and their parameters' types in full (`noder.(*reader).param`).
  `(*User).Posts(ctx, ..., where *filter.PostWhereInput)` puts every where
  input in reach of every entity. Making them smaller does not help: without
  struct tags (-15% export data) and with the fields entities have in common
  declared once and embedded (-28%) the CPU time was the same within 0.01 s.
  Taking the parameter as `interface{ P() predicate.Post }` does, and that
  is what is generated. An engine that matches a method's parameter types
  exactly then no longer binds the method: gqlc learned to accept an
  interface the argument's type implements (graphql-go, branch
  `gqlc-interface-params`).

An earlier version of this section compared the front end's wall time
(`-gcflags=-bench`, best of three) under a load that changed between runs:
165 / 372 / 180 ms for none / on the entities / off the entities. The order
was right and the sizes were not; off the entities is 0.30 s here, not the
same as no layer. Compare CPU time, and repeat the baseline in every round.

## The generated tree of a real schema, by kind of file (2026-10-13, load 15-45)

The mock schema of this fixture is 450 entities of one shape. A real one of
455 (33 columns and 3 edges on average, 12 column types per entity, 9,921
defaults and validators, mixins, hooks, policies) puts the weight elsewhere,
and what follows was measured on it: the tree as `velox-gen -graphql` wrote
it, each package compiled alone with `go tool compile` against the import
configuration of a full build (`go list -export -deps`), CPU = user + sys.

| | files | MB | lines | compile, CPU-s | memory | object |
|---|---|---|---|---|---|---|
| the entities' packages: `columns.go` | 455 | 16.4 | 459,560 | 70 for the 455 packages | | |
| the entities' packages: the rest | 899 | 5.8 | 138,642 | (with the line above) | | |
| `entity` | 456 | 11.7 | 328,417 | 24.0 | 1.8 GB | 131 MB |
| `entity`, the GraphQL layer | 445 | 1.7 | 45,470 | (in `entity`) | | |
| `filter` | 445 | 13.1 | 164,659 | 3.9 | 0.6 GB | 31 MB |
| `schemainit` | 456 | 2.2 | 87,728 | 11.8 | 1.0 GB | 39 MB |
| `migrate` | 2 | 2.6 | 89,110 | 3.7 | 0.4 GB | 12 MB |
| the root package | 457 | 1.3 | 35,396 | 4.1 | 0.5 GB | 31 MB |
| `predicate` | 1 | 0.2 | 5,012 | 0.1 | | |
| all | 3,618 | 55.0 | 1,354,545 | | | |

Three things in it were found only at this size.

### `schemainit`: a closure per default and per validator

The package was 9,921 generated closures (`f.Default = func() any { return
def }`, a loop over the validators of each field), and it recompiles on every
edit to the schema. The closures are in package `orm` now (`orm.DefaultValue`,
`DefaultFunc`, `Validators`), called once per field:

| | lines | CPU-s | memory | object |
|---|---|---|---|---|
| a closure written out per field | 87,728 | 11.6-12.9 (14.0-16.1 at load 35-45) | 0.86-0.98 GB | 38.9 MB |
| calls to helpers the compiler may inline | 45,495 | 16.3-17.3 | 1.22-1.24 GB | 54.5 MB |
| the same helpers, `//go:noinline` | 45,495 | 6.8-7.9 (at load 35-45) | 0.53-0.62 GB | 18.1 MB |

The middle row is why the helpers carry `//go:noinline`: inlined, every call
is the closure written out again, by the compiler, and the package was dearer
than the one it replaced.

### Starting a program: every entity stitched before `main`

`GODEBUG=inittrace=1` on a program that imports the generated tree and exits:

| | `schemainit` | all package initialization | heap in use at `main` |
|---|---|---|---|
| every entity stitched at initialization | 160 ms, 550,270 allocations, 52.4 MB | 194 ms, 559,869 allocations | 9.9 MB |
| each entity stitched when first used | 0.03 ms, none | 33-42 ms, 9,607 allocations | 5.2-5.6 MB |

Stitching an entity calls its schema's `Fields`, `Mixin`, `Hooks` and
`Policy`, which build every field of the entity and of each of its mixins to
get at the few that have a default or a validator. For all 455 entities that
was what every start of the program paid, a test binary's too (the
application has 353 test packages), and a command that touches three tables.
`orm.StitchAll` is the old behavior for a program that wants it.

### Column types: an interface over one generic type, tried and not adopted

`columns.go` is a third of the tree: 5,586 column types and 73,019 methods,
the same few methods for every Go type a column has, per entity. Tried
instead: no generated column type at all, each column a variable of a
generic interface type of package `orm` (`orm.OrdCol[int64,
predicate.User]`), all implemented by one generic type there.

| | the tree | the 455 entity packages | 253 packages of the application's services |
|---|---|---|---|
| generated column types (as generated) | 1,354,545 lines, 55.0 MB | 69.6 / 70.9 CPU-s | 134.8 / 141.9 CPU-s |
| interface-typed columns over one generic type | 956,076 lines, 41.1 MB | 199.5 / 200.9 | 154.2 / 158.1 |

A third less source, and nearly three times the compile of the packages that
declare the columns: each instantiates the generic type's methods for every
Go type its columns have (one entity's package: 0.14 s and 0.8 MB of object
generated, 0.35 s and 2.9 MB generic), and the packages that use the columns
are 13% dearer too, though they only call through an interface. It also
makes every column's methods reachable through an interface, which the
linker then keeps. This is the earlier table's "method syntax is what is
paid for", on a real schema: a method that returns an entity's own predicate
type is compiled once per entity whoever writes it, and the compiler writes
it at a higher price than the generator.

### Column types of the library, with ordinary methods: measured, not adopted

The generator this one replaces writes one line per column,
`var NameField = sql.StringField[predicate.User](FieldName)`, over generic
types of the library, and that reads better than a `columns.go` per entity.
The same here: thirteen generic types in package `orm`, one per set of
methods a column has (`orm.StringColumn[string, predicate.User]`, pointer
receivers, a string underneath), and a generated type only for a column
whose set none of them has. Not the interface design of the section above:
the variables are of the concrete generic types, and an application that
passes `&user.Name` to a function generic over entities compiles unchanged.

| | the tree | the 455 entity packages | 253 packages of the application's services |
|---|---|---|---|
| generated column types (as generated) | 1,316,306 lines | 77.9 / 80.0 CPU-s | 164.9 / 165.2; later 153.6 / 148.4 |
| column types of the library | 858,949 lines | 107.8 / 114.1 / 115.1 | 198.6 / 214.6 |
| the same, their methods `//go:noinline` | 858,949 lines | 102.9 / 108.2 | 178.4 / 183.5; later 183.9 |

("Later" is one run of each, alternating, an hour on: compare within a
column of runs, not across.) A third less source, and it costs both sides:
the packages that declare columns 35-45% more, the packages that use them
about 20% more with `//go:noinline` and 20-30% without. Why the users pay:
a package emits the whole method set of every instantiated column type it
touches, called or not. One service package that touches a few dozen
(entity, Go type) pairs: 1.40 CPU-s and 6.2 MB of object with generated
types, 1.92 CPU-s and 9.2 MB with the library's, of which 936 functions are
column methods of `orm`.

So the look of the old generator, with ordinary methods, costs about a fifth
more compile time for an application's own packages on a schema this size.
What was adopted is the next section: the same types with methods that are
compiled where they are called.

### One file for all the tables

`migrate/schema.go` was the one file that was still the whole schema: 2.6 MB
and 89,000 lines. It is a file per entity now, like `entity`, the root
package and `schemainit`, and `schema.go` keeps what is no entity's (the join
tables, the list of tables, the foreign keys' wiring): 64 KB.

## Column types whose methods are generic methods (2026-10-13, load 3-7)

Measured on this fixture, each variant generated and built, then every
package of a kind compiled alone with `go tool compile` against the import
configuration of that build (CPU = user + sys; the 312 services were
generated again for the current API first).

A method of a generic type is compiled, with every other method of the type,
in each package that touches a variable of the type: that is what the two
sections above measured. A method that has a type parameter of its own is
compiled where it is called, like a generic function. Go 1.27 has such
methods, and the type parameter can be one a call never writes, because it
is inferred from the receiver's:

```go
type Lazy[T any] interface{ *T }

func (c *StringColumn[T, E]) EQ[_ Lazy[T]](v T) E { ... }

post.Title.EQ("a") // no type argument
```

| | generated lines | the 450 entity packages | the 312 services |
|---|---|---|---|
| generated column and edge types (before) | 1,155,519 | 50.7 / 50.7 / 52.0 CPU-s | 92.2 / 92.3 / 92.5 / 94.5 CPU-s |
| column types of `orm`, generic methods; edges still generated | 613,537 | 24.6 / 25.4 | 95.3 / 95.8 |
| the same, the methods `//go:noinline` | 613,537 | 25.5 | 96.9 |
| column and edge types of `orm`, generic methods (as generated now) | 564,135 | 25.4 | 95.7 |

Half the generated code, half the compile of the entities' packages (what
is left of it is what a package costs to compile at all, 0.056 s), and 1 to
4% more for the packages that use the columns: a service emits the methods
it calls and no others (16 functions in one that the ordinary-method variant
gave several hundred). `entity`, the root package and `schemainit` do not
change (13.0, 2.3 and 3.3 CPU-s).

At run time a predicate is built with the same allocations and about 1.5 ns
more (benchmark of both forms side by side, three runs):

| | generated type | `orm` type, generic method | allocations |
|---|---|---|---|
| `name.EQ(v)` | 9.5-9.8 ns | 11.2-11.3 ns | 1, 16 B |
| `age.GT(v)` | 3.6 ns | 5.0-5.1 ns | 0 |
| `name.In(a, b, c)` | 43.8-44.5 ns | 45.6-45.7 ns | 5, 120 B |
| `name.Set(v)` | 22.2-22.7 ns | 23.0-23.2 ns | 2, 96 B |
| three conditions into a slice | 85.9-86.6 ns | 87.6-88.2 ns | 6, 392 B |

What it costs: Go 1.27, and a method with a type parameter does not satisfy
an interface, so code written once for several entities takes a column as a
concrete column type (`*orm.OrderedColumn[T, E]`, through the column's
`AsOrdered`) where it took an interface.

## Builders whose methods are generic methods (2026-10-13, load 3-16)

The same measure, for the builders. Generated, every entity had its own
query, create, bulk create, update and delete types in package `entity`, some
forty methods of three to ten lines each, and its own client type in the
root package: 326,433 of the 564,135 lines, in the two packages that every
package of an application reads. They are types of package `orm` now
(`orm/builders.go`), every method with the `Lazy` type parameter, and a
generated tree names them:

```go
// package entity
type PostQuery = orm.QueryOf[*Post, predicate.Post, int]

// the root package
var Post = orm.ClientOf[*entity.Post, predicate.Post, int]{Meta: &entity.PostMeta}
```

The first type argument is the pointer to the entity's struct, so the
builders of every entity are one shape to the compiler, and a package
compiles each method it calls once per id type, not once per entity.

| | generated lines | `entity` | root package | the 312 services | the 450 entity packages |
|---|---|---|---|---|---|
| generated builders (before) | 564,135 | 12.3 / 12.9 CPU-s, 1.5-1.6 GB, 112 MB object | 2.2 / 2.3 CPU-s, 0.44 GB | 95.2 / 95.3 / 98.0 CPU-s | 26.4 / 27.0 CPU-s |
| builders of `orm`, generic methods that can be inlined | 345,945 | 3.6 CPU-s, 0.6 GB, 32 MB | 0.3 CPU-s, 0.08 GB | 87.6 / 88.7 | 26.4 |
| the same, the methods `//go:noinline` | 345,259 | 3.4 / 3.7 CPU-s, 0.6 GB, 32 MB | 0.2 CPU-s, 0.08 GB | 76.8 / 77.7 / 78.7 / 78.9 | 26.5 |
| the same, the five constructors of the client inlined | 345,259 | 3.5 / 3.6 CPU-s, 0.6 GB | 0.4 CPU-s | 76.8 / 78.1 / 78.5 / 78.9 | |
| the same, nothing of `entity` marked `//go:noinline` (as generated now) | 337,945 | 3.5 CPU-s, 0.6 GB, 32 MB | | 78.4 / 79.8 | |

`entity` is what a change to any entity recompiles: a quarter of the CPU and
two fifths of the memory it took. The services compile a fifth faster,
though they now compile the builder methods themselves: reading the export
data of a package with 18,000 functions cost each of them more than
compiling the dozen methods it calls (one service of 2,198 lines: 105 ms to
74 ms, the front end from 84 to 48, the back end from 22 to 27). `schemainit`,
`migrate` and `predicate` do not change (3.1-3.3, 1.4 and 0.1 CPU-s).

Inlined, a builder method is compiled again at every call: the third row
against the second. The five constructors of the client (`Query`, `Create`,
`CreateBulk`, `Update`, `Delete`) are the exception, a composite literal
each: not inlined, the builder they return is allocated for every query,
where the compiler otherwise keeps it on the stack of the function that
builds and runs it.

The functions of package `entity` were all marked `//go:noinline` while the
builders were among them (17% off the services then, see above). For what
is left of the package, the accessors of the edges and `Query<Edge>`, it
makes no difference (the last two rows), so they can be inlined again.

At run time nothing changes (`tests/orm/bench_test.go`, SQLite in memory,
2,000 iterations, two or three runs of each binary in turn):

| | generated builders | builders of `orm` | allocations |
|---|---|---|---|
| point lookup | 10.8-12.1 µs | 11.1-11.8 µs | 81, 4.5 KB |
| read 500 rows | 611-653 µs | 616-651 µs | 10,304, 336 KB |
| create | 26.5-28.8 µs | 25.7-28.7 µs | 69, 4.9 KB |
| update, `Exec` | 10.4-10.7 µs | 10.1-10.8 µs | 59, 4.7 KB |
| update, `SaveOne` | 27.5-28.3 µs | 26.9-28.3 µs | 120, 7.9 KB |

What it costs: eager loading is one method that takes the edges,
`With(post.Author, post.Comments.Load(q))`, where it was a method per edge
(`WithAuthor()`, `WithComments(q)`): a type of the library cannot have a
method named after an edge of the schema. A builder's name in a compiler
error is the library's (`*orm.QueryOf[*entity.Post, predicate.Post, int]`).
gqlgen 0.17.86 binds the connection types as before
(`entity.PostConnection` is `orm.Connection[*entity.Post]`).
