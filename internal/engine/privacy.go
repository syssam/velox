package engine

import (
	"context"
	"errors"
)

// This file is veloxe's privacy model, which Policy.Query and
// Policy.Mutation accept so that a schema written for veloxe compiles and
// behaves as it did there. Package privacy exports it; its doc says how a
// rule list becomes velox's conditions.

// Decision is the verdict of a privacy rule: PrivacyAllow, PrivacyDeny or
// PrivacySkip, which a rule returns as an error, wrapped or not.
type Decision struct{ msg string } //nolint:errname // veloxe's name, which schemas written for it use

// Error returns the decision's text.
func (d *Decision) Error() string { return d.msg }

// The three decisions, with veloxe's texts.
var (
	PrivacyAllow = &Decision{"velox/privacy: allow rule"} //nolint:errname // veloxe's decisions
	PrivacyDeny  = &Decision{"velox/privacy: deny rule"}  //nolint:errname // veloxe's decisions
	PrivacySkip  = &Decision{"velox/privacy: skip rule"}  //nolint:errname // veloxe's decisions
)

// PrivacyFilter is what a privacy rule narrows a statement with: each
// Where's conditions are ANDed into it.
type PrivacyFilter interface {
	Where(ps ...Predicate)
}

// PrivacyQuery is the read a QueryRule judges.
type PrivacyQuery interface {
	// Filter returns the read's filter.
	Filter() PrivacyFilter
}

// PrivacyMutation is the write a MutationRule judges.
type PrivacyMutation interface {
	// Op returns the write's kind: OpInsert, OpUpdate or OpDelete.
	Op() Op
	// Filter returns the write's filter. An update's or a delete's narrows
	// the rows it changes; an insert's is ignored, as veloxe ignored it.
	Filter() PrivacyFilter
	// Value returns an inserted row's value of the column of the given
	// name, as Row.Value does. It reports false for a column the entity
	// lacks, and on an update or a delete, whose values a rule does not see.
	Value(name string) (any, bool)
}

// QueryRule decides whether a read may run, and may narrow it through its
// filter. It returns a Decision, nil (which is PrivacySkip), or another
// error, which denies the read.
type QueryRule interface {
	EvalQuery(ctx context.Context, q PrivacyQuery) error
}

// QueryPolicy is a list of query rules, run in order.
type QueryPolicy []QueryRule

// MutationRule decides whether a write may run, and may narrow an update or
// a delete through its filter, as QueryRule does a read.
type MutationRule interface {
	EvalMutation(ctx context.Context, m PrivacyMutation) error
}

// MutationPolicy is a list of mutation rules, run in order.
type MutationPolicy []MutationRule

// EvalQuery runs the rules in order: nil and PrivacySkip go on to the next
// rule, PrivacyAllow ends the list with nil, and any other error ends it
// with that error. A list that runs out returns nil. The conditions the
// rules gave the filter before the end stay.
func (p QueryPolicy) EvalQuery(ctx context.Context, q PrivacyQuery) error {
	for _, r := range p {
		if done, err := decided(r.EvalQuery(ctx, q)); done {
			return err
		}
	}
	return nil
}

// EvalMutation runs the rules in order, as QueryPolicy.EvalQuery does.
func (p MutationPolicy) EvalMutation(ctx context.Context, m PrivacyMutation) error {
	for _, r := range p {
		if done, err := decided(r.EvalMutation(ctx, m)); done {
			return err
		}
	}
	return nil
}

// decided reads a rule's result as veloxe did: nil and Skip go on, Allow
// ends with nil, anything else ends with itself.
func decided(err error) (bool, error) {
	switch {
	case err == nil || errors.Is(err, PrivacySkip):
		return false, nil
	case errors.Is(err, PrivacyAllow):
		return true, nil
	}
	return true, err
}

// privacyDecisionKey is the context key of PrivacyDecisionContext.
type privacyDecisionKey struct{}

// PrivacyDecisionContext returns parent carrying decision, which every
// Query and Mutation list then returns without running its rules. A nil
// decision, or one that is PrivacySkip, returns parent.
func PrivacyDecisionContext(parent context.Context, decision error) context.Context {
	if decision == nil || errors.Is(decision, PrivacySkip) {
		return parent
	}
	return context.WithValue(parent, privacyDecisionKey{}, decision)
}

