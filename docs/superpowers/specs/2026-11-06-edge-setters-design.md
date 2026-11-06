# Edge setters and GraphQL input setters

Date: 2026-11-06. Status: approved by the user ("make sure clean").

## Why

GraphQL inputs run their writes themselves: `input.Create(ctx, x)` and
`input.Update(ctx, id, set...)` decode, check, insert or update, and write
edges, through gqlimpl's `Create`, `Update`, `Key`, `Exists`, `Join`,
`Repoint`, `Children`, `Child` and `Through`. The user finds the generated
code and the call hard to read, and wants to control the write. ent writes
edges as part of a mutation (`AddTagIDs`, `Save`); entgql's input only moves
its fields onto that mutation.

## What changes

### Core: a relation handle makes setters

`velox.Rel[E, T]` gains setters, as a column handle has `Set`:

```go
gen.Posts.Tags.Add(1, 2)  // Setter[Post]
gen.Posts.Tags.Remove(3)
gen.Posts.Tags.Clear()
```

The keys are T's primary-key values, converted as a predicate's value is.
They apply to an edge whose rows are not the owner's own: a many-to-many edge
(`EdgeThrough`) or one whose foreign key the target holds (`EdgeTo` with the
owner's key as the owner column). An edge whose key is on the owner's row is
set by its column handle; its Add fails the statement.

`Setter.Column()` of an edge setter is -1.

### Core: Insert and Update take them

- `db.Insert(x).Set(ss...)` applies column setters to x before the insert (a
  setter wins over x's field; Add and Sub fail) and writes the edge setters
  after it, with x's key. `InsertAll` takes them for every row.
- `db.Update[E]().Set(ss...)` takes edge setters beside column ones. An
  update of only edge setters is allowed. The edges are written for each row
  the update matches: the update first reads their keys, under the Read
  policy and the update's conditions, as ent reads the IDs of its nodes.
- A write with edge setters runs in one transaction, `db.Tx`, a savepoint
  inside one.
- Order, for each owner: Clear, then Remove, then Add.

### Core: edge semantics (moved from gqlimpl, unchanged)

Many-to-many, through J:
- Add checks that every key names a row of T the caller may read; one that
  does not fails the write with a `*velox.EdgeError` (Edge, Keys, Err
  `ErrNotFound`). It inserts the join rows ON CONFLICT DO NOTHING.
- Remove and Clear delete only the join rows to targets the caller may read;
  a link to a hidden target stays. Clear reads the links in pages.

Foreign key on T (one-to-many, or a one-to-one T holds):
- Add re-points the children whose key is NULL. A child another owner holds,
  or one the caller may not change, fails with an `*EdgeError` (Err
  `ErrNotFound`, or `ErrLinked` when the caller may read it and it has
  another owner). The count is read after the UPDATE, not from its row count.
- Remove sets NULL on the given children this owner holds; Clear on all of
  them. The foreign key must be nullable, or the statement fails.
- For a one-to-one edge Add of one key lets go of the child the owner holds
  first, as gqlimpl's Child did; one the caller may not change fails.
- Add for many owners at once (an Update matching several rows) of an edge to
  many with the key on T fails: a child has one owner.

Edge writes go through the untyped Table API, so T's and J's policies and
hooks run as for any write.

### GraphQL: an input gives setters

Each create and update input has one method, with no ctx, since it does
no I/O:

```go
func (in *CreatePostInput) Setters() ([]velox.Setter[Post], error)
func (in *UpdatePostInput) Setters() ([]velox.Setter[Post], error)
```

It is plain Go: a field given becomes its column's Set (or SetNull for a
clear field), an edge's IDs are decoded and become the edge's or the foreign
key column's setter. A validator's error, or an ID that does not decode, is
BAD_USER_INPUT naming the input's field, before any write. An explicit null
is a field not given, as in entgql: a field is cleared by its clear field
alone. Existence of linked rows is checked by the core when the write runs;
a resolver maps `*velox.EdgeError` with `gqlimpl.WriteError`, which names
the input's field (the Conn knows edge to field).

The resolver writes:

```go
set, err := input.Setters()
if err != nil {
	return nil, err
}
x := &gen.Post{AuthorID: viewer(ctx)}
if err := r.db.Insert(x).Set(set...).Exec(ctx); err != nil {
	return nil, err
}
return x, nil
```

The generated `Create` and `Update`, and gqlimpl's write machinery, are
removed. `veloxgql.GeneratedCodeIsVersion15`.

## Phases

1. Core edge setters, with tests (root module).
2. velox-graphql Setters, gqlimpl cleanup, examples, docs, ADR.
