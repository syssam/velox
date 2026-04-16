# tree

Self-referencing edge: a `Category` entity with `parent`/`children` on itself, producing a classic hierarchical tree.

## Run

```bash
go run generate.go
go test ./...
```

## What it shows

- **Self-reference shortcut:** a single `edge.To("children", Category.Type).From("parent").Unique()` declares both sides of the relationship on one type
- **Building a tree:** `SetParentID(parentID)` attaches a child to its parent
- **Finding roots:** `Not(HasParent())` — categories with no parent
- **Finding leaves:** `Not(HasChildren())` — categories with no children
- **Walking up / down:** `QueryParent(cat)` and `QueryChildren(cat)`

## Schema

```go
edge.To("children", Category.Type).
    From("parent").
    Unique()
```

`Unique()` on the inverse side produces **O2M** ("parent has many children / child has one parent"). Remove it for a symmetric M2M relationship (e.g. social graph of friends).

See [`docs/reference.md`](../../docs/reference.md#self-referencing) for more edge direction conventions.
