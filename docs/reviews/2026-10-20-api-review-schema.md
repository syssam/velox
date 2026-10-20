# velox2 API review: schema DSL, velox-gen, plugin, generated gen

### Summary verdict

The DSL follows ent closely, and its differences from ent are justified. velox-gen's rule errors are good: it reports every broken rule, and each error names the schema and the field or edge. Four things block 1.0:
1. The rules for nullability and pointers are muddled.
2. velox-gen is a separate module that imports `internal/`.
3. The plugin protocol and model are not ready for gRPC, and plugins' version checks never fire.
4. The schema options that migrations need are missing.

The other findings are about docs, generics and polish. All of them are cheap to fix now.

### Findings

**1. Must · nullability and pointers · breaking: yes**
- **Evidence:**
  - `lower.go:196` sets `Pointer = f.Pointer || f.Optional || f.Nillable`.
  - `build.go:104` makes any bool or number field with a `Default` a pointer.
  - In the golden output, `field.Float("score").Default(1.5)` becomes `Score *float64` with the handle `velox.NullNumberCol` on a NOT NULL column (`golden/blog/want/user_velox.go:32,51`).
  - My probe `Bool("active").Default(true)` became `*bool` with `NullCol`, so `Users.Active.SetNull()` compiles.
  - A `String` with a `Default` stays `string`. Because `insert.go:263` fills the default into any zero field, `""` can never be stored.
  - The layers disagree on `Text("bio").Nillable()`, a field that is not Optional. describe shows "nillable, pointer" and not optional, while `plugin.Field.Nullable` (Optional || Nillable) is true.
  - The docs say Nillable "makes the Go field a pointer". The package example writes `Optional().Nillable()`, which is redundant.
- **Proposed change:** one rule.
  - `Optional()` means a NULL-able column, a pointer field and a `Null*` handle.
  - `Nillable()` stays for ent ports: a no-op on an Optional field, an error on any other.
  - A `Default` never changes the Go type. velox-gen refuses a `Default` on a required bool or number field with "cannot tell false/0 from unset; make it Optional or use a DB default".
  - Document that a zero value means unset.
- This goes beyond the follow-up "Optional means a pointer"; raise its priority.

**2. Must · module layout · breaking: no**
- **Evidence:** `cmd/velox-gen/go.mod` is its own module, and `plugin.go:14` imports `velox2/internal/gen`.
- The original reason for a separate module was to keep the x/tools dependency out of the root (`core-2-generator.md:17`). That dependency is gone: the module now requires only velox2.
- Upgrading velox2 alone compiles the old tool against a changed `internal/gen`, and the build fails.
- **Proposed change:** fold `cmd/velox-gen` into the root module, as ent does. The `go get -tool` path stays the same.

**3. Must · plugin protocol 2 for gRPC · breaking: yes**
- **Evidence:** I agree with the follow-ups (no plugin options, no `.proto`, plugins cannot warn) and would raise them to Must, because gRPC ships in 1.0. Two further problems:
  - **The version check is vacuous.** `velox-graphql/main.go:49` compares `req.Protocol` with `plugin.ProtocolVersion`. velox-gen and the plugin are both built from the single velox2 version in the user's module, so the check always passes.
  - **Bare file names only.** The path check allows no directories, but gRPC needs a sub-package for its pb code.
- **Proposed change:**

```go
type Request  struct{ Protocol int; Generator string; Options map[string]string; OutPackage, OutPath string; Model *Model }
type Response struct{ Files []File; Warnings []string; Error string }
// File.Path: slash path below -out, no ".."; .go, .graphqls, .proto
func Main(s Supports, gen func(*Request) ([]File, error)) // decode, check literals, encode
type Supports struct{ Protocol, Model int }               // literals the plugin was written for
```

- On the command line this is `-plugin velox-grpc:go_package=x/pb`.

