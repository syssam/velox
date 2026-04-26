# Migration — Edge Method `where` Autobind (2026-04-25)

This migration removes the `@goField(forceResolver: true)` workaround that
edge connections with `where` previously required, and lifts the `where`
parameter into the entity-level edge method itself. After this change,
edge-with-where connections autobind to the entity method just like edges
without `where` — no user-written resolver body needed.

**Why:** the previous design forced every edge connection that exposed a
`where` arg in GraphQL to be served by a hand-written resolver in the user's
gqlgen package, with a stereotypical ~5-line body delegating to
`Paginate(...)` plus a `WithXxxFilter(where.Filter)` option. That existed
solely because the entity-package method couldn't take `*filter.XxxWhereInput`
without closing the `entity → filter → query → entity` import cycle.

The cycle-break migration (2026-04-24) made `filter/` no longer depend on
`query/`, which finally lets `entity/` import `filter/`. Plan 3 then:

1. Changed the generated edge method signature to accept
   `where *filter.XxxWhereInput` directly.
2. Threaded `where.Filter` into the underlying query as an
   `entity.WithXxxPredicate` option (typed closure form `WithXxxFilter` —
   `func() (predicate.X, error)` — is kept for backcompat with hand-written
   code that uses it directly).
3. Dropped the `@goField(forceResolver: true)` directive emission for
   edge-with-where SDL fields.

After both migrations, gqlgen autobinds the SDL field straight to the
generated entity method — no resolver-interface stub, no panic stub, no
user code.

## What you need to do

1. **Bump velox** and re-run `go generate ./...` (or however your project
   runs the velox generator). This regenerates the entity package with the
   new edge method signature.

2. **Re-run gqlgen** in the package that holds your gqlgen config:

   ```bash
   gqlgen generate
   ```

   gqlgen will:
   - Notice that the resolver-interface for those edge fields has gone
     away (the SDL no longer carries `@goField(forceResolver: true)`).
   - **Not** delete your hand-written resolver methods automatically. The
     methods become unreferenced — they still compile but are dead code.
   - Move the now-unreferenced methods into a `/* WARNING ... */` block at
     the bottom of `schema.resolvers.go` so you can review them.

3. **Delete the dead resolver stubs.** For each edge connection that
   previously required a hand-written resolver, you can delete the stub
   safely. Stubs that follow the canonical pattern below are now
   completely redundant — the entity method does exactly the same thing,
   including the eager-load fast path:

   ```go
   // DELETE — autobind takes over
   func (r *userResolver) Todos(
       ctx context.Context, obj *entity.User,
       after *gqlrelay.Cursor, first *int,
       before *gqlrelay.Cursor, last *int,
       orderBy *entity.TodoOrder,
       where *filter.TodoWhereInput,
   ) (*entity.TodoConnection, error) {
       if where == nil && after == nil && before == nil {
           if nodes, err := obj.Edges.TodosOrErr(); err == nil {
               return entity.BuildTodoConnection(nodes, 0, orderBy, after, first, before, last), nil
           }
       }
       q := r.Client.Todo.Query().Where(todo.HasOwnerWith(user.IDField.EQ(obj.ID)))
       return q.Paginate(ctx, after, first, before, last,
           entity.WithTodoOrder(orderBy),
           entity.WithTodoFilter(where.Filter))
   }
   ```

   You may also delete the now-unused per-entity resolver structs and the
   `(*Resolver).User()` / `(*Resolver).Category()` etc. methods if no
   methods remain on them. gqlgen does this for you on the next run after
   the dead methods are gone.

4. **Custom resolver bodies are still supported.** If your hand-written
   stub did anything other than the canonical pattern above (e.g. extra
   authz, custom join, instrumentation), keep it. The SDL no longer
   carries `forceResolver`, but you can opt back in by adding the
   directive in your local schema overlay or by writing the method with
   a different name and aliasing it. For most users this is unnecessary —
   the entity method is the right hook for customization via velox hooks
   and interceptors.

## Hand-written entity-method calls

If you have Go code (tests, services, batch jobs) that calls the
generated edge method directly — outside gqlgen — the signature has
changed:

```go
// BEFORE
conn, err := user.Todos(ctx, after, first, before, last, orderBy)

// AFTER — pass nil (or a *filter.TodoWhereInput) for the new where arg
conn, err := user.Todos(ctx, after, first, before, last, orderBy, nil)
```

The compiler catches every call site, so the migration is mechanical.

## Why this is safe

The pre-Phase-C implementation was a workaround for a package cycle.
Phase C/D removes the workaround now that the cycle is gone. The runtime
behavior is identical — the same `WithXxxPredicate` plumbing the
hand-written `WithXxxFilter(where.Filter)` invoked. Both are still
exported; the closure form is preserved for backcompat.

### 2026-04-27 — `WithXxxFilter` typed

`WithXxxFilter`'s `any` parameter was retyped to
`func() (predicate.X, error)`. Caller code (`WithXxxFilter(where.Filter)`)
is unchanged because `where.Filter` is already a method value of that
exact shape. The change closes the last `any`-erased seam in the
pagination API — callers now get a compile-time type error if they pass
the wrong shape, instead of a runtime "invalid filter type" panic. The
`any` was a cycle-era artifact; once the entity → filter cycle was
broken, the closure type could be named directly.

## Compared to Ent

Ent puts every edge method, predicate, query, and gqlfilter type in a
single root `ent/` package, so the entity method can take
`*TodoWhereInput` directly without any cycle worry. Velox's per-entity
sub-package layout (chosen for build performance at 100+ entity scale)
required the cycle-break migration before this signature became
possible. After Plan 3, the user-facing surface matches Ent: zero lines
per where-carrying edge connection.
