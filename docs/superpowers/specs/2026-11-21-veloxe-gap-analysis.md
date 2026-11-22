# veloxe → velox2 gap analysis (2026-11-21)

The question: what veloxe has that velox2 should still add. This report lists
veloxe's features, says what velox2 has for each, and ranks the gaps by how
much nextapp uses them.

**Sources.**
- veloxe: `/Users/s.y.s/go/pkg/mod/github.com/syssam/veloxe@v0.3.1-0.20261113175532-cfd8da50023e`,
  written `veloxe/` below. nextapp pins this version.
- velox2: this repository at `506e341c`. Paths are relative to the repo root.
- nextapp: `/Users/s.y.s/Jobs/project/nextapp/nextapp-graphql`, written `N/`.
  Its port, `nextapp-graphql-velox2`, has its log of upstream asks in
  `docs/velox2-migration/upstream-feedback.md`, written `UF`.

**Method.** nextapp usage counts come from grep over `N/internal`, `N/cmd`
and `N/graph`, leaving out `N/velox` (generated), `_test.go` and
`generated*.go` files. Schema-side counts grep `N/veloxschema`. A count can
include a comment or an identifier with the same name. "Unknown" means the
grep could not settle it.

**Status values.**
- **Have**: velox2 has it, under the API named.
- **Different by design**: velox2 does it another way, or deliberately not,
  with the ADR or doc that says so.
- **Partial**: velox2 covers part of it.
- **Missing**: velox2 has nothing for it. The evidence is a grep that came
  back empty.

---

## 1. Inventory

### 1.1 Codegen entry point and configuration

| # | veloxe feature | how veloxe exposes it | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 1 | Generate from Go code | `compiler.Generate` / `GenerateContext` (`veloxe/compiler/compiler.go:131,138`) | **Partial**: only the `velox-gen` command. `internal/codegen` is internal, and `cmd/velox-gen/generate.go:274` `runGenerate` is in package main (UF P12) | yes, `N/veloxschema/generate.go:67` |
| 2 | Load the graph without writing | `compiler.LoadGraph` (`compiler.go:79`) | **Partial**: `velox-gen describe -json` prints the resolved `plugin.Model` | no |
| 3 | Output directory | `gen.WithTarget` (`veloxe/compiler/gen/option.go:85`) | **Have**: `-out` (default `./veloxgen`, ADR 0065) | yes, `generate.go:30` |
| 4 | Output package path | `gen.WithPackage` (`option.go:61`) | **Have**: `-package` sets the name; the import path comes from the module | no |
| 5 | Schema package path | `gen.WithSchema` (`option.go:73`) | **Have**: the positional package argument (default `./veloxschema`) | implicit |
| 6 | File header | `gen.WithHeader` (`option.go:16`) | **Missing**: the header is fixed (`cmd/velox-gen/generate.go:42` `headerFor`) | no |
| 7 | Default ID type | `gen.WithIDType` / `WithIDTypeInfo` (`option.go:25,49`) | **Different by design**: the implicit id is int64; a schema declares `field.X("id")` for any other type (`docs/coming-from-ent.md`, "The implicit ID is int64") | no |
| 8 | Storage driver | `gen.WithStorage` / `WithStorageDriver`, `compiler.Storage` (`option.go:118,128`) | **Different by design**: SQL only; the dialect is picked when the DB opens (`velox.Open(dialect, db)`) | no |
| 9 | Hooks that rewrite the graph | `gen.WithHooks(gen.Hook)` (`option.go:149`) | **Partial**: there is no model-rewrite stage. Three of nextapp's four hooks are now defaults: index-name prefix (ADR 0083/0090), `WhereInput` opt-in, and `OpsString` as the string default (UF N-V20). Column order (`SortFieldsHook`) has no home (UF N-V4) | yes, 4 hooks, `generate.go:38-43` |
| 10 | Templates | `gen.WithTemplates`, `compiler.TemplateFiles/Glob/Dir` (`compiler.go:259-275`). Legacy: the built-in SQL dialect never runs them (`gen/template.go:3-8`) | **Different by design**: generator plugins over JSON (`plugin/doc.go`, `-plugin`) | no |
| 11 | Global annotations | `gen.WithAnnotations`, `compiler.Annotations` (`option.go:167`) | **Partial**: per-plugin options, `-plugin tool:key=value` | no |
| 12 | Build flags and tags | `gen.WithBuildFlags`, `compiler.BuildFlags/BuildTags` (`compiler.go:244,253`) | **Partial**: the `go` commands velox-gen runs inherit the environment (`internal/codegen/load.go:116`), so `GOFLAGS=-p=N` works. There is no flag for it, and a schema file behind a build tag is refused | yes, `-p` at `generate.go:62` |
| 13 | Custom generator | `gen.WithGenerator` (`option.go:188`) | **Different by design**: plugins | no |
| 14 | Extensions | `compiler.Extensions`, `RegisterExtension` (`compiler.go:326`, `extension_registry.go:16`) | **Have**, as plugins: `-plugin velox-graphql` | yes, graphqlgen |
| 15 | Dependency injection into builders | `compiler.Dependency*` (`compiler.go:373-433`) | **Missing**. velox2 has no generated builders to inject into. Unsure whether veloxe's SQL dialect uses it at all: `compiler/gen/sql` never mentions it | no |
| 16 | Features by name | `compiler.FeatureNames` (`compiler.go:198`) | **Different by design**: velox2 has no feature flags; see §1.2 | no |
| 17 | CLI | `cmd/velox` (generate, validate, init, watch, version: `cmd/velox/main.go:38-46`), `velox.yaml` | **Partial**: `velox-gen generate/verify/describe/version` plus `migrate …`. No `init`, `watch` or config file | no (`go run generate.go`) |
| 18 | AGENTS.md generated next to the client | `compiler/gen/agent_docs.go` | **Partial**: `docs/for-agents.md` and `skills/velox` live in the repo; nothing is generated into the output | yes, `N/velox/AGENTS.md` exists |
| 19 | Second, experimental generator | `cmd/velox-gen` + package `orm` + `cmd/velox-migrate` (`veloxe/docs/orm.md:1-25`) | **Different by design**: velox2 is that line's successor | no |

### 1.2 `gen.Feature*` flags (`veloxe/compiler/gen/feature.go`)

