# Architecture

## Overview

Velox follows a pipeline architecture: schema definitions are loaded, validated, and transformed into a typed graph, which is then used to generate database access code.

```
Schema (schema/*.go)
    |
    v
compiler/load (compile + execute schema package)
    |
    v
compiler/gen (Graph construction + validation)
    |
    v
JenniferGenerator (parallel codegen via Jennifer library)
    |
    v
Generated output (per-entity sub-packages + shared files)
```

## Pipeline Stages

### 1. Schema Loading (`compiler/load/`)

The loader uses a **compile-and-execute** strategy:

1. Introspects the schema package using `go/packages` to find exported types implementing `velox.Interface`
2. Generates a temporary `main.go` that calls each schema's `MarshalJSON()`
3. Compiles and executes the binary, capturing JSON output
4. Unmarshals into `[]*load.Schema` structs

This approach gives full access to Go type information (including custom types, function defaults, and validators) without runtime reflection in the generated code.

### 2. Graph Construction (`compiler/gen/`)

`NewGraph()` builds a typed graph in a fixed order:

1. **addNode** -- Create `*Type` nodes for all schemas
2. **addEdges** -- Populate edges, detect field/edge name collisions
3. **resolve** -- Determine relationship types (O2O, O2M, M2O, M2M)
4. **setupFKs** -- Determine which side holds the foreign key
5. **addIndexes** -- Process index definitions
6. **edgeSchemas** -- Resolve `Through()` edge schemas
7. **aliases/defaults** -- Set up naming conventions

Validation runs during construction. Errors are collected and returned as structured types (`SchemaError`, `EdgeError`, etc.) that support `errors.Is`/`errors.As`.

### 3. Code Generation (`compiler/gen/generate.go`)

The `JenniferGenerator` orchestrates parallel code generation:

- Uses `errgroup` with `SetLimit(min(GOMAXPROCS, 16))`
- Writes files atomically via `os.CreateTemp` + `os.Rename`
- Checks `ctx.Err()` before each write for cancellation support
- External templates run sequentially (not goroutine-safe)

### 4. SQL Dialect (`compiler/gen/sql/`)

Implements the `DialectGenerator` interface hierarchy:

```
MinimalDialect (Name + EntityGenerator + GraphGenerator)
       |
       v
DialectGenerator (+ FeatureGenerator + OptionalFeatureGenerator)
```

Optional capabilities are detected via type assertion at runtime, following the Interface Segregation Principle.

## Generated Code Structure

```
{target}/
+-- entity/              # Shared entity structs (cross-entity imports OK)
|   +-- entity_model.go  # User, Post structs with typed edge fields
+-- query/               # Query builders (all in one package)
|   +-- user_query.go    # UserQuery with edge loading
|   +-- post_query.go
+-- user/                # Per-entity sub-package (zero cross-entity imports)
|   +-- client.go        # Create, Update, Delete, Query, Get
|   +-- create.go        # CreateUserInput builder
|   +-- update.go        # UpdateUser/UpdateUserOne builders
|   +-- delete.go        # DeleteUser/DeleteUserOne builders
|   +-- mutation.go      # UserMutation type
|   +-- where.go         # Type-safe predicates
|   +-- runtime.go       # init() registration
+-- predicate/           # Predicate type aliases
+-- runtime/             # Schema descriptors, validators
+-- client.go            # Root Client with schema + hooks
+-- velox.go             # Base types, errors, Op enum
+-- tx.go                # Transaction support
```

Key design decisions:
- **Per-entity sub-packages** self-register via `init()` (protobuf-go model)
- **Entity structs are pure data** -- no query methods on structs
- **Edge queries** go through entity client or query builder, not entity struct
- **Root Client** has no per-entity fields -- only config + Schema

## Interface Hierarchy

| Interface | Methods | Purpose |
|-----------|---------|---------|
| `MinimalDialect` | 15 | Minimum for basic dialect support |
| `DialectGenerator` | 15 + features | Full dialect with all features |
| `FeatureGenerator` | 2 | Feature flag support |
| `OptionalFeatureGenerator` | 7 | Named feature generation |
| `MigrateGenerator` | 1 | Migration file generation |
| `TypesGenerator` | 1 | Shared type generation |
| `EntityPackageDialect` | marker | Signals per-entity sub-packages |

## Error Handling

Two-tier error system:

1. **Sentinel errors** -- `ErrNotFound`, `ErrConstraintError`, etc. for `errors.Is()` checks
2. **Structured types** -- `NotFoundError`, `ConstraintError`, etc. with diagnostic fields

Each structured type implements `Is(target error) bool` matching its sentinel, so `errors.Is(structuredErr, ErrNotFound)` works through wrapping chains.

## Privacy Layer

Core types live in `privacy/` (no codegen dependency). Policy evaluation:

1. Check `DecisionFromContext` (cached decision from parent)
2. Evaluate rules in order: `Allow` stops with permit, `Skip` continues, `Deny` stops with reject
3. If no rule decides, deny by default

`FilterFunc` enables query-level filtering without codegen dependency by using interface assertion on the query's `Filter()` method.

## GraphQL Extension

Optional extension in `contrib/graphql/` that generates:
- SDL schema files (single, per-category, or per-entity split)
- Relay Node interface with registry-based dispatch
- Cursor pagination with composite cursors (msgpack + base64)
- WhereInput types (whitelist model -- fields must opt in)
- Mutation inputs with validation tags
- Field collection for efficient eager loading

The extension hooks into the codegen pipeline via the `compiler.Extension` interface.
