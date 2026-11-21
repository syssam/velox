# velox-graphql on graphql-go: design spec (Phase B of the veloxe gaps)

Status: proposal, 2026-11-22. Base: local main b816c01f (Phase A merged,
ADRs 0098–0102). Replaces the parked
`docs/superpowers/specs/2026-11-03-graphql-go-backend-design.md` (kept as
history; section 0.3 says what carries over). Gap rows #115, #116, #117,
#133 and UF N-V1, N-V2, N-V3 of
`docs/superpowers/specs/2026-11-21-veloxe-gap-analysis.md` (§1.11, §2.1
item 1). ADR numbers start at 0103.

## 0. What this rests on

### 0.1 The engine is github.com/syssam/graphql-go, not graph-gophers

The brief names github.com/graph-gophers/graphql-go. nextapp does not use
it. Its go.mod requires `github.com/syssam/graphql-go
v0.0.0-20261029022150-3e0050be7462` (nextapp-graphql/go.mod:42), and the
module cache has no graph-gophers checkout (`~/go/pkg/mod/github.com/
graph-gophers` does not exist). syssam/graphql-go is a different engine:
typed bindings (`graphql.Object[T]`, `Field`, `Resolve`, `ResolveArgs`;
docs/public-api.txt:196-262 of the cached module) and a generator, `gqlc`,
that writes those bindings from SDL. This spec targets syssam/graphql-go
and gqlc. It does not target graph-gophers (open question Q1).

Its upstream HEAD is 800266e (local checkout `~/Jobs/project/graphql-go`,
`git log`), four commits past nextapp's pin 3e0050b. The API this spec
uses is the same at both (`docs/public-api.txt` diffed for FieldContext,
Selection, FieldFrom).

### 0.2 How nextapp serves GraphQL today

- veloxe's generator writes the SDL only: `graphqlgen.WithoutGQLGen()`,
  `WithSchemaGenerator()`, `WithSchemaPath("schema/velox.graphql")`,
  `WithSchemaSplitMode(SchemaSplitPerEntity)`
  (nextapp-graphql/veloxschema/generate.go:17-23). That is 478
  `schema/velox_<entity>.graphql` files plus `schema/velox.graphql`
  (`ls schema | grep -c '^velox_'` = 478), 5.6 MB of SDL.
- gqlc binds that SDL to veloxe's Go by autobind (nextapp-graphql/
  gqlc.yaml:2-14: `autoBind: ./velox/entity`, `modelDirective: goModel`,
  `fieldDirective: goField/forceResolver`, `zeroForNullInputs`,
  `inputListPointers`, `inlineAccessors`, `groups.trimPrefix: velox_`,
  `rootFields: file`). One SDL file is one group package: 625 packages
  under `graph/`, 126,314 lines of `generated.go` (wc).
- A group's bindings call veloxe's methods positionally:
  `v.Participants(ctx, a.After, a.First, a.Before, a.Last, a.OrderBy,
  a.Where)` and `v.AbTest(ctx)` with `graphql.Inline()`
  (graph/abtestvariant/generated.go:89-95). Enums bind by
  `graphql.EnumMarshaler[T]` (:57-58), inputs by `graphql.Input[T](…,
  graphql.ZeroForNull())` (:59-60), connections by struct fields
  (:115-122).
- Root fields are hand-written in one group: 452 functions in
  graph/velox/velox.go, each `r.Client.X.Query().Paginate(ctx, …,
  entity.WithXOrder(args.OrderBy), entity.WithXFilter(args.Where.Filter))`
  (:21-25) or a service call. Node and nodes are hand-written too (:13-19).
- The SDL veloxe writes for nextapp has entity types, enums, where inputs,
  orders, connections and edges, and no mutation inputs
  (`grep -c '^input Create' schema/velox_*.graphql` = 0; 477 where inputs,
  480 connections). nextapp's mutation inputs are its own SDL
  (schema/access_policy.graphql:114 and the like), bound to gqlc models.
- The server uses graphql-go's own error presenter, cost, timeouts and
  field interceptors (internal/server/executor.go:33-212, cost.go:57). It
  installs no selection source: veloxe's projection and eager loading never
  ran, with one log line (upstream-feedback.md N-V2).

### 0.3 What the port does by hand, and where it stops

nextapp-graphql-velox2 runs `velox-gen -plugin
velox-graphql:compat=veloxe,where=whitelist` (tools/velox2proof/
phase0.sh:72) and gets 479 `veloxgen/*_gql.go` files (434,067 lines, 17.3
MB) and one 4.6 MB `veloxgen/velox.graphql`. Its `graph/` still binds
veloxe's `velox/entity`; its gqlc.yaml is main's, unchanged (diff empty).
It stops there: velox-graphql's Go reaches gqlgen through `veloxgql`, its
runtime only batches and projects inside a gqlgen field, and its SDL is
one file. The migration design already chose the shape this spec builds:
"SDL + autobind … gqlc binds, not the spec's 'emit graphql-go bindings
directly' shape" (nextapp-graphql-velox2/docs/superpowers/specs/
2026-11-18-velox2-migration-design.md:110, :122-126).

### 0.4 The parked spec, refreshed

