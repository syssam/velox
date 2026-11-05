# Untyped behaviour: the schema declares it by column name; gen imports the schema

Status: design spec, 2026-11-05. Nothing is built yet.
Base: main at 6236c309, `GeneratedCodeIsVersion15`.
Supersedes: 0034's typing and package runtime, 0037's gen/runtime half,
0036's leftovers (typed binds, `NotLinkedError`, MetaOnce's retry).
Revises: 0038, 0029, 0007. New record: 0039 (next free in
`docs/decisions/README.md`).

## 0. Decision, as given

The user approved this direction:

- Behaviour declared in the schema is untyped and names columns by name, like
  ent's `ent.Policy`, `ent.Hook` and the generic `ent.Mutation`
  (`Field`/`SetField`).
- The schema package never imports gen.
- gen imports the schema package directly and binds `sch.Issue{}.Policy` in
  the descriptor.
- The `gen/runtime` package and its blank import are deleted.
- Mixin methods are plain, non-generic methods.
- Typed handles (`gen.Issues.AuthorID.EQ`) stay for application code.

The two complaints it answers:

1. `examples/tracker/schema/policy.go` imports gen, which is generated from
   the schema. A new project's schema does not compile before gen exists.
   The cycle is the reason for `gen/runtime`, `import _ ".../gen/runtime"`,
   the loader's prune overlay and `NotLinkedError`.
2. `func (InOrg) Policy[E any]() velox.Policy[E] { return scope.By[E](OrgKey,
   "org_id").Policy() }` is three layers deep. ent's mixin methods are not
   generic.

Prototypes are in
`/private/tmp/claude-501/-Users-s-y-s-Jobs-go-velox2/c1780402-cdf0-4c2f-8794-5840bbe2a6d1/scratchpad/untyped-proto/`
(Go 1.27.3): `v/` (the API's shape), `sch/` (a schema), `gen/` (a descriptor
binding method values) and `check/` (the go/types column scan of section 2).

## 1. Summary of the choices

| Question | Choice |
| --- | --- |
| Condition type | `velox.Cond`, an alias of `Pred[x]` for an unexported `x`. `velox.Or`, `And` and `Not` serve typed and untyped conditions alike. |
| Column by name | `velox.C("org_id")` returns a `velox.Named`, whose operators return a `Cond`. |
| Relation by name | `velox.Edge("doc")` returns a `velox.NamedEdge`: `Has()` and `HasWhere(cs...)`. |
| Policy | `velox.Policy{Read, Write, Insert}`. `Read` and `Write` return `(Cond, error)`. `Insert` takes a `velox.Row`. |
| Hook | `type Hook func(next Mutator) Mutator` over `*velox.Mutation`. `velox.Before(f, ops...)` is the hook that runs `f` and then next. |
| Interceptor | `velox.Interceptor{Filter func(ctx) (Cond, error)}`. |
| Mutation | `Op`, `Set`, `Fill`, `Value`, `Rows`, `Where`, `Conds`, `DB`, `Table`, `Affected`, `SetAffected`. Names resolve to column indexes once, at metadata build. |
| Typed behaviour | Removed: `Policy[E]`, `Hook[E]`, `Mutator[E]`, `MutateFunc[E]`, `Mutation[E]`, `Interceptor[E]`, `Named[E, V]`, `Field`, `Bind*`, `Bound*`, `NotLinkedError`, `Allow[E]`, the handles' `ValueIn`. |
| Run-time hooks | One narrow form: `velox.WithHooks[gen.Task](hooks...)`, a DB option, for a hook that must name gen's types. No run-time policy. |
| Generated code | gen imports the schema as `sch` and lists method values: `Policy: []func() velox.Policy{sch.InOrg{}.Policy, sch.Issue{}.Policy}`. One mode. |
| Column checks | velox-gen type-checks the schema and mixin packages with go/types and refuses a constant name a using entity lacks, at its line. A non-constant name is a warning. |
| Tenancy | The application's `InOrg` mixin, over `scope.Key` (`Require`, `Match`). `scope.By` and `Scope` are gone. |
| Version | `GeneratedCodeIsVersion16`, once, in Phase 2. |

## 2. Public types

### 2.1 Conditions

```go
// Cond is a condition that names its columns by name. It is resolved
// against the statement's entity when the statement runs.
type Cond = Pred[anyEntity] // anyEntity is unexported

func Allow() Cond               // no restriction
func C(name string) Named       // a column by its name, as the schema declares it
func Edge(name string) NamedEdge // a relation by its name, as the schema declares it

type Named = engine.Named // its fields are not exported
func (Named) EQ(v any) Cond
func (Named) NEQ(v any) Cond
func (Named) GT(v any) Cond
func (Named) GTE(v any) Cond
func (Named) LT(v any) Cond
func (Named) LTE(v any) Cond
func (Named) In(vs ...any) Cond
func (Named) NotIn(vs ...any) Cond
func (Named) Contains(s string) Cond
func (Named) HasPrefix(s string) Cond
func (Named) HasSuffix(s string) Cond
func (Named) IsNull() Cond
func (Named) NotNull() Cond

type NamedEdge = engine.NamedEdge
func (NamedEdge) Has() Cond
func (NamedEdge) HasWhere(cs ...Cond) Cond // cs resolve against the edge's target
```

- `velox.Or`, `velox.And` and `velox.Not` stay generic over `Pred[E]`. They
  take `Cond` by inference. The prototype confirms it, and confirms that
  mixing `Cond` with a typed `Pred[gen.Issue]` fails to compile, naming
  `velox.Cond`.
- Decided: `Cond` is an alias of a `Pred` instance, not an interface. One
  set of combinators then serves both worlds, and no untyped `AnyOf` is
  needed.
- Decided: `Allow` becomes the non-generic `Allow() Cond`. `Allow[E]` was used
  only in policies and interceptors, which are untyped now. Typed code that
  wants "true" writes `velox.And[gen.User]()`, as it can today.
- The zero `Cond{}` fails the statement, naming the entity, as the zero `Pred`
  does. A policy that forgot to return a condition still lets nothing through.
- A value is converted as `Table.Convert` converts: the column's own type as
  is; another type of the same kind class (integers of any size, floats,
  strings, a named string type for an enum) converted and range-checked.
  Another kind fails the statement:
  `velox: Issue: policy: author_id is an int64, not a string`.
- `EQ(nil)` on any column fails: `IsNull` says that. On a nullable column a
  non-nil value compares the value.
- Decided: `Named` is the name freed by removing `Named[E, V]`. `Col[E, V]` and
  `Column[E]` are taken by the handles.
- Decided: `velox.Edge` names the relation by the schema's edge name
  (`edge.From("doc", Doc.Type)`), as `C` names a column by the schema's field
  name, and as ent's `entql.HasEdge("doc")` does. The generated `EdgeTo`,
  `EdgeBidi` and `EdgeThrough` take that name instead of the Go name
  (`"doc"`, not `"Doc"`). Errors print it.

### 2.2 Policy

```go
type Policy = engine.Policy

// engine.Policy:
type Policy struct {
	Read   func(ctx context.Context) (Cond, error) // ANDed into every read, update, delete, Save, upsert update branch
	Write  func(ctx context.Context) (Cond, error) // ANDed into every update, delete, Save, upsert update branch
	Insert func(ctx context.Context, r Row) error   // each inserted or upserted row, after hooks, defaults, validators
}
```

- Semantics are today's `Policy[E]`, field for field. ADR 0008 holds: a row
  `Write` hides is left alone, and an update that matches only such rows
  affects 0 rows.
- Decided: keep the names `Read`, `Write` and `Insert`. `Read` already covers
  reads, updates and deletes. `Filter` is the Interceptor's field, and
  `WithoutPolicy` does not lift an interceptor, so one name for both would
  blur the one difference that matters.
