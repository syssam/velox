package velox_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
)

// TestSchemaDefaultMethods tests the default implementations of Schema methods.
func TestSchemaDefaultMethods(t *testing.T) {
	t.Parallel()

	type TestSchema struct {
		velox.Schema
	}

	s := TestSchema{}

	// All default implementations should return nil or empty values
	assert.Nil(t, s.Fields())
	assert.Nil(t, s.Edges())
	assert.Nil(t, s.Indexes())
	assert.Equal(t, velox.Config{}, s.Config())
	assert.Nil(t, s.Mixin())
	assert.Nil(t, s.Hooks())
	assert.Nil(t, s.Interceptors())
	assert.Nil(t, s.Policy())
	assert.Nil(t, s.Annotations())
}

// TestView tests the View struct.
func TestView(t *testing.T) {
	t.Parallel()

	type TestView struct {
		velox.View
	}

	v := TestView{}

	// View embeds Schema, so it should have the same default methods
	assert.Nil(t, v.Fields())
	assert.Nil(t, v.Edges())

	// Test that View implements Viewer interface
	var _ velox.Viewer = v
}

// TestMutateFunc tests the MutateFunc adapter.
func TestMutateFunc(t *testing.T) {
	t.Parallel()

	called := false
	expectedValue := "result"

	f := velox.MutateFunc(func(_ context.Context, _ velox.Mutation) (velox.Value, error) {
		called = true
		return expectedValue, nil
	})

	ctx := context.Background()
	result, err := f.Mutate(ctx, nil)

	assert.True(t, called)
	assert.NoError(t, err)
	assert.Equal(t, expectedValue, result)
}

// TestQuerierFunc tests the QuerierFunc adapter.
func TestQuerierFunc(t *testing.T) {
	t.Parallel()

	called := false
	expectedValue := []string{"a", "b", "c"}

	f := velox.QuerierFunc(func(_ context.Context, _ velox.Query) (velox.Value, error) {
		called = true
		return expectedValue, nil
	})

	ctx := context.Background()
	result, err := f.Query(ctx, nil)

	assert.True(t, called)
	assert.NoError(t, err)
	assert.Equal(t, expectedValue, result)
}

// TestInterceptFunc tests the InterceptFunc adapter.
func TestInterceptFunc(t *testing.T) {
	t.Parallel()

	innerCalled := false

	innerQuerier := velox.QuerierFunc(func(_ context.Context, _ velox.Query) (velox.Value, error) {
		innerCalled = true
		return "inner result", nil
	})

	interceptorCalled := false
	f := velox.InterceptFunc(func(next velox.Querier) velox.Querier {
		interceptorCalled = true
		return next
	})

	wrapped := f.Intercept(innerQuerier)
	assert.True(t, interceptorCalled)

	// Execute the wrapped querier
	ctx := context.Background()
	result, err := wrapped.Query(ctx, nil)

	assert.True(t, innerCalled)
	assert.NoError(t, err)
	assert.Equal(t, "inner result", result)
}

// TestTraverseFunc tests the TraverseFunc adapter.
func TestTraverseFunc(t *testing.T) {
	t.Parallel()

	t.Run("Traverse", func(t *testing.T) {
		t.Parallel()

		called := false

		f := velox.TraverseFunc(func(_ context.Context, _ velox.Query) error {
			called = true
			return nil
		})

		ctx := context.Background()
		err := f.Traverse(ctx, nil)

		assert.True(t, called)
		assert.NoError(t, err)
	})

	t.Run("Intercept", func(t *testing.T) {
		t.Parallel()

		// TraverseFunc.Intercept returns the next querier unchanged
		innerQuerier := velox.QuerierFunc(func(_ context.Context, _ velox.Query) (velox.Value, error) {
			return "result", nil
		})

		f := velox.TraverseFunc(func(_ context.Context, _ velox.Query) error {
			return nil
		})

		wrapped := f.Intercept(innerQuerier)
		// wrapped should be the same as innerQuerier
		ctx := context.Background()
		result, err := wrapped.Query(ctx, nil)

		assert.NoError(t, err)
		assert.Equal(t, "result", result)
	})
}