| Parked decision (2026-11-03) | Now |
|---|---|
| velox-graphql emits graphql-go bindings itself (`postGraphQL()`), section 1 | **Dropped.** nextapp binds with gqlc and keeps its 625 group packages, which split the compile per entity; the port's design says so (0.3). The parked measurement (bindings add 12-14 s CPU and 0.85 GB to one `gen` package at N=100) argues against it as well. |
| `gqlimpl` engine-agnostic runtime | Holds, as `gqlgraph` (ADR 0047/0049). Its only gqlgen reads are three: `select.go:33`, `edge.go:309`, `gqlcore/batch.go:130`. |
| Engine seam: object, field, occurrence key, `Selected(typ)`, `Nulls` | Holds, minus `Nulls`: explicit-null detection is gone from gqlgraph (`grep gqlgen veloxgql/gqlgraph` hits only select.go and edge.go; ADR 0062 made null "not given"). |
| Input structs need `graphql:"…"` tags | Holds, narrowed: graphql-go derives `AuthorID → authorId` (graphql-go input.go:909-918), so a tag is written only where the derived name differs (section 3.4). |
| Bound directives on input fields fail NewSchema (G1) | Still true at HEAD (graphql-go directive.go:230). No generator work: compat=veloxe writes no directives on inputs (internal/sdl/veloxe.go:26), and nextapp's only one, `@sensitive`, is an unbound marker read by an interceptor (internal/server/directives.go:84). A bound one fails NewSchema with graphql-go's message, loudly. |
| No public field-occurrence identity (G2) | Still true. `Selection` is a struct of one pointer (context.go:320-322), comparable; it is the key (section 2.3). Ask kept. |
| No schema in OperationContext (G7) | No longer needed: graphql-go's `Selection.ForType` folds fragments on interfaces itself (context.go:463-468). |
| Two new modules (gqlgenx, graphqlgo) | Changed: one module, enforced by package imports (section 1.4, Q3). |
| `backend=` option, graphql-go the default | Renamed `engine=`; default stays gqlgen until parity is proven (Q4). |
| Velox runs cost, document bounds, tx, response limits on graphql-go | Changed: graphql-go has its own (`WithQueryCost`, `WithMaxDepth`, `WithMaxTokens`, `WithMaxResponseBytes`, `WithOperationTimeout`; public-api.txt:240-260), and nextapp uses them. The adapter maps velox's limits onto them and makes velox's Cost and the mutation transaction opt-in (section 1.3, Q5, Q6). |

## 1. Package split

### 1.1 Packages

| Package | Holds | May import |
|---|---|---|
| `contrib/graphql/veloxgql` (core) | `Cursor`, `PageInfo`, `OrderDirection`; the scalar types `Time`, `UUID`, `Bytes`, `JSON`, `Uint64`; `Limits`, `LimitsFromContext`; the errors and codes; `DB`, `DBFromContext`; `Batch`; `Cost`; the document and variable walks; `Present(err)` (code and message); the options `WithLimits`, `WithTxOptions`, `WithTxFilter`, `WithBatchScope`; `GeneratedCodeIsVersionN` | velox, gqlparser `ast` (both engines parse with gqlparser v2.5.59: contrib/graphql go.mod, graphql-go go.mod) |
| `veloxgql/gqlgraph` | the runtime generated code calls: `Type`, `Paginate`, `EdgeOne`, `EdgeList`, filters, orders, enums, Node | core, `internal/gqlcore`, velox, untyped |
| `veloxgql/internal/gqlcore` | the batch `Table`, `Group`, `Batch`; `SelectionSource`; `Selected`'s connection walk; coded errors | core types only |
| `veloxgql/gqlgenx` (new) | `NewExtension`, `Extension`, `Setup`, `ErrorPresenter`, the response budget, the marshal functions gqlgen.yml names (`MarshalTime`, `MarshalInt64ID`…), gqlgen's `SelectionSource` (today's `collect`/`abstractFragment`) | core, gqlcore, gqlgen, gqlparser |
| `veloxgql/graphqlgo` (new) | `With(db, opts...) []graphql.ExecutorOption`, `Scalars()`, `ErrorPresenter`, `Limits(l)`, `WithMutationTx`, `WithCost`, graphql-go's `SelectionSource` | core, gqlcore, syssam/graphql-go |

