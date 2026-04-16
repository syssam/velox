# fulltest

**Internal codegen target.** Not a user-facing tutorial.

Mirrors the `fullgql` schema (10 entities) but without a server or application code. Exists so the velox generators — including `contrib/graphql` — have a fixture project to emit into when exercising full-feature codegen paths. Some internal tests glob against `examples/fulltest/velox/entity/gql_edge_*.go` to assert generator invariants.

## Run

```bash
go run generate.go   # regenerate everything
go build ./...       # sanity-check the generated code
```

If you are learning velox, use `../basic/` or `../fullgql/` instead.
