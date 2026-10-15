// Package billing is an entity package as a team that owns invoices would
// write it: plain structs, a Schema method on each, and the *_velox.go files
// that go tool velox-gen writes beside them.
package billing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/syssam/velox2"
)

// Timestamps is a mixin: embedded in an entity, its fields become columns and
// its Mixin method runs before the entity's Schema.
type Timestamps struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Mixin sets CreatedAt on insert, and UpdatedAt on every insert and update.
func (t *Timestamps) Mixin(s *velox.Schema) {
	s.Column(&t.CreatedAt).DefaultFunc(time.Now)
	s.Column(&t.UpdatedAt).OnUpdate(time.Now)
}

// Status is where an invoice is in its life.
type Status string

// The statuses of an invoice.
const (
	Draft Status = "draft"
	Sent  Status = "sent"
	Paid  Status = "paid"
)

// Invoice is a bill sent to a customer. Every invoice belongs to one
// organization, the tenant, and its number is unique within it.
type Invoice struct {
	Timestamps
	ID         int64
	OrgID      int64
	Number     string
	Customer   string
	Status     Status
	TotalCents int64
	Lines      velox.Many[Line] `json:",omitzero"` // a relation, never a column; Line.InvoiceID is its key
}

// Schema declares the table, the tenant column, the unique number, who may
// see and change invoices, and what a valid new invoice is.
func (x *Invoice) Schema(s *velox.Schema) {
	s.Table("invoices")
	s.Tenant(&x.OrgID)
	s.Unique(&x.OrgID, &x.Number)
	s.Policy(velox.Policy[Invoice]{
		// Accountants see every invoice of their organization; viewers see
		// only the ones that were sent.
		Read: func(ctx context.Context) (velox.Pred[Invoice], error) {
			switch RoleOf(ctx) {
			case Accountant:
				return velox.Pred[Invoice]{}, nil
			case Viewer:
				return Invoices.Status.NEQ(Draft), nil
			}
			return velox.Pred[Invoice]{}, velox.Deny("no billing role")
		},
		Write: func(ctx context.Context) (velox.Pred[Invoice], error) {
			return velox.Pred[Invoice]{}, requireAccountant(ctx)
		},
		Insert: func(ctx context.Context, _ *Invoice) error { return requireAccountant(ctx) },
	})
	s.BeforeInsert(func(_ context.Context, x *Invoice) error {
		if !strings.HasPrefix(x.Number, "INV-") {
			return fmt.Errorf("invoice number %q does not start with INV-", x.Number)
		}
		return nil
	})
}

// Role is what a user may do with billing data.
type Role string

// The billing roles.
const (
	Viewer     Role = "viewer"     // reads sent and paid invoices
	Accountant Role = "accountant" // reads and writes everything
)

type roleKey struct{}

// WithRole returns ctx acting with role. In a service, authentication
// middleware sets it, next to velox.WithTenant.
func WithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey{}, role)
}

// RoleOf returns the role ctx acts with; "" when none.
func RoleOf(ctx context.Context) Role {
	r, _ := ctx.Value(roleKey{}).(Role)
	return r
}

// requireAccountant denies a write by anyone but an accountant.
func requireAccountant(ctx context.Context) error {
	if RoleOf(ctx) != Accountant {
		return velox.Deny("only accountants change billing data")
	}
	return nil
}
