# hooks

Typed entity hooks on `model.Task`, declared in its `Schema`. Hooks are for
validation and derived fields; there are no after-hooks, and hooks get no
`*DB`. Any error a hook returns refuses the statement.

```sh
cd example
go run ./hooks
```

```go
s.BeforeInsert(func(ctx context.Context, x *Task) error { … })                // on the struct, per row
s.BeforeUpdate(func(ctx context.Context, m *velox.Mutation[Task]) error { … }) // Update and Save
s.BeforeDelete(func(ctx context.Context, m *velox.Mutation[Task]) error { … })
```

- **Mixin hook.** `Audit`, embedded in `Task`, declares a `BeforeInsert` typed
  on `Audit` in its `Mixin` method; it fills `CreatedBy` from the context and
  runs before `Task`'s own hooks.
- **BeforeInsert** refuses a task with no title and derives `Slug`.
- **BeforeUpdate** reads the new title with `m.Value(Tasks.Title)` and adds the
  new slug with `m.Set(Tasks.Slug.Set(…))`.
- **Save writes back** every value it sent, the slug the hook set included.
- **BeforeDelete** narrows with `m.Where(Tasks.Done.EQ(true))`: even
  `Delete().All()` removes only finished tasks.
- **IsDryRun.** `SQL(ctx)` runs the hooks too. The update hook appends to an
  audit log only when `!velox.IsDryRun(ctx)`, so printing a statement records
  nothing.

The output is in `testdata/output.golden`.
