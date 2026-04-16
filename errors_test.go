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

func TestNotFoundErrorWithID(t *testing.T) {
	t.Run("Error with ID", func(t *testing.T) {
		err := velox.NewNotFoundErrorWithID("User", 123)
		assert.Equal(t, "velox: User not found (id=123)", err.Error())
		assert.Equal(t, "User", err.Label())
		assert.Equal(t, 123, err.ID())
	})

	t.Run("Error with string ID", func(t *testing.T) {
		err := velox.NewNotFoundErrorWithID("Post", "abc-123")
		assert.Equal(t, "velox: Post not found (id=abc-123)", err.Error())
		assert.Equal(t, "abc-123", err.ID())
	})

	t.Run("Label and nil ID", func(t *testing.T) {
		err := velox.NewNotFoundError("Group")
		assert.Equal(t, "Group", err.Label())
		assert.Nil(t, err.ID())
	})

	t.Run("Is matches ErrNotFound", func(t *testing.T) {
		err := velox.NewNotFoundErrorWithID("User", 42)
		assert.True(t, errors.Is(err, velox.ErrNotFound))
	})
}

func TestNotSingularErrorWithCount(t *testing.T) {
	t.Run("Error with count", func(t *testing.T) {
		err := velox.NewNotSingularErrorWithCount("User", 5)
		assert.Equal(t, "velox: User not singular (got 5 results, expected 1)", err.Error())
		assert.Equal(t, "User", err.Label())
		assert.Equal(t, 5, err.Count())
	})

	t.Run("Error with count zero", func(t *testing.T) {
		err := velox.NewNotSingularErrorWithCount("Post", 0)
		assert.Equal(t, "velox: Post not singular (got 0 results, expected 1)", err.Error())
		assert.Equal(t, 0, err.Count())
	})

	t.Run("Error without count", func(t *testing.T) {
		err := velox.NewNotSingularError("Comment")
		assert.Equal(t, -1, err.Count())
	})
}

func TestQueryError(t *testing.T) {
	t.Run("Error with op", func(t *testing.T) {
		underlying := errors.New("connection refused")
		err := velox.NewQueryError("User", "select", underlying)
		assert.Equal(t, "velox: querying User (select): connection refused", err.Error())
		assert.True(t, errors.Is(err, underlying))
	})

	t.Run("Error without op", func(t *testing.T) {
		underlying := errors.New("timeout")
		err := velox.NewQueryError("Post", "", underlying)
		assert.Equal(t, "velox: querying Post: timeout", err.Error())
	})

	t.Run("IsQueryError", func(t *testing.T) {
		err := velox.NewQueryError("User", "count", errors.New("fail"))
		assert.True(t, velox.IsQueryError(err))

		wrapped := fmt.Errorf("wrapper: %w", err)
		assert.True(t, velox.IsQueryError(wrapped))

		assert.False(t, velox.IsQueryError(errors.New("other")))
		assert.False(t, velox.IsQueryError(nil))
	})
}

func TestMutationError(t *testing.T) {
	t.Run("Error", func(t *testing.T) {
		underlying := errors.New("constraint violation")
		err := velox.NewMutationError("User", "create", underlying)
		assert.Equal(t, "velox: create User: constraint violation", err.Error())
		assert.True(t, errors.Is(err, underlying))
	})

	t.Run("IsMutationError", func(t *testing.T) {
		err := velox.NewMutationError("Post", "update", errors.New("fail"))
		assert.True(t, velox.IsMutationError(err))

		wrapped := fmt.Errorf("wrapper: %w", err)
		assert.True(t, velox.IsMutationError(wrapped))

		assert.False(t, velox.IsMutationError(errors.New("other")))
		assert.False(t, velox.IsMutationError(nil))
	})
}

func TestPrivacyError(t *testing.T) {
	t.Run("Error with rule", func(t *testing.T) {
		err := velox.NewPrivacyError("User", "query", "IsOwner")
		assert.Equal(t, "velox: privacy denied query on User (rule: IsOwner)", err.Error())
	})

	t.Run("Error without rule", func(t *testing.T) {
		err := velox.NewPrivacyError("Post", "mutation", "")
		assert.Equal(t, "velox: privacy denied mutation on Post", err.Error())
	})

	t.Run("IsPrivacyError", func(t *testing.T) {
		err := velox.NewPrivacyError("User", "query", "DenyAll")
		assert.True(t, velox.IsPrivacyError(err))

		wrapped := fmt.Errorf("wrapper: %w", err)
		assert.True(t, velox.IsPrivacyError(wrapped))

		assert.False(t, velox.IsPrivacyError(errors.New("other")))
		assert.False(t, velox.IsPrivacyError(nil))
	})
}

func TestAggregateError_EmptyErrors(t *testing.T) {
	agg := &velox.AggregateError{Errors: []error{}}
	assert.Equal(t, "velox: no errors", agg.Error())
}

func TestAggregateError_SingleError(t *testing.T) {
	err := errors.New("single failure")
	agg := &velox.AggregateError{Errors: []error{err}}
	assert.Equal(t, "single failure", agg.Error())
}

func TestAggregateError_Unwrap(t *testing.T) {
	sentinel := errors.New("sentinel")
	err1 := fmt.Errorf("wrapped: %w", sentinel)
	err2 := errors.New("other")
	agg := &velox.AggregateError{Errors: []error{err1, err2}}

	// errors.Is should traverse inner errors via Unwrap() []error
	assert.True(t, errors.Is(agg, sentinel), "errors.Is should find sentinel inside AggregateError")
	assert.False(t, errors.Is(agg, errors.New("not there")))
}

func TestAggregateError_MultipleWithNilFiltering(t *testing.T) {
	err1 := errors.New("err1")
	err2 := errors.New("err2")
	err3 := errors.New("err3")
	result := velox.NewAggregateError(nil, err1, nil, err2, nil, err3)
	require.NotNil(t, result)
	assert.Contains(t, result.Error(), "multiple errors")
	assert.Contains(t, result.Error(), "err1")
	assert.Contains(t, result.Error(), "err2")
	assert.Contains(t, result.Error(), "err3")
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

	b.Run("NewAggregateError_multiple", func(b *testing.B) {
		err1 := errors.New("err1")
		err2 := errors.New("err2")
		err3 := errors.New("err3")
		for b.Loop() {
			_ = velox.NewAggregateError(err1, err2, err3)
		}
	})
}
