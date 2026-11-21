# Contributing to velox

This page says how the repository is laid out, how to check a change, and what
a change has to keep true. Go 1.27 is required. The database tests need Docker.

## Layout

There are five Go modules, and one more that only pins a tool. A change that touches more than one is checked in
each.

- The root, `github.com/syssam/velox`: the runtime, with `schema`, `migrate`,
  `plugin`, `veloxtype` (the plain data types the root re-exports), `veloxpgx`
  (pgx's native path), `generate` (velox-gen's generate as a Go API) and
  `cmd/velox-gen`. `bench/` holds the baselines and the
  scale check. `scripts/` holds CI's scripts and their configuration. Under
  `internal/`:
  - The product: the non-generic runtime (`engine`), the executors (`conn`), the
    generator (`codegen`), velox-gen's generate run and its all-or-nothing
    writer (`genrun`), the DDL writer (`ddl`), the reader of the drivers' errors (`dberr`), the writer of the root's
    `handles.go` (`handlegen`) and the name of the module a program is built from (`buildinfo`).
  - The tests: of the public API (`apitest`), against real databases
    (`integration`) and of the repository itself (`repocheck`).
  - What the tests use: the generated models (`testmodel`, `testbind`), a fake
    executor (`fakeconn`), a repository layer for caller capture (`testrepo`),
    temporary modules (`tempmod`) and build measurement (`buildcost`).
- `contrib/graphql`: the annotations, the `velox-graphql` plugin (its answer
  in `internal/gqlplugin`, and in the process as `graphqlgen`) and the
  `veloxgql` runtime.
- `contrib/veloxotel`: OpenTelemetry tracing and metrics.
- `examples`: the runnable examples. Each has its schema, its generated code and
  its migrations.
- `bench/compare`: the harness that compares velox's build and runtime cost
  with veloxe and ent. CI checks it but does not run it. `bench/compare/veloxe`
  is a small module beside it that pins veloxe, whose `go.mod` declares this
  module's path. It is not tidied, and a test keeps it at the harness's versions.

`docs/decisions` records why the code is as it is. `docs/history` holds the specs,
plans and reviews that built it, as written. It is a historical record: nothing
depends on it, and it is not kept up to date.

velox-gen writes an entity to `<entity>.go`, as ent does, and velox-graphql
to `<entity>_gql.go`. When go build would read `<entity>.go` as a test or as
one system's, such as `ab_test.go` or `server_linux.go`, or it is `velox.go`,
velox-gen writes `<entity>_velox.go` instead (decision 0042).

## The public API

`internal/repocheck/testdata/api.txt` lists every exported name of the public
packages. If `TestPublicAPISnapshot` fails and the change is meant, regenerate it
with `go test ./internal/repocheck -run TestPublicAPISnapshot -update-api`. Read
the diff first: after 1.0, a removed line or a changed signature breaks users.
Before 1.0 nothing is released, so nothing is kept for compatibility.

## Checks

`scripts/ci-local.sh` is the local mirror of CI. It needs Docker. It has two
levels:

```sh
bash scripts/ci-local.sh          # the merge gate: run it before a local merge
bash scripts/ci-local.sh --full   # everything: run it before any push
```

golangci-lint's `gocognit` refuses a function whose cognitive complexity is
over 40, outside tests and the SQL lexers. Split such a function into helpers
that each do one job. `unparam` refuses a parameter or result no caller
needs, outside tests.

The merge gate is about the ORM. It always runs the root module's gofmt,
tidiness, vet, govulncheck and golangci-lint, the root's tests with `-race`
on PostgreSQL and MySQL, `velox-gen -check` and `verify` on the test models
and the examples, the examples' lint, tests and migrations, the root's fuzz
targets, and `scripts/scale-check.sh`, velox's own build cost. The examples'
lint covers `examples/graphql` and `examples/otel`; their tests run with their
groups.

The rest is in three groups, which the merge gate runs only when the change
touches them. The change is every file that differs from the merge base with
`main`, committed or not. `scripts/ci-changes.sh` reads it and prints the
groups:

