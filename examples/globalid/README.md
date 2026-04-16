# globalid

**Experimental.** Opaque, cross-type identifiers for every entity — useful for GraphQL Relay's `Node` interface where every object must have a globally unique `id`.

## Run

```bash
go run generate.go
go test ./velox/
```

## What it shows

With `gen.WithFeatures(gen.FeatureGlobalID)` enabled, velox emits a `velox/internal/globalid.go` file that:

- Encodes `(type, id)` pairs into a single opaque string (base64 of `Type:42`)
- Decodes them back into their components
- Publishes a `TypeMap` so you can iterate every entity registered in the schema

```go
gid := internal.NewGlobalID("User", 42)   // "VXNlcjo0Mg=="
gid.Decode()                              // ("User", "42", nil)
gid.IntID()                               // (42, nil)
```

## Why opaque IDs?

Traditional databases expose numeric IDs directly:

```
GET /users/42
```

Clients can guess `/users/43` or enumerate the table. Also, if you later reshuffle ID allocation or add a type prefix, every client breaks.

GraphQL Relay (and other federated-ID systems) want every object identified by a single opaque string:

```graphql
{
  node(id: "VXNlcjo0Mg==") {
    ... on User { name }
    ... on Post { title }
  }
}
```

The client doesn't know or care that it's really `User#42`. The server round-trips the base64 string to recover both the type and the row.

## Feature status

`FeatureGlobalID` is marked **Experimental** in velox. The underlying encoding is stable, but:

- The generated code lives in an `internal/` package (Go visibility rules restrict access to code under `velox/…`)
- Integration with the generated GraphQL Node resolver still requires manual wiring in some scenarios

If you need production-grade global IDs today, prefer one of:

- UUIDs (velox supports `field.UUID("id", uuid.UUID{}).Default(uuid.New)`)
- A custom `Noder` that encodes type + id yourself

## Test file placement

Because `velox/internal/` is only importable by packages rooted at `velox/`, the handwritten test file lives at [`velox/globalid_example_test.go`](velox/globalid_example_test.go) — alongside the generated code, but with no `Code generated` header. `go run generate.go` leaves it untouched.
