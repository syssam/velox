# 109. Edge schemas

Status: accepted, 2026-11-22.

## Context

ent's `edge.Through(name, T)` makes a many-to-many relation's links rows of
a schema of their own, an edge schema, with fields, indexes, hooks and
privacy. ent ports and veloxe applications use it for memberships and
followers with a role or a date. velox wrote a join entity for every
many-to-many relation, keyed by its two columns, with no fields of its own
(0041). Its Rel setters (0044) already wrote the join rows as entity rows,
through the entity pipeline. The gap analysis lists the edge schema as row
#46.

## Decision

- `Through(name, T)` is on `edge.To` and `edge.From`, as in ent. The edge
  schema is a schema like any other: its fields, edges, indexes, mixins,
  hooks and Insert and Write policies are its own. Its two key fields are
  ordinary fields, each bound by a Required to-one edge.
- Resolution follows ent's. The relation's table is the edge schema's
  table. Each of the two default column names is matched to the edge
  schema's edge bound to a field of that name, else to its one edge to the
  side's type. velox-gen adds the edge `Through` names to the owner: an O2M
  to the edge schema, whose `Ref` names the key edge. The key edge does not
  name it back. No join entity is written.
- The edge schema is keyed by its own `id`. velox-gen adds a unique index over the two keys, named as any index is
  (0090), unless the schema declares one, unique and over every row, on the
  two columns in either order. `Add`'s `ON CONFLICT` names it.
- velox-gen refuses, at the edge's line, as numbered rules:
  - 63: `Through` on an edge that is not many-to-many.
  - 64: `Through` names a type that is not a schema of the package, the
    edge's own schema, or its target's.
  - 65: `Through`'s name is empty, or taken by an edge or a field of the
    owner, or by their Go names.
  - 66: a second edge names the same edge schema, or an inverse's `Through`
    names another type than its assoc's.
  - 67: the edge schema has no edge to a side bound to the column's field;
    a key edge is not Required, or is one-to-one; or the schema declares an
    index over every row on the two columns, in their order, that is not
    unique, which would stand where the pair's index goes.
  - 68: the edge has a StorageKey (`edge.Table`, `edge.Columns`,
    `edge.Symbols`): the table and columns are the edge schema's.
  - 69: `Through` on a bidirectional edge, an M2M to its own schema with no
    inverse. The workaround is an inverse edge.
  - 70: the edge schema has a Read or Query policy, Interceptors or
    `softdelete.Mixin`, its own or a mixin's. `Has` and `With` through the
    relation read its rows through the two sides and would not apply them.
    The engine refuses such a relation when it resolves, for a policy
    velox-gen cannot read. A `WithFilter` filter that returns anything but
    `NoFilter` for a join entity or an edge schema fails the statements
    that read through it, as amended in 0098.
- `velox.EdgeThrough(..., unfilled ...int)` lists the edge schema's
  columns an insert would write as a zero that the schema calls required:
  no Default, DefaultFunc, Version or Nillable, and not Optional. A
  SchemaDefault fills a column only when it covers every dialect. `Add`
  on such a relation fails before any statement:
  `Add cannot fill Membership.Note, which has no default; insert Membership
  rows, or give it a Default or Optional`. Entities and fields are named by
  their Go names. There is no setter list on `Add` in this version.
- `Add` inserts the edge schema's rows with their defaults, through the
  entity pipeline, and keeps a link that exists. `Remove` and `Clear`
  delete rows through it too. The edge schema's hooks run on both, and a
  link its Write policy hides stays through `Clear`. ent fills only
  defaults on `AddGroupIDs`, and runs neither hooks nor privacy.
- velox-gen warns:
  - of an edge schema whose key edges declare no `OnDelete`: its keys get
    no action, where a plain join table's are `ON DELETE CASCADE`;
  - on MySQL, of each unique key of the edge schema other than the pair's:
    `ON DUPLICATE KEY UPDATE` applies to every unique key, so a new link
    that collides with another row only on that key is dropped without an
    error. The warning is left out when `Add` cannot fill the schema.
- The plugin model gains `Entity.EdgeSchema`, `Edge.Through` and
  `Edge.Unfilled`; an M2M edge's `Join` names its edge schema. ModelVersion
  stays 3. velox-graphql gives the edge schema a type like any schema's.
  It writes no input that adds links to an M2M edge whose edge schema has
  a column `Add` cannot fill, and warns of each such edge.
