# external-module

**Internal QA, not a tutorial.** This sub-module verifies that velox works correctly when consumed as a **third-party library** (via its own `go.mod` + `replace` directive), complementing `tests/integration/` which covers in-tree behavior.

## What this catches

The root `tests/integration/` package lives inside the main velox module — it can import anything, including unexported runtime internals, without the usual external-consumer constraints. That hides a whole class of issues:

- Missing public exports needed by generated code
- Internal packages accidentally referenced from public generator output
- `go.mod` declaration drift between velox and consumers
- Regressions that only surface at compile time in a foreign module

This package reproduces the consumer experience: a separate module path (`example.com/integration-test`) with its own `go.mod` and a `replace` directive pointing back to the velox source tree. If velox emits generated code that an external project can't compile, this test catches it.

## Run

```bash
go run generate.go   # regenerate the ORM (output goes under ./velox/)
go test ./...        # run both integration_test.go + db_test.go
```

## Schema

Five entities chosen to cover every edge cardinality (O2M, M2O, O2O, M2M):
`User`, `Post`, `Comment`, `Tag`, `Profile`.

## Why this lives under `tests/` not `examples/`

Following big-tech layout conventions, `examples/` is reserved for user-facing tutorials. This sub-module is a **smoke test**, not a learning resource — moving it under `tests/` reflects that intent and avoids name collision with `tests/integration/`.