**4. Must · plugin model before it freezes · breaking: yes (ModelVersion 2, same bump as #3)**
- **Evidence:**
  - `Entity.Imports` lists velox-gen's own file imports, including the `errors` and `regexp` of its check code (`plugin.go:51`). That is an internal detail exposed as API.
  - The `TableSchema` from `TableIn` is not passed on, though Table is.
  - The model has no indexes.
  - A field has no `Size`, no literal `Default`, no `Checks` and no `SchemaType`.
  - `Edge.Kind` and `Field.Type` are untyped strings.
  - Four flags overlap: `Pointer`, `Nullable`, `Optional` and `Nillable`.
- **Proposed change:**
  - Replace `Entity.Imports` with `Field.Imports`.
  - Add `Entity.TableSchema` and `Indexes []*Index{Name, Fields, Unique, Where}`.
  - Add `Field.Size`, `Field.Default json.RawMessage`, `Field.Checks` and `Field.SchemaType`.
  - Add `type EdgeKind string` with constants.
  - After #1, keep only `Pointer` and `Nullable`.

**5. Must · options migrations need · breaking: no**
- **Evidence:**
  - There is no ON DELETE option for foreign keys anywhere.
  - `SchemaType` dialect keys are not checked, so a typo such as `"postgress"` is ignored.
  - `Other` only checks that `SchemaType` is non-empty (`build.go:654`).
  - `index.go:42` says "Only PostgreSQL has partial indexes". SQLite has them too; MySQL lacks them.
- **Proposed change:**
  - Add `edge.From(...).OnDelete(edge.Cascade|edge.SetNull|edge.Restrict)`.
  - Export dialect keys (`field.Postgres`, `field.MySQL`, `field.SQLite`) and validate them.
  - Fix the `Where` doc, and refuse `Where` when planning for MySQL.
  - Add `.SchemaDefault(map[string]string)` with the migration planner.

**6. Should · godoc · breaking: no**
- **Evidence:** every builder type is unexported (`*stringBuilder`, `*numberBuilder[T]`, `*assocBuilder`), and the options are promoted from the unexported `common[B]` and `typed[B,V]`.
- So `go doc -all ./schema/field` shows no `Optional`, `Unique`, `Default`, `Validate` or `GoType`.
- The package doc points to "section 1.1 of the schema design".
- **Proposed change:** export `field.StringBuilder`, `field.NumberBuilder[T]`, `edge.AssocBuilder` and the rest, or put a table of options in each package doc.

**7. Should · use generics in the DSL · breaking: no (additive), except a typed `Time.Default`**
- **Evidence:**
  - `DefaultFunc`, `UpdateDefault`, `Time.Default` and `UUID.Default` take `any`.
  - My probe `field.Time("t").Default(5)` compiled and failed only when velox-gen ran.
  - With `GoType(Cents(0))`, `Validate` still takes `func(int64) error`.
- **Proposed change:** add typed constructors, and keep the ent spellings for ports.

```go
field.Number[Cents]("price").Default(100).Validate(func(c Cents) error {…}) // T ~int|~int64|…
field.EnumOf[Status]("status")       // T interface{ ~string; Values() []string }
field.UUIDOf[uuid.UUID]("id").Default(uuid.New) // typed func() T
```

- Also make `Time.Default` take `func() time.Time`, as ent's does.

**8. Should · contradictions accepted silently · breaking: no**
- **Evidence:** my probes generated code from all of the following without an error:
  - `Min(10).Max(1)` and `MaxLen(10).MaxLen(5)`;
  - `Immutable().UpdateDefault(time.Now)`;
  - `Strings("tags").Unique()` and an index on that JSON column, which MySQL refuses (error 3152);
  - two identical indexes, and two indexes named `StorageKey("dup")`.
- `Bytes.MaxLen` does not set `Size`, while `String.MaxLen` does (`builders.go:131/164`).
- **Proposed change:** add a rule for each case, and make the MaxLen behaviour the same.

**9. Should · error UX · breaking: no**
- **Evidence:**
  - Builder misuse stops at the first error, because `load.Marshal` returns early.
  - The subprocess's "exit status 1" leaks into the message.
  - No error has a file:line.
- **Proposed change:**
  - Record `runtime.Caller(1)` in each constructor, and print `schema/user.go:14: User.name: …`.
  - Collect all builder errors before reporting.
  - Add "did you mean" for `Ref` and index names.

**10. Should · CLI · breaking: describe -json only**
- **Evidence:**
  - `velox-gen generate` fails with "package generate is not in std", which is a likely ent habit.
  - There is no `version`.
  - `describe -json` prints the internal, unversioned `gen.Graph`: a third JSON shape. It already disagrees with the generated code: an Optional `pw` shows no pointer but generates `*string`.
  - `verify` accepts `-check` and `-package` and ignores them.
  - Exit code 1 means both stale and broken.
- **Proposed change:**
  - Accept an explicit `generate` subcommand.
  - Add `version`.
  - Make `describe -json` print `plugin.Model`.
  - Give each subcommand its own flags.
  - Exit 3 when files are stale.

**11. Should · shrink `schema/load` · breaking: yes (no real users)**
- **Evidence:** `schema/load` exports a second versioned model that only velox-gen reads.
- **Proposed change:** export only `Marshal` and move the types into `internal/gen`.

**12. Should · generated-code contract · breaking: no**
- **Evidence:** generated code reads `(&sch.User{}).Mixin()[0].Fields()[0].Descriptor().Default.(func() time.Time)` (`user_velox.go:78`). That reaches into the fields of `schema/internal/desc.Field` through an exported method, so the struct's layout is frozen API in practice.
- **Proposed change:** document, as `protoimpl` does, that `desc`, `velox.Desc`, `ColumnDesc`, `MetaOnce`, `EdgeTo` and `Bind*` are covered only by `GeneratedCodeIsVersionN`.

**13. Should · annotation JSON · breaking: yes (affects plugins)**
- **Evidence:**
  - `TableAnnotation` and `CommentAnnotation` have no JSON tags, so plugins receive `{"Schema":…,"Table":…}`.
  - Two annotations with the same name replace each other silently.
- **Proposed change:** add lowercase JSON tags, and document module-path names for third-party annotations.

**14. Should · freeze naming · breaking: no**
- **Evidence:** I agree with the follow-up to add `plugin.Plural`, but it covers too little:
  - `snake`, `plural` and `singular` decide table, foreign-key and join-column names, which live in databases.
  - The acronym list decides Go identifiers.
- **Proposed change:** put all four in one frozen, stdlib-only package, with golden tests that list names. internal/gen and the plugins import it.
- Optionally add `schema.Handles("People")` for irregular plurals.

**15. Could · completeness compared with ent · breaking: no**
- **Edge schemas:** `edge.To(...).Through("memberships", Membership.Type)`. Make this Should if the criterion "ports after an import-path change" is meant seriously.
- **Field options:** `Deprecated(reason)`.
- **Edge options:** `StructTag`.
- **Mixin helpers:** `mixin.AnnotateFields`.
- **Index options:** per-column `Desc` and an index type.
- **Table-level options:** CHECK constraints and charset.
- **Leave out on purpose:** ValueScanner, templates and entc, and schema hooks.

**16. Could · generated-code polish · breaking: JSON output**
- `omitempty` on `time.Time` has no effect. Edges already use `omitzero`, so use it throughout.
- Enums could get `String()` and `Valid()`.
- The `// Model: sha256:` comment hashes the internal Graph (`lower.go:38`). It churns `velox.go` on velox upgrades, and `-check` already compares bytes. Drop it.

**17. Could · one way to do things**
- `Float` and `Float64` are the same type. ent has only `Float`, so drop `Float64`.

### Keep as is

- Builder names follow ent: `To`/`From`/`Ref`, inline `To(...).From(...)`, `edge.Column/Table/Columns/Symbol`, `NamedValues`, and indexes with `Fields`/`Edges`.
- Edges name their target with the `Type` method expression, which the compiler checks.
- Policies and hooks stay out of schemas, and the loader refuses a `Hooks`/`Policy` method on a schema. That helps ent ports.
- Builders record errors instead of panicking.
- Rule errors are numbered and joined, and each names its schema and field.
- velox-gen owns only files with its header, removes stale ones, and prints the rerun command.
- The protoc-style process protocol with lenient JSON.
- `Tenant()`, and `Where` as a first-class index option.
- `gen.Users.Name.EQ` handles.
- Deterministic output, golden tests with `-update`, and `GeneratedCodeIsVersionN`.

### Proposed order of work

1. Fold velox-gen into the root module (#2).
2. Settle the nullability rules (#1) across the DSL, gen, handles and velox-graphql, before the migration planner reads them.
3. Ship plugin protocol and model 2 in one bump before gRPC: #3, #4, #13, #14, and `describe -json` (#10).
4. Shrink `schema/load` (#11) and document the generated-code contract (#12).
5. Add the migration options (#5) together with the migration planner design, and `Through`.
6. Improve the DSL itself: #6, #7, #8, #9, and the rest of #10.
7. The Could items.
