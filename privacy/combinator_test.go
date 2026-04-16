package privacy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"

	"github.com/stretchr/testify/assert"
)

func TestAnd(t *testing.T) {
	ctx := context.Background()
	q := &mockQuery{}

	t.Run("all_allow", func(t *testing.T) {
		rule := privacy.And(
			privacy.AlwaysAllowRule(),
			privacy.AlwaysAllowRule(),
			privacy.AlwaysAllowRule(),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("first_deny_short_circuits", func(t *testing.T) {
		called := false
		rule := privacy.And(
			privacy.AlwaysDenyRule(),
			privacy.ContextQueryMutationRule(func(_ context.Context) error {
				called = true
				return privacy.Allow
			}),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Deny))
		assert.False(t, called, "second rule should not be called")
	})

	t.Run("skip_treated_as_non_allow", func(t *testing.T) {
		rule := privacy.And(
			privacy.AlwaysAllowRule(),
			privacy.ContextQueryMutationRule(func(_ context.Context) error {
				return privacy.Skip
			}),
			privacy.AlwaysAllowRule(),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Skip))
	})

	t.Run("empty_rules_returns_allow", func(t *testing.T) {
		rule := privacy.And()
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("single_rule_passthrough", func(t *testing.T) {
		rule := privacy.And(privacy.AlwaysDenyRule())
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("works_for_mutations", func(t *testing.T) {
		m := &mockMutation{op: velox.OpCreate}
		rule := privacy.And(
			privacy.AlwaysAllowRule(),
			privacy.AlwaysAllowRule(),
		)
		err := rule.EvalMutation(ctx, m)
		assert.True(t, errors.Is(err, privacy.Allow))

		rule = privacy.And(
			privacy.AlwaysAllowRule(),
			privacy.AlwaysDenyRule(),
		)
		err = rule.EvalMutation(ctx, m)
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("empty_rules_returns_allow_for_mutation", func(t *testing.T) {
		m := &mockMutation{op: velox.OpCreate}
		rule := privacy.And()
		err := rule.EvalMutation(ctx, m)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("deny_in_middle_short_circuits", func(t *testing.T) {
		called := false
		rule := privacy.And(
			privacy.AlwaysAllowRule(),
			privacy.AlwaysDenyRule(),
			privacy.ContextQueryMutationRule(func(_ context.Context) error {
				called = true
				return privacy.Allow
			}),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Deny))
		assert.False(t, called)
	})
}

func TestOr(t *testing.T) {
	ctx := context.Background()
	q := &mockQuery{}

	t.Run("first_allow_short_circuits", func(t *testing.T) {
		called := false
		rule := privacy.Or(
			privacy.AlwaysAllowRule(),
			privacy.ContextQueryMutationRule(func(_ context.Context) error {
				called = true
				return privacy.Deny
			}),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Allow))
		assert.False(t, called, "second rule should not be called")
	})

	t.Run("all_deny_returns_last_deny", func(t *testing.T) {
		rule := privacy.Or(
			privacy.AlwaysDenyRule(),
			privacy.AlwaysDenyRule(),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("skip_continues_to_next", func(t *testing.T) {
		rule := privacy.Or(
			privacy.ContextQueryMutationRule(func(_ context.Context) error {
				return privacy.Skip
			}),
			privacy.AlwaysAllowRule(),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("empty_rules_returns_deny", func(t *testing.T) {
		rule := privacy.Or()
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("works_for_mutations", func(t *testing.T) {
		m := &mockMutation{op: velox.OpUpdate}
		rule := privacy.Or(
			privacy.AlwaysDenyRule(),
			privacy.AlwaysAllowRule(),
		)
		err := rule.EvalMutation(ctx, m)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("empty_rules_returns_deny_for_mutation", func(t *testing.T) {
		m := &mockMutation{op: velox.OpUpdate}
		rule := privacy.Or()
		err := rule.EvalMutation(ctx, m)
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("last_error_returned_when_none_allow", func(t *testing.T) {
		lastErr := privacy.Denyf("final deny")
		rule := privacy.Or(
			privacy.ContextQueryMutationRule(func(_ context.Context) error {
				return privacy.Skip
			}),
			privacy.ContextQueryMutationRule(func(_ context.Context) error {
				return lastErr
			}),
		)
		err := rule.EvalQuery(ctx, q)
		assert.Equal(t, lastErr, err)
	})

	t.Run("single_allow_rule", func(t *testing.T) {
		rule := privacy.Or(privacy.AlwaysAllowRule())
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("single_deny_rule", func(t *testing.T) {
		rule := privacy.Or(privacy.AlwaysDenyRule())
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Deny))
	})
}

func TestNot(t *testing.T) {
	ctx := context.Background()
	q := &mockQuery{}

	t.Run("inverts_allow_to_deny", func(t *testing.T) {
		rule := privacy.Not(privacy.AlwaysAllowRule())
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("inverts_deny_to_allow", func(t *testing.T) {
		rule := privacy.Not(privacy.AlwaysDenyRule())
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("skip_passes_through", func(t *testing.T) {
		rule := privacy.Not(privacy.ContextQueryMutationRule(func(_ context.Context) error {
			return privacy.Skip
		}))
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Skip))
	})

	t.Run("works_for_mutations", func(t *testing.T) {
		m := &mockMutation{op: velox.OpDelete}

		rule := privacy.Not(privacy.AlwaysAllowRule())
		err := rule.EvalMutation(ctx, m)
		assert.True(t, errors.Is(err, privacy.Deny))

		rule = privacy.Not(privacy.AlwaysDenyRule())
		err = rule.EvalMutation(ctx, m)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("wrapped_errors_inverted", func(t *testing.T) {
		rule := privacy.Not(privacy.ContextQueryMutationRule(func(_ context.Context) error {
			return privacy.Allowf("user is admin")
		}))
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Deny))
	})

	t.Run("wrapped_deny_inverted", func(t *testing.T) {
		rule := privacy.Not(privacy.ContextQueryMutationRule(func(_ context.Context) error {
			return privacy.Denyf("user is banned")
		}))
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Allow))
	})
}

func TestCombinatorComposition(t *testing.T) {
	ctx := context.Background()
	q := &mockQuery{}

	t.Run("and_inside_or", func(t *testing.T) {
		// Or(And(allow, deny), allow) -> allow (second Or branch)
		rule := privacy.Or(
			privacy.And(privacy.AlwaysAllowRule(), privacy.AlwaysDenyRule()),
			privacy.AlwaysAllowRule(),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("or_inside_and", func(t *testing.T) {
		// And(Or(deny, allow), allow) -> allow
		rule := privacy.And(
			privacy.Or(privacy.AlwaysDenyRule(), privacy.AlwaysAllowRule()),
			privacy.AlwaysAllowRule(),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("not_inside_and", func(t *testing.T) {
		// And(Not(deny), allow) -> And(allow, allow) -> allow
		rule := privacy.And(
			privacy.Not(privacy.AlwaysDenyRule()),
			privacy.AlwaysAllowRule(),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("not_inside_or", func(t *testing.T) {
		// Or(Not(allow), deny) -> Or(deny, deny) -> deny
		rule := privacy.Or(
			privacy.Not(privacy.AlwaysAllowRule()),
			privacy.AlwaysDenyRule(),
		)
		err := rule.EvalQuery(ctx, q)
		assert.True(t, errors.Is(err, privacy.Deny))
	})
}

func BenchmarkCombinators(b *testing.B) {
	ctx := context.Background()
	q := &mockQuery{}
	m := &mockMutation{op: velox.OpCreate}

	b.Run("And_3Rules", func(b *testing.B) {
		rule := privacy.And(
			privacy.AlwaysAllowRule(),
			privacy.AlwaysAllowRule(),
			privacy.AlwaysAllowRule(),
		)
		for b.Loop() {
			_ = rule.EvalQuery(ctx, q)
		}
	})

	b.Run("Or_3Rules", func(b *testing.B) {
		rule := privacy.Or(
			privacy.AlwaysDenyRule(),
			privacy.AlwaysDenyRule(),
			privacy.AlwaysAllowRule(),
		)
		for b.Loop() {
			_ = rule.EvalQuery(ctx, q)
		}
	})

	b.Run("Not", func(b *testing.B) {
		rule := privacy.Not(privacy.AlwaysAllowRule())
		for b.Loop() {
			_ = rule.EvalQuery(ctx, q)
		}
	})

	b.Run("And_Mutation", func(b *testing.B) {
		rule := privacy.And(
			privacy.AlwaysAllowRule(),
			privacy.AlwaysAllowRule(),
		)
		for b.Loop() {
			_ = rule.EvalMutation(ctx, m)
		}
	})

	b.Run("Nested_And_Or_Not", func(b *testing.B) {
		rule := privacy.And(
			privacy.Or(
				privacy.Not(privacy.AlwaysDenyRule()),
				privacy.AlwaysAllowRule(),
			),
			privacy.AlwaysAllowRule(),
		)
		for b.Loop() {
			_ = rule.EvalQuery(ctx, q)
		}
	})
}