- An `Insert` that calls `r.Set` is a misuse and fails the insert. A policy
  decides; it does not change the row (0007).

### 2.3 Row

```go
type Row = engine.Row // a row of an insert; a value, cheap to copy

func (Row) Value(name string) any            // the column's value; nil for NULL; a nullable column's value is not a pointer
func (Row) Set(name string, v any) error     // v converted as a Cond's value; nil sets NULL on a nullable column
func (Row) IsZero(name string) bool          // the column holds its Go zero value (nil for a nullable column)
func (Row) Entity() any                      // the row as its *E, for application code that can name E
```

- `Entity` serves run-time hooks (2.6), which live in application code and
  may write `x := r.Entity().(*gen.Task)`.

### 2.4 Hook and Mutation

```go
type Mutator interface{ Mutate(ctx context.Context, m *Mutation) error }
type MutateFunc func(ctx context.Context, m *Mutation) error
type Hook func(next Mutator) Mutator

func (f MutateFunc) Mutate(ctx context.Context, m *Mutation) error
func On(h Hook, ops ...Op) Hook                // as today: h only for the ops listed
func Before(f MutateFunc, ops ...Op) Hook      // On of the hook that calls f, then next when f returned nil

type Mutation = engine.Mutation
func (*Mutation) Op() Op                          // OpInsert, OpUpdate or OpDelete
func (*Mutation) Rows() iter.Seq[Row]             // an insert's rows; none for an update or delete
func (*Mutation) Set(name string, v any) error    // insert: every row; update: replaces the assignment; delete: misuse
func (*Mutation) Fill(name string, v any) error   // insert: each row whose column IsZero; else misuse
func (*Mutation) Value(name string) (any, bool)   // update: the value Set assigns; false when not assigned or Add/Sub; insert: misuse
func (*Mutation) Where(cs ...Cond)                // update or delete: narrows; insert: misuse
func (*Mutation) Conds() []Cond                   // as today's Conds
func (*Mutation) DB() *DB                         // as today
func (*Mutation) Table() Table                    // the entity's Table, for a hook that makes the write itself
func (*Mutation) Affected() int64                 // as today
func (*Mutation) SetAffected(n int64)             // as today
```

Today's semantics of `Hook[E]` stay, word for word, in the new doc comment:

- The first hook listed is the outermost. velox builds the chain anew for
  each write, so a hook may keep per-write state in its closure.
- A hook calls next once, or not at all to refuse or to make the write
  itself. A second call fails the write.
- Insert and InsertAll run the hooks once over all their rows. Update,
  delete, Save and their `SQL(ctx)` run them too; under `SQL(ctx)` next renders
  and `IsDryRun(ctx)` is true.
- The guard (`ErrNoWhere`) runs before the hooks and again inside next.
- next fills defaults, adds `UpdateDefault` assignments, refuses an
  `Immutable` column, runs the validators and the policy, and then sends. So
  validators and `Policy.Insert` see what hooks set.
- An error velox returns from next passes back unchanged; another error is
  wrapped `velox: <Entity>: hook: <err>`.
- A misuse (a wrong name, a value of another kind, `Set` on a delete,
  `Value` on an insert, `SetAffected` with next) is recorded and fails the
  write before next sends it, or when the hook returns, as today.

Decided: `Before(f, ops...)` replaces the target's `OnCreate`. It names when
`f` runs relative to next, and it takes the ops as `On` does. With no op it
runs on no write, as `On` does.

Decided: `Mutation` is passed as a pointer, as today. The state lives in
place, and next sees what the hook set.

Decided: `Fill` is new. Tenancy needs "set it where the caller left it zero":
an insert under `WithoutPolicy` may name another organization, and a row that
names a different one is refused by the policy's `Insert` with
`scope.ErrMismatch`, as `scope.Fill` and `scope.Policy` behave today.

### 2.5 Interceptor

```go
type Interceptor = engine.Interceptor
// engine.Interceptor:
type Interceptor struct{ Filter func(ctx context.Context) (Cond, error) }
```

Semantics are today's. The conditions come before the Read policy's.
`WithoutPolicy` does not lift them. A nil `Filter` is refused at metadata
build, by its index.

### 2.6 Run-time hooks: `velox.WithHooks`

```go
func WithHooks[E any, PE Entity[E]](hs ...Hook) Option
```

```go
db, err := velox.Connect("sqlite", dsn, velox.WithHooks[gen.Task](velox.On(auditRename, velox.OpUpdate)))
```

- Recommendation: application code gets this one form, and nothing else.
  The schema cannot name gen's types, so a hook that inserts a `gen.Audit` or
  reads a `gen.Issue` with the typed API has no home in the schema. ADR 0007
  says such a rule is a hook that queries through `Mutation.DB`. Without
  `WithHooks`, the hooks example's audit could not be written.
- The hooks are the untyped `Hook`. Their closures use gen freely, since they
  live in application code. There is no typed `Hook[E]` and no typed
  `Mutation[E]`.
- They belong to the DB, and to every `Tx` and shard it opens, as ent's
  `client.User.Use`.
- Decided: they run after the schema's hooks, innermost. A mixin's `Fill` has
  run when they see the rows, and they cannot skip it. ent runs client hooks
  outermost; velox's order keeps the schema's invariants first.
- Decided: no run-time policy or interceptor. A policy decides from the
  context and needs no gen type, so the schema can always declare it.
- `E` instantiates one small function per entity, in the application's
  package, not in gen.

### 2.7 What is removed

Nothing is kept for compatibility (before 1.0).

| Removed | Why, or what replaces it |
| --- | --- |
| `Policy[E]`, `Interceptor[E]` | `Policy`, `Interceptor` |
| `Hook[E]`, `Mutator[E]`, `MutateFunc[E]`, `Mutation[E]`, `On[E]` | `Hook`, `Mutator`, `MutateFunc`, `Mutation`, `On` |
| `Named[E, V]`, `Field[E, V]` | `C(name)`. A mixin no longer needs a typed handle by name. |
| `BindPolicy`, `BindHooks`, `BindInterceptors`, `BoundPolicy`, `BoundHooks`, `BoundInterceptors` | Nothing is erased: the descriptor lists untyped method values. |
| `NotLinkedError`, engine's `unlinkedError`, `Unlinked`, MetaOnce's retry | No package binds gen late. Removed in Phase 4. |
| `Allow[E]()` | `Allow() Cond` |
| the handles' `ValueIn(m *Mutation[E])` (25 methods in `handles.go`) | `m.Value(name)`. `go generate .` drops them. |
| `Lazy[E]` on `Mutation[E]`'s methods, `Lazy[V]` on `Named[E, V]`'s | Their types go. `Lazy` stays for the handles, `Meta` and `MetaOnce`. |
| `scope.By`, `scope.Scope`, `Scope.Policy`, `Scope.Fill` | `InOrg` in the application, over `Key.Require` and `Key.Match`. |

`Descriptor`'s fields become:

```go
Policy       []func() Policy
Hooks        []func() []Hook
Interceptors []func() []Interceptor
```

- `Load` calls each function once, at metadata build, as today, under the
  same recovery. A panic, a nil function, a nil hook or a nil `Filter` is the
  entity's `*SchemaError`.
- The engine's refusal of an unbound `Policy`, `Hooks` or `Interceptors`
  method on `*E` stays, for entities written by hand. Such an entity binds
  `Policy: []func() velox.Policy{(*E)(nil).Policy}`.
- Decided: the new types are engine types under root aliases, and the
  methods are engine code (0020, 0035). velox's export data then carries no
  inlinable bodies of them, which every importer would read.

### 2.8 Package scope, scopetest and softdelete

`scope` after:

