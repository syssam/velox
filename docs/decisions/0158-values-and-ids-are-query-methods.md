# 0158. Values and IDs are methods of Query, and Values takes the handle

- Status: accepted, 2026-11-24. Supersedes the section "Values and IDs" of
  [0100](0100-after-commit-values-and-log.md).

## Context

0100 made `velox.Values(ctx, q, I.Number.Key())` and
`velox.IDs[int64](ctx, q)` functions. Next to ent's
`client.Invoice.Query().Where(invoice.Paid(true)).Select(invoice.FieldNumber).Strings(ctx)`
they read inside out: the query is the middle argument, and the column
needs `.Key()` to give its value type.

Two things changed since 0100 and 0022. Every method of `Query` is now a
generic method that compiles only where it is called (0055, 0059), so a
method costs a package that names `Query` only its declaration. And a
type parameter can be inferred from a struct type through a constraint
whose core type is that struct, which no handle used, as every handle's
struct was `struct{ I int32 }`, the same for every V.

What velox keeps from 0100: V is checked when the program compiles, a
pointer for a nullable column, and the read is All's, with the Read
policy, the scope, the interceptors, `WithFilter`, the
Limit-or-AllowUnbounded rule and the row cap.

## Decision

- `func (q Query[E]) Values[H valueColumn[E, V], V any](ctx, h H) ([]V, error)`:

      numbers, err := db.Query[veloxgen.Invoice]().Where(I.Paid.EQ(true)).Limit(100).Values(ctx, I.Number)

- Each column handle kind that has a `Key` names the type of the values it
  reads in its struct, in a field that is always nil:
  `StringCol[E, V]` is `struct{ _ *V; I int32 }` and `NullStringCol[E, V]`
  is `struct{ _ **V; I int32 }`. `JSONCol` and `NullJSONCol` stay
  `struct{ I int32 }`: their stored bytes are not their value.
- `valueColumn[E, V]` is `Column[E]` (the method `column(*E) int32` every
  handle already has) and `~struct{ _ *V; I int32 }`. A call infers H from
  the argument and V from the struct. The method ties the handle to E: the
  struct holds no E. The constraint is unexported; no other type
  satisfies it.
