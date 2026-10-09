package runtime

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	velsql "github.com/syssam/velox/dialect/sql"
)

// countingEntity counts ScanValues calls, so a test can tell whether ScanAll
// built one set of scan holders for the result set or one per row.
type countingEntity struct {
	testEntity
}

var countingScanValuesCalls int

func (e *countingEntity) ScanValues(columns []string) ([]any, error) {
	countingScanValuesCalls++
	return e.testEntity.ScanValues(columns)
}

// rawAge is a scan holder ScanAll does not know, standing in for a custom
// ValueScanner whose pointer an entity may keep.
type rawAge struct{ v int64 }

func (r *rawAge) Scan(src any) error {
	r.v, _ = src.(int64)
	return nil
}

// retainingEntity keeps the POINTER to its age holder, which is only correct
// when every row gets its own holders.
type retainingEntity struct {
	Name string
	Age  *rawAge
}

var retainingScanValuesCalls int

func (*retainingEntity) ScanValues(columns []string) ([]any, error) {
	retainingScanValuesCalls++
	values := make([]any, len(columns))
	for i, col := range columns {
		switch col {
		case "age":
			values[i] = new(rawAge)
		case "name":
			values[i] = new(sql.NullString)
		default:
			values[i] = new(sql.NullInt64)
		}
	}
	return values, nil
}

func (e *retainingEntity) AssignValues(columns []string, values []any) error {
	for i, col := range columns {
		switch col {
		case "age":
			e.Age = values[i].(*rawAge)
		case "name":
			e.Name = values[i].(*sql.NullString).String
		}
	}
	return nil
}

// seedThree returns a driver holding three users and the query meta for them.
func seedThree(ctx context.Context, t *testing.T) (dialect.Driver, *testMeta) {
	t.Helper()
	drv := newTestDB(t)
	meta := testTypeInfo()
	seedUsers(ctx, t, drv, meta, []struct {
		Name string
		Age  int
	}{
		{"Alice", 30}, {"Bob", 25}, {"Charlie", 35},
	})
	return drv, meta
}

// TestScanAll_ReusesNullHolders pins that the database/sql null holders are
// built once per result set, and that reusing them does not carry a value
// from one row into the next.
func TestScanAll_ReusesNullHolders(t *testing.T) {
	ctx := context.Background()
	drv, meta := seedThree(ctx, t)
	countingScanValuesCalls = 0
	nodes, err := ScanAll[countingEntity, *countingEntity](ctx, drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := newTestQuery(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		s, err := qb.BuildSelector(ctx)
		if err != nil {
			return nil, err
		}
		// A NULL between two non-NULL rows: a reused holder must be reset by
		// the scan, not keep Alice's 30.
		s.Select("id", "name").AppendSelectExpr(velsql.Expr("CASE WHEN name = 'Bob' THEN NULL ELSE age END AS age"))
		return s, nil
	})
	require.NoError(t, err)
	require.Len(t, nodes, 3)
	assert.Equal(t, 1, countingScanValuesCalls, "holders must be built once for the result set")
	assert.Equal(t, []string{"Alice", "Bob", "Charlie"}, []string{nodes[0].Name, nodes[1].Name, nodes[2].Name})
	assert.Equal(t, []int{30, 0, 35}, []int{nodes[0].Age, nodes[1].Age, nodes[2].Age})
	assert.NotEqual(t, nodes[0].ID, nodes[1].ID)
}

// TestScanAll_UnknownHolderIsNotReused pins the fallback: a holder type the
// entity may retain gets a fresh instance per row.
func TestScanAll_UnknownHolderIsNotReused(t *testing.T) {
	ctx := context.Background()
	drv, meta := seedThree(ctx, t)
	retainingScanValuesCalls = 0
	nodes, err := ScanAll[retainingEntity, *retainingEntity](ctx, drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := newTestQuery(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		return qb.BuildSelector(ctx)
	})
	require.NoError(t, err)
	require.Len(t, nodes, 3)
	assert.Equal(t, 3, retainingScanValuesCalls, "an unknown holder type must not be shared between rows")
	assert.Equal(t, []int64{30, 25, 35}, []int64{nodes[0].Age.v, nodes[1].Age.v, nodes[2].Age.v})
}

func TestScanHolderPlan(t *testing.T) {
	fresh, ok := scanHolderPlan([]any{new(sql.NullInt64), new(sql.NullString), new(sql.NullTime), new(sql.NullBool), new(sql.NullFloat64)})
	assert.True(t, ok)
	assert.Empty(t, fresh)

	fresh, ok = scanHolderPlan([]any{new([]byte), new(sql.NullInt64), new([]byte)})
	assert.True(t, ok)
	assert.Equal(t, []int{0, 2}, fresh, "bytes holders are replaced per row")

	_, ok = scanHolderPlan(nil)
	assert.True(t, ok)
	_, ok = scanHolderPlan([]any{new(sql.NullInt64), new(velsql.UnknownType)})
	assert.False(t, ok)
	_, ok = scanHolderPlan([]any{new(rawAge), new(sql.NullString)})
	assert.False(t, ok)
}

// bytesPtrEntity is a generated entity with a nillable bytes field: its
// AssignValues keeps the *[]byte holder itself (e.Data = value), exactly as
// compiler/gen/sql/entity_scan.go emits it.
type bytesPtrEntity struct {
	ID   int
	Data *[]byte
}

func (*bytesPtrEntity) ScanValues(columns []string) ([]any, error) {
	values := make([]any, len(columns))
	for i, col := range columns {
		switch col {
		case "name":
			values[i] = new([]byte)
		default:
			values[i] = new(sql.NullInt64)
		}
	}
	return values, nil
}

func (e *bytesPtrEntity) AssignValues(columns []string, values []any) error {
	for i, col := range columns {
		switch col {
		case "id":
			e.ID = int(values[i].(*sql.NullInt64).Int64)
		case "name":
			if value, ok := values[i].(*[]byte); ok && value != nil {
				e.Data = value
			}
		}
	}
	return nil
}

// TestScanAll_NillableBytesKeepTheirOwnHolder pins that a *[]byte holder is
// never shared between rows. Sharing it made every row of a
// field.Bytes().Nillable() column point at the last row's value (found in
// review, before release).
func TestScanAll_NillableBytesKeepTheirOwnHolder(t *testing.T) {
	ctx := context.Background()
	drv, meta := seedThree(ctx, t)
	nodes, err := ScanAll[bytesPtrEntity, *bytesPtrEntity](ctx, drv, func(ctx context.Context) (*velsql.Selector, error) {
		qb := newTestQuery(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
		return qb.BuildSelector(ctx)
	})
	require.NoError(t, err)
	require.Len(t, nodes, 3)
	got := make([]string, len(nodes))
	for i, n := range nodes {
		require.NotNil(t, n.Data)
		got[i] = string(*n.Data)
	}
	assert.Equal(t, []string{"Alice", "Bob", "Charlie"}, got)
	assert.NotSame(t, nodes[0].Data, nodes[1].Data)
	assert.NotSame(t, nodes[1].Data, nodes[2].Data)
}
