# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Velox is a type-safe Go ORM framework with integrated code generation for GraphQL services. It generates production-ready database access code and GraphQL schemas/resolvers from declarative schema definitions written in Go.

## Build and Generate Commands

```bash
# Run tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run linter
golangci-lint run

# Format code
gofmt -s -w .
goimports -w .
```

## Project Structure

Recommended layout separates schema definitions from generated code:

```
myproject/
├── schema/              # Your schema definitions (you edit these)
│   ├── user.go
│   ├── post.go
│   └── ...
├── velox/               # Generated code (don't edit)
│   ├── client.go
│   ├── user.go
│   ├── ent.graphql
│   └── ...
├── generate.go          # Code generation script
├── gqlgen.yml           # gqlgen configuration
└── go.mod
```

**generate.go:**
```go
//go:build ignore

package main

import (
    "log/slog"
    "os"

    "github.com/syssam/velox/compiler"
    "github.com/syssam/velox/compiler/gen"
    "github.com/syssam/velox/contrib/graphql"
)

func main() {
    ex, err := graphql.NewExtension(
        graphql.WithConfigPath("./gqlgen.yml"),
        graphql.WithSchemaPath("./velox/ent.graphql"),
    )
    if err != nil {
        slog.Error("creating graphql extension", "error", err)
        os.Exit(1)
    }

    cfg, err := gen.NewConfig(
        gen.WithTarget("./velox"),
    )
    if err != nil {
        slog.Error("creating config", "error", err)
        os.Exit(1)
    }

    if err := compiler.Generate("./schema", cfg,
        compiler.Extensions(ex),
    ); err != nil {
        slog.Error("running velox codegen", "error", err)
        os.Exit(1)
    }
}
```

Run with: `go run generate.go`

## Architecture

### Data Flow

```
Schema Definition (schema/*.go)
        ↓
   velox.Schema interface + builders (field, edge, mixin)
        ↓
   graph.Builder → graph.Graph (internal representation)
        ↓
   graph.Validate() (edge linking, uniqueness checks)
        ↓
   compiler/gen/* generators (Jennifer-based code generation)
        ↓
   Generated code (velox/)
        ↓
   dialect/sql for runtime database operations
```

### Core Packages

#### Root Package (`velox.go`)
Core interfaces that define the schema contract:
- `velox.Schema` - Base type for entity definitions (embeddable)
- `velox.Field` - Field interface with `Descriptor()` method
- `velox.Edge` - Relationship interface
- `velox.Index` - Index definition interface
- `velox.Mixin` - Reusable schema components
- `velox.Hook` - Mutation lifecycle hooks
- `velox.Interceptor` - Query middleware
- `velox.Policy` - Privacy/authorization policies
- `velox.Annotation` - Extensibility for generators
- Standard errors: `ErrNotFound`, `ErrNotSingular`, `ErrConstraintViolation`, etc.
- Hook utilities: `On()` for conditional hooks, `Traverse()` for interceptors

