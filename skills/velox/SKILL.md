---
name: velox
description: Use when a Go project imports github.com/syssam/velox, or when writing or changing a veloxschema package, velox-gen output, a velox query, update, transaction, policy or migration.
---

# velox

velox is a Go ORM with an ent-style schema, generated entities and column
handles, and a generic runtime. Its rules differ from ent's and from other
ORMs' in ways that fail at run time: a read without `Limit`, an update
without `Where`, an `Optional` field that is still NOT NULL.

Read the rules and the commands first:

```sh
VELOX=$(go list -m -f '{{.Dir}}' github.com/syssam/velox)
cat "$VELOX/docs/for-agents.md"
```

Then open the guide section your task names, in `$VELOX/docs/guide.md`:
schema, reading, relations, writing, transactions, policies, scope, soft
delete or raw SQL. A schema that came from ent also needs
`$VELOX/docs/coming-from-ent.md`.

`veloxgen/` is generated: change the schema and run `go tool velox-gen
./veloxschema`.
