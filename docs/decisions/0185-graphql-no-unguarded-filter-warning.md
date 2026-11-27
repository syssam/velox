# 185. velox-graphql does not warn of filters and orders on a directive's field

Status: accepted, 2026-11-27. Supersedes 0156's section "Directives that do
not reach predicates or orders".

## Context

0156 made an application directive reach the object type's field only, and
warned of every field that carries one and still has where predicates or an
order value:

```
Post.email has @admin, but its where predicates are not guarded by it;
drop graphql.WhereOps or graphql.WhereInput, or guard the field another way
```

`graphql.OrderUnguarded` could accept the order half. Nothing could accept
the where half.

nextapp (a large multi-tenant CRM/ERP on velox) got this warning 145 times on
every generation. In nextapp:

- Its `@sensitive` controls no access: it masks log output and records a
  PII-read audit entry.
- Access to a field, including to filtering and ordering by it, is the
  application's field control, which refuses a `where` or `orderBy` key on a
  field the caller may not see.
- Searching customers by email and phone is the product, so dropping the
  predicates was not an option.

The warning assumed every application directive is an access guard. When it
is not, the warning can only be lived with, and 145 permanent warnings hide
the one that matters.

The precedents do not warn either:
- entgql, the plugin velox-graphql follows, has only per-field opt-outs
  (`entgql.Skip(SkipWhereInput)`, `SkipOrderField`), and velox-graphql has
  the same (`graphql.Skip`, no `WhereOps`).
- Hasura and PostGraphile tie filtering to the permission system or to a
  per-column opt-in, not to directives.

## Decision

The warning goes. Which caller may filter or order by a field that carries a
directive is the application's access control. velox-graphql does not judge
it: the field-level directive, the entity's `velox.Policy` and the
application's own checks do.

Kept:
- The refusal of `graphql.OrderField` on a field with an application
  directive, unless `graphql.OrderUnguarded` allows it. It is an error with
  an explicit opt-in, so it is never permanent noise, and an annotated order
  is a choice the schema makes on purpose.
- The refusals on edges (`OrderVia`, `OrderField` on an edge with a
  directive).

Under `orderfields=all`, which orders such a field without refusing it,
`OrderUnguarded` now changes nothing. Its only effect there was to silence
the warning.

## Consequences

- No output changes: the warning never changed the SDL.
- An application whose directives are access guards and that filters on
  their fields gets no reminder from the generator. It should leave those
  fields without predicates (`graphql.Skip(graphql.SkipWhereInput)`, no
  `WhereOps`), as under entgql.
