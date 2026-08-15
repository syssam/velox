# Multi-tenant isolation

A runnable reference for isolating tenants without breaking the reads that
guard invariants. Everything here is exercised by `e2e_test.go`.

## The shape

Two clients over one connection pool:

| | Reads | Writes |
|---|---|---|
| `Clients.Scoped` | narrowed to the viewer's tenant | tenant-filtered by the Policy |
| `Clients.System` | **not** narrowed | tenant-filtered unless the viewer has the system role |

Wire it so request handlers receive `Scoped` and services receive `System`.
The distinction then holds by construction, rather than by anyone
remembering an opt-out at every call site.

## Why reads and writes use different mechanisms

**Reads: an interceptor, registered on `Scoped` only.** Interceptors live on
the client (`c.interStore`), so `System` is untouched by construction.

**Writes: a `Policy()` on `TenantMixin`.** A Policy is process-global, which
is correct here — there is no such thing as a legitimate *silently*
cross-tenant write. Interceptors could not do this job in any case: they
never run on mutations, so a filter built only from interceptors leaves
`Update().Where(...)` and `Delete().Where(...)` unscoped.

The reverse assignment is what goes wrong in practice. Scoping reads with a
global Policy narrows every read in the process, including the ones that
guard an invariant:

```go
// through Scoped, this returns false whenever the blocking order belongs
// to another tenant — and the delete proceeds
blocked, _ := client.SalesOrder.Query().
    Where(salesorder.CustomerIDField.EQ(id), salesorder.ActiveField.EQ(true)).
    Exist(ctx)
```

A narrowed read of the first kind leaks data and can be audited afterwards.
A narrowed read of the second kind destroys data and cannot. Only the call
site knows which kind it is, which is why the choice is a handle rather than
a context flag — a context value is inherited invisibly down a call stack, a
client is not.

## The tenant column is stamped, not accepted

`TenantMixin`'s hook sets `tenant_id` from the viewer on every create,
overwriting whatever the caller passed. That closes two holes: a create
omitting the column would otherwise pass every rule — a policy whose rules
all skip allows — and land a row with an empty tenant that no tenant can
read; and a caller could otherwise plant a row in someone else's tenant.

## Known gaps

- **Edge predicates are not scoped.** `HasOrdersWith(...)` compiles to a
  subquery built directly on a `*sql.Selector`; it never becomes a Query, so
  neither mechanism observes it. Treat edge predicates as unscoped in
  tenant-sensitive paths. Ent has the same limitation.
- **Raw driver access bypasses everything**, by definition.

## Auditing

`WithSystem` is the only way to lift the write filter, and the system role
has to be constructed rather than inherited — so `grep -rn WithSystem` is
the complete list of places that can write across the tenant boundary.
