package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/scope"
)

// InOrg confines an entity's rows to the context's testmodel.Org: reads,
// updates and deletes see only its rows, and an insert gets its id.
type InOrg struct{ mixin.Schema }

// Fields returns org_id. It is Immutable, so that no update moves a row to
// another organization.
func (InOrg) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Immutable()}
}

// Policy confines statements to the context's organization.
func (InOrg) Policy[E any]() velox.Policy[E] {
	return scope.By[E](testmodel.Org, "org_id").Policy()
}

// Hooks fills org_id on insert.
func (InOrg) Hooks[E any]() []velox.Hook[E] {
	return []velox.Hook[E]{scope.By[E](testmodel.Org, "org_id").Fill()}
}
