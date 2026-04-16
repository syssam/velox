# edge-schema

M2M relationship with an intermediate entity that carries extra fields — the classic Django-style "membership table." Here: `User ⇆ Group via Membership`, where each membership records `role` and `joined_at`.

## Run

```bash
go run generate.go
go test ./...
```

## What it shows

- **`Through("memberships", Membership.Type)`** on both sides of the M2M declares that the join is backed by a first-class entity, not an anonymous table
- **Extra fields on the relationship:** `role` (enum), `joined_at` (auto-filled timestamp) — queryable and type-safe, unlike a plain join table
- **Field() on the join's edges:** `edge.To("user", User.Type).Unique().Required().Immutable().Field("user_id")` — binds the edge to an explicit FK column on Membership
- **Filtering by relationship attributes:** `Where(membership.RoleField.EQ(membership.RoleOwner))` — something you can't do with an implicit M2M

## Schema topology

```
User ─── Membership ─── Group
         (role, joined_at)
```

Each side declares `Through()`:

```go
// User
edge.To("groups", Group.Type).Through("memberships", Membership.Type)

// Group
edge.From("users", User.Type).Ref("groups").Through("memberships", Membership.Type)
```

And the Membership entity declares its two FK edges explicitly:

```go
// Membership
edge.To("user", User.Type).Unique().Required().Immutable().Field("user_id")
edge.To("group", Group.Type).Unique().Required().Immutable().Field("group_id")
```

`Immutable()` on both edges + FK fields signals that once a membership is created, the user and group are fixed — role and joined_at can evolve, but the relationship identity cannot.

## When to use

- The relationship itself has state (timestamps, roles, quantities, weights)
- You need to query by relationship attributes (`WHERE role = 'owner'`)
- The relationship should participate in auditing, hooks, or privacy rules

For a plain "User belongs to Groups" without extra fields, use a simple M2M without `Through()` — see the `basic/` example for that pattern.
