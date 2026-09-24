package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
)

// throughEdgePolicy is a policy that filters its entity through an edge to
// next, the way "a post is visible if its author is" does.
type throughEdgePolicy struct{ next string }

func (p throughEdgePolicy) EvalQuery(ctx context.Context, _ velox.Query) error {
	sub := sql.Select("id").From(sql.Table(p.next)).WithContext(ctx)
	ApplyEdgePolicy(sub, p.next)
	return sub.Err()
}

func (throughEdgePolicy) EvalMutation(context.Context, velox.Mutation) error { return nil }

// TestApplyEdgePolicy_CycleFailsClosed pins that two policies filtering
// through edges to each other end in an error naming the cycle, not in
// unbounded recursion (a stack overflow that takes the process down).
func TestApplyEdgePolicy_CycleFailsClosed(t *testing.T) {
	var a, b velox.Policy = throughEdgePolicy{next: "EdgePolicyCycB"}, throughEdgePolicy{next: "EdgePolicyCycA"}
	for name, p := range map[string]*velox.Policy{"EdgePolicyCycA": &a, "EdgePolicyCycB": &b} {
		RegisterQueryFactory(name, func(Config) any { return struct{}{} })
		RegisterEntityPolicy(name, p)
		// The registries are process-global: leave no half-registered
		// entity behind for TestValidateRegistries_Consistent to find.
		t.Cleanup(func() {
			queryMu.Lock()
			delete(queryFactories, name)
			queryMu.Unlock()
			policyMu.Lock()
			delete(policyRegistry, name)
			policyMu.Unlock()
		})
	}
	s := sql.Select("id").From(sql.Table("a"))
	ApplyEdgePolicy(s, "EdgePolicyCycA")
	require.ErrorContains(t, s.Err(), "EdgePolicyCycA -> EdgePolicyCycB -> EdgePolicyCycA")
}
