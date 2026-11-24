# velox, for coding agents

Read this before you write or change code that uses velox. It lists what you
cannot learn by reading the code: the rules that differ from ent and from what
an ORM usually does. A person points an agent at it with the `velox` skill
(`skills/velox`, copied into the project's `.claude/skills/`), or copies this
file into the project and imports it from `CLAUDE.md` or `AGENTS.md`.

The docs ship in the module, so you can read any of them offline:

```sh
VELOX=$(go list -m -f '{{.Dir}}' github.com/syssam/velox)
```

`$VELOX/docs/guide.md` is the tour by task. Its sections are `Defining a
schema`, `Reading`, `Relations`, `Writing`, `Transactions`, `Hooks, policies
and interceptors`, `Multi-tenancy with a scope`, `Soft delete` and `Raw SQL`.
Open the one your task names. `$VELOX/docs/coming-from-ent.md` covers a schema
that came from ent. The `examples/` module is in the repository only.

## The loop

The schema package (`veloxschema/`) is yours. The generated package
(`veloxgen/`) is velox-gen's: regenerate it, never edit it.

```sh
go tool velox-gen ./veloxschema            # after every schema change
go tool velox-gen -check ./veloxschema     # stale veloxgen exits 3, writes nothing
go tool velox-gen verify ./veloxschema     # builds each entity, reports mistakes
go tool velox-gen migrate diff -dialect postgres
go tool velox-gen migrate apply -db app.db
```

Run velox-gen again after you update velox: generated code carries a version
constant, and an old `veloxgen` stops compiling.

An entity is the struct `veloxgen.User`; its typed column handles are
`veloxgen.Users`. The runtime is generic: `db.Query[veloxgen.User]()`,
`db.Insert(&u)`, `db.Update[...]()`, `db.Delete[...]()`. A wrong column or
value type is a compile error, so build before you run.

## Rules that differ from intuition

Each rule names the test that backs it.

- **A read needs `Limit` or `AllowUnbounded()`**, or it fails with
  `ErrUnbounded`. `Count` and `Exists` need neither
  (`TestReadWithoutLimitIsRefused`).
- **An update or delete needs `Where` or `All()`**, or it fails with
  `ErrNoWhere` (`TestWriteWithoutWhereIsRefused`).
- **`Optional()` stays NOT NULL.** It only means the field may be left out on
  create; left out, it is stored as its Go zero, `""`, `0` or `false`
  (`TestOptionalValueField`). `Nillable()` makes the column NULL and the Go
  field a pointer (`TestBuildNillableIsPointer`). velox-gen refuses an
  `Optional` enum with neither `Default` nor `Nillable`, as `""` is none of
  its values: write `Optional().Default("calm")` or `Optional().Nillable()`
  (`TestOptionalNeedsAValue`).
- **A struct's zero field takes the `Default`.** `field.Bool("active").Default(true)`
  stores `true` for a struct's `false`; store `false` with a setter,
  `db.Insert(&x).Set(Xs.Active.Set(false))`, or a hook's `Mutation.Set`. The
  insert fills defaults before setters and hooks, as ent does
  (`TestDefaultUnderExplicitZero`).
- **IDs are `int64`** (`TestBuildBlog`). A foreign-key field has its target's
  ID type (`TestTablesForeignKeyCopiesType`).
- **A Write policy filters.** An update aimed at rows the caller may not change
  affects 0 rows and returns no error, so check the count
  (`TestWriteFiltersAffectZero`).
- **Behaviour in the schema package names columns as strings**:
  `velox.FieldEQ("status", s)`, `m.Value("title")`. The schema package never
  imports `veloxgen`. velox-gen checks the names when it generates
  (`TestGenerateRefusesWrongColumns`).
- **`db.WithoutPolicy("reason")` lifts policies, not interceptors.** Soft delete
  and similar filters still apply (`TestWithoutPolicyKeepsInterceptors`,
  `TestWithoutPolicyKeepsSoftDelete`).
- **Inside `db.Tx`, use the `tx` and the `ctx` you were given.** A statement on
  the outer `db` under that `ctx` joins the transaction; one on another DB
  fails with `ErrOutsideTx` (`TestStatementOutsideItsTx`,
  `TestBeginnerJoinsItsTx`).
- **SQLite has a pool of one connection.** Finish an `Iter` loop before you
  start another statement (`TestIterNestedStatementFails`).
- **A MySQL DSN needs `parseTime=true` and `clientFoundRows=true`**
  (`TestConnectRefuses`).

## Errors

velox's guard errors name the fix, for example `add Limit, or AllowUnbounded to
read every row`. Apply that fix, and match with `errors.Is(err, velox.ErrX)`,
never by message.

## Before you say it works

Run `go build ./...`, `go vet ./...` and `go test ./...`, and in CI
`go tool velox-gen -check ./veloxschema`. PostgreSQL and MySQL tests need their
own DSNs; say so when you only ran SQLite.
