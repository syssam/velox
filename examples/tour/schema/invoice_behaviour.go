package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tour/gen"
	"github.com/syssam/velox/examples/tour/role"
)

// Policy says who may see and change invoices, within the context's
// organization: velox ANDs it after InOrg's policy, which confines them to
// it. gen/runtime binds both into Invoice's metadata, and velox calls each
// once.
func (Invoice) Policy() velox.Policy[gen.Invoice] {
	return velox.Policy[gen.Invoice]{
		// Accountants see every invoice of their organization; viewers see
		// only the ones that were sent.
		Read: func(ctx context.Context) (velox.Pred[gen.Invoice], error) {
			switch role.FromContext(ctx) {
			case role.Accountant:
				return velox.Allow[gen.Invoice](), nil
			case role.Viewer:
				return gen.Invoices.Status.NEQ(gen.InvoiceStatusDraft), nil
			}
			return velox.Pred[gen.Invoice]{}, velox.Deny("no billing role")
		},
		Write: func(ctx context.Context) (velox.Pred[gen.Invoice], error) {
			if err := role.RequireAccountant(ctx); err != nil {
				return velox.Pred[gen.Invoice]{}, err
			}
			return velox.Allow[gen.Invoice](), nil
		},
		Insert: func(ctx context.Context, _ *gen.Invoice) error { return role.RequireAccountant(ctx) },
	}
}

// Hooks says what a valid new invoice is: a number that starts with INV-.
// gen/runtime binds it after InOrg's fill of the organization and the soft
// delete's hook.
func (Invoice) Hooks() []velox.Hook[gen.Invoice] {
	return []velox.Hook[gen.Invoice]{velox.On(validNumber, velox.OpInsert)}
}

// validNumber refuses an inserted invoice whose number does not start with
// INV-.
func validNumber(next velox.Mutator[gen.Invoice]) velox.Mutator[gen.Invoice] {
	return velox.MutateFunc[gen.Invoice](func(ctx context.Context, m *velox.Mutation[gen.Invoice]) error {
		for _, x := range m.Rows() {
			if !strings.HasPrefix(x.Number, "INV-") {
				return fmt.Errorf("invoice number %q does not start with INV-", x.Number)
			}
		}
		return next.Mutate(ctx, m)
	})
}