Both adapters sit below `veloxgql` so they may import
`veloxgql/internal/gqlcore` (Go's internal rule goes by path).
`SelectionSource` stays internal (Q8).

### 1.2 What moves

From `veloxgql` to `gqlgenx`: extension.go (`NewExtension`, :149), budget.go
(its `operationResponse`/`finish` take `*graphql.Response`, :233-267),
setup.go (`Setup`, :61, gqlgen's validator rules, the body and websocket
limits), presenter.go's `ErrorPresenter` (:137, returns
`graphql.ErrorPresenterFunc`), marshal.go (`MarshalTime` and the rest return
`graphql.Marshaler`, :25-101), id.go's `MarshalInt64ID`/`UnmarshalInt64ID`
(:24-34), document.go's `checkRequest` (takes `*graphql.RawParams`, :77).
From `gqlgraph/select.go`: `selected`'s gqlgen half (`GetFieldContext`,
`CollectFields`, `abstractFragment`, :32-160) becomes gqlgenx's source; the
connection walk (`computeSelected`'s switch, :45-89) moves to gqlcore and
serves both. `gqlcore.WithTable` loses its `*ast.Schema` (batch.go:96);
gqlgenx's source keeps it.

Stays in core, with these changes:
- scalar.go drops its `var _ graphql.Marshaler = …` lines (:18-29), its
  only gqlgen use; the types keep `MarshalGQL`/`UnmarshalGQL`, which both
  engines call (graphql-go's `Marshaler[T]`, public-api.txt:288).
- `Cursor` gains `MarshalGQL(w)`/`UnmarshalGQL(v)` beside its context
  pair (cursor.go:266-289); graphql-go's `ScalarMarshaler` needs them.
  gqlgen prefers the context pair when both exist (gqlgen binder.go:
  502-506); the gate is examples/graphql's `graph/generated.go` unchanged.
- `gqlcore.Error` gains `GraphQLExtensions()` beside `Extensions()`
  (error.go:33): graphql-go's `ExtensionsProvider` (errors.go:250), so
  velox's codes reach any graphql-go presenter, nextapp's included.
- `gqlgraph.(*Connection).Page` and `TotalCount` (paginate.go:581, :591)
  accept a nil receiver: the zero `XConnection` is the empty connection.
  nextapp writes 176 `&entity.XConnection{…}` literals for denied lists
  (`grep -rhoE` over internal/, 57 of them `{}`).

### 1.3 Options shared by both adapters

`veloxgql.Option` configures a `veloxgql.Config` (DB limits, tx options, tx
filter on `*ast.OperationDefinition`, batch scope) that both adapters read:
`gqlgenx.NewExtension(db, veloxgql.WithLimits(l))`, `graphqlgo.With(db,
veloxgql.WithLimits(l))`. Engine-only options live in the adapter:
`graphqlgo.WithMutationTx()`, `graphqlgo.WithCost()`.

### 1.4 Import rules and how they are enforced

- core, gqlgraph, gqlcore, internal/cost: neither gqlgen nor graphql-go,
  no `net/http`.
- gqlgenx: no graphql-go. graphqlgo: no gqlgen.
- Generated code (both engines) imports only core and gqlgraph, so one
  generated package serves either server.
- internal/sdl, internal/gogen, the plugin: neither engine (sdl/names.go
  names gqlgen's introspection models as strings only, :68-78).

`deps_test.go` gains one test per rule, by `go list -deps` as
`TestAnnotationsImportOnlySchema` does (deps_test.go:13). Today `go list
-deps ./veloxgql/gqlgraph` is 230 packages and includes
`99designs/gqlgen/graphql`, `graphql/errcode`, `sosodev/duration`,
`agnivade/levenshtein`, gqlparser's validator and `net/http` (run
2026-11-22). After: none of them.

One module (contrib/graphql) requires both engines. Compile cost goes by
imported packages, so a graphql-go server compiles no gqlgen; both appear in
its go.sum. A module split is mechanical later (Q3).

## 2. Selection and batching without gqlgen

### 2.1 The interface

```go
// package gqlcore

// A SelectionSource tells the runtime about the field resolving in ctx.
// An adapter installs exactly one with the operation's state.
type SelectionSource interface {
	// Field is the field resolving in ctx; ok is false outside a field
	// (an application's own code, a field the engine gives no context).
	Field(ctx context.Context) (f Field, ok bool)
	// Wait is called around a wait on another field's batch and returns
	// the function that ends it; graphql-go parks the wave coordinator.
	Wait(ctx context.Context) (done func())
}

// Field is one field occurrence of the operation.
type Field struct {
	Object string // the parent GraphQL type, "Post"
	Name   string // the field's name, not its alias, "tags"
	// Key is comparable and distinct per occurrence (two aliases are two
	// keys); every row of a list shares it. Batches and selections are
	// memoised by it.
	Key any
	// Sel is the field's sub-selection.
	Sel Selection
}

// Selection is a sub-selection, its fragments folded.
type Selection interface {
	// On is the fields selected on objects of the type typ, with each
	// one's sub-selection; ok is false when the source cannot tell (a
	// fragment on an interface it has no schema for).
	On(typ string) (fields []SelectedField, ok bool)
}

type SelectedField struct {
	Name string
	Sel  Selection // nil for a leaf
}
```

gqlcore implements, once for both engines: `Selected(ctx, typ)` (today's
`computeSelected`: a connection's `edges.node` fields, `ok=false` on an
application's own connection or edge field), its memo per `(Key, typ)`
(today keyed by `*ast.Field` plus selections, batch.go:221-259), `Add`'s
"inside a field" test (batch.go:130), and `batchOf`'s field match and batch
key (edge.go:309-336, `g.Batch(fc.Field.Field, fc.Field.Selections, …)`
becomes `g.Batch(f.Key, …)`).

### 2.2 gqlgen's source (gqlgenx)

`Field`: `graphql.GetFieldContext(ctx)`; `Object` = `fc.Object`, `Name` =
`fc.Field.Name`, `Key` = `fc.Field.Field` (the `*ast.Field`) with the
merged selections, as today. `On(typ)`: `graphql.CollectFields` with the
schema's interfaces of `typ` (select.go:97-110). `Wait`: no-op. Behaviour
is today's, byte for byte.

### 2.3 graphql-go's source (graphqlgo)

graphql-go gives, at both pins:
- `FieldFrom(ctx) *FieldContext` for Resolve and ResolveArgs functions
  (context.go:294-297); a `Field`/`FieldArgs` accessor takes no context,
  and generated code never reaches gqlgraph from one (every velox method
  that reads takes `ctx`).
- `FieldContext{Field *ast.FieldDefinition; Object *ast.Definition}` and
  `Selection()` (context.go:184-207).
- `Selection.ForType(typ)`, `Fields()` (aliases visited once each),
  `Sub(name)` (context.go:382-490). Fragments, `@skip`/`@include` and
  abstract fragments are folded in the plan.

So: `Object` = `fc.Object.Name`, `Name` = `fc.Field.Name`, `Key` =
`struct{obj, name string; sel graphql.Selection}`, `On(typ)` =
`fc.Selection().ForType(typ).Fields()` mapped to `SelectedField{f.Name,
f.Selection()}`, `ok` always true. `Wait` = `OperationFrom(ctx).Waves().
Park()` and `Unpark` (public-api.txt:169-170), so a velox waiter never
holds back an application loader's wave.

The key leans on `Selection` being one pointer, which graphql-go memoises
per (parent type, AST nodes): two aliases have two `*ast.Field`s, so two
keys. Where a key is shared wrongly, the batch's argument check
(`reflect.DeepEqual`, batch.go:177-186) makes the second caller read alone:
correct, one statement more. Ask GA2 makes the key documented.

`SelectedField.Withheld` (context.go:439) is not consulted: a withheld
field's column read anyway costs bytes, not correctness.

### 2.4 Fail closed

| Situation | Today | After |
|---|---|---|
| No veloxgql state in ctx | `ErrNoDB` (db.go:29-35) | same |
| State but no `SelectionSource` | cannot happen with gqlgen; under graphql-go velox silently reads every column and batches nothing (select.go:34 returns `ok=false`; parked spec measured 241 statements for 5) | `ErrNoSelectionSource`, an internal error, from every gqlgraph entry point (`Paginate`, `EdgeOne`, `EdgeList`, an edge's connection). The state is built only by `gqlcore.Install(ctx, cfg, src)`, which panics on a nil `src`; only the adapters call it |
| Source present, ctx in no field | read alone, every column | same, documented: an application's code calling `p.Tags(ctx, …)` outside the edge's field |
| Source present, in another field (an application resolver calls `x.Children`) | read alone | same |
| Source cannot tell the selection (an app field on a connection) | every column | same: reads more, never less |
| `veloxgql.Batch` outside an operation | no-op (batch.go:28-31) | same: it is a hint |

No log line anywhere: veloxe's one-time log is what hid N-V2.

## 3. Generated output for graphql-go

### 3.1 Plugin options

`velox-graphql` (gqlplugin.go:27-37) gains two:

- `engine=gqlgen|graphql-go`. Default `gqlgen` (Q4).
- `split=entity`: `velox.graphql` plus one `velox_<entity>.graphql` per
  entity, named as veloxe named them (`velox_abtestvariant.graphql`:
  the GraphQL type name, lower-cased). Works with either engine.

`only=enums` takes `engine` (it writes Go only, the same for both) and not
`split`. `graphqlgen.Plugin(opts)` takes the same map (ADR 0097,
graphqlgen.go). nextapp passes
`compat=veloxe,where=whitelist,engine=graphql-go,split=entity`.

### 3.2 SDL under engine=graphql-go

The SDL is gqlc's input; gqlc reads `@goModel` and `@goField(forceResolver)`
when configured (codegen/config.go:357-377; nextapp configures both,
gqlc.yaml:6-7) and graphql-go's NewSchema accepts them (parked 0, ggproto
`TestSchema`). So the directives stay, which departs from the brief's "no
@goModel/@goField", for these reasons. Differences from gqlgen's SDL:

| Item | gqlgen | graphql-go |
|---|---|---|
| `extend type __Type @goModel(…)` introspection binds (names.go:80-92) | when needed | never |
| `@goModel` on velox-gen's types | only on the application's enum types and Node (render.go:287-292, node.go:36-43) | on every type velox declares: entities, enums, connections, edges, orders, order fields, where and mutation inputs, Node, PageInfo, OrderDirection. nextapp autobinds a second package (`./veloxschema/schematype`, gqlc.yaml:12-14) whose names may collide; explicit models make the binding independent of autobind order. This is the L2 goal of the migration design (:111) |
| scalars | `scalar Time` etc., models in gqlgen.yml (examples/graphql/gqlgen.yml) | `scalar Time @goModel(model: "time.Time")`, `UUID` → `github.com/google/uuid.UUID`, `Bytes` → `[]byte`, `Uint64` → `uint64`, `Cursor` → `veloxgql.Cursor`, `JSON` → `veloxgql.JSON`: the entity's own Go types, so gqlc binds struct fields directly (codegen/autobind_reconcile.go:28-40 needs an exact type or a basic class) |
| `@goField(name: …)` (fields.go:222-232, :425) | written | written; read once gqlc honours a name argument (ask GA1) |
| `@goField(maxList: N)` | written; velox's Cost reads it (internal/cost/cost.go:290-295) | written; gqlc ignores the argument |
| `@goField(forceResolver: true)` on `graphql.Map` fields (fields.go:112) | written | written; gqlc forces the field onto the group's Resolver |
| application directives on input fields | written (not under compat) | written; a bound one fails NewSchema (graphql-go directive.go:230) |

Everything else is the same text.

### 3.3 split=entity

- `velox.graphql`: the vocabulary (scalars, PageInfo, OrderDirection, the
  directive declarations), Node, `type Query` (node, nodes and every
  QueryField, as veloxe kept them in one file: nextapp
  schema/velox.graphql:250), and enums shared by several entities (ADR
  0086).
- `velox_<entity>.graphql`: the entity's type, its own enums, connection,
  edge, order, order field, where input, create and update inputs, in
  today's order.
- Under gqlc each file is a group (`groups.trimPrefix: velox_`), so
  nextapp's 625 group directories keep their names and its 452 root
  resolvers stay in graph/velox. Per-entity root fields
  (`extend type Query` per file) are Q7.
- The definitions of the split files, parsed and sorted, equal the single
  file's (a test, 6.2).

### 3.4 Go under engine=graphql-go

The Go is gqlgen mode's (examples/graphql/veloxgen/post_gql.go), which gqlc
already binds by autobind: edge methods by name, ignoring case (codegen/
autobind_fields.go:265-268, the same rule gqlgen uses: fields.go:420-422);
positional connection arguments matched by type (:195-205); connection
fields as `ctx` methods; enums by their `MarshalGQL`/`UnmarshalGQL`. Each
generated method stays one line over gqlgraph (ADR 0047). Differences:

1. An input field whose SDL name is not graphql-go's derivation of its Go
   name (`AuthorID` → `authorId`, `IDNEQ` → `idNEQ`; input.go:906-918)
   gets `graphql:"authorID"`. Under compat the names are veloxe's camel
   (`customerIdNEQ`), which the derivation gives, so most fields get none.
   gogen carries a copy of the derivation with a test that runs graphql-go's
   (via the example) over every input field of the testmodel; NewSchema
   refuses a mismatch by name at start-up either way.
2. An edge renamed by `graphql.FieldName` (ADR 0043 binds it with
   `@goField(name:)`) gets its method named after the GraphQL field, so
   gqlc's case-insensitive match finds it. A clash with a declared name is
   a generation error (`renderer.declare`). Until GA1, the 26 getters
   (`<GoName>GQL`, sdl/fields.go:149) of nextapp's SDL
   (`grep -c '@goField(name:' veloxgen/velox.graphql` = 26) have the same
   problem and no such fix, as the struct field holds the name; see GA1
   and Q2.
3. Nothing else. No graphql-go import, no bindings.

Root fields, Node and nodes stay the application's, as gqlc puts every root
field on a Resolver. Each is one line:
`veloxgql.DB(ctx).Query[veloxgen.X]().Paginate(ctx, a.After, a.First,
a.Before, a.Last, veloxgen.WithXOrder(a.OrderBy),
veloxgen.WithXFilter(a.Where))` (examples/graphql/graph/
schema.resolvers.go:134), `veloxgen.ResolveNode(ctx, veloxgql.DB(ctx),
a.ID)` (gogen/node.go, ADR 0088).

### 3.5 What the application configures

gqlc.yaml keys velox's output needs, documented in doc.go and checked by
the example:

```yaml
schema: [veloxgen/*.graphql, graph/*.graphql]
autoBind: [<module>/veloxgen]
models: { ID: int64 }
modelDirective: { name: goModel, arg: model }
fieldDirective: { name: goField, forceResolverArg: forceResolver }  # + nameArg: name (GA1)
zeroForNullInputs: true   # where inputs' IsNil/NotNil and update inputs' clearX are bool
inputListPointers: true   # []*XOrder, []*XWhereInput, as the methods take them
inlineAccessors: true     # to-one edges answer from the batch
groups: { trimPrefix: velox_ }
```

Without `inputListPointers` a multi-order edge's method stops fitting
(autobind_fields.go:195-205) and the field falls to a Resolver stub; gqlc
5d906e7 makes such flips and stubs visible (upstream-feedback.md:32).

And the server:

```go
s, err := graph.NewSchema(post.Bindings(h), user.Bindings(h), graphqlgo.Scalars())
exec := graphql.NewExecutor(s, graphqlgo.With(db, veloxgql.WithLimits(l))...)
```

`graphqlgo.Scalars()` binds `Time` (`graphql.Time`), `UUID`, `Bytes`,
`Uint64`; gqlc binds `Cursor`, `JSON`, `OrderDirection` and `PageInfo`
itself, from their marshalers and fields, as it does for veloxe's
(graph/velox/generated.go:5299-5306).

### 3.6 The graphqlgo adapter

`With(db, opts...)` returns:
- an OperationInterceptor: the state (DB, limits, source) and a fresh batch
  table per operation;
- a SubscriptionInterceptor: a table per event, as gqlgen's extension does;
- with `WithMutationTx()`, a mutation runs in `db.Tx`, rolled back when the
  response has errors, data nulled, and a FieldInterceptor serialises the
  operation's fields (the parked spec's 2.5; ask GA4 removes it). Off by
  default (Q5).
- with `WithCost()`, velox's `Cost` on `oc.Operation` and `Schema().AST()`
  before `next`.

`Limits(l veloxgql.Limits) []graphql.ExecutorOption` maps MaxDepth,
MaxTokens, MaxErrors, MaxResponseBytes, Timeout onto graphql-go's options.
MaxPage, MaxList, MaxOrderTerms and the filter limits stay velox's
(gqlgraph reads them from the state). `ErrorPresenter` wraps `Present`.

## 4. What nextapp needs, mapped

| nextapp today (veloxe) | count | After Phase B |
|---|---|---|
| `veloxschema/generate.go`: WithoutGQLGen, WithSchemaGenerator, WithSchemaPath, SchemaSplitPerEntity, WithWhereInputs | 1 | `generate.Run` with `graphqlgen.Plugin({compat: veloxe, where: whitelist, engine: graphql-go, split: entity})` (ADR 0097) |
| `schema/velox_*.graphql` + `schema/velox.graphql` | 479 files | `veloxgen/velox_*.graphql` + `veloxgen/velox.graphql`; gqlc `schema:` gains `veloxgen/*.graphql` |
| `@goModel(model: "nextapp-graphql/velox/entity.X")` | 4,004 | `@goModel(model: "nextapp-graphql/veloxgen.X")` |
| gqlc `autoBind: ./velox/entity` | 1 | `./veloxgen` |
| Edge fields bound to `v.AbTest(ctx)`, `v.Participants(ctx, …)` | ≥286 (design :122) | same calls on veloxgen types (ADR 0043), now batched and projected |
| Connection/edge struct fields bound by gqlc | 477 | methods `Edges(ctx)`, `PageInfo(ctx)`, `TotalCount(ctx)` (gqlc binds methods) |
| `&entity.XConnection{}` empty/denied connections | 176 | `&veloxgen.XConnection{}` (zero = empty, 1.2) |
| `conn.Edges`, `.TotalCount`, `.PageInfo` read in services | ~1,051/109/101 (all types) | method calls, codemod |
| `r.Client.X.Query().Paginate(ctx, …, entity.WithXOrder(o), entity.WithXFilter(w.Filter))` | 446 / 453 / 373 | `veloxgql.DB(ctx).Query[veloxgen.X]().Paginate(ctx, …, veloxgen.WithXOrder(o), veloxgen.WithXFilter(w))` |
| `where.Filter` | 414 | `where` (the option takes the input) |
| `where.P()` | 51 | `where.Predicate(ctx)` / `Pred(ctx)` |
| `filter.XWhereInput` (veloxe's filter package) | 2,372 | `veloxgen.XWhereInput` |
| `gqlrelay.Cursor`, `gqlrelay.PageInfo`, `gqlrelay.OrderDirection*` | 1,443 / 636 / 27 | `veloxgql.Cursor`, `veloxgql.PageInfo`, `veloxgql.OrderDirection*` (mostly in gqlc output, regenerated) |
| `gqlrelay.MaxPaginationLimit` | 8 | `veloxgql.LimitsFromContext(ctx).MaxPage` |
| `gqlrelay.ValidateFirstLast`, `TotalCountSelected`, `CollectConnectionFields` | 2 / 1 / 1 (customobject/sort.go:697-729) | graphql-go's own `graphql.SelectionFrom(ctx).Sub("totalCount")`; first/last checks by `gqlgraph.Args` |
| Node / Nodes, `velox.Noder` | 2 resolvers, 45 refs | `veloxgen.ResolveNode/ResolveNodes`, `veloxgen.Noder` (ADR 0088) |
| Actor fields (`createdByUser`…) via `loader.Resolve*ByID` | ~4,000 resolver lines | unchanged: they are `graphql.Map` fields with `@goField(forceResolver)`, not edges |
| `loader.*` dataloadgen loaders | 4,982 refs | unchanged; generated edges no longer need them |
| Mutation inputs (own SDL + gqlc models) | 1,087 / 647 refs | unchanged (velox writes none under compat) |

What goes away: veloxe's 191 MB generated tree (5,415,009 lines, wc of
nextapp-graphql/velox), `velox/filter`, `velox/gql_node.go`, `gqlrelay`,
and the fallback that made projection and eager loading silently do nothing.
What gqlc writes stays about the same size: the SDL surface is veloxe's by
design (ADR 0087).

## 5. Compile cost and generated size at ~450 entities

Measured inputs (2026-11-22, wc and `ls`):
- the port's generated package: 479 `_gql.go` files, 434,067 lines, 17.3 MB;
  the ORM files 287,401 lines, 13.2 MB;
- nextapp's gqlc bindings today: 625 packages, 126,314 lines, 7.9 MB;
- the port's SDL: 4.6 MB in one file, 72,249 input fields.
- velox2 vs veloxe on nextapp's schema: 23 s / 4.2 GB against 190 s /
  8.4 GB, a one-file edit 16-18 s against 29-32 s (2026-11-20 CRUD compare,
  same machine).
- bench/scale n=500: plain 186 s CPU; `-graphql` (gqlgen-mode output, no
  server) 420 s CPU, link peak 654 MB (bench/scale/thresholds.json).

Estimate:
- The generated package costs what gqlgen mode costs: the Go differs by
  tags and a few method names (3.4). Tags only where the derivation
  differs: under compat, near none; native velox names (`authorID`) at
  most one per ID field. Bound: +2% bytes.
- The gqlc side is today's: same SDL surface, so about 126k lines in 625
  packages, compiled in parallel and per group on an edit.
- gqlgen leaves the graphql-go server's build: `gqlgen/graphql`, `errcode`,
  `sosodev/duration`, `levenshtein`, gqlparser's validator, and `net/http`
  from the runtime (1.4). nextapp's veloxe build never compiled gqlgen; the
  port would have, through `veloxgql`.

Limits, checked in task 7:
1. `go list -deps` of the example's `veloxgen` and `graphqlgo` has no
   `99designs/gqlgen` and no `net/http` (deps test).
2. bench/scale `500-graphqlgo` (n=500, engine=graphql-go, split=entity):
   cold-build CPU ratio ≤ the `500-graphql` threshold (29.5x the
   reference); link peak ≤ 993 MB; `_gql.go` bytes ≤ 1.02x the `-graphql`
   variant's.
3. SDL at n=500 under split: total bytes ≤ 1.01x the single file's (headers
   only); no file over 64 KB for the scale shape.
4. NewSchema of the example's graphql-go server ≤ 50 ms (parked: 28 ms for
   623 types at N=100).
