# relations

An `Author` has many `Post`s, a `Post` belongs to its `Author` and has many
`Comment`s. Relations are `velox.Many[T]` and `velox.One[T]` fields, never
columns, and never loaded lazily.

```sh
cd example
go run ./relations
```

Keys follow a convention, so `Author.Posts` and `Post.Author` declare nothing:
a `Many` finds the key in the target's field named after the owner
(`Post.AuthorID`), and a `One` named `Author` uses the owner's `AuthorID`.
`Post.Comments` writes its key out, as you would when a field is named
otherwise:

```go
s.Many(&x.Comments).ForeignKey(func(c *Comment) *int64 { return &c.PostID })
// for a One: s.One(&x.Writer).References(&x.WriterKey)
```

Reading:

```go
db.Query[model.Author]().With(A.Posts).Limit(10).All(ctx)          // one more query: IN (...)
A.Posts.Where(P.Votes.GTE(10)).OrderBy(P.Votes.Desc()).With(P.Comments) // filtered, ordered, nested
db.Query[model.Post]().With(P.Author)                              // belongs-to
posts, loaded := author.Posts.Get()

A.Posts.Has()                       // EXISTS
A.Posts.HasWhere(P.Votes.GT(20))    // EXISTS with a condition
velox.Not(A.Posts.Has())            // authors with no posts

page = page.After(&last)            // keyset pagination; OrderBy includes the key
```

The output is in `testdata/output.golden`.
