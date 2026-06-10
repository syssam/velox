# Roadmap

Velox is pre-1.0 (`v0.1.0`). This document is the answer to two questions the
README's [Feature Stability](README.md#feature-stability) table raises but
does not answer: **what must be true before v1.0.0**, and **what does it take
for an individual feature to move up a stage**.

Dates are deliberately absent — velox is a personal project and stages are
promoted by evidence, not by calendar.

## Stage promotion criteria

A feature moves up one stage when all criteria for the target stage hold:

| Target stage | Criteria |
|---|---|
| **Alpha** | Generated output compiles in the integration prototype (`tests/integration/`); at least one e2e test exercises the happy path |
| **Beta** | API has survived one real downstream consumer without a breaking change; e2e coverage includes the failure/edge cases, not just the happy path; documented in README or `docs/` |
| **Stable** | Exercised across the dialect matrix (SQLite + Postgres + MySQL) where dialect-relevant; covered by the parity harness or an equivalent behavioral guard; no open correctness issues |

Demotion is allowed: a correctness bug found in a Beta feature drops it to
Alpha until the fix lands with a pinning test.

## Path to v1.0.0

Ordered roughly by how much they de-risk the release, not by effort.

- [ ] **Promote `privacy` and `intercept` to Beta.** They are enabled
  together in the integration prototype and covered by e2e tests; what's
  missing is a second real downstream consumer and a freeze on the
  `FilterFunc`/`Filterable` surface.
- [ ] **Promote `sql/upsert` to Beta.** The dialect-divergent paths
  (`ON CONFLICT` vs `ON DUPLICATE KEY`) are pinned by
  `TestMultiDialect_BulkOnConflictUpdate`; remaining work is documenting the
  `DoNothing` unreliable-IDs caveat in the feature docs (currently only in
  the generated docstring and `docs/architecture-overview.md` §4.7).
- [ ] **API freeze for the generated surface.** The generated query/mutation
  builder API follows Ent and is effectively frozen already; the runtime
  packages (`runtime/`, `dialect/sql/`) need an explicit "these symbols are
  API" pass. `apidiff` already gates changes in CI; the freeze turns warnings
  into failures.
- [ ] **Versioned-migration story out of Experimental.** Atlas integration
  works in `examples/versioned-migration`; needs multi-dialect e2e and a
  documented downgrade path before users can trust it for production schema
  evolution.
- [ ] **GraphQL extension promoted as a unit.** `contrib/graphql` is the main
  reason velox exists; its sub-features (connections, WhereInput, mutations,
  multi-order pagination, unions, Skip modes) are individually pinned, but
  the extension should carry a single stage marker so users don't have to
  reason per-annotation.
- [ ] **Documented limitations are part of the contract.** Known, deliberate
  divergences and inherited Ent limitations (no `(*Entity).Update()`,
  `DoNothing` IDs, NULL-cursor pagination dead-end) are documented in
  `docs/architecture-overview.md` §4 and pinned by tests. Any new limitation
  discovered before v1.0 gets the same treatment — a pin test plus a gotcha
  entry — or a fix.
- [ ] **One release-candidate cycle.** Tag `v1.0.0-rc.1`, hold the API for a
  cooling period while downstream consumers upgrade, then tag `v1.0.0` with
  the COMPATIBILITY.md guarantees in force.

## Non-goals (deliberate)

These are decided, not pending — see `docs/architecture-overview.md` §4 and
`CLAUDE.md` for the rationale:

- `(*Entity).Update()` / `(*Entity).Delete()` methods (ActiveRecord pattern;
  spreads the tx-driver footgun).
- Flat single-package generated layout (defeats the incremental-rebuild
  thesis).
- Auto-chunking of bulk inserts over the SQLite parameter limit (surface the
  error; the caller owns the chunking policy).
- Union SDL / resolver scaffolding for `graphql.UnionMember` (Go markers
  only, matching every other gqlgen-bound ORM).
- NULL-aware cursor pagination beyond Ent parity (requires dialect-aware
  `NULLS FIRST/LAST` emission; revisit only if Ent parity stops being the
  compatibility bar or ≥3 real users hit it).

## Already shipped

Kept here so the checklist above reads against the right baseline: the
model-elimination 2-layer architecture, Ent-style shared hook/interceptor
stores, explicit privacy policy fields, entity `Unwrap()` tx-detach parity,
edge-method `where` autobind (zero resolver code), multi-order Relay
pagination with e2e guards, the three-way parity differential harness
(reference ⟷ velox ⟷ ent) across the dialect matrix, per-tier CI coverage
gates, and the 10–25× incremental-rebuild advantage measured in
`docs/benchmarks.md`.
