package integration_test

// Tests targeting uncovered generated code paths to push integration
// coverage from ~60% to 80%+. Each section matches a file + function
// from the coverage report.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	velsql "github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// =============================================================================
// client.go — AlternateSchema, Debug option, Log option
// =============================================================================

func TestClient_AlternateSchema(t *testing.T) {
	// AlternateSchema returns an option; verify it doesn't panic when applied.
	sc := integration.SchemaConfig{} // zero value
	opt := integration.AlternateSchema(sc)
	require.NotNil(t, opt)

	// Apply it to a real client — should not panic.
	drv := mustOpenDriver(t)
	client := integration.NewClient(integration.Driver(drv), opt)
	defer client.Close()

	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))
}

func TestClient_DebugOption(t *testing.T) {
	var logged bool
	drv := mustOpenDriver(t)
	client := integration.NewClient(
		integration.Driver(drv),
		integration.Debug(),
		integration.Log(func(v ...any) { logged = true }),
	)
	defer client.Close()

	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))

	// Any query should trigger the debug logger.
	_, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	assert.True(t, logged, "debug logger should have been called")
}

func TestClient_LogOption(t *testing.T) {
	var calls int
	drv := mustOpenDriver(t)
	client := integration.NewClient(
		integration.Driver(drv),
		integration.Debug(),
		integration.Log(func(v ...any) { calls++ }),
	)
	defer client.Close()

	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))
	_, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	assert.Greater(t, calls, 0, "log function should have been called at least once")
}

// =============================================================================
// tx.go — txDriver: Exec, Close, nested Tx, Commit, Rollback
// =============================================================================

func TestTxDriver_ExecAndQuery(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	// Create a user within the transaction (exercises txDriver.Exec + txDriver.Query).
	u, err := tx.User.Create().
		SetName("tx-user").
		SetEmail("tx@test.com").
		SetAge(20).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	require.NotZero(t, u.ID)

	// Query within the same transaction sees the user (exercises txDriver.Query).
	found, err := tx.User.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, found, 1)

	require.NoError(t, tx.Commit())

	all, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestTxDriver_Rollback(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	_, err = tx.User.Create().
		SetName("rollback-user").
		SetEmail("rollback@test.com").
		SetAge(20).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Rollback())

	all, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestTxDriver_NestedTx_Fails(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	// Starting a transaction from a transactional client should fail.
	txClient := tx.Client()
	_, err = txClient.Tx(ctx)
	assert.ErrorIs(t, err, integration.ErrTxStarted)

	require.NoError(t, tx.Rollback())
}

func TestTxDriver_Close_IsNoop(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	// txDriver.Close is a no-op — should return nil.
	txClient := tx.Client()
	err = txClient.Close()
	assert.NoError(t, err)

	require.NoError(t, tx.Rollback())
}

func TestTxDriver_InnerTx_ReturnsItself(t *testing.T) {
	// When internal builders call Tx() on txDriver, it returns itself.
	// This is exercised indirectly when mutations run inside a transaction.
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	// A create inside a transaction calls the txDriver.Tx() method internally.
	_, err = tx.User.Create().
		SetName("inner-tx-user").
		SetEmail("inner@test.com").
		SetAge(25).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Also exercise a delete (different mutation path, still calls txDriver.Tx internally).
	_, err = tx.User.Delete().Where(user.NameField.EQ("inner-tx-user")).Exec(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())
}

func TestTxDriver_ExecContext(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	// Exercise txDriver.ExecContext directly on the Tx (promoted from config).
	result, err := tx.ExecContext(ctx, "INSERT INTO users (name, email, age, role, created_at, updated_at) VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))", "exec-ctx", "exec@test.com", 30, "user")
	require.NoError(t, err)
	n, err := result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	require.NoError(t, tx.Commit())
}

func TestTxDriver_QueryContext(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "qctx-user", "qctx@test.com")

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	// Exercise txDriver.QueryContext directly on the Tx.
	rows, err := tx.QueryContext(ctx, "SELECT name FROM users WHERE email = ?", "qctx@test.com")
	require.NoError(t, err)
	defer rows.Close()

	require.True(t, rows.Next())
	var name string
	require.NoError(t, rows.Scan(&name))
	assert.Equal(t, "qctx-user", name)

	require.NoError(t, tx.Commit())
}

// =============================================================================
// client.go — ExecContext, QueryContext (on config, accessed via Client)
// =============================================================================

