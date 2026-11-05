# 40. The descriptor names the schema's value; conditions take ent's names

Status: accepted, 2026-11-05. Revises 0039: gen names the schema's value
instead of listing method values, and the untyped conditions are ent's
`Field*` and `HasEdge*` functions over one `velox.Predicate`.

## Context

Under 0039 each descriptor listed method values, mixins first:
`Policy: []func() velox.Policy{sch.InOrg{}.Policy, sch.Issue{}.Policy}`.
velox-gen had to know every mixin's methods, import each mixin's package,
and bind a non-zero mixin through `schema.MixinAt`. The schema package was
imported as `sch`, a name a reader has to learn.

The conditions were velox's own: `velox.C("org_id").EQ(v)` and
`velox.Edge("doc").HasWhere(...)`, of type `velox.Cond`, beside an older
`velox.Predicate` that `velox.Predicates` converted to. ent's generic
predicates are `sql.FieldEQ("org_id", v)` and its graph ones
`HasNeighbors` and `HasNeighborsWith`.

## Decision

- The descriptor names the schema's value: `Schema: schema.Issue{}`, or
  `&schema.Issue{}` for a schema whose methods take a pointer. velox reads
  the behaviour when it builds the metadata: each value `Mixin()` returns,
  in order, skipping nil, then the schema's own, each through a pointer to
  a copy, so methods of either receiver count. The policies are ANDed, the
  hooks run in order and the interceptors' conditions are ANDed, as before.
- `Descriptor.Policy`, `Hooks` and `Interceptors` are gone. The database
  schema's name, which was `Descriptor.Schema`, is `TableSchema`.
- An entity with no behaviour, its own or a mixin's, names no schema: its
  file is unchanged and imports nothing more. A `Schema` that declares
  nothing is refused, so that a string meant for `TableSchema` is not
  silently ignored.
- A method named Policy, Hooks or Interceptors, on the schema or a mixin,
  whose signature velox does not call, is the entity's `*SchemaError` at
  first use. Under 0039 the descriptor's method value failed to compile;
  `Schema` is an `any`, so the check moves to run time, and velox-gen
  still refuses the shape at its line first.
- A hand-written entity names itself: `Schema: (*E)(nil)`. A method on *E
  that the descriptor does not reach is refused, as under 0039.
- gen imports the schema package by its own name. A name the generated code
  uses (`velox`, `errors`, `fmt`, `regexp`, `time`, `json`, `utf8`, `gen`'s
  own package name) or a field type's package takes `userschema`; when the
  schema package is named `schema`, `velox/schema` is `veloxschema`. gen no
  longer imports mixin packages. `schema.MixinAt` is gone.
- Conditions take ent's names: `velox.FieldEQ`, `FieldNEQ`, `FieldGT`,
  `FieldGTE`, `FieldLT`, `FieldLTE`, `FieldIn`, `FieldNotIn`, `FieldIsNull`,
  `FieldNotNull`, `FieldContains`, `FieldHasPrefix`, `FieldHasSuffix` and
  `FieldEqualFold`, which the engine's case-folded comparison serves;
  `velox.HasEdge(name)` and `velox.HasEdgeWith(name, ps...)`. There is no
  `FieldContainsFold`: the engine has no case-insensitive substring match.
- `velox.Predicate` is `Pred[anyEntity]`, an instance of `Pred`, so `And`,
  `Or` and `Not` take it and `velox.Predicate(p)` converts a typed `Pred[E]`.
  `Cond`, `C`, `Named`, `Edge`, `NamedEdge` and `velox.Predicates` are gone;
  `Mutation.Conds` is `Mutation.Predicates`.
- The zero `Predicate` still fails the statement. A `Predicate` is a struct
  shared with `Pred[E]`: making nil possible would take a pointer or an
  interface, an allocation per condition and the loss of the conversion
  from `Pred[E]`.
- velox-gen's column check reads the `Field*` and `HasEdge*` calls and
  prints each whole call in its message.
- `GeneratedCodeIsVersion17`.

## Consequences

- velox-gen no longer needs a mixin's method set to write gen; it still
  reads it to check the mixin's shape and column names.
- A Policy method of the wrong signature is caught by velox-gen and at the
  entity's first use, not by the compiler of gen.
- Compile cost (`TestGenericTypesCompileSmall`, darwin/arm64, one
  worktree, base f0d19620 against this change): the field `Schema` is an
  `any`, where the lists named `velox.Hook`, whose `Mutation.DB` names
  `*DB`, so an entity package that uses no DB no longer describes DB's
  methods: entityDescriptor is 136078 bytes, down from 287036;
  entityDescUse 763846, down from 774634; entityDescHooks 847422, down
  from 863410; a Policy and three hooks add 83576 bytes, down from 88776.
  entityDescMixin is 892654, up from 873658: the row compiles S, M and the
  `schema` and `mixin` types they name into the measured package, where
  velox-gen's live in the schema package.
- At N=100 with a Policy per entity (0039's Review shape, three runs
  each, back to back): gen's archive is 23896614 bytes, down from
  24455668; the schema's 5008904, up from 4959122; user CPU 3.46 to
  3.59 s, against 3.54 to 3.71 s; max RSS 278 to 306 MB, against 308 to
  318 MB. Lines in gen are 13084 both.

## Evidence

- `internal/apitest`: `TestWrongBehaviourIsRefused`,
  `TestUnboundMethodsAreRefused`, `TestInterceptorZeroPredicateFailsClosed`,
  `TestZeroPredicateFails`; `interceptor_compose_test.go`'s panicking
  `Mixin()`.
- `internal/codegen`: `TestRenderNamesSchema`, `TestSchemaPackageNames`,
  `TestSchemaValueOfMixinBehaviour`, `TestLowerPackageNameClash`, the
  `TestCheckColumns*` tests, `TestWrongPolicySignatureIsRefused` (e2e) and
  the goldens.
- `cmd/velox-gen`: `TestGenerateRefusesWrongColumns` (golden
  `testdata/wrongcolumn`), `TestDiscoverPointerReceivers`.
- The examples and test models, regenerated.
