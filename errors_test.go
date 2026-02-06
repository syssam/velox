package velox_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
)

func TestNotFoundError(t *testing.T) {
	t.Run("Error", func(t *testing.T) {
		err := velox.NewNotFoundError("User")
		assert.Equal(t, "velox: User not found", err.Error())
	})

	t.Run("Is", func(t *testing.T) {
		err := velox.NewNotFoundError("Post")
		assert.True(t, errors.Is(err, velox.ErrNotFound))
	})

	t.Run("IsNotFound", func(t *testing.T) {
		err := velox.NewNotFoundError("Comment")
		assert.True(t, velox.IsNotFound(err))

		// Wrapped error
		wrapped := fmt.Errorf("wrapper: %w", err)
		assert.True(t, velox.IsNotFound(wrapped))

		// Sentinel error
		assert.True(t, velox.IsNotFound(velox.ErrNotFound))

		// Non-matching error
		assert.False(t, velox.IsNotFound(errors.New("other error")))
		assert.False(t, velox.IsNotFound(nil))
	})
}

func TestNotSingularError(t *testing.T) {
	t.Run("Error", func(t *testing.T) {
		err := velox.NewNotSingularError("User")
		assert.Equal(t, "velox: User not singular", err.Error())
	})

	t.Run("Is", func(t *testing.T) {
		err := velox.NewNotSingularError("Post")
		assert.True(t, errors.Is(err, velox.ErrNotSingular))
	})

	t.Run("IsNotSingular", func(t *testing.T) {
		err := velox.NewNotSingularError("Comment")
		assert.True(t, velox.IsNotSingular(err))

		// Wrapped error
		wrapped := fmt.Errorf("wrapper: %w", err)
		assert.True(t, velox.IsNotSingular(wrapped))

		// Sentinel error
		assert.True(t, velox.IsNotSingular(velox.ErrNotSingular))

		// Non-matching error
		assert.False(t, velox.IsNotSingular(errors.New("other error")))
		assert.False(t, velox.IsNotSingular(nil))
	})
}

func TestNotLoadedError(t *testing.T) {
	t.Run("Error", func(t *testing.T) {
		err := velox.NewNotLoadedError("posts")
		assert.Equal(t, `velox: edge "posts" was not loaded`, err.Error())
	})

	t.Run("IsNotLoaded", func(t *testing.T) {
		err := velox.NewNotLoadedError("comments")
		assert.True(t, velox.IsNotLoaded(err))

		// Wrapped error
		wrapped := fmt.Errorf("wrapper: %w", err)
		assert.True(t, velox.IsNotLoaded(wrapped))

		// Non-matching error
		assert.False(t, velox.IsNotLoaded(errors.New("other error")))
		assert.False(t, velox.IsNotLoaded(nil))
	})
}

func TestConstraintError(t *testing.T) {
	t.Run("Error", func(t *testing.T) {
		err := velox.NewConstraintError("UNIQUE constraint failed", nil)
		assert.Equal(t, "velox: constraint failed: UNIQUE constraint failed", err.Error())
	})

	t.Run("Unwrap", func(t *testing.T) {
		underlying := errors.New("db error")
		err := velox.NewConstraintError("constraint violated", underlying)
		assert.True(t, errors.Is(err, underlying))
	})

	t.Run("IsConstraintError", func(t *testing.T) {
		err := velox.NewConstraintError("check failed", nil)
		assert.True(t, velox.IsConstraintError(err))

		// Wrapped error
		wrapped := fmt.Errorf("wrapper: %w", err)
		assert.True(t, velox.IsConstraintError(wrapped))

		// Non-matching error
		assert.False(t, velox.IsConstraintError(errors.New("other error")))
		assert.False(t, velox.IsConstraintError(nil))
	})
}

