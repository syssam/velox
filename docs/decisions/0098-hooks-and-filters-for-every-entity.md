# 98. Hooks and row filters for every entity

Status: accepted, 2026-11-21.

## Context

ent's `client.Use(hooks...)` adds a hook to every entity's mutations, and
`client.Intercept(interceptors...)` adds query middleware to every entity's
reads. Each dispatches on `m.Type()` or `q.Type()`, the entity's name.
nextapp registers three interceptors this way (soft delete, permission
conditions, row scope) and four hooks (an access-policy check, two
custom-field hooks and a refund-status hook). velox had per-entity
`WithHooks[E]`, schema hooks and Interceptors, and the low-level
`WithRewrite`, which sees statements, not rows, and no entity name to
dispatch on. The gap analysis
(`docs/superpowers/specs/2026-11-21-veloxe-gap-analysis.md` §2.1 item 3)
asked for both at run time.

## Decision

- `velox.WithHooksAll(hs ...Hook) Option`. The hooks run on every entity's
  writes, outermost: before the schema's hooks (mixins' then the entity's)
  and before `WithHooks[E]`'s, in the order given. They follow every rule
  of `Hook`: transaction, errors (`velox: <Entity>: hook: <err>`), dry runs
  and change recording. A nil hook panics, as `WithHooks`'s does.
- `Mutation.Entity() string` names the write's entity: its Go type's name
  without the package, which for a generated entity is the schema's name
  (`"Invoice"`), as ent's `Mutation.Type` gives. Mutation had no name of
  its own; `untyped.MutationTable` gives a Table, not a name.
- `velox.WithFilter(f Filter) Option`, with
  `type Filter func(ctx context.Context, entity string, op Op) (Predicate, error)`
  and the same entity name. The engine keeps the filters in the DB's
  config, shared by its Tx and shards, and the scope calls them in
  `RowCond`, where it calls an entity's interceptors, before them. Every
  place that asked "does this entity have interceptors?" asks "does it
  have interceptors, or does the DB have filters?" (`Scoper.filtered`). So
  a filter reaches exactly what an Interceptor reaches, by construction:
  every read, update, delete and Save, every With load, Join ON, `Has` and
  `HasWith` EXISTS (`Select`/`InSelect` subqueries included, 0057), Count,
  aggregate and page, and an upsert's update branch. `WithoutPolicy` does
  not lift it.
- What a filter returns is checked as an interceptor's is
  (`Scoper.filterCond`): an error fails the statement and is not a denial;
  the zero `Predicate` fails it, so a filter that forgot an entity fails
  closed; a column the entity lacks fails it; `velox.NoFilter()` is no
  condition. `NoFilter` is `Allow` by the name a filter reads best with:
  a function, as `Allow` is, so no one can reassign it.
- The filter is given the op. For the statement's own entity it is the
  statement's op: `OpSelect`, `OpUpdate` (an update, a Save, a Writable
  read), `OpDelete`, and `OpInsert` for an upsert's update branch. It is
  `OpSelect` for every entity the statement reaches as a target: With,
  Join, `Has`, `HasWith`, subqueries. Without it a filter could not keep
  veloxe's behaviour: veloxe's interceptors wrap queries only, and nextapp's
  permission interceptor reads its read conditions. A soft-delete filter
  over every op breaks a restore: nextapp's email handler reads a deleted
  user with the deleted included, then clears `deleted_at` under the plain
  context, and that update would match 0 rows and fail silently. So a
  filter that narrows reads only returns `NoFilter()` when op is not
  `OpSelect`. The fail-closed rules do not depend on op.
- A soft delete (`schema/mixin/softdelete`) sends an UPDATE, which
  `engine.MarkSoftDelete` marks. `scope()` gives the filter `OpDelete` for
  it, so a filter that restricts deletes is not bypassed by a soft delete,
  which as `OpUpdate` it would be: that failed open. The entity's policies
  still see `OpUpdate`, as the mixin's godoc says, and a restore stays
  `OpUpdate`. A read-only filter is unaffected.
- A `WithHooksAll` hook runs outside the mixin's hook, so a soft delete
  reaches it twice: as the delete (`OpDelete`), then as the mixin's update
  inside it (`OpUpdate`). That is how hooks compose with a mixin that
  issues its own write, and it stays. `m.Op()` tells the two apart; the
  inner update's `m.Value("deleted_at")` is a time, a restore's is nil.
- The filter is given the statement's context. It runs at most once per
  statement for each entity and op, except where a relation loops back
  (0091). `RowCond`'s per-statement memo is keyed by entity and op, so a
  self-relation UPDATE with an EXISTS on its own entity gets both
  conditions, the update's on the row and the read's inside the EXISTS. On
  a DB with no filters the key is the entity alone, as before. velox keeps
  nothing across statements, so a filter that reads the context is right
  in every call.
- Several filters AND, in registration order; their conditions come before
  the interceptors' and the Read policy's.
- A join entity of a many-to-many edge is an entity: the filter is called
  for it where its rows are a statement's own (a With load through it, the
  join-row writes of `Rel.Add`, `Remove`, `Clear`), and not inside a `Has`
  through it, which reads it through its two sides. A filter that returns
  `NoFilter` for names it does not know keeps the interceptor rule that a
  join entity is read through its sides; the godoc asks for it. An
  interceptor on a join entity stays refused: a schema can be checked when
  it is built, a run-time filter only when it runs.
- MySQL's `ON DUPLICATE KEY UPDATE` takes no condition. An upsert that
  updates is refused there with `ErrPolicyUpsert` when a filter returns a
  condition for its entity under `OpInsert`, and runs when every filter
  returns `NoFilter`. An interceptor or a Read/Write policy still refuses
  it without asking the filters. A multi-row upsert on a DB with filters
  runs in a transaction, as one on an entity with an interceptor does.

## Consequences

- ent's client-wide hooks and interceptors port one to one, with the
  entity name to dispatch on. velox's filter is a row filter, not query
  middleware: it is asked for writes as well as reads, and narrows reads
  only when it returns `NoFilter()` for other ops, as ent's query-only
  interceptors do. It cannot add an ORDER BY or a LIMIT.
- `Meta.HasRowFilter` and `untyped.Table.HasRowFilter` are about the
  schema: they know no DB, so they do not see `WithFilter`. velox-graphql's
  where filter, which reads them to treat a foreign key whose target is
  hidden as null, does not see a run-time filter on the target.
- No change to generated code, and none to `EntityInfo`'s export data.
