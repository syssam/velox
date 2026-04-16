package runtime

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotFoundError(t *testing.T) {
	err := NewNotFoundError("User")
	assert.Equal(t, "velox: User not found", err.Error())
}

func TestNotSingularError(t *testing.T) {
	err := NewNotSingularError("User")
	assert.Equal(t, "velox: User not singular", err.Error())
}

func TestNotLoadedError(t *testing.T) {
	err := NewNotLoadedError("posts")
	assert.Contains(t, err.Error(), "posts")
}

func TestConstraintError(t *testing.T) {
	cause := fmt.Errorf("unique violation")
	err := NewConstraintError("duplicate key", cause)
	assert.Equal(t, "velox: constraint failed: duplicate key", err.Error())
	assert.ErrorIs(t, err, cause)
}

func TestConstraintErrorNilWrap(t *testing.T) {
	err := NewConstraintError("check failed", nil)
	assert.Equal(t, "velox: constraint failed: check failed", err.Error())
	assert.Nil(t, err.Unwrap())
}

func TestValidationError(t *testing.T) {
	cause := fmt.Errorf("too short")
	err := &ValidationError{Name: "email", Err: cause}
	assert.Equal(t, `velox: validator failed for field "email": too short`, err.Error())
	assert.ErrorIs(t, err, cause)
}

func TestIsNotFound(t *testing.T) {
	assert.True(t, IsNotFound(NewNotFoundError("User")))
	assert.True(t, IsNotFound(fmt.Errorf("wrap: %w", NewNotFoundError("User"))))
	assert.False(t, IsNotFound(errors.New("other")))
	assert.False(t, IsNotFound(nil))
}

func TestIsNotSingular(t *testing.T) {
	assert.True(t, IsNotSingular(NewNotSingularError("User")))
	assert.True(t, IsNotSingular(fmt.Errorf("wrap: %w", NewNotSingularError("User"))))
	assert.False(t, IsNotSingular(errors.New("other")))
	assert.False(t, IsNotSingular(nil))
}

func TestIsConstraintError(t *testing.T) {
	assert.True(t, IsConstraintError(NewConstraintError("dup", nil)))
	assert.True(t, IsConstraintError(fmt.Errorf("wrap: %w", NewConstraintError("dup", nil))))
	assert.False(t, IsConstraintError(errors.New("other")))
	assert.False(t, IsConstraintError(nil))
}

func TestIsNotLoaded(t *testing.T) {
	assert.True(t, IsNotLoaded(NewNotLoadedError("posts")))
	assert.True(t, IsNotLoaded(fmt.Errorf("wrap: %w", NewNotLoadedError("posts"))))
	assert.False(t, IsNotLoaded(errors.New("other")))
	assert.False(t, IsNotLoaded(nil))
}

func TestIsValidationError(t *testing.T) {
	assert.True(t, IsValidationError(&ValidationError{Name: "x", Err: errors.New("bad")}))
	assert.True(t, IsValidationError(fmt.Errorf("wrap: %w", &ValidationError{Name: "x", Err: errors.New("bad")})))
	assert.False(t, IsValidationError(errors.New("other")))
	assert.False(t, IsValidationError(nil))
}

func TestSentinelErrors(t *testing.T) {
	require.NotNil(t, ErrNotFound)
	require.NotNil(t, ErrNotSingular)
	require.NotNil(t, ErrTxStarted)
	assert.Equal(t, "velox: entity not found", ErrNotFound.Error())
	assert.Equal(t, "velox: entity not singular", ErrNotSingular.Error())
	assert.Equal(t, "velox: cannot start a transaction within a transaction", ErrTxStarted.Error())
}

func TestNotFoundError_Is_CompatibleWithRootPackage(t *testing.T) {
	// runtime.NotFoundError should be matchable via errors.Is with velox.ErrNotFound
	err := NewNotFoundError("User")
	assert.True(t, errors.Is(err, ErrNotFound), "errors.Is should match runtime.NotFoundError against ErrNotFound")

	// Also works when wrapped
	wrapped := fmt.Errorf("wrap: %w", err)
	assert.True(t, errors.Is(wrapped, ErrNotFound))
}

func TestNotSingularError_Is_CompatibleWithRootPackage(t *testing.T) {
	err := NewNotSingularError("User")
	assert.True(t, errors.Is(err, ErrNotSingular), "errors.Is should match runtime.NotSingularError against ErrNotSingular")

	wrapped := fmt.Errorf("wrap: %w", err)
	assert.True(t, errors.Is(wrapped, ErrNotSingular))
}
