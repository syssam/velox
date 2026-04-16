# Velox vs Ent Benchmark Report

Comprehensive performance comparison between Velox and [Ent](https://entgo.io/) ORM code generation.

## Test Environment

| | |
|---|---|
| **CPU** | Apple M3 Max (14 cores) |
| **RAM** | 36 GB |
| **OS** | macOS (darwin/arm64) |
| **Go** | 1.26.1 |
| **Ent** | v0.14.6 + entgql contrib v0.7.0 |
| **Velox** | Current branch |
| **Date** | 2026-03-30 |

## Schema

Both benchmarks use **identical 50-entity schemas** with full GraphQL integration:

- 50 entity types (address, user, order, product, etc.)
- Relay connections on all edges
- WhereInput filtering enabled
- Mutation inputs (Create + Update)
- Mixed edge types (O2M, M2M, O2O)

The schemas are in `benchmarks/fixtures/ent/ent/schema/` and `benchmarks/fixtures/velox/schema/`.

> **Note:** Only the schemas (authored) are tracked in git. Ent's generated output under `benchmarks/fixtures/ent/ent/` (everything outside `schema/`) is gitignored and must be regenerated locally before running the benchmark:
>
> ```bash
> cd benchmarks/fixtures/ent && go generate ./... || go run generate.go
> cd benchmarks/fixtures/velox && go run generate.go
> ```
>
> This keeps Ent's generator output out of the velox repo — we're not vendoring Ent, we're comparing generators on the same schema.

### Feature differences

| | Ent benchmark | Velox benchmark |
|---|---|---|
| **ORM features** | `FeatureNamedEdges` | `FeatureIntercept` |
| **GraphQL** | `entgql` (schema + WhereInputs) | `graphql` (schema + WhereInputs) |

Different optional features are enabled, but the core generation workload (50 entities + GraphQL) is comparable.

## Methodology

1. Generator binaries pre-compiled (`go build -o /tmp/bench-*-gen generate.go`)
2. Warmup run executed and discarded
3. 5 measured runs using `/usr/bin/time -l`
4. Generated code verified to compile (`go build ./... = PASS`)
5. Build cache cleared with `go clean -cache` before each cold build run
6. Working directories verified for every command

## Results

### Code Generation Performance

Pre-compiled generator binary, 5 measured runs after warmup:

| Run | Ent (wall) | Velox (wall) | Ent (RSS) | Velox (RSS) |
|-----|-----------|-------------|-----------|-------------|
| 1 | 6.94s | 2.02s | 1.69 GB | 0.94 GB |
| 2 | 6.30s | 2.04s | 1.90 GB | 0.88 GB |
| 3 | 6.76s | 1.96s | 1.61 GB | 0.97 GB |
| 4 | 6.32s | 2.00s | 3.04 GB | 0.89 GB |
| 5 | 6.06s | 1.99s | 1.86 GB | 0.88 GB |
| **Median** | **6.32s** | **2.00s** | **1.86 GB** | **0.89 GB** |
| **Avg** | **6.48s** | **2.00s** | **2.02 GB** | **0.91 GB** |

**Velox is 3.2x faster with 2.1x less peak memory.**

CPU time breakdown (averages):

| | Ent | Velox | Ratio |
|---|-----|-------|-------|
| User CPU | 13.26s | 4.25s | 3.1x less |
| Sys CPU | 22.22s | 2.63s | 8.5x less |

The large sys-time gap is due to Ent's `go/format` pass which processes all generated code through the Go formatter. Velox's Jennifer generates pre-formatted AST, eliminating the formatting step entirely.

### Compilation Time

#### Cold build (`go clean -cache`, 3 runs)

| Run | Ent (wall) | Velox (wall) | Ent (RSS) | Velox (RSS) |
|-----|-----------|-------------|-----------|-------------|
| 1 | 12.26s | 13.71s | 3.11 GB | 1.59 GB |
| 2 | 12.34s | 13.57s | 3.35 GB | 1.52 GB |
| 3 | 12.50s | 13.35s | 3.49 GB | 1.52 GB |
| **Avg** | **12.37s** | **13.54s** | **3.31 GB** | **1.54 GB** |

Ent compiles 9% faster in wall-clock time. However, Velox uses **2.2x less peak memory**.

Velox uses more total CPU (~110s user vs ~68s user) because the Go compiler processes 57 smaller packages with more parallelism overhead. The tradeoff: each package is small enough to be GC'd independently, resulting in dramatically lower peak memory.

Both have exactly **57 Go packages**.

#### Incremental build (cached, 3 runs)

| | Ent | Velox |
|---|-----|-------|
| Avg wall time | 0.20s | 0.24s |
| Peak RSS | ~50 MB | ~52 MB |

No practical difference.

### Generated Code Metrics

| Metric | Ent | Velox | Delta |
|--------|-----|-------|-------|
| Total `.go` files | 418 | 918 | Velox 2.2x more files |
| Total lines of code | 335,365 | 230,280 | **31% fewer lines** |
| Directory size on disk | 11 MB | 9.1 MB | **17% smaller** |
| Avg lines per file | 802 | 251 | **3.2x smaller files** |
| Largest single file | 56,050 | 9,444 | **5.9x smaller** |
| Files > 1,000 lines | 47 | 0 | **Velox has none** |
| `client.go` | 9,686 lines | 658 lines | **14.7x smaller** |

### Ent's Largest Files (monoliths)

| File | Lines |
|------|-------|
| `mutation.go` | 56,050 |
| `gql_where_input.go` | 26,093 |
| `gql_collection.go` | 19,900 |
| `gql_pagination.go` | 18,602 |
| `client.go` | 9,686 |
| `gql_mutation_input.go` | 7,743 |

These 6 files alone total **138K lines**. At 50 entities, Ent's `mutation.go` contains all entity mutation types in a single file, which impacts IDE performance (indexing, autocomplete, go-to-definition).

### Velox's Largest Files

| File | Lines |
|------|-------|
| `model/gql_pagination.go` | 9,444 |
| `migrate/schema.go` | 2,588 |
| `intercept/intercept.go` | 1,761 |
| `edge_loaders.go` | 814 |
| `*/update.go` (per entity) | ~800 |

### GraphQL Generated Code

| Metric | Ent | Velox | Delta |
|--------|-----|-------|-------|
| Total GraphQL Go lines | 77,289 | 28,726 | **2.7x less** |
| GraphQL SDL lines | 18,985 | (separate) | |
| Max GraphQL file | 26,093 | 9,444 | **2.8x smaller** |
| Architecture | 7 monolith files | Per-entity + shared model | |

### Dependencies

| Metric | Ent | Velox |
|--------|-----|-------|
| Direct dependencies | 6 | 2 |
| Transitive (unique modules) | 47 | 45 |
| Generator binary size | 22 MB | 32 MB |

Velox's generator binary is larger because it bundles Jennifer and the GraphQL extension. Ent's generator delegates some work to external tools.

## SQL Builder Benchmarks

Velox's runtime SQL builder performance (not a comparison — Ent's SQL builder is separate):

