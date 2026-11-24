# 0156. velox-graphql has options, not a compat mode; its defaults are entgql's

- Status: accepted, 2026-11-24. Supersedes the GraphQL parts of 0087,
  0088, 0108, 0150 and 0152. Amends 0027 and 0103 for GraphQL foreign-key
  predicates.

## Context

velox-graphql had `compat=veloxe`, which made the API of velox's
predecessor: schema-name camel case, foreign keys in the type, nullable
Optional fields, Node, lenient where inputs, every orderable field in the
order enum, a root field per type and more (0087, 0088, 0108). Later
records split `names=`, `filters=` and `node=` out of it as presets
(0150, 0152). The predecessor is private, so a setting, a type, a field or
a public doc named after it tells a reader nothing.

The user ruled that velox names no configuration, option, type, field,
value, test, file or public doc after the predecessor, and that each
choice is the best design, decided in this order:

1. The standard, where one exists: SQL semantics, the GraphQL spec and
   graphql.org's practices, the Relay specs.
2. ent and entgql, where they are the established convention.
3. Otherwise the safer or the faster design.

A behaviour an application needs stays reachable through a neutral
option.

## Decision

### Options

- `compat=` is removed, with `sdl.Config.Veloxe` and every preset. Given,
  it is refused, and the error lists the options that replace it.
- `names=go|schema`, default `go`: entgql's Go initialisms (`imageURL`),
  or the schema name's camel case (`imageUrl`).
- `filters=strict|lenient`, default `strict`, SQL's semantics: `x IN ()`
  is false, `x NOT IN ()` true, and `xIsNil: false` is `IS NOT NULL`.
  `lenient` is what `filters=veloxe` was. `gqlgraph.Type.VeloxeFilters`
  is `Type.LenientFilters`.
- Under `lenient`, a where input nested in an `or`, a `not` or a
  `has<Edge>With` that sets nothing fails with `BAD_USER_INPUT` wrapping
  the exported `veloxgql.ErrEmptyWhereInput`, as entgql's
  `ErrEmpty<T>WhereInput`; an `and` leaves such an input out.
  `errors.Is` holds on what a where input's `Predicate` and `Paginate`
  return. `DecodeWhere` only decodes, so it holds on the `Predicate` that
  follows it.
- `node=on|off`, default `off`. Auto-increment keys, velox's default,
  cannot be unique across types as the Relay spec asks. The name stays
  `node=`: entgql's `WithRelaySpec` also gates connections, which velox
  always writes.
- `idops=equality|kind`, default `kind`. `kind` gives a real foreign key
  equality alone (EQ, NEQ, In, NotIn, and IsNil and NotNil when nullable),
  as ent's enum ops, and every other field its kind's operators.
  `equality` also gives equality alone to the key, to any field named like
  a key (`id`, `*_id`, `*ID`) and to an Other field.
- `orderfields=annotated|all`, default `annotated`, entgql's
  `OrderField`: an order by an unindexed column is costly. `all` lists
  every orderable field (string but not text, time, int, int64, bool,
  enum, float), its value `OrderField`'s or the name in upper snake case;
  an `OrderField` on a type that cannot be ordered is then a warning, and
  a field with an application directive is listed too.
- `queryfields=annotated|all`, default `annotated`, entgql's
  `QueryField`.
- `optional=nonnull|nullable`, default `nonnull`. An Optional column is
  NOT NULL and always holds a value (0003), so non-null is the exact type.
  In the inputs an Optional field stays optional.
- `requirededges=nullable|nonnull`, default `nullable`, graphql.org's
  practice: a policy may hide the target, and a null in a non-null field
  nulls the parent.
- `inputkeys=edge|column`, default `edge`. An implicit foreign key's input
  field is named from its edge (`bestFriendID`) or from its column
  (`userBestFriend`). The option was added beyond the ruling's list, so
  that the full option set writes byte for byte what compat wrote.
- `jsontypes=json|named`, default `json`. `named` types a JSON field of a
  named Go type by that name (`CRMSettings`, `[TaskAssignee!]`), and the
  application declares it.
- `apptypes=` also takes the scalars compat left to the application
  (`JSON`, `UUID`, `Uint64`, `Bytes`, `Decimal`, `Map` and the like):
  velox.graphql declares none it lists. It works without `entities=`.
- `sdlfiles=one|entity` stays, default `one`. One rule names an entity's
  file: `velox_<schema name in lower case>.graphql`, such as
  `velox_salesorder.graphql`, since gqlc names a Go package after the
  file. The label rule is gone.
- `where=whitelist` stays, its doc reworded.

### Defaults

- A foreign key bound to a declared field is an `ID` in its type beside
  its edge, as in ent. Its predicates are named from the field, it stays
  when its edge is skipped, and its names follow `names=`.
- A foreign key's predicates test the column alone, as SQL and ent do,
  with no EXISTS on the target, which is also faster. This replaces
  0027's and 0103's scoping for GraphQL foreign-key predicates.
  `has<Edge>` and `has<Edge>With` still test the edge through the
  target's policies.
- An input names a foreign key as the type does: from the declared field
  when the edge binds one, else the edge's name and `ID`. Under gqlgen
  the Go field follows that name (`AuthorID`), since gqlgen binds by
  name; under graphql-go the struct tag binds, and the Go name is as it
  was. An edge list keeps `add<Edge>IDs`. A foreign key's input name
  comes from the column's camel case and ignores a `graphql.FieldName` on
  that key.
- Application directives are not copied onto input or where fields, as in
  ent. A directive declared for `FIELD_DEFINITION` alone made the copied
  SDL invalid.
