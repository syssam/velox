package testmodel

import (
	"context"
	"errors"

	"github.com/syssam/velox2"
)

// userKey is the context key of the user the tests act as.
type userKey struct{}

// AsUser returns ctx acting as user; "admin" sees and changes every ticket.
func AsUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

func userOf(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }

// Stamp is a mixin whose BeforeInsert records who created the row.
type Stamp struct {
	CreatedBy string
}

// Mixin fills CreatedBy from the context's user when it is empty.
func (st *Stamp) Mixin(s *velox.Schema) {
	s.BeforeInsert(func(ctx context.Context, x *Stamp) error {
		if x.CreatedBy == "" {
			x.CreatedBy = userOf(ctx)
		}
		return nil
	})
}

// Ticket is tenant-scoped and has a policy and every kind of hook.
type Ticket struct {
	Stamp
	ID        int64
	OrgID     int64
	Owner     string
	Title     string
	Done      bool
	InvoiceID int64
	ParentID  *int64
	Invoice   velox.One[Invoice]
	Parent    velox.One[Ticket]
}

// Schema scopes tickets to OrgID and lets a user see and change only their own.
func (x *Ticket) Schema(s *velox.Schema) {
	s.Table("tickets")
	s.Tenant(&x.OrgID)
	s.Unique(&x.OrgID, &x.Title)
	own := func(ctx context.Context) (velox.Pred[Ticket], error) {
		switch u := userOf(ctx); u {
		case "":
			return velox.Pred[Ticket]{}, velox.Deny("no user")
		case "admin":
			return velox.Pred[Ticket]{}, nil
		default:
			return Tickets.Owner.EQ(u), nil
		}
	}
	s.Policy(velox.Policy[Ticket]{
		Read:  own,
		Write: own,
		Insert: func(ctx context.Context, x *Ticket) error {
			if u := userOf(ctx); u != "admin" && u != x.Owner {
				return velox.Deny("a ticket must be created by its owner")
			}
			return nil
		},
	})
	s.BeforeInsert(func(_ context.Context, x *Ticket) error {
		if x.Title == "" {
			return errors.New("a ticket needs a title")
		}
		return nil
	})
	s.BeforeUpdate(func(_ context.Context, m *velox.Mutation[Ticket]) error {
		if v, ok := m.Value(Tickets.Title); ok && v == "" {
			return errors.New("a ticket needs a title")
		}
		return nil
	})
	s.BeforeDelete(func(_ context.Context, m *velox.Mutation[Ticket]) error {
		m.Where(Tickets.Done.EQ(true)) // only finished tickets are deleted
		return nil
	})
}
