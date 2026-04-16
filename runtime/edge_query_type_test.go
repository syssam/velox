package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema/field"
)

func newTestEdgeQuery(t *testing.T) (*EdgeQuery, *testMeta) {
	t.Helper()
	drv := newTestDB(t)
	meta := testTypeInfo()
	seedUsers(context.Background(), t, drv, meta, []struct {
		Name string
		Age  int
	}{
		{"Alice", 30}, {"Bob", 25}, {"Carol", 35},
	})

	qb := NewQueryBase(drv, "users", meta.Columns, meta.IDColumn, nil, "User")
	sc := meta.ScanConfig()
	sc.IDFieldType = field.TypeInt
	eq := NewEdgeQuery(Config{Driver: drv}, qb, sc)
	return eq, meta
}

// =============================================================================
// NewEdgeQuery
// =============================================================================

func TestNewEdgeQuery(t *testing.T) {
	drv := &mockDriver{dialectName: "sqlite"}
	qb := NewQueryBase(drv, "users", []string{"id", "name"}, "id", []string{"team_id"}, "User")
	qb.Predicates = append(qb.Predicates, func(s *sql.Selector) {})
	qb.Order = append(qb.Order, func(s *sql.Selector) {})
	qb.Modifiers = append(qb.Modifiers, func(s *sql.Selector) {})
	qb.Edges = append(qb.Edges, EdgeLoad{Name: "posts"})
	qb.WithFKs = true

	sc := &ScanConfig{Table: "users", IDFieldType: field.TypeInt}
	eq := NewEdgeQuery(Config{Driver: drv}, qb, sc)

	assert.Equal(t, drv, eq.driver)
	assert.Equal(t, "users", eq.table)
	assert.Equal(t, "id", eq.idColumn)
	assert.Len(t, eq.predicates, 1)
	assert.Len(t, eq.order, 1)
	assert.Len(t, eq.modifiers, 1)
	assert.Len(t, eq.edges, 1)
	assert.True(t, eq.withFKs)
	assert.NotNil(t, eq.scan)
}

// =============================================================================
// Fluent methods
// =============================================================================

func TestEdgeQuery_FluentMethods(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)

	// Where
	eq.Where(func(s *sql.Selector) { s.Where(sql.EQ("name", "Alice")) })
	assert.Len(t, eq.predicates, 1)

	// Limit
	eq.Limit(10)
	assert.Equal(t, 10, *eq.ctx.Limit)

	// Offset
	eq.Offset(5)
	assert.Equal(t, 5, *eq.ctx.Offset)

	// Order
	eq.Order(func(s *sql.Selector) { s.OrderBy("name") })
	assert.Len(t, eq.order, 1)

	// Select
	eq.Select("name", "age")
	assert.Equal(t, []string{"name", "age"}, eq.ctx.Fields)

	// Unique
	eq.Unique(true)
	assert.True(t, *eq.ctx.Unique)

	// Modify
	eq.Modify(func(s *sql.Selector) {})
	assert.Len(t, eq.modifiers, 1)

	// WithEdge
	eq.WithEdge("posts")
	assert.Len(t, eq.edges, 1)
	assert.True(t, eq.withFKs)
}

// =============================================================================
// Clone
// =============================================================================

func TestEdgeQuery_Clone(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	eq.Where(func(s *sql.Selector) {})
	eq.Limit(10)

	clone := eq.Clone()
	assert.NotNil(t, clone)
	assert.Equal(t, 10, *clone.ctx.Limit)

	// Mutating clone should not affect original.
	clone.Limit(20)
	assert.Equal(t, 10, *eq.ctx.Limit)
	assert.Equal(t, 20, *clone.ctx.Limit)

	// Nil clone
	var nilEQ *EdgeQuery
	assert.Nil(t, nilEQ.Clone())
}

// =============================================================================
// Count / Exist
// =============================================================================

func TestEdgeQuery_Count(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)

	n, err := eq.Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}

func TestEdgeQuery_CountX(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	assert.Equal(t, 3, eq.CountX(context.Background()))
}

func TestEdgeQuery_CountX_Panics(t *testing.T) {
	drv := &failDriver{Driver: newTestDB(t), failAfter: 0, err: errors.New("fail")}
	qb := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
	eq := NewEdgeQuery(Config{Driver: drv}, qb, &ScanConfig{IDFieldType: field.TypeInt})

	assert.Panics(t, func() { eq.CountX(context.Background()) })
}

func TestEdgeQuery_Exist(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)

	ok, err := eq.Exist(context.Background())
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestEdgeQuery_ExistX(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	assert.True(t, eq.ExistX(context.Background()))
}

