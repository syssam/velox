package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tour/gen"
	"github.com/syssam/velox/examples/tour/role"
)

// Policy has no Read of its own, so anyone in the organization may read lines;
// only accountants change them. velox ANDs it after InOrg's policy, so the
// organization is not its business.
func (Line) Policy() velox.Policy[gen.Line] {
	return velox.Policy[gen.Line]{
		Write: func(ctx context.Context) (velox.Pred[gen.Line], error) {
			if err := role.RequireAccountant(ctx); err != nil {
				return velox.Pred[gen.Line]{}, err
			}
			return velox.Allow[gen.Line](), nil
		},
		Insert: func(ctx context.Context, _ *gen.Line) error { return role.RequireAccountant(ctx) },
	}
}
