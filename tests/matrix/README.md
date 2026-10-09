# Schema-shape matrix

One generated project that covers many schema shapes at once, with a test per
shape asserting its **runtime behaviour**. The generator's own tests mostly
assert on rendered strings; the bugs this module exists for only show once a
project is generated, compiled *and* run (a schema whose only runtime code is
`Policy()` silently having no policy, nillable bytes aliasing between rows,
shapes whose generated code does not compile).

It is its own module (`example.com/matrix`, `replace github.com/syssam/velox => ../..`),
a sibling of `tests/external-module`.

| Path | What |
|---|---|
| `schema/` | the shapes — one small entity (or a few) each |
| `types/` | custom Go types bound with `GoType()`, deliberately outside `schema/` |
| `narrow/` | application-style code that never imports the generated root package |
| `velox/` | generated output, gitignored |
| `matrix_test.go`, `matrix_data_test.go`, `matrix_edges_test.go` | one behaviour test per shape |
| `known_gaps_test.go` | shapes known to be broken, pinned as broken |

## Regenerate

```bash
go run generate.go
```

`velox/` is gitignored, so it must be generated before the first `go build`,
`go vet` or `go test` in a fresh checkout, and again after any generator
change. Never run `go mod tidy` here: `generate.go` is `//go:build ignore`,
so tidy prunes the generator's dependencies. Use `go get <module>@<version>`.

## Run

```bash
go test ./...          # everything, ~15s
go test -short ./...   # skips the known-gap cases that spawn go run + go build, ~1s
go test -race ./...
```

Tests use in-memory SQLite, one fresh database per test, and do not depend on
each other.

## Add a shape

1. Add a small entity to `schema/` (custom types go in `types/`).
2. `go run generate.go && go build ./...`.
3. Add a test named after the shape. It must **fail if the feature silently
   does nothing**: assert what the hook wrote, that the denied call returned
   `privacy.Deny`, that the validator rejected the value on Create *and*
   Update — not that a value was stored or that the code compiles.

## The rule for generator bugs

A generator bug found here is **pinned, not worked around and not fixed in
this module**:

- If the shape does not generate or compile, take it out of `schema/` and add
  a case to `gapCases` in `known_gaps_test.go` with the outcome it has today
  (`generateFails`, `generatorPanics`, `buildFails`, plus a substring of the
  error) and a one-line comment saying what correct behaviour would be.
- If it compiles and misbehaves at run time, add a `TestKnownGap_*` test that
  asserts the wrong behaviour.

Either way the pin fails the day the generator is fixed, and its message says
what to do then: move the shape into the positive matrix and delete the pin.
Updating an expected outcome from one broken state to another needs a reason.

Names the generator rejects on purpose with a clear error (`Schema`,
`Client`, `Config`, `Hook`, `Mutation`, `Op`, `Option`, `Query`, `Value`,
`Driver`, `Type`, `Error`) are not gaps and are not pinned.
