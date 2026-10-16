# start

Create, read, update and delete on one entity, `gen.User`. Start here.

```sh
cd example
go run ./start
```

`schema/user.go` is the whole schema: a struct that embeds `schema.Schema` and
lists the entity's fields. `go tool velox-gen -out ./start/gen ./start/schema`
writes the `gen` package from it: `gen.User`, the struct a query returns, and
`gen.Users`, the typed handle of every column.

```go
U := gen.Users

db.Insert(&ada).Exec(ctx)                        // reads the new ID into ada
db.InsertAll(users).Exec(ctx)                    // one statement, no IDs read back

db.Query[gen.User]().Where(U.Age.GT(30)).OrderBy(U.Age.Desc()).Limit(10).All(ctx)
db.Query[gen.User]().Where(U.Email.EQ(e)).One(ctx)   // velox.ErrNotFound for no row
db.Query[gen.User]().Where(U.Name.HasPrefix("B")).Count(ctx)
for u, err := range db.Query[gen.User]().Unbounded().Iter(ctx) { … }

db.Update[gen.User]().Set(U.Age.Add(1)).Where(U.Name.In("Ada", "Linus")).Exec(ctx)
db.Save(ctx, grace, U.Email)                     // the named columns of a loaded struct
db.Delete[gen.User]().Where(U.Age.LT(30)).Exec(ctx)
```

Guards are always on: a query with no `Limit` fails with `velox.ErrUnbounded`
unless it says `Unbounded()`, and an update or delete with no `Where` fails
with `velox.ErrNoWhere` unless it says `All()`.

The output is in `testdata/output.golden`.
