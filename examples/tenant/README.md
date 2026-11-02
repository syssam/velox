# tenant

`gen.Project` belongs to an organization. velox has no tenant concept: the
scope is a `Policy` and a hook, written in `gen/scope.go` with the `scope`
package, so that a soft delete or an owner rule is written the same way.

```sh
cd examples
go run ./tenant
```

```go
var Org = scope.NewKey[int64]("org")
var projectScope = scope.By(Org, Projects.OrgID.EQ, func(x *Project) *int64 { return &x.OrgID })

func (*Project) Policy() velox.Policy[Project] { return projectScope.Policy() }
func (*Project) Hooks() []velox.Hook[Project]  { return []velox.Hook[Project]{projectScope.Fill()} }
```

```go
ctx = gen.Org.With(ctx, acme)          // acme is an int64, the column's type

db.Insert(&p).Exec(ctx)                // fills p.OrgID
db.Query[gen.Project]().Count(ctx)     // ... WHERE "org_id" = ?
db.Update[gen.Project]()...Exec(ctx)   // changes only this organization's rows

db.Insert(&p).OnConflict(P.OrgID, P.Name).Update(P.Status).Exec(ctx) // update branch is scoped too

report := velox.WithoutPolicy(ctx, "weekly status report") // unscoped, with a reason
```

It fails closed:

| What | Error |
|---|---|
| no `Org.With` (or the zero value) | `scope.ErrNoScope` |
| inserting a row whose `OrgID` names another organization | `scope.ErrMismatch` |
| `Set` of `OrgID` (it is `Immutable`) | `velox.ErrImmutable` |

What the scope does not do: it does not check that a foreign key names a row
of the same organization, and it does not reach a bare many-to-many join table.
A composite foreign key, `(org_id, project_id)` to `(org_id, id)`, makes the
database refuse both, for every writer of the table; `velox-gen` does not write
one for you. An edge schema gives a join table its own `Policy`.

The output is in `testdata/output.golden`.
