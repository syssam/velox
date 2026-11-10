# start

Create, read, update and delete on one entity, `veloxgen.User`. Start here.

```sh
cd examples
go run ./start
```

`veloxschema/user.go` is the whole schema: a struct that embeds `schema.Schema` and
lists the entity's fields. `go tool velox-gen -out ./start/gen ./start/schema`
writes the `veloxgen` package from it: `veloxgen.User`, the struct a query returns, and
`veloxgen.Users`, the typed handle of every column.

```go
U := veloxgen.Users

db.Insert(&ada).Exec(ctx)                        // reads the new ID into ada
db.InsertAll(users).Exec(ctx)                    // one statement, no IDs read back

db.Query[veloxgen.User]().Where(U.Age.GT(30)).OrderBy(U.Age.Desc()).Limit(10).All(ctx)
db.Query[veloxgen.User]().Where(U.Email.EQ(e)).One(ctx)   // velox.ErrNotFound for no row: velox.IsNotFound(err)
db.Query[veloxgen.User]().Where(U.Name.HasPrefix("B")).Count(ctx)
for u, err := range db.Query[veloxgen.User]().AllowUnbounded().Iter(ctx) { … }

db.Update[veloxgen.User]().Set(U.Age.Add(1)).Where(U.Name.In("Ada", "Linus")).Exec(ctx)
db.Save(grace, U.Email).Exec(ctx)               // the named columns of a loaded struct
db.Delete[veloxgen.User]().Where(U.Age.LT(30)).Exec(ctx)
```

Guards are always on: a query with no `Limit` fails with `velox.ErrUnbounded`
unless it says `AllowUnbounded()`, and an update or delete with no `Where` fails
with `velox.ErrNoWhere` unless it says `All()`.

The table comes from `migrations/`. `go tool velox-gen migrate diff -dialect
sqlite -name init -dir ./start/migrations ./start/schema` wrote it, and
`main.go` applies it with `migrate.Up` to a temporary SQLite file. After a
schema change, run `migrate diff` again without `-name init`, and regenerate.

The output is in `testdata/output.golden`.
