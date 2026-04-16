# Velox vs Ent: Full Comparison

Comprehensive API, logic, architecture, and feature comparison between Velox and [Ent](https://entgo.io/) (v0.14.6).

## Table of Contents

1. [Schema Definition](#1-schema-definition)
2. [Field API](#2-field-api)
3. [Edge/Relationship API](#3-edgerelationship-api)
4. [Index API](#4-index-api)
5. [Mixin API](#5-mixin-api)
6. [Generated Client API](#6-generated-client-api)
7. [Query Builder API](#7-query-builder-api)
8. [Mutation Builder API](#8-mutation-builder-api)
9. [Transaction API](#9-transaction-api)
10. [Hook & Interceptor API](#10-hook--interceptor-api)
11. [Privacy/Policy API](#11-privacypolicy-api)
12. [GraphQL Integration](#12-graphql-integration)
13. [Schema Migration](#13-schema-migration)
14. [Feature Flags](#14-feature-flags)
15. [Code Generation](#15-code-generation)
16. [Error Handling](#16-error-handling)
17. [SQL Builder](#17-sql-builder)
18. [Database Support](#18-database-support)
19. [Performance](#19-performance)
20. [Velox-Only Features](#20-velox-only-features)
21. [Ent-Only Features](#21-ent-only-features)
22. [Summary Table](#22-summary-table)

---

## 1. Schema Definition

Both use the same embedded-struct pattern with identical method signatures.

```go
// Ent
type User struct { ent.Schema }
func (User) Fields() []ent.Field   { ... }
func (User) Edges() []ent.Edge     { ... }
func (User) Mixin() []ent.Mixin    { ... }
func (User) Indexes() []ent.Index  { ... }
func (User) Policy() ent.Policy    { ... }
func (User) Hooks() []ent.Hook     { ... }
func (User) Interceptors() []ent.Interceptor { ... }
func (User) Annotations() []schema.Annotation { ... }

// Velox (identical pattern)
type User struct { velox.Schema }
func (User) Fields() []velox.Field   { ... }
func (User) Edges() []velox.Edge     { ... }
func (User) Mixin() []velox.Mixin    { ... }
func (User) Indexes() []velox.Index  { ... }
func (User) Policy() velox.Policy    { ... }
func (User) Hooks() []velox.Hook     { ... }
func (User) Interceptors() []velox.Interceptor { ... }
func (User) Annotations() []schema.Annotation { ... }
```

| Aspect | Ent | Velox | Notes |
|--------|-----|-------|-------|
| Base struct | `ent.Schema` | `velox.Schema` | Same pattern |
| View schemas | `ent.View` | `velox.View` | Read-only entities |
| `Config()` | Deprecated | Deprecated | Both say use `Annotations()` |
| `Type()` | Marker method | Marker method | Used for edge declarations |

**Verdict: Identical.** Velox maintains full Ent schema API compatibility.

---

## 2. Field API

### Constructors

| Constructor | Ent | Velox | Notes |
|-------------|-----|-------|-------|
| `field.String(name)` | Yes | Yes | |
| `field.Text(name)` | Yes | Yes | Unbounded string |
| `field.Bool(name)` | Yes | Yes | |
| `field.Int(name)` | Yes | Yes | |
| `field.Int8(name)` | Yes | Yes | |
| `field.Int16(name)` | Yes | Yes | |
| `field.Int32(name)` | Yes | Yes | |
| `field.Int64(name)` | Yes | Yes | |
| `field.Uint(name)` | Yes | Yes | |
| `field.Uint8(name)` | Yes | Yes | |
| `field.Uint16(name)` | Yes | Yes | |
| `field.Uint32(name)` | Yes | Yes | |
| `field.Uint64(name)` | Yes | Yes | |
| `field.Float(name)` | Yes | Yes | float64 |
| `field.Float32(name)` | Yes | Yes | |
| `field.Bytes(name)` | Yes | Yes | |
| `field.Time(name)` | Yes | Yes | |
| `field.JSON(name, typ)` | Yes | Yes | |
| `field.Strings(name)` | Yes | Yes | JSON string slice |
| `field.Ints(name)` | Yes | Yes | JSON int slice |
| `field.Floats(name)` | Yes | Yes | JSON float slice |
| `field.Any(name)` | Yes | Yes | Dynamic JSON |
| `field.Enum(name)` | Yes | Yes | |
| `field.UUID(name, typ)` | Yes | Yes | |
| `field.Other(name, typ)` | Yes | Yes | Custom types |

### Builder Methods

| Method | Ent | Velox | Notes |
|--------|-----|-------|-------|
| `.Unique()` | Yes | Yes | |
| `.Optional()` | Yes | Yes | ORM-only zero value, NOT a DB DEFAULT |
| `.Nillable()` | Yes | Yes | NULL in DB, `*T` in Go |
| `.Nullable()` | Yes | **Removed** | Velox uses `Nillable()` only |
| `.Default(value)` | Yes | Yes | Literal default |
| `.DefaultFunc(fn)` | Yes | Yes | Function default (Go-only, no SQL effect) |
| `.UpdateDefault(fn)` | Yes | Yes | Called on update |
| `.Immutable()` | Yes | Yes | Create-only |
| `.Sensitive()` | Yes | Yes | Not printable/serializable |
| `.Comment(string)` | Yes | Yes | |
| `.StructTag(string)` | Yes | Yes | |
| `.StorageKey(string)` | Yes | Yes | Custom column name |
| `.SchemaType(map)` | Yes | Yes | Per-dialect SQL type |
| `.GoType(typ)` | Yes | Yes | Custom Go type |
| `.ValueScanner(vs)` | Yes | Yes | Custom value scanner |
| `.Annotations(...)` | Yes | Yes | |
| `.Deprecated(reason)` | Yes | Yes | |
| `.Validate(fn)` | Yes | Yes | Custom validation function |
| `.Match(regex)` | Yes | Yes | String only |
| `.MinLen(n)` | Yes | Yes | String only |
| `.MaxLen(n)` | Yes | Yes | String only |
| `.MinRuneLen(n)` | Yes | Yes | String only |
| `.MaxRuneLen(n)` | Yes | Yes | String only |
| `.NotEmpty()` | Yes | Yes | String/Bytes |
| `.Min(n)` | Yes | Yes | Numeric only |
| `.Max(n)` | Yes | Yes | Numeric only |
| `.Range(min, max)` | Yes | Yes | Numeric only |
| `.Positive()` | Yes | Yes | Numeric only |
| `.Negative()` | Yes | Yes | Numeric only |
| `.NonNegative()` | Yes | Yes | Numeric only |
| `.Values(...)` | Yes | Yes | Enum only |

### Field Types (Constants)

Both define identical field type constants: `TypeBool`, `TypeTime`, `TypeJSON`, `TypeUUID`, `TypeBytes`, `TypeEnum`, `TypeString`, `TypeOther`, plus all integer and float variants.

**Verdict: 99% identical.** Only difference: Velox removed `Nullable()` in favor of `Nillable()`.

---

## 3. Edge/Relationship API

### Constructors

| Constructor | Ent | Velox | Notes |
|-------------|-----|-------|-------|
| `edge.To(name, type)` | Yes | Yes | Forward/owner edge |
| `edge.From(name, type)` | Yes | Yes | Inverse/back-reference edge |

### Relationship Types

| Type | Ent | Velox | How to Define |
|------|-----|-------|---------------|
| One-to-One (O2O) | Yes | Yes | Both edges `.Unique()` |
| One-to-Many (O2M) | Yes | Yes | `To()` default (no `.Unique()`) |
| Many-to-One (M2O) | Yes | Yes | `From()` with `.Unique()` |
| Many-to-Many (M2M) | Yes | Yes | `.Through(name, edgeSchema)` |

### Edge Builder Methods

| Method | Ent | Velox | Notes |
|--------|-----|-------|-------|
| `.Unique()` | Yes | Yes | O2O or unique FK |
| `.Required()` | Yes | Yes | Must be set on create |
| `.Immutable()` | Yes | Yes | Create-only |
| `.Field(name)` | Yes | Yes | Bind to FK field |
| `.From(name)` | Yes | Yes | Inline inverse on `To()` |
| `.Ref(name)` | Yes | Yes | Forward edge name on `From()` |
| `.Through(name, type)` | Yes | Yes | M2M junction table |
| `.StructTag(string)` | Yes | Yes | |
| `.Comment(string)` | Yes | Yes | |
| `.StorageKey(...)` | Yes | Yes | Custom FK/table config |
| `.Annotations(...)` | Yes | Yes | |

### StorageKey Options

| Option | Ent | Velox | Notes |
|--------|-----|-------|-------|
| `edge.Table(name)` | Yes | Yes | M2M table name |
| `edge.Column(name)` | Yes | Yes | FK column name |
| `edge.Columns(a, b)` | Yes | Yes | M2M FK columns |
| `edge.Symbol(name)` | Yes | Yes | FK constraint name |
| `edge.Symbols(a, b)` | Yes | Yes | M2M FK constraints |

**Verdict: Identical.**

---

## 4. Index API

| Feature | Ent | Velox | Notes |
|---------|-----|-------|-------|
| `index.Fields(...)` | Yes | Yes | |
| `index.Edges(...)` | Yes | Yes | |
| `.Fields(...)` on builder | Yes | Yes | |
| `.Edges(...)` on builder | Yes | Yes | |
| `.Unique()` | Yes | Yes | |
| `.StorageKey(name)` | Yes | Yes | Custom index name |
| `.Annotations(...)` | Yes | Yes | |

**Verdict: Identical.**

---

## 5. Mixin API

### Interface

Both have the same `Mixin` interface: `Fields()`, `Edges()`, `Indexes()`, `Hooks()`, `Interceptors()`, `Policy()`, `Annotations()`.

### Built-in Mixins

| Mixin | Ent | Velox | Notes |
|-------|-----|-------|-------|
| `mixin.Schema` | Yes | Yes | Base (all methods return nil) |
| `mixin.Time` | Yes | Yes | `created_at` + `updated_at` |
| `mixin.CreateTime` | Yes | Yes | `created_at` only |
| `mixin.UpdateTime` | Yes | Yes | `updated_at` only |
| `mixin.ID` | No | **Yes** | UUID primary key |
| `mixin.SoftDelete` | No | **Yes** | `deleted_at` field |
| `mixin.TimeSoftDelete` | No | **Yes** | Time + SoftDelete |
| `mixin.TenantID` | No | **Yes** | Multi-tenant `tenant_id` |
| `mixin.Audit` | No | **Yes** | `created_at/by`, `updated_at/by` |

### Helper Functions

| Function | Ent | Velox | Notes |
|----------|-----|-------|-------|
| `mixin.AnnotateFields(m, ann...)` | Yes | Yes | Add annotations to mixin fields |
| `mixin.AnnotateEdges(m, ann...)` | Yes | Yes | Add annotations to mixin edges |

**Verdict: Velox adds 5 built-in mixins.** Ent has only 4 (Schema, Time, CreateTime, UpdateTime). Velox adds ID, SoftDelete, TimeSoftDelete, TenantID, and Audit.

---

## 6. Generated Client API

### Client Structure

```go
// Ent: Per-entity fields on Client struct
client := ent.NewClient(ent.Driver(drv))
client.User.Create()     // UserClient is a struct field
client.Pet.Create()

// Velox: Registry-based (no per-entity struct fields on root Client)
client := velox.NewClient(velox.Driver(drv))
client.User.Create()     // Still works, but implemented via registry
```

| Feature | Ent | Velox | Notes |
|---------|-----|-------|-------|
| Root Client | Per-entity fields | Registry + delegation | Velox uses `init()` registration |
| Entity Client | `*UserClient` struct | Interface-based | |
| `NewClient(opts...)` | Yes | Yes | |
| `Client.Debug()` | Yes | Yes | Debug mode |
| `Client.Close()` | Yes | Yes | |
| `Client.Use(hooks...)` | Yes | Yes | Global hooks |
| `Client.Intercept(...)` | Yes | Yes | Global interceptors |
| `Client.Schema` | Migration | Schema access | |

### Entity Client Methods

| Method | Ent | Velox | Notes |
|--------|-----|-------|-------|
| `.Create()` | Yes | Yes | Returns Create builder |
| `.CreateBulk(...)` | Yes | Yes | Bulk insert |
| `.Update()` | Yes | Yes | Update by predicate |
| `.UpdateOne(entity)` | Yes | Yes | Update single entity |
| `.UpdateOneID(id)` | Yes | Yes | Update by ID |
| `.Delete()` | Yes | Yes | Delete by predicate |
| `.DeleteOne(entity)` | Yes | Yes | Delete single entity |
| `.DeleteOneID(id)` | Yes | Yes | Delete by ID |
| `.Query()` | Yes | Yes | Query builder |
| `.Get(ctx, id)` | Yes | Yes | Get by ID |
| `.GetX(ctx, id)` | Yes | Yes | Get by ID (panic) |
| `.Hooks()` | Yes | Yes | Entity-specific hooks |
| `.Interceptors()` | Yes | Yes | Entity-specific interceptors |
| `.Use(hooks...)` | Yes | Yes | |
| `.Intercept(inters...)` | Yes | Yes | |

### Entity Struct

```go
// Ent: Entity struct has query methods
user.QueryPosts()       // Returns *PostQuery

// Velox: Entity struct also has query methods (via registry dispatch)
user.QueryPosts()       // Returns PostQuerier (interface)
// Also available through entity client:
userClient.QueryPosts(user)   // Returns PostQuerier
```

| Feature | Ent | Velox | Notes |
|---------|-----|-------|-------|
| Entity has query methods | Yes | Yes | Velox returns interface, Ent returns concrete type |
| Entity has `Unwrap()` | Yes | Yes | |
| Entity has `String()` | Yes | Yes | |
| Entity has `Edges` field | Yes | Yes | Loaded edge data |
| Entity has `selectValues` | Yes | Yes | For Select queries |

**Verdict: Same CRUD API.** Both have `QueryXxx()` on entity structs. Velox returns interfaces (`PostQuerier`) instead of concrete types (`*PostQuery`) due to sub-package architecture.

---

## 7. Query Builder API

### Query Methods

| Method | Ent | Velox | Notes |
|--------|-----|-------|-------|
| `.Where(predicates...)` | Yes | Yes | |
| `.Limit(n)` | Yes | Yes | |
| `.Offset(n)` | Yes | Yes | |
| `.Order(options...)` | Yes | Yes | |
| `.Select(fields...)` | Yes | Yes | |
| `.Unique(bool)` | Yes | Yes | |
| `.GroupBy(fields...)` | Yes | Yes | |
| `.Aggregate(funcs...)` | Yes | Yes | |
| `.Clone()` | Yes | Yes | |
| `.WithEdge(opts...)` | Yes | Yes | Eager loading |
| `.QueryEdge()` | Yes | Yes | Edge traversal |

### Execution Methods

| Method | Ent | Velox | Notes |
|--------|-----|-------|-------|
| `.All(ctx)` | Yes | Yes | All results |
| `.AllX(ctx)` | Yes | Yes | Panic variant |
| `.First(ctx)` | Yes | Yes | First result |
| `.FirstX(ctx)` | Yes | Yes | |
| `.FirstID(ctx)` | Yes | Yes | |
| `.FirstIDX(ctx)` | Yes | Yes | |
| `.Only(ctx)` | Yes | Yes | Exactly one |
| `.OnlyX(ctx)` | Yes | Yes | |
| `.OnlyID(ctx)` | Yes | Yes | |
| `.OnlyIDX(ctx)` | Yes | Yes | |
| `.Count(ctx)` | Yes | Yes | |
| `.CountX(ctx)` | Yes | Yes | |
| `.Exist(ctx)` | Yes | Yes | |
| `.ExistX(ctx)` | Yes | Yes | |
| `.IDs(ctx)` | Yes | Yes | |
| `.IDsX(ctx)` | Yes | Yes | |
| `.Scan(ctx, v)` | Yes | Yes | Custom struct |

### Predicate Functions (per-entity `where.go`)

| Predicate | Ent | Velox | Notes |
|-----------|-----|-------|-------|
| `user.ID(id)` | Yes | Yes | |
| `user.IDEQ(id)` | Yes | Yes | |
| `user.IDNEQ(id)` | Yes | Yes | |
| `user.IDIn(ids...)` | Yes | Yes | |
| `user.IDNotIn(ids...)` | Yes | Yes | |
| `user.IDGT(id)` | Yes | Yes | |
| `user.IDGTE(id)` | Yes | Yes | |
| `user.IDLT(id)` | Yes | Yes | |
| `user.IDLTE(id)` | Yes | Yes | |
| `user.NameEQ(v)` | Yes | Yes | Per-field |
| `user.NameNEQ(v)` | Yes | Yes | |
| `user.NameIn(v...)` | Yes | Yes | |
| `user.NameNotIn(v...)` | Yes | Yes | |
| `user.NameGT(v)` | Yes | Yes | Ordered types |
| `user.NameGTE(v)` | Yes | Yes | |
| `user.NameLT(v)` | Yes | Yes | |
| `user.NameLTE(v)` | Yes | Yes | |
| `user.NameContains(v)` | Yes | Yes | String only |
| `user.NameContainsFold(v)` | Yes | Yes | |
| `user.NameHasPrefix(v)` | Yes | Yes | |
| `user.NameHasSuffix(v)` | Yes | Yes | |
| `user.NameEqualFold(v)` | Yes | Yes | |
| `user.NameIsNil()` | Yes | Yes | Nillable fields |
| `user.NameNotNil()` | Yes | Yes | |
| `user.HasPosts()` | Yes | Yes | Edge exists |
| `user.HasPostsWith(...)` | Yes | Yes | Edge with conditions |
| `user.And(predicates...)` | Yes | Yes | |
| `user.Or(predicates...)` | Yes | Yes | |
| `user.Not(predicate)` | Yes | Yes | |

### Named Edge Loading

```go
// Ent (with FeatureNamedEdges)
posts, err := user.NamedPosts("recent")

// Velox (with FeatureNamedEdges)
posts, err := user.NamedPosts("recent")
```

**Verdict: Identical query API.**

---

## 8. Mutation Builder API

### Create Builder

| Method | Ent | Velox | Notes |
|--------|-----|-------|-------|
| `.Set<Field>(v)` | Yes | Yes | |
| `.SetNillable<Field>(*v)` | Yes | Yes | Set if non-nil |
| `.Add<Edge>(entities...)` | Yes | Yes | Add by entity |
| `.Add<Edge>IDs(ids...)` | Yes | Yes | Add by ID |
| `.Set<Edge>(entity)` | Yes | Yes | Set single edge |
| `.Set<Edge>ID(id)` | Yes | Yes | Set single edge by ID |
| `.Save(ctx)` | Yes | Yes | Returns `*Entity` |
| `.SaveX(ctx)` | Yes | Yes | Panic variant |
| `.Exec(ctx)` | Yes | Yes | Discard result |
| `.ExecX(ctx)` | Yes | Yes | |
| `.Mutation()` | Yes | Yes | Access mutation |

### Update Builder

| Method | Ent | Velox | Notes |
|--------|-----|-------|-------|
| `.Where(predicates...)` | Yes | Yes | |
| `.Set<Field>(v)` | Yes | Yes | |
| `.SetNillable<Field>(*v)` | Yes | Yes | |
| `.Add<NumericField>(delta)` | Yes | Yes | Increment/decrement |
| `.Clear<Field>()` | Yes | Yes | Set NULL (Nillable fields only) |
| `.Add<NumericField>(delta)` | Yes | Yes | Increment/decrement |
| `.Add<Edge>IDs(ids...)` | Yes | Yes | |
| `.Remove<Edge>IDs(ids...)` | Yes | Yes | |
| `.Clear<Edge>()` | Yes | Yes | |
| `.Save(ctx)` | Yes | Yes | Returns affected count |
| `.SaveX(ctx)` | Yes | Yes | |
| `.Exec(ctx)` | Yes | Yes | |
| `.ExecX(ctx)` | Yes | Yes | |
| `.Modifier(fn)` | Yes (feature) | Yes (feature) | Raw SQL modifier |
| `.Mutation()` | Yes | Yes | |
| `.SkipDefaults()` | No | **Yes** | Skip all UpdateDefault fields |
| `.SkipDefault(field)` | No | **Yes** | Skip specific UpdateDefault |

### Delete Builder

| Method | Ent | Velox | Notes |
|--------|-----|-------|-------|
| `.Where(predicates...)` | Yes | Yes | |
| `.Exec(ctx)` | Yes | Yes | Returns deleted count |
| `.ExecX(ctx)` | Yes | Yes | |

### Mutation Interface

Both implement the same `Mutation` interface:

| Method | Ent | Velox | Notes |
|--------|-----|-------|-------|
| `.Op()` | Yes | Yes | Operation type |
| `.Type()` | Yes | Yes | Entity type name |
| `.Fields()` | Yes | Yes | Changed field names |
| `.Field(name)` | Yes | Yes | Get field value |
| `.SetField(name, v)` | Yes | Yes | |
| `.AddedFields()` | Yes | Yes | Incremented fields |
| `.AddedField(name)` | Yes | Yes | |
| `.AddField(name, v)` | Yes | Yes | |
| `.ClearedFields()` | Yes | Yes | Cleared fields |
| `.FieldCleared(name)` | Yes | Yes | |
| `.ClearField(name)` | Yes | Yes | |
| `.ResetField(name)` | Yes | Yes | |
| `.AddedEdges()` | Yes | Yes | |
| `.AddedIDs(name)` | Yes | Yes | |
| `.RemovedEdges()` | Yes | Yes | |
| `.RemovedIDs(name)` | Yes | Yes | |
| `.ClearedEdges()` | Yes | Yes | |
| `.EdgeCleared(name)` | Yes | Yes | |
| `.ClearEdge(name)` | Yes | Yes | |
| `.ResetEdge(name)` | Yes | Yes | |
| `.OldField(ctx, name)` | Yes | Yes | Previous value |

**Verdict: Identical, plus Velox adds `SkipDefaults()`/`SkipDefault()`.**

---

## 9. Transaction API

| Feature | Ent | Velox | Notes |
|---------|-----|-------|-------|
| `client.Tx(ctx)` | Yes | Yes | Begin transaction |
| `client.BeginTx(ctx, opts)` | Yes | Yes | With SQL options |
| `tx.Commit()` | Yes | Yes | |
| `tx.Rollback()` | Yes | Yes | |
| `tx.OnCommit(hook)` | Yes | Yes | |
| `tx.OnRollback(hook)` | Yes | Yes | |
| `tx.Client()` | Yes | Yes | Get transactional client |
| `tx.<Entity>` | Yes | Yes | Entity clients in tx |
| Nested tx prevention | Yes | Yes | `ErrTxStarted` |

Both support the same `WithTx` helper pattern:

```go
// Works the same in both
func WithTx(ctx context.Context, client *Client, fn func(tx *Tx) error) error {
    tx, err := client.Tx(ctx)
    if err != nil { return err }
    defer func() {
        if v := recover(); v != nil {
            tx.Rollback()
            panic(v)
        }
    }()
    if err := fn(tx); err != nil {
        if rerr := tx.Rollback(); rerr != nil {
            err = fmt.Errorf("%w: rolling back: %v", err, rerr)
        }
        return err
    }
    return tx.Commit()
}
```

**Verdict: Identical.**

---

## 10. Hook & Interceptor API

### Types

| Type | Ent | Velox | Notes |
|------|-----|-------|-------|
| `Hook` | `func(Mutator) Mutator` | `func(Mutator) Mutator` | Same |
| `Mutator` | `Mutate(ctx, Mutation) (Value, error)` | Same | |
| `MutateFunc` | Adapter | Adapter | Same |
| `Interceptor` | `Intercept(Querier) Querier` | Same | |
| `Querier` | `Query(ctx, Query) (Value, error)` | Same | |
| `QuerierFunc` | Adapter | Adapter | Same |
| `InterceptFunc` | Adapter | Adapter | Same |
| `Traverser` | `Traverse(ctx, Query) error` | Same | Graph traversal |
| `TraverseFunc` | Adapter | Adapter | Same |

### Hook Registration

```go
// Both: Schema-level hooks
func (User) Hooks() []ent.Hook { ... }

// Both: Client-level hooks
client.Use(hook1, hook2)

// Both: Entity-level hooks
client.User.Use(hook1)
```

### Interceptor Registration

```go
// Both: Schema-level interceptors
func (User) Interceptors() []ent.Interceptor { ... }

// Both: Client-level interceptors
client.Intercept(inter1, inter2)

// Both: Entity-level interceptors
client.User.Intercept(inter1)
```

### Operations

| Constant | Ent | Velox | Notes |
|----------|-----|-------|-------|
| `OpCreate` | Yes | Yes | |
| `OpUpdate` | Yes | Yes | |
| `OpUpdateOne` | Yes | Yes | |
| `OpDelete` | Yes | Yes | |
| `OpDeleteOne` | Yes | Yes | |

**Verdict: Identical.**

---

## 11. Privacy/Policy API

### Core Types

| Type | Ent | Velox | Notes |
|------|-----|-------|-------|
| `Policy` interface | Yes | Yes | `EvalMutation` + `EvalQuery` |
| `QueryPolicy` | `[]QueryRule` | `[]QueryRule` | Same |
| `MutationPolicy` | `[]MutationRule` | `[]MutationRule` | Same |
| `Allow` sentinel | Yes | Yes | |
| `Deny` sentinel | Yes | Yes | |
| `Skip` sentinel | Yes | Yes | |
| `Allowf()` | Yes | Yes | |
| `Denyf()` | Yes | Yes | |
| `Skipf()` | Yes | Yes | |

### Built-in Rules

| Rule | Ent | Velox | Notes |
|------|-----|-------|-------|
| `AlwaysAllowRule()` | Yes | Yes | |
| `AlwaysDenyRule()` | Yes | Yes | |
| `ContextQueryMutationRule(fn)` | Yes | Yes | |
| `OnMutationOperation(rule, op)` | Yes | Yes | |
| `DenyMutationOperationRule(op)` | Yes | Yes | |
| `AllowMutationOperationRule(op)` | No | **Yes** | Velox addition |
| `DenyIfNoViewer()` | No | **Yes** | Velox addition |
| `HasRole(role)` | No | **Yes** | Velox addition |
| `HasAnyRole(roles...)` | No | **Yes** | Velox addition |
| `IsOwner(fieldName)` | No | **Yes** | Velox addition |
| `TenantRule(fieldName)` | No | **Yes** | Velox addition |
| `TenantQueryRule()` | No | **Yes** | Velox addition |
| `OwnerQueryRule()` | No | **Yes** | Velox addition |

### Viewer System

| Feature | Ent | Velox | Notes |
|---------|-----|-------|-------|
| `Viewer` interface | No | **Yes** | `ID()`, `Roles()` |
| `TenantIDer` interface | No | **Yes** | `TenantID()` |
| `SimpleViewer` struct | No | **Yes** | Ready-made implementation |
| `WithViewer(ctx, v)` | No | **Yes** | Context helper |
| `ViewerFromContext(ctx)` | No | **Yes** | Context helper |

### FilterFunc

| Feature | Ent | Velox | Notes |
|---------|-----|-------|-------|
| `FilterFunc` type | Yes | Yes | Row-level filtering |
| `Filter` interface | Yes | Yes | `WhereP()` method |
| `DecisionContext()` | Yes | Yes | Cached decision |
| `DecisionFromContext()` | Yes | Yes | |

**Verdict: Same core. Velox adds a Viewer system, built-in rules, and multi-tenancy helpers.** In Ent, users must implement these from scratch.

---

## 12. GraphQL Integration

### Entity-Level Annotations

| Annotation | Ent (`entgql`) | Velox (`graphql`) | Notes |
|------------|----------------|-------------------|-------|
| `RelayConnection()` | Yes | Yes | Cursor pagination |
| `QueryField()` | Yes | Yes | Root query field |
| `Type(name)` | Yes | Yes | Custom type name |
| `Mutations(opts...)` | Yes | Yes | |
| `MutationCreate()` | Yes | Yes | |
| `MutationUpdate()` | Yes | Yes | |
| `MutationDelete()` | No | No | Neither generates delete mutations |
| `Skip(mode)` | Yes | Yes | |
| `Directives(...)` | Yes | Yes | |
| `Implements(...)` | Yes | Yes | |
| `MultiOrder()` | Yes | Yes | |
| `Bind()` | Yes | No | Ent-only (auto-bind fields to GQL) |

### Field-Level Annotations

| Annotation | Ent (`entgql`) | Velox (`graphql`) | Notes |
|------------|----------------|-------------------|-------|
| `OrderField(name)` | Yes | Yes | |
| `Skip(mode)` | Yes | Yes | |
| `Directives(...)` | Yes | Yes | |
| `Omittable()` | No | **Yes** | PATCH semantics |
| `CreateInputValidate(tag)` | No | **Yes** | go-playground/validator |
| `UpdateInputValidate(tag)` | No | **Yes** | go-playground/validator |
| `MutationInputValidate(c,u)` | No | **Yes** | Shorthand for both |
| `EnumValues(map)` | No | **Yes** | Override GraphQL enum names |

### WhereInput (Filtering)

| Feature | Ent | Velox | Notes |
|---------|-----|-------|-------|
| Default mode | **All fields exposed** | **Whitelist (opt-in)** | Major difference |
| `WhereInput()` (field) | N/A | Yes | Opt-in per field |
| `WhereOps(ops)` | N/A | Yes | Control operators |
| `WhereInputFields(names...)` | N/A | Yes | Bulk opt-in |
| `WhereInputEdges(names...)` | N/A | Yes | Edge filtering |
| `FeatureWhereInputAll` | N/A | Yes | Ent-compat mode |

This is a **significant security difference**. Ent exposes all fields in WhereInput by default, which can leak internal fields (e.g., `password_hash`, `internal_status`). Velox requires explicit opt-in.

### Resolver Annotations (Velox-only)

```go
// Velox-only: Custom resolver field definitions
graphql.Resolvers(
    graphql.Map("priceListItem(priceListId: ID!)", "PriceListItem!").WithComment("..."),
    graphql.Map("glAccount", "PublicGlAccount!"),
)
```

Ent has no equivalent — custom resolvers must be manually added to the GraphQL schema.

### Schema Split Modes

| Mode | Ent | Velox | Notes |
|------|-----|-------|-------|
| Single file | Yes | Yes | |
| Per-category split | No | **Yes** | types/inputs/connections/scalars |
| Per-entity split | No | **Yes** | For large schemas (50+ entities) |

### Mutation Generation Comparison

| | Ent | Velox |
|---|-----|-------|
| Default (no annotation) | No mutations | No mutations |
| `Mutations()` (no args) | Create + Update | Create + Update |
| `Mutations(MutationCreate())` | Create only | Create only |
| `Mutations(MutationUpdate())` | Update only | Update only |
Both Ent and Velox require hand-written resolvers for delete mutations. Delete operations are available at the ORM layer (`client.User.DeleteOneID(id).Exec(ctx)`), but neither generates GraphQL delete mutations from annotations.

### Generated GraphQL Code Architecture

| Aspect | Ent | Velox |
|--------|-----|-------|
| Architecture | 7 monolith files | Per-entity sub-packages |
| Largest file (50 entities) | 56,050 lines (`mutation.go`) | 9,444 lines |
| WhereInput file | 26,093 lines | Per-entity |
| Node interface | Central resolver | Registry dispatch |

**Verdict: Compatible core, but Velox adds input validation, whitelist filtering, resolver annotations, schema splitting, and Omittable.**

---

## 13. Schema Migration

### API

| Feature | Ent | Velox | Notes |
|---------|-----|-------|-------|
| Auto-migration | Yes | Yes | `client.Schema.Create(ctx)` |
| Atlas integration | Yes | Yes | Diff-based migration |
| Versioned migrations | Yes (feature) | Yes (feature) | |
| `WithDropColumn` | Yes | Yes | |
| `WithDropIndex` | Yes | Yes | |
| `WithGlobalUniqueID` | Yes | Yes | |
| `WithForeignKeys` | Yes | Yes | |

### SQL Schema Annotations

| Annotation | Ent (`entsql`) | Velox (`sqlschema`) | Notes |
|------------|----------------|---------------------|-------|
| `Table(name)` | Yes | Yes | Custom table name |
| `ColumnType(type)` | Yes | Yes | Custom column type |
| `Charset(cs)` | Yes | Yes | |
| `Collation(c)` | Yes | Yes | |
| `Check(expr)` | No | **Yes** | CHECK constraint |
| `Default(v)` | No | **Yes** | SQL-level DEFAULT |
| `DefaultExpr(expr)` | No | **Yes** | SQL expression DEFAULT |
| `OnDelete(action)` | No | **Yes** | FK cascade action |
| `IndexType(type)` | No | **Yes** | Custom index type (GIN, etc.) |

**Verdict: Same migration engine. Velox adds more granular SQL annotations.**

---

## 14. Feature Flags

### Shared Features (both Ent and Velox)

| Feature | Description |
|---------|-------------|
| `FeaturePrivacy` | Privacy/authorization layer |
| `FeatureIntercept` | Query interceptors |
| `FeatureEntQL` | Runtime query language |
| `FeatureNamedEdges` | Dynamic edge loading |
| `FeatureBidiEdgeRefs` | Two-way edge references |
| `FeatureSnapshot` | Schema snapshots |
| `FeatureSchemaConfig` | Multi-schema names |
| `FeatureLock` | Row-level SQL locking (FOR UPDATE) |
| `FeatureModifier` | Custom query modifiers |
| `FeatureExecQuery` | Raw SQL execution |
| `FeatureUpsert` | ON CONFLICT support |
| `FeatureVersionedMigration` | Versioned migration files |
| `FeatureGlobalID` | Unique global IDs |

### Velox-Only Features

| Feature | Description |
|---------|-------------|
| `FeatureValidator` | ORM-level field validation code generation |
| `FeatureEntPredicates` | Generate Ent-compatible predicate functions |
| `FeatureAutoDefault` | Auto-add DB DEFAULT for all NOT NULL fields |
| `FeatureWhereInputAll` | All fields filterable (Ent-compat mode) |

**Verdict: Velox has all Ent features plus 4 additional.**

---

## 15. Code Generation

### Architecture

| Aspect | Ent | Velox |
|--------|-----|-------|
| Engine | `text/template` + `go/format` | **Jennifer AST** |
| Formatting | Post-process via `go/format` | Pre-formatted from AST |
| Parallelism | Sequential templates | `errgroup` + semaphore |
| Output | Monolithic files | Per-entity sub-packages |
| Entity registration | Client struct fields | `init()` registry |
| Extension system | `entc.Extension` interface | `compiler.Extension` interface |
| Custom templates | Yes | Yes |
| Gen hooks | Yes | Yes |

### Template vs Jennifer

```go
// Ent: text/template
{{ range $e := $.Edges }}
func ({{ $.Receiver }} *{{ $.Name }}) Query{{ $e.StructField }}() *{{ $e.Type.QueryName }} {
    return New{{ $e.Type.QueryName }}({{ $.Receiver }}.config, ...)
}
{{ end }}

// Velox: Jennifer AST
func (h *SQLDialect) genQueryEdge(f *jen.File, t *gen.Type, e *gen.Edge) {
    f.Func().Params(jen.Id("q").Op("*").Id(t.QueryName())).
        Id("Query" + e.StructField()).
        Params().Op("*").Id(e.Type.QueryName()).
        Block(...)
}
```

| Comparison | Ent (template) | Velox (Jennifer) |
|------------|----------------|------------------|
| Type safety | None (string concatenation) | **Compile-time checked** |
| IDE support | No autocomplete | **Full Go autocomplete** |
| Debugging | Template parse errors | **Go compiler errors** |
| Formatting | Requires `go/format` pass | **Inherent from AST** |
| Performance | Slower (format pass) | **Faster (no format pass)** |
| Readability | More natural for text | More verbose but precise |
| Learning curve | Lower | Higher |

### Generated Code Layout

```
# Ent: Monolithic
ent/
├── client.go          (9,686 lines for 50 entities)
├── mutation.go        (56,050 lines)
├── user.go            (entity + edges + query methods)
├── user_create.go
├── user_update.go
├── user_delete.go
├── user_query.go
└── ...

# Velox: Per-entity sub-packages
velox/
├── client.go          (658 lines)
├── velox.go
├── user/
│   ├── client.go      (Create, Update, Delete, Query, Get)
│   ├── create.go
│   ├── update.go
│   ├── delete.go
│   ├── mutation.go
│   ├── where.go
│   └── runtime.go     (init() registration)
├── query/
│   ├── user_query.go  (with edge loading)
│   └── ...
└── ...
```

**Verdict: Fundamentally different engines. Velox uses type-safe AST generation vs Ent's string templates.**

---

## 16. Error Handling

### Sentinel Errors

| Error | Ent | Velox | Notes |
|-------|-----|-------|-------|
| `ErrNotFound` | Yes | Yes | |
| `ErrNotSingular` | Yes | Yes | |
| `ErrTxStarted` | Yes | Yes | |

### Structured Errors

| Error Type | Ent | Velox | Notes |
|------------|-----|-------|-------|
| `NotFoundError` | Yes | Yes | |
| `NotSingularError` | Yes | Yes | |
| `ConstraintError` | Yes | Yes | |
| `ValidationError` | Yes | Yes | |
| `NotLoadedError` | Yes | Yes | |
| `QueryError` | No | **Yes** | Velox addition |
| `MutationError` | No | **Yes** | Velox addition |
| `PrivacyError` | No | **Yes** | Velox addition |
| `RollbackError` | No | **Yes** | Velox addition |
| `AggregateError` | No | **Yes** | Velox addition |

### Error Checkers

| Function | Ent | Velox | Notes |
|----------|-----|-------|-------|
| `IsNotFound(err)` | Yes | Yes | |
| `IsNotSingular(err)` | Yes | Yes | |
| `IsConstraintError(err)` | Yes | Yes | |
| `IsValidationError(err)` | Yes | Yes | |
| `IsNotLoaded(err)` | No | **Yes** | Velox addition |

Both use `errors.Is`/`errors.As` compatible error chains.

**Verdict: Same core errors. Velox adds more structured error types.**

---

## 17. SQL Builder

Both use a similar SQL builder pattern. Velox's builder is in `dialect/sql/`.

### Builder API

| Feature | Ent | Velox | Notes |
|---------|-----|-------|-------|
| `sql.Select(cols...)` | Yes | Yes | |
| `sql.Insert(table)` | Yes | Yes | |
| `sql.Update(table)` | Yes | Yes | |
| `sql.Delete(table)` | Yes | Yes | |
| `sql.CreateTable(table)` | Yes | Yes | DDL |
| `.Where(predicates)` | Yes | Yes | |
| `.Join(table)` | Yes | Yes | |
| `.LeftJoin(table)` | Yes | Yes | |
| `.RightJoin(table)` | Yes | Yes | |
| `.GroupBy(cols...)` | Yes | Yes | |
| `.Having(predicates)` | Yes | Yes | |
| `.OrderBy(cols...)` | Yes | Yes | |
| `.Limit(n)` | Yes | Yes | |
| `.Offset(n)` | Yes | Yes | |
| `.Distinct()` | Yes | Yes | |
| `.As(alias)` | Yes | Yes | |
| `.Count()` | Yes | Yes | |
| `.Max(col)` | Yes | Yes | |
| `.Min(col)` | Yes | Yes | |
| `.Sum(col)` | Yes | Yes | |
| `.Avg(col)` | Yes | Yes | |
| `sql.EQ(col, val)` | Yes | Yes | |
| `sql.NEQ(col, val)` | Yes | Yes | |
| `sql.GT/GTE/LT/LTE` | Yes | Yes | |
| `sql.In(col, vals...)` | Yes | Yes | |
| `sql.NotIn(col, vals...)` | Yes | Yes | |
| `sql.Like(col, pat)` | Yes | Yes | |
| `sql.IsNull(col)` | Yes | Yes | |
| `sql.NotNull(col)` | Yes | Yes | |
| `sql.And(...)` | Yes | Yes | |
| `sql.Or(...)` | Yes | Yes | |
| `sql.Not(...)` | Yes | Yes | |

### Driver API

| Feature | Ent | Velox | Notes |
|---------|-----|-------|-------|
| `Driver` interface | Yes | Yes | `Exec`, `Query`, `Tx`, `Close`, `Dialect` |
| `Tx` interface | Yes | Yes | `Commit`, `Rollback` |
| `Debug(driver)` | Yes | Yes | Debug logging |
| `DebugWithContext(driver, fn)` | Yes | Yes | Contextual debug |
| Driver stats | No | **Yes** | `DriverStats` for connection pool |

**Verdict: Same SQL builder API. Velox adds driver stats.**

---

## 18. Database Support

| Database | Ent | Velox | Notes |
|----------|-----|-------|-------|
| PostgreSQL | Yes | Yes | Primary target |
| MySQL | Yes | Yes | |
| SQLite | Yes (CGO) | Yes (**pure Go**) | Ent uses `go-sqlite3` (CGO), Velox uses `modernc.org/sqlite` |
| TiDB | Yes | No | MySQL-compatible |
| CockroachDB | Yes | No | Postgres-compatible |
| Gremlin (graph DB) | Yes | **No** | Ent-only dialect |

### SQLite Difference

| | Ent | Velox |
|---|-----|-------|
| Driver | `mattn/go-sqlite3` | `modernc.org/sqlite` |
| CGO required | **Yes** | **No** |
| Driver name | `"sqlite3"` | `"sqlite"` |
| FK pragma | `_fk=1` | `_pragma=foreign_keys(1)` |
| Cross-compile | Difficult | Easy |

**Verdict: Same core databases. Ent has Gremlin + TiDB/CockroachDB. Velox has CGO-free SQLite.**

---

## 19. Performance

### Code Generation (50-entity schema)

| Metric | Ent | Velox | Winner |
|--------|-----|-------|--------|
| Generation speed (median) | 6.32s | **2.00s** | Velox (3.2x) |
| Generation memory (median) | 1.86 GB | **0.89 GB** | Velox (2.1x) |
| Sys CPU time | 22.22s | **2.63s** | Velox (8.5x) |

### Compilation

| Metric | Ent | Velox | Winner |
|--------|-----|-------|--------|
| Cold build wall time | **12.37s** | 13.54s | Ent (9%) |
| Cold build memory | 3.31 GB | **1.54 GB** | Velox (2.2x) |
| Incremental build | 0.20s | 0.24s | Tie |

### Generated Code Size

| Metric | Ent | Velox | Winner |
|--------|-----|-------|--------|
| Total lines | 335,365 | **230,280** | Velox (31% less) |
| Largest file | 56,050 | **9,444** | Velox (5.9x smaller) |
| Files > 1,000 lines | 47 | **0** | Velox |
| `client.go` size | 9,686 | **658** | Velox (14.7x) |

---

## 20. Velox-Only Features

Features present in Velox but NOT in Ent:

| Feature | Description |
|---------|-------------|
| **WhereInput whitelist** | Fields must opt-in to filtering (Ent exposes all by default) |
| **Input validation tags** | `CreateInputValidate("required,email")` via go-playground/validator |
| **Resolver annotations** | `graphql.Resolvers(graphql.Map(...))` for custom resolver fields |
| **Schema split modes** | Per-category and per-entity GraphQL schema splitting |
| **`Omittable()` annotation** | PATCH semantics with `graphql.Omittable[T]` |
| **`SkipDefaults()`/`SkipDefault()`** | Skip UpdateDefault fields on update |
| **`FeatureAutoDefault`** | Auto-add DB DEFAULT for all NOT NULL fields |
| **`FeatureValidator`** | ORM-level field validation code generation |
| **`FeatureWhereInputAll`** | Ent-compat mode (all fields filterable) |
| **`FeatureEntPredicates`** | Generate Ent-compatible predicate style |
| **`sqlschema.Check(expr)`** | CHECK constraint annotation |
| **`sqlschema.Default(v)`** | SQL-level DEFAULT annotation |
| **`sqlschema.DefaultExpr(expr)`** | SQL expression DEFAULT annotation |
| **`sqlschema.OnDelete(action)`** | FK cascade action annotation |
| **`sqlschema.IndexType(type)`** | Custom index type (GIN, etc.) |
| **Built-in mixins** | ID, SoftDelete, TimeSoftDelete, TenantID, Audit |
| **Privacy Viewer system** | `Viewer` interface, `SimpleViewer`, context helpers |
| **Privacy built-in rules** | `DenyIfNoViewer`, `HasRole`, `IsOwner`, `TenantRule`, etc. |
| **Structured errors** | `QueryError`, `MutationError`, `PrivacyError`, `RollbackError`, `AggregateError` |
| **Driver stats** | Connection pool statistics |
| **Pure Go SQLite** | `modernc.org/sqlite` (no CGO) |
| **Jennifer AST codegen** | Type-safe code generation |
| **Per-entity sub-packages** | Smaller files, better IDE performance |
| **`EnumValues()` annotation** | Override GraphQL enum value names |
| **`WhereOps()` operator sets** | Granular control over filter operators |

---

## 21. Ent-Only Features

Features present in Ent but NOT in Velox:

| Feature | Description |
|---------|-------------|
| **Gremlin dialect** | Graph database support (Apache TinkerPop) |
| **TiDB support** | MySQL-compatible distributed database |
| **CockroachDB support** | Postgres-compatible distributed database |
| **`entgql.Bind()`** | Auto-bind fields to GraphQL schema |
| **`entgql.SkipTxFunc`** | Skip transaction for specific GQL operations |
| **Larger ecosystem** | More community extensions, tutorials, examples |
| **Atlas DevURL** | Built-in dev database for migration diffing |

---

## 22. Summary Table

| Category | Ent | Velox | Notes |
|----------|-----|-------|-------|
| **Schema API** | Full | **Full + extras** | Identical core, Velox adds mixins |
| **Field API** | Full | **Full** | Velox removed `Nullable()` |
| **Edge API** | Full | **Full** | Identical |
| **Index API** | Full | **Full** | Identical |
| **Client API** | Full | **Full** | Different internals, same surface |
| **Query API** | Full | **Full** | Identical |
| **Mutation API** | Full | **Full + SkipDefaults** | |
| **Transaction API** | Full | **Full** | Identical |
| **Hook/Interceptor** | Full | **Full** | Identical |
| **Privacy** | Basic | **Extended** | Velox adds Viewer, built-in rules |
| **GraphQL** | Good | **Better** | Velox adds validation, whitelist |
| **Migration** | Good | **Better** | Velox adds CHECK, DEFAULT, FK actions |
| **Feature flags** | 13 | **17** | Velox adds 4 |
| **Code gen** | Templates | **Jennifer AST** | Velox is type-safe, 3.2x faster |
| **Error handling** | Basic | **Extended** | Velox adds 5 error types |
| **Databases** | 5 (w/ Gremlin) | 3 | Ent has more backends |
| **Performance** | Baseline | **3.2x faster gen** | Velox wins on gen, Ent wins cold compile |
| **Ecosystem** | Large | Growing | Ent has more community |
| **CGO** | Required (SQLite) | **Not required** | Velox uses pure Go SQLite |

### One-Line Summary

**Velox is a superset of Ent's API** with identical schema/query/mutation patterns, faster code generation (3.2x), smaller output (31% fewer lines), and additional features (input validation, whitelist filtering, built-in privacy rules, pure Go SQLite). Ent has a larger ecosystem and supports more databases (Gremlin, TiDB, CockroachDB).
