package schema

import (
	"context"
	"errors"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel/actor"
)

// Policy lets a user see and change only their own tickets, and create only
// tickets they own; "admin" sees, changes and creates every ticket. InOrg's
// policy, ANDed before it, confines tickets to the context's Org.
func (Ticket) Policy() velox.Policy {
	own := func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
		switch u := actor.UserFromContext(ctx); u {
		case "":
			return velox.Predicate{}, velox.Deny("no user")
		case "admin":
			return velox.Allow(), nil
		default:
			return velox.FieldEQ("owner", u), nil
		}
	}
	return velox.Policy{
		Read:  own,
		Write: own,
		Insert: func(ctx context.Context, _ *velox.DB, r velox.Row) error {
			if u := actor.UserFromContext(ctx); u != "admin" && r.Value("owner") != u {
				return velox.Deny("a ticket must be created by its owner")
			}
			return nil
		},
	}
}

// Hooks fills created_by on insert, refuses an empty title on insert and
// update, and deletes only finished tickets. InOrg's hook, before them,
// fills org_id on insert.
func (Ticket) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.Before(stamp, velox.OpInsert),
		velox.Before(needsTitle, velox.OpInsert, velox.OpUpdate),
		velox.Before(onlyDone, velox.OpDelete),
	}
}

// stamp fills each row's created_by from the context's user when it is
// empty.
func stamp(ctx context.Context, m *velox.Mutation) error {
	return m.Fill("created_by", actor.UserFromContext(ctx))
}

// needsTitle refuses a row inserted, or an update that sets, an empty title.
func needsTitle(_ context.Context, m *velox.Mutation) error {
	if m.Op() == velox.OpInsert {
		for r := range m.Rows() {
			if r.Value("title") == "" {
				return errors.New("a ticket needs a title")
			}
		}
	} else if v, ok := m.Value("title"); ok && v == "" {
		return errors.New("a ticket needs a title")
	}
	return nil
}

// onlyDone narrows a delete to finished tickets.
func onlyDone(_ context.Context, m *velox.Mutation) error {
	m.Where(velox.FieldEQ("done", true))
	return nil
}