```go
var ErrNoScope = errors.New("velox/scope: the context has no scope value")
var ErrMismatch = errors.New("velox/scope: the row is not in the context's scope")

type Key[V comparable] struct{ /* name */ }
func NewKey[V comparable](name string) *Key[V]
func (k *Key[V]) With(ctx context.Context, v V) context.Context // records the value with velox.WithScopeValue, as today
func (k *Key[V]) From(ctx context.Context) (V, bool)
func (k *Key[V]) Require(ctx context.Context) (V, error)        // new: ErrNoScope, wrapped with the key's name
func (k *Key[V]) Match(ctx context.Context, v any) error        // new: ErrNoScope, or ErrMismatch when v is not the context's value
```

- `ErrNoScope` and `ErrMismatch` stay: veloxgql's presenter and gqlcore, and
  veloxotel, map them.
- `Key.With` stays the way a request sets the value. It records it for the
  GraphQL batcher (`WithScopeValue`). That is why `InOrg` reads the key, not
  a context helper of its own.

`scopetest.Isolates[E, V](t, ctx, db, key, column, a, b, row)` keeps its
signature. It finds the column with `TableOf[E]().Info().Column(column)` and
sets it with `Table.SetField`, not with `velox.Field`.

Decided: the Immutable and NOT NULL check of the scope column moves to
`scopetest.Isolates`. The mixin declares the column itself, so the code that
makes it Immutable sits beside the code that filters on it, and no runtime
helper names a column on the mixin's behalf any more. A test that calls
`Isolates` fails on a mutable or nullable column, with today's message.

`softdelete.Mixin` after:

```go
// Interceptors returns the interceptor that hides rows whose deleted_at is
// set, but under Skip.
func (Mixin) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(ctx context.Context) (velox.Cond, error) {
		if Skipped(ctx) {
			return velox.Allow(), nil
		}
		return velox.C(Column).IsNull(), nil
	}}}
}

// Hooks returns the hook that makes a delete an update that sets
// deleted_at, but under Skip.
func (Mixin) Hooks() []velox.Hook {
	return []velox.Hook{velox.On(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			if Skipped(ctx) {
				return next.Mutate(ctx, m)
			}
			n, err := softDelete(ctx, m, time.Now())
			m.SetAffected(n)
			return err
		})
	}, velox.OpDelete)}
}

// softDelete updates the rows the delete would remove, or every row when
// the delete said All.
func softDelete(ctx context.Context, m *velox.Mutation, now time.Time) (int64, error) {
	t := m.Table()
	col, _ := t.Info().Column(Column)
	set, err := t.Assign(col, now)
	if err != nil {
		return 0, err
	}
	u := m.DB().UpdateTable(t).Assign(set)
	if cs := m.Conds(); len(cs) > 0 {
		u = u.Where(velox.Predicates(cs)...)
	} else {
		u = u.All()
	}
	return u.Exec(ctx)
}
```

`velox.Predicates(cs []Cond) []Predicate` converts for the untyped surface.
A resolved condition carries its entity, so one used on another entity's
statement fails it, as 0035's untyped conditions do. `softdelete.Skip` and its
semantics are unchanged.

## 3. Column checks

### 3.1 Runtime: fail closed

- At metadata build, velox builds the entity's name index: column name to
  column index, and edge name to relation index.
- A `Cond` leaf that names a column resolves when its statement is prepared
  (5.1). An unknown name, a value of another kind, a text operator on a
  column that is not text, or `IsNull` on a NOT NULL column fails the
  statement before anything is sent:
  `velox: Issue: policy: no column "authr_id"`.
- `Mutation.Set`, `Fill`, `Value`, and `Row.Value`, `Set` and `IsZero` with an
  unknown name or a wrong kind record a misuse, and the write fails.
- `velox.Edge` on a relation whose target is not linked (2.1, 4.4) fails the
  statement. Nothing is ever filtered on nothing.

### 3.2 velox-gen: refuse a wrong constant at its line

velox-gen type-checks the schema package, and each package that declares a
mixin with behaviour, from source, with go/types. It imports their
dependencies from the export data `go list -export -deps` already lists for
the loader (`exports` in `internal/codegen/discover.go`). The schema no longer
imports gen, so the check needs no gen and is sound. The prototype
(`check/main.go`) does this with the standard library alone, generic methods
included.

Checked call forms, each found by the callee go/types resolves, not by the
text of the call:

| Call | Name checked | Value checked |
| --- | --- | --- |
| `velox.C(name)` | a column | the operator called on the call itself: `EQ`, `NEQ`, `GT`, `GTE`, `LT`, `LTE` (v); `In`, `NotIn` (each v); `Contains`, `HasPrefix`, `HasSuffix` (a text column); `IsNull`, `NotNull` (a nullable column) |
| `velox.Edge(name)` | a relation; the name must be constant (4.4) | the `C` calls inside `HasWhere(...)`'s arguments are checked against the edge's target |
| `(*velox.Mutation).Set(name, v)`, `.Fill(name, v)` | a column | v |
| `(*velox.Mutation).Value(name)` | a column | |
| `velox.Row.Value(name)`, `.IsZero(name)` | a column | |
| `velox.Row.Set(name, v)` | a column | v |

- A name is checked when go/types gives it a constant value: a literal, or a
  named string constant (`softdelete.Column`).
- A value is checked when its type is known and is not an interface. An
  untyped constant must be representable in the column's type (300 for an
  int8 column is refused). A typed value must be of the column's kind class,
  as `Table.Convert` accepts. `nil` is accepted only by `Set` and `Fill` on a
  nullable column. A value held in `C`'s result stored in a variable is not
  checked.
- Which entities a call is checked against: the roots are the entities'
  `Policy`, `Hooks` and `Interceptors` methods, and the mixins' methods, each
  with the entities that use it (an entity's own; every entity that lists
  the mixin). A call is checked against every root that reaches it: its body,
  and, transitively, the package-level functions, methods and variables of the
  same package that body names. So the tracker's `member` and `authoredBy`
  helpers are checked against Project, Issue and Comment.