| # | flag | veloxe (line) | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 20 | `FeaturePrivacy` | :22 | **Have**, always on: `Policy()` on the schema, plus package `privacy` with veloxe's names (ADR 0089) | yes, `generate.go:31`; 116 `Policy()` |
| 21 | `FeatureIntercept` (generated `intercept` package: `intercept.Func`, `q.Type()`, `WhereP`) | :33 | **Different by design**: an Interceptor is a row filter, `Interceptor.Filter func(ctx) (Predicate, error)`, declared on a schema or mixin (`docs/coming-from-ent.md:219`, ADR 0040). There is no `intercept` package | yes, `N/internal/lib/conditions/softdelete.go:124` |
| 22 | `FeatureEntQL` | :49 | **Partial**: predicates by name (`velox.FieldEQ`… in `cond.go`) and package `untyped`. No `querylanguage.P` tree and no generated `Filter()` | no |
| 23 | `FeatureNamedEdges` (`WithNamedX`) | :60 | **Missing**: one `With` per relation (`query.go:60`); grep for `WithNamed`/`NamedEdge` in the public API is empty. GraphQL batches per SQL instead (ADR 0054) | no |
| 24 | `FeatureBidiEdgeRefs` | :70 | **Missing**: a load does not set back-references. grep `back.?ref|inverse` in `relation.go`, `each.go` and `internal/engine/load.go` is empty | enabled at `generate.go:33`; whether code relies on it is unknown |
| 25 | `FeatureSnapshot` | :78 | **Different by design**: not needed. A schema compiles before `veloxgen` exists (`docs/coming-from-ent.md`, layout section) | no (commented out) |
| 26 | `FeatureSchemaConfig` (alternate schema per client at run time) | :91 | **Partial**: static `schema.TableIn` / `sqlschema.Schema`, carried into `Descriptor.TableSchema` (`descriptor.go:15`). No per-DB override | no |
| 27 | `featureMultiSchema` | :104 | **Have**: `sqlschema.Schema/SchemaTable`; migrate plans per schema | no |
| 28 | `FeatureLock` | :115 | **Have**: `Query.ForUpdate/ForShare/SkipLocked/NoWait` (`query.go:132`) | yes, 60 sites, `projectsprint/workflow.go:50` |
| 29 | `FeatureModifier` (`Modify`) | :127 | **Different by design**: `Expr` (0069), `Select/SelectRaw/GroupBy/Having/Calc` (0074), `SetExpr` (0095); mapping in `docs/sql.md:195-200` | yes, 51 sites, `aggregate/expense.go:39` |
| 30 | `FeatureExecQuery` | :135 | **Have**: `db.Exec`, `db.Rows`, `db.Raw`, each with a reason (`raw.go:25,67`) | app calls database/sql directly (`cronjob/jobstate/store.go:73`) |
| 31 | `FeatureUpsert` | :143 | **Have**: `InsertQuery.OnConflict(...).Update(...)/DoNothing()` (`insert.go:59`) | no ORM upsert (grep `OnConflict` is empty) |
| 32 | `FeatureVersionedMigration` | :151 | **Have**: velox's own planner (`velox-gen migrate diff/apply/status/drift/lint/baseline/resolve`) plus `migrate sql` for Atlas (ADR 0006) | no |
| 33 | `FeatureGlobalID` | :159 | **Different by design**: an ID is the plain key (ADR 0061); Node exists only under `compat=veloxe` (ADR 0088) | no |
| 34 | `FeatureValidator` | :177 | **Have**, always on | yes (schemas) |
| 35 | `FeatureEntPredicates` | :199 | **Have**: typed handles (`veloxgen.Users.Name.EQ`) and `velox.FieldEQ`… by name | yes, the generic form (`IDField.EQ`) |
| 36 | `FeatureAutoDefault` | :223 | **Have**: the default behaviour (ADR 0090; `docs/coming-from-ent.md`, "Optional is NOT NULL") | yes, `generate.go:37` |
| 37 | `FeatureWhereInputAll` | :237 | **Missing**: where inputs are opt-in only (`graphql.WhereOps`/`WhereInput`, `contrib/graphql/annotation.go:478-510`) | no (nextapp forces the opposite) |
| 38 | `FeatureCheckBounds` | :256 | **Missing**: validators emit no CHECK. grep `CheckBounds` is empty. `schema.Check` / `sqlschema.Check` work by hand | no |

### 1.3 Schema DSL

| # | veloxe feature | veloxe | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 39 | Field types String, Text, Bool, Int*, Uint*, Float, Time, Enum, JSON, Strings/Ints/Floats, Bytes, UUID, Other | `veloxe/schema/field/field.go` | **Have**: `schema/field` | yes (String 2027, Enum 1074, …) |
| 40 | `field.Any` | `field.go` | **Missing** (grep `func Any` is empty) | no |
| 41 | Modifiers: Optional, Nillable, Unique, Immutable, Default, DefaultFunc, UpdateDefault, Sensitive, StorageKey, StructTag, SchemaType, Comment, GoType, Validate, validators | `schema/field` builders | **Have** (ADR 0003, 0082, 0090) | yes (StorageKey 2350, Sensitive 249) |
| 42 | `Nullable()` | field builders | **Different by design**: `Nillable` makes a column NULL-able (ADR 0003) | no (0 uses) |
| 43 | `field.Deprecated` | field builders | **Missing**: only the GraphQL directive `graphql.Deprecated` | no (one comment) |
| 44 | `field.ValueScanner` (external scanner) | field builders | **Missing** (grep is empty) | no |
| 45 | Edges: To, From, Ref, Unique, Required, Immutable, Field, StorageKey (Column/Table/Symbol), Comment, Annotations | `veloxe/schema/edge/edge.go` | **Have**: `schema/edge`, plus `OnDelete` | yes (To 950, From 382) |
| 46 | `edge.Through` (edge schema with extra columns) | `edge.go` | **Have**: `edge.To(...).Through(name, T)` and `edge.From(...).Through`, an edge schema keyed by its own `id`; no composite `field.ID` (ADR 0109; 0110 reserved) | no |
| 47 | Edge `StructTag` | `edge.go` | **Missing** | no |
| 48 | Indexes: Fields, Edges, Unique, StorageKey, Annotations | `veloxe/schema/index/index.go` | **Have**, plus `Where` | yes (2352) |
| 49 | `sqlschema` annotations: IndexType(s), Default, DefaultExpr(s), NonUniqueFK, Table, Schema, Check(s), Collation, Charset, Size, ColumnType, IncrementStart, WithComments, Prefix, OpClass, Desc, IncludeColumns, StorageParams, Skip, OnDelete/OnUpdate | `veloxe/dialect/sqlschema/annotation.go` | **Have**, same names (ADR 0075). `NonUniqueFK` is the veloxe default (ADR 0090) | yes (IndexType 252, Default 29, NonUniqueFK 10) |
| 50 | `sqlschema.View` / `ViewFor` | `annotation.go` | **Missing** (grep is empty) | no |
| 51 | `schema.Comment` | `veloxe/schema` | **Have**: `schema.Comment` | yes (26) |
| 52 | Mixins: Fields, Edges, Indexes, Annotations, Hooks, Policy, Interceptors | `veloxe/schema/mixin` | **Have**; a nested mixin is refused (UF V1) | yes (3 mixins) |
| 53 | `mixin.AuditHook`, `AnnotateFields`, `AnnotateEdges` | `veloxe/schema/mixin/audit_hook.go`, `mixin.go` | **Missing** (grep is empty). `db.Record` (ADR 0094) covers auditing at run time | no |
| 54 | Schema `Hooks()` | generated runtime | **Have**: untyped `velox.Hook`, `velox.On`/`Before` (ADR 0039/0040) | yes (6 schemas) |
| 55 | Schema `Policy()` | generated runtime | **Have**: `velox.Policy` with Read, Write, Insert and veloxe's Query/Mutation lists (ADR 0089) | yes (116) |
| 56 | Schema `Interceptors()` | generated runtime | **Different by design**: row filters only (ADR 0040) | no (nextapp registers them on the client instead) |
| 57 | Op names `OpCreate`, `OpUpdateOne`, `OpDeleteOne` | `veloxe/velox.go` | **Missing**: velox2 has only `OpInsert`, `OpUpdate`, `OpDelete` (grep `OpCreate` in the root is empty; UF P9) | yes, 9 in `N/veloxschema` |
| 58 | Soft delete | nextapp's own mixin over veloxe hooks | **Partial**: `softdelete.Mixin` exists but fits nextapp poorly; it cannot assign `deleted_by_*` and always filters reads (UF N-V11) | yes, 279 entities |

