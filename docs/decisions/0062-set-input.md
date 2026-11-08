# 62. SetInput writes an input, and keeps its error

Status: accepted, 2026-11-08. Revises 0048's bullet on `Setters()`.

## Context

Every GraphQL mutation resolver began with three lines:

```go
set, err := input.Setters()
if err != nil {
	return nil, err
}
```

then wrote `Set(set...)`. entgql's resolver writes
`client.Todo.Create().SetInput(input).Save(ctx)` in one line. 0048 kept
`Setters()`'s error because an ID could fail to decode. Since 0061 an ID
is the key in its own Go type, which gqlgen decodes, but `Setters()` can
still fail: a validator's refusal, an edge list over `gqlgraph.MaxIDs`,
JSON that does not decode, or a zero value given to a field with a
Default.

## Decision

- `velox.Input[E]` is an interface with one method,
  `Setters() ([]Setter[E], error)`. A generated input is one.
- `UpdateQuery.SetInput(in)` and `InsertQuery.SetInput(in)` add its
  setters, as `Set` does. When `Setters` fails, the builder keeps the
  first error, and `Exec`, `One` and `SQL` return it unchanged before
  anything is sent, as a condition's error is kept by `Where`. So the
  error presenter still codes a refused input BAD_USER_INPUT.
- The generated `Setters` has a value receiver, so the value gqlgen
  passes to a resolver is an `Input` as it is. Generated code changed:
  `veloxgql.GeneratedCodeIsVersion22`.
- `Setters()` stays exported, for a resolver that wants the setters
  themselves.

## Consequences

- A resolver is one line, as in entgql:
  `db.Update[gen.Post]().Where(gen.Posts.ID.EQ(id)).SetInput(input).One(ctx)`.
- A resolver's own setter goes beside the input's in the same statement:
  `u := ….SetInput(input); u = u.Set(gen.Users.PasswordHash.Set(hash))`.
- The error is found when the statement runs, not where `SetInput` is
  called. Work the resolver does in between, such as hashing a password,
  runs for an input that is then refused.
- Tests: `TestSetInputWritesItsSetters` and `TestSetInputKeepsItsError`
  in `internal/apitest`.