5. The gqlc build of the scale shape is report-only (gqlc is not velox's).

## 6. Testing

### 6.1 examples/graphqlgo

A new example in the examples module (requires syssam/graphql-go, gqlc as
a tool), on the same schema as examples/graphql (Post, Tag/Label, User,
scope mixin), generated with `engine=graphql-go,split=entity`, bound by
gqlc into `graph/<group>/`, served by `graphqlgo.With`. Its tests port
examples/graphql's:
- e2e queries: paging both ways, cursors, where (every op kind,
  and/or/not, has/hasWith), orders, nested edges, a policy, scope, a
  mutation through the application's own resolver, errors and codes;
- `TestNestedRequestBudget`: 5 statements at n=10 and n=50, as gqlgen's
  (bench_test.go:207-241); an allocation budget of its own;
- fail closed: a server built without `graphqlgo.With` fails with ErrNoDB;
  a state with no source fails with `ErrNoSelectionSource`;
- `TestBindings`: gqlc's `Resolver` interfaces in `graph/` list no field
  velox's SDL declares, except `graphql.Map` fields (the binding-flip guard
  of field report G1);
- `-race` with application Resolve fields concurrent with velox batches.

Its own CI lane beside examples/graphql's (ci-local.sh lane list :162-277),
in ci.yml too (`TestCILocalMirrorsCI`); staleness: `velox-gen -check` and
`gqlc -check`.

