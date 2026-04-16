package velox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
)

// hookTestMutation implements velox.Mutation for hook testing.
type hookTestMutation struct {
	op  velox.Op
	typ string
}

func (m *hookTestMutation) Op() velox.Op                            { return m.op }
func (m *hookTestMutation) Type() string                            { return m.typ }
func (m *hookTestMutation) Fields() []string                        { return nil }
func (m *hookTestMutation) Field(_ string) (velox.Value, bool)      { return nil, false }
func (m *hookTestMutation) SetField(_ string, _ velox.Value) error  { return nil }
func (m *hookTestMutation) AddedFields() []string                   { return nil }
func (m *hookTestMutation) AddedField(_ string) (velox.Value, bool) { return nil, false }
func (m *hookTestMutation) AddField(_ string, _ velox.Value) error  { return nil }
func (m *hookTestMutation) ClearedFields() []string                 { return nil }
func (m *hookTestMutation) FieldCleared(_ string) bool              { return false }
func (m *hookTestMutation) ClearField(_ string) error               { return nil }
func (m *hookTestMutation) ResetField(_ string) error               { return nil }
func (m *hookTestMutation) OldField(_ context.Context, _ string) (velox.Value, error) {
	return nil, nil
}
func (m *hookTestMutation) AddedEdges() []string              { return nil }
func (m *hookTestMutation) AddedIDs(_ string) []velox.Value   { return nil }
func (m *hookTestMutation) RemovedEdges() []string            { return nil }
func (m *hookTestMutation) RemovedIDs(_ string) []velox.Value { return nil }
func (m *hookTestMutation) ClearedEdges() []string            { return nil }
func (m *hookTestMutation) EdgeCleared(_ string) bool         { return false }
func (m *hookTestMutation) ClearEdge(_ string) error          { return nil }
func (m *hookTestMutation) ResetEdge(_ string) error          { return nil }

// TestHookExecutionOrder verifies that hooks wrap in LIFO order: the last hook
// registered runs outermost, so hook2 wraps hook1 which wraps the mutator.
func TestHookExecutionOrder(t *testing.T) {
	var order []string

	// hook1 records before/after around calling next.
	hook1 := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			order = append(order, "hook1-before")
			v, err := next.Mutate(ctx, m)
			order = append(order, "hook1-after")
			return v, err
		})
	}

	// hook2 records before/after around calling next.
	hook2 := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			order = append(order, "hook2-before")
			v, err := next.Mutate(ctx, m)
			order = append(order, "hook2-after")
			return v, err
		})
	}

	// The terminal mutator records its execution.
	terminal := velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
		order = append(order, "mutator")
		return nil, nil
	})

	// Apply hooks in LIFO order: iterating hooks=[hook1,hook2] in reverse means
	// hook2 wraps terminal first, then hook1 wraps that result — making hook1 the
	// outermost layer. Execution order: hook1-before → hook2-before → mutator →
	// hook2-after → hook1-after.
	hooks := []velox.Hook{hook1, hook2}
	mut := velox.Mutator(terminal)
	for i := len(hooks) - 1; i >= 0; i-- {
		mut = hooks[i](mut)
	}

	mutation := &hookTestMutation{op: velox.OpCreate, typ: "User"}
	_, err := mut.Mutate(context.Background(), mutation)
	require.NoError(t, err)

	expected := []string{"hook1-before", "hook2-before", "mutator", "hook2-after", "hook1-after"}
	assert.Equal(t, expected, order)
}

// TestHookErrorPropagation verifies that when an inner hook returns an error
// without calling next, the outer hook sees the error and the mutator is never reached.
func TestHookErrorPropagation(t *testing.T) {
	mutatorCalled := false

	// Terminal mutator — should never be called.
	terminal := velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
		mutatorCalled = true
		return nil, nil
	})

	// Inner hook that short-circuits with an error.
	errHook := func(_ velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			return nil, assert.AnError
		})
	}

	outerSawError := false

	// Outer hook that observes the error returned by the inner chain.
	outerHook := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			v, err := next.Mutate(ctx, m)
			if err != nil {
				outerSawError = true
			}
			return v, err
		})
	}

	// Build chain: outerHook wraps errHook wraps terminal.
	mut := velox.Mutator(terminal)
	mut = errHook(mut)
	mut = outerHook(mut)

	mutation := &hookTestMutation{op: velox.OpCreate, typ: "Post"}
	_, err := mut.Mutate(context.Background(), mutation)

	assert.True(t, errors.Is(err, assert.AnError))
	assert.True(t, outerSawError, "outer hook should have observed the error")
	assert.False(t, mutatorCalled, "terminal mutator must not be called when hook short-circuits")
}

// TestHookContextPropagation verifies that a hook can enrich the context and
// the modified context is received by the downstream mutator.
type hookCtxKey struct{}

func TestHookContextPropagation(t *testing.T) {
	var receivedValue any

	// Terminal mutator reads the context value set by the hook.
	terminal := velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
		receivedValue = ctx.Value(hookCtxKey{})
		return nil, nil
	})

	// Hook that injects a value into the context before calling next.
	ctxHook := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			enriched := context.WithValue(ctx, hookCtxKey{}, "injected-value")
			return next.Mutate(enriched, m)
		})
	}

	mut := ctxHook(terminal)

	mutation := &hookTestMutation{op: velox.OpUpdate, typ: "User"}
	_, err := mut.Mutate(context.Background(), mutation)
	require.NoError(t, err)

	assert.Equal(t, "injected-value", receivedValue)
}
