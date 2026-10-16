# hooks

Typed entity hooks on `gen.Task`. A hook is middleware over an entity's
writes: it gets the `next` step and returns a `velox.Mutator` that does its
work and calls `next` (or does not, to refuse). Hooks are for validation,
derived fields and effects of your own. Any error a hook returns refuses the
statement.

```sh
cd example
go run ./hooks
```

`schema/schema.go` declares the structure only: `Task`, the `Audit` rows its
update hook writes, and a `CreatedBy` mixin. The hooks are a method you write
on the generated entity, in `gen/task_hooks.go`, typed with the generated
handles. velox-gen sees the method and binds it into `Task`'s metadata; a
wrong signature does not compile. The first hook listed is the outermost:

```go
func (*Task) Hooks() []velox.Hook[Task] {
	return []velox.Hook[Task]{
		velox.On(createdBy(func(t *Task) *string { return &t.CreatedBy }), velox.OpInsert),
		velox.On(deriveSlug, velox.OpInsert),
		velox.On(renamed, velox.OpUpdate),
		velox.On(onlyDone, velox.OpDelete),
	}
}
```

Each hook has the shape `func(next velox.Mutator[Task]) velox.Mutator[Task]`
and works on a `*velox.Mutation[Task]`.

- **`velox.On`** limits a hook to `OpInsert`, `OpUpdate` or `OpDelete`; the
  other writes skip it.
- **Insert hook.** `deriveSlug` refuses a task with no title and sets `Slug`
  in each row of `m.Rows()`. `Insert` and `InsertAll` run it once over all
  their rows, and it changes the caller's structs.
- **A reusable hook is a generic function.** `createdBy` in
  `gen/created_by.go` fills a column from the context's user, for any entity
  that passes it a way to reach the column. A mixin carries fields, never
  behaviour, so the `CreatedBy` mixin only adds the `created_by` column, and
  each entity lists the hook in its own `Hooks`.
- **Update hook.** `renamed` reads the new title with `m.Value(Tasks.Title)`
  and adds the slug with `m.Set(Tasks.Slug.Set(…))`. It sees an `Update` and a
  `Save`, and `Save` writes back every value the hook set.
- **Audit row on the write's DB.** After `next` succeeds, `renamed`
  inserts an `Audit` row with `m.DB().Insert(&audit).Exec(ctx)`. `m.DB()` is
  the `*velox.DB` the write runs on: inside `db.Tx` it is the transaction, so
  the row commits or rolls back with the update. Outside a transaction it is
  the database, and the two statements are separate. A hook gets it only on
  an entity that has hooks.
- **Delete hook.** `onlyDone` narrows with `m.Where(Tasks.Done.EQ(true))`: even
  `Delete().All()` removes only finished tasks.
- **IsDryRun.** `SQL(ctx)` runs the hooks too, with `next` rendering the
  statement instead of sending it. `renamed` writes its audit row only when
  `!velox.IsDryRun(ctx)`, so printing a statement records nothing.

The output is in `testdata/output.golden`.
