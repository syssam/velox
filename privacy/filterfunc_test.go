package privacy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/privacy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- FilterFunc test helpers ---

// filterableQuery implements privacy.Filterable for testing FilterFunc.
type filterableQuery struct {
	filter *testFilter
}

func (q *filterableQuery) Filter() privacy.Filter { return q.filter }

// filterableMutation wraps mockMutation and also implements privacy.Filterable.
type filterableMutation struct {
	mockMutation
	filter *testFilter
}

func (m *filterableMutation) Filter() privacy.Filter { return m.filter }

// testFilter records which predicates were applied via WhereP.
type testFilter struct {
	applied []func(*sql.Selector)
}

func (f *testFilter) WhereP(ps ...func(*sql.Selector)) {
	f.applied = append(f.applied, ps...)
}

// --- FilterFunc tests ---

// TestFilterFuncAppliesPredicatesToQuery verifies that FilterFunc invokes
// WhereP on the filter returned by a filterable query.
func TestFilterFuncAppliesPredicatesToQuery(t *testing.T) {
	t.Parallel()

	called := false
	predicate := func(s *sql.Selector) { called = true }

	ff := privacy.FilterFunc(func(_ context.Context, f privacy.Filter) error {
		f.WhereP(predicate)
		return privacy.Skip
	})

	filter := &testFilter{}
	q := &filterableQuery{filter: filter}

	err := ff.EvalQuery(context.Background(), q)

	assert.True(t, errors.Is(err, privacy.Skip))
	require.Len(t, filter.applied, 1)
	// invoke to confirm our predicate is the one stored
	filter.applied[0](nil)
	assert.True(t, called)
}

// TestFilterFuncAppliesPredicatesToMutation verifies that FilterFunc invokes
// WhereP on the filter returned by a filterable mutation.
func TestFilterFuncAppliesPredicatesToMutation(t *testing.T) {
	t.Parallel()

	called := false
	predicate := func(s *sql.Selector) { called = true }

	ff := privacy.FilterFunc(func(_ context.Context, f privacy.Filter) error {
		f.WhereP(predicate)
		return privacy.Skip
	})

	filter := &testFilter{}
	m := &filterableMutation{filter: filter}

	err := ff.EvalMutation(context.Background(), m)

	assert.True(t, errors.Is(err, privacy.Skip))
	require.Len(t, filter.applied, 1)
	filter.applied[0](nil)
	assert.True(t, called)
}

// TestFilterFuncDeniesNonFilterableQuery verifies that a query that does not
// implement privacy.Filterable causes FilterFunc to return a Deny error with
// a helpful message.
func TestFilterFuncDeniesNonFilterableQuery(t *testing.T) {
	t.Parallel()

	ff := privacy.FilterFunc(func(_ context.Context, _ privacy.Filter) error {
		return privacy.Skip
	})

	// mockQuery does NOT implement Filterable.
	err := ff.EvalQuery(context.Background(), &mockQuery{})

	assert.True(t, errors.Is(err, privacy.Deny))
	assert.Contains(t, err.Error(), "does not support filtering")
}

// TestFilterFuncDeniesNonFilterableMutation verifies that a mutation that does
// not implement privacy.Filterable causes FilterFunc to return a Deny error.
func TestFilterFuncDeniesNonFilterableMutation(t *testing.T) {
	t.Parallel()

	ff := privacy.FilterFunc(func(_ context.Context, _ privacy.Filter) error {
		return privacy.Skip
	})

	// mockMutation does NOT implement Filterable.
	err := ff.EvalMutation(context.Background(), &mockMutation{})

	assert.True(t, errors.Is(err, privacy.Deny))
	assert.Contains(t, err.Error(), "does not support filtering")
}

// TestFilterFuncImplementsQueryMutationRule confirms the compile-time assertion
// that FilterFunc satisfies QueryMutationRule (already guaranteed by var _ in
// privacy.go, but exercised here for coverage).
func TestFilterFuncImplementsQueryMutationRule(t *testing.T) {
	t.Parallel()
	var _ privacy.QueryMutationRule = privacy.FilterFunc(nil)
}

