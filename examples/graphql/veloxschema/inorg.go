package veloxschema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
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
// velox.DB.WithoutPolicy lifts the policy, with a reason, as it lifts every
// policy.
type InOrg struct{ mixin.Schema }

// Fields returns org_id. It is Immutable, so that no update moves a row to
// another organization, and graphql.Skip leaves it out of every GraphQL type
// and input: the hook fills it.
func (InOrg) Fields() []velox.Field {
	return []velox.Field{field.Int64("org_id").Immutable().Annotations(graphql.Skip())}
}

// Policy confines reads, updates and deletes to the context's organization,
// and refuses an insert into another one.
func (InOrg) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
			org, err := OrgKey.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ("org_id", org), nil
		},
		Insert: func(ctx context.Context, _ *velox.DB, r velox.Row) error {
			return OrgKey.Match(ctx, r.Value("org_id"))
		},
	}
}

// Hooks fills org_id on insert, in each row that leaves it zero. Without an
// organization it does nothing, and the policy refuses the insert.
func (InOrg) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(func(ctx context.Context, m *velox.Mutation) error {
			if org, ok := OrgKey.From(ctx); ok {
				return m.Fill("org_id", org)
			}
			return nil
		}, velox.OpInsert),
	}
}
