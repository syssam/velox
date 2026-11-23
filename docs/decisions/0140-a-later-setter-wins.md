# 140. A later setter of a column wins

- Status: accepted, 2026-11-23. Replaces the refusal of a column assigned
  twice in an UPDATE.

## Context

velox refused an UPDATE that assigned one column twice, because
PostgreSQL rejects `SET x = 1, x = 2`. ent and veloxe keep the last value:
a builder's mutation holds one value per field, so `SetStatus(a)` and,
under a condition, `SetStatus(b)` write `b`. The nextapp port has 344
builders written that way (252 UpdateOneID, 92 Create), a default then a
conditional override.

## Decision

- A column given more than one setter in an UPDATE keeps the last one, at
  its place in the SET list (`lastAssignments`). It holds across Set calls
  and within one, for Set, SetNull, Add, Sub and SetExpr alike: a Set after
  an Add writes the Set, an Add after a Set writes the Add.
- An Add or Sub after another Add or Sub of the column fails, naming it,
  before anything is sent. ent and veloxe sum repeated Adds; keeping only
  the last would lose the first silently, and summing in velox would need
  arithmetic on every field type. The caller gives one Add of the sum.
- A skipped setter (`SetPtr(nil)`, a skipped Input field) sets nothing, so
  it never replaces an earlier setter, as veloxe's `SetNillableX(nil)`.
- A Set edge setter assigns its foreign key: it and a setter of that column
  replace each other in the same way, the later winning.
- `Save(&e, cols...)` that names a column twice writes it once.
- An insert already kept the last setter: setters write the row before it
  is sent.
- An upsert's update branch still refuses a column assigned twice: it mixes
  columns taken from the inserted row with setters, and which one wins is
  not a builder's last-value rule.
- A builder is a value: a setter added to a copy leaves the original as it
  was.

## Consequences

The SQL never assigns a column twice, on any dialect. A column assigned
twice by mistake is no longer reported; ent does not report it either.

## Evidence

`internal/apitest/write_test.go`: `TestALaterSetterWins` (one Set, two Set
calls, Set then Add, two Adds refused, SetPtr(nil) after Set, an insert,
Save naming a column twice, a derived builder leaves its base unchanged); `edgeset_test.go`: `TestEdgeSetterSetKey` (an edge's
Set and its column's setter, the later wins).
