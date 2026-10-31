# Migrate Plan 1: Model, Builder, Plan — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** From a schema package and a snapshot, velox computes the migration without a database:
- the generator caps derived names, checks one database namespace, and refuses what a database cannot hold (rules 44 and 49–52);
- `internal/gen.Tables` turns the `Graph` into a `migrate.Schema` for one dialect, the only builder of tables;
- `migrate.Diff`, `Lint`, `Render` and `NewPlan` turn two models into classified changes and SQL scripts, on PostgreSQL, MySQL and SQLite.

**Architecture:** A new internal package, `internal/ddl`, holds what the generator and the planner must agree on byte for byte: the 63-byte cap, the helper names, the type normaliser and literal escaping. `internal/gen` derives every database name in one file (`dbnames.go`), which `Build`'s rule 44 and `Tables` both call. The public package `migrate` holds the model (a value: nothing writes through a `*Schema` it is given), the snapshot JSON, `Diff` with derived-name renames and §7.3's order, `Lint`, and `Render`, which writes the main script, PostgreSQL's `_enum` and `_online` scripts with units, the enum swap, identity changes and SQLite's rebuild. Its tests build models from edited load models through `gen.Build` and `gen.Tables`, so every case runs on all three dialects; no test opens a database.

**Tech Stack:** Go 1.27. The root module's dependencies (pgx, go-sql-driver/mysql, modernc SQLite) are unchanged; `migrate` and `internal/ddl` use the standard library and the root package only.

**Spec:** `docs/history/specs/2026-10-23-migrate-design.md`: Intent, Goals, Anti-goals, Constraints, §1–§9 (§9 only for the directives a script carries), §14 (the model, Diff, Lint, Render and Plan parts), §15, §16, §17 (Generator, Render goldens, Model invariants, Fuzz), Plans item 1 and rulings 1–55. Follow-ups :131, :138, :271, :303 and :333 in `docs/history/plans/2026-10-14-core-1-followups.md`. Where this plan's rulings differ from the spec, the rulings stand (see "Rulings this plan makes").

## Global Constraints

- **Dependency discipline.** "The root module's dependencies stay pgx, the MySQL driver and SQLite. `migrate` adds none." `go.mod` does not change.
- **`migrate` imports** the standard library, `github.com/syssam/velox2` (for `velox.Dialect`) and `internal/ddl`, nothing else; never `internal/gen`. `TestImports` (Task 4) pins it.
- **The model is a value.** "`Diff`, `Lint`, `Render`, `Drift` and `Inspect` never write through a `*Schema` they are given; planning copies." `Tables` does not change the `Graph`.
- **Deterministic output.** "Every list in the model, the plan and the files is sorted by a stated key." The same inputs give the same bytes; every task that adds output has a test that runs it twice.
- **The core rules hold.** "Generated code does not change for an application that never migrates, beyond the generator fixes of §15. `GeneratedCodeIsVersion4` stays 4." The only generated change in this plan is the `// Model: sha256:` line of packages that use `mixin.Time`, `CreateTime` or `UpdateTime` (Task 2), and of any package with a derived index name over 63 bytes (Task 1; none in this repository).
- **Errors** print as `file:line: schema User: field "name": reason` (`gen.Error`); `migrate`'s own errors start with `migrate: `. Messages carry no rule numbers, as `Build`'s do today.
- **No database.** Plan 1 has no itest. Nothing in it reads `VELOX_TEST_*`.
- **Budgets and escape check unchanged.**
  - `go test -run Budget -v .` prints the same numbers before and after every task.
  - `go build -gcflags=-m . 2>&1 | grep "db.go:$(grep -n 'func (db \*DB) plain' db.go | cut -d: -f1):"` prints `leaking param content: st` and `read does not escape`.
  - `TestGenericTypesCompileSmall`: a row a task moves is re-measured, and its bound set to the new size plus the margin of about 1.2% the test documents. A task that moves a budget stops and reports it. Only Task 3 touches the core.
- **godoc.** `TestGodocCitesNoSpec` scans `migrate` (Task 4 adds it): no comment or message there cites a spec, a section or a ruling.
- **The full gate passes at every task's commit,** run on the committed tree, with Docker. If golangci-lint's lock is held, run it with `--allow-parallel-runners`.
- **CI.** `scripts/ci-local.sh` and `.github/workflows/ci.yml` stay identical in what they run; a task that changes one changes both (only Task 5 does: `FuzzLiteral`).
- **No machine-specific paths** in committed files.
- **Shell:** before any go command, export `GOCACHE` set to the shared cache the full gate uses, and `GOFLAGS=-p=2`. One heavy command at a time. Do not kill other processes.
- **Regenerating** (Task 2 only):
  - the generator goldens: `go test ./internal/gen -run TestGolden -update`;
  - the test model: `go build -o bin/velox-gen ./cmd/velox-gen && bin/velox-gen -out internal/testmodel -package testmodel ./internal/testmodel/schema`;
  - the tour: `(cd example && go tool velox-gen -out ./tour/gen ./tour/schema)`.
  - `bin/velox-gen -check` and `verify` on each, as `scripts/ci-local.sh` runs them, pass after.
- **Goldens of this plan** are rewritten with `-update`: `go test ./internal/gen -run TestTablesGolden -update`, `go test ./migrate -run 'TestLintGolden|TestRenderGolden' -update`. Read every rewritten file before committing.
- **Commits** are `Area: what changed`, end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`, stage files by name, and are never pushed.

## Review Focus

1. **An index on an enum column.** `field.Enum("status")` with `index.Fields("status")` gives the index `orders_status` and, on PostgreSQL, the enum type `orders_status`. PostgreSQL keeps indexes and types in different catalogues, so the schema must build and the plan must create both. Task 1 pins this in `TestNamespaceIndexOnEnumColumn`; Task 10's `create-tables` golden creates both.
2. **Names at the cap.** A 60-byte table name gives `_pkey`, `_id_seq`, `_not_null` and `velox_new_` names over 63 bytes; a 63-byte enum type gives a `_velox_old` helper over 63. Each must be capped as a whole, stay distinct from its base, and stay within 63 bytes; two long index names that differ only after byte 54 must stay distinct. Task 1 pins this in `TestCap`, `TestCapHelpers` and `TestIndexNamesCapped`.
3. **A literal with `'` and `\`.** A string default `it's a \ test`, an enum value `o'neil` and a comment with both, on each dialect: doubled `'` everywhere, doubled `\` on MySQL only, and the round trip exact. Task 5 pins this in `TestLiteral`, `TestTablesDefaults` and `FuzzLiteral`; Tasks 9 and 10 in the `literals` goldens.
4. **Adding a field to a populated table.** An `Optional` string, bool or number is safe, with the zero default; an `Optional` `Nillable` field is safe with no default; a required field with no default is `add-required-column`, blocked unless allowed; a required field with a `Default` is safe. Task 8 pins this in `TestLintAddColumn`.
5. **A table rename.** `users` → `accounts` by `-rename-table` must rename the primary key, the identity sequence, the derived indexes, foreign keys and checks, and a `<table>_<col>` enum type, and must not rename a `StorageKey`, `Symbol` or `EnumOf` name; on MySQL the foreign key is dropped and added, and on SQLite one rebuild follows. Task 7 pins this in `TestDiffRenameTableCarriesDerivedNames`; Tasks 9 and 10 in the `rename-table` goldens.

---

### Task 1: Names: the cap, one database namespace, rules 44 and 50–52

**Files:**
- Create: `internal/ddl/doc.go`, `internal/ddl/name.go`, `internal/ddl/types.go`, `internal/ddl/name_test.go`, `internal/ddl/types_test.go`.
- Create: `internal/gen/dbnames.go`, `internal/gen/dbnames_test.go`.
- Modify: `internal/gen/build.go` (`indexName` caps; `indexes` records the indexes it refuses instead of checking rule 44; a new step `names` after `indexes`; rule 51 in `validateEdges`), `internal/gen/build_test.go`, `plugin/plugin.go` (the doc of `Index.Name` says the name is capped).

**Interfaces:**
- Produces, in `internal/ddl` (package `ddl`):

```go
const MaxName = 63

// Cap returns name when it is MaxName bytes or less; otherwise its first 54
// bytes, cut back to a UTF-8 boundary, then "_", then the 8 lower-case hex
// digits of the FNV-1a 32 hash of the whole name.
func Cap(name string) string

func NotNull(table, column string) string // Cap(table + "_" + column + "_not_null")
func Rebuild(table string) string         // Cap("velox_new_" + table)
func OldEnum(typ string) string           // Cap(typ + "_velox_old")

