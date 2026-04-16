package mixin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/mixin"
)

// mockMutation is a minimal velox.Mutation implementation for testing AuditHook.
// It tracks which fields were set via SetField.
type mockMutation struct {
	op        velox.Op
	setFields map[string]velox.Value
}

func newMockMutation(op velox.Op) *mockMutation {
	return &mockMutation{op: op, setFields: make(map[string]velox.Value)}
}

func (m *mockMutation) Op() velox.Op { return m.op }
func (m *mockMutation) Type() string { return "Mock" }

func (m *mockMutation) SetField(name string, value velox.Value) error {
	m.setFields[name] = value
	return nil
}

func (m *mockMutation) Fields() []string                        { return nil }
func (m *mockMutation) Field(_ string) (velox.Value, bool)      { return nil, false }
func (m *mockMutation) AddedFields() []string                   { return nil }
func (m *mockMutation) AddedField(_ string) (velox.Value, bool) { return nil, false }
func (m *mockMutation) AddField(_ string, _ velox.Value) error  { return nil }
func (m *mockMutation) ClearedFields() []string                 { return nil }
func (m *mockMutation) FieldCleared(_ string) bool              { return false }
func (m *mockMutation) ClearField(_ string) error               { return nil }
func (m *mockMutation) ResetField(_ string) error               { return nil }
func (m *mockMutation) AddedEdges() []string                    { return nil }
func (m *mockMutation) AddedIDs(_ string) []velox.Value         { return nil }
func (m *mockMutation) RemovedEdges() []string                  { return nil }
func (m *mockMutation) RemovedIDs(_ string) []velox.Value       { return nil }
func (m *mockMutation) ClearedEdges() []string                  { return nil }
func (m *mockMutation) EdgeCleared(_ string) bool               { return false }
func (m *mockMutation) ClearEdge(_ string) error                { return nil }
func (m *mockMutation) ResetEdge(_ string) error                { return nil }
func (m *mockMutation) OldField(_ context.Context, _ string) (velox.Value, error) {
	return nil, errors.New("not supported")
}

// compile-time check.
var _ velox.Mutation = (*mockMutation)(nil)

// captureHook is a terminal hook that captures the mutation passed to it.
func captureHook(captured **mockMutation) velox.Mutator {
	return velox.MutateFunc(func(_ context.Context, m velox.Mutation) (velox.Value, error) {
		*captured = m.(*mockMutation)
		return nil, nil
	})
}

func TestAuditHook_Create_SetsCreatedByAndUpdatedBy(t *testing.T) {
	const actor = "user-123"

	mut := newMockMutation(velox.OpCreate)
	var passed *mockMutation

	hook := mixin.AuditHook(func(_ context.Context) string { return actor })
	next := hook(captureHook(&passed))

	_, err := next.Mutate(context.Background(), mut)
	require.NoError(t, err)

	assert.Equal(t, actor, passed.setFields["created_by"], "created_by should be set on OpCreate")
	assert.Equal(t, actor, passed.setFields["updated_by"], "updated_by should be set on OpCreate")
}

func TestAuditHook_Update_SetsOnlyUpdatedBy(t *testing.T) {
	const actor = "user-456"

	mut := newMockMutation(velox.OpUpdate)
	var passed *mockMutation

	hook := mixin.AuditHook(func(_ context.Context) string { return actor })
	next := hook(captureHook(&passed))

	_, err := next.Mutate(context.Background(), mut)
	require.NoError(t, err)

	_, hasCreatedBy := passed.setFields["created_by"]
	assert.False(t, hasCreatedBy, "created_by should NOT be set on OpUpdate")
	assert.Equal(t, actor, passed.setFields["updated_by"], "updated_by should be set on OpUpdate")
}

func TestAuditHook_UpdateOne_SetsOnlyUpdatedBy(t *testing.T) {
	const actor = "user-789"

	mut := newMockMutation(velox.OpUpdateOne)
	var passed *mockMutation

	hook := mixin.AuditHook(func(_ context.Context) string { return actor })
	next := hook(captureHook(&passed))

	_, err := next.Mutate(context.Background(), mut)
	require.NoError(t, err)

	_, hasCreatedBy := passed.setFields["created_by"]
	assert.False(t, hasCreatedBy, "created_by should NOT be set on OpUpdateOne")
	assert.Equal(t, actor, passed.setFields["updated_by"], "updated_by should be set on OpUpdateOne")
}

func TestAuditHook_EmptyActor_SkipsSettingFields(t *testing.T) {
	mut := newMockMutation(velox.OpCreate)
	var passed *mockMutation

	hook := mixin.AuditHook(func(_ context.Context) string { return "" })
	next := hook(captureHook(&passed))

	_, err := next.Mutate(context.Background(), mut)
	require.NoError(t, err)

	require.NotNil(t, passed, "next mutator should still be called")
	assert.Empty(t, passed.setFields, "no fields should be set when actor is empty")
}

func TestAuditHook_ActorFromContext(t *testing.T) {
	type ctxKey struct{}
	actorFromCtx := func(ctx context.Context) string {
		if v, ok := ctx.Value(ctxKey{}).(string); ok {
			return v
		}
		return ""
	}

	ctx := context.WithValue(context.Background(), ctxKey{}, "ctx-actor")
	mut := newMockMutation(velox.OpCreate)
	var passed *mockMutation

	hook := mixin.AuditHook(actorFromCtx)
	next := hook(captureHook(&passed))

	_, err := next.Mutate(ctx, mut)
	require.NoError(t, err)

	assert.Equal(t, "ctx-actor", passed.setFields["created_by"])
	assert.Equal(t, "ctx-actor", passed.setFields["updated_by"])
}
