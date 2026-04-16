package integration_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/query"
	"github.com/syssam/velox/tests/integration/user"
)

// TestInterceptor_ChainOrder_ThreeDeep pins that the interceptor
// chain stays correctly nested when more than two interceptors are
// registered. The pre-existing TestInterceptor_ChainOrder only
// covered N=2, which is the trivial case — the recursive nesting
// logic kicks in at N≥3. Order should be registration-outer to
// registration-inner on the way down, reversed on the way up.
func TestInterceptor_ChainOrder_ThreeDeep(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "Alice", "alice@test.com")

	var order []string
	mk := func(name string) integration.Interceptor {
		return integration.InterceptFunc(func(next integration.Querier) integration.Querier {
			return integration.QuerierFunc(func(ctx context.Context, q integration.Query) (integration.Value, error) {
				order = append(order, name+"-before")
				v, err := next.Query(ctx, q)
				order = append(order, name+"-after")
				return v, err
			})
		})
	}
	client.Intercept(mk("outer"))
	client.Intercept(mk("middle"))
	client.Intercept(mk("inner"))

	_, err := client.User.Query().All(ctx)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"outer-before", "middle-before", "inner-before",
		"inner-after", "middle-after", "outer-after",
	}, order,
		"three-deep chain must stay properly nested: outer wraps middle wraps inner on the way down, reversed on the way up")
}

// TestInterceptor_ModifiesQuery verifies an interceptor can reach
// into the concrete query builder via type assertion and add a
// predicate (or other modifier) that the downstream execution
// honors. This is the velox analog of Ent's query interceptors
// injecting soft-delete / tenant filters. velox.Query is `any`,
// so type assertion is how you get at the typed builder.
func TestInterceptor_ModifiesQuery(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Bob", "bob@example.com")
	createUser(t, client, "Charlie", "charlie@example.com")

	// Interceptor that type-asserts to *query.UserQuery and injects
	// a Where(NameField == "Alice") before forwarding. The inner
	// query should then return only Alice.
	client.Intercept(integration.InterceptFunc(func(next integration.Querier) integration.Querier {
		return integration.QuerierFunc(func(ctx context.Context, q integration.Query) (integration.Value, error) {
			if uq, ok := q.(*query.UserQuery); ok {
				uq.Where(user.NameField.EQ("Alice"))
			}
			return next.Query(ctx, q)
		})
	}))

	users, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1, "interceptor's Where filter should have reduced 3 users to 1")
	assert.Equal(t, "Alice", users[0].Name)
}

// TestInterceptor_QueryWithModifierFires pins that a query with a
// raw SQL modifier (q.Modify(func(*sql.Selector))) still runs
// through the interceptor chain. The adjacent-paths audit cleared
// this by code-reading (interceptors fire at All()'s entry,
// modifiers are applied later during buildSelector), but no test
// pinned it. If a future refactor hoists modifier application
// ahead of the interceptor chain — or swaps the execution order
// in a way that bypasses interceptors when a modifier is present
// — this test fails loudly.
func TestInterceptor_QueryWithModifierFires(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Bob", "bob@example.com")
	createUser(t, client, "Charlie", "charlie@example.com")

	var calls atomic.Int32
	client.Intercept(integration.InterceptFunc(func(next integration.Querier) integration.Querier {
		return integration.QuerierFunc(func(ctx context.Context, q integration.Query) (integration.Value, error) {
			calls.Add(1)
			return next.Query(ctx, q)
		})
	}))

	// Query with a raw SQL modifier that narrows to names starting
	// with A or B. Both the interceptor and the modifier must be
	// honored: interceptor fires (at least 1 call), modifier
	// filters out Charlie.
	users, err := client.User.Query().
		Modify(func(s *sql.Selector) {
			s.Where(sql.Or(
				sql.Like(s.C(user.FieldName), "A%"),
				sql.Like(s.C(user.FieldName), "B%"),
			))
		}).
		All(ctx)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, calls.Load(), int32(1),
		"interceptor must fire on .Modify().All()")
	require.Len(t, users, 2, "Modify predicate should have filtered Charlie out")
	names := []string{users[0].Name, users[1].Name}
	assert.Contains(t, names, "Alice")
	assert.Contains(t, names, "Bob")
}

// TestInterceptor_ErrorInTx_TriggersRollback verifies the full
// composition: an explicit caller tx + a query that goes through
// an interceptor that errors out + caller's Rollback. The caller
// tx must cleanly undo any side-effects written before the erroring
// query. This pins the interaction between tx lifecycle and
// interceptor error propagation.
func TestInterceptor_ErrorInTx_TriggersRollback(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	sentinel := errors.New("interceptor aborted in tx")

	// The interceptor flips to "error mode" only after a setup flag,
	// so we can write a row first without tripping the error, then
	// enable the error mode and observe the query failing.
	var erroring atomic.Bool
	client.Intercept(integration.InterceptFunc(func(next integration.Querier) integration.Querier {
		return integration.QuerierFunc(func(ctx context.Context, q integration.Query) (integration.Value, error) {
			if erroring.Load() {
				return nil, sentinel
			}
			return next.Query(ctx, q)
		})
	}))

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	// Write a row inside the tx.
	_, err = tx.User.Create().
		SetName("InTx").
		SetEmail("intx@test.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Enable the interceptor error mode and fire a query inside the tx.
	erroring.Store(true)
	_, qErr := tx.User.Query().All(ctx)
	require.Error(t, qErr)
	assert.ErrorIs(t, qErr, sentinel,
		"interceptor error must propagate out of tx-scoped query")

	// Caller rollback must undo the written row.
	require.NoError(t, tx.Rollback())

	// Turn off the interceptor side-effect for the verification query.
	erroring.Store(false)
	count, err := client.User.Query().Where(user.NameField.EQ("InTx")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count,
		"tx rollback after an interceptor error must undo rows written earlier in the same tx")
}
