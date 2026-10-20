# Core: JSON Fields — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A schema field declared with `field.JSON`, `field.Strings`, `field.Ints` or `field.Any` is stored as JSON and read back on every dialect. Today velox binds the Go value raw (a `[]string` reaches the driver as-is) and scans the column straight into the field, so these fields fail on PostgreSQL, MySQL and SQLite.

**Architecture:**
- Each column's metadata says whether it holds JSON. velox-gen sets this for `json` fields.
- **Writes:** wherever velox binds a value for a column, a JSON column's value is first encoded with `encoding/json`. That covers insert rows, update assignments, `Save`, upsert updates and predicate values.
- **Reads:** wherever velox scans into a column, a JSON column is scanned through a small decoder. That covers `All`, `One`, `Iter`, loads and `RETURNING`.
- Non-JSON columns are untouched, so every allocation budget stays the same.

**Tech Stack:** Go 1.27, the core module only; no new dependency.

**Spec:** the schema design `docs/specs/2026-10-15-schema-design.md` (field types, §1) and the core design `docs/specs/2026-10-14-core-design.md` (§3 rules, §8 budgets). This plan fixes a defect found during GraphQL plan 2, recorded in `docs/plans/2026-10-14-core-1-followups.md`.

## Global Constraints

- **Core rules.**
  - Go 1.27 generic methods.
  - Nothing runs at program start.
  - No reflect in generic code: JSON encoding and decoding live in non-generic helpers.
  - The allocation budgets (`go test -run Budget -v .`) are unchanged: one-row 6, tenant 7, hundred rows 105, tx 9/6, observed 7/6, metadata 19, Insert 13, replica one-row 6.
  - The escape check is unchanged. Run `go build -gcflags=-m . 2>&1 | grep "db.go:$(grep -n 'func (db \*DB) plain' db.go | cut -d: -f1):"`. It must print `leaking param content: st` and `read does not escape`.
- **Encoding.**
  - A JSON column's value is encoded with `json.Marshal` and bound as `[]byte` on PostgreSQL and MySQL, and as a string on SQLite so that the column holds TEXT, not a BLOB (ruling of 2026-10-20, Task 2).
  - A nil pointer field, or a nil slice or map in a nullable field, binds SQL NULL.
  - A nil slice or map in a non-nullable field binds the JSON `null`, as ent does. The value is documented and tested.
- **Decoding.**
  - SQL NULL leaves a pointer field nil and a non-pointer field at its zero value.
  - A value that is not valid JSON fails with an error that names the entity and the column.
  - Drivers hand JSON to a scanner as `[]byte` or `string`. Both are accepted.
- **Generated code.**
  - velox-gen writes the JSON flag into `ColumnDesc`.
  - `GeneratedCodeIsVersion3` stays, because the change is additive: a `Desc` without the flag still compiles.
  - The goldens are updated.
- **CI.**
  - The itests run on PostgreSQL (pgx and database/sql), MySQL and SQLite.
  - `scripts/ci-local.sh` and `.github/workflows/ci.yml` stay identical.
- **No machine-specific paths** in committed files. Set `GOCACHE=/private/tmp/claude-501/velox2-gocache` and `GOFLAGS=-p=2` only in the shell.
- **Commits** end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.

## Rulings this plan makes

1. **One flag, set by the generator.** `ColumnDesc.JSON bool` becomes `ColumnInfo.JSON bool`, set by velox-gen for every `json` field.
   - **Amended 2026-10-20 (controller ruling, during Task 1), and its flag renamed `Codec` (Task 1 review):** the core never imports encoding/json, so a program whose entities hold no JSON does not link it. `ColumnDesc` carries the codec instead of a flag: `Encode func(v any) ([]byte, error)` and `Decode func(data []byte, v any) error`, which velox-gen sets to `json.Marshal` and `json.Unmarshal` for every `json` field (the generated file imports encoding/json). `ColumnInfo.Codec` (first named `JSON`) is derived from `Encode != nil`, and the codec is kept unexported in `ColumnInfo`. Any codec sets the flag, so it does not mean "JSON": the migration planner takes a column's database type from the schema's field type, not from it. A `Desc` with one of the two is refused with a `*SchemaError` naming the column. A hand-written `Desc` may plug in another codec. `ColumnDesc.JSON bool` is dropped.
   - The core never guesses JSON from the Go type, because a `[]byte` field is bytes, not JSON.
2. **Encoding is a binding concern.** It happens where arguments are built for a column.
   - It does not happen in the entity, the handles or the hooks.
   - Hooks and policies see Go values. Only `Statement.Args` holds the encoded bytes. Rewrite hooks run before rendering, so they see Go values too.
3. **Comparison predicates on a JSON column are refused at statement time.** These are `EQ`, `NEQ`, `In`, `NotIn`, `GT`, `GTE`, `LT`, `LTE`, `Contains`, `HasPrefix` and `HasSuffix`.
   - The error names the column: JSON equality differs by dialect (jsonb normalises, TEXT does not).
   - `IsNull`/`IsNotNull` work.
   - JSON path predicates are a follow-up.

## Review Focus

