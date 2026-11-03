package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/policy/auth"
	"github.com/syssam/velox/examples/policy/gen/entity"
)

// Policy declares Doc's policy. velox-gen binds it into Doc's metadata, and
// velox calls it once, on a nil *entity.Doc. Read and Write return a condition that
// velox ANDs into every statement; an error denies the statement. Allow means
// no restriction, and a zero Pred is an error.
func (Doc) Policy() velox.Policy[entity.Doc] {
	return velox.Policy[entity.Doc]{
		Read: func(ctx context.Context) (velox.Pred[entity.Doc], error) {
			switch u := auth.UserFromContext(ctx); u {
			case "":
				return velox.Pred[entity.Doc]{}, velox.Deny("no user")
			case "admin":
				return velox.Allow[entity.Doc](), nil
			default:
				return velox.Or(entity.Docs.Owner.EQ(u), entity.Docs.Shared.EQ(true)), nil
			}
		},
		// Write filters which rows may change; Read applies too.
		Write: func(ctx context.Context) (velox.Pred[entity.Doc], error) {
			switch u := auth.UserFromContext(ctx); u {
			case "":
				return velox.Pred[entity.Doc]{}, velox.Deny("no user")
			case "admin":
				return velox.Allow[entity.Doc](), nil
			default:
				return entity.Docs.Owner.EQ(u), nil
			}
		},
		// Insert checks each new row.
		Insert: func(ctx context.Context, x *entity.Doc) error {
			if u := auth.UserFromContext(ctx); u != "admin" && u != x.Owner {
				return velox.Deny("a doc must be created by its owner")
			}
			return nil
		},
	}
}
