# Testing Guide

Testing standards for the Velox project, following Google and Uber Go testing best practices.

## Coverage Tiers

| Tier | Threshold | Packages |
|------|-----------|----------|
| **Critical** | 90% | `privacy/`, `dialect/sql/`, `compiler/gen/sql/`, `runtime/` |
| **High** | 80% | `compiler/gen/`, `contrib/graphql/`, `dialect/sql/schema/`, `dialect/sql/sqlgraph/`, `compiler/load/` |
| **Standard** | 80% | All other non-generated packages |
| **Exempt** | — | `internal/prototype/`, generated code, `examples/` |

CI enforces these thresholds on every PR. See `.github/workflows/ci.yml`.

## Test Types

### Unit Tests (Mandatory)

Every exported function MUST have a corresponding test. Tests live in the same package (`_test.go` suffix).

```go
// Good: tests the function's behavior, not implementation
func TestNopTx_Commit_ReturnsNil(t *testing.T) {
    tx := NopTx(mockDriver{})
    assert.NoError(t, tx.Commit())
}
```

### Table-Driven Tests (Mandatory for >2 cases)

Use table-driven tests when a function has more than 2 meaningful input combinations. Follow the [Google Go testing guide](https://google.github.io/styleguide/go/decisions#table-driven-tests) and [Uber Go style](https://github.com/uber-go/guide/blob/master/style.md#test-tables).

```go
func TestPascal(t *testing.T) {
    t.Parallel()
    tests := []struct {
        name string
        in   string
        want string
    }{
        {"simple", "user_name", "UserName"},
        {"already_pascal", "UserName", "UserName"},
        {"empty", "", ""},
        {"acronym", "http_url", "HTTPURL"},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            assert.Equal(t, tt.want, Pascal(tt.in))
        })
    }
}
```

Rules:
- Name the slice `tests`, loop variable `tt`
- Every case MUST have a `name` field
- Use `t.Run(tt.name, ...)` for subtests
- Add `t.Parallel()` to both the parent and each subtest

### Golden File Tests (Mandatory for codegen)

All code generation output MUST have golden file tests. Golden files live in `testdata/golden/`.

```bash
# Update golden files after changing codegen output:
go test ./compiler/gen/sql/ -update-golden
```

Rules:
- Never manually edit golden files
- Review golden file diffs carefully in PRs — they show exactly what changes in generated code
- One golden file per generated file type (entity, query, delete, etc.)

### Cross-Generator Consistency Tests (Mandatory)

When two generators produce code that must agree (e.g., SQL querier interface + GraphQL Paginate method), a consistency test MUST verify they agree for all annotation combinations.

```go
func TestSQLAndGraphQLGeneratorsAgree(t *testing.T) {
    // For every entity where GraphQL generates Paginate,
    // the SQL querier interface must also include Paginate.
}
```

This class of test exists because the Paginate bug (SQL generator didn't add Paginate to querier interface for entities without explicit RelayConnection annotation, while GraphQL generator defaulted to generating it) was only caught in production. These tests prevent regression.

### Integration Tests (Mandatory for database code)

Tests that require real databases use the `integration` build tag.

```bash
go test -tags integration ./dialect/sql/schema/ -run "Test(Postgres|MySQL|MultiDialect)"
```

Rules:
- Tag with `//go:build integration`
- Use environment variables for connection strings (`VELOX_TEST_POSTGRES`, `VELOX_TEST_MYSQL`)
- CI runs these against PostgreSQL 16 and MySQL 8 in Docker
- Never skip integration tests — they catch real driver behavior differences

### Fuzz Tests (Mandatory for parsers and builders)

Functions that parse or build strings from untrusted input MUST have fuzz tests.

```go
func FuzzQuote(f *testing.F) {
    f.Add("simple")
    f.Add("it's quoted")
    f.Add("'; DROP TABLE users; --")
    f.Fuzz(func(t *testing.T, s string) {
        result := Quote(s)
        // Must not contain unescaped quotes
        assert.NotContains(t, result[1:len(result)-1], "'")
    })
}
```

Current fuzz targets: `dialect/sql/` (6 targets), `compiler/gen/` (4 targets). CI runs each for 2 minutes.

### Benchmark Tests (Mandatory for hot paths)

SQL builder, privacy evaluation, and code generation MUST have benchmarks.

```go
func BenchmarkSelectBuilder_Complex(b *testing.B) {
    for b.Loop() {
        sql.Select("id", "name").From("users").Where(sql.EQ("active", true))
    }
}
```

CI compares benchmarks against the main branch baseline. Regressions >20% trigger warnings.

### Race Condition Tests (Mandatory via CI)

All tests run with `-race` in CI. No additional work needed per test, but:
- Global registries (mutators, type info, edge queries) MUST be tested for concurrent access
- Use `t.Parallel()` to surface race conditions during local development

## Test Patterns

### Parallel Execution (Mandatory)

ALL independent tests MUST use `t.Parallel()`. This catches race conditions and speeds up CI.

```go
func TestFeature(t *testing.T) {
    t.Parallel()                    // Parent test
    t.Run("case_one", func(t *testing.T) {
        t.Parallel()                // Each subtest
        // ...
    })
}
```

Exceptions (do NOT parallelize):
- Tests that modify global state (registries, environment variables)
- Tests that share database state in integration tests
- Tests with explicit ordering dependencies

### Test Naming

Follow Google's [test function names](https://google.github.io/styleguide/go/decisions#test-function-names) convention:

```
Test<Unit>_<Scenario>_<Expected>

TestDebugDriver_Exec_LogsQuery
TestNopTx_Commit_ReturnsNil
TestRegisterMutator_Duplicate_Panics
TestShouldIncludePaginate_NoAnnotation_DefaultsTrue
```

For table-driven tests, the parent is `Test<Unit>` and scenarios go in `t.Run` names.

### Assertions

Use [testify](https://github.com/stretchr/testify) for assertions:

```go
assert.Equal(t, expected, actual)       // Value equality
assert.NoError(t, err)                  // No error
assert.ErrorIs(t, err, ErrNotFound)     // Specific error
assert.Contains(t, str, "substring")    // String contains
assert.Panics(t, func() { ... })        // Panic expected
require.NoError(t, err)                 // Fatal on error (stops test)
```

Use `require` when subsequent assertions depend on the check passing. Use `assert` otherwise.

### Mock Patterns

- **SQL tests**: Use `go-sqlmock` for unit tests, real databases for integration
- **Codegen tests**: Use `mockHelper` implementing `gen.GeneratorHelper` interface
- **Driver tests**: Create minimal mock structs implementing `dialect.Driver` / `dialect.Tx`

```go
// Minimal mock — only implement what you need
type mockDriver struct {
    execFunc  func(ctx context.Context, query string, args, v any) error
    queryFunc func(ctx context.Context, query string, args, v any) error
}

func (m mockDriver) Exec(ctx context.Context, query string, args, v any) error {
    if m.execFunc != nil {
        return m.execFunc(ctx, query, args, v)
    }
    return nil
}
```

### Test File Organization

```
package_name/
├── feature.go
├── feature_test.go          # Unit tests for feature.go
├── feature_bench_test.go    # Benchmarks (if separate)
├── feature_fuzz_test.go     # Fuzz tests (if applicable)
├── testdata/
│   └── golden/              # Golden files for codegen
└── testutil_test.go         # Shared test helpers (unexported)
```

Rules:
- Test helpers go in `testutil_test.go` (not exported, not a separate package)
- Test fixtures go in `testdata/` directory
- Never import test helpers from other packages — copy or use interfaces

## What to Test

### Must Test
- Every exported function and method
- Error paths and edge cases (nil input, empty slices, zero values)
- Boundary conditions (max int, empty string, unicode)
- Concurrent access to shared state (registries, caches)
- Cross-generator contract (SQL ↔ GraphQL agreement)
- Generated code compiles and produces correct output (golden tests)

### Should Test
- Unexported functions with complex logic
- Builder pattern chains (Create → SetField → Save)
- Edge cases in type conversion and scanning

### Do Not Test
- Trivial getters/setters with no logic
- Generated code internals (test via golden files instead)
- Third-party library behavior
- Private functions that are fully exercised by public function tests

## Running Tests

```bash
# Development workflow
go test ./path/to/package/          # Single package
go test -run TestName ./...         # Single test
go test -race -cover ./...          # Full suite with race + coverage
go test -v -count=1 ./...           # Verbose, no cache

# Code generation
go test ./compiler/gen/sql/ -update-golden  # Update golden files

# Integration (requires databases)
go test -tags integration ./dialect/sql/schema/ -run "Test(Postgres|MySQL)"

# Fuzz (local exploration)
go test -fuzz=FuzzQuote -fuzztime=5m ./dialect/sql/

# Benchmarks
go test -bench=. -benchmem ./dialect/sql/
```

## CI Enforcement

GitHub Actions (`.github/workflows/ci.yml`) enforces:

| Check | Runs On | Fails Build |
|-------|---------|-------------|
| `go test -race -cover` | Every PR + push | Yes |
| Coverage thresholds (per-tier) | Every PR + push | Yes |
| `golangci-lint` (zero warnings) | Every PR + push | Yes |
| Integration tests (Postgres + MySQL) | Every PR + push | Yes |
| Fuzz tests (2min per target) | Every PR + push | Yes |
| Benchmark regression (>20%) | PRs only | Warning |
| API compatibility (`apidiff`) | PRs only | Warning |
| `govulncheck` | Every PR + push | Yes |
| Example build/test | Every PR + push | Yes |

## Adding a New Package

When creating a new package:

1. Create `*_test.go` with at least one test for each exported function
2. Add `t.Parallel()` to all tests
3. Use table-driven tests for functions with multiple cases
4. If the package generates code, add golden file tests
5. If the package touches the database, add integration tests with `//go:build integration`
6. Add the package to CI coverage thresholds in `.github/workflows/ci.yml`
7. Target the appropriate coverage tier (see Coverage Tiers above)

## Adding a New Generator

When creating a new code generator (or modifying annotation-driven behavior):

1. Add golden file tests for all generated output
2. Add a cross-generator consistency test if the generator's output must match another generator's expectations
3. Test ALL annotation combinations: explicit on, explicit off, default (no annotation), and mixed (some entities annotated, some not)
4. Test with the `SkipType` annotation to ensure skipped entities don't generate code
