# 0085. A one-to-one pair to its own schema may keep its key on the assoc edge

- Status: accepted, 2026-11-19. Narrows rule 11.

## Context

In ent, the inverse edge of a one-to-one pair holds the foreign key, and
`Field` on the assoc edge is refused. The largest consumer declares:

```go
field.Int64("reversed_by_id").Optional().Nillable(),
edge.To("reversed_by", PaymentAllocation.Type).Field("reversed_by_id").Unique(),
edge.From("reversal_of", PaymentAllocation.Type).Ref("reversed_by").Unique(),
```

Here a row's `reversed_by_id` holds the ID of the row that reverses it, and
services call `SetReversedByID`. veloxe and ent accept this schema by
accident. Their resolver visits the `To` edge first, takes it for a
bidirectional one-to-one, and then pairs it with the `From` edge without
clearing that flag. So both edges claim the key, and `reversal_of` reads the
column from the wrong end.

When both edges are on one table, either row can hold the key, and the
`Field` says which.

## Decision

- When both edges of a one-to-one pair target their own schema, and only the
  assoc edge binds a field, that edge's rows hold the key (`Edge.KeyOnAssoc`).
  The column, its unique index and its foreign key are the ones veloxe made.
  The assoc edge reads the row whose ID the key holds. The inverse edge reads
  the row whose key holds its ID.
- Every other one-to-one pair keeps ent's rule, and rule 11 refuses its assoc
  edge's `Field`.

## Consequences

- The schema ports unchanged. `reversal_of` now returns the reversed row,
  where veloxe returned the same row as `reversed_by`.
- Generated code for other schemas does not change.
