package gen

import (
	"context"

	"github.com/syssam/velox2"
)

// Policy declares Doc's policy. velox-gen binds it into Doc's metadata, and
// velox calls it once, on a nil *Doc. Read and Write return a condition that
// velox ANDs into every statement; an error denies the statement. A zero Pred
// restricts nothing.
func (*Doc) Policy() velox.Policy[Doc] {
	return velox.Policy[Doc]{
		Read: func(ctx context.Context) (velox.Pred[Doc], error) {
			switch u := userOf(ctx); u {
			case "":
				return velox.Pred[Doc]{}, velox.Deny("no user")
			case "admin":
				return velox.Pred[Doc]{}, nil
			default:
				return velox.Or(Docs.Owner.EQ(u), Docs.Shared.EQ(true)), nil
			}
		},
		// Write filters which rows may change; Read applies too.
		Write: func(ctx context.Context) (velox.Pred[Doc], error) {
			switch u := userOf(ctx); u {
			case "":
				return velox.Pred[Doc]{}, velox.Deny("no user")
			case "admin":
				return velox.Pred[Doc]{}, nil
			default:
				return Docs.Owner.EQ(u), nil
			}
		},
		// Insert checks each new row.
		Insert: func(ctx context.Context, x *Doc) error {
			if u := userOf(ctx); u != "admin" && u != x.Owner {
				return velox.Deny("a doc must be created by its owner")
			}
			return nil
		},
	}
}