- A call in another package (a helper package of the application's) is not
  checked. It is checked at run time.
- Refusal: an `*Error` at the call's line, naming the entity and the root:
  `schema/policy.go:41: velox.C("authr_id"): Issue has no column "authr_id"
  (Issue.Policy)`, or, for a mixin,
  `inorg.go:30: velox.C("org_id"): Session, which lists InOrg, has no column "org_id"`.
  All refusals are joined, as the loader joins its own.
- Warning: a name that is not constant:
  `inorg.go:30: velox.C(col) names its column with a value velox-gen cannot
  read; velox checks it when the statement runs`. Today's `velox.Field`
  warning is the model.

Phase 3 of the 2026-11-04 spec checked a literal `velox.Field[E, V]` name and
its `V`, by parsing. This check replaces it and covers every call form above.

## 4. Generated code

### 4.1 One mode

- gen imports the schema package, as `sch`, when it binds something from it:
  a behaviour method, a mixin's, or a closure default or validator. This is
  today's plain mode, made the only mode.
- velox-gen refuses a schema package that imports gen, directly (a
  `*FileError` at the import) or through other packages (an error naming the
  chain, from `genImports`):
  `schema imports github.com/x/gen: the schema declares behaviour by column
  name and does not use gen, which imports the schema; move the code that
  needs gen out of the schema package`.
- Removed from velox-gen: `runtime.go` (`renderRuntimeHolder`,
  `RuntimeImport`, `NotLinkedMessage`), `OutBind` and `OutEntity.Binds`,
  `Config.SchemaImportsGen` and `Out.SchemaImportsGen`, `Out.Runtime`, every
  branch on them in `lower.go`, `render.go` and `mixinlower.go`, the
  `veloxRuntime()` call in each descriptor, the warning that no file imports
  gen/runtime, and verify's link of package runtime.
- The sweep removes a generated `gen/runtime/runtime.go` and
  `gen/runtime_velox.go`, as it removed `scope_velox.go`. A user file left in
  `gen/runtime` stays, and velox-gen names it.
- Decided: the behaviour header ("Behaviour, outermost first") is dropped,
  not moved to `gen/velox.go`. Each descriptor's `Policy`, `Hooks` and
  `Interceptors` lines list the sources in order, in the file a reader opens
  for the entity.
- `gen/velox.go`'s package doc loses its runtime paragraph:
  `// Package gen holds the entities of <schema path>, generated by velox-gen.`

### 4.2 Defaults and validators

They are bound today in two ways (`schemaFunc` and `schemaValue` in
`internal/codegen/lower.go`):

- in plain mode, a named function by its name (`time.Now`, `sch.NewSlug`),
  and a closure read once, in the descriptor function, with
  `schema.FieldDescriptor(&sch.X{}, "name")`;
- in runtime mode, a field of `VeloxRuntime` that gen/runtime set.

Decided: keep plain mode's two forms and delete the runtime form. A closure
has no name to call, so reading it from the field's descriptor is the
shortest way that exists. It is read once per metadata build, as today.

### 4.3 Behaviour binding

Each source is a method value, mixins first, in `Mixin()` order, then the
entity's own:

```go
Policy:       []func() velox.Policy{sch.InOrg{}.Policy, sch.Issue{}.Policy},
Hooks:        []func() []velox.Hook{sch.InOrg{}.Hooks},
Interceptors: []func() []velox.Interceptor{softdelete.Mixin{}.Interceptors},
```

- A pointer receiver binds `(&sch.Issue{}).Policy`. A mixin `Mixin()` returns
  as a non-zero value binds `schema.MixinAt[sch.InOrg](sch.Project{}, 1).Policy`,
  as today.
- A wrong signature fails to compile at that line in gen. velox-gen refuses
  it earlier, by shape (4.5).

### 4.4 Linked edges

A relation's descriptor does not link its target's metadata (rule 2 of the
compile-cost rules): a program that links Comment does not link Doc. But
`velox.Edge("doc").Has()` in Comment's policy must apply Doc's Read policy,
or fail. Decided:

- velox-gen finds, with the scan of 3.2, every `velox.Edge(name)` that an
  entity's behaviour reaches, and wraps that relation:
  `velox.Linked[Doc](velox.EdgeTo[Doc]("doc", commentColDocID, docColID))`.
  `Linked` records `PT(nil).VeloxMeta()`, as `EdgeThrough` records its join
  entity.
- The name of `velox.Edge` must be a constant. velox-gen refuses another,
  at its line, since it could not link the target.
- At run time, an `Edge` leaf on a relation that is not linked fails the
  statement: `velox: Comment: policy: relation doc's target is not linked:
  velox-gen links it when the schema names it with velox.Edge`.

So Phase 2 already holds the go/types scan, for the edges. Phase 3 adds the
column and value checks to it.

### 4.5 velox-gen's shape checks

- An entity's method: `func (X) Policy() velox.Policy`,
  `func (X) Hooks() []velox.Hook`, `func (X) Interceptors() []velox.Interceptor`,
  value or pointer receiver. A join entity's goes on an empty struct of its
  name, as today.
- A mixin's method has the same shape. A generic one is refused:
  `InOrg.Policy: behaviour methods are not generic: func (InOrg) Policy() velox.Policy`.
- The old typed shape (`velox.Policy[gen.Issue]`, `Policy[E any]()`) is
  refused with the new shape in the message.
- Decided: velox-gen keeps reading these from the source
  (`SchemaMethods`, `MixinBehaviour`). The loader cannot see a join entity's
  type, and the source gives the positions.

### 4.6 The tracker's Issue descriptor, after

```go
// issueDescriptor describes Issue to velox: its table, columns, edges and behaviour.
func issueDescriptor() velox.Descriptor[Issue] {
	return velox.Descriptor[Issue]{
		Table: "issues",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"},
			{Field: "CreatedAt", Name: "created_at", DefaultFunc: func() any { return time.Now() }, Immutable: true},
			{Field: "UpdatedAt", Name: "updated_at", DefaultFunc: func() any { return time.Now() }, UpdateDefault: func() any { return time.Now() }},
			{Field: "OrgID", Name: "org_id", Immutable: true},
			{Field: "ProjectID", Name: "project_id"},
			{Field: "AuthorID", Name: "author_id"},
			{Field: "AssigneeID", Name: "assignee_id"},
			{Field: "Title", Name: "title"},
			{Field: "Body", Name: "body"},
			{Field: "Status", Name: "status", Default: IssueStatusOpen},
		},
		PrimaryKey: []int{issueColID},
		Indexes: []velox.IndexInfo{
			{Columns: []int{issueColProjectID, issueColStatus, issueColID}}, // project_id, status, id
		},
		Edges: []velox.EdgeDescriptor{
			velox.EdgeTo[Project]("project", issueColProjectID, projectColID),
			velox.EdgeTo[User]("author", issueColAuthorID, userColID),
			velox.EdgeTo[User]("assignee", issueColAssigneeID, userColID),
			velox.EdgeTo[Comment]("comments", issueColID, commentColIssueID),
			velox.EdgeThrough[IssueLabel, Label]("labels", issueColID, issueLabelColIssueID, issueLabelColLabelID, labelColID),
		},
		Policy: []func() velox.Policy{sch.InOrg{}.Policy, sch.Issue{}.Policy},
		Hooks:  []func() []velox.Hook{sch.InOrg{}.Hooks},
		FieldPtr: func(x *Issue, i int) any {
			// unchanged
		},
		EdgeFieldPtr: func(x *Issue, i int) any {
			// unchanged
		},
		Check: func(col int, v any) error {
			// unchanged
		},
	}
}
```

Its imports gain `sch "github.com/syssam/velox/examples/tracker/schema"`.
The `rt := veloxRuntime()` line, `gen/runtime_velox.go` and
`gen/runtime/runtime.go` are gone. `main.go` loses
`_ "github.com/syssam/velox/examples/tracker/gen/runtime"`.

### 4.7 Import graph

- `gen → schema → {velox, velox/schema/..., velox/scope, principal}`. No
  cycle. `principal` must not import gen, and does not. A package the schema
  imports that imports gen is refused, with the chain.
- The schema's tests: an external test (`package schema_test`) may import
  gen. An internal one that does fails under `go test` with go's own cycle
  error. velox-gen does not read test files for the refusal.
- A field's `GoType` may now be declared in the schema package itself. 0034's
  consequence ("goes in a package of its own") is lifted.
- A program that links gen links the schema package and the DSL packages
  it imports, as plain mode does today. The linker keeps only the methods the
  descriptors name (method values, not interface conversions), plus
  `Fields` and the rest for an entity whose closures are read through
  `schema.FieldDescriptor`.
- GraphQL: the schema imports `contrib/graphql`'s annotation package (stdlib
  and `velox/schema` only), so gen imports it through the schema, as the
  graphql example's plain-mode gen already does. velox-graphql writes
  `*_gql.go` into gen, which import veloxgql and not the schema. gqlgen's
  `graph` imports gen and autobinds gen's types. `go -C examples/graphql tool
  gqlgen generate` must produce no diff (a Phase 2 check).

### 4.8 Loader

The schema never imports gen, so the loader's workarounds have no case left:
`prune` and the overlay (`writeOverlay`, `unoverlay`) and `listCycle`. They
stay harmless in Phase 2 and are removed in Phase 4. A new project generates
gen from a schema with behaviour and no gen directory; Phase 2 tests it.

## 5. Runtime cost

### 5.1 Conditions

- A by-name leaf is its own engine op. Its `W` holds the name until it is
  resolved, then the entity, as `Table.Pred`'s leaves hold it. `Node` gains no
  field, so it does not grow.
- `group` propagates a `ByName` flag, in `Node`'s padding beside `Deep`.
- The engine resolves in one place: when a statement is prepared, it walks
  every condition attached to it (the caller's `Where`, the interceptors', the
  policy's, a hook's) whose tree has the flag. A typed statement has none and
  pays one flag test.
- Resolving a leaf is one map lookup in the entity's name index and a
  `Convert`, whose common case (the column's own type) is a type comparison.
- The resolved tree is the one the typed handle builds, so the render cache
  key and the SQL are the same (Phase 1 tests it).
- Decided: resolve per statement, not once. A policy function builds its
  `Cond` anew for each statement, from the context, so there is nothing to
  resolve ahead. A per-column cache keyed by the `Named` value would cost
  more than the map lookup it saves.
- Budget, measured in Phase 1: a scoped SELECT with a by-name policy within
  3% of the same SELECT with the typed policy (`BenchmarkPolicyByName`
  against `BenchmarkPolicyByIndex`, SQLite in memory, benchstat).

### 5.2 Mutations

- At metadata build, velox builds per column a setter
  `func(row unsafe.Pointer, v any) error`. It knows the field's offset. For
  int64, int32, int, string, bool, float64, time.Time and their pointers it
  stores after a type assertion. For other types it falls back to reflect.
- `m.Set(name, v)` on an insert is one lookup and, per row, one setter call.
  On an update it is one lookup, a `Convert` and today's `MutateSet`.
- `Fill` adds a zero test per row, read through the same offset.
- A wrong kind fails the write:
  `velox: Issue: hook: Set("org_id", "7"): org_id is an int64, not a string`.
- Budget, measured in Phase 1: `BenchmarkSetByName` within 20 ns per row of
  a typed store through the field's pointer.

## 6. Compile cost

The typed path instantiated, per entity with behaviour, `BindPolicy[E]`,
`BindHooks[E]`, `On[E]`, `MutateFunc[E]`, `Mutation[E]`'s methods,
`policyOf[E]` and `erase[E]`, plus, per entity listing a generic mixin, the
mixin's methods, `velox.Field[E, V]`, `Named[E, V]`'s methods and
`scope.By[E, V]`. Untyped behaviour instantiates nothing per entity. Its
bodies compile once, in the schema package (or the mixin's), and an entity's
package holds a slice of method values.

Expected effect on `TestGenericTypesCompileSmall`, against the bounds in
`internal/repocheck/compilecost_test.go`. The sources are rewritten untyped,
the bodies left as they are:

| Row | Today (measured) | Expected after |
| --- | --- | --- |
| `entityDescriptor` | 141368 | about 141 KB; unchanged within 1 KB |
| `entityDescUse` | 769648 | about 768 KB; `ValueIn` leaves the handles' export data |
| `entityDescHooks` | 1057508 | about 780 to 790 KB: entityDescUse plus the non-generic closures |
| `entityDescMixin` | 1132078 | about entityDescHooks; the 74.5 KB mixin premium goes |
| policy and hooks (`policyGrowthMax`) | 287860 | under 20 KB |
| `Named`, `Policy`, `Mutation` rows | 19402, 4050, 2226 | rows removed; their types are not generic |

- Phase 2 measures each, sets each bound to the measurement plus the file's
  margin rule, and rewrites the comments. That is a drop of about 270 KB per
  entity with behaviour, and about 345 KB per entity with a mixin's.
- bench/scale's schemas declare no behaviour (`bench/scale/main.go` has no
  `Policy` or `Mixin`). Its thresholds do not move.
- 0037's Review variant (N=100, a `Policy` per entity) loses gen/runtime
  (3.56 MB of archive and one package's compile). gen stays within 1%: each
  descriptor swaps `rt.XPolicy` for a slice of method values and loses
  `veloxRuntime()`. The schema package loses its `Pred[gen.X]` and
  `Policy[gen.X]` instances. The build's critical path shortens from
  gen → schema → gen/runtime to schema → gen. Phase 4 re-measures it and
  records the numbers in 0039's Review.
- velox-gen gains one go/types check of the schema package and its mixin
  packages against export data the loader already lists, and loses the
  overlay build.

## 7. Semantics to preserve

| Semantics | How it holds after | Test (Phase 2 unless noted) |
| --- | --- | --- |
| Fail closed | The zero `Cond`, a nil function or hook, an unknown name, a wrong kind and an unlinked edge each fail the statement or the write. No package can be left unlinked any more. | `TestZeroCondFails`, `TestUnknownColumnFailsClosed` (policy, interceptor, hook), `TestEdgeNotLinkedFails` |
| Policies ANDed | `engine.AndPolicies`, unchanged. None lifts another. | `TestPoliciesANDedInOrder` |
| Mixins first, in Mixin order | velox-gen lists sources mixins first; `Load` composes in order. | codegen golden `mixinbehaviour`; `TestHookOrderMixinsFirst` |
| Interceptors not lifted by WithoutPolicy | Engine unchanged. | `TestWithoutPolicyKeepsInterceptors` (adapted from `interceptor_compose_test.go`) |
| softdelete.Skip | Unchanged. | `schema/mixin/softdelete` tests, adapted |
| The WithoutPolicy audit | `Statement.PolicyBypass` for `Rewrite` and `Around`, unchanged. | existing engine tests |
| ErrNoScope | `Key.Require`; `scopetest` checks it. | `TestKeyRequire`, `TestKeyMatch`, `scopetest` tests |
| ADR 0008, write filters | Engine unchanged; `Write` returns a `Cond`. | `TestWriteFiltersAffectZero` |
| Hooks before policy; validators and `Insert` see hook values | Engine unchanged. | `TestInsertPolicySeesHookFill` |
| Tenant column Immutable and NOT NULL | The mixin declares it; `scopetest.Isolates` checks it. | `TestIsolatesRefusesMutableColumn`, `TestIsolatesRefusesNullableColumn` |
| Unbound method on a hand-written entity | `unboundMethods`, unchanged. | existing engine test, adapted |

## 8. Examples after

### 8.1 tracker

`schema/inorg.go`:

```go
package schema

