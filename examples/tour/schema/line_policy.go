package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tour/gen/entity"
	"github.com/syssam/velox/examples/tour/role"
)

// Policy has no Read of its own, so anyone in the organization may read lines;
// only accountants change them. The scope that Scoped generates is bound around
// it, so the organization is not its business.
func (Line) Policy() velox.Policy[entity.Line] {
	return velox.Policy[entity.Line]{
		Write: func(ctx context.Context) (velox.Pred[entity.Line], error) {
			if err := role.RequireAccountant(ctx); err != nil {
				return velox.Pred[entity.Line]{}, err
			}
			return velox.Allow[entity.Line](), nil
		},
		Insert: func(ctx context.Context, _ *entity.Line) error { return role.RequireAccountant(ctx) },
	}
}
