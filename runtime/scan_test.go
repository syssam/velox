package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	velsql "github.com/syssam/velox/dialect/sql"
)

func TestScanAll(t *testing.T) {
	drv := newTestDB(t)
	meta := testTypeInfo()
	seedUsers(context.Background(), t, drv, meta, []struct {
		Name string
		Age  int
	}{
		{"Alice", 30}, {"Bob", 25},
	})

	nodes, err := ScanAll[testEntity, *testEntity](context.Background(), drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := NewQueryBase(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		return qb.BuildSelector(ctx)
	})
	require.NoError(t, err)
	assert.Len(t, nodes, 2)
	assert.Equal(t, "Alice", nodes[0].Name)
	assert.Equal(t, "Bob", nodes[1].Name)
}

func TestScanFirst(t *testing.T) {
	drv := newTestDB(t)
	meta := testTypeInfo()
	seedUsers(context.Background(), t, drv, meta, []struct {
		Name string
		Age  int
	}{
		{"Alice", 30}, {"Bob", 25},
	})

	node, err := ScanFirst[testEntity, *testEntity](context.Background(), drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := NewQueryBase(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		qb.SetLimit(1)
		return qb.BuildSelector(ctx)
	}, "User")
	require.NoError(t, err)
	assert.Equal(t, "Alice", node.Name)
}

func TestScanFirst_NotFound(t *testing.T) {
	drv := newTestDB(t)

	_, err := ScanFirst[testEntity, *testEntity](context.Background(), drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := NewQueryBase(drv, "users", []string{"id", "name", "age"}, "id", nil, "User")
		qb.SetLimit(1)
		return qb.BuildSelector(ctx)
	}, "User")
	assert.True(t, IsNotFound(err))
}

func TestScanOnly(t *testing.T) {
	drv := newTestDB(t)
	meta := testTypeInfo()
	seedUsers(context.Background(), t, drv, meta, []struct {
		Name string
		Age  int
	}{
		{"Alice", 30},
	})

	node, err := ScanOnly[testEntity, *testEntity](context.Background(), drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := NewQueryBase(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		qb.SetLimit(2)
		return qb.BuildSelector(ctx)
	}, "User")
	require.NoError(t, err)
	assert.Equal(t, "Alice", node.Name)
}

func TestScanOnly_NotSingular(t *testing.T) {
	drv := newTestDB(t)
	meta := testTypeInfo()
	seedUsers(context.Background(), t, drv, meta, []struct {
		Name string
		Age  int
	}{
		{"Alice", 30}, {"Bob", 25},
	})

	_, err := ScanOnly[testEntity, *testEntity](context.Background(), drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := NewQueryBase(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		qb.SetLimit(2)
		return qb.BuildSelector(ctx)
	}, "User")
	assert.True(t, IsNotSingular(err))
}

func TestScanOnly_NotFound(t *testing.T) {
	drv := newTestDB(t)

	_, err := ScanOnly[testEntity, *testEntity](context.Background(), drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := NewQueryBase(drv, "users", []string{"id", "name", "age"}, "id", nil, "User")
		qb.SetLimit(2)
		return qb.BuildSelector(ctx)
	}, "User")
	assert.True(t, IsNotFound(err))
}

// TestScanFirst_InjectsLimit verifies that ScanFirst adds LIMIT 1
// even when the caller's build function does not set a limit.
func TestScanFirst_InjectsLimit(t *testing.T) {
	drv := newTestDB(t)
	meta := testTypeInfo()
	seedUsers(context.Background(), t, drv, meta, []struct {
		Name string
		Age  int
	}{
		{"Alice", 30}, {"Bob", 25}, {"Charlie", 35},
	})

	// Build function does NOT set any limit.
	node, err := ScanFirst[testEntity, *testEntity](context.Background(), drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := NewQueryBase(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		return qb.BuildSelector(ctx)
	}, "User")
	require.NoError(t, err)
	assert.Equal(t, "Alice", node.Name)

	// Verify via a capturing driver that the SQL includes LIMIT 1.
	cdrv := &captureDriver{Driver: drv}
	_, _ = ScanFirst[testEntity, *testEntity](context.Background(), cdrv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := NewQueryBase(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		return qb.BuildSelector(ctx)
	}, "User")
	assert.Contains(t, cdrv.lastQuery, "LIMIT 1", "ScanFirst must inject LIMIT 1")
}

// TestScanOnly_InjectsLimit verifies that ScanOnly adds LIMIT 2
// even when the caller's build function does not set a limit.
func TestScanOnly_InjectsLimit(t *testing.T) {
	drv := newTestDB(t)
	meta := testTypeInfo()
	seedUsers(context.Background(), t, drv, meta, []struct {
		Name string
		Age  int
	}{
		{"Alice", 30}, {"Bob", 25}, {"Charlie", 35},
	})

	// Build function does NOT set any limit — ScanOnly should inject LIMIT 2.
	_, err := ScanOnly[testEntity, *testEntity](context.Background(), drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := NewQueryBase(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		return qb.BuildSelector(ctx)
	}, "User")
	// 3 rows, LIMIT 2 means we get 2 → NotSingular
	assert.True(t, IsNotSingular(err), "ScanOnly with 3 rows and injected LIMIT 2 should return NotSingular")

	// Verify via a capturing driver that the SQL includes LIMIT 2.
	cdrv := &captureDriver{Driver: drv}
	_, _ = ScanOnly[testEntity, *testEntity](context.Background(), cdrv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := NewQueryBase(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		return qb.BuildSelector(ctx)
	}, "User")
	assert.Contains(t, cdrv.lastQuery, "LIMIT 2", "ScanOnly must inject LIMIT 2")
}
