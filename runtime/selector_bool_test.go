package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// Selector BoolsX / BoolX coverage
// =============================================================================

func TestSelector_BoolsX(t *testing.T) {
	scanFn := func(_ context.Context, v any) error {
		ptr := v.(*[]bool)
		*ptr = []bool{true, false}
		return nil
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	result := s.BoolsX(context.Background())
	assert.Equal(t, []bool{true, false}, result)
}

func TestSelector_BoolsX_Panics(t *testing.T) {
	scanFn := func(_ context.Context, _ any) error {
		return errors.New("scan error")
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	assert.Panics(t, func() { s.BoolsX(context.Background()) })
}

func TestSelector_BoolX(t *testing.T) {
	scanFn := func(_ context.Context, v any) error {
		ptr := v.(*[]bool)
		*ptr = []bool{true}
		return nil
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	result := s.BoolX(context.Background())
	assert.True(t, result)
}

func TestSelector_BoolX_Panics(t *testing.T) {
	scanFn := func(_ context.Context, _ any) error {
		return errors.New("scan error")
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	assert.Panics(t, func() { s.BoolX(context.Background()) })
}

func TestSelector_Bool_NotFound(t *testing.T) {
	scanFn := func(_ context.Context, v any) error {
		ptr := v.(*[]bool)
		*ptr = []bool{}
		return nil
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	_, err := s.Bool(context.Background())
	assert.True(t, IsNotFound(err))
}

func TestSelector_Bool_NotSingular(t *testing.T) {
	scanFn := func(_ context.Context, v any) error {
		ptr := v.(*[]bool)
		*ptr = []bool{true, false}
		return nil
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	_, err := s.Bool(context.Background())
	assert.True(t, IsNotSingular(err))
}
