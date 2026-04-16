# Generator/Runtime Protocol

This document enumerates the contract that generated entity packages (`compiler/gen/sql/`) must honor so the non-generated runtime (`runtime/`, `privacy/`, `dialect/`) works correctly. It is a specification, not a tutorial.

Every clause below is pinned by an AST-level test in `compiler/gen/sql/wiring_test.go`. If you change generator output, consult the pinning test first; if you change the protocol, update this document and the test in the same commit.

---

## 1. Hook and interceptor propagation

1.1. Client-level interceptors propagate via a shared `*entity.InterceptorStore` pointer — never a per-query `[]Interceptor` slice copy.

1.2. Every generated `*EntityQuery` struct holds an `*entity.InterceptorStore` field (not `[]Interceptor`, `[]runtime.Interceptor`, or `[]velox.Interceptor`).

1.3. No generated code performs an inline interface assertion of the form `.(interface{ SetInters(...) })` or calls `SetInters(c.Interceptors())`. Interceptors are wired at construction via `q.SetInterStore(c.interStore)`.

1.4. Hooks use a matching `*entity.HookStore` pointer with the same construction-time wiring.

1.5. `EntityClient.Query()` calls `NewXxxQuery(c.config)` then `q.SetInterStore(c.interStore)`. `EdgeQuery` reads interceptors dynamically via `runtime.EntityInterceptors()` using `InterceptorAccessor` / `PackageInterceptors` registered in `EntityRegistration`.

**Pinned by:** `TestNoSetIntersInterfaceAssertion`, `TestQueryHasInterceptorStorePointer`

---

## 2. Privacy evaluation

2.1. Privacy is an explicit step, not an interceptor. `prepareQuery` calls `q.policy.EvalQuery(ctx, q)` before `runtime.RunTraversers`.

2.2. Queries access interceptors via `q.inters.<Entity>` directly. No `effectiveInters()` wrapper, no `append(q.inters.<Entity>, packageInters...)` merging.

2.3. Entities with a policy expose `SetPolicy(p velox.Policy)`; entities without a policy must not emit `SetPolicy`, must not reference `EvalQuery`, and must not carry a `policy` field.

2.4. `Select.Scan` and `GroupBy.Scan` route through `runtime.ScanWithInterceptors`, pulling interceptors from `q.inters.<Entity>` directly. Same for privacy-bearing and non-privacy-bearing entities.

**Pinned by:** `TestPrivacyIsExplicitInPrepareQuery`, `TestPolicyExplicitEvaluation`, `TestQueryIntersUnifiedAccess`, `TestSelectScanUsesDirectInters`

---

## 3. Config propagation

3.1. Config-propagation call sites (`_node`, `_old`, `nodes[i]` in create/update) must route through `Type.SetConfigMethodName()` — never a hardcoded `SetConfig` literal.

3.2. When a schema declares a user field named `Config` or `config`, the generator emits `SetRuntimeConfig` to avoid clashing with the field setter. Hardcoded `SetConfig` on such schemas is a compile-break.

**Pinned by:** `TestConfigMethodNameHonoredInCreateAndUpdate`

---

## 4. Query construction and execution

4.1. Generated `*EntityQuery` exposes `QueryReader` getters (`GetDriver`, `GetTable`, `GetColumns`, `GetFKColumns`, `GetIDFieldType`, `GetPath`, `GetPredicates`, `GetOrder`, `GetModifiers`, `GetWithFKs`). No `queryBase()` method.

4.2. `buildQuery` delegates to `runtime.BuildQueryFrom`; `buildSelector` delegates to `runtime.BuildSelectorFrom`.

4.3. `prepareQuery` delegates to `runtime.RunTraversers` — no inline traverser loops.

4.4. `Where()` accepts the named `predicate.<Entity>` type, not a raw `func(*sql.Selector)`.

4.5. `And` / `Or` / `Not` are assignments to `sql.PredicateAnd[P]` / `PredicateOr[P]` / `PredicateNot[P]` generics — no per-entity function bodies.

**Pinned by:** `TestQueryImplementsQueryReader`, `TestWhereUsesNamedPredicateType`

---

## 5. Aggregate scan routing

5.1. `UserSelect.sqlScan` calls `runtime.QuerySelect(ctx, base, s.Fns(), v)` — not `QueryScan` — so aggregate functions registered via `Aggregate(...)` become the SELECT list.

5.2. `UserQuery.Scan` calls `runtime.QueryScan` (no aggregate context).

Do not swap `QuerySelect` for `QueryScan` during generator refactors; the aggregate path depends on the former.

---

## 6. Mutation state

6.1. Mutation state lives inline on the generated `*EntityMutation` struct. There is no `runtime.MutationBase` embed.

6.2. The struct carries: `op`, typed `id *IDType`, typed field pointers (`_name *string`), typed edge maps, `oldValue func(ctx)(*entity.T, error)` closure, and (for JSON-bearing entities) a local `appends` map.

6.3. `Op()`, `Type()`, `ID()`, `SetID()`, `SetOp()` are emitted as methods on the concrete type.

6.4. `OldXxx()` reads typed fields directly off the loaded entity via the `oldValue` closure. No `runtime.ConvertOldValue[T]` shim, no `map[string]any` intermediate.

6.5. `UpdateOne.sqlSave` honors `selectFields` for both SET clause and post-update re-query (`len(_u.selectFields)`, `slices.Contains(_u.selectFields, ...)`, `columns = append([]string{user.FieldID}, _u.selectFields...)`). Bulk `UserUpdate.sqlSave` does **not** reference `selectFields`.

**Pinned by:** `TestUpdateOneSelectFieldsRestriction`

---

## 7. Generator layout invariants

7.1. Exactly one production generator per output file. Legacy parallel generators (`entity.go`, `query.go`, `query_execute.go`, `query_select.go`, `entity_edges.go`, `entity_crud.go`, `entity_traversal.go`) were removed 2026-04-13. Do not recreate them; parallel paths drift silently.

7.2. Per-entity generators: `entity_client.go`, `entity_pkg.go`, `entity_helpers.go`, `entity_scan.go`, `entity_hooks.go`, `mutation.go`, `predicate.go`, `query_pkg.go`, `create.go`, `update.go`, `delete.go`, `meta.go`.

7.3. Graph-level generators: `client.go`, `client_options.go`, `velox.go`, `tx.go`, `migrate.go`.

7.4. Shared helpers live in `helper.go` (e.g. `assertSetInterStore`, `assertSetPath`, `edgeSpecBase`, `genFieldSetter`, `genEdgeSetter`).

---

## Changing the protocol

1. Write or amend the wiring test in `compiler/gen/sql/wiring_test.go`. The test must fail before the generator change and pass after.
2. Update this document in the same commit. Drift between `PROTOCOL.md` and `wiring_test.go` is itself a defect.
3. Mention the pinning test name in the commit message so `git log --grep=TestXxx` finds the provenance.
