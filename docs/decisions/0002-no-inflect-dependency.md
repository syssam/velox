# 0002. Inflection is copied, not imported

- Status: accepted, 2026-10-29

## Context

ent pluralizes with github.com/go-openapi/inflect v0.19.0. Importing it would
let the user's module graph select another version, and a different plural
renames a table. The plugin package is what plugins import, and it should cost
them nothing else.

## Decision

`Plural` and `Singular` use inflect v0.19.0's default rule tables, copied into
the plugin package (plugin/inflect.go) with that package's MIT notice. The
plugin package imports only the standard library. The rule set is built on
first use. The outputs of `Plural`, `Singular` and the other naming helpers
never change within a major version, for any input; helpers may be added.

## Consequences

The plural of a name is the same whatever the user's module graph holds, and
is ent's. A rule fix upstream does not reach velox until a major version.
velox has no go.mod line for inflect.

## Evidence

- `Plural`'s doc (plugin/names.go): "Its outputs never change: a change would
  rename tables in existing databases."
- plugin/doc.go, "# Naming": frozen within a major version; the goldens
  plugin/testdata/names.golden and plugin/testdata/ent.golden pin them
  (`TestNamesGolden`, plugin/names_test.go:35; `TestRuleCounts`,
  plugin/inflect_test.go:7).
- `go list -deps ./plugin` lists no package outside the standard library but
  the plugin package itself; `git grep inflect -- '*go.mod'` prints nothing.
- Builder ruling 3, docs/specs/2026-10-25-pre-1.0-hardening-design.md:152.
