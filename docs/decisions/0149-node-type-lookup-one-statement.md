# 0149. node(id:) finds its types with one statement

- Status: accepted, 2026-11-24. Amends 0088.

## Context

Under compat=veloxe, `gqlgraph.ResolveNode` probed every type of Node with
`LoadNode`, one query each, and `ResolveNodes` read every type with one
`IN` query (0088). nextapp has about 500 types of Node, so one `node(id:)`
cost about 500 statements, and so did one `nodes(ids:)`. Its IDs are
snowflakes, unique across tables, so all but one of those statements find
nothing.

## Decision

- `ResolveNode` and `ResolveNodes` first find which types hold the IDs,
  with one statement through `db.Rows`, so on the DB's connection, a
  transaction's included: a `UNION ALL` of one `SELECT` per type, which
  yields the type's index for each row it finds:
  `SELECT 0 FROM "badges" WHERE "id" = ? UNION ALL SELECT 1 FROM "people"
  WHERE "id" IN (?, ?) …`. Then they read each type found as before:
  `LoadNode`, or one `IN` query, under the type's Read policy, scope and
  interceptors.
- The lookup decides only which types are read. The reads decide the
  result, as before: a row a policy hides or denies is none, and an ID is
  ambiguous only when two types show the caller a row. A type the lookup
  does not find has no row of the ID, seen or not, so skipping it changes
  nothing. Deciding ambiguity from the lookup would tell a caller that a
  row it may not read exists.
- The `SELECT` yields only a type index, not the key, so the `UNION ALL`
  has one integer column and needs no common key type. Each type binds its
  own keys, converted to its column's type (`Table.Convert`) and bound as
  an `int64` or a `string`.
- The lookup reads a type whose key column is of an integer type of at
  most 32 bits unsigned, any signed integer type, or a string type, with
  no codec. Any other type (a `uint64`, a `[]byte`, a UUID with a codec)
  is read whatever the lookup finds, as before. A type of two key columns
  is an error before any statement, as before.
- A lookup that a hook denies (`ErrDenied`) reads every type, as before;
  any other error is returned.
- The statements are cached per list of types, dialect and number of
  keys: at most 32 lists and 64 statements per list, so an application
  that passes a new list each call cannot grow the cache. A statement binds
  at most 30000 parameters, under SQLite's 32766, and holds at most 500
  `SELECT`s on SQLite (`SQLITE_MAX_COMPOUND_SELECT`'s default); past
  either, the lookup is more than one statement.
- Quoting and placeholders are velox's, written in gqlgraph: double quotes,
  backquotes on MySQL; `$n` on PostgreSQL, `?` elsewhere. Only literal
  integers, quoted identifiers and placeholders appear, so the SQL is the
  same on the three dialects.

## Why

- One statement, not one per type: at 500 types `node(id:)` was 500
  statements; now it is the lookup and one read.
- Raw SQL, not velox's builder: the builder writes one table per
  statement and applies the policies, which the lookup must not, as a
  denial would hide a type that the read must still report as none.
- Not cast keys to text to return them: PostgreSQL normalises a UUID's
  text, MySQL compares strings without case, and the casts differ by
  dialect; the reads already map rows to IDs.

## Consequences

- `node(id:)` is 2 statements when one type holds the ID, 1 when none
  does, and 3 for an ambiguous ID of two types. `nodes(ids:)` is 1 plus
  one per type found. The lookup is one statement up to 30000 keys in
  all (500 types of 60 IDs each), and on SQLite up to 500 types; 100 IDs
  of 500 types are two.
- The lookup is raw SQL, so the Around hooks see an `OpRawQuery` named
  "Rows" with the reason `velox-graphql: which types of Node hold the IDs;
  …` as its policy bypass.
- No generated code changes; `veloxgql.GeneratedCodeIsVersion27` stays.
  Only the generated doc comments of `ResolveNode` and `ResolveNodes`
  changed.

## Evidence

- `TestNodeLookup` in `contrib/graphql/veloxgql/gqlgraph`: the statements
  by table for a person, a missing ID, a hidden badge, a denied vault, an
  ambiguous ID and `nodes(ids:)`; the lookup's SQL; a hook that denies the
  lookup.
- `TestNodeLookupFallback`: a `[]byte` key is read beside the lookup.
- `TestRenderLookup`, `TestRenderLookupChunks`, `TestNodePlan`,
  `TestBindKey`: each dialect's SQL, a schema, the 500 and 30000 bounds,
  the caches.
- `TestParity/node` in `contrib/graphql/veloxgql/graphqlgo`: `node` with
  an author and tags is 4 statements on both backends, where it was 5.
- `TestResolveNode`, `TestResolveNodes`, `TestResolveNodesCap`,
  `TestNodeStatements` pass unchanged; `TestResolveNodesBatched` reads
  the lookup and children, not owners, which hold none of its IDs.
