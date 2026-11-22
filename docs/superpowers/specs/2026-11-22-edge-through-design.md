# Edge schemas: `edge.Through` (2026-11-22)

Gap #46 of `2026-11-21-veloxe-gap-analysis.md`. A many-to-many relation's
join table becomes an entity that has its own schema, fields, indexes and
behaviour, as ent's and veloxe's edge schemas are.

**Sources.**
- velox: this repository at `f395547e` (main).
- veloxe: `/Users/s.y.s/Jobs/go/veloxe`. Written `V/`.
  - `V/examples/edge-schema` holds the schema, README and e2e test.
  - `V/compiler/gen/graph_resolve.go:122-245` is `edgeSchemas`.
  - `V/compiler/gen/graph_resolve.go:247-273` is `resolveEdgeSchemaFK`.
  - `V/compiler/gen/graph_tables.go:103` skips the join table.
  - `V/compiler/gen/sql/helper.go:91-100` and `V/runtime/registry.go:220-235`
    fill the defaults of an M2M edge's join row.
  - `V/schema/field/annotation.go:43` is `field.ID`.
- Probe: `/Users/s.y.s/.claude/jobs/5f286dfc/tmp/probe`. Written `PR`.
  - Its schema is User, Group and Membership. Membership has its own ID,
    `role` with `Default("member")`, `joined_at` with `Default(time.Now)`,
    and `user_id`/`group_id` bound by two Required edges, plus a unique
    index on the pair.
  - velox-gen generated it unchanged. Then `gen/user.go` was hand-edited
    with one `velox.EdgeThrough[Membership, Group]("groups", …)` relation.
  - `probe_test.go` and `hook_test.go` run it on SQLite.

## 1. What velox has today

- **Every M2M relation already has a join entity.** velox-gen makes it
  (`internal/codegen/lower.go:1186` `through`). It is named by the singular
  of the join table (`UserGroup`) and has handles (`UserGroups`). Its
  `PrimaryKey` is `[0, 1]`, the two key columns. It has no schema. Its
  behaviour goes on an empty struct of its name (`docs/coming-from-ent.md`,
  "Finding schemas").
- **The runtime reads and writes join rows as entity rows.**
  - `velox.EdgeThrough[J, T]` is at `descriptor.go:215`.
  - `Rel.Add` inserts J's rows with `InsertRows(...).OnConflict(owner,
    target).DoNothing()` (`internal/engine/edgeset.go:508` `writeThrough`).
  - Remove and Clear are a `DeleteTable(J)`.
  - Both go through `TableInsert.Exec` and `TableDelete.Exec`. Those fill
    defaults and run J's hooks and its Insert and Write policies
    (`internal/engine/twrite.go:173`).
  - Has and load read J inside an EXISTS (`OpThrough`) and in
    `fetchThrough`.
- **A join entity may not have a Read or Query policy, or interceptors.**
  `buildThrough` refuses them when the relation resolves
  (`internal/engine/relation.go:148`). Has reads J's rows inside an EXISTS
  that the two sides scope. J's own conditions could disagree with them.
- **Composite keys.** The engine reads and writes entities whose primary key
  has several columns: join entities, change recording, upserts, lean
  inserts. `IDs()` refuses them (`internal/engine/column_read.go:32`). So do
  `Add`/`Remove` on an edge whose target has such a key
  (`internal/engine/edgeset.go:258`).
  - The schema DSL has no composite ID, and velox has no `field.ID`.
  - The codegen Graph and the plugin model both say `Fields[0]` is the ID
    (`internal/codegen/graph.go:23`, `plugin/plugin.go:67`). velox-graphql's
    types, cursors and Node rely on it.
- **The probe needed no engine change.** `EdgeThrough` works when J is an
  ordinary entity with its own int64 ID and a unique index on the pair (`PR`):
  - `Rel.Add` on insert and update writes Membership rows, with
    `role = member` and `joined_at` filled from the defaults.
  - Adding a link twice is a no-op, through ON CONFLICT on the unique index.
  - `Remove` deletes the row.
  - `With(U.Groups)` and `With(U.Memberships)` load.
  - `U.Groups.HasWith(G.Name.EQ("g2"))` counts 1.
  - A Membership hook sees `[insert delete]` for Add and Remove.
  - A hook that fails on Add rolls back the owner's insert.
  - `U.Memberships.Clear()` is refused before any SQL: "its key
    gen.Membership.UserID is not nullable, so a gen.Membership always has an
    owner: set that column instead".

What is missing is the schema side:
- a `Through` builder;
- velox-gen's resolution of an edge schema as the join;
- the synthesized "through" edge;
- the unique index;
- a rule for extra columns that `Add` cannot fill.

## 2. Decisions

### D1. Schema DSL: ent's shape, ent's rules

```go
func (User) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("groups", Group.Type).Through("memberships", Membership.Type),
	}
}
func (Group) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("users", User.Type).Ref("groups").Through("memberships", Membership.Type),
	}
}

