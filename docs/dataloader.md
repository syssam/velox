# DataLoader Integration

DataLoaders solve the N+1 query problem in GraphQL resolvers. The `contrib/dataloader` package provides generic utilities that work with any DataLoader library.

---

## The N+1 Problem

In a GraphQL API, resolvers typically run independently. When a list resolver returns N users and each user resolver fetches that user's posts separately, you get N+1 database queries:

```
Query: { users { posts { title } } }

→ SELECT * FROM users                          (1 query)
→ SELECT * FROM posts WHERE user_id = 1        (1 per user)
→ SELECT * FROM posts WHERE user_id = 2
→ SELECT * FROM posts WHERE user_id = 3
...                                            (N more queries)
```

A DataLoader batches these into a single query:

```
→ SELECT * FROM users
→ SELECT * FROM posts WHERE user_id IN (1, 2, 3, ...)  (1 query total)
```

---

## Setup

Install a DataLoader library. Velox's utilities are designed to work with `github.com/vikstrous/dataloadgen`, which uses Go generics:

```bash
go get github.com/vikstrous/dataloadgen
```

They also work with `github.com/graph-gophers/dataloader/v7` and any other library that follows a similar batch function pattern.

---

## Creating a Loader

### One-to-One (Fetch by Primary Key)

Use `dataloader.OrderByKeys` to reorder query results to match the input key order. DataLoaders require the result slice to be the same length as the input keys and in the same order.

```go
import (
    "context"

    "github.com/vikstrous/dataloadgen"
    "github.com/syssam/velox/contrib/dataloader"
    "yourapp/velox"
    "yourapp/velox/user"
)

// userBatchFn loads a batch of users by their IDs.
func userBatchFn(ctx context.Context, ids []int) ([]*velox.User, []error) {
    users, err := client.User.Query().
        Where(user.IDIn(ids...)).
        All(ctx)
    if err != nil {
        // Return the same error for all keys in the batch
        errs := make([]error, len(ids))
        for i := range errs {
            errs[i] = err
        }
        return nil, errs
    }
    return dataloader.OrderByKeys(ids, users, func(u *velox.User) int {
        return u.ID
    })
}

// Create the loader (per-request, not shared across requests)
userLoader := dataloadgen.NewLoader(userBatchFn,
    dataloadgen.WithWait(2*time.Millisecond),
)

// Use it in a resolver
user, err := userLoader.Load(ctx, userID)
```

`OrderByKeys` builds a lookup map from the query results and returns them in the order of the input `ids` slice. For any ID that had no matching row, the corresponding error slot contains `dataloader.ErrNotFound`.

### Handling Missing Entities

`OrderByKeys` returns `([]V, []error)` where the error slice uses `dataloader.ErrNotFound` for missing keys. If missing entities are acceptable (optional foreign keys, for example), use `OrderByKeysNoError`:

```go
func optionalTagBatchFn(ctx context.Context, ids []int) ([]*velox.Tag, []error) {
    tags, _ := client.Tag.Query().Where(tag.IDIn(ids...)).All(ctx)
    // Returns zero values for missing IDs without errors
    result := dataloader.OrderByKeysNoError(ids, tags, func(t *velox.Tag) int {
        return t.ID
    })
    return result, make([]error, len(ids))
}
```

---

## One-to-Many Loading

For relationships where multiple entities share a foreign key, use `GroupByKey` to group results by parent ID and `OrderGroupsByKeys` to return them in input order.

```go
// postsByUserBatchFn loads all posts for a batch of user IDs.
func postsByUserBatchFn(ctx context.Context, userIDs []int) ([][]*velox.Post, []error) {
    posts, err := client.Post.Query().
        Where(post.UserIDIn(userIDs...)).
        All(ctx)
    if err != nil {
        errs := make([]error, len(userIDs))
        for i := range errs {
            errs[i] = err
        }
        return nil, errs
    }

    // Group posts by user ID
    grouped := dataloader.GroupByKey(posts, func(p *velox.Post) int {
        return p.UserID
    })

    // Return slices in the same order as userIDs
    result := dataloader.OrderGroupsByKeys(userIDs, grouped)
    return result, make([]error, len(userIDs))
}

postsLoader := dataloadgen.NewLoader(postsByUserBatchFn,
    dataloadgen.WithWait(2*time.Millisecond),
)

// In a User resolver
posts, err := postsLoader.Load(ctx, user.ID)
```

For users with no posts, `OrderGroupsByKeys` returns an empty (nil) slice for that user, not an error.

---

## Context Integration

Loaders should be created per-request so each request gets fresh, isolated caches. Store them in the request context using `dataloader.WithLoaders` and retrieve them with `dataloader.For`.

### Define Your Loaders Struct

```go
// loaders/loaders.go
package loaders

import (
    "context"
    "time"

    "github.com/vikstrous/dataloadgen"
    "github.com/syssam/velox/contrib/dataloader"
    "yourapp/velox"
)

// Loaders holds all DataLoaders for a single request.
type Loaders struct {
    User  *dataloadgen.Loader[int, *velox.User]
    Posts *dataloadgen.Loader[int, []*velox.Post]
}

// New creates a fresh set of loaders for a request.
func New(client *velox.Client) *Loaders {
    return &Loaders{
        User:  dataloadgen.NewLoader(newUserBatchFn(client), dataloadgen.WithWait(2*time.Millisecond)),
        Posts: dataloadgen.NewLoader(newPostsBatchFn(client), dataloadgen.WithWait(2*time.Millisecond)),
    }
}

func newUserBatchFn(client *velox.Client) dataloadgen.BatchFunc[int, *velox.User] {
    return func(ctx context.Context, ids []int) ([]*velox.User, []error) {
        users, err := client.User.Query().Where(user.IDIn(ids...)).All(ctx)
        if err != nil {
            errs := make([]error, len(ids))
            for i := range errs {
                errs[i] = err
            }
            return nil, errs
        }
        return dataloader.OrderByKeys(ids, users, func(u *velox.User) int { return u.ID })
    }
}

// For retrieves the loaders from context.
func For(ctx context.Context) *Loaders {
    return dataloader.For[*Loaders](ctx)
}
```

