package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAggTermRefusals(t *testing.T) {
	boom := errors.New("boom")
	col := ColumnTerm(0)
	for _, c := range []struct {
		name string
		term Term
		want string
	}{
		{"of no term", AggTerm(TermSum, Term{}), "an aggregate of no term"},
		{"of an invalid term", AggTerm(TermSum, InvalidTerm(boom)), "boom"},
		{"of an aggregate", AggTerm(TermSum, AggTerm(TermMax, col)), "an aggregate of an aggregate"},
	} {
		err := c.term.problem()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: problem = %v, want one with %q", c.name, err, c.want)
		}
	}
	if err := AggTerm(TermCountAll, Term{}).problem(); err != nil {
		t.Errorf("COUNT(*) of no term: problem = %v, want none", err)
	}
	if err := AggTerm(TermSum, col).problem(); err != nil {
		t.Errorf("SUM(column): problem = %v, want none", err)
	}
}

func TestCheckProjectRefusals(t *testing.T) {
	in := &EntityInfo{Name: "Thing"}
	terms := []Term{ColumnTerm(0)}
	for _, c := range []struct {
		name string
		st   Statement
		want string
	}{
		{"a lock", Statement{Lock: LockForUpdate, Project: &Projection{Terms: terms}}, "cannot take a lock"},
		{"a lock wait", Statement{LockWait: LockNoWait, Project: &Projection{Terms: terms}}, "cannot take a lock"},
		{"no terms", Statement{Project: &Projection{}}, "has no terms"},
	} {
		c.st.Entity = in
		c.st.Op = OpSelect
		err := checkProject(&c.st, "test")
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "Thing") {
			t.Errorf("%s: checkProject = %v, want one naming Thing with %q", c.name, err, c.want)
		}
	}
	if err := checkProject(&Statement{}, "test"); err != nil {
		t.Errorf("a statement with no projection: %v", err)
	}
}

func TestAndInserts(t *testing.T) {
	var order []string
	step := func(name string, err error) func(context.Context, Row) error {
		return func(context.Context, Row) error {
			order = append(order, name)
			return err
		}
	}
	ctx := context.Background()
	denied := errors.New("denied")

	if got := andInserts(nil, step("b", nil)); got == nil || got(ctx, Row{}) != nil || strings.Join(order, "") != "b" {
		t.Errorf("a nil first: runs only b, got %v", order)
	}
	order = nil
	if err := andInserts(step("a", nil), step("b", nil))(ctx, Row{}); err != nil || strings.Join(order, "") != "ab" {
		t.Errorf("both: err %v, order %v", err, order)
	}
	order = nil
	if err := andInserts(step("a", denied), step("b", nil))(ctx, Row{}); !errors.Is(err, denied) || strings.Join(order, "") != "a" {
		t.Errorf("a first denies: err %v, order %v; want b skipped", err, order)
	}
	order = nil
	if err := andInserts(step("a", nil), step("b", denied))(ctx, Row{}); !errors.Is(err, denied) || strings.Join(order, "") != "ab" {
		t.Errorf("b denies: err %v, order %v", err, order)
	}
}