#### Schema Builders (`schema/`)
Fluent API for defining schemas (following Ent's patterns):
- `schema/field/` - Field builders: `String()`, `Int64()`, `Time()`, `UUID()`, `Enum()`, `JSON()`, `Custom()`
- `schema/edge/` - Relationship builders: `To()` (O2M default), `From()` (belongs-to), `.Unique()` (O2O), `.Through()` (M2M)
- `schema/mixin/` - Base mixin types: `Schema` (embed for custom mixins), `AnnotateFields()`, `AnnotateEdges()`
- `contrib/mixin/` - Common mixin implementations (optional): `CreateTime`, `UpdateTime`, `Time`, `ID`, `SoftDelete`, `TenantID`, `TimeSoftDelete`
- `schema/index/` - Index builder: `Fields()` for composite indexes, `.Unique()`, `.Name()`
- `dialect/sqlschema/` - SQL annotations (size, column type, check, charset, collation, defaults, foreign key actions)
- `contrib/graphql/` - GraphQL annotations (skip, mutations, relay, directives) and code generation

#### Graph Package (`graph/`)
Documentation package only. The actual graph implementation is in `compiler/gen/`:
- `graph/doc.go` - Package documentation

**Actual implementation in `compiler/gen/`:**
- `graph.go` - `Graph` type holding all `Type` definitions with validation, `Config`, `Snapshot` (uses explicit error returns, no panic-recover)
- `type.go` - `Type` struct definition with core methods
- `type_field.go` - Field-related methods (Constant, DefaultName, ScanType, etc.)
- `type_edge.go` - Edge-related methods (Label, M2M, O2O, ForeignKey) and Rel type
- `type_helpers.go` - Helper functions (structTag, builderField) and global variables

#### Jennifer Generator (`compiler/gen/`)
High-performance code generation using [Jennifer](https://github.com/dave/jennifer/jen) instead of templates:

**Architecture:**
```
┌─────────────────────────────────────────────────────────────┐
│                    JenniferGenerator                        │
│  (Orchestration: parallel execution, file writing)          │
└─────────────────────────┬───────────────────────────────────┘
                          │ uses
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                   DialectGenerator                          │
│  (Interface: defines what each dialect must implement)      │
└─────────────────────────┬───────────────────────────────────┘
                          │ implemented by
          ┌───────────────┼───────────────┐
          ▼               ▼               ▼
   ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
   │ SQLDialect  │ │GremlinDialect│ │ CustomDialect│
   │ (built-in)  │ │  (future)   │ │ (user-defined)│
   └─────────────┘ └─────────────┘ └─────────────┘
```

**Core Files (`compiler/gen/`):**
- `generate.go` - `JenniferGenerator` struct with parallel execution via errgroup
- `dialect.go` - Generator interfaces (EntityGenerator, GraphGenerator, FeatureGenerator, etc.)
- `graph.go` - `Graph` and `Config` types for schema representation
- `type.go` - `Type` representing an entity with fields, edges, indexes
- `type_field.go` - Field-related methods (Constant, DefaultName, ScanType, etc.)
- `type_edge.go` - Edge-related methods and Rel type (O2O, O2M, M2O, M2M)
- `type_helpers.go` - Helper functions and global variables
- `errors.go` - Structured error types (SchemaError, ConfigError, EdgeError, etc.)
- `option.go` - Functional options pattern (WithHeader, WithPackage, etc.)
- `config.go` - Config methods and grouped configuration types
- `func.go` - Helper functions (Pascal, snake_case conversion, etc.)
- `feature.go` - Feature flags and optional feature definitions
- `template.go` - Template utilities for code generation
- `writer.go` - File writing utilities

**SQL Dialect Implementation (`compiler/gen/sql/`):**

*Per-Entity Generation:*
- `entity.go` - Entity struct, edges struct, scanValues/assignValues, entity client
- `create.go` - Create builder, CreateBulk builder, field setters, upsert support
- `update.go` - Update builder, UpdateOne builder, field setters
- `delete.go` - Delete builder, DeleteOne builder
- `query.go` - Query builder with Where, Order, Limit, Offset, eager loading
- `mutation.go` - Mutation type with field getters/setters, edge operations
- `predicate.go` - Where predicates (EQ, NEQ, GT, LT, Contains, etc.)
- `package.go` - Entity package constants (table name, columns, descriptors)

*Graph-Level Generation:*
- `velox.go` - Base types, errors, interfaces, Op enum, Value interface
- `client.go` - Client struct, entity clients, hooks/interceptors registration
- `tx.go` - Transaction support (Tx struct, Commit, Rollback)
- `runtime.go` - Runtime utilities, schema descriptors, hook helpers
- `dialect.go` - SQL dialect implementation, feature support

*Feature-Specific Generation:*
- `intercept.go` - Query interceptors support
- `privacy.go` - Privacy policy code generation
- `snapshot.go` - Schema snapshot for conflict resolution
- `schemaconfig.go` - Multi-schema configuration
- `globalid.go` - Global ID support for Relay
- `versioned_migration.go` - Versioned migration support
- `entql.go` - EntQL query language support

**Benefits over Templates:**
- Auto-tracking imports (no goimports needed)
- Streaming writes to disk (lower memory)
- Compile-time type safety
- Parallel generation with configurable workers (errgroup + semaphore)

**Custom Dialect Example:**
```go
type MyDialect struct {
    helper gen.GeneratorHelper
}

func (d *MyDialect) Name() string { return "my-dialect" }
func (d *MyDialect) GenEntity(t *gen.Type) *jen.File { ... }
func (d *MyDialect) GenCreate(t *gen.Type) *jen.File { ... }
// ... implement all DialectGenerator interface methods

// Use with sql.Generate():
generator := gen.NewJenniferGenerator(graph, outDir)
generator.WithDialect(&MyDialect{helper: generator})
generator.Generate(ctx)
```

**Generator Interface Hierarchy (Interface Segregation Principle):**

The generator interfaces are split into focused, composable interfaces:

```go
// EntityGenerator generates per-entity code (8 methods)
type EntityGenerator interface {
    GenEntity(t *Type) *jen.File
    GenCreate(t *Type) *jen.File
    GenUpdate(t *Type) *jen.File
    GenDelete(t *Type) *jen.File
    GenQuery(t *Type) *jen.File
    GenMutation(t *Type) *jen.File
    GenPredicate(t *Type) *jen.File
    GenPackage(t *Type) *jen.File
}

// GraphGenerator generates graph-level code (5 methods)
type GraphGenerator interface {
    GenClient() *jen.File
    GenVelox() *jen.File
    GenTx() *jen.File
    GenRuntime() *jen.File
    GenPredicatePackage() *jen.File
}

// FeatureGenerator handles feature detection and generation
type FeatureGenerator interface {
    SupportsFeature(feature string) bool
    GenFeature(feature string) *jen.File
}

// OptionalFeatureGenerator provides optional feature support (7 methods)
type OptionalFeatureGenerator interface {
    GenSchemaConfig() *jen.File
    GenIntercept() *jen.File
    GenPrivacy() *jen.File
    GenSnapshot() *jen.File
    GenVersionedMigration() *jen.File
    GenGlobalID() *jen.File
    GenEntQL() *jen.File
}

// MinimalDialect is the minimum interface for basic dialect support
type MinimalDialect interface {
    Name() string
    EntityGenerator
    GraphGenerator
}

// DialectGenerator is the full interface (composes all interfaces)
type DialectGenerator interface {
    MinimalDialect
    FeatureGenerator
    OptionalFeatureGenerator
}
```

Custom dialects can implement `MinimalDialect` for basic support, or `DialectGenerator` for full feature support. The `JenniferGenerator` detects optional capabilities via type assertion at runtime.

**Structured Error Types (`compiler/gen/errors.go`):**

The gen package provides structured error types for better error handling:

```go
// Sentinel errors
var (
    ErrInvalidSchema    = errors.New("velox: invalid schema")
    ErrMissingConfig    = errors.New("velox: missing configuration")
    ErrInvalidEdge      = errors.New("velox: invalid edge definition")
    ErrGenerationFailed = errors.New("velox: code generation failed")
    ErrValidationFailed = errors.New("velox: validation failed")
)

// Structured error types
type SchemaError struct { Type, Field, Message string; Cause error }
type ConfigError struct { Option string; Value any; Message string }
type EdgeError struct { From, To, Edge, Message string; Cause error }
type GenerationError struct { Phase, File, Message string; Cause error }
type ValidationError struct { Type, Field string; Value any; Message string; Cause error }
// Note: For collecting multiple errors, use errors.Join (Go 1.20+)

// Error checking functions
func IsSchemaError(err error) bool
func IsConfigError(err error) bool
func IsEdgeError(err error) bool
func IsGenerationError(err error) bool
func IsValidationError(err error) bool
```

**Functional Options Pattern (`compiler/gen/option.go`):**

Configuration can be done via functional options. **Package and Target are auto-inferred**
from the schema path, so most users don't need to specify them:

```go
// Basic usage - Package and Target auto-inferred from schema path
config, err := gen.NewConfig()
compiler.Generate("./schema", config)  // Infers package from go.mod, target from schema path

// Available options (use only when needed)
WithHeader(header string) Option      // Set file header comment
WithIDType(t string) Option           // Set default ID type ("int", "int64", "string", "uuid")
WithIDTypeInfo(info *field.TypeInfo)  // Set ID type with TypeInfo struct
WithPackage(pkg string) Option        // Override package path (rarely needed)
WithSchema(schema string) Option      // Set schema package path
WithTarget(dir string) Option         // Override output directory (rarely needed)
WithFeatures(features ...Feature)     // Enable features
WithStorage(storage *Storage) Option  // Set storage configuration
WithHooks(hooks ...Hook) Option       // Add generation hooks
WithTemplates(templates ...*Template) // Add custom templates
WithAnnotations(annotations Annotations) // Set global annotations
WithBuildFlags(flags ...string)       // Set build flags

// Usage example - only specify options you need
config, err := gen.NewConfig(
    gen.WithFeatures(gen.FeaturePrivacy, gen.FeatureIntercept),
)
```

**Features System:**
Velox supports optional features that can be enabled in the schema configuration:
```go
var AllFeatures = []Feature{
    FeaturePrivacy,            // ORM-level authorization policies
    FeatureIntercept,          // Query interceptors
    FeatureEntQL,              // Runtime query language
    FeatureNamedEdges,         // Named edge loading
    FeatureBidiEdgeRefs,       // Bidirectional edge references
    FeatureSnapshot,           // Schema snapshot for migrations
    FeatureSchemaConfig,       // Multi-schema support
    FeatureLock,               // SQL row-level locking
    FeatureModifier,           // Query modifiers
    FeatureExecQuery,          // Raw SQL execution
    FeatureUpsert,             // ON CONFLICT support
    FeatureVersionedMigration, // Versioned migrations
    FeatureGlobalID,           // Relay Global ID
    FeatureAutoDefault,        // Auto DB DEFAULT for Optional() fields
}
```

**Generated Output Structure:**
```
{target}/
├── velox.go                    # Base types, errors, Op enum, Value interface
├── client.go                   # Client struct, entity clients, hooks registration
├── tx.go                       # Transaction (Tx, Commit, Rollback)
├── runtime/
│   └── runtime.go              # Runtime utilities, schema descriptors
├── predicate/
│   └── predicate.go            # Predicate type definitions
├── internal/                   # (Optional features)
│   ├── schema.go               # Schema snapshot
│   └── schemaconfig.go         # Multi-schema config
├── intercept/
│   └── intercept.go            # Interceptor helpers
├── privacy/
│   └── privacy.go              # Privacy policy helpers
├── migrate/
│   └── migrate.go              # Migration support
│
├── {entity}.go                 # Entity struct + Edges + Client
├── {entity}_create.go          # Create/CreateBulk builders
├── {entity}_update.go          # Update/UpdateOne builders
├── {entity}_delete.go          # Delete/DeleteOne builders
├── {entity}_query.go           # Query builder
├── {entity}_mutation.go        # Mutation type
├── {entity}_filter.go          # Privacy filter (when privacy feature enabled)
│
└── {entity}/
    ├── {entity}.go             # Table name, columns, field descriptors
    └── where.go                # WHERE predicate functions
```

#### SQL Dialect (`dialect/`)
SQL building primitives and database abstraction:
- `dialect/dialect.go` - `Dialect` interface for PostgreSQL, MySQL, SQLite
- `dialect/driver.go` - Database driver abstraction
- `dialect/sql/` - Query builders:
  - `builder.go` - All SQL builders (`Selector`, `InsertBuilder`, `UpdateBuilder`, `DeleteBuilder`, `Predicate`, joins, locking)
  - `driver.go` - SQL driver implementation with connection pooling
  - `scan.go` - Row scanning utilities
  - `sql.go` - Common SQL utilities and helpers
- `dialect/sql/sqlgraph/` - Graph traversal for eager loading and joins
- `dialect/sql/schema/` - Schema migration utilities

#### Privacy (`privacy/`)
ORM-level authorization layer:
- `privacy.go` - Policy types (MutationPolicy, QueryPolicy)
- `rules.go` - Rule implementations (Allow, Deny, Skip, AlwaysAllowRule)
- `combinators.go` - Rule combinators (And, Or, Not, Chain)
- `context.go` - Viewer interface and context utilities
- `errors.go` - DenyError, AuthorizationError types

#### GraphQL Extension (`contrib/graphql/`) - Optional
**Optional extension** for GraphQL schema and resolver generation. Not imported by core packages - users opt-in by importing `contrib/graphql`.

```go
// User opts-in by importing in their generate.go
import "github.com/syssam/velox/contrib/graphql"

// Then calling the generator
graphql.Generate(graph, graphql.Config{...})
```

**Files:**
- `generator.go` - Main generator orchestrating GraphQL code generation
- `collection.go` - Collection query generation with filtering/pagination
- `pagination.go` - Relay-style cursor pagination
- `where_input.go` - WhereInput type generation for filtering
- `mutation_input.go` - Mutation input type generation
- `node.go` - Node interface implementation for Relay
- `edge.go` - Edge resolver generation
- `transaction.go` - Transaction middleware for mutations
- `helpers.go` - SkipMode, PaginationNames, OrderTerm helpers

**Generated GraphQL Output:**
```graphql
type User implements Node {
  id: ID!
  name: String!
  email: String!
  posts: [Post!]!
}

input UserWhereInput {
  id: ID
  idNEQ: ID
  name: String
  nameContains: String
  and: [UserWhereInput!]
  or: [UserWhereInput!]
  not: UserWhereInput
}

type UserConnection {
  edges: [UserEdge!]!
  pageInfo: PageInfo!
  totalCount: Int!
}
```

## Generated Code Architecture

The generated code provides a complete type-safe ORM layer. Understanding its structure helps when debugging or extending functionality.

### Code Size Metrics

| Schema Size | Generated Lines | Files |
|-------------|-----------------|-------|
| 1 entity | ~2,900 | 10 |
| 3 entities | ~8,300 | 25 |

### Entity Structure (`{entity}.go`)

```go
type User struct {
    config       `json:"-"`              // Embedded config (driver, hooks)
    ID           int    `json:"id"`      // Primary key
    Age          int    `json:"age"`     // Fields
    Name         string `json:"name"`
    selectValues sql.SelectValues        // Dynamic SELECT storage
    Edges        UserEdges               // Relationship container
}
```

**Key Methods:**

| Method | Purpose |
|--------|---------|
| `scanValues()` | Map database columns to Go types |
| `assignValues()` | Assign scanned values to struct fields |
| `Value(name)` | Get dynamically selected column values |
| `QueryCars()` | Convenience method for relationship queries |

### Edge/Relationship Container

```go
type UserEdges struct {
    Cars         []*Car `json:"cars,omitempty"`  // O2M relationship
    loadedCars   bool                            // Prevents silent nil on unloaded
    Groups       []*Group
    loadedGroups bool
}

// Safe access with error on unloaded edge
func (e UserEdges) CarsOrErr() ([]*Car, error) {
    if e.loadedCars {
        return e.Cars, nil
    }
    return nil, &NotLoadedError{edge: "cars"}
}
```

### Builder Patterns

**Create Builder:**
```go
client.User.Create().
    SetAge(25).
    SetName("Alice").
    AddCarIDs(1, 2, 3).
    Save(ctx)

// Internal flow:
// 1. defaults() - Set default values
// 2. check() - Validate required fields + run validators
// 3. createSpec() - Build sqlgraph.CreateSpec
// 4. sqlgraph.CreateNode() - Execute SQL INSERT
```

**Query Builder:**
```go
type UserQuery struct {
    predicates []predicate.User
    withCars   *CarQuery      // Eager-load configuration
    order      []user.OrderOption
    modifiers  []func(*sql.Selector)
}

// Execution methods
All(ctx)    → []*User, error    // SELECT * FROM users
First(ctx)  → *User, error      // LIMIT 1
Only(ctx)   → *User, error      // Exactly 1 row or error
Count(ctx)  → int, error        // SELECT COUNT(*)
Exist(ctx)  → bool, error       // SELECT 1 LIMIT 1
```

**Update Builder:**
```go
// Conditional update
client.User.Update().
    Where(user.AgeGT(18)).
    SetName("Adult").
    Exec(ctx)

// Single entity update
client.User.UpdateOne(existingUser).
    SetAge(26).
    AddAge(1).      // Atomic increment
    Save(ctx)
```

### Mutation Tracking (`{entity}_mutation.go`)

```go
type UserMutation struct {
    op            Op              // OpCreate, OpUpdate, OpDelete
    age           *int            // New value
    addage        *int            // Increment value
    clearedFields map[string]struct{}
    cars          map[int]struct{}       // Edge IDs to add
    removedcars   map[int]struct{}       // Edge IDs to remove
    clearedcars   bool                   // Clear all
    oldValue      func(context.Context) (*User, error) // Lazy load old values
}

// Get old value before update
func (m *UserMutation) OldAge(ctx context.Context) (int, error) { ... }
```

### Predicate System (`{entity}/where.go`)

```go
// Type-safe predicate type
type predicate.User func(*sql.Selector)

// Comparison operations
func AgeEQ(v int) predicate.User { return predicate.User(sql.FieldEQ(FieldAge, v)) }
func AgeGT(v int) predicate.User { return predicate.User(sql.FieldGT(FieldAge, v)) }
func AgeIn(vs ...int) predicate.User { return predicate.User(sql.FieldIn(FieldAge, vs...)) }

// Relationship predicates
func HasCars() predicate.User { ... }
func HasCarsWith(preds ...predicate.Car) predicate.User { ... }

// Compile-time type safety prevents misuse
client.User.Query().Where(user.AgeGT(18))     // ✓ Correct
client.User.Query().Where(car.ModelEQ("BMW")) // ✗ Compile error
```

### Error Types (`errors.go`)

```go
// Sentinel errors
var (
    ErrNotFound    = errors.New("velox: entity not found")
    ErrNotSingular = errors.New("velox: entity not singular")
    ErrTxStarted   = errors.New("velox: cannot start a transaction within a transaction")
)

// Query errors with context
type NotFoundError struct { label string; id any }      // Includes ID searched for
type NotSingularError struct { label string; count int } // Includes result count
type NotLoadedError struct { edge string }               // Edge not eager-loaded
type QueryError struct { Entity, Op string; Err error }  // Wraps query errors

// Mutation errors
type MutationError struct { Entity, Op string; Err error }
type ValidationError struct { Name string; Err error }
type ConstraintError struct { msg string; wrap error }

// Privacy errors
type PrivacyError struct { Entity, Op, Rule string }

// Aggregate errors
type AggregateError struct { Errors []error }

// Constructors with context
NewNotFoundErrorWithID("User", 123)          // "velox: User not found (id=123)"
NewNotSingularErrorWithCount("User", 5)      // "velox: User not singular (got 5 results, expected 1)"
NewQueryError("User", "select", err)         // "velox: querying User (select): ..."
NewMutationError("User", "create", err)      // "velox: create User: ..."
NewPrivacyError("User", "query", "IsOwner")  // "velox: privacy denied query on User (rule: IsOwner)"

// Check functions
func IsNotFound(err error) bool
func IsNotSingular(err error) bool
func IsConstraintError(err error) bool
func IsValidationError(err error) bool
func IsQueryError(err error) bool
func IsMutationError(err error) bool
func IsPrivacyError(err error) bool
```

### Generics Usage (`velox.go`)

The generated code uses generics for common infrastructure:

```go
// Hook execution with type safety
func withHooks[V Value, M any, PM interface{*M; Mutation}](
    ctx context.Context,
    exec func(context.Context) (V, error),
    mutation PM,
    hooks []Hook,
) (V, error)

// Query execution
func querierAll[V Value, Q interface{sqlAll(...) (V, error)}]() Querier
func querierCount[Q interface{sqlCount(...) (int, error)}]() Querier
func withInterceptors[V Value](...) (V, error)
```

**What Cannot Be Generic:**
- Field setters (`SetName`, `SetAge`) - field names are compile-time specific
- SQL spec builders (`createSpec`) - entity-specific field mapping
- Validation logic (`check`) - entity-specific rules

### Runtime Initialization (`runtime.go`)

Validators and defaults are injected at module init time:

```go
func init() {
    userFields := schema.User{}.Fields()

    // Inject validators
    userDescAge := userFields[0].Descriptor()
    user.AgeValidator = userDescAge.Validators[0].(func(int) error)

    // Inject defaults
    userDescName := userFields[1].Descriptor()
    user.DefaultName = userDescName.Default.(string)
}
```

**Benefits:**
- Validators parsed once at init, not per-use
- Avoids circular dependencies
- Performance optimization

### Performance Characteristics

| Feature | Implementation |
|---------|----------------|
| Streaming writes | Code generation writes directly to disk |
| Pre-allocation | `make([]any, len(columns))` in `scanValues()` |
| Lazy loading | `oldValue` only executes when `OldXxx()` called |
| Zero reflection | Compile-time type checking |
| Projection | `.Select()` limits loaded columns |
| Eager-loading | `.WithCars()` loads relationships in single query |

### Hooks and Interceptors

```go
// Mutation hooks (Create/Update/Delete)
client.User.Use(func(next Mutator) Mutator {
    return MutateFunc(func(ctx context.Context, m Mutation) (Value, error) {
        slog.Info("mutation", "op", m.Op(), "type", m.Type())
        result, err := next.Mutate(ctx, m)
        if err == nil {
            slog.Info("mutation success", "op", m.Op())
        }
        return result, err
    })
})

// Query interceptors
client.User.Intercept(func(next Querier) Querier {
    return QuerierFunc(func(ctx context.Context, q Query) (Value, error) {
        // Can implement caching, logging, etc.
        return next.Query(ctx, q)
    })
})
```

---

## Schema Annotations

### Entity-Level GraphQL Annotations

**Note:** Mutations follow an **opt-in pattern** (like Ent) - they are NOT generated by default. You must explicitly enable them using `graphql.Mutations()`.

```go
func (User) Annotations() []velox.Annotation {
    return []velox.Annotation{
        graphql.RelayConnection(),              // Enable Relay connections
        graphql.QueryField(),                   // Include in Query type
        graphql.Type("Member"),                 // Custom GraphQL type name
        graphql.Mutations(                      // Enable mutations (opt-in)
            graphql.MutationCreate(),           // Generate createUser mutation + CreateUserInput
            graphql.MutationUpdate(),           // Generate updateUser mutation + UpdateUserInput
            graphql.MutationDelete(),           // Generate deleteUser mutation (Velox extension)
        ),
        graphql.Skip(graphql.SkipWhereInput),   // Skip specific features
        graphql.Directives(                     // Add GraphQL directives
            graphql.Directive{Name: "deprecated", Args: map[string]any{"reason": "Use Member"}},
        ),
    }
}
```

**Mutation Examples (Ent-compatible):**
```go
// Create + Update (Ent default when Mutations() called with no args)
graphql.Mutations()

// Create + Update (explicit, same as above)
graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate())

// Create only (immutable entities)
graphql.Mutations(graphql.MutationCreate())

// Full CRUD with delete (Velox extension - Ent doesn't have MutationDelete)
graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate(), graphql.MutationDelete())

// Read-only (no mutations) - simply omit the Mutations annotation
```

### Field-Level Annotations

```go
func (User) Fields() []velox.Field {
    return []velox.Field{
        field.String("email").Unique().
            Annotations(
                graphql.OrderField("EMAIL"),     // Custom order field name
                graphql.Skip(graphql.SkipAll),   // Exclude from GraphQL
            ),
        field.String("data").
            Annotations(
                sqlschema.ColumnType("JSONB"),         // Custom SQL column type
                sqlschema.Check("length(data) > 0"),   // CHECK constraint
            ),
        // Ent-style struct literal (alternative syntax)
        field.String("code").
            Annotations(sqlschema.Annotation{
                Size:       10,
                ColumnType: "CHAR(10)",
                Charset:    "ascii",
            }),

        // Enum with automatic GraphQL uppercase (default behavior)
        // DB: "big", "small" → GraphQL: BIG, SMALL (auto-uppercase)
        field.Enum("size").Values("big", "small"),

        // Custom GraphQL enum values (override auto-uppercase)
        field.Enum("status").
            NamedValues(
                "InProgress", "IN_PROGRESS",  // Go: InProgress, DB: IN_PROGRESS
                "Completed", "COMPLETED",
            ).
            Default("IN_PROGRESS").
            Annotations(
                graphql.EnumValues(map[string]string{
                    "IN_PROGRESS": "inProgress",  // GraphQL: inProgress (custom)
                    "COMPLETED":   "completed",   // GraphQL: completed (custom)
                }),
            ),
    }
}
```

### Edge-Level Annotations

```go
func (User) Edges() []velox.Edge {
    return []velox.Edge{
        edge.To("posts", Post.Type).  // O2M by default
            Annotations(
                sqlschema.OnDelete(sqlschema.Cascade),       // CASCADE on delete
            ),
    }
}
```

### Index-Level Annotations

```go
func (User) Indexes() []velox.Index {
    return []velox.Index{
        // Basic composite index
        index.Fields("email", "status"),

        // Index with custom type (PostgreSQL GIN for array/JSON)
        index.Fields("tags").Annotations(sqlschema.IndexType("GIN")),

        // Descending order index (for reverse chronological queries)
        index.Fields("created_at").Annotations(sqlschema.Desc()),

        // Partial index with WHERE clause
        index.Fields("status").Annotations(
            &sqlschema.IndexAnnotation{Where: "status = 'active'"},
        ),

        // Index with storage parameters
        index.Fields("id").Annotations(
            sqlschema.IndexType("BTREE"),
            sqlschema.StorageParams("fillfactor=90"),
        ),
    }
}
```

## Schema Patterns

Entity schema definition pattern:
```go
import "github.com/syssam/velox/contrib/mixin" // For common mixins (Time, ID, etc.)

type User struct{ velox.Schema }

func (User) Mixin() []velox.Mixin { return []velox.Mixin{mixin.ID{}, mixin.Time{}} }
func (User) Fields() []velox.Field { return []velox.Field{field.String("email").Unique()} }
func (User) Edges() []velox.Edge { return []velox.Edge{edge.To("posts", Post.Type)} }  // O2M default
func (User) Indexes() []velox.Index { return []velox.Index{index.Fields("email", "status")} }
```

## Field Nullability (Optional vs Nillable)

Velox separates API input requirements from database nullability, following Ent ORM patterns:

### Methods

| Method | API Input | Database | Go Type | Use Case |
|--------|-----------|----------|---------|----------|
| `Optional()` | Not required | NOT NULL | Value type | Standard types use zero value |
| `Nillable()` | Not required | NULL | Pointer type | Nullable database column |
| `Nullable()` | *(deprecated)* | NULL | Pointer type | Use `Nillable()` instead |

### Design Principles

- **`Optional()`** - Field is not required in API/GraphQL input. Database column is NOT NULL. Standard Go types (string, int, bool, float, time, bytes, UUID) use their zero values automatically at **ORM layer only**. Enums and custom types require explicit `Default()`.
- **`Nillable()`** - Database column allows NULL. Go struct uses pointer type (`*string`, `*int64`). Field is optional in input. Generates `Clear{Field}()` method.
- **`Nullable()`** - Deprecated alias for `Nillable()`. Kept for backward compatibility.

**Standard Go types with well-defined zero values:**
- `string` → `""`
- `int`, `int64`, etc. → `0`
- `bool` → `false`
- `float64` → `0.0`
- `time.Time` → zero time
- `[]byte` → `[]byte{}`
- `UUID` → `uuid.Nil`

**Types requiring explicit `Default()`:** Enums, JSON with custom types, Other (custom types).

### Important: `Optional()` Does NOT Add Database DEFAULT

`Optional()` handles zero values at the **ORM layer only**, not at the database layer. The migration will generate `NOT NULL` without a `DEFAULT` clause:

```go
// This field
field.String("nickname").Optional()

// Generates migration SQL:
// nickname VARCHAR(255) NOT NULL
// (no DEFAULT clause!)
```

**Why this matters:**
1. **Adding columns to existing tables may fail** - `ALTER TABLE ADD COLUMN ... NOT NULL` without DEFAULT fails if table has data
2. **Database schema doesn't reflect ORM behavior** - DB shows no default, but ORM inserts zero value
3. **`Optional()` is about API validation, not database constraints** - It means "API input not required", not "DB should have default"

**If you need database-level DEFAULT, explicitly declare it:**

```go
// Option 1: ORM + DB default (recommended)
field.String("nickname").Optional().Default("")

// Option 2: DB-only default (for migrations)
field.String("nickname").Optional().Annotations(sqlschema.Default("''"))

// Option 3: Allow NULL instead
field.String("nickname").Nillable()
```

**Best practice:** Always use `.Default()` with `Optional()` for fields that may be added to existing tables.

### FeatureAutoDefault (Big Tech Best Practice)

Enable `FeatureAutoDefault` to automatically add database DEFAULT values for **ALL NOT NULL fields** (both Required and Optional). This follows big tech best practices where:
- **DB DEFAULT** = migration safety (database layer)
- **Application validation** = enforce required fields (ORM/API layer)

```go
// Enable in your generate.go
config, err := gen.NewConfig(
    gen.WithFeatures(gen.FeatureAutoDefault),
)
```

**With FeatureAutoDefault enabled:**

| Field Type | Auto DEFAULT | Zero Value |
|------------|--------------|------------|
| `String` | ✅ Yes | `''` (empty string) |
| `Int`, `Int8`, `Int16`, `Int32`, `Int64` | ✅ Yes | `0` |
| `Uint`, `Uint8`, `Uint16`, `Uint32`, `Uint64` | ✅ Yes | `0` |
| `Float32`, `Float64` | ✅ Yes | `0` |
| `Bool` | ✅ Yes | `false` |
| `Enum` | ❌ No | Requires explicit `Default()` |
| `JSON` | ❌ No | Requires explicit default |
| `Time` | ❌ No | Use `DefaultExpr("CURRENT_TIMESTAMP")` |
| `UUID` | ❌ No | Use `DefaultExpr("gen_random_uuid()")` |
| `Bytes` | ❌ No | Requires explicit default |
| `Nillable` fields | ❌ No | `NULL` allowed, no DEFAULT needed |

**Example:**
```go
// With FeatureAutoDefault enabled:
field.String("email").Unique()        // → NOT NULL DEFAULT ''
field.Int("age")                      // → NOT NULL DEFAULT 0
field.Bool("active")                  // → NOT NULL DEFAULT false
field.Time("created_at")              // → NOT NULL, NO DEFAULT (use DefaultExpr!)
field.String("bio").Nillable()        // → NULL (no DEFAULT needed)
```

**Why this is safe:**
- Required fields still require values at the ORM/API layer (validation unchanged)
- DB DEFAULT only provides migration safety for `ALTER TABLE ADD COLUMN`
- Application will never actually use the DEFAULT (ORM enforces required fields)

### Usage Examples

```go
func (User) Fields() []velox.Field {
    return []velox.Field{
        // Required field (default behavior)
        field.String("email").Unique(),

        // Optional with explicit default: API can omit, DB stores "user"
        field.String("role").Optional().Default("user"),

        // Optional standard type: API can omit, DB stores "" (Go zero value)
        field.String("nickname").Optional(),
        field.Int("age").Optional(),        // defaults to 0
        field.Bool("active").Optional(),    // defaults to false

        // ERROR: Enum requires explicit Default()
        // field.Enum("status").Values("pending", "active").Optional()  // Validation error!
        field.Enum("status").Values("pending", "active").Optional().Default("pending"),  // OK

        // Nillable: DB allows NULL, Go uses *string, has ClearDisplayName()
        field.String("display_name").Nillable(),

        // Both: API can omit, DB allows NULL, has ClearBio()
        field.String("bio").Optional().Nillable(),
    }
}
```

### Generated Code Behavior

| Configuration | GraphQL Input | DB Column | DB DEFAULT | Go Type | ORM Default |
|---------------|---------------|-----------|------------|---------|-------------|
| (default) | `field: String!` | NOT NULL | ❌ None | `string` | Must be set |
| `Optional()` (standard type) | `field: String` | NOT NULL | ❌ None | `string` | Go zero value (ORM only) |
| `Optional()` (enum/custom) | ❌ ERROR | - | - | - | Requires `Default()` |
| `Optional().Default("x")` | `field: String` | NOT NULL | ✅ `'x'` | `string` | `"x"` |
| `Nillable()` | `field: String` | NULL | ❌ None | `*string` | `nil` (NULL) |
| `Optional().Nillable()` | `field: String` | NULL | ❌ None | `*string` | `nil` (NULL) |

**Note:** Only `Optional().Default()` adds a `DEFAULT` clause to the database schema. Plain `Optional()` handles defaults at ORM layer only.

## Field Immutability and Update Defaults

Velox provides options to control field behavior during create and update operations.

### Immutable Fields

Use `Immutable()` for fields that should only be set during creation and cannot be modified afterwards:

```go
func (User) Fields() []velox.Field {
    return []velox.Field{
        // Cannot be changed after creation
        field.String("username").Unique().Immutable(),

        // Reference ID that shouldn't change
        field.Int64("tenant_id").Immutable(),
    }
}
```

**Effects of `Immutable()`:**
- Field excluded from `UpdateXXXInput` in GraphQL
- No `SetXxx()` method generated on update builders
- Field still appears in create inputs and query results

### Function Defaults (Ent-style ergonomic API)

`Default()` and `UpdateDefault()` accept both literal values and function references:

```go
import "time"
import "github.com/google/uuid"

func (User) Fields() []velox.Field {
    return []velox.Field{
        // String literal default
        field.String("status").Default("active"),

        // Function reference - calls time.Now() when creating
        field.Time("created_at").Default(time.Now).Immutable(),

        // Function reference - calls time.Now() on every update
        field.Time("updated_at").UpdateDefault(time.Now),

        // Function reference - calls uuid.New() when creating
        field.UUID("id", uuid.UUID{}).Default(uuid.New),

        // Integer literal default
        field.Int64("count").Default(0),
    }
}
```

The API automatically detects whether the argument is a function or a literal value using reflection.

### SQL-Level Defaults (sqlschema.Default/sqlschema.DefaultExpr)

Use SQL annotations for database-level defaults that work in migrations:

```go
import "time"
import "github.com/google/uuid"
import "github.com/syssam/velox/dialect/sqlschema"

func (User) Fields() []velox.Field {
    return []velox.Field{
        // Go calls time.Now(), SQL uses CURRENT_TIMESTAMP
        field.Time("created_at").
            Default(time.Now).
            Annotations(sqlschema.Default("CURRENT_TIMESTAMP")),

        // SQL expression default
        field.UUID("id", uuid.UUID{}).
            Default(uuid.New).
            Annotations(sqlschema.DefaultExpr("gen_random_uuid()")),

        // SQL-only default (no Go default)
        field.String("status").
            Annotations(sqlschema.Default("'pending'")),

        // Computed default using other columns
        field.String("slug").
            Annotations(sqlschema.DefaultExpr("lower(title)")),
    }
}
```

### Default Precedence

For migrations, SQL defaults are checked in this order:
1. `sqlschema.DefaultExpr()` - SQL expression (highest priority)
2. `sqlschema.Default()` - SQL literal value
3. `Default()` literal - Fallback

| Method | Go Code | SQL Migration | Use Case |
|--------|---------|---------------|----------|
| `Default(literal)` | Uses value literally | Uses value | Simple literals |
| `Default(func)` | Calls function | No effect | Runtime values (time.Now, uuid.New) |
| `sqlschema.Default(v)` | No effect | Uses `v` | DB-level defaults |
| `sqlschema.DefaultExpr(expr)` | No effect | Uses `expr` | DB functions |
| `UpdateDefault(literal)` | Uses value on update | No effect | Update values |
| `UpdateDefault(func)` | Calls function on update | No effect | Runtime updates |
| `Immutable()` | Normal | Normal | Write-once fields |

## Field Validation

Velox provides two approaches for validation:

### Built-in Validators (ORM Layer)

**Requires `gen.FeatureValidator`** - ORM validators are opt-in to reduce generated code size.

```go
// Enable ORM validators in your gen.go
config, err := gen.NewConfig(
    gen.WithFeatures(gen.FeatureValidator),  // Enable ORM-level validators
)
if err != nil {
    return err
}
graph, err := gen.NewGraph(config, schemas...)
```

Use type-specific builder methods for schema-level constraints:

```go
func (User) Fields() []velox.Field {
    return []velox.Field{
        // String constraints
        field.String("name").NotEmpty().MaxLen(100).MinLen(2),
        field.String("email").Match(emailRegex),

        // Numeric constraints
        field.Int64("age").NonNegative().Max(150),
        field.Int64("rating").Range(1, 5),
        field.Int64("quantity").Positive(),
        field.Float64("price").NonNegative(),

        // Enum validation is automatic
        field.Enum("status").Values("pending", "active", "suspended"),
    }
}
```

| Method | Type | Description |
|--------|------|-------------|
| `NotEmpty()` | String | Must not be empty string |
| `MinLen(n)` | String | Minimum length |
| `MaxLen(n)` | String | Maximum length |
| `Match(regex)` | String | Must match pattern |
| `Positive()` | Int/Float | Must be > 0 |
| `NonNegative()` | Int/Float | Must be >= 0 |
| `Negative()` | Int/Float | Must be < 0 |
| `Min(n)` | Int/Float | Minimum value |
| `Max(n)` | Int/Float | Maximum value |
| `Range(min, max)` | Int/Float | Value between min and max |

### GraphQL Input Validation (API Layer)

For GraphQL mutation input validation, use annotations with go-playground/validator syntax.
Validation tags are applied to generated `CreateXXXInput` and `UpdateXXXInput` structs:

```go
import "github.com/syssam/velox/contrib/graphql"

func (User) Fields() []velox.Field {
    return []velox.Field{
        field.String("email").
            NotEmpty().MaxLen(255).  // ORM-level constraint
            Annotations(
                graphql.CreateInputValidate("required,email"),
                graphql.UpdateInputValidate("omitempty,email"),
            ),

        field.String("password").
            Annotations(
                graphql.CreateInputValidate("required,min=8,max=72"),
                graphql.UpdateInputValidate("omitempty,min=8,max=72"),
            ),

        field.Int64("age").
            NonNegative().Max(150).  // ORM-level constraint
            Annotations(
                graphql.CreateInputValidate("required,gte=0,lte=150"),
            ),

        // Convenience function for both create and update
        field.String("username").
            Annotations(
                graphql.MutationInputValidate(
                    "required,alphanum,min=3,max=30",  // CreateInput
                    "omitempty,alphanum,min=3,max=30", // UpdateInput
                ),
            ),
    }
}
```

Common validator tags (go-playground/validator):
- `required` - Field must be present
- `omitempty` - Skip validation if empty (for updates)
- `email` - Valid email format
- `url` - Valid URL format
- `min=n`, `max=n` - String length or numeric bounds
- `gte=n`, `lte=n` - Greater/less than or equal
- `alphanum` - Alphanumeric only
- `oneof=a b c` - Must be one of listed values

### Validation Design Principles

**ORM-level validators** (`NotEmpty()`, `MaxLen()`, etc.):
- Requires `gen.FeatureValidator` to generate validation code
- Applied at the database/ORM layer during create/update operations
- Self-documenting in schema definitions
- Validated by generated builders before save

**GraphQL input validators** (`graphql.CreateInputValidate()`, etc.):
- Always available (no feature flag required)
- Applied to generated GraphQL input structs as struct tags
- Validated at the API layer before reaching the ORM
- Can have different rules for create vs update operations

**Recommendation:**
- For API-first applications: Use only GraphQL annotations (skip `FeatureValidator`)
- For ORM-heavy applications: Enable `FeatureValidator` for database-level guarantees
- You can use both: GraphQL validates at API boundary, ORM validates internally

## Supported Databases

PostgreSQL (primary), MySQL, SQLite — dialect-specific SQL is generated via `dialect/`.

SQLite uses `modernc.org/sqlite` (pure Go, no CGO required). The driver name is `"sqlite"` and DSN uses `_pragma=foreign_keys(1)` syntax.

---

## Velox vs gqlgen: Separation of Concerns

Velox core focuses on the **database/ORM layer**. GraphQL support is provided via the **optional** `contrib/graphql` extension.

| Feature | Velox Core | contrib/graphql (Optional) | gqlgen (Runtime) |
|---------|------------|---------------------------|------------------|
| ORM Code | ✅ Always generated | - | - |
| GraphQL Schema | - | ✅ Generates types, inputs | Reads schema |
| Resolvers | - | ✅ Generates stubs | Runtime execution |
| Subscriptions | - | ❌ Not generated | ✅ Pub/sub runtime |
| Federation | - | ❌ Not generated | ✅ `_entities`, `_service` |
| Directives | - | ❌ Schema only | ✅ Runtime execution |
| DataLoader | - | ❌ Not generated | ✅ Context batching |
| Privacy/Auth | ✅ ORM-level policies | - | Middleware optional |

**Key Principle:**
- **Velox Core** = ORM only (no GraphQL dependency)
- **contrib/graphql** = Optional extension for GraphQL generation
- **gqlgen** = Runtime GraphQL server (handles subscriptions, federation, etc.)

---

## Runtime Features via Hooks/Interceptors

Velox follows Ent's design philosophy: **provide hooks and interceptors, not bundled implementations**. This allows users to integrate their own tracing, caching, and logging solutions.

### Adding Tracing (User-Implemented)

Wrap the database driver or use hooks:

```go
// Option 1: Wrap at driver level with your tracing library
drv := sql.OpenDB(dialect.Postgres, db)
tracedDrv := yourcompany.WrapDriverWithTracing(drv)
client := ent.NewClient(ent.Driver(tracedDrv))

// Option 2: Use hooks for mutation tracing
client.Use(func(next ent.Mutator) ent.Mutator {
    return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
        span, ctx := tracer.Start(ctx, m.Type()+"."+m.Op().String())
        defer span.End()
        return next.Mutate(ctx, m)
    })
})
```

### Adding Caching (User-Implemented)

Use interceptors to add query caching:

```go
// Use interceptors for query caching
client.User.Intercept(func(next ent.Querier) ent.Querier {
    return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
        key := buildCacheKey(q)
        if cached, ok := yourCache.Get(key); ok {
            return cached, nil
        }
        result, err := next.Query(ctx, q)
        if err == nil {
            yourCache.Set(key, result)
        }
        return result, err
    })
})
```

### Why No Built-in Implementations?

1. **Different teams use different infrastructure** (Redis vs Memcached, Jaeger vs Datadog)
2. **Keeps the core library lightweight**
3. **Avoids opinionated vendor lock-in**
4. **Companies often have existing standardized solutions**

### Built-in Query Statistics & Slow Query Detection

Velox provides optional driver wrappers for monitoring:

```go
import "github.com/syssam/velox/dialect/sql"

// Open with statistics collection
drv, stats, err := sql.OpenWithStats("postgres", dsn,
    sql.WithSlowThreshold(100*time.Millisecond),
    sql.WithSlowQueryLog(),  // Log slow queries
)
client := ent.NewClient(ent.Driver(drv))

// Monitor statistics
go func() {
    for range time.Tick(time.Minute) {
        s := stats.Stats()
        slog.Info("query stats",
            "total", s.TotalQueries, "avg", s.AvgQueryDuration(),
            "slow", s.SlowQueries, "errors", s.Errors)
    }
}()

// Custom slow query handler
sql.WithSlowQueryHook(func(ctx context.Context, query string, args []any, duration time.Duration) {
    slog.Warn("slow query detected",
        "query", query,
        "duration", duration,
        "args", args,
    )
})
```

### DataLoader Helpers (`contrib/dataloader/`)

Generic utilities for batch loading with any DataLoader library:

```go
import "github.com/syssam/velox/contrib/dataloader"

// Batch function for users
func userBatchFn(ctx context.Context, ids []int) ([]*ent.User, []error) {
    users, err := client.User.Query().Where(user.IDIn(ids...)).All(ctx)
    if err != nil {
        return nil, []error{err}
    }
    // OrderByKeys ensures results match input order (required by DataLoader)
    return dataloader.OrderByKeys(ids, users, func(u *ent.User) int { return u.ID })
}

// For one-to-many relationships
func postsByUserBatchFn(ctx context.Context, userIDs []int) ([][]*ent.Post, []error) {
    posts, _ := client.Post.Query().Where(post.UserIDIn(userIDs...)).All(ctx)
    grouped := dataloader.GroupByKey(posts, func(p *ent.Post) int { return p.UserID })
    return dataloader.OrderGroupsByKeys(userIDs, grouped), nil
}
```

### Migration Validation (`dialect/sql/schema/`)

Validate schema changes before applying migrations:

```go
import "github.com/syssam/velox/dialect/sql/schema"

// Validate diff between current and desired schema
result := schema.ValidateDiff(currentTables, desiredTables)

if result.HasBreakingChanges() {
    slog.Error("breaking changes detected", "diff", result)
    os.Exit(1)
}

if result.HasWarnings() {
    slog.Warn("schema warnings", "diff", result)
}

// Allow specific changes
result := schema.ValidateDiff(current, desired,
    schema.AllowDropColumn(),
    schema.AllowDropIndex(),
)
```

Detected issues include:
- Dropped tables/columns (breaking)
- Column type changes
- NULL to NOT NULL changes
- Size reductions
- New NOT NULL columns without defaults

---

## Privacy Layer (`privacy/`)

ORM-level authorization that evaluates before queries/mutations reach the database.

```go
import (
    "github.com/syssam/velox/privacy"
    "github.com/syssam/velox/schema/policy"
)

// Define policy in schema
func (User) Policy() velox.Policy {
    return policy.Policy(
        policy.Mutation(
            privacy.DenyIfNoViewer(),           // Require authentication
            privacy.HasRole("admin"),           // Allow admins
            privacy.IsOwner("user_id"),         // Allow owners
            privacy.AlwaysDenyRule(),           // Deny by default
        ),
        policy.Query(
            privacy.AlwaysAllowQueryRule(),     // Allow all queries
        ),
    )
}

// Use viewer context at runtime
ctx := privacy.WithViewer(ctx, &privacy.SimpleViewer{
    UserID: "user-123",
    Roles:  []string{"user"},
})
```

**Available Rules:**
- `DenyIfNoViewer()` - Require authenticated viewer
- `HasRole(role)`, `HasAnyRole(roles...)` - Role-based access
- `IsOwner(field)` - Owner-based access (checks if viewer owns the entity)
- `TenantRule(field)` - Multi-tenant isolation
- `AlwaysAllowRule()`, `AlwaysDenyRule()` - Catch-all rules
- `And(rules...)`, `Or(rules...)`, `Not(rule)` - Combinators

### Privacy Filter API

The core `github.com/syssam/velox/privacy` package provides filter types that can be imported **without running code generation first**. This solves the circular dependency problem when schema files need to import privacy rules.

**Core types (import from `github.com/syssam/velox/privacy`):**
```go
// Filter is the interface for filtering queries/mutations
type Filter interface {
    WhereP(...func(*sql.Selector))
}

// Filterable is implemented by queries/mutations that support filtering
type Filterable interface {
    Filter() Filter
}

// FilterFunc creates a QueryMutationRule from a filter function
type FilterFunc func(context.Context, Filter) error
```

**Generated code:**
- `Query.Filter()` and `Mutation.Filter()` return `privacy.Filter` interface
- This allows queries/mutations to implement `privacy.Filterable`
- Core `FilterFunc` works without entity-specific type switches

**Generated filter types (entity-specific):**
```go
// Generated for each entity - implements privacy.Filter
type UserFilter struct {
    config
    predicates *[]predicate.User
}

// WhereP appends storage-level predicates using raw sql.Selector functions.
func (f *UserFilter) WhereP(ps ...func(*sql.Selector))

// Where appends type-safe predicates to the filter.
func (f *UserFilter) Where(ps ...predicate.User)

// HasColumn reports whether the entity has the given column name.
func (f *UserFilter) HasColumn(column string) bool
```

**Design Principles:**
- Core types in `github.com/syssam/velox/privacy` - no code generation needed
- Generated `Filter()` returns interface, not concrete type - enables `Filterable` implementation
- Minimal API - only 3 methods per filter (no verbose `Where{Field}EQ`, `Where{Field}NEQ`, etc.)
- No circular dependencies - privacy rules can be written before code generation

### Multi-Tenant Filtering

For multi-tenant isolation that works across multiple entities dynamically.

**Your rule package can import from core - no circular dependency:**

```go
package rule

import (
    "context"

    "github.com/syssam/velox/privacy"      // Core package - always available
    "github.com/syssam/velox/dialect/sql"
)

// ColumnChecker is defined at point of use (Google/Uber Go style)
type ColumnChecker interface {
    HasColumn(column string) bool
}

// FilterWorkspaceRule applies workspace isolation to entities that have workspace_id column.
func FilterWorkspaceRule(workspaceID string) privacy.QueryMutationRule {
    return privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
        // Check if filter supports column checking
        cc, ok := f.(ColumnChecker)
        if !ok || !cc.HasColumn("workspace_id") {
            return privacy.Skip  // Entity doesn't have this field
        }
        // Apply the filter
        f.WhereP(func(s *sql.Selector) {
            s.Where(sql.EQ(s.C("workspace_id"), workspaceID))
        })
        return privacy.Skip
    })
}
```

**Alternative: Using Type-Safe Predicates**

For compile-time safety when you know the entity type:

```go
func FilterUserWorkspace(workspaceID string) privacy.QueryMutationRule {
    return privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
        uf, ok := f.(*velox.UserFilter)
        if !ok {
            return privacy.Skip
        }
        // Use type-safe predicates
        uf.Where(user.WorkspaceIDField.EQ(workspaceID))
        return privacy.Skip
    })
}
```

---

# Go Best Practices & Style Guide

This project follows Google Go Style Guide, Uber Go Style Guide, and official Go guidelines.

## Naming Conventions

### Package Names (Google/Uber)
- Use short, lowercase, single-word names: `field`, `edge`, `schema`
- Avoid underscores, hyphens, or mixedCaps: `fieldbuilder` not `field_builder`
- Package name should not repeat in exported names: `field.String()` not `field.FieldString()`
- Avoid generic names like `util`, `common`, `misc`, `helpers`

### Variable Names
```go
// GOOD: Short names for short scopes
for i, v := range values { }
if err := doThing(); err != nil { }

// GOOD: Descriptive names for longer scopes
userRepository := NewUserRepository(db)
connectionTimeout := 30 * time.Second

// BAD: Unnecessarily long names in short scopes
for index, value := range values { }  // Use i, v
```

### Acronyms and Initialisms
```go
// GOOD: Acronyms are all caps or all lowercase
userID, httpClient, xmlParser, urlPath
UserID, HTTPClient, XMLParser, URLPath

// BAD: Mixed case acronyms
UserId, HttpClient, XmlParser, UrlPath
```

### Interface Names
```go
// GOOD: Single-method interfaces use -er suffix
type Reader interface { Read(p []byte) (n int, err error) }
type Stringer interface { String() string }

// GOOD: Multi-method interfaces describe behavior
type ReadWriter interface { ... }
type EntityStore interface { ... }
```

### Receiver Names (Uber)
```go
// GOOD: Short, consistent receiver names (1-2 letters)
func (c *Client) Do() error { }
func (b *StringBuilder) Build() string { }

// BAD: Long or inconsistent receiver names
func (client *Client) Do() error { }
func (this *Client) Do() error { }
func (self *Client) Do() error { }
```

## Error Handling (Google/Uber)

### Error Wrapping
```go
// GOOD: Add context when wrapping errors
if err := store.Get(id); err != nil {
    return fmt.Errorf("get user %d: %w", id, err)
}

// GOOD: Use errors.Is/As for error checking
if errors.Is(err, sql.ErrNoRows) {
    return ErrNotFound
}

// BAD: String matching on errors
if err.Error() == "not found" { }
```

### Custom Error Types
```go
// GOOD: Implement error interface for custom errors
type ValidationError struct {
    Field   string
    Message string
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("validation failed on %s: %s", e.Field, e.Message)
}

// GOOD: Sentinel errors for expected conditions
var (
    ErrNotFound     = errors.New("entity not found")
    ErrUnauthorized = errors.New("unauthorized")
)
```

### Error Handling Patterns
```go
// GOOD: Handle errors immediately
result, err := doSomething()
if err != nil {
    return err
}
// use result

// GOOD: Early returns reduce nesting
func process(data []byte) error {
    if len(data) == 0 {
        return errors.New("empty data")
    }
    if !isValid(data) {
        return errors.New("invalid data")
    }
    return processValid(data)
}

// BAD: Don't ignore errors
_ = file.Close()  // BAD
file.Close()      // BAD - error discarded

// GOOD: Handle or explicitly ignore with comment
if err := file.Close(); err != nil {
    slog.Warn("failed to close file", "error", err)
}
```

## Code Organization (Google/Uber)

### File Structure
```
package/
├── doc.go          # Package documentation
├── types.go        # Type definitions
├── interface.go    # Interface definitions
├── errors.go       # Error types and sentinels
├── feature.go      # Feature implementation
├── feature_test.go # Tests for feature
└── internal/       # Internal implementation details
```

### Import Grouping
```go
import (
    // Standard library
    "context"
    "fmt"
    "time"

    // Third-party packages
    "github.com/google/uuid"

    // Internal packages
    "github.com/syssam/velox/dialect"
    "github.com/syssam/velox/schema"
    "github.com/syssam/velox/schema/field"
    "github.com/syssam/velox/schema/edge"
)
```

### Function Organization
```go
// Order within a file:
// 1. Package constants
// 2. Package variables
// 3. Type definitions
// 4. Constructor functions (New*)
// 5. Public methods (alphabetical)
// 6. Private methods (alphabetical)
// 7. Helper functions
```

## Struct Design (Uber)

### Field Ordering
```go
// GOOD: Group related fields, exported first
type Config struct {
    // Exported fields first
    Host     string
    Port     int
    Timeout  time.Duration

    // Unexported fields last
    logger   *log.Logger
    mu       sync.Mutex
}
```

### Zero Value Usefulness
```go
// GOOD: Zero value is useful
type Buffer struct {
    buf []byte
}

func (b *Buffer) Write(p []byte) {
    b.buf = append(b.buf, p...)  // Works with nil slice
}

// GOOD: Document when zero value is NOT useful
// Client must be created with NewClient. Zero value is not usable.
type Client struct {
    driver dialect.Driver
}
```

### Functional Options Pattern
```go
// GOOD: Use functional options for complex configuration
type Option func(*Client)

func WithTimeout(d time.Duration) Option {
    return func(c *Client) { c.timeout = d }
}

func WithLogger(l *log.Logger) Option {
    return func(c *Client) { c.logger = l }
}

func NewClient(addr string, opts ...Option) *Client {
    c := &Client{addr: addr, timeout: defaultTimeout}
    for _, opt := range opts {
        opt(c)
    }
    return c
}
```

## Concurrency (Google/Uber)

### Goroutine Lifecycle
```go
// GOOD: Always ensure goroutines can exit
func worker(ctx context.Context, jobs <-chan Job) {
    for {
        select {
        case <-ctx.Done():
            return
        case job := <-jobs:
            process(job)
        }
    }
}

// GOOD: Use sync.WaitGroup for goroutine coordination
func processAll(items []Item) {
    var wg sync.WaitGroup
    for _, item := range items {
        wg.Add(1)
        go func(it Item) {
            defer wg.Done()
            process(it)
        }(item)
    }
    wg.Wait()
}
```

### Mutex Usage
```go
// GOOD: Mutex guards the fields below it
type SafeCounter struct {
    mu    sync.Mutex
    count int  // guarded by mu
}

// GOOD: Use defer for unlock
func (c *SafeCounter) Inc() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.count++
}

// GOOD: Prefer RWMutex for read-heavy workloads
type Cache struct {
    mu   sync.RWMutex
    data map[string]string
}

func (c *Cache) Get(key string) (string, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    v, ok := c.data[key]
    return v, ok
}
```

### Channel Patterns
```go
// GOOD: Sender closes the channel
func produce(ch chan<- int) {
    defer close(ch)
    for i := 0; i < 10; i++ {
        ch <- i
    }
}

// GOOD: Use buffered channels to avoid blocking
results := make(chan Result, numWorkers)

// GOOD: Use select with default for non-blocking operations
select {
case msg := <-ch:
    process(msg)
default:
    // Channel empty, do something else
}
```

## Testing (Google/Uber)

### Test Naming
```go
// GOOD: Test function names describe behavior
func TestUserRepository_Create_ValidUser(t *testing.T) { }
func TestUserRepository_Create_DuplicateEmail(t *testing.T) { }

// GOOD: Use table-driven tests
func TestAdd(t *testing.T) {
    tests := []struct {
        name     string
        a, b     int
        expected int
    }{
        {"positive", 1, 2, 3},
        {"negative", -1, -2, -3},
        {"zero", 0, 0, 0},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := Add(tt.a, tt.b); got != tt.expected {
                t.Errorf("Add(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.expected)
            }
        })
    }
}
```

### Test Assertions
```go
// GOOD: Use testify for cleaner assertions (when appropriate)
assert.Equal(t, expected, actual)
assert.NoError(t, err)
require.NotNil(t, obj)  // Fails test immediately

// GOOD: Standard library style
if got != want {
    t.Errorf("Get() = %v, want %v", got, want)
}
```

### Test Helpers
```go
// GOOD: Mark helpers with t.Helper()
func assertNoError(t *testing.T, err error) {
    t.Helper()
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
}

// GOOD: Use cleanup for resource management
func TestWithTempDB(t *testing.T) {
    db := setupTestDB(t)
    t.Cleanup(func() { db.Close() })
    // test code
}
```

## Performance (Uber)

### Avoid Unnecessary Allocations
```go
// GOOD: Preallocate slices when size is known
users := make([]User, 0, len(ids))
for _, id := range ids {
    users = append(users, getUser(id))
}

// GOOD: Use strings.Builder for string concatenation
var b strings.Builder
for _, s := range parts {
    b.WriteString(s)
}
result := b.String()

// BAD: String concatenation in loop
var result string
for _, s := range parts {
    result += s  // Creates new string each iteration
}
```

### Avoid Unnecessary Conversions
```go
// GOOD: Use []byte directly when possible
func process(data []byte) { }

// BAD: Unnecessary string conversion
func process(data string) { }
process(string(byteData))  // Allocates
```

### Prefer strconv Over fmt
```go
// GOOD: strconv is faster for simple conversions
s := strconv.Itoa(42)
i, err := strconv.Atoi("42")

// SLOWER: fmt has more overhead
s := fmt.Sprintf("%d", 42)
```

## Documentation (Google)

### Package Documentation
```go
// Package field provides fluent builders for defining entity fields.
//
// Field names follow database conventions (snake_case), while Go names
// are automatically converted to PascalCase:
//
//     field.Int64("user_id")    // DB: user_id, Go: UserID
//     field.String("email")     // DB: email, Go: Email
//
// # Field Types
//
// The package supports various field types:
//   - String, Text: variable-length strings
//   - Int, Int64: integers
//   - Bool: boolean values
//   - Time: timestamps
//   - UUID: universally unique identifiers
//   - Enum: enumerated values
//   - JSON: JSON data
//   - Custom: user-defined types
package field
```

### Function Documentation
```go
// NewClient creates a new database client with the given connection string.
//
// The client must be closed when no longer needed to release resources.
// Use [Client.Close] to close the connection.
//
// Example:
//
//     client, err := NewClient("postgres://localhost/mydb")
//     if err != nil {
//         slog.Error("creating client", "error", err)
//         os.Exit(1)
//     }
//     defer client.Close()
func NewClient(connStr string) (*Client, error) { }
```

## Go 1.24+ Features

This project requires Go 1.24+ and uses modern Go idioms.

### Use `any` Instead of `interface{}`
```go
// GOOD: Go 1.18+ style
func Process(data any) error { }
func ParseJSON(input []byte) (any, error) { }
var cache = make(map[string]any)

// BAD: Pre-Go 1.18 style
func Process(data interface{}) error { }
```

### Use Octal Literal Prefix (Go 1.13+)
```go
// GOOD: Modern octal syntax
os.WriteFile(path, data, 0o644)
os.MkdirAll(dir, 0o755)

// BAD: Legacy octal syntax
os.WriteFile(path, data, 0644)
```

### Use Standard Library Enhancements
```go
// GOOD: Use slices package
import "slices"
slices.Sort(items)
slices.Contains(items, target)

// GOOD: Use maps package
import "maps"
maps.Clone(m)
maps.Keys(m)

// GOOD: Use slog for structured logging
import "log/slog"
slog.Info("user created", "id", userID, "email", email)
```

### Use Generics Where Appropriate
```go
// GOOD: Generic container types
type Cache[K comparable, V any] struct {
    data map[K]V
}

// GOOD: Generic utility functions
func Filter[T any](items []T, pred func(T) bool) []T {
    var result []T
    for _, item := range items {
        if pred(item) {
            result = append(result, item)
        }
    }
    return result
}
```

## Project-Specific Guidelines

### Code Generation
- Generated files must include `// Code generated by velox. DO NOT EDIT.` header
- Generated code must be valid Go that compiles without errors
- Generated imports must be minimal and sorted
- Generated enum types must include validation methods and sql.Scanner/driver.Valuer

### Schema Design
- All entities must have a primary key field
- Field names use snake_case (database convention)
- Go struct field names are auto-generated as PascalCase
- Enum fields must have at least one value defined
- Relationships must reference valid entity names

### Testing Requirements
- Core packages require comprehensive tests
- New features require corresponding tests
- Table-driven tests preferred for multiple cases
- Use golden file tests for code generation output
- Integration tests for database operations

## CI/CD

GitHub Actions pipeline (`.github/workflows/ci.yml`) runs on push and pull request:
- **test**: `go test -race -cover` with coverage upload to Codecov
- **lint**: `golangci-lint` via official action
- **build**: `CGO_ENABLED=0 go build ./...` (verifies pure Go build)

## Linting Configuration

This project uses golangci-lint with **zero warnings**. Linters enabled:

**Default linters:**
- `errcheck`, `gosimple`, `govet`, `ineffassign`, `staticcheck`, `unused`

**Additional linters (Google/Uber recommended):**
- `gofmt`, `goimports`, `misspell`, `gocritic`, `revive`, `prealloc`, `unconvert`, `unparam`, `whitespace`

**Key exclusions in `.golangci.yml`:**
- `govet shadow` disabled (too many false positives with closures)
- `gocritic`: `ifElseChain`, `dupBranchBody`, `commentedOutCode`, `elseif`, `octalLiteral` disabled
- `compiler/gen/sql/` excluded from `unparam` (consistent interface pattern)
- Privacy sentinels (`Allow`, `Deny`, `Skip`) excluded from `error-naming`
- Test files excluded from `unused-parameter`