| Group | Its lanes | Touched by |
|---|---|---|
| `graphql` | `graphql`, `examples_graphql`, `root_graphql`, `scale_graphql`, `gqlgen_latest` | `contrib/graphql/**`, `examples/graphql/**`, `scripts/gqlgen-latest.sh`, `schema/**`, `internal/tempmod/**`, `bench/scale/**`, `scripts/scale-check.sh` |
| `otel` | `veloxotel`, `examples_otel` | `contrib/veloxotel/**`, `examples/otel/**`, `internal/testmodel/**` |
| `bench` | `bench_compare`, then `scripts/bench-compare.sh` | `bench/**`, `scripts/bench-compare.sh` |

`graphql` and `otel` also run on a change to what both contrib modules
compile against: `internal/repocheck/testdata/api.txt` (an exported name),
`plugin/**`, `internal/codegen/**`, `scope/**`, `veloxpgx/**`, the root's
`go.mod` or `go.sum`, or the line that declares `GeneratedCodeIsVersionN`. A
change to the CI itself (`ci.yml`, `ci-local.sh`, `ci-changes.sh`,
`docker-compose.ci.yml`) runs all three. The header of `scripts/ci-changes.sh`
has the rules.

`--full` runs every group, and two checks the merge gate leaves out: the
root's tests with `-coverpkg=./...` and a coverage profile, in place of the
plain run, and the coverage floors after the lanes. Coverage costs the root's
tests little, but the floors need the profile of every module, so they run only
when every group does.

The script starts PostgreSQL 18 and MySQL 8.4 from
`scripts/docker-compose.ci.yml` and sets the database variables below. Then,
one after another, it runs `gofmt` and `go mod tidy -diff` in the root and in
`examples`, and downloads the root's module graph. Each other module's tidy
check runs at the start of its lane. `bench/compare/veloxe` is left out.

The rest runs in lanes, at once, as the jobs of `ci.yml` do. Each lane has a
level: `gate`, `merge` (the merge gate only), `full` (`--full` only) or a
group.

- `root_lint` (gate): `go vet`, govulncheck and golangci-lint v2.14.0 in the
  root.
- `root_test` (merge): the root's `go test -race -count=1`, but for
  `bench/scale`'s two GraphQL tests, which `root_graphql` runs.
- `root_cover` (full): the same, with `-coverpkg=./...` and a coverage profile.
- `fuzz` (gate): the root's nine fuzz targets.
- `generated` (gate): `velox-gen -check` and `velox-gen verify` on the test
  models, and revive on their generated code.
- `examples` (gate): `go vet`, govulncheck, golangci-lint, revive on the
  generated code, and the tests with a coverage profile, but for
  `examples/graphql` and `examples/otel`.
- `examples_generated` (gate): `velox-gen -check` and `velox-gen verify` on
  every example but `examples/graphql`, and the migrations replay.
- `scale` (gate): `scripts/scale-check.sh`, which runs `bench/scale` against
  `bench/scale/thresholds.json`.
- `graphql` (graphql): the tidy check, `go vet`, govulncheck, golangci-lint, the
  tests with a coverage profile and `FuzzCost`, in `contrib/graphql`.
- `examples_graphql` (graphql): `examples/graphql`'s generated files, its tests,
  and gqlgen's output, which waits for the `examples` lane, whose vet builds
  it.
- `root_graphql` (graphql): `bench/scale`'s GraphQL tests, which build
  velox-graphql's output cold, with a coverage profile of their own.
- `scale_graphql` (graphql): `scripts/scale-check.sh -graphql`.
- `gqlgen_latest` (graphql): `scripts/gqlgen-latest.sh`, which tests
  `velox-graphql` against the newest gqlgen in a copy of the tree.
- `veloxotel` (otel): the tidy check, `go vet`, govulncheck, golangci-lint and
  the tests with a coverage profile, in `contrib/veloxotel`.
- `examples_otel` (otel): `examples/otel`'s tests.
- `bench_compare` (bench): the tidy check, `go vet`, golangci-lint and
  `go test -race -short` in `bench/compare`. The measurements never run here.

