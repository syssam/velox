# Edge schemas (edge.Through) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** ent's `edge.Through`: a many-to-many relation's join table is an entity with its own ID and fields.

**Architecture:** The edge schema is an ordinary entity; velox-gen matches its two key edges, adds the through edge (O2M) to the owner, derives the unique index, and lowers the M2M onto it instead of a generated join entity. Runtime M2M relations already use the entity pipeline.

**Tech Stack:** Go 1.27; schema/edge, schema/load, internal/codegen, internal/engine, migrate, contrib/graphql.

**Spec:** `docs/history/superpowers/specs/2026-11-22-edge-through-design.md` (binding; §7 holds the rulings).

## Global Constraints

- ADR 0109 (edge schemas); 0110 reserved for composite keys.
- Output for schemas without `Through` is byte-identical apart from the version constant (`GeneratedCodeIsVersion` +1, CHANGELOG line; loadjson Version 8 → 9).
- Every refusal names the schema, the edge and the line to write (rules 63–70).
- Composite `field.ID` and bidirectional self edges via Through are refused in v1.
- Compile cost measured once (Task 3).
- API snapshot updated and read; coverage floors; godoc on every export; no spec citations in godoc.
- Subagents never run scripts/ci-local.sh or docker; commit `area: what` + Co-Authored-By; never push.

## Review Focus

Spec §6, items 1–5 (Add writing a zero; a link read past a filter; wrong column matched on self M2M; duplicate links or failing re-add; migration/naming regressions on conversion).

---

### Task 1: DSL and load model (spec §5.1, D1)
- [ ] failing tests; [ ] implement; [ ] verify; [ ] commit

### Task 2: Graph resolution and rules 63–69 (spec §5.2, D1, D2, D4)
- [ ] failing tests; [ ] implement; [ ] verify; [ ] commit

### Task 3: Lowering, runtime refusal, rule 70, version, compile cost (spec §5.3, D3, D5, D7)
- [ ] failing tests; [ ] implement; [ ] verify; [ ] commit

### Task 4: velox-graphql (spec §5.4, D6)
- [ ] goldens; [ ] implement; [ ] verify; [ ] commit

### Task 5: Docs and example (spec §5.5)
- [ ] example golden; [ ] docs; [ ] verify; [ ] commit
