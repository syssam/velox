# velox2 Schema DSL and code generation — design

Status: implemented (schema plans 1–4) · designed 2026-10-15 · Go 1.27+

This design replaced Core §1 (entity model) and §2 (generated code) of `2026-10-14-core-design.md`. Entities were plain structs with a `Schema(*velox.Schema)` method. They are now declared in an ent-style schema DSL, following the old velox's schema API. velox-gen generates the entity structs from it. The struct-first path is gone from the code: the DSL is the only way to declare an entity.

The runtime is unchanged:
- the generic query and mutation API;
- the pipeline, guards and deadlines (Core §4–§8, policy design);
- observability and routing.

GraphQL and gRPC generation read the same model. Each gets its own design.

## Decisions

Made by the author (2026-10-15):

- **The schema is the single source of truth.** You write it in an ent-style DSL: `type User struct{ schema.Schema }` with `Fields`, `Edges`, `Indexes`, `Mixin` and `Annotations`. velox-gen generates the Go entities, their handles and their metadata. One model also drives the database DDL (migration planner), GraphQL and gRPC.
- **One schema package and one generated package,** as in ent and the old velox. Edges work in both directions.
- **Generated code is velox2's own, built on generics.**
  - It holds structs, typed handles and metadata, not per-entity builders.
  - Queries stay `db.Query[gen.User]()`.
- **Policies and hooks are methods you write beside the generated code.** The schema holds only declarative structure. The reasons:
  - Policies stay fully typed with the generated handles.
  - Nothing runs at program start.
  - There is no import cycle.
  - A policy cannot be left unbound.

## Success criteria

1. A schema using the old velox's (and ent's) common DSL generates working velox2 entities after an import-path change. Policy and hook bodies move to methods.
2. Nothing runs at program start. Metadata is built on an entity's first use, from generated literals; no struct is reflected over to discover columns.
3. Compile cost follows Core §3: generated code per entity is small (structs, handles, a metadata table), and velox's generic code is compiled only where it is used.
4. velox-gen's output is deterministic, gofmt-clean, and checked in CI (`-check`, `verify`).
5. Every runtime feature works unchanged on generated entities, with the same tests on all three dialects: tenant, policy, hooks, guards, observability and routing.

## 1. The schema

```go
package schema

import (
    "github.com/syssam/velox2/schema"         // schema.Schema, Field, Edge, Index, Mixin, Annotation
    "github.com/syssam/velox2/schema/field"
    "github.com/syssam/velox2/schema/edge"
    "github.com/syssam/velox2/schema/index"
    "github.com/syssam/velox2/schema/mixin"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
    return []schema.Field{
        field.Int64("org_id").Tenant(),
        field.String("name").NotEmpty().MaxLen(100),
        field.String("email").Unique(),
        field.Enum("role").Values("viewer", "admin").Default("viewer"),
        field.Time("deleted_at").Optional().Nillable(),
    }
}
func (User) Edges() []schema.Edge {
    return []schema.Edge{
        edge.To("posts", Post.Type),
        edge.From("org", Org.Type).Ref("users").Field("org_id").Unique().Required(),
    }
}
func (User) Indexes() []schema.Index      { return []schema.Index{index.Fields("org_id", "name").Unique()} }
func (User) Mixin() []schema.Mixin        { return []schema.Mixin{mixin.Time{}} }
func (User) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("users")} }
```

### 1.1 Fields

