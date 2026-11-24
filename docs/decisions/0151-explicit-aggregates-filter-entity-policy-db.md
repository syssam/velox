# 0151. An aggregate Calc is marked, a Filter reads its entity, a policy gets the DB

- Status: accepted, 2026-11-24. Supersedes the "aggregate in Calc's text"
  answer of [0142](0142-nextapp-projection-recipes.md) and
  [0137](0137-nextapp-port-helpers.md)'s `velox.HasColumn`; amends
  [0132](0132-filter-reads-through-the-statements-db.md)'s Filter
  signature; supersedes [0135](0135-policies-read-through-the-transaction.md)'s
  "no parameter" for policy functions and privacy rules, keeping its
  recursion guard.

## Context

A review asked for no hidden state and no guessing. It found three places
that fell short.

1. 0142 made a `Calc` an aggregate when its text called a known aggregate
   function: a name such as SUM or MAX followed by `(`. The scan misfired on
   SQLite's two-argument `MAX(a, b)` and `MIN(a, b)`, which are values of
   each row. It missed every aggregate it did not list: `PERCENTILE_CONT`,
   `MEDIAN`, a user's aggregate.
2. 0137's `velox.HasColumn(entity, column)` read a process-wide registry,
   filled when an entity's metadata was first built, under an RWMutex. It
   was false for an entity not yet used, answered by name, so two
   packages' entities of one name were one, and took a lock per call. A
   Filter was given only the entity's name, so it had nothing better.
3. A policy function took only ctx. One that read (an organization's
   subtree) needed a package-level `var AppDB *velox.DB`, set after
   Connect, and `velox.TxFrom(ctx, AppDB)` to join the statement's
   transaction (0135). That is hidden state, and the schema compiled
   without the variable set. 0135 rejected a parameter because the shapes
   were fixed by existing and veloxe-ported schemas; before 1.0 nothing is
   kept for compatibility, and a Filter already got the DB (0132).

## Decision

### 1. `velox.Agg` marks an aggregate Calc

`func Agg[E any](t Term[E]) Term[E]` marks an expression as an
aggregate, as `Decimal()` marks one as holding decimals (0131).
It is a function, not a method of Term: every package that names a Term
describes its methods, and a method `Agg` added 432 bytes to each
(`entityDescUse`); a function costs only where it is called.
`untyped.Agg(t)` is the same for an untyped term. A marked term is placed
and refused as an aggregate Term is (`hasAgg`): MySQL orders a grouped
projection by it as written, not as `ANY_VALUE` of it; it cannot be inside
an aggregate, a joined term or a Rollup. velox does not read the text, so
`exprCallsAgg` and its list are gone. An unmarked `Calc("SUM(?)", x)` is a
value of each row to velox; the database refuses it where SQL does.

The mark is in the term's shape and in `sameTerm`. `Agg` of a term that is
not an expression, of a window function, or of an expression that holds
an aggregate Term (`Calc("SUM(?)", x.Sum())`) fails the statement, the
last as an aggregate of an aggregate, as `Sum` of a `Sum` does. A marked
term's arguments are inside an aggregate, as a `Sum`'s are: a HAVING
writes a GROUP BY key there as it is, not as MySQL's `MIN` of it
(0138), which inside `COUNT(DISTINCT …)` was error 1111
(`TestHavingRepeatedTerm`).
`GroupBy` now refuses an aggregate key, marked or not, before anything is
sent. `SetExpr` takes text, not a term, so it no longer refuses aggregate
text itself; the database does (`TestCalcAgg`).

### 2. A Filter gets a `FilterEntity`

```go
type Filter func(ctx context.Context, db *DB, entity FilterEntity, op Op) (Predicate, error)
type FilterEntity struct{ /* the entity's *engine.EntityInfo */ }
func (e FilterEntity) Name() string
func (e FilterEntity) HasColumn(column string) bool
```

`FilterEntity` is a value holding the entity's metadata. `Name` is what
the Filter was given before, the `TypeX` constant's value. `HasColumn`
looks the column up in that entity's own name index (`ColumnNamed`), as a
condition by name does: one map read, no lock, exact for that entity.
The zero value names nothing. `velox.HasColumn`, `engine.HasColumn`,
`registerInfo` and the registry are removed. `privacy.Filter`'s
`HasColumn` reads the same index (`TestFilterEntityHasColumn`). The
generated `TypeX` comment says the value is `FilterEntity.Name`'s; a
comment only, so no version change.

`FilterEntity` is declared in the root and the engine hands a filter the
`*EntityInfo`: an engine type with methods that `DB` reaches is described
by every package that uses a DB.

The name `FilterEntity` was chosen over `EntityInfo` and `Meta`, which
name other things, and over `Entity`, taken.

### 3. Policy functions and privacy rules get the statement's DB