After the lanes come the finals, one after another: the coverage floors
(full), and then `scripts/bench-compare.sh` (bench), which only reports. It runs
alone, so that its times compare.

Each lane writes its own log. The script prints the level and the groups it
runs, a line as each lane ends, and the whole log of the first lane that
fails; that failure stops the other lanes. The root's tests use the database
`velox`, as in CI. The other lanes with tests each use a database of their own
on the same servers (`velox_examples`, `velox_examples_graphql`,
`velox_examples_otel`, `velox_veloxotel`, `velox_graphql`), as each CI job has
its own containers. The fuzz, scale and gqlgen lanes run at a lower priority.
`VELOX_CI_JOBS=n` runs at most `n` lanes at once; `VELOX_CI_JOBS=1` runs them
one after another. In the merge gate, `VELOX_CI_GROUPS` names the groups in
place of `ci-changes.sh`: `VELOX_CI_GROUPS= bash scripts/ci-local.sh` runs the
ORM's checks alone, and `VELOX_CI_GROUPS=otel` adds `otel`'s.

CI tests PostgreSQL 14 and 18. `VELOX_CI_MATRIX=1 bash scripts/ci-local.sh`
runs the tests again on PostgreSQL 14 at the end, in every module the run's
level tests, one module at a time on `velox`. It reads the image from
`.github/workflows/ci.yml`. The root module's tests have a 40-minute timeout on
PostgreSQL 14, and 20 minutes on 18.

`ci.yml` has the same levels. A pull request is the merge gate: its `changes`
job runs `scripts/ci-changes.sh` against the pull request's base, and the
`graphql`, `otel` and `bench` jobs, and the group steps of the `test` job, run
on its outputs. A push to `main` is `--full`: every group runs, the root's
tests measure coverage, and the floors are checked.

`TestCILocalMirrorsCI` fails when the two scripts stop running the same
commands in the same directories at the same levels. `TestCIGateIsTheORM`
fails when the merge gate loses an ORM check or a contrib test joins it, and
`TestCIChangesGroups` checks `ci-changes.sh`'s rules.

The script removes its containers on exit. It does not push.

### Merging, for agents

Agents that work in parallel share the machine, so a check costs every
session. Iterate with `scripts/quick.sh`. Run the merge gate,
`bash scripts/ci-local.sh`, once, at the end, before the merge. Leave
`--full` for the push. Never run two `ci-local.sh` at once: both use the
compose project `velox-ci`.

### The quick loop

`scripts/quick.sh` is for the inner loop, not for a merge or a push. It runs `gofmt`,
`go vet` and `go test -short` in each module that has changed since `main`,
committed or not, on SQLite only, without `-race`, and with go test's cache, so
a package that did not change is not tested again. `-short` skips the tests
that build whole modules.

```sh
scripts/quick.sh          # the changed modules
scripts/quick.sh -all     # every module
scripts/quick.sh -race    # with the race detector
scripts/quick.sh -long    # without -short
```

The other modules reach the root through a `replace`: after a change to the
root's API, run it with `-all`.

## Database tests

Without a database variable, the PostgreSQL and MySQL tests skip. SQLite always
runs.

| Variable | Meaning |
|---|---|
| `VELOX_TEST_POSTGRES` | The PostgreSQL DSN. It must name a database. |
| `VELOX_TEST_MYSQL` | The MySQL DSN of the test database. |
| `VELOX_TEST_MYSQL_ADMIN` | MySQL's root DSN. The routing and migrate tests use it to make databases beside the test one. |
| `VELOX_ITEST_REQUIRE` | When it is `1`, a test run fails if a database variable it needs is unset, rather than skipping. CI and `ci-local.sh` set it. |
| `VELOX_DB` | The database DSN that `velox-gen migrate apply`, `status`, `drift`, `baseline` and `resolve` read when `-db` is not given. It is not a test variable. |
| `VELOX_CI_POSTGRES_IMAGE` | The PostgreSQL image of `scripts/docker-compose.ci.yml`. The default is PostgreSQL 18. `VELOX_CI_MATRIX=1` sets it to 14. |

To run the database tests without `ci-local.sh`, start the same databases and
export the DSNs of `scripts/docker-compose.ci.yml`:

```sh
docker compose -f scripts/docker-compose.ci.yml -p velox-ci up -d --wait
export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true'
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
export VELOX_ITEST_REQUIRE=1
go test -race -count=1 ./...
docker compose -f scripts/docker-compose.ci.yml -p velox-ci down -v
```

Do not run this while `ci-local.sh` runs: both use the compose project
`velox-ci`.

## Generated code

CI fails when a generated file is stale.

- `handles.go` comes from `go generate .` in the root. Run it, and commit the
  result. `TestHandlesFileIsCurrent` checks it.
- The test models and the examples come from `velox-gen`. After a change to the
  generator or to a schema, regenerate and commit the output:

  ```sh
  go run ./cmd/velox-gen -out internal/testmodel -package testmodel ./internal/testmodel/schema
  go run ./cmd/velox-gen -out internal/testbind -package testbind ./internal/testbind/schema
  cd examples && go tool velox-gen -out ./start/gen ./start/schema
  ```

  Do the last line for each example directory. `examples/graphql` also needs
  `-plugin velox-graphql` and `go -C graphql tool gqlgen generate`. The commands
  CI runs to check them are in `scripts/ci-local.sh`.
- velox-gen writes one package, veloxgen by default: `velox.go` and an
  `<entity>.go` per entity. veloxgen imports the schema package by its own
  name when a descriptor names a schema's value,
  `Schema: veloxschema.Issue{}`, because the schema or a mixin it lists
  declares behaviour, or reads a closure default. `velox/schema` is then
  imported as `schema`, or as `veloxschema` when the schema package is also
  named `schema`. The schema package never imports veloxgen (decisions 0039,
  0040 and 0065). A run removes the generated files it
  no longer writes, an older velox-gen's `veloxgen/runtime` among them.
- Golden files are rewritten with the test package's `-update` flag. Run it for
  one package, and read the diff before you commit it:

  ```sh
  go test ./internal/codegen -update
  ```

  The root module's packages with the flag are `internal/codegen`, `internal/ddl`,
  `migrate` and `cmd/velox-gen`. In `contrib/graphql` they are
  `internal/gogen` and `internal/sdl`. Each example's test has it too, for its
  `testdata/output.golden`: run `go test ./start -update` in `examples`.

A change to the generated code's shape replaces `GeneratedCodeIsVersionN` with
`N+1`, in a minor release, with a line in `CHANGELOG.md`.

## Coverage floors

`scripts/coverage-floors.txt` holds a floor for each package, in percent. A
floor is the coverage measured at its commit minus 1.0. `scripts/covfloor`
checks the merged profiles of the four modules against them, in
`scripts/ci-local.sh --full` and on a push to `main`:

```sh
go run ./scripts/covfloor -floors scripts/coverage-floors.txt cover-root.out cover-root-graphql.out cover-examples.out cover-examples-graphql.out cover-examples-otel.out cover-veloxotel.out cover-graphql.out
```

The profiles are the `-coverprofile` files of the test runs above. The
examples module writes three: its ORM examples, `examples/graphql` and
`examples/otel`, which run with their groups. A new
package needs a line. After you add tests, raise the floor to the measured
value minus 1.0. The checker prints that value. The file's header has the rest.

## Fuzz targets

There are ten. CI runs each for 10 or 20 seconds:

```sh
go test -run XXX -fuzz '^FuzzRender$' -fuzztime 10s ./internal/engine
```

The targets are `FuzzRender`, `FuzzRenderWrite`, `FuzzQuote`, `FuzzScope` and
`FuzzConstraint` in `internal/dberr`, `FuzzColumnScan` in `internal/conn`, `FuzzLiteral` in `internal/ddl`, `FuzzSplit` and `FuzzDiff` in
`migrate`, and `FuzzCost` in `contrib/graphql/veloxgql/internal/cost` (run from
`contrib/graphql`). `.github/workflows/ci.yml` and `scripts/ci-local.sh` list
them with their packages and times.