### 6.2 Golden and identity tests

- engine=gqlgen output byte-identical to today's (internal/sdl, gogen,
  cmd/velox-graphql goldens unchanged); examples/graphql's veloxgen and
  `graph/generated.go` unchanged.
- engine=graphql-go goldens for sdl and gogen, with and without compat.
- split=entity: the split files' definitions equal the single file's
  (parsed with gqlparser, printed sorted).
- `graphqlgen.Plugin(opts)` writes the tool's bytes for the new options
  (graphqlgen_test.go pattern).
- deps tests (1.4).
- Adapter parity in contrib/graphql: one hand-bound schema over
  internal/testmodel served by gqlgenx and graphqlgo, same queries, same
  JSON and statement counts.

## 7. Tasks

Each task builds and passes alone, ends with `scripts/quick.sh` green, and
the branch gets one `bash scripts/ci-local.sh` before merge (CLAUDE.md
"Workflow": a large change, worktree branch).

1. **FK predicates see run-time filters.** gqlgraph's FK IsNil/NotNil and
   equality predicates add the edge's `Has` only when the target has an
   interceptor or a Read policy (`fk.target.HasRowFilter()`,
   where.go:578-599), and `Table.HasRowFilter` ignores `velox.WithFilter`
   by design (internal/engine/table.go:476-485). Add an engine predicate
   `untyped.ScopedKey(p, edge, target)`: `p`, AND the edge's `Has` when
   the target is filtered on the statement's DB (`Scoper.filtered`,
   scope.go:98), decided when the statement renders, so `Pred(ctx)` built
   outside a server is right too. gqlgraph's `scoped` and `fkNull` use it.
   Test: a `WithFilter` on the target hides a row; `authorIDIsNil`,
   `authorID`, `authorIDIn` agree with `hasAuthor`. ADR 0103.
