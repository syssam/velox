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
   `entity.WithXxxFilter(where.Filter)` option (the typed closure form,
   `func() (predicate.X, error)`, matches `where.Filter`'s method-value
   shape directly — Paginate invokes the closure once at apply time and
   propagates any error).
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
behavior is identical — the generated body and the (now-removed) hand-
written stub both routed through the same `WithXxxFilter(where.Filter)`
plumbing. After the 2026-04-27 collapse, `WithXxxFilter` is the sole
public option, matching Ent's `gql_edge.go` uniform pattern.

### 2026-04-27 — `WithXxxFilter` typed

`WithXxxFilter`'s `any` parameter was retyped to
`func() (predicate.X, error)`. Caller code (`WithXxxFilter(where.Filter)`)
is unchanged because `where.Filter` is already a method value of that
exact shape. The change closes the last `any`-erased seam in the
pagination API — callers now get a compile-time type error if they pass
the wrong shape, instead of a runtime "invalid filter type" panic. The
`any` was a cycle-era artifact; once the entity → filter cycle was
broken, the closure type could be named directly.

### 2026-04-27 — `WithXxxPredicate` removed (single-option API)

`WithXxxPredicate` (added briefly in Plan 3 Phase B as a typed escape
hatch for the generated entity edge method body) has been removed. The
sole public option for threading a predicate into Paginate is now
`WithXxxFilter(where.Filter)` — matching Ent's `gql_edge.go` uniform
pattern exactly.

**Why collapse:**

- The Predicate field on `XxxPagerConfig` and the WithXxxPredicate option
  were duplicates of the closure form once `WithXxxFilter` was typed
  (`func() (predicate.X, error)`); the only difference was that
  `WithXxxFilter` defers the resolve-or-error decision by one frame.
- Coexistence had a silent-double-filter footgun: if both `Filter` and
  `Predicate` were set on the same config (e.g. by mixing options across
  callers), both predicates were applied additively (AND-combined) with
  no test, no docs, no warning.
- The generated entity edge method now passes `where.Filter` directly
  through `WithXxxFilter` instead of resolving the predicate at the call
  site; semantics are identical (Paginate's body invokes the closure
  exactly once), output is shorter, and the public surface area is the
  same as Ent.

**What you need to do:** nothing. `WithXxxPredicate` was never used
outside the generator's own output. Hand-written code uses
`WithXxxFilter(where.Filter)` (the canonical form documented in the
Phase D resolver template above) and is unaffected.

**If you somehow had a direct call to `WithXxxPredicate`:** replace
`WithXxxPredicate(pred)` with `WithXxxFilter(func() (predicate.X, error) { return pred, nil })`,
or migrate the call site to a `*XxxWhereInput` and pass `where.Filter`
directly.

**Brief co-existence window:** `WithXxxPredicate` shipped between
2026-04-25 (Plan 3 Phase B) and 2026-04-27 (this collapse). If you
pinned to a velox version inside that two-day window and adopted the
new option, the migration above is the only change needed.

## Compared to Ent

Ent puts every edge method, predicate, query, and gqlfilter type in a
single root `ent/` package, so the entity method can take
`*TodoWhereInput` directly without any cycle worry. Velox's per-entity
sub-package layout (chosen for build performance at 100+ entity scale)
required the cycle-break migration before this signature became
possible. After Plan 3, the user-facing surface matches Ent: zero lines
per where-carrying edge connection.
