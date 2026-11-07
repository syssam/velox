# graphql

A GraphQL server with gqlgen over velox entities: a `User` writes `Post`s,
each post belongs to an organization and carries `Tag`s, a
many-to-many edge, which the GraphQL schema calls `Label`.

```sh
cd examples
go run ./graphql            # serves http://localhost:8080/graphql
# X-Org-ID picks the organization, for this demo only (see below)
curl -s -H 'Content-Type: application/json' -H 'X-Org-ID: 1' \
  -d '{"query":"{ posts(first: 2) { totalCount edges { node { title author { name } } } } }"}' \
  localhost:8080/graphql
# X-User is the viewer, createPost's author, for this demo only too
curl -s -H 'Content-Type: application/json' -H 'X-Org-ID: 1' -H 'X-User: 2' \
  -d '{"query":"mutation { createPost(input: {title: \"Hello\"}) { title author { name } } }"}' \
  localhost:8080/graphql
```

## What the application writes

1. The schema, `schema/schema.go`, with `graphql.*` annotations from
   `github.com/syssam/velox/contrib/graphql`: `QueryField` makes a root
   connection, `OrderField` an `orderBy` value, `WhereOps` and `WhereEdge`
   the predicates of the `where` input, `MultiOrder` a list `orderBy`, and
   `Type` renames an entity's GraphQL type (`Tag` is `Label`), and
   `Mutations` the mutation inputs (`CreatePostInput`, `UpdatePostInput`,
   `CreateLabelInput`, `UpdateLabelInput`, `CreateUserInput`,
   `UpdateUserInput`), which the application's own mutations take. A `Sensitive`
   field (`password_hash`) is in no type, where input or order. velox-graphql
   would still put it in the inputs, write-only, and hidden from their
   `String`; here `graphql.Skip` leaves it out, so a client never sends a hash
   it chose. `createUser` and `updateUser` take a `password` instead, which
   the resolver hashes (`graph/password.go`, PBKDF2-HMAC-SHA256 from the
   standard library) and sets on the row.
   The author edge has `graphql.Skip(graphql.SkipMutationCreateInput,
   graphql.SkipMutationUpdateInput)`: neither post input has `authorID`, the
   create resolver sets the author to the viewer, and no update gives a post
   to another author. The `org_id`
   column is `graphql.Skip()`ped: it is in no type or input. `title` is `NotEmpty()`, a validator the
   inputs' `Setters` run before any statement, and the tag's `name` is `Unique()`.
   `views` is `Optional()`: `CreatePostInput` may leave it out, and it is then 0,
   its zero value. `published_at` is `Optional().Nillable()`: `Optional`
   makes it optional on create, and `Nillable` makes its column NULL-able and
   the Go field a `*time.Time` with a `Null*` handle, so a draft's
   `publishedAt` is `null` and `publishedAtIsNil` filters on it. velox-gen refuses `Optional()`
   alone on a time field without a Default: its unset value, the zero time,
   would go into a NOT NULL column, which MySQL's strict mode refuses.
   `email` has `graphql.Directives(graphql.Directive{Name: "admin"})`: see
   below.
2. velox-gen with the plugin, which writes `gen/`: the entities, the GraphQL
   schema `gen/velox.graphql`, and the `*_gql.go` files it binds to (IDs,
   connections, edges, orders, where inputs, mutation inputs with their
   `Setters` method, `Node` and `Nodes`):

   ```sh
   go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema
   ```
3. `graph/app.graphql`, the application's own schema: `type Mutation`,
   with `createPost`, `updatePost`, `deletePost`, `createLabel`,
   `updateLabel`, `createUser` and `updateUser`, which velox-graphql leaves to
   it, as entgql does; and its own query and field (below). `gqlgen.yml`:
   the schema files, `autobind` to `gen`, and `models` binding `Node` and the
   scalars to `veloxgql`.
4. gqlgen, which writes `graph/generated.go` and `graph/models_gen.go`, and
   the resolver stubs once:

   ```sh
   cd graphql && go tool gqlgen generate
   ```