2. **Core split, gqlgen adapter, selection seam.** Section 1 and 2.1-2.2:
   gqlgenx, the moves, `SelectionSource`, `gqlcore.Install`,
   `ErrNoSelectionSource`, Cursor's plain marshalers,
   `GraphQLExtensions`, nil-safe Connection. examples/graphql and docs
   switch to gqlgenx; API snapshot updated. Gate: every test, moved, same
   assertions; deps tests; gqlgen goldens and generated.go unchanged;
   Nested statements equal and allocs within 2%. ADRs 0104, 0105.
3. **graphqlgo adapter.** 2.3 and 3.6: source, `With`, `Scalars`,
   `Limits`, `ErrorPresenter`, `WithMutationTx`, `WithCost`, Park/Unpark.
   Test: the parity suite (6.2) on a hand-bound schema; fail-closed tests;
   `-race`. ADR 0106.
4. **engine=graphql-go output.** 3.1, 3.2, 3.4: sdl `Config.Engine`,
   `@goModel` everywhere, scalar models, no introspection binds, input
   tags by derivation, FieldName edges named after the field.
   `GeneratedCodeIsVersion28` with a CHANGELOG line. Goldens for both
   engines. ADR 0107.
5. **split=entity.** 3.3, both engines, the definitions-equal test, the
   `graphqlgen.Plugin` identity test. ADR: the next free number.
