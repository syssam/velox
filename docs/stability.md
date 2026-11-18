# API stability

This page says what you can rely on when you build on velox, today and after
the first release. It summarizes the decision records that set the rules,
mainly [0004 Versioning](decisions/0004-versioning.md) and
[0005 Deprecation](decisions/0005-deprecation.md). Where this page and a
record disagree, the record wins. Where the records say nothing, this page
says so rather than guess.

## Today: before 1.0, anything can change

Nothing has been released from this repository yet. The first tag will be
`v1.0.0` ([0023](decisions/0023-module-path.md)). Until then:

- **Any API can change or disappear**, in the root package, the `schema`
  packages, `migrate`, `contrib/graphql` and `contrib/veloxotel` alike.
- **Nothing is deprecated first.** An identifier is changed or removed
  outright, without a `// Deprecated:` period
  ([0005](decisions/0005-deprecation.md)). An upgrade may not compile.
- **The changelog tells you what to do.** [CHANGELOG.md](../CHANGELOG.md)
  lists every change under "Unreleased", and its "Changed" and "Removed"
  lists say what to change in your code.

If you try velox before 1.0, pin a commit and read the changelog when you
move to a newer one.

## What is already guarded

A few rules are enforced today, so that changes are deliberate and visible
even before 1.0.

### The public API is snapshotted

`internal/repocheck/testdata/api.txt` lists the exported API of every public
package, including the members of the root's aliases of engine types.
`TestPublicAPISnapshot` fails on any difference, so a change to the API is a
diff that someone reviews, and an accidental change fails CI
([0024](decisions/0024-public-surface.md)). This makes changes visible; it
does not stop them before 1.0.

### Generated code is versioned

Every file velox-gen writes refers to a constant, today
`velox.GeneratedCodeIsVersion21` (and `veloxgql.GeneratedCodeIsVersion23` for
velox-graphql's files). When the shape of generated code must change, the
constant is replaced by the next number, with a line in the changelog. Code
generated for the old version then fails to compile against the new runtime,
instead of misbehaving at run time. The fix is always the same: run velox-gen
again ([0004](decisions/0004-versioning.md)). The old constant is not kept
beside the new one.

The runtime identifiers that only generated code should use are documented
"For generated code." (the "Generated code" section of the root package's
godoc lists them). They follow the version constant, not the rest of the API.

### Migration files stay readable

A migrations directory holds two versioned files, `schema.json` and
`velox.json`, both at version 1. A newer velox reads every older version, and
refuses a newer one with an error that names both versions
([0004](decisions/0004-versioning.md)). A directory written by an older velox
keeps working.

### Plugins check their protocol

velox-gen and a plugin such as velox-graphql exchange JSON whose shape has two
version numbers, `plugin.ProtocolVersion` and `plugin.ModelVersion`. A
velox-gen and a plugin with different protocols refuse each other, and the
model's number changes only when a field is removed or changes meaning
([0004](decisions/0004-versioning.md)).

### Database names follow ent

Table, column, index and constraint names follow ent's rules, so an ent
database can be adopted without renames
([0001](decisions/0001-ent-names.md)). The naming helpers (`Plural`,
`Singular` and the rest) never change their output within a major version
([0002](decisions/0002-no-inflect-dependency.md)), so regenerating does not
rename your tables.

### Design decisions are written down

[docs/decisions/](decisions/README.md) records why velox behaves as it does,
and where each decision is enforced: a test, the code, or the spec it came
from. When a later decision changes an earlier one, the index marks the
earlier record as revised or superseded.

## From 1.0

When `v1.0.0` is tagged, the root module, `contrib/graphql` and
`contrib/veloxotel` are released together, at one version. From then on:

- **Semantic versioning.** velox follows semantic versioning
  ([0004](decisions/0004-versioning.md)): no breaking change to the public
  API within a major version.
- **Except generated code's API.** The identifiers marked "For generated
  code." change only when the generated-code constant is replaced. That
  happens in a minor release, with a changelog line, and asks you to run
  velox-gen again. It never asks you to change code you wrote.
- **Deprecation before removal.** A deprecated identifier carries a
  `// Deprecated:` comment that names its replacement, and stays through
  every release of its major version
  ([0005](decisions/0005-deprecation.md)).
- **The plugin protocol, the plugin model and the migration files** keep the
  rules above.
- **Releases are checked.** Before a tag, `gorelease` compares the API with
  the last release ([CONTRIBUTING.md](../CONTRIBUTING.md#releases)).

## What the records do not cover

Some questions have no recorded answer yet. Until a decision record settles
them, treat them as unpromised:

- **The SQL velox sends.** No record promises that the text of a rendered
  statement stays the same between releases, so treat `Query.SQL(ctx)`
  output as a debugging aid, not as something to compare in your own
  tests.
- **Error text.** Errors start with a stable prefix (`velox: <Entity>: `,
  documented in the root package's godoc), but the rest of the message is not
  promised. Test errors with `errors.Is`, `errors.As` and `velox.IsNotFound`,
  never by string.
- **The output of a migration diff.** The records promise that old migration
  directories stay readable. They do not promise that a newer velox plans the
  same SQL for the same schema change.
- **Supported Go and database versions.** velox needs Go 1.27, and its
  migrations need PostgreSQL 14, MySQL 8.4 or SQLite 3.35
  ([0013](decisions/0013-version-floors.md)). No record says whether raising
  one of these floors counts as a breaking change.
- **Performance.** The build cost of a large generated model is checked
  against thresholds (`bench/scale`), allocation counts by budget tests, and
  benchmarks are compared with a baseline (`bench/baseline`), as
  [CONTRIBUTING.md](../CONTRIBUTING.md#benchmarks) describes. No record
  promises a number to users.

Packages under `internal/` are not part of the API; Go does not let you
import them.
