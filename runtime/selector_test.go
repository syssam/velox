package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSelector(t *testing.T, scanFn func(context.Context, any) error) *Selector {
	t.Helper()
	fields := []string{"name"}
	s := NewSelector("TestSelector", &fields, scanFn)
	return &s
}

func TestSelector_Strings(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]string)
		*ptr = []string{"alice", "bob"}
		return nil
	})
	vals, err := s.Strings(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"alice", "bob"}, vals)
}

func TestSelector_StringsX(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]string)
		*ptr = []string{"alice"}
		return nil
	})
	assert.Equal(t, []string{"alice"}, s.StringsX(context.Background()))
}

func TestSelector_StringsX_Panic(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, _ any) error {
		return fmt.Errorf("boom")
	})
	assert.Panics(t, func() { s.StringsX(context.Background()) })
}

func TestSelector_String(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]string)
		*ptr = []string{"alice"}
		return nil
	})
	val, err := s.String(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "alice", val)
}

func TestSelector_String_NotFound(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]string)
		*ptr = []string{}
		return nil
	})
	_, err := s.String(context.Background())
	require.Error(t, err)
	assert.True(t, IsNotFound(err))
}

func TestSelector_String_NotSingular(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]string)
		*ptr = []string{"a", "b"}
		return nil
	})
	_, err := s.String(context.Background())
	require.Error(t, err)
}

func TestSelector_StringX(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]string)
		*ptr = []string{"alice"}
		return nil
	})
	assert.Equal(t, "alice", s.StringX(context.Background()))
}

func TestSelector_StringX_Panic(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, _ any) error {
		return fmt.Errorf("boom")
	})
	assert.Panics(t, func() { s.StringX(context.Background()) })
}

func TestSelector_Ints(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]int)
		*ptr = []int{1, 2, 3}
		return nil
	})
	vals, err := s.Ints(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, vals)
}

func TestSelector_Int(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]int)
		*ptr = []int{42}
		return nil
	})
	val, err := s.Int(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestSelector_Float64s(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]float64)
		*ptr = []float64{1.1, 2.2}
		return nil
	})
	vals, err := s.Float64s(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []float64{1.1, 2.2}, vals)
}

func TestSelector_Float64(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]float64)
		*ptr = []float64{3.14}
		return nil
	})
	val, err := s.Float64(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3.14, val)
}

func TestSelector_Bools(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]bool)
		*ptr = []bool{true, false}
		return nil
	})
	vals, err := s.Bools(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []bool{true, false}, vals)
}

func TestSelector_Bool(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, v any) error {
		ptr := v.(*[]bool)
		*ptr = []bool{true}
		return nil
	})
	val, err := s.Bool(context.Background())
	require.NoError(t, err)
	assert.True(t, val)
}

func TestSelector_ScanError(t *testing.T) {
	scanErr := errors.New("scan failed")
	s := newTestSelector(t, func(_ context.Context, _ any) error {
		return scanErr
	})
	_, err := s.Strings(context.Background())
	require.ErrorIs(t, err, scanErr)
}

func TestSelector_MultipleFields_Error(t *testing.T) {
	fields := []string{"name", "age"}
	sel := NewSelector("Test", &fields, func(_ context.Context, _ any) error { return nil })
	s := &sel
	_, err := s.Strings(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not achievable when selecting more than 1 field")
}

func TestSelector_ScanX_Panic(t *testing.T) {
	s := newTestSelector(t, func(_ context.Context, _ any) error {
		return fmt.Errorf("boom")
	})
	assert.Panics(t, func() { s.ScanX(context.Background(), nil) })
}
