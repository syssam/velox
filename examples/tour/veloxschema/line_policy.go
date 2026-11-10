package veloxschema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tour/role"
)

// Policy has no Read of its own, so anyone in the organization may read lines;
// only accountants change them. velox ANDs it after InOrg's policy.
func (Line) Policy() velox.Policy {
	return velox.Policy{
		Write: func(ctx context.Context) (velox.Predicate, error) {
			if err := role.RequireAccountant(ctx); err != nil {
				return velox.Predicate{}, err
			}
			return velox.Allow(), nil
		},
		Insert: func(ctx context.Context, _ velox.Row) error { return role.RequireAccountant(ctx) },
	}
}
