package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel/actor"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// InOrg confines an entity's rows to the context's actor.Org: reads,
// updates and deletes see only its rows, and an insert gets its id.
type InOrg struct{ mixin.Schema }

// Fields returns org_id. It is Immutable, so that no update moves a row to
// another organization.
func (InOrg) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Immutable()}
}

// Policy confines statements to the context's organization, and refuses an
// insert into another one.
func (InOrg) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Predicate, error) {
			org, err := actor.Org.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ("org_id", org), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error {
			return actor.Org.Match(ctx, r.Value("org_id"))
		},
	}
}

// Hooks fills org_id on insert, in each row that leaves it zero.
func (InOrg) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if org, ok := actor.Org.From(ctx); ok {
			return m.Fill("org_id", org)
		}
		return nil
	}, velox.OpInsert)}
}
