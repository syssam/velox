// Package privacy provides sets of types and helpers for writing privacy
// rules in user schemas, and deal with their evaluation at runtime.
package privacy

import (
	"context"
	"errors"
	"fmt"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
)

// Decision represents a privacy policy evaluation result.
// It implements the error interface for compatibility with existing
// error handling patterns (errors.Is, fmt.Errorf wrapping).
type Decision struct {
	msg string
}

// Error implements the error interface.
func (d *Decision) Error() string { return d.msg }

var (
	// Allow may be returned by rules to indicate that the policy
	// evaluation should terminate with an allow decision.
	Allow = &Decision{"velox/privacy: allow rule"}

	// Deny may be returned by rules to indicate that the policy
	// evaluation should terminate with a deny decision.
	Deny = &Decision{"velox/privacy: deny rule"}

	// Skip may be returned by rules to indicate that the policy
	// evaluation should continue to the next rule in the chain.
	Skip = &Decision{"velox/privacy: skip rule"}
)

// IsDecision reports whether err is or wraps a privacy Decision.
func IsDecision(err error) bool {
	var d *Decision
	return errors.As(err, &d)
}

// Allowf returns a formatted wrapped Allow decision.
func Allowf(format string, a ...any) error {
	return fmt.Errorf(format+": %w", append(a, Allow)...)
}

// Denyf returns a formatted wrapped Deny decision.
func Denyf(format string, a ...any) error {
	return fmt.Errorf(format+": %w", append(a, Deny)...)
}

// Skipf returns a formatted wrapped Skip decision.
func Skipf(format string, a ...any) error {
	return fmt.Errorf(format+": %w", append(a, Skip)...)
}

// AlwaysAllowRule returns a rule that always returns an Allow decision.
// This rule unconditionally permits both queries and mutations.
func AlwaysAllowRule() QueryMutationRule {
	return fixedDecision{Allow}
}

// AlwaysDenyRule returns a rule that always returns a Deny decision.
// This rule unconditionally rejects both queries and mutations.
func AlwaysDenyRule() QueryMutationRule {
	return fixedDecision{Deny}
}

// ContextQueryMutationRule creates a query/mutation rule from a context evaluation function.
// The provided function receives the context and should return Allow, Deny, Skip, or nil.
// Returning nil is equivalent to returning Skip.
func ContextQueryMutationRule(eval func(context.Context) error) QueryMutationRule {
	return contextDecision{eval}
}

type (
	// QueryRule defines the interface deciding whether a
	// query is allowed and optionally modify it.
	QueryRule interface {
		EvalQuery(context.Context, velox.Query) error
	}

	// QueryPolicy combines multiple query rules into a single policy.
	QueryPolicy []QueryRule

	// MutationRule defines the interface deciding whether a
	// mutation is allowed and optionally modify it.
	MutationRule interface {
		EvalMutation(context.Context, velox.Mutation) error
	}

	// MutationPolicy combines multiple mutation rules into a single policy.
	MutationPolicy []MutationRule

	// QueryMutationRule is an interface which groups query and mutation rules.
	QueryMutationRule interface {
		QueryRule
		MutationRule
	}
)

// MutationRuleFunc type is an adapter which allows the use of
// ordinary functions as mutation rules.
type MutationRuleFunc func(context.Context, velox.Mutation) error

// EvalMutation returns f(ctx, m).
func (f MutationRuleFunc) EvalMutation(ctx context.Context, m velox.Mutation) error {
	err := f(ctx, m)
	RecordTrace(ctx, "MutationRuleFunc", decisionString(err))
	return err
}

// OnMutationOperation evaluates the given rule only on a given mutation operation.
func OnMutationOperation(rule MutationRule, op velox.Op) MutationRule {
	return MutationRuleFunc(func(ctx context.Context, m velox.Mutation) error {
		if m.Op().Is(op) {
			return rule.EvalMutation(ctx, m)
		}
		return Skip
	})
}

// DenyMutationOperationRule returns a rule denying specified mutation operation.
func DenyMutationOperationRule(op velox.Op) MutationRule {
	rule := MutationRuleFunc(func(_ context.Context, m velox.Mutation) error {
		return Denyf("velox/privacy: operation %s is not allowed", m.Op())
	})
	return OnMutationOperation(rule, op)
}

// Policy groups query and mutation policies.
type Policy struct {
	Query    QueryPolicy
	Mutation MutationPolicy
}

// EvalQuery forwards evaluation to query a policy.
func (p Policy) EvalQuery(ctx context.Context, q velox.Query) error {
	return p.Query.EvalQuery(ctx, q)
}

// EvalMutation forwards evaluation to mutate a policy.
func (p Policy) EvalMutation(ctx context.Context, m velox.Mutation) error {
	return p.Mutation.EvalMutation(ctx, m)
}

// PolicyProvider is an interface for types that can provide a privacy policy.
// This interface is implemented by schema types that define a Policy() method.
type PolicyProvider interface {
	Policy() velox.Policy
}

// NewPolicies creates a velox.Policy from list of mixin.Schema
// and velox.Schema that implement the velox.Policy interface.
//
// Note that, this is a runtime function used by velox-generated
// code and should not be used in velox schemas as a privacy rule.
func NewPolicies(schemas ...PolicyProvider) velox.Policy {
	policies := make(Policies, 0, len(schemas))
	for i := range schemas {
		if policy := schemas[i].Policy(); policy != nil {
			policies = append(policies, policy)
		}
	}
	return policies
}

