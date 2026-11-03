package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/policy/auth"
	"github.com/syssam/velox/examples/policy/gen"
)

// Policy declares Doc's policy. velox-gen binds it into Doc's metadata, and
// velox calls it once, on a nil *gen.Doc. Read and Write return a condition that
// velox ANDs into every statement; an error denies the statement. Allow means
// no restriction, and a zero Pred is an error.
func (Doc) Policy() velox.Policy[gen.Doc] {
	return velox.Policy[gen.Doc]{
		Read: func(ctx context.Context) (velox.Pred[gen.Doc], error) {
			switch u := auth.UserFromContext(ctx); u {
			case "":
				return velox.Pred[gen.Doc]{}, velox.Deny("no user")
			case "admin":
				return velox.Allow[gen.Doc](), nil
			default:
				return velox.Or(gen.Docs.Owner.EQ(u), gen.Docs.Shared.EQ(true)), nil
			}
		},
		// Write filters which rows may change; Read applies too.
		Write: func(ctx context.Context) (velox.Pred[gen.Doc], error) {
			switch u := auth.UserFromContext(ctx); u {
			case "":
				return velox.Pred[gen.Doc]{}, velox.Deny("no user")
			case "admin":
				return velox.Allow[gen.Doc](), nil
			default:
				return gen.Docs.Owner.EQ(u), nil
			}
		},
		// Insert checks each new row.
		Insert: func(ctx context.Context, x *gen.Doc) error {
			if u := auth.UserFromContext(ctx); u != "admin" && u != x.Owner {
				return velox.Deny("a doc must be created by its owner")
			}
			return nil
		},
	}
}