func TestEdgeQuery_ExistX_Panics(t *testing.T) {
	drv := &failDriver{Driver: newTestDB(t), failAfter: 0, err: errors.New("fail")}
	qb := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
	eq := NewEdgeQuery(Config{Driver: drv}, qb, &ScanConfig{IDFieldType: field.TypeInt})

	assert.Panics(t, func() { eq.ExistX(context.Background()) })
}

// =============================================================================
// Scan / ScanX
// =============================================================================

func TestEdgeQuery_Scan(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	eq.Select("name")

	var names []string
	err := eq.Scan(context.Background(), &names)
	require.NoError(t, err)
	assert.Len(t, names, 3)
}

func TestEdgeQuery_ScanX(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	eq.Select("name")

	var names []string
	eq.ScanX(context.Background(), &names)
	assert.Len(t, names, 3)
}

func TestEdgeQuery_ScanX_Panics(t *testing.T) {
	drv := &failDriver{Driver: newTestDB(t), failAfter: 0, err: errors.New("fail")}
	qb := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
	eq := NewEdgeQuery(Config{Driver: drv}, qb, &ScanConfig{IDFieldType: field.TypeInt})

	assert.Panics(t, func() {
		var v []string
		eq.ScanX(context.Background(), &v)
	})
}

// =============================================================================
// IDs / IDsX / FirstID / FirstIDX / OnlyID / OnlyIDX
// =============================================================================

func TestEdgeQuery_IDs(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)

	ids, err := eq.IDs(context.Background())
	require.NoError(t, err)
	assert.Len(t, ids, 3)
}

func TestEdgeQuery_IDsX(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	ids := eq.IDsX(context.Background())
	assert.Len(t, ids, 3)
}

func TestEdgeQuery_IDsX_Panics(t *testing.T) {
	drv := &failDriver{Driver: newTestDB(t), failAfter: 0, err: errors.New("fail")}
	qb := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
	eq := NewEdgeQuery(Config{Driver: drv}, qb, &ScanConfig{IDFieldType: field.TypeInt})

	assert.Panics(t, func() { eq.IDsX(context.Background()) })
}

func TestEdgeQuery_FirstID(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)

	id, err := eq.FirstID(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, id)
}

func TestEdgeQuery_FirstIDX(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	id := eq.FirstIDX(context.Background())
	assert.NotNil(t, id)
}

func TestEdgeQuery_FirstIDX_Panics(t *testing.T) {
	drv := &failDriver{Driver: newTestDB(t), failAfter: 0, err: errors.New("fail")}
	qb := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
	eq := NewEdgeQuery(Config{Driver: drv}, qb, &ScanConfig{IDFieldType: field.TypeInt})

	assert.Panics(t, func() { eq.FirstIDX(context.Background()) })
}

func TestEdgeQuery_OnlyID(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	eq.Where(func(s *sql.Selector) { s.Where(sql.EQ(s.C("name"), "Alice")) })

	id, err := eq.OnlyID(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, id)
}

func TestEdgeQuery_OnlyIDX(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	eq.Where(func(s *sql.Selector) { s.Where(sql.EQ(s.C("name"), "Alice")) })
	id := eq.OnlyIDX(context.Background())
	assert.NotNil(t, id)
}

func TestEdgeQuery_OnlyIDX_Panics(t *testing.T) {
	drv := &failDriver{Driver: newTestDB(t), failAfter: 0, err: errors.New("fail")}
	qb := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
	eq := NewEdgeQuery(Config{Driver: drv}, qb, &ScanConfig{IDFieldType: field.TypeInt})

	assert.Panics(t, func() { eq.OnlyIDX(context.Background()) })
}

// =============================================================================
// AllAny / FirstAny / OnlyAny
// =============================================================================

func TestEdgeQuery_AllAny(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)

	nodes, err := eq.AllAny(context.Background())
	require.NoError(t, err)
	assert.Len(t, nodes, 3)
}

func TestEdgeQuery_AllAny_NilScan(t *testing.T) {
	qb := NewQueryBase(nil, "users", []string{"id"}, "id", nil, "User")
	eq := NewEdgeQuery(Config{}, qb, nil)

	nodes, err := eq.AllAny(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, nodes)
}

func TestEdgeQuery_FirstAny(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)

	node, err := eq.FirstAny(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, node)
	u := node.(*testEntity)
	assert.NotEmpty(t, u.Name)
}

func TestEdgeQuery_FirstAny_NotFound(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	eq.Where(func(s *sql.Selector) { s.Where(sql.EQ(s.C("name"), "NoSuchUser")) })

	_, err := eq.FirstAny(context.Background())
	assert.True(t, IsNotFound(err))
}

