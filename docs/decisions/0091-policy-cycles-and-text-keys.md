# 0091. A policy that reaches itself is cut; an integer meets a text key as text

- Status: accepted, 2026-11-19. Amends [0079](0079-field-in-select.md) and
  [0081](0081-entity-table.md).

## Context

The largest consumer moved from veloxe, whose policy rules wrote raw SQL
subqueries with `sql.Table(...)`. A raw subquery applied no policy. velox
applies each subquery's target row condition (its interceptors, Read policy
and, on a read, Query rules), which stays. Two veloxe rules could not be
written:

- **Cycles (N-V15).** `RequireConversationParticipantToRead` lets Message,
  Conversation and ConversationParticipant read through
  `conversation_id IN (SELECT conversation_id FROM conversation_participants
  WHERE user_id = me)`. ConversationParticipant's own policy holds the same
  subquery, so applying it inside the subquery reaches it again, and
  `Scoper.RowCond` refused every such statement (`cycleError`).
- **Text keys (N-V16).** A rule wrote `CAST(<id> AS TEXT) IN (SELECT
  entity_id FROM approval WHERE ...)`: `approval.entity_id` is text and holds
  the ids of several entities. `FieldInSelect` refused the two column types.

## Decision

### A cycle is cut where it loops

While an entity's row condition is being made, an `EXISTS` (a relation's or a
`FieldInSelect`'s) that reaches the same entity again gets no row condition
from it. The outer occurrence applies the entity's condition in full, and
every other entity's condition still applies inside. No statement is refused
for a cycle; `cycleError` is gone.

Under nextapp's rule, a read of participants is `conversation_id IN (SELECT
conversation_id FROM participants WHERE user_id = me)` with nothing more
inside, which is veloxe's raw subquery. A read of messages applies the
participants' whole condition inside its subquery, which veloxe did not, so
the result is never wider than veloxe's. In a mutual cycle (A's policy reads
B, B's reads A) a read of A applies B's condition inside, and B's applies none
of A's again; a read of B is the mirror.

**The cut drops the looping entity's interceptors too.** RowCond makes one
condition per entity, the AND of its interceptors' and its policy's, and the
cut is of that condition. Keeping the interceptors would need them computed
for the inner occurrence, and an interceptor whose own condition reaches its
entity (`TestInterceptorCycleIsCut`) would loop the same way, so a second cut
rule would be needed for the interceptors alone: two rules where one does.
`WithoutPolicy` does not lift interceptors, and the cut does not lift them on
the statement either: only the one inner subquery, which is already filtered
by its own conditions and sits inside the outer occurrence that applies them
all, goes without them. The raw veloxe subquery applied none, a soft delete
included. A rule that wants a soft-deleted row left out of the subquery says
so among the subquery's conditions (`FieldIsNull("deleted_at")`).

**Memoisation.** RowCond keeps each entity's condition for the rest of the
statement. A condition made below a cut depends on where it was reached: B's
condition made inside A's has A's cut in it, which B's condition reached on
its own does not. Each RowCond therefore records the outermost cut its
condition met (`Scoper.cut`, a place on the `computing` stack). A condition is
kept only when that cut is its own entity's looping occurrence or none; one
cut at an entity further out is made again when it is reached elsewhere.
`TestPolicyMutualCycleCut` reads C through `FieldInSelect` on A and on B:
keeping B's cut-short condition lets a row through that B's whole condition
hides, and the test fails.

### An integer column meets a text key as text

`FieldInSelect(column, entity, key)` accepts an integer `column` (signed or
unsigned, nullable or not) against a `key` of a string kind, and compares
them as text by casting the outer side: `vx.key = CAST(outer.column AS TEXT)`
on PostgreSQL and SQLite, `vx.key = CONCAT(outer.column)` on MySQL. The cast
is on the outer side so that an index on the key still serves. Every other
mismatch is refused as before, the reverse (text column, integer key)
included, and a float.

MySQL's spelling is not `CAST(x AS CHAR)`: a CAST's text carries the
connection's collation with the same weight as the column's own (both
`IMPLICIT`), and MySQL refuses the comparison when the two differ (error
1267, seen with the CI database's `utf8mb4_0900_ai_ci` column and the
driver's `utf8mb4_general_ci` connection). `CONCAT` of a number is
`COERCIBLE`, so the column's collation wins and its index is used (checked
with `EXPLAIN` on MySQL 8.4). Without any cast MySQL compares a string with a
number as numbers, so `'02'` would equal 2.

The resolved link carries the choice (`Link.TextKey`), and the render cache
keys it. velox-gen's `FieldInSelect` check accepts the same pair and refuses
the rest at the call's line; the runtime check (`subKey`) is the same rule.

## Consequences

- A rule that reaches its own entity runs, with veloxe's meaning or narrower.
  A schema mistake that loops no longer fails loudly; it is cut instead.
- In a cycle, an entity's condition can be made more than once per
  statement, and its policy called more than once.
- `TestPolicyCycleCut` (the three-entity conversation),
  `TestPolicyMutualCycleCut` and `TestFieldInSelectTextKeyOnDatabases` in
  `internal/engine` run on SQLite, and on PostgreSQL and MySQL when their
  DSNs are set. The apitest tests that asserted the refusal now assert the
  cut SQL.
