# Decisions

Each record is a binding decision of velox2's design, with its context, its
consequences and where it is enforced: a test, the code, or the spec line it
came from. The specs in [docs/specs](../specs/) and the plans in
[docs/plans](../plans/) hold every ruling; these are the ones a user or a
contributor needs to know.

| Record | Decision |
| --- | --- |
| [0001](0001-ent-names.md) | Tables, columns, indexes, constraints and inflection follow ent's, so an ent database baselines without renames. |
| [0002](0002-no-inflect-dependency.md) | Plural's rules are copied from go-openapi/inflect v0.19.0 into the stdlib-only plugin package, and the naming helpers' outputs never change within a major version. |
| [0003](0003-nullability.md) | Optional stays NOT NULL, as in old velox; Nillable makes a column NULL-able. |
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
| [0016](0016-withtenant-converts.md) | WithTenant converts a predeclared integer or a string to the tenant column's type; a named id never converts. |
| [0017](0017-upsert-skips-updatedefault.md) | An upsert's update branch does not fire UpdateDefault, as in old velox and ent. |
| [0018](0018-pgx-stdlib-scan-wrapper.md) | On pgx's stdlib driver, scans go through a sql.Scanner, so pgx's per-column type check passes at its first step: 81% less time for a 100-row query with time columns (the before and after runs had different loads). |
| [0019](0019-veloxpgx.md) | pgx's native path is the package veloxpgx in this module; the core reads pgx's errors by type name, so it links no driver. |
| [0020](0020-root-and-engine.md) | The root package is the generic API and `internal/engine` is the rest: the root's `DB` holds an `engine.DB`, its public names are aliases, and each test lives by the code it tests. |
| [0021](0021-dbtype.md) | The plain data types are in the public package `dbtype`, which the root re-exports, so their fields and methods have real documentation; what reaches a private field stays in the engine. |
