package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tour/gen/entity"
	"github.com/syssam/velox/examples/tour/role"
)

// Policy says who may see and change invoices, within the context's organization
// (the scope that Scoped generates is bound around it). velox-gen binds it into
// Invoice's metadata, and velox calls it once, on a nil *entity.Invoice.
func (Invoice) Policy() velox.Policy[entity.Invoice] {
	return velox.Policy[entity.Invoice]{
		// Accountants see every invoice of their organization; viewers see
		// only the ones that were sent.
		Read: func(ctx context.Context) (velox.Pred[entity.Invoice], error) {
			switch role.FromContext(ctx) {
			case role.Accountant:
				return velox.Allow[entity.Invoice](), nil
			case role.Viewer:
				return entity.Invoices.Status.NEQ(entity.InvoiceStatusDraft), nil
			}
			return velox.Pred[entity.Invoice]{}, velox.Deny("no billing role")
		},
		Write: func(ctx context.Context) (velox.Pred[entity.Invoice], error) {
			if err := role.RequireAccountant(ctx); err != nil {
				return velox.Pred[entity.Invoice]{}, err
			}
			return velox.Allow[entity.Invoice](), nil
		},
		Insert: func(ctx context.Context, _ *entity.Invoice) error { return role.RequireAccountant(ctx) },
	}
}

// Hooks says what a valid new invoice is: a number that starts with INV-.
// gen binds it after the scope's fill of the organization.
func (Invoice) Hooks() []velox.Hook[entity.Invoice] {
	return []velox.Hook[entity.Invoice]{velox.On(validNumber, velox.OpInsert)}
}

// validNumber refuses an inserted invoice whose number does not start with
// INV-.
func validNumber(next velox.Mutator[entity.Invoice]) velox.Mutator[entity.Invoice] {
	return velox.MutateFunc[entity.Invoice](func(ctx context.Context, m *velox.Mutation[entity.Invoice]) error {
		for _, x := range m.Rows() {
			if !strings.HasPrefix(x.Number, "INV-") {
				return fmt.Errorf("invoice number %q does not start with INV-", x.Number)
			}
		}
		return next.Mutate(ctx, m)
	})
}
