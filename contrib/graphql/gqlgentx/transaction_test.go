package gqlgentx

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
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

// TestTransactioner_SkipTx pins which operations run outside a transaction:
// anything that is not a mutation, and mutations the SkipTxFunc rejects.
func TestTransactioner_SkipTx(t *testing.T) {
	mutation := func(name string) *ast.OperationDefinition {
		return &ast.OperationDefinition{Operation: ast.Mutation, Name: name}
	}
	tests := []struct {
		name string
		skip SkipTxFunc
		op   *ast.OperationDefinition
		want bool
	}{
		{"nil operation", nil, nil, true},
		{"query", nil, &ast.OperationDefinition{Operation: ast.Query}, true},
		{"subscription", nil, &ast.OperationDefinition{Operation: ast.Subscription}, true},
		{"mutation", nil, mutation("createUser"), false},
		{"mutation skipped by SkipTxFunc", SkipOperations("logout"), mutation("logout"), true},
		{"mutation not matched by SkipTxFunc", SkipOperations("logout"), mutation("createUser"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := Transactioner{TxOpener: &mockTxOpener{}, SkipTxFunc: tt.skip}
			assert.Equal(t, tt.want, tr.skipTx(tt.op))
		})
	}
}

// TestTransactioner_InterceptResponse_NoOperationContext pins that the
// response interceptor tolerates a context without a gqlgen operation
// (custom transports, tests, non-gqlgen callers): it must pass the request
// through instead of panicking in GetOperationContext. Ports ent/contrib#630.
func TestTransactioner_InterceptResponse_NoOperationContext(t *testing.T) {
	tr := Transactioner{TxOpener: TxOpenerFunc(func(ctx context.Context) (context.Context, driver.Tx, error) {
		t.Fatal("OpenTx must not be called without an operation context")
		return nil, nil, nil
	})}
	called := false
	resp := tr.InterceptResponse(context.Background(), func(ctx context.Context) *graphql.Response {
		called = true
		return &graphql.Response{}
	})
	if !called || resp == nil {
		t.Fatalf("InterceptResponse must delegate to next without an operation context (called=%v, resp=%v)", called, resp)
	}
}

type txKey struct{}

// recordingTx records what the middleware did with the transaction.
type recordingTx struct {
	commits, rollbacks int
	commitErr          error
}

func (r *recordingTx) Commit() error   { r.commits++; return r.commitErr }
func (r *recordingTx) Rollback() error { r.rollbacks++; return nil }

func mutationCtx() context.Context {
	return graphql.WithOperationContext(context.Background(), &graphql.OperationContext{
		Operation: &ast.OperationDefinition{Operation: ast.Mutation, Name: "m"},
	})
}

func opener(tx *recordingTx, err error) TxOpener {
	return TxOpenerFunc(func(ctx context.Context) (context.Context, driver.Tx, error) {
		if err != nil {
			return ctx, nil, err
		}
		return context.WithValue(ctx, txKey{}, tx), tx, nil
	})
}

// A mutation's resolvers run inside the transaction, and what they wrote is
// committed only when the response has no errors: an error anywhere, a
// panic, or a failed commit leaves nothing behind.
func TestTransactioner_CommitsOnlyAnErrorFreeMutation(t *testing.T) {
	t.Run("success commits, inside the transaction", func(t *testing.T) {
		tx := &recordingTx{}
		rsp := Transactioner{TxOpener: opener(tx, nil)}.InterceptResponse(mutationCtx(), func(ctx context.Context) *graphql.Response {
			if ctx.Value(txKey{}) != tx {
				t.Error("resolvers ran outside the transaction")
			}
			return &graphql.Response{Data: []byte(`{"m":1}`)}
		})
		assert.Equal(t, `{"m":1}`, string(rsp.Data))
		assert.Equal(t, 1, tx.commits)
		assert.Equal(t, 0, tx.rollbacks)
	})
	t.Run("an error rolls back and drops the data", func(t *testing.T) {
		tx := &recordingTx{}
		rsp := Transactioner{TxOpener: opener(tx, nil)}.InterceptResponse(mutationCtx(), func(context.Context) *graphql.Response {
			return &graphql.Response{Data: []byte(`{"m":1}`), Errors: gqlerror.List{{Message: "boom"}}}
		})
		assert.Nil(t, rsp.Data, "data written before the rollback must not be reported as done")
		assert.Len(t, rsp.Errors, 1)
		assert.Equal(t, 0, tx.commits)
		assert.Equal(t, 1, tx.rollbacks)
	})
	t.Run("a panic rolls back and still panics", func(t *testing.T) {
		tx := &recordingTx{}
		assert.PanicsWithValue(t, "resolver", func() {
			Transactioner{TxOpener: opener(tx, nil)}.InterceptResponse(mutationCtx(), func(context.Context) *graphql.Response {
				panic("resolver")
			})
		})
		assert.Equal(t, 0, tx.commits)
		assert.Equal(t, 1, tx.rollbacks)
	})
	t.Run("a failed commit is an error", func(t *testing.T) {
		tx := &recordingTx{commitErr: errors.New("serialization failure")}
		rsp := Transactioner{TxOpener: opener(tx, nil)}.InterceptResponse(mutationCtx(), func(context.Context) *graphql.Response {
			return &graphql.Response{Data: []byte(`{"m":1}`)}
		})
		assert.Nil(t, rsp.Data)
		assert.Contains(t, rsp.Errors.Error(), "serialization failure")
	})
	t.Run("a transaction that cannot open runs nothing", func(t *testing.T) {
		rsp := Transactioner{TxOpener: opener(nil, errors.New("pool exhausted"))}.InterceptResponse(mutationCtx(), func(context.Context) *graphql.Response {
			t.Error("resolvers ran without a transaction")
			return &graphql.Response{}
		})
		assert.Contains(t, rsp.Errors.Error(), "pool exhausted")
	})
	t.Run("a query opens none", func(t *testing.T) {
		ctx := graphql.WithOperationContext(context.Background(), &graphql.OperationContext{
			Operation: &ast.OperationDefinition{Operation: ast.Query},
		})
		Transactioner{TxOpener: opener(nil, errors.New("must not open"))}.InterceptResponse(ctx, func(context.Context) *graphql.Response {
			return &graphql.Response{}
		})
	})
}

// One transaction is one connection, and a database driver's connection is
// not safe for concurrent use: gqlgen resolves sibling fields concurrently,
// so a mutation's resolvers are serialized. A query's are left alone.
func TestTransactioner_SerializesAMutationsResolvers(t *testing.T) {
	run := func(op ast.Operation) int32 {
		oc := &graphql.OperationContext{
			Operation:          &ast.OperationDefinition{Operation: op},
			ResolverMiddleware: func(ctx context.Context, next graphql.Resolver) (any, error) { return next(ctx) },
		}
		Transactioner{TxOpener: opener(&recordingTx{}, nil)}.MutateOperationContext(context.Background(), oc)
		var active, peak atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				_, _ = oc.ResolverMiddleware(context.Background(), func(context.Context) (any, error) {
					n := active.Add(1)
					for {
						p := peak.Load()
						if n <= p || peak.CompareAndSwap(p, n) {
							break
						}
					}
					time.Sleep(time.Millisecond)
					active.Add(-1)
					return nil, nil
				})
			})
		}
		wg.Wait()
		return peak.Load()
	}
	assert.Equal(t, int32(1), run(ast.Mutation), "a mutation's resolvers overlapped")
	assert.Greater(t, run(ast.Query), int32(1), "a query's resolvers were serialized")
}