func TestValidationError(t *testing.T) {
	t.Run("Error", func(t *testing.T) {
		err := velox.NewValidationError("email", errors.New("invalid format"))
		assert.Equal(t, `velox: validator failed for field "email": invalid format`, err.Error())
	})

	t.Run("Unwrap", func(t *testing.T) {
		underlying := errors.New("too short")
		err := velox.NewValidationError("name", underlying)
		assert.True(t, errors.Is(err, underlying))
	})

	t.Run("IsValidationError", func(t *testing.T) {
		err := velox.NewValidationError("age", errors.New("must be positive"))
		assert.True(t, velox.IsValidationError(err))

		// Wrapped error
		wrapped := fmt.Errorf("wrapper: %w", err)
		assert.True(t, velox.IsValidationError(wrapped))

		// Non-matching error
		assert.False(t, velox.IsValidationError(errors.New("other error")))
		assert.False(t, velox.IsValidationError(nil))
	})
}

func TestRollbackError(t *testing.T) {
	t.Run("Error", func(t *testing.T) {
		err := &velox.RollbackError{Err: errors.New("connection lost")}
		assert.Equal(t, "velox: rollback failed: connection lost", err.Error())
	})

	t.Run("Unwrap", func(t *testing.T) {
		underlying := errors.New("timeout")
		err := &velox.RollbackError{Err: underlying}
		assert.True(t, errors.Is(err, underlying))
	})
}

func TestAggregateError(t *testing.T) {
	t.Run("NoErrors", func(t *testing.T) {
		err := velox.NewAggregateError()
		assert.Nil(t, err)
	})

	t.Run("NilErrors", func(t *testing.T) {
		err := velox.NewAggregateError(nil, nil, nil)
		assert.Nil(t, err)
	})

	t.Run("SingleError", func(t *testing.T) {
		single := errors.New("single error")
		err := velox.NewAggregateError(single)
		assert.Equal(t, single, err)
	})

	t.Run("MultipleErrors", func(t *testing.T) {
		err1 := errors.New("error 1")
		err2 := errors.New("error 2")
		err := velox.NewAggregateError(err1, err2)

		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "multiple errors")
		assert.Contains(t, err.Error(), "error 1")
		assert.Contains(t, err.Error(), "error 2")
	})

	t.Run("MixedNilAndErrors", func(t *testing.T) {
		err1 := errors.New("error 1")
		err := velox.NewAggregateError(nil, err1, nil)

		require.NotNil(t, err)
		assert.Equal(t, err1, err) // Single non-nil error returned directly
	})
}

func TestSentinelErrors(t *testing.T) {
	t.Run("ErrNotFound", func(t *testing.T) {
		assert.Error(t, velox.ErrNotFound)
		assert.Contains(t, velox.ErrNotFound.Error(), "not found")
	})

	t.Run("ErrNotSingular", func(t *testing.T) {
		assert.Error(t, velox.ErrNotSingular)
		assert.Contains(t, velox.ErrNotSingular.Error(), "not singular")
	})

	t.Run("ErrTxStarted", func(t *testing.T) {
		assert.Error(t, velox.ErrTxStarted)
		assert.Contains(t, velox.ErrTxStarted.Error(), "transaction")
	})
}

// BenchmarkErrors benchmarks error creation and checking.
func BenchmarkErrors(b *testing.B) {
	b.Run("NewNotFoundError", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = velox.NewNotFoundError("User")
		}
	})

	b.Run("IsNotFound", func(b *testing.B) {
		err := velox.NewNotFoundError("User")
		for i := 0; i < b.N; i++ {
			_ = velox.IsNotFound(err)
		}
	})

	b.Run("NewConstraintError", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = velox.NewConstraintError("unique", nil)
		}
	})

	b.Run("IsConstraintError", func(b *testing.B) {
		err := velox.NewConstraintError("unique", nil)
		for i := 0; i < b.N; i++ {
			_ = velox.IsConstraintError(err)
		}
	})

	b.Run("NewValidationError", func(b *testing.B) {
		underlying := errors.New("invalid")
		for i := 0; i < b.N; i++ {
			_ = velox.NewValidationError("field", underlying)
		}
	})

	b.Run("NewAggregateError_multiple", func(b *testing.B) {
		err1 := errors.New("err1")
		err2 := errors.New("err2")
		err3 := errors.New("err3")
		for i := 0; i < b.N; i++ {
			_ = velox.NewAggregateError(err1, err2, err3)
		}
	})
}