5. The root query's and mutations' resolvers in `graph/schema.resolvers.go`,
   most of them one call each:

   ```go
   func (r *queryResolver) Node(ctx context.Context, id string) (veloxgql.Noder, error) {
   	return gen.Node(ctx, id)
   }

   func (r *queryResolver) Users(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *gen.UserOrder, where *gen.UserWhereInput) (*gen.UserConnection, error) {
   	return gen.NewUserConnection(ctx, veloxgql.DB(ctx).Query[gen.User](), after, first, before, last, orderBy, where)
   }

   func (r *mutationResolver) CreatePost(ctx context.Context, input gen.CreatePostInput) (*gen.Post, error) {
   	viewer, err := Author(ctx)
   	if err != nil {
   		return nil, err
   	}
   	set, err := input.Setters()
   	if err != nil {
   		return nil, err
   	}
   	x := &gen.Post{AuthorID: viewer}
   	if err := veloxgql.DB(ctx).Insert(x).Set(set...).Exec(ctx); err != nil {
   		return nil, err
   	}
   	return x, nil
   }

   func (r *mutationResolver) UpdatePost(ctx context.Context, id string, input gen.UpdatePostInput) (*gen.Post, error) {
   	db := veloxgql.DB(ctx)
   	key, err := OwnPost(ctx, db, id)
   	if err != nil {
   		return nil, err
   	}
   	set, err := input.Setters()
   	if err != nil {
   		return nil, err
   	}
   	x, err := db.Update[gen.Post]().Where(gen.Posts.ID.EQ(key)).Set(set...).One(ctx)
   	if velox.IsNotFound(err) {
   		return nil, NotFound(ctx, "Post")
   	}
   	return x, err
   }
   ```

   An input's `Setters` turns its fields and edges into setters, and does no
   I/O; the resolver writes them. The insert takes the row with what the
   client must not choose already on it (the author). It trusts that row's
   own fields, foreign keys included, so `Author` checks first that the
   viewer is a user. `veloxgql.DB(ctx)` is the request's DB, which the
   extension puts there, in the mutation's transaction. The update's
   `One` writes the one row and returns it as written, `velox.ErrNotFound`
   when it names none the request may change, which the resolver shows as
   `NOT_FOUND`; `updateUser` appends a setter of its own, the password's hash, to the
   input's. `OwnPost` lets only a post's author change it (`FORBIDDEN`
   otherwise). velox-graphql generates no
   delete: `DeletePost` is the application's, `OwnPost` and a scoped
   `db.Delete[gen.Post]()`, in a savepoint, `NOT_FOUND` when it deleted no
   row and an internal error when the driver reports no count. The post's
   join rows go with it by `post_tags`' `ON DELETE CASCADE`: `main.go` opens
   SQLite with foreign keys on (`_pragma=foreign_keys(1)`).

   `NewXConnection` is named after the GraphQL type, as every name `gen`
   declares is: `gen.NewLabelConnection` for the `labels` field of the type
   `Label` (the entity `Tag`), on `db.Query[gen.Tag]()`. The connection is
   lazy: it sends nothing until gqlgen resolves its edges, pageInfo or
   totalCount.

   And `User.initials`, the application's own field (`graph/app.graphql`
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

   Every other field binds to `gen`'s methods: an edge is the method of its
   name on the entity, `Post.Author(ctx)`, as entgql's is, and `id` is
   `GlobalID()`; connections are lazy (`totalCount` alone runs one
   `COUNT`, and no page query).
6. `main.go`: gqlgen's server with `veloxgql.NewExtension(db)` first, which
   puts the DB and the limits in each operation's context (and runs a
   mutation in one transaction), `veloxgql.ErrorPresenter`, a limit on the
   cost of a request, middlewares that put the organization, the role and the
   viewer in the context, and the directive `@admin`.

### Mutations

`createX(input:)` inserts a row with its input's setters and links the edges
its input names (`tagIDs`). `updateX(id:, input:)` changes only the fields
given: a field left out, or given as `null`, is unchanged;
`clearPublishedAt: true` sets a NULL-able column to NULL; and an edge is
cleared, then has its `remove…IDs` removed, then its `add…IDs` added. `deletePost(id:)`
deletes the row, and with it its join rows, and returns its ID. Only a
post's author, the `X-User`, may update or delete it. Repeated IDs count
once, and adding a link that exists changes nothing.

Every ID to set or add is checked against what the caller may read: the
organization scope and the entity's Read policy (the `InOrg` mixin's
policy and `Post`'s own). An ID of another organization's row, or
of one a policy hides, is `NOT_FOUND`, as a missing one is, so a client
cannot tell that a row exists. An ID to remove is not checked, and neither
it nor `clearTags` or `clearPosts` touches a link to a row the caller may
not read: `updateLabel(id:, input: {clearPosts: true})` unlinks the
caller's organization's posts from the label, and another organization's
stay linked. `deletePost` of another organization's post is `NOT_FOUND`, as
its check of the author is scoped to the organization, and its join rows
are left alone.

