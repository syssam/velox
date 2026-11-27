# 183. veloxvet's opt-in check of a locking read's handle

Status: accepted, 2026-11-27.

## Context

velox refuses a locking read (`ForUpdate` or `ForShare`) outside a
transaction, when it runs: "a locking read runs only inside Tx"
(`guardLock`). An application found this late: a helper locked rows
with `client.Query[...]().Where(...).ForUpdate().All(ctx)` on a
`*velox.DB` parameter, and one caller passed its struct field `s.client`
with its request context, before it opened its transaction. Nothing
static caught it.

No sound check can. A plain DB adopts the transaction its context
carries (`DB.joined`), so a read on `s.client` under a context
parameter may be in a transaction, and a check that followed contexts
would have to give up at every context parameter. A check that is
useful has to judge the handle and leave the context out.

## Decision

veloxvet gets a third analyzer, `LockTx` (`locktx`). It is strict, and
so it is off by default.

- **Switch.** `LockTx` is in `veloxvet.Analyzers`, and so in
  `cmd/veloxvet`, but does nothing unless its analyzer flag
  `-locktx.enable` is set:
  `go vet -vettool=$(which veloxvet) -locktx.enable ./...` runs it
  beside `unbounded` and `zerodefault`. A flag of an analyzer
  (`-NAME.flag`) does not select analyzers. Naming one (`-locktx`) runs
  only those named, and would turn the other two off; a separate command
  or a separate slice would make a second binary or a second driver
  setup for one check. The default run's output does not change, and it
  exports no new fact. Another driver sets the flag with
  `LockTx.Flags.Set("enable", "true")`.
- **Locking read.** `Query`'s `All`, `One`, `First`, `Iter`, `Exists`,
  `IDs`, `Values`, `Rows`, `SQL` and `Paginate`, `velox.Scan` and
  `ScanOne`, and `TableQuery`'s `All`, `One`, `First`, `Exists`, `Rows`
  and `SQL`, of a query that `ForUpdate` or `ForShare` was applied to on
  some path. `Count` and the aggregates, `LoadEach` and `CountEach`
  refuse a lock whatever the handle and are left out. The query is
  followed back through locals, reassignments, branches (phis), free
  variables and the chaining methods of Unbounded's `roles` table, to the
  handle it started from: `db.Query`'s receiver, `untyped.Query`'s
  argument, or the parameters a helper that returns a query started it
  from (a `QueryFrom` fact). `TestTablesMatchAPI` checks every table
  against velox's API.
- **A transaction's handle.** A `*velox.DB` parameter of a function
  literal that is only ever given to `DB.Tx`, or to a parameter of a
  function (or of a function literal, a test's `inTx`) that hands that
  parameter only a transaction's handle (a `TxCallback` fact: the
  `TxFrom`-then-`Tx` helper, a test's `Hold`, a wrapper that checks its
  callback for nil first), or called with one; a variable a literal
  captures, which go/ssa spills to an `Alloc`, is followed through its
  loads; `velox.TxFrom`'s and `velox.UseTx`'s first result;
  `WithoutPolicy` or `Record` of such a handle. These are accepted.
- **A parameter's handle.** A handle (or a query) that is a parameter of
  the enclosing named function, through its literals too, is not judged
  there: the function gets a `LocksOnParam` fact naming the parameter,
  and each static call of it, in its package or another, is judged with
  the argument it passes, by the same rules. So a helper's fact passes up
  to the function that first holds a handle of another kind.
- **A field of a parameter.** `input.Tx`, a field of a parameter that is
  a struct or a pointer to one, or a field of such a field (`legs.in.Tx`,
  up to five levels), counts as that parameter's (the fact names the
  path), unless the function stores to the field. A call is
  judged by what its struct literal or local struct variable stores in
  the field, or by the caller's own parameter's field. The receiver is
  left out: a service's `s.client` is set when the service is built, not
  per call, and following it would carry every report up to callers the
  check cannot see. A receiver's field is judged where it is read.
- **Anything else is reported**, at the read or at the call, naming the
  callee and the handle: a field of the receiver or of a value that is not
  a parameter, a package variable, a call's
  result, an entity's edge query (its handle is the entity's Source), a
  function literal's parameter that is not a transaction's. The message
  says to run it in `db.Tx` on the closure's tx or to take the handle as a
  parameter.
