package runtime

import "testing"

func TestQueryPlanFields(t *testing.T) {
	plan := QueryPlan{
		SQL:          "SELECT id, name FROM users WHERE name = ?",
		Args:         []any{"alice"},
		Edges:        []EdgePlan{{Name: "posts", SQL: "SELECT * FROM posts WHERE user_id IN (?)", Args: []any{1}}},
		Interceptors: []string{"privacy:User"},
	}
	if plan.SQL == "" {
		t.Error("SQL should be set")
	}
	if len(plan.Args) != 1 {
		t.Errorf("expected 1 arg, got %d", len(plan.Args))
	}
	if len(plan.Edges) != 1 {
		t.Errorf("expected 1 edge plan, got %d", len(plan.Edges))
	}
	if plan.Edges[0].Name != "posts" {
		t.Errorf("expected edge name 'posts', got %q", plan.Edges[0].Name)
	}
	if len(plan.Interceptors) != 1 {
		t.Errorf("expected 1 interceptor, got %d", len(plan.Interceptors))
	}
}
