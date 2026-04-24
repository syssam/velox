# Migration — Cycle Break (2026-04-24)

This migration restructures the generated code layout so that the per-entity
leaf package (`{entity}/`) becomes a true leaf (schema metadata + predicates
only) and the heavy generated code (client, CRUD builders, mutations, runtime,
filter, gql_mutation_input) moves into a sibling sub-package
`client/{entity}/` declared as `package {entity}client`.

**Why:** break the `entity/ → filter/ → query/ → entity/` import cycle that
previously blocked `(c *entity.Category).Todos(ctx, ..., where *filter.TodoWhereInput)`
from being a real method. After this migration:

- `filter/` no longer imports `query/`. `Filter` returns a predicate rather
  than taking a `*XxxQuery`.
- `entity/` can safely import `filter/` (the payoff — arriving in a follow-up
  plan).
- The `{entity}/` leaf is now a true leaf (schema constants, predicates,
  enum types) that nothing generated imports back into.

**Layout before and after:**

```
BEFORE                                AFTER
ent/                                  ent/
├── entity/                           ├── entity/
├── query/                            ├── query/
├── predicate/                        ├── predicate/
├── filter/  (imports query/)         ├── filter/  (NO query/ import)
└── user/                             ├── user/                # TRUE LEAF
    ├── user.go                       │   ├── user.go
    ├── where.go                      │   └── where.go
    ├── client.go        ─────→       └── client/
    ├── create.go                         └── user/            # package userclient
    ├── update.go                             ├── client.go
    ├── delete.go                             ├── create.go
    ├── mutation.go                           ├── update.go
    ├── runtime.go                            ├── delete.go
    ├── filter.go                             ├── mutation.go
    └── gql_mutation_input.go                 ├── runtime.go
                                              ├── filter.go
                                              └── gql_mutation_input.go
```

## Step 1 — Regenerate

```bash
go run path/to/your/generate.go
```

If you use GraphQL via `contrib/graphql`, also re-run gqlgen:

```bash
go install github.com/99designs/gqlgen@latest
cd path/to/your/project && gqlgen generate
```

## Step 2 — Sweep stale files from leaf directories

The velox generator's cleanup is manifest-based: it can only remove files
listed in the *previous* regen's manifest. Files emitted by the pre-migration
generator (`{entity}/client.go`, etc.) are not in the new manifest, so
they're not auto-cleaned. Run this once:

```bash
#!/usr/bin/env bash
# Save as scripts/sweep-cycle-break.sh and run from your project root.
# Removes pre-migration heavy files from {entity}/ leaves.
set -euo pipefail

VELOX_DIR="${1:-velox}"   # directory velox generates into

for sub in "$VELOX_DIR"/*/; do
  name=$(basename "$sub")
  case "$name" in
    # known non-entity dirs — skip
    client|entity|query|predicate|filter|hook|intercept|internal|migrate|privacy|schema)
      continue ;;
  esac
  # Keep only the leaf files; delete everything else (they now live in client/{entity}/).
  find "$sub" -maxdepth 1 -name '*.go' \
      ! -name "$name.go" \
      ! -name "where.go" \
      ! -name "gql_collection.go" \
      ! -name "gql_node.go" \
      -delete
done

echo "Sweep complete. Re-run your generator and build to verify."
```

Or inline:

```bash
find velox -mindepth 2 -maxdepth 2 -name "client.go" -delete
find velox -mindepth 2 -maxdepth 2 -name "create.go" -delete
find velox -mindepth 2 -maxdepth 2 -name "update.go" -delete
find velox -mindepth 2 -maxdepth 2 -name "delete.go" -delete
find velox -mindepth 2 -maxdepth 2 -name "mutation.go" -delete
find velox -mindepth 2 -maxdepth 2 -name "runtime.go" -delete
find velox -mindepth 2 -maxdepth 2 -name "filter.go" -delete
find velox -mindepth 2 -maxdepth 2 -name "gql_mutation_input.go" -delete
# Re-run generate to restore anything that should be there.
go run ./generate.go
```

**Warning:** the sweep looks at all subdirectories of `velox/`. If you
have handwritten Go files in a leaf named anything other than
`{entity}.go`, `where.go`, `gql_collection.go`, or `gql_node.go`, they
will be deleted. Convention is that handwritten code lives outside the
`velox/` tree.

## Step 3 — Update handwritten entity client references

Search your codebase for calls that reach into a leaf package for heavy
types. These types moved to `client/{entity}/`:

| Before | After |
|---|---|
| `user.NewUserClient(cfg)` | `userclient.NewUserClient(cfg)` |
| `user.UserClient` | `userclient.UserClient` |
| `user.UserCreate` | `userclient.UserCreate` |
| `user.UserCreateBulk` | `userclient.UserCreateBulk` |
| `user.UserUpdate` / `UserUpdateOne` | `userclient.UserUpdate` / `UserUpdateOne` |
| `user.UserDelete` / `UserDeleteOne` | `userclient.UserDelete` / `UserDeleteOne` |
| `user.UserMutation` | `userclient.UserMutation` |
| `user.CreateUserInput` / `UpdateUserInput` | `userclient.CreateUserInput` / `UpdateUserInput` |