import (
	"context"

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
// velox.WithoutPolicy lifts the policy, with a reason, as it lifts every
// policy.
type InOrg struct{ mixin.Schema }

// Fields returns org_id. It is Immutable, so that no update moves a row to
// another organization.
func (InOrg) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Immutable()}
}

// Policy confines reads, updates and deletes to the context's organization,
// and refuses an insert into another one.
func (InOrg) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Cond, error) {
			org, err := OrgKey.Require(ctx)
			if err != nil {
				return velox.Cond{}, err
			}
			return velox.C("org_id").EQ(org), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error {
			return OrgKey.Match(ctx, r.Value("org_id"))
		},
	}
}

// Hooks fills org_id on insert, in each row that leaves it zero. Without an
// organization it does nothing, and the policy refuses the insert.
func (InOrg) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(func(ctx context.Context, m *velox.Mutation) error {
			if org, ok := OrgKey.From(ctx); ok {
				return m.Fill("org_id", org)
			}
			return nil
		}, velox.OpInsert),
	}
}
```

`schema/policy.go`:

```go
package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tracker/principal"
)

// InOrg's policy confines every statement to the caller's Org. velox ANDs it
// with each policy here, which decides what a member may do within the Org.
// Read and Write return a condition ANDed into every statement, so a row the
// caller may not change is one the statement does not touch; an error denies
// the statement.

