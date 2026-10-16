# policy

Row-level policy. A `Doc` is readable by its owner, by anyone once shared,
and by `admin`; only its owner (or `admin`) changes it. A `Comment` is
visible exactly when its `Doc` is. The user comes from the context.

```sh
cd example
go run ./policy
```

`schema/schema.go` declares the entities' structure only. The policy is a
method you write on the generated entity, in `gen/doc_policy.go`, typed with
the generated handles (`Docs.Owner.EQ(u)`). velox-gen sees the method and binds
it into `Doc`'s metadata; a wrong signature does not compile:

```go
func (*Doc) Policy() velox.Policy[Doc] {
	return velox.Policy[Doc]{
		Read:   func(ctx context.Context) (velox.Pred[Doc], error) { … }, // ANDed into every read
		Write:  func(ctx context.Context) (velox.Pred[Doc], error) { … }, // ANDed into every update and delete
		Insert: func(ctx context.Context, x *Doc) error { … },            // checks each new row
	}
}
```

- A zero `velox.Pred[Doc]{}` restricts nothing; an error denies the statement
  with a `*velox.PolicyError` that matches `velox.ErrDenied`
  (`velox.Deny(reason)` makes one).
- `Write` filters: a user's update of a doc they do not own changes no rows;
  it is not an error.
- Inheritance through `Has`: `Comment`'s `Read` is `Comments.Doc.Has()`, and
  velox applies `Doc`'s own `Read` inside that `EXISTS`.
- `SQL(ctx)` prints the conditions the policy adds for that context.
- `velox.SkipPolicy(ctx, reason)` switches policies off for one context. It
  does not lift tenant scoping.

The output is in `testdata/output.golden`.
