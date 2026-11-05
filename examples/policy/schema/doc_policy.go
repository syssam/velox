package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/policy/auth"
)

// Policy declares Doc's policy. Read and Write return a condition that velox
// ANDs into every statement; an error denies the statement. Allow means no
// restriction, and a zero Cond is an error.
func (Doc) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Cond, error) {
			switch u := auth.UserFromContext(ctx); u {
			case "":
				return velox.Cond{}, velox.Deny("no user")
			case "admin":
				return velox.Allow(), nil
			default:
				return velox.Or(velox.C("owner").EQ(u), velox.C("shared").EQ(true)), nil
			}
		},
		// Write filters which rows may change; Read applies too.
		Write: func(ctx context.Context) (velox.Cond, error) {
			switch u := auth.UserFromContext(ctx); u {
			case "":
				return velox.Cond{}, velox.Deny("no user")
			case "admin":
				return velox.Allow(), nil
			default:
				return velox.C("owner").EQ(u), nil
			}
		},
		// Insert checks each new row.
		Insert: func(ctx context.Context, r velox.Row) error {
			if u := auth.UserFromContext(ctx); u != "admin" && r.Value("owner") != u {
				return velox.Deny("a doc must be created by its owner")
			}
			return nil
		},
	}
}
