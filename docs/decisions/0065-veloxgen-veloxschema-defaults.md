# 0065. The default schema package is veloxschema and the generated one veloxgen

- Status: accepted, 2026-11-10. Amends [0037](0037-ent-layout.md) and
  [0042](0042-plain-file-names.md) where they name the generated package `gen`
  and the schema package `schema`.

## Context

The generated package was `gen` and the schema package `schema`. Both names
clash with others a program holds:

- Many generators write a package called `gen`, and a program that imports two
  needs an alias for one.
- A schema package named `schema` has the name of velox's own `schema`
  package, which every schema file imports. A generated file that names both
  imported velox's as `veloxschema` to avoid the clash (`pick` in
  internal/codegen/lower.go).

The Go team's guidance is that a package's name says what it provides and is
unlikely to clash, and that protobuf names its packages for their contents
(`foopb`), not for the tool.

## Decision

`velox-gen` with no flags reads `./veloxschema` and writes `./veloxgen`, a
package named `veloxgen`. `-out` names another folder and `-package` another
package name, as before; the package name still defaults to the folder's base
name. There is no configuration file: the folder is set once in the
`go:generate` line.

The examples use the defaults, and CI and `scripts/ci-local.sh` name them in
the same commands. The generated code's shape does not change, so
`GeneratedCodeIsVersionN` stays; only the package clause and the qualifier of
a runtime error that names an entity (`veloxgen.Task`) change.

When the schema package is `veloxschema`, a generated file imports velox's
`schema` under that name; the `veloxschema` alias is kept for a schema package
named `schema`, which `-out` and a path still allow.

Not changed: the root's `schema` package, the packages of velox's own tests
(`internal/testmodel`, `internal/testbind`, which pass `-out` and `-package`),
the benchmark harnesses, which name their layout in each command, and the
accepted decisions above, which record the names they were made under.

## Consequences

An app that used the old defaults passes `-out ./gen ./schema` or renames its
folders and imports; the import path and the identifiers (`gen.User` to
`veloxgen.User`) change in every file that names the package.

## Evidence

- `TestGenerateDefaults` (cmd/velox-gen/generate_test.go): a run with no flags
  writes `veloxgen/` with `package veloxgen`.
- `TestReadmeQuickStart` (internal/repocheck): the README's steps, with
  `veloxschema/user.go` and `go tool velox-gen ./veloxschema`, build and run.
- Each example's test and `velox-gen -check` on `./veloxgen` pass; the output
  goldens differ from the old only in `gen.` becoming `veloxgen.`.
