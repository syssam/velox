package gen

import (
	"context"
	"fmt"
	"strings"

	"github.com/syssam/velox2"
)

// Policy says who may see and change invoices. velox-gen binds it into
// Invoice's metadata, and velox calls it once, on a nil *Invoice.
func (*Invoice) Policy() velox.Policy[Invoice] {
	return velox.Policy[Invoice]{
		// Accountants see every invoice of their organization; viewers see
		// only the ones that were sent.
		Read: func(ctx context.Context) (velox.Pred[Invoice], error) {
			switch RoleOf(ctx) {
			case Accountant:
				return velox.Pred[Invoice]{}, nil
			case Viewer:
				return Invoices.Status.NEQ(InvoiceStatusDraft), nil
			}
			return velox.Pred[Invoice]{}, velox.Deny("no billing role")
		},
		Write: func(ctx context.Context) (velox.Pred[Invoice], error) {
			return velox.Pred[Invoice]{}, requireAccountant(ctx)
		},
		Insert: func(ctx context.Context, _ *Invoice) error { return requireAccountant(ctx) },
	}
}

// Hooks says what a valid new invoice is: a number that starts with INV-.
// velox-gen binds it into Invoice's metadata too.
func (*Invoice) Hooks() []velox.Hook[Invoice] {
	return []velox.Hook[Invoice]{velox.On(validNumber, velox.OpInsert)}
}

// validNumber refuses an inserted invoice whose number does not start with
// INV-.
func validNumber(next velox.Mutator[Invoice]) velox.Mutator[Invoice] {
	return velox.MutateFunc[Invoice](func(ctx context.Context, m *velox.Mutation[Invoice]) error {
		for _, x := range m.Rows() {
			if !strings.HasPrefix(x.Number, "INV-") {
				return fmt.Errorf("invoice number %q does not start with INV-", x.Number)
			}
		}
		return next.Mutate(ctx, m)
	})
}
