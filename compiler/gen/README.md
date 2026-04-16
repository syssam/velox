# compiler/gen

Code generation engine for Velox ORM.

## Pipeline

```
Schema files -> compiler/load -> Graph (gen/graph.go) -> Validate -> Generate (Jennifer)
```

## Key Types

- **Graph** -- Holds all entity Types, validates schema, orchestrates generation
- **Type** -- Single entity with fields, edges, indexes, annotations
- **Config** -- Generation options (target, package, features, dialect)

## Dialect Interface

```
MinimalDialect -> DialectGenerator (adds feature support)
```

SQL dialect implementation: `gen/sql/`

## Features

Features are opt-in capabilities (privacy, intercept, upsert, etc.).
See `feature.go` for the full list and lifecycle stages.