### 1.4 Query builder

| # | veloxe feature | veloxe | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 59 | Generic field predicates (EQ, NEQ, In, GT…, Contains, HasPrefix, EqualFold, ContainsFold, IsNil) | generated `where.go` | **Have**: handle methods; `FieldEQ`… by name | yes (2088 `.Where(`) |
| 60 | Edge predicates `HasX`, `HasXWith` | generated | **Have**: `Rel.Has/HasWith`; `velox.HasEdge/HasEdgeWith` | yes (258) |
| 61 | Raw predicates: `func(*sql.Selector)`, `sql.P`, `sql.ExprP`, `WhereP` | `veloxe/dialect/sql/predicate.go` | **Partial**: guarded `velox.Expr`, `FieldInSelect` (0079/0091), `InSelect` (0057). No access to the selector (ADR 0069) | yes (397 `sql.Selector`, `lib/paidshare/paidshare.go:38`) |
| 62 | JSON predicates (`sqljson`) | `veloxe/dialect/sql/sqljson` | **Have**: `JSONCol.Text/Number/Bool/HasKey/Has/HasSome/HasEvery/IsEmpty` (0058/0080) | yes (13, `loader/customfield.go:69`) |
| 63 | Order: `ByX(sql.OrderDesc())`, `OrderByField`, expression orders (`NULLS LAST`) | generated | **Have**: `OrderBy(h.Asc()/Desc())`, `Calc(...).Desc()`; NULL sorts largest on every dialect (0032) | yes (`financialledger/reverse.go:445`, `pricelistitem/reprice.go:123`) |
| 64 | Limit, Offset | generated | **Have** | yes |
| 65 | `Unique(bool)` (DISTINCT) | `veloxe/compiler/gen/sql/entity_pkg.go:821` | **Missing**: no `Query.Distinct`; only `CountDistinct` | no |
| 66 | Eager loading `WithX(opts)` | generated | **Have**: `With(rel.Where/OrderBy/LimitEach/With)` | yes |
| 67 | Named eager loads `WithNamedX` | `query_pkg.go:128` | **Missing** (see #23) | no |
| 68 | Edge traversal `QueryX()` | generated | **Different by design**: `Rel.Of(owners...)`, `HasWith`; "there is no `QueryPosts()`" (`docs/coming-from-ent.md:165`) | yes (~60 hits) |
| 69 | `Select(fields)` plus `Scan`, `Strings`, `Ints`, … | `entity_pkg.go:835,971` | **Partial**: `Columns/Omit` for entity reads; `Select(terms).Rows` + `Scan`; no `Strings(ctx)`-style helpers | yes (45 `Select`, `bill/validation.go:849`) |
| 70 | First, Only, All, Count, Exist | generated | **Have**: `First`, `One`, `All`, `Count`, `Exists` (`query.go:573`) | yes (Exist 356, Only 167) |
| 71 | `IDs`, `FirstID`, `OnlyID` | generated | **Partial**: `Columns(ID).All`; no `IDs` helper | yes (IDs 75, `middleware/api_key_auth.go:152`) |
| 72 | Panicking `X` variants (`AllX`, …) | generated | **Different by design**: none, as in idiomatic Go (no ADR) | no |
| 73 | Aggregates: `GroupBy(...).Aggregate(Sum…).Scan` | `entity_pkg.go:839,842` | **Have**: `Sum/Avg/Min/Max`, `SumBy…` (0022), `Select/GroupBy/Having/Rows` (0074) | yes (7, `bankreconciliation/gl_book_balance.go:147`) |
| 74 | `Modify(func(*sql.Selector))` | `entity_pkg.go:836` | **Different by design** (ADR 0069). Window functions are not in the builder (`docs/guide.md`, "Reports") | yes (51; `COUNT(*) OVER ()` at `aggregate/generic.go:253`) |
| 75 | Paginate (Relay cursors) | `entity_pkg.go:881` | **Have**: `Query.Paginate` (0059), `After` keyset, `veloxgql` cursors | yes (444) |
| 76 | `Clone` | `entity_pkg.go:851` | **Different by design**: builders are values | no |
| 77 | `Explain` | `query_pkg_terminals.go:458` | **Missing**: `Query.SQL(ctx)` shows the SQL but runs no EXPLAIN | no |
| 78 | Row locks with actions (`ForUpdate(sql.WithLockAction(sql.SkipLocked))`) | `entity_pkg.go:857` | **Have**: `.ForUpdate().SkipLocked()` | yes (`taxprovidertransaction/worker.go:321`) |
| 79 | `Unwrap()` (detach an entity from its tx client) | `entity_pkg.go:66` | **Different by design**: an entity is a plain struct with no client in it | yes (4, `postransaction/intercompany_gl.go:311`) |

### 1.5 Mutations

| # | veloxe feature | veloxe | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 80 | Create + `SetX` | generated create builders | **Have**: `db.Insert(&x)`, `.Set(...)` | yes |
| 81 | `SetNillableX(*T)` (set only when non-nil) | generated | **Partial**: insert takes a struct (a pointer field is nil or not). An update has no set-if-non-nil setter | yes (2829 `SetNillable`) |
| 82 | `CreateBulk`, `MapCreateBulk` | `entity_client.go:152,165` | **Have**: `db.InsertAll` (`insert.go:39`) | yes (233 / 6) |
| 83 | Upsert: `OnConflict`, `OnConflictColumns`, `UpdateNewValues`, `Ignore`, `DoNothing`, `Update(func)` | `create.go:357,1069` | **Partial**: `OnConflict(cols).Update(cols)/DoNothing`, `ConflictClause.Where`. No "update every column" shortcut | no |
| 84 | Update: `Where`, `SetX`, `AddX`, `ClearX` | generated | **Have**: `Update().Set(h.Set/Add/Sub/SetNull)` | yes (Clear 790, Add 8) |
| 85 | `UpdateOne`, `UpdateOneID` | generated | **Have**: `UpdateQuery.One` (0048/0092), `db.Save` | yes (1529) |
| 86 | `SkipDefault(s)` (leave `UpdateDefault` alone) | `update.go:999-1008` | **Missing** (grep `SkipDefault` is empty) | yes (36, `opportunity/related_records.go:418`) |
| 87 | Delete, `DeleteOne`, `DeleteOneID` | generated | **Have**: `Delete().Where().Exec` returns the count; there is no NotFound on 0 rows | yes (203) |
| 88 | Edge mutations `AddXIDs`, `RemoveXIDs`, `ClearX`, `SetXID` | generated | **Have**: `Rel.Add/Remove/Clear/Set` setters (ADR 0044) | no ID-list calls (0) |
| 89 | Mutation API in hooks (`Field`, `SetField`, `OldField`, `AddedField`, `Cleared*`, `AddedEdges`, `Type()`) | `mutation.go:961` | **Partial**: `velox.Mutation` has `Op`, `Value`, `Set`, `Fill`, `Rows`, `Predicates`, `Where`; `untyped.MutationTable(m)` gives the entity. No edge-change view, no `OldField` | yes (16 in schema mixins; hand-ported, UF P11) |
| 90 | `OldX(ctx)` | generated | **Different by design**: `Col.Old(change)` from `db.Record` (ADR 0094) | no |

### 1.6 Client, runtime hooks, transactions, drivers, errors

| # | veloxe feature | veloxe | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 91 | `Client.Use` (hooks for every entity at run time) | `client.go:309` | **Partial**: `velox.WithHooks[E]` per entity (`hook.go:120`), and mixin hooks. Nothing for every entity at once | yes (21 sites, `app/access_policy.go:35`; the hook dispatches on `m.Type()`) |
| 92 | `Client.Intercept` (interceptors for every entity at run time) | `client.go:326` | **Partial**: `velox.WithRewrite` sees every statement (`options.go:63`), and loads leave room for its conditions (`internal/engine/load.go:167-171`). That it reaches every `HasWith` subquery is unverified | yes (`infra/database/database.go:404,410`, `app/row_scope.go:532`) |
| 93 | Transactions: `Tx`, `BeginTx`, `WithTx`, commit, rollback | `tx.go` | **Have**: `db.Tx` (savepoints, retries, isolation, read-only, timeout), `velox.UseTx` (0066) | yes (133 `Tx(ctx)`) |
| 94 | Per-transaction `OnCommit` / `OnRollback` | `veloxe/compiler/gen/sql/tx.go:219` | **Partial**: `WithTxAround` is per DB; a `db.Record` sink runs before commit. No per-transaction after-commit callback | yes (`auditlog/status_events.go:126`) |
| 95 | `Debug()` driver, `Log` option | `client_options.go:27` | **Partial**: `WithAround`, `contrib/veloxotel`, `Query.SQL`; no one-line debug logger | yes (`infra/database/database.go:393`) |
| 96 | Driver: `sql.OpenDB(dialect, db)` | `veloxe/dialect/sql/driver.go` | **Have**: `velox.Open/Connect/OpenConn`, `veloxpgx` | yes (`database.go:375`) |
| 97 | Error helpers: `IsNotFound`, `IsConstraintError`, `IsValidationError`, `IsNotSingular`, `NotLoadedError` | generated `errors.go`, `veloxe/runtime/errors.go` | **Partial**: `IsNotFound`, and `errors.As` to `*ConstraintError`/`*ValidationError`, `ErrNotSingular`. "helpers for them would be an addition" (`doc.go:296-300`) | yes (`IsConstraintError` 109, `mq/trigger/app/writers.go:440`) |
| 98 | `runtime` registries: `NewEntityQuery(name)`, `NodeResolver(s)`, `WithConfigContext`, `FindMutator` | `veloxe/runtime/registry.go` | **Partial**: `untyped.Query(db, table)`, `EntityTable`/`LinkEntity` (0081); `Noder` under compat (0088) | yes (`service/accessexplain/record.go:144,242`, `service/node/node.go:675`) |
| 99 | Read replicas, shards, deadlines, SQL comments, optimistic locking, change recording | not in veloxe | velox2-only (context, not counted) | n/a |

### 1.7 `privacy` package (`veloxe/privacy`)

| # | veloxe feature | veloxe | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 100 | Decisions `Allow/Deny/Skip`, `Allowf/Denyf/Skipf`, `IsDecision`, `DecisionContext/FromContext` | `privacy.go:27-55,240` | **Have**: `privacy/privacy.go:58-202` | yes (Skip 105, `employee/compensation_sync.go:67`) |
| 101 | `Policy{Query, Mutation}`, `QueryPolicy`, `MutationPolicy`, `QueryRule`, `MutationRule`, `QueryMutationRule`, `FilterFunc`, `Filter`, `Filterable` | `privacy.go:134,289-312` | **Have** (ADR 0089). `Filter.Where` takes a `velox.Predicate`, not `WhereP(*sql.Selector)` (`privacy/privacy.go:40-44`) | yes (Policy 71, FilterFunc 24) |
| 102 | `NewPolicies`, `Policies` | `privacy.go:160,174` | **Different by design**: velox composes the policies itself (`privacy/privacy.go:43`) | comments only |
| 103 | `OnMutationOperation`, `DenyMutationOperationRule`, `AlwaysAllowRule`, `AlwaysDenyRule`, `ContextQueryMutationRule` | `privacy.go:61-126` | **Have** | yes (AlwaysAllowRule 2) |
| 104 | Combinators `And`, `Or`, `Not` | `combinator.go:13,47,95` | **Missing** (none in `privacy/privacy.go`) | no |
| 105 | Viewer, role and tenant rules (`WithViewer`, `HasRole`, `HasAnyRole`, `IsOwner`, `TenantRule`, `TenantFilterRule`, `DenyIfNoViewer`, `SimpleViewer`) | `rules.go:14-442` | **Missing**; package `scope` covers tenancy (`scope.NewKey`, ADR 0029/0038) | no |
| 106 | Rule trace (`WithTrace`, `TraceFrom`) | `trace.go:28-45` | **Missing** | no |

### 1.8 querylanguage / entql

| # | veloxe feature | veloxe | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 107 | `querylanguage.P` expression tree (`F`, `EQ`, `And`, `Or`, `FieldEQ`…) evaluated into SQL through generated descriptors | `veloxe/querylanguage/querylanguage.go:146-355`, `compiler/gen/sql/entql.go`, `filter.go:38` | **Partial**: name-based `velox.Predicate` builders and `untyped` queries do the same job; there is no serialisable tree (see #22) | no |

### 1.9 Dialect features

| # | veloxe feature | veloxe | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 108 | An exported SQL builder (`sql.Selector`, `sql.Builder`, `sql.Select`, `sql.Table`, `sqlgraph`) | `veloxe/dialect/sql/builder.go` | **Different by design**: "not ent's `Modify`, which needs an exported SQL builder" (ADR 0069) | yes (545 `sql.Selector`, 85 `sql.Builder`) |
| 109 | Table and column comments | `sqlschema.WithComments`, `Comment` | **Have**: `schema.Comment`, field `Comment`, `sqlschema.WithComments` | yes (Comment 120) |
| 110 | Dialect capability probes | `veloxe/dialect/capability.go` | **Partial**: velox2 fixes version floors instead (ADR 0013) | unknown |

### 1.10 Migration

| # | veloxe feature | veloxe | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 111 | Auto-migration: `client.Schema.Create`, `WriteTo`, `WithDropColumn`, `WithDropIndex`, `WithForeignKeys` | `veloxe/dialect/sql/schema` | **Different by design**: versioned files only, applied in-process with `migrate.Up` (ADR 0006, `docs/guide.md` "Migrations") | yes (`N/cmd/migrate/main.go:423-446`) |
| 112 | Versioned migrations through Atlas | `examples/versioned-migration` | **Have**: velox's own `migrate`, plus `migrate sql` for Atlas | no |
| 113 | Generated `migrate.Tables` | generated `migrate/schema.go` | **Partial**: `velox-gen describe -json`, `migrate sql`, `untyped.TableOf` | yes (`lib/conditions/softdelete.go:64`) |

### 1.11 contrib GraphQL (`veloxe/contrib/graphql`)

| # | veloxe feature | veloxe | velox2 status | used by nextapp? |
|---|---|---|---|---|
| 114 | Generator extension (SDL + Go) | `graphqlgen.NewExtension` (`graphqlgen/extension.go`) | **Have**: the `velox-graphql` plugin, for gqlgen | yes (`generate.go:17`) |
| 115 | `WithoutGQLGen` (output that does not import gqlgen; served by graphql-go) | `graphqlgen` `:374` | **Missing**: only `only=enums` (ADR 0068), and its output still imports gqlgen (UF N-V1, N-V3) | yes (`generate.go:18`) |
| 116 | `WithSchemaGenerator`, `WithSchemaPath` | `:476,283` | **Partial**: always writes `velox.graphql` into `-out`; the path is fixed (`contrib/graphql/cmd/velox-graphql/main.go:103`) | yes |
| 117 | `WithSchemaSplitMode(SchemaSplitPerEntity)`, `WithSplitSchema` | `:330,308` | **Missing**: one SDL file | yes (`generate.go:21`) |
| 118 | Where inputs: `WithWhereInputs`, `WhereOps`, `WhereInput`, `EnableWhereInputs`, `WhereInputEdges` | `annotation.go` | **Have** (`annotation.go:478-510`; OpsJSONArray 0080, fold ops 0078) | yes (WhereOps 46, OpsJSONArray 40) |
| 119 | `WhereInputFields` | `annotation.go` | **Missing** | no |
| 120 | `RelayConnection`, `QueryField` | `annotation.go` | **Have** (0077) | yes (451 / 448) |
| 121 | `WithNodeInterface`, `WithRelaySpec`, Node/nodes, global IDs | `:415,404`, `gen_entity_node.go` | **Different by design** (0061); Node under `compat=veloxe` (0088) | yes (compat) |
| 122 | Mutation inputs: `WithMutations`, `Mutations(Create, Update)`, `SkipMutation*`, `Omittable` | `:383`, `mutation_input.go` | **Have**: Create/Update inputs with `Setters()` (0045), `SetInput` (0062); null means not given | yes (SkipMutationCreateInput 24) |
| 123 | Ordering: `OrderField`, `MultiOrder`, `WithOrdering` | `annotation.go` | **Have**, plus `OrderVia` and `OrderUnguarded` | yes (749) |
| 124 | Skip modes | `annotation.go:16-46` | **Have** (`contrib/graphql/annotation.go:23-37`) | yes (416) |
| 125 | `Type`, `Map`/`Resolvers`, `FieldName`, `Directives`, `NewDirective`, `Deprecated`, `EnumValues` | `annotation.go` | **Have** (0071, 0087) | yes (Map 497, Resolvers 379, Type 261) |
| 126 | Federation (`Federation*`, `WithFederation`) | `annotation.go`, `:502` | **Missing** | no |
| 127 | Subscriptions (`Subscription`, `SubscriptionField`) | `annotation.go` | **Missing** | no |
| 128 | Interfaces and unions (`InterfaceField`, `Implements`, `UnionMember`) | `interface_fields.go`, `gen_entity_unions.go` | **Missing** | no |
| 129 | Input validation annotations (`CreateInputValidate`, `UpdateInputValidate`, `MutationInputValidate`) | `annotation.go` | **Missing**; inputs run the schema's validators | no |
| 130 | Smaller annotations (`Unbind`, `MapsTo`, `Mapping`, `CollectedFor`, `FieldMutationOps`, `EnableOrderField`, `EnumValue`, `MergeAnnotations`, `SkipField`) | `annotation.go` | **Missing** | no |
| 131 | Generator hooks (`WithSchemaHook`, `WithOutputWriter`, `WithMapScalarFunc`, `WithTemplates`, `WithGQLGenConfig`, `WithConfigPath`, `WithORMPackage`) | `graphqlgen` `:425-618` | **Missing**; the plugin takes only `only` and `compat` options (`main.go:79-80`) | no |
| 132 | `WithMaxFilterDepth` | `:511` | **Partial**: `veloxgql.Limits` (depth, size, cost) | no |
| 133 | Relay runtime (`gqlrelay`: cursors, PageInfo, field collection, selection projection) | `contrib/graphql/gqlrelay` | **Partial**: `veloxgql` cursors and `gqlgraph` projection and edge batching, but gqlgen only; no engine-neutral selection source (UF N-V2) | yes (669 `gqlrelay` imports) |
| 134 | A transaction per mutation (`gqlgentx`) | `contrib/graphql/gqlgentx` | **Have**: `veloxgql.NewExtension(db, WithTxOptions, WithTxFilter)` (`extension.go:149-183`) | unknown (graphql-go server) |
| 135 | Runtime input decoding (`gqlinput`, `ormgql`) | `contrib/graphql/gqlinput`, `ormgql` | **Partial**: generated inputs and `Pred(ctx)` | unknown |
| 136 | `contrib/dataloader` | `contrib/dataloader/dataloader.go` | **Different by design**: `veloxgql.Batch`, `velox.LoadEach/CountEach`, `gqlgraph.EdgeOne` | no |

### Counts

| status | rows |
|---|---|
| Have | 54 |
| Different by design | 23 |
| Partial | 29 |
| Missing | 29 |
| (context row #99, velox2-only) | 1 |
| **Total** | 136 |

---

## 2. Gaps that matter

The order is nextapp's need first (count of call sites, and whether it blocks
the port), then how common the pattern is in ent and veloxe apps.

### 2.1 Needed by nextapp

1. **velox-graphql for graphql-go (#115, #117, #133).** nextapp serves
   GraphQL with graphql-go (`generate.go:18`), at about 450 entities. Two
   things stop it from using velox-graphql today:
   - Its full output targets gqlgen, and its enum output imports gqlgen.
   - Projection and batching read gqlgen's field context.

   **Proposal.** Split `veloxgql` into two packages:
   - an engine-free core: IDs, cursors, marshalers, `Batch`, limits;
   - a gqlgen adapter.

   Add a `SelectionSource` interface in the core, and fail closed when none
   is installed, instead of logging once. Add plugin options `engine=graphql-go`
   (SDL + autobind, no `@goModel`/`@goField`) and `split=entity`, which
   writes `velox_<entity>.graphql` per entity. The generated code stays one
   line per method over `gqlgraph`, as ADR 0047 has it. The parked spec
   `docs/superpowers/specs/2026-11-03-graphql-go-backend-design.md` should be
   refreshed against ADRs 0045–0061 first. This is UF N-V1/N-V2/N-V3.

2. **A Go API for generation, with a field-order rule (#1, #9, #12, §3).**
   nextapp drives generation from Go and keeps one hook with no velox2 home:
   `SortFieldsHook` puts `workspace_id` first and the timestamp and actor
   columns last.

   **Proposal.** Export what `runGenerate` does as a package (§3). Add a
   narrow option, `FieldOrder func(entity string, fields []string) []string`,
   applied to the model before `codegen.Lower`. Do not add a general
   model-rewrite hook. Order is the only rewrite nextapp still needs, and a
   general hook would let generated descriptors drift from the schema values
   that velox reads behaviour from (ADR 0040).

3. **Hooks and row filters for every entity, at run time (#91, #92, #21).**
   nextapp registers on the client, not the schema:
   - a soft-delete interceptor;
   - a permission (row-scope) interceptor;
   - an access-policy hook, two custom-field hooks and a refund-status hook.

   Each dispatches on the entity name. velox2 offers per-entity
   `WithHooks[E]` and the low-level `WithRewrite`.

   **Proposal.** Two DB options:
   - `velox.WithHooksAll(hs ...Hook)`: the hook sees every entity's
     `Mutation`; `untyped.MutationTable(m)` names the entity.
   - `velox.WithFilter(func(ctx context.Context, t untyped.Table) (velox.Predicate, error))`.

   `WithFilter` is a row filter with the same reach as a schema Interceptor:
   every read, write, load, join and `Has`/`HasWith` subquery. Like an
   Interceptor, `WithoutPolicy` does not lift it, and a zero `Predicate`
   fails the statement. Behaviour stays in velox's terms (row filters, not
   ent's query middleware), so ADR 0040 still holds. This needs a short ADR.

4. **Leave `UpdateDefault` alone on an update (#86).** nextapp has 36 calls to
   `SkipDefault(updated_at)`. They are system writes that must not bump
   `updated_at`.

   **Proposal.** `UpdateQuery.KeepDefaults(cols ...Column[E])` and the same
   on `SaveQuery`. It refuses a column that has no `UpdateDefault`. It is one
   generic method in the root, so it adds nothing per entity. Today,
   `SetExpr(U.UpdatedAt, "?", U.UpdatedAt)` may do the same. Unverified:
   check whether an explicit assignment already wins over `UpdateDefault`.

5. **Set a field only when given (#81).** nextapp has 2,829
   `SetNillableX(p)` calls. On an update they become
   `if p != nil { set = append(set, h.Set(*p)) }`.

   **Proposal.** `SetPtr(p *V) Setter[E]` on the value handles. It returns a
   setter the builders skip when `p` is nil. On a `NullCol`, a nil `p` could
   mean `SetNull` instead; the name must make the difference plain. Both are
   generic handle methods, so the shape of generated code does not change.

6. **Error helpers and op aliases (#97, #57).** nextapp has 109
   `IsConstraintError` calls, and 9 schema uses of `OpCreate`, `OpUpdateOne`
   and `OpDeleteOne`.

   **Proposal.** Add `velox.IsConstraintError`, `IsValidationError` and
   `IsNotSingular`. Each is `errors.As`/`Is` underneath, safe with a typed
   nil, as `IsNotFound` is. `doc.go:299` already calls them "an addition".
   Add `OpCreate = OpInsert`, and `OpUpdateOne`/`OpDeleteOne` as ops that a
   hook filter treats like `OpUpdate`/`OpDelete`. The alternative is to put
   them in package `privacy`'s veloxe names only (ADR 0089 style). Choose one.

7. **A callback after a transaction commits (#94).** nextapp publishes events
   after commit (`auditlog/status_events.go:126`). The outbox and
   cache-invalidation patterns are common.

   **Proposal.** `velox.AfterCommit(ctx, func(context.Context))`, valid
   inside `db.Tx`. It queues the callback on the transaction and runs it
   after a successful outer commit, never on rollback. A savepoint's
   callbacks are dropped if the savepoint is undone. Outside a transaction
   it fails, which keeps it fail-closed. `WithTxAround` stays the DB-wide
   form.

8. **Read keys and single columns (#71, #69).** nextapp has 75 `IDs(ctx)` and
   a few `Strings(ctx)` calls.

   **Proposal.** A generic function `velox.Values(ctx, q Query[E], c Col[E,V]) ([]V, error)`,
   with `velox.IDs` built on it using the key handle. It is a projection of
   one term, so the Read policy applies. It is a function, not a method,
   because a generic method on a handle type costs every entity (ADR 0022's
   reasoning).

9. **A debug logger (#95).** nextapp turns on `velox.Debug()` from config.
   **Proposal.** `velox.WithLog(*slog.Logger)`, an `Around` that logs each
   statement's SQL, duration and rows. It shows the arguments only when
   `WithLogArgs()` is given, and never a `Sensitive` column's value.

10. **Moving from auto-migration (#111).** nextapp runs `Schema.Create` with
    drop flags and dry-run DDL (`cmd/migrate/main.go:423-446`). velox2 has
    versioned files only (ADR 0006). The refusal stands. What is missing is
    the path: `migrate baseline` adopts the existing database, then
    `migrate diff`, `apply` and `migrate.Up` replace `Schema.Create`, and
    `-dry-run` replaces `WriteTo`.

    **Proposal.** A section of a `docs/coming-from-veloxe.md` (UF N-V8) that
    walks nextapp's `cmd/migrate` through it. The same guide maps `Modify`
    and `sql.Selector` to `Expr`/`Calc`/`Select`, `intercept` to
    `WithFilter`, `gqlrelay` to `gqlgraph`, and `Unwrap` to nothing.
    Done 2026-11-22 (UF N-V8): `docs/coming-from-veloxe.md`.

11. **Window functions in projections (#74).** nextapp's generic aggregate
    service pages groups with `COUNT(*) OVER ()` (`aggregate/generic.go:253`).

    **Proposal.** Allow `OVER (...)` in `Calc`/`SelectRaw` text, still
    refusing subqueries. Or give a `velox.CountAll[E]().Over()` term. Today it
    is raw SQL, which gets no policies; that is UF N-V7's isolation risk.

12. **Soft-delete options (#58).** UF N-V11's ask still stands:
    - `softdelete.Mixin{Assign: ...}` for `deleted_by_*` columns;
    - `softdelete.Mixin{Reads: softdelete.Unfiltered}`.

    nextapp worked around it, so this is low priority.

13. **Back-references on load (#24).** nextapp turns the feature on. Whether
    its code reads `child.Edges.Parent` after `parent.With(children)` is
    unknown.

    **Proposal.** Off by default, as in ent. A `Rel.WithBack()` load option
    would fill the inverse `One` of each loaded child with its owner. JSON
    must not loop: `One.MarshalJSON` would skip a back-reference. First grep
    nextapp's services for that read; drop the item if nothing reads it.

### 2.2 Common ent and veloxe patterns nextapp does not use

- **Edge schemas, `edge.Through` (#46).** A many-to-many join with its own
  columns. **Proposal.** Let a schema named as the join entity declare
  fields, with `edge.To(...).Through("memberships", Membership.Type)`.
  velox-gen already writes the join entity. Extra fields would become
  columns, and `Rel.Add` would need the extra values (a setter list).
  Until then the workaround is two one-to-many edges to an explicit entity.
  Done 2026-11-22 (ADR 0109): Add fills defaults and fails when a column
  has none, instead of a setter list.
- **DISTINCT (#65).** `Query.Distinct()` for projections only, since entity
  reads are by key.
- **Explain (#77).** `Query.Explain(ctx) (string, error)` reusing `SQL`, run
  with each dialect's `EXPLAIN`.
- **Update every column on upsert (#83).** `InsertQuery.UpdateAll()`: every
  column but the key and the conflict columns, with ADR 0017's
  `UpdateDefault` rule kept.
- **Privacy combinators and rule trace (#104, #106).** `privacy.And`, `Or`,
  `Not` over rules, and `privacy.WithTrace` for debugging policy decisions.
  These are small and keep veloxe's names.
- **GraphQL federation, subscriptions, interfaces and unions (#126-#128).**
  Each is a velox-graphql feature of its own. Build them on demand; no
  current consumer needs them.
- **`FeatureWhereInputAll` (#37).** A plugin option `where=all` for apps
  coming from entgql. Low priority, since velox2's whitelist default is
  deliberate.
- **`FeatureCheckBounds` (#38).** A velox-gen option, or a `sqlschema`
  annotation, that turns `Min`, `Max` and `Range` into CHECKs. Low priority.
- **Named edges (#23).** Low priority: GraphQL batches by SQL already (ADR
  0054).
- **`field.Deprecated`, `field.ValueScanner`, `field.Any`, `sqlschema.View`
  (#40, #43, #44, #50).** Build on demand.

### 2.3 Refused by design: does the refusal still look right?

| refusal | ADR / doc | still right? |
|---|---|---|
| `Modify` and an exported SQL builder | 0069 (also 0074, 0095) | Yes. `Expr`, `Calc`, `Select` and `SetExpr` cover nextapp's predicate and select cases with policies kept. The remaining hole is window functions (§2.1 item 11), which `Calc` could allow. |
| Interceptors as query middleware (`intercept` package) | 0040, `coming-from-ent.md:219` | Yes, for the schema. Run-time filters for every entity are still needed (§2.1 item 3) and fit as row filters. |
| Global IDs and Node | 0061 (compat Node in 0088) | Yes. compat covers nextapp. |
| Auto-migration | 0006 | Yes. It needs the migration guide (§2.1 item 10). |
| Templates | plugins (`plugin/doc.go`) | Yes. Plugins compose and run out of process. |
| Global ID type | int64 by default, per-schema `id` | Yes. |
| `NewPolicies` | `privacy/privacy.go:43` | Yes. velox composes policies. |
| Edge traversal `QueryX` | `coming-from-ent.md:165` | Yes. `Rel.Of` and `HasWith` express it. |
| `Clone`, `X` variants, `Unwrap` | value builders, plain structs | Yes. |
| Upsert fires no `UpdateDefault` | 0017 | Yes. It matches veloxe and ent. |
| Snapshot | layout (0037/0040) | Yes. It has no purpose in velox2. |

---

## 3. The codegen entry point

### 3.1 What veloxe accepts

`compiler.Generate(schemaPath, cfg, opts...)` (`compiler.go:131`) runs
`LoadGraph` (`:79`), which does three things:
1. loads the schema with `load.Config{Path, BuildFlags}`;
2. takes the default target, the schema's parent directory;
3. derives `cfg.Package` from the module and the target.

It then runs the configured `Generator` (SQL by default), wrapped by the
`gen.Hook`s.

**`gen.NewConfig(...)` options** (`option.go`):

| option | what it does |
|---|---|
| `WithHeader(s)` (:16) | sets the header comment of every generated file |
| `WithIDType("int"\|"int64"\|"uint64"\|"string"\|"uuid")` (:25), `WithIDTypeInfo` (:49) | sets the default type of the implicit id |
| `WithPackage(importPath)` (:61) | sets the generated root package's import path |
| `WithSchema(importPath)` (:73) | sets the schema package's import path |
| `WithTarget(dir)` (:85) | sets the output directory |
| `WithFeatures(fs...)` (:98) | turns features on; a feature's `Requires` are turned on with a warning |
| `WithStorage` (:118), `WithStorageDriver("sqlite"\|"mysql"\|"postgres")` (:128) | sets the dialect used for migration code |
| `WithHooks(hs...)` (:149) | middleware around `Generator.Generate(*Graph)`, which may change the graph |
| `WithTemplates(ts...)` (:158) | adds external text templates (ent-style) |
| `WithAnnotations(a)` (:167) | sets global annotations that templates and extensions read |
| `WithBuildFlags(fs...)` (:178) | sets flags for `packages.Load` and the `go` commands it spawns |
| `WithGenerator(g)` (:188) | replaces the dialect generator |

**`compiler.Option`s** (`compiler.go`):
- `Storage(typ)` (:185);
- `FeatureNames(names...)` (:198);
- `Annotations(...)` (:224);
- `BuildFlags(...)` (:244) and `BuildTags(...)` (:253);
- `TemplateFiles`, `TemplateGlob` and `TemplateDir` (:259-275);
- `Extensions(...)` (:326), and the named registry `RegisterExtension`
  (`extension_registry.go:16`);
- `Dependency(DependencyType, DependencyTypeInfo, DependencyName)`
  (:373-433), which adds injected fields to builders.

**Features**: see §1.2. The default-off flags are privacy, intercept, entql,
namedges, bidiedges, schema/snapshot, sql/upsert, sql/execquery,
sql/versioned-migration, sql/globalid, sql/autodefault, sql/checkbounds and
graphql/whereinputall. `sql/lock`, `sql/modifier` and `validator` are
deprecated no-ops. sql/schemaconfig, sql/entpredicates and
graphql/whereinputall are marked Stable.

### 3.2 What velox-gen takes today

`velox-gen [generate] [-out dir] [-package name] [-plugin tool[:k=v,...]]... [-check] [package]`
(from `go run ./cmd/velox-gen -h`).

| flag | where it goes |
|---|---|
| package (default `./veloxschema`) | `cmd/velox-gen/generate.go:72` `schemaPackage` |
| `-out` | `genFlags.outDir` (:98) |
| `-package` | `genFlags.packageName` (:112) |
| `-plugin` | `cmd/velox-gen/plugin.go` runs `go tool <tool>`, speaking `plugin.Request`/`Response` |
| `-check` | `reportCheck` (:418) |

The pipeline:
1. `loadModel` (:163) calls `codegen.Load` (`internal/codegen/load.go:116`):
   inspect, then a `go run` of a generated loader, then `codegen.Build`
   (`build.go:27`). `CheckUserFiles`, `SchemaMethods` and `MixinBehaviour`
   find the declared behaviour.
2. `buildOutput` (:241) calls `codegen.Lower` (`lower.go:34`) with
   `codegen.Config` (`out.go:4`), then `Render` (`render.go:40`), then the
   plugins on `PluginModel` (`plugin.go:21`).
3. `staleFiles` (:487) works out what to write and remove, and
   `writeAllHeld` writes it atomically, interruptible by a signal.

### 3.3 Proposed Go API

Package `github.com/syssam/velox/generate`. It is not `gen`, so it does not
clash with the generated `veloxgen` or with ent habit. `cmd/velox-gen` and
`go:generate` would both call it.

```go
type Config struct {
    Schema  string   // package pattern; default "./veloxschema"    (positional arg)
    Out     string   // default "./veloxgen"                        (-out)
    Package string   // default: base of Out                         (-package)
    Plugins []Plugin // run in order on the model                    (-plugin)
    Check   bool     // write nothing; report stale files            (-check)
    Env     []string // the go commands' environment; nil = os.Environ()
                     // (replaces compiler.BuildFlags: GOFLAGS=-p=4)
    // FieldOrder reorders an entity's fields before lowering, the one
    // model rewrite allowed (replaces nextapp's SortFieldsHook). It must
    // return a permutation; anything else fails the run.
    FieldOrder func(entity string, fields []string) []string
    Stdout, Stderr io.Writer
}

type Plugin struct {
    Tool    string            // as go tool names it, e.g. "velox-graphql"
    Options map[string]string // tool:key=value
    // Run, if set, is called in-process with the same Request/Response
    // the JSON protocol carries, so a Go program needs no tool build.
    Run func(context.Context, *plugin.Request) (*plugin.Response, error)
}

type Result struct{ Written, Removed, Stale []string; Warnings []string }

func Run(ctx context.Context, dir string, cfg Config) (Result, error)
```

To implement it:
1. Move `runGenerate`, `loadModel`, `buildOutput`, `staleFiles` and
   `writeAllHeld` from package main into `generate`, unchanged.
2. Have `main` parse flags into a `Config`.
3. Apply `FieldOrder` between `codegen.Build` and `codegen.Lower`, and check
   that it returned a permutation.
4. Export an in-process entry point from velox-graphql, e.g.
   `contrib/graphql/generate.Plugin(opts)`, so a `generate.go` can call it
   directly.

### 3.4 Feature flags with no velox2 equivalent, because velox2 always has them

These are always on, so they need no flag:
- privacy: `Policy`, package `privacy`;
- intercept: as row filters;
- lock: `ForUpdate`…;
- modifier: as `Expr`/`Calc`/`Select`/`SetExpr`;
- validator;
- upsert: `OnConflict`;
- execquery: `db.Exec`/`Rows`/`Raw`;
- autodefault: zero `DEFAULT` (ADR 0090);
- entpredicates: handles and `FieldEQ`;
- versioned-migration: own migrate;
- multischema and static schemaconfig: `TableIn`/`sqlschema.Schema`;
- snapshot: not needed.

These would need work:
- namedges (#23);
- bidiedges (#24);
- entql as a serialisable expression (#22, #107);
- run-time schemaconfig (#26): a `WithTableSchema(map)` DB option;
- whereinputall (#37), as a plugin option;
- checkbounds (#38);
- globalid outside compat: refused (0061).

---

## 4. Summary

**Counts (136 rows; one context row not counted below):**

| status | count |
|---|---|
| Have | 54 |
| Different by design | 23 |
| Partial | 29 |
| Missing | 29 |

**Top 10 recommendations, in order:**

1. A graphql-go target for velox-graphql: an engine-neutral `veloxgql`
   core, a fail-closed selection source and per-entity SDL split (UF
   N-V1/N-V2/N-V3).
2. A public `velox/generate.Run(ctx, dir, Config)` Go API mirroring
   velox-gen's flags, with in-process plugins and a `FieldOrder` rule (UF
   P12, N-V4).
3. Hooks and row filters for every entity, at run time:
   `velox.WithHooksAll` and `velox.WithFilter`. These replace
   `client.Use`/`client.Intercept`; they are fail-closed and not lifted by
   `WithoutPolicy`. This needs an ADR.
4. `UpdateQuery`/`SaveQuery` `KeepDefaults(cols...)`, for veloxe's
   `SkipDefault` (36 sites).
5. `SetPtr` setters (set only when non-nil), for `SetNillableX` (2,829
   sites).
6. `IsConstraintError`, `IsValidationError` and `IsNotSingular` helpers,
   and `OpCreate`/`OpUpdateOne`/`OpDeleteOne` aliases (109 + 9 sites).
7. `velox.AfterCommit(ctx, fn)`: per-transaction post-commit callbacks.
8. `velox.Values` / `velox.IDs`: single-column reads under policies (75+
   sites).
9. `velox.WithLog(*slog.Logger)` debug logging, which redacts arguments and
   Sensitive values by default.
10. `docs/coming-from-veloxe.md` (UF N-V8), covering auto-migration →
    baseline + versioned files, `Modify`/`sql.Selector` → `Expr`/`Calc`/`Select`,
    `intercept` → `WithFilter`, and `gqlrelay` → `gqlgraph`. Allowing
    `OVER (...)` in `Calc` would go with it. Done 2026-11-22:
    `docs/coming-from-veloxe.md`.