// NormalizeType is t in the dialect's canonical spelling: lower case, white
// space collapsed; PostgreSQL's aliases varchar, int4, int8, float8, bool,
// timestamptz, decimal and int mapped to character varying, integer, bigint,
// double precision, boolean, timestamp with time zone, numeric and integer
// (with a length or precision kept); MySQL's integer display widths dropped,
// but for tinyint(1).
func NormalizeType(d velox.Dialect, t string) string

// StringType reports whether t, normalised, is a built-in string type:
// text, character varying[(n)] and character(n) on PostgreSQL; varchar(n),
// char(n), tinytext, text, mediumtext and longtext on MySQL; text on SQLite.
func StringType(d velox.Dialect, t string) bool
```

- Produces, in `internal/gen` (unexported; `Tables`, Task 5, derives every name through these and nowhere else):

```go
func pkName(table string) string             // ddl.Cap(table + "_pkey")
func seqName(table, column string) string    // ddl.Cap(table + "_" + column + "_seq")
func fkName(table, column string) string     // ddl.Cap(table + "_" + column + "_fkey")
func checkName(table, column string) string  // ddl.Cap(table + "_" + column + "_check")

// enumType is the PostgreSQL enum type of the enum field f of e: its
// Postgres SchemaType when that is not a string type; else, for a field with
// a Go type (EnumOf, or Enum with GoType), the snake case of the type's name
// without its package; else <table>_<column>. native is false when the
// Postgres SchemaType is a string type. Derived names are capped.
func enumType(e *Entity, f *Field) (name string, native bool)

// symbols is the foreign-key names ed's relation is given: ed's Symbols, else
// its pair's; nil when neither gives any.
func symbols(g *Graph, e *Entity, ed *Edge) []string

// joinTable is a many-to-many relation's table, once per relation.
type joinTable struct {
	Schema, Table string
	Columns       [2]string // the assoc edge's owner's column, then its target's
	Owner, Target *Entity   // of the assoc edge
	Edge          *Edge     // the assoc edge (the Bidi edge for a self M2M)
	Symbols       []string  // in the order of Columns
}
func joinTables(g *Graph) []joinTable // sorted by (Schema, Table)

// foreignKey is one foreign key of a relation that holds one, or one of a
// join table's two.
type foreignKey struct {
	Schema, Table, Column string // where the key is
	Name                  string // a Symbol, or fkName
	Ref                   *Entity
	OnDelete              string   // "", "CASCADE", "SET NULL" or "RESTRICT"; a join table's are "CASCADE"
	Owner                 *Entity  // the schema that declares the edge, for errors
	Edge                  *Edge
}
func foreignKeys(g *Graph) []foreignKey // sorted by (Schema, Table, Column)

// fkIndexed reports whether column leads the primary key or one of the
// non-partial indexes of a table (declared, unique-field, or a join table's
// primary key), so that it needs no foreign-key index.
func fkIndexed(pk []string, indexes [][]string, column string) bool

// object is one name the graph puts in a database, on any dialect.
type object struct {
	space  nameSpace // relations, types or constraints
	schema string    // the database schema (TableSchema)
	name   string
	owner  string    // the schema that declares it
	at     loc
	what   string    // as an error names it: "table", "index 0", "enum type of field \"role\""…
	values []string  // an enum type's values; nil for other objects
}
type nameSpace uint8 // spaceRelations, spaceTypes, spaceConstraints

// objects lists every object of g: tables (relations and types), indexes
// (declared, unique-field, foreign-key), primary keys and identity sequences
// (relations), enum types (types), foreign keys and checks (constraints), and
// the helpers velox_new_<table> (relations), <type>_velox_old (types) and
// <table>_<col>_not_null for each NOT NULL column (constraints). The indexes
// in refused are left out.
func objects(g *Graph, refused map[*Index]bool) []object
```

- [ ] **Step 1: Write the failing tests.**
  - `internal/ddl/name_test.go`:
    - `TestCap`: `Cap(strings.Repeat("a", 62))` and the 63-byte name are unchanged; `Cap(a64)` is `strings.Repeat("a", 54) + "_" + fmt.Sprintf("%08x", fnv32a(a64))`, 63 bytes; `Cap(strings.Repeat("a", 53) + "é" + strings.Repeat("b", 20))` cuts at byte 53, before the `é`, and is 62 bytes and valid UTF-8.
    - `TestCapHelpers` (Review Focus 2): for a 63-byte `base`, `OldEnum(base)`, `NotNull(base, "c")` and `Rebuild(base)` are each at most 63 bytes, differ from `base` and from `Cap(base)`, and equal `Cap` of the uncapped whole; for a short base they are `users_role_velox_old`, `users_email_not_null`, `velox_new_users`.
  - `internal/ddl/types_test.go`: `TestNormalizeType` (PostgreSQL `VARCHAR(20)` → `character varying(20)`, `int4` → `integer`, `int8` → `bigint`, `float8` → `double precision`, `bool` → `boolean`, `timestamptz` → `timestamp with time zone`, `decimal(10,2)` → `numeric(10,2)`, `int` → `integer`, ` Character  Varying(5) ` → `character varying(5)`; MySQL `int(11)` → `int`, `bigint(20) unsigned` → `bigint unsigned`, `tinyint(1)` kept; SQLite `TEXT` → `text`) and `TestStringType` (true: PostgreSQL `text`, `varchar(9)`, `char(3)`; MySQL `varchar(9)`, `tinytext`, `longtext`; SQLite `text`. False: PostgreSQL `citext`, `integer`, `status`; MySQL `json`, `blob`).
  - `internal/gen/build_test.go` and `dbnames_test.go`:
    - `TestIndexNamesCapped` (Review Focus 2): two indexes over columns whose names make 70-byte derived names that differ only after byte 54 get `ddl.Cap` of their uncapped names, distinct, at most 63 bytes; `PluginModel` shows the same names.
    - `TestIndexNamesAcrossGraph`: the same-schema line becomes `schema keyMixed: index 1: name "k" is also this schema's index 0`; the cross-schema lines stay.
    - `TestNamespace`, each case one error, compared with `plain`:
      - tables `Users` (`schema.Table("Users")`) and `users`: `schema tUsers: name "Users" is also schema tUser's table` (case-insensitive);
      - a table `orders_status` beside `orders.status`, a `field.Enum`: `… field "status": name "orders_status" is also schema tOrderStatus's table`;
      - an index `StorageKey("posts")` beside table `posts`: relations;
      - an edge `Symbol("users_role_check")` beside the enum check of `users.role`: constraints;
      - a table `velox_new_users` beside `users`: `… is also schema tUser's rebuild table`;
      - the same names in two database schemas (`schema.TableIn`): no error;
      - two `field.EnumOf[Status]` fields in two tables share `status`: no error.
    - `TestNamespaceIndexOnEnumColumn` (Review Focus 1): `field.Enum("status")` with `index.Fields("status")` builds.
    - `TestBuildRules` gains:
      - `"50 long table"`: `schema.Table` of 64 bytes: `table name "…" is 64 bytes; a database name has at most 63`;
      - `"50 long column"`: a field `StorageKey` of 64 bytes, `field "x": column name …`; `"50 long derived column"`: a field named with 64 bytes and no `StorageKey`, the same rule, with `; name it with StorageKey`;
      - `"50 long index key"`, `"50 long symbol"`, `"50 long join table"` (`edge.Table`), `"50 long enum type"` (`SchemaType{field.Postgres: <64 bytes>}`);
      - `"51 two symbols on O2M"`: `edge "pets": a relation that is not many-to-many has one foreign key; use edge.Symbol`;
      - `"51 one symbol on M2M"`: `edge "peers": a many-to-many relation has two foreign keys; name them with edge.Symbols`;
      - `"51 symbols disagree"`: on the inverse, `Symbols ["a"] differ from edge "pets"'s ["b"] on <schema>`;
      - `"52 shared values differ"`: `SchemaType{field.Postgres: "status"}` on two fields with other values: `field "state": enum type status is also field "status" of schema tOrder (<pos>), with other values`;
      - `"44 table and enum type"` as in `TestNamespace`.
    - `TestBuildRulesAreSingle` gains each new schema.
