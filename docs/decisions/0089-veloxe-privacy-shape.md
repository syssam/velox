# 0089. velox accepts veloxe's privacy policies

- Status: accepted, 2026-11-19. The user chose that the largest consumer's
  veloxe schemas compile and behave unchanged on velox.

## Context

The consumer's veloxe schemas declare 65 entity policies, all of one shape:

```go
func (PerformanceGoal) Policy() velox.Policy {
	return privacy.Policy{Query: privacy.QueryPolicy{
		rule.RequireScopeOrSelfAnyToRead("performancegoal", cols, scopes),
	}}
}
```

and one mixin policy, `func (WorkspaceIdMixin) Policy() velox.Policy { return
rule.FilterWorkspaceRule() }`. veloxe's `velox.Policy` was an interface
(`EvalQuery`, `EvalMutation`), `privacy.Policy` a struct of a query and a
mutation rule list implementing it. velox's `velox.Policy` is a struct of
functions (Read, Write, Insert, 0007, 0008, 0039). A `privacy.Policy` literal
returned as a `velox.Policy` compiles only if the two are one type.

veloxe's semantics that the consumer relies on:

- A rule list runs in order: Skip (or nil) goes on, Allow ends the list
  allowing, any other error denies. Predicates a rule added (`WhereP`) before
  the end stay. A list that runs out allows.
- Each policy's list ends alone: `QueryPolicy.EvalQuery` turns Allow into nil,
  so `privacy.NewPolicies(mixin, entity)` went on to the next policy. A
  tenant pin in the mixin is never lifted by an entity's Allow.
- The query policy applies to queries only. The consumer says so in its
  schemas ("Writes are unaffected: privacy.Policy{Query: …} constrains
  queries only, so the webhook writer and outbox transitions keep working
  under any context"). velox's Read applies to updates and deletes too.
- `privacy.DecisionContext(ctx, privacy.Allow)` lifts the rule lists for a
  read the caller is already authorized for (two service call sites).

## Decision

- `velox.Policy` gains two fields, `Query QueryPolicy` and `Mutation
  MutationPolicy`. Package `github.com/syssam/velox/privacy` declares
  `Policy = velox.Policy` and veloxe's other names: `Decision`, `Allow`,
  `Deny`, `Skip`, `Allowf`/`Denyf`/`Skipf`, `IsDecision`, `QueryRule`,
  `QueryPolicy`, `MutationRule`, `MutationPolicy`, `QueryMutationRule`,
  `QueryRuleFunc`, `MutationRuleFunc`, `FilterFunc`, `Filter`,
  `Filterable`, `Query`, `Mutation`, `OnMutationOperation`,
  `DenyMutationOperationRule`, `AlwaysAllowRule`, `AlwaysDenyRule`,
  `ContextQueryMutationRule`, `DecisionContext`, `DecisionFromContext`. The
  types live in `internal/engine`, which evaluates them.
- A rule narrows through `Filter.Where(...velox.Predicate)`. The filter also
  has `HasColumn(name) bool`, so a rule shared by entities can check a column
  as veloxe's generated filters let it.
- Query's rules become a select-only row condition (`EntityPolicy.Query`).
  It is ANDed after Read's into every statement that only reads: a SELECT
  that is not Writable, with its EXISTS and joins (`Scoper.Reads`). It is not
  applied to an UPDATE, a DELETE, a Save, a Writable probe or an upsert's
  update branch, nor to anything inside them.
- Mutation's rules judge each inserted row (op `OpInsert`, `Value` reads the
  row, `Filter.Where` is ignored, as veloxe ignored predicates on a create)
  after Insert, and narrow every UPDATE and DELETE after Write. The engine
  passes Write the statement's kind for this (`OpUpdate` for a Writable
  probe); a velox Write ignores it.
- Each Policy's lists end alone, and the bound policies AND as before: the
  mixins' first, then the entity's. A Deny is a `*velox.PolicyError` wrapping
  the rule's error, so `errors.Is(err, privacy.Deny)` and
  `errors.Is(err, velox.ErrDenied)` both hold.
- A decision in the context replaces every Query and Mutation list: Allow
  lifts them, any other denies. It does not lift Read, Write, Insert or an
  interceptor; `WithoutPolicy` lifts all.
- velox-gen accepts `Policy() privacy.Policy` as well as `Policy()
  velox.Policy`, and counts a `privacy.Policy` literal's Query and Mutation
  keys as conditions, which the join entity of a bidirectional edge refuses.
- When a schema's Policy method is in a file that imports velox's privacy,
  velox-gen also writes `<out>/privacy/privacy.go`: package privacy beside
  the entities, as veloxe generated it, whose names are aliases of velox's
  privacy package's. A veloxe application's service code imports
  `<module>/<out>/privacy` for `DecisionContext` and `Allow`, and it builds
  unchanged. veloxe's per-entity rule funcs (`UserQueryRuleFunc`...) are left
  out: no application code used them, they cost a type per entity, and
  velox's `QueryRuleFunc` and `FilterFunc` take any entity's query. A schema
  written with `velox.Policy` alone gets no such package.

## Consequences

- The consumer's schema files compile as they are, given a `rule` package
  whose functions return `privacy.QueryRule` and whose `FilterWorkspaceRule`
  returns a `velox.Policy` (Read and Insert), as its velox port already does.
  The rules themselves are rewritten from `WhereP(func(*sql.Selector))` to
  `Filter.Where(velox.Predicate)`: velox has no `dialect/sql` selector.
- `NewPolicies` and `Policies`, which veloxe's generated runtime called, are
  not provided: velox composes the mixins' and the entity's policies itself.
  `RecordTrace` is not provided.
- A Query condition applies inside an EXISTS or join of a read (Has,
  HasWith, With loads), as Read does. veloxe's edge predicates applied the
  target's policy too (`runtime.ApplyEntityPolicy`), but not its
  interceptors; velox applies both (amended 2026-11-22).
- When one entity mixes Read and Query across its policies, all Reads run
  before all Queries, whatever the policies' order; only which denial is
  reported can differ.
- `OnMutationOperation` compares the op with `==`: velox's ops are kinds,
  not veloxe's bit set (no OpUpdateOne or OpDeleteOne).
- A Policy literal that names a field mentions the new field types: a
  Policy and three hooks grow an entity's package by about 1.5 KB (the
  compile-cost bound is now 87040 bytes). `GeneratedCodeVersion` is
  unchanged: the generated code is the same.
