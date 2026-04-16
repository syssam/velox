# Velox Build Performance Report

**Date:** 2026-04-05 (updated after protobuf-style client refactor)
**Go version:** go1.26.1 darwin/arm64
**Platform:** macOS Darwin 25.3.0 (Apple Silicon)

## RESOLVED: Protobuf-Style Client Architecture

The root package import fan-out issue (12.9 GB for 100 entities) has been resolved by adopting the protobuf-go model:
- Entity subpackages self-register via `init()` (TypeInfo, QueryFactory, Mutator)
- Root package has zero per-entity imports (11 imports total)
- Users import entity subpackages directly: `user.NewClient(cfg).Create().SetName("foo").Save(ctx)`

| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| Root imports (100 entities) | 114 | **11** | -90% |
| Cold build time | 69.5s | **8.1s** | -88% |
| Cold build peak RSS | 12.9 GB | **1.1 GB** | -91% |
| Codegen time | 1.85s | **1.34s** | -28% |

---

## Project Size

| Metric | Count |
|--------|-------|
| Core source (excl examples/generated) | 69,114 LOC |
| Core test files | 59,265 LOC |
| Generated prototype | 14,583 LOC |
| Packages (buildable) | 46 |
| Total transitive deps (max) | 334 (contrib/graphql) |
| Build cache size | 1.1 GB |
| CLI binary size | 30 MB |
| Largest test binary | 35 MB (contrib/graphql) |

---

## Build Benchmarks

### Summary

| Scenario | Wall Time | Peak RSS | CPU (user+sys) |
|----------|-----------|----------|----------------|
| **Cold build** (clean cache) | 11.57s | 1.71 GB | 81.80s |
| **Warm build** (fully cached) | 1.29s | 342 MB | 3.64s |
| **Incremental** (touch velox.go) | 1.54s | 345 MB | 3.13s |

### Bench-100 (100 Entities, 346K LOC Generated)

| Scenario | Wall Time | Peak RSS |
|----------|-----------|----------|
| **Codegen** (generate 100 entities) | 1.85s | 1.65 GB |
| **Cold build** (clean cache) | 69.10s | 12.49 GB |
| **Warm build** (fully cached) | 0.54s | 45 MB |
| **Incremental** (touch 1 entity) | 0.32s | 47 MB |
| **Incremental** (touch root client.go) | 0.32s | 45 MB |

Key insight: per-entity sub-package isolation means incremental builds are **O(1)** — touching one entity out of 100 rebuilds only that entity's package (0.32s). Even touching `client.go` (root file) does not cascade to recompile all 100 entities.

#### Root Cause: 12.5 GB Cold Build Memory

Traced via process monitoring (`ps aux` during build) and per-package isolation:

**The bottleneck is a single `compile` process** for the root `velox/` package. Memory timeline:

```
0-58s:   Compiles 100 entity subpackages + query + entity (normal, ~2.5 GB cumulative)
58-69s:  Single compile process for root package spikes from 2.5 GB → 12.3 GB
```

Root cause chain:
1. Root package has **206 files** and imports **114 packages** (100 entity subpackages + query + entity + runtime + stdlib)
2. Each entity subpackage exports **2.5 MB** of type data (object file). Total: **100 × 2.5 MB = 250 MB**
3. The `query/` package exports **81 MB** of type data (71K LOC, 100 query types)
4. The `entity/` package exports **13 MB**
5. Total export data loaded by root compiler: **~358 MB**
6. Go compiler expands export data ~33x into internal AST/type-check structures: **358 MB × 33 ≈ 12 GB**

This is NOT caused by:
- ~~Parallel compilation~~ — `GOMAXPROCS=1` still uses 13.9 GB
- ~~Linker~~ — it's the `compile` binary, not `link`
- ~~Build cache writes~~ — peak is during compilation, not I/O

#### Feature Impact Test (100 Entities)

Tested each feature individually to determine if any feature causes the memory spike:

| Feature | Generated LOC | Files | Cold Build RSS |
|---------|--------------|-------|----------------|
| **none** (baseline) | 341,260 | 1,211 | **13.0 GB** |
| intercept | 346,733 | 1,212 | 12.7 GB |
| privacy | 348,150 | 1,312 | 12.6 GB |
| entql | 345,364 | 1,212 | 13.3 GB |
| namedges | 346,360 | 1,211 | 12.9 GB |
| lock | 341,260 | 1,211 | 12.8 GB |
| upsert | 355,160 | 1,211 | 12.7 GB |
| modifier | 341,260 | 1,211 | 13.3 GB |
| validator | 341,260 | 1,211 | 13.1 GB |
| entpredicates | 341,260 | 1,211 | 12.8 GB |
| autodefault | 341,360 | 1,211 | 13.0 GB |