| Operation | Dialect | ns/op | B/op | allocs/op |
|-----------|---------|-------|------|-----------|
| INSERT (default) | SQLite | 179 | 176 | 7 |
| INSERT (default) | MySQL | 123 | 104 | 5 |
| INSERT (default) | Postgres | 191 | 176 | 7 |
| INSERT (8 cols) | SQLite | 958 | 1,176 | 24 |
| INSERT (8 cols) | MySQL | 964 | 1,176 | 23 |
| INSERT (8 cols) | Postgres | 1,163 | 1,192 | 32 |
| SELECT (simple) | SQLite | 428 | 544 | 14 |
| SELECT (joins) | SQLite | 1,658 | 2,040 | 52 |
| SELECT (complex) | MySQL | 2,276 | 3,616 | 90 |
| UPDATE (simple) | MySQL | 586 | 632 | 22 |
| UPDATE (multi-col) | Postgres | 1,679 | 2,472 | 56 |
| DELETE (simple) | SQLite | 305 | 416 | 13 |
| DELETE (complex) | Postgres | 1,219 | 1,816 | 52 |
| Predicates (simple) | — | 257 | 736 | 12 |
| Predicates (compound) | — | 543 | 1,576 | 27 |

## Reproducing

```bash
# Code generation benchmark
cd benchmarks/fixtures/ent && go build -o /tmp/ent-gen generate.go
cd benchmarks/fixtures/velox && go build -o /tmp/velox-gen generate.go

# Run (from respective directories)
cd benchmarks/fixtures/ent && /usr/bin/time -l /tmp/ent-gen
cd benchmarks/fixtures/velox && /usr/bin/time -l /tmp/velox-gen

# Cold compile
cd benchmarks/fixtures/ent && go clean -cache && /usr/bin/time -l go build ./ent/...
cd benchmarks/fixtures/velox && go clean -cache && /usr/bin/time -l go build ./velox/...

# SQL builder benchmarks
go test -bench=. -benchmem -count=3 ./dialect/sql/

# Code generation micro-benchmark
go test -bench=BenchmarkGraph_Gen -benchmem -count=3 ./compiler/gen/
```

## Summary

| Category | Winner | Magnitude |
|----------|--------|-----------|
| Code generation speed | **Velox** | 3.2x faster |
| Generation memory | **Velox** | 2.1x less |
| Generation consistency | **Velox** | 0.08s range vs 0.88s |
| Cold compile wall time | **Ent** | 9% faster |
| Cold compile memory | **Velox** | 2.2x less |
| Incremental build | Tie | Both < 0.25s |
| Total generated code | **Velox** | 31% fewer lines |
| Max file size | **Velox** | 5.9x smaller |
| File count | **Ent** | 2.2x fewer files |
| Generator binary size | **Ent** | 10 MB smaller |
