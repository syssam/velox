# runtime

Generic runtime library for Velox-generated code.

This package provides base types and helper functions used by generated entity code:

- **QueryBase** -- Shared query state (driver, table, predicates, edges)
- **QueryScan / QuerySelect / QueryGroupBy** -- Query execution paths; `QuerySelect` handles both plain `Select(fields...).Scan()` and `Aggregate(fns...).Int/Scan/...` by honoring the Selector's registered aggregate functions
- **LoadConfig** -- Eager loading configuration with nested edge support
- **Config** -- Runtime configuration; `HookStore` and `InterStore` carry shared `*entity.HookStore` / `*entity.InterceptorStore` pointers so mutation hooks and query interceptors registered on any client reach all code paths
- **Scanning** -- Row scanning, value extraction, and type conversion (`ScanAll`, `ScanFirst`, `ScanOnly`)
- **Registry** -- `RegisterEntity`, `RegisterQueryFactory`, `FindMutator`, `NewEntityQuery` — the decoupling layer that lets runtime dispatch mutations/queries without importing entity sub-packages
- **Node** -- Relay Node interface support and resolver registry

Mutation state (op, id, typed field pointers, typed edge state, typed `oldValue` closure) lives directly on the generated `*EntityMutation` struct — there is no shared `MutationBase` pointer. This matches Ent's layout.

Generated code imports this package; application code typically does not.
