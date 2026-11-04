package schema

import (
	"context"
	"errors"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// Policy lets a user see and change only their own tickets, and create only
// tickets they own; "admin" sees, changes and creates every ticket. InOrg's
// policy, ANDed before it, confines tickets to the context's Org.
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
	return velox.Policy[testmodel.Ticket]{
		Read:  own,
		Write: own,
		Insert: func(ctx context.Context, x *testmodel.Ticket) error {
			if u := testmodel.UserFromContext(ctx); u != "admin" && u != x.Owner {
				return velox.Deny("a ticket must be created by its owner")
			}
			return nil
		},
	}
}

// Hooks fills CreatedBy on insert, refuses an empty title on insert and
// update, and deletes only finished tickets. InOrg's hook, before them,
// fills OrgID on insert.
func (Ticket) Hooks() []velox.Hook[testmodel.Ticket] {
	return []velox.Hook[testmodel.Ticket]{
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
