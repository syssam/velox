# policy

Row-level policy. A `Doc` is readable by its owner, by anyone once shared,
and by `admin`; only its owner (or `admin`) changes it. A `Comment` is
visible exactly when its `Doc` is. The user comes from the context.

```sh
cd examples
go run ./policy
```

`schema/schema.go` declares the entities' structure, and
`schema/doc_policy.go` and `schema/comment_policy.go` their policies, as ent's
schema does, typed with the generated entity and handles
(`entity.Docs.Owner.EQ(u)`) of `gen/entity`. `gen` re-exports them and binds
the policies, so `main.go` imports `gen` alone; a wrong signature is
refused. The user
a request acts as is in the small package `auth`:

```go
func (Doc) Policy() velox.Policy[entity.Doc] {
	return velox.Policy[entity.Doc]{
		Read:   func(ctx context.Context) (velox.Pred[entity.Doc], error) { … }, // ANDed into every read
		Write:  func(ctx context.Context) (velox.Pred[entity.Doc], error) { … }, // ANDed into every update and delete
		Insert: func(ctx context.Context, x *entity.Doc) error { … },            // checks each new row
	}
}
```

- `velox.Allow[Doc]()` says "no restriction"; a zero `velox.Pred[Doc]{}` fails the
  statement, naming the entity, so a forgotten branch never lets anyone
  through. An error denies the statement
  with a `*velox.PolicyError` that matches `velox.ErrDenied`
  (`velox.Deny(reason)` makes one).
- `Write` filters: a user's update of a doc they do not own changes no rows;
  it is not an error.
- Inheritance through `Has`: `Comment`'s `Read` is `Comments.Doc.Has()`, and
  velox applies `Doc`'s own `Read` inside that `EXISTS`.
- `SQL(ctx)` prints the conditions the policy adds for that context.
- `velox.WithoutPolicy(ctx, reason)` switches every policy off for one context,
  a scope (the `scope` package) among them.

The output is in `testdata/output.golden`.
