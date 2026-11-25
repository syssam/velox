# 176. veloxvet reports unbounded reads at build time

Status: accepted, 2026-11-25.

## Context

A read with no `Limit` or `AllowUnbounded` fails with `ErrUnbounded` when
it runs (`engine.(*DB).guard`). A consumer found 76 such reads by hand,
among them chains broken by comments and queries held in variables. The
rule is local to a query's builder chain, so a static check can find most
of them before the program runs.

The rule, as the guard and the readers enforce it:

- `Query`'s `All`, `Iter`, `Values`, `IDs`, `Rows` and `SQL`,
  `velox.Scan`, and `TableQuery`'s `All`, `Rows` and `SQL` need `Limit`
  or `AllowUnbounded`.
- A grouped aggregate (`CountBy`, `SumBy`, `AvgBy`, `MinBy`, `MaxBy` and
  their `Null` forms) needs `AllowUnbounded`: it clears `Limit`, and a
  `Limit` does not stand in for it.
- `One`, `First` and `ScanOne` set their own `Limit`. `Count`, `Exists`
  and the plain aggregates read one value. `LoadEach` and `CountEach` set
  `AllowUnbounded` themselves. `Paginate` limits each page.
- Every chaining method returns a copy. None but `Limit` and
  `AllowUnbounded` changes the bound.

## Decision

A new module, `github.com/syssam/velox/veloxvet`, holds package
`veloxvet`, whose `Unbounded` analyzer (go/analysis) reports such a read,
and `cmd/veloxvet`, a multichecker that runs as `go vet -vettool` and
alone.

- The analyzer builds the package's SSA form and follows each query value
  through phis to a fixed point. A value's state is the set of what it can
  be on the paths to it: started with no bound, limited, allowed, or
  unknown. A read is reported only when every path is known and none is
  bounded. A parameter, a field, a closure's variable, a method value and
  a call it cannot see are unknown, so they are never reported.
- A function each of whose returns is a query velox started, with no
  bound, gets a `FreshQuery` fact. velox-gen's `QueryX` methods get it, so
  `post.QueryTags().All(ctx)` is reported in the calling package.
- velox's functions are named by import path and name: the module does
  not import velox. A chaining method it does not know returns an unknown
  query, so a new method cannot make it report a bounded read.
- `//velox:unbounded-ok` waives a report when it is on any line of the
  statement that holds the read, or alone on the line before the
  statement's first line. The statement is the innermost one: an `if`'s
  or a `switch`'s init is its own. For a read in the condition of an
  `if` or a `for`, a `switch`'s tag, a `range`'s expression or a `case`'s
  list, only the header's lines count; outside a function, the
  declaration's spec is the statement. A chain spread over lines is waived
  by one directive, wherever its read call sits. It is for a test or a demo that expects
  `ErrUnbounded`; `AllowUnbounded` cannot say that, as it removes the
  failure.
- The module requires only `golang.org/x/tools`. The root module does not
  depend on x/tools, so a library user's build stays as it was. The
  examples module has x/tools only indirectly and is not published.
- CI runs the module's checks in the lint job, its tests in the test job,
  whose coverage floors read their profile, and the command over the
  examples and the root, which must report nothing. ci-local.sh's
  `veloxvet` lane does the same, at the gate level: the module is about
  the ORM, not contrib, so it is not under `contrib/`.

## Consequences

Known false negatives: a query passed through a function's parameter or
returned by a function that is not fresh on every path, a query in a
field, a slice or a map, a closure's captured query, a method value
(`f := q.All`), a read through an interface or an embedding type, and
`TableQuery.Limit(0)`, which leaves the query unbounded.

Rejected: a check in velox-gen, which sees only the schema; an AST walk,
which would need its own reaching definitions for variables and branches;
a package in the root module, which would add x/tools to every user's
module graph.