- `WhereOps` on a nullable field adds IsNil and NotNil, as ent does, and
  an explicit `WhereOps` is the exact set of operators: it replaces the
  defaults.
- `RelayConnection` is honoured as entgql honours it. A to-many edge and
  a root field with it are connections; a `QueryField` without it is a
  plain list `[X!]!`, which reads at most `Limits.MaxPage` rows and fails
  past that.
- A connection's edges and node are nullable: `edges: [XEdge!]`,
  `node: X`. The Relay spec allows a nullable node, which policies need.
- An annotation with no effect, on a skipped schema or a sensitive field
  and the like, is a warning on stderr, not an error, as ent ignores it.
  Default where predicates (`WhereOps()` or `WhereInput`) on a JSON field
  that is not a scalar slice are such a warning; an explicit operator
  there is still refused.

### Runtime

- A connection always reads a msgpack cursor (entgql's family), with
  msgpack's standard time extension `-1`, so an application from ent
  keeps its clients' cursors. velox keeps writing its own.
- With one order term, the key breaks ties in that term's direction, as
  in entgql.
- A page after a cursor has `hasPreviousPage: true`, and a page before
  one `hasNextPage: true`, as in entgql.
- `gqlgraph.Type.Veloxe` is removed.
- `veloxgql.GeneratedCodeIsVersion30`, from 29.

### The ORM and the records

- The ORM's behaviour does not change. Its docs and comments say "as ent
  does" where ent agrees, or state velox's rule. Tests and files are
  renamed (`TestSymbolCap`, `zero_defaults_test.go`,
  `symbol_cap_test.go`), and the examples' migrations are
  `*_zero_defaults.sql`, rehashed. go.mod's retraction stays, its comment
  "the earlier project published at this path (decision 0023)".
- `docs/coming-from-veloxe.md` is folded into `docs/coming-from-ent.md`
  and deleted. The guide lists the options and the set that keeps the
  predecessor's GraphQL API. Public docs say "the predecessor" only where
  a fact needs it.
- ADR bodies, `docs/history` and the measured arm of `bench/compare` keep
  the name: there it is a record, or a library measured as ent is.

### The full option set

These options together write what `compat=veloxe,where=whitelist,
engine=graphql-go` wrote, byte for byte but for the version line and
comments:

```
names=schema,filters=lenient,node=on,sdlfiles=entity,orderfields=all,
queryfields=all,optional=nullable,requirededges=nonnull,jsontypes=named,
idops=equality,inputkeys=column,apptypes=JSON+UUID+Uint64,
where=whitelist,engine=graphql-go
```

`apptypes=` adds `Bytes` when the schema has no bytes field. `idops=
equality` keeps compat's equality-only rule, so no predicate is added.
Under gqlgen, the set's output differs from compat's in one place: an
input's foreign key's Go field is named from its GraphQL name, above.

### The example

examples/graphql's schema gives users, posts and labels `RelayConnection`.
Since directives no longer reach where inputs, its `email` field lost
`WhereOps`, and `@admin` moved to the `createUser` and `updateUser`
mutation fields in app.graphql, the directive declared on
`FIELD_DEFINITION` only.

## Open

- A field guarded by an application directive has where predicates that
  are not guarded. A warning was considered and not added, as it would
  change the warnings an existing schema gets.
- `graphql.FieldName` on a foreign key does not rename its input field.

## Rejected

- Keeping compat under a neutral name. A preset hides what it sets, and
  0150 and 0152 had already split three of its parts into options of
  their own.
- `idops=equality` as the default. Only a real foreign key is an ID; a
  field named like one is not, and the heuristic guessed.
- `optional=nullable` and `requirededges=nonnull` as the defaults. The
  first misstates a NOT NULL column; the second turns a hidden target into
  a null parent.

## Evidence

- `TestOldVeloxGolden` in `contrib/graphql/internal/gqlplugin`: the full
  option set against committed goldens (`testdata/oldvelox/graphqlgo`,
  `edgeschema`, `salesorder`), captured from compat before its removal.
- `TestOldVeloxAPI` in `contrib/graphql/cmd/velox-graphql`: the full set,
  generated, built and served end to end.
- `TestRun` in `contrib/graphql/internal/gqlplugin` and
  `TestCheckOptions` in `contrib/graphql/internal/sdl`: `compat=` refused
  with the list of options; each option's unknown value refused.
- `TestIDOpsEquality`, `TestIDOps`; `TestInputKeys`,
  `TestForeignKeyNames`, `TestSkippedEdgeKey`; `TestWhereCarriesNoDirectives`,
  `TestInputsCarryNoDirectives`, `TestOrderFieldUnderDirective`,
  `TestDirectiveOnKey`; `TestWhereDefaults`; `TestConnectionsDefault`,
  `TestQueryFieldsAll`; `TestEdges`; `TestAnnotationMisuse`,
  `TestResolversPlacement`, `TestNoEffectWarns`, in
  `contrib/graphql/internal/sdl` and `gogen`.
- `TestErrEmptyWhereInput` in `contrib/graphql/veloxgql/gqlgraph`:
  `or: [{}]`, `not: {}` and `or: [{}]` beside a condition, through
  `Predicate` and `Paginate`, wrap `ErrEmptyWhereInput` with
  `BAD_USER_INPUT`.
- `FuzzMsgpackCursor` and the cursor tests in `gqlcore`: msgpack cursors
  with the `-1` time extension.
- `TestOrgIsolation` in examples/graphql: nullable edges and node.
