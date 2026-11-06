# 41. An entity's relations live in its Edges struct

Status: accepted, 2026-11-06.

## Context

velox-gen wrote an entity's relations as fields beside its columns:

```go
type Issue struct {
	ID        int64 `json:"id"`
	ProjectID int64 `json:"project_id"`
	Project   velox.One[Project]  `json:"project,omitzero"`
	Comments  velox.Many[Comment] `json:"comments,omitzero"`
}
```

A reader had to tell a column from a relation by its type. ent puts the
relations in a struct of their own, `Edges UserEdges`, with
`json:"edges"`, and an ent user looks for them there.

## Decision

- An entity with relations has a field `Edges XEdges`, after its columns,
  tagged `json:"edges,omitzero"`. `XEdges` holds one `velox.One[T]` or
  `velox.Many[T]` per relation, each tagged `json:"<edge>,omitzero"`.
  An entity with no relations has neither the field nor the type.
- One and Many keep the loaded state: `x.Edges.Project.Get()` returns the
  value and whether it was loaded. There is no `loadedTypes` and no
  per-edge `ProjectOrErr`, as ent has, because the state is in the field.
- `Descriptor.EdgeFieldPtr` returns `&x.Edges.<Field>`. The runtime reads a
  relation only through it, so nothing else in velox changed. The handles,
  `gen.Issues.Project`, are unchanged.
- `XEdges` joins velox-gen's check of the names gen declares at its top
  level: a schema named `IssueEdges` beside an `Issue` with relations is
  refused, `edges type IssueEdges collides with schema IssueEdges`.
- An edge's `Comment` documents its field; without one the field reads
  "Project is the edge project, to Project."
- `String` prints the columns alone, as before.
- `GeneratedCodeIsVersion18`.

## Consequences

- `x.Project.Get()` is `x.Edges.Project.Get()`.
- JSON nests the loaded relations under `"edges"`. A struct of One and
  Many is zero when none was loaded, so `omitzero` leaves `"edges"` out.
- Compile cost does not change measurably. `bench/scale -n 500 -s 100`,
  darwin/arm64, one run each on a shared machine, base 0688a286 against
  this change: cold build 253.4 s CPU (6.44x the reference) against
  264.7 s (7.10x); schema edit and rebuild 215.1 s (5.47x) against
  212.5 s (5.70x); a service 4.3 MB against 4.2 MB; link peak 210 MB
  against 203 MB. The reference build itself varied from 35 to 39 s.

## Evidence

- `internal/codegen`: `TestLowerCollisions` (`edges`), the goldens.
- `internal/apitest`: `TestEdgesJSON`.
- The examples and test models, regenerated.