func TestEdgeQuery_OnlyAny(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	eq.Where(func(s *sql.Selector) { s.Where(sql.EQ(s.C("name"), "Alice")) })

	node, err := eq.OnlyAny(context.Background())
	require.NoError(t, err)
	u := node.(*testEntity)
	assert.Equal(t, "Alice", u.Name)
}

func TestEdgeQuery_OnlyAny_NotFound(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)
	eq.Where(func(s *sql.Selector) { s.Where(sql.EQ(s.C("name"), "NoSuchUser")) })

	_, err := eq.OnlyAny(context.Background())
	assert.True(t, IsNotFound(err))
}

func TestEdgeQuery_OnlyAny_NotSingular(t *testing.T) {
	eq, _ := newTestEdgeQuery(t)

	_, err := eq.OnlyAny(context.Background())
	assert.True(t, IsNotSingular(err))
}

// =============================================================================
// Getter methods
// =============================================================================

func TestEdgeQuery_GetterMethods(t *testing.T) {
	drv := &mockDriver{dialectName: "sqlite"}
	qb := NewQueryBase(drv, "users", []string{"id", "name"}, "id", []string{"team_id"}, "User")
	qb.Predicates = append(qb.Predicates, func(s *sql.Selector) {})
	qb.Order = append(qb.Order, func(s *sql.Selector) {})
	qb.Modifiers = append(qb.Modifiers, func(s *sql.Selector) {})
	qb.WithFKs = true
	pathFn := func(_ context.Context) (*sql.Selector, error) { return nil, nil }
	qb.Path = pathFn
	inters := []Interceptor{InterceptFunc(func(next Querier) Querier { return next })}
	qb.Inters = inters

	eq := NewEdgeQuery(Config{Driver: drv}, qb, &ScanConfig{IDFieldType: field.TypeInt})

	assert.Equal(t, drv, eq.GetDriver())
	assert.NotNil(t, eq.GetCtx())
	assert.Len(t, eq.GetPredicates(), 1)
	assert.Len(t, eq.GetOrder(), 1)
	assert.Len(t, eq.GetModifiers(), 1)
	assert.NotNil(t, eq.GetPath())
	assert.Len(t, eq.GetInters(), 1)
	assert.True(t, eq.GetWithFKs())
}

// =============================================================================
// idFieldType
// =============================================================================

func TestEdgeQuery_idFieldType(t *testing.T) {
	t.Run("from_scan_config", func(t *testing.T) {
		qb := NewQueryBase(nil, "users", []string{"id"}, "id", nil, "User")
		eq := NewEdgeQuery(Config{}, qb, &ScanConfig{IDFieldType: field.TypeUUID})
		assert.Equal(t, field.TypeUUID, eq.idFieldType())
	})

	t.Run("fallback_to_int", func(t *testing.T) {
		qb := NewQueryBase(nil, "users", []string{"id"}, "id", nil, "User")
		eq := NewEdgeQuery(Config{}, qb, nil)
		assert.Equal(t, field.TypeInt, eq.idFieldType())
	})
}

// =============================================================================
// Selector BoolsX / BoolX coverage
// =============================================================================

func TestSelector_BoolsX(t *testing.T) {
	scanFn := func(_ context.Context, v any) error {
		ptr := v.(*[]bool)
		*ptr = []bool{true, false}
		return nil
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	result := s.BoolsX(context.Background())
	assert.Equal(t, []bool{true, false}, result)
}

func TestSelector_BoolsX_Panics(t *testing.T) {
	scanFn := func(_ context.Context, _ any) error {
		return errors.New("scan error")
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	assert.Panics(t, func() { s.BoolsX(context.Background()) })
}

func TestSelector_BoolX(t *testing.T) {
	scanFn := func(_ context.Context, v any) error {
		ptr := v.(*[]bool)
		*ptr = []bool{true}
		return nil
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	result := s.BoolX(context.Background())
	assert.True(t, result)
}

func TestSelector_BoolX_Panics(t *testing.T) {
	scanFn := func(_ context.Context, _ any) error {
		return errors.New("scan error")
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	assert.Panics(t, func() { s.BoolX(context.Background()) })
}

func TestSelector_Bool_NotFound(t *testing.T) {
	scanFn := func(_ context.Context, v any) error {
		ptr := v.(*[]bool)
		*ptr = []bool{}
		return nil
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	_, err := s.Bool(context.Background())
	assert.True(t, IsNotFound(err))
}

func TestSelector_Bool_NotSingular(t *testing.T) {
	scanFn := func(_ context.Context, v any) error {
		ptr := v.(*[]bool)
		*ptr = []bool{true, false}
		return nil
	}
	flds := []string{"active"}
	s := NewSelector("test", &flds, scanFn)

	_, err := s.Bool(context.Background())
	assert.True(t, IsNotSingular(err))
}
