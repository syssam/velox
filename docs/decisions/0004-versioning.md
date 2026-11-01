# 0004. Versioning

- Status: accepted, 2026-10-29

## Context

velox has four version numbers besides its release: the generated-code
constants of velox and veloxgql, the plugin protocol and model, and the two
files a migration directory holds. Each needs a rule a user can plan around.

## Decision

- From 1.0, velox follows semantic versioning, except the API for generated
  code: the identifiers whose doc says "For generated code." (doc.go,
  "# Generated code"). They change only when the constant changes.
- A change to the generated code replaces `velox.GeneratedCodeIsVersionN` (and
  `veloxgql.GeneratedCodeIsVersionN`) with the next number, in a minor release,
  with a line in CHANGELOG.md. Regenerate with the new `velox-gen`: code that
  names the old constant does not compile against the new runtime. The old
  constant is not kept beside the new one: a bump means old generated code
  could not run correctly.
- The plugin protocol (`plugin.ProtocolVersion`, 2) and model
  (`plugin.ModelVersion`, 2) follow the same rule. velox-gen and a plugin of
  different protocols refuse each other; the model's number changes only when
  a field is removed or changes meaning.
- `schema.json` and `velox.json` are both version 1. A newer velox reads every
  older version and refuses a newer one, with an error that names both
  versions.

## Consequences

Updating velox may need `velox-gen` run again, and only a minor release asks
for it. A migration directory written by an older velox stays readable.

## Evidence

- `GeneratedCodeIsVersion6` (doc.go) and `GeneratedCodeIsVersion4`
  (contrib/graphql/veloxgql/doc.go); each doc names its replacement.
- `TestVersions` (plugin/main_test.go:47), `TestRunChecksLiterals`
  (plugin/main_test.go), and velox-gen's refusal in cmd/velox-gen/plugin.go.
- `snapshotVersion` (migrate/json.go) and `TestSnapshotRefusesVersion`
  (migrate/json_test.go:137); `parseConfig` (migrate/dir.go) refuses
  "version 2; this velox reads version 1", and `TestConfig`
  (migrate/dir_test.go:74) pins the whole text: "migrate: velox.json:
  version 2; this velox reads version 1".
- Controller rulings 5 and 6, docs/history/plans/2026-10-29-hardening-6-docs.md.