func TestClient_ExecContext(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	result, err := client.ExecContext(ctx, "INSERT INTO users (name, email, age, role, created_at, updated_at) VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))", "alice", "alice@test.com", 25, "user")
	require.NoError(t, err)
	n, err := result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

func TestClient_QueryContext(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "bob", "bob@test.com")

	rows, err := client.QueryContext(ctx, "SELECT name FROM users WHERE email = ?", "bob@test.com")
	require.NoError(t, err)
	defer rows.Close()

	require.True(t, rows.Next())
	var name string
	require.NoError(t, rows.Scan(&name))
	assert.Equal(t, "bob", name)
	assert.False(t, rows.Next())
}

// =============================================================================
// velox.go — Generic Utilities: Ptr, Deref, DerefOr, Map, Filter, First
// =============================================================================

func TestPtr(t *testing.T) {
	p := integration.Ptr(42)
	require.NotNil(t, p)
	assert.Equal(t, 42, *p)

	s := integration.Ptr("hello")
	assert.Equal(t, "hello", *s)
}

func TestDeref(t *testing.T) {
	v := 42
	assert.Equal(t, 42, integration.Deref(&v))

	var nilPtr *int
	assert.Equal(t, 0, integration.Deref(nilPtr))
}

func TestDerefOr(t *testing.T) {
	v := 42
	assert.Equal(t, 42, integration.DerefOr(&v, 99))
	assert.Equal(t, 99, integration.DerefOr[int](nil, 99))
}

func TestMap(t *testing.T) {
	doubled := integration.Map([]int{1, 2, 3}, func(n int) int { return n * 2 })
	assert.Equal(t, []int{2, 4, 6}, doubled)

	// Nil input returns nil.
	assert.Nil(t, integration.Map[int, int](nil, func(n int) int { return n }))
}

func TestFilter(t *testing.T) {
	evens := integration.Filter([]int{1, 2, 3, 4, 5}, func(n int) bool { return n%2 == 0 })
	assert.Equal(t, []int{2, 4}, evens)

	// Nil input returns nil.
	assert.Nil(t, integration.Filter[int](nil, func(int) bool { return true }))

	// Empty result.
	none := integration.Filter([]int{1, 3, 5}, func(n int) bool { return n%2 == 0 })
	assert.Empty(t, none)
}

func TestFirst(t *testing.T) {
	v, ok := integration.First([]int{1, 2, 3, 4}, func(n int) bool { return n > 2 })
	assert.True(t, ok)
	assert.Equal(t, 3, v)

	_, ok = integration.First([]int{1, 2, 3}, func(n int) bool { return n > 10 })
	assert.False(t, ok)
}

// =============================================================================
// querylanguage.go — GetSchema, ApplyFilter
// =============================================================================

func TestGetSchema(t *testing.T) {
	schema, ok := integration.GetSchema("User")
	require.True(t, ok)
	require.NotNil(t, schema)
	assert.NotEmpty(t, schema.Fields)

	_, ok = integration.GetSchema("NonExistent")
	assert.False(t, ok)
}

func TestApplyFilter_AllOperators(t *testing.T) {
	// Test that ApplyFilter with each operator produces valid SQL without panicking.
	ops := []struct {
		op    string
		value any
	}{
		{integration.OpEQ, "alice"},
		{integration.OpNEQ, "bob"},
		{integration.OpGT, "a"},
		{integration.OpGTE, "a"},
		{integration.OpLT, "z"},
		{integration.OpLTE, "z"},
		{integration.OpContains, "li"},
		{integration.OpHasPrefix, "al"},
		{integration.OpHasSuffix, "ce"},
		{integration.OpIsNull, nil},
		{integration.OpIsNotNull, nil},
	}

	for _, tc := range ops {
		t.Run(tc.op, func(t *testing.T) {
			s := velsql.Select("id", "name").From(velsql.Table("users"))
			filter := &integration.RuntimeFilter{
				Field: "name",
				Op:    tc.op,
				Value: tc.value,
			}
			integration.ApplyFilter(s, filter, "name")
			// Verify the selector can produce valid SQL.
			query, _ := s.Query()
			assert.NotEmpty(t, query)
		})
	}
}

func TestApplyFilter_UnknownOp(t *testing.T) {
	// Unknown operators are silently ignored (no-op).
	s := velsql.Select("id").From(velsql.Table("users"))
	filter := &integration.RuntimeFilter{
		Field: "name",
		Op:    "unknown_op",
		Value: "test",
	}
	integration.ApplyFilter(s, filter, "name")
	query, _ := s.Query()
	assert.NotContains(t, query, "WHERE")
}

func TestApplyFilter_Nil(t *testing.T) {
	s := velsql.Select("id").From(velsql.Table("users"))
	// Nil filter is a no-op.
	integration.ApplyFilter(s, nil, "name")
	query, _ := s.Query()
	assert.NotContains(t, query, "WHERE")
}

