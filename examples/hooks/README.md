# hooks

Entity hooks on `veloxgen.Task`. A hook is middleware over an entity's writes: it
gets the `next` step and returns a `velox.Mutator` that does its work and
calls `next` (or does not, to refuse). Hooks are for validation, derived
fields and effects of your own. Any error a hook returns refuses the
statement.

```sh
cd examples
go run ./hooks
```

`veloxschema/schema.go` declares the structure: `Task`, the `Audit` rows of the
trail, and a `CreatedBy` mixin with its own hook. `veloxschema/hooks.go` declares
`Task`'s hooks, as ent's schema does. They name columns by their schema
names (`r.Value("title")`, `m.Set("slug", …)`), so the schema does not
import `veloxgen`; velox-gen lists the mixin's and `Task`'s `Hooks` in
`veloxgen.Task`'s descriptor. The mixin's run first; of `Task`'s, the first
listed is the outermost:

```go
func (Task) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(deriveSlug, velox.OpInsert),
		velox.Before(renamed, velox.OpUpdate),
		velox.Before(onlyDone, velox.OpDelete),
	}
}
```

A hook has the shape `func(next velox.Mutator) velox.Mutator` and works on a
`*velox.Mutation`. `velox.Before(f, ops...)` makes one from a function that
runs before the write: it calls `next` when `f` returns nil.

- **`velox.On` and `velox.Before`** limit a hook to `OpInsert`, `OpUpdate`
  or `OpDelete`; the other writes skip it.
- **Insert hook.** `deriveSlug` refuses a task with no title and sets `slug`
  with `r.Set` in each row of `m.Rows()`. `Insert` and `InsertAll` run it
  once over all their rows, and it changes the caller's structs.
- **A mixin's hook.** `CreatedBy` adds `created_by` and declares `Hooks`,
  which fill it from the context's user with `m.Fill`, in each inserted row
  that leaves it empty. Every entity that lists the mixin gets the hook.
- **Update hook.** `renamed` reads the new title with `m.Value("title")` and
  adds the slug with `m.Set("slug", …)`. It sees an `Update` and a `Save`,
  and `Save` writes back every value the hook set.
- **A run-time hook, for the audit row.** The audit row is a `veloxgen.Audit`,
  which the schema cannot name, so `main.go` adds that hook when it opens
  the database: `velox.WithHooks[veloxgen.Task](velox.On(auditRename,
  velox.OpUpdate))`. Run-time hooks run after the schema's. After `next`
  succeeds, `auditRename` inserts an `Audit` row with
  `m.DB().Insert(&a).Exec(ctx)`. `m.DB()` is the `*velox.DB` the write runs
  on: inside `db.Tx` it is the transaction, so the row commits or rolls back
  with the update. Outside a transaction it is the database, and the two
  statements are separate.
- **Delete hook.** `onlyDone` narrows with
  `m.Where(velox.FieldEQ("done", true))`: even `Delete().All()` removes only
  finished tasks.
- **Conditions and counts.** `m.Predicates()` returns the conditions the caller
  and the earlier hooks gave an update or a delete, as given: never the
  policy's, and empty after `All()`. A hook that makes the write itself, as
  a soft delete does, passes them to an update instead of calling `next`,
  and reports that update's count with `m.SetAffected(n)`; the delete's
  `Exec` returns it. `m.Affected()` is the count once `next` has returned.
- **Dry runs.** `SQL(ctx)` runs the hooks too, with `next` rendering the
  statement instead of sending it. A write under that context fails with
  `velox.ErrDryRun`, so `auditRename`'s insert is refused and printing a
  statement records nothing, with no check in the hook. `SQL(ctx)` still
  returns the statement. Reads in a hook run as normal; `velox.IsDryRun(ctx)`
  tells a hook it is in one.

The output is in `testdata/output.golden`.
