# relations

An `Author` has many `Post`s, a `Post` belongs to its `Author` and has many
`Comment`s. Relations are `velox.Many[T]` and `velox.One[T]` fields that
velox-gen writes into the entities, never columns, and never loaded lazily.

```sh
cd example
go run ./relations
```

`schema/schema.go` declares each relation as an edge. The side that owns the
rows has `edge.To`, and the side that holds the key has `edge.From` with the
name of the `To` it mirrors. `Unique` makes it a belongs-to (a `velox.One`),
and `Field` names the column that holds the key:

```go
// Author:  edge.To("posts", Post.Type)
// Post:    edge.From("author", Author.Type).Ref("posts").Unique().Required().Field("author_id").OnDelete(edge.Cascade)
//          edge.To("comments", Comment.Type)
// Comment: edge.From("post", Post.Type).Ref("comments").Unique().Required().Field("post_id")
```

`OnDelete(edge.Cascade)` on `Post.author` records what the database does to a
post when its author is deleted: delete it. The choices are `edge.Cascade`,
`edge.SetNull` (the key must be NULL-able: the edge not `Required`, its field
`Nillable`), `edge.Restrict` and `edge.NoAction`; left unset, a NULL-able key
is SET NULL and a NOT NULL one NO ACTION, as in ent. Either edge of a relation may carry it, and two edges that both do must
agree. velox-gen records the action, and `describe` shows it as `on delete
CASCADE`; the migration planner applies it. A many-to-many edge has no foreign
key, so it refuses `OnDelete`.

The key is a field like any other (`Post.AuthorID`, `Comment.PostID`): you
set it when you insert a row that belongs to another.

Reading:

```go
db.Query[gen.Author]().With(A.Posts).Limit(10).All(ctx)          // one more query: IN (...)
A.Posts.Where(P.Votes.GTE(10)).OrderBy(P.Votes.Desc()).With(P.Comments) // filtered, ordered, nested
db.Query[gen.Post]().With(P.Author)                              // belongs-to
posts, loaded := author.Posts.Get()

A.Posts.Has()                       // EXISTS
A.Posts.HasWhere(P.Votes.GT(20))    // EXISTS with a condition
velox.Not(A.Posts.Has())            // authors with no posts

page = page.After(&last)            // keyset pagination; OrderBy includes the key
```

The output is in `testdata/output.golden`.
