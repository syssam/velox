# veloxe → velox2 behaviour parity (2026-11-22)

The first gap analysis (`2026-11-21-veloxe-gap-analysis.md`) asked whether
velox2 has an API for each veloxe feature. It marked pagination (#75) and
where filtering (#59, #118) "Have". This report asks a second question: does
the API behave the same for nextapp's callers? Its focus is the GraphQL
surface clients see, since the user's report was "missing page, where, and
many".

**Sources.**
- veloxe: `/Users/s.y.s/Jobs/go/veloxe` at `cfd8da5`, the commit nextapp
  pins. Written `V/` below.
- velox2: this repository at `b816c01f` (main). Paths have no prefix.
- Phase B worktree: `.claude/worktrees/graphql-go` at `42a35d6a`.
  Written `B/`.
- nextapp: `/Users/s.y.s/Jobs/project/nextapp`.
  - `N/` is `nextapp-graphql`, on veloxe.
  - `P/` is `nextapp-graphql-velox2`, the port.
  - `W/` is `nextapp-web/app`.
  - `S/` is `/Users/s.y.s/Jobs/project/strata/packages`. This is the Strata
    UI library, which nextapp-web links through `node_modules/@strata`.

**Probes.** The two probe transcripts are cited as `VP` and `XP`, with a
label such as `VP:P5`.
- `VP` is veloxe's `examples/fullgql`, run in a scratch clone. It serves
  real GraphQL requests through gqlgen against SQLite.
- `XP` is velox2's `gqlgraph` paginator in a scratch clone, given the same
  argument combinations.

The scripts and outputs are listed in §8.

**Classes.**
- **Same**: a caller sees no difference.
- **Diff-OK**: velox2 differs, and the difference is acceptable. The ADR is
  cited where one exists.
- **Breaks**: a nextapp client or caller would see a change.
- **Missing**: velox2 has nothing for the behaviour.

**How nextapp serves GraphQL.** Four facts recur in the tables below.
1. `N/internal/server/pagination.go:100` (`PaginationInterceptor`, the same
   in `P/`) acts before any velox code runs.
   - It refuses a `first` or `last` above `max_page_size` (default 100,
     `N/internal/infra/config/config.go:1171`) with
     `MAX_PAGE_SIZE_EXCEEDED`.
   - It injects `first: 100`, or `last: 100` when a `before` is given, into
     any relay-shaped field that has neither, nested connections included.
2. `N/internal/lib/errors/graphqlgo.go:70` masks any resolver error that is
   not nextapp's own `*Error`, a `*gqlerror.Error` or a coded
   `*graphql.Error`. The client gets `INTERNAL_ERROR` with a generic message
   (`N/internal/lib/errors/graphql.go:125-129`). This holds for veloxe's
   plain errors and, as written today, for velox2's coded `gqlcore.Error`.
3. Argument decoding errors are wrapped by graphql-go as `BAD_USER_INPUT`
   (graphql-go `exec_object.go:323`). The presenter renames that code to
   `GRAPHQL_VALIDATION_FAILED`. A bad cursor shows up this way.
4. nextapp installs no selection source on veloxe (UF N-V2). So veloxe's
   projection, eager loading and "is totalCount selected?" checks never ran
   in nextapp. Every connection read every column and ran the COUNT.

---

## 1. Pagination

| # | behaviour | veloxe | velox2 | class | nextapp uses? | evidence |
|---|---|---|---|---|---|---|
| 1.1 | `first`, and `first`+`after`: the rows, and `hasNextPage` | first N rows after the cursor; `hasNextPage` from N+1 | same | Same | yes: forward paging on every list | VP:P1/P2; XP:P1/P2; velox2 `TestPaginate` |
| 1.2 | `hasPreviousPage` on a forward page with `after` | **true whenever `after` is given** | **false** (Relay allows false) | Breaks | selected 261× in `W/../schema/*.graphql`. nextapp-web never branches on it: DataGrid uses `page.index` (`S/patterns/src/data-grid.tsx:1107`), edge-array uses `pageIndex` (`W/components/ui/description/edge-array.tsx:533`). Callers using API keys or the marketplace may | VP:P2 `hasPreviousPage:true`; XP:P2 `prev=false`; `N/velox/entity/gql_pagination.go:68379-68381`; `contrib/graphql/veloxgql/gqlgraph/paginate.go:645-650` |
| 1.3 | `last`, and `last`+`before`: the rows, in order | last N rows before the cursor, ascending | same | Same | yes: DataGrid's previous page (`S/patterns/src/grid/use-data-grid-query.ts:104-113`) | VP:P3/P4; XP:P3/P4 |
| 1.4 | `hasNextPage` on a backward page with `before` | **true whenever `before` is given** | **false** | Breaks | as 1.2. Strata's inbox reads `hasPreviousPage` on backward pages only, which agrees (`S/patterns/src/inbox/use-conversation-messages.ts:551`) | VP:P4 `hasNextPage:true`; XP:P4 `next=false`; `gql_pagination.go:68376-68378` |
| 1.5 | `first`+`before` | accepted: the first N rows of the window before the cursor, counted from the start | BAD_USER_INPUT "first pages forward, and before goes with last" | Breaks | nextapp-web never sends it: `W/lib/strata/velox-data-source.ts:500-506` sends `last`/`before` or `first`/`after`. Other clients: unknown | VP:P5; XP:P5; `paginate.go:704-716` |
| 1.6 | `last`+`after` | accepted | BAD_USER_INPUT | Breaks | as 1.5 | VP:P6; XP:P6 |
| 1.7 | `after`+`before` (a window), no size | accepted: every row between the two | BAD_USER_INPUT "after and before given together" | Breaks | as 1.5. The interceptor injects a `first` here, so veloxe would answer `first`+`after`+`before` | VP:P7; XP:P7 |
| 1.8 | `first`+`last` | error "invalid pagination parameters" | BAD_USER_INPUT `ErrBadPagination` | Same | both are masked to `INTERNAL_ERROR` (fact 2) | VP:P10d; XP:P10d |
| 1.9 | `first: -1` | error | BAD_USER_INPUT | Same | masked in both | VP:P10a; XP:P10a |
| 1.10 | no `first` or `last` | **every row** (unbounded) | MaxPage (100) rows: forward, or backward from `before` | Same in nextapp (Diff-OK in the library) | the interceptor injects 100 first (fact 1) | VP:P8; XP:P8/P8b; `contrib/graphql/veloxgql/limits.go:142-150` |
| 1.11 | largest page | 1000 (`gqlrelay.MaxPaginationLimit`, `V/contrib/graphql/gqlrelay/helpers.go:14`); 1001 is "pagination limit exceeded" | 100 (`Limits.MaxPage`, settable); 101 is `ErrPageTooLarge` | Same in nextapp (Diff-OK in the library) | the interceptor caps at 100 first (fact 1). Outside a request, `LimitsFromContext` gives 100 (`limits.go:158-163`); service code calls `Paginate` with the client's arguments (`N/internal/service/messaging/search.go:51` and others) | VP:P10b/c; XP:P10b/c |
| 1.12 | `first: 0` and `last: 0` | empty page; `hasNextPage` (or `hasPreviousPage`) true when rows exist | same | Same | yes: 9 documents use `(first: 0` to get only totalCount | VP:P9/P9b; XP:P9/P9b |
| 1.13 | empty result | edges `[]`, cursors null, both flags false, totalCount 0 | same | Same | yes | `V/examples/fullgql/e2e_test.go` `TestPagination_EmptyResult`; velox2 `TestPaginate` |
| 1.14 | when totalCount runs | a COUNT when `TotalCountSelected`; with no selection source (nextapp) on every page | a COUNT only when the field resolves (`Connection.TotalCount`, `paginate.go:591`) | Diff-OK (one COUNT fewer per page that leaves it out; same values) | 453 selections; `reuseTotal` strips it on later pages (`W/lib/strata/velox-data-source.ts:362-370`) | `N/velox/query/gql_pagination_expense_claim.go:33`; VP:P14 |
| 1.15 | totalCount ignores the limit and honours `where` | yes | yes | Same | yes | VP:P15 (totalCount 3, 2 edges); `paginate.go:591-602` |
| 1.16 | a totalCount-only selection skips the page query | yes, given a selection source (not in nextapp) | yes (`Page` is lazy) | Same | yes | V `TestPaginate_TotalCountOnlySkipsThePageQuery`; `paginate.go:396-402` |
| 1.17 | cursor encoding | base64 RawStd of msgpack `{"i":<id>,"v":<value>[,"vs":[…]]}`, e.g. `gqFpAaF2wA` | base64url of JSON `{"v":1,"t":"<Type>","o":[{f,d,v}…],"k":<key>}` | Diff-OK (a cursor is opaque) | yes | `V/contrib/graphql/gqlrelay/types.go:173-230`; `contrib/graphql/veloxgql/cursor.go:55-91`; VP prints the msgpack decode; XP prints the velox2 token |
| 1.18 | velox2 decoding a cursor veloxe wrote, as a client holding it across a deploy would send it | n/a | **refused**: BAD_USER_INPUT "malformed cursor: not a cursor document", for all five veloxe cursors probed | Breaks | yes. Strata keeps the cursor of the page on screen in the URL (`S/patterns/src/grid/use-data-grid-query.ts:135-143`, `use-url-list-state.ts`). An open tab or bookmark past page 1 fails after the deploy; so does a velox2 cursor after a rollback | XP "decode veloxe cursor" lines |
| 1.19 | a cursor taken under another order, or under no order | **accepted silently**: the cursor's value is read as the new order's (a wrong page) | BAD_USER_INPUT "taken under another order" | Diff-OK (fail closed) | Strata drops cursors when the sort changes (`S/patterns/src/grid/use-cursor-pagination.ts:49-55`) | VP:P11c-f; XP:P11c/e/f/g; `gqlgraph/cursor.go:20-30` |
| 1.20 | `after: ""` | read as a zero cursor: an empty page with `hasPreviousPage: true` (a bug) | BAD_USER_INPUT | Diff-OK | never sent; nextapp sends null | VP:P13b; XP decode `""` |
| 1.21 | a malformed cursor | "cannot decode cursor: …", an argument error | "malformed cursor: …", an argument error | Same (same code; the message text differs) | `GRAPHQL_VALIDATION_FAILED` in both (fact 3) | VP:P13; XP decode `garbage!!` |
| 1.22 | `orderBy` shape | one `XOrder` (`[XOrder!]` with MultiOrder); `direction` defaults to ASC | the same under compat (`internal/sdl/order.go:81-90`) | Same | yes: one order only (`W/lib/strata/velox-data-source.ts:164-174`); 0 MultiOrder in `N/veloxschema` | VP:P17b; `P/veloxgen/velox.graphql:164` |
| 1.23 | tie-break by id | **id in the order's own direction**: NAME DESC gives ties id DESC (5,3,1) | **key always ASC**: NAME DESC gives ties 5,9 | Breaks | yes: every DESC sort on a non-unique column (status, dates, amounts); `defaultSort` DESC lists | VP-ties T1/T2; XP:P11b; `N/velox/query/gql_pagination_expense_claim.go:50-51`; `paginate.go:527-533` |
| 1.24 | default order | id ASC | key ASC | Same | yes | `gql_pagination_expense_claim.go:53`; `paginate.go:422` |
| 1.25 | NULLs in the order | the dialect's default: on PostgreSQL ASC puts NULLs last and DESC first; SQLite is the reverse; keyset paging crosses NULLs | NULL is the largest value on every dialect (ADR 0032); keyset paging crosses NULLs | Same on PostgreSQL (nextapp's dialect) | yes (nullable order fields) | VP:P12/P12b/P12d (SQLite); velox2 `TestPaginateNullable` |
| 1.26 | order by a to-many edge's count (`OrderField` on an edge) | yes | not built (ADR 0087, Consequences) | Missing | no (`MEMBER_COUNT` and `HELPFUL_COUNT` are int fields) | ADR 0087 |
| 1.27 | nested connection: `first` per parent | per parent (one query per parent in nextapp) | per owner, batched (`LimitEach`, `gqlgraph/edge.go`) | Same (fewer statements) | 75 nested `(first: 100)`; `exhaust` pages them (`W/lib/strata/velox-data-source.ts:552-570`) | V `TestCollect_NestedConnectionFirstIsPerParent`; velox2 `edge_test.go` |
| 1.28 | nested connection: totalCount, `where`, `orderBy` | the whole edge, filtered | same | Same | yes | V `TestGraphQL_EdgeConnectionWhereFilter`; `paginate.go:591-602` |
| 1.29 | to-many list edge (target has no connection) | every row | at most `MaxList` (1000); more fails with `ErrTooManyRows` | Diff-OK (ADR 0027) | almost none: only edges into `Workspace`, the one schema with `QueryField` and no `RelayConnection` | `limits.go:21-26` |
| 1.30 | offset/limit in GraphQL | none generated | none generated | Same | the app's own report SDL has `offset: Int` (`N/schema/aging_report.graphql:43` and 9 others), served by service code with ORM `Limit`/`Offset` | — |
| 1.31 | error codes for paging errors, as nextapp's clients see them | plain errors, masked to `INTERNAL_ERROR` | coded BAD_USER_INPUT, masked to `INTERNAL_ERROR` by nextapp's presenter as written | Same | yes | facts 2 and 3. Port note: once Phase B adds `GraphQLExtensions` (B spec §1.2), nextapp's presenter could pass BAD_USER_INPUT through |
| 1.32 | the column set read for a connection | every column (no selection source) | the selection's columns, plus the key, the foreign keys and the order columns; a `graphql.Map` field reads every column (ADR 0071) | Diff-OK | yes | `paginate.go:436-445` |
| 1.33 | hand-rolled cursors in service code | `gqlrelay.Cursor{ID, Value, Values}` built and read freely | `veloxgql.Cursor{Type, Terms, Key}`, bound to a type and an order | Diff-OK (a code change on the server only) | 13 files, e.g. `N/internal/service/accountingreport/account_ledger.go:227`, `lookup/page.go:162` | `cursor.go:22-40` |
| 1.34 | `Paginate` on a query that is already ordered | allowed (the connection's order is appended) | refused with an internal error | Diff-OK (server code) | not seen in the 594 `Paginate` sites sampled | `paginate.go:513-516` |

## 2. Where filtering

The velox2 rows here are under `compat=veloxe,where=whitelist`, as the port
generates (`P/tools/velox2proof/phase0.sh:72`).

| # | behaviour | veloxe | velox2 | class | nextapp uses? | evidence |
|---|---|---|---|---|---|---|
| 2.1 | EQ, NEQ, In, NotIn, GT, GTE, LT, LTE | yes | yes, with veloxe's names (`customerId`, `customerIdNEQ`…) | Same | yes: the client sends eq, NEQ, GT/GTE/LT/LTE and In (`W/lib/strata/velox-data-source.ts:42-56`) | ADR 0087; `N/velox/filter/budget.go:633+` |
| 2.2 | Contains, HasPrefix, HasSuffix | escaped LIKE | escaped LIKE | Same | HasPrefix/HasSuffix from the `smith*` wildcard syntax (`velox-data-source.ts:96-120`) | `V/dialect/sql/builder.go:1339,1429` |
| 2.3 | EqualFold, ContainsFold | PostgreSQL: `col ILIKE lower(v)` | `LOWER(col) = LOWER(v)`, `LOWER(col) LIKE LOWER(%v%)` | Same results (Diff-OK on index use: ILIKE vs lower(); nextapp has no trigram indexes) | ContainsFold is the app's "contains" | `V/dialect/sql/builder.go:1399-1438`; `internal/engine/render.go:775-783`; ADR 0078 |
| 2.4 | `xIsNil: true` / `xNotNil: true` | IS NULL / IS NOT NULL | the same, except on a foreign key (2.16) | Same | the "exists" filter (`velox-data-source.ts:130-131`) | VP:P16 `ageIsNil:true`→2 |
| 2.5 | `xIsNil: false` / `xNotNil: false` | adds nothing | adds nothing under compat | Same | rarely | VP:P16 →5; `TestVeloxeNullTests`; ADR 0088 |
| 2.6 | `xIn: []` / `xNotIn: []` | adds nothing | adds nothing under compat | Same | possibly, from a cleared multi-select | VP:P16; `TestVeloxeWhere` "idIn []"; ADR 0088 |
| 2.7 | `or: []`, `and: []`, `and: [{}]`, `where: {}` | each adds nothing | the same under compat | Same | — | VP:P16; `TestVeloxeWhere` |
| 2.8 | `not: {}` and `or: [{}]` | error "empty predicate XWhereInput" | BAD_USER_INPUT, the same text | Same (masked in both) | — | VP:P16; `TestVeloxeWhere` |
| 2.9 | a field set to `null` | the same as absent | the same as absent | Same | the client spreads filter objects | VP:P16 `{name:null}`, `{ageIsNil:null}`, `{nameIn:null}` → 5; `N/velox/filter/gqlunmarshal.go:105-108`; ADR 0062 |
| 2.10 | a null entry in `or`/`and`/`hasXWith` | skipped | skipped under compat | Same | — | `N/velox/filter/budget.go:557-580`; `TestVeloxeWhere` "or [null…]" |
| 2.11 | list width (`idIn` of 150) and node count | no cap | no cap under compat | Same | yes: `idIn` lists over 100 (ADR 0088) | `TestVeloxeWhere` "idIn of 150", "1200 nodes" |
| 2.12 | nesting depth | 5, through not/and/or/hasXWith | `MaxFilterDepth` 5 | Same | — | `N/velox/filter/filter.go:11`, `opportunity.go:792`; `TestVeloxeDepth` |
| 2.13 | `hasX: true/false`, `hasXWith: [...]`: shape | EXISTS; the entries ANDed inside one EXISTS; `[]` adds nothing | the same (`gqlgraph/where.go:310-326`) | Same | ~10 hand-written `hasXWith` sites (`hasTeamMembersWith` ×24 in `W/`, `hasJournalEntryWith`, `hasRolesWith` on a many-to-many) | `N/velox/filter/opportunity.go:780-798` |
| 2.14 | `hasX`/`hasXWith`: which target rows count | the target's schema Policy applies (`runtime.ApplyEntityPolicy`); **client-registered interceptors do not** (soft delete, row scope) | the target's Read policy, Query policy, interceptors **and `WithFilter`** (as `OpSelect`) all apply | Breaks (likely wanted) | soft delete and row scope are client interceptors in nextapp (`N/internal/lib/conditions/softdelete.go:124`, `N/internal/app/row_scope.go:149`); `hasItems: true` stops counting soft-deleted children | `N/velox/expenseclaim/where.go:192-212`; `V/runtime/edge.go:159-186`; `V/examples/multitenant/e2e_test.go:173-192` (the gap is asserted); ADR 0098 |
| 2.15 | `hasXWith` on a many-to-many | through the join table | through the join table | Same (2.14 caveat) | `hasRolesWith` | — |
| 2.16 | predicates on a foreign key bound to an edge (`customerId`, `customerIdIn`, `customerIdIsNil`…) | **plain column predicates** | **scoped by the target's row filter, even under compat**: an ID predicate ANDs `EXISTS(target visible)`; `IsNil` becomes NOT EXISTS, so a hidden or soft-deleted target reads as null; `NotNil` becomes EXISTS | Breaks | yes: ~210 hand-written `where` literals with an `…Id` key (`organizationId`, `customerId`, `employeeId`, `supplierId`… in `W/`); `parentIdIsNil` for tree roots. It also adds an EXISTS per predicate when the target has a Read policy (most do: 116 `Policy()`) | `contrib/graphql/veloxgql/gqlgraph/where.go:515-599`; ADR 0027 §"A foreign key's predicates"; B/`0ca986f7` widens it to `WithFilter` |
| 2.17 | JSON array ops (`OpsJSONArray`: Has/HasSome/HasEvery/IsEmpty) | sqljson | ADR 0080 semantics | Same (not probed) | 40 annotations | ADR 0080 |
| 2.18 | case folding of non-ASCII text | ILIKE (locale-aware) | LOWER (locale-aware) | Same | — | 2.3 |
| 2.19 | Time inputs | RFC 3339, via the engine's Time scalar | the same; graphql-go binds `time.Time` (B spec §3.2) | Same | `createdAtGTE` from `startOfDayISO` (`velox-data-source.ts:75-94`) | — |
| 2.20 | enum inputs | GraphQL value `DONE` ↔ Go `done` | the same (ADR 0068, 0086, 0087) | Same | yes | — |
| 2.21 | ID and UUID inputs | `ID` decoded into int64; the app's own UUID scalar | the same (ADR 0061, 0087) | Same | yes | — |
| 2.22 | an unknown or ill-typed input field | refused by the engine's validation | the same | Same | — | `N/velox/filter/gqlunmarshal.go:78-85` |
| 2.23 | where and the root's privacy and soft delete | the Query policy and client interceptors apply | the Query policy (ADR 0089) and `WithFilter` (ADR 0098) apply | Same | yes | — |
| 2.24 | `WhereInput.AddPredicates` (server code adds a predicate to the input) | yes | no field for it; the caller adds `q.Where(p)` before `Paginate` | Missing | 2 sites | `N/velox/filter/budget.go:520` |
| 2.25 | `where.P()` / `Filter()` in service code | returns `(predicate, error)`; `Filter()` swallows an empty input | `Pred(ctx)` / `WhereOf` | Diff-OK (rename) | 51 / 414 sites (B spec §4) | — |

## 3. Everything else the examples exercise

| # | behaviour | veloxe | velox2 | class | nextapp uses? | evidence |
|---|---|---|---|---|---|---|
| 3.1 | generated create and update inputs: `clearX`, `addXIDs`, `removeXIDs`, `xID`; null means not given | yes | the same shape (`examples/graphql/veloxgen/velox.graphql:181-202`); `Setters`, `SetInput` (ADR 0045, 0062) | Same | no: nextapp's mutation inputs are its own SDL (B spec §0.2) | `V/examples/fullgql/velox/schema.graphql:1170-1195` |
| 3.2 | input validation annotations (`CreateInputValidate`…) | yes | inputs run the schema's validators only | Missing | no | gap analysis #129 |
| 3.3 | ORM CRUD: Create/SetInput/Save, UpdateOne, Delete counts, First/Only, Exist, IDs | yes | yes, plus `Values`/`IDs`, `KeepDefaults`, `SetPtr`, `Is*Error` (ADR 0099, 0100) | Same | heavy: 2,406 `IsNotFound(`, 341 `Exist(ctx)`, 64 `IDs(ctx)` | gap analysis §1.4-1.6 |
| 3.4 | Node: `node(id:)` / `nodes(ids:)` | the app's own resolvers over the registry; ENTITY_NOT_FOUND | the same under compat; `ResolveNode` counts a denial as none | Same | 2 resolvers, 45 refs | ADR 0088 |
| 3.5 | `edge.Through` (edge schemas) | yes | no | Missing | 0 | `V/examples/edge-schema`; `grep Through schema/edge` is empty |
| 3.6 | tree and self edges, eager parent of the root | yes | yes (ADR 0085 for a self one-to-one) | Same (not probed) | yes (parent edges) | `V/examples/tree` |
| 3.7 | multitenancy: tenant stamp on create, scoped reads, bulk writes kept to the tenant | interceptor plus rules; edge predicates not scoped | the scope mixin (ADR 0029, 0038) plus `WithFilter`; edge predicates scoped | Diff-OK (closes the gap veloxe asserts) | yes: workspace pin and row scope | `V/examples/multitenant/e2e_test.go:173-192` |
| 3.8 | JSON fields: an Optional JSON field writes `[]`/`{}`; typed JSON named in the SDL | yes | yes (ADR 0090, 0087) | Same | yes | `V/examples/json-field` |
| 3.9 | transactions: commit, rollback, WithTx, savepoints; OnCommit | yes | `db.Tx` and `UseTx` (ADR 0066); `AfterCommit` (ADR 0100) | Same | 2,185 `WithTx(` | gap analysis #93-94 |
| 3.10 | runtime hooks (`client.Use`), op names | `OpCreate`, `OpUpdateOne`, `OpDeleteOne` | `WithHooksAll` (ADR 0098); only `OpInsert`, `OpUpdate`, `OpDelete` (ADR 0099 refused the aliases) | Diff-OK (server code) | 21 `Use(`; 9 op names in schemas | ADR 0098, 0099 |
| 3.11 | privacy: Query/Mutation lists, Skip/Allow/Deny, DecisionContext | yes | yes, with veloxe's names (ADR 0089) | Same | 116 `Policy()` | ADR 0089 |
| 3.12 | eager loading of edges that GraphQL selects | never ran in nextapp (no selection source): one query per parent | batched per edge (ADR 0054) | Diff-OK (same results, far fewer statements) | every nested edge | UF N-V2; B spec §2 |
| 3.13 | a Required to-one edge whose target is hidden | `MaskNotFound`: the edge is null, the parent nulls | non-null under compat, and the parent nulls | Same | yes | ADR 0087 |
| 3.14 | aggregates and group by | `GroupBy().Aggregate()`, `Modify` (51 sites) | `Select`/`GroupBy`/`Having`/`Calc` (ADR 0074); window functions in `Calc` | Same for clients (the services port the code) | 7 `GroupBy(`, 51 `Modify(` | gap analysis #73-74 |
| 3.15 | upsert | `OnConflict…UpdateNewValues` | `OnConflict(...).Update/DoNothing/UpdateAll` (ADR 0102) | Same | 0 | — |
| 3.16 | migrations | `Schema.Create` (auto), Atlas versioned | versioned only (ADR 0006) | Diff-OK (an operations change) | `N/cmd/migrate/main.go:423-446` | gap analysis #111 |
| 3.17 | GraphQL error codes | presenter-defined | presenter-defined; velox2 errors carry `extensions.code` | Same (nextapp's presenter decides) | yes | facts 2 and 3 |
| 3.18 | back-references on load (`FeatureBidiEdgeRefs`) | enabled | none | Missing | enabled; whether code reads it is unknown | gap analysis #24 |
| 3.19 | `Unwrap`, `NotLoadedError`, `Entity.String` (ORM-only) | yes | plain structs; no client in an entity | Diff-OK (by design) | 4 `Unwrap` | `V/examples/realworld/e2e_test.go:115-147` |

## 4. nextapp's real usage (counts)

**Frontend.** These counts are from `nextapp-web`: `schema/*.graphql`
documents and `app/**/*.ts(x)`, generated `app/gql` left out.
- **List documents.** 113 documents declare `($first: Int, $after: Cursor,
  $last: Int, $before: Cursor, $orderBy: XOrder, $where: XWhereInput)`.
  Example: `schema/budget.graphql:2-3`.
- **Selections.** 374 select `pageInfo`, 261 `hasPreviousPage` and 453
  `totalCount`.
- **Nested pages.** These are `(first: 100)` ×75, `(first: 1)` ×32,
  `(first: 50)` ×13 and `(first: 0)` ×9.
- **Paging variables.** They are only `first`[+`after`] or `last`+`before`:
  - `app/lib/strata/velox-data-source.ts:500-506`;
  - `S/patterns/src/grid/use-data-grid-query.ts:104-113`.
- **The previous button.** It comes from the page index, not
  `hasPreviousPage`:
  - `S/patterns/src/data-grid.tsx:1107`;
  - `app/components/ui/description/edge-array.tsx:533`.
- **Cursors in the URL.** Cursors are kept in the URL:
  `S/patterns/src/grid/use-data-grid-query.ts:135-143`.
- **Order.** It is a single `{field, direction}`:
  `velox-data-source.ts:164-174`.
- **Where operators sent.**
  - Suffixes: `""` (eq), `NEQ`, `ContainsFold`, `HasPrefix`, `HasSuffix`,
    `GT`, `GTE`, `LT`, `LTE` and `In`.
  - `IsNil`/`NotNil`, sent only as true.
  - Range bounds as ISO timestamps.

  Evidence: `velox-data-source.ts:42-56,75-94,130-153`.
- **Foreign-key filters.** About 210 hand-written `where`/`baseWhere`
  literals use an `…Id` key, for example `organizationId`, `customerId`,
  `employeeId` and `supplierId`. About 10 hand-written sites use
  `has…With`.

**Server.** These counts are from `nextapp-graphql`, `internal`,
`graph/velox` and `cmd`, with `_test` and generated files left out.
- **Paginate calls.** 444 in `internal` and 150 in `graph/velox` (452 root
  resolvers).
- **Where-input methods.** `where.Filter` 414, `where.P()` 51 and
  `AddPredicates` 2.
- **Hand-rolled cursors.** 13 files use `gqlrelay.Cursor` (87 reads of
  `.ID`, `.Value` or `.Values`).
- **Hand-built PageInfo.** 5 services set `HasPreviousPage` themselves, for
  example `internal/service/lookup/page.go:130` and
  `customobject/sort.go:752`.
- **Interceptor and presenter.** `PaginationInterceptor` and
  `GoErrorPresenter` are the same in the port: `diff` of `pagination.go` is
  empty.
- **Out of scope.** `admin-graphql` is a separate veloxe application,
  pinned to a different veloxe version.

---

## 5. Breaks the port, ranked by impact on nextapp's clients

1. **veloxe cursors are refused after the deploy (1.18).**
   - Who sees it: every user with a list open, or bookmarked, past page 1.
     Strata keeps the cursor in the URL, so the next fetch fails with
     `GRAPHQL_VALIDATION_FAILED` until the user goes back to page 1. A
     rollback breaks the same way in the other direction.
   - Fix: under compat, accept a legacy cursor.
     - Recognise base64 RawStd msgpack `{i, v, vs}`. A small hand decoder
       for that subset suffices: fixmap, ints, strings, nil and the
       cursor-time extension 1, so no msgpack dependency.
     - Read it as `Key = i`, and `v` (or `vs`) as the current order's term
       values. veloxe read a cursor the same way, without checking the
       order.
     - Keep emitting velox2 cursors. Add an opt-in `veloxgql` option to
       emit the legacy format for one rollback window.
   - Phase: **B, task 2.** Task 2 reworks `Cursor` (plain
     `MarshalGQL`/`UnmarshalGQL`). The flag comes from `gqlgraph.Type.Veloxe`,
     as ADR 0088 does.
2. **Ties sort the other way under DESC (1.23).**
   - Who sees it: any list sorted descending by a non-unique column. The
     rows within a tie reverse: veloxe orders them id DESC, velox2 id ASC.
     Users see rows move between pages and positions.
   - Fix: when `Type.Veloxe` is set and the order has one term, the key
     takes that term's direction, as veloxe did
     (`gql_pagination_expense_claim.go:50-51`). The cursor checks
     (`checkCursor`) carry the key's direction already, through
     `key:<col>` terms.
   - Phase: **not B.** It is a gqlgraph compat change and can land on main
     now. It touches only `newConnection` (`paginate.go:527-533`) and its
     tests.
3. **Foreign-key predicates are scoped under compat (2.16).**
   - What changes:
     - `customerId: X`, where X is soft-deleted or hidden by policy,
       returns no rows.
     - `parentIdIsNil: true` matches rows whose parent is hidden.
     - Every such predicate on a policy-bearing target pays an EXISTS. That
       target is on about 210 hand-written client filters.
   - Why the scoping does nothing under compat: compat already shows the
     foreign key in the type as an `ID` (ADR 0087). So scoping its
     predicates hides nothing. It only changes results and cost.
   - Fix: when `Type.Veloxe` is set, `EdgeIDField` acts as `IDField` plus
     plain `IsNil`/`NotNil`. These are veloxe's column predicates. Amend
     ADR 0027 for compat.
   - Phase: **B, task 1.** Task 1 is
     `0ca986f7 gqlgraph: FK predicates see run-time row filters`, which
     widens the same code. Make it skip under `Veloxe` in that commit, or
     soft delete will add an EXISTS to every foreign-key filter.
4. **`hasX`/`hasXWith` now respect soft delete and row scope (2.14).**
   - What changes: a parent whose only matching children are soft-deleted,
     or row-scope-hidden, no longer matches.
   - Recommendation: keep velox2's behaviour. veloxe's own multitenant
     example records the old behaviour as a known gap. Write it into
     `docs/coming-from-veloxe.md`, and have nextapp sign off on its ~10
     `hasXWith` sites.
   - Fallback: if parity is required, add a compat-only option that leaves
     interceptors and `WithFilter` out of `Has` subqueries, keeping the
     Read and Query policies, as veloxe's `ApplyEntityPolicy` did.
   - Phase: **not B.** It is a decision first, then perhaps an engine
     option.
5. **`hasPreviousPage` and `hasNextPage` when a cursor is given (1.2, 1.4).**
   - What changes: veloxe says true for a forward page with `after` and for
     a backward page with `before`. velox2 says false. nextapp-web does not
     branch on either flag. A client on the API or the marketplace that
     shows a "previous" control from `hasPreviousPage` would hide it.
   - Fix: when `Type.Veloxe` is set, `run` sets `HasPreviousPage = after
     != nil` forward and `HasNextPage = before != nil` backward. This is
     Relay-legal.
   - Phase: **not B.** It is a gqlgraph compat change, a few lines in
     `paginate.go:645-650`, and goes with item 2.
6. **`first`+`before`, `last`+`after` and `after`+`before` are refused (1.5-1.7).**
   - Who sees it: nextapp-web never sends these. Unknown for API-key
     clients.
   - Fix: when `Type.Veloxe` is set, accept them with veloxe's meaning:
     - `after`+`before` is a window;
     - `first`+`before` is the first N rows of the window before the cursor;
     - `last`+`after` is the last N rows after it.

     It needs a second keyset bound in the engine, which has `After` only
     (an untyped `Before(row)` that reverses the order). Check nextapp's
     access logs first; drop the item if no client sends these.
   - Phase: **not B.** It is an engine plus gqlgraph change, low priority.

Rows 1.10, 1.11, 1.29 and 1.31 would break a veloxe application that has no
pagination interceptor and no masking presenter. In nextapp, its own server
code hides each of them.

## 6. Missing

| # | behaviour | nextapp | note |
|---|---|---|---|
| 1.26 | order by a to-many edge's count | 0 | ADR 0087 left it out |
| 2.24 | `WhereInput.AddPredicates` | 2 | workaround: `q.Where(p)` before `Paginate` |
| 3.2 | input validation annotations on generated inputs | 0 | gap analysis #129 |
| 3.5 | `edge.Through` | 0 | Phase C |
| 3.18 | back-references on load | enabled, reads unknown | gap analysis #24 |

## 7. A standing parity example

**Recommendation.** Yes, add a standing parity example. Do not port veloxe's
`examples/realworld` as it is.

**Why not realworld as it is.**
- It is 3 schemas (Task, User, Workspace), HTTP handlers, and one 150-line
  e2e test (`V/examples/realworld/e2e_test.go`).
- That test covers an edge connection, a to-one edge and `Unwrap`. The last
  is by design absent in velox2.
- Its policies use `privacy.DenyIfNoViewer` and `HasRole`, which velox2 left
  out (gap analysis #105).
- It holds no pagination or where assertions.
- A literal port is about 500 lines and proves little.

**What to build instead.** The veloxe example that pins the semantics in
question is `examples/fullgql`: 10 schemas, 2,042 lines of `e2e_test.go`
plus about 850 in five other test files. They cover pagination both ways,
validation, ties, filters, nested connections, collect, Node, many-to-many,
interface fields and CRUD. Build `examples/veloxeparity` (or name it
`examples/realworld`, if the user prefers that word) as follows.
- **Schemas.** Port fullgql's schemas, about 450 lines. Generate them with
  `compat=veloxe,where=whitelist`, and with `engine=graphql-go,split=entity`
  once Phase B task 4 lands. Serve them through gqlc and `graphqlgo.With`, as
  nextapp serves its API, with a copy of nextapp's `PaginationInterceptor`.
- **Transcript test.**
  - Record about 60 GraphQL requests and veloxe's responses as goldens. The
    probe in §8 is the seed: 1.1-1.25 and 2.4-2.13.
  - Compare velox2's responses after normalising cursor strings.
  - Feed the legacy cursors into the velox2 server (item 1).
- **Ported assertions.** Port fullgql's GraphQL-facing assertions:
  `TestPagination_*`, `TestCollect_*`, `TestGraphQL_*`, M2M, node and edge
  methods. That is about 1,200-1,500 of fullgql's 2,900 test lines. Skip the
  ORM-only rows that are different by design (`Unwrap`, `NotLoadedError`,
  `String`).

**Size.** About 2,000-2,500 lines, 2-3 days, and one CI lane of about a
minute. Phase B task 6 already plans `examples/graphqlgo` on velox's own
schema. Building it beside that example, or folding the compat variant into
it, shares the gqlc wiring.

**What it would have caught.** The suite fails today on items 1, 2, 3 and 5.

## 8. Probe scripts and outputs

All of these live in `/Users/s.y.s/Jobs/go/.scratch/parity/`, outside both
repositories.

| path | what |
|---|---|
| `veloxe/` | a clone of veloxe at `cfd8da5`; `examples/fullgql/velox` copied from the original checkout and regenerated with `go run generate.go` |
| `veloxe/examples/fullgql/probe_parity_test.go` | `TestProbeParity` (labels P0-P17: paging combinations, errors, orders, NULLs, cursors, totalCount, where edge cases) and `TestProbeParityTies` (T1-T2: tie order) through gqlgen |
| `veloxe-probe.out`, `veloxe-probe-ties.out` | the raw responses (`VP`) |
| `veloxe-cursors.json` | the cursors veloxe wrote, fed to the velox2 probe |
| `velox2/` | a clone of velox2 at `b816c01f` |
| `velox2/contrib/graphql/veloxgql/gqlgraph/probe_parity_test.go` | `TestProbeParity`: the same combinations on `gqlgraph.PageWith`, the decode of the veloxe cursors, and the errors' codes |
| `velox2-probe.out` | its output (`XP`) |

**How to rerun.**
- veloxe:

  ```
  cd /Users/s.y.s/Jobs/go/.scratch/parity/veloxe/examples/fullgql
  go test -count=1 -v -run '^TestProbeParity' .
  ```

- velox2:

  ```
  cd /Users/s.y.s/Jobs/go/.scratch/parity/velox2/contrib/graphql
  go test -count=1 -v -run '^TestProbeParity$' ./veloxgql/gqlgraph
  ```

**Existing tests cited as evidence.** All of these passed on the velox2
clone: `TestPaginate`, `TestPaginateNullable`, `TestPaginateNullableVia`,
`TestVeloxeWhere`, `TestVeloxeDepth`, `TestVeloxeNullTests`,
`TestResolveNode` and `TestResolveNodes`. veloxe's `TestPagination_*` passed
on its clone.

## 9. Counts

The count is per table row, §1-§3 (78 rows). These rows count as Same:
"Same in nextapp" (1.10, 1.11), "Same on PostgreSQL" (1.25), "Same results"
(2.3) and "Same for clients" (3.14).

| class | rows |
|---|---|
| Same | 50 |
| Diff-OK | 14 |
| Breaks | 9 (1.2, 1.4, 1.5, 1.6, 1.7, 1.18, 1.23, 2.14, 2.16), ranked as 6 items in §5 |
| Missing | 5 |
