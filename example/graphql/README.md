# graphql

A GraphQL server with gqlgen over velox entities: a `User` writes `Post`s,
each post belongs to an organization (the tenant) and carries `Tag`s, a
many-to-many edge, which the GraphQL schema calls `Label`.

```sh
cd example
go run ./graphql            # serves http://localhost:8080/graphql
# X-Org-ID picks the tenant, for this demo only (see below)
curl -s -H 'Content-Type: application/json' -H 'X-Org-ID: 1' \
  -d '{"query":"{ posts(first: 2) { totalCount edges { node { title author { name } } } } }"}' \
  localhost:8080/graphql
```

## What the application writes

1. The schema, `schema/schema.go`, with `graphql.*` annotations from
   `github.com/syssam/velox2/contrib/graphql`: `QueryField` makes a root
   connection, `OrderField` an `orderBy` value, `WhereOps` and `WhereEdge`
   the predicates of the `where` input, `MultiOrder` a list `orderBy`, and
   `Type` renames an entity's GraphQL type (`Tag` is `Label`), and
   `Mutations` the root mutations (`createPost`, `updatePost`, `deletePost`,
   `createLabel`, `updateLabel`, `createUser`, `updateUser`). A `Sensitive`
   field (`password_hash`) is in no type, where input or order: it is an
   input only, `passwordHash` on `createUser` and `updateUser`. The tenant
   column is in no type or input. `title` is `NotEmpty()`, a validator the
   mutations run before any statement, and the tag's `name` is `Unique()`.
   `views` is `Optional()`: `createPost` may leave it out, and it is then 0,
   its zero value. `published_at` is `Optional().Nillable()`: `Optional`
   makes it optional on create, and `Nillable` makes its column NULL-able and
   the Go field a `*time.Time` with a `Null*` handle, so a draft's
   `publishedAt` is `null` and `publishedAtIsNil` filters on it. velox-gen refuses `Optional()`
   alone on a time field without a Default: its unset value, the zero time,
   would go into a NOT NULL column, which MySQL's strict mode refuses.
   `email` has `graphql.Directives(graphql.Directive{Name: "admin"})`: see
   below.
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
   cd graphql && go tool gqlgen generate
   ```
5. The root query's and mutations' resolvers in `graph/schema.resolvers.go`,
   one call each:

   ```go
   func (r *queryResolver) Node(ctx context.Context, id string) (veloxgql.Noder, error) {
   	return gen.Node(ctx, id)
   }

   func (r *queryResolver) Users(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *gen.UserOrder, where *gen.UserWhereInput) (*gen.UserConnection, error) {
   	return gen.UsersConnection(ctx, after, first, before, last, orderBy, where)
   }

   func (r *mutationResolver) CreatePost(ctx context.Context, input gen.CreatePostInput) (*gen.Post, error) {
   	return gen.CreatePost(ctx, input)
   }

   func (r *mutationResolver) UpdatePost(ctx context.Context, id string, input gen.UpdatePostInput) (*gen.Post, error) {
   	return gen.UpdatePost(ctx, id, input)
   }

   func (r *mutationResolver) DeletePost(ctx context.Context, id string) (string, error) {
   	return gen.DeletePost(ctx, id)
   }
   ```

   The root function is named after the plural of the GraphQL type, as every
   name `gen` declares is: `gen.LabelsConnection` for the `labels` field of
   the type `Label` (the entity `Tag`).

   And `User.initials`, the application's own field (`graph/app.graphqls`
   extends `User`), from the name. velox-graphql cannot see what its
   resolver reads, so a page that selects it reads every column:

   ```go
   func (r *userResolver) Initials(ctx context.Context, obj *gen.User) (string, error) {
   	var b strings.Builder
   	for _, w := range strings.Fields(obj.Name) {
   		first, _ := utf8.DecodeRuneInString(w)
   		b.WriteRune(unicode.ToUpper(first))
   	}
   	return b.String(), nil
   }
   ```

   Every other field binds to `gen`'s methods: edges are `<Edge>GQL`
   methods on the entities, connections are lazy (`totalCount` alone runs one
   `COUNT`, and no page query).
6. `main.go`: gqlgen's server with `veloxgql.NewExtension(db)` first, which
   puts the DB and the limits in each operation's context (and runs a
   mutation in one transaction), `veloxgql.ErrorPresenter`, a limit on the
   cost of a request, middlewares that put the tenant and the role in the
   context, and the directive `@admin`.

### Mutations

`createX(input:)` inserts a row and links the edges its input names
(`authorID`, `tagIDs`). `updateX(id:, input:)` changes only the fields
given: a field left out, or `null`, is unchanged, `clearPublishedAt: true`
sets a NULL-able column to NULL, and an edge is cleared, then has its
`remove…IDs` removed, then its `add…IDs` added. `deleteX(id:)` deletes the
row's join rows, then the row, and returns its ID. Repeated IDs count once,
and adding a link that exists changes nothing.

Every ID to set or add is checked against what the caller may read: the
tenant scope and the entity's Read policy. An ID of another tenant's row, or
of one a policy hides, is `NOT_FOUND`, as a missing one is, so a client
cannot tell that a row exists. An ID to remove is not checked, and neither
it nor `clearTags` or `clearPosts` touches a link to a row the caller may
not read: `updateLabel(id:, input: {clearPosts: true})` unlinks the
caller's organization's posts from the label, and another organization's
stay linked. `deletePost` of another tenant's post is `NOT_FOUND`, and its
join rows, which are not tenant-scoped, are left alone. A row's own delete
removes all its join rows, whatever the tenant at their other end: the row
is gone, and a shared row, as a label is, is unlinked from every
organization's posts.

The validators (`title`'s `NotEmpty()`) run on the fields an input sets
before any statement: `createPost` with an empty title is `BAD_USER_INPUT`,
naming `title`, whatever its IDs name.

**One transaction per operation.** `veloxgql.NewExtension` runs a mutation
operation in one transaction, and rolls it back when the response has any
error. In `mutation { a: createLabel(…) b: createLabel(…) }`, when `b` fails
(its name is taken), `a`'s row is rolled back too, and `data` is `null`.
Inside that transaction each generated mutation runs in a savepoint, so its
error rolls back its own writes at once. A resolver that returns the error,
as this example's do, fails the operation, and the transaction rolls back
all of it; a resolver that swallows the error keeps the operation's other
writes, and the operation commits. A generated mutation called outside a
server, or in an operation `WithTxFilter` leaves out, runs in a transaction
of its own, so it is never half-written either.

**The row returned.** `createX` returns the row as written. `updateX`
reads its row back under the tenant scope but without the Read policy: the
caller just wrote it, so an update that moves the row out of the caller's
Read view still returns it.

**Write permission.** An update that changes only edges (`addTagIDs` and
nothing else) still needs permission to write the row: it is checked under
the same scope as an `UPDATE` of it, the tenant and the Read and Write
policies, so no edge changes on a row the caller cannot write.

**MySQL** needs `clientFoundRows=true` in its DSN
(`user:pass@tcp(host)/db?parseTime=true&clientFoundRows=true`). `updateX`
checks the `UPDATE`'s row count, and without it MySQL counts only the rows
whose values changed, so an update that sets a field to its current value
would read as `NOT_FOUND`.

### The tenant comes from a header only in this demo

`orgFromHeader` takes the tenant from the client's `X-Org-ID` header, so
that you can try tenants with `curl`. That is for this demo only: a client
that chooses its own tenant can read every tenant's rows. A real server
derives the tenant from what authenticates the request (a verified session
or token), and ignores any header the client sets for it.

### A field for admins only

`User.email` carries the application's directive `@admin`, which
`graph/app.graphqls` declares and `main.go` implements: it lets the field
through when the request's `X-Role` header is `admin`, and otherwise fails
with an application error, code `FORBIDDEN`. velox-graphql copies a field's
directives onto its `where` predicates (`email`, `emailHasPrefix`…), so the
directive guards those too: without it, a client could find an email it may
not read by filtering on it, a prefix at a time. That is why the directive is
declared `on FIELD_DEFINITION | INPUT_FIELD_DEFINITION`: gqlgen refuses the
schema otherwise.

It guards the mutations' input fields as well: velox-graphql copies it onto
`CreateUserInput.email` and `UpdateUserInput.email`, so a `createUser` that
sets `email` without `X-Role: admin` fails with `FORBIDDEN`, at the path
`["createUser", "input", "email"]`, and writes nothing.

`email` has no `OrderField`: the order and the cursors would reveal its
values, which no directive guards, so velox-graphql refuses `OrderField` on a
field with an application directive unless `graphql.OrderUnguarded()` says
so.

Like `X-Org-ID`, `X-Role` is for this demo only: a client that names its own
role is whoever it says. A real server takes the role from what
authenticates the request. A directive is field-level access, as is gqlgen's
`srv.AroundFields`; row-level access, which rows a caller may read, is the
entity's `velox.Policy`.

**Upgrading.** An application whose schema has a directive on a filterable
field or edge must now also declare that directive on
`INPUT_FIELD_DEFINITION` (`on FIELD_DEFINITION | INPUT_FIELD_DEFINITION`), as
`@admin` does here: velox-graphql copies it onto the `where` inputs, and
gqlgen refuses to load a schema where a directive is used on a location it
is not declared on. This is a breaking change, made before the first
release, because those filters were unguarded before.

### What a client sees of an error

The server sets `srv.SetErrorPresenter(veloxgql.ErrorPresenter(nil))`.
gqlgen's default presenter shows the client every error's text, and velox's
and the driver's name entities, tables, constraints and columns: without a
tenant, a client would read `velox: gen.Post: no tenant in the context`.
`ErrorPresenter` shows only what is safe, with a code in
`extensions.code`:

| The error | `extensions.code` | `message` |
|---|---|---|
| a bad ID, cursor, paging argument, filter or scalar | `BAD_USER_INPUT` | what was expected, as `ID: malformed ID: not canonical base64url without padding` |
| an ID in a mutation that names no row the caller may see or change | `NOT_FOUND` | `deletePost: no Post has this ID` |
| a conflicting update input (`publishedAt` with `clearPublishedAt`), too many IDs | `BAD_USER_INPUT` | naming the input field |
| no tenant (`velox.ErrNoTenant`) | `UNAUTHENTICATED` | `unauthenticated` |
| a policy's denial | `FORBIDDEN` | `forbidden` |
| a validator's refusal | `BAD_USER_INPUT` | naming the input field: `UpdatePostInput.title: invalid value: must not be empty` |
| a violated constraint | `BAD_USER_INPUT` | `unique constraint violated` |
| an input of the wrong shape, an unknown field (gqlgen's validation) | `GRAPHQL_VALIDATION_FAILED` | gqlgen's, left as it is: a variable's error points at the field by its `path` (`["variable", "input", "title"]`), an inline literal's by its `locations` |
| an `Int` out of range for the field's Go type (`300` for `Label.rank`, an `int8`) | `BAD_USER_INPUT` | gqlgen's, as `300 overflows signed 8-bit integer` |
| anything else: the driver's errors, a timeout | `INTERNAL_SERVER_ERROR` | `internal error`, and the error is logged with `slog` |

Pass your own function to log internal errors elsewhere. An error that is
only the cancellation of a request whose client went away is not logged as
an internal error: `slog` records it at level debug, and your function does
not get it. Anything else that fails at the same moment is logged.

### The cost of a request

Every `veloxgql.Limits` field (`MaxPage`, `MaxList`, `MaxFilterDepth`,
`MaxFilterWidth`, `MaxFilterNodes`, `MaxOrderTerms`) bounds one argument or
one edge. The server takes the defaults; `veloxgql.WithLimits` sets others:

```go
veloxgql.NewExtension(db, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 50}))
```

A zero field is the default; a negative one is refused. They do not bound a
request: connections nested in connections multiply (each level reads up to
`MaxPage` rows for each parent), and aliases repeat a field
as often as the client writes it. So `Limits.MaxCost` (100000 by default)
bounds the whole request. The extension computes the request's cost before
any resolver runs: a field costs 1 plus its children's cost times its
multiplier, which is `first` or `last` for a connection (`MaxPage` when
neither is given), the number of ids for `nodes`, and `MaxList` for another
list of objects. Every alias and every branch of a fragment counts, except a
selection that `@skip(if: true)` or `@include(if: false)` leaves out (its
condition a literal or a variable's value, or a missing variable's default;
one whose condition is not a boolean, such as a missing variable with no
default, counts), and
introspection costs nothing. A request over
the limit fails with `COST_LIMIT_EXCEEDED` and runs no statement. Two levels
of full connection pages (about 30000) pass; three do not (about a million).
`main_test.go`'s `TestRequestCost` shows both. A list edge (one whose field
is a list, not a connection) costs `MaxList`, 1000, for each parent, so one
full page with a list edge, `posts(first: 100) { edges { node { likes { id }
} } }`, costs 100301 and is refused by default: raise `MaxCost`, or lower
`MaxList`, with `WithLimits`. gqlgen's `extension.FixedComplexityLimit` counted
fields, not rows, and is not used. Choose the limit for your schema and
clients.

### A nested request

A request that nests edges sends one statement per edge per level, however
many rows each level has. This one reads three users, the first post of each
(by title) with its count, and the tags of that post:

```graphql
query($n: Int) { users(first: $n, orderBy: {field: NAME}) { edges { node {
  name
  posts(first: 1, orderBy: [{field: TITLE}]) { totalCount edges { node { title tags { edges { node { name } } } } } }
} } } }
```

Its statements, with `n` = 3 (placeholders shown as `?`; `TestNestedEdges` and
`TestNoNPlusOne` assert them exactly, and that one user sends the same four):

```sql
-- the users page: n+1 rows, the columns asked for
SELECT "id", "name", "joined_at" FROM "users" ORDER BY "name", "id" LIMIT ?
-- the posts page of every user at once: a window per author
SELECT "id", "author_id", "title", "views", "org_id" FROM (
  SELECT "id", "author_id", "title", "views", "org_id",
         ROW_NUMBER() OVER (PARTITION BY "author_id" ORDER BY "title", "id") AS "vx_rn"
  FROM "posts" WHERE "author_id" IN (?, ?, ?) AND "org_id" = ?
) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"
-- totalCount of every user's posts: one grouped count
SELECT "author_id", COUNT(*) FROM "posts" WHERE "author_id" IN (?, ?, ?) AND "org_id" = ? GROUP BY "author_id"
-- the tags page of every post read, through the join table
SELECT "id", "name", "vx_owner" FROM (
  SELECT "tags"."id", "tags"."name", "vx_j"."post_id" AS "vx_owner",
         ROW_NUMBER() OVER (PARTITION BY "vx_j"."post_id" ORDER BY "tags"."id") AS "vx_rn"
  FROM "tags" JOIN "post_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id"
  WHERE "vx_j"."post_id" IN (?, ?, ?)
) AS "vx" WHERE "vx_rn" <= ?
```

(The listing is laid out for reading; the statements are one line each.) The
tenant condition is inside the window, so a post of another organization
takes no rank. `BenchmarkNestedRequest` runs a request of this shape over 50
users: 5 statements at 10 users and at 50, where the edges resolved one parent
at a time sent 221 and 1101. `TestNestedRequestBudget` pins the statements and
the allocations (about 45 per node returned; its budget holds under the pinned
gqlgen only).

## What it shows

| Request | Result |
|---|---|
| `node(id:)`, `nodes(ids:)` | one query per type, of the columns selected; `null` for an ID with no row, or a row of another tenant |
| an ID of another type (a `Post` ID in `authorID`) | a `BAD_USER_INPUT` error naming the expected type, and no query |
| `first`/`after`, `last`/`before` | keyset pages, `n+1` rows read for `hasNextPage`/`hasPreviousPage` |
| `orderBy`, `where` (`and`/`or`/`not`, `hasPostsWith`) | `ORDER BY … "id"`, `WHERE …`, `EXISTS (…)` |
| nested edges, `Post.tags` many-to-many | one statement per edge per level, whatever the parents: a `ROW_NUMBER()` window for the pages, a `GROUP BY` for `totalCount` |
| a page's fields | only their columns, the key, the foreign keys and the order fields' columns: never `password_hash` |
| `initials`, the application's own field on `User` (`graph/app.graphqls`) | every column, since velox-graphql cannot see what its resolver reads |
| the same edge twice under aliases, with different arguments | one statement each, each alias its own page |
| a request over `MaxCost` | a `COST_LIMIT_EXCEEDED` error naming the cost, and no statement |
| an `orderBy` list of more than `MaxOrderTerms` terms | an error naming the limit, and no statement |
| `email`, or a `where` on it, without `X-Role: admin` | a `FORBIDDEN` error; the filter runs no statement |
| `Post` with tenant 1, 2 or none | only that organization's posts, through `node`, connections, edges and `where`; `unauthenticated` without one |
| `createPost`, `updatePost`, `deletePost` | a `COUNT` per edge's IDs, then the writes; the row as written |
| `deletePost` of another tenant's post | `NOT_FOUND`, one `COUNT`, nothing deleted |
| two mutations, the second failing | both rolled back, `data` `null` |
| `createUser` setting `email` without `X-Role: admin` | `FORBIDDEN`, nothing written |

### A connection over the application's own query

`graph/app.graphqls` extends `Query` with `publishedPosts`. Its resolver
composes the connection from a query it writes, with `gen.PostsConnectionOf`
(every Node entity has one):

```go
func (r *queryResolver) PublishedPosts(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*gen.PostOrder, where *gen.PostWhereInput) (*gen.PostConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return gen.PostsConnectionOf(ctx, db.Query[gen.Post]().Where(gen.Posts.Status.EQ(gen.PostStatusPublished)), after, first, before, last, orderBy, where)
}
```

The query keeps its filters, its `With` loads and the tenant scope. Its
`Offset` is cleared and its `Limit` replaced: the page is the
connection's. It must have no `OrderBy`, which `ConnectionOf` refuses with
an internal error (`velox.Query.Ordered`), since the connection orders it.
It must also select the key and the columns `orderBy` names, which the
cursors hold: a `Columns` or `Omit` that drops one is refused with an
internal error. The page reads only the columns its selection needs, of
those the query selects.

`main_test.go` sends each over HTTP and checks the JSON response and the
statements sent exactly.

The schema declares no JSON field (contrib/graphql's end-to-end test covers
a JSON field's getter and inputs).

## Regenerate

After changing the schema, run both generators, in this order, from
`example/`:

```sh
go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema
go -C graphql tool gqlgen generate
```

gqlgen keeps the resolvers' bodies in `graph/schema.resolvers.go`, and moves
those of fields that are gone into a comment at its end. CI reruns both and
fails when a checked-in file differs.

## Newer gqlgen

The go.mod files pin gqlgen v0.17.86, which `ci-local.sh` and the `test` job
use. velox-graphql supports v0.17.86 and later, so CI has a second job,
`gqlgen-latest`, that runs `scripts/gqlgen-latest.sh`. It copies the tree,
upgrades gqlgen to the latest release, regenerates the example, then builds
and tests it. The job is red when a new gqlgen breaks velox-graphql. The script
needs only Go and git, and never touches the checkout.
