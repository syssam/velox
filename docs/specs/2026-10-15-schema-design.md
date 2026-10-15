# velox2 Schema DSL and code generation — design

Status: draft for review · 2026-10-15 · Go 1.27+

This design replaces Core §1 (entity model) and §2 (generated code) of `2026-10-14-core-design.md`. Entities were plain structs with a `Schema(*velox.Schema)` method. They are now declared in an ent-style schema DSL, following the old velox's schema API. velox-gen generates the entity structs from it.

The runtime is unchanged:
- the generic query and mutation API;
- the pipeline, guards and deadlines (Core §4–§8, policy design);
- observability and routing.

GraphQL and gRPC generation read the same model. Each gets its own design.

## Decisions

Made by the author (2026-10-15):

- **The schema is the single source of truth.** You write it in an ent-style DSL: `type User struct{ velox.Schema }` with `Fields`, `Edges`, `Indexes`, `Mixin` and `Annotations`. velox-gen generates the Go entities, their handles and their metadata. One model also drives the database DDL (migration planner), GraphQL and gRPC.
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
    "github.com/syssam/velox2/schema"         // velox.Schema, Field, Edge, Index, Mixin, Annotation
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
- `Bool`, `Int`, `Int8`–`Int64`, `Uint`–`Uint64`, `Float32`, `Float64`;
- `String`, `Text`, `Bytes`, `Time`, `UUID`, `JSON`, `Enum`;
- `Other` for a custom Go type, with explicit SQL types.

Options, which carry their ent meanings:

| Option | Effect |
|---|---|
| `Optional()` | not required on create, and the column is NULL-able |
| `Nillable()` | the Go field is a pointer |
| `Default(v)`, `DefaultFunc(f)`, `UpdateDefault(f)` | values on insert, and on every update |
| `Immutable()` | refused in `Update`, `Save` and upsert update lists |
| `Unique()` | a single-column unique index |
| `StorageKey(name)` | the column name; the default is the field name |
| `SchemaType(map[dialect]string)` | a per-dialect SQL type |
| `Comment(s)` | a column comment, where the dialect has them |
| `Sensitive()` | left out of `String()`, JSON, logs and spans |

**velox2 additions:**
- `Tenant()` marks the tenant column (policy design §2.1). It must be required, not `Nillable`, and of a comparable type, and it may have no `Default` or `UpdateDefault`.
- `GoType(T)` uses a named Go type for the field.

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

The generated struct holds the edges as `velox.Many[T]` and `velox.One[T]`, with the semantics of Core §1: never loaded lazily, and the zero value means not loaded. `With`, `Has` and `HasWhere` use generated relation handles.

### 1.3 Indexes, mixins, annotations

- **Indexes:** `index.Fields(...)` with `Unique()`, `StorageKey(...)` and, on PostgreSQL, `Where(sql)`.
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
- **The handles variable,** for example `gen.Users.Name` and `gen.Users.Posts`. These are the column and relation handles of today (`Col`, `OrderedCol`, `NumberCol`, `StringCol` and their `Null…` forms, and `Rel`).
- **The metadata.** A literal table of columns, keys, indexes, tenant, edges, validators, defaults and annotations, turned into the runtime's `EntityInfo` on first use. That replaces `Schema(*velox.Schema)` and the reflection over struct fields.
- **The stale check.** `GeneratedCodeIsVersion3` and a compile-time check that the generated package matches the schema it was generated from (a hash of the model).

The API of the runtime does not change:

```go
u, err := db.Query[gen.User]().Where(gen.Users.Name.EQ("ada")).Limit(10).All(ctx)
err = db.Insert(&gen.User{Name: "Ada", OrgID: 1}).Exec(ctx)
```

## 3. Behaviour beside the generated code

You write policies and hooks as methods in your own files in `gen/`. velox-gen writes only files that start with its header, and never touches others.