// --- IsOwner skip-on-update edge cases ---

// TestIsOwnerSkipsForWriteOperations verifies IsOwner skips for every write
// operation except OpCreate, because ownership cannot be verified without DB.
func TestIsOwnerSkipsForWriteOperations(t *testing.T) {
	t.Parallel()

	rule := privacy.IsOwner("user_id")
	viewer := &privacy.SimpleViewer{UserID: "user-123"}
	ctx := privacy.WithViewer(context.Background(), viewer)

	ops := []velox.Op{
		velox.OpUpdate,
		velox.OpUpdateOne,
		velox.OpDelete,
		velox.OpDeleteOne,
	}

	for _, op := range ops {
		t.Run(op.String(), func(t *testing.T) {
			t.Parallel()
			m := &mockMutation{
				op:       op,
				field:    "user_id",
				value:    "user-123",
				hasField: true,
			}
			err := rule.EvalMutation(ctx, m)
			assert.True(t, errors.Is(err, privacy.Skip),
				"IsOwner should Skip for op %s", op)
		})
	}
}

// TestIsOwnerInt64IDComparison verifies that IsOwner compares int64 field values
// correctly after stringification (e.g. int64(42) == viewer ID "42").
func TestIsOwnerInt64IDComparison(t *testing.T) {
	t.Parallel()

	rule := privacy.IsOwner("user_id")
	viewer := &privacy.SimpleViewer{UserID: "42"}
	ctx := privacy.WithViewer(context.Background(), viewer)

	t.Run("int64_matching_allows", func(t *testing.T) {
		t.Parallel()
		m := &mockMutation{
			op:       velox.OpCreate,
			field:    "user_id",
			value:    int64(42),
			hasField: true,
		}
		err := rule.EvalMutation(ctx, m)
		assert.True(t, errors.Is(err, privacy.Allow))
	})

	t.Run("int64_mismatch_skips", func(t *testing.T) {
		t.Parallel()
		m := &mockMutation{
			op:       velox.OpCreate,
			field:    "user_id",
			value:    int64(99),
			hasField: true,
		}
		err := rule.EvalMutation(ctx, m)
		assert.True(t, errors.Is(err, privacy.Skip))
	})
}

// TestNewPoliciesWithNilProviders verifies that NewPolicies gracefully skips
// providers that return nil policies, and returns an empty Policies that allows.
func TestNewPoliciesWithNilProviders(t *testing.T) {
	t.Parallel()

	nilProvider := &policyProvider{policy: nil}
	policy := privacy.NewPolicies(nilProvider, nilProvider, nilProvider)

	t.Run("eval_query_allows_with_all_nil_providers", func(t *testing.T) {
		t.Parallel()
		err := policy.EvalQuery(context.Background(), &mockQuery{})
		assert.NoError(t, err)
	})

	t.Run("eval_mutation_allows_with_all_nil_providers", func(t *testing.T) {
		t.Parallel()
		err := policy.EvalMutation(context.Background(), &mockMutation{})
		assert.NoError(t, err)
	})
}

// TestFilterFuncMultiplePredicates verifies that multiple predicates passed to
// WhereP are all stored and callable.
func TestFilterFuncMultiplePredicates(t *testing.T) {
	t.Parallel()

	callCount := 0
	ff := privacy.FilterFunc(func(_ context.Context, f privacy.Filter) error {
		f.WhereP(
			func(*sql.Selector) { callCount++ },
			func(*sql.Selector) { callCount++ },
			func(*sql.Selector) { callCount++ },
		)
		return privacy.Allow
	})

	filter := &testFilter{}
	q := &filterableQuery{filter: filter}

	err := ff.EvalQuery(context.Background(), q)
	assert.True(t, errors.Is(err, privacy.Allow))
	require.Len(t, filter.applied, 3)

	for _, p := range filter.applied {
		p(nil)
	}
	assert.Equal(t, 3, callCount)
}