// member lets anyone logged in read.
func member(ctx context.Context) (velox.Cond, error) {
	if _, ok := principal.FromContext(ctx); !ok {
		return velox.Cond{}, velox.Deny("not logged in")
	}
	return velox.Allow(), nil
}

// authoredBy refuses a new row whose author is not the caller.
func authoredBy(why string) func(context.Context, velox.Row) error {
	return func(ctx context.Context, r velox.Row) error {
		if p, ok := principal.FromContext(ctx); !ok || r.Value("author_id") != p.UserID {
			return velox.Deny(why)
		}
		return nil
	}
}

// Policy lets anyone in the Org read Projects and only an admin change them.
func (Project) Policy() velox.Policy {
	return velox.Policy{
		Read: member,
		Write: func(ctx context.Context) (velox.Cond, error) {
			if p, ok := principal.FromContext(ctx); !ok || !p.Admin {
				return velox.Cond{}, velox.Deny("only an admin can change a project")
			}
			return velox.Allow(), nil
		},
		Insert: func(ctx context.Context, _ velox.Row) error {
			if p, ok := principal.FromContext(ctx); !ok || !p.Admin {
				return velox.Deny("only an admin can create a project")
			}
			return nil
		},
	}
}

// Policy lets anyone in the Org read Issues; an admin, the author and the
// assignee can change one. A new Issue must name its caller as its author.
func (Issue) Policy() velox.Policy {
	return velox.Policy{
		Read: member,
		Write: func(ctx context.Context) (velox.Cond, error) {
			p, ok := principal.FromContext(ctx)
			switch {
			case !ok:
				return velox.Cond{}, velox.Deny("not logged in")
			case p.Admin:
				return velox.Allow(), nil
			}
			return velox.Or(velox.C("author_id").EQ(p.UserID), velox.C("assignee_id").EQ(p.UserID)), nil
		},
		Insert: authoredBy("an issue must be created by its author"),
	}
}

// Policy lets anyone in the Org read Comments; an admin and the author can
// change one. A new Comment must name its caller as its author.
func (Comment) Policy() velox.Policy {
	return velox.Policy{
		Read: member,
		Write: func(ctx context.Context) (velox.Cond, error) {
			p, ok := principal.FromContext(ctx)
			switch {
			case !ok:
				return velox.Cond{}, velox.Deny("not logged in")
			case p.Admin:
				return velox.Allow(), nil
			}
			return velox.C("author_id").EQ(p.UserID), nil
		},
		Insert: authoredBy("a comment must be created by its author"),
	}
}
```

`schema/schema.go` is unchanged. `main.go` and the test package drop the
`gen/runtime` import.

### 8.2 tenant, routing, graphql, testmodel

Their `InOrg` is the tracker's. The graphql example's field keeps
`.Annotations(graphql.Skip())`. tenant's `Project` declares no behaviour of
its own. `scope_test.go` calls
`scopetest.Isolates[gen.Project](t, ctx, db, schema.OrgKey, "org_id", 1, 2, newProject)`,
unchanged. `internal/testmodel`'s schema stops importing testmodel:
`UserFromContext` and its setter move to a new package
`internal/testmodel/actor`.

### 8.3 tour: soft delete plus InOrg

`schema/inorg.go` is the tracker's. `schema/invoice.go` keeps
`Mixin() []schema.Mixin{mixin.Time{}, InOrg{}, softdelete.Mixin{}}`.

`schema/invoice_behaviour.go`:

```go
package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tour/role"
)

// Policy says who may see and change invoices, within the context's
// organization: velox ANDs it after InOrg's policy, which confines them to it.
func (Invoice) Policy() velox.Policy {
	return velox.Policy{
		// Accountants see every invoice of their organization; viewers see
		// only the ones that were sent.
		Read: func(ctx context.Context) (velox.Cond, error) {
			switch role.FromContext(ctx) {
			case role.Accountant:
				return velox.Allow(), nil
			case role.Viewer:
				return velox.C("status").NEQ("draft"), nil
			}
			return velox.Cond{}, velox.Deny("no billing role")
		},
		Write: func(ctx context.Context) (velox.Cond, error) {
			if err := role.RequireAccountant(ctx); err != nil {
				return velox.Cond{}, err
			}
			return velox.Allow(), nil
		},
		Insert: func(ctx context.Context, _ velox.Row) error { return role.RequireAccountant(ctx) },
	}
}

// Hooks says what a valid new invoice is: a number that starts with INV-.
// velox runs it after InOrg's fill and the soft delete's hook.
func (Invoice) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(validNumber, velox.OpInsert)}
}

// validNumber refuses an inserted invoice whose number does not start with
// INV-.
func validNumber(_ context.Context, m *velox.Mutation) error {
	for r := range m.Rows() {
		if n, _ := r.Value("number").(string); !strings.HasPrefix(n, "INV-") {
			return fmt.Errorf("invoice number %q does not start with INV-", n)
		}
	}
	return nil
}
```

`"draft"` converts to `gen.InvoiceStatus`, a named string type. velox-gen
checks the kind (string to a string enum).

`schema/line_policy.go`:

```go
// Policy has no Read of its own, so anyone in the organization may read lines;
// only accountants change them. velox ANDs it after InOrg's policy.
func (Line) Policy() velox.Policy {
	return velox.Policy{
		Write: func(ctx context.Context) (velox.Cond, error) {
			if err := role.RequireAccountant(ctx); err != nil {
				return velox.Cond{}, err
			}
			return velox.Allow(), nil
		},
		Insert: func(ctx context.Context, _ velox.Row) error { return role.RequireAccountant(ctx) },
	}
}
```

Invoice's descriptor:

```go
Policy:       []func() velox.Policy{sch.InOrg{}.Policy, sch.Invoice{}.Policy},
Hooks:        []func() []velox.Hook{sch.InOrg{}.Hooks, softdelete.Mixin{}.Hooks, sch.Invoice{}.Hooks},
Interceptors: []func() []velox.Interceptor{softdelete.Mixin{}.Interceptors},
```

### 8.4 hooks

`schema/schema.go`: the `CreatedBy` mixin declares its hook itself.
`auth.CreatedBy`, the generic hook each entity listed, is removed:

```go
// CreatedBy adds created_by, and fills it on insert from the context's
// user, in each row that leaves it empty.
type CreatedBy struct{ mixin.Schema }

// Fields returns created_by.
func (CreatedBy) Fields() []schema.Field {
	return []schema.Field{field.String("created_by")}
}

// Hooks fills created_by on insert.
func (CreatedBy) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if u := auth.UserFromContext(ctx); u != "" {
			return m.Fill("created_by", u)
		}
		return nil
	}, velox.OpInsert)}
}
```

`schema/hooks.go`:

```go
// Hooks declares Task's hooks. The first listed is the outermost; CreatedBy's,
// a mixin's, runs before them. Before runs a function before the write, for
// the kinds of write it names.
func (Task) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(deriveSlug, velox.OpInsert),
		velox.Before(renamed, velox.OpUpdate),
		velox.Before(onlyDone, velox.OpDelete),
	}
}

// deriveSlug refuses a task with no title and derives slug from title, in each
// row the caller passed: Insert and InsertAll hand the hook all their rows.
func deriveSlug(_ context.Context, m *velox.Mutation) error {
	for r := range m.Rows() {
		title, _ := r.Value("title").(string)
		if title == "" {
			return errors.New("a task needs a title")
		}
		if err := r.Set("slug", slug(title)); err != nil {
			return err
		}
	}
	return nil
}

// renamed derives the new slug when an update sets the title.
func renamed(_ context.Context, m *velox.Mutation) error {
	v, ok := m.Value("title")
	if !ok {
		return nil // an update of other columns
	}
	title, _ := v.(string)
	if title == "" {
		return errors.New("a task needs a title")
	}
	return m.Set("slug", slug(title))
}

