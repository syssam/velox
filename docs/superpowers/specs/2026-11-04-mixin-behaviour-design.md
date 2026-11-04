> **Note, 2026-11-04, at implementation.** Two things changed after this
> spec was written. Main merged `one-shape`, which kept the Descriptor's
> fields erased, so the list fields are erased too, for the same compile
> cost (decision 0037's Review): `Policy []func() BoundPolicy`,
> `Hooks []func() BoundHooks` and `Interceptors []func() BoundInterceptors`,
> not the typed `[]func() Policy[E]` of section 7.1. And
> `GeneratedCodeIsVersion` was 12 on main, so each phase's version is one
> higher than section 9 says: Phase 1 is 13, Phase 3 is 14, Phase 5 is 15.

# Mixins declare behaviour; interceptors filter rows

Status: design spec, 2026-11-04. Nothing is built yet.
Base: main at 18c9d64d, plus the `one-shape` branch. That branch replaces
`Descriptor.Policy func() BoundPolicy` with plain typed fields, `Scope Scoper[E]`,
`Policy func() Policy[E]` and `Hooks func() []Hook[E]`, and has the runtime
compose them.
Supersedes: 0030 in full. Revises 0029, 0034 and 0037. New record: 0038.

## 0. Decision, as given

- Remove `schema.Scoped`, the `scope_velox.go` it generates, and the `scope`
  package's role in codegen.
- Follow ent. A schema **and a mixin** can declare `Hooks()` (writes),
  `Interceptors()` (reads, new) and `Policy()`.
- Mixin behaviour runs before the entity's own, as in ent.
- Multi-tenancy and soft delete are each one ordinary mixin, written once.

## 1. Summary of the choices

| Question | Choice | Rejected |
| --- | --- | --- |
| Typing of mixin behaviour | Generic methods `Policy[E any, PE velox.Entity[E]]()`. velox-gen instantiates them per entity in gen/runtime. | Non-generic mixin methods typed with gen; handles passed as arguments; generated accessor methods |
| Columns a mixin uses | `velox.ColumnByName[E, V, PE]("org_id")`. It is resolved and type-checked while E's metadata is built, so a wrong name is a `*SchemaError` at first use. velox-gen also checks literal names when it generates. | A lazy resolve at statement time; a string column in predicates |
| Interceptor shape | `velox.Interceptor[E]{Filter func(ctx) (Pred[E], error)}`. It is a row filter that reaches everywhere Read reaches. | A full Querier wrapper; changing order or limit |
| Where interceptors run | Every SELECT, Count, aggregate, With load, EXISTS, join ON, UPDATE, DELETE, Save and upsert update branch. They run before the policy. `WithoutPolicy` does not lift them. | Queries only (ent); a top-level query only |
| Tenancy | A Policy plus an insert hook, from `scope.By`. `WithoutPolicy` lifts it. | An interceptor |
| Soft delete | An interceptor plus a delete hook that makes an update. The skip is `softdelete.Skip(ctx)`. | `Mutation.SetOp` |
| Composition | Mixins first, in `Mixin()` order, then the entity. Policies are ANDed and none can short-circuit. Hooks are concatenated, and the first is outermost. Interceptors are ANDed. | ent's first-Allow-wins |
| What velox ships | `schema/mixin/softdelete` as a package. Tenancy is an example written by the user, built on a reshaped `scope` package (`scope.By`, `scope.NewKey`). | A full tenancy mixin package; examples only |
| Descriptor shape | `Policy []func() Policy[E]`, `Hooks []func() []Hook[E]`, `Interceptors []func() []Interceptor[E]`. The runtime composes them. | One composed func written by velox-gen |

## 2. Typing: how a mixin declares behaviour generic over the entity

### 2.1 Generic methods

Go 1.27 here has generic methods (see `[_ Lazy[E]]` in query.go). A mixin
declares its behaviour as generic methods with exactly two type parameters:

```go
func (InOrg) Policy[E any, PE velox.Entity[E]]() velox.Policy[E]
func (InOrg) Hooks[E any, PE velox.Entity[E]]() []velox.Hook[E]
func (softdelete.Mixin) Interceptors[E any, PE velox.Entity[E]]() []velox.Interceptor[E]
```

`PE` is needed because generic code cannot infer it: `*E` does not satisfy
`velox.Entity[E]` when `E` is a type parameter. A prototype on go1.27.3 shows
this: `Col[E, int64]("x")` inside the method fails with "*E does not satisfy
Entity[E]". `Col[E, int64, PE]("x")` compiles, and so does
`m.DB().Update[E, PE]()`. velox-gen writes the partial instantiation
`sch.InOrg{}.Policy[gen.Project]`, and Go infers `PE = *gen.Project`. The
prototype confirmed four things: a method value of an instantiated generic
method, a generic method on a generic type, a type assertion from `any`
followed by instantiation, and a slice literal of such method values.

The rules:

- **Compile time.** The method's shape and its use of `E` are checked at the
  line in gen/runtime where velox-gen instantiates it. So are predicate
  values (`EQ(v V)`) and the agreement of a context key with its column
  (`scope.By` takes `*Key[V]` and a column of `V`).
- **Generate time (velox-gen).** Generic methods are not in a type's method
  set, so neither reflect nor an interface finds them. velox-gen therefore
  reads them from the mixin package's source with go/types. It does this for
  a mixin in the schema package and for one in any other package, such as
  `schema/mixin/softdelete`. It refuses each of these at its line:
  - a behaviour method on a mixin that is not generic ("a mixin is shared by
    entities: declare Policy[E any, PE velox.Entity[E]]()");
  - type parameters other than `[E any, PE velox.Entity[E]]`;
  - a result other than `velox.Policy[E]`, `[]velox.Hook[E]` or
    `[]velox.Interceptor[E]`;
  - a mixin that is itself a generic type and declares behaviour. velox-gen
    could not name its instance.

  Promoted methods count, as go/types resolves them through embedded
  structs. `mixin.Schema` declares no behaviour methods. If it did, every
  mixin would seem to have them.
- **Load time.** Each method runs once, when E's metadata is built, as an
  entity's methods do today. A panic in it becomes the entity's
  `*SchemaError`.

The entity's own schema keeps non-generic methods typed with gen, as today:
`func (Project) Policy() velox.Policy[gen.Project]`. The entity's own rules
use `gen.Projects.OrgID`, so they are checked fully at compile time.

Rejected:

- **A non-generic mixin method typed with one gen entity.** A mixin is shared
  by many entities, so this is useless.
- **Handles passed as arguments** (`Policy[E](h OrgHandles[E])`). velox-gen
  would need a convention per mixin, and the signature would vary.
- **Accessor methods generated on each entity** (`OrgIDColumn()`). They pollute
  gen's names for every mixin field.

### 2.2 Columns by name: `velox.ColumnByName`

A mixin cannot name `gen.Projects.OrgID`. It names the column it declares
itself, by name, and gives its Go type:

```go
// Named is the handle of column name of entity E, whose Go field is of type V,
// found by name when E's metadata is built. For mixins, which cannot name
// gen's handles. V is the field's type as velox-gen writes it: int64, or
// *time.Time for a Nillable time.
type Named[E, V any] struct {
	i     int32
	flags uint8                  // nullable, immutable
	ptr   func(x *E, i int) any // the Descriptor's FieldPtr
}

// ColumnByName returns the handle of E's column name, of Go type V.
// It panics when E has no such column or the column's field is not a V. Called
// from a mixin's Policy, Hooks or Interceptors method, which velox runs while it
// builds E's metadata, the panic is E's *SchemaError at its first use.
func ColumnByName[E, V any, PE Entity[E]](name string) Named[E, V]

func (c Named[E, V]) EQ[_ Lazy[V]](v V) Pred[E]    // for a pointer V, EQ(nil) is IS NULL
func (c Named[E, V]) NEQ[_ Lazy[V]](v V) Pred[E]
func (c Named[E, V]) In[_ Lazy[V]](vs ...V) Pred[E]
func (c Named[E, V]) IsNull[_ Lazy[V]]() Pred[E]   // nullable columns; else an invalid Pred, which fails the statement
func (c Named[E, V]) NotNull[_ Lazy[V]]() Pred[E]
func (c Named[E, V]) Set[_ Lazy[V]](v V) Setter[E] // a nil pointer sets NULL
func (c Named[E, V]) Ptr[_ Lazy[V]](x *E) *V        // &x.<field>
func (c Named[E, V]) Immutable[_ Lazy[V]]() bool
func (c Named[E, V]) Nullable[_ Lazy[V]]() bool
func (c Named[E, V]) column(*E) int32               // so it is a Column[E]: Columns, Save, OnConflict
```

**Resolution during the build.** The methods run inside `MetaOnce.Load`. A
call to `PE(nil).VeloxMeta()` there would deadlock. So `Load` publishes E's
column layout before it calls any behaviour function. The layout is the
`EntityInfo` columns and `FieldPtr`, and it never changes. `Load` keeps it in
an engine registry keyed by `ElemType((*E)(nil))`, a `sync.Map`.

- `ColumnByName` looks in the registry first. A call during the build always
  finds the layout, since the same goroutine published it. Another goroutine
  that finds it reads data that does not change.
- If the layout is missing, E was never loaded. `ColumnByName` then calls
  `PE(nil).VeloxMeta()`, which builds E or waits for the build. It is not
  inside E's build, so this cannot deadlock.
- A build that fails after publication leaves a layout that is still correct.
  Statements on E fail anyway, with its `*SchemaError`.

The checks at load are: the name exists (`EntityInfo.Column(name)`); the Go
type is exactly V, so a nullable column requires a pointer V; and the
`FieldPtr` result asserts to `*V`. Each failure panics with
`velox: gen.Project: mixin column "org_id": the field is string, not int64`,
which the entity's `*SchemaError` carries.

**Generate time.** velox-gen finds each `velox.ColumnByName[E, T, PE]("lit")`
call in a mixin method body with go/ast. For each entity that lists the mixin,
it refuses at the call's line a literal that names no column of that entity,
or a `T` that differs from the column's Go type. A name that is not a literal
is left to the load check, and velox-gen warns with the line.

Rejected:

- **Lazy resolution at statement time.** A wrong name would surface on the
  first statement that builds the predicate. That might be a rare code path
  in production.
- **A column name inside the predicate node, resolved at render.** This needs
  a new node kind and a cache, and it does not give earlier checks.

**What stays a runtime check:** the name and type of a non-literal name, the
`Immutable` and non-null checks of `scope.By`, a nil hook or interceptor, and a
missing context value (a statement-time denial).

## 3. Interceptors

### 3.1 The type

```go
// Interceptor filters entity E's rows for the application. Filter's condition
// is ANDed into every statement that reads or changes E's rows: SELECT, Count,
// the aggregates, Each and Iter, a With load of E, an EXISTS on E (Has,
// HasWhere), a join of E, UPDATE, DELETE, Save and an upsert's update branch.
// It runs before the policy, and WithoutPolicy does not lift it: an
// interceptor offers its own way to be skipped (softdelete.Skip). Raw SQL is
// not filtered.
//
// Filter returns Allow[E]() for no restriction; the zero Pred fails the
// statement, as a policy's does. An error fails the statement and is not a
// denial: errors.Is(err, ErrDenied) only when Filter returns a Deny.
type Interceptor[E any] struct {
	Filter func(ctx context.Context) (Pred[E], error)
}
```

It is a struct, like `Policy`, so that it can gain fields later. Before 1.0
nothing is kept for compatibility, but adding a field is cheaper than
replacing a function type. An `Interceptor` whose `Filter` is nil is refused
at load, as a nil hook is.

**It cannot change order or limit.** An interceptor reaches subqueries, joins
and With loads. An `ORDER BY` or `LIMIT` there means nothing, or it breaks
`LimitEach` pagination. Global limits already exist as `WithMaxLimit` and
`WithMaxRows`.

**It cannot wrap execution.** `WithAround` wraps every statement and sees
`st.Entity`, so a per-entity wrapper is three lines in an `Around` hook. A
per-entity Querier chain would add a second middleware model with nothing to
wrap for an EXISTS.

### 3.2 Where it runs

All of a statement's row conditions are made in one place:
`internal/engine/scope.go`, through `Scoper`. `ReadOf(t)` builds an entity's
Read condition once per statement. `target(t)` gives it to every EXISTS and
join ON. `scope()` gives it to the statement's own WHERE. `ConditionUpsert`
gives it to an upsert's update branch. A With load is a SELECT of the target,
so it gets the target's condition through `scope()`. Today Policy reaches all
of these (`Policy.Read`'s doc; `scopeJoins`).

The interceptors join the same path. `EntityInfo` gains
`filters []func(ctx) (Predicate, error)`, which has E erased. `ReadOf`
becomes `RowCond(t)`:

```
RowCond(t) = AND( t.filters...,                 // always
                  t.policy.Read if PolicyOn() ) // not under WithoutPolicy
```

It is cached per statement, and the existing cycle check applies to it. A
filter's `Has` reaches its target's `RowCond`. `target(t)` returns nothing only
when t has no filters and no Read policy applies. `scope()` builds this WHERE
for SELECT, UPDATE and DELETE:

```
WHERE <caller's conditions> AND <interceptors> AND <Read> AND <Write>
```

The order follows ent, where traversers run before `Policy.EvalQuery`.

Interceptors also reach writes. A row the application cannot read is a row
an update must not change. For example, `Update[Invoice]().All()` must not
re-stamp soft-deleted invoices. ent's interceptors skip mutations, so ent's
soft-delete example leaves ordinary updates able to change deleted rows.
velox closes that gap.

The following follow from where Read policies already go:

- A join entity of a many-to-many edge cannot have interceptors, as it cannot
  have a Read policy. It is read through its two sides, whose interceptors
  apply. A `Bidi` entity cannot have them either.
- On MySQL, an upsert that updates an entity with interceptors fails with
  `ErrPolicyUpsert`, as one with a Read or Write policy does today. It is
  renamed in the message only: "has a Read or Write policy or an interceptor".
- `Meta.HasReadPolicy` and `Table.HasReadPolicy` become `HasRowFilter`, true
  for a Read policy or an interceptor. gqlimpl's foreign-key filters use it
  (`gqlimpl/where.go:532`), so a GraphQL filter on a foreign key does not
  reach a soft-deleted target.
- The GraphQL batcher keeps callers apart by `RowScope`. An interceptor that
  reads a context value records it with `velox.WithScopeValue`, as
  `softdelete.Skip` does.

### 3.3 Interceptor or filtering Policy (0008)

Both add a condition and both reach the same statements. A write to a hidden
row affects 0 rows in both, as 0008 rules. They differ in purpose and in how
they are lifted:

| | Policy (Read/Write/Insert) | Interceptor |
| --- | --- | --- |
| Says | who may see or change a row | which rows exist for the application |
| Lifted by | `WithoutPolicy(ctx, reason)`, which is audited as `Statement.PolicyBypass` | only its own named skip |
| An error is | a denial, `*PolicyError`, `ErrDenied` | a failure of the statement |
| Insert | `Policy.Insert` checks the row | nothing; use a hook |

**Tenancy is a Policy.** A missing tenant is a denial. An operator's
cross-tenant job is the one audited bypass, `WithoutPolicy`, as 0029 ruled.
ent puts tenancy in privacy too (`FilterTenantRule`).

**Soft delete is an interceptor.** An operator who lifts policies to repair
data still does not want deleted rows unless they say so. ent uses an
interceptor too.

## 4. Soft delete

### 4.1 Delete becomes an update

A hook can already turn a delete into an update. It makes the write itself
and does not call next: `Mutation.DB`, `Mutation.Conds` and
`Mutation.SetAffected` exist and are documented with this example in
`policy.go`. `TestSoftDeleteThroughConds` (internal/apitest/hook_test.go:809)
tests it.

There is no `Mutation.SetOp` (ent's `mx.SetOp(ent.OpUpdate)`), and this spec
adds none. The update the hook makes runs the entity's whole pipeline: its
update hooks, the policy (tenancy and Write), the interceptors (so
re-deleting a deleted row affects 0), `UpdateDefault` (`updated_at`) and the
validators. `SetOp` would have to reproduce all of that inside a delete.

The consequences are documented on the package:

- An entity's own delete hooks see only hard deletes. A soft delete reaches
  its update hooks, since the mixin hook is outermost and does not call next.
  ent behaves the same.
- `SQL(ctx)` of a soft delete fails with `ErrDryRun`. It already does: there
  is no DELETE to render.
- `ON DELETE CASCADE` does not fire. Children stay, and a hook that wants to
  cascade soft-deletes them itself.

### 4.2 The package `schema/mixin/softdelete`

`schema/mixin/softdelete` imports velox's root, which does not import
`schema/...` (checked with `go list -deps .`), so there is no cycle.

```go
// Package softdelete is the mixin that makes an entity's delete set deleted_at,
// and hides rows whose deleted_at is set from every read and write.
package softdelete

import (
	"context"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Mixin adds deleted_at. List it in a schema's Mixin method.
type Mixin struct{ mixin.Schema }

// Fields returns deleted_at, NULL while the row is live.
func (Mixin) Fields() []schema.Field {
	return []schema.Field{field.Time("deleted_at").Optional().Nillable()}
}

type skipKey struct{}

// Skip returns ctx under which the mixin does nothing: reads see deleted
// rows, and a delete removes rows for good. It lifts nothing else: a policy,
// a tenant's included, still applies.
func Skip(ctx context.Context) context.Context {
	return velox.WithScopeValue(context.WithValue(ctx, skipKey{}, true), "softdelete", "skip")
}

// Skipped reports whether ctx is Skip's.
func Skipped(ctx context.Context) bool { v, _ := ctx.Value(skipKey{}).(bool); return v }

// Interceptors hides rows whose deleted_at is set.
func (Mixin) Interceptors[E any, PE velox.Entity[E]]() []velox.Interceptor[E] {
	del := velox.ColumnByName[E, *time.Time, PE]("deleted_at")
	return []velox.Interceptor[E]{{Filter: func(ctx context.Context) (velox.Pred[E], error) {
		if Skipped(ctx) {
			return velox.Allow[E](), nil
		}
		return del.IsNull(), nil
	}}}
}

// Hooks makes a delete an update that sets deleted_at.
func (Mixin) Hooks[E any, PE velox.Entity[E]]() []velox.Hook[E] {
	del := velox.ColumnByName[E, *time.Time, PE]("deleted_at")
	return []velox.Hook[E]{velox.On(func(next velox.Mutator[E]) velox.Mutator[E] {
		return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
			if Skipped(ctx) {
				return next.Mutate(ctx, m)
			}
			now := time.Now()
			q := m.DB().Update[E, PE]().Set(del.Set(&now))
			if cs := m.Conds(); len(cs) > 0 {
				q = q.Where(cs...)
			} else {
				q = q.All() // the caller said All: the guard ran before the hooks
			}
			n, err := q.Exec(ctx)
			m.SetAffected(n)
			return err
		})
	}, velox.OpDelete)}
}
```

User code is one line per entity: `softdelete.Mixin{}` in `Mixin()`.

**The skip.** `softdelete.Skip(ctx)` is the named, per-mixin skip. It lifts
the interceptor and the delete hook only. Tenancy and other policies still
apply, because `WithoutPolicy` is a separate key. Restoring a row is an
ordinary update under the skip, and the package doc shows it:
`db.Update[gen.Invoice]().Where(gen.Invoices.ID.EQ(id)).Set(gen.Invoices.DeletedAt.SetNull()).Exec(softdelete.Skip(ctx))`.
Without the skip, the interceptor would hide the row from the update.

**Hard delete** is `db.Delete[E]()...Exec(softdelete.Skip(ctx))`. Under
`Skip` it also reaches rows that are already soft-deleted, which is the point
of a purge.

### 4.3 Uniqueness with soft-deleted rows

A unique key over live rows only is a partial index. velox's index DSL has one
already: `index.Fields("org_id", "email").Unique().Where("deleted_at IS NULL")`.
PostgreSQL and SQLite build it (schema/index/index.go:50).

- **PostgreSQL, SQLite:** use the partial index. The package doc shows it,
  and the integration test inserts, deletes and re-inserts one email.
- **MySQL** has no partial index. velox writes no workaround, such as a
  generated column with a unique key on `(email, alive)`. The doc states the
  choice: either keep the key over every row, so a deleted row blocks reuse
  until it is purged or restored, or check uniqueness in a hook. velox-gen
  already rejects `Where` on MySQL where it does today. This spec changes
  nothing there.
- No helper builds the index. The fields are the entity's, and the index is
  one line.

Rejected: a velox-generated rewrite of every `Unique()` on a soft-delete
entity into a partial index. It is magic, it differs by dialect, and it
surprises in migrations.

## 5. Tenancy as a mixin

### 5.1 The reshaped `scope` package

`scope` keeps `NewKey`, `Key.With`, `Key.From`, `ErrNoScope` and
`ErrMismatch`. veloxotel and veloxgql map those errors. `By` now takes a
column name. It loses `Wrap`, since the runtime composes policies.

```go
// By returns the scope of E by its column named column, of Go type V, which
// must be Immutable and not nullable. Call it in a mixin's Policy or Hooks
// method; a column that is missing, of another type, mutable or nullable
// panics there, which is E's *SchemaError at its first use.
func By[E any, PE velox.Entity[E], V comparable](key *Key[V], column string) Scope[E, V]

// Policy confines reads, updates and deletes to the context's value and checks
// that an inserted row is in it. No value: ErrNoScope; another value: ErrMismatch.
func (s Scope[E, V]) Policy() velox.Policy[E]

// Fill sets the column of every inserted row that leaves it zero to the
// context's value.
func (s Scope[E, V]) Fill() velox.Hook[E]
```

`By` calls `velox.ColumnByName[E, V, PE](column)`, then checks `Immutable()`
and `!Nullable()`. Those checks restore, at load, two of the generate-time
refusals of `Scoped`.

### 5.2 The full user code (tracker)

```go
// examples/tracker/schema/inorg.go
package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/scope"
)

// OrgKey carries the caller's organization. A request sets it with
// OrgKey.With(ctx, id); a statement on an InOrg entity without it fails with
// scope.ErrNoScope.
var OrgKey = scope.NewKey[int64]("org")

// InOrg confines an entity's rows to the context's organization: reads,
// updates and deletes see only its rows, and an insert gets its id.
// velox.WithoutPolicy lifts it, with a reason, as it lifts every policy.
type InOrg struct{ mixin.Schema }

// Fields returns org_id. It is Immutable, so that no update moves a row to
// another organization.
func (InOrg) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Immutable()}
}

// Policy confines statements to the context's organization.
func (InOrg) Policy[E any, PE velox.Entity[E]]() velox.Policy[E] {
	return scope.By[E, PE](OrgKey, "org_id").Policy()
}

// Hooks fills org_id on insert.
func (InOrg) Hooks[E any, PE velox.Entity[E]]() []velox.Hook[E] {
	return []velox.Hook[E]{scope.By[E, PE](OrgKey, "org_id").Fill()}
}
```

The mixin is named `InOrg` because tracker already has an entity `Org`. Each
scoped entity drops its `org_id` field line and lists the mixin:

```go
func (Project) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}, InOrg{}} }
func (User) Mixin() []schema.Mixin    { return []schema.Mixin{InOrg{}} }
```

Mixin fields come first, in `Mixin()` order. So `mixin.Time{}, InOrg{}` keeps
today's column order of `created_at, updated_at, org_id, ...`, and the
committed migrations do not change. `Session` keeps its own unscoped `org_id`.
In examples/graphql the mixin's field also carries
`graphql.Skip(graphql.SkipMutationCreateInput, graphql.SkipMutationUpdateInput)`.

### 5.3 Size and failure modes compared

| | `schema.Scoped` (today) | velox mixin (this spec) | ent (privacytenant example) |
| --- | --- | --- | --- |
| User code | one annotation per scoped field | one 30-line file, and `InOrg{}` in each `Mixin()` (the field line moves into the mixin) | mixin (25 lines), filter rule (25), `entql` feature flag, viewer package |
| Insert fill | generated | `scope.Fill` | none: the caller sets the tenant |
| Typed | generated `scope.By` with gen's handles | generic, by-name column checked at load and at generate time | `entql.IntP` through an interface assertion that fails at run time (`unexpected filter type`) |
| Can another rule lift it? | no: Wrap is outermost | no: policies are ANDed | yes: an earlier `privacy.Allow`, such as the example's admin rule in `BaseMixin`, skips it |
| Wrong column shape | refused by velox-gen | refused at load (`scope.By`) and, for a literal name, by velox-gen | not checked |

**What can now go wrong that `Scoped` prevented, and the mitigations:**

1. **A mixin without its Policy method** leaves every entity open. Without
   its Hooks method, inserts fail closed, since `Policy.Insert` refuses a zero
   `org_id`. With `Scoped` neither could be dropped.
   - Mitigation: the code is written once, in one file, and reviewed once.
   - Mitigation: `scope/scopetest.Isolates[E]` is a conformance test the user
     runs per entity. It inserts in org A and expects org B to see nothing.
     It expects no value to give `ErrNoScope`, and an insert into another org
     to give `ErrMismatch`. It ships with the package.
   - Mitigation: gen/runtime's header lists, per entity, every bound Policy,
     Hooks and Interceptors and where each came from (§7). A missing `InOrg`
     Policy shows in review as a changed generated line.
2. **A wrong column name or type.** `Scoped` sat on the field, so this could
   not happen. Now it is refused by velox-gen for a literal and by load for
   any name. It is never skipped silently.
3. **A mutable or nullable tenant column.** `Scoped` refused it at generate
   time. Now `scope.By` refuses it at load, which is the entity's first use and
   so every test that touches it.
4. **One key on columns of two types.** This is a compile error, since `By`
   takes `*Key[V]` and the column's `V`. That is stronger than before.
5. **An entity that forgets the mixin** reads every org. It is the same
   failure as forgetting the annotation, so it is not new. A velox-gen warning
   for "an entity has `org_id` but not `InOrg`" was considered and rejected.
   Tracker's `Session` has an unscoped `org_id` on purpose, and a warning that
   is always on gets ignored.
6. **An entity's own Policy cannot lift the scope.** This is still true:
   composition is AND.

## 6. Ordering and composition

ent is followed in each of these: mixins first, in `Mixin()` order, each
mixin's list in its own order, then the entity's own.

- **Hooks:** concatenated. The first hook is outermost. So `InOrg`'s fill runs
  before the entity's hooks, which then see `org_id`, as with `Scoped`. A soft
  delete hook, being a mixin's, is outside the entity's delete hooks.
- **Interceptors:** each Filter runs in that order and the results are ANDed.
  The first error fails the statement.
- **Policies:** ANDed. `Read` is the AND of every non-nil `Read`, evaluated in
  order, and the first error or zero Pred ends evaluation. `Write` works the
  same way. `Insert` runs every non-nil `Insert` in order, and the first error
  refuses. velox's policies return conditions, not decisions, so there is no
  Allow that short-circuits. This differs from ent, where a mixin rule
  returning `privacy.Allow` skips the entity's rules. It is safer, and a
  mixin cannot weaken an entity.
- **`velox.WithoutPolicy(ctx, reason)`** lifts every policy, the mixins'
  included, so it lifts tenancy. It lifts no interceptor and no hook.
- **Named skips** belong to their mixins (`softdelete.Skip`) and lift only
  them.
- Errors name the position, "the Read policy [0] of gen.Project". gen/runtime's
  header maps the position to its source.

## 7. Generated code

### 7.1 Descriptor

The `one-shape` field names stay. Their types become lists, and `Scope`
goes (Phase 5):

```go
	// Policy, Hooks and Interceptors list the entity's behaviour: its mixins'
	// first, in Mixin order, then its schema's. velox calls each function once,
	// when it builds the metadata, and composes the results: policies ANDed,
	// hooks in order (the first outermost), interceptors ANDed.
	Policy       []func() Policy[E]
	Hooks        []func() []Hook[E]
	Interceptors []func() []Interceptor[E]
```

Each field is nil when the entity has none. Then `Load` instantiates no
policy, hook or interceptor generic for the entity.

### 7.2 tracker: `InOrg` plus `mixin.Time`

`mixin.Time` has no behaviour. Its defaults are `time.Now`, which gen calls by
name (0036 Review), so it adds nothing to the runtime.

`gen/runtime_velox.go`:

```go
type VeloxRuntime struct {
	CommentPolicy []func() velox.Policy[Comment]
	CommentHooks  []func() []velox.Hook[Comment]
	IssuePolicy   []func() velox.Policy[Issue]
	IssueHooks    []func() []velox.Hook[Issue]
	LabelPolicy   []func() velox.Policy[Label]
	LabelHooks    []func() []velox.Hook[Label]
	ProjectPolicy []func() velox.Policy[Project]
	ProjectHooks  []func() []velox.Hook[Project]
	UserPolicy    []func() velox.Policy[User]
	UserHooks     []func() []velox.Hook[User]
}
```

`gen/runtime/runtime.go`:

```go
// Code generated by velox-gen. DO NOT EDIT.

// Package runtime binds the schema package to package gen ... (as today)
//
// Behaviour, outermost first:
//
//	Comment  Policy: InOrg, Comment  Hooks: InOrg
//	Issue    Policy: InOrg, Issue    Hooks: InOrg
//	Label    Policy: InOrg           Hooks: InOrg
//	Project  Policy: InOrg, Project  Hooks: InOrg
//	User     Policy: InOrg           Hooks: InOrg
package runtime

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tracker/gen"
	sch "github.com/syssam/velox/examples/tracker/schema"
)

func init() {
	gen.SetVeloxRuntime(gen.VeloxRuntime{
		CommentPolicy: []func() velox.Policy[gen.Comment]{sch.InOrg{}.Policy[gen.Comment], sch.Comment{}.Policy},
		CommentHooks:  []func() []velox.Hook[gen.Comment]{sch.InOrg{}.Hooks[gen.Comment]},
		IssuePolicy:   []func() velox.Policy[gen.Issue]{sch.InOrg{}.Policy[gen.Issue], sch.Issue{}.Policy},
		IssueHooks:    []func() []velox.Hook[gen.Issue]{sch.InOrg{}.Hooks[gen.Issue]},
		LabelPolicy:   []func() velox.Policy[gen.Label]{sch.InOrg{}.Policy[gen.Label]},
		LabelHooks:    []func() []velox.Hook[gen.Label]{sch.InOrg{}.Hooks[gen.Label]},
		ProjectPolicy: []func() velox.Policy[gen.Project]{sch.InOrg{}.Policy[gen.Project], sch.Project{}.Policy},
		ProjectHooks:  []func() []velox.Hook[gen.Project]{sch.InOrg{}.Hooks[gen.Project]},
		UserPolicy:    []func() velox.Policy[gen.User]{sch.InOrg{}.Policy[gen.User]},
		UserHooks:     []func() []velox.Hook[gen.User]{sch.InOrg{}.Hooks[gen.User]},
	})
}
```

`gen/project_velox.go`, the descriptor:

```go
func projectDescriptor() velox.Descriptor[Project] {
	rt := veloxRuntime()
	return velox.Descriptor[Project]{
		Table:      "projects",
		Columns:    []velox.ColumnDescriptor{ /* id, created_at, updated_at, org_id, name */ },
		PrimaryKey: []int{projectColID},
		Indexes:    []velox.IndexInfo{ /* (org_id, name) unique */ },
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[Issue]("issues", projectColID, issueColProjectID)},
		Policy:     rt.ProjectPolicy,
		Hooks:      rt.ProjectHooks,
		FieldPtr:   func(x *Project, i int) any { /* as today */ },
	}
}
```

`scope_velox.go` and `gen.OrgKey` are gone. Programs write `schema.OrgKey`.

### 7.3 An entity with soft delete

tour's Invoice with `Mixin() { mixin.Time{}, InOrg{}, softdelete.Mixin{} }`
and its own Policy and Hooks:

```go
import "github.com/syssam/velox/schema/mixin/softdelete"

		InvoicePolicy:       []func() velox.Policy[gen.Invoice]{sch.InOrg{}.Policy[gen.Invoice], sch.Invoice{}.Policy},
		InvoiceHooks:        []func() []velox.Hook[gen.Invoice]{sch.InOrg{}.Hooks[gen.Invoice], softdelete.Mixin{}.Hooks[gen.Invoice], sch.Invoice{}.Hooks},
		InvoiceInterceptors: []func() []velox.Interceptor[gen.Invoice]{softdelete.Mixin{}.Interceptors[gen.Invoice]},
```

The descriptor gains `Interceptors: rt.InvoiceInterceptors,`.

**Plain mode.** Take a schema that does not import gen and whose only
behaviour comes from mixins, such as a `Note` with `softdelete.Mixin{}` alone.
Under 0037 it gets no gen/runtime. gen imports the mixin's package (and the
schema package, when the mixin is declared there) and writes the lists in the
descriptor:

```go
		Hooks:        []func() []velox.Hook[Note]{softdelete.Mixin{}.Hooks[Note]},
		Interceptors: []func() []velox.Interceptor[Note]{softdelete.Mixin{}.Interceptors[Note]},
```

No blank import is needed. That is a gain over ent, whose soft-delete mixin
imports the generated client and so always needs `ent/runtime`.

**A mixin value that is not the zero value.** velox-gen's loader runs
`Mixin()` and sees each value. velox-gen writes `sch.InOrg{}` when the value
is zero. Otherwise it reads the instance by position with a checked helper:

```go
schema.MixinAt[sch.Audited](sch.Project{}, 2).Hooks[gen.Project]
```

`schema.MixinAt[T](s schema.Interface, i int) T` is new. It panics with
`schema: Project.Mixin()[2] is softdelete.Mixin, not schema.Audited: run
velox-gen` when gen is stale. Using the zero value always would drop a
mixin's configuration silently, so it was rejected.

## 8. Compile cost

**Where the cost lands.** Generic methods are instantiated where they are
written, which is gen/runtime, or gen in plain mode. `E` is a struct, so each
entity is its own GC shape and gets its own instance. Per entity, the
instances are:

- the mixin methods (`InOrg.Policy[E]`, `InOrg.Hooks[E]`, and softdelete's two);
- `scope.By[E, PE, V]`, with its `Policy` closures and `Fill`;
- `ColumnByName`, plus the `Named` methods used. They are lazy
  (`[_ Lazy[V]]`), so only the called ones are compiled;
- `velox.On`, `MutateFunc`, the `Mutation[E]` methods used;
- for soft delete, `DB.Update[E, PE]` and the `UpdateQuery[E]` methods `Set`,
  `Where`, `All` and `Exec`.

**Estimate.** `policyGrowthMax` measures a Policy plus three hooks at about
289 KB per entity package, mostly export data. gen/runtime exports nothing,
so its share is smaller. 0036 measured +0.4 s CPU at N=100 with a Policy on
each entity. With `InOrg` and `softdelete` on all 100 entities, the estimate
is +0.4 to 0.8 s of user CPU over a schema without behaviour.

`Scoped` already instantiates `scope.By`, `Wrap` and `Fill` per entity, in
gen. So **against today's `Scoped` the delta should be near zero for
tenancy**. Most of the cost moves from gen to gen/runtime. gen/runtime is one
package, so its compile is serial. That matters for wall time.

**What to measure:**

1. `internal/repocheck` `TestGenericTypesCompileSmall`: a new row,
   `mixinBehaviour`. It is an entity package that instantiates a
   softdelete-like mixin (an interceptor plus a delete hook through
   `DB.Update`) and a scope mixin. Its maximum is the measured value plus
   `genericObjects`' margin, compared against `entityDescUse`.
2. bench/scale at N=100, in three shapes:
   - (a) today's `Scoped` on every entity;
   - (b) `InOrg` on every entity;
   - (c) `InOrg` plus `softdelete.Mixin` on every entity.

   For each, record the user CPU and max RSS of `go build ./gen/... ./schema`
   (cold, dependencies cached, five runs, median), as 0037's Review did. Also
   record the size of gen/runtime's `.a` file and the binary size of a program
   that uses one entity.
3. Gate: (b) at most 5% over (a), and (c) at most 15% over (a). Over the gate,
   erase earlier. `scope.By` and softdelete then build non-generic closures
   from a column index and an `any`-typed value, so the per-E instance is a
   thin wrapper.

## 9. Migrating this repo

Nothing is released, so nothing is kept for compatibility.

**Examples**

- **tracker:** add `schema/inorg.go`. Five entities list `InOrg{}`, and their
  `org_id` lines go. `auth.go` uses `schema.OrgKey`. README.
- **tenant:** Project uses `InOrg`. `scope_test.go` becomes a mixin test,
  using `scopetest.Isolates`. `main.go` and the README use `schema.OrgKey`.
- **tour:** Invoice and Line use `InOrg`. Invoice also takes
  `softdelete.Mixin`, so the tour shows both, and gets a partial unique
  `number` index where the dialect allows one. `main.go`'s role contexts use
  `schema.OrgKey`. Rewrite the comments in `invoice_behaviour.go` and
  `line_policy.go` ("the scope that Scoped generates...").
- **routing:** Note uses `InOrg`. `main.go`'s `WithShardFor` reads
  `schema.OrgKey.From`.
- **graphql:** Post uses `InOrg`, whose field carries `graphql.Skip()`.
  `main.go` and `bench_test.go` use `schema.OrgKey`.
- **hooks** (optional): `auth.CreatedBy`, today a generic hook that each
  entity lists, becomes a `CreatedBy` mixin with `Hooks`. 0034's consequence
  "mixins carry no behaviour" is gone.
- Regenerate every example's gen, plus `internal/testmodel` and
  `internal/testbind`. Golden files are updated with `-update`, and the diffs
  are read.

**Internal and contrib**

- `internal/testmodel/schema/behaviour.go` and `context.go`: an `InOrg`-like
  mixin replaces `invoiceScope` and `ticketScope`. `testmodel.Org` stays as
  the key.
- `internal/codegen`: remove `OutScope`, `OutScopeKey`, the `scoped`
  lowering, `scope_velox.go` rendering, the scope refusals, and the reserved
  `<Key>Key` and `<entity>Scope` names. Add mixin-behaviour lowering. Replace
  golden `testdata/golden/scoped` with `mixinbehaviour`, which covers runtime
  mode and plain mode. The sweep removes a stale generated `scope_velox.go`.
- `cmd/velox-gen/generate_test.go`: replace the Scoped cases.
- `contrib/graphql/internal/gogen/mutation.go` `leftOut` keys on
  `Annotations["Scoped"]`. It now treats a field whose create input is
  skipped explicitly as set by the application or a hook, and excludes it.
- `contrib/graphql/veloxgql` and `contrib/veloxotel` keep mapping
  `scope.ErrNoScope`, which stays.
- `gqlimpl/where.go` moves to `HasRowFilter`.

**API removed:** `schema.Scoped`, `schema.ScopedAnnotation`, `scope.Scope.Wrap`,
the old `scope.By(key, eq, field)`, `velox.Scoper` and `Descriptor.Scope`
(both from `one-shape`), and `Meta.HasReadPolicy` and `Table.HasReadPolicy`
(renamed).

**API added:** `velox.Interceptor`, `velox.Named`, `velox.ColumnByName`,
`Descriptor.Interceptors`, `schema.MixinAt`, `Meta.HasRowFilter`,
`Table.HasRowFilter`, the new `scope.By`, package `schema/mixin/softdelete`,
and package `scope/scopetest`. Update `internal/repocheck/testdata/api.txt`
with `-update-api` and read the diff.

**Records**

- New ADR **0038 "Mixins declare behaviour; interceptors filter rows"**.
- **0030:** superseded in full.
- **0029:** revised. Its core rule stands: the core has no tenant, and a
  scope is a Policy plus a hook. Only `scope.By`'s signature and `Wrap`
  change.
- **0034:** revised, at its consequence "Mixins still carry no behaviour".
- **0037:** revised. A schema whose only behaviour comes from mixins outside
  gen needs no gen/runtime.
- **0008:** unchanged. 0038 states that interceptors filter the same way.
- Update the ADR README index, docs/coming-from-ent.md, `schema/mixin`'s
  package doc ("carries fields... and nothing else"), `schema/doc.go`, and
  the root `doc.go` lines on scopes. Add a CHANGELOG entry for each phase.

**Generated-code version.** Each phase that changes what velox-gen writes
bumps `GeneratedCodeIsVersionN` with a CHANGELOG line. Phase 1 makes it 12
(list fields, `Interceptors`). Phase 3 makes it 13 (mixin instances,
`schema.MixinAt`). Phase 5 makes it 14 (no `Scope:`, no `scope_velox.go`).
`plugin.ModelVersion` is unchanged: the model JSON carries field annotations
generically, and Scoped's disappearance is only an annotation that is no
longer written.

**Coverage floors.** Add lines to `scripts/coverage-floors.txt` for
`schema/mixin/softdelete` and `scope/scopetest`.

## 10. Phases

Each phase merges on its own and passes `scripts/ci-local.sh`. `Scoped`
keeps working until Phase 5. In Phases 1 to 4 the runtime composes it as
`one-shape` does: `Scope.Wrap` around the AND of the policies, and
`Scope.Fill` before the hooks.

### Phase 1: interceptors and list fields (runtime, with entity-level codegen)

This phase adds `velox.Interceptor`, makes the Descriptor fields lists, and
has the runtime compose them. In the engine it adds `EntityInfo.filters` and
`RowCond`. It refuses interceptors on join and Bidi entities, renames
`HasRowFilter`, and handles the MySQL upsert refusal. A hand-written
`(*E).Interceptors` is bound, and an unbound one is refused. velox-gen binds
an entity schema's own `Interceptors()` method, which is not a mixin's yet.
This is Version12.

Tests:

- `internal/apitest`:
  - `TestInterceptorFiltersEveryRead`: Query, One, Count, Exists, Each, Iter,
    aggregates and GroupBy;
  - `TestInterceptorReachesRelations`: a With load (to-one, to-many,
    through), Has and HasWhere, and a to-one Join's ON;
  - `TestInterceptorReachesWrites`: Update, Delete, Save, and an upsert's
    update branch;
  - `TestWithoutPolicyKeepsInterceptors`;
  - `TestInterceptorRunsBeforePolicy`: the order in the SQL;
  - `TestInterceptorZeroPredFailsClosed`;
  - `TestInterceptorErrorIsNotADenial`;
  - `TestInterceptorRawSQLUnfiltered`;
  - `TestPoliciesComposeAND`: order, first error, no short-circuit;
  - `TestHooksComposeInOrder`;
  - `TestNilInterceptorRefused`;
  - `TestInterceptorOnJoinEntityRefused` and the Bidi refusal;
  - `TestUnboundInterceptorsMethodRefused`.
- `internal/engine`:
  - the render cache does not share a filtered statement across contexts;
  - the `scope_fuzz_test` corpus gains filters;
  - the `RowCond` cycle through a filter's `Has`.
- `internal/integration`: interceptor reach on PostgreSQL, MySQL and SQLite,
  including the MySQL `ErrPolicyUpsert` case.
- `gqlimpl`: a foreign-key filter on a target that has only an interceptor.
- `internal/codegen`: golden output for an entity's `Interceptors()`.
- `repocheck`: the API snapshot, and the existing compile-cost tests still
  pass.

### Phase 2: columns by name

This phase adds `velox.Named` and `velox.ColumnByName`, and publishes the
layout in `Load`.

Tests:

- `TestColumnByNameInsideLoad`: from a hand-written entity's Policy body;
- `TestColumnByNameWrongNameIsSchemaError`;
- `TestColumnByNameWrongTypeIsSchemaError`, covering V against `*V` both
  ways;
- `TestColumnByNameAfterLoad`: from a closure at statement time;
- `TestColumnByNameBeforeFirstUse`: it triggers the build;
- `TestColumnByNameConcurrentFirstUse`: run under `-race`, two entities
  whose policies name each other's columns;
- `TestNamedPtrSetNull`, and `Named` used in `Columns`, `Save` and
  `OnConflict`;
- a `NotLinkedError` retry with the layout published;
- the API snapshot.

### Phase 3: velox-gen binds mixin behaviour

velox-gen finds generic behaviour methods on mixins in any package with
go/types, and refuses wrong shapes. It writes the instance lists in
gen/runtime, or in the descriptor in plain mode, together with gen/runtime's
behaviour header. It adds `schema.MixinAt` for mixin values that are not
zero, and checks literal `ColumnByName` names. The loader's prune covers
mixin methods in the schema package. This is Version13.

Tests:

- `internal/codegen`:
  - golden `mixinbehaviour` in runtime mode and in plain mode;
  - `TestMixinMethodShapeRefused`: not generic, wrong type parameters,
    wrong result, a generic mixin type;
  - `TestMixinBehaviourOrder`: several mixins plus the entity;
  - `TestMixinNonZeroValueUsesMixinAt`;
  - `TestMixinColumnLiteralChecked`: a wrong name or type is refused at its
    line, and a non-literal name gives a warning;
  - `TestMixinInOtherPackage`;
  - `TestPlainModeMixinNeedsNoRuntime`.
- `cmd/velox-gen`:
  - `TestWrongMixinSignatureDoesNotCompile`;
  - the bootstrap: generate with gen deleted while a mixin has behaviour;
  - `TestMixinAtStalePanics`.
- `internal/codegen/testdata/e2e`: a mixin Policy, Hook and Interceptor run
  end to end.

### Phase 4: helpers

This phase reshapes `scope` (`By(key, column)`, with no `Wrap`), and adds
`schema/mixin/softdelete` and `scope/scopetest`. `internal/testmodel` moves
to a mixin.

Tests:

- scope:
  - `TestByRefusesMutableColumn` and `TestByRefusesNullableColumn`, each at
    load;
  - the existing `internal/apitest/scope_test.go`,
    `insert_scope_test.go` and integration `scope_test.go`, ported to the
    mixin;
  - `TestScopetestIsolates`, including a deliberately unscoped entity that
    it must fail.
- softdelete:
  - `TestDeleteSetsDeletedAt`: the SQL and the returned count;
  - `TestDeleteAll`;
  - `TestRedeleteAffectsZero`;
  - `TestUpdateSkipsDeleted`;
  - `TestReadsHideDeleted`: Query, Count, With, Has and Join;
  - `TestSkipShowsDeletedAndHardDeletes`;
  - `TestSkipKeepsTenancy`;
  - `TestWithoutPolicyKeepsSoftDelete`;
  - `TestSoftDeleteDryRunIsErrDryRun`;
  - `TestSkipSplitsBatcher` (RowScope);
  - integration on three dialects, with the partial unique index re-insert
    on PostgreSQL and SQLite.
- Coverage floors for the new packages.

### Phase 5: migrate the examples and remove `Scoped`

This phase migrates tracker, tenant, tour, routing and graphql (and hooks,
optionally). It removes `schema.Scoped`, the codegen scope code,
`Descriptor.Scope` and `velox.Scoper`. The sweep removes `scope_velox.go`, and
gqlgen's `leftOut` changes. It also adds ADR 0038, the ADR revisions, the
docs and the CHANGELOG. This is Version14.

Tests:

- each example's existing tests and golden files, regenerated;
- tenant's mixin test;
- the graphql e2e: `org_id` is absent from the inputs, and `leftOut` does
  not list it;
- `TestSweepRemovesScopeVelox`;
- the API snapshot;
- the docs tests: godoc on new exported methods, and no plan cited;
- `TestReadmeQuickStart`, if the README mentions scopes;
- `bash scripts/ci-local.sh --full` before any push.

## 11. Open points, decided here so implementation does not stall

- **Mixin method receivers** may be values or pointers. velox-gen writes
  `(&sch.InOrg{}).Policy[gen.X]` for a pointer receiver, as it does for
  schemas.
- **A mixin listed twice on one entity** is refused by velox-gen, as ent
  refuses duplicate fields. Its fields would clash anyway.
- **Ordering of the Time and tenant mixins** is the user's to choose. Listing
  `mixin.Time{}` first keeps the column order shown above. Hook order does
  not matter between them, since Time has none.
- **`WithoutPolicy` under no tenant value:** the fill does nothing, and the
  lifted `Insert` lets a zero `org_id` through. This is unchanged from today,
  and the doc says so.

## 12. Controller review (overrides earlier sections)

- **No `PE` in user-facing signatures.** A mixin's methods are `Policy[E any]()`, `Hooks[E any]()` and `Interceptors[E any]()`. `velox.ColumnByName[E, V]` and `scope.By[E]` find the metadata the way `metaOf[E]` does today, with the runtime assertion `any((*E)(nil)).(interface{ VeloxMeta() *Meta[E] })`. velox-gen instantiates only gen entities, so the assertion cannot fail in generated wiring. A hand-made misuse fails at first use with the existing "not an entity" error. Measure that this costs nothing at run time: it runs once per metadata build.
- **Name.** Call the handle constructor `velox.Field[E, V]("org_id")`, not `ColumnByName`, if that doesn't clash with an existing name; otherwise `velox.FieldOf`.
