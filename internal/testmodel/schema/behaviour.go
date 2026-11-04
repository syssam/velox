package schema

import (
	"context"
	"errors"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/scope"
)

var (
	invoiceScope = scope.By(testmodel.Org, testmodel.Invoices.OrgID.EQ, func(x *testmodel.Invoice) *int64 { return &x.OrgID })
	ticketScope  = scope.By(testmodel.Org, testmodel.Tickets.OrgID.EQ, func(x *testmodel.Ticket) *int64 { return &x.OrgID })
)

// Policy confines Invoice to the context's Org.
func (Invoice) Policy() velox.Policy[testmodel.Invoice] { return invoiceScope.Policy() }

// Hooks fills OrgID on insert.
func (Invoice) Hooks() []velox.Hook[testmodel.Invoice] {
	return []velox.Hook[testmodel.Invoice]{invoiceScope.Fill()}
}

// Policy confines tickets to the context's Org, and lets a user see and change
// only their own tickets, and create only tickets they own; "admin" sees,
// changes and creates every ticket.
func (Ticket) Policy() velox.Policy[testmodel.Ticket] {
	own := func(ctx context.Context) (velox.Pred[testmodel.Ticket], error) {
		switch u := testmodel.UserFromContext(ctx); u {
		case "":
			return velox.Pred[testmodel.Ticket]{}, velox.Deny("no user")
		case "admin":
			return velox.Allow[testmodel.Ticket](), nil
		default:
			return testmodel.Tickets.Owner.EQ(u), nil
		}
	}
	return ticketScope.Wrap(velox.Policy[testmodel.Ticket]{
		Read:  own,
		Write: own,
		Insert: func(ctx context.Context, x *testmodel.Ticket) error {
			if u := testmodel.UserFromContext(ctx); u != "admin" && u != x.Owner {
				return velox.Deny("a ticket must be created by its owner")
			}
			return nil
		},
	})
}

// Hooks fills CreatedBy on insert, refuses an empty title on insert and
// update, and deletes only finished tickets.
func (Ticket) Hooks() []velox.Hook[testmodel.Ticket] {
	return []velox.Hook[testmodel.Ticket]{
		ticketScope.Fill(),
		velox.On(stamp, velox.OpInsert),
		velox.On(needsTitle, velox.OpInsert, velox.OpUpdate),
		velox.On(onlyDone, velox.OpDelete),
	}
}

// stamp fills each row's CreatedBy from the context's user when it is empty.
func stamp(next velox.Mutator[testmodel.Ticket]) velox.Mutator[testmodel.Ticket] {
	return velox.MutateFunc[testmodel.Ticket](func(ctx context.Context, m *velox.Mutation[testmodel.Ticket]) error {
		for _, x := range m.Rows() {
			if x.CreatedBy == "" {
				x.CreatedBy = testmodel.UserFromContext(ctx)
			}
		}
		return next.Mutate(ctx, m)
	})
}

// needsTitle refuses a row inserted, or an update that sets, an empty title.
func needsTitle(next velox.Mutator[testmodel.Ticket]) velox.Mutator[testmodel.Ticket] {
	return velox.MutateFunc[testmodel.Ticket](func(ctx context.Context, m *velox.Mutation[testmodel.Ticket]) error {
		if m.Op() == velox.OpInsert {
			for _, x := range m.Rows() {
				if x.Title == "" {
					return errors.New("a ticket needs a title")
				}
			}
		} else if v, ok := testmodel.Tickets.Title.ValueIn(m); ok && v == "" {
			return errors.New("a ticket needs a title")
		}
		return next.Mutate(ctx, m)
	})
}

// onlyDone narrows a delete to finished tickets.
func onlyDone(next velox.Mutator[testmodel.Ticket]) velox.Mutator[testmodel.Ticket] {
	return velox.MutateFunc[testmodel.Ticket](func(ctx context.Context, m *velox.Mutation[testmodel.Ticket]) error {
		m.Where(testmodel.Tickets.Done.EQ(true))
		return next.Mutate(ctx, m)
	})
}