There is no committed corpus yet: the targets' seeds are their `f.Add` calls.
A new target needs a line in `ci.yml` and one in `ci-local.sh`. Anchor its name
with `^` and `$`. `go test -fuzz` exits 0 when its pattern matches nothing, so
`TestFuzzLinesNameRealTargets` checks each line against its package. A crash writes a file under `testdata/fuzz`. Commit that
file with the fix, so that the fix keeps a test.

## Benchmarks

`scripts/bench-compare.sh` runs the benchmarks of the root, `contrib/veloxotel`
and `examples/graphql`, and compares them with `bench/baseline` using benchstat.
It only reports: the baselines come from one machine, so read allocs/op and B/op
across machines and sec/op only on the same hardware. The budget tests enforce
allocation counts.

`scripts/bench-compare.sh -update` writes the baselines. Run it on an idle
machine, and commit the three files with the reason. `bench/baseline/README.md`
has the provenance.

`bench/scale` measures the build and link cost of a large generated model, and
`scripts/scale-check.sh` compares it with `bench/scale/thresholds.json`:

```sh
scripts/scale-check.sh -n 500 -s 100
scripts/scale-check.sh -n 500 -s 100 -graphql
```

The thresholds were measured only on darwin/arm64. Binary size and link memory differ
by platform, so going over one fails only on darwin/arm64, and elsewhere, CI's Linux
included, is a warning. A failed build fails everywhere. To gate CI, recalibrate the
file from its Linux runs, by the rule in its `_measured` notes, and drop the platform
check from the script.

`go test -run '^$' -bench '^BenchmarkScan$' -benchmem -count 6 ./internal/integration`
(with the database variables set) measures the read path on every database;
`docs/decisions/0018-pgx-stdlib-scan-wrapper.md` has its before and after.

`bash bench/compare/run.sh` measures velox, veloxe and ent. It takes hours,
starts PostgreSQL, and writes `bench/compare/results/`. The last report is
`docs/history/reviews/2026-10-29-perf-comparison.md`.

## Docs

- `TestReadmeQuickStart` runs the quick start of `README.md` in a temporary module.
  After a change to its steps, keep each block in order and each Go block's first
  line naming its file.
- Godoc is for the user of the package. It cites no spec, plan, ruling or wave
  (`TestGodocCitesNoSpec`).
- Every exported method has a doc comment (`TestExportedMethodsHaveDocs`).
  Generated-only API is marked as such (`TestGeneratedOnlyAPIIsMarked`).
- Write short declarative sentences, one claim to a sentence. A sentence about
  behaviour has a test, a run or a grep behind it.
- Wrap comments at the file's width, about 78 columns.

## Commits

Name the area, then what changed: `migrate: a PostgreSQL unique constraint is
Index.Constraint`. Stage files by name. Run the merge gate before you merge, and
`scripts/ci-local.sh --full` before you push.

## Releases

The root, `contrib/graphql` and `contrib/veloxotel` are published together, at one
version. In the tree the contrib modules reach the root through a `replace`, which Go
ignores in a dependency, so each tag must require a real version of the root.
`scripts/release.sh` does that in three steps, and only prints what to push:

```sh
scripts/release.sh root v1.2.3     # then push the tag
scripts/release.sh contrib v1.2.3  # then push the commit and the two tags
scripts/release.sh dev v1.2.3      # then push the commit
```

Before you tag, check the release on a clean tree. `-base=none` is for the first
tag, which has no earlier release to compare with. After it, `-base` names the last
release, so that the check compares the API:

```sh
go run golang.org/x/exp/cmd/gorelease@latest -base=none -version=v1.0.0
```

Nothing is released yet. The first tag is `v1.0.0`. Never tag `v0.1.0`, `v0.2.0`,
`v0.2.1` or `v0.3.0`: the Go checksum database holds another project's contents for
those versions at this path (`docs/decisions/0023-module-path.md`). `go.mod`
retracts them, so `go get` warns a module that requires one once `v1.0.0`
is published.

For the first release, the repository must first be published as `syssam/velox`,
with private vulnerability reporting turned on. Then remove the "Not released yet"
section of `README.md` and the marked paragraph of `SECURITY.md`.

## License

See the License section of README.md.