- [ ] **Step 2: Run them.** Run `go test ./internal/ddl/ ./internal/gen/ -run 'Cap|Normalize|StringType|IndexNames|Namespace|BuildRules'`. Expected: FAIL (package `ddl` and `objects` undefined).
- [ ] **Step 3: Implement** as the Interfaces say.
  - `indexName` returns `ddl.Cap` of the whole name, suffixes included.
  - `indexes` no longer checks names; it records in `builder.refused` (a `map[*Index]bool`) the indexes rules 23–25, 42 and 43 refuse or whose derived name is partial, as it skips them today.
  - `names` (step 5b) runs rule 50 on every table and column name, explicit or derived (implicit foreign keys, join tables and their columns), every index `StorageKey`, `Symbol` and enum type named by `SchemaType`; then rules 44 and 52 over `objects(b.g, b.refused)`: a key is (space, lower-cased schema, lower-cased name); a second object with a key reports `name %q is also <other>`, where `<other>` is `this schema's <what>` or `schema X's <what>`; a pair is reported once (in the types space, two tables are not compared); two enum types with one name are one type when their values are equal and in order, and rule 52 otherwise.
  - Rule 51 is checked in `validateEdges`, on resolved edges only.
- [ ] **Step 4: Run the tests and the generator goldens.** Run `go test ./internal/ddl/ ./internal/gen/ ./plugin/ ./cmd/velox-gen/`. Expected: PASS, and `TestGolden` unchanged (no golden name is over 63 bytes).
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Generator: capped derived names, one database namespace, rules 50 to 52`.

### Task 2: `field.SchemaDefault` and `mixin.Time`'s defaults

**Files:**
- Modify: `schema/field/field.go` (`SchemaDefault`; the docs of `SchemaType`, and in `builders.go` of `Enum`, `EnumOf` and `Uint64`), `schema/internal/desc/desc.go` (`Field.SchemaDefault`), `schema/load/marshal.go`, `internal/gen/loadjson/loadjson.go` (`Field.SchemaDefault`, `Version` 3), `internal/gen/discover_test.go` (the version literal), `internal/gen/build.go` (rules 16 and 21), `schema/mixin/mixin.go`.
- Test: `schema/field/field_test.go`, `schema/load/marshal_test.go`, `schema/mixin/mixin_test.go`, `internal/gen/build_test.go`, `internal/gen/lower_test.go`, `internal/gen/plugin_test.go`.
- Regenerate: `internal/gen/testdata/golden/blog/want`, `internal/testmodel`, `example/tour/gen`.

**Interfaces:**

```go
// SchemaDefault sets the column's default in the database per dialect, as
// SQL written as the database prints it, such as {field.Postgres: "now()"}.
// velox's inserts write every column, so it serves rows written outside
// velox and existing rows when the column is added. The keys are Postgres,
// MySQL and SQLite; any other, or an empty value, is a misuse.
func (c common[B]) SchemaDefault(m map[string]string) B

// desc.Field:
	SchemaDefault map[string]string // the database default per dialect, as SQL
// loadjson.Field:
	SchemaDefault map[string]string `json:"schema_default,omitempty"`
// loadjson:
const Version = 3
```

- `CreateTime` and `UpdateTime` (so `Time` too) give `SchemaDefault(map[string]string{field.Postgres: "now()", field.MySQL: "CURRENT_TIMESTAMP(6)", field.SQLite: "CURRENT_TIMESTAMP"})`.
- `Lower` and `PluginModel` do not read it.

- [ ] **Step 1: Write the failing tests.**
  - `TestSchemaDefault` (`field_test.go`): the descriptor holds a copy of the map (changing the caller's map after does not change it); `{"oracle": "x"}` is the misuse `SchemaDefault: unknown dialect "oracle"; the keys are field.Postgres, field.MySQL and field.SQLite`; `{field.MySQL: ""}` is `SchemaDefault: the mysql default is empty`.
  - `TestMarshalSchemaDefault` (`marshal_test.go`): the load model's field has `schema_default`; a field without one has no key.
  - `TestTimeSchemaDefault` (`mixin_test.go`): both fields of `Time{}` carry the three defaults above.
  - `TestBuildRules` gains `"16 schema default on the tenant"` (the existing rule-16 message) and `"21 schema default on an integer ID"`: `field "id": an integer ID is assigned by the database and cannot have a SchemaDefault`. A string ID with a `SchemaDefault` builds.
  - `TestSchemaDefaultChangesOnlyTheHash` (`lower_test.go`): a schema generated with and without a `SchemaDefault` gives files that differ only in the `// Model: sha256:` line.
  - `TestPluginModelHasNoSchemaDefault` (`plugin_test.go`): the plugin model's JSON of that schema has no `schema_default`.
- [ ] **Step 2: Run them.** Run `go test ./schema/... ./internal/gen/ -run 'SchemaDefault|TimeSchemaDefault|BuildRules'`. Expected: FAIL (`SchemaDefault` undefined).
- [ ] **Step 3: Implement.** The docs: `SchemaType`'s says that on an enum field a PostgreSQL value that is not a string type names the enum type, which the planner creates and manages and fields may share, and that a string type opts that dialect out to text plus a `CHECK`; `Enum`'s and `EnumOf`'s say enums are native on PostgreSQL and MySQL, and that MySQL and PostgreSQL sort an enum by its values' declaration order; `Uint64`'s says SQLite's `integer` holds at most 2⁶³−1.
- [ ] **Step 4: Regenerate** the three trees (Global Constraints). `git diff --stat` shows only `// Model: sha256:` lines in `internal/gen/testdata/golden/blog/want`, `internal/testmodel` and `example/tour/gen`. Run `go test ./... && (cd example && go test ./...)`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0, the `-check` and `verify` lines included.
- [ ] **Step 6: Commit** `Schema: field.SchemaDefault, and mixin.Time's database defaults`.

### Task 3: Rule 49, and `SQLType` leaves the core

**Files:**
- Modify: `internal/gen/build.go` (rule 49), `internal/gen/lower.go` (`nilKinds` is slice, map and ptr), `internal/gen/plugin.go` (`nullable`'s doc), `plugin/plugin.go` (`Field.Nullable`'s doc), `desc.go` (`ColumnDesc.SQLType` removed), `meta.go` (`ColumnInfo.SQLType` removed).
- Test: `internal/gen/build_test.go`, `internal/gen/plugin_test.go`, `meta_test.go`, `desc_internal_model_test.go`, `compilecost_test.go` (only if a row moves).

**Interfaces:**
- Produces: `nullable(gf *Field) bool` (unchanged signature) is the one nullability rule: `gf.Nillable || gf.Optional && gf.Type == "json" && gf.GoType != nil && nilKinds[gf.GoType.Kind]`, with `nilKinds` = `{"slice", "map", "ptr"}`. `Tables` (Task 5) calls it.
- Rule 49: a JSON field whose Go type's kind is `interface`, `func`, `chan` or `unsafe.Pointer` is refused; one of kind `complex64` or `complex128` is refused unless `GoType.Marshaler`.

- [ ] **Step 1: Write the failing tests.**
  - `TestBuildRules` gains `"49 func"` (`field.JSON("cb", func() {})`): `field "cb": its Go type func() is a func, which a JSON column cannot hold`; `"49 chan"`; `"49 complex"` (`complex(1, 2)`): the same, then `; give the type a MarshalJSON or MarshalText method`; `"49 func with MarshalJSON"` (a test-local `type jsonFunc func()` with `MarshalJSON`): refused as `"49 func"`.
  - A test-local `type jsonComplex complex128` with `MarshalJSON` builds as a required JSON field.
  - `TestNullableMatchesCore` (`plugin_test.go`): for an `Optional` and a `Nillable` JSON field of a slice, a map, a pointer and a struct (the struct one `Nillable` only), `nullable` equals what the core reads: the lowered field's `Pointer || NilIsNull`, or its Go type a pointer.
  - `meta_test.go` and `desc_internal_model_test.go` lose their `SQLType` assertions and literal.
- [ ] **Step 2: Run them.** Run `go test ./internal/gen/ -run 'BuildRules|Nullable'`. Expected: FAIL (rule 49 missing).
- [ ] **Step 3: Implement.** Remove both `SQLType` fields and the line of `buildDesc` that copies it.
- [ ] **Step 4: Run the tests, the budgets and the escape check.** Run `go test -count=1 ./...`, `go test -run Budget -v .`, the escape check and `go test -run TestGenericTypesCompileSmall -v .`. Expected: PASS, budgets unchanged; a compile-cost row that moved is re-measured by the 1.2% rule in this commit.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Generator: JSON fields no codec can write are refused; SQLType leaves the core`.

### Task 4: The `migrate` model and its snapshot JSON

**Files:**
- Create: `migrate/doc.go` (a short package doc; plan 3 writes the full one), `migrate/model.go`, `migrate/json.go`, `migrate/errors.go`, `migrate/model_test.go`, `migrate/json_test.go`, `migrate/imports_test.go`.
- Modify: `godoc_test.go` (`add("migrate", false, false)`).

**Interfaces:**
- Produces: §2's types and §14's model block, verbatim: `Schema`, `TableRef`, `ColumnRef`, `Table`, `Column`, `Index`, `ForeignKey`, `Check`, `Enum`, `(*Schema).Table`, `(*Schema).Equal`, `Schema.MarshalJSON`, `(*Schema).UnmarshalJSON`, `(*Table).Ref`, `TableRef.String`. And:

```go
type Error struct{ Pos, Object, Code, Reason string }