**Conclusion: No single feature causes the memory spike.** All features produce ~12.5-13.5 GB (within noise). The baseline with ZERO features already uses 13.0 GB. The root cause is the **root package architecture**, not any feature flag.

The root package (`velox/`) contains:
- 100 entity wrapper files (`entity000.go` ... `entity099.go`, ~157 LOC each)
- 100 meta registration files (`entity000_meta.go` ... `entity099_meta.go`)
- `client.go` (1,300 LOC, imports all 100 entity subpackages)
- `tx.go`, `velox.go`, `errors.go`, etc.

Total: **206 files, 114 imports, 20,871 LOC** — all compiled as a single unit by the Go compiler.

**Fix direction:** Reduce what the root package imports. Options:
1. Split `client.go` so each entity client is in its own file with only its own subpackage import
2. Use a registry/init pattern where entity subpackages register themselves, eliminating direct imports from root
3. Lazy-load entity type data via `go:linkname` or interface-based indirection
4. Accept the cost — 12 GB is only for cold builds; warm builds are 45 MB

### Cold Build Analysis (Core)

- **1.71 GB peak RSS** is the main concern — this is the Go compiler toolchain running in parallel
- `user+sys = 81.80s` vs `wall = 11.57s` → ~7x parallelism (good utilization on Apple Silicon)
- Cache write I/O (9.35s sys) is significant — build cache writes dominate sys time

### Warm/Incremental Build

- Warm builds are **fast** (1.29s) — Go's build cache is working well
- Touching `velox.go` (root package, many dependents) only adds 0.25s
- Memory drops to 342 MB — only link/validation, no compilation

---

## Per-Package Build Time (Warm Cache, Sorted)

| Package | Build Time |
|---------|-----------|
| `cmd/velox` | 1,590 ms |
| `compiler/gen/cmd/testgen` | 977 ms |
| `schema/field/internal` | 281 ms |
| `querylanguage/internal` | 273 ms |
| `contrib/graphql` | 173 ms |
| `compiler/gen/sql` | 162 ms |
| `compiler` | 156 ms |
| `compiler/gen` | 144 ms |
| `compiler/internal` | 138 ms |
| `contrib/graphql/gqlrelay` | 122 ms |
| All others | < 120 ms |

**Hotspots:**
1. `cmd/velox` (1.59s) — CLI binary links everything; expected for a leaf binary
2. `compiler/gen/cmd/testgen` (0.98s) — test generation helper, also a leaf binary
3. `schema/field/internal` (281ms) — likely large generated type tables

---

## Test Suite Performance

### Without Race Detector

| Metric | Value |
|--------|-------|
| **Wall time** | 14.64s |
| **Peak RSS** | 364 MB |
| **CPU (user+sys)** | 49.62s |

### With Race Detector (`-race`)

| Metric | Value |
|--------|-------|
| **Wall time** | 78.53s |
| **Peak RSS** | 1.98 GB |
| **CPU (user+sys)** | 315.89s |

**Race detector overhead: 5.4x wall time, 5.4x peak memory**

### Slowest Test Packages

| Package | Time | Source LOC | Test LOC |
|---------|------|-----------|----------|
| `compiler/load` | 16.67s | 907 | 617 |
| `cmd/velox` | 7.15s | — | — |
| `privacy` | 6.96s | — | — |
| `runtime` | 6.81s | 3,128 | 5,659 |
| `schema/edge` | 6.69s | — | — |
| `schema/index` | 6.64s | — | — |
| `schema` | 6.62s | — | — |
| `schema/mixin` | 6.51s | — | — |
| `querylanguage` | 6.51s | — | — |
| `schema/field` | 6.27s | 4,694 | 4,658 |
| **Total (46 packages)** | **116.76s** | | |

**Key finding:** `compiler/load` is the slowest package at 16.67s — it spawns `go/packages` to load schemas, which involves exec'ing the Go toolchain. The 6-7s cluster (privacy, runtime, schema/*) likely reflects test compilation time rather than actual test execution.

**Memory profile for `compiler/load`:**
- Peak RSS: 352 MB
- This package loads Go packages via `golang.org/x/tools/go/packages`, which internally invokes `go list` — expensive subprocess + JSON parsing

---

## Dependency Analysis

### Highest Dependency Fan-out (Direct Imports)

| Package | Direct Imports |
|---------|---------------|
| `compiler/gen` | 40 |
| `dialect/sql/schema` | 30 |
| `compiler/load` | 27 |
| `contrib/graphql` | 24 |
| `compiler` | 19 |

