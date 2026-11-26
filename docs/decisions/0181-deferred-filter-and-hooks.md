# 181. A filter and hooks given after Open fail closed until given

Status: accepted, 2026-11-26.

## Context

A row-scope filter and a write hook often need a service that takes the DB:
a lookup of the user's grants, a check that reads a table. The DB must exist
before the service, and the filter before the first statement, so the circle
cannot be closed with `WithFilter` and `WithHooksAll` alone. An application
solved it with a slot installed at open and filled later, which lets every
statement through until it is filled. A statement that runs early (a backfill
in a startup hook) is then unfiltered, and nothing says so.

## Decision

`velox.Deferred` is that slot, and it fails closed:

- `NewDeferred()`, then `late.Option()` at open, installs a filter and a hook
  for every entity (`WithFilter`, `WithHooksAll`).
- Until `late.Set(filter, hooks...)`, the filter returns `ErrNotReady` and
  the hook fails the write with it, so every statement is refused and
  nothing is sent. `WithoutPolicy` does not lift a filter (0038), so it does
  not lift this either.
- `Set` runs once; a second call returns an error and changes nothing. A
  nil filter allows. Nothing is added to the DB: the filter and hooks are
  read through an atomic pointer on each statement.
- A DB opened without `Option` waits for nothing.

One `Set`, not an append, because the late part of a configuration is one
decision: giving it in pieces would let a part of it run without the rest.
A caller that builds the filter and hooks in several places gathers them
before it calls `Set`.

## Consequences

An application that reads before `Set` (a backfill, a migration check run
through the DB) must call `Set` first. The error names the cause.

Per statement cost after `Set` is one atomic load, and a hook chain built as
every hook's is (0030).

Rejected: opening the DB twice on one `*sql.DB`. The service would keep the
first, unfiltered one. Rejected: a `Set` that may be called again, to swap
the filter: a swapped filter changes what a running request may see.
