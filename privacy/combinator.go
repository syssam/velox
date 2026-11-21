package privacy

import (
	"context"
	"errors"

	"github.com/syssam/velox/internal/engine"
)

// And returns a rule that allows only when every one of rules allows, as
// veloxe's And does. It runs them in order and returns the first result
// that is not Allow, as it is: Deny, Skip, nil or another error. With every
// rule allowing, and with no rules, it returns Allow.
//
//	rules            And
//	Allow, Allow     Allow
//	Allow, Skip      Skip (the list goes on to its next rule)
//	Skip, Deny       Skip (Deny does not run)
//	Allow, Deny      Deny
//	(none)           Allow
//
// A condition a rule gives the filter before And returns stays, as in a
// list.
func And(rules ...QueryMutationRule) QueryMutationRule { return andRule(rules) }

// Or returns a rule that allows when any one of rules allows, as veloxe's
// Or does. It runs them in order and returns Allow at the first that
// allows; when none does, the last one's result, as it is. With no rules it
// returns Deny.
//
//	rules            Or
//	Deny, Allow      Allow
//	Allow, Deny      Allow (Deny does not run)
//	Deny, Skip       Skip
//	Skip, Deny       Deny
//	(none)           Deny
//
// A condition a rule gives the filter stays, whether or not that rule
// allowed: a rule meant only for Or's other branch should not narrow.
func Or(rules ...QueryMutationRule) QueryMutationRule { return orRule(rules) }

// Not returns a rule that inverts rule's decision, failing closed, as
// veloxe's Not does:
//
//	rule                       Not
//	Allow or Allowf            Deny
//	Deny (the bare sentinel)   Allow
//	Denyf, or a wrapped Deny   the same (a denial)
//	Skip, Skipf or nil         the same (skip)
//	another error              the same (a denial)
//
// A wrapped Deny stays a denial: a rule reports a refusal to decide, such
// as no viewer in the context, that way, and inverting it would allow. A
// rule meant to be inverted returns the bare Deny for its "no".
func Not(rule QueryMutationRule) QueryMutationRule { return notRule{rule} }

type andRule []QueryMutationRule

func (rs andRule) EvalQuery(ctx context.Context, q Query) error {
	for i, r := range rs {
		if err := engine.EvalQueryRule(ctx, r, q, i); !errors.Is(err, Allow) {
			return err
		}
	}
	return Allow
}

func (rs andRule) EvalMutation(ctx context.Context, m Mutation) error {
	for i, r := range rs {
		if err := engine.EvalMutationRule(ctx, r, m, i); !errors.Is(err, Allow) {
			return err
		}
	}
	return Allow
}

type orRule []QueryMutationRule

func (rs orRule) EvalQuery(ctx context.Context, q Query) error {
	var last error = Deny
	for i, r := range rs {
		last = engine.EvalQueryRule(ctx, r, q, i)
		if errors.Is(last, Allow) {
			return Allow
		}
	}
	return last
}

func (rs orRule) EvalMutation(ctx context.Context, m Mutation) error {
	var last error = Deny
	for i, r := range rs {
		last = engine.EvalMutationRule(ctx, r, m, i)
		if errors.Is(last, Allow) {
			return Allow
		}
	}
	return last
}

type notRule struct{ rule QueryMutationRule }

func (n notRule) EvalQuery(ctx context.Context, q Query) error {
	return invert(engine.EvalQueryRule(ctx, n.rule, q, 0))
}

func (n notRule) EvalMutation(ctx context.Context, m Mutation) error {
	return invert(engine.EvalMutationRule(ctx, n.rule, m, 0))
}

// invert is Not's table. Only the bare Deny becomes Allow.
func invert(err error) error {
	switch {
	case errors.Is(err, Allow):
		return Deny
	case err == Deny: //nolint:errorlint // identity on purpose: a wrapped Deny stays a denial
		return Allow
	}
	return err
}

// TraceEntry is one rule's decision, which WithTrace's function is given:
// the rule's Go type and its place in the list or combinator that ran it,
// the statement's entity and kind (velox.OpSelect for a read), the
// decision as the list reads it ("allow", "deny" or "skip") and what the
// rule returned.
type TraceEntry = engine.TraceEntry

// WithTrace returns ctx carrying fn, which each privacy rule run under it
// reports its decision to, for debugging a policy: every rule of a Query or
// Mutation list, and every rule an And, Or or Not runs, after that rule
// returns. A list or combinator nested in a list reports its own rules,
// then itself. A rule that a list's earlier decision skipped reports
// nothing, and neither does any rule under DecisionContext, which replaces
// them. fn runs on the statement's goroutine, inside the policy, and may
// run concurrently for statements that share ctx. It changes no decision.
// A nil fn returns ctx. In a process that never calls WithTrace, tracing
// costs each rule one atomic load; after a call, a context lookup.
func WithTrace(ctx context.Context, fn func(TraceEntry)) context.Context {
	return engine.PrivacyTraceContext(ctx, fn)
}
