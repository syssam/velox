package privacy_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"
)

// TestCombinators is And's, Or's and Not's truth tables, veloxe's but for
// Or keeping an error that is not a decision, on reads and writes: which
// result each returns, as it is, and which rules ran. Not never turns an
// error into Allow.
func TestCombinators(t *testing.T) {
	other := errors.New("other")
	wrappedDeny := privacy.Denyf("no viewer")
	wrappedAllow := privacy.Allowf("admin")
	wrappedSkip := privacy.Skipf("later")
	and := func(rs ...privacy.QueryMutationRule) privacy.QueryMutationRule { return privacy.And(rs...) }
	or := func(rs ...privacy.QueryMutationRule) privacy.QueryMutationRule { return privacy.Or(rs...) }
	not := func(rs ...privacy.QueryMutationRule) privacy.QueryMutationRule { return privacy.Not(rs[0]) }
	for _, c := range []struct {
		name  string
		comb  func(...privacy.QueryMutationRule) privacy.QueryMutationRule
		rules []error
		want  error // compared by identity
		ran   int
	}{
		{"and empty", and, nil, privacy.Allow, 0},
		{"and allow allow", and, []error{privacy.Allow, wrappedAllow}, privacy.Allow, 2},
		{"and allow skip", and, []error{privacy.Allow, privacy.Skip}, privacy.Skip, 2},
		{"and skip deny", and, []error{privacy.Skip, privacy.Deny}, privacy.Skip, 1},
		{"and nil deny", and, []error{nil, privacy.Deny}, nil, 1},
		{"and allow deny", and, []error{privacy.Allow, privacy.Deny}, privacy.Deny, 2},
		{"and allow wrapped deny", and, []error{privacy.Allow, wrappedDeny}, wrappedDeny, 2},
		{"and other", and, []error{other, privacy.Allow}, other, 1},
		{"or empty", or, nil, privacy.Deny, 0},
		{"or deny allow", or, []error{privacy.Deny, wrappedAllow}, privacy.Allow, 2},
		{"or allow deny", or, []error{privacy.Allow, privacy.Deny}, privacy.Allow, 1},
		{"or deny skip", or, []error{privacy.Deny, privacy.Skip}, privacy.Skip, 2},
		{"or skip deny", or, []error{privacy.Skip, privacy.Deny}, privacy.Deny, 2},
		{"or skip nil", or, []error{privacy.Skip, nil}, nil, 2},
		{"or other deny", or, []error{other, wrappedDeny}, other, 2},
		{"or other skip", or, []error{other, privacy.Skip}, other, 2},
		{"or skip other nil", or, []error{privacy.Skip, other, nil}, other, 3},
		{"or other allow", or, []error{other, privacy.Allow}, privacy.Allow, 2},
		{"and allow other", and, []error{privacy.Allow, other}, other, 2},
		{"not allow", not, []error{privacy.Allow}, privacy.Deny, 1},
		{"not wrapped allow", not, []error{wrappedAllow}, privacy.Deny, 1},
		{"not deny", not, []error{privacy.Deny}, privacy.Allow, 1},
		{"not wrapped deny", not, []error{wrappedDeny}, wrappedDeny, 1},
		{"not skip", not, []error{privacy.Skip}, privacy.Skip, 1},
		{"not wrapped skip", not, []error{wrappedSkip}, wrappedSkip, 1},
		{"not nil", not, []error{nil}, nil, 1},
		{"not other", not, []error{other}, other, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			var ran []string
			rules := make([]privacy.QueryMutationRule, len(c.rules))
			for i, e := range c.rules {
				rules[i] = step(&ran, "r"+strconv.Itoa(i), e)
			}
			r := c.comb(rules...)
			if got := r.EvalQuery(context.Background(), recQuery{&recFilter{}}); got != c.want || len(ran) != c.ran { //nolint:errorlint // identity: the result is returned as it is
				t.Errorf("query: %v after %d rules, want %v after %d", got, len(ran), c.want, c.ran)
			}
			ran = nil
			if got := r.EvalMutation(context.Background(), recMutation{velox.OpUpdate, &recFilter{}}); got != c.want || len(ran) != c.ran { //nolint:errorlint // as above
				t.Errorf("mutation: %v after %d rules, want %v after %d", got, len(ran), c.want, c.ran)
			}
		})
	}
}

