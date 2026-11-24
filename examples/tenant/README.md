# tenant

`veloxgen.Project` belongs to an organization. velox has no tenant concept: the
scope is a `Policy` and a hook that the `InOrg` mixin declares, reading a
`scope.Key` from the context, so that a soft delete or an owner rule is
written the same way.

```sh
cd examples
go run ./tenant
```

```go
// veloxschema/inorg.go
var OrgKey = scope.NewKey[int64]("org")

type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []velox.Field { return []velox.Field{field.Int64("org_id").Immutable()} }
func (InOrg) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
			org, err := OrgKey.Require(ctx) // scope.ErrNoScope without one
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ("org_id", org), nil
		},
		Insert: func(ctx context.Context, _ *velox.DB, r velox.Row) error {
			return OrgKey.Match(ctx, r.Value("org_id")) // scope.ErrMismatch
		},
	}
}
func (InOrg) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(func(ctx context.Context, m *velox.Mutation) error {
			if org, ok := OrgKey.From(ctx); ok {
				return m.Fill("org_id", org) // each row that leaves it zero
			}
			return nil
		}, velox.OpInsert),
	}
}

// veloxschema/project.go
func (Project) Mixin() []velox.Mixin { return []velox.Mixin{InOrg{}} }
```

The policy and the hook name the column by its schema name, `org_id`, so
the schema does not import `veloxgen`. velox-gen lists `InOrg`'s policy and hook
in `veloxgen.Project`'s descriptor, before the `Policy` and `Hooks` a `Project`
declares itself, if any. The policies are ANDed, so Project's own cannot
lift the scope. `scope_test.go` checks it with `scopetest.Isolates`, which
also checks that `org_id` is `Immutable`, NOT NULL and an `int64`.

```go
ctx = veloxschema.OrgKey.With(ctx, acme)          // acme is an int64, the column's type

db.Insert(&p).Exec(ctx)                // fills p.OrgID
db.Query[veloxgen.Project]().Count(ctx)     // ... WHERE "org_id" = ?
db.Update[veloxgen.Project]()...Exec(ctx)   // changes only this organization's rows

db.Insert(&p).OnConflict(P.OrgID, P.Name).Update(P.Status).Exec(ctx) // update branch is scoped too

report := db.WithoutPolicy("weekly status report") // a DB that is unscoped, with a reason
```

It fails closed:

| What | Error |
|---|---|
| no `OrgKey.With` (or the zero value) | `scope.ErrNoScope` |
| inserting a row whose `OrgID` names another organization | `scope.ErrMismatch` |
| `Set` of `OrgID` (it is `Immutable`) | `velox.ErrImmutable` |

What the scope does not do: it does not check that a foreign key names a row
of the same organization, and it does not reach a bare many-to-many join table.
A composite foreign key, `(org_id, project_id)` to `(org_id, id)`, makes the
database refuse both, for every writer of the table; `velox-gen` does not write
one for you. An edge schema gives a join table its own `Policy`.

The output is in `testdata/output.golden`.