The validators (`title`'s `NotEmpty()`) run on the fields an input sets in
its `Setters`, before any statement: `createPost` with an empty title is
`BAD_USER_INPUT`, naming `title`, whatever its IDs name. An ID that names no
row the caller may read fails the write with a `*velox.EdgeError`, which
`veloxgql.ErrorPresenter` shows as `NOT_FOUND` naming the edge.

**One transaction per operation.** `veloxgql.NewExtension` runs a mutation
operation in one transaction, and rolls it back when the response has any
error. In `mutation { a: createLabel(…) b: createLabel(…) }`, when `b` fails
(its name is taken), `a`'s row is rolled back too, and `data` is `null`.
Inside that transaction an insert or an update with edge setters runs in a
savepoint, so an edge's error rolls back its row at once; one of columns
alone is one statement. A resolver that returns the error, as this
example's do, fails the operation, and the transaction rolls back all of
it; a resolver that swallows the error keeps the operation's other writes,
and the operation commits. Outside a server, or in an operation
`WithTxFilter` leaves out, a write with edge setters runs in a transaction
of its own, so it is never half-written either.

**The row returned.** `createX` returns the row as written.
`UpdateQuery.One` reads its row back by its key without the policies, the
organization scope among them: the caller just wrote it, so an update that
moves the row out of the caller's Read view still returns it.

**Write permission.** An update that changes only edges (`addTagIDs` and
nothing else) still needs permission to write the row: velox reads the rows
it matches under the organization and the Read and Write policies before it
writes their edges, so no edge changes on a row the caller cannot write.

### The organization comes from a header only in this demo

`orgFromHeader` takes the organization from the client's `X-Org-ID` header, so
that you can try organizations with `curl`. That is for this demo only: a client
that chooses its own organization can read every organization's rows. A real server
derives the organization from what authenticates the request (a verified session
or token), and ignores any header the client sets for it.

### A field for admins only

`User.email` carries the application's directive `@admin`, which
`graph/app.graphql` declares and `main.go` implements: it lets the field
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
organization, a client would read `velox: gen.Post: select denied by policy: velox/scope: the context has no scope value: org`.
`ErrorPresenter` shows only what is safe, with a code in
`extensions.code`:

| The error | `extensions.code` | `message` |
|---|---|---|
| a bad ID, cursor, paging argument, filter or scalar | `BAD_USER_INPUT` | what was expected, as `ID: malformed ID: not canonical base64url without padding` |
| an ID in a mutation that names no row the caller may see or change | `NOT_FOUND` | `UpdatePostInput: no Post has this ID`; `deletePost`'s own `no Post has this ID` |
| a conflicting update input (`publishedAt` with `clearPublishedAt`), too many IDs | `BAD_USER_INPUT` | naming the input field |
| no organization (`scope.ErrNoScope`) | `UNAUTHENTICATED` | `unauthenticated` |
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
as often as the client writes it. So `Limits.MaxCost` (150000 by default)
bounds the whole request. The extension computes the request's cost before
any resolver runs: a field costs 1 plus its children's cost times its
multiplier, which is `first` or `last` for a connection's `edges` (`MaxPage`
when neither is given), the number of ids for `nodes`, and `MaxList` for another
list of objects. On top of that, with no multiplier, a field that runs a
statement adds 100, so `first: 0` and aliases count and the default allows 1500
statements, and an input object such as a `where` adds its size at each use.
Every alias and every branch of a fragment counts, except a
selection that `@skip(if: true)` or `@include(if: false)` leaves out (its
condition a literal or a variable's value, or a missing variable's default;
one whose condition is not a boolean, such as a missing variable with no
default, counts); the value above it is read all the same, and costs as if it
selected `__typename`. Introspection costs one for each field of its response, and
answers to its own `Limits.MaxIntrospectionCost` (250000 by default): GraphiQL's
query costs 3478 here, and about 1400 for each rich entity. A request over
the limit fails with `COST_LIMIT_EXCEEDED` and runs no statement. Two levels
of full connection pages (about 20000) pass; three do not (about two million).
`main_test.go`'s `TestRequestCost` shows both. A list edge (one whose field
is a list, not a connection) costs `MaxList`, 1000, for each parent, so one
full page with two list edges, `posts(first: 100) { edges { node { a: likes { id }
b: likes { id } } } }`, costs 200602 and is refused by default: raise `MaxCost`, or lower
`MaxList`, with `WithLimits`. gqlgen's `extension.FixedComplexityLimit` counted
fields, not rows, and is not used. Choose the limit for your schema and
clients.

