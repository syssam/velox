package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/runtime"
)

// TestAutoDefault_UnsetOptionalFieldIsNotValidated pins that FeatureAutoDefault
// fills an unset Optional field's zero value without validating it. The
// zero was set in defaults(), before check(), so field.Int("age").Optional()
// .Positive() rejected every create that omitted age — the Optional field
// was effectively required. Ent validates only values the caller set; the
// zero is now filled after check(). A value the caller sets is still
// validated.
func TestAutoDefault_UnsetOptionalFieldIsNotValidated(t *testing.T) {
	ctx := context.Background()
	c := openTestClient(t)

	u, err := c.User.Create().SetName("a").SetEmail("a@x").SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err, "omitting an Optional field must not trip its validator")
	require.Equal(t, 0, u.Age)
	got, err := c.User.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, 0, got.Age)

	_, err = c.User.Create().SetName("b").SetEmail("b@x").SetAge(0).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.True(t, runtime.IsValidationError(err), "an explicit 0 is still validated: got %v", err)

	bulk, err := c.User.CreateBulk(
		c.User.Create().SetName("c").SetEmail("c@x").SetCreatedAt(now).SetUpdatedAt(now),
	).Save(ctx)
	require.NoError(t, err, "bulk create")
	require.Equal(t, 0, bulk[0].Age)
}