// A combinator in a list decides as one rule: an And that skips lets the
// list go on, an Or that allows ends it, and an Or that keeps an error
// denies.
func TestCombinatorInList(t *testing.T) {
	var ran []string
	qp := privacy.QueryPolicy{
		privacy.And(step(&ran, "a0", privacy.Allow), step(&ran, "a1", privacy.Skip)),
		privacy.Or(step(&ran, "o0", privacy.Deny), step(&ran, "o1", privacy.Allow)),
		step(&ran, "last", privacy.Deny),
	}
	if err := qp.EvalQuery(context.Background(), recQuery{&recFilter{}}); err != nil || !slices.Equal(ran, []string{"a0", "a1", "o0", "o1"}) {
		t.Errorf("%v ran %v", err, ran)
	}
	// An Or whose rule failed denies the list, though a later branch
	// skipped: the list does not run out and allow.
	other := errors.New("no store")
	ran = nil
	qp = privacy.QueryPolicy{privacy.Or(step(&ran, "e", other), step(&ran, "s", privacy.Skip)), step(&ran, "after", nil)}
	if err := qp.EvalQuery(context.Background(), recQuery{&recFilter{}}); err != other || !slices.Equal(ran, []string{"e", "s"}) { //nolint:errorlint // identity
		t.Errorf("failed Or: %v ran %v", err, ran)
	}
}

// TestWithTrace: each rule of a list, and each a combinator runs, reports
// its decision after it returns, inner rules before the combinator; a rule
// after the list's end reports nothing; a nil function and no trace change
// nothing.
func TestWithTrace(t *testing.T) {
	var got []privacy.TraceEntry
	ctx := privacy.WithTrace(context.Background(), func(e privacy.TraceEntry) { got = append(got, e) })
	var ran []string
	other := errors.New("other")
	mp := privacy.MutationPolicy{
		privacy.Not(step(&ran, "n", privacy.Deny)),
		step(&ran, "never", privacy.Skip),
	}
	if err := mp.EvalMutation(ctx, recMutation{velox.OpDelete, &recFilter{}}); err != nil {
		t.Fatal(err)
	}
	want := []privacy.TraceEntry{
		{Rule: "privacy.FilterFunc", Index: 0, Op: velox.OpDelete, Decision: "deny", Err: privacy.Deny},
		{Rule: "privacy.notRule", Index: 0, Op: velox.OpDelete, Decision: "allow", Err: privacy.Allow},
	}
	if !slices.Equal(got, want) {
		t.Errorf("mutation trace\n got %+v\nwant %+v", got, want)
	}
	got = nil
	qp := privacy.QueryPolicy{privacy.QueryRuleFunc(func(context.Context, privacy.Query) error { return nil }), step(&ran, "o", other)}
	if err := qp.EvalQuery(ctx, recQuery{&recFilter{}}); err != other { //nolint:errorlint // identity
		t.Fatal(err)
	}
	want = []privacy.TraceEntry{
		{Rule: "privacy.QueryRuleFunc", Index: 0, Op: velox.OpSelect, Decision: "skip"},
		{Rule: "privacy.FilterFunc", Index: 1, Op: velox.OpSelect, Decision: "deny", Err: other},
	}
	if !slices.Equal(got, want) {
		t.Errorf("query trace\n got %+v\nwant %+v", got, want)
	}
	if privacy.WithTrace(context.Background(), nil) != context.Background() {
		t.Error("a nil function changed the context")
	}
	got = nil
	if err := qp.EvalQuery(context.Background(), recQuery{&recFilter{}}); err != other || got != nil { //nolint:errorlint // identity
		t.Errorf("untraced: %v, %v", err, got)
	}
}

// TestNotKeepsConditions: Not over a rule that narrows inverts its decision
// and keeps its condition, not negated, so the rows stay narrowed.
func TestNotKeepsConditions(t *testing.T) {
	var ran []string
	f := &recFilter{}
	if err := privacy.Not(step(&ran, "f", privacy.Deny)).EvalQuery(context.Background(), recQuery{f}); err != privacy.Allow { //nolint:errorlint // identity
		t.Errorf("Not(Deny): %v, want Allow", err)
	}
	if f.n != 1 {
		t.Errorf("Not over a filter rule: %d conditions, want its 1 kept", f.n)
	}
	m := recMutation{velox.OpUpdate, &recFilter{}}
	if err := privacy.Not(step(&ran, "f", privacy.Skip)).EvalMutation(context.Background(), m); err != privacy.Skip { //nolint:errorlint // identity
		t.Errorf("Not(Skip): %v, want Skip", err)
	}
	if m.f.n != 1 {
		t.Errorf("Not over a filter rule on a write: %d conditions, want 1", m.f.n)
	}
}
