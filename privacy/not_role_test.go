package privacy_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/privacy"
)

// TestNotHasRole_DeniesButDoesNotAllow pins the guidance in docs/privacy.md
// § Combining Rules: Not(HasRole("guest")) denies guests but returns Skip —
// not Allow — for everyone else, so "anyone signed in except guests" needs
// an explicit terminal Allow after it.
func TestNotHasRole_DeniesButDoesNotAllow(t *testing.T) {
	t.Parallel()
	guest := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{UserID: "g", UserRoles: []string{"guest"}})
	member := privacy.WithViewer(context.Background(), &privacy.SimpleViewer{UserID: "m", UserRoles: []string{"member"}})
	anonymous := context.Background()

	notGuest := privacy.Not(privacy.HasRole("guest"))
	assert.ErrorIs(t, notGuest.EvalQuery(guest, nil), privacy.Deny)
	assert.ErrorIs(t, notGuest.EvalQuery(member, nil), privacy.Skip, "a non-guest is skipped, not allowed")

	policy := privacy.QueryPolicy{
		privacy.DenyIfNoViewer(),
		privacy.Not(privacy.HasRole("guest")),
		privacy.AlwaysAllowRule(),
	}
	assert.ErrorIs(t, policy.EvalQuery(anonymous, nil), privacy.Deny)
	assert.ErrorIs(t, policy.EvalQuery(guest, nil), privacy.Deny)
	assert.NoError(t, policy.EvalQuery(member, nil))
}
