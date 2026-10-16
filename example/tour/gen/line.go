package gen

import (
	"context"

	"github.com/syssam/velox2"
)

// Policy has no Read, so anyone in the organization may read lines; only
// accountants change them.
func (*Line) Policy() velox.Policy[Line] {
	return velox.Policy[Line]{
		Write: func(ctx context.Context) (velox.Pred[Line], error) {
			return velox.Pred[Line]{}, requireAccountant(ctx)
		},
		Insert: func(ctx context.Context, _ *Line) error { return requireAccountant(ctx) },
	}
}
