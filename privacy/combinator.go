package privacy

import (
	"context"
	"errors"

	"github.com/syssam/velox"
)

// And returns a rule that requires ALL rules to Allow.
// Short-circuits on the first non-Allow (Deny or Skip) result.
// An empty rule list returns Allow.
func And(rules ...QueryMutationRule) QueryMutationRule {
	return andRule(rules)
}

type andRule []QueryMutationRule

func (rules andRule) EvalQuery(ctx context.Context, q velox.Query) error {
	if len(rules) == 0 {
		return Allow
	}
	for _, rule := range rules {
		if err := rule.EvalQuery(ctx, q); !errors.Is(err, Allow) {
			return err
		}
	}
	return Allow
}

func (rules andRule) EvalMutation(ctx context.Context, m velox.Mutation) error {
	if len(rules) == 0 {
		return Allow
	}
	for _, rule := range rules {
		if err := rule.EvalMutation(ctx, m); !errors.Is(err, Allow) {
			return err
		}
	}
	return Allow
}

// Or returns a rule that requires ANY rule to Allow.
// Short-circuits on the first Allow result.
// If no rule allows, returns the last error.
// An empty rule list returns Deny.
func Or(rules ...QueryMutationRule) QueryMutationRule {
	return orRule(rules)
}

type orRule []QueryMutationRule

func (rules orRule) EvalQuery(ctx context.Context, q velox.Query) error {
	if len(rules) == 0 {
		return Deny
	}
	var lastErr error
	for _, rule := range rules {
		err := rule.EvalQuery(ctx, q)
		if errors.Is(err, Allow) {
			return Allow
		}
		lastErr = err
	}
	return lastErr
}

func (rules orRule) EvalMutation(ctx context.Context, m velox.Mutation) error {
	if len(rules) == 0 {
		return Deny
	}
	var lastErr error
	for _, rule := range rules {
		err := rule.EvalMutation(ctx, m)
		if errors.Is(err, Allow) {
			return Allow
		}
		lastErr = err
	}
	return lastErr
}

// Not returns a rule that inverts the decision of rule, failing closed:
//
//   - Allow, bare or wrapped (Allowf), becomes Deny.
//   - The bare Deny sentinel becomes Allow.
//   - A deny that carries a reason (Denyf, or any error wrapping Deny) stays
//     Deny. That is how fail-closed paths report themselves — FilterFunc on
//     a type that does not support filtering, TenantFilterRule or
//     DenyIfNoViewer without a viewer — and inverting them would turn a
//     refusal to evaluate into a grant. A rule meant to be negated must
//     return the bare Deny for its "no" answer.
//   - Skip (bare or wrapped) and any non-decision error pass through
//     unchanged; the policy treats the latter as a denial.
func Not(rule QueryMutationRule) QueryMutationRule {
	return notRule{rule}
}

type notRule struct {
	rule QueryMutationRule
}

func (n notRule) EvalQuery(ctx context.Context, q velox.Query) error {
	return invertDecision(n.rule.EvalQuery(ctx, q))
}

func (n notRule) EvalMutation(ctx context.Context, m velox.Mutation) error {
	return invertDecision(n.rule.EvalMutation(ctx, m))
}

// invertDecision implements Not's semantics. Only the identity comparison
// err == Deny may produce Allow; everything wrapping Deny is kept as is.
func invertDecision(err error) error {
	switch {
	case errors.Is(err, Allow):
		return Deny
	case err == Deny: //nolint:errorlint // identity on purpose: a wrapped Deny must stay a denial.
		return Allow
	default:
		return err
	}
}
