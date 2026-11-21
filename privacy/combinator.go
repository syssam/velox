package privacy

import (
	"context"
	"errors"

	"github.com/syssam/velox/internal/engine"
)

// And returns a rule that allows only when every one of rules allows, as
// veloxe's And does. It runs them in order and returns the first result
// that is not Allow, as it is: Deny, Skip, nil or another error. With every
// rule allowing, and with no rules, it returns Allow. An earlier Skip or
// nil ends And as a Deny does, so a later Deny does not run.
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

// Or returns a rule that allows when any one of rules allows. It runs them
// in order and returns Allow at the first that allows. When none does, it
// returns the first error that is not a decision (Allow, Deny, Skip, nil,
// or one wrapping them), so that a failing rule still denies; with none,
// the last result, as it is. With no rules it returns Deny.
//
//	rules            Or
//	Deny, Allow      Allow
//	Allow, Deny      Allow (Deny does not run)
//	err, Allow       Allow
//	err, Skip        err (a denial)
//	Deny, Skip       Skip
//	Skip, Deny       Deny
//	(none)           Deny
//
// veloxe's Or returned the last result, so err, Skip gave Skip and a list
// that then ran out allowed; velox fails closed (ADR 0102).
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
//
// Not inverts only decisions. The conditions a rule adds with its Filter's
// Where, as a FilterFunc does, are kept as they are, not negated: they
// narrow the rows, so Not over such a rule fails closed.
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
	var o orResult
	for i, r := range rs {
		if o.add(engine.EvalQueryRule(ctx, r, q, i)) {
			return Allow
		}
	}
	return o.result()
}

func (rs orRule) EvalMutation(ctx context.Context, m Mutation) error {
	var o orResult
	for i, r := range rs {
		if o.add(engine.EvalMutationRule(ctx, r, m, i)) {
			return Allow
		}
	}
	return o.result()
}

// orResult is what Or has seen: the last result and the first error that
// is not a decision.
type orResult struct {
	last, failed error
	ran          bool
}

// add records err and reports whether it allows.
func (o *orResult) add(err error) bool {
	if errors.Is(err, Allow) {
		return true
	}
	o.last, o.ran = err, true
	if o.failed == nil && err != nil && !IsDecision(err) {
		o.failed = err
	}
	return false
}

// result is Or's result when no rule allowed.
func (o *orResult) result() error {
	switch {
	case o.failed != nil:
		return o.failed
	case !o.ran:
		return Deny
	}
	return o.last
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
// the statement's entity as velox.Mutation.Entity names it ("Doc"), the
// kind of the rule's list (velox.OpSelect for a Query rule, even one an
// update's Read policy runs), the decision as the list reads it ("allow",
// "deny" or "skip") and what the rule returned.
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
