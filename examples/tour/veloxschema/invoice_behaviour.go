package veloxschema

import (
	"context"
	"fmt"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tour/role"
)

// Policy says who may see and change invoices, within the context's
// organization: velox ANDs it after InOrg's policy, which confines them to it.
func (Invoice) Policy() velox.Policy {
	return velox.Policy{
		// Accountants see every invoice of their organization; viewers see
		// only the ones that were sent.
		Read: func(ctx context.Context) (velox.Predicate, error) {
			switch role.FromContext(ctx) {
			case role.Accountant:
				return velox.Allow(), nil
			case role.Viewer:
				return velox.FieldNEQ("status", "draft"), nil
			}
			return velox.Predicate{}, velox.Deny("no billing role")
		},
		Write: func(ctx context.Context) (velox.Predicate, error) {
			if err := role.RequireAccountant(ctx); err != nil {
				return velox.Predicate{}, err
			}
			return velox.Allow(), nil
		},
		Insert: func(ctx context.Context, _ velox.Row) error { return role.RequireAccountant(ctx) },
	}
}

// Hooks says what a valid new invoice is: a number that starts with INV-.
// velox runs it after InOrg's fill and the soft delete's hook.
func (Invoice) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(validNumber, velox.OpInsert)}
}

// validNumber refuses an inserted invoice whose number does not start with
// INV-.
func validNumber(_ context.Context, m *velox.Mutation) error {
	for r := range m.Rows() {
		if n, _ := r.Value("number").(string); !strings.HasPrefix(n, "INV-") {
			return fmt.Errorf("invoice number %q does not start with INV-", n)
		}
	}
	return nil
}