### HTTP Middleware

Attach a new `Loaders` instance to each incoming request:

```go
func DataLoaderMiddleware(client *velox.Client) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            l := loaders.New(client)
            ctx := dataloader.WithLoaders(r.Context(), l)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

Register this middleware before your GraphQL handler.

### Using Loaders in Resolvers

```go
func (r *queryResolver) User(ctx context.Context, id int) (*velox.User, error) {
    return loaders.For(ctx).User.Load(ctx, id)
}

func (r *userResolver) Posts(ctx context.Context, obj *velox.User) ([]*velox.Post, error) {
    return loaders.For(ctx).Posts.Load(ctx, obj.ID)
}
```

### Checking Loader Availability

If loaders might not be wired up in all code paths (e.g., background jobs), use `dataloader.ForOK`:

```go
l, ok := dataloader.ForOK[*loaders.Loaders](ctx)
if !ok {
    // Fall back to a direct query
    return client.User.Get(ctx, id)
}
return l.User.Load(ctx, id)
```

---

## Utilities Reference

All functions are in `github.com/syssam/velox/contrib/dataloader`.

| Function | Description |
|----------|-------------|
| `OrderByKeys(keys, values, keyFn)` | Reorders `values` to match `keys` order. Returns `([]V, []error)` where missing keys get `ErrNotFound`. |
| `OrderByKeysNoError(keys, values, keyFn)` | Same as `OrderByKeys` but returns zero values for missing keys without errors. |
| `GroupByKey(values, keyFn)` | Groups values into a `map[K][]V` by extracting a key from each value. |
| `OrderGroupsByKeys(keys, groups)` | Converts a `map[K][]V` into a `[][]V` ordered to match `keys`. |
| `WithLoaders(ctx, loaders)` | Injects a loaders struct into the context. |
| `For[T](ctx)` | Retrieves the loaders struct from context. Returns zero value if not present. |
| `ForOK[T](ctx)` | Retrieves the loaders struct from context. Returns `(T, false)` if not present. |
| `PrimeMany(cache, values, keyFn)` | Primes multiple values into a DataLoader cache after a mutation. |
| `ClearMany(cache, keys)` | Clears multiple keys from a DataLoader cache after a mutation. |
| `Results(values, errs)` | Combines separate value and error slices into `[]BatchResult[V]`. |
| `NewBatchResult(value, err)` | Creates a single `BatchResult[V]`. |

---

## Testing

### Unit Testing a Batch Function

```go
func TestUserBatchFn(t *testing.T) {
    // Set up a test database (e.g., SQLite in memory)
    client := enttest.Open(t, "sqlite", "file:ent?mode=memory&_pragma=foreign_keys(1)")
    defer client.Close()

    // Create test data
    u1 := client.User.Create().SetName("Alice").SaveX(t.Context())
    u2 := client.User.Create().SetName("Bob").SaveX(t.Context())

    batchFn := newUserBatchFn(client)
    results, errs := batchFn(t.Context(), []int{u2.ID, u1.ID})

    require.NoError(t, errs[0])
    require.NoError(t, errs[1])

    // Results must be in the same order as input keys
    assert.Equal(t, u2.ID, results[0].ID)
    assert.Equal(t, u1.ID, results[1].ID)
}

func TestUserBatchFnMissingID(t *testing.T) {
    client := enttest.Open(t, "sqlite", "file:ent?mode=memory&_pragma=foreign_keys(1)")
    defer client.Close()

    u := client.User.Create().SetName("Alice").SaveX(t.Context())

    batchFn := newUserBatchFn(client)
    results, errs := batchFn(t.Context(), []int{u.ID, 99999})

    require.NoError(t, errs[0])
    assert.Equal(t, u.ID, results[0].ID)
    assert.ErrorIs(t, errs[1], dataloader.ErrNotFound)
}
```

### Testing GroupByKey

```go
func TestGroupByKey(t *testing.T) {
    type Post struct {
        ID     int
        UserID int
    }

    posts := []Post{
        {ID: 1, UserID: 10},
        {ID: 2, UserID: 10},
        {ID: 3, UserID: 20},
    }

    grouped := dataloader.GroupByKey(posts, func(p Post) int { return p.UserID })

    assert.Len(t, grouped[10], 2)
    assert.Len(t, grouped[20], 1)
    assert.Nil(t, grouped[30]) // user with no posts
}
```

### Testing Context Integration

```go
func TestLoaderMiddleware(t *testing.T) {
    client := openTestClient(t)
    handler := DataLoaderMiddleware(client)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        l, ok := dataloader.ForOK[*loaders.Loaders](r.Context())
        if !ok {
            t.Error("loaders not in context")
        }
        if l.User == nil {
            t.Error("user loader is nil")
        }
        w.WriteHeader(http.StatusOK)
    }))

    req := httptest.NewRequest(http.MethodGet, "/", nil)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    assert.Equal(t, http.StatusOK, rec.Code)
}
```
