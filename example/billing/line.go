package billing

import (
	"context"

	"github.com/syssam/velox2"
)

// Line is one item of an Invoice. It carries the tenant too, so a line is
// scoped on its own, without a join to its invoice.
type Line struct {
	ID          int64
	OrgID       int64
	InvoiceID   int64
	Description string
	AmountCents int64
}

// Schema declares the table and the tenant column. The policy has no Read, so
// anyone in the organization may read lines; only accountants change them.
func (x *Line) Schema(s *velox.Schema) {
	s.Table("lines")
	s.Tenant(&x.OrgID)
	s.Policy(velox.Policy[Line]{
		Write: func(ctx context.Context) (velox.Pred[Line], error) {
			return velox.Pred[Line]{}, requireAccountant(ctx)
		},
		Insert: func(ctx context.Context, _ *Line) error { return requireAccountant(ctx) },
	})
}
