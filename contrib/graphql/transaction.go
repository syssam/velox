package graphql

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// TxOpener represents types that can open transactions.
// Implement this interface on your ORM client to use the Transactioner middleware.
type TxOpener interface {
	OpenTx(ctx context.Context) (context.Context, driver.Tx, error)
}

// TxOpenerFunc is an adapter to allow the use of ordinary functions as TxOpener.
type TxOpenerFunc func(ctx context.Context) (context.Context, driver.Tx, error)

// OpenTx returns f(ctx).
func (f TxOpenerFunc) OpenTx(ctx context.Context) (context.Context, driver.Tx, error) {
	return f(ctx)
}

// Transactioner is a gqlgen middleware that wraps GraphQL mutations in database
// transactions. It serializes field resolvers during mutations to prevent
// concurrent access, and automatically rolls back on errors or panics.
//
// This is equivalent to Ent's entgql.Transactioner.
//
// Example:
//
//	srv := handler.NewDefaultServer(generated.NewExecutableSchema(resolver))
//	srv.Use(graphql.Transactioner{TxOpener: client})
type Transactioner struct {
	TxOpener
	SkipTxFunc
}

// SkipTxFunc allows skipping operations from running under a transaction.
type SkipTxFunc func(*ast.OperationDefinition) bool

// SkipOperations skips the given operation names from running under a transaction.
//
// Example:
//
//	graphql.Transactioner{
//	    TxOpener:   client,
//	    SkipTxFunc: graphql.SkipOperations("introspection"),
//	}
func SkipOperations(names ...string) SkipTxFunc {
	return func(op *ast.OperationDefinition) bool {
		return slices.Contains(names, op.Name)
	}
}

// SkipIfHasFields skips the operation if it contains a mutation field with one of the given names.
//
// Example:
//
//	graphql.Transactioner{
//	    TxOpener:   client,
//	    SkipTxFunc: graphql.SkipIfHasFields("logout"),
//	}
func SkipIfHasFields(names ...string) SkipTxFunc {
	return func(op *ast.OperationDefinition) bool {
		return slices.ContainsFunc(op.SelectionSet, func(s ast.Selection) bool {
			f, ok := s.(*ast.Field)
			return ok && slices.Contains(names, f.Name)
		})
	}
}

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
	graphql.ResponseInterceptor
} = Transactioner{}

// ExtensionName returns the extension name.
func (Transactioner) ExtensionName() string {
	return "VeloxTransactioner"
}

// Validate is called when adding an extension to the server.
func (t Transactioner) Validate(graphql.ExecutableSchema) error {
	if t.TxOpener == nil {
		return errors.New("graphql: tx opener is nil")
	}
	return nil
}

// MutateOperationContext serializes field resolvers during mutations
// to prevent concurrent access within a single transaction.
func (t Transactioner) MutateOperationContext(_ context.Context, oc *graphql.OperationContext) *gqlerror.Error {
	if !t.skipTx(oc.Operation) {
		previous := oc.ResolverMiddleware
		var mu sync.Mutex
		oc.ResolverMiddleware = func(ctx context.Context, next graphql.Resolver) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			return previous(ctx, next)
		}
	}
	return nil
}

// InterceptResponse runs graphql mutations under a transaction.
// It automatically commits on success and rolls back on errors or panics.
func (t Transactioner) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	if t.skipTx(graphql.GetOperationContext(ctx).Operation) {
		return next(ctx)
	}
	txCtx, tx, err := t.OpenTx(ctx)
	if err != nil {
		return graphql.ErrorResponse(ctx,
			"cannot create transaction: %s", err.Error(),
		)
	}
	ctx = txCtx

	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		}
	}()
	rsp := next(ctx)
	if len(rsp.Errors) > 0 {
		_ = tx.Rollback()
		return &graphql.Response{
			Errors: rsp.Errors,
		}
	}
	if err := tx.Commit(); err != nil {
		return graphql.ErrorResponse(ctx,
			"cannot commit transaction: %s", err.Error(),
		)
	}
	return rsp
}

func (t Transactioner) skipTx(op *ast.OperationDefinition) bool {
	return op == nil || op.Operation != ast.Mutation || (t.SkipTxFunc != nil && t.SkipTxFunc(op))
}
