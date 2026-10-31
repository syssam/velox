# Contributing to velox2

This page says how the repository is laid out, how to check a change, and what
a change has to keep true. Go 1.27 is required. The database tests need Docker.

## Layout

There are five Go modules. A change that touches more than one is checked in
each.

- The root, `github.com/syssam/velox2`: the runtime, with `schema`, `migrate`,
  `plugin`, `dbtype` (the plain data types the root re-exports) and
  `cmd/velox-gen`. `internal/` holds the non-generic runtime (`engine`), the
  generator (`codegen`), the DDL writer (`ddl`), the generated test models
  (`testmodel`, `testbind`), the database tests (`integration`), the tests of the
  public API (`apitest`) and the tests of the repository itself (`repocheck`).
  `bench/` holds the baselines and the scale check. `scripts/` holds CI's
  scripts.
- `contrib/graphql`: the annotations, the `velox-graphql` plugin and the
  `veloxgql` runtime.
- `contrib/veloxotel`: OpenTelemetry tracing and metrics.
- `examples`: the runnable examples. Each has its schema, its generated code and
  its migrations.
- `bench/compare`: the harness that compares velox2's build and runtime cost
  with old velox and ent. CI checks it but does not run it.

## Checks

`bash scripts/ci-local.sh` is the local mirror of CI. It needs Docker. Run it
before you push.

It starts PostgreSQL 18 and MySQL 8.4 from `scripts/docker-compose.ci.yml`, sets the
database variables below, and runs what CI runs, module by module, in this
order:

1. The root module: `gofmt`, `go vet`, golangci-lint v2.14.0, then
   `go test -race -count=1` with a coverage profile. Then the nine root fuzz
   targets, `velox-gen -check` and `velox-gen verify` on the test models, and
   revive on their generated code.
2. `examples`: `gofmt`, `go vet`, golangci-lint, revive on the generated code,
   then the tests with a coverage profile. Then `velox-gen -check` and
   `velox-gen verify` on every example, the migrations replay, and gqlgen's
   output for `examples/graphql`.
3. `contrib/veloxotel`: `gofmt`, `go vet`, golangci-lint, then the tests with
   a coverage profile.
4. `contrib/graphql`: `gofmt`, `go vet`, golangci-lint, the tests with a
   coverage profile, then `FuzzCost`.
5. `bench/compare`: `go vet`, golangci-lint and `go test -race -short`. The
   measurements never run here.
6. The coverage floors.
7. `bench/scale`, in plain and GraphQL form, against `bench/scale/thresholds.json`.
8. `scripts/bench-compare.sh`, which only reports.
9. `scripts/gqlgen-latest.sh`, which tests `velox-graphql` against the newest
   gqlgen in a copy of the tree.

CI tests PostgreSQL 14 and 18. `VELOX_CI_MATRIX=1 bash scripts/ci-local.sh`
runs the tests again on PostgreSQL 14 at the end, in every module. It reads the
image from `.github/workflows/ci.yml`.

The script stops at the first failure. It removes its containers on exit. It
does not push.

For a quick loop, run one module's checks by hand, as the script does:

```sh
go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
go test -race -count=1 ./...
```

Run them in the root, in `examples`, in `contrib/veloxotel` and in
`contrib/graphql`.

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
docker compose -f scripts/docker-compose.ci.yml -p velox2-ci up -d --wait
export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true'
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
export VELOX_ITEST_REQUIRE=1
go test -race -count=1 ./...
docker compose -f scripts/docker-compose.ci.yml -p velox2-ci down -v
```

Do not run this while `ci-local.sh` runs: both use the compose project
`velox2-ci`.

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
checks the merged profiles of the four modules against them:

```sh
go run ./scripts/covfloor -floors scripts/coverage-floors.txt cover-root.out cover-example.out cover-veloxotel.out cover-graphql.out
```

The profiles are the `-coverprofile` files of the test runs above. A new
package needs a line. After you add tests, raise the floor to the measured
value minus 1.0. The checker prints that value. The file's header has the rest.

## Fuzz targets

There are ten. CI runs each for 10 or 20 seconds:

```sh
go test -run XXX -fuzz '^FuzzRender$' -fuzztime 10s ./internal/engine
```

The targets are `FuzzRender`, `FuzzRenderWrite`, `FuzzQuote`, `FuzzScope` and
`FuzzConstraint` in `internal/engine`, `FuzzColumnScan` in `internal/conn`, `FuzzLiteral` in `internal/ddl`, `FuzzSplit` and `FuzzDiff` in
`migrate`, and `FuzzCost` in `contrib/graphql/veloxgql` (run from
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

`bench/scale` measures the build and link cost of a large generated model and
fails when a CPU-time ratio to a reference build passes
`bench/scale/thresholds.json`:

```sh
go run ./bench/scale -n 500 -s 100 -check bench/scale/thresholds.json
go run ./bench/scale -n 500 -s 100 -graphql -check bench/scale/thresholds.json
```

`go test -run '^$' -bench '^BenchmarkScan$' -benchmem -count 6 ./internal/integration`
(with the database variables set) measures the read path on every database;
`docs/decisions/0018-pgx-stdlib-scan-wrapper.md` has its before and after.

`bash bench/compare/run.sh` measures velox2, old velox and ent. It takes hours,
starts PostgreSQL, and writes `bench/compare/results/`. The last report is
`docs/reviews/2026-10-29-perf-comparison.md`.

## Docs

- Godoc is for the user of the package. It cites no spec, plan, ruling or wave
  (`TestGodocCitesNoSpec`).
- Every exported method has a doc comment (`TestExportedMethodsHaveDocs`).
  Generated-only API is marked as such (`TestGeneratedOnlyAPIIsMarked`).
- Write short declarative sentences, one claim to a sentence. A sentence about
  behaviour has a test, a run or a grep behind it.
- Wrap comments at the file's width, about 78 columns.

## Commits

Name the area, then what changed: `migrate: a PostgreSQL unique constraint is
Index.Constraint`. Stage files by name. Run the checks above before you push.

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
go run golang.org/x/exp/cmd/gorelease@latest -base=none -version=v0.1.0
```

## License

See the License section of README.md.