6. **examples/graphqlgo.** 6.1, with gqlc, its CI lane in ci-local.sh and
   ci.yml, coverage floor lines. Depends on 3-5.
7. **Measure and document.** bench/scale `500-graphqlgo` with thresholds
   (5); doc.go per engine, the guide's GraphQL section, gqlc.yaml keys,
   `docs/coming-from-ent.md` and the gap analysis rows #115-#117, #133
   updated; UF N-V1/N-V2/N-V3 answered.
8. **Default engine (if Q4 says so).** Flip the default to graphql-go;
   examples/graphql passes `engine=gqlgen`. ADR: the next free number.

ADRs: 0103 FK predicates and run-time filters; 0104 the veloxgql core and
its adapters; 0105 the selection source, fail closed; 0106 the graphqlgo
adapter (limits, opt-in tx and cost); 0107 engine=graphql-go output for
gqlc; 0108 veloxe compat paging and FK filters (task 3b). split=entity
and the default engine (tasks 5 and 8, parked) take the next free numbers.

### Asks of graphql-go (the user's repository; not velox work)

- GA1. gqlc `FieldDirective.NameArg` (`fieldDirective: {name: goField,
  forceResolverArg: forceResolver, nameArg: name}`): bind the field to the
  named Go field or method, as `fields: {T: {f: {name: X, method: true}}}`
  does from gqlc.yaml. Needed for velox's 26 getter fields in nextapp's SDL
  and every `graphql.FieldName` rename.
