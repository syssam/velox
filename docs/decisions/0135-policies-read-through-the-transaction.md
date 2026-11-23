# 135. A policy or interceptor that reads joins the transaction, and is not applied to its own reads

- Status: accepted, 2026-11-23. Extends
  [0132](0132-filter-reads-through-the-statements-db.md); uses
  [0137](0137-nextapp-port-helpers.md)'s `TxFrom`. Superseded in part by
  [0148](0148-explicit-aggregates-filter-entity-policy-db.md): a policy
  function gets the statement's DB as a parameter and a privacy rule's
  Query and Mutation have `DB()`; the recursion guard stays, and an
  interceptor's Filter gets the statement's DB too.

## Context

0132 gave a `WithFilter` filter the statement's DB and left open that
"`Interceptor` and `Policy` functions get no DB; a read there would need the
same rule". They are user functions too, and a rule such as "the caller's
organization subtree" may read. Two faults were reproduced on the apitest
fixture `PNote` (SQLite, a pool of one connection):

- A Read policy that read through the DB it captured failed inside `db.Tx`
  with `ErrOutsideTx`, so one that narrows on a failed lookup saw 1 note
  outside a transaction and 0 inside. The same held for a privacy `Query`
  rule and an interceptor's `Filter`.
- A Read policy that read its own entity called itself for that read, and
  again for that one's, without end: each level a "select denied by
  policy" around the next, cut only by the test's own depth guard.

## Decision

- No new accessor. A policy function (`Read`, `Write`, `Insert`), privacy
  rule (`Query`, `Mutation`) or interceptor `Filter` that reads uses the DB
  the application gave it, joined to the statement's transaction with
  `velox.TxFrom` (0137), as an ent rule uses `ent.TxFromContext`:

  ```go
  rdb, ok := velox.TxFrom(ctx, db)
  if !ok {
  	rdb = db
  }
  ```

  The joined DB keeps `db`'s own `WithoutPolicy` reason, none when `db` has
  none, so a policy's lookup gets no bypass from the caller's transaction.
  A schema's behaviour is read from the schema's zero value, so its `db` is
  a variable the application sets after `Connect`.
- Rejected: an accessor for the statement's DB on the context
  (`StatementDB(ctx)`, this record's first draft). The user rejected a
  context accessor once (`FilterDB`, 0132), and it would overlap `TxFrom`,
  which already answers "the transaction this ctx is in". A parameter on
  the functions was rejected too: their shapes are fixed by schemas
  already written (nextapp's `rule.ReadRule`, the `scope` and `softdelete`
  mixins, veloxe's `EvalQuery(ctx, q)`, 0084–0089), about 170 function
  literals in 69 files, and `Policy`, an alias of `engine.Policy`, cannot
  name the root `*DB`.
- Recursion, keyed on the context: a statement made under the ctx a
  policy function or interceptor b was given, directly or through other
  functions' reads, is not judged or narrowed by b, whatever DB the
  statement runs on, `TxFrom`-joined or captured. A skipped Read, Write,
  Query, Mutation or Filter adds no condition; a skipped Insert passes.
  The key is each function of each Policy value, so a mixin's Read that
  reads its own entity skips itself, not the entity's own Read, which
  still applies to the read. Every other filter, interceptor and policy
  applies. The skip is a privilege only for the function's own read, made
  to decide; a statement the application makes is under no such frame
  unless it passes the function's ctx on. This is 0132's rule for filters;
  refusing a re-entry and a depth limit were rejected there for the same
  reasons.
- Mechanism: the engine wraps each function once, at bind (`guardCond`,
  `guardWrite`, `guardInsert`), and calls it with 0132's frame (`filterCtx`)
  holding the function's key. One allocation per call, on an entity that
  has the function. The engine's policy and interceptor types are
  unchanged. `fmt.Sprint(ctx)` ends in `.Policy` or `.Interceptor`.
- Prefer a condition that reads nothing itself (`HasEdgeWith`): it runs
  inside the statement. velox keeps nothing between statements, as 0132
  says.
- No generated code, schema signature or velox-gen check changes. Compile
  cost (`TestGenericTypesCompileSmall`): `entityDescriptor` and
  `entityDescUse` unchanged (145252 and 795564 bytes), `entityDescHooks`
  and `entityDescMixin` 6 bytes more.

## Consequences

A policy, a privacy rule or an interceptor that reads gets the same answer
inside a transaction and outside when it joins with `TxFrom`, and one that
reads its own entity ends. A captured DB without `TxFrom` still fails
inside a transaction with `ErrOutsideTx`, which catches the bug.

## Evidence

- `internal/apitest/policydb_test.go`: `TestPolicyReadsThroughTxFrom`
  (Read, Query, Interceptor: 1 note inside and outside a Tx through
  `TxFrom`; 1 and 0 with `ErrOutsideTx` through a captured DB),
  `TestInsertPolicyReadsInTx`, `TestBehaviourContextNames`,
  `TestPolicyReadsWithoutItself` (a Read policy reading its own entity runs
  once, inside a Tx and outside; the interceptor narrows its read; it
  failed without the guard).
- `internal/engine/filterdb_internal_test.go`: `TestGuards`.
