package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/scope"
)

// OrgKey carries the caller's organization. A request sets it with
// OrgKey.With(ctx, id); a statement on an InOrg entity without it fails with
// scope.ErrNoScope.
var OrgKey = scope.NewKey[int64]("org")

// InOrg confines an entity's rows to the context's organization: reads,
// updates and deletes see only its rows, and an insert gets its id.
// velox.WithoutPolicy lifts it, with a reason, as it lifts every policy.
type InOrg struct{ mixin.Schema }

// Fields returns org_id. It is Immutable, so that no update moves a row to
// another organization, and graphql.Skip leaves it out of every GraphQL type
// and input: the hook fills it.
func (InOrg) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Immutable().Annotations(graphql.Skip())}
}

// Policy confines statements to the context's organization.
func (InOrg) Policy[E any]() velox.Policy[E] {
	return scope.By[E](OrgKey, "org_id").Policy()
}

// Hooks fills org_id on insert.
func (InOrg) Hooks[E any]() []velox.Hook[E] {
	return []velox.Hook[E]{scope.By[E](OrgKey, "org_id").Fill()}
}
