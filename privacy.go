package velox

import (
	"context"

	"github.com/syssam/velox/internal/engine"
)

// This file is the ent-style privacy rules, which Policy.Query and
// Policy.Mutation hold. Package privacy exports them with ent's names
// (privacy.Query, privacy.QueryRule, …) and says how a rule list becomes
// velox's conditions.

// PrivacyFilter is what a privacy rule narrows a statement with: each
// Where's conditions are ANDed into it. The value a rule is given also has
// HasColumn(name string) bool, which reports whether the entity has a
// column of that name.
type PrivacyFilter = engine.PrivacyFilter

// PrivacyQuery is the read a QueryRule judges.
type PrivacyQuery interface {
	// Filter returns the read's filter.
	Filter() PrivacyFilter
	// DB returns the DB the read runs on: the transaction's inside Tx. A
	// rule that reads to decide reads through it; its reads are not judged
	// by the rule list it is in.
	DB() *DB
}

// PrivacyMutation is the write a MutationRule judges.
type PrivacyMutation interface {
	// Op returns the write's kind: OpInsert, OpUpdate or OpDelete.
	Op() Op
	// Filter returns the write's filter. An update's or a delete's narrows
	// the rows it changes; an insert's is ignored, as it has no rows.
	Filter() PrivacyFilter
	// Value returns an inserted row's value of the column of the given
	// name, as Row.Value does. It reports false for a column the entity
	// lacks, and on an update or a delete, whose values a rule does not see.
	Value(name string) (any, bool)
	// DB returns the DB the write runs on, as PrivacyQuery's DB does.
	DB() *DB
}

// QueryRule decides whether a read may run, and may narrow it through its
// filter. It returns a privacy decision, nil (which is Skip), or another
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

// EvalQuery runs the rules in order: nil and Skip go on to the next rule,
// Allow ends the list with nil, and any other error ends it with that
// error. A list that runs out returns nil. The conditions the rules gave
// the filter before the end stay.
func (p QueryPolicy) EvalQuery(ctx context.Context, q PrivacyQuery) error {
	for _, r := range p {
		if done, err := engine.PrivacyDecided(r.EvalQuery(ctx, q)); done {
			return err
		}
	}
	return nil
}

// EvalMutation runs the rules in order, as QueryPolicy.EvalQuery does.
func (p MutationPolicy) EvalMutation(ctx context.Context, m PrivacyMutation) error {
	for _, r := range p {
		if done, err := engine.PrivacyDecided(r.EvalMutation(ctx, m)); done {
			return err
		}
	}
	return nil
}

// privacyQuery is the PrivacyQuery of one read.
type privacyQuery struct {
	f  PrivacyFilter
	db *DB
}

func (q privacyQuery) Filter() PrivacyFilter { return q.f }
func (q privacyQuery) DB() *DB               { return q.db }

// privacyMutation is the PrivacyMutation of one write: an inserted row's
// (row set) or an update's or a delete's.
type privacyMutation struct {
	op  Op
	f   PrivacyFilter
	row *Row
	db  *DB
}

func (m privacyMutation) Op() Op                        { return m.op }
func (m privacyMutation) Filter() PrivacyFilter         { return m.f }
func (m privacyMutation) DB() *DB                       { return m.db }
func (m privacyMutation) Value(name string) (any, bool) { return engine.RowLookup(m.row, name) }
