// Package privacy is veloxe's privacy package, for a schema written for
// veloxe: its Policy method compiles and behaves as it did there.
//
//	func (Doc) Policy() velox.Policy {
//		return privacy.Policy{Query: privacy.QueryPolicy{
//			rule.RequireScope("doc:read"),
//			privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
//				f.Where(velox.FieldEQ("archived", false))
//				return privacy.Skip
//			}),
//		}}
//	}
//
// Policy is velox.Policy, whose Query and Mutation fields hold veloxe's rule
// lists; a Policy may set them beside Read, Write and Insert. velox binds it
// as any Policy: a mixin's first, then the entity's own.
//
// A list runs its rules in order. A rule returns Skip (or nil) to go on to
// the next, Allow to end the list with no further restriction, Deny or any
// other error to deny the statement, which then fails with a
// *velox.PolicyError that wraps the error. A list that runs out allows the
// statement, as ent's privacy does: velox.Policy's Read, Write and Insert
// fail closed, and these lists do not. To deny by default, end a list with
// AlwaysDenyRule(). Conditions a rule gives the filter (Filter.Where) before the
// list ends stay, Allow or not: they are ANDed into the statement. Each
// Policy's list ends on its own, so an Allow in an entity's list does not
// lift a mixin's, as veloxe's generated runtime did not.
//
// Query's rules decide reads: their conditions are ANDed into every SELECT,
// Count, With load, join and Has/HasWith subquery of a statement that only
// reads. They do not apply to an UPDATE, a DELETE, a Save or an upsert, as
// veloxe's query policy did not; Read is velox's condition for both.
// Mutation's rules judge every inserted row (Mutation.Op is velox.OpInsert,
// Mutation.Value reads the row, and Filter.Where is ignored, as in veloxe),
// and narrow every UPDATE, DELETE and Save (velox.OpUpdate, velox.OpDelete).
//
// A rule that reads to decide, as veloxe's did through a client from the
// context, reads with ctx through Query.DB or Mutation.DB: the DB the
// statement runs on, the transaction's inside Tx. A statement made under
// a rule's ctx is not judged by its Policy's list.
//
// DecisionContext puts a decision in a context: every Query and Mutation
// list then returns it without running its rules, Allow lifting them and
// Deny refusing. It does not lift Read, Write, Insert or an interceptor,
// such as a tenant mixin's; velox.DB.WithoutPolicy lifts every policy.
//
// What veloxe had and velox does not: a rule narrows with Filter.Where and
// velox.Predicate conditions, not WhereP and *sql.Selector functions; a
// rule sees no generated query or mutation type, only Query and Mutation;
// and NewPolicies and Policies, which veloxe's generated code called, have
// no use, since velox composes a mixin's and an entity's policies itself.
package privacy

import (
	"context"
	"errors"
	"fmt"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
)

// Decision is a rule's verdict: Allow, Deny or Skip. It is an error, so a
// rule returns it, wrapped or not (Allowf, Denyf, Skipf).
type Decision = engine.Decision //nolint:errname // veloxe's name

var (
	// Allow ends a rule list, allowing the statement with the conditions
	// the rules before gave it.
	Allow = engine.PrivacyAllow //nolint:errname // veloxe's name
	// Deny ends a rule list, denying the statement.
	Deny = engine.PrivacyDeny //nolint:errname // veloxe's name
	// Skip goes on to the next rule.
	Skip = engine.PrivacySkip //nolint:errname // veloxe's name
)

// IsDecision reports whether err is or wraps a Decision.
func IsDecision(err error) bool {
	_, ok := errors.AsType[*Decision](err)
	return ok
}

// Allowf returns Allow wrapped with a formatted message.
func Allowf(format string, a ...any) error {
	return fmt.Errorf(format+": %w", append(a, Allow)...)
}

// Denyf returns Deny wrapped with a formatted message.
func Denyf(format string, a ...any) error {
	return fmt.Errorf(format+": %w", append(a, Deny)...)
}

// Skipf returns Skip wrapped with a formatted message.
func Skipf(format string, a ...any) error {
	return fmt.Errorf(format+": %w", append(a, Skip)...)
}

// Policy is velox.Policy: Query and Mutation hold veloxe's rule lists.
type Policy = velox.Policy

