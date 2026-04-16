package privacy_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/syssam/velox/privacy"
)

// ExampleDenyIfNoViewer demonstrates a rule that denies access when no viewer
// (authenticated user) is present in the context. This is typically used as
// the first rule in a policy to require authentication.
func ExampleDenyIfNoViewer() {
	rule := privacy.DenyIfNoViewer()

	// Without a viewer in context — access is denied.
	err := rule.EvalQuery(context.Background(), nil)
	fmt.Println("denied:", errors.Is(err, privacy.Deny))

	// With a viewer in context — the rule skips to allow subsequent rules to decide.
	ctx := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{UserID: "user-1"})
	err = rule.EvalQuery(ctx, nil)
	fmt.Println("skipped:", errors.Is(err, privacy.Skip))
	// Output:
	// denied: true
	// skipped: true
}
