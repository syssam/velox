# json-field

JSON columns in three common shapes: typed Go struct, untyped map, and slice. Useful when parts of the model are flexible or semi-structured but you still want type-safe access in Go.

## Run

```bash
go run generate.go
go test ./...
```

## What it shows

```go
// Typed — DB stores JSON, Go sees a concrete Specs struct.
field.JSON("specs", Specs{})

// Untyped — flexible bag of key/value pairs.
field.JSON("metadata", map[string]any{}).Optional()

// Slice — JSON array of strings.
field.JSON("tags", []string{}).Optional()
```

The second argument to `field.JSON()` is a zero value of the Go type you want. Velox uses it (via `reflect.TypeOf`) to generate typed getters/setters. Under the hood, every JSON field round-trips through `encoding/json`.

## When to choose typed vs. untyped

| | Typed (`Specs{}`) | Untyped (`map[string]any{}`) |
|---|---|---|
| Go ergonomics | ✅ compiler-checked fields | ❌ `v["key"].(float64)` |
| Schema evolution | Need to update Go struct | ✅ no code change |
| Validation | ✅ you control the struct | Manual |
| Per-row variation | ❌ fixed shape | ✅ each row can differ |

Use typed when the shape is known and stable. Use untyped for user-supplied extras, A/B test configs, feature flags per entity, etc.

## What JSON fields don't give you

SQLite / MySQL / PostgreSQL all have JSON operators (`->>`, `@>`, `jsonb_path_query`, etc.), but velox's generated query builder doesn't expose them yet. If you need to filter by nested JSON keys, drop to a raw predicate:

```go
client.Product.Query().
    Where(func(s *sql.Selector) {
        s.Where(sql.ExprP("json_extract(specs, '$.color') = ?", "graphite"))
    }).
    All(ctx)
```

For frequent nested-key queries, consider pulling the hot fields out of JSON into their own typed columns.