### Deepest Transitive Dependency Chains

| Package | Transitive Deps |
|---------|----------------|
| `contrib/graphql` | 334 |
| `cmd/velox` | 268 |
| `compiler` | 263 |
| `compiler/gen/sql` | 261 |
| `compiler/gen` | 260 |

---

## Known Issues

### 1. Import Cycle in `examples/test/velox/user`

```
package github.com/syssam/velox/examples/test/velox/user
    imports github.com/syssam/velox/examples/test/velox/user from mutation.go
```

`mutation.go` in the `user` package imports itself — a self-referential import cycle. This is in test fixture code (comparing Ent vs Velox output), not core code. `go list ./...` fails entirely due to this, requiring `-e` flag or explicit exclusion.

**Impact:** Cannot run `go build ./...` or `go test ./...` without filtering. CI should exclude `examples/test/`.

### 2. Schema Contamination Risk in `benchmarks/fixtures/stress-100/generate.go`

`generate.go` uses relative paths (`./schema`, `./velox`). If executed from the wrong directory, it writes 100 entity schema files into the **project root's `schema/`** directory, causing import cycles that break the entire build. During this benchmark run, this contamination was discovered and cleaned.

**Fix:** Use absolute paths or validate working directory in `generate.go`.

### 3. Large Generated Examples

| File | LOC |
|------|-----|
| `examples/erp/velox/gql_pagination.go` | 157,280 |
| `examples/erp/graph/ent.resolvers.go` | 114,955 |
| `examples/erp/velox/migrate/schema.go` | 80,671 |

These are legitimate generated outputs for a large ERP schema but contribute significantly to cache size and `go list` overhead.

---

## Recommendations

### Short-term (Quick Wins)

1. **Fix import cycle** in `examples/test/velox/user/mutation.go` — remove self-import or restructure
2. **Add `examples/test/` to CI exclusion** if not already done
3. **Set `GOGC=200`** for CI builds — trades memory for faster GC on machines with plenty of RAM

### Medium-term

5. **Profile `compiler/load` tests** — 16.67s is disproportionate for 617 LOC of tests. The `go/packages` subprocess overhead may be reducible by caching or batching loads
6. **Consider `-race` only on CI** — 5.4x overhead is standard but costly for local dev. Run `-race` in CI, use plain `go test` locally
7. **Reduce `compiler/gen` import fan-out** (40 direct imports) — this is the compilation bottleneck; splitting could improve incremental build times

### Long-term

8. **Monitor binary sizes** — 30-35 MB binaries are normal for Go with debug info. Use `go build -ldflags="-s -w"` for release builds (~20 MB)
9. **Consider `GOMEMLIMIT`** for CI containers — prevents OOM kills in constrained environments. Suggested: `GOMEMLIMIT=2GiB` for 4 GB CI runners
10. **Harden `benchmarks/fixtures/stress-100/generate.go`** — validate working directory or use absolute paths to prevent schema contamination

---

## Baseline for Future Tracking

```
# Quick benchmark commands
go clean -cache && /usr/bin/time -l go build ./cmd/velox        # Cold CLI build
/usr/bin/time -l go build ./cmd/velox                            # Warm CLI build
/usr/bin/time -l go test -count=1 ./...                          # Full test suite
/usr/bin/time -l go test -race -count=1 ./...                    # Tests + race
```

### Core (46 packages)

| Metric | Baseline (2026-04-05) | Alert Threshold |
|--------|----------------------|-----------------|
| Cold build wall time | 11.57s | > 20s |
| Cold build peak RSS | 1.71 GB | > 3 GB |
| Warm build wall time | 1.29s | > 3s |
| Test suite (no race) | 14.64s | > 30s |
| Test suite (-race) | 78.53s | > 120s |
| Test peak RSS (-race) | 1.98 GB | > 4 GB |
| CLI binary size | 30 MB | > 50 MB |
| Build cache size | 1.1 GB | > 3 GB |

### Bench-100 (100 entities, 1,212 files, 346K LOC)

| Metric | Baseline (2026-04-05) | Alert Threshold |
|--------|----------------------|-----------------|
| Codegen time | 1.85s | > 5s |
| Codegen peak RSS | 1.65 GB | > 3 GB |
| Cold build wall time | 69.10s | > 120s |
| Cold build peak RSS | 12.49 GB | > 20 GB |
| Warm build wall time | 0.54s | > 2s |
| Incremental (1 entity) | 0.32s | > 1s |
| Incremental (root file) | 0.32s | > 2s |
