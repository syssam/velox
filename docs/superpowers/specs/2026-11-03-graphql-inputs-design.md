# GraphQL mutations: generate inputs, not mutations

Date: 2026-11-03. Controller's design (the user asked to "improve" after comparing
with entgql; decisions are the controller's, see the autonomy memory).

## Why

The user compared velox-graphql's mutations with entgql's and found ours ugly:

- velox-graphql writes the root `type Mutation { createX updateX deleteX }` and sealed
  Go functions `gen.CreateX(ctx, input)`. The application cannot rename a mutation,
  return a payload, add an argument or a directive, or set a field the client must
  not choose. In `examples/graphql`, `CreatePostInput.authorID` comes from the client
  and the resolver cannot force it to the viewer: a real security smell.
- entgql generates no delete: there is no input to generate, and what a delete means
  (soft delete, cascade, payload) is the application's.
- Our mutation Go is heavy: ~210 lines per entity, everything inline (decode, check,
  transaction, link, unlink, read back, a name switch per input).

Measured at 100 entities (scale schema of `cmd/velox-graphql/scale_e2e_test.go`,
compile of one package via `-toolexec`, Apple M3 Max):

| | gen (velox) | graph (gqlgen) |
|---|---|---|
| no mutations | 25.8 s CPU, 1.83 GB RSS, 43.6k lines | 21.8 s CPU, 1.56 GB, 134k lines |
| create+update+delete | 32.7 s CPU, 2.33 GB, 64.4k lines | 28.7 s CPU, 2.13 GB, 172k lines |

## Decision

### SDL

- velox-graphql writes `CreateXInput` and `UpdateXInput` only, with the fields they
  have today. It writes no `type Mutation` and no mutation field.
- `graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)` means "generate
  these inputs", as entgql's `entgql.Mutations` does. `graphql.MutationDelete` is
  removed (pre-1.0: a compile error, the CHANGELOG says what to write instead).
- The application declares its mutations in its own `.graphql`, e.g.
  `createPost(input: CreatePostInput!): Post!`, with any name, argument name,
  payload or directive.
- sdl rules that reserved the names `createX`/`updateX`/`deleteX` or checked the
  Mutation type go; rules about the inputs stay.

### Go: the input and two methods

Per input, velox-graphql writes the struct (fields unchanged) and one method:

```go
// Create inserts x with the input's fields set on it ... x's other fields are
// inserted as the caller set them: a resolver sets there what the client must
// not choose. nil x is a new Post. Returns x as written.
func (in *CreatePostInput) Create(ctx context.Context, x *Post) (*Post, error)

// Update updates the Post with the ID id from the input, then set, the
// application's own setters (updated_by...), validated with the input's.
func (in *UpdatePostInput) Update(ctx context.Context, id string, set ...velox.Setter[Post]) (*Post, error)
```

- The `set func(*E)` parameter of today's `CreateX` for left-out required fields is
  replaced by x. velox-gen's left-out-field doc moves to `Create`'s doc.
- `String`/`GoString` are written only for an input that has a sensitive field.
- One `<entity>InputGQL(goField string) string` name function per entity, shared by
  create and update (today there are two identical switches). If the two inputs ever
  name a Go field differently, keep two; otherwise one.
- No `DeleteX`. The docs (package doc "Mutations", examples/graphql, coming-from-ent)
  show the application's delete resolver: `veloxgql.DecodeID`, then
  `db.Delete[gen.Post]().Where(gen.Posts.ID.EQ(k)).Exec(ctx)`, `n == 0` → not found
  (a gqlimpl/veloxgql exported helper for the NOT_FOUND error is fine if one is
  needed for a coded error; keep it tiny). Join rows go by the FK's ON DELETE CASCADE
  (velox-gen writes it on join tables, as ent).

### Go: a generic runtime does the work

The per-entity code becomes a short description; `gqlimpl` executes it. Same
pattern as where inputs (`gqlimpl.NewFilter` + one call per field, method values
passed as call arguments, never stored in struct fields by generated code: a Go 1.27
ICE). Target shape (names are guidance; the implementer picks final ones, in velox's
naming style):