// PrivacyDecisionFromContext returns the decision PrivacyDecisionContext put
// in ctx, nil for PrivacyAllow, and whether there was one.
func PrivacyDecisionFromContext(ctx context.Context) (error, bool) {
	d, ok := ctx.Value(privacyDecisionKey{}).(error)
	if ok && errors.Is(d, PrivacyAllow) {
		d = nil
	}
	return d, ok
}

// privacyFilter collects the conditions rules give a statement of entity
// in. HasColumn lets a rule written for several entities check that this
// one has a column, as veloxe's generated filters could.
type privacyFilter struct {
	in    *EntityInfo
	where []Predicate
}

func (f *privacyFilter) Where(ps ...Predicate) { f.where = append(f.where, ps...) }

// HasColumn reports whether the entity has a column of the given name.
func (f *privacyFilter) HasColumn(name string) bool {
	_, ok := f.in.Column(name)
	return ok
}

// cond is the AND of the filter's conditions; Allow for none.
func (f *privacyFilter) cond() Predicate {
	if len(f.where) == 0 {
		return NewPredicate(Node{Op: OpAllow})
	}
	return AndPredicates(f.where...)
}

// privacyQuery is the PrivacyQuery of one read.
type privacyQuery struct{ f *privacyFilter }

func (q privacyQuery) Filter() PrivacyFilter { return q.f }

// privacyMutation is the PrivacyMutation of one write: an inserted row's
// (row set) or an update's or a delete's.
type privacyMutation struct {
	op  Op
	f   PrivacyFilter
	row *Row
}

func (m privacyMutation) Op() Op                { return m.op }
func (m privacyMutation) Filter() PrivacyFilter { return m.f }

func (m privacyMutation) Value(name string) (any, bool) {
	if m.row == nil {
		return nil, false
	}
	if _, ok := m.row.in.Column(name); !ok {
		return nil, false
	}
	return m.row.Value(name), true
}

// ignoredFilter is an insert's filter, which drops what it is given: an
// insert has no rows to narrow, and veloxe ignored a rule's predicates on
// one, so a rule written for both reads and writes, such as a tenant's,
// still passes an insert.
type ignoredFilter struct{ in *EntityInfo }

func (ignoredFilter) Where(...Predicate) {}

// HasColumn reports whether the entity has a column of the given name.
func (f ignoredFilter) HasColumn(name string) bool {
	_, ok := f.in.Column(name)
	return ok
}

// queryCond is the condition QueryPolicy p gives a read of entity in: the
// AND of what its rules gave the filter, or the error that denies the read.
// A decision in the context replaces the rules.
func queryCond(in *EntityInfo, p QueryPolicy) func(ctx context.Context) (Predicate, error) {
	return func(ctx context.Context) (Predicate, error) {
		if d, ok := PrivacyDecisionFromContext(ctx); ok {
			return decisionCond(d)
		}
		f := &privacyFilter{in: in}
		if err := p.EvalQuery(ctx, privacyQuery{f}); err != nil {
			return Predicate{}, err
		}
		return f.cond(), nil
	}
}

// mutationCond is the condition MutationPolicy p gives an update or a delete
// (op) of entity in, as queryCond.
func mutationCond(in *EntityInfo, p MutationPolicy) func(ctx context.Context, op Op) (Predicate, error) {
	return func(ctx context.Context, op Op) (Predicate, error) {
		if d, ok := PrivacyDecisionFromContext(ctx); ok {
			return decisionCond(d)
		}
		f := &privacyFilter{in: in}
		if err := p.EvalMutation(ctx, privacyMutation{op: op, f: f}); err != nil {
			return Predicate{}, err
		}
		return f.cond(), nil
	}
}

// mutationInsert is MutationPolicy p as an Insert policy of entity in: it
// judges each inserted row.
func mutationInsert(in *EntityInfo, p MutationPolicy) func(ctx context.Context, r Row) error {
	return func(ctx context.Context, r Row) error {
		if d, ok := PrivacyDecisionFromContext(ctx); ok {
			return d
		}
		return p.EvalMutation(ctx, privacyMutation{op: OpInsert, f: ignoredFilter{in}, row: &r})
	}
}

// decisionCond is the condition of a decision found in the context: no
// restriction for Allow (nil), a denial for any other.
func decisionCond(d error) (Predicate, error) {
	if d != nil {
		return Predicate{}, d
	}
	return NewPredicate(Node{Op: OpAllow}), nil
}