gqlgen validates a request before any of this, and validation is quadratic in the
fields that share a response name, and other stages grow with spreads, names, depth
and variables. So the extension parses a request first, and refuses one over
`Limits.MaxTokens` (15000), `MaxFields` (2000), `MaxFragments` (500), `MaxDepth` (64),
`MaxNameBytes` (128), `MaxLiteralBytes` (64 KiB) or `MaxVariableNodes` (25000), or with
more than 5 times `MaxFields` pairs that differ under one response name, with
`BAD_USER_INPUT`. `main.go` calls `veloxgql.Setup`, which does what a server needs: it
uses the extension and `ErrorPresenter`, stops validation at `MaxErrors` (100) errors,
shows no suggestions, and the handler it returns refuses a body over
`Limits.MaxBodyBytes` (1 MiB). `bounded_test.go` asserts what each shape allocates, and
the most a request can, about 290 MB at `MaxCost`, is real work charged to the cost.

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
organization condition is inside the window, so a post of another organization
takes no rank. `BenchmarkNestedRequest` runs a request of this shape over 50
users: 5 statements at 10 users and at 50, where the edges resolved one parent
at a time sent 221 and 1101. `TestNestedRequestBudget` pins the statements and
the allocations (about 45 per node returned; its budget holds under the pinned
gqlgen only).

## What it shows

| Request | Result |
|---|---|
| `node(id:)`, `nodes(ids:)` | one query per type, of the columns selected; `null` for an ID with no row, or a row of another organization |
| an ID of another type (a `Post` ID in `authorID`) | a `BAD_USER_INPUT` error naming the expected type, and no query |
| `first`/`after`, `last`/`before` | keyset pages, `n+1` rows read for `hasNextPage`/`hasPreviousPage` |
| `orderBy`, `where` (`and`/`or`/`not`, `hasPostsWith`) | `ORDER BY … "id"`, `WHERE …`, `EXISTS (…)` |
| nested edges, `Post.tags` many-to-many | one statement per edge per level, whatever the parents: a `ROW_NUMBER()` window for the pages, a `GROUP BY` for `totalCount` |
| a page's fields | only their columns, the key, the foreign keys and the order fields' columns: never `password_hash` |
| `initials`, the application's own field on `User` (`graph/app.graphql`) | every column, since velox-graphql cannot see what its resolver reads |
| the same edge twice under aliases, with different arguments | one statement each, each alias its own page |
| a request over `MaxCost` | a `COST_LIMIT_EXCEEDED` error naming the cost, and no statement |
| a query or variables over a document limit (`MaxTokens`, `MaxFields`, `MaxFragments`, `MaxDepth`, `MaxNameBytes`, `MaxLiteralBytes`, `MaxVariableNodes`), or a body over `MaxBodyBytes` | `GRAPHQL_PARSE_FAILED` over the tokens, otherwise `BAD_USER_INPUT` naming the limit, and nothing validated or run |
| an `orderBy` list of more than `MaxOrderTerms` terms | an error naming the limit, and no statement |
| `email`, or a `where` on it, without `X-Role: admin` | a `FORBIDDEN` error; the filter runs no statement |
| `Post` with organization 1, 2 or none | only that organization's posts, through `node`, connections, edges and `where`; `unauthenticated` without one |
| `createPost`, `updatePost` | a `COUNT` per edge's IDs, then the writes; the row as written |
| `createPost` without `X-User`, or with one that names no user | `UNAUTHENTICATED`, nothing written; an `authorID` is no field of the input |
| `updatePost`, `deletePost` of another user's post | `FORBIDDEN`, nothing written; `UNAUTHENTICATED` without `X-User` |
| `deletePost` of another organization's post | `NOT_FOUND`, one scoped read of its author, nothing deleted |
| two mutations, the second failing | both rolled back, `data` `null` |
| `createUser` setting `email` without `X-Role: admin` | `FORBIDDEN`, nothing written |

### A connection over the application's own query

`graph/app.graphql` extends `Query` with `publishedPosts`. Its resolver
pages a query it writes with `gen.NewPostConnection`, as the `posts` field
pages every post:

```go
func (r *queryResolver) PublishedPosts(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*gen.PostOrder, where *gen.PostWhereInput) (*gen.PostConnection, error) {
	return gen.NewPostConnection(ctx, veloxgql.DB(ctx).Query[gen.Post]().Where(gen.Posts.Status.EQ(gen.PostStatusPublished)), after, first, before, last, orderBy, where)
}
```

The query keeps its filters, its `With` loads and the organization scope. Its
`Offset` is cleared and its `Limit` replaced: the page is the
connection's. It must have no `OrderBy`, which `NewPostConnection` refuses with
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
`examples/`:

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
