# Generated scope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `schema.Scoped("org")` on a column makes velox-gen write the scope key and wiring, and bind the scope around the entity's own Policy and Hooks, so the five hand-written `scope.go` files go away.

**Architecture:** A field annotation reaches `codegen.Lower` as raw JSON (like `Comment`). Lower resolves it into `OutEntity.Scope` and `Out.ScopeKeys` and refuses bad uses. `Render` writes a new `scope_velox.go` (keys and per-entity `<entity>Scope` vars) and, for a scoped entity, changes the descriptor's `Policy` and `Hooks` binding to wrap the user's methods in the scope. The `scope` package and the engine do not change.

**Tech Stack:** Go 1.27, `internal/codegen` (Lower, Render, golden files), `cmd/velox-gen`, the `scope` package, the `examples` module.

**Spec:** `docs/superpowers/specs/2026-11-02-generated-scope-design.md`

## Global Constraints

- The engine, `Descriptor` and the `scope` package's API do not change (spec: Goals, Anti-goals). No "tenant" concept returns (ADR 0029).
- The annotation is `schema.Scoped(key string)`, a field annotation in package `schema` beside `Table` and `Check`; its `Name()` is `"Scoped"`.
- The key variable is `<GoName(key)>Key` always (`OrgKey`), never a bare `Org`. The per-entity variable is `<lowerCamel(Name)>Scope` (`noteScope`). Both are unexported-or-exported exactly so, and both are in the generated file `scope_velox.go`, which exists only when some entity is scoped.
- A scoped column is refused unless `Immutable`; refused if `Optional`, `Nillable`/pointer; at most one scoped column per entity; one key must have one Go type; a user file declaring `<entity>Scope` or `<Key>Key` is a `*FileError`.
- The scope is outermost: `Fill()` is the first hook, `Wrap` encloses the user's Policy; a user's `Policy` never calls `Wrap`.
- `velox-gen -check` output is deterministic: keys and entities sorted.
- `plugin.ModelVersion` is unchanged.
- Do not commit: the working tree holds the owner's uncommitted tenant-removal work, and the owner commits. Each task ends with a verification, not a commit. Run `scripts/ci-local.sh` (the repo's CI mirror) before any push.

## Review Focus

- The scoped column is also an edge's foreign-key field (`edge.To(...).Field("org_id")`): allowed; the scope still applies. Pinned in Task 2.
- An entity with a scope and only one of `Policy` or `Hooks`, or neither: each of the four combinations binds the right thing. Pinned in Task 3.
- Removing `Scoped` from the last scoped entity removes `scope_velox.go` and the wrapping (no stale file, no dangling `noteScope`). Pinned in Task 4.
- A non-`int64` key (a `string` column, and a column of a Go type with an import such as a UUID): the key is `scope.NewKey[string]`, with the import in `scope_velox.go`. Pinned in Tasks 2 and 3.
- A json, enum or other column whose handle has no `EQ(V)`, and a join entity: refused with a message, not a compile error in `gen/`. Pinned in Task 2.

---

### Task 1: The `Scoped` annotation

**Files:**
- Modify: `schema/annotations.go`
- Test: `schema/schema_test.go`

**Interfaces:**
- Produces: `type ScopedAnnotation struct{ Key string \`json:"key"\` }`; `func (*ScopedAnnotation) Name() string` returning `"Scoped"`; `func Scoped(key string) *ScopedAnnotation`. Its JSON, in a graph, is `{"key":"org"}` under the annotation name `Scoped`.

- [ ] **Step 1: Write the failing test** `TestScopedAnnotation` in `schema/schema_test.go`: `Scoped("org")` has `Name() == "Scoped"` and `Key == "org"`, and attached with `field.Int64("org_id").Annotations(schema.Scoped("org"))` it appears in the field's descriptor annotations (use the same helper the existing `Table`/`Check` tests use to read them).
- [ ] **Step 2: Run** `go test ./schema -run TestScopedAnnotation`. Expected: FAIL, `Scoped` undefined.
- [ ] **Step 3: Implement** the type and constructor in `schema/annotations.go` with a doc comment that states what it does (marks the column an entity is confined by; velox-gen writes the key and wiring; the column must be Immutable) and shows the one-line example. No `Merge` method: the same field cannot carry two.
- [ ] **Step 4: Run** `go test ./schema`. Expected: PASS.

