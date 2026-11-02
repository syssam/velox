package gen

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"
)

// Org is the organization a request acts for. Middleware sets it once with
// Org.With(ctx, id); a statement on a scoped entity without it fails with
// scope.ErrNoScope.
var Org = scope.NewKey[int64]("org")

var projectScope = scope.By(Org, Projects.OrgID.EQ, func(x *Project) *int64 { return &x.OrgID })

// Policy confines Project's reads, updates and deletes to the context's
// organization, and refuses an insert for another. velox-gen binds it into the
// entity's metadata.
func (*Project) Policy() velox.Policy[Project] { return projectScope.Policy() }

// Hooks fills OrgID on an insert that leaves it unset.
func (*Project) Hooks() []velox.Hook[Project] {
	return []velox.Hook[Project]{projectScope.Fill()}
}
