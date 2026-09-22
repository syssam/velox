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

	t.Run("Edge", func(t *testing.T) {
		err := velox.NewNotLoadedError("posts")
		assert.Equal(t, "posts", err.Edge())
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

	t.Run("Message", func(t *testing.T) {
		err := velox.NewConstraintError("UNIQUE constraint failed", nil)
		assert.Equal(t, "UNIQUE constraint failed", err.Message())
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

	t.Run("EntityAndFieldFields", func(t *testing.T) {
		err := &velox.ValidationError{
			Name:   "name",
			Err:    errors.New("missing required field \"User.name\""),
			Entity: "User",
			Field:  "name",
		}
		assert.Equal(t, "User", err.Entity)
		assert.Equal(t, "name", err.Field)
		// Error() output is unchanged — backward compatible.
		assert.Equal(t, `velox: validator failed for field "name": missing required field "User.name"`, err.Error())
	})

	t.Run("EntityAndFieldViaErrorsAs", func(t *testing.T) {
		err := &velox.ValidationError{
			Name:   "email",
			Err:    errors.New("invalid format"),
			Entity: "User",
			Field:  "email",
		}
		wrapped := fmt.Errorf("wrapper: %w", err)

		var ve *velox.ValidationError
		require.True(t, errors.As(wrapped, &ve))
		assert.Equal(t, "User", ve.Entity)
		assert.Equal(t, "email", ve.Field)
		assert.Equal(t, "email", ve.Name)
	})

	t.Run("BackwardCompatWithoutEntityField", func(t *testing.T) {
		// Existing code that only sets Name and Err still works.
		err := velox.NewValidationError("age", errors.New("must be positive"))
		assert.Equal(t, "", err.Entity)
		assert.Equal(t, "", err.Field)
		assert.Equal(t, `velox: validator failed for field "age": must be positive`, err.Error())
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
		for b.Loop() {
			_ = velox.NewNotFoundError("User")
		}
	})

	b.Run("IsNotFound", func(b *testing.B) {
		err := velox.NewNotFoundError("User")
		for b.Loop() {
			_ = velox.IsNotFound(err)
		}
	})

	b.Run("NewConstraintError", func(b *testing.B) {
		for b.Loop() {
			_ = velox.NewConstraintError("unique", nil)
		}
	})

	b.Run("IsConstraintError", func(b *testing.B) {
		err := velox.NewConstraintError("unique", nil)
		for b.Loop() {
			_ = velox.IsConstraintError(err)
		}
	})

	b.Run("NewValidationError", func(b *testing.B) {
		underlying := errors.New("invalid")
		for b.Loop() {
			_ = velox.NewValidationError("field", underlying)
		}
	})
}