// =============================================================================
// velox.go — Aggregate: Max, Mean, Min, Sum with string field type
// =============================================================================

func TestAggregate_MaxStringField(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "aaa", "agg1@test.com")
	createUser(t, client, "zzz", "agg2@test.com")

	var v []struct {
		Max string `json:"max"`
	}
	err := client.User.Query().
		Aggregate(integration.Max(user.FieldName)).
		Scan(ctx, &v)
	require.NoError(t, err)
	require.Len(t, v, 1)
	assert.Equal(t, "zzz", v[0].Max)
}

func TestAggregate_MinStringField(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "aaa", "min1@test.com")
	createUser(t, client, "zzz", "min2@test.com")

	var v []struct {
		Min string `json:"min"`
	}
	err := client.User.Query().
		Aggregate(integration.Min(user.FieldName)).
		Scan(ctx, &v)
	require.NoError(t, err)
	require.Len(t, v, 1)
	assert.Equal(t, "aaa", v[0].Min)
}

func TestAggregate_SumAge(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "sum1", "sum1@test.com")
	createUser(t, client, "sum2", "sum2@test.com")

	var v []struct {
		Sum int `json:"sum"`
	}
	err := client.User.Query().
		Aggregate(integration.Sum(user.FieldAge)).
		Scan(ctx, &v)
	require.NoError(t, err)
	require.Len(t, v, 1)
	assert.Equal(t, 60, v[0].Sum)
}

func TestAggregate_MeanAge(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "mean1", "mean1@test.com")
	createUser(t, client, "mean2", "mean2@test.com")

	// Mean uses sql.Avg which produces AVG(...) as column name.
	// Use As() to alias the output column for struct scanning.
	var v []struct {
		Avg float64 `json:"avg"`
	}
	err := client.User.Query().
		Aggregate(integration.As(integration.Mean(user.FieldAge), "avg")).
		Scan(ctx, &v)
	require.NoError(t, err)
	require.Len(t, v, 1)
	assert.Equal(t, float64(30), v[0].Avg)
}

// =============================================================================
// velox.go — Asc/Desc ordering (exercises remaining branches)
// =============================================================================

func TestAscDesc_Ordering(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "charlie", "c@test.com")
	createUser(t, client, "alice", "a@test.com")
	createUser(t, client, "bob", "b@test.com")

	// Asc
	asc, err := client.User.Query().Order(integration.Asc(user.FieldName)).All(ctx)
	require.NoError(t, err)
	require.Len(t, asc, 3)
	assert.Equal(t, "alice", asc[0].Name)
	assert.Equal(t, "bob", asc[1].Name)
	assert.Equal(t, "charlie", asc[2].Name)

	// Desc
	desc, err := client.User.Query().Order(integration.Desc(user.FieldName)).All(ctx)
	require.NoError(t, err)
	require.Len(t, desc, 3)
	assert.Equal(t, "charlie", desc[0].Name)
	assert.Equal(t, "bob", desc[1].Name)
	assert.Equal(t, "alice", desc[2].Name)
}

// =============================================================================
// tx.go — WithTx helper
// =============================================================================

func TestWithTx_Success(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	err := integration.WithTx(ctx, client, func(tx *integration.Tx) error {
		_, err := tx.User.Create().
			SetName("withtx-user").
			SetEmail("withtx@test.com").
			SetAge(25).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		return err
	})
	require.NoError(t, err)

	all, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestWithTx_Error(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	err := integration.WithTx(ctx, client, func(tx *integration.Tx) error {
		_, err := tx.User.Create().
			SetName("withtx-err").
			SetEmail("withtx-err@test.com").
			SetAge(25).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
		return assert.AnError
	})
	require.Error(t, err)

	all, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestWithTx_Panic(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	assert.Panics(t, func() {
		_ = integration.WithTx(ctx, client, func(tx *integration.Tx) error {
			panic("test panic")
		})
	})
}

// =============================================================================
// tx.go — BeginTx with options
// =============================================================================

func TestBeginTx_WithOptions(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.BeginTx(ctx, &sql.TxOptions{})
	require.NoError(t, err)

	_, err = tx.User.Create().
		SetName("begintx-user").
		SetEmail("begintx@test.com").
		SetAge(20).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

func TestBeginTx_FromTxClient_Fails(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	txClient := tx.Client()
	_, err = txClient.BeginTx(ctx, nil)
	assert.ErrorIs(t, err, integration.ErrTxStarted)

	require.NoError(t, tx.Rollback())
}

// =============================================================================
// Helpers
// =============================================================================

func mustOpenDriver(t *testing.T) dialect.Driver {
	t.Helper()
	drv, err := velsql.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { drv.Close() })
	return drv
}
