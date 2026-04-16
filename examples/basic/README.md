# basic

Minimal velox tutorial: four entities (`User`, `Post`, `Comment`, `Tag`) demonstrating fields, edges, and basic queries. No GraphQL, no hooks — the smallest complete velox app.

## Run

```bash
go run generate.go   # regenerate the ORM from schema/
go test ./...        # run the e2e test
```

## What it shows

- Schema definition in `schema/`
- Generated client and per-entity query builders in `velox/`
- End-to-end create/query/edge traversal in `e2e_test.go`

Start here if you are new to velox.