type Membership struct{ velox.Schema }

func (Membership) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("role").Values("owner", "member").Default("member"),
		field.Time("joined_at").Default(time.Now).Immutable(),
		field.Int64("user_id"),
		field.Int64("group_id"),
	}
}
func (Membership) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("user", User.Type).Unique().Required().Field("user_id"),
		edge.To("group", Group.Type).Unique().Required().Field("group_id"),
	}
}
```

- `Through(name string, t any)` is on `*AssocBuilder` and `*InverseBuilder`.
  It sets `desc.Edge.Through{Name, Type, TypePkg}`.
  - `t` is a schema's `Type` method value, read by `typ`, as `To`'s target is.
  - An empty name is a builder error (`Errs`).
- **The edge schema is a schema like any other.** It has its own Fields,
  Edges, Indexes, Mixin, Hooks, Policy and Annotations. Its table is its own
  (`memberships`), and so is its ID (D2).
  - Its two key fields are ordinary fields.
  - The edges that bind them are ordinary to-one edges: `edge.To`, as in
    ent's docs, or `edge.From(...).Ref(...)`.
  - Their keys are NOT NULL, and their foreign keys take the usual
    `OnDelete` (rules 45–47).
- **Resolution follows ent's `edgeSchemas` and `resolveEdgeSchemaFK`.**
  1. The M2M relation's table becomes the edge schema's table.
  2. Its two columns are the M2M's default names (`user_id`, `group_id`, or
     `edge.Columns`' default rule for a self edge).
  3. Each column is matched to an edge of the edge schema bound with `Field`
     to a field of that name.
  4. Failing that, it is matched to the one edge of the edge schema that has
     the side's type and is bound to a field. The column is renamed to that
     field, as ent renames it.
  5. velox-gen adds a to-many edge named by `Through`'s first argument to the
     owner: `User.memberships`, `Group.memberships`. It is an O2M to the edge
     schema, paired (`Ref`) with the edge it matched.
  6. Only an edge that declares `Through` gets a through edge. An inverse
     without `Through` still uses the edge schema's table, as in ent.
- **velox-gen refuses each of these, as a numbered rule at the edge's line.**
  The rules continue after 62. Each text below is the error's reason.
  - 63: `Through is allowed only on a many-to-many edge; this edge is O2M`.
  - 64: `Through names Membership, which is not a schema of this package`.
    Also: `an edge cannot go through its own schema`.
  - 65: `Through's name "memberships" is taken by edge "memberships" of
    User`. The same holds for a field, a Go name, or an empty name.
    - Declaring the through edge yourself, as in the probe, is this
      refusal, as in ent.
  - 66: `Membership is already the edge schema of User.groups`. It is
    reported when a second assoc edge names the schema. It is also reported
    when an inverse's Through names a type other than its assoc's.
  - 67: `edge schema Membership has no edge to User bound to field
    "user_id"; declare edge.To("user", User.Type).Unique().Required().Field("user_id")`.
    Also: `edge "user" of Membership must be Required: a link has both ends`.
    - A key field whose type is not the side's ID is rule 12's existing
      text. It already suggests `field.Int64` for an ent port's `field.Int`.
  - 68: `Through takes its table and columns from Membership; remove
    StorageKey (edge.Table, edge.Columns, edge.Symbols)`.
  - 69: `Through on a bidirectional edge (an M2M to its own schema with no
    inverse) is not supported; give it an inverse edge, or drop Through`.
    This is deferred (§4).
  - 70: `edge schema Membership has a Read or Query policy or interceptors,
    which Has and With through groups would not apply; a link is read
    through its two sides`. It covers interceptors from mixins, such as
    softdelete's.
    - velox-gen reports what it can see, by the analysis behind
      `bidiPolicyError` (`lower.go:136`).
    - `buildThrough` still refuses it when the statement runs.
- **The behaviour-on-an-empty-struct form stays only for plain M2M.** An edge
  schema's behaviour is on the schema. The paragraph in "Finding schemas" is
  reworded.

**Recommendation: as above.** ent's names and rules are what ports bring.
Every refusal names the line to write. Where the engine would refuse only
later, velox-gen refuses earlier.

### D2. Keys: the edge schema's own int64 ID; composite keys deferred

**Options.**
- (a) The edge schema's own ID. This is ent's default when no `field.ID`
  annotation is given, and veloxe's example uses it.
- (b) A composite key `(user_id, group_id)`. This is today's join entity
  and ent's `field.ID("user_id", "group_id")` annotation.
- (c) Both.

**Recommendation: (a) for v1.**
- With (a), an edge schema is an ordinary entity in the Graph, the plugin
  model, the descriptor and GraphQL. `Fields[0]` is its ID. The engine needs
  no change for it (`PR`).
- (b) breaks `Fields[0]`-is-ID in the Graph and the plugin model.
  - `build.go`, `lower.go` and `plugin.go` assume it.
  - So do velox-graphql's types, inputs, cursors and Node, which cannot
    name a row by one key.
  - That is a separate design of its own.
- **velox-gen adds the unique index on the pair, as ent does**
  (`V/compiler/gen/graph_resolve.go:212-240`). It does so unless the schema
  declares a unique index over the two columns, in either order, with no
  `Where`.
  - Its name is derived like any index's (0090).
  - `Add`'s `ON CONFLICT (owner, target) DO NOTHING` needs it.
- `field.ID` stays out of velox.
  - A port that uses it fails to compile on `field.ID`.
  - `coming-from-ent.md` says to remove it, and that velox then keys the
    rows by `id` with a unique pair.
- The cost of (a) is one more column and index per link. The second cost is
  the migration of an existing join table (D4).

### D3. Generated API

The model is `User —groups(M2M via Membership)→ Group`. `Group` has the
inverse `users`.

- **Membership is generated as any entity.**
  - Its struct has `ID`, `Role`, `JoinedAt`, `UserID` and `GroupID`.
  - Its `MembershipEdges` has `User velox.One[User]` and `Group
    velox.One[Group]`.
  - It has the handles `Memberships` and its own descriptor.
  - No `UserGroup` join entity is generated. The name `UserGroup` is free.
- **`UserEdges`** has:
  - `Groups velox.Many[Group]`, the M2M, unchanged;
  - `Memberships velox.Many[Membership]`, the through edge.
- **`UserHandles`** has:
  - `Groups velox.Rel[User, Group]`, described by
    `velox.EdgeThrough[Membership, Group]("groups", userColID,
    membershipColUserID, membershipColGroupID, groupColID)`;
  - `Memberships velox.Rel[User, Membership]`, described by
    `velox.EdgeTo[Membership]("memberships", userColID,
    membershipColUserID)`.
- **The Rel methods.**
  - `Groups.Add/Remove/Clear` insert and delete Membership rows, as a plain
    M2M's do today. Defaults fill the new rows' other columns.
  - `Memberships.Add/Remove/Clear` are refused before SQL by the existing
    NOT NULL check (`PR`). A link is made by inserting a Membership.
  - `With`, `Has`, `HasWith`, `Of`, `OrderBy`, `LimitEach` and nested
    `With(U.Memberships.With(M.Group))` work on both edges as on any.
  - `Memberships.User` and `Memberships.Group` can be `Join`ed (0031).
- **Querying the edge schema directly** is `db.Query[gen.Membership]()`.
  - It takes a `Where` on any column, for example `M.Role.EQ(...)`.
  - It takes `With(M.User)` or `Join(M.Group)`.
  - It inserts with `db.Insert(&gen.Membership{...})`, and updates and
    deletes by id.
- **`Rel.Add` when the edge schema has a column it cannot fill.**
  - Such a column is a field that is:
    - not the ID or one of the two keys;
    - not `Optional`, `Nillable`, `Default`/`DefaultFunc`, a `Version`, or
      an Optional JSON field;
    - not set by velox's existing defaults (0090).
  - velox-gen lists those columns as a trailing variadic of
    `EdgeThrough(..., unfilled ...int)`.
  - `resolveEdge` fails an edge write with Add on such a relation before
    any SQL. The text is: `velox: User: edge groups: Add cannot fill
    Membership.Note, which has no default; insert Membership rows, or give
    note a Default or Optional`.
  - An Optional field without a Default is written as its zero by Add, as
    an Insert that leaves it unset writes it (0090). It is not listed.
  - **Recommendation: fail closed, no setter list in v1.**
    - Through a struct, velox cannot tell an unset column from a zero one.
    - So without the list, Add would silently write `""` into a column the
      schema calls required. ent fails at the database instead.
    - A hook that fills the column is refused too. Give the column a Default
      instead.
    - `Rel.AddWith(keys, setters...)` is deferred (§4).
- **Name-based behaviour.** `velox.HasEdge("groups")` and
  `HasEdge("memberships")` resolve through the EntityTable (0081) as any
  edge does. velox-gen's constant-name check sees both edges, since both are
  in the Graph.

### D4. Migration

- **New tables.**
  - The edge schema's table is an entity table. It has the `id` key, its
    columns, its foreign keys from the two edges, its declared indexes and
    the derived unique pair index.
  - `joinTables` (`dbnames.go:107`) and rule 27 (`build.go:1264`) skip an
    M2M relation that has an edge schema. So do `checkJoinTables` and the
    join-entity half of `checkDeclared` (`lower.go:1273,1368`). No second
    table is written.
- **Foreign key actions are ent's.** A Required edge's key with no
  `OnDelete` has none (NO ACTION). A plain join table's keys are
  `ON DELETE CASCADE`.
  - velox-gen warns, once per edge schema with neither `OnDelete`: `deleting
    a User fails while a Membership names it; declare OnDelete(edge.Cascade)
    on edge "user" to delete its links with it`. The warning goes through the
    path `SoftDeleteWarnings` uses.
  - **Recommendation:** keep ent's rule and warn. Do not make CASCADE
    implicit. A port's database stays what ent made.
- **Converting a plain M2M join table into an edge schema.**
  - When the edge schema keeps the old name with `schema.Table("user_groups")`,
    Diff sees one table whose primary key changes from `(user_id, group_id)`
    to `(id)`. It refuses with code `primary-key` ("write this migration by
    hand"), as `diff_match.go:143` does for any key change.
    - The added columns and the unique index are in the same refused plan.
  - When the table is renamed, Diff plans a drop of `user_groups` and a
    create of `memberships`. The drop guards refuse the loss unless allowed.
  - **Recommendation:** no planner change in v1. Both paths fail closed.
  - `docs/migrations-operations.md` gains a hand-written recipe for each
    dialect:
    1. add `id` as an identity or auto-increment column;
    2. move the primary key;
    3. add the unique pair index;
    4. add the extra columns with their defaults.
  - The ADR records that a composite-key edge schema (deferred) would make
    this conversion "added columns only".
- `internal/ddl` needs no change. The edge schema's table is rendered by
  the entity path.

### D5. Behaviour on the edge schema

| | Direct statements on Membership | `Groups.Add` / `Remove` / `Clear` | `Has` / `With` through `groups` |
| --- | --- | --- | --- |
| Hooks (schema, mixin, `WithHooks`) | fire | **fire**: Add is an insert of Membership rows, Remove and Clear a delete (`PR`) | not applicable |
| Insert policy | applies | applies to each new row; a denial fails the whole write and rolls back the owner's insert | — |
| Write policy | filters (0008) | filters Remove and Clear: a hidden link stays, 0 rows | — |
| Read/Query policy, interceptors | — | — | **refused** (rule 70, `buildThrough`) |
| Defaults | fill | fill | — |

- **This differs from ent and veloxe.**
  - They fill only defaults on `AddGroupIDs`
    (`V/compiler/gen/sql/helper.go:96`). Their edge-schema hooks and
    privacy do not run on the M2M edge.
  - velox runs them because its join rows already go through the entity
    pipeline. That is the stricter reading, and it fails closed.
  - `coming-from-ent.md` states the difference.
- `EdgeThrough`'s godoc already says that velox does not check endpoints and
  that J's Policy or a hook should. It is reworded for an edge schema.
- Change recording (0094) records Membership rows written by Add, as it
  records join rows today.

**Recommendation: as the table.** Hooks fire, Insert and Write policies
apply, and a Read policy is refused.

### D6. GraphQL (velox-graphql, minimal)

- **Membership is a normal type.** It has the `id` field, its fields, the
  edges `user` and `group`, connections and where inputs. It has create and
  update inputs with `userID`/`groupID`, and Node under compat=veloxe (0088).
  It is a schema with one key, so nothing new is needed.
- **The M2M edge `groups` is unchanged.** It keeps the field, the
  connection, `groupIDs` on create, and `addGroupIDs`/`removeGroupIDs`/
  `clearGroups` on update.
  - Exception: when the edge schema has columns Add cannot fill (D3), sdl
    and gogen leave out the M2M's create and add inputs.
  - velox-gen warns `User.groups: no add inputs; Membership.note has no
    default`.
  - The plugin computes this from `plugin.Field`'s `Optional`,
    `HasDefault` and `Nullable`.
- **The through edge `memberships`** is an ordinary O2M field and
  connection. Its key on Membership is NOT NULL, so sdl's existing rule
  writes no re-pointing input for it (`sdl/input.go:282-340`).
- **Plugin model.** `ModelVersion` stays 3: fields are added, none changes
  meaning.
  - `Entity.EdgeSchema bool` is new.
  - `Edge.Through string` is new: on the M2M edge, the through edge's name.
  - `Edge.Join` names the edge schema, a schema entity whose `Join` is
    false.
  - `joinFields` (`plugin.go:225`) applies only to `Join` entities.
- `veloxgql.GeneratedCodeIsVersion27` stays, since gogen's templates do not
  change shape.

### D7. Generated code shape and compile cost

- **The bumps.**
  - `velox.GeneratedCodeIsVersion25` → **`GeneratedCodeIsVersion26`**,
    with a `CHANGELOG.md` line. Generated code may call `EdgeThrough` with
    `unfilled` columns, which an older runtime lacks.
  - `loadjson.Version` 8 → **9**, for `Edge.Through`.
  - `internal/repocheck/testdata/api.txt` gains `edge.(*AssocBuilder).Through`,
    `edge.(*InverseBuilder).Through` and `EdgeThrough`'s new signature.
- **Output for a schema without `Through` is byte-identical.** The goldens of
  `internal/codegen`, `internal/testmodel`, `internal/testbind` and the
  examples show no diff except the version constant.
- **Measure once, in the task that lands the codegen (T3).**
  - Run `scripts/scale-check.sh` before and after.
  - Run a `buildcost` probe at 100 entities, comparing 20 plain M2M edges
    with the same 20 as edge schemas that have one extra field each.
  - Record both in ADR 0109.
  - Expectation: an edge schema replaces a generated join entity with an
    ordinary one, so the cost per relation is one entity's plus one `Rel`
    per side. Plain M2M is unchanged.

### D8. Scope

**v1, ent-compatible for the common case:**
- `Through` on assoc and inverse M2M edges, between two schemas or on a
  self edge with an inverse;
- an edge schema with its own int64 or UUID ID (0056), any extra fields,
  indexes, edges to other schemas, mixins, hooks, and Insert and Write
  policies;
- the synthesized through edge, the unique pair index, and rules 63–70;
- `Rel.Add/Remove/Clear` on the M2M with defaults, failing closed on
  unfillable columns;
- velox-graphql's normal type for the edge schema;
- docs, ADR 0109, and an `examples/relations` section.

**Deferred, each refused or absent rather than half-working:**
- the composite key, `field.ID(a, b)`, and the add-columns-only migration it
  would allow;
- bidirectional self edges through an edge schema, which is ent's
  `Friendship` docs example (rule 69). velox's bidi mirror (`checkBidi`)
  needs the two key columns as the primary key;
- a Read/Query policy or interceptors on an edge schema, applied inside
  `Has` and load (rule 70);
- `Rel.AddWith(keys, setters...)`, which sets extra columns on Add;
- GraphQL inputs that take edge-schema fields on M2M add, such as
  `addGroups: [{id, role}]`;
- a planner conversion from a plain join table to an edge schema.

## 3. Files that change

- `schema/edge/edge.go`, `edge_test.go`, `example_test.go`: `Through`.
- `schema/internal/desc/desc.go`: `Edge.Through`.
- `schema/load/marshal.go`: carry it.
- `internal/codegen/loadjson/loadjson.go`: `Edge.Through`, Version 9.
- `internal/codegen/build.go`: edge-schema resolution after `resolveInverse`
  and `resolveAssoc`, rules 63–69, the through edge, the unique index, and
  rule 27 skipped for edge schemas.
- `internal/codegen/graph.go`: `Edge.Through`, `Entity.EdgeSchema`.
- `internal/codegen/lower.go`: `through` uses the edge schema; `unfilled`;
  join entities and `checkJoinTables` skip edge schemas.
- `internal/codegen/behaviourcheck.go` or `lower.go`: rule 70.
- `internal/codegen/dbnames.go`, `tables.go`: no join table for an edge
  schema.
- `internal/codegen/plugin.go`: model fields, `joinOf`.
- `internal/codegen/describe.go`: show `M2M via Membership`.
- `internal/codegen/softdelete_check.go` or a sibling: the OnDelete warning.
- `descriptor.go`: `EdgeThrough(..., unfilled ...int)` and its godoc.
- `internal/engine/descriptor.go`: `Through.Unfilled`.
- `internal/engine/edgeset.go`: the refusal in `resolveEdge`.
- `doc.go`: Version26. Also `CHANGELOG.md`, `internal/repocheck/testdata/api.txt`.
- `plugin/plugin.go`: `Entity.EdgeSchema`, `Edge.Through`; docs of `Edge.Join`.
- `contrib/graphql/internal/sdl/input.go`, `contrib/graphql/internal/gogen/input.go`:
  omit unfillable add inputs.
- `internal/testmodel/schema` gains one edge schema, and is regenerated.
  `internal/testbind` and every example are regenerated for the version.
- `examples/relations`: a Membership section, migration, golden output.
- `docs/coming-from-ent.md`, `docs/migrations-operations.md`, `docs/guide.md`
  (relations), `docs/decisions/0109-edge-schemas.md` and its README row.
- Gap analysis row #46 becomes "Have (0109)".

## 4. ADR

- **0109 `edge-schemas`** records D1–D8:
  - the own-ID choice, and why composite keys are deferred;
  - that hooks fire on Add and Remove, unlike ent;
  - the `unfilled` refusal;
  - the compile-cost numbers.
- It amends the "a join entity holds behaviour only" sentence of
  `coming-from-ent.md`. It cites 0041, 0044, 0081 and 0090.
- 0110 is left free for the composite-key follow-up.

## 5. Tasks

All tasks are on one worktree branch (generator output shape). Run
`scripts/quick.sh` per task and one `bash scripts/ci-local.sh` at the end.
The tests listed with each task fail before it.

1. **DSL and load model.**
   - `Through` on both builders, `desc.Edge.Through`, marshal,
     `loadjson.Edge.Through`, Version 9.
   - Tests:
     - `schema/edge` unit tests: the fields are set, and an empty name or a
       non-schema type is a builder error;
     - `schema/load` marshal round trip;
     - a `schema/edge` `Example` with Through.
2. **Graph resolution and rules.**
   - Edge-schema matching, by field and then by type; the through edge with
     `Ref`; the unique index unless declared; rules 63–69; no join table in
     `joinTables`; describe.
   - Tests:
     - `build_test.go`: a table of one failing schema per rule, asserting
       the full error text and line;
     - matching for a self M2M with an inverse (follower/followee), where
       column order is checked;
     - a declared reversed-order unique index is accepted, and a partial
       one is not;
     - `tables_test.go`: one entity table, no join table, FKs NO ACTION,
       the unique index;
     - `describe -json` golden;
     - a `migrate diff` golden for the probe's schema on all three dialects.
3. **Lowering, runtime refusal, behaviour rule, version.**
   - `through` uses the edge schema; `EdgeThrough` takes `unfilled`;
     `resolveEdge` refuses; rule 70; the OnDelete warning; Version26;
     CHANGELOG; api.txt; compile-cost measurement.
   - Tests:
     - a new `internal/codegen/testdata/golden/edgeschema` (User/Group/
       Membership plus a Follow self edge), plus `-update` and diff;
     - in `internal/engine` (package `engine_test`):
       - Add fills defaults;
       - Add on an unfilled relation fails before any SQL (dry run shows
         none) with the text;
       - Membership hooks see Add and Remove, and a hook error rolls back
         the owner's insert;
       - a Write policy leaves a hidden link on Remove;
       - a Read policy on the edge schema fails at resolve;
     - `internal/testmodel` gains an edge schema; `internal/apitest` runs
       Add, Remove, Clear, With and HasWith on PostgreSQL, MySQL and SQLite,
       including the re-add no-op;
     - rule 70 in `behaviourcheck` tests, with a softdelete mixin.
4. **velox-graphql.**
   - Plugin model fields; sdl and gogen omit unfillable add inputs and warn;
     the edge schema as a normal type.
   - Tests:
     - `contrib/graphql/internal/sdl` and `gogen` goldens for the edgeschema
       model, one with and one without an unfillable column;
     - a model test that `Edge.Join` names a non-join entity and `joinFields`
       ignores it.
5. **Docs and example.**
   - ADR 0109 and README row; coming-from-ent (Finding schemas, the M2M
     paragraph, the hooks difference, `field.ID` removal); the
     migrations-operations recipe; the guide; an `examples/relations`
     Membership section with a migration and golden output; gap row #46.
   - Tests:
     - the example's golden and migrations check (`velox-gen -check`,
       `verify`);
     - the docs tests (doc comments, no spec citations);
     - `TestCILocalMirrorsCI` unchanged.

## 6. Review focus: five failure modes

1. **Add silently writes a zero.** The `unfilled` list is computed wrong for
   a field kind:
   - an Optional enum without a default (velox-gen already asks for a
     Default or Nillable);
   - a mixin's field;
   - a Version field;
   - an UpdateDefault-only field;
   - an Optional JSON field;
   - a UUID or DefaultFunc ID.

   Check each kind against `FillDefault` and 0090. Check that a field listed
   as unfilled could not in fact be filled.
2. **A link is read past a filter.**
   - An edge schema gets a Read/Query policy or an interceptor that velox-gen
     does not see: from a mixin's mixin, a `WithFilter`, or a run-time
     option. `Has` and `With` through the M2M then ignore it while direct
     queries apply it.
   - Softdelete is the sharp case. Remove would soft-delete, and `Has`
     would still count the row.
   - `buildThrough`'s refusal must stay the backstop.
3. **The wrong column is matched.**
   - On a self M2M with an inverse, both edges of the edge schema point to
     User. The type fallback must not pick one silently. Only the field-name
     match decides, or rule 67 fires.
   - The inverse side's column order is reversed (`ed.Rel.Columns`
     reversed in `resolveInverse`). A swap writes links backwards and passes
     tests that are symmetric.
4. **Duplicate links or a failing re-add.**
   - The derived unique index is missing or wrong: partial, a different
     column set, or reversed order judged as missing so a second index is
     added. Then `ON CONFLICT (user_id, group_id) DO NOTHING` errors on
     PostgreSQL.
   - On SQLite and MySQL the same fault inserts duplicates.
   - Test the re-add on all three dialects, and on MySQL with a second
     unique key on the edge schema.
5. **Migration and naming regressions on conversion.**
   - Turning a plain M2M into an edge schema must:
     - be refused by Diff (`primary-key`) or a drop guard, never planned as
       a silent drop and create;
     - warn that deletes change from CASCADE to NO ACTION;
     - drop the old `UserGroup` and `UserGroups` names without leaving a
       stale generated file. velox-gen removes the files it no longer
       writes.
   - A plugin that assumed `Edge.Join` names a `Join` entity must not
     misbehave.

## 7. Rulings on the open questions (controller, 2026-11-22)

1. Composite keys (`field.ID("a", "b")`): deferred to ADR 0110. v1 refuses
   it with a rule that says so.
2. Bidirectional self edges through an edge schema (ent's `User.friends`):
   refused in v1 (rule 69), with an error naming the workaround (an inverse
   pair).