The field types follow the old velox (`field` package):
- `Bool`, `Int`, `Int8`–`Int64`, `Uint`–`Uint64`, `Float32`, `Float64` *(note 2026-10-21: `Float64` is gone; `Float` declares a float64 field, as in ent. Typed constructors were added: `Number[T]`, `EnumOf[T]` and `UUIDOf[T]`, equal to their ent spellings `Int64(..).GoType(T(0))` and the like. Builder types are exported, `field.StringBuilder` and the rest. `SchemaType` keys are `field.Postgres`, `field.MySQL` and `field.SQLite`, equal to the core's dialect names, and an unknown key is an error. See the API cleanup spec §3.6 and §6b.)*;
- `String`, `Text`, `Bytes`, `Time`, `UUID`, `JSON`, `Enum`;
- `Other` for a custom Go type, with explicit SQL types.

Options, which carry their ent meanings:

| Option | Effect |
|---|---|
| `Optional()` | not required on create, and the column is NULL-able *(note 2026-10-20: no longer so. `Optional` leaves the column NOT NULL and the field a value type; `Nillable` makes the column NULL-able; see the API cleanup spec §3.1)* |
| `Nillable()` | the Go field is a pointer *(note 2026-10-20: also NULL-able column, per the API cleanup spec §3.1)* |
| `Default(v)`, `DefaultFunc(f)` | the value of a column an insert does not set |
| `UpdateDefault(f)` | the value an update sets when it does not set the column; **update only**, as in ent. `mixin.Time`'s `updated_at` has both a `Default` and an `UpdateDefault`, so an insert fills it too |
| `Immutable()` | refused in `Update`, `Save` and upsert update lists |
| `Unique()` | a single-column unique index |
| `StorageKey(name)` | the column name; the default is the field name |
| `SchemaType(map[dialect]string)` | a per-dialect SQL type |
| `Comment(s)` | a column comment, where the dialect has them |
| `Sensitive()` | left out of `String()`, JSON, logs and spans |

**velox2 additions:**
- `Tenant()` marks the tenant column (policy design §2.1). It must be required, not `Nillable`, and of a comparable type, and it may have no `Default` or `UpdateDefault`.
- `GoType(T)` uses a named Go type for the field.

**JSON fields (note of 2026-10-20).** A `JSON` field is stored as JSON text, in `jsonb` on PostgreSQL, `JSON` on MySQL and `TEXT` on SQLite. The plan is `docs/plans/2026-10-20-core-json-fields.md`; its rulings are:
- **One codec, supplied by the generator.** velox-gen writes `Encode: json.Marshal` and `Decode: json.Unmarshal` into the `ColumnDesc` of every `json` field (the generated file imports encoding/json). The core never imports encoding/json, so a program whose entities hold no JSON does not link it. `ColumnInfo.Codec` is true for a column with a codec; it says nothing of the database type, which the migration planner takes from the field type. A hand-written `Desc` may plug in another codec, and a `Desc` with only one of the two is refused. The core never guesses JSON from a Go type, because a `[]byte` field is bytes.
- **Encoding is a binding concern.** The value is encoded where the arguments for a column are built. It is bound as `[]byte` on PostgreSQL, whose drivers take it for `jsonb`. On SQLite and MySQL it is bound as a string when the bytes are valid UTF-8, as JSON always is: SQLite would store a `[]byte` as a BLOB, which its JSON functions read as JSONB, so the column holds `TEXT`; and MySQL's driver with `interpolateParams=true` writes a `[]byte` as a binary string, which a `JSON` column refuses (error 3144). The bytes of a binary codec, which are not valid UTF-8, stay `[]byte`. Hooks and policies see Go values, and only `Statement.Args` holds the bytes. A nil pointer, or a nil slice or map in a nullable field, binds SQL NULL; a nil slice or map in a field that is not a pointer binds the JSON `null` (as ent does) and reads back as nil. Reading decodes `[]byte` or `string`; SQL NULL leaves a pointer nil and any other field at its zero value; a value that does not decode fails with an error naming the entity and the column.
- **Conditions and ordering are refused.** velox-gen gives a `json` field the handle `velox.JSONCol[E, V]` (`velox.NullJSONCol[E, V]` when nullable), which has only `Set` (and `SetNull`, `IsNull` and `NotNull`), so generated code cannot compare a JSON column or order by it. Behind that, for hand-written `Desc`s, `EQ`, `NEQ`, `In`, `NotIn`, the ordering and text predicates on an encoded column, and `Asc` or `Desc` by one, fail when the statement runs, naming the column, because equality and order differ by dialect (jsonb normalises JSON, TEXT does not). So does keyset paging ordered by one, and `Add` or `Sub` in an update. `IsNull` and `NotNull` work (SQL NULL only, not the JSON `null`). A primary-key, tenant or relation-key column cannot have a codec; the metadata is refused. JSON path predicates are a follow-up.

**Validators** compile into the generated code and run before every insert and update that assigns the field:
- `NotEmpty`, `MinLen`, `MaxLen`, `Match(re)`;
- `Range`, `Min`, `Max`, `Positive`, `NonNegative`;
- `Validate(func(V) error)`.

A failure is a `*ValidationError{Entity, Field, Err}`.

**IDs:** an `id` field is added unless one is declared. It is `int64` by default; `field.UUID("id")`, `field.String("id")` and others are allowed.

### 1.2 Edges

`edge.To(name, T.Type)` and `edge.From(name, T.Type).Ref(inverse)` follow ent:
- `Unique()` makes an edge one-to-one or many-to-one.
- `Required()` and `Field(fk)` bind an edge to a declared foreign-key field.
- `StorageKey(...)` names the foreign-key column or join table.
- Many-to-many edges use a join table.
- `OnDelete(edge.Cascade|SetNull|Restrict)` on either edge of a one-to-many or one-to-one relation sets what the database does to the rows holding the foreign key *(note 2026-10-21, API plan 2)*. `SetNull` needs a NULL-able key, both edges of a pair must agree, and an M2M edge refuses it. It reaches `describe` and the migration planner, not the plugin model.

The generated struct holds the edges as `velox.Many[T]` and `velox.One[T]`, with the semantics of Core §1: never loaded lazily, and the zero value means not loaded. `With`, `Has` and `HasWhere` use generated relation handles, `gen.Users.Posts`, a `velox.Rel[User, Post]`. How a relation names its target is in §2.

### 1.3 Indexes, mixins, annotations

- **Indexes:** `index.Fields(...)` with `Unique()`, `StorageKey(...)` and, on PostgreSQL, `Where(sql)` *(note 2026-10-21: SQLite has partial indexes too and MySQL does not. `Where` takes plain SQL and has no dialect keys; the dialect keys `field.Postgres`, `field.MySQL` and `field.SQLite` belong to a field's `SchemaType`. Two indexes with the same columns, `Unique` and `Where` are refused, and index names, explicit or derived, must be unique within one database schema (`schema.TableIn`), as PostgreSQL requires)*.
- **Mixins:** these contribute fields, edges, indexes and annotations. `mixin.Time` adds `created_at` and `updated_at`. A mixin carries no behaviour.
- **Annotations:**
  - `schema.Table`, and `schema.TableIn(schema, table)`;
  - GraphQL and gRPC options from `contrib/graphql` and `contrib/grpc`;
  - an annotation velox core does not know is passed to the generators that do.

## 2. Generated code

`velox-gen` writes one Go package, by default `./gen` with package `gen`. Both the directory and the package name are configurable. The package holds one file per entity, `<entity>_velox.go`, plus `velox.go` for the shared parts.

For each entity it writes:
- **The struct.** Every field has its Go type: a pointer for `Nillable`, a named enum type for `Enum`. Edge fields are `velox.Many[T]` / `velox.One[T]`. JSON tags are on every field, and `Sensitive` fields are omitted from JSON.
- **The typed enums**, with constants and validation.
- **The handles variable,** for example `gen.Users.Name` and `gen.Users.Posts`. These are the column and relation handles of today (`Col`, `OrderedCol`, `NumberCol`, `StringCol` and their `Null…` forms, `JSONCol` and `NullJSONCol` for `json` fields since 2026-10-20, and `Rel`).
- **The metadata.** A `velox.Desc` literal of columns, keys, indexes, tenant, relations, validators and defaults, turned into the runtime's `EntityInfo` on first use. That replaced `Schema(*velox.Schema)` and the reflection over struct fields.
  - **A relation names no target.** The `Desc` records the relation's key and the types of its holder (`*One[T]` or `*Many[T]`), and nothing that would link the target's metadata. The handle's use site supplies the target: `Where`, `Has`, `HasWhere` and `With` on a `Rel[User, Post]` take `Post` from the handle's type parameter, and `velox.CheckRelation(gen.Users.Posts)` resolves one relation. Every resolution checks the handle's `T` against the field's `One[T]` or `Many[T]` (two comparisons, before the cached link is read), so a stale or hand-edited handle fails each time it is used, whichever handle resolved first, with a `*SchemaError` naming the relation and both types, not at compile time.
  - **Why.** With the target in the `Desc`, linking one entity linked every entity reachable through relations, and inverse edges make a whole schema one connected component. A service that queries five entities of 500 was 23.6 MB. Without it the binary is 5.66 MB at any N (core spec, A.6, note of 2026-10-19). `EdgeThrough` keeps its join entity, which has no relations of its own, so that is one hop.
  - **What it costs.** An invalid relation target is reported where the relation is used, not at the owner's first use. `velox-gen verify` calls `CheckEntity` for every entity, which checks the entity only, and `CheckRelation` for every relation, so CI finds it. The linker still reads the whole `gen` package, so its memory grows with N (185 MB at N=100, 279 MB at N=500), though the binary does not.
- **The stale check.** `GeneratedCodeIsVersion4`, which fails to compile when generated code and the library disagree, and the model's hash in `velox.go`, which `velox-gen -check` compares in CI: a compile-time check across packages is not possible.

The API of the runtime does not change:

```go
u, err := db.Query[gen.User]().Where(gen.Users.Name.EQ("ada")).Limit(10).All(ctx)
err = db.Insert(&gen.User{Name: "Ada", OrgID: 1}).Exec(ctx)
```

## 3. Behaviour beside the generated code

You write policies and hooks as methods in your own files in `gen/`. velox-gen writes only files that start with its header, and never touches others.

```go
// gen/user_policy.go — yours
func (*User) Policy() velox.Policy[User] {
    return velox.Policy[User]{
        // Read is ANDed into every SELECT, UPDATE and DELETE; Write and Insert are the other two.
        Read: func(ctx context.Context) (velox.Pred[User], error) {
            return Users.DeletedAt.IsNull(), nil
        },
    }
}

func (*User) Hooks() []velox.Hook[User] {
    return []velox.Hook[User]{
        velox.On(auditHook[User](), velox.OpInsert, velox.OpUpdate, velox.OpDelete),
        func(next velox.Mutator[User]) velox.Mutator[User] {
            return velox.MutateFunc[User](func(ctx context.Context, m *velox.Mutation[User]) error {
                // before: m.Rows() on insert (mutable); m.Value/Set/Where on update and delete
                err := next.Mutate(ctx, m)
                // after: m.Result().Affected — rows affected; generated IDs are in m.Rows()
                return err
            })
        },
    }
}
```

**Hooks** follow ent's middleware shape, typed on the entity:
- `Hook[E] func(Mutator[E]) Mutator[E]`, where `Mutator[E]` has `Mutate(ctx, *Mutation[E]) error`.
- `MutateFunc[E]` adapts a function to a `Mutator[E]`.
- `velox.On(h, ops...)` runs a hook only for the given operations, the `Op` constants that `Statement.Op` uses: `OpInsert`, `OpUpdate`, `OpDelete`, listed as arguments.
- Hooks run in the order listed, each wrapping the next.
- A generic hook function, such as `auditHook[E any]() velox.Hook[E]`, is reusable across entities: each entity lists it.

`Mutation[E]` covers every operation:
- `Op()`;
- `Rows() []*E` for an insert (all rows of an `Insert` or `InsertAll` statement), changeable before `next`;
- `Value(col)`, `Set(...)` and `Where(...)` for an update or delete, as in the policy design §3;
- `DB()`, the database the write runs on: the transaction's inside `db.Tx`, else the database's. A hook that writes or reads other rows uses `m.DB().Insert(&audit).Exec(ctx)`, so its work commits or rolls back with the write inside a transaction (ent's `m.Client()`). It is set for the writes of entities that have hooks, which are the only ones that see a `Mutation`;
- `Result()` after `next`: its `Affected` is the rows inserted, matched or deleted. A generated key is written back into its row, so after `next` an `Insert`'s `m.Rows()[0].ID` holds it; `InsertAll` reads no keys back.

**Order of a write:**
1. For an update or delete, the guard (`ErrNoWhere` and the like), on the statement as the caller built it: a condition a hook adds never stands in for the caller's `Where`.
2. The hooks' code before `next` runs.
3. Inside `next`, velox runs:
   - for an insert: the defaults, the tenant (fill, then check), the validators, and the policy's `Insert` check;
   - for an update: the `UpdateDefault` assignments (an insert does not apply them), the immutable check, and the validators;
   - then the pipeline: the guard again, the Rewrite hooks, the scope, render, and execute.
4. The hooks' code after `next` runs.

Validators therefore see what hooks set. In a transaction, the code after `next` runs before the commit; hooks after commit are out of scope. A write a hook makes through `m.DB()` is part of the caller's transaction; through any other `*DB` it is not. Under `SQL(ctx)`, `next` does not execute: `Result()` is empty and `velox.IsDryRun(ctx)` is true.

**Detection:** velox-gen binds `Policy()` and `Hooks()` in the generated metadata (`velox.BindPolicy` and `velox.BindHooks`) when the entity declares them; a wrong signature does not compile, and an entity whose method the metadata does not bind is refused at first use. Nothing runs at program start.

Building predicates in the method bodies is fine, `Has` and `HasWhere` included. `HasWhere` reads no metadata: it records the relation and its target, and the statement that holds the condition resolves it (its `prepare`, before the guard runs). A relation that cannot be resolved therefore fails that statement, not the call that built the condition. (Until plan 4, a `Has` in a `Policy()` body deadlocked on the metadata build in progress.)

Hooks for every entity stay the statement-level `WithAround` and `WithRewrite`, which are untyped by nature. **Mixins** carry no behaviour: a reusable hook is a generic function that each entity lists.

This replaced the policy design §3's `BeforeInsert`, `BeforeUpdate` and `BeforeDelete`. Their uses map onto a hook's code before `next`.

## 4. velox-gen

1. **Load:** compile and run a small program that imports the schema package and serialises each schema's fields, edges, indexes, mixins and annotations to a model (JSON, versioned). `verify` uses the same mechanism.
2. **Validate:** check the model:
   - duplicate fields and columns;
   - unknown edge targets, inverse edges that do not match, and foreign keys bound to missing fields;
   - tenant rules;
   - enum values that are not Go identifiers.

   Errors name the schema type and the field.
3. **Generate:** write the `gen` package from the model with `go/format`, deterministically.
4. **Generators:** pass the model to registered generators (`contrib/graphql`, `contrib/grpc`), each writing its own files.

Commands:

```
velox-gen [-out dir] [-package name] [-plugin tool]... [-check] [./schema]
velox-gen verify [-out dir] [-plugin tool]... [./schema]
velox-gen describe [-json] [./schema]
```

- The package is one schema package, `./schema` by default; `./...` and a pattern that names no schemas are errors.
- `-out` is the generated package's directory (default `./gen`), `-package` its name.
- `-check` writes nothing and exits 1 *(note 2026-10-21: 3)* when a generated file is out of date, missing or left over. This is the stale check of §2.
- `-plugin tool` (repeatable) names a generator plugin: a tool of the application's module that `go tool` runs with the resolved model as JSON on stdin and that answers with files for `-out`. velox-gen adds the header to those files and owns them like its own (`-check`, rewrite, stale removal). The protocol and the GraphQL plugin are in `2026-10-19-graphql-design.md`.
- `verify` builds a program that imports the generated package and reports what `velox.CheckEntity` finds for every entity and `velox.CheckRelation` for every relation.
- `describe` prints the resolved model; `-json` prints it as JSON.

*Note 2026-10-21 (API plan 2).* The commands are now:

```
velox-gen [generate] [-out dir] [-package name] [-plugin tool[:key=value,...]]... [-check] [package]
velox-gen verify [-out dir] [-package name] [-plugin tool[:key=value,...]]... [package]
velox-gen describe [-json] [package]
velox-gen version
```

`generate` is the default subcommand, and each has its own flags, which may follow the package. Exit codes: 0 success, 1 error, 2 misuse, 3 stale (`-check`, and `verify` for a plugin's Go file). `describe -json` prints `plugin.Model`, the model a plugin receives. Plugin protocol 2 adds options, warnings, files in directories below `-out` and `.proto` files; see the GraphQL design's note and the `plugin` package doc.

Flags remain all the configuration there is; there is no `velox.yaml`.

## 5. Repository layout

```
velox2/
  *.go             the runtime: DB, Query, writes, Tx, Policy, Hook, Mutation, errors,
                   options, pipeline. One package: generated code imports only this.
  schema/          the DSL: Schema, Field, Edge, Index, Mixin, Annotation
    field/ edge/ index/ mixin/
  dialect/pgx/     OpenPgx; the only package importing pgx
  dialect/sql/     the database/sql executor
  cmd/velox-gen/   the generator command
  internal/gen/    the generator: load, model, validate, write
  contrib/veloxotel/ contrib/graphql/ contrib/grpc/
  example/ docs/ scripts/ bench/
```

**Why the runtime stays in one package.** Its generic types share unexported internals: the statement, predicate nodes and metadata. Splitting it would make those internals exported API, which an open-source 1.0 must then keep stable. Internals that need no exported glue (SQL writing, the render cache, error classification) may move under `internal/` later without changing the public API.

**Moving `OpenPgx` to `dialect/pgx`** means an application that does not use pgx no longer links it.
- The root package keeps `Open(d, *sql.DB)` working.
- `dialect/pgx` and `dialect/sql` construct a DB through an internal executor interface shared under `internal/`.

## 6. What this replaced

- Core §1 and §2: struct-first entities, `Schema(*velox.Schema)`, `Mixin(*velox.Schema)`, `s.Policy`, `s.BeforeInsert` and the rest, and velox-gen's struct scanner. All were removed in plan 4.
- `meta.go`'s reflection-based `buildInfo`, which became a builder from generated metadata.
- `internal/testmodel`, `example/` and `bench/scale`'s generator, which were rewritten in the DSL.
- The policy design §2.2 and §3 API forms (`s.Policy`, `s.BeforeInsert`), which became methods. Their semantics are unchanged.

## 7. Testing

- **Generator golden tests:** schemas in, generated files compared.
- **Errors:** a model-validation error for each rule.
- **Determinism:** the same output twice.
- **The full runtime suite** runs on the rewritten test model, on all three dialects, unchanged in what it asserts.
- **Compile-cost guards** (Core §3) are re-measured with generated entities, and `bench/scale` is re-run.
- **Ports:** a port test of a representative old-velox / ent schema.

## 8. Out of scope

- Schema-level hooks and privacy in the old velox's or ent's form.
- ent's per-entity generated builder API.
- Splitting the generated package by domain (after 1.0, if needed).
- Migrations: the migration planner reads this model; it is sub-project 5.