- GA2. A documented, comparable occurrence key: `FieldContext.Key()`.
- GA4. Serial execution of an operation from an OperationInterceptor
  (`oc.SetSerial()`), so `WithMutationTx` needs no FieldInterceptor.
- GA5. A tag before velox 1.0 (N-G1).
- GA6. Keep `Err` on subscription source errors: subscribe's
  `Errorf("%v", serr)` drops the source's error, so a presenter shows its
  text and cannot mask it, and a `*graphql.Error`'s code and extensions
  are lost (0106). Keep the error itself.
- (GA3, input-field directives, is not needed: see 0.4.)

nextapp must move its pin past 3e0050b to 5d906e7 for the binding-flip
report and scaffold stubs (upstream-feedback.md:32).

## 8. Open questions

- Q1. graph-gophers/graphql-go as well? Recommend no: nextapp and
  crushtask use syssam/graphql-go and gqlc; graph-gophers binds resolvers
  by reflection and has no generator. The `SelectionSource` seam would
  allow a third adapter later.
- Q2. If GA1 is declined, what of getter fields and renamed edges under
  graphql-go? Recommend velox refuses them under `engine=graphql-go` with a
  message naming the field and pointing at `graphql.Type` and a gqlc model,
  rather than emitting fields gqlc would bind to a Resolver stub.
- Q3. Separate modules for gqlgenx and graphqlgo, so neither engine is in
  the other's go.sum? Recommend not now: compile cost goes by package, and
  each new module adds a CI lane and a coverage file; split before 1.0 if
  the user wants clean go.sums.
- Q4. Default engine. The user chose graphql-go as the default on
  2026-11-03, before GraphQL was parked. Recommend keeping gqlgen the
  default through tasks 1-7 (byte-identity gates rely on it) and flipping
  in task 8 after the user reconfirms.
- Q5. Mutation transaction on graphql-go by default, as gqlgen's extension
  has it (extension.go:21-66)? Recommend off by default: graphql-go
  resolves nested fields concurrently, a transaction's DB is not safe for
  that, and nextapp's services open their own transactions. Opt-in
  `WithMutationTx` until GA4.
- Q6. velox's Cost, document bounds and response budget on graphql-go?
  Recommend graphql-go's own (`WithQueryCost` and the rest), which nextapp
  already configures, with velox's `Cost` opt-in through `WithCost`; do not
  port budget.go.
- Q7. Root fields per entity under split=entity (`extend type Query` in
  each file, so gqlc's `rootFields: file` spreads nextapp's 452 root
  resolvers and graph/velox's 7,228-line generated.go across groups)?
  Recommend veloxe's layout now (all root fields in velox.graphql), and an
  option `query=entity` only if graph/velox's compile measures hot.
- Q8. Export `SelectionSource` for third-party servers? Recommend no until
  someone asks; it stays in `veloxgql/internal/gqlcore`.
- Q9. Should `graphqlgo` consult `SelectedField.Withheld` to skip columns
  of authorization-withheld fields? Recommend no: reading a column too many
  is safe, and Withheld's cost is a decision lookup per field.