```go
type Policy struct {
	Read     func(ctx context.Context, db *DB) (Predicate, error)
	Write    func(ctx context.Context, db *DB) (Predicate, error)
	Insert   func(ctx context.Context, db *DB, r Row) error
	Query    QueryPolicy
	Mutation MutationPolicy
}
type PrivacyQuery interface { Filter() PrivacyFilter; DB() *DB }
type PrivacyMutation interface { Op() Op; Filter() PrivacyFilter; Value(name string) (any, bool); DB() *DB }
```

`db` is the DB the statement runs on, never nil: inside `Tx` the
transaction's, as `Mutation.DB` is for a hook and the `db` a Filter gets.
An insert that velox runs in a transaction of its own (a batch of several
statements, a recording DB's insert, MySQL's upsert that reads its row
back, a bidirectional edge's link) asks `Insert` and the Mutation rules
inside that transaction, with its DB; before, they ran before it began,
on the outer DB (`TestInsertPolicyInImplicitTx`). A denied row then rolls
back a transaction that wrote nothing. A hook around the insert runs
before velox begins it, so its `Mutation.DB` is the outer DB. `SQL(ctx)`
asks the policy on the DB it was called on, as before.
`privacy.Query` and `privacy.Mutation` are `PrivacyQuery` and
`PrivacyMutation`, so a rule list reads through `q.DB()` or `m.DB()`.
0135's guard stays: a statement made under a policy function's or rule
list's ctx is not judged by it, whatever DB it runs on
(`TestPolicyReadsThroughItsDB`, `TestPolicyGetsTheStatementsDB`,
`TestPolicyReadsWithoutItself`).

`Policy` was an alias of an engine struct; its functions now name the
root's `DB`, which the engine cannot import, so `Policy` and the privacy
rule types (`PrivacyQuery`, `PrivacyMutation`, `QueryRule`, `QueryPolicy`,
`MutationRule`, `MutationPolicy`, and `PrivacyFilter`, an alias) are
declared in the root, and package privacy aliases them as before. The
root erases a Policy to `engine.Policy`, whose functions take
`*engine.DB`, and casts the DB back, as `WithFilter` does. `erasePolicy`
is a function, not a method of Policy, which every schema with a
`Policy()` would describe.

A generic `engine.Policy[D]` with `type Policy = engine.Policy[DB]` was
built first and rejected: every package that names `velox.Policy`
instantiated the rule types and their methods, and a Policy and three
hooks grew an entity package by 177 KB, against 87 KB before. The root
types cost nothing per package.

The functions an entity's metadata keeps (`EntityPolicy`'s, which
`EntityInfo` reaches) take the DB as `engine.StmtDB`, an `any` holding
the `*engine.DB`, as `MutationState.DB` is (`dbOf` converts). With
`*DB` in those signatures an entity package that uses no DB described
`DB` and all it reaches: `entityDescriptor` grew from 144550 to 169908
bytes. With `StmtDB` the compile-cost bounds are those before this
record: `entityDescriptor` 145480, `entityDescUse` 795980,
`entityDescHooks` 881746, `entityDescMixin` 927220 bytes.

An `Interceptor`'s `Filter` gets the statement's DB too, so a schema's
behaviour reads one way: `Filter func(ctx context.Context, db *DB)
(Predicate, error)`. `Interceptor` is declared in the root, as `Policy`
is, and the engine keeps each Filter as a `FilterFn` taking a `StmtDB`.
0135's guard stays for it: a statement made under a Filter's ctx is not
narrowed by that Filter. WithoutPolicy does not lift an interceptor, so
the DB its Filter gets is the statement's without the WithoutPolicy
reason: a read through it is judged by the policies, which the bypass
would otherwise have lent the interceptor
(`TestInterceptorDBKeepsPolicies`). A `WithFilter` filter, which
WithoutPolicy does not lift either, still gets the statement's DB as it
is (0132). `TxFrom` stays for helpers handed a DB and a
ctx, such as a `WithTx` that joins the caller's transaction; no behaviour
function needs it. velox-gen checks policy keys, not function types, so it is unchanged, and
the generated code is unchanged: no version change.

## Consequences

- A `Calc` that aggregates needs `velox.Agg`. Forgetting it on MySQL writes
  `ANY_VALUE(SUM(x))` in a grouped ordering, which MySQL refuses: a loud
  failure, not a wrong answer.
- A Filter compares `e.Name()` where it compared `entity`.
- An Interceptor's Filter gains a `db *velox.DB` parameter, `_` where it
  reads nothing.
- Every Policy function gains a `db *velox.DB` parameter, `_` where it
  reads nothing. A veloxe or ent port's privacy rules compile unchanged
  unless they implement `privacy.Query` or `privacy.Mutation` themselves.
