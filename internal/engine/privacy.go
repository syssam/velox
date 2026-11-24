package engine

import (
	"context"
	"errors"
)

// This file is the engine's side of the ent-style privacy model, which the
// root's Policy.Query and Policy.Mutation accept so that a schema written
// as ent's are compiles and decides as ent's does. The rule types name the
// root's DB, so the root declares them and hands the engine a QueryEval and
// a MutationEval. Package privacy exports them; its doc says how a rule
// list becomes velox's conditions.

// Decision is the verdict of a privacy rule: PrivacyAllow, PrivacyDeny or
// PrivacySkip, which a rule returns as an error, wrapped or not.
type Decision struct{ msg string } //nolint:errname // ent's name, which schemas written as ent's use

// Error returns the decision's text.
func (d *Decision) Error() string { return d.msg }

// The three decisions, with ent's texts under velox's name.
var (
	PrivacyAllow = &Decision{"velox/privacy: allow rule"} //nolint:errname // ent's decisions
	PrivacyDeny  = &Decision{"velox/privacy: deny rule"}  //nolint:errname // ent's decisions
	PrivacySkip  = &Decision{"velox/privacy: skip rule"}  //nolint:errname // ent's decisions
)

// PrivacyFilter is what a privacy rule narrows a statement with: each
// Where's conditions are ANDed into it.
type PrivacyFilter interface {
	Where(ps ...Predicate)
}

// QueryEval runs a read's rule list (the root's QueryPolicy) given the DB
// the read runs on and the filter its rules narrow it with.
type QueryEval func(ctx context.Context, db *DB, f PrivacyFilter) error

// MutationEval runs a write's rule list (the root's MutationPolicy) given
// the DB the write runs on, its kind, its filter and, for an insert, the
// row; row is nil for an update or a delete.
type MutationEval func(ctx context.Context, db *DB, op Op, f PrivacyFilter, row *Row) error

// PrivacyDecided reads a rule's result as ent does: nil and PrivacySkip
// go on (false), PrivacyAllow ends with nil, anything else ends with
// itself.
func PrivacyDecided(err error) (bool, error) { return decided(err) }

// RowLookup is r's value of the column of the given name, and false when
// the entity has no such column. It records no misuse, as Value does.
func RowLookup(r *Row, name string) (any, bool) {
	if r == nil {
		return nil, false
	}
	if _, ok := ColumnNamed(r.in, name); !ok {
		return nil, false
	}
	return r.Value(name), true
}

// decided reads a rule's result as ent does: nil and Skip go on, Allow
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
// one has a column, as ent's generated filters can.
type privacyFilter struct {
	in    *EntityInfo
	where []Predicate
}

func (f *privacyFilter) Where(ps ...Predicate) { f.where = append(f.where, ps...) }

// HasColumn reports whether the entity has a column of the given name, as
// velox.FilterEntity's HasColumn does.
func (f *privacyFilter) HasColumn(name string) bool {
	_, ok := ColumnNamed(f.in, name)
	return ok
}

// cond is the AND of the filter's conditions; Allow for none.
func (f *privacyFilter) cond() Predicate {
	if len(f.where) == 0 {
		return NewPredicate(Node{Op: OpAllow})
	}
	return AndPredicates(f.where...)
}

// ignoredFilter is an insert's filter, which drops what it is given: an
// insert has no rows to narrow, and a rule's predicates on one are
// ignored, so a rule written for both reads and writes, such as a tenant's,
// still passes an insert.
type ignoredFilter struct{ in *EntityInfo }

func (ignoredFilter) Where(...Predicate) {}

// HasColumn reports whether the entity has a column of the given name.
func (f ignoredFilter) HasColumn(name string) bool {
	_, ok := ColumnNamed(f.in, name)
	return ok
}

// queryCond is the condition the rule list eval gives a read of entity in:
// the AND of what its rules gave the filter, or the error that denies the
// read. A decision in the context replaces the rules.
func queryCond(in *EntityInfo, eval QueryEval) func(ctx context.Context, db StmtDB) (Predicate, error) {
	return func(ctx context.Context, db StmtDB) (Predicate, error) {
		if d, ok := PrivacyDecisionFromContext(ctx); ok {
			return decisionCond(d)
		}
		f := &privacyFilter{in: in}
		if err := eval(ctx, dbOf(db), f); err != nil {
			return Predicate{}, err
		}
		return f.cond(), nil
	}
}

// mutationCond is the condition the rule list eval gives an update or a
// delete (op) of entity in, as queryCond.
func mutationCond(in *EntityInfo, eval MutationEval) func(ctx context.Context, db StmtDB, op Op) (Predicate, error) {
	return func(ctx context.Context, db StmtDB, op Op) (Predicate, error) {
		if d, ok := PrivacyDecisionFromContext(ctx); ok {
			return decisionCond(d)
		}
		f := &privacyFilter{in: in}
		if err := eval(ctx, dbOf(db), op, f, nil); err != nil {
			return Predicate{}, err
		}
		return f.cond(), nil
	}
}

// mutationInsert is the rule list eval as an Insert policy of entity in:
// it judges each inserted row.
func mutationInsert(in *EntityInfo, eval MutationEval) func(ctx context.Context, db StmtDB, r Row) error {
	return func(ctx context.Context, db StmtDB, r Row) error {
		if d, ok := PrivacyDecisionFromContext(ctx); ok {
			return d
		}
		return eval(ctx, dbOf(db), OpInsert, ignoredFilter{in}, &r)
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
