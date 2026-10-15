# start

Create, read, update and delete on one entity, `model.User`. Start here.

```sh
cd example
go run ./start
```

`model/user.go` is the whole schema: a plain struct and a `Schema` method.
`go tool velox-gen ./...` writes `model/user_velox.go` beside it, which holds
`model.Users`, the typed handle of every column.

```go
U := model.Users

db.Insert(&ada).Exec(ctx)                        // reads the new ID into ada
db.InsertAll(users).Exec(ctx)                    // one statement, no IDs read back

db.Query[model.User]().Where(U.Age.GT(30)).OrderBy(U.Age.Desc()).Limit(10).All(ctx)
db.Query[model.User]().Where(U.Email.EQ(e)).One(ctx)   // velox.ErrNotFound for no row
db.Query[model.User]().Where(U.Name.HasPrefix("B")).Count(ctx)
for u, err := range db.Query[model.User]().Unbounded().Iter(ctx) { … }

db.Update[model.User]().Set(U.Age.Add(1)).Where(U.Name.In("Ada", "Linus")).Exec(ctx)
db.Save(ctx, grace, U.Email)                     // the named columns of a loaded struct
db.Delete[model.User]().Where(U.Age.LT(30)).Exec(ctx)
```

Guards are always on: a query with no `Limit` fails with `velox.ErrUnbounded`
unless it says `Unbounded()`, and an update or delete with no `Where` fails
with `velox.ErrNoWhere` unless it says `All()`.

The output is in `testdata/output.golden`.
