package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// TestAggregate_PureSumOverQuery verifies Aggregate() without GroupBy emits
// a pure aggregate SELECT (no row columns) and scans into a scalar via IntX.
// This covers the runtime.QuerySelect path added to replace QueryScan for
// aggregate-only queries.
func TestAggregate_PureSumOverQuery(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	for i, age := range []int{10, 20, 30} {
		_, err := client.User.Create().
			SetName("user" + string(rune('A'+i))).
			SetEmail("u" + string(rune('a'+i)) + "@test.com").
			SetAge(age).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	sum, err := client.User.Query().
		Aggregate(integration.Sum(user.FieldAge)).
		Int(ctx)
	require.NoError(t, err)
	assert.Equal(t, 60, sum)
}

// TestAggregate_PureMinMaxMean verifies Min/Max/Mean without GroupBy scan
// into a struct with matching JSON tags.
func TestAggregate_PureMinMaxMean(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	for i, age := range []int{10, 20, 30} {
		_, err := client.User.Create().
			SetName("user" + string(rune('A'+i))).
			SetEmail("u" + string(rune('a'+i)) + "@test.com").
			SetAge(age).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	var out []struct {
		Min  int     `json:"min"`
		Max  int     `json:"max"`
		Mean float64 `json:"mean"`
	}
	err := client.User.Query().
		Aggregate(
			integration.As(integration.Min(user.FieldAge), "min"),
			integration.As(integration.Max(user.FieldAge), "max"),
			integration.As(integration.Mean(user.FieldAge), "mean"),
		).
		Scan(ctx, &out)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, 10, out[0].Min)
	assert.Equal(t, 30, out[0].Max)
	assert.InDelta(t, 20.0, out[0].Mean, 0.01)
}

// TestAggregate_PureCountWithPredicate verifies Aggregate() composes with
// Where() predicates to count a filtered subset.
func TestAggregate_PureCountWithPredicate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@test.com")
	createUser(t, client, "Amy", "amy@test.com")
	createUser(t, client, "Bob", "bob@test.com")

	n, err := client.User.Query().
		Where(user.NameField.HasPrefix("A")).
		Aggregate(integration.Count()).
		Int(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

// TestAggregate_GroupByRoleCount verifies GroupBy with Count aggregation.
// This exercises the GroupBy + Aggregate + Count path end-to-end.
func TestAggregate_GroupByRoleCount(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Create 2 users and 1 admin.
	createUser(t, client, "Alice", "a@test.com")
	createUser(t, client, "Bob", "b@test.com")
	_, err := client.User.Create().
		SetName("Admin").
		SetEmail("admin@test.com").
		SetAge(40).
		SetRole(user.RoleAdmin).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	var out []struct {
		Role  string `json:"role"`
		Count int    `json:"count"`
	}
	err = client.User.Query().
		GroupBy(user.FieldRole).
		Aggregate(integration.As(integration.Count(), "count")).
		Scan(ctx, &out)
	require.NoError(t, err)
	require.Len(t, out, 2)

	byRole := map[string]int{}
	for _, row := range out {
		byRole[row.Role] = row.Count
	}
	assert.Equal(t, 2, byRole["user"])
	assert.Equal(t, 1, byRole["admin"])
}

// TestAggregate_GroupBySumMeanMinMax verifies multiple numeric aggregations
// over a grouped field.
func TestAggregate_GroupBySumMeanMinMax(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Two users with role=user, one with role=admin, all different ages.
	for _, spec := range []struct {
		name, email string
		role        user.Role
		age         int
	}{
		{"Alice", "a@test.com", user.RoleUser, 10},
		{"Bob", "b@test.com", user.RoleUser, 20},
		{"Admin", "admin@test.com", user.RoleAdmin, 40},
	} {
		_, err := client.User.Create().
			SetName(spec.name).
			SetEmail(spec.email).
			SetAge(spec.age).
			SetRole(spec.role).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	var out []struct {
		Role string  `json:"role"`
		Min  int     `json:"min"`
		Max  int     `json:"max"`
		Sum  int     `json:"sum"`
		Mean float64 `json:"mean"`
	}
	err := client.User.Query().
		GroupBy(user.FieldRole).
		Aggregate(
			integration.As(integration.Min(user.FieldAge), "min"),
			integration.As(integration.Max(user.FieldAge), "max"),
			integration.As(integration.Sum(user.FieldAge), "sum"),
			integration.As(integration.Mean(user.FieldAge), "mean"),
		).
		Scan(ctx, &out)
	require.NoError(t, err)
	require.Len(t, out, 2)

	byRole := map[string]struct {
		min, max, sum int
		mean          float64
	}{}
	for _, row := range out {
		byRole[row.Role] = struct {
			min, max, sum int
			mean          float64
		}{row.Min, row.Max, row.Sum, row.Mean}
	}

	assert.Equal(t, 10, byRole["user"].min)
	assert.Equal(t, 20, byRole["user"].max)
	assert.Equal(t, 30, byRole["user"].sum)
	assert.InDelta(t, 15.0, byRole["user"].mean, 0.01)

	assert.Equal(t, 40, byRole["admin"].min)
	assert.Equal(t, 40, byRole["admin"].max)
	assert.Equal(t, 40, byRole["admin"].sum)
	assert.InDelta(t, 40.0, byRole["admin"].mean, 0.01)
}

// TestOrder_AscDesc verifies integration.Asc / integration.Desc order helpers
// against the query pipeline.
func TestOrder_AscDesc(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Charlie", "c@test.com")
	createUser(t, client, "Alice", "a@test.com")
	createUser(t, client, "Bob", "b@test.com")

	ascUsers, err := client.User.Query().Order(integration.Asc(user.FieldName)).All(ctx)
	require.NoError(t, err)
	require.Len(t, ascUsers, 3)
	assert.Equal(t, "Alice", ascUsers[0].Name)
	assert.Equal(t, "Bob", ascUsers[1].Name)
	assert.Equal(t, "Charlie", ascUsers[2].Name)

	descUsers, err := client.User.Query().Order(integration.Desc(user.FieldName)).All(ctx)
	require.NoError(t, err)
	require.Len(t, descUsers, 3)
	assert.Equal(t, "Charlie", descUsers[0].Name)
	assert.Equal(t, "Bob", descUsers[1].Name)
	assert.Equal(t, "Alice", descUsers[2].Name)
}
