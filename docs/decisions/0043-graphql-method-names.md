# 43. velox-graphql's methods are named as entgql's

Status: accepted, 2026-11-06. Its Node type, `GlobalID` and `node_gql.go`
are superseded by [0061](0061-graphql-plain-ids.md): an ID is the key, and
there is no Node.

## Context

velox-graphql wrote, for each Node type:

```go
func (x *Post) IDGQL() string
func (x *Post) AuthorGQL(ctx context.Context) (*User, error)
func (x *Post) TagsGQL(ctx context.Context, after *veloxgql.Cursor, …) (*LabelConnection, error)
func postNodeGQL() gqlimpl.NodeKind
func postKeyGQL(x *Post) int64
```

and the SDL bound each with `@goField(name: "AuthorGQL")`. An edge's method
took the suffix because the struct held a field of the edge's name. Since
ADR 0041 the relations are in the struct's `Edges` field, so `Author` is
free. entgql names the method `Author`. `postKeyGQL` was called nowhere,
and `postNodeGQL` once, from `node_gql.go`.

## Decision

- An edge's method is the edge's Go name: `Post.Author(ctx)`,
  `Post.Tags(ctx, after, first, before, last, orderBy, where)`.
- gqlgen binds a field to a method whose name matches it, ignoring case
  (gqlgen v0.17.86, `findBindMethoderTarget`). The SDL writes
  `@goField(name: …)` only where it does not: an edge renamed by
  `graphql.FieldName`, and an edge with `graphql.MaxList`, whose limit
  `@goField` holds.
- A Node's ID method is `GlobalID()`, and `veloxgql.Noder` has it. The
  struct's `ID` is the raw key, so the method cannot be `ID`, and `id` keeps
  `@goField(name: "GlobalID")`.
- `node_gql.go` builds each Node type in its switch; no `<entity>NodeGQL`
  or `<entity>KeyGQL` is written.
- The plugin refuses an edge whose method would take a name the row
  already has: a field, `Edges`, `String` or `GoString`.
- A field getter keeps `<Field>GQL`: it converts a field the struct holds
  under that name. The unexported helpers keep the suffix too, so they never
  meet velox-gen's unexported names in the same package.
- `veloxgql.GeneratedCodeIsVersion12` becomes `GeneratedCodeIsVersion13`.

## Consequences

- An application's resolver that called `p.AuthorGQL(ctx)` calls
  `p.Author(ctx)`; one that read `IDGQL()` reads `GlobalID()`.
- examples/graphql: its four `_gql.go` files go from 1242 lines to 1221,
  and velox.graphql keeps 3 of its 7 `@goField(name: …)`, the ids'.
- A test checks that no function the plugin writes goes uncalled.
- Compile cost does not change measurably. `bench/scale -n 500 -s 20
  -graphql`, darwin/arm64, one run each on a shared machine, base 8b06013c
  against this change: cold build 105.7 s CPU (3.73x the reference) against
  103.5 s (3.51x); schema edit and rebuild 75.6 s (2.67x) against 77.2 s
  (2.62x); a service 7.6 MB both; link peak 320 MB against 314 MB.