```go
func (in *CreatePostInput) Create(ctx context.Context, x *Post) (*Post, error) {
	c, x := gqlimpl.NewCreate(ctx, "CreatePostInput", x, postInputGQL, Posts.Title)
	x.Title = in.Title
	if in.Status != nil {
		x.Status = *in.Status
		c.Given(Posts.Status)
	}
	...
	gqlimpl.Ref(c, &x.AuthorID, "authorID", in.AuthorID, "User", Users.ID.EQ)
	gqlimpl.Through(c, "tagIDs", in.TagIDs, "Label", Tags.ID.In, func(x *Post, k int64) PostTag { return PostTag{PostID: x.ID, TagID: k} }, PostTags.PostID, PostTags.TagID)
	return c.Insert()
}
```

Every helper records the first error (decode errors, NotZero/NotNull...) and
registers closures that run inside the transaction (checks before the insert,
links after it). `Insert`/`Exec` runs validation, then `Atomic`, then the closures
in today's order, then the read-back. Column handles are all `struct{ I int32 }`;
where a helper needs many operations of one column (re-pointing a target-held key
needs Set, SetNull, IsNull, NotNull, EQ, NEQ), it may take the handle as a value of
a `~struct{ I int32 }` type parameter and convert it to the matching velox handle
type, rather than six method values.

**Every semantic of today's generated code is kept**, because each was reviewed and
tested: decode before any statement (and MaxPage on ID lists), validate before any
statement with the input's field names in errors, the savepoint per mutation via
Atomic, CheckExists of linked IDs under the target's Read policy, Repointed /
StillLinked (the ID-oracle closing), only re-pointing a child no owner holds, M2M
unlink scoped by Readable targets, UnlinkAll ordering, DoNothing on join inserts,
bidi edges, Disjoint add/remove, clear-with-value refusals, NullGiven / NotNull /
NullEdge, NotZero for non-zero defaults, LockRow when an update sets nothing, -1
row counts, the read-back without the Read policy, named key types (DecodeIDAs and
conversions), JSON fields (FromJSON), composite-key refusals. The existing e2e tests
(`cmd/velox-graphql/mutation_e2e_test.go`) are the contract: port them to the new
API (`input.Create(ctx, x)`, `input.Update(ctx, id)`), drop only the DeleteX tests,
and keep every other assertion.

### Explicit null when the app names the argument

`NullGiven`, `NotNull` and `NullEdge` look the input up in the field's arguments by
the argument name `"input"`. The application now names it. They find the input
instead by its GraphQL input type name (search the field's argument values,
recursively through objects and lists, for values whose definition is the input
type). Exactly one occurrence: as today. None (called from Go, not GraphQL): no
explicit null, as today outside a request. More than one (a list of inputs, two
arguments): an explicit null for a clearable field in any of them is refused with
BAD_USER_INPUT naming the `clearX` field to use; no explicit null anywhere is fine.

## Out of scope

- gqlgen's generated.go per declared mutation is gqlgen's; apps declare only what
  they serve. Not generating delete removes its share.
- The query side's build cost (gen alone is 1.83 GB at 100 entities without
  mutations) is a separate follow-up.
- No core (`velox`) API change.

## Done when

- Generated SDL: inputs, no Mutation. Generated Go: inputs + Create/Update, no
  DeleteX, no per-edge inline transaction code.
- `examples/graphql` declares its mutations in `graph/app.graphql`; `createPost`
  sets the author from the viewer (header-based, like `@admin`) and its
  `CreatePostInput` leaves `authorID` out with
  `graphql.Skip(graphql.SkipMutationCreateInput)` on the edge or field;
  `deletePost` is a hand-written resolver.
- Ported e2e tests green on SQLite, PostgreSQL, MySQL (`scripts/ci-local.sh`).
- Remeasured at 100 entities with the app declaring create+update for each entity;
  numbers recorded in the ADR.
- ADR 0033, CHANGELOG (Changed/Removed, with the migration recipe), package doc
  "Mutations", README / coming-from-ent, public API snapshot updated.
