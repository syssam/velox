package gen

import (
	"context"

	"github.com/syssam/velox"
)

// Policy has no Read of its own, so anyone in the organization may read lines;
// only accountants change them. The scope that Scoped generates is bound around
// it, so the organization is not its business.
func (*Line) Policy() velox.Policy[Line] {
	return velox.Policy[Line]{
		Write: func(ctx context.Context) (velox.Pred[Line], error) {
			if err := requireAccountant(ctx); err != nil {
				return velox.Pred[Line]{}, err
			}
			return velox.Allow[Line](), nil
		},
		Insert: func(ctx context.Context, _ *Line) error { return requireAccountant(ctx) },
	}
}
