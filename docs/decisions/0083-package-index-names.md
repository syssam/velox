# 0083. A schema package's options: table-prefixed index names, veloxe's cap

- Status: accepted, 2026-11-19.

## Context

PostgreSQL wants index names unique in a database schema. ent names an index
by its StorageKey, or derives `<entity>_<columns>`. A mixin's index has one
StorageKey for every schema that lists it, so on two tables it is one name
twice, which velox-gen refuses (rule 44).

veloxe applications got around this with a codegen hook (nextapp's
`UniqueIndexNamesHook`): every index became `<table>_<name>`, unless the name
already started with `<table>_`. A derived name was prefixed too, so a table
`automation_execution` has `automation_execution_automationexecution_...`.
veloxe's Atlas then capped every name longer than the dialect allows with an
FNV-1 128 suffix (ent uses MD5). The largest consumer's database has 3628
index names made this way, 13 of them capped, and its audit mixin's
`ix_workspace_created_at` is on about 450 tables. Its schemas must not
change for the port, and a mixin cannot name the table it lands on, so only
velox-gen can apply the rule.

## Decision

- `schema.Package` holds a schema package's options. velox-gen reads them from
  the package's one exported variable of that type, whatever its name:

  ```go
  var Velox = schema.Package{PrefixIndexNames: true, Names: schema.VeloxeNames}
  ```

  The loader passes it to `load.MarshalPackage`, and the load model carries
  it, so generate, verify, describe and every migrate command apply the same
  options. Two such variables are refused, and so is an unexported one,
  which the export data velox-gen reads cannot show and would otherwise be
  ignored without a word.
- `PrefixIndexNames` makes each declared index `<table>_<name>` unless its
  name, StorageKey or derived, starts with `<table>_`: the hook's rule. A
  prefixed name is capped as a derived one is. Rule 50 still checks the
  StorageKey as written, and rule 44 compares the prefixed names.
- `Names: schema.VeloxeNames` caps derived and prefixed names (indexes, and
  derived foreign keys) with veloxe's FNV-1 128 suffix (`ddl.VeloxeSymbol`).
  The zero value, `EntNames`, keeps ent's MD5 cap.
- The zero `Package`, and a package without one, behave as before.

## Alternatives

- A velox-gen flag: every command and every script would need it, and one
  that forgot it would diff the whole database's indexes.
- `velox.json`: it belongs to a migration directory, and generate does not
  read it.
- A `//velox:` directive: a second syntax for package options, and invisible
  to `go vet` and the type checker; a typed variable is checked by the
  compiler.
- The port's converter writing the prefix into each StorageKey: it cannot
  reach a mixin's index, which has no one table.

## Consequences

- A veloxe application adds one file to its schema package and keeps its
  database's index names; velox-gen's names match the hook's and veloxe's cap.
- `loadjson.Version` is 7. Generated code does not change.