Add the import:

```go
// new import
userclient "example.com/app/velox/client/user"
```

**Keep the leaf import** — predicates (`user.IDField`, `user.NameField`,
`user.HasPostsWith(...)`) and enum values (`user.RoleAdmin`) still live in
the leaf.

Most project code reaches these via the root client (`client.User.Create()`),
which doesn't need to change — the field is still named `User`, only the
underlying type moved. Direct package-level calls (common in tests and
background jobs that get a `runtime.Config` and bypass the root client)
need the rename.

## Step 4 — Update entity-prefixed enum references

Phase A of this refactor moved enum TYPES out of `entity/` and into the
`{entity}/` leaf. Enum names lost their entity prefix.

| Before | After |
|---|---|
| `entity.UserRoleAdmin` | `user.RoleAdmin` |
| `entity.UserRoleValues()` | `user.RoleValues()` |
| `entity.TodoStatusPending` | `todo.StatusPending` |
| `entity.TodoStatus("in_progress")` | `todo.Status("in_progress")` |

(In practice: strip the entity prefix from the Go identifier, drop the
import of `{proj}/velox/entity` **for enum references**, add the leaf
import.)

Note: `entity.User`, `entity.Todo`, etc. (the entity struct types
themselves) did NOT move — they still live in `entity/`. Only enum
TYPE names changed.

## Step 5 — Update Filter callers (if using gqlfilter/contrib/graphql)

The `Filter` method on `*XxxWhereInput` changed signature to break
the `filter/ → query/` cycle:

```go
// BEFORE — took and returned a *XxxQuery
func (w *UserWhereInput) Filter(q *query.UserQuery) (*query.UserQuery, error)

// Caller pattern (before):
q, err := where.Filter(q)

// AFTER — returns a predicate
func (w *UserWhereInput) Filter() (predicate.User, error)

// Caller pattern (after):
p, err := where.Filter()
if err != nil { return err }
if p != nil { q.Where(p) }
```

For pagination filter options (`entity.WithXxxFilter(...)`):

```go
// BEFORE
filter := entity.WithUserFilter(func(q *query.UserQuery) (*query.UserQuery, error) {
    q.Where(user.ActiveField.EQ(true))
    return q, nil
})

// AFTER
filter := entity.WithUserFilter(func() (predicate.User, error) {
    return user.ActiveField.EQ(true), nil
})
```

If you were passing `where.Filter` as a method value to `entity.WithXxxFilter`
(e.g., in hand-written gqlgen resolvers), **no change is needed** — the
method value's new signature matches the new expected type.

## Step 6 — Update `gqlgen.yml` autobind (if using gqlgen)

The `CreateXxxInput` / `UpdateXxxInput` structs moved from
`velox/{entity}/` to `velox/client/{entity}/`. gqlgen autobind needs
both locations:

```yaml
autobind:
  - example.com/app/velox
  - example.com/app/velox/entity
  - example.com/app/velox/filter
  # existing leaf packages
  - example.com/app/velox/user
  - example.com/app/velox/post
  # ADD the matching client sub-packages:
  - example.com/app/velox/client/user
  - example.com/app/velox/client/post
```

After updating, re-run `gqlgen generate`.

## Step 7 — Verify

```bash
go build ./...
go test ./...
```

Both should be clean.

## Known limitations after migration

1. **`Filter()` returns only a predicate — no query preloads.** The previous
   `Filter(q *XxxQuery)` could add `WithXxx(...)` eager loads. The new form
   returns only a predicate. If you need filter-driven preloads, apply them
   at the call site after `q.Where(p)`.

2. **`tests/integration/.velox-manifest` (or your project's equivalent) is
   per-checkout state** and should be gitignored. Different team members'
   regens may leave different orphan files; if someone else's regen emitted
   a file yours doesn't, the sweep script in Step 2 handles it.

3. **Renaming `EntityPkgPath` → `LeafPkgPath`.** If you had custom
   `gen.GeneratorHelper` implementations (rare — most users don't), the
   interface method renamed from `EntityPkgPath` to `LeafPkgPath`. Same
   behavior, clearer name.

## Questions to check before declaring done

- [ ] `go build ./...` is clean.
- [ ] `go test ./...` passes.
- [ ] `golangci-lint run ./...` is clean.
- [ ] `go list -deps ./velox/filter/` shows no `velox/query` in the output.
- [ ] No leftover files in `velox/{entity}/` other than `{entity}.go`,
      `where.go`, `gql_collection.go`, `gql_node.go`.

If all six check out, the migration is complete.
