# fullgql

End-to-end GraphQL example: ten-entity schema wired through velox + gqlgen with hooks, privacy rules, a running HTTP server, and Relay-style connections.

## Run

```bash
go run generate.go                       # regenerate ORM + GraphQL bindings
go run -tags=ignore server.go            # start the HTTP server
go test ./...                            # run e2e tests
```

Playground: <http://localhost:8080/>

## What it shows

- `schema/` — 10 entities with a mix of O2M, M2O, M2M and polymorphic edges
- `gqlgen/` — generated gqlgen resolvers + velox bindings
- `hook/`, `rule/` — hook and privacy rule wiring
- `server.go` — minimal handler setup (build tag `ignore` so it stays out of `go test`)

This is the canonical showcase when evaluating velox + GraphQL. For a minimal ORM-only tutorial see `../basic/`.
