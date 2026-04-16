package integration_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
)

// TestInterceptor_SharedPointer_VisibleAfterConstruction pins SP-2's
// behavior change: a query constructed BEFORE client.User.Intercept(...)
// must still see the new interceptor at execution time, because the
// query holds a shared pointer to the central *entity.InterceptorStore
// rather than a per-query slice copy.
//
// Pre-SP-2 this test would FAIL — the query would see only the
// interceptors registered at the moment client.User.Query() was
// called, not interceptors added later.
//
// This test is the regression contract for the shared-pointer
// semantics described in docs/migration.md (SP-2).
func TestInterceptor_SharedPointer_VisibleAfterConstruction(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "alice", "alice@x")

	// Construct the query BEFORE registering any interceptor.
	q := client.User.Query()

	var fired atomic.Int32
	client.User.Intercept(integration.InterceptFunc(func(next integration.Querier) integration.Querier {
		return integration.QuerierFunc(func(ctx context.Context, qq integration.Query) (integration.Value, error) {
			fired.Add(1)
			return next.Query(ctx, qq)
		})
	}))

	_, err := q.All(ctx)
	require.NoError(t, err)
	assert.Equal(t, int32(1), fired.Load(),
		"shared-pointer Intercept-after-construction should fire once")
}

// TestInterceptor_SharedPointer_VisibleAcrossExistingQueries reinforces
// the semantics with N pre-constructed queries: a single Intercept call
// after construction fires for ALL of them, not just future queries.
func TestInterceptor_SharedPointer_VisibleAcrossExistingQueries(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "alice", "alice@x")

	queries := []entity.UserQuerier{
		client.User.Query(),
		client.User.Query(),
		client.User.Query(),
	}

	var fired atomic.Int32
	client.User.Intercept(integration.InterceptFunc(func(next integration.Querier) integration.Querier {
		return integration.QuerierFunc(func(ctx context.Context, qq integration.Query) (integration.Value, error) {
			fired.Add(1)
			return next.Query(ctx, qq)
		})
	}))

	for i, q := range queries {
		if _, err := q.All(ctx); err != nil {
			t.Fatalf("q%d: %v", i+1, err)
		}
	}

	assert.Equal(t, int32(3), fired.Load(),
		"shared-pointer late-Intercept should fire for every pre-constructed query")
}
