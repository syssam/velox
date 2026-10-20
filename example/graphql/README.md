# graphql

A GraphQL server with gqlgen over velox entities: a `User` writes `Post`s,
each post belongs to an organization (the tenant) and carries `Tag`s, a
many-to-many edge, which the GraphQL schema calls `Label`.

```sh
cd example
go run ./graphql            # serves http://localhost:8080/graphql
curl -s -H 'Content-Type: application/json' -H 'X-Org-ID: 1' \
  -d '{"query":"{ posts(first: 2) { totalCount edges { node { title author { name } } } } }"}' \
  localhost:8080/graphql
```

## What the application writes

1. The schema, `schema/schema.go`, with `graphql.*` annotations from
   `github.com/syssam/velox2/contrib/graphql`: `QueryField` makes a root
   connection, `OrderField` an `orderBy` value, `WhereOps` and `WhereEdge`
   the predicates of the `where` input, `MultiOrder` a list `orderBy`, and
   `Type` renames an entity's GraphQL type (`Tag` is `Label`). A
   `Sensitive` field (`password_hash`) and the tenant column are in no type
   or input.
2. velox-gen with the plugin, which writes `gen/`: the entities, the GraphQL
   schema `gen/velox.graphqls`, and the `gql_*.go` files it binds to (IDs,
   connections, edges, orders, where inputs, `Node` and `Nodes`):

   ```sh
   go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema
   ```
3. `gqlgen.yml`: the schema files, `autobind` to `gen`, and `models` binding
   `Node` and the scalars to `veloxgql`.
4. gqlgen, which writes `graph/generated.go` and `graph/models_gen.go`, and
   the resolver stubs once:

   ```sh
   cd graphql && go run github.com/99designs/gqlgen generate
   ```
5. The root query's resolvers in `graph/schema.resolvers.go`, one call each:

   ```go
   func (r *queryResolver) Node(ctx context.Context, id string) (veloxgql.Noder, error) {
   	return gen.Node(ctx, id)
   }

   func (r *queryResolver) Users(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *gen.UserOrder, where *gen.UserWhereInput) (*gen.UserConnection, error) {
   	return gen.UsersConnection(ctx, after, first, before, last, orderBy, where)
   }
   ```

   Every other field binds to `gen`'s methods: edges are `<Edge>GQL`
   methods on the entities, connections are lazy (`totalCount` alone runs one
   `COUNT`, and no page query).
6. `main.go`: gqlgen's server with `veloxgql.Handler(db)` first, which puts
   the DB in each operation's context (and runs a mutation in one
   transaction), and a middleware that puts the tenant in the context.

## What it shows

| Request | Result |
|---|---|
| `node(id:)`, `nodes(ids:)` | one query per type; `null` for an ID with no row, or a row of another tenant |
| an ID of another type (a `Post` ID in `authorID`) | an error naming the expected type, and no query |
| `first`/`after`, `last`/`before` | keyset pages, `n+1` rows read for `hasNextPage`/`hasPreviousPage` |
| `orderBy`, `where` (`and`/`or`/`not`, `hasPostsWith`) | `ORDER BY … "id"`, `WHERE …`, `EXISTS (…)` |
| nested edges, `Post.tags` many-to-many | one query per parent (batching comes with GraphQL plan 4) |
| `Post` with tenant 1, 2 or none | only that organization's posts, through `node`, connections, edges and `where`; `velox.ErrNoTenant` without one |

`main_test.go` sends each over HTTP and checks the JSON response and the
statements sent exactly.

The schema has no JSON field and no mutations yet: velox core does not yet
read or write JSON fields, and the generated mutations come with GraphQL
plan 3.

## Regenerate

After changing the schema, run both generators, in this order, from
`example/`:

```sh
go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema
go -C graphql run github.com/99designs/gqlgen generate
```

gqlgen keeps the resolvers' bodies in `graph/schema.resolvers.go`, and moves
those of fields that are gone into a comment at its end. CI reruns both and
fails when a checked-in file differs.
