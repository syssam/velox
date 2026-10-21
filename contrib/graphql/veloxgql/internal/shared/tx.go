package shared

import (
	"context"

	"github.com/syssam/velox2"
)

// operationTxKey is the context key of the transaction's DB the Extension
// opened for a mutation operation.
type operationTxKey struct{}

// WithOperationTx returns ctx marked as running in tx, the transaction's DB
// the Extension opened for the mutation operation of ctx.
func WithOperationTx(ctx context.Context, tx *velox.DB) context.Context {
	return context.WithValue(ctx, operationTxKey{}, tx)
}

// OperationTx returns the transaction's DB WithOperationTx marked ctx with;
// nil when ctx has none.
func OperationTx(ctx context.Context) *velox.DB {
	tx, _ := ctx.Value(operationTxKey{}).(*velox.DB)
	return tx
}