```go
// gen/user_policy.go — yours
func (*User) Policy() velox.Policy[User] { return velox.Policy[User]{Read: …, Write: …, Insert: …} }

func (*User) Hooks() []velox.Hook[User] {
    return []velox.Hook[User]{
        velox.On(audit.Hook[User](), velox.OpInsert, velox.OpUpdate, velox.OpDelete),
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
- Generic hooks are reusable across entities, for example `audit.Hook[E]()`.

`Mutation[E]` covers every operation:
- `Op()`;
- `Rows() []*E` for an insert (all rows of an `Insert` or `InsertAll` statement), changeable before `next`;
- `Value(col)`, `Set(...)` and `Where(...)` for an update or delete, as in the policy design §3;
- `Result()` after `next`: its `Affected` is the rows inserted, matched or deleted. A generated key is written back into its row, so after `next` an `Insert`'s `m.Rows()[0].ID` holds it; `InsertAll` reads no keys back.

**Order of a write:**
1. For an update or delete, the guard (`ErrNoWhere` and the like), on the statement as the caller built it: a condition a hook adds never stands in for the caller's `Where`.
2. The hooks' code before `next` runs.
3. Inside `next`, velox runs:
   - for an insert: the defaults, the tenant (fill, then check), the validators, and the policy's `Insert` check;
   - for an update: the `UpdateDefault` assignments, the immutable check, and the validators;
   - then the pipeline: the guard again, the Rewrite hooks, the scope, render, and execute.
4. The hooks' code after `next` runs.

Validators therefore see what hooks set. In a transaction, the code after `next` runs before the commit; hooks after commit are out of scope. A hook cannot yet reach the caller's transaction: `Mutation` holds no DB and the context carries no transaction, so a write a hook makes itself runs outside it. Plan 3 decides how to expose it, for example `velox.TxOf(ctx)`. Under `SQL(ctx)`, `next` does not execute: `Result()` is empty and `velox.IsDryRun(ctx)` is true.

**Detection:** velox-gen binds `Policy()` and `Hooks()` in the generated metadata when the entity declares them; a wrong signature does not compile, and an entity whose method the metadata does not bind is refused at first use. Nothing runs at program start.

Neither method's body may read the entity's own metadata outside the functions it returns: a `Has` or `HasWhere` there waits on the metadata's build in progress and deadlocks. Such predicates are built inside the returned functions.

Hooks for every entity stay the statement-level `WithAround` and `WithRewrite`, which are untyped by nature. **Mixins** carry no behaviour: a reusable hook is a generic function that each entity lists.

This replaces the policy design §3's `BeforeInsert`, `BeforeUpdate` and `BeforeDelete`. Their uses map onto a hook's code before `next`.

## 4. velox-gen

1. **Load:** compile and run a small program that imports the schema package and serialises each schema's fields, edges, indexes, mixins and annotations to a model (JSON, versioned). This is the mechanism `verify` uses today.
2. **Validate:** check the model:
   - duplicate fields and columns;
   - unknown edge targets, inverse edges that do not match, and foreign keys bound to missing fields;
   - tenant rules;
   - enum values that are not Go identifiers.

   Errors name the schema type and the field.
3. **Generate:** write the `gen` package from the model with `go/format`, deterministically.
4. **Generators:** pass the model to registered generators (`contrib/graphql`, `contrib/grpc`), each writing its own files.

Commands: `go tool velox-gen ./schema` (configured by flags or a `velox.yaml`), `-check`, and `describe -json`, which prints the model.

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
  contrib/otel/ contrib/graphql/ contrib/grpc/
  example/ docs/ scripts/ bench/
```

**Why the runtime stays in one package.** Its generic types share unexported internals: the statement, predicate nodes and metadata. Splitting it would make those internals exported API, which an open-source 1.0 must then keep stable. Internals that need no exported glue (SQL writing, the render cache, error classification) may move under `internal/` later without changing the public API.

**Moving `OpenPgx` to `dialect/pgx`** means an application that does not use pgx no longer links it.
- The root package keeps `Open(d, *sql.DB)` working.
- `dialect/pgx` and `dialect/sql` construct a DB through an internal executor interface shared under `internal/`.

## 6. What this replaces

- Core §1 and §2: struct-first entities, `Schema(*velox.Schema)`, `Mixin(*velox.Schema)`, `s.Policy`, `s.BeforeInsert` and the rest, and velox-gen's struct scanner.
- `meta.go`'s reflection-based `buildInfo`, which becomes a builder from generated metadata.
- `internal/testmodel`, `example/` and `bench/scale`'s generator, which are rewritten in the DSL.
- The policy design §2.2 and §3 API forms (`s.Policy`, `s.BeforeInsert`), which become methods. Their semantics are unchanged.

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
