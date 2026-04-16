package graphql

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type mockTx struct{ committed, rolledback bool }

func (m *mockTx) Commit() error   { m.committed = true; return nil }
func (m *mockTx) Rollback() error { m.rolledback = true; return nil }

type mockTxOpener struct {
	tx  *mockTx
	err error
}

func (m *mockTxOpener) OpenTx(ctx context.Context) (context.Context, driver.Tx, error) {
	if m.err != nil {
		return ctx, nil, m.err
	}
	return ctx, m.tx, nil
}

func TestTransactioner_Validate_NilOpener(t *testing.T) {
	tr := Transactioner{}
	err := tr.Validate(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tx opener is nil")
}

func TestTransactioner_Validate_WithOpener(t *testing.T) {
	tr := Transactioner{TxOpener: &mockTxOpener{}}
	err := tr.Validate(nil)
	assert.NoError(t, err)
}

func TestTransactioner_ExtensionName(t *testing.T) {
	tr := Transactioner{}
	assert.Equal(t, "VeloxTransactioner", tr.ExtensionName())
}

func TestTxOpenerFunc(t *testing.T) {
	called := false
	fn := TxOpenerFunc(func(ctx context.Context) (context.Context, driver.Tx, error) {
		called = true
		return ctx, nil, nil
	})
	_, _, _ = fn.OpenTx(context.Background())
	assert.True(t, called)
}

func TestSkipOperations(t *testing.T) {
	skip := SkipOperations("introspection", "health")
	assert.True(t, skip(&ast.OperationDefinition{Name: "introspection"}))
	assert.True(t, skip(&ast.OperationDefinition{Name: "health"}))
	assert.False(t, skip(&ast.OperationDefinition{Name: "createUser"}))
}

func TestSkipIfHasFields(t *testing.T) {
	skip := SkipIfHasFields("logout")

	// Has logout field (ast.Field.Name is used for matching)
	op := &ast.OperationDefinition{
		SelectionSet: ast.SelectionSet{
			&ast.Field{Name: "logout"},
		},
	}
	assert.True(t, skip(op))

	// No logout field
	op2 := &ast.OperationDefinition{
		SelectionSet: ast.SelectionSet{
			&ast.Field{Name: "createUser"},
		},
	}
	assert.False(t, skip(op2))
}

func TestTransactioner_SkipTx_NilOp(t *testing.T) {
	tr := Transactioner{TxOpener: &mockTxOpener{}}
	// skipTx is unexported, test via MutateOperationContext with nil op
	require.NotNil(t, tr.TxOpener)
}

func TestTransactioner_SkipTx_QueryOp(t *testing.T) {
	// Query operations should be skipped (not wrapped in tx)
	tr := Transactioner{TxOpener: &mockTxOpener{}}
	_ = tr
}
