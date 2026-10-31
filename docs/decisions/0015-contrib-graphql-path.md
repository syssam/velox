# 0015. The module path stays contrib/graphql

- Status: accepted, 2026-10-29

## Context

The annotation package `github.com/syssam/velox/contrib/graphql` is named
`graphql`, as gqlgen's is (R8). Renaming the module to `contrib/veloxgql`
would give `contrib/veloxgql/veloxgql` for the runtime. ent's `entgql` is a
single package under `entgo.io/contrib/entgql`, a different layout.

## Decision

The module path `contrib/graphql` stays, and so does the annotation package's
name `graphql`, as the naming spec ruled. A file that also imports gqlgen's
graphql package names this one `vgraphql`:

    import vgraphql "github.com/syssam/velox/contrib/graphql"

## Consequences

Schema code that sits beside gqlgen code uses the alias. `vgraphql` is not
taken by gqlgen or entgql.

## Evidence

- contrib/graphql/go.mod: `module github.com/syssam/velox/contrib/graphql`.
- contrib/graphql/doc.go, its first paragraphs (commit 1d233f4).
- Spec ruling 19, docs/specs/2026-10-25-pre-1.0-hardening-design.md:57.
- Decision 3 and controller ruling 3, docs/plans/2026-10-29-hardening-6-docs.md.