- **Strict inside a closure.** `s.client.Query[...]().ForUpdate()` inside
  `s.client.Tx`'s closure is reported, though velox runs it in the
  transaction, as the context joins. The closure's `tx` is the handle the
  check can prove; using it costs nothing and says what is meant.
- **Conditional locks.** A lock whose `ForUpdate`/`ForShare` call, read
  or call site is reached only through one branch of an `if` on a bool
  parameter, or on a parameter compared `==`/`!=` with a Bool, Int or
  String constant (typed constants included, and `switch`, which is
  lowered to such ifs), carries that condition in the fact. A call whose
  argument for it is a constant that fails it is not judged; a condition
  on the caller's parameter passes up as the caller's; any other argument
  (`tx != nil`, a variable) counts as taking the lock. A lock on a
  handle of another kind under such a condition goes into the fact as
  the function's own handle (`Param` -1), so the callers that pass a
  locking constant are reported, not the read.
- **Cycles and recursion.** A walk back through a loop (a phi, a
  variable stored back into itself) reaches its own start while open; it
  sees the answer so far, and the value at the head of the cycle walks
  again until its answer stops growing. A walk that reached an open walk
  outside it is not cached, so no answer depends on which read was seen
  first. A call of a function whose summary is being computed (mutual or
  self recursion) sees that function's locks of the previous pass over
  the package, and the passes go on until no lock changes (at most 16).
  Repeated paths are merged; past 64 distinct paths to one query, the
  check keeps one path, on a handle it does not follow, with any lock.
- **A helper's conditions.** A `QueryFrom` fact carries the conditions of
  the lock on the query it returns, and a call translates them as it does
  a `LocksOnParam`'s.
- **Unseen writes.** At a call, a struct argument's field is judged by
  what its literal or local variable stores there; when the variable's
  address, or the field's, goes anywhere else, or the struct is a call's
  result, the field holds a handle the check cannot follow, and is
  reported. A callee that writes its parameter's field, in its body or a
  literal, does not pass that field to its callers.
- **Waiver.** `//velox:lock-ok`, best with a reason, by the statement
  rule of `//velox:unbounded-ok` ([0176](0176-veloxvet-unbounded-reads.md)):
  on any line of the statement that holds the read or the call, or alone
  on the line before it; for a read in the header of an if, for or
  switch statement, or of a case, only the header counts, and a function
  literal's body lines belong to its own statements. It suppresses the
  report and also what would pass to the callers. A reason is not
  required, as with the other two directives.

## Consequences

Not followed, so not reported: calls through an interface or a function
value (a method value, a stored func); a locked query handed to another
function that reads it; a handle in a struct passed on by pointer and
changed elsewhere;
a function literal given to a callback API velox does not know and whose
use the check cannot see. A lock under a condition the check cannot read
(`a || b`, a condition on a field) counts as taken. A function called
only through an interface (a resolver) with a locking constant for its
gate is not checked. These are the limits of a per-function SSA walk with
facts; none of them reports a read that is fine.

Also not followed, and so reported as a function literal's parameter: a
literal returned from a factory (a hook chain) or sent through a channel
(a held test transaction's `Commit`), and a literal called only by its
own function with a handle of another kind. These need a waiver.

What it reports that velox would accept: a field or any other handle
inside a Tx closure, and a handle that the caller's context puts in a
transaction. Code written that way waives the site or passes the tx.

Rejected: following contexts, which no static check can do soundly;
turning it on by default, which would report code that relies on
context joining; a run-time warning, which is what already exists.

Measured on one application with 803 locking reads (2026-11-27), whose
only bug of this class had been fixed: the first version reported 164
sites, nearly all closures behind wrappers that go/ssa's spilled
captures hid, and handles in input structs (`input.Tx`). After following
spilled captures, nil checks, literal wrappers and parameter fields, 10
remain: one read on a receiver's field, four closures given to a held
transaction's `Commit`, one hook-factory closure, one literal called with
`input.Tx`, the `inTx`/`tx != nil` shape of a helper, and two sites that
a local copy of a field struct hid, which the check now follows (tested