### Task 2: Lower resolves and checks the scope

**Files:**
- Modify: `internal/codegen/out.go`, `internal/codegen/lower.go`
- Test: `internal/codegen/lower_test.go`, `internal/codegen/build_refusals_test.go`

**Interfaces:**
- Consumes: `Field.Annotations["Scoped"]` raw JSON on `codegen.Field` (via `loadjson.Field`), `OutField.Immutable`, `OutField.Pointer`, `OutField.Handle`, `OutField.GoType`.
- Produces (in `out.go`):
  - `type OutScope struct { Key, KeyVar, Var, Field, Handle string }` — `Key` the annotation's text ("org"), `KeyVar` "OrgKey", `Var` "noteScope", `Field` the column's Go name ("OrgID"), `Handle` the equality func expression ("Notes.OrgID.EQ").
  - `type OutScopeKey struct { Name, Var, GoType string; Imports []string }` — the key's name, "OrgKey", the value's Go type ("int64"), and import paths that type needs.
  - new fields `OutEntity.Scope *OutScope` (nil when not scoped) and `Out.ScopeKeys []*OutScopeKey` sorted by `Var`.

- [ ] **Step 1: Write failing tests** in `lower_test.go`, named `TestLowerScope*`, each building a graph the way the neighbouring tests do and asserting on `Lower`'s result or error text:
  - `TestLowerScopeResolves`: an `int64` Immutable `org_id` with `Scoped("org")` on entity `Note` gives `Scope == &OutScope{Key: "org", KeyVar: "OrgKey", Var: "noteScope", Field: "OrgID", Handle: "Notes.OrgID.EQ"}` and `ScopeKeys == [{Name:"org", Var:"OrgKey", GoType:"int64"}]`; two entities using key "org" give one key.
  - `TestLowerScopeStringAndImported`: a `string` column gives GoType "string"; a column of a Go type from another package (use the repo's existing test type with a `PkgPath`) puts that path in `Imports`.
  - `TestLowerScopeForeignKeyField`: the scoped column is the declared foreign-key field of an edge: accepted.
  - `TestLowerScopeRefusals` (table, in `build_refusals_test.go` style): not Immutable; Optional; Nillable; two scoped columns on one entity; one key on an `int64` and a `string` column; a json column; an enum column; a join entity (many-to-many through table) carrying it. Each asserts the error names the entity and field, and states the rule ("must be Immutable", and so on).
- [ ] **Step 2: Run** `go test ./internal/codegen -run 'TestLowerScope'`. Expected: FAIL (types undefined).
- [ ] **Step 3: Implement** the types in `out.go` and, in `lower.go`'s `entities()` next to the `Comment` annotation handling, `func (s *entState) scoped(e *Entity)` that reads the annotation from each field, applies the Global Constraints' refusals via `s.fail(where, ...)` (so errors carry the field's `Pos`), fills `OutEntity.Scope`, and `lowerer` collects `ScopeKeys`, checking type agreement across entities and sorting at the end. KeyVar is `GoName(key) + "Key"` using the package's existing GoName function.
- [ ] **Step 4: Run** `go test ./internal/codegen`. Expected: PASS, golden tests unchanged.

### Task 3: Render `scope_velox.go` and the wrapped binding

**Files:**
- Modify: `internal/codegen/render.go`
- Create: `internal/codegen/testdata/golden/scoped/` (`plugin.json` graph and `want/`), and a case in `goldenCases` in `render_test.go`
- Test: `internal/codegen/render_test.go`

**Interfaces:**
- Consumes: `OutEntity.Scope`, `Out.ScopeKeys`, `OutEntity.Bound` (Task 2 and `out.go`).
- Produces: `func renderScope(o *Out) string` (the body of `scope_velox.go`); `Render` appends `File{Name: "scope_velox.go"}` last, after `velox.go`, only when `len(o.ScopeKeys) > 0`; update `Render`'s doc comment to say so.

- [ ] **Step 1: Write the golden case** `scoped`: a graph of four entities covering every combination of (Policy bound?, Hooks bound?) over one key "org", plus one `string`-keyed entity under key "region". Generate the expected files once with the repo's `-update` flag only after Step 3, then review them by hand against the shapes below.
- [ ] **Step 2: Write failing tests** in `render_test.go`: `TestRenderScopeFile` asserts the file for the golden case contains `var OrgKey = scope.NewKey[int64]("org")`, `var RegionKey = scope.NewKey[string]("region")`, and `var noteScope = scope.By(OrgKey, Notes.OrgID.EQ, func(x *Note) *int64 { return &x.OrgID })`, with the `scope` import, header and `GeneratedCodeIsVersion` guard of the other files. `TestRenderScopedBinding` asserts the four descriptor shapes by substring:
  - Policy bound: `velox.BindPolicy(noteScope.Wrap((*Note)(nil).Policy()))`; not bound: `velox.BindPolicy(noteScope.Policy())`.
  - Hooks bound: `velox.BindHooks(append([]velox.Hook[Note]{noteScope.Fill()}, (*Note)(nil).Hooks()...))`; not bound: `velox.BindHooks([]velox.Hook[Note]{noteScope.Fill()})`.
  - An unscoped entity's lines are byte-identical to today's. `TestRenderNoScopeFile`: a graph with no scoped entity yields no `scope_velox.go`.
- [ ] **Step 3: Run** `go test ./internal/codegen -run 'TestRenderScope|TestGolden/scoped'`. Expected: FAIL.
- [ ] **Step 4: Implement** `renderScope` and the change in `entityDescriptor` (`render.go:307-312`): when `e.Scope != nil` the `Policy` and `Hooks` lines are always written, in the shapes above; otherwise unchanged. Keys are written in `ScopeKeys` order, entity vars in `o.Entities` order.
- [ ] **Step 5: Run** `go test ./internal/codegen -run TestGolden -update` for the `scoped` case only, read `want/` against the shapes above, then `go test ./internal/codegen`. Expected: PASS; other golden cases byte-unchanged (`git diff --stat internal/codegen/testdata/golden/blog internal/codegen/testdata/golden/minimal` is empty).

### Task 4: `velox-gen` end to end

**Files:**
- Modify: `internal/codegen/methods.go` (name-clash check), `cmd/velox-gen/generate.go` only if the stale-file path needs it
- Test: `internal/codegen/methods_test.go`, `cmd/velox-gen/generate_test.go`

**Interfaces:**
- Consumes: `codegen.Methods(dir, entities)`, `codegen.Generate`, and the test helper `generate_test.go` already uses to run `runGenerate` on a temp module.
- Produces: `codegen.Methods` returns a `*FileError` for a user file that declares a package-level identifier equal to a scope's `Var` or `KeyVar`; to know them it takes the names as an extra parameter: `Methods(dir string, entities, reserved []string)`; the single caller in `generate.go` passes `gm.scopeNames()` (a new method on `genModel` returning the sorted `Var`s and `KeyVar`s from `gm.out`). Update the other callers and tests.

- [ ] **Step 1: Write failing tests:** `TestMethodsReservedName` (a user file with `var OrgKey = 1`, and another with `var noteScope int`, each give a `*FileError` whose Reason names the identifier); in `generate_test.go`: `TestGenerateScoped` (a temp schema with a scoped `Note` and a user file `policy.go` defining `Note.Policy` with a `Read` rule and no `Wrap`: generation writes `scope_velox.go`, the module compiles, and a test inside it shows the scope still applies when the context has an org); `TestGenerateScopedRemoved` (generate, then drop `Scoped` from the schema and generate again: `scope_velox.go` is gone and the module still compiles).
- [ ] **Step 2: Run** `go test ./cmd/velox-gen ./internal/codegen -run 'Scoped|ReservedName'`. Expected: FAIL.
- [ ] **Step 3: Implement** the `Methods` parameter and the `genModel.scopeNames` method; confirm by the second test whether the existing stale-file removal already deletes `scope_velox.go` (velox-gen removes its own headered files that are no longer wanted); change `generate.go` only if it does not.
- [ ] **Step 4: Run** `go test ./cmd/velox-gen ./internal/codegen`. Expected: PASS.

### Task 5: Migrate the examples and `internal/testmodel`

**Files:**
- Modify: each of `examples/{graphql,routing,tenant,tour,tracker}/schema/*.go` (add `.Annotations(schema.Scoped("org"))` to each scoped `org_id`), `examples/*/main.go`, `*_test.go` and comments that name `gen.Org` or `Org.With` (now `gen.OrgKey` / `OrgKey.With`), `examples/tracker/gen/policy.go` (drop every `projectScope.Wrap(...)`: return the plain `velox.Policy`), `examples/*/README.md`, `internal/testmodel/schema/ticket.go`, `internal/testmodel/ticket.go`, and its tests.
- Delete: `examples/{graphql,routing,tenant,tour,tracker}/gen/scope.go`, `internal/testmodel/scope.go`.
- Regenerate: every example's `gen/` and `internal/testmodel` with `go tool velox-gen`, each with its `-out` as in `scripts/ci-local.sh`.

**Interfaces:**
- Consumes: Tasks 1-4. The generated `OrgKey`, `<entity>Scope`; tracker's `userScope` etc. stay as names (they now come from `scope_velox.go`).
- Produces: `gen/` in routing, tenant and graphql with no hand-written file except what the example really owns; their package comment is the generated one again.

- [ ] **Step 1: Migrate one example first (routing)**, regenerate, and run `cd examples && go test ./routing`. Expected: PASS and `routing/gen` has no `scope.go`.
- [ ] **Step 2: Migrate the others,** one by one, running that example's tests after each. In `tracker`, `Hooks` methods that only returned the scope's `Fill()` are deleted; the `Policy` methods keep only the roles' rules. In `tour`, the `invoiceScope` and `lineScope` variables now come from `scope_velox.go`; keep any file that holds more than the scope. Where a `gen/` loses its only hand-written file, remove the package comment I added to `scope.go` (it was the reason `UserDoc` was true).
- [ ] **Step 3: Migrate `internal/testmodel`** (and `internal/testbind` if it has a hand-written scope), with `internal/testmodel/ticket.go`'s `Policy` reduced to its own rules.
- [ ] **Step 4: Run the example checks** from `scripts/ci-local.sh` in `examples/`: `go mod tidy -diff`, `gofmt -l .`, `go vet ./...`, `golangci-lint` (v2.14.0) and `revive` as the script runs them, `go tool velox-gen -check` and `verify` for every example (graphql with `-plugin velox-graphql`), `go -C graphql tool gqlgen generate` with no diff in `graphql/graph`, and `go test -race -count=1 ./...`. Expected: all clean. Then from the root: `go test ./...`.

### Task 6: Docs and release notes

**Files:**
- Create: `docs/decisions/0030-generated-scope.md`
- Modify: `docs/decisions/README.md` (index line), `CHANGELOG.md` (Unreleased), `scope/scope.go` (package doc: the schema annotation is the normal way; the hand-written form is for rules the annotation does not cover), `docs/superpowers/specs/2026-11-02-generated-scope-design.md` (status: implemented)

- [ ] **Step 1:** Write ADR 0030 in the format of 0029: context (hand-written `scope.go` and the silent-drop risk), decision (annotation, generated wiring, always-wrap), the generator-not-plugin reason, consequences (examples' `Org` is now `OrgKey`; no per-entity opt-out except `WithoutPolicy`).
- [ ] **Step 2:** Add the CHANGELOG "Unreleased" entry (Added: `schema.Scoped`; Changed: examples) and the ADR index line.
- [ ] **Step 3:** Update the `scope` package doc example to the annotation form and keep the hand-written form as the second example.
- [ ] **Step 4:** Run the whole repo: `scripts/ci-local.sh`. Expected: passes; report anything that was skipped or failed.

---

## Self-review notes

- Spec coverage: annotation (T1); keys and per-entity vars, checks (T2); generated file and always-wrap binding, determinism (T3); name clashes, stale file (T4); examples and call-site renames (T5); ADR, CHANGELOG (T6). The spec's open question (no per-entity opt-out) is recorded in the ADR (T6).
- Names used across tasks: `OutScope`, `OutScopeKey`, `Out.ScopeKeys`, `OutEntity.Scope`, `renderScope`, `genModel.scopeNames`, `Methods(dir, entities, reserved)` are defined once above.
- Left to the builder, as the spec says: where the `Handle` expression comes from for a handle kind with no `EQ` (Task 2 refuses json and enum; the builder checks the other handle kinds in `lower.go`), the exact wording of refusals, and whether a UUID column's import is carried by `OutField` or `OutEntity.Imports`.