// onlyDone narrows a delete to the finished tasks, even one that deletes
// every row.
func onlyDone(_ context.Context, m *velox.Mutation) error {
	m.Where(velox.C("done").EQ(true))
	return nil
}

func slug(title string) string { return strings.ReplaceAll(strings.ToLower(title), " ", "-") }
```

`main.go`: the audit row is a gen.Audit, so its hook is a run-time hook:

```go
db, err := velox.Connect("sqlite", filepath.Join(dir, "hooks.db"),
	velox.WithHooks[gen.Task](velox.On(auditRename, velox.OpUpdate)))

// auditRename writes an Audit row when an update renames a task, after the
// update succeeded, on the write's DB: inside db.Tx it commits or rolls back
// with the update. It is a run-time hook because it inserts a gen.Audit,
// which the schema cannot name. Under SQL(ctx) the insert is refused with
// velox.ErrDryRun, so printing the statement records nothing.
func auditRename(next velox.Mutator) velox.Mutator {
	return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
		title, renamed := m.Value("title")
		if err := next.Mutate(ctx, m); err != nil || !renamed {
			return err
		}
		a := gen.Audit{Actor: auth.UserFromContext(ctx), Message: "renamed a task to " + title.(string)}
		return m.DB().Insert(&a).Exec(ctx)
	})
}
```

### 8.5 policy

`schema/doc_policy.go`:

```go
// Policy declares Doc's policy. Read and Write return a condition that velox
// ANDs into every statement; an error denies the statement. Allow means no
// restriction, and a zero Cond is an error.
func (Doc) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Cond, error) {
			switch u := auth.UserFromContext(ctx); u {
			case "":
				return velox.Cond{}, velox.Deny("no user")
			case "admin":
				return velox.Allow(), nil
			default:
				return velox.Or(velox.C("owner").EQ(u), velox.C("shared").EQ(true)), nil
			}
		},
		// Write filters which rows may change; Read applies too.
		Write: func(ctx context.Context) (velox.Cond, error) {
			switch u := auth.UserFromContext(ctx); u {
			case "":
				return velox.Cond{}, velox.Deny("no user")
			case "admin":
				return velox.Allow(), nil
			default:
				return velox.C("owner").EQ(u), nil
			}
		},
		// Insert checks each new row.
		Insert: func(ctx context.Context, r velox.Row) error {
			if u := auth.UserFromContext(ctx); u != "admin" && r.Value("owner") != u {
				return velox.Deny("a doc must be created by its owner")
			}
			return nil
		},
	}
}
```

`schema/comment_policy.go`:

```go
// Policy declares Comment's policy by inheritance: a comment is visible when
// its doc is. Has is an EXISTS on the Doc, and velox applies Doc's own Read
// policy inside it, for this context.
func (Comment) Policy() velox.Policy {
	return velox.Policy{
		Read: func(context.Context) (velox.Cond, error) { return velox.Edge("doc").Has(), nil },
	}
}
```

Comment's descriptor links Doc:
`velox.Linked[Doc](velox.EdgeTo[Doc]("doc", commentColDocID, docColID))`.

### 8.6 graphql

`schema/inorg.go` is the tracker's, with the field
`field.Int64("org_id").Immutable().Annotations(graphql.Skip())`. Post's
descriptor:

```go
Policy: []func() velox.Policy{sch.InOrg{}.Policy},
Hooks:  []func() []velox.Hook{sch.InOrg{}.Hooks},
```

The graphql example's gen is in plain mode today, so `post_velox.go` already
imports `sch`. Only the two lines above change, from
`velox.BindPolicy[Post](sch.InOrg{}.Policy)`. `graph/` is unchanged.

## 9. ent compared

| | ent | velox after |
| --- | --- | --- |
| A mixin's Policy | `func (TenantMixin) Policy() ent.Policy`, built from `privacy` rules; a filter rule writes `f.Where(entql.FieldEQ("tenant_id", id))` | `func (InOrg) Policy() velox.Policy`, `Read` returns `velox.C("org_id").EQ(org)` |
| A mixin's Hooks | `func (TenantMixin) Hooks() []ent.Hook`, with `hook.On(...)` and `m.SetField("tenant_id", id)` on the generic `ent.Mutation` | `func (InOrg) Hooks() []velox.Hook`, with `velox.Before(f, velox.OpInsert)` and `m.Fill("org_id", org)` on `*velox.Mutation` |
| An entity's Policy | `privacy.Policy{Query: ..., Mutation: ...}`; rule order matters: the first `Allow` or `Deny` decides, `Skip` passes on. A typed rule (`privacy.IssueMutationRuleFunc`) imports gen. | `velox.Policy{Read, Write, Insert}`; conditions, ANDed with the mixins'; none lifts another. Never imports gen. |
| A hook | Typed: `hook.IssueFunc(func(ctx, m *gen.IssueMutation) (ent.Value, error))`, which imports gen. Untyped: `ent.Mutation`. | Untyped in the schema, by name. Typed work in the application with `velox.WithHooks[gen.Issue](...)`, as ent's `client.Issue.Use(...)`. |
| Binding | `ent/runtime` assigns the schema's policies and hooks; each program imports `_ ".../ent/runtime"` | gen's descriptor lists the schema's method values; no runtime package, no blank import |
| Wrong column name | an error from `SetField`, or a failed query, at run time | refused by velox-gen at its line when constant; fails closed at run time otherwise |

## 10. Phases

Each phase merges on its own and passes `bash scripts/ci-local.sh`, and
`bash scripts/ci-local.sh --full` before a push.

### Phase 1: by-name conditions and mutations in the engine

Internal only. No public API, no generated change, no version bump.

- `internal/engine`: the by-name leaf op; the `ByName` flag in `group`;
  `resolve` in statement preparation for every condition attached; the
  entity's name index (columns and relations) built at metadata build; the
  per-column setters; `MutationState` operations by name (set, fill, value,
  rows); the relation link (`Target func() (*EntityInfo, error)` on the
  relation, set by a later `Linked`).
- Tests: `TestResolveByName` (unknown column, converted value, out of range,
  wrong kind, `IsNull` on NOT NULL, text operator on a number), `TestResolveRendersAsTyped`
  (same SQL and render-cache key as the index leaf), `TestResolveEdgeNeedsLink`,
  `TestMutationSetByName` (insert rows, update assignment, delete misuse),
  `TestMutationFillByName`, `TestMutationValueByName`,
  `BenchmarkPolicyByName`, `BenchmarkPolicyByIndex`, `BenchmarkSetByName`.
  The 5.1 and 5.2 budgets are measured and recorded in the benchmarks'
  comments.
- Coverage floor of `internal/engine` holds.

### Phase 2: untyped behaviour

One merge. The new names collide with the typed ones, and the generated code
calls the API, so the API, the generator and every caller change together.

- Root and engine: the types of section 2; the removals of 2.7, but for
  `NotLinkedError` and MetaOnce's retry (Phase 4); `Descriptor`'s fields;
  `Load`'s binding, not generic; `WithHooks`; `Linked`; `Predicates`;
  `EdgeTo`, `EdgeBidi` and `EdgeThrough` named by the schema's edge name;
  `go generate .` without `ValueIn`.
- `scope` (`Require`, `Match`; `By` and `Scope` removed), `scopetest` (by
  name; the column check), `softdelete` (untyped).
- velox-gen: one mode (4.1); the refusal of a schema that imports gen; the
  shapes of 4.5; the go/types scan for `velox.Edge` and the links (4.4); the
  sweep of gen/runtime; no runtime warning.
- Regenerate `internal/testmodel`, `internal/testbind` and every example;
  `go -C examples/graphql tool gqlgen generate` with no diff in `graph/`.
- Migrate the callers: `internal/apitest` fixtures and tests, root internal
  tests, `internal/engine` tests, `internal/integration`,
  `internal/codegen/testdata/e2e`, contrib/graphql's e2e tests, and the
  testmodel schema (`internal/testmodel/actor`).
- `internal/repocheck`: `TestPublicAPISnapshot -update-api`;
  `TestGenericTypesCompileSmall`'s sources untyped, rows removed, bounds
  measured (section 6).
- `GeneratedCodeIsVersion16`, with the CHANGELOG line: "Behaviour declared in
  the schema is untyped and names columns by name; gen imports the schema;
  gen/runtime is gone; remove `import _ ".../gen/runtime"`."
- Docs: package docs of velox, scope and softdelete; `docs/coming-from-ent.md`
  (Queries and writes); CONTRIBUTING's Generated code; the examples' READMEs;
  ADR 0039 with the README index; status lines on 0034, 0036, 0037, 0038,
  0029 and 0007.
- Tests, new or rewritten:
  - apitest: `TestPoliciesANDedInOrder`, `TestHookOrderMixinsFirst`,
    `TestBeforeRunsBeforeNext`, `TestInsertPolicySeesHookFill`,
    `TestWithoutPolicyKeepsInterceptors`, `TestWriteFiltersAffectZero`,
    `TestZeroCondFails`, `TestUnknownColumnFailsClosed`,
    `TestCondValueConverted`, `TestEdgeHasAppliesTargetPolicy`,
    `TestEdgeNotLinkedFails`, `TestRowValueNullable`, `TestFillOnlyZero`,
    `TestSetOnDeleteIsMisuse`, `TestValueOnInsertIsMisuse`,
    `TestRowSetInPolicyIsMisuse`, `TestRunTimeHooksRunAfterSchemaHooks`,
    `TestRunTimeHooksInTx`, `TestRunTimeHooksOnlyTheirEntity`.
  - scope: `TestKeyRequire`, `TestKeyMatch`. scopetest:
    `TestIsolatesRefusesMutableColumn`, `TestIsolatesRefusesNullableColumn`.
  - softdelete: today's tests, untyped.
  - codegen: goldens `blog`, `scoped`, `mixinbehaviour` and `mixinplain`
    regenerated with no runtime files; `TestSchemaMethodsRefusesTypedShape`,
    `TestMixinMethodRefusesGeneric`, `TestBehaviourRefusesSchemaImportingGen`
    (direct, and through a chain), `TestRenderBindsMethodValues`,
    `TestEdgeLinkedWhenBehaviourNamesIt`, `TestEdgeNameMustBeConstant`.
  - cmd/velox-gen: `TestSweepRemovesGenRuntime` (generated files removed, a
    user file named), `TestGenerateWithBehaviourWithoutGen` (a schema with
    `Policy` and `Hooks`, no gen directory: the bootstrapping case of 0034);
    the runtime warning's tests and `testdata/e2e/closed` are removed.
  - examples: each `main_test.go`; tenant's `TestInOrgIsolatesProject`.

### Phase 3: velox-gen checks columns

No generated change, no version bump.

- The checks of 3.2 on the Phase 2 scan: names, values, reachability from
  roots through same-package helpers, the per-mixin entity sets, the
  warnings. `checkMixinFields` and the `velox.Field` scan in
  `mixinMethodShape` are removed.
- Tests: `TestCheckColumnsRefusesUnknown` (each call form of the table),
  `TestCheckColumnsPerMixinEntity` (one listing entity lacks the column),
  `TestCheckColumnsFollowsHelpers` (`member`, `authoredBy`),
  `TestCheckColumnsConstName` (a named constant is checked),
  `TestCheckColumnsValueKind` (a string for an int64 column; 300 for an int8
  column; an `any` value not checked), `TestCheckColumnsNilOnNotNull`,
  `TestCheckColumnsTextOpOnNumber`, `TestCheckColumnsIsNullOnNotNull`,
  `TestCheckColumnsHasWhereTarget`, `TestCheckColumnsWarnsNonConstant`, and
  an e2e testdata package `wrongcolumn` whose velox-gen output is a golden.

### Phase 4: remove what no case needs

No generated change, no version bump.

- Loader: `prune.go`, `writeOverlay`, `unoverlay`, `listCycle` and their
  tests. The refusal of Phase 2 stands where `listCycle` passed over a cycle.
- Engine and root: `NotLinkedError`, `unlinkedError`, `Unlinked`, and
  MetaOnce's retry of a NotLinkedError build. A failed build is kept, as any
  other failure. The `Descriptor` and `Load` doc comments lose those
  paragraphs.
- Re-measure 0037's N=100 Policy-per-entity variant and record it in 0039's
  Review.
- Tests: `TestLoadSchemaImportingGenRefused` stays (Phase 2); the prune,
  `listCycle` and retry tests go. Each coverage floor holds; no floor is
  lowered.

## 11. ADRs

New: **0039. Behaviour is untyped, by column name; gen imports the schema.**
Its decision is sections 1, 2, 3 and 4. Its consequences are the removals,
the run-time hooks and section 6. Its evidence is the Phase 2 and 3 tests.

| Record | Change |
| --- | --- |
| 0034 | Superseded by 0039, except "the schema declares behaviour" and "gen is all generated". Typing with gen, package runtime, fail closed by `veloxRuntime()`, and the prune overlay are gone. |
| 0036 | Its leftovers, which 0037 kept (typed binds by name, `NotLinkedError`, MetaOnce's retry), are gone (0039). |
| 0037 | Revised by 0039: every entity stays in gen, always; gen/runtime, its blank import and the runtime warning are gone; gen imports the schema. |
| 0038 | Revised by 0039: `InOrg`'s methods are plain and untyped; `scope.By` is gone; the column check moves to `scopetest.Isolates`; velox-gen checks the column names. |
| 0029 | Revised by 0039: package `scope` is `Key` with `Require` and `Match`; a scope is the application's mixin. |
| 0007 | Revised by 0039: a hook that must name gen's types (read or write another entity, typed) is a run-time hook, `velox.WithHooks`. |
| 0035 | Unchanged. The untyped behaviour resolves to the conditions 0035's surface builds. |
| 0008 | Unchanged. |

## 12. Departures from the target code, and why

1. `Filter` is `Read`. `Read` already covers reads, updates and deletes, and
   `Filter` is the Interceptor's field, which `WithoutPolicy` does not lift.
2. `return nil, velox.Deny(...)` is `return velox.Cond{}, velox.Deny(...)`.
   `Cond` is an instance of `Pred`, a struct, so `velox.Or`, `And` and `Not`
   serve typed and untyped conditions, and the zero value fails closed.
3. `velox.OnCreate(f)` is `velox.Before(f, velox.OpInsert)`. velox calls the
   op an insert, `Before` says when `f` runs, and it takes ops as `On` does.
4. `m velox.Mutation` is `m *velox.Mutation`, as today: the state is changed
   in place and next sees it.
5. `OrgFrom(ctx)` is `OrgKey.Require(ctx)` and `OrgKey.From(ctx)`.
   `Key.With` records the value for the GraphQL batcher, and `Require` gives
   `scope.ErrNoScope`.
6. The hook calls `m.Fill`, not `m.Set`, and does not deny without an
   organization. Filling only zero values keeps today's semantics: an insert
   under `WithoutPolicy` may name an organization, a row naming another
   organization is refused by the policy with `scope.ErrMismatch`, and an
   insert without one is refused by the policy with `scope.ErrNoScope`.
7. `InOrg`'s policy has an `Insert` (`OrgKey.Match`), which the target left
   out. It keeps the mismatch refusal of `scope.Policy`.
8. The Issue policy keeps `Read` and `Insert`, which the target left out, so
   the example keeps its behaviour.