// Policies combines multiple policies into a single policy.
//
// Note that, this is a runtime type used by velox-generated
// code and should not be used in velox schemas as a privacy rule.
type Policies []velox.Policy

// EvalQuery evaluates the query policies. If the Allow error is returned
// from one of the policies, it stops the evaluation with a nil error.
func (policies Policies) EvalQuery(ctx context.Context, q velox.Query) error {
	return policies.eval(ctx, func(policy velox.Policy) error {
		return policy.EvalQuery(ctx, q)
	})
}

// EvalMutation evaluates the mutation policies. If the Allow error is returned
// from one of the policies, it stops the evaluation with a nil error.
func (policies Policies) EvalMutation(ctx context.Context, m velox.Mutation) error {
	return policies.eval(ctx, func(policy velox.Policy) error {
		return policy.EvalMutation(ctx, m)
	})
}

func (policies Policies) eval(ctx context.Context, eval func(velox.Policy) error) error {
	if decision, ok := DecisionFromContext(ctx); ok {
		return decision
	}
	for _, policy := range policies {
		switch decision := eval(policy); {
		case decision == nil || errors.Is(decision, Skip):
		case errors.Is(decision, Allow):
			return nil
		default:
			return decision
		}
	}
	return nil
}

// EvalQuery evaluates a query against a query policy.
func (policies QueryPolicy) EvalQuery(ctx context.Context, q velox.Query) error {
	for _, policy := range policies {
		switch decision := policy.EvalQuery(ctx, q); {
		case decision == nil || errors.Is(decision, Skip):
		case errors.Is(decision, Allow):
			return nil
		default:
			return decision
		}
	}
	return nil
}

// EvalMutation evaluates a mutation against a mutation policy.
func (policies MutationPolicy) EvalMutation(ctx context.Context, m velox.Mutation) error {
	for _, policy := range policies {
		switch decision := policy.EvalMutation(ctx, m); {
		case decision == nil || errors.Is(decision, Skip):
		case errors.Is(decision, Allow):
			return nil
		default:
			return decision
		}
	}
	return nil
}

type decisionCtxKey struct{}

// DecisionContext creates a new context from the given parent context with
// a policy decision attach to it.
func DecisionContext(parent context.Context, decision error) context.Context {
	if decision == nil || errors.Is(decision, Skip) {
		return parent
	}
	return context.WithValue(parent, decisionCtxKey{}, decision)
}

// DecisionFromContext retrieves the policy decision from the context.
func DecisionFromContext(ctx context.Context) (error, bool) {
	decision, ok := ctx.Value(decisionCtxKey{}).(error)
	if ok && errors.Is(decision, Allow) {
		decision = nil
	}
	return decision, ok
}

type fixedDecision struct {
	decision *Decision
}

func (f fixedDecision) EvalQuery(context.Context, velox.Query) error {
	return f.decision
}

func (f fixedDecision) EvalMutation(context.Context, velox.Mutation) error {
	return f.decision
}

type contextDecision struct {
	eval func(context.Context) error
}

func (c contextDecision) EvalQuery(ctx context.Context, _ velox.Query) error {
	err := c.eval(ctx)
	RecordTrace(ctx, "ContextQueryMutationRule", decisionString(err))
	return err
}

func (c contextDecision) EvalMutation(ctx context.Context, _ velox.Mutation) error {
	err := c.eval(ctx)
	RecordTrace(ctx, "ContextQueryMutationRule", decisionString(err))
	return err
}

// Filter is the interface that wraps the WhereP method for filtering
// nodes in queries and mutations based on predicates.
//
// This interface is implemented by generated *Filter types (e.g., UserFilter)
// and allows writing generic privacy rules that work across multiple entities.
type Filter interface {
	// WhereP appends storage-level predicates to the filter.
	WhereP(...func(*sql.Selector))
}

// Filterable is implemented by queries and mutations that support filtering.
// The generated Query and Mutation types implement this interface when
// the privacy feature is enabled.
type Filterable interface {
	Filter() Filter
}

// FilterFunc is an adapter that allows using ordinary functions as
// query/mutation rules that apply predicates to filter results.
//
// Example usage:
//
//	privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
//	    f.WhereP(func(s *sql.Selector) {
//	        s.Where(sql.EQ(s.C("workspace_id"), workspaceID))
//	    })
//	    return privacy.Skip
//	})
type FilterFunc func(context.Context, Filter) error

// EvalQuery calls f(ctx, q.Filter()) if the query implements Filterable.
func (f FilterFunc) EvalQuery(ctx context.Context, q velox.Query) error {
	fr, ok := q.(Filterable)
	if !ok {
		err := Denyf("velox/privacy: query type %T does not support filtering", q)
		RecordTrace(ctx, "FilterFunc", decisionString(err))
		return err
	}
	err := f(ctx, fr.Filter())
	RecordTrace(ctx, "FilterFunc", decisionString(err))
	return err
}

// EvalMutation calls f(ctx, m.Filter()) if the mutation implements Filterable.
func (f FilterFunc) EvalMutation(ctx context.Context, m velox.Mutation) error {
	fr, ok := m.(Filterable)
	if !ok {
		err := Denyf("velox/privacy: mutation type %T does not support filtering", m)
		RecordTrace(ctx, "FilterFunc", decisionString(err))
		return err
	}
	err := f(ctx, fr.Filter())
	RecordTrace(ctx, "FilterFunc", decisionString(err))
	return err
}

var _ QueryMutationRule = FilterFunc(nil)
