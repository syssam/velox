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
		qb := newTestQuery(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
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
		qb := newTestQuery(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		qb.SetLimit(1)
		return qb.BuildSelector(ctx)
	}, "User")
	require.NoError(t, err)
	assert.Equal(t, "Alice", node.Name)
}

func TestScanFirst_NotFound(t *testing.T) {
	drv := newTestDB(t)

	_, err := ScanFirst[testEntity, *testEntity](context.Background(), drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := newTestQuery(drv, "users", []string{"id", "name", "age"}, "id", nil, "User")
		qb.SetLimit(1)
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
		qb := newTestQuery(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		return qb.BuildSelector(ctx)
	}, "User")
	require.NoError(t, err)
	assert.Equal(t, "Alice", node.Name)

	// Verify via a capturing driver that the SQL includes LIMIT 1.
	cdrv := &captureDriver{Driver: drv}
	_, _ = ScanFirst[testEntity, *testEntity](context.Background(), cdrv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := newTestQuery(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		return qb.BuildSelector(ctx)
	}, "User")
	assert.Contains(t, cdrv.lastQuery, "LIMIT 1", "ScanFirst must inject LIMIT 1")
}
