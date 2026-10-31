# Triage G: followups lines 178–347 (GraphQL plans 1–4, Before 1.0 (veloxgql), API plans 1–5)

Checked against `main` at b8f006f. Lines already marked resolved, done or moved, with no open remainder, are left out (L180–182, 196–201, 208, 212–216, 220, 224, 242–248, 254–259, 271, 279–280, 282–283, 287, 298, 303, 332–333, 337). Paths are repo-relative. CG = `contrib/graphql`.

| id | followups line | summary | category | before-1.0 | effort | proposed fix |
|---|---|---|---|---|---|---|
| G1 | 183 | An entity with an auto ID gets no `id`/`idIn` filters, because no field carries the WhereOps annotation. This is still the case: `example/graphql/gen/velox.graphqls:134,231,317` has no `id` predicates. | FEATURE | no: adding where fields later is additive SDL | M | Give the node key `OpsEquality` by default, or let an entity-level `graphql.WhereOps` cover it. Files: CG/internal/sdl/where.go:163, CG/internal/gogen/where.go, goldens. |
| G2 | 184 | `Create()/Update()/Delete()` are package-level names | STALE | n/a | – | Already `MutationCreate/Update/Delete` (CG/annotation.go:228-234). |
| G3 | 185 | The plugin path check passes Go-ignored or build-constrained base names (`_x.go`, `.x.go`, `x_linux.go`, `x_test.go`). Still open: `checkPluginPath` checks only directories for a leading `.`/`_` (cmd/velox-gen/plugin.go:227). | DEFECT | yes: such a file is accepted and then silently ignored, or built only on one OS | S | Refuse a `.go` base that starts with `_` or `.`, ends in `_test.go`, or has a GOOS/GOARCH suffix (go/build's rule). Files: cmd/velox-gen/plugin.go, plugin_test.go, and the doc at plugin/plugin.go:40. |
| G4 | 186 | "No such tool" is not pinned by a test, and the error has no `go get -tool` hint. Still open: runPlugin returns the raw `go tool` error (cmd/velox-gen/plugin.go:150-162). | TEST | no: an error-message improvement | S | When stderr says no such tool, add "run go get -tool <path>". Add a test in cmd/velox-gen/plugin_test.go. |
| G5 | 187 | Weak tests: the inverse join edge, the duplicated blog loader, the round-trip test. The inverse join edge is now covered (CG/internal/gogen/render_test.go:1080, CG/cmd/velox-graphql/mutation_e2e_test.go:17). Two `testModel` loaders still read one model.json (CG/internal/sdl/render_test.go:33, CG/internal/gogen/render_test.go:41). | TEST | no | S | Share one loader through an internal test helper. Strengthen the round-trip test (which one is not named; likely SDL→gqlparser→fields). |
| G6 | 188 | `Directive.Args` maps are shared between merged annotations. Still true: `slices.Concat` copies the slice but not the maps (CG/annotation.go:166; `Directives` at :289 likewise). | DEFECT | no: the merged annotation is only marshaled to JSON; nothing mutates Args | S | `maps.Clone` each Args in Merge and in Directives. |
| G7 | 189 | The `Op` constants lack doc comments | STALE | n/a | – | The block has a group comment (CG/annotation.go:37), which revive accepts: zero findings in 43e4d11. |
| G8 | 190 | The deps test admits `schema/...` subpackages | TEST | no | S | Make the direct-imports loop require exactly `velox2/schema`. The `-deps` loop needs subpackages (schema/internal/desc). File: CG/deps_test.go:21. |
| G9 | 191 | A lone `\r` in a description is not normalized. Still open: `desc()` only replaces `\r\n` (CG/internal/sdl/render.go:702). | DEFECT | yes: a raw CR lands in generated SDL, so output varies by platform; cheap | S | After the `\r\n` replace, also replace `\r` with `\n`. Add a case to render_test.go. |
| G10 | 192 | `childrenIDs` vs entgql's `childIDs` | STALE | n/a | – | Frozen by ruling (plugin.Singular names join columns). No action. |
| G11 | 202 | Application (custom Go type) enums are not bound with `@goModel`. Still open: CG/doc.go:225 lists it as "not written", and no `@goModel` is emitted. The Go side already uses the app type (`levels.Level` in where inputs, CG/cmd/velox-graphql/e2e_test.go:309,973), so the app must list the enum in gqlgen.yml models by hand. | FEATURE | no: additive SDL, and the workaround is a gqlgen.yml entry | M | Emit `@goModel(model: "<PkgPath>.<Type>")` on the enum (CG/internal/sdl/render.go, surface.go:334). Update CG/doc.go:225. |
| G12 | 203 | No test of a custom enum on a renamed entity | TEST | no | S | Add it to e2eEvent with `graphql.Type(...)`, alongside G11. |
| G13 | 204 | Refuse `@goModel` as an application directive once velox emits it | FEATURE | no: needed only with G11 | S | Add `goModel` beside `goField` in CG/internal/sdl/rules.go:173. |
| G14 | 221 | JSON path predicates (`->`, `JSON_EXTRACT`) | FEATURE | no | L | Per-dialect core predicates, then where-input ops. |
| G15 | 222 | Custom time Go types are refused by velox-graphql | FEATURE | no | M | Have the model describe a conversion (plugin.Field gains a "defined type of" fact). |
| G16 | 223 | `@goModel` enums (duplicate) | STALE | n/a | – | Same item as G11. |
| G17 | 230 | `Bidi` self-edges: a symmetric edge needs both directions written | FEATURE | no: documented one-way (CG/doc.go:220). Bidi is an additive model flag | M | Add `plugin.Edge.Bidi` and write the inverse row in gogen/mutation.go. Note that data written one way before then stays one way. |
| G18 | 231 | `Query.With[T]` is a method with an extra type parameter, against ruling 21. Still a method (query.go:61). This is the same item as the "1.0 release" last bullet (followups:412). | API | yes: changing it to a function after 1.0 breaks callers | M | Measure the compile-cost rows (compilecost_test.go). Either move it to a function form like `Sum`, or record why it stays. |
| G19 | 232 | Plan 4's other left-outs | STALE | n/a | – | A pointer list only. Each item is tracked elsewhere (G11, G17, G40, G41, G47, G48). |
| G20 | 233 | Linux re-measure of bench/scale, BenchmarkNestedRequest and the alloc budget | RELEASE | yes: thresholds are darwin-only | M | Do it from the first Linux CI run (followups "1.0 release"). |
| G21 | 234 | `GOFLAGS=-mod=mod` overwrites the user's GOFLAGS. Still open at bench/scale/main.go:146,169 and internal/tempmod/tempmod.go:90. It also drops `-p=2` on memory-constrained runs. | TEST | no: test and bench harness only | S | Append to the existing `GOFLAGS` (`os.Getenv("GOFLAGS")+" -mod=mod"`) in all three places. |
| G22 | 235 | `go mod tidy -e` in `tempmod.WriteGraphQL` can mask resolution errors. Still open: internal/tempmod/tempmod.go:51. | TEST | no | S | Run tidy without `-e`, or after tidy check that every error is the levenshtein test dep. |
| G23 | 236 | A batch reads under its first caller's context and may read another replica | STALE | n/a | – | Accepted (plan 4 ruling 13) and documented (CG/veloxgql/batch.go:23, doc.go:72). |
| G24 | 237 | `veloxgql.Cost` counts every fragment branch and ignores `@skip`/`@include` | FEATURE | no: documented as an upper bound (CG/veloxgql/cost.go:24) | S | Evaluate `@skip`/`@include` from the operation's variables in cost.go. |
| G25 | 238 | The alloc budget holds under pinned gqlgen only | STALE | n/a | – | By design: it is skipped under gqlgen-latest. No action. |
| G26 | 260 | `Save` allocates its `cols` slice. Still true (write.go:202). | PERF | no: no budget pins it | S | Do it with the pgx split's perf pass, e.g. a fixed array for the 1–4 column case. |
| G27 | 261 | Empty InsertAll; Save compile test's cgo note | STALE | n/a | – | Accepted, no change. |
| G28 | 267 | `Lower`'s rule errors carry no position | STALE | n/a | – | Fixed: `builder.fail` records `Pos` (internal/gen/build.go:85-102) and `Error()` prints it (internal/gen/graph.go:122). Commits 612dcf1, 89e118f. |
| G29 | 268 | Errors in velox2's own mixins print module-cache paths. Still true: `desc.Caller` records the constructor's line (schema/internal/desc/desc.go:23). | FEATURE | no: error UX | S | Skip frames inside `velox2/schema/mixin` so the position is the user's `Mixin()` line. |
| G30 | 269 | A plugin panic's stack is dropped. Still true (plugin/main.go:81). | FEATURE | no | S | Append `debug.Stack()` to the error when an env var (e.g. `VELOX_PLUGIN_TRACE=1`) is set. |
| G31 | 270 | Plugin path case folding does no Unicode normalization (cmd/velox-gen/plugin.go:145 `strings.ToLower`) | DEFECT | yes: refusing more later would break plugins; cheap now | S | Refuse non-ASCII plugin paths in `checkPluginPath` (simplest), or fold with NFC+`unicode.SimpleFold`. |
| G32 | 272 | Linux CI never runs the case-respell path of the stale-file sweep | TEST | no | M | Add a test hook that folds case on Linux, or a CI job on a case-insensitive volume. |
| G33 | 273 | The `Marshaler` exemption of `Field.Default` has no number case left | STALE | n/a | – | The ruling stands. Loosening it is additive. |
| G34 | 281 | `TestPredTwiceOnOneContext` does not pin zero extra cost per call | TEST | no | S | Add `testing.AllocsPerRun` on the second call. |
| G35 | 284 | The `gqlgen@latest` job fails red by design | STALE | n/a | – | By design. The supported range is documented (CG/doc.go:384-387). |
| G36 | 285 | Type-level directives (federation `@key`) need an opt-in | FEATURE | no: today it is a refusal; an opt-in is additive | M | Add an annotation that marks a directive as non-guard, or copy guards onto XConnection/XEdge/XWhereInput. |
| G37 | 286 | `ErrorPresenter` gives no hook for request-cancellation errors (CG/veloxgql/presenter.go:116-128) | FEATURE | no: not wrong. Note that `ErrorPresenter(log)` has no options slot, so adding one later needs a new function or a variadic param | S | If wanted, give `ErrorPresenter` variadic options now and add `OnCanceled`. |
| G38 | 293 | MySQL without `clientFoundRows=true` makes `UpdateX` of an unchanged row fail NOT_FOUND. Open has no check (db.go:130). Moved to the pgx split. | DEFECT | yes: wrong result on MySQL. The pgx split reshapes Open before 1.0 | M | In the split's `Open(d, Conn)`, take the MySQL config and refuse or warn when the flag is missing. Files: db.go, the new conn constructors. |
| G39 | 294 | Self-edges one way (duplicate) | STALE | n/a | – | Same item as G17. |
| G40 | 295 | A caller-supplied key is not in the create input, so such entities cannot be created through GraphQL (CG/internal/sdl/input.go:55 refuses) | FEATURE | no: today a refusal; additive | M | Add an opt-in annotation that puts the key in the create input. |
| G41 | 296 | JSON append and partial updates of a JSON field | FEATURE | no | M | Add core `Append` assignments, then update-input fields. |
| G42 | 297 | `@goModel` enums (duplicate) | STALE | n/a | – | Same item as G11. |
| G43 | 299 | Clearing or removing children silently skips the ones the caller may not change | FEATURE | no: documented (CG/doc.go:220-222) | M | Return or record the skipped count (an extension warning, or an error option). |
| G44 | 300 | A created row is returned as written, and an updated one is read back without the Read policy (CG/internal/gogen/mutation.go:1038 `velox.WithoutPolicy`). The UPDATE itself is Read-scoped, so only hook- or default-set values of the new state can leak. | DEFECT | yes: changing what a mutation returns after 1.0 is a client-visible break, so decide it now | S | Decide. Either read back under the Read policy and return null/NOT_FOUND when it is hidden, or keep this and pin it in CG/doc.go as a contract. |
| G45 | 301 | No savepoint inside the extension's transaction: a resolver that swallows a mutation error commits its partial writes (CG/veloxgql/gqlimpl/mutation.go:24-29). | API | yes: transaction semantics of every mutation; the followup says reconsider before 1.0 | M | `Atomic` becomes `db.Tx(ctx, fn)`, which makes a savepoint when nested. Drop `shared.OperationTxFromContext`. Re-measure `TestNestedRequestBudget`. |
| G46 | 302 | `TestMutationsRun`'s inner `go test` runs without `-race` (CG/cmd/velox-graphql/mutation_e2e_test.go:1689) | TEST | no | S | Pass `-race` under a `//go:build race` const. |
| G47 | 304 | An O2O re-point can hit a hidden current child. The unlink is policy-scoped (CG/internal/gogen/mutation.go:1022-1029), so the hidden child stays linked. The link then fails "unique constraint violated", which reveals the child, or with no unique index the row gets two children. | DEFECT | yes: an info leak and a data-integrity gap | M | Before re-pointing, run a scope-free `COUNT` of the current child. Refuse with "already linked" (BAD_USER_INPUT) when it is hidden. Add a test in mutation_e2e_test.go. |
| G48 | 305 | A one-sided M2M leaves join rows on the target's delete: `writeDelete` only walks `e.Edges` (CG/internal/gogen/mutation.go:~1084). | DEFECT | yes: the FK refuses the delete, or rows dangle | S | Also gather the model's M2M edges whose `Target` is e, plus a golden and an e2e case. |
| G49 | 306 | `internal/gogen/mutation.go` repeats itself: "is no field" appears 4 times, plus the own/other, repoint and CheckExists emission | TEST | no: a refactor with no behaviour change; the goldens pin it | M | Factor `joinCols(ed)`, `fkOf(ent, ed)` and `repoint(...)`. The goldens do not change. |
| G50 | 307 | The generated owner-key check uses `In(k)` on one key where `EQ(k)` fits (golden gql_post.go:574) | API | no: a function body, not a signature; golden churn only | S | Emit `.EQ(k)` for a single key in mutation.go and update the goldens. |
| G51 | 308 | Generated godoc is long: UpdateX's is about 350 words per entity (CG/internal/gogen/mutation.go:737) | DOCS | no | M | Move the shared semantics into CG/doc.go and leave a contract and a pointer per function. |
| G52 | 309 | An M2M add checks only Read on the target, so a link can be made to a row the caller may not write | DEFECT | yes: a security semantics decision, flagged "decide before 1.0" | M | Decide whether a link needs Write on both ends. If yes, use a Writable check in the add-IDs CheckExists in gogen/mutation.go, with an e2e test. |
| G53 | 315 | A tx span has no namespace, server or shard, because `TxInfo` has no route | FEATURE | no: additive field | M | Add the route to `velox.TxInfo` and read it in contrib/veloxotel/tx.go. |
| G54 | 316 | Adapter outside a retrying hook: the duration includes the first attempt's loop body | STALE | n/a | – | Documented order (contrib/veloxotel/doc.go:47-55, config.go:58). |
| G55 | 317 | A sampler cannot sample by route target | FEATURE | no: the route is final only after the statement | – | None planned. |
| G56 | 318 | `DriverCode` order inside `errors.Join` | STALE | n/a | – | Documented and harmless. |
| G57 | 319 | No `Config.Filter`, `Config.Attributes` or `network.peer.address` | FEATURE | no: Config is a struct, so new fields are additive | M | Add the fields in contrib/veloxotel/config.go. |
| G58 | 320 | The adapter's go.mod lists the SDK and pgx for tests (contrib/veloxotel/go.mod:7-11; CG lists modernc sqlite) | RELEASE | no: graph pruning keeps them out of builds | M | Move the SDK/pgx tests to a nested test module. |
| G59 | 321 | `record` allocates its attribute set under a no-op MeterProvider (contrib/veloxotel/metrics.go:72) | PERF | no | S | Skip building the set when the instrument is not enabled (`Enabled(ctx)`). |
| G60 | 322 | `collection(st)` ran three times | STALE | n/a | – | Fixed: computed once (contrib/veloxotel/statement.go:20). `operation` allocates nothing. |
| G61 | 323 | The timing tests use sleeps and wall-clock bounds | TEST | no | S | Widen the bounds, or inject a clock. |
| G62 | 324 | Two clock reads per Iter row under an Around hook | STALE | n/a | – | Accepted: cheap, and 0 with no hook. |
| G63 | 330 | Optional `velox.aggregate` span attribute | FEATURE | no | S | Add it in veloxotel attrs.go when `st.Aggregate` is set. |
| G64 | 331 | `clientFoundRows` check (duplicate) | STALE | n/a | – | Same item as G38. |
| G65 | 334 | The core still links pgx (go.mod:7; `OpenPgx` at db.go:137) | API | yes: the pgx split reshapes `Open`, which is next before 1.0 | L | The pgx split: `Open(d, Conn)`, `velox.SQLConn`, `veloxpgx.Conn` in a subpackage. |
| G66 | 335 | Left to velox-gen: positions, mixin paths, panic stacks, case folding | STALE | n/a | – | A pointer list only: G28, G29, G30, G31. |
| G67 | 336 | TxInfo route (duplicate) | STALE | n/a | – | Same item as G53. |
| G68 | 338 | The aggregates take `NumberCol` only, so a nullable column's aggregate goes through `Raw` | FEATURE | no | M | When asked for, add `NullNumberCol` overloads (separate functions). |
| G69 | 339 | `Avg` is float64, exact only to 2^53 | STALE | n/a | – | Documented (aggregate.go:84-85). |
| G70 | 340 | No sentinel for a failed lock; InnoDB locks scanned rows | FEATURE | no: additive sentinel. The behaviour is documented (query.go:123,151) | S | `velox.ErrLocked`, matched from 55P03 and 3572 in errclass.go. |
| G71 | 341 | Rewrite may not change Count/Exists/Aggregate/Lock or their Columns | STALE | n/a | – | A rule, documented (statement.go:154-157). |
| G72 | 342 | Aggregates stay functions; `SetAffected` on an insert is a misuse | STALE | n/a | – | A ruling. The misuse is refused (`errInsertAffected`, scope_api.go:326). |
| G73 | 343 | The LIKE escape is `!`, which supersedes ruling 12 | STALE | n/a | – | Informational. |
| G74 | 344 | Compile-cost bounds keep about 1.2% margin, and entityDescUse is near its bound | STALE | n/a | – | Informational (compilecost_test.go:265: 757436 measured / 766976 bound). |
| G75 | 345 | The MySQL lock itests read performance_schema through `VELOX_TEST_MYSQL_ADMIN` | STALE | n/a | – | Informational; CI sets it. |
| G76 | 346 | GOPROXY=off failures say "run go mod download" | STALE | n/a | – | Done. |

## Review items that never reached the followups file and are still open

From docs/history/reviews/2026-10-20-api-review-{core,schema,contrib}.md. Most findings were taken by docs/history/specs/2026-10-20-api-cleanup-design.md and are in the code. I checked Tx ctx, `ErrOutsideTx`, `Allow`, `ErrDryRun`, the renames, `Exec`/`Raw`, `SaveQuery`, plugin protocol and model 2, `EdgeKind`, `TableSchema`, `Indexes`, the exported builders, `Number[T]`, `EnumOf`, `UUIDOf`, `OnDelete`, dialect keys, CLI `generate`/`version`/exit 3/`describe -json`, annotation JSON tags, `NewExtension`, `WithTxFilter`/`WithTxOptions`, the gqlgen supported range, and the InsertQuery.SQL doc. The following are open and appear nowhere in the followups file.

| id | source | summary | category | before-1.0 | effort | proposed fix |
|---|---|---|---|---|---|---|
| R1 | schema review #16 | Generated structs put `omitempty` on `time.Time`, which has no effect (internal/gen/testdata/golden/blog/want/user_velox.go:20-21). Edges already use `omitzero`. | API | yes: changing the generated JSON output after 1.0 breaks clients | S | Emit `omitzero` for struct-typed fields, or throughout, in internal/gen/render.go, and update the goldens. |
| R2 | schema review #16; left out by spec §6b.18 | The `// Model: sha256:` header hashes the internal Graph (internal/gen/render.go:99). It churns `velox.go` on velox upgrades, and `-check` already compares bytes. | API | no: generated-file header only | S | Drop the line, or hash only the schema input. |
| R3 | schema review #16; left out by §6b.18 | Generated enums have `Values()` only, with no `String()` or `Valid()` (golden velox.go:21) | FEATURE | no: additive methods | S | Emit both in internal/gen/render.go. |
| R4 | schema review #9; left out by §6b.18 | No "did you mean" for `Ref` and index names | FEATURE | no | S | Add a Levenshtein suggestion in the internal/gen/build.go rule errors. |
| R5 | schema review #15; left out by §6b.18 | Edge schemas through the DSL (`edge.To(...).Through(name, T.Type)`; core has `EdgeThrough`, the DSL has none), `Deprecated(reason)`, `SchemaDefault`, `schema.Handles` for irregular plurals | FEATURE | no: additive. The review says make `Through` Should if "ports after an import-path change" is meant seriously | L | A DSL design for each. `Through` first, if ent porting matters for 1.0. |
| R6 | schema review #15 | `mixin.AnnotateFields`, per-column index `Desc` and index type, and table-level CHECK and charset are absent (no matches in schema/) and not mentioned in the spec either | FEATURE | no | M | Additive DSL options, together with the migrate planner. |
| R7 | contrib review #17 | Nothing warns that `graphql.Type` renames change every ID and cursor clients hold (CG/annotation.go:211; IDs embed the type, CG/veloxgql/doc.go:12) | DOCS | no | S | One sentence on `graphql.Type` and in CG/doc.go. |
| R8 | contrib review #15; left out by spec §6c.16 with no follow-up | Renaming the module `contrib/graphql` to `contrib/veloxgql`, matching `contrib/veloxotel` | API | yes: an import-path change, free only before 1.0. Decide it, even if the answer is "keep" | S (decide) / M (do) | Decide. If renaming, move the module and fix the imports, docs and example. |
| R9 | core review #5 | `Desc.Tenant` uses -1 for "none", so a zero `Desc` makes column 0 the tenant (desc.go:21) | API | no: a generated-only type outside semver (GeneratedCodeIsVersionN); velox-gen always writes it | S | Store tenant+1 (0 = none), or use a `HasTenant` bool, with the next generated-code version bump. |
| R10 | schema review #13 | No guidance that third-party annotations should use module-path names to avoid colliding (a non-Merger of the same name replaces silently, schema/load/marshal.go:211) | DOCS | no | S | Add a doc sentence on `schema.Annotation` (schema/schema.go:5). |

## Groups

1. **Plugin path check**: G3, G31 (and G4's hint). These all touch cmd/velox-gen/plugin.go `checkPluginPath`/`runPlugin`.
2. **velox-gen error UX**: G29, G30, G4, R4.
3. **GraphQL SDL polish**: G9, G6, G1, R7.
4. **Application enums via @goModel**: G11, G12, G13.
5. **Mutation data safety (decide before 1.0)**: G47, G48, G44, G52, G45.
6. **Mutation codegen cleanup**: G49, G50, G51.
7. **pgx split**: G65, G38, G26.
8. **With[T] compile cost**: G18. This is the same as the followups' "1.0 release" last bullet.
9. **Generated-code shape before freeze**: R1, R2, R3, R9.
10. **Module path decision**: R8.
11. **Test harness hygiene**: G21, G22, G46, G8, G5, G12, G32, G34, G61.
12. **otel additions (post-1.0)**: G53, G57, G59, G63.
13. **Release**: G20, G58.
14. **GraphQL and core features (post-1.0)**: G14, G15, G17, G24, G36, G37, G40, G41, G43, G55, G68, G70, R5, R6, R10.

## Counts

- G (76): STALE 27, FEATURE 20, TEST 11, DEFECT 9, API 4, PERF 2, RELEASE 2, DOCS 1. Before 1.0: 12 (G3, G9, G18, G20, G31, G38, G44, G45, G47, G48, G52, G65).
- R (10): API 4, FEATURE 4, DOCS 2. Before 1.0: 2 (R1, R8).