// Error is "migrate: " and the non-empty of Pos, Object and Reason, joined
// by ": ". Code names the failure for programs ("primary-key", "rename").
func (e *Error) Error() string

func (s *Schema) clone() *Schema // a deep copy; Diff, Lint and Render plan on clones
func (s *Schema) sort()          // every list in its order of §2
```

- **Equal** ignores `Pos` and column order, treats a nil and an empty slice alike, and compares everything else exactly, `Dialect` included.
- **JSON** (version 1), compact; plan 2's writer indents it. Keys in snake case, empty values left out, `Pos` never written; `MarshalJSON` writes a sorted copy:

```json
{"version":1,"dialect":"postgres","tables":[{"name":"users","columns":[{"name":"id","type":"bigint","identity":true,"sequence":"users_id_seq"}],"primary_key":["id"],"primary_key_name":"users_pkey","indexes":[{"name":"users_email_unique","columns":["email"],"unique":true}],"foreign_keys":[{"name":"users_org_id_fkey","columns":["org_id"],"ref_table":{"name":"orgs"},"ref_columns":["id"],"on_delete":"CASCADE"}],"checks":[{"name":"c","expr":"…"}],"comment":"…","collation":"…"}],"enums":[{"name":"users_role","values":["viewer","admin"]}]}
```

  `UnmarshalJSON` refuses a version other than 1 (`migrate: schema.json is version 2; this velox reads version 1`) and an unknown dialect.

- [ ] **Step 1: Write the failing tests.**
  - `TestTableRefString`: `{Schema: "billing", Name: "invoices"}` is `billing.invoices`; `{Name: "invoices"}` is `invoices`.
  - `TestSchemaTable`: finds by `(Schema, Name)`; a missing ref is `nil, false`.
  - `TestEqualIgnoresPosAndColumnOrder`; `TestEqualComparesEverythingElse`: a table of edits, one per field of every type (a column's `Default`, an index's `Where`, a foreign key's `OnDelete`, an enum's value order, `Collation`, `PrimaryKeyName`, `Dialect` …), each making `Equal` false.
  - `TestSnapshotRoundTrip`: a schema with every field set unmarshals to one `Equal` to it, and marshals to the same bytes twice; `TestSnapshotSorted`: an unsorted schema marshals sorted, and is not changed; `TestSnapshotOmitsPos`; `TestSnapshotRefusesVersion`.
  - `TestErrorText`: `&Error{Pos: "schema/user.go:3", Object: "users", Reason: "r"}` is `migrate: schema/user.go:3: users: r`.
  - `TestImports` (`imports_test.go`): parses `migrate/*.go` (not tests) and finds only standard-library imports, `github.com/syssam/velox2` and `github.com/syssam/velox2/internal/ddl`.
- [ ] **Step 2: Run them.** Run `go test ./migrate/`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement** as the Interfaces say.
- [ ] **Step 4: Run them, and `go test -run TestGodocCitesNoSpec .`.** Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: the model and its snapshot JSON`.

### Task 5: `internal/gen.Tables`

**Files:**
- Create: `internal/gen/tables.go`, `internal/gen/tables_test.go`, `internal/gen/testdata/tables/{postgres,mysql,sqlite}.json`, `internal/ddl/literal.go`, `internal/ddl/literal_test.go` (with `FuzzLiteral`).
- Modify: `scripts/ci-local.sh` and `.github/workflows/ci.yml` (one fuzz line each: `go test -run XXX -fuzz FuzzLiteral -fuzztime 10s ./internal/ddl`).

**Interfaces:**
- Consumes: Task 1's `dbnames.go` and `ddl`; Task 3's `nullable`; Task 4's model.
- Produces:

```go
// Tables builds the database model of g for the dialect d: one table per
// entity and per many-to-many join table, sorted. Its errors are *Error
// values with positions, joined as Build's are (Task 6 adds them).
func Tables(g *Graph, d velox.Dialect) (*migrate.Schema, error)

// in internal/ddl:
// Literal is s as a string literal of the dialect: ' doubled, and on MySQL \ doubled too.
func Literal(d velox.Dialect, s string) string
// Unquote is the string a literal of Literal's form holds; false when lit is not one.
func Unquote(d velox.Dialect, lit string) (string, bool)
```

- What `Tables` builds is §3, §4, §5 and §6, with these values:
  - **Types:** §4's table, keyed by the field type and `Size`; a `SchemaType` for the dialect wins, through `ddl.NormalizeType`, except on an enum (below).
  - **IDs:** an integer ID is `Identity`; on PostgreSQL its type is `bigint` for `uint`/`uint64` and its `Sequence` is `seqName`; on SQLite every integer ID is `integer`.
  - **Foreign-key columns** copy the referenced ID column's `Type`, with no identity.
  - **Nullable** is `nullable(f)`.
  - **Default** is §5's: `SchemaDefault[d.String()]` verbatim; else a literal `Default` of a bool (`true`/`false` on PostgreSQL, `1`/`0` elsewhere), number (`strconv`), string or enum (`ddl.Literal`; on MySQL a text-family column takes `(<literal>)`); else, for an `Optional` field that is not `Nillable`, of type string, bool or number, its zero value; else "". An identity column has none.
  - **Comments:** a field's `Comment`; an implicit foreign key takes the `Comment` of the edge `Implicit` names; a table, its entity's `Comment` annotation; a join table, none; all "" on SQLite.
  - **Primary key:** `[id]`, or a join table's two columns; `PrimaryKeyName` is `pkName` on PostgreSQL, `PRIMARY` on MySQL, "" on SQLite.
  - **Indexes:** the `Graph`'s; each `Unique` field but the ID as `indexName(table, &Index{Columns: []string{col}, Unique: true})` unless a declared unique index on that column alone exists; each foreign-key column `fkIndexed` says needs one, as `indexName` of a one-column non-unique index (a join table's target column always).
  - **Foreign keys:** `foreignKeys(g)`, `RefColumns` the referenced ID's column, `RefTable` its entity's `TableRef`.
  - **Enums:** PostgreSQL, a native field's `Type` is the quoted type name (schema-qualified when its table's schema is not "") and the schema's `Enums` holds the type once per (schema, name), its values in order; MySQL, `enum('a','b')`; SQLite, and an opted-out field on any dialect, its string type with a `Check` named `checkName` whose `Expr` is `<quoted column> IN (<literal>, <literal>…)`.
  - **MySQL:** every table's `Collation` is `utf8mb4_bin`.
  - **`Pos`:** a column's, index's, foreign key's (its edge's) and check's (its field's) are the element's; a table's is its first declared field's or edge's; an enum's is its first field's.

- [ ] **Step 1: Write the failing tests** (`tables_test.go`, schemas declared in the test and built with `build`):
  - `TestTablesGolden`: one set of schemas covering every field type and option, every relation kind (O2O, O2M with and without an inverse, M2O, M2M, a Bidi M2M, a self O2O), `Symbol`s, `OnDelete`s, `Enum`, `EnumOf` shared by two tables, `SchemaType` enums (named, shared, opted out), comments, a partial index and a `StorageKey` index, gives `testdata/tables/<dialect>.json` (`json.MarshalIndent`). PostgreSQL and MySQL also get a schema with `schema.TableIn("billing", "invoices")`.
  - `TestTablesTypes`: a table over §4's rows on three dialects, with the size edges: string `MaxLen` 16383 / 16384 / 65535 / 65536 / 16777215 / 16777216 on MySQL (`varchar(16383)`, `text`, `text`, `mediumtext`, `mediumtext`, `longtext`); 10485760 / 10485761 on PostgreSQL (`character varying(10485760)`, `text`); bytes 255 / 256 / 65536 / 16777216 on MySQL (`varbinary(255)`, `blob`, `mediumblob`, `longblob`); a `SchemaType{field.MySQL: "INT(11)"}` gives `int`.
  - `TestTablesIdentity`: an `int64` ID on PostgreSQL is `bigint`, `Identity`, `Sequence` `users_id_seq`; a `uint64` ID is `bigint` there and `bigint unsigned` on MySQL; a string or UUID ID has no identity.
  - `TestTablesForeignKeyCopiesType`: a `uint64` ID gives `bigint` foreign keys on PostgreSQL, a declared `field.Uint64` key field included, and `bigint unsigned` on MySQL.
  - `TestTablesNullable`: `Nillable` → true; `Optional` string → false; `Optional` JSON slice → true; `Optional().Nillable()` JSON struct → true.
  - `TestTablesDefaults` (Review Focus 3): `SchemaDefault` verbatim per dialect; `Default("it's a \\ test")` is `'it''s a \ test'` on PostgreSQL and SQLite and `'it''s a \\ test'` on MySQL, `('…')` on a MySQL `Text` column; `Nillable().Default(true)` is `true` / `1` / `1`; `Default(1.5)` is `1.5`; `Optional` string, bool and int are `''`, `false`/`0`/`0`, `0`; a `DefaultFunc`, a time and a UUID default give ""; enum `Default("viewer")` is `'viewer'`.
  - `TestTablesEnums`: §3's three PostgreSQL names; `EnumOf[OrderStatus]` in two tables is one `Enum` `order_status`; `SchemaType{field.Postgres: "text"}` gives `text` and the check `"role" IN ('a', 'b')` named `users_role_check`; an enum value `o'neil` is `'o''neil'` in the check and in MySQL's `enum(…)`.
  - `TestTablesIndexes`: the unique-field rule; a foreign-key index for an O2M key with no index, none for one an index leads with, one beside a partial index on that column; a join table's target index and none on its owner column.
  - `TestTablesForeignKeys`: derived and `Symbol` names, a `Symbol` given on the inverse edge only, `OnDelete`, a join table's two `CASCADE` keys named by `Symbols` in column order.
  - `TestTablesComments`, `TestTablesMySQLCollation`.
  - `TestTablesDoesNotChangeGraph`: the `Graph`'s JSON is the same after `Tables` on each dialect; `TestTablesDeterministic`: two calls give `Equal` models and the same JSON.
  - `internal/ddl/literal_test.go`: `TestLiteral` (the strings above) and `FuzzLiteral`: for every string and dialect, `Unquote(d, Literal(d, s))` is `s, true`.
- [ ] **Step 2: Run them.** Run `go test ./internal/gen/ ./internal/ddl/ -run 'Tables|Literal'`. Expected: FAIL (`Tables` undefined).
- [ ] **Step 3: Implement.** Write the goldens with `-update` and read them line by line against §3–§6.
- [ ] **Step 4: Run them, and the fuzz test.** Run `go test ./internal/gen/ ./internal/ddl/` and `go test -run XXX -fuzz FuzzLiteral -fuzztime 20s ./internal/ddl`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0, with the new fuzz line.
- [ ] **Step 6: Commit** `Generator: Tables, the one builder of the database model`.

### Task 6: `Tables`' plan-time refusals

**Files:**
- Modify: `internal/gen/tables.go` (the checks, in `tables_check.go` if `tables.go` passes 600 lines), `internal/gen/tables_test.go`.

**Interfaces:**
- Consumes: Task 5's `Tables`.
- Produces: `Tables`' errors, each a `*Error` with the element's `Pos`, `Schema` and `Where`, joined and ordered by schema name, then by the order of the elements:
  - **MySQL:**
    - an index with `Where`: `MySQL has no partial indexes (Where); drop Where or use a separate MySQL schema`;
    - an index, or a `Unique` field, on a column of type `tinytext`, `text`, `mediumtext`, `longtext`, a `blob` type or `json`: `column x is <type> on MySQL, which an index covers only by a prefix; give the field a MaxLen or a SchemaType`;
    - an index whose key is over 3072 bytes, counting 4 bytes per `varchar` or `char` character, a `varbinary`'s length, and 1, 2, 4, 8, 4, 8, 8 and 2 bytes for `tinyint`, `smallint`, `int`, `bigint`, `float`, `double`, `datetime(6)` and `enum`, and nothing for another `SchemaType`: `the key is <n> bytes on MySQL, over its 3072`;
    - a row over 65535 bytes in `varchar` and `varbinary` columns, as ruling 47 counts it, with §16's text: the schema's file as `Pos`, then one line per such field, `  <pos>: field "<name>": <type>, <n> bytes`, then `  use Text() or a smaller MaxLen`;
    - an enum value over 255 characters; an enum's MySQL `SchemaType` that is not `ddl.StringType`; a column comment over 1024 characters or a table comment over 2048.
  - **PostgreSQL:** an enum's `SchemaType` that is neither a string type nor a type name, which is `^[a-z_][a-z0-9_]*$` and not a type of §4's PostgreSQL column or `ddl.NormalizeType`'s aliases.
  - **SQLite:** a `TableSchema`: `schema.TableIn puts the table in database schema <s>, which SQLite does not have`.
  - **Any dialect:** a `field.Other` with no `SchemaType` for the dialect: `an Other field needs a SchemaType for <dialect>`.

- [ ] **Step 1: Write the failing tests.**
  - `TestTablesRefusals`: one case per refusal above, on its dialect, asserting the error's text contains the reason and its `Pos` is the element's line (`here()`), and that the other two dialects build the same schema (but for `Other`).
  - `TestTablesRowLimit`: two `MaxLen(10000)` strings refuse on MySQL with `these need 80004:` and both fields' lines, `varchar(10000), 40002 bytes`; a row of exactly 65535 bytes builds (Spec §17's targeted itest is plan 2's).
  - `TestTablesKeyLimit`: an index over a `MaxLen(769)` string (`varchar(769)`, 3076 bytes) refuses with `the key is 3076 bytes`; over a `MaxLen(768)` one (3072 bytes) builds.
- [ ] **Step 2: Run them.** Run `go test ./internal/gen/ -run 'TablesRefusals|RowLimit|KeyLimit'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/gen/`. Expected: PASS; `TestTablesGolden` unchanged.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Generator: Tables refuses what a dialect cannot hold, with positions`.

### Task 7: `migrate.Diff`

**Files:**
- Create: `migrate/change.go`, `migrate/diff.go`, `migrate/diff_test.go` (package `migrate_test`), `migrate/cases_test.go` (package `migrate_test`: the base schema and the cases every later task reuses), `migrate/apply_test.go` (package `migrate`: the model-level apply), `migrate/export_test.go`.

**Interfaces:**
- Consumes: Task 4's model and `clone`; in tests, `gen.Build`, `gen.Tables` (Tasks 5–6), `load.Marshal` and `loadjson`.
- Produces: §14's Diff block, verbatim (`Change`, `Kind`, `ColumnChange` with `ColumnType`, `ColumnNullable`, `ColumnDefault`, `ColumnIdentity`, `ColumnComment` as bits from 1, each change struct, `DiffOptions`, `Diff`), plus one change (ruling 8):

```go
type SetTableCollation struct{ Table TableRef; From, To string } // Kind "set-table-collation"; MySQL
```

  No `Kind` constants: callers switch on the change's type (ruling 18). In tests:

```go
// apply_test.go (package migrate): s with changes applied as a model, for the invariants.
func applyChanges(s *Schema, changes []Change) (*Schema, error)
// export_test.go:
var ApplyChanges = applyChanges

// cases_test.go (package migrate_test):
type testCase struct {
	name     string
	dialects []velox.Dialect                         // nil: all three
	from     func(*loadjson.Model)                   // edits the base for from; nil: the base
	to       func(*loadjson.Model)                   // edits the base for to
	fromDB   func(d velox.Dialect) *migrate.Schema   // a hand-made from (serial, ent-shaped); nil: from the load model
	empty    bool                                    // from is the empty schema
	opts     migrate.DiffOptions
	allow    []migrate.Allow
}
var cases []testCase
func models(t *testing.T, c testCase, d velox.Dialect) (from, to *migrate.Schema) // Build and Tables of the edited copies
```

- **Diff** is §7.1's matching, §7.2's changes and §7.3's order, with these decisions:
  - Column types compare modulo enum renames: a column whose type is a renamed enum type is not altered.
  - `RenameSequence` only when `Identity` is unchanged; otherwise the `AlterColumn` carries it.
  - A PostgreSQL `AlterEnum` that only adds values, keeping the others' order, is one change in step 3. One that removes or reorders is two: step 3 adds the new values at the end (`AlterEnum{From, Mid}`), step 9 swaps to the final order (`AlterEnum{Mid, To}`) after the columns are dropped (ruling 9).
  - Step 7 is indexes, then checks, then foreign keys, each across all tables, sorted by table then name (ruling 10).
  - Foreign keys among created tables that reference a table not created yet are left out of the `CreateTable` and added in step 7, on PostgreSQL and MySQL; a self-reference stays inline; SQLite keeps every key inline (ruling 11).
  - `SetTableCollation` is in step 5, beside `SetTableComment`.
  - Errors are `*Error`: `from` and `to` of different dialects (`Code` "dialect"); a changed primary key (`Code` "primary-key", `migrate: users: the primary key changes from (id) to (id, org_id); write this migration by hand`); a rename whose source is not in `from`, whose target is not in `to`, or whose target is also in `from` (`Code` "rename", ruling 7).
- **Cases** (each a few lines in `cases_test.go`, reused by Tasks 8–10): `create-tables` (empty `from`, with a two-table cycle and a self-reference), `drop-table`, `rename-table`, `add-column-optional`, `add-column-nillable`, `add-column-required`, `add-column-default`, `add-column-schema-default`, `drop-column`, `rename-column`, `widen-int`, `widen-varchar`, `narrow-varchar`, `set-not-null`, `drop-not-null`, `change-default`, `change-comment`, `table-comment`, `key-type-change` (the ID `int32` → `int64`, with keys referencing it), `add-index`, `drop-index`, `rename-index` (a `StorageKey` changed), `add-unique`, `fk-index-replaced`, `fk-index-restored`, `add-foreign-key`, `drop-foreign-key`, `on-delete-change`, `enum-append`, `enum-insert`, `enum-remove`, `enum-reorder`, `enum-shared` (an `EnumOf` in two tables loses a value), `enum-opt-out` (native to `SchemaType` text), `literals`, `schema-qualified` (PostgreSQL and MySQL), `add-identity` and `serial-to-identity` (PostgreSQL, hand-made `from`), `collation` (MySQL, hand-made `from`), `rebuild-parent` (SQLite: a parent with `CASCADE` children gains a column type change).

- [ ] **Step 1: Write the failing tests** (`diff_test.go`, over `cases` on each of their dialects unless named):
  - `TestDiffSelfIsEmpty`: `Diff(x, x)` is empty for every case's `from` and `to`, and for the three `Tables` goldens of Task 5 read from `../internal/gen/testdata/tables`.
  - `TestApplyDiffGivesTo`: `ApplyChanges(from, Diff(from, to))` is `Equal` to `to`.
  - `TestDiffDoesNotMutate`: `from` and `to`, deep-copied before, are `reflect.DeepEqual` to the copies after `Diff`.
  - `TestDiffDeterministic`: two calls give `reflect.DeepEqual` changes.
  - `TestDiffOrder`: one hand-made pair touching every step gives the kinds in §7.3's order, with ruling 10's sub-order.
  - `TestDiffRenameTableCarriesDerivedNames` (Review Focus 5): `rename-table` on PostgreSQL gives `rename-table`, `rename-sequence` (`users_id_seq` → `accounts_id_seq`), `rename-index` for each derived index, `rename-constraint` for `users_pkey` and `users_org_id_fkey`, and `rename-enum` for `users_role`; no change names the `StorageKey` index, the `Symbol` key or the `EnumOf` type; on MySQL there is no primary-key or enum change.
  - `TestDiffMatchesByDefinition`: an index, a foreign key and a check whose names alone differ are renames; one whose definition differs under one name is a drop in step 1 and an add in step 7.
  - `TestDiffFKIndexReplaced`: in `fk-index-replaced`, the `AddIndex` of `(org_id, created_at)` precedes the `DropIndex` of `users_org_id`.
  - `TestDiffCreateOrder`: in `create-tables`, a referenced table is created first; in the cycle one key is an `AddForeignKey` after both `CreateTable`s on PostgreSQL and MySQL and inline on SQLite; the self-reference is inline.
  - `TestDiffEnumValues`: `enum-append` and `enum-insert` are one `AlterEnum` on PostgreSQL; `enum-remove` and `enum-reorder` are the two of the decision above; on MySQL each is an `AlterColumn` of type; on SQLite a check drop and add.
  - `TestDiffErrors`: the three `*Error`s above, with their codes and texts.
- [ ] **Step 2: Run them.** Run `go test ./migrate/`. Expected: FAIL (`Diff` undefined).
- [ ] **Step 3: Implement** `Diff` on clones of its arguments, and `applyChanges` in the test file.
- [ ] **Step 4: Run** `go test ./migrate/`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: Diff, with derived-name renames and the change order`.

### Task 8: `Lint`, `Blocked` and `ParseAllow`

**Files:**
- Create: `migrate/lint.go`, `migrate/lint_test.go`, `migrate/testdata/lint/{postgres,mysql,sqlite}.txt`.

**Interfaces:**
- Consumes: Task 7's changes and cases.
- Produces: §14's Lint block, verbatim (`Class` with `Safe`, `Review`, `Destructive` and `String`, `Finding`, `Allow`, `ParseAllow`, `Lint`, `Blocked`).
  - Codes, classes and conditions are §8.1's table, plus `convert-collation` on `SetTableCollation`. `Finding.Target` is the table's ref, then `.` and the column, index, foreign key or check it acts on; an enum type's ref for PostgreSQL enum findings; `Finding.Pos` is the `to` element's.
  - `widen-type`: PostgreSQL `smallint` → `integer` → `bigint`, `real` → `double precision`, `character varying(n)` → `(m > n)` or `text` (safe on PostgreSQL), MySQL `tinyint` → `smallint` → `int` → `bigint` (signed and unsigned apart), `float` → `double`, `varchar(n)` → `(m > n)`, `tinytext` → `text` → `mediumtext` → `longtext` and the blob family likewise, SQLite none (a SQLite type change is `sqlite-rebuild` only). Any other type change is `alter-type`.
  - `volatile-default`: a PostgreSQL `AddColumn` on an existing table whose default is not a string literal, a number, `true`, `false` or `NULL`.
  - No finding for: the `DropForeignKey` and `AddForeignKey` around a key's type change; the `DropIndex` of an index whose columns lead an index the same plan adds (a replacement); a `DropIndex`, `DropForeignKey` or `DropCheck` folded into a SQLite rebuild (the rebuild's `sqlite-rebuild` stands for them).
  - `ParseAllow` takes `code[=target]` items joined by `,`; an empty item or a code that is not destructive is an `*Error` with `Code` "allow".
  - `Blocked` keeps the destructive findings no `Allow` covers (`Code` equal, `Target` "" or equal), in order.

- [ ] **Step 1: Write the failing tests.**
  - `TestLintGolden`: for each case and dialect, `testdata/lint/<dialect>.txt` holds a section `== <case>` and one line per finding: `<change index> <code> <class> <target> [<pos>]: <message>`.
  - `TestLintAddColumn` (Review Focus 4): `add-column-optional`, `add-column-nillable` and `add-column-default` give no finding; `add-column-required` gives one `add-required-column`, destructive, target `users.<col>`; the same on a table `from` lacks gives none.
  - `TestLintWidenType`: a table of `(dialect, from type, to type, code, class)` over the lattice above, `character varying(10)` → `text` safe on PostgreSQL, `integer` → `smallint` `alter-type`.
  - `TestLintReplacementsAreQuiet`: `key-type-change` has no `drop-foreign-key` or `add-foreign-key` finding; `fk-index-replaced` no `drop-index`.
  - `TestBlocked` and `TestParseAllow` (`"drop-column=users.name,drop-table"` gives two; `"rename-table"`, `""` and `"drop-column=,"` are errors).
  - `TestLintDoesNotMutate` and `TestLintDeterministic`.
- [ ] **Step 2: Run them.** Run `go test ./migrate/ -run 'Lint|Blocked|ParseAllow'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Write the goldens with `-update`; read each against §8.1.
- [ ] **Step 4: Run** `go test ./migrate/`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: Lint classifies changes; Blocked and ParseAllow`.

### Task 9: `Render`: scripts, MySQL and SQLite

**Files:**
- Create: `migrate/render.go` (the types, `Render`, `writeScript`), `migrate/render_mysql.go`, `migrate/render_sqlite.go`, `migrate/render_test.go`, `migrate/testdata/render/<case>.{mysql,sqlite}.sql`.

**Interfaces:**
- Consumes: Tasks 7 and 8; `ddl.Literal`, `ddl.Rebuild`.
- Produces: §14's Render block, verbatim (`RenderOptions`, `ScriptKind` with `MainScript`, `EnumScript`, `OnlineScript`, `Script`, `Statement`, `Render`), and `Directives` as §14's directory block has it (plan 2's `File` reuses it). And:

```go
// writeScript writes s as the body of its file: the directives (no-transaction,
// foreign-keys off, lock-timeout <d>, then each allow <code> <target>, sorted),
// a blank line, then each statement: a "-- velox:unit" line before a unit's
// first statement in a no-transaction script, a "-- review: <text>" line when
// it has one, and the SQL with ";". Plan 2's directory writes files with it.
func writeScript(b *strings.Builder, s Script)
```

- **Common to every dialect:**
  - `Render` calls `Lint` for the review comments (`Statement.Review` is `<code>: <message>`, several joined by `; `) and writes an `allow <code> <target>` directive, into the script holding the change's first statement, for each destructive finding an `opts.Allow` covers. `Render` refuses changes `Diff` would not give (`*Error`, `Code` "render").
  - In a script run in a transaction every `Statement.Unit` is 0; otherwise statements are numbered into units from 0.
  - Consecutive changes on one table that are actions of one `ALTER TABLE` join one statement, comma-separated, carrying the first change's index.
- **MySQL** (§8.2's MySQL column): one main script, with `LockTimeout` when `opts.LockTimeout` > 0, every statement its own unit.
  - Column definitions: `` `c` <type> NULL|NOT NULL [AUTO_INCREMENT] [DEFAULT x] [COMMENT '…'] ``; tables end `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin [COMMENT='…']`.
  - A table's index adds and drops of steps 7 and 8, and the drops of step 1 whose name an add reuses, are one `ALTER TABLE t ADD INDEX …, DROP INDEX …, ALGORITHM=INPLACE, LOCK=NONE` at step 7; foreign keys and checks follow in their own statements; step-1 drops of indexes on a dropped column stay at step 1 (ruling 10).
  - `SetTableCollation` is `ALTER TABLE t CONVERT TO CHARACTER SET utf8mb4 COLLATE <to>`.
- **SQLite** (§8.2's SQLite column, §8.4): one main script, no lock timeout.
  - The rebuild runs at the position of the table's last change and folds every change to the table but its rename (ruling 22); the script gets `ForeignKeysOff`; `sqlite_sequence`'s two statements only for a table with an `AUTOINCREMENT` column; the copy maps renamed columns and leaves out added ones.
  - A non-integer primary key is a table constraint `PRIMARY KEY ("id")`, unnamed (ruling 13).
- `Render` on PostgreSQL returns `*Error` (`Code` "render", `PostgreSQL rendering is not built yet`) until Task 10.

- [ ] **Step 1: Write the failing tests.**
  - `TestRenderGolden`: for each case on MySQL and SQLite, with `RenderOptions{LockTimeout: 5 * time.Second, Allow: c.allow}` (each case's `allow` names every destructive code it has), `testdata/render/<case>.<dialect>.sql` is each script as `-- script: main|enum|online` then `writeScript`'s text.
  - `TestRenderMySQLIndexStatement`: `fk-index-replaced` and `fk-index-restored` are one `ALTER TABLE … ALGORITHM=INPLACE, LOCK=NONE` each, the add first.
  - `TestRenderSQLiteRebuild`: `rebuild-parent` is §8.4's statements in order, `ForeignKeysOff` set, one `sqlite-rebuild` review line, and a table with a string ID has no `sqlite_sequence` statement.
  - `TestRenderQuotesIdentifiers`: a column named ``a`b`` and `a"b` is quoted with the quote doubled.
  - `TestRenderLiterals` (Review Focus 3): `literals` has `'it''s a \\ test'` on MySQL and `'it''s a \ test'` on SQLite, and the comment and enum value likewise.
  - `TestRenderDirectives`: `drop-column` with `Allow{Code: "drop-column"}` writes `-- velox:allow drop-column users.legacy`; without the allow, the directive is absent; `LockTimeout` 0 writes none.
  - `TestRenderDoesNotMutate`, `TestRenderDeterministic`.
- [ ] **Step 2: Run them.** Run `go test ./migrate/ -run Render`. Expected: FAIL.
- [ ] **Step 3: Implement.** Write the goldens with `-update`, and read every file: each statement must be one MySQL 8.0.19 and SQLite 3.35 accept.
- [ ] **Step 4: Run** `go test ./migrate/`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: Render for MySQL and SQLite, with SQLite's rebuild`.

### Task 10: `Render` for PostgreSQL, and `NewPlan`

**Files:**
- Create: `migrate/render_postgres.go`, `migrate/plan.go`, `migrate/plan_test.go`, `migrate/testdata/render/<case>.postgres.sql`.
- Modify: `migrate/render.go` (the PostgreSQL stub goes), `migrate/errors.go` (`BlockedError`), `migrate/render_test.go`, `migrate/doc.go`.
- Modify: `docs/history/plans/2026-10-14-core-1-followups.md` (mark :271 and :303 resolved, :131 and :333 resolved for the parts plan 1 owns; add "Left by migrate plan 1"); `docs/history/specs/2026-10-23-migrate-design.md` (a section "Rulings made in plan 1", listing this plan's rulings and those found while executing it).

**Interfaces:**
- Consumes: Task 9's framework.
- Produces: §14's Plan block, verbatim (`PlanOptions`, `Plan`, `NewPlan`), and:

```go
type BlockedError struct{ Findings []Finding }

// Error is "migrate: <n> destructive change(s) refused:", then one line per
// finding: "  [<pos>: ]<target>: <code>: <message>; allow with -allow <code>=<target>".
func (e *BlockedError) Error() string
```

- **PostgreSQL** (§8.2's PostgreSQL column, §8.3):
  - Column definitions: `"c" <type> [GENERATED BY DEFAULT AS IDENTITY (SEQUENCE NAME "s")] [NOT NULL] [DEFAULT x]`; comments are `COMMENT ON` statements after the table.
  - `CREATE SCHEMA IF NOT EXISTS "s"` before the first statement on a schema no table of `from` uses.
  - Scripts in run order: `EnumScript` (`NoTransaction`, `LockTimeout` as the main's, each `ALTER TYPE … ADD VALUE IF NOT EXISTS 'v' [BEFORE 'w']` a unit), `MainScript` (`LockTimeout`), `OnlineScript` (`NoTransaction`, no lock timeout, §8.3's units in change order). A script with no statement is left out.
  - On an existing table (one of `from`): `AddIndex`, `DropIndex`, `AddForeignKey`, `AddCheck` and `SET NOT NULL` go to the online script as §8.3's units; on a table `CreateTable` makes, they are inline in the main script.
  - A type change drops the column's default before it and sets it after (`DROP DEFAULT, TYPE … USING …, SET DEFAULT …`) when the column has one (ruling 12); into an enum, `USING "c"::text::"n"`.
  - The enum swap and adding an identity, `serial` to identity included, are §8.2's statements, with `ddl.OldEnum` and `ddl.NotNull` for the helpers.
- **NewPlan** runs `Diff`, `Lint` and `Blocked`; a non-empty `Blocked` returns `nil` and a `*BlockedError`; else `Render` with `RenderOptions{LockTimeout, Allow}` and returns the plan.

- [ ] **Step 1: Write the failing tests.**
  - `TestRenderGolden` runs PostgreSQL too: `testdata/render/<case>.postgres.sql`.
  - `TestRenderOnlineUnits`: `add-index`, `add-foreign-key` and `set-not-null` put §8.3's units in the online script, each starting with `-- velox:unit` and its `IF EXISTS` drop; the main script keeps `-- velox:lock-timeout 5s` and the online script has no lock-timeout directive.
  - `TestRenderEnumScript`: `enum-insert` is one `EnumScript` with `ALTER TYPE "users_role" ADD VALUE IF NOT EXISTS 'editor' BEFORE 'admin'`, and no main script when nothing else changes; `enum-shared`'s swap alters both tables' columns between the rename to `order_status_velox_old` and its drop.
  - `TestRenderIdentity`: `add-identity` is §8.2's two statements with an `add-identity` review line; `serial-to-identity` drops the default and the old sequence first, with `serial-to-identity`.
  - `TestRenderTableRename` (Review Focus 5): `rename-table` on each dialect is §8.2's example, statement for statement.
  - `TestRenderCreateEnumAndIndex` (Review Focus 1): in `create-tables`, `CREATE TYPE "orders_status"` and `CREATE INDEX "orders_status"` are both present.
  - `TestNewPlan`: `drop-column` without an allow is `*BlockedError` with one finding, whose text is `migrate: 1 destructive change refused:\n  users.legacy: drop-column: drops column legacy and its data; allow with -allow drop-column=users.legacy`; with `Allow{Code: "drop-column", Target: "users.legacy"}` the plan's scripts equal `Render`'s, and its `Findings` equal `Lint`'s.
  - `TestNewPlanDoesNotMutate`, `TestNewPlanDeterministic`.
- [ ] **Step 2: Run them.** Run `go test ./migrate/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Write the PostgreSQL goldens with `-update`, and read every file: each statement must be one PostgreSQL 12 accepts, in the script that may run it (`CONCURRENTLY` and `ADD VALUE` only in no-transaction scripts).
- [ ] **Step 4: Run** `go test ./migrate/ ./internal/... && go test -run TestGodocCitesNoSpec .`. Expected: PASS.
- [ ] **Step 5: Edit the documents** (the Files list). The followups' "Left by migrate plan 1" names what plan 2 owns from this plan: `FuzzSplit`, the directory goldens, writing files with `writeScript`, and the itests of every change kind.
- [ ] **Step 6: Run the full gate.** Expected: rc=0.
- [ ] **Step 7: Commit** `Migrate: PostgreSQL scripts with units, the enum swap, and NewPlan`.

---

## Rulings this plan makes

Each is a call made while writing this plan. An implementer who finds one wrong stops and reports it instead of changing it.

1. **(call) `internal/ddl` holds the cap, the helper names, the type normaliser and literal escaping.** The generator and the planner must agree on these byte for byte, `migrate` cannot import `internal/gen`, and §14 exports none of them. This is the spec's "one function in `internal/gen` that `Build` and `Tables` share" moved one package down, so `Render`'s helper names share it too.
2. **(call) Rule 44 checks three namespaces, not one** (spec defect). §6 lists every kind of name as one namespace, but its own reasons give PostgreSQL two catalogues (`pg_class`: tables, indexes, sequences, primary keys; `pg_type`: tables and types) and MySQL a third (foreign keys and checks). One namespace would refuse `index.Fields("status")` beside `field.Enum("status")`, whose index and type are both `orders_status` and coexist in every database. Rule 44 compares names, case-insensitively, within relations, types and constraints, each per database schema.
3. **(call) Rule 50 covers every table and column name, derived ones too** (spec defect). §15.2 refuses only explicit long names and §6 caps only derived index and constraint names, which leaves a long field name, a long schema name's table and an implicit foreign key or join column uncapped and unrefused: PostgreSQL would truncate them silently. Tables and columns are the names the generated code queries, so capping them would hide them; the rule refuses them and names the option that shortens each.
4. **(call) Rule 49's `Marshaler` exception covers the complex kinds only** (spec defect). A `func` or `chan` type with a `MarshalJSON` can be encoded, but its nil is no pointer, slice or map, so the core would still refuse `Nullable` on it while the plugin rule said yes: the mismatch §15.3 removes would remain. A complex type with its own encoding is a value type, which rule 48 governs like a struct. A required JSON field of a pointer type keeps `ColumnInfo.Nullable` true and a `NOT NULL` column: that flag reports the Go type's nil, not the column.
5. **(call) Every enum field with a Go type names its PostgreSQL type after the type,** `field.Enum(…).GoType(T(""))` as well as `field.EnumOf[T]`: `EnumOf` is that call, and the load model cannot tell them apart.
6. **(call) Rule 52 compares fields per (database schema, type name).** §3 makes a shared name one type per schema, so two schemas may hold two types of one name with other values.
7. **(call) A rename is an error when its source is not in `from`, its target is not in `to`, or its target is already in `from`** (spec defect). §7.1's "a target that already exists in `to`" describes every valid rename; the collision it means is with `from`.
8. **(call) `SetTableCollation` is a change kind** (spec defect). §8.1 lints `convert-collation`, and §2 models `Collation`, but §7.2 has no change for it. It is §14's only addition, rendered on MySQL as `CONVERT TO CHARACTER SET utf8mb4 COLLATE …`.
9. **(call) A PostgreSQL enum that loses or reorders values changes in two steps.** §7.3 puts enum value additions in step 3 and swaps in step 9. A swap in step 3 could not drop the old type while a column dropped in step 6 still uses it; a swap in step 9 alone would leave values a step-5 default uses missing. So the new values are appended in step 3 and the swap to the final order runs in step 9.
10. **(call) Step 7 is indexes, checks, then foreign keys, across all tables; MySQL's index changes of a table are one `ALTER TABLE … ALGORITHM=INPLACE, LOCK=NONE`, with foreign keys and checks in later statements.** MySQL adds a foreign key in place only with `foreign_key_checks` off, so it cannot share the online clause; and a key added before its index makes InnoDB create an index of its own. A step-1 drop of an index on a dropped column stays at step 1: MySQL drops a one-column index with its column, and a later `DROP INDEX` would fail.
11. **(call) Keys inside a cycle of created tables are added after them on PostgreSQL and MySQL; SQLite keeps them inline,** since it resolves a reference only when it is used, and an added key would rebuild a new table.
12. **(call) A PostgreSQL type change drops and sets the column's default around it,** as the enum swap does: PostgreSQL cannot cast a default into the new type.
13. **(call) SQLite's non-integer primary key is unnamed,** `PRIMARY KEY ("id")`. The model gives SQLite no primary-key name; §8.2's `CONSTRAINT …` has none to write.
14. **(call) MySQL writes `NULL` on a NULL-able column,** as ent does, so no column's nullability rests on MySQL's defaults.
15. **(call) Directives:** an `allow` goes into the script holding its change, so an online-only plan records its `add-unique`; the `_enum` script carries the lock timeout, as `ADD VALUE` takes a short exclusive lock on the type; the online script carries none (ruling 45).
16. **(call) `Render` calls `Lint` itself** for the review comments and the allow directives, keeping §14's signature.
17. **(call) `FuzzSplit` and the directory's goldens are plan 2's** (spec defect: the Plans list them in plan 1, but the splitter and the directory are plan 2's code). `FuzzLiteral` round-trips through `ddl.Unquote`, which plan 2's normaliser reuses. `writeScript` is plan 1's, so the goldens show the file bodies plan 2 writes.
18. **(call) `Kind` has no exported constants.** §14 lists its values only; callers switch on the change's type, and `Kind` names a change in text and file names.
19. **(call) A partial index does not count for the foreign-key index rule:** it covers some rows only, and `WHERE fk IN (…)` loads need all.
20. **(call) Positions:** a table's `Pos` is its first declared field's or edge's (an entity has none of its own); the row-limit error names the schema's file, as §16 shows.
21. **(call) The planner's tests build models from load models through `gen.Build` and `gen.Tables`,** in package `migrate_test`, editing a copy of one base model per case. Every case then runs on three dialects with the generator's own names, and no test hand-writes a derived name. The model-level apply is test code, exported to those tests by `export_test.go`.
22. **(call) SQLite's rebuild runs at its table's last change and folds every change to the table but its rename,** so a renamed table is rebuilt under its new name, as §8.2's example shows.
23. **(call) The foreign keys dropped and re-added around a type change, and the drop of a replaced index, carry no finding:** the type change's and the add's findings stand for them, as §8.1's "not part of a type change, a replacement or a rebuild" says.
24. **(call) `volatile-default` treats any default but a literal as an expression,** since `Lint` cannot know a function's volatility.
25. **(call) `MarshalJSON` is compact and sorted;** `encoding/json` compacts what it returns anyway, so plan 2's writer indents `schema.json`.
26. **(call) `Tables` reads a relation's symbols from either edge of a pair;** the `Graph` stays as `Build` makes it, so no model hash changes.
27. **(call) `CreateTime` and `UpdateTime` carry the `SchemaDefault`s,** so `Time`, which is both, has them, and so does a schema using either alone.
28. **(call) Messages keep `Build`'s style, without rule numbers;** §16's texts are examples, and the spec leaves the exact text to the builder.
29. **(call) A PostgreSQL enum `SchemaType` names a type when it is a lower-case identifier and not a built-in type,** so `SchemaType{field.Postgres: "integer"}` is refused rather than creating an enum type named `integer`.
30. **(call) Left out, for plan 2:** the directory, the runner, `Inspect`, `Drift`, the CLI and every itest; the `FuzzSplit` of ruling 17. For plan 3: baseline, the property test, the examples on the planner and the documents.
