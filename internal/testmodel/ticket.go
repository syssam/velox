package testmodel

import (
	"context"
	"errors"

	"github.com/syssam/velox2"
)

// userKey is the context key of the user the tests act as.
type userKey struct{}

// WithUser returns ctx acting as user; "admin" sees and changes every ticket.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

func userFromContext(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }

// Policy lets a user see and change only their own tickets, and create only
// tickets they own; "admin" sees, changes and creates every ticket. velox
// calls it once, on a nil *Ticket.
func (*Ticket) Policy() velox.Policy[Ticket] {
	own := func(ctx context.Context) (velox.Pred[Ticket], error) {
		switch u := userFromContext(ctx); u {
		case "":
			return velox.Pred[Ticket]{}, velox.Deny("no user")
		case "admin":
			return velox.Allow[Ticket](), nil
		default:
			return Tickets.Owner.EQ(u), nil
		}
	}
	return velox.Policy[Ticket]{
		Read:  own,
		Write: own,
		Insert: func(ctx context.Context, x *Ticket) error {
			if u := userFromContext(ctx); u != "admin" && u != x.Owner {
				return velox.Deny("a ticket must be created by its owner")
			}
			return nil
		},
	}
}

// Hooks fills CreatedBy on insert, refuses an empty title on insert and
// update, and deletes only finished tickets. velox calls it once, on a nil
// *Ticket.
func (*Ticket) Hooks() []velox.Hook[Ticket] {
	return []velox.Hook[Ticket]{
		velox.On(stamp, velox.OpInsert),
		velox.On(needsTitle, velox.OpInsert, velox.OpUpdate),
		velox.On(onlyDone, velox.OpDelete),
	}
}

// stamp fills each row's CreatedBy from the context's user when it is empty.
func stamp(next velox.Mutator[Ticket]) velox.Mutator[Ticket] {
	return velox.MutateFunc[Ticket](func(ctx context.Context, m *velox.Mutation[Ticket]) error {
		for _, x := range m.Rows() {
			if x.CreatedBy == "" {
				x.CreatedBy = userFromContext(ctx)
			}
		}
		return next.Mutate(ctx, m)
	})
}

// needsTitle refuses a row inserted, or an update that sets, an empty title.
func needsTitle(next velox.Mutator[Ticket]) velox.Mutator[Ticket] {
	return velox.MutateFunc[Ticket](func(ctx context.Context, m *velox.Mutation[Ticket]) error {
		if m.Op() == velox.OpInsert {
			for _, x := range m.Rows() {
				if x.Title == "" {
					return errors.New("a ticket needs a title")
				}
			}
		} else if v, ok := Tickets.Title.ValueIn(m); ok && v == "" {
			return errors.New("a ticket needs a title")
		}
		return next.Mutate(ctx, m)
	})
}

// onlyDone narrows a delete to finished tickets.
func onlyDone(next velox.Mutator[Ticket]) velox.Mutator[Ticket] {
	return velox.MutateFunc[Ticket](func(ctx context.Context, m *velox.Mutation[Ticket]) error {
		m.Where(Tickets.Done.EQ(true))
		return next.Mutate(ctx, m)
	})
}
