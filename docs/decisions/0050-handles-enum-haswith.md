# 50. Exported handle types, enum handles and HasWith

Status: accepted, 2026-11-07. Amends 0036 (point 3).

## Context

A review of the generated code found three things below the bar:

- `var Posts = postHandles{…}`: the type was unexported, so `go doc` and
  pkg.go.dev showed the variable's literal and not the handles' comments.
- An enum field's handle was a `StringCol`, which compiles
  `Posts.Status.Contains("dr")`, `HasPrefix` and `Like`: text operations no
  value of an enum needs, and ent's enum predicates do not have.
- The relation condition was `Rel.HasWhere`, where schema behaviour says
  `velox.HasEdgeWith`, the GraphQL filter `hasTagsWith` and ent
  `HasTagsWith`.

## Decision

- The handles' type is exported, `PostHandles`. velox-gen refuses a schema
  whose name collides with it. A hover says `var gen.Posts gen.PostHandles`.
- An enum field's handle is `velox.Col[E, V]` (`NullCol` when Nillable):
  EQ, NEQ, In, NotIn, Asc, Desc, Key and Set, as ent's enum predicates.
- `Rel.HasWhere` is `Rel.HasWith`, one name across velox and ent.
- velox's generated code is version 19.

## Cost

`Col` with a string shape is one more instantiation in each entity package
that has an enum. examples/tracker's gen (two entities with enums), built
without DWARF, went from 1,266,322 to 1,279,868 bytes (+1.1%); the tracker
binary is the same size to the byte. The exported name costs nothing: with
the type unexported again the package is the same size.