type (
	// Query is the read a QueryRule judges: Filter narrows it, and DB is
	// the DB it runs on.
	Query = velox.PrivacyQuery
	// Mutation is the write a MutationRule judges: its Op, its Filter, an
	// inserted row's values, and DB, the DB it runs on.
	Mutation = velox.PrivacyMutation
	// Filter narrows a statement: Where's conditions are ANDed into it. The
	// value a rule is given also has HasColumn(name string) bool, which
	// reports whether the entity has a column of that name, for a rule
	// shared by several entities.
	Filter = velox.PrivacyFilter
	// QueryRule decides a read.
	QueryRule = velox.QueryRule
	// QueryPolicy is a list of query rules; it is a QueryRule itself.
	QueryPolicy = velox.QueryPolicy
	// MutationRule decides a write.
	MutationRule = velox.MutationRule
	// MutationPolicy is a list of mutation rules; it is a MutationRule
	// itself.
	MutationPolicy = velox.MutationPolicy
)

// QueryMutationRule is a rule for both reads and writes.
type QueryMutationRule interface {
	QueryRule
	MutationRule
}

// Filterable is what has a Filter: every Query and Mutation.
type Filterable interface {
	Filter() Filter
}

// QueryRuleFunc is a function as a QueryRule.
type QueryRuleFunc func(context.Context, Query) error

// EvalQuery returns f(ctx, q).
func (f QueryRuleFunc) EvalQuery(ctx context.Context, q Query) error { return f(ctx, q) }

// MutationRuleFunc is a function as a MutationRule.
type MutationRuleFunc func(context.Context, Mutation) error

// EvalMutation returns f(ctx, m).
func (f MutationRuleFunc) EvalMutation(ctx context.Context, m Mutation) error { return f(ctx, m) }

// FilterFunc is a function as a rule for reads and writes, given the
// statement's Filter.
type FilterFunc func(context.Context, Filter) error

// EvalQuery returns f(ctx, q.Filter()).
func (f FilterFunc) EvalQuery(ctx context.Context, q Query) error { return f(ctx, q.Filter()) }

// EvalMutation returns f(ctx, m.Filter()).
func (f FilterFunc) EvalMutation(ctx context.Context, m Mutation) error {
	return f(ctx, m.Filter())
}

// OnMutationOperation returns rule applied only to writes of kind op
// (velox.OpInsert, velox.OpUpdate or velox.OpDelete); it skips the others.
func OnMutationOperation(rule MutationRule, op velox.Op) MutationRule {
	return MutationRuleFunc(func(ctx context.Context, m Mutation) error {
		if m.Op() == op {
			return rule.EvalMutation(ctx, m)
		}
		return Skip
	})
}

// DenyMutationOperationRule returns a rule that denies writes of kind op.
func DenyMutationOperationRule(op velox.Op) MutationRule {
	return OnMutationOperation(MutationRuleFunc(func(_ context.Context, m Mutation) error {
		return Denyf("velox/privacy: operation %s is not allowed", m.Op())
	}), op)
}

// AlwaysAllowRule returns a rule that allows every read and write.
func AlwaysAllowRule() QueryMutationRule { return fixed{Allow} }

// AlwaysDenyRule returns a rule that denies every read and write.
func AlwaysDenyRule() QueryMutationRule { return fixed{Deny} }

// ContextQueryMutationRule returns a rule for reads and writes that decides
// from the context alone: eval returns Allow, Deny, Skip or nil (Skip).
func ContextQueryMutationRule(eval func(context.Context) error) QueryMutationRule {
	return contextRule(eval)
}

// fixed is a rule that always returns its decision.
type fixed struct{ d *Decision }

func (r fixed) EvalQuery(context.Context, Query) error       { return r.d }
func (r fixed) EvalMutation(context.Context, Mutation) error { return r.d }

// contextRule is ContextQueryMutationRule's rule.
type contextRule func(context.Context) error

func (r contextRule) EvalQuery(ctx context.Context, _ Query) error       { return r(ctx) }
func (r contextRule) EvalMutation(ctx context.Context, _ Mutation) error { return r(ctx) }

// DecisionContext returns parent carrying decision, which every Query and
// Mutation rule list then returns without running its rules (see the
// package doc). A nil decision, or Skip, returns parent.
func DecisionContext(parent context.Context, decision error) context.Context {
	return engine.PrivacyDecisionContext(parent, decision)
}

// DecisionFromContext returns the decision DecisionContext put in ctx (nil
// for Allow) and whether there was one.
func DecisionFromContext(ctx context.Context) (error, bool) {
	return engine.PrivacyDecisionFromContext(ctx)
}