- Converting a plain join table into an edge schema is not planned by
  `migrate diff`. Keeping the table's name changes its primary key, which
  diff refuses (`primary-key`). A new table means dropping the join table,
  which diff refuses without `-allow drop-table=...`. With it, one hand-added
  `INSERT ... SELECT` copies the links (migrations-operations.md).
- `GeneratedCodeIsVersion26` and `loadjson.Version` 9. Output for a schema
  without `Through` is byte-identical but for the version constant.

## Deferred

- Composite keys, ent's `field.ID("user_id", "group_id")`. velox has no
  `field.ID`, so a port that uses it fails to compile. They need an entity
  whose key is two columns in the Graph, the plugin model and velox-graphql,
  where `Fields[0]` is the ID today. With them, converting a join table
  would add columns only. ADR 0110 is left for them.
- Bidirectional self edges through an edge schema (rule 69): velox's
  bidirectional mirror needs the two key columns as the primary key.
- A Read or Query policy, interceptors or soft delete on an edge schema,
  applied inside `Has` and loading (rule 70).
- `Rel.AddWith(keys, setters...)`, which would set an edge schema's
  columns on `Add`.
- GraphQL inputs that take an edge schema's fields on an M2M add.

## Consequences

- An ent schema with `Through` ports as written, but for `field.ID`, a
  bidirectional `Through`, and behaviour that narrows reads.
- An edge schema costs one more column and one more index per link than a
  join table.
- Compile cost, measured once with
  `go run ./bench/scale -n 100 -s 1 -m2m 20` and
  `go run ./bench/scale -n 100 -s 1 -m2m 20 -through`, 3 runs each,
  alternating. Each probe adds 20 M2M edges. With `-through`, each is
  through an edge schema with one extra field. Ratios are to the reference
  build of the same run:

  | | cold build | edit, regenerate, rebuild |
  | --- | --- | --- |
  | 20 plain M2M | 0.89x / 0.82x / 0.92x | 0.21x / 0.21x / 0.22x |
  | 20 edge schemas | 0.89x / 0.89x / 0.89x | 0.23x / 0.24x / 0.24x |

  - The cold build differs by less than the run-to-run spread (0.82x to
    0.92x).
  - The rebuild after an edit is about 10% more. It regenerates 20 more
    schemas: edge schemas are schemas, where join entities were not.
  - The probe puts `Through` on the owner's edge alone. An inverse with
    `Through` adds a through edge to the target as well, so these numbers
    are a lower bound.
  - `scripts/scale-check.sh` at n=500 stays within its thresholds: cold
    build 6.47x, edit and rebuild 5.97x.

## Evidence

- `schema/edge`: `Through` on both builders, its builder errors and an
  `Example`. `schema/load` carries it.
- `internal/codegen`: `TestEdgeSchemaRules` and `TestEdgeSchemaRulePos`
  (rules 63–69, with their text and line), `TestEdgeSchemaGraph`,
  `TestEdgeSchemaMatchByType`, `TestEdgeSchemaSelf`,
  `TestEdgeSchemaDeclaredIndex`, `TestEdgeSchemaOnDelete`,
  `TestEdgeSchemaBehaviourRefused` (rule 70), `TestEdgeSchemaWarnings`, the
  `edgeschema` render golden and `TestPluginModelEdgeSchemaGolden`.
- `internal/engine`: `TestEdgeSchemaAddUnfilled` (no statement, the text),
  `TestEdgeSchemaRefusedAtResolve`. `internal/apitest`:
  `TestFilterOnJoinEntityRefused`.
- `internal/integration`: `TestEdgeSchemaThrough` (defaults, re-add keeps
  the row, Remove, Clear under a Write policy), `TestEdgeSchemaHooks`,
  `TestEdgeSchemaOtherUniqueKey` (MySQL drops the link, PostgreSQL and
  SQLite fail), on each dialect.
- `cmd/velox-gen`: `TestConvertJoinTable` runs migrations-operations.md's
  conversion on each dialect.
- `contrib/graphql`: the `edgeschema` goldens of `internal/sdl` (gqlgen,
  graphql-go, compat=veloxe) and `internal/gogen` (gqlgen, graphql-go),
  `TestUnfilledWarning`, the plugin model's checks in `plugin`'s `TestRun`,
  and `TestGqlcBinds` with an edge schema.
- `examples/relations`: Author and Club through Membership, its migration
  and its golden output.
- `bench/scale`: `-m2m` and `-through`; `TestAddM2MShape`.
