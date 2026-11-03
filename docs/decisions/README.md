# Decisions

Each record is a binding decision of velox's design, with its context, its
consequences and where it is enforced: a test, the code, or the spec line it
came from. The specs in [docs/history/specs](../history/specs/) and the plans in
[docs/history/plans](../history/plans/) hold every ruling; these are the ones a user or a
contributor needs to know.

| Record | Decision |
| --- | --- |
| [0001](0001-ent-names.md) | Tables, columns, indexes, constraints and inflection follow ent's, so an ent database baselines without renames. |
| [0002](0002-no-inflect-dependency.md) | Plural's rules are copied from go-openapi/inflect v0.19.0 into the stdlib-only plugin package, and the naming helpers' outputs never change within a major version. |
| [0003](0003-nullability.md) | Optional stays NOT NULL, as in veloxe; Nillable makes a column NULL-able. |
| [0004](0004-versioning.md) | Semantic versioning from 1.0, except generated code: its version constant is replaced in a minor release; schema.json and velox.json read older versions and refuse newer ones. |
| [0005](0005-deprecation.md) | Nothing is deprecated before 1.0; after 1.0, `// Deprecated:` until the next major version. |
| [0006](0006-own-migrate-and-atlas.md) | velox keeps its own planner and ships `migrate sql` for Atlas; a directory is managed by one of them. |
| [0007](0007-policies-decide-hooks-query.md) | Policies decide from the context and the row; a rule that must see other rows is a hook. |
| [0008](0008-write-policies-filter.md) | A Write policy filters: a write to rows it hides affects 0 rows, not an error. |
| [0009](0009-mutations-return-as-written.md) | A GraphQL mutation returns the row as written; a many-to-many link needs Read on the target, a link from the owner that sets its key Write. |
| [0010](0010-savepoint-per-mutation.md) | Each GraphQL mutation runs in a savepoint of the operation's transaction. |
| [0011](0011-bidirectional-edges.md) | A self edge with no inverse is bidirectional, as in ent: links and unlinks write both directions. |
| [0012](0012-drift-never-hides.md) | Drift never hides a real difference, except a cast whose meaning depends on the column's type. |
| [0013](0013-version-floors.md) | migrate needs PostgreSQL 14, MySQL 8.4 or SQLite 3.35. |
| [0014](0014-with-stays-a-method.md) | Query.With and Rel.With stay methods: as functions they would save 1638 bytes, under 1%. |
| [0015](0015-contrib-graphql-path.md) | The module path stays contrib/graphql, imported as `vgraphql` beside gqlgen's graphql. |
| [0016](0016-withtenant-converts.md) | Superseded by 0029: `WithTenant` converted a predeclared integer or a string to the tenant column's type. |
| [0017](0017-upsert-skips-updatedefault.md) | An upsert's update branch does not fire UpdateDefault, as in veloxe and ent. |
| [0018](0018-pgx-stdlib-scan-wrapper.md) | On pgx's stdlib driver, scans go through a sql.Scanner, so pgx's per-column type check passes at its first step: 81% less time for a 100-row query with time columns (the before and after runs had different loads). |
| [0019](0019-veloxpgx.md) | pgx's native path is the package veloxpgx in this module; the core reads pgx's errors by type name, so it links no driver. |
| [0020](0020-root-and-engine.md) | The root package is the generic API and `internal/engine` is the rest: the root's `DB` holds an `engine.DB`, its public names are aliases, and each test lives by the code it tests. |
| [0021](0021-veloxtype.md) | The plain data types are in the public package `veloxtype`, which the root re-exports, so their fields and methods have real documentation; what reaches a private field stays in the engine. |
| [0022](0022-group-by.md) | Grouped aggregates take `handle.Key()`, a generic method, since an ordinary method or a phantom field costs every entity package; a group list obeys `Unbounded` and the row cap; NULL keys are nil, NULL sums 0, and an empty group has no average, least or greatest. |
| [0023](0023-module-path.md) | The module is `github.com/syssam/velox`; the earlier project is veloxe, and its tags are never reused |
| [0024](0024-public-surface.md) | The public API is pinned by a snapshot; `migrate.Change` is opaque; `internal/engine` exports only what crosses it |
| [0025](0025-connect.md) | `velox.Connect` opens a database in one call; `DB.Close` closes only what it opened; `velox.Pool` is the `*sql.DB` under it |
| [0026](0026-migration-safety.md) | `Up` refuses to destroy what the model cannot hold: a SQLite rebuild and a MySQL `MODIFY COLUMN` are checked against the database at apply time, and `-- velox:allow unmodeled-loss` accepts the loss. |
| [0027](0027-graphql-limits.md) | The cost charges statements and introspection's response; a Required edge is nullable; the generator refuses what could only fail later; an explicit zero value on a Default is refused in GraphQL and stays unset in the core |
| [0028](0028-runtime-edges.md) | The runtime's edges fail or say so: After needs its order columns selected, a nested Tx's error means its work is undone, SQLite enforces TxReadOnly and writes velox's time form, a statement inside Iter on a pool of one fails rather than hang, a MySQL deadlock breaks its transaction |
| [0029](0029-scope-replaces-tenant.md) | The core has no tenant: a scope is a `Policy` plus a hook from `velox/scope`, `WithoutPolicy` lifts it, the scope column is `Immutable`, and `WithShardFor` reads the context. |
| [0030](0030-generated-scope.md) | A scoped column is marked `schema.Scoped(key)` in the schema; velox-gen writes the key (`OrgKey`) and each entity's scope into `scope_velox.go` and binds it around the entity's own `Policy` and `Hooks`, so it cannot be dropped. |
| [0031](0031-to-one-joins.md) | A query joins a to-one relation (`Join`, `LeftJoin`): the related entity is filled from the same row, `rel.Is` and `rel.By` filter and order by its columns, and the target's Read policy is in the ON clause. A to-many relation is refused. |
| [0032](0032-ent-refusals.md) | What ent users write is generated: a zero Default, graphql.CreateDefault for a non-zero one, an OrderField on a nullable field with NULL last on every dialect, JSON lists as GraphQL lists; a Uint64 over 2^63-1 says why it is refused |
| [0033](0033-graphql-inputs-not-mutations.md) | velox-graphql generates `Create<T>Input` and `Update<T>Input` with their methods `Create(ctx, x)` and `Update(ctx, id, set...)`, run by a generic gqlimpl runtime, and no `type Mutation` and no delete: the application declares its mutations, as with entgql. |
| [0034](0034-schema-declares-behaviour.md) | An entity's `Policy` and `Hooks` are declared on its schema, typed with gen; velox-gen writes `gen/runtime` to bind them, an entity fails at first use when it is not linked, and the loader compiles the schema without them, so `gen/` is all generated and a stale one never blocks generation. |
| [0035](0035-untyped-runtime-for-generated-graphql.md) | Generated GraphQL code runs on a non-generic gqlimpl over velox's untyped `Table` surface, "For generated code.", which builds the typed API's statements through the same engine and refuses another entity's conditions, rows and out-of-range values; a 100-entity `gen` compiles in 1.7 s instead of 7.2 s. |
