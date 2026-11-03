package schema

import (
	"context"
	"errors"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel/entity"
	"github.com/syssam/velox/scope"
)

var (
	invoiceScope = scope.By(entity.Org, entity.Invoices.OrgID.EQ, func(x *entity.Invoice) *int64 { return &x.OrgID })
	ticketScope  = scope.By(entity.Org, entity.Tickets.OrgID.EQ, func(x *entity.Ticket) *int64 { return &x.OrgID })
)

// Policy confines Invoice to the context's Org.
func (Invoice) Policy() velox.Policy[entity.Invoice] { return invoiceScope.Policy() }

// Hooks fills OrgID on insert.
func (Invoice) Hooks() []velox.Hook[entity.Invoice] {
	return []velox.Hook[entity.Invoice]{invoiceScope.Fill()}
}

// Policy confines tickets to the context's Org, and lets a user see and change
// only their own tickets, and create only tickets they own; "admin" sees,
// changes and creates every ticket.
func (Ticket) Policy() velox.Policy[entity.Ticket] {
	own := func(ctx context.Context) (velox.Pred[entity.Ticket], error) {
		switch u := entity.UserFromContext(ctx); u {
		case "":
			return velox.Pred[entity.Ticket]{}, velox.Deny("no user")
		case "admin":
			return velox.Allow[entity.Ticket](), nil
		default:
			return entity.Tickets.Owner.EQ(u), nil
		}
	}
	return ticketScope.Wrap(velox.Policy[entity.Ticket]{
		Read:  own,
		Write: own,
		Insert: func(ctx context.Context, x *entity.Ticket) error {
			if u := entity.UserFromContext(ctx); u != "admin" && u != x.Owner {
				return velox.Deny("a ticket must be created by its owner")
			}
			return nil
		},
	})
}

// Hooks fills CreatedBy on insert, refuses an empty title on insert and
// update, and deletes only finished tickets.
func (Ticket) Hooks() []velox.Hook[entity.Ticket] {
	return []velox.Hook[entity.Ticket]{
		ticketScope.Fill(),
		velox.On(stamp, velox.OpInsert),
		velox.On(needsTitle, velox.OpInsert, velox.OpUpdate),
		velox.On(onlyDone, velox.OpDelete),
	}
}

// stamp fills each row's CreatedBy from the context's user when it is empty.
func stamp(next velox.Mutator[entity.Ticket]) velox.Mutator[entity.Ticket] {
	return velox.MutateFunc[entity.Ticket](func(ctx context.Context, m *velox.Mutation[entity.Ticket]) error {
		for _, x := range m.Rows() {
			if x.CreatedBy == "" {
				x.CreatedBy = entity.UserFromContext(ctx)
			}
		}
		return next.Mutate(ctx, m)
	})
}

// needsTitle refuses a row inserted, or an update that sets, an empty title.
func needsTitle(next velox.Mutator[entity.Ticket]) velox.Mutator[entity.Ticket] {
	return velox.MutateFunc[entity.Ticket](func(ctx context.Context, m *velox.Mutation[entity.Ticket]) error {
		if m.Op() == velox.OpInsert {
			for _, x := range m.Rows() {
				if x.Title == "" {
					return errors.New("a ticket needs a title")
				}
			}
		} else if v, ok := entity.Tickets.Title.ValueIn(m); ok && v == "" {
			return errors.New("a ticket needs a title")
		}
		return next.Mutate(ctx, m)
	})
}

// onlyDone narrows a delete to finished tickets.
func onlyDone(next velox.Mutator[entity.Ticket]) velox.Mutator[entity.Ticket] {
	return velox.MutateFunc[entity.Ticket](func(ctx context.Context, m *velox.Mutation[entity.Ticket]) error {
		m.Where(entity.Tickets.Done.EQ(true))
		return next.Mutate(ctx, m)
	})
}
