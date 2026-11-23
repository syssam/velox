package privacy_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"
)

// recFilter records the conditions a rule gives it.
type recFilter struct{ n int }

func (f *recFilter) Where(ps ...velox.Predicate) { f.n += len(ps) }

type recQuery struct{ f *recFilter }

func (q recQuery) Filter() privacy.Filter { return q.f }
func (q recQuery) DB() *velox.DB          { return nil }

type recMutation struct {
	op velox.Op
	f  *recFilter
}

func (m recMutation) Op() velox.Op                   { return m.op }
func (m recMutation) Filter() privacy.Filter         { return m.f }
func (m recMutation) DB() *velox.DB                  { return nil }
func (m recMutation) Value(string) (any, bool)       { return nil, false }
func (m recMutation) withOp(op velox.Op) recMutation { m.op = op; return m }

// step is a rule that records its name, narrows, and returns err.
func step(ran *[]string, name string, err error) privacy.QueryMutationRule {
	return privacy.FilterFunc(func(_ context.Context, f privacy.Filter) error {
		*ran = append(*ran, name)
		f.Where(velox.FieldEQ(name, 1))
		return err
	})
}

// A list runs in order: nil and Skip go on, Allow ends it with nil, any
// other error ends it with that error, and a list that runs out allows. The
// conditions given before the end stay.
func TestEvalOrder(t *testing.T) {
	other := errors.New("other")
	for _, c := range []struct {
		name  string
		rules []error
		ran   []string
		err   error
	}{
		{"empty", nil, nil, nil},
		{"skip and nil go on", []error{privacy.Skip, nil, privacy.Skipf("why")}, []string{"r0", "r1", "r2"}, nil},
		{"allow stops", []error{privacy.Skip, privacy.Allow, privacy.Deny}, []string{"r0", "r1"}, nil},
		{"wrapped allow stops", []error{privacy.Allowf("ok %d", 1), privacy.Deny}, []string{"r0"}, nil},
		{"deny stops", []error{nil, privacy.Deny, privacy.Allow}, []string{"r0", "r1"}, privacy.Deny},
		{"other error denies", []error{other, privacy.Allow}, []string{"r0"}, other},
	} {
		t.Run(c.name, func(t *testing.T) {
			var ran []string
			var qp privacy.QueryPolicy
			var mp privacy.MutationPolicy
			for i, e := range c.rules {
				r := step(&ran, "r"+string(rune('0'+i)), e)
				qp = append(qp, r)
				mp = append(mp, r)
			}
			f := &recFilter{}
			err := qp.EvalQuery(context.Background(), recQuery{f})
			if !errors.Is(err, c.err) || (c.err == nil) != (err == nil) || !slices.Equal(ran, c.ran) || f.n != len(c.ran) {
				t.Errorf("query: %v ran %v (%d conditions), want %v ran %v", err, ran, f.n, c.err, c.ran)
			}
			ran = nil
			err = mp.EvalMutation(context.Background(), recMutation{velox.OpUpdate, &recFilter{}})
			if !errors.Is(err, c.err) || (c.err == nil) != (err == nil) || !slices.Equal(ran, c.ran) {
				t.Errorf("mutation: %v ran %v, want %v ran %v", err, ran, c.err, c.ran)
			}
		})
	}
}

// A QueryPolicy is a rule itself: an Allow in it ends it alone, and the
// outer list goes on.
func TestNestedPolicy(t *testing.T) {
	var ran []string
	qp := privacy.QueryPolicy{
		privacy.QueryPolicy{step(&ran, "in0", privacy.Allow), step(&ran, "in1", nil)},
		step(&ran, "out", privacy.Deny),
	}
	if err := qp.EvalQuery(context.Background(), recQuery{&recFilter{}}); !errors.Is(err, privacy.Deny) || !slices.Equal(ran, []string{"in0", "out"}) {
		t.Errorf("%v ran %v", err, ran)
	}
}

func TestDecisions(t *testing.T) {
	if !privacy.IsDecision(privacy.Denyf("x")) || privacy.IsDecision(errors.New("x")) {
		t.Error("IsDecision")
	}
	if got := privacy.Denyf("no %s", "way").Error(); got != "no way: velox/privacy: deny rule" {
		t.Errorf("Denyf: %q", got)
	}
	ctx := context.Background()
	q := recQuery{&recFilter{}}
	if privacy.AlwaysAllowRule().EvalQuery(ctx, q) != privacy.Allow || privacy.AlwaysDenyRule().EvalQuery(ctx, q) != privacy.Deny {
		t.Error("fixed rules")
	}
	m := recMutation{velox.OpDelete, &recFilter{}}
	rule := privacy.DenyMutationOperationRule(velox.OpDelete)
	if err := rule.EvalMutation(ctx, m); !errors.Is(err, privacy.Deny) || err.Error() != "velox/privacy: operation delete is not allowed: velox/privacy: deny rule" {
		t.Errorf("delete: %v", err)
	}
	if err := rule.EvalMutation(ctx, m.withOp(velox.OpUpdate)); err != privacy.Skip {
		t.Errorf("update: %v", err)
	}
	cr := privacy.ContextQueryMutationRule(func(context.Context) error { return privacy.Allow })
	if cr.EvalQuery(ctx, q) != privacy.Allow || cr.EvalMutation(ctx, m) != privacy.Allow {
		t.Error("context rule")
	}
	if d, ok := privacy.DecisionFromContext(privacy.DecisionContext(ctx, privacy.Allow)); !ok || d != nil {
		t.Errorf("Allow in context: %v %v", d, ok)
	}
	if d, ok := privacy.DecisionFromContext(privacy.DecisionContext(ctx, privacy.Deny)); !ok || d != privacy.Deny {
		t.Errorf("Deny in context: %v %v", d, ok)
	}
	if _, ok := privacy.DecisionFromContext(privacy.DecisionContext(ctx, nil)); ok {
		t.Error("nil in context")
	}
}