1. **A JSON field under an upsert's update branch, and under `Save`.** Both encode. Task 1's `TestJSONWrites` covers every write path.
2. **A JSON field loaded through `With`, read back by `RETURNING`, or streamed by `Iter`.** All of these decode. Task 1's `TestJSONReads` covers every read path.
3. **The per-row cost of a JSON column.** At most one allocation per JSON column per row, beyond what `json.Unmarshal` itself makes. A non-JSON entity pays nothing. Task 1's `TestJSONAllocations` and the unchanged budgets pin this.
4. **Real drivers.** pgx binding `[]byte` to `jsonb`, MySQL's `JSON`, SQLite `TEXT`, and scanning each back. Task 2's itests pin this.
5. **NULL versus JSON `null`.** A nullable pointer field that is nil stores SQL NULL. A non-nullable nil slice stores the JSON `null` and reads back as a nil slice. Task 2 pins this on every dialect.

---

### Task 1: The JSON flag, encoding and decoding

**Files:**
- Modify:
  - `desc.go`: `ColumnDesc.Encode` and `Decode`, kept in `ColumnInfo` (flag `Codec`);
  - `meta.go`: the `ColumnInfo` field and its doc;
  - the argument building, wherever a statement's values become `Args` (render, insert rows, assignments, upserts, predicates);
  - the scanning (`scanRow`, the kit's scan, the `RETURNING` scan in `insert.go`).
  - For the generator: `internal/gen/lower.go` and `render.go` set `JSON: true` in the generated `ColumnDesc`.
- Create: `json.go`, with the non-generic encode and decode helpers and the scan wrapper.
- Test:
  - `json_test.go` (FakeExec);
  - `internal/gen` goldens.

**Interfaces:**
- Produces: `ColumnDesc.Encode` and `Decode`, and `ColumnInfo.Codec bool`. Everything else is unexported.

- [ ] **Step 1: Write the failing tests.**
  - `TestJSONWrites` uses a `Desc` entity with `[]string`, `map[string]any`, a struct and a `*[]int` (nullable) JSON column. Through FakeExec it checks the bound args of:
    - `Insert`, `InsertAll`, `Update().Set`, `Save`, and an upsert's update branch;
    - each arg must be the JSON bytes;
    - nil pointer gives `nil` (NULL), and a nil non-nullable slice gives `[]byte("null")`.
  - `TestJSONReads`: FakeExec returns `[]byte` and `string` JSON. It checks that `All`, `One`, `Iter`, a `With` load and an Insert `RETURNING` decode into the fields. NULL leaves the pointer nil. Invalid JSON gives an error naming the entity and column.
  - `TestJSONPredicatesRefused`: `EQ` on a JSON column fails at statement time naming the column. `IsNull` works.
  - `TestJSONAllocations`: a one-row read of the JSON entity makes at most `budget + 1 + json's own` allocations per JSON column. Measure it and pin the number.
  - `internal/gen`: golden update; a `field.Strings` field renders `JSON: true`.
- [ ] **Step 2: Run** `go test -run 'TestJSON' .`. Expected: FAIL.
- [ ] **Step 3: Implement** (Rulings 1–3).
- [ ] **Step 4: Run.** Run `go test -run Budget -v .` (every number unchanged) and the escape check. Then run `go test -race -count=1 ./...`, `go test ./internal/gen/`, vet, gofmt, lint and the example's `velox-gen -check` for every example (regenerated output committed if it changed). Expected: PASS.
- [ ] **Step 5: Commit** `JSON fields are encoded on write and decoded on read`.

### Task 2: Real databases, GraphQL and the documents

**Files:**
- Create: `internal/itest/json_test.go`.
- Modify:
  - `contrib/graphql`'s e2e model, to add `Owner.tags` back (it was dropped because of this defect), and the JSON scalar getter test;
  - `docs/specs/2026-10-15-schema-design.md`, with a dated note that JSON fields are stored as JSON per dialect (PostgreSQL `jsonb`, MySQL `JSON`, SQLite `TEXT`), Rulings 1–3;
  - `docs/plans/2026-10-14-core-1-followups.md`, to tick the JSON item and add "JSON path predicates".

- [ ] **Step 1: Write the tests.** On each of PostgreSQL via pgx, PostgreSQL via database/sql, MySQL and SQLite, the table has `jsonb` on PostgreSQL, `JSON` on MySQL and `TEXT` on SQLite. Insert, read, update, upsert and `Save` each of `[]string`, `map[string]any`, a struct, a nullable pointer and NULL. Read the raw column through `database/sql` to prove it holds JSON text. Also test the JSON `null` versus SQL NULL distinction (Review Focus 5).
- [ ] **Step 2: Run** the full gate `<scratchpad>/fix1.sh`, with Docker. Expected: every step rc=0, and the new itests RUN on every driver.
- [ ] **Step 3: Restore `Owner.tags`** in contrib/graphql's e2e and run `go -C contrib/graphql test -race ./...`. Expected: PASS.
- [ ] **Step 4: Edit the documents.**
- [ ] **Step 5: Commit** `JSON fields on every dialect, and in GraphQL`.
