# 0079. A subquery on another entity, by name: FieldInSelect

- Status: accepted, 2026-11-19.
- Amended 2026-11-19 by [0081](0081-entity-table.md): the entity is found in the
  package's entity table, by name or by table, and need not be a constant;
  `Descriptor.Links` is gone.
- Amended 2026-11-19 by [0091](0091-policy-cycles-and-text-keys.md): a cycle is
  cut, not refused, and an integer column against a text key is compared as
  text.
- Amended 2026-11-24 by [0157](0157-field-in-select-raw.md): a text column
  against an integer key is compared as text too, the key cast, and
  `FieldInSelectRaw` is the same subquery with no policy, filter or
  interceptor of the target inside.

## Context

`column.InSelect(sub)` (ADR 0057) needs two typed handles, so it can be written
only where the generated package is imported. A schema's Policy, Hooks and
Interceptors are not: they sit in the schema package, which the generated one
imports, and they name columns and relations by string (`FieldEQ`, `HasEdge`).
The largest consumer of the old velox has 13 policy rules that narrow one table
by the rows of another (a row is visible when its `org_id` is among the
organisation ids the viewer reads). Without a by-name form, a port had to drop
the rule's arm, which widens what the policy allows.

## Decision

`velox.FieldInSelect(column, entity, key string, ps ...Predicate)` is "the
entity's `column` is in the `key` column of the rows of `entity` that meet
`ps`", written as the `EXISTS` that `InSelect` writes (`ColRef`), so:

- The target's Read policy, scope and interceptors apply inside it, as they do
  for `HasEdgeWith`. `Not(FieldInSelect(...))` is `NOT EXISTS`, which a NULL in
  the subquery does not empty.
- It is resolved when a statement is prepared, like `FieldEQ` and `HasEdge`:
  the column and key by name, the types compared, the conditions in `ps` by the
  target's names. A name that does not exist, or two columns of different types,
  is an error, never a dropped arm, so the policy still fails closed.
- `entity` is the schema's Go type name. The runtime has no registry of
  entities by name, so the generated `Descriptor` carries `Links`
  (`velox.LinkEntity[Doc]("Doc")`) and velox-gen writes it, exactly as it
  writes `velox.Linked` for `HasEdge`.

velox-gen reads the call with go/types, as it reads `HasEdge`. `entity` must be
a constant, and the call is refused at its line when the entity is not a schema
of the package, the column or the key is not a column, their types differ, or a
condition in `ps` names a column the target lacks (the conditions are checked
against the target entity, including a `HasEdge` inside them, whose relation is
linked on the target). A column or key that is not a constant is a warning, as
elsewhere, and the runtime checks it.

`GeneratedCodeIsVersion21` replaces Version20: the descriptors have `Links`.

## Not done

- A `privacy` package with ent's typed `FilterFunc` and `QueryRule`: they take
  a typed filter, which the untyped, fail-closed policy of ADR 0034 and 0040
  does not have. A rule that narrows by the viewer's organisation is `scope.By`;
  the rest are written as a `Predicate`, now including those that read another
  table.
- A cycle (the target's own policy reaching the entity again through an
  `EXISTS`) was refused when applying the policy, as it was for relations;
  0091 cuts it instead.
- MySQL cannot select from the table being updated in a single statement
  (error 1093): a policy on an update that names its own entity in
  `FieldInSelect` fails there, as the same `EXISTS` on `HasEdgeWith` does.
