# veloxe gaps, phase B: velox-graphql for graphql-go — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** velox-graphql serves github.com/syssam/graphql-go (bound by its generator gqlc) as well as gqlgen, with a runtime core that imports neither, so nextapp's port can use velox-graphql's output.

**Architecture:** Split `veloxgql` into an engine-free core plus `gqlgenx` and `graphqlgo` adapters behind an internal selection source that fails closed; add `engine=graphql-go` SDL output for gqlc autobind and `split=entity` files.

**Tech Stack:** Go 1.27; contrib/graphql module (velox-graphql plugin, veloxgql runtime); gqlgen; github.com/syssam/graphql-go + gqlc.

**Spec:** `docs/history/superpowers/specs/2026-11-22-graphql-go-backend-design.md` — §7 lists the tasks; each task's details are in the spec sections it cites. The spec is binding.

## Decisions (binding; controller rulings on the spec's §8)

- Q1: syssam/graphql-go only; no graph-gophers adapter.
- Q2: until gqlc honours `@goField(name:)` (spec GA1, the user's repository), `engine=graphql-go` refuses getter fields and `graphql.FieldName`-renamed edges with a clear velox-gen error naming the field and the reason. No stub-bound output.
- Q3: one module; adapters are packages. Deps tests enforce import rules.
- Q4: graphql-go becomes the default engine in Task 8 (the user chose "both, graphql-go default" on 2026-11-03). Tasks 1–7 keep gqlgen the default so the byte-identity gates hold.
- Q5: transaction per mutation is opt-in (`WithMutationTx`).
- Q6: graphql-go's own limits; velox `Cost` opt-in; budget.go not ported.
- Q7: root query fields stay in velox.graphql (veloxe layout).
- Q8: `SelectionSource` stays internal.
- Q9: graphqlgo does not consult `Withheld`.
- ADR numbers 0103–0108 as built: 0108 is Task 3b's. The parked Tasks 5 and 8 take the next free numbers.

## Global Constraints

- gqlgen output stays byte-identical through Tasks 1–7 (examples/graphql goldens and generated.go unchanged).
- The core (`veloxgql`, `gqlgraph`, `internal/gqlcore`) imports neither gqlgen, graphql-go nor net/http; deps tests prove it.
- Missing selection source → `ErrNoSelectionSource` on every gqlgraph read; never a log-and-fallback.
- A change to generated code's shape bumps `GeneratedCodeIsVersionN` with a CHANGELOG line (Task 4: Version28).
- API snapshot updated and read; coverage floors for new packages; compile cost not raised without measurement.
- Subagents never run scripts/ci-local.sh or docker; commit style `area: what`, Co-Authored-By line; never push.
- NEVER run Go commands under ~/Jobs/project/nextapp.

## Review Focus

1. A gqlgraph read under graphqlgo with no source installed: errors, no rows.
2. A WithFilter-hidden FK target: `xIDIsNil`, `xID`, `xIDIn` agree with `hasX`.
3. graphql-go's concurrent field execution with velox's Batch: no data race, same statement count as gqlgen.
4. A refused getter field under engine=graphql-go: velox-gen fails with the field named, writes nothing.
5. split=entity: the union of the per-entity files equals the single-file SDL's definitions.

---

### Task 1: FK predicates see run-time filters (spec §7.1, ADR 0103)
- [ ] failing tests; [ ] implement; [ ] verify; [ ] commit

### Task 2: Core split, gqlgenx, selection seam (spec §1, §2.1–2.2, §2.4; ADRs 0104, 0105)
- [ ] failing tests (deps, fail-closed); [ ] implement; [ ] verify gqlgen byte-identity + statement/alloc parity; [ ] commit

### Task 3: graphqlgo adapter (spec §2.3, §3.6; ADR 0106)
- [ ] parity suite; [ ] implement; [ ] verify -race; [ ] commit

### Task 3b: veloxe compat parity for clients (parity report §5 items 1, 2, 3, 5; ADR 0108)
Source: `docs/history/superpowers/specs/2026-11-22-veloxe-behaviour-parity.md` §1, §2, §5. All changes apply only when `gqlgraph.Type.Veloxe` (compat=veloxe) is set; default output and behaviour unchanged.
- Item 1: `veloxgql.Cursor` decoding also accepts veloxe's cursor (base64 RawStd msgpack `{i, v, vs}`, hand decoder for fixmap/ints/strings/nil/ext 1, no msgpack dependency), read as Key=i and v/vs as the current order's values. Velox cursors are still emitted. No legacy-emit option (YAGNI). Seed test vectors from the probe output in §8.
- Item 2: single-term order: the key tie-break takes that term's direction.
- Item 3: FK predicates (`xID`, `xIDIn`, `xIDIsNil`, ...) are plain column predicates (no ScopedKey/EXISTS); amend ADRs 0027/0103.
- Item 5: forward page with `after` → hasPreviousPage true; backward with `before` → hasNextPage true.
- Item 4 is kept as velox2 behaviour (documented in Phase D); item 6 deferred.
- [ ] failing tests (one per item, compat on, plus default-unchanged); [ ] implement; [ ] verify; [ ] commit

### Task 4: engine=graphql-go output (spec §3.1, §3.2, §3.4; ADR 0107; Version28; Q2 refusal)
- [ ] goldens both engines; [ ] implement; [ ] verify; [ ] commit

### Task 5: split=entity (spec §3.3; ADR: the next free number)
- [ ] definitions-equal + identity tests; [ ] implement; [ ] verify; [ ] commit

### Task 6: examples/graphqlgo (spec §6.1; CI lane in ci-local.sh and ci.yml)
- [ ] e2e; [ ] implement; [ ] verify (TestCILocalMirrorsCI); [ ] commit

### Task 7: Measure and document (spec §5, §7.7)
- [ ] bench 500-graphqlgo thresholds; [ ] docs; [ ] commit

### Task 8: graphql-go default engine (ADR: the next free number)
- [ ] flip; examples/graphql passes engine=gqlgen; [ ] verify; [ ] commit