- These do not compile, each with the checker's error:
  - a JSON handle: `H (type velox.JSONCol[E, []string]) does not satisfy
    velox.valueColumn[E, V]`;
  - another entity's handle: `does not satisfy velox.valueColumn[E, V]
    (wrong type for method column) have column(*T) int32 want
    column(*E) int32`;
  - a handle's pointer, and a `Key()`, which is a `GroupKey`.
- `func (q Query[E]) IDs[K any](ctx) ([]K, error)`, called as
  `q.IDs[int64](ctx)`: Go 1.27 instantiates a generic method explicitly.
  K is still the caller's, as nothing in E's type says the key's type; a
  K that is not the key field's type fails before anything is sent.
- The error for that K is built by `engine.KeyTypeError`, not in the
  generic body. With `reflect.TypeFor[K]()` in the body, every package
  that called `IDs` compiled reflect's instance for K: a package calling
  `Values` twice and `IDs` once took 242,478 bytes, and 85,538 without.
- The functions `velox.Values` and `velox.IDs` are removed. Before 1.0
  nothing is kept for compatibility.
- Nothing in generated code changes: velox-gen writes
  `velox.StringCol[Invoice, string]{I: invoiceColNumber}`, which compiles
  against the new struct. `GeneratedCodeIsVersionN` does not move.

## Candidates

Measured on darwin/arm64, Go 1.27.4, with `go build -trimpath`, as
`TestGenericTypesCompileSmall` measures: the bytes a package takes beyond
one that imports velox. "handle" is one variable of
`StringCol[E, string]`. "20 entities" is a package of 20 entities with six
handle kinds each (Col, TimeCol, NumberCol, StringCol, NullNumberCol,
NullStringCol): 120 handle types. "entityDescUse" is the row of
`TestGenericTypesCompileSmall`. "call" is a package that reads a text and a
nullable number column and the key, over one that calls nothing.

| Candidate | Call | handle | 20 entities | entityDescUse | call |
| --- | --- | --- | --- | --- | --- |
| 0100, today | `velox.Values(ctx, q, I.Number.Key())` | 14,494 | 1,253,418 | 787,574 | 242,478 |
| (2) method with Key | `q.Values(ctx, I.Number.Key())` | 14,494 | 1,257,980 | 792,314 | 85,538 |
| (2b) chosen: `_ *V` | `q.Values(ctx, I.Number)` | 17,302 | 1,286,814 | 794,992 | 81,814 |
| (2b) `_ [0]*V` | `q.Values(ctx, I.Number)` | 17,880 | 1,307,390 | 798,018 | 81,494 |
| (2b) `_ [0]GroupKey[E, V]` | `q.Values(ctx, I.Number)` | 20,014 | 1,536,390 | 808,202 | 84,400 |
| (2b) an ordinary method `valueKey() GroupKey[E, V]` | `q.Values(ctx, I.Number)` | 26,880 | 2,144,786 | 822,268 | 89,472 |
| (3) a method of each handle | `I.Number.Values(ctx, q)` | 14,494 | 1,260,456 | 791,704 | 78,504 |

Every row but today's has `IDs` as a method, without reflect in its body.

- **(2b) `_ *V` is chosen.** It is the only form that reads left to right
  with no `.Key()`. It adds 240 bytes a handle type over (2) (28,834 over
  120 types), and 2,678 bytes to an entity package. The handle row stays
  under its bound of 19,456. The cost is in the type descriptors of a
  struct with two fields; a pointer field needs no array and slice
  descriptors, as `[0]*V` does.
- **`_ [0]*V`** keeps a handle at four bytes, with no pointer. It adds 412
  bytes a handle type, as each `[0]*V` needs its own array and slice
  descriptors. A handle is a package variable, passed by value and boxed
  only by `Columns`, `Omit` and `Validate`. Twelve more bytes a handle
  cost less than the 172 more bytes of object a handle type.
- **`_ [0]GroupKey[E, V]`** names E as well, so it needs no method in the
  constraint. It adds 2.3 KB a handle type: a `GroupKey` instance, its
  array and slice for each (E, V). 0022 measured the same shape at +4% of
  `internal/testmodel` and chose `Key()`.
- **An ordinary method** satisfies an interface from which a call infers
  V, `interface{ valueKey() GroupKey[E, V] }`. It adds 7.4 KB a handle
  type and takes the handle row to 26,880, over the bound, as 0100 said.
  A lazy generic method satisfies no interface: with `Key` as the
  interface's method, the call fails with `type StringCol[Inv, string] of
  StringCol[Inv, string]{…} does not match keyerL[Inv, V] (cannot infer V)`.
- **A union of the handle types**, or a constraint
  `~struct{ I int32 }`, cannot give V: the struct does not mention it.
  Both fail with `in call to q.ValuesU, cannot infer V`.
- **(2), `q.Values(ctx, I.Number.Key())`**, is the cheapest left-to-right
  form, but keeps `.Key()`.
- **(3), `I.Number.Values(ctx, q)`**, costs no more than (2) and needs no
  Key, as V is the receiver's. It puts the column first and the query
  last, and is ten methods more, one on each kind. ent's order is the
  query's chain, then the read.
- **IDs**: `q.IDs[int64](ctx)` compiles and is ent's `IDs(ctx)` with the
  key's type named. `q.Values(ctx, U.ID)` reads the same column and
  infers the type, and `IDs` stays for code that is generic over E, which
  has no handle of the key.

At bench/scale's 500 entities and 100 services (`scripts/scale-check.sh
-n 500 -s 100`, GOFLAGS=-p=2, one run each on a shared machine):

| Run | Cold build | Schema edit | Service | Link peak |
| --- | --- | --- | --- | --- |
| today, 1 | 5.58x (169.3 s CPU) | 5.10x (154.6 s) | 5.7 MB | 217 MB |
| today, 2 | 5.80x (170.7 s) | 5.15x (151.4 s) | 5.7 MB | 218 MB |
| (2b) `_ *V`, 1 | 6.02x (175.0 s) | 5.32x (154.7 s) | 5.7 MB | 240 MB |
| (2b) `_ *V`, 2 | 5.49x (172.6 s) | 5.43x (170.7 s) | 5.7 MB | 224 MB |

The thresholds are 8.3x, 7.0x, 5.9 MB and 299 MB. The build ratios move
within the runs' noise, which the notes of `bench/scale/thresholds.json`
put at 8 to 29%. The link peak rose by 6 to 22 MB, within its bound.
Generated code calls no Values, so the rise is the handles' new field. `internal/testmodel`'s archive
took 3,894,530 bytes, up from 3,863,902 (+0.8%), where 0022 measured +4.0%
for a phantom field that named E.

## Consequences

- `internal/apitest/values_test.go` covers Values and IDs under a policy,
  a filter and `WithoutPolicy`, a nullable column, the refusals and real
  SQLite rows; `internal/integration/keys_test.go` reads `IDs` of a UUID
  key and a string key on every database.
  `internal/repocheck/save_compile_test.go`'s `TestValuesAtCompileTime`
  checks the inferred type of every readable kind, and that a JSON
  handle, another entity's handle, a handle's pointer, a `Key()` and a
  result of another type do not compile.
- `TestGenericTypesCompileSmall`'s handle rows grew by about 2.8 KB each,
  within their bounds; its entity package bounds were raised by the
  measured growth.
- A conversion between a nullable and a non-nullable handle kind no
  longer compiles, as their structs differ: `SumNull` and the other
  aggregates of a `NullNumberCol` build a `NumberCol` from its `I`.
- The grouped aggregates still take `Key()`. The field now in every
  readable handle would let `CountBy` and the others take the bare handle
  too; that is left for another decision.
- veloxe's `Strings(ctx)`, `Ints(ctx)` and the like map to
  `q.Values(ctx, T.F)`; `IDs(ctx)` maps to `q.IDs[int64](ctx)`.