// TestOpIs tests the Op.Is method.
// Note: Op.Is uses bitwise AND, so each Op is unique (they're bit flags).
func TestOpIs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		op       velox.Op
		check    velox.Op
		expected bool
	}{
		{"Create is Create", velox.OpCreate, velox.OpCreate, true},
		{"Create is not Update", velox.OpCreate, velox.OpUpdate, false},
		{"Update is Update", velox.OpUpdate, velox.OpUpdate, true},
		{"UpdateOne is UpdateOne", velox.OpUpdateOne, velox.OpUpdateOne, true},
		{"UpdateOne is not Update", velox.OpUpdateOne, velox.OpUpdate, false},
		{"Delete is Delete", velox.OpDelete, velox.OpDelete, true},
		{"DeleteOne is DeleteOne", velox.OpDeleteOne, velox.OpDeleteOne, true},
		{"DeleteOne is not Delete", velox.OpDeleteOne, velox.OpDelete, false},
		{"Update is not Delete", velox.OpUpdate, velox.OpDelete, false},
		{"Combined Update|UpdateOne is Update", velox.OpUpdate | velox.OpUpdateOne, velox.OpUpdate, true},
		{"Combined Update|UpdateOne is UpdateOne", velox.OpUpdate | velox.OpUpdateOne, velox.OpUpdateOne, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.op.Is(tt.check)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestOpString tests the Op.String method.
func TestOpString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		op       velox.Op
		expected string
	}{
		{velox.OpCreate, "OpCreate"},
		{velox.OpUpdate, "OpUpdate"},
		{velox.OpUpdateOne, "OpUpdateOne"},
		{velox.OpDelete, "OpDelete"},
		{velox.OpDeleteOne, "OpDeleteOne"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.op.String())
		})
	}
}

// TestQueryContext tests the QueryContext functions.
func TestQueryContext(t *testing.T) {
	t.Parallel()

	t.Run("NewQueryContext and QueryFromContext", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		qc := &velox.QueryContext{
			Fields: []string{"id", "name"},
		}
		withQuery := velox.NewQueryContext(ctx, qc)

		retrieved := velox.QueryFromContext(withQuery)
		require.NotNil(t, retrieved)
		assert.Equal(t, qc.Fields, retrieved.Fields)
	})

	t.Run("QueryFromContext with no query", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		retrieved := velox.QueryFromContext(ctx)
		assert.Nil(t, retrieved)
	})

	t.Run("Clone", func(t *testing.T) {
		t.Parallel()

		original := &velox.QueryContext{
			Fields: []string{"id", "name"},
			Limit:  &[]int{10}[0],
		}

		cloned := original.Clone()
		require.NotNil(t, cloned)
		assert.Equal(t, original.Fields, cloned.Fields)

		// Modifying cloned should not affect original
		cloned.Fields = append(cloned.Fields, "email")
		assert.NotEqual(t, len(original.Fields), len(cloned.Fields))
	})

	t.Run("AppendFieldOnce", func(t *testing.T) {
		t.Parallel()

		t.Run("adds new field", func(t *testing.T) {
			t.Parallel()

			qc := &velox.QueryContext{
				Fields: []string{"a", "b", "c"},
			}
			result := qc.AppendFieldOnce("d")
			assert.True(t, slices.Contains(result.Fields, "d"))
		})

		t.Run("does not duplicate existing field", func(t *testing.T) {
			t.Parallel()

			qc := &velox.QueryContext{
				Fields: []string{"a", "b", "c"},
			}
			result := qc.AppendFieldOnce("b")

			// Count occurrences of "b" - should be exactly 1
			count := 0
			for _, f := range result.Fields {
				if f == "b" {
					count++
				}
			}
			assert.Equal(t, 1, count, "field 'b' should appear exactly once")
		})
	})
}
